package interactions

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

var discordSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const (
	defaultMaxRequestBytes = int64(64 * 1024)
	defaultSignatureMaxAge = 5 * time.Minute
	maximumResponseLength  = 1900
)

// SessionService is the application boundary used by Discord commands.
type SessionService interface {
	Create(
		ctx context.Context,
		command appsession.CreateCommand,
	) (domain.Session, error)

	Get(
		ctx context.Context,
		query appsession.GetQuery,
	) (domain.Session, error)

	List(
		ctx context.Context,
		query appsession.ListQuery,
	) ([]domain.Session, error)
}

// Clock supplies current UTC time for signature validation and tests.
type Clock interface {
	Now() time.Time
}

// IDGenerator creates correlation identifiers.
type IDGenerator interface {
	New(now time.Time) (string, error)
}

// Config contains the security and routing configuration for interactions.
type Config struct {
	PublicKey       ed25519.PublicKey
	ApplicationID   string
	AllowedGuildIDs []string
	MaxRequestBytes int64
	SignatureMaxAge time.Duration
}

// Handler verifies and routes Discord HTTP interactions.
type Handler struct {
	service         SessionService
	ids             IDGenerator
	clock           Clock
	logger          *slog.Logger
	publicKey       ed25519.PublicKey
	applicationID   string
	allowedGuildIDs map[string]struct{}
	maxRequestBytes int64
	signatureMaxAge time.Duration
}

// NewHandler creates a Discord interaction handler.
func NewHandler(
	service SessionService,
	ids IDGenerator,
	clock Clock,
	logger *slog.Logger,
	config Config,
) (*Handler, error) {
	switch {
	case service == nil:
		return nil, fmt.Errorf("session service is required")
	case ids == nil:
		return nil, fmt.Errorf("correlation ID generator is required")
	case clock == nil:
		return nil, fmt.Errorf("clock is required")
	case logger == nil:
		return nil, fmt.Errorf("logger is required")
	case len(config.PublicKey) != ed25519.PublicKeySize:
		return nil, fmt.Errorf("Discord public key is invalid")
	case strings.TrimSpace(config.ApplicationID) == "":
		return nil, fmt.Errorf("Discord application ID is required")
	case len(config.AllowedGuildIDs) == 0:
		return nil, fmt.Errorf("at least one allowed Discord guild ID is required")
	}

	allowedGuildIDs := make(map[string]struct{}, len(config.AllowedGuildIDs))
	for _, guildID := range config.AllowedGuildIDs {
		guildID = strings.TrimSpace(guildID)
		if guildID == "" {
			return nil, fmt.Errorf("allowed Discord guild IDs cannot contain empty values")
		}

		allowedGuildIDs[guildID] = struct{}{}
	}

	maxRequestBytes := config.MaxRequestBytes
	if maxRequestBytes <= 0 {
		maxRequestBytes = defaultMaxRequestBytes
	}

	signatureMaxAge := config.SignatureMaxAge
	if signatureMaxAge <= 0 {
		signatureMaxAge = defaultSignatureMaxAge
	}

	return &Handler{
		service:         service,
		ids:             ids,
		clock:           clock,
		logger:          logger,
		publicKey:       append(ed25519.PublicKey(nil), config.PublicKey...),
		applicationID:   strings.TrimSpace(config.ApplicationID),
		allowedGuildIDs: allowedGuildIDs,
		maxRequestBytes: maxRequestBytes,
		signatureMaxAge: signatureMaxAge,
	}, nil
}

// ServeHTTP verifies the exact raw body before parsing or routing it.
func (handler *Handler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := validateContentType(request.Header.Get("Content-Type")); err != nil {
		http.Error(writer, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	body, err := readBody(writer, request, handler.maxRequestBytes)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			http.Error(writer, "request body is too large", http.StatusRequestEntityTooLarge)
			return
		}

		handler.logger.Warn("failed to read Discord interaction body", slog.Any("error", err))
		http.Error(writer, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := verifySignature(
		handler.publicKey,
		request.Header.Get("X-Signature-Ed25519"),
		request.Header.Get("X-Signature-Timestamp"),
		body,
		handler.clock.Now(),
		handler.signatureMaxAge,
	); err != nil {
		handler.logger.Warn(
			"rejected Discord interaction",
			slog.String("reason", signatureFailureReason(err)),
		)
		http.Error(writer, "invalid request signature", http.StatusUnauthorized)
		return
	}

	var payload interactionPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		handler.logger.Warn("failed to decode Discord interaction", slog.Any("error", err))
		http.Error(writer, "invalid interaction payload", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(payload.ApplicationID) != handler.applicationID {
		handler.logger.Warn(
			"rejected Discord interaction for unexpected application",
			slog.String("application_id", payload.ApplicationID),
		)
		http.Error(writer, "invalid interaction application", http.StatusUnauthorized)
		return
	}

	if payload.Type == interactionTypePing {
		writeJSON(writer, http.StatusOK, interactionResponse{Type: interactionResponsePong})
		return
	}

	if payload.Type != interactionTypeApplicationCommand {
		writeInteractionMessage(writer, "This interaction type is not supported yet.")
		return
	}

	if strings.TrimSpace(payload.ID) == "" {
		handler.logger.Warn("rejected Discord command without interaction ID")
		http.Error(writer, "invalid interaction payload", http.StatusBadRequest)
		return
	}

	if _, allowed := handler.allowedGuildIDs[strings.TrimSpace(payload.GuildID)]; !allowed {
		handler.logger.Warn(
			"rejected Discord interaction from unapproved guild",
			slog.String("guild_id", payload.GuildID),
		)
		writeInteractionMessage(writer, "This app is not enabled in this Discord server.")
		return
	}

	correlationID, err := handler.ids.New(handler.clock.Now().UTC())
	if err != nil {
		handler.logger.Error("failed to generate Discord correlation ID", slog.Any("error", err))
		writeInteractionMessage(writer, "The command could not be processed. Please try again.")
		return
	}

	content, commandName, err := handler.routeCommand(
		request.Context(),
		payload,
		correlationID,
	)
	if err != nil {
		content = handler.commandErrorMessage(err, correlationID)
		handler.logger.Error(
			"Discord command failed",
			slog.String("correlation_id", correlationID),
			slog.String("command", commandName),
			slog.Any("error", err),
		)
	} else {
		handler.logger.Info(
			"Discord command completed",
			slog.String("correlation_id", correlationID),
			slog.String("command", commandName),
		)
	}

	writeInteractionMessage(writer, content)
}

func (handler *Handler) routeCommand(
	ctx context.Context,
	payload interactionPayload,
	correlationID string,
) (string, string, error) {
	actorID := payload.actorID()
	if actorID == "" {
		return "", "session", newUserError("Discord user information is missing from the command.")
	}

	if strings.TrimSpace(payload.ChannelID) == "" {
		return "", "session", newUserError("Discord channel information is missing from the command.")
	}

	subcommand, err := payload.subcommand()
	if err != nil {
		return "", "session", newUserError("Use one of the supported `/session` subcommands.")
	}

	actor := domain.Actor{
		Type: domain.ActorTypeDiscordUser,
		ID:   actorID,
	}

	commandName := "session " + subcommand.Name
	switch subcommand.Name {
	case "create":
		content, err := handler.createSession(
			ctx,
			payload,
			subcommand.Options,
			actor,
			correlationID,
		)
		return content, commandName, err
	case "list":
		content, err := handler.listSessions(ctx, actor, payload.GuildID)
		return content, commandName, err
	case "status":
		content, err := handler.sessionStatus(
			ctx,
			subcommand.Options,
			actor,
			payload.GuildID,
		)
		return content, commandName, err
	default:
		return "", commandName, newUserError(
			"That `/session` subcommand is not supported yet.",
		)
	}
}

func (handler *Handler) createSession(
	ctx context.Context,
	payload interactionPayload,
	options []applicationCommandOption,
	actor domain.Actor,
	correlationID string,
) (string, error) {
	slug, err := stringOption(options, "slug", true)
	if err != nil || len(slug) > 64 || !discordSlugPattern.MatchString(slug) {
		return "", newUserError(
			"The slug must use lowercase letters, numbers, and single hyphens.",
		)
	}

	displayName, err := stringOption(options, "name", true)
	if err != nil || len(displayName) > 100 {
		return "", newUserError("The session name must contain 1 to 100 characters.")
	}

	gameType, err := stringOption(options, "game", false)
	if err != nil {
		return "", newUserError("The game option must be text.")
	}
	if gameType == "" {
		gameType = "arma3"
	}
	if strings.ToLower(gameType) != "arma3" {
		return "", newUserError("Only Arma 3 is supported in the current release.")
	}

	session, err := handler.service.Create(
		ctx,
		appsession.CreateCommand{
			Actor:          actor,
			CorrelationID:  correlationID,
			IdempotencyKey: "discord:" + strings.TrimSpace(payload.ID),
			Slug:           slug,
			DisplayName:    displayName,
			GameType:       gameType,
			GuildID:        strings.TrimSpace(payload.GuildID),
			ChannelID:      strings.TrimSpace(payload.ChannelID),
		},
	)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}

	return formatCreatedSession(session), nil
}

func (handler *Handler) listSessions(
	ctx context.Context,
	actor domain.Actor,
	guildID string,
) (string, error) {
	sessions, err := handler.service.List(
		ctx,
		appsession.ListQuery{Actor: actor, Limit: 100},
	)
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}

	guildID = strings.TrimSpace(guildID)
	visible := make([]domain.Session, 0, 10)
	for _, session := range sessions {
		if session.GuildID != guildID {
			continue
		}

		visible = append(visible, session)
		if len(visible) == 10 {
			break
		}
	}

	return formatSessionList(visible), nil
}

func (handler *Handler) sessionStatus(
	ctx context.Context,
	options []applicationCommandOption,
	actor domain.Actor,
	guildID string,
) (string, error) {
	sessionID, err := stringOption(options, "session-id", true)
	if err != nil {
		return "", newUserError("A session ID is required.")
	}

	session, err := handler.service.Get(
		ctx,
		appsession.GetQuery{Actor: actor, SessionID: sessionID},
	)
	if err != nil {
		return "", fmt.Errorf("get session status: %w", err)
	}

	if session.GuildID != strings.TrimSpace(guildID) {
		return "", fmt.Errorf(
			"session belongs to another Discord guild: %w",
			domain.ErrForbidden,
		)
	}

	return formatSessionStatus(session), nil
}

func (handler *Handler) commandErrorMessage(err error, correlationID string) string {
	var userErr userError
	if errors.As(err, &userErr) {
		return userErr.message
	}

	switch {
	case errors.Is(err, domain.ErrNotFound):
		return "Session not found. Use `/session list` to see your sessions."
	case errors.Is(err, domain.ErrForbidden):
		return "You do not have access to that session."
	case errors.Is(err, domain.ErrIdempotencyConflict):
		return "Discord reused this interaction ID for different command data. Please run the command again."
	default:
		return fmt.Sprintf(
			"The command failed. Reference: `%s`",
			sanitizeInline(correlationID),
		)
	}
}

func validateContentType(value string) error {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return err
	}

	if mediaType != "application/json" {
		return fmt.Errorf("unexpected media type %q", mediaType)
	}

	return nil
}

func readBody(
	writer http.ResponseWriter,
	request *http.Request,
	limit int64,
) ([]byte, error) {
	request.Body = http.MaxBytesReader(writer, request.Body, limit)
	defer request.Body.Close()

	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}

	if len(body) == 0 {
		return nil, fmt.Errorf("request body is empty")
	}

	return body, nil
}

func writeInteractionMessage(writer http.ResponseWriter, content string) {
	writeJSON(
		writer,
		http.StatusOK,
		interactionResponse{
			Type: interactionResponseChannelMessageWithSource,
			Data: &interactionResponseData{
				Content: content,
				Flags:   messageFlagEphemeral,
				AllowedMentions: interactionAllowedMentions{
					Parse: []string{},
				},
			},
		},
	)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)

	if err := json.NewEncoder(writer).Encode(value); err != nil {
		slog.Default().Error("failed to encode HTTP response", slog.Any("error", err))
	}
}

func signatureFailureReason(err error) string {
	switch {
	case errors.Is(err, errStaleTimestamp):
		return "stale_timestamp"
	case errors.Is(err, errInvalidSignature):
		return "invalid_signature"
	default:
		return "signature_validation_error"
	}
}

type userError struct {
	message string
}

func newUserError(message string) userError {
	return userError{message: message}
}

func (err userError) Error() string {
	return err.message
}
