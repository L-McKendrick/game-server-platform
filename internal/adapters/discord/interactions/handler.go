package interactions

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const (
	defaultMaxRequestBytes = int64(64 * 1024)
	defaultSignatureMaxAge = 5 * time.Minute
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

	Select(
		ctx context.Context,
		query appsession.SelectQuery,
	) ([]appsession.Selection, error)

	Resolve(
		ctx context.Context,
		query appsession.ResolveQuery,
	) (appsession.Selection, error)
	ResolveCardControl(ctx context.Context, query appsession.CardControlQuery) (domain.Session, error)

	Configure(
		ctx context.Context,
		command appsession.ConfigureCommand,
	) (domain.Session, error)

	RequestArtifactIngest(
		ctx context.Context,
		actor domain.Actor,
		request domain.ArtifactIngestRequest,
	) error
	RequestSessionCard(ctx context.Context, command appsession.SessionCardCommand) error
	GetActiveModlist(ctx context.Context, query appsession.ActiveModlistQuery) (appsession.ActiveModlist, error)
	PrepareCreationArtifacts(ctx context.Context, command appsession.PrepareCreationArtifactsCommand) (domain.Session, error)
	UpdateDraftSetup(ctx context.Context, command appsession.UpdateDraftSetupCommand) (domain.Session, error)
	UpdateModOptions(ctx context.Context, command appsession.UpdateModOptionsCommand) (domain.Session, error)
	UpdateMission(ctx context.Context, command appsession.UpdateMissionCommand) (domain.Session, error)

	RequestStart(ctx context.Context, command appsession.StartCommand) error
	RequestLifecycle(ctx context.Context, command appsession.LifecycleCommand) error
	RequestConfirmation(ctx context.Context, command appsession.ConfirmationRequest) (domain.Confirmation, error)
	Confirm(ctx context.Context, command appsession.ConfirmCommand) (domain.Confirmation, error)
	CancelConfirmation(ctx context.Context, command appsession.CancelConfirmationCommand) (domain.Confirmation, error)
	RequestWorkflowCancellation(ctx context.Context, command appsession.WorkflowCancellationCommand) (domain.Workflow, error)
}

type AccessService interface {
	Authorize(ctx context.Context, guildID string, channelID string, userID string, roles []string) error
	AllowedRoles(ctx context.Context, guildID string) ([]string, int64, error)
	Configure(ctx context.Context, guildID string, userID string, canManageGuild bool, roleIDs []string, channelIDs []string) (domain.GuildAccessPolicy, error)
	ClearRoles(ctx context.Context, guildID string, userID string, canManageGuild bool, expectedVersion int64) (domain.GuildAccessPolicy, error)
	PublicCardChannel(ctx context.Context, guildID string) (string, error)
	ConfigurePublicCardChannel(ctx context.Context, guildID, userID string, canManageGuild bool, channelID string) (domain.GuildAccessPolicy, error)
}

type ResetService interface {
	Enabled() bool
	Active(ctx context.Context) (domain.ResetOperation, bool, error)
	Latest(ctx context.Context) (domain.ResetOperation, bool, error)
	Prepare(ctx context.Context, confirmationID, guildID, actorID string, isAdministrator bool) (domain.ResetConfirmation, error)
	Start(ctx context.Context, confirmationID, operationID, correlationID, guildID, actorID, phrase string, isAdministrator bool) (domain.ResetOperation, error)
	Status(ctx context.Context, operationID, guildID string, isAdministrator bool) (domain.ResetOperation, error)
}

type ServerConfigService interface {
	Current(ctx context.Context, guildID string, isAdministrator bool) (domain.GuildServerConfig, bool, error)
	RequestUpload(ctx context.Context, request domain.ArtifactIngestRequest, isAdministrator bool) error
	Remove(ctx context.Context, guildID, actorID string, expectedRevision int64, isAdministrator bool) (domain.GuildServerConfig, error)
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
	PlayerQuery     ports.PlayerQuery
	ResetService    ResetService
	ServerConfig    ServerConfigService
}

// Handler verifies and routes Discord HTTP interactions.
type Handler struct {
	service         SessionService
	access          AccessService
	ids             IDGenerator
	clock           Clock
	logger          *slog.Logger
	playerQuery     ports.PlayerQuery
	reset           ResetService
	serverConfig    ServerConfigService
	publicKey       ed25519.PublicKey
	applicationID   string
	allowedGuildIDs map[string]struct{}
	maxRequestBytes int64
	signatureMaxAge time.Duration
}

// NewHandler creates a Discord interaction handler.
func NewHandler(
	service SessionService,
	access AccessService,
	ids IDGenerator,
	clock Clock,
	logger *slog.Logger,
	config Config,
) (*Handler, error) {
	switch {
	case service == nil:
		return nil, fmt.Errorf("session service is required")
	case access == nil:
		return nil, fmt.Errorf("access service is required")
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
		access:          access,
		ids:             ids,
		clock:           clock,
		logger:          logger,
		playerQuery:     config.PlayerQuery,
		reset:           config.ResetService,
		serverConfig:    config.ServerConfig,
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

	if payload.Type != interactionTypeApplicationCommand &&
		payload.Type != interactionTypeMessageComponent &&
		payload.Type != interactionTypeApplicationCommandAutocomplete &&
		payload.Type != interactionTypeModalSubmit {
		writeInteractionMessage(writer, "This interaction type is not supported yet.")
		return
	}

	if strings.TrimSpace(payload.ID) == "" {
		handler.logger.Warn("rejected Discord command without interaction ID")
		http.Error(writer, "invalid interaction payload", http.StatusBadRequest)
		return
	}

	guildID := strings.TrimSpace(payload.GuildID)
	if guildID == "" {
		handler.logger.Warn("rejected Discord interaction outside a guild")
		if payload.Type == interactionTypeApplicationCommandAutocomplete {
			writeAutocompleteChoices(writer, nil)
		} else {
			writeInteractionMessage(writer, "This command is available only in a configured Discord server.")
		}
		return
	}
	if _, allowed := handler.allowedGuildIDs[guildID]; !allowed {
		handler.logger.Warn(
			"rejected Discord interaction from unapproved guild",
			slog.String("guild_id", payload.GuildID),
		)
		if payload.Type == interactionTypeApplicationCommandAutocomplete {
			writeAutocompleteChoices(writer, nil)
		} else {
			writeInteractionMessage(writer, "This app is not enabled in this Discord server.")
		}
		return
	}
	actorID := payload.actorID()
	roles := []string{}
	if payload.Member != nil {
		roles = payload.Member.Roles
	}
	if payload.isAdminCommand() || payload.isAdminComponent() {
		if !payload.memberCanManageGuild() {
			handler.logger.Warn("rejected Discord administration without Manage Server permission", slog.String("guild_id", payload.GuildID))
			if payload.Type == interactionTypeApplicationCommandAutocomplete {
				writeAutocompleteChoices(writer, nil)
			} else {
				writeInteractionMessage(writer, "Only members with Administrator or Manage Server permission can use `/rb admin` actions.")
			}
			return
		}
		if payload.Type == interactionTypeApplicationCommandAutocomplete {
			choices, err := handler.sessionAutocompleteChoices(request.Context(), payload, domain.Actor{
				Type: domain.ActorTypeDiscordUser, ID: actorID,
			})
			if err != nil {
				handler.logger.Warn("Discord admin autocomplete failed", slog.String("guild_id", payload.GuildID), slog.Any("error", err))
			}
			writeAutocompleteChoices(writer, choices)
			return
		}
		correlationID, err := handler.ids.New(handler.clock.Now().UTC())
		if err != nil {
			handler.logger.Error("failed to generate Discord correlation ID", slog.Any("error", err))
			writeInteractionMessage(writer, "The command could not be processed. Please try again.")
			return
		}
		if err := handler.handleAdmin(request.Context(), writer, payload, actorID, correlationID); err != nil {
			handler.logger.Error("Discord administration failed", slog.String("correlation_id", correlationID), slog.Any("error", err))
			writeInteractionMessage(writer, handler.adminErrorMessage(err, correlationID))
			return
		}
		handler.logger.Info("Discord administration completed", slog.String("correlation_id", correlationID), slog.String("guild_id", payload.GuildID))
		return
	}
	if handler.reset != nil {
		if operation, active, err := handler.reset.Active(request.Context()); err != nil {
			handler.logger.Error("failed to check platform reset lock", slog.Any("error", err))
			writeInteractionMessage(writer, "The platform state could not be checked safely. No operation was queued. Please try again later.")
			return
		} else if active {
			writeInteractionMessage(writer, fmt.Sprintf("A platform reset is in progress at **%s**. No session operation was queued.", sanitizeInline(operation.Stage)))
			return
		}
	}
	if err := handler.access.Authorize(request.Context(), payload.GuildID, payload.ChannelID, actorID, roles); err != nil && !payload.memberCanManageGuild() {
		handler.logger.Warn("rejected unauthorized Discord interaction", slog.String("guild_id", payload.GuildID), slog.String("channel_id", payload.ChannelID))
		if payload.Type == interactionTypeApplicationCommandAutocomplete {
			writeAutocompleteChoices(writer, nil)
		} else {
			writeInteractionMessage(writer, "You are not authorized to use this app in this channel.")
		}
		return
	}
	if payload.Type == interactionTypeMessageComponent {
		if isMissionManagerComponent(payload) {
			if err := handler.handleMissionManagerComponent(request.Context(), writer, payload, domain.Actor{Type: domain.ActorTypeDiscordUser, ID: actorID}); err != nil {
				writeInteractionMessage(writer, componentErrorMessage(err))
			}
			return
		}
		if isCreateModsContinue(payload) {
			err := handler.openCreateModsModal(request.Context(), writer, payload, domain.Actor{Type: domain.ActorTypeDiscordUser, ID: actorID})
			if err != nil {
				writeInteractionMessage(writer, componentErrorMessage(err))
			}
			return
		}
		content, err := handler.handleSessionCardControl(request.Context(), payload, domain.Actor{
			Type: domain.ActorTypeDiscordUser, ID: actorID,
		})
		if err != nil {
			handler.logger.Warn("Discord session card control failed", slog.String("guild_id", payload.GuildID), slog.Any("error", err))
			content = componentErrorMessage(err)
		}
		writeInteractionMessage(writer, content)
		return
	}
	if payload.Type == interactionTypeApplicationCommandAutocomplete {
		choices, err := handler.sessionAutocompleteChoices(request.Context(), payload, domain.Actor{
			Type: domain.ActorTypeDiscordUser,
			ID:   actorID,
		})
		if err != nil {
			handler.logger.Warn(
				"Discord session autocomplete failed",
				slog.String("guild_id", payload.GuildID),
				slog.Any("error", err),
			)
		}
		writeAutocompleteChoices(writer, choices)
		return
	}
	if payload.Type == interactionTypeModalSubmit && (payload.Data == nil || (payload.Data.CustomID != createModalCustomID && !isSetupModalCustomID(payload.Data.CustomID) && !isModsModalCustomID(payload.Data.CustomID) && !isMissionUploadModal(payload.Data.CustomID) && !strings.HasPrefix(payload.Data.CustomID, adminResetModalPrefix) && !strings.HasPrefix(payload.Data.CustomID, adminServerConfigUploadPrefix))) {
		writeInteractionMessage(writer, "This modal is not supported or has expired.")
		return
	}
	if payload.isRBCreateCommand() {
		if message := payload.channelCapabilities().setupBlockedMessage(false); message != "" {
			writeInteractionMessage(writer, message)
			return
		}
		gameType, err := createGameType(payload)
		if err != nil {
			writeInteractionMessage(writer, err.Error())
			return
		}
		writeCreateModal(writer, gameType)
		return
	}
	if payload.isRBSetupCommand() {
		if message := payload.channelCapabilities().setupBlockedMessage(true); message != "" {
			writeInteractionMessage(writer, message)
			return
		}
		correlationID, err := handler.ids.New(handler.clock.Now().UTC())
		if err != nil {
			writeInteractionMessage(writer, "The command could not be processed. Please try again.")
			return
		}
		err = handler.openSetupModal(request.Context(), writer, payload, domain.Actor{Type: domain.ActorTypeDiscordUser, ID: actorID})
		if err != nil {
			writeInteractionMessage(writer, handler.commandErrorMessage(err, correlationID))
		}
		return
	}
	if payload.isRBEditCommand() {
		if message := payload.channelCapabilities().setupBlockedMessage(true); message != "" {
			writeInteractionMessage(writer, message)
			return
		}
		correlationID, err := handler.ids.New(handler.clock.Now().UTC())
		if err != nil {
			writeInteractionMessage(writer, "The command could not be processed. Please try again.")
			return
		}
		if err := handler.openEdit(request.Context(), writer, payload, domain.Actor{Type: domain.ActorTypeDiscordUser, ID: actorID}); err != nil {
			writeInteractionMessage(writer, handler.commandErrorMessage(err, correlationID))
		}
		return
	}

	correlationID, err := handler.ids.New(handler.clock.Now().UTC())
	if err != nil {
		handler.logger.Error("failed to generate Discord correlation ID", slog.Any("error", err))
		writeInteractionMessage(writer, "The command could not be processed. Please try again.")
		return
	}
	if payload.Type == interactionTypeModalSubmit {
		edit := isSetupModalCustomID(payload.Data.CustomID) || isModsModalCustomID(payload.Data.CustomID) || isMissionUploadModal(payload.Data.CustomID)
		if message := payload.channelCapabilities().setupBlockedMessage(edit); message != "" {
			writeInteractionMessage(writer, message)
			return
		}
		actor := domain.Actor{
			Type: domain.ActorTypeDiscordUser,
			ID:   actorID,
		}
		var content string
		var components []interactionComponent
		if isMissionUploadModal(payload.Data.CustomID) {
			content, err = handler.submitMissionUpload(request.Context(), payload, actor, correlationID)
		} else if isModsModalCustomID(payload.Data.CustomID) {
			content, err = handler.submitModsModal(request.Context(), payload, actor, correlationID)
		} else if isSetupModalCustomID(payload.Data.CustomID) {
			content, err = handler.submitSetupModal(request.Context(), payload, actor, correlationID)
		} else {
			var result createModalResult
			result, err = handler.submitCreateModal(request.Context(), payload, actor, correlationID)
			content, components = result.content, result.components
		}
		if err != nil {
			content = handler.commandErrorMessage(err, correlationID)
			handler.logger.Error(
				"Discord creation modal failed",
				slog.String("correlation_id", correlationID),
				slog.Any("error", err),
			)
		} else {
			handler.logger.Info(
				"Discord creation modal completed",
				slog.String("correlation_id", correlationID),
			)
		}
		writeInteractionMessageWithComponents(writer, content, components)
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
		return "", "rb", newUserError("Discord user information is missing from the command.")
	}

	if strings.TrimSpace(payload.ChannelID) == "" {
		return "", "rb", newUserError("Discord channel information is missing from the command.")
	}

	actor := domain.Actor{
		Type: domain.ActorTypeDiscordUser,
		ID:   actorID,
	}
	subcommand, err := payload.subcommand()
	if err != nil {
		return "", "rb", newUserError("Use one of the supported `/rb` subcommands.")
	}

	commandName := "rb " + subcommand.Name
	switch subcommand.Name {
	case "help":
		content, err := handler.help(ctx, subcommand.Options, actor, payload.GuildID)
		return content, commandName, err
	case "list":
		content, err := handler.listSessions(ctx, subcommand.Options, actor, payload.GuildID)
		return content, commandName, err
	case "status":
		content, err := handler.sessionStatus(
			ctx,
			subcommand.Options,
			actor,
			payload.GuildID,
		)
		return content, commandName, err
	case "configure":
		content, err := handler.configureSession(ctx, payload, subcommand.Options, actor, correlationID)
		return content, commandName, err
	case "upload-mission":
		content, err := handler.requestArtifactIngest(ctx, payload, subcommand.Options, actor, correlationID, domain.ArtifactMission)
		return content, commandName, err
	case "upload-preset":
		content, err := handler.requestArtifactIngest(ctx, payload, subcommand.Options, actor, correlationID, domain.ArtifactPreset)
		return content, commandName, err
	case "start":
		content, err := handler.startSession(ctx, payload, subcommand.Options, actor, correlationID)
		return content, commandName, err
	case "sleep", "wake", "restore":
		content, err := handler.requestLifecycle(ctx, payload, subcommand.Options, actor, correlationID, subcommand.Name)
		return content, commandName, err
	case "archive", "terminate":
		content, err := handler.createConfirmation(ctx, payload, subcommand.Options, actor, subcommand.Name)
		return content, commandName, err
	case "confirm":
		content, err := handler.confirmAction(ctx, payload, subcommand.Options, actor, correlationID)
		return content, commandName, err
	case "cancel-confirmation":
		content, err := handler.cancelConfirmation(ctx, payload, subcommand.Options, actor)
		return content, commandName, err
	case "cancel":
		content, err := handler.cancelWorkflow(ctx, payload, subcommand.Options, actor, correlationID)
		return content, commandName, err
	default:
		return "", commandName, newUserError(
			"That `/rb` subcommand is not supported yet.",
		)
	}
}

func (handler *Handler) cancelWorkflow(ctx context.Context, payload interactionPayload, options []applicationCommandOption, actor domain.Actor, correlationID string) (string, error) {
	sessionID, err := handler.resolveSessionID(ctx, options, actor, payload.GuildID, false, false)
	if err != nil {
		return "", err
	}
	workflow, err := handler.service.RequestWorkflowCancellation(ctx, appsession.WorkflowCancellationCommand{Actor: actor, SessionID: sessionID, GuildID: payload.GuildID, CorrelationID: correlationID})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("**Cancellation requested**\nThe `%s` workflow will stop only if it has not crossed its initial safe boundary. Otherwise its current operation and any required rollback will finish. No new retry was scheduled.\n\nNext: use `/rb status` to verify the authoritative outcome.", workflow.Type), nil
}

func (handler *Handler) createConfirmation(ctx context.Context, payload interactionPayload, options []applicationCommandOption, actor domain.Actor, action string) (string, error) {
	sessionID, err := handler.resolveSessionID(ctx, options, actor, payload.GuildID, false, false)
	if err != nil {
		return "", err
	}
	confirmationAction := domain.ConfirmationArchive
	if action == "terminate" {
		confirmationAction = domain.ConfirmationTerminate
	}
	_, err = handler.service.RequestConfirmation(ctx, appsession.ConfirmationRequest{
		Actor: actor, SessionID: sessionID, GuildID: payload.GuildID, RequestID: payload.ID, Action: confirmationAction,
	})
	if err != nil {
		if errors.Is(err, domain.ErrIdempotencyConflict) {
			return "", newUserError("You already have a pending archive or termination in this server. Run `/rb confirm` or `/rb cancel-confirmation` before requesting another one.")
		}
		return "", err
	}
	if action == "archive" {
		return "**Archive confirmation required**\nNo destructive work has been queued. Archiving stops game services and removes EC2/EBS only after the portable backup is verified. A later restore creates billable replacement resources.\n\nWithin 10 minutes, run `/rb confirm` without any options. To cancel, run `/rb cancel-confirmation`.", nil
	}
	return "**Termination confirmation required**\nNo destructive work has been queued. Termination permanently deletes tagged EC2/EBS resources and all stored session artifacts without creating a backup. This is irreversible.\n\nWithin 10 minutes, run `/rb confirm` without any options. To cancel, run `/rb cancel-confirmation`.", nil
}

func (handler *Handler) confirmAction(ctx context.Context, payload interactionPayload, options []applicationCommandOption, actor domain.Actor, correlationID string) (string, error) {
	if len(options) != 0 {
		return "", newUserError("Run `/rb confirm` without any options.")
	}
	roles := []string{}
	if payload.Member != nil {
		roles = append(roles, payload.Member.Roles...)
	}
	confirmation, err := handler.service.Confirm(ctx, appsession.ConfirmCommand{
		Actor: actor, Roles: roles, GuildID: payload.GuildID, ChannelID: payload.ChannelID,
		CommandID: payload.ID, CorrelationID: correlationID, IdempotencyKey: "discord:" + payload.ID,
	})
	if err != nil {
		return "", confirmationUserError(err)
	}
	action := "Archive"
	if confirmation.Action == domain.ConfirmationTerminate {
		action = "Termination"
	}
	return fmt.Sprintf("**%s request accepted**\nThe confirmation was consumed and cannot be replayed. Use `/rb status` to follow progress.", action), nil
}

func (handler *Handler) cancelConfirmation(ctx context.Context, payload interactionPayload, options []applicationCommandOption, actor domain.Actor) (string, error) {
	if len(options) != 0 {
		return "", newUserError("Run `/rb cancel-confirmation` without any options.")
	}
	confirmation, err := handler.service.CancelConfirmation(ctx, appsession.CancelConfirmationCommand{Actor: actor, GuildID: payload.GuildID})
	if err != nil {
		return "", confirmationUserError(err)
	}
	return fmt.Sprintf("**%s confirmation cancelled**\nNo destructive work was queued. The pending action cannot be confirmed.\n\nNext: use `/rb status` or request a new action if it is still appropriate.", strings.ToUpper(string(confirmation.Action[:1]))+strings.ToLower(string(confirmation.Action[1:]))), nil
}

func confirmationUserError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrConfirmationMismatch):
		return newUserError("You have no pending archive or termination to confirm in this server. Run the destructive command again if it is still appropriate.")
	case errors.Is(err, domain.ErrConfirmationExpired):
		return newUserError("That confirmation expired. Run the archive or terminate command again to create a new ten-minute confirmation.")
	case errors.Is(err, domain.ErrConfirmationConsumed):
		return newUserError("That confirmation was already used and cannot be replayed.")
	case errors.Is(err, domain.ErrConfirmationCancelled):
		return newUserError("That confirmation was cancelled and cannot be used.")
	case errors.Is(err, domain.ErrConfirmationStateDrift):
		return newUserError("The session changed after this confirmation was created. Run `/rb status`, then request a new confirmation if the action is still appropriate.")
	case errors.Is(err, domain.ErrConfirmationDispatchUncertain):
		return newUserError("The confirmation was consumed, but queue delivery could not be confirmed. No automatic retry is scheduled. Check `/rb status`; if no operation appears, run archive or terminate again. Resources may remain and incur cost.")
	default:
		return err
	}
}

func (handler *Handler) requestLifecycle(ctx context.Context, payload interactionPayload, options []applicationCommandOption, actor domain.Actor, correlationID, action string) (string, error) {
	sessionID, err := handler.resolveSessionID(
		ctx, options, actor, payload.GuildID,
		payload.memberCanManageGuild() && (action == "sleep" || action == "wake"),
		false,
	)
	if err != nil {
		return "", err
	}
	roles := []string{}
	if payload.Member != nil {
		roles = append(roles, payload.Member.Roles...)
	}
	typeName := domain.CommandSleepSession
	if action == "wake" {
		typeName = domain.CommandWakeSession
	} else if action == "archive" {
		typeName = domain.CommandArchiveSession
	} else if action == "restore" {
		typeName = domain.CommandRestoreSession
	} else if action == "terminate" {
		typeName = domain.CommandDestroySession
	}
	if err := handler.service.RequestLifecycle(ctx, appsession.LifecycleCommand{Actor: actor, Roles: roles, SessionID: sessionID, GuildID: payload.GuildID, ChannelID: payload.ChannelID, CommandID: payload.ID, CorrelationID: correlationID, IdempotencyKey: "discord:" + payload.ID, CommandType: typeName, CanManageGuild: payload.memberCanManageGuild()}); err != nil {
		return "", err
	}
	message := fmt.Sprintf("**%s request accepted**\nUse `/rb status` to follow progress.", strings.ToUpper(action[:1])+action[1:])
	if action == "archive" {
		message += "\nThe game services will stop while the archive is captured. EC2 and EBS are removed only after the archive and manifest checksums are durably verified."
	} else if action == "restore" {
		message += "\nNew EC2 and EBS resources will be created; the recorded archive is revalidated before provisioning."
	} else if action == "terminate" {
		message += "\nTermination is irreversible. Tagged EC2/EBS resources and all stored session artifact/archive versions will be permanently deleted; only an audit tombstone remains."
	}
	return message, nil
}

func (handler *Handler) startSession(
	ctx context.Context,
	payload interactionPayload,
	options []applicationCommandOption,
	actor domain.Actor,
	correlationID string,
) (string, error) {
	sessionID, err := handler.resolveSessionID(ctx, options, actor, payload.GuildID, false, false)
	if err != nil {
		return "", err
	}
	roles := []string{}
	if payload.Member != nil {
		roles = append(roles, payload.Member.Roles...)
	}
	if err := handler.service.RequestStart(ctx, appsession.StartCommand{
		Actor: actor, Roles: roles, SessionID: sessionID,
		GuildID: strings.TrimSpace(payload.GuildID), ChannelID: strings.TrimSpace(payload.ChannelID),
		CommandID: strings.TrimSpace(payload.ID), CorrelationID: correlationID,
		IdempotencyKey: "discord:" + strings.TrimSpace(payload.ID),
	}); err != nil {
		return "", fmt.Errorf("request session start: %w", err)
	}
	return "**Start request accepted**\nUse `/rb status` to follow provisioning or bootstrap progress.", nil
}

func (handler *Handler) handleAdmin(ctx context.Context, writer http.ResponseWriter, payload interactionPayload, actorID, correlationID string) error {
	if payload.isAdminCommand() {
		handler.writeAdminView(writer, interactionResponseChannelMessageWithSource,
			"**Server administration**\nChoose an administration area. Normal access is role-based; every control here rechecks Administrator or Manage Server.",
			"", nil, payload.memberIsAdministrator())
		return nil
	}
	if payload.Data == nil {
		return newUserError("This administration control is invalid or has expired.")
	}
	if handler.reset != nil && (payload.Data.CustomID == adminRoleSelectCustomID || payload.Data.CustomID == adminRepairSelectCustomID || payload.Data.CustomID == adminPublicCardChannelCustomID || strings.HasPrefix(payload.Data.CustomID, adminRoleClearConfirmCustomID+":") || strings.HasPrefix(payload.Data.CustomID, adminServerConfigUploadPrefix) || strings.HasPrefix(payload.Data.CustomID, adminServerConfigConfirmPrefix)) {
		if operation, active, err := handler.reset.Active(ctx); err != nil {
			return fmt.Errorf("check reset mutation lock: %w", err)
		} else if active {
			return newUserError(fmt.Sprintf("Platform reset is in progress at **%s**. This administration change was not applied.", sanitizeInline(operation.Stage)))
		}
	}
	if strings.HasPrefix(payload.Data.CustomID, adminServerConfigUploadPrefix) && payload.Type == interactionTypeModalSubmit {
		return handler.submitServerConfigModal(ctx, writer, payload, actorID, correlationID)
	}
	if strings.HasPrefix(payload.Data.CustomID, adminRoleClearConfirmCustomID+":") {
		if payload.Data.ComponentType != componentTypeButton {
			return newUserError("This administration control is invalid or has expired.")
		}
		expectedVersion, err := strconv.ParseInt(strings.TrimPrefix(payload.Data.CustomID, adminRoleClearConfirmCustomID+":"), 10, 64)
		if err != nil || expectedVersion < 0 {
			return newUserError("This administration control is invalid or has expired.")
		}
		policy, err := handler.access.ClearRoles(ctx, payload.GuildID, actorID, true, expectedVersion)
		if err != nil {
			return fmt.Errorf("remove guild role access: %w", err)
		}
		return handler.writeAdminAccessView(ctx, writer, interactionResponseUpdateMessage, payload.GuildID,
			fmt.Sprintf("All normal role access was removed at revision `%d`.", policy.Version), payload.memberIsAdministrator())
	}
	if strings.HasPrefix(payload.Data.CustomID, adminResetModalPrefix) {
		if !payload.memberIsAdministrator() {
			return domain.ErrForbidden
		}
		confirmationID := strings.TrimSpace(strings.TrimPrefix(payload.Data.CustomID, adminResetModalPrefix))
		phrase, err := resetModalPhrase(payload)
		if err != nil {
			return err
		}
		if handler.reset == nil {
			return domain.ErrFeatureDisabled
		}
		operation, err := handler.reset.Start(ctx, confirmationID, "reset-"+strings.TrimSpace(payload.ID), correlationID, payload.GuildID, actorID, phrase, true)
		if err != nil {
			return fmt.Errorf("start platform reset: %w", err)
		}
		handler.writeAdminView(writer, interactionResponseChannelMessageWithSource,
			fmt.Sprintf("**Platform reset queued**\nStage: %s\nNew session operations are frozen until cleanup finishes. No automatic retry is scheduled if cleanup becomes incomplete.", sanitizeInline(operation.Stage)),
			adminMenuReset, nil, true)
		return nil
	}
	switch payload.Data.CustomID {
	case adminMenuCustomID:
		if payload.Data.ComponentType != componentTypeStringSelect || len(payload.Data.Values) != 1 {
			return newUserError("Choose one administration area.")
		}
		switch payload.Data.Values[0] {
		case adminMenuAccess:
			return handler.writeAdminAccessView(ctx, writer, interactionResponseUpdateMessage, payload.GuildID, "", payload.memberIsAdministrator())
		case adminMenuRepair:
			return handler.writeAdminRepairView(ctx, writer, payload, actorID)
		case adminMenuPublicCard:
			return handler.writeAdminPublicCardView(ctx, writer, interactionResponseUpdateMessage, payload.GuildID, "", payload.memberIsAdministrator())
		case adminMenuReset:
			if !payload.memberIsAdministrator() {
				return domain.ErrForbidden
			}
			return handler.writeAdminResetView(ctx, writer, interactionResponseUpdateMessage, payload.GuildID)
		case adminMenuServerConfig:
			if !payload.memberIsAdministrator() {
				return domain.ErrForbidden
			}
			return handler.writeAdminServerConfigView(ctx, writer, interactionResponseUpdateMessage, payload.GuildID, "")
		default:
			return newUserError("That administration area is not available.")
		}
	case adminRoleSelectCustomID:
		if payload.Data.ComponentType != componentTypeRoleSelect || len(payload.Data.Values) == 0 || len(payload.Data.Values) > 25 {
			return newUserError("Select between one and 25 Discord roles.")
		}
		if payload.Data.Resolved == nil || !resolvedRolesContain(payload.Data.Resolved.Roles, payload.Data.Values) {
			return newUserError("Discord could not verify every selected role. Reopen `/rb admin` and try again.")
		}
		policy, err := handler.access.Configure(ctx, payload.GuildID, actorID, true, payload.Data.Values, nil)
		if err != nil {
			return fmt.Errorf("configure guild access: %w", err)
		}
		mentions := make([]string, 0, len(policy.AllowedRoleIDs))
		for _, roleID := range policy.AllowedRoleIDs {
			mentions = append(mentions, "<@&"+roleID+">")
		}
		return handler.writeAdminAccessView(ctx, writer, interactionResponseUpdateMessage, payload.GuildID,
			fmt.Sprintf("Access settings updated to revision `%d`: %s.", policy.Version, strings.Join(mentions, ", ")), payload.memberIsAdministrator())
	case adminPublicCardChannelCustomID:
		if payload.Data.ComponentType != componentTypeChannelSelect || len(payload.Data.Values) != 1 {
			return newUserError("Choose one Discord text channel.")
		}
		if payload.Data.Resolved == nil || !resolvedTextChannelsContain(payload.Data.Resolved.Channels, payload.Data.Values) {
			return newUserError("Discord could not verify the selected channel. Reopen `/rb admin` and try again.")
		}
		policy, err := handler.access.ConfigurePublicCardChannel(ctx, payload.GuildID, actorID, true, payload.Data.Values[0])
		if err != nil {
			return fmt.Errorf("configure public card channel: %w", err)
		}
		return handler.writeAdminPublicCardView(ctx, writer, interactionResponseUpdateMessage, payload.GuildID,
			fmt.Sprintf("New public session cards will be posted in <#%s> at revision `%d`.", policy.PublicCardChannelID, policy.Version), payload.memberIsAdministrator())
	case adminRoleClearPromptCustomID:
		if payload.Data.ComponentType != componentTypeButton {
			return newUserError("This administration control is invalid or has expired.")
		}
		roleIDs, version, err := handler.access.AllowedRoles(ctx, payload.GuildID)
		if err != nil {
			return fmt.Errorf("read guild access roles: %w", err)
		}
		if len(roleIDs) == 0 {
			return handler.writeAdminAccessView(ctx, writer, interactionResponseUpdateMessage, payload.GuildID, "Normal role access is already disabled.", payload.memberIsAdministrator())
		}
		controls := []interactionComponent{{Type: componentTypeActionRow, Components: []interactionComponent{
			{Type: componentTypeButton, Style: buttonStyleDanger, Label: "Remove role access", CustomID: fmt.Sprintf("%s:%d", adminRoleClearConfirmCustomID, version)},
			{Type: componentTypeButton, Style: buttonStyleSecondary, Label: "Keep current roles", CustomID: adminRoleClearCancelCustomID},
		}}}
		handler.writeAdminView(writer, interactionResponseUpdateMessage,
			"**Remove all normal role access?**\nMembers will no longer be able to use normal `/rb` commands. Administrators and members with Manage Server can still reopen `/rb admin` and restore access.",
			adminMenuAccess, controls, payload.memberIsAdministrator())
		return nil
	case adminRoleClearCancelCustomID:
		if payload.Data.ComponentType != componentTypeButton {
			return newUserError("This administration control is invalid or has expired.")
		}
		return handler.writeAdminAccessView(ctx, writer, interactionResponseUpdateMessage, payload.GuildID, "No access roles were changed.", payload.memberIsAdministrator())
	case adminRepairSelectCustomID:
		if payload.Data.ComponentType != componentTypeStringSelect || len(payload.Data.Values) != 1 {
			return newUserError("Choose one session card to repair.")
		}
		value, err := json.Marshal(payload.Data.Values[0])
		if err != nil {
			return fmt.Errorf("encode selected session: %w", err)
		}
		content, err := handler.repairSessionCard(ctx, payload, []applicationCommandOption{{
			Type: applicationCommandOptionString, Name: "session", Value: value,
		}}, actorID, correlationID)
		if err != nil {
			return err
		}
		handler.writeAdminView(writer, interactionResponseUpdateMessage, content, adminMenuRepair, nil, payload.memberIsAdministrator())
		return nil
	case adminResetPrepareCustomID:
		if payload.Data.ComponentType != componentTypeButton || !payload.memberIsAdministrator() {
			return domain.ErrForbidden
		}
		if handler.reset == nil {
			return domain.ErrFeatureDisabled
		}
		confirmation, err := handler.reset.Prepare(ctx, "reset-confirm-"+strings.TrimSpace(payload.ID), payload.GuildID, actorID, true)
		if err != nil {
			return fmt.Errorf("prepare platform reset: %w", err)
		}
		writeResetConfirmationModal(writer, confirmation)
		return nil
	case adminServerConfigCancelID:
		if !payload.memberIsAdministrator() {
			return domain.ErrForbidden
		}
		return handler.writeAdminServerConfigView(ctx, writer, interactionResponseUpdateMessage, payload.GuildID, "No server configuration was removed.")
	default:
		if strings.HasPrefix(payload.Data.CustomID, adminServerConfigUploadPrefix) {
			return handler.openServerConfigModal(writer, payload)
		}
		if strings.HasPrefix(payload.Data.CustomID, adminServerConfigRemovePrefix) {
			return handler.writeServerConfigRemovePrompt(writer, payload)
		}
		if strings.HasPrefix(payload.Data.CustomID, adminServerConfigConfirmPrefix) {
			return handler.removeServerConfig(ctx, writer, payload, actorID)
		}
		return newUserError("This administration control is invalid or has expired.")
	}
}

func (handler *Handler) writeAdminPublicCardView(ctx context.Context, writer http.ResponseWriter, responseType int, guildID, status string, showReset bool) error {
	channelID, err := handler.access.PublicCardChannel(ctx, guildID)
	if err != nil {
		return fmt.Errorf("read public card channel: %w", err)
	}
	minimum, maximum := 1, 1
	selector := interactionComponent{
		Type: componentTypeChannelSelect, CustomID: adminPublicCardChannelCustomID,
		Placeholder: "Choose the public session-card channel", MinValues: &minimum, MaxValues: &maximum,
		ChannelTypes: []int{0},
	}
	current := "Not set — new cards use the channel where `/rb create` is submitted."
	if channelID != "" {
		current = "<#" + channelID + ">"
		selector.DefaultValues = []interactionSelectDefaultValue{{ID: channelID, Type: "channel"}}
	}
	content := "**Public session-card channel**\nCurrent channel: " + current + "\n\nChoose the text channel where new public session cards and their linked modlist messages should be posted."
	if strings.TrimSpace(status) != "" {
		content = "**Saved**\n" + status + "\n\n" + content
	}
	handler.writeAdminView(writer, responseType, content, adminMenuPublicCard, []interactionComponent{{Type: componentTypeActionRow, Components: []interactionComponent{selector}}}, showReset)
	return nil
}

func (handler *Handler) writeAdminAccessView(ctx context.Context, writer http.ResponseWriter, responseType int, guildID, status string, showReset bool) error {
	roleIDs, _, err := handler.access.AllowedRoles(ctx, guildID)
	if err != nil {
		return fmt.Errorf("read guild access roles: %w", err)
	}
	if len(roleIDs) > 25 {
		return newUserError("This server has more than 25 configured access roles. Reduce the policy through the operator runbook before editing it in Discord.")
	}
	minimum, maximum := 1, 25
	defaults := make([]interactionSelectDefaultValue, 0, len(roleIDs))
	mentions := make([]string, 0, len(roleIDs))
	for _, roleID := range roleIDs {
		defaults = append(defaults, interactionSelectDefaultValue{ID: roleID, Type: "role"})
		mentions = append(mentions, "<@&"+roleID+">")
	}
	controls := []interactionComponent{{
		Type: componentTypeActionRow,
		Components: []interactionComponent{{
			Type: componentTypeRoleSelect, CustomID: adminRoleSelectCustomID,
			Placeholder: "Replace allowed roles", MinValues: &minimum, MaxValues: &maximum, DefaultValues: defaults,
		}},
	}}
	if len(roleIDs) > 0 {
		controls = append(controls, interactionComponent{Type: componentTypeActionRow, Components: []interactionComponent{{
			Type: componentTypeButton, Style: buttonStyleDanger, Label: "Remove all role access", CustomID: adminRoleClearPromptCustomID,
		}}})
	}
	current := "None — normal member access is disabled."
	if len(mentions) > 0 {
		current = strings.Join(mentions, ", ")
	}
	content := "**Platform access**\nCurrent allowed roles: " + current + "\n\nUse the role picker to replace the complete allowed-role set. Use the removal action only when normal member access should be disabled. `/rb admin` always requires Administrator or Manage Server."
	if strings.TrimSpace(status) != "" {
		content = "**Saved**\n" + status + "\n\n" + content
	}
	handler.writeAdminView(writer, responseType,
		content,
		adminMenuAccess, controls, showReset)
	return nil
}

func (handler *Handler) writeAdminRepairView(ctx context.Context, writer http.ResponseWriter, payload interactionPayload, actorID string) error {
	selections, err := handler.service.Select(ctx, appsession.SelectQuery{
		Actor: domain.Actor{Type: domain.ActorTypeDiscordUser, ID: actorID}, GuildID: payload.GuildID,
		Limit: maximumAutocompleteChoices, AllowGuildMember: true,
	})
	if err != nil {
		return fmt.Errorf("select repairable sessions: %w", err)
	}
	if len(selections) == 0 {
		handler.writeAdminView(writer, interactionResponseUpdateMessage,
			"**Repair a session card**\nNo non-terminated session cards are available in this server.", adminMenuRepair, nil, payload.memberIsAdministrator())
		return nil
	}
	options := make([]interactionSelectOption, 0, len(selections))
	for _, selection := range selections {
		options = append(options, interactionSelectOption{
			Label: sessionSelectionLabel(selection), Value: selection.ID,
			Description: "Refresh or recreate the authoritative public card",
		})
	}
	minimum, maximum := 1, 1
	controls := []interactionComponent{{
		Type: componentTypeActionRow,
		Components: []interactionComponent{{
			Type: componentTypeStringSelect, CustomID: adminRepairSelectCustomID,
			Placeholder: "Choose a session card", MinValues: &minimum, MaxValues: &maximum, Options: options,
		}},
	}}
	handler.writeAdminView(writer, interactionResponseUpdateMessage,
		"**Repair a session card**\nChoose one session. The bot will refresh its authoritative card or recreate a deleted card in the original channel.",
		adminMenuRepair, controls, payload.memberIsAdministrator())
	return nil
}

func (handler *Handler) writeAdminView(writer http.ResponseWriter, responseType int, content, selected string, controls []interactionComponent, showReset bool) {
	minimum, maximum := 1, 1
	menu := interactionComponent{
		Type: componentTypeActionRow,
		Components: []interactionComponent{{
			Type: componentTypeStringSelect, CustomID: adminMenuCustomID,
			Placeholder: "Choose an administration area", MinValues: &minimum, MaxValues: &maximum,
			Options: []interactionSelectOption{
				{Label: "Access", Value: adminMenuAccess, Description: "Configure allowed Discord roles", Default: selected == adminMenuAccess},
				{Label: "Public card channel", Value: adminMenuPublicCard, Description: "Choose where new session cards are posted", Default: selected == adminMenuPublicCard},
				{Label: "Repair card", Value: adminMenuRepair, Description: "Refresh or recreate a session card", Default: selected == adminMenuRepair},
			},
		}},
	}
	if showReset {
		menu.Components[0].Options = append(menu.Components[0].Options, interactionSelectOption{Label: "Server config", Value: adminMenuServerConfig, Description: "Set the Arma server.cfg for future sessions", Default: selected == adminMenuServerConfig})
		menu.Components[0].Options = append(menu.Components[0].Options, interactionSelectOption{Label: "Reset platform", Value: adminMenuReset, Description: "Permanently clear runtime state", Default: selected == adminMenuReset})
	}
	components := append([]interactionComponent{menu}, controls...)
	flags := 0
	if responseType == interactionResponseChannelMessageWithSource {
		flags = messageFlagEphemeral
	}
	writeJSON(writer, http.StatusOK, interactionResponse{
		Type: responseType,
		Data: renderer.messageData(content, flags, &components),
	})
}

func (handler *Handler) writeAdminResetView(ctx context.Context, writer http.ResponseWriter, responseType int, guildID string) error {
	if handler.reset == nil || !handler.reset.Enabled() {
		handler.writeAdminView(writer, responseType, "**Reset platform**\nThis destructive feature is disabled in this deployment.", adminMenuReset, nil, true)
		return nil
	}
	if operation, active, err := handler.reset.Active(ctx); err != nil {
		return fmt.Errorf("read reset status: %w", err)
	} else if active {
		handler.writeAdminView(writer, responseType, fmt.Sprintf("**Platform reset in progress**\nStage: %s\nNo session operation can start until this finishes.", sanitizeInline(operation.Stage)), adminMenuReset, nil, true)
		return nil
	}
	latest, hasLatest, err := handler.reset.Latest(ctx)
	if err != nil {
		return fmt.Errorf("read latest reset result: %w", err)
	}
	controls := []interactionComponent{{Type: componentTypeActionRow, Components: []interactionComponent{{Type: componentTypeButton, Style: buttonStyleDanger, Label: "Prepare full reset", CustomID: adminResetPrepareCustomID}}}}
	content := "**Reset platform**\nPermanently removes all sessions, exactly tagged game instances and disposable volumes, session archives/files, bot session messages, queued runtime work, runtime metadata, and eligible pre-reset application logs.\n\nTerraform infrastructure, guild access, secrets, configuration, budgets, CloudTrail, billing records, and AWS-retained execution history remain. Resources may incur cost until deletion is verified."
	if hasLatest {
		if latest.Status == domain.ResetSucceeded {
			content += fmt.Sprintf("\n\n**Last reset:** complete at %s — %d sessions and %d stored object versions removed.", latest.CompletedAt.UTC().Format(time.RFC3339), latest.DeletedSessions, latest.DeletedObjects)
		} else {
			content += "\n\n**Last reset:** incomplete. Some runtime state may remain and incur cost. No automatic retry is scheduled; inspect the reset worker logs before preparing another reset."
		}
	}
	handler.writeAdminView(writer, responseType, content, adminMenuReset, controls, true)
	return nil
}

func writeResetConfirmationModal(writer http.ResponseWriter, confirmation domain.ResetConfirmation) {
	required := true
	minimum, maximum := len(confirmation.Phrase()), len(confirmation.Phrase())
	components := []interactionComponent{{Type: componentTypeActionRow, Components: []interactionComponent{{
		Type: componentTypeTextInput, CustomID: adminResetPhraseCustomID, Style: textInputStyleShort,
		Label: "Type the exact reset phrase", Placeholder: confirmation.Phrase(), Required: &required, MinLength: &minimum, MaxLength: &maximum,
	}}}}
	writeJSON(writer, http.StatusOK, interactionResponse{Type: interactionResponseModal, Data: &interactionResponseData{
		CustomID: adminResetModalPrefix + confirmation.ID, Title: "Confirm full platform reset", Components: &components,
	}})
}

func resetModalPhrase(payload interactionPayload) (string, error) {
	if payload.Type != interactionTypeModalSubmit || payload.Data == nil || len(payload.Data.Components) != 1 {
		return "", newUserError("This reset confirmation is malformed or expired. Reopen `/rb admin`.")
	}
	row := payload.Data.Components[0]
	if row.Type != componentTypeActionRow || len(row.Components) != 1 || row.Components[0].Type != componentTypeTextInput || row.Components[0].CustomID != adminResetPhraseCustomID {
		return "", newUserError("This reset confirmation is malformed or expired. Reopen `/rb admin`.")
	}
	return row.Components[0].Value, nil
}

func (handler *Handler) repairSessionCard(
	ctx context.Context,
	payload interactionPayload,
	options []applicationCommandOption,
	actorID string,
	correlationID string,
) (string, error) {
	actor := domain.Actor{Type: domain.ActorTypeDiscordUser, ID: actorID}
	sessionID, err := handler.resolveSessionID(ctx, options, actor, payload.GuildID, true, true)
	if err != nil {
		return "", err
	}
	session, err := handler.service.Get(ctx, appsession.GetQuery{
		Actor: actor, SessionID: sessionID, GuildID: payload.GuildID, AllowGuildMember: true,
	})
	if err != nil {
		return "", err
	}
	cardProjection := sessioncard.Project(session, sessioncard.Options{Now: session.UpdatedAt.UTC()})
	if err := handler.service.RequestSessionCard(ctx, appsession.SessionCardCommand{
		Actor: actor, SessionID: session.ID, GuildID: payload.GuildID, ChannelID: session.ChannelID,
		CorrelationID: correlationID, NotificationID: "card-admin-repair-" + strings.TrimSpace(payload.ID),
		Content: sessioncard.RenderPublic(cardProjection), Embed: sessioncard.RenderPublicEmbed(cardProjection), CardRevision: session.Version, AllowGuildMember: true, RequireCurrentRevision: true,
	}); err != nil {
		return "", fmt.Errorf("request session card repair: %w", err)
	}
	return "**Session card repair queued**\nThe bot will refresh the current card or recreate it in its original channel if it was deleted.", nil
}

func (payload interactionPayload) isAdminCommand() bool {
	return (payload.Type == interactionTypeApplicationCommand || payload.Type == interactionTypeApplicationCommandAutocomplete) &&
		payload.Data != nil && strings.TrimSpace(payload.Data.Name) == "rb" && len(payload.Data.Options) == 1 &&
		payload.Data.Options[0].Type == applicationCommandOptionSubcommand && strings.TrimSpace(payload.Data.Options[0].Name) == "admin" &&
		len(payload.Data.Options[0].Options) == 0
}

func (payload interactionPayload) isAdminComponent() bool {
	if payload.Data == nil {
		return false
	}
	if payload.Type == interactionTypeModalSubmit {
		return strings.HasPrefix(payload.Data.CustomID, adminResetModalPrefix) || strings.HasPrefix(payload.Data.CustomID, adminServerConfigUploadPrefix)
	}
	if payload.Type != interactionTypeMessageComponent {
		return false
	}
	switch payload.Data.CustomID {
	case adminMenuCustomID, adminRoleSelectCustomID, adminRoleClearPromptCustomID, adminRoleClearCancelCustomID, adminRepairSelectCustomID, adminPublicCardChannelCustomID, adminResetPrepareCustomID, adminServerConfigCancelID:
		return true
	default:
		return strings.HasPrefix(payload.Data.CustomID, adminRoleClearConfirmCustomID+":") || strings.HasPrefix(payload.Data.CustomID, adminServerConfigUploadPrefix) || strings.HasPrefix(payload.Data.CustomID, adminServerConfigRemovePrefix) || strings.HasPrefix(payload.Data.CustomID, adminServerConfigConfirmPrefix)
	}
}

func (payload interactionPayload) memberIsAdministrator() bool {
	if payload.Member == nil {
		return false
	}
	permissions, ok := new(big.Int).SetString(strings.TrimSpace(payload.Member.Permissions), 10)
	return ok && permissions.Sign() >= 0 && permissions.Bit(3) == 1
}

func (payload interactionPayload) memberCanManageGuild() bool {
	if payload.Member == nil {
		return false
	}
	permissions, ok := new(big.Int).SetString(strings.TrimSpace(payload.Member.Permissions), 10)
	if !ok || permissions.Sign() < 0 {
		return false
	}
	return permissions.Bit(3) == 1 || permissions.Bit(5) == 1
}

func resolvedRolesContain(resolved map[string]json.RawMessage, selected []string) bool {
	for _, roleID := range selected {
		if _, ok := resolved[strings.TrimSpace(roleID)]; !ok {
			return false
		}
	}
	return true
}

func resolvedTextChannelsContain(resolved map[string]json.RawMessage, selected []string) bool {
	for _, channelID := range selected {
		raw, ok := resolved[strings.TrimSpace(channelID)]
		if !ok {
			return false
		}
		var channel struct {
			Type int `json:"type"`
		}
		if json.Unmarshal(raw, &channel) != nil || channel.Type != 0 {
			return false
		}
	}
	return true
}

func (handler *Handler) configureSession(
	ctx context.Context,
	payload interactionPayload,
	options []applicationCommandOption,
	actor domain.Actor,
	correlationID string,
) (string, error) {
	sessionID, err := handler.resolveSessionID(ctx, options, actor, payload.GuildID, false, false)
	if err != nil {
		return "", err
	}
	profile, err := stringOption(options, "profile", false)
	if err != nil {
		return "", newUserError("The game profile must be text.")
	}
	if profile == "" {
		profile = "arma3-default"
	}
	sleepMinutes, err := integerOption(options, "sleep-minutes", defaultSleepMinutes)
	if err != nil || sleepMinutes < 10 || sleepMinutes > 1440 {
		return "", newUserError("Sleep time must be between 10 and 1440 minutes.")
	}
	archiveDays, err := integerOption(options, "archive-days", defaultArchiveDays)
	if err != nil || archiveDays < 1 || archiveDays > 90 {
		return "", newUserError("Archive time must be between 1 and 90 days.")
	}
	teamSpeak, err := booleanOption(options, "teamspeak", false)
	if err != nil {
		return "", newUserError("The TeamSpeak option must be true or false.")
	}
	vanilla, err := booleanOption(options, "vanilla", false)
	if err != nil {
		return "", newUserError("The vanilla option must be true or false.")
	}

	session, err := handler.service.Configure(ctx, appsession.ConfigureCommand{
		Actor:               actor,
		SessionID:           sessionID,
		GuildID:             strings.TrimSpace(payload.GuildID),
		CorrelationID:       correlationID,
		IdempotencyKey:      "discord:" + strings.TrimSpace(payload.ID),
		GameProfileID:       profile,
		SleepAfterSeconds:   sleepMinutes * 60,
		ArchiveAfterSeconds: archiveDays * 86400,
		TeamSpeakEnabled:    teamSpeak,
		Vanilla:             vanilla,
	})
	if err != nil {
		return "", fmt.Errorf("configure session: %w", err)
	}
	return formatConfiguredSession(session), nil
}

func (handler *Handler) requestArtifactIngest(
	ctx context.Context,
	payload interactionPayload,
	options []applicationCommandOption,
	actor domain.Actor,
	correlationID string,
	kind domain.ArtifactKind,
) (string, error) {
	sessionID, err := handler.resolveSessionID(ctx, options, actor, payload.GuildID, false, false)
	if err != nil {
		return "", err
	}
	attachment, err := attachmentOption(payload.Data, options, "file")
	if err != nil {
		return "", newUserError("A valid Discord attachment is required.")
	}
	request := domain.ArtifactIngestRequest{
		SchemaVersion:  1,
		SessionID:      sessionID,
		Kind:           kind,
		AttachmentID:   attachment.ID,
		Filename:       attachment.Filename,
		ContentType:    attachment.ContentType,
		SizeBytes:      attachment.Size,
		SourceURL:      attachment.URL,
		ActorID:        actor.ID,
		GuildID:        strings.TrimSpace(payload.GuildID),
		ChannelID:      strings.TrimSpace(payload.ChannelID),
		CorrelationID:  correlationID,
		IdempotencyKey: "discord:" + strings.TrimSpace(payload.ID),
		RequestedAt:    handler.clock.Now().UTC(),
	}
	if err := request.Validate(); err != nil {
		return "", newUserError(err.Error())
	}
	if err := handler.service.RequestArtifactIngest(ctx, actor, request); err != nil {
		return "", fmt.Errorf("request artifact ingestion: %w", err)
	}
	return formatArtifactAccepted(kind, attachment.Filename), nil
}

func (handler *Handler) listSessions(
	ctx context.Context,
	options []applicationCommandOption,
	actor domain.Actor,
	guildID string,
) (string, error) {
	filter, err := stringOption(options, "state", false)
	if err != nil {
		return "", newUserError("The lifecycle filter is invalid.")
	}
	states, filterLabel, err := sessionListStates(filter)
	if err != nil {
		return "", err
	}
	page, err := integerOption(options, "page", 1)
	if err != nil || page < 1 || page > 20 {
		return "", newUserError("Page must be between 1 and 20.")
	}
	sessions, err := handler.service.List(
		ctx,
		appsession.ListQuery{Actor: actor, Limit: 100, States: states},
	)
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}

	guildID = strings.TrimSpace(guildID)
	visible := make([]domain.Session, 0, len(sessions))
	for _, session := range sessions {
		if session.GuildID != guildID {
			continue
		}
		visible = append(visible, session)
	}
	const pageSize = 5
	totalPages := (len(visible) + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if int(page) > totalPages {
		return "", newUserError(fmt.Sprintf("Page %d is not available. This list has %d page(s).", page, totalPages))
	}
	start := (int(page) - 1) * pageSize
	end := start + pageSize
	if end > len(visible) {
		end = len(visible)
	}
	return formatSessionListPage(visible[start:end], int(page), totalPages, filterLabel), nil
}

func sessionListStates(filter string) ([]domain.LifecycleState, string, error) {
	switch strings.ToLower(strings.TrimSpace(filter)) {
	case "":
		return nil, "Active sessions", nil
	case "setting-up":
		return []domain.LifecycleState{domain.StateDraft, domain.StateNew, domain.StateValidating, domain.StateProvisioning, domain.StateBootstrapping, domain.StateInstalling}, "Setting up", nil
	case "ready":
		return []domain.LifecycleState{domain.StateReady}, "Ready", nil
	case "starting":
		return []domain.LifecycleState{domain.StateWaking, domain.StateRestoring}, "Starting", nil
	case "running":
		return []domain.LifecycleState{domain.StateRunning, domain.StateIdle}, "Running", nil
	case "sleeping":
		return []domain.LifecycleState{domain.StateStopping, domain.StateSleeping, domain.StateWarning1, domain.StateWarning2, domain.StateArchiving, domain.StateDestroying}, "Sleeping", nil
	case "archived":
		return []domain.LifecycleState{domain.StateArchived}, "Archived", nil
	case "action-required":
		return []domain.LifecycleState{domain.StateFailed}, "Action required", nil
	case "terminated":
		return []domain.LifecycleState{domain.StateDeleting, domain.StateDeleted}, "Terminated", nil
	case "deleted":
		return []domain.LifecycleState{domain.StateDeleted}, "Terminated records", nil
	default:
		return nil, "", newUserError("Choose a supported lifecycle filter.")
	}
}

func (handler *Handler) sessionStatus(
	ctx context.Context,
	options []applicationCommandOption,
	actor domain.Actor,
	guildID string,
) (string, error) {
	sessionID, err := handler.resolveSessionID(ctx, options, actor, guildID, false, true)
	if err != nil {
		return "", err
	}

	session, err := handler.service.Get(
		ctx,
		appsession.GetQuery{Actor: actor, SessionID: sessionID, GuildID: guildID, AllowGuildMember: true},
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

	return handler.renderDetailedSession(ctx, session), nil
}

func (handler *Handler) resolveSessionID(
	ctx context.Context,
	options []applicationCommandOption,
	actor domain.Actor,
	guildID string,
	canManageGuild bool,
	allowGuildMember bool,
) (string, error) {
	reference, err := stringOption(options, "session", true)
	if err != nil {
		// Accept the previous option name only for in-flight payloads while the
		// registered command definition is replaced.
		reference, err = stringOption(options, "session-id", true)
	}
	if err != nil {
		return "", newUserError("Select a session or enter its exact slug.")
	}
	selection, err := handler.service.Resolve(ctx, appsession.ResolveQuery{
		Actor: actor, GuildID: guildID, Reference: reference,
		CanManageGuild: canManageGuild, AllowGuildMember: allowGuildMember,
	})
	if err != nil {
		return "", err
	}
	return selection.ID, nil
}

func (handler *Handler) commandErrorMessage(err error, correlationID string) string {
	var userErr userError
	if errors.As(err, &userErr) {
		return userErr.message
	}
	var active domain.OperationInProgressError
	if errors.As(err, &active) {
		return activeOperationMessage(active)
	}

	switch {
	case errors.Is(err, domain.ErrNotFound):
		return "Session not found. Use `/rb list` to see your sessions."
	case errors.Is(err, domain.ErrForbidden):
		return "You do not have access to that session."
	case errors.Is(err, domain.ErrIdempotencyConflict):
		return "Discord reused this interaction ID for different command data. Please run the command again."
	case errors.Is(err, domain.ErrFeatureDisabled):
		return "Infrastructure provisioning is not enabled in this environment yet."
	case errors.Is(err, domain.ErrQuotaExceeded):
		return "Session capacity reached. Archive or terminate the currently provisioned session before starting or waking another one."
	case errors.Is(err, domain.ErrConfirmationRequired):
		return "Create a durable confirmation with `/rb archive` or `/rb terminate` before requesting that action."
	default:
		return fmt.Sprintf(
			"The command failed. Reference: `%s`\nState may be unchanged or partially complete. No automatic retry is scheduled. Check `/rb status` before trying again. Provisioned resources may remain and incur cost.",
			sanitizeInline(correlationID),
		)
	}
}

func (handler *Handler) adminErrorMessage(err error, correlationID string) string {
	var userErr userError
	if errors.As(err, &userErr) {
		return userErr.message
	}
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrInvalidTransition) || errors.Is(err, domain.ErrConfirmationStateDrift) {
		return "This administration control is stale because the underlying state changed. Reopen `/rb admin` and try again."
	}
	switch {
	case errors.Is(err, domain.ErrConfirmationMismatch):
		return "The reset phrase, server, or requesting Administrator did not match. Reopen `/rb admin` and prepare a new reset."
	case errors.Is(err, domain.ErrConfirmationExpired):
		return "The reset confirmation expired. Reopen `/rb admin` and prepare a new reset."
	case errors.Is(err, domain.ErrConfirmationConsumed):
		return "That reset confirmation was already used. Reopen `/rb admin` to see the current result."
	case errors.Is(err, domain.ErrCommandInProgress):
		return "A platform reset is already in progress. Reopen `/rb admin` to see its current stage."
	case errors.Is(err, domain.ErrFeatureDisabled):
		return "Platform reset is disabled in this deployment."
	case errors.Is(err, domain.ErrConfirmationDispatchUncertain):
		return fmt.Sprintf("The reset was confirmed, but queue delivery could not be verified. Reference: `%s`\nDo not prepare another reset. Check the reset operation and worker logs; no automatic retry is scheduled.", sanitizeInline(correlationID))
	}
	return handler.commandErrorMessage(err, correlationID)
}

func activeOperationMessage(active domain.OperationInProgressError) string {
	operation := map[string]string{
		"ProvisionSession": "Starting server", domain.BootstrapWorkflowType: "Setting up game and content",
		domain.SleepWorkflowType: "Putting server to sleep", domain.WakeWorkflowType: "Waking server",
		domain.ArchiveWorkflowType: "Archiving server", domain.RestoreWorkflowType: "Restoring server",
		domain.TerminationWorkflowType: "Terminating server",
	}[active.WorkflowType]
	if operation == "" {
		operation = "Session operation"
	}
	progress := sessioncard.ProgressStageLabel(active.Milestone)
	if progress == "" {
		progress = "Request accepted"
	}
	message := fmt.Sprintf("**%s is already in progress**\nCurrent stage: %s.", operation, progress)
	if step, total, ok := sessioncard.ProgressStep(active.WorkflowType, active.Milestone); ok {
		message = fmt.Sprintf("**%s is already in progress**\nStep %d/%d — %s.", operation, step, total, progress)
	}
	if !active.StartedAt.IsZero() {
		message += fmt.Sprintf(" Started <t:%d:R>.", active.StartedAt.UTC().Unix())
	}
	return message + " No second operation was queued. Use `/rb status` for the latest details."
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
	writeInteractionMessageWithComponents(writer, content, nil)
}

func writeInteractionMessageWithComponents(writer http.ResponseWriter, content string, components []interactionComponent) {
	data := renderer.messageData(content, messageFlagEphemeral, nil)
	if len(components) != 0 {
		data.Components = &components
	}
	writeJSON(
		writer,
		http.StatusOK,
		interactionResponse{
			Type: interactionResponseChannelMessageWithSource,
			Data: data,
		},
	)
}

func writeAutocompleteChoices(writer http.ResponseWriter, choices []applicationCommandChoice) {
	if choices == nil {
		choices = []applicationCommandChoice{}
	}
	writeJSON(
		writer,
		http.StatusOK,
		interactionResponse{
			Type: interactionResponseAutocompleteResult,
			Data: &interactionResponseData{Choices: &choices},
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
