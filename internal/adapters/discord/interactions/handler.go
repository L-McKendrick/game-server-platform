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

	RequestStart(ctx context.Context, command appsession.StartCommand) error
	RequestLifecycle(ctx context.Context, command appsession.LifecycleCommand) error
}

type AccessService interface {
	Authorize(ctx context.Context, guildID string, channelID string, userID string, roles []string) error
	Configure(ctx context.Context, guildID string, userID string, canManageGuild bool, roleIDs []string, channelIDs []string) (domain.GuildAccessPolicy, error)
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
}

// Handler verifies and routes Discord HTTP interactions.
type Handler struct {
	service         SessionService
	access          AccessService
	ids             IDGenerator
	clock           Clock
	logger          *slog.Logger
	playerQuery     ports.PlayerQuery
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

	if _, allowed := handler.allowedGuildIDs[strings.TrimSpace(payload.GuildID)]; !allowed {
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
	if payload.isAdminCommand() || payload.isAdminRoleSelection() {
		if !payload.memberCanManageGuild() {
			handler.logger.Warn("rejected Discord administration without Manage Server permission", slog.String("guild_id", payload.GuildID))
			if payload.Type == interactionTypeApplicationCommandAutocomplete {
				writeAutocompleteChoices(writer, nil)
			} else {
				writeInteractionMessage(writer, "Only members with Administrator or Manage Server permission can use `/admin` actions.")
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
			writeInteractionMessage(writer, handler.commandErrorMessage(err, correlationID))
			return
		}
		handler.logger.Info("Discord administration completed", slog.String("correlation_id", correlationID), slog.String("guild_id", payload.GuildID))
		return
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
	if payload.Type == interactionTypeModalSubmit && (payload.Data == nil || (payload.Data.CustomID != createModalCustomID && !isSetupModalCustomID(payload.Data.CustomID))) {
		writeInteractionMessage(writer, "This modal is not supported or has expired.")
		return
	}
	if payload.isRBCreateCommand() {
		if message := payload.channelCapabilities().setupBlockedMessage(false); message != "" {
			writeInteractionMessage(writer, message)
			return
		}
		writeCreateModal(writer)
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

	correlationID, err := handler.ids.New(handler.clock.Now().UTC())
	if err != nil {
		handler.logger.Error("failed to generate Discord correlation ID", slog.Any("error", err))
		writeInteractionMessage(writer, "The command could not be processed. Please try again.")
		return
	}
	if payload.Type == interactionTypeModalSubmit {
		edit := isSetupModalCustomID(payload.Data.CustomID)
		if message := payload.channelCapabilities().setupBlockedMessage(edit); message != "" {
			writeInteractionMessage(writer, message)
			return
		}
		actor := domain.Actor{
			Type: domain.ActorTypeDiscordUser,
			ID:   actorID,
		}
		var content string
		if isSetupModalCustomID(payload.Data.CustomID) {
			content, err = handler.submitSetupModal(request.Context(), payload, actor, correlationID)
		} else {
			content, err = handler.submitCreateModal(request.Context(), payload, actor, correlationID)
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
		writeInteractionMessage(writer, content)
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
	case "archive":
		confirmed, err := booleanOption(subcommand.Options, "confirm", false)
		if err != nil || !confirmed {
			return "", commandName, newUserError("Archiving stops the game services and removes the current EC2 instance and EBS volumes after the portable backup is verified. A later restore creates billable replacement resources. Set `confirm` to true to continue.")
		}
		content, err := handler.requestLifecycle(ctx, payload, subcommand.Options, actor, correlationID, subcommand.Name)
		return content, commandName, err
	case "terminate":
		confirmed, err := booleanOption(subcommand.Options, "confirm", false)
		if err != nil || !confirmed {
			return "", commandName, newUserError("Termination is immediate and irreversible. It permanently deletes the session's tagged EC2/EBS infrastructure and every stored artifact/archive version without creating a backup. Set `confirm` to true to continue.")
		}
		content, err := handler.requestLifecycle(ctx, payload, subcommand.Options, actor, correlationID, subcommand.Name)
		return content, commandName, err
	default:
		return "", commandName, newUserError(
			"That `/rb` subcommand is not supported yet.",
		)
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
		subcommand, err := payload.namedSubcommand("admin")
		if err != nil {
			return newUserError("Use a supported `/admin` action.")
		}
		if subcommand.Name == "repair-card" {
			content, repairErr := handler.repairSessionCard(ctx, payload, subcommand.Options, actorID, correlationID)
			if repairErr != nil {
				return repairErr
			}
			writeInteractionMessage(writer, content)
			return nil
		}
		if subcommand.Name != "access" {
			return newUserError("Use `/admin access` or `/admin repair-card`.")
		}
		minimum, maximum := 1, 25
		components := []interactionComponent{{
			Type: componentTypeActionRow,
			Components: []interactionComponent{{
				Type: componentTypeRoleSelect, CustomID: adminRoleSelectCustomID,
				Placeholder: "Select allowed roles", MinValues: &minimum, MaxValues: &maximum,
			}},
		}}
		writeJSON(writer, http.StatusOK, interactionResponse{
			Type: interactionResponseChannelMessageWithSource,
			Data: renderer.messageData(
				"Choose the Discord roles that may use game-server platform commands.",
				messageFlagEphemeral,
				&components,
			),
		})
		return nil
	}
	if payload.Data == nil || len(payload.Data.Values) == 0 {
		return newUserError("Select at least one role.")
	}
	policy, err := handler.access.Configure(ctx, payload.GuildID, actorID, true, payload.Data.Values, nil)
	if err != nil {
		return fmt.Errorf("configure guild access: %w", err)
	}
	mentions := make([]string, 0, len(policy.AllowedRoleIDs))
	for _, roleID := range policy.AllowedRoleIDs {
		mentions = append(mentions, "<@&"+roleID+">")
	}
	emptyComponents := []interactionComponent{}
	writeJSON(writer, http.StatusOK, interactionResponse{
		Type: interactionResponseUpdateMessage,
		Data: renderer.messageData(
			fmt.Sprintf("**Access settings updated**\nRevision: `%d`\nAllowed roles: %s", policy.Version, strings.Join(mentions, ", ")),
			0,
			&emptyComponents,
		),
	})
	return nil
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
	content := sessioncard.RenderPublic(sessioncard.Project(session, sessioncard.Options{Now: session.UpdatedAt.UTC()}))
	if err := handler.service.RequestSessionCard(ctx, appsession.SessionCardCommand{
		Actor: actor, SessionID: session.ID, GuildID: payload.GuildID, ChannelID: session.ChannelID,
		CorrelationID: correlationID, NotificationID: "card-admin-repair-" + strings.TrimSpace(payload.ID),
		Content: content, CardRevision: session.Version, AllowGuildMember: true, RequireCurrentRevision: true,
	}); err != nil {
		return "", fmt.Errorf("request session card repair: %w", err)
	}
	return "**Session card repair queued**\nThe bot will refresh the current card or recreate it in its original channel if it was deleted.", nil
}

func (payload interactionPayload) isAdminCommand() bool {
	return (payload.Type == interactionTypeApplicationCommand || payload.Type == interactionTypeApplicationCommandAutocomplete) &&
		payload.Data != nil && strings.TrimSpace(payload.Data.Name) == "admin"
}

func (payload interactionPayload) isAdminRoleSelection() bool {
	return payload.Type == interactionTypeMessageComponent && payload.Data != nil &&
		payload.Data.ComponentType == componentTypeRoleSelect && payload.Data.CustomID == adminRoleSelectCustomID
}

func (payload interactionPayload) memberCanManageGuild() bool {
	if payload.Member == nil {
		return false
	}
	permissions, err := strconv.ParseUint(strings.TrimSpace(payload.Member.Permissions), 10, 64)
	if err != nil {
		return false
	}
	return permissions&administratorPermission != 0 || permissions&manageGuildPermission != 0
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
		return "The environment has reached its provisioned-session limit. Try again after another session is removed."
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
			Data: renderer.messageData(content, messageFlagEphemeral, nil),
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
