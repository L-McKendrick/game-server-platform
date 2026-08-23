package sessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	appreliability "github.com/L-McKendrick/game-server-platform/internal/app/reliability"
	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

// Clock supplies the current time.
//
// It is an interface so unit tests can use a fixed timestamp.
type Clock interface {
	Now() time.Time
}

// IDGenerator creates platform identifiers.
type IDGenerator interface {
	New(now time.Time) (string, error)
}

// SystemClock supplies the current UTC time.
type SystemClock struct{}

// Now returns the current time in UTC.
func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}

// Service implements session application commands and queries.
type Service struct {
	repository           ports.SessionRepository
	artifactQueue        ports.ArtifactQueue
	commandQueue         ports.CommandQueue
	confirmations        ports.ConfirmationRepository
	serverConfigs        ports.GuildServerConfigRepository
	notificationQueue    ports.NotificationQueue
	reliability          *appreliability.Service
	ids                  IDGenerator
	clock                Clock
	idempotencyRetention time.Duration
}

// Option configures optional application-service capabilities.
type Option func(*Service)

// WithArtifactQueue enables asynchronous Discord attachment ingestion.
func WithArtifactQueue(queue ports.ArtifactQueue) Option {
	return func(service *Service) { service.artifactQueue = queue }
}

// WithCommandQueue enables asynchronous lifecycle command dispatch.
func WithCommandQueue(queue ports.CommandQueue) Option {
	return func(service *Service) { service.commandQueue = queue }
}

// WithConfirmationRepository enables durable destructive-action confirmation.
func WithConfirmationRepository(repository ports.ConfirmationRepository) Option {
	return func(service *Service) { service.confirmations = repository }
}

func WithServerConfigRepository(repository ports.GuildServerConfigRepository) Option {
	return func(service *Service) { service.serverConfigs = repository }
}

// WithNotificationQueue enables durable public session-card delivery.
func WithNotificationQueue(queue ports.NotificationQueue) Option {
	return func(service *Service) { service.notificationQueue = queue }
}

func WithReliabilityService(reliability *appreliability.Service) Option {
	return func(service *Service) { service.reliability = reliability }
}

// NewService creates a session application service.
func NewService(
	repository ports.SessionRepository,
	ids IDGenerator,
	clock Clock,
	idempotencyRetention time.Duration,
	options ...Option,
) (*Service, error) {
	switch {
	case repository == nil:
		return nil, fmt.Errorf("session repository is required")
	case ids == nil:
		return nil, fmt.Errorf("ID generator is required")
	case clock == nil:
		return nil, fmt.Errorf("clock is required")
	case idempotencyRetention <= 0:
		return nil, fmt.Errorf("idempotency retention must be positive")
	default:
		service := &Service{
			repository:           repository,
			ids:                  ids,
			clock:                clock,
			idempotencyRetention: idempotencyRetention,
		}
		for _, option := range options {
			if option != nil {
				option(service)
			}
		}
		return service, nil
	}
}

// StartCommand contains the signed Discord context used to request
// infrastructure provisioning.
type StartCommand struct {
	Actor          domain.Actor
	Roles          []string
	SessionID      string
	GuildID        string
	ChannelID      string
	CommandID      string
	CorrelationID  string
	IdempotencyKey string
}

// LifecycleCommand requests sleep, wake, or restore. Archive and termination
// must enter through the durable confirmation service.
type LifecycleCommand struct {
	Actor                                                                   domain.Actor
	Roles                                                                   []string
	SessionID, GuildID, ChannelID, CommandID, CorrelationID, IdempotencyKey string
	CommandType                                                             string
	CanManageGuild                                                          bool
}

type ConfirmationRequest struct {
	Actor     domain.Actor
	SessionID string
	GuildID   string
	RequestID string
	Action    domain.ConfirmationAction
}

type ConfirmCommand struct {
	Actor          domain.Actor
	Roles          []string
	GuildID        string
	ChannelID      string
	CommandID      string
	CorrelationID  string
	IdempotencyKey string
}

type WorkflowCancellationCommand struct {
	Actor                             domain.Actor
	SessionID, GuildID, CorrelationID string
}

func (service *Service) RequestWorkflowCancellation(ctx context.Context, command WorkflowCancellationCommand) (domain.Workflow, error) {
	if service.reliability == nil {
		return domain.Workflow{}, fmt.Errorf("workflow cancellation is not configured")
	}
	session, err := service.repository.Get(ctx, strings.TrimSpace(command.SessionID))
	if err != nil {
		return domain.Workflow{}, err
	}
	if command.Actor.Type != domain.ActorTypeDiscordUser || session.OwnerDiscordUserID != command.Actor.ID || session.GuildID != strings.TrimSpace(command.GuildID) {
		return domain.Workflow{}, domain.ErrForbidden
	}
	if session.ActiveWorkflowID == "" {
		return domain.Workflow{}, fmt.Errorf("%w: session has no active workflow", domain.ErrInvalidTransition)
	}
	return service.reliability.RequestCancellation(ctx, appreliability.CancellationCommand{
		SessionID: session.ID, WorkflowID: session.ActiveWorkflowID, RequestedBy: command.Actor.ID, CorrelationID: strings.TrimSpace(command.CorrelationID),
	})
}

type CancelConfirmationCommand struct {
	Actor   domain.Actor
	GuildID string
}

// SessionCardCommand requests an idempotent create-or-edit of the one public
// card associated with a session.
type SessionCardCommand struct {
	Actor                                                                 domain.Actor
	SessionID, GuildID, ChannelID, CorrelationID, NotificationID, Content string
	Embed                                                                 *domain.NotificationEmbed
	CardRevision                                                          int64
	AllowGuildMember, RequireCurrentRevision                              bool
}

// ActiveModlistQuery authorizes a read of safe delivery metadata at a specific
// card revision.
type ActiveModlistQuery struct {
	Actor            domain.Actor
	SessionID        string
	GuildID          string
	ExpectedRevision int64
}

type ActiveModlist struct {
	ChannelID string
	MessageID string
	Filename  string
}

type PrepareCreationArtifactsCommand struct {
	Actor                                             domain.Actor
	SessionID, GuildID, CorrelationID, IdempotencyKey string
	HasPreset                                         bool
}

// PrepareCreationArtifacts durably records queued inputs before asynchronous
// workers can advance session readiness.
func (service *Service) PrepareCreationArtifacts(ctx context.Context, command PrepareCreationArtifactsCommand) (domain.Session, error) {
	if err := command.Actor.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("validate actor: %w", err)
	}
	key := strings.TrimSpace(command.IdempotencyKey)
	if key == "" {
		return domain.Session{}, fmt.Errorf("idempotency key is required")
	}
	hash, err := hashRequest(struct {
		CommandType string `json:"command_type"`
		ActorID     string `json:"actor_id"`
		SessionID   string `json:"session_id"`
		HasPreset   bool   `json:"has_preset"`
	}{"PrepareCreationArtifacts", command.Actor.ID, strings.TrimSpace(command.SessionID), command.HasPreset})
	if err != nil {
		return domain.Session{}, fmt.Errorf("hash artifact preparation: %w", err)
	}
	if replayed, found, err := service.replaySession(ctx, key, hash, command.Actor); err != nil || found {
		return replayed, err
	}
	session, err := service.repository.Get(ctx, strings.TrimSpace(command.SessionID))
	if err != nil {
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}
	if err := authorizeOwner(command.Actor, session); err != nil {
		return domain.Session{}, err
	}
	if session.GuildID != strings.TrimSpace(command.GuildID) {
		return domain.Session{}, domain.ErrForbidden
	}
	now, expectedVersion := service.clock.Now().UTC(), session.Version
	if err := session.PrepareCreationArtifacts(command.HasPreset, now); err != nil {
		return domain.Session{}, err
	}
	eventID, err := service.newID(now, "artifact preparation event")
	if err != nil {
		return domain.Session{}, err
	}
	correlationID, err := service.resolveCorrelationID(command.CorrelationID, now)
	if err != nil {
		return domain.Session{}, err
	}
	event := domain.SessionEvent{
		ID: eventID, SessionID: session.ID, Type: domain.EventArtifactRequested, OccurredAt: now,
		ActorType: string(command.Actor.Type), ActorID: command.Actor.ID, CorrelationID: correlationID,
		Data: map[string]string{"mission": "pending", "preset": strconv.FormatBool(command.HasPreset)},
	}
	record, err := domain.NewCompletedIdempotencyRecord(key, hash, session.ID, now, service.idempotencyRetention)
	if err != nil {
		return domain.Session{}, err
	}
	if err := service.repository.SaveWithEvent(ctx, session, expectedVersion, event, record); err != nil {
		if replayed, found, replayErr := service.replaySession(ctx, key, hash, command.Actor); replayErr != nil || found {
			return replayed, replayErr
		}
		return domain.Session{}, fmt.Errorf("persist artifact preparation: %w", err)
	}
	return session, nil
}

func (service *Service) RequestSessionCard(ctx context.Context, command SessionCardCommand) error {
	if err := command.Actor.Validate(); err != nil {
		return fmt.Errorf("validate actor: %w", err)
	}
	if service.notificationQueue == nil {
		return fmt.Errorf("session card delivery is not configured")
	}
	session, err := service.repository.Get(ctx, strings.TrimSpace(command.SessionID))
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if command.AllowGuildMember {
		if session.GuildID != strings.TrimSpace(command.GuildID) {
			return domain.ErrNotFound
		}
	} else if err := authorizeOwner(command.Actor, session); err != nil {
		return err
	}
	if session.GuildID != strings.TrimSpace(command.GuildID) || session.ChannelID != strings.TrimSpace(command.ChannelID) {
		return fmt.Errorf("session card destination does not match session: %w", domain.ErrForbidden)
	}
	if command.CardRevision < 1 || command.CardRevision > session.Version {
		return fmt.Errorf("session card revision must reference a persisted session version")
	}
	if command.RequireCurrentRevision && command.CardRevision != session.Version {
		return fmt.Errorf("session card revision is stale: %w", domain.ErrConflict)
	}
	return service.notificationQueue.Enqueue(ctx, domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: strings.TrimSpace(command.NotificationID),
		SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID,
		Content: command.Content, Embed: command.Embed, Kind: domain.NotificationSessionCard, CardRevision: command.CardRevision,
		CorrelationID: strings.TrimSpace(command.CorrelationID), RequestedAt: service.clock.Now().UTC(),
	})
}

// GetActiveModlist returns only the safe Discord delivery reference after
// guild-read authorization and an exact persisted revision check.
func (service *Service) GetActiveModlist(ctx context.Context, query ActiveModlistQuery) (ActiveModlist, error) {
	session, err := service.Get(ctx, GetQuery{
		Actor: query.Actor, SessionID: query.SessionID, GuildID: query.GuildID, AllowGuildMember: true,
	})
	if err != nil {
		return ActiveModlist{}, err
	}
	if query.ExpectedRevision < 1 || query.ExpectedRevision != session.Version {
		return ActiveModlist{}, fmt.Errorf("session card revision is stale: %w", domain.ErrConflict)
	}
	if session.Vanilla || session.PresetArtifactStatus != domain.ArtifactAccepted {
		return ActiveModlist{}, domain.ErrNotFound
	}
	repository, ok := service.repository.(ports.SessionCardRepository)
	if !ok {
		return ActiveModlist{}, fmt.Errorf("session modlist delivery metadata is not configured")
	}
	reference, err := repository.GetModlistReference(ctx, session.ID)
	if err != nil {
		return ActiveModlist{}, err
	}
	if err := reference.Validate(); err != nil {
		return ActiveModlist{}, fmt.Errorf("validate active modlist reference: %w", err)
	}
	if !sessioncard.IsActiveModlistReference(session, reference) {
		return ActiveModlist{}, fmt.Errorf("active modlist reference does not match session")
	}
	return ActiveModlist{ChannelID: reference.ChannelID, MessageID: reference.MessageID, Filename: reference.Filename}, nil
}

func (service *Service) RequestConfirmation(ctx context.Context, command ConfirmationRequest) (domain.Confirmation, error) {
	if err := command.Actor.Validate(); err != nil {
		return domain.Confirmation{}, fmt.Errorf("validate actor: %w", err)
	}
	if service.confirmations == nil || service.commandQueue == nil {
		return domain.Confirmation{}, fmt.Errorf("%w: destructive confirmations", domain.ErrFeatureDisabled)
	}
	session, err := service.repository.Get(ctx, strings.TrimSpace(command.SessionID))
	if err != nil {
		return domain.Confirmation{}, err
	}
	if err := authorizeOwner(command.Actor, session); err != nil {
		return domain.Confirmation{}, err
	}
	if session.GuildID != strings.TrimSpace(command.GuildID) {
		return domain.Confirmation{}, domain.ErrForbidden
	}
	if operation := activeOperation(session, service.clock.Now().UTC()); operation != nil {
		return domain.Confirmation{}, *operation
	}
	switch command.Action {
	case domain.ConfirmationArchive:
		if !session.CanArchive() {
			return domain.Confirmation{}, fmt.Errorf("session cannot archive now: %w", domain.ErrInvalidTransition)
		}
	case domain.ConfirmationTerminate:
		if !session.CanTerminate() {
			return domain.Confirmation{}, fmt.Errorf("session cannot terminate now: %w", domain.ErrInvalidTransition)
		}
	default:
		return domain.Confirmation{}, fmt.Errorf("unsupported confirmation action")
	}
	requestID := strings.TrimSpace(command.RequestID)
	if requestID == "" {
		return domain.Confirmation{}, fmt.Errorf("confirmation request ID is required")
	}
	confirmation, err := domain.NewConfirmation(requestID, domain.PendingConfirmationCode(command.GuildID, command.Actor.ID), session, command.Action, service.clock.Now().UTC())
	if err != nil {
		return domain.Confirmation{}, err
	}
	if err := service.confirmations.CreateConfirmation(ctx, confirmation); err != nil {
		if !errors.Is(err, domain.ErrAlreadyExists) {
			return domain.Confirmation{}, err
		}
		existing, getErr := service.confirmations.GetConfirmation(ctx, confirmation.Code)
		if getErr != nil {
			return domain.Confirmation{}, getErr
		}
		if existing.ID != confirmation.ID {
			if pendingErr := existing.CheckPending(service.clock.Now().UTC()); pendingErr != nil {
				return domain.Confirmation{}, pendingErr
			}
			return domain.Confirmation{}, domain.ErrIdempotencyConflict
		}
		if existing.SessionID != confirmation.SessionID || existing.Action != confirmation.Action || existing.OwnerDiscordUserID != confirmation.OwnerDiscordUserID || existing.GuildID != confirmation.GuildID {
			return domain.Confirmation{}, domain.ErrIdempotencyConflict
		}
		if existing.BoundState != session.LifecycleState || existing.BoundVersion != session.Version {
			return domain.Confirmation{}, domain.ErrConfirmationStateDrift
		}
		if err := existing.CheckPending(service.clock.Now().UTC()); err != nil {
			return domain.Confirmation{}, err
		}
		return existing, nil
	}
	return confirmation, nil
}

func (service *Service) Confirm(ctx context.Context, command ConfirmCommand) (domain.Confirmation, error) {
	if err := command.Actor.Validate(); err != nil {
		return domain.Confirmation{}, fmt.Errorf("validate actor: %w", err)
	}
	if service.confirmations == nil || service.commandQueue == nil {
		return domain.Confirmation{}, fmt.Errorf("%w: destructive confirmations", domain.ErrFeatureDisabled)
	}
	now := service.clock.Now().UTC()
	confirmation, session, err := service.confirmations.ConsumeConfirmation(ctx, domain.PendingConfirmationCode(command.GuildID, command.Actor.ID), command.Actor.ID, command.GuildID, now)
	if err != nil {
		return domain.Confirmation{}, err
	}
	commandType := ""
	switch confirmation.Action {
	case domain.ConfirmationArchive:
		if !session.CanArchive() {
			return domain.Confirmation{}, domain.ErrConfirmationStateDrift
		}
		commandType = domain.CommandArchiveSession
	case domain.ConfirmationTerminate:
		if !session.CanTerminate() {
			return domain.Confirmation{}, domain.ErrConfirmationStateDrift
		}
		commandType = domain.CommandDestroySession
	default:
		return domain.Confirmation{}, domain.ErrConfirmationMismatch
	}
	envelope := domain.CommandEnvelope{
		SchemaVersion: 1, CommandID: strings.TrimSpace(command.CommandID), CommandType: commandType,
		RequestedAt: now, Actor: domain.CommandActor{DiscordUserID: command.Actor.ID, GuildID: strings.TrimSpace(command.GuildID), ChannelID: strings.TrimSpace(command.ChannelID), Roles: append([]string(nil), command.Roles...)},
		SessionID: session.ID, IdempotencyKey: strings.TrimSpace(command.IdempotencyKey), CorrelationID: strings.TrimSpace(command.CorrelationID), Parameters: map[string]string{},
	}
	if err := service.commandQueue.Enqueue(ctx, envelope); err != nil {
		return domain.Confirmation{}, fmt.Errorf("%w: %v", domain.ErrConfirmationDispatchUncertain, err)
	}
	return confirmation, nil
}

func (service *Service) CancelConfirmation(ctx context.Context, command CancelConfirmationCommand) (domain.Confirmation, error) {
	if err := command.Actor.Validate(); err != nil {
		return domain.Confirmation{}, fmt.Errorf("validate actor: %w", err)
	}
	if service.confirmations == nil {
		return domain.Confirmation{}, fmt.Errorf("%w: destructive confirmations", domain.ErrFeatureDisabled)
	}
	return service.confirmations.CancelConfirmation(ctx, domain.PendingConfirmationCode(command.GuildID, command.Actor.ID), command.Actor.ID, command.GuildID, service.clock.Now().UTC())
}

func (service *Service) RequestLifecycle(ctx context.Context, command LifecycleCommand) error {
	if err := command.Actor.Validate(); err != nil {
		return fmt.Errorf("validate actor: %w", err)
	}
	if service.commandQueue == nil {
		return fmt.Errorf("%w: lifecycle commands", domain.ErrFeatureDisabled)
	}
	session, err := service.repository.Get(ctx, strings.TrimSpace(command.SessionID))
	if err != nil {
		return err
	}
	canManageLifecycle := command.CanManageGuild && (command.CommandType == domain.CommandSleepSession || command.CommandType == domain.CommandWakeSession)
	if err := authorizeLifecycleActor(command.Actor, session, canManageLifecycle); err != nil {
		return err
	}
	if session.GuildID != strings.TrimSpace(command.GuildID) {
		return domain.ErrForbidden
	}
	if operation := activeOperation(session, service.clock.Now().UTC()); operation != nil {
		return *operation
	}
	if command.CommandType == domain.CommandArchiveSession || command.CommandType == domain.CommandDestroySession {
		return domain.ErrConfirmationRequired
	}
	if command.CommandType == domain.CommandSleepSession && !session.CanSleep() {
		return fmt.Errorf("session cannot sleep now: %w", domain.ErrInvalidTransition)
	}
	if command.CommandType == domain.CommandWakeSession && !session.CanWake() {
		return fmt.Errorf("session cannot wake now: %w", domain.ErrInvalidTransition)
	}
	if command.CommandType == domain.CommandRestoreSession && !session.CanRestore() {
		return fmt.Errorf("session cannot restore now: %w", domain.ErrInvalidTransition)
	}
	if command.CommandType != domain.CommandSleepSession && command.CommandType != domain.CommandWakeSession && command.CommandType != domain.CommandRestoreSession {
		return fmt.Errorf("unsupported lifecycle command")
	}
	return service.commandQueue.Enqueue(ctx, domain.CommandEnvelope{SchemaVersion: 1, CommandID: strings.TrimSpace(command.CommandID), CommandType: command.CommandType, RequestedAt: service.clock.Now().UTC(), Actor: domain.CommandActor{DiscordUserID: command.Actor.ID, GuildID: strings.TrimSpace(command.GuildID), ChannelID: strings.TrimSpace(command.ChannelID), Roles: append([]string(nil), command.Roles...), CanManageGuild: command.CanManageGuild}, SessionID: session.ID, IdempotencyKey: strings.TrimSpace(command.IdempotencyKey), CorrelationID: strings.TrimSpace(command.CorrelationID), Parameters: map[string]string{}})
}

// RequestStart validates the synchronous boundary and queues a normalized
// command. The command worker revalidates authorization and state before it
// acquires a workflow lock.
func (service *Service) RequestStart(ctx context.Context, command StartCommand) error {
	if err := command.Actor.Validate(); err != nil {
		return fmt.Errorf("validate actor: %w", err)
	}
	if service.commandQueue == nil {
		return fmt.Errorf("%w: lifecycle commands", domain.ErrFeatureDisabled)
	}
	session, err := service.repository.Get(ctx, strings.TrimSpace(command.SessionID))
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if err := authorizeOwner(command.Actor, session); err != nil {
		return err
	}
	if session.GuildID != strings.TrimSpace(command.GuildID) {
		return fmt.Errorf("session belongs to another guild: %w", domain.ErrForbidden)
	}
	if operation := activeOperation(session, service.clock.Now().UTC()); operation != nil {
		return *operation
	}
	commandType := domain.CommandStartSession
	switch {
	case session.CanStartInfrastructureProvisioning():
	case session.CanStartBootstrap():
		commandType = domain.CommandBootstrapServer
	default:
		return fmt.Errorf("session is not ready for provisioning or bootstrap: %w", domain.ErrInvalidTransition)
	}
	parameters := map[string]string{}
	if service.serverConfigs != nil {
		config, configErr := service.serverConfigs.GetGuildServerConfig(ctx, session.GuildID)
		switch {
		case errors.Is(configErr, domain.ErrNotFound), configErr == nil && !config.Active():
			parameters[domain.ServerConfigModeParameter] = domain.ServerConfigModeGenerated
		case configErr != nil:
			return fmt.Errorf("read guild server configuration: %w", configErr)
		default:
			parameters[domain.ServerConfigModeParameter] = domain.ServerConfigModeCustom
			parameters[domain.ServerConfigRevisionParameter] = strconv.FormatInt(config.Revision, 10)
			parameters[domain.ServerConfigObjectParameter] = config.ObjectKey
			parameters[domain.ServerConfigSHAParameter] = config.SHA256
		}
	}
	envelope := domain.CommandEnvelope{
		SchemaVersion: 1,
		CommandID:     strings.TrimSpace(command.CommandID), CommandType: commandType,
		RequestedAt: service.clock.Now().UTC(),
		Actor: domain.CommandActor{
			DiscordUserID: command.Actor.ID, GuildID: strings.TrimSpace(command.GuildID),
			ChannelID: strings.TrimSpace(command.ChannelID), Roles: append([]string(nil), command.Roles...),
		},
		SessionID: session.ID, IdempotencyKey: strings.TrimSpace(command.IdempotencyKey),
		CorrelationID: strings.TrimSpace(command.CorrelationID), Parameters: parameters,
	}
	if err := service.commandQueue.Enqueue(ctx, envelope); err != nil {
		return fmt.Errorf("enqueue start command: %w", err)
	}
	return nil
}

func activeOperation(session domain.Session, now time.Time) *domain.OperationInProgressError {
	if session.ActiveWorkflowID == "" || !session.ActiveWorkflowLeaseExpiresAt.After(now.UTC()) {
		return nil
	}
	return &domain.OperationInProgressError{
		WorkflowType: session.ActiveWorkflowType, Milestone: session.Progress.Milestone,
		StartedAt: session.ActiveWorkflowStartedAt, UpdatedAt: session.Progress.LastProgressAt,
	}
}

// ConfigureCommand replaces the editable configuration of a draft session.
type ConfigureCommand struct {
	Actor               domain.Actor
	SessionID           string
	GuildID             string
	CorrelationID       string
	IdempotencyKey      string
	GameProfileID       string
	SleepAfterSeconds   int64
	ArchiveAfterSeconds int64
	TeamSpeakEnabled    bool
	Vanilla             bool
	CreatorDLCs         []string
	StartWhenReady      bool
}

type UpdateDraftSetupCommand struct {
	Actor                                             domain.Actor
	SessionID, GuildID, CorrelationID, IdempotencyKey string
	DisplayName, Description                          string
	GameProfileID                                     string
	SleepAfterSeconds, ArchiveAfterSeconds            int64
	TeamSpeakEnabled, Vanilla                         bool
	CreatorDLCs                                       []string
	StartWhenReady                                    bool
	ReplaceMission, ReplacePreset                     bool
}

type UpdateModOptionsCommand struct {
	Actor                                             domain.Actor
	SessionID, GuildID, CorrelationID, IdempotencyKey string
	ExpectedVersion                                   int64
	CreatorDLCs                                       []string
	Roles                                             []string
	PreparePreset                                     bool
}

// UpdateModOptions atomically persists a stale-bound desired Creator DLC set.
func (service *Service) UpdateModOptions(ctx context.Context, command UpdateModOptionsCommand) (domain.Session, error) {
	if err := command.Actor.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("validate actor: %w", err)
	}
	creatorDLCs, err := domain.NormalizeCreatorDLCs(command.CreatorDLCs)
	if err != nil {
		return domain.Session{}, err
	}
	key := strings.TrimSpace(command.IdempotencyKey)
	if key == "" || command.ExpectedVersion < 1 {
		return domain.Session{}, fmt.Errorf("mod options idempotency and expected version are required")
	}
	hash, err := hashRequest(struct {
		CommandType, ActorID, SessionID, GuildID string
		ExpectedVersion                          int64
		CreatorDLCs                              []string
		Roles                                    []string
		PreparePreset                            bool
	}{"UpdateModOptions", command.Actor.ID, strings.TrimSpace(command.SessionID), strings.TrimSpace(command.GuildID), command.ExpectedVersion, creatorDLCs, command.Roles, command.PreparePreset})
	if err != nil {
		return domain.Session{}, fmt.Errorf("hash mod options: %w", err)
	}
	if replayed, found, err := service.replaySession(ctx, key, hash, command.Actor); err != nil || found {
		if err == nil {
			err = service.requestAutomaticStart(ctx, replayed, command.CorrelationID, command.Roles)
		}
		return replayed, err
	}
	session, err := service.repository.Get(ctx, strings.TrimSpace(command.SessionID))
	if err != nil {
		return domain.Session{}, fmt.Errorf("get mod options session: %w", err)
	}
	if err := authorizeOwner(command.Actor, session); err != nil || session.GuildID != strings.TrimSpace(command.GuildID) {
		return domain.Session{}, domain.ErrForbidden
	}
	if session.Version != command.ExpectedVersion {
		return domain.Session{}, fmt.Errorf("%w: session changed while mod options were open", domain.ErrConflict)
	}
	now, expectedVersion := service.clock.Now().UTC(), session.Version
	if err := session.UpdateCreatorDLCs(creatorDLCs, command.PreparePreset, now); err != nil {
		return domain.Session{}, err
	}
	eventID, err := service.newID(now, "mod options event")
	if err != nil {
		return domain.Session{}, err
	}
	correlationID, err := service.resolveCorrelationID(command.CorrelationID, now)
	if err != nil {
		return domain.Session{}, err
	}
	event := domain.NewSessionConfiguredEvent(eventID, correlationID, command.Actor, session, now)
	record, err := domain.NewCompletedIdempotencyRecord(key, hash, session.ID, now, service.idempotencyRetention)
	if err != nil {
		return domain.Session{}, err
	}
	if err := service.repository.SaveWithEvent(ctx, session, expectedVersion, event, record); err != nil {
		if replayed, found, replayErr := service.replaySession(ctx, key, hash, command.Actor); replayErr != nil || found {
			if replayErr == nil {
				replayErr = service.requestAutomaticStart(ctx, replayed, command.CorrelationID, command.Roles)
			}
			return replayed, replayErr
		}
		return domain.Session{}, fmt.Errorf("persist mod options: %w", err)
	}
	if err := service.requestAutomaticStart(ctx, session, correlationID, command.Roles); err != nil {
		return session, err
	}
	return session, nil
}

func (service *Service) requestAutomaticStart(ctx context.Context, session domain.Session, correlationID string, roles []string) error {
	if !session.StartWhenReady || !session.CanStartInfrastructureProvisioning() {
		return nil
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", session.ID, session.ConfigurationRevision)))
	commandID := hex.EncodeToString(digest[:16])
	return service.RequestStart(ctx, StartCommand{
		Actor: domain.Actor{Type: domain.ActorTypeDiscordUser, ID: session.OwnerDiscordUserID}, Roles: append([]string(nil), roles...),
		SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID,
		CommandID: commandID, CorrelationID: strings.TrimSpace(correlationID), IdempotencyKey: "auto-start:" + commandID,
	})
}

// UpdateDraftSetup atomically edits the draft fields and marks only selected
// missing or rejected artifacts pending.
func (service *Service) UpdateDraftSetup(ctx context.Context, command UpdateDraftSetupCommand) (domain.Session, error) {
	if err := command.Actor.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("validate actor: %w", err)
	}
	key := strings.TrimSpace(command.IdempotencyKey)
	if key == "" {
		return domain.Session{}, fmt.Errorf("idempotency key is required")
	}
	creatorDLCs, err := domain.NormalizeCreatorDLCs(command.CreatorDLCs)
	if err != nil {
		return domain.Session{}, err
	}
	hash, err := hashRequest(struct {
		CommandType                                                              string `json:"command_type"`
		ActorID, SessionID, GuildID, DisplayName, Description, GameProfileID     string
		SleepAfterSeconds, ArchiveAfterSeconds                                   int64
		TeamSpeakEnabled, Vanilla, StartWhenReady, ReplaceMission, ReplacePreset bool
		CreatorDLCs                                                              []string
	}{
		CommandType: "UpdateDraftSetup", ActorID: command.Actor.ID,
		SessionID: strings.TrimSpace(command.SessionID), GuildID: strings.TrimSpace(command.GuildID),
		DisplayName: strings.TrimSpace(command.DisplayName), Description: strings.TrimSpace(command.Description),
		GameProfileID: strings.TrimSpace(command.GameProfileID), SleepAfterSeconds: command.SleepAfterSeconds,
		ArchiveAfterSeconds: command.ArchiveAfterSeconds, TeamSpeakEnabled: command.TeamSpeakEnabled,
		Vanilla: command.Vanilla, ReplaceMission: command.ReplaceMission, ReplacePreset: command.ReplacePreset,
		CreatorDLCs:    creatorDLCs,
		StartWhenReady: command.StartWhenReady,
	})
	if err != nil {
		return domain.Session{}, fmt.Errorf("hash draft setup: %w", err)
	}
	if replayed, found, err := service.replaySession(ctx, key, hash, command.Actor); err != nil || found {
		return replayed, err
	}
	session, err := service.repository.Get(ctx, strings.TrimSpace(command.SessionID))
	if err != nil {
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}
	if err := authorizeOwner(command.Actor, session); err != nil {
		return domain.Session{}, err
	}
	if session.GuildID != strings.TrimSpace(command.GuildID) {
		return domain.Session{}, domain.ErrForbidden
	}
	now, expectedVersion := service.clock.Now().UTC(), session.Version
	if err := session.ConfigureDraftSetup(command.DisplayName, command.Description, domain.SessionConfiguration{
		GameProfileID: command.GameProfileID, SleepAfterSeconds: command.SleepAfterSeconds,
		ArchiveAfterSeconds: command.ArchiveAfterSeconds, TeamSpeakEnabled: command.TeamSpeakEnabled, Vanilla: command.Vanilla,
		CreatorDLCs:    command.CreatorDLCs,
		StartWhenReady: command.StartWhenReady,
	}, command.ReplaceMission, command.ReplacePreset, now); err != nil {
		return domain.Session{}, err
	}
	eventID, err := service.newID(now, "draft setup event")
	if err != nil {
		return domain.Session{}, err
	}
	correlationID, err := service.resolveCorrelationID(command.CorrelationID, now)
	if err != nil {
		return domain.Session{}, err
	}
	event := domain.NewSessionConfiguredEvent(eventID, correlationID, command.Actor, session, now)
	event.Data["replacement_mission"] = strconv.FormatBool(command.ReplaceMission)
	event.Data["replacement_preset"] = strconv.FormatBool(command.ReplacePreset)
	record, err := domain.NewCompletedIdempotencyRecord(key, hash, session.ID, now, service.idempotencyRetention)
	if err != nil {
		return domain.Session{}, err
	}
	if err := service.repository.SaveWithEvent(ctx, session, expectedVersion, event, record); err != nil {
		if replayed, found, replayErr := service.replaySession(ctx, key, hash, command.Actor); replayErr != nil || found {
			return replayed, replayErr
		}
		return domain.Session{}, fmt.Errorf("persist draft setup: %w", err)
	}
	return session, nil
}

// UpdateDescriptionCommand replaces a session's optional description.
type UpdateDescriptionCommand struct {
	Actor          domain.Actor
	SessionID      string
	GuildID        string
	CorrelationID  string
	IdempotencyKey string
	Description    string
}

// UpdateDescription normalizes and atomically persists a description change
// with its immutable audit event.
func (service *Service) UpdateDescription(ctx context.Context, command UpdateDescriptionCommand) (domain.Session, error) {
	if err := command.Actor.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("validate actor: %w", err)
	}

	idempotencyKey, requestHash, err := updateDescriptionRequestIdentity(command)
	if err != nil {
		return domain.Session{}, err
	}
	if replayed, found, err := service.replaySession(ctx, idempotencyKey, requestHash, command.Actor); err != nil || found {
		return replayed, err
	}

	session, err := service.repository.Get(ctx, strings.TrimSpace(command.SessionID))
	if err != nil {
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}
	if err := authorizeOwner(command.Actor, session); err != nil {
		return domain.Session{}, err
	}
	if session.GuildID != strings.TrimSpace(command.GuildID) {
		return domain.Session{}, fmt.Errorf("session belongs to another guild: %w", domain.ErrForbidden)
	}

	now := service.clock.Now().UTC()
	expectedVersion := session.Version
	previous, err := session.SetDescription(command.Description, now)
	if err != nil {
		return domain.Session{}, fmt.Errorf("update session description: %w", err)
	}

	eventID, err := service.newID(now, "description event")
	if err != nil {
		return domain.Session{}, err
	}
	correlationID, err := service.resolveCorrelationID(command.CorrelationID, now)
	if err != nil {
		return domain.Session{}, err
	}
	event := domain.NewSessionDescriptionChangedEvent(eventID, correlationID, command.Actor, session, previous, now)
	idempotency, err := domain.NewCompletedIdempotencyRecord(idempotencyKey, requestHash, session.ID, now, service.idempotencyRetention)
	if err != nil {
		return domain.Session{}, fmt.Errorf("construct idempotency record: %w", err)
	}
	if err := service.repository.SaveWithEvent(ctx, session, expectedVersion, event, idempotency); err != nil {
		if replayed, found, replayErr := service.replaySession(ctx, idempotencyKey, requestHash, command.Actor); replayErr != nil || found {
			return replayed, replayErr
		}
		return domain.Session{}, fmt.Errorf("persist session description: %w", err)
	}
	return session, nil
}

// Configure validates and persists a new immutable configuration revision.
func (service *Service) Configure(ctx context.Context, command ConfigureCommand) (domain.Session, error) {
	if err := command.Actor.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("validate actor: %w", err)
	}

	idempotencyKey, requestHash, err := configureRequestIdentity(command)
	if err != nil {
		return domain.Session{}, err
	}
	if replayed, found, err := service.replaySession(ctx, idempotencyKey, requestHash, command.Actor); err != nil || found {
		return replayed, err
	}

	session, err := service.repository.Get(ctx, strings.TrimSpace(command.SessionID))
	if err != nil {
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}
	if err := authorizeOwner(command.Actor, session); err != nil {
		return domain.Session{}, err
	}
	if session.GuildID != strings.TrimSpace(command.GuildID) {
		return domain.Session{}, fmt.Errorf("session belongs to another guild: %w", domain.ErrForbidden)
	}

	now := service.clock.Now().UTC()
	expectedVersion := session.Version
	if err := session.Configure(domain.SessionConfiguration{
		GameProfileID:       command.GameProfileID,
		SleepAfterSeconds:   command.SleepAfterSeconds,
		ArchiveAfterSeconds: command.ArchiveAfterSeconds,
		TeamSpeakEnabled:    command.TeamSpeakEnabled,
		Vanilla:             command.Vanilla,
		CreatorDLCs:         command.CreatorDLCs,
		StartWhenReady:      command.StartWhenReady,
	}, now); err != nil {
		return domain.Session{}, fmt.Errorf("configure session: %w", err)
	}

	eventID, err := service.newID(now, "configuration event")
	if err != nil {
		return domain.Session{}, err
	}
	correlationID, err := service.resolveCorrelationID(command.CorrelationID, now)
	if err != nil {
		return domain.Session{}, err
	}
	event := domain.NewSessionConfiguredEvent(eventID, correlationID, command.Actor, session, now)
	idempotency, err := domain.NewCompletedIdempotencyRecord(idempotencyKey, requestHash, session.ID, now, service.idempotencyRetention)
	if err != nil {
		return domain.Session{}, fmt.Errorf("construct idempotency record: %w", err)
	}
	if err := service.repository.SaveWithEvent(ctx, session, expectedVersion, event, idempotency); err != nil {
		if replayed, found, replayErr := service.replaySession(ctx, idempotencyKey, requestHash, command.Actor); replayErr != nil || found {
			return replayed, replayErr
		}
		return domain.Session{}, fmt.Errorf("persist configuration: %w", err)
	}
	return session, nil
}

// RequestArtifactIngest authorizes and queues a Discord attachment for asynchronous ingestion.
func (service *Service) RequestArtifactIngest(ctx context.Context, actor domain.Actor, request domain.ArtifactIngestRequest) error {
	if err := actor.Validate(); err != nil {
		return fmt.Errorf("validate actor: %w", err)
	}
	if service.artifactQueue == nil {
		return fmt.Errorf("artifact ingestion is not configured")
	}
	if request.ActorID != actor.ID {
		return fmt.Errorf("artifact actor does not match command actor: %w", domain.ErrForbidden)
	}
	if err := request.Validate(); err != nil {
		return err
	}
	session, err := service.repository.Get(ctx, strings.TrimSpace(request.SessionID))
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if err := authorizeOwner(actor, session); err != nil {
		return err
	}
	if session.GuildID != request.GuildID {
		return fmt.Errorf("session belongs to another guild: %w", domain.ErrForbidden)
	}
	if session.ChannelID != request.ChannelID {
		return fmt.Errorf("artifact destination does not match session channel: %w", domain.ErrForbidden)
	}
	if request.IsPresetRevision() {
		if err := session.ValidatePresetRevisionStaging(request.ExpectedActivePresetRevision); err != nil {
			return err
		}
	} else if session.LifecycleState != domain.StateDraft {
		return fmt.Errorf("attachments are only accepted while a session is DRAFT: %w", domain.ErrInvalidTransition)
	}
	if err := service.artifactQueue.Enqueue(ctx, request); err != nil {
		return fmt.Errorf("enqueue artifact ingestion: %w", err)
	}
	return nil
}

// CreateCommand contains the user-controlled values needed to create a session.
type CreateCommand struct {
	Actor          domain.Actor
	CorrelationID  string
	IdempotencyKey string

	Slug        string
	DisplayName string
	Description string
	GameType    string
	GuildID     string
	ChannelID   string
}

const maximumSlugCollisionAttempts = 1000

// Create creates a draft session and its initial event.
func (service *Service) Create(
	ctx context.Context,
	command CreateCommand,
) (domain.Session, error) {
	if err := command.Actor.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("validate actor: %w", err)
	}

	idempotencyKey, requestHash, err := createRequestIdentity(command)
	if err != nil {
		return domain.Session{}, err
	}

	if replayed, found, err := service.replaySession(
		ctx,
		idempotencyKey,
		requestHash,
		command.Actor,
	); err != nil || found {
		return replayed, err
	}

	now := service.clock.Now().UTC()

	sessionID, err := service.newID(now, "session")
	if err != nil {
		return domain.Session{}, err
	}

	eventID, err := service.newID(now, "creation event")
	if err != nil {
		return domain.Session{}, err
	}

	correlationID, err := service.resolveCorrelationID(
		command.CorrelationID,
		now,
	)
	if err != nil {
		return domain.Session{}, err
	}

	explicitSlug := strings.TrimSpace(command.Slug)
	generatedSlug := explicitSlug == ""
	baseSlug := explicitSlug
	if generatedSlug {
		baseSlug = domain.GenerateSessionSlug(command.DisplayName)
	}

	session, err := domain.NewSession(
		domain.NewSessionInput{
			ID:                 sessionID,
			Slug:               baseSlug,
			DisplayName:        command.DisplayName,
			Description:        command.Description,
			GameType:           command.GameType,
			OwnerDiscordUserID: command.Actor.ID,
			GuildID:            command.GuildID,
			ChannelID:          command.ChannelID,
		},
		now,
	)
	if err != nil {
		return domain.Session{}, fmt.Errorf(
			"construct session: %w",
			err,
		)
	}

	idempotency, err := domain.NewCompletedIdempotencyRecord(
		idempotencyKey,
		requestHash,
		session.ID,
		now,
		service.idempotencyRetention,
	)
	if err != nil {
		return domain.Session{}, fmt.Errorf(
			"construct idempotency record: %w",
			err,
		)
	}

	for attempt := 1; attempt <= maximumSlugCollisionAttempts; attempt++ {
		session.Slug = slugCollisionCandidate(baseSlug, attempt)
		event := domain.NewSessionCreatedEvent(eventID, correlationID, command.Actor, session, now)
		if err := event.Validate(); err != nil {
			return domain.Session{}, fmt.Errorf("construct creation event: %w", err)
		}

		err := service.repository.Create(ctx, session, event, idempotency)
		if err == nil {
			return session, nil
		}
		if replayed, found, replayErr := service.replaySession(
			ctx,
			idempotencyKey,
			requestHash,
			command.Actor,
		); replayErr != nil || found {
			return replayed, replayErr
		}
		if !generatedSlug || !errors.Is(err, domain.ErrSlugConflict) {
			return domain.Session{}, fmt.Errorf("persist session: %w", err)
		}
	}

	return domain.Session{}, fmt.Errorf("persist session: no readable slug available after %d attempts: %w", maximumSlugCollisionAttempts, domain.ErrSlugConflict)
}

func slugCollisionCandidate(base string, attempt int) string {
	if attempt <= 1 {
		return base
	}
	suffix := "-" + strconv.Itoa(attempt)
	maximumBaseLength := domain.MaximumGeneratedSlugLength - len(suffix)
	if maximumBaseLength < 1 {
		return "session" + suffix
	}
	if len(base) > maximumBaseLength {
		base = strings.TrimRight(base[:maximumBaseLength], "-")
	}
	return base + suffix
}

// GetQuery identifies a session read operation.
type GetQuery struct {
	Actor            domain.Actor
	SessionID        string
	GuildID          string
	AllowGuildMember bool
}

// Get returns a session when the actor owns it or an authorized caller has
// explicitly enabled guild-member read access for the requested guild.
func (service *Service) Get(
	ctx context.Context,
	query GetQuery,
) (domain.Session, error) {
	if err := query.Actor.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("validate actor: %w", err)
	}

	session, err := service.repository.Get(
		ctx,
		strings.TrimSpace(query.SessionID),
	)
	if err != nil {
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}

	if query.AllowGuildMember {
		if session.GuildID != strings.TrimSpace(query.GuildID) {
			return domain.Session{}, domain.ErrNotFound
		}
	} else if err := authorizeOwner(query.Actor, session); err != nil {
		return domain.Session{}, err
	}

	return session, nil
}

// ListQuery identifies an owner-session query.
type ListQuery struct {
	Actor  domain.Actor
	Limit  int32
	States []domain.LifecycleState
}

// List returns sessions owned by the requesting actor.
func (service *Service) List(
	ctx context.Context,
	query ListQuery,
) ([]domain.Session, error) {
	if err := query.Actor.Validate(); err != nil {
		return nil, fmt.Errorf("validate actor: %w", err)
	}
	allowed := make(map[domain.LifecycleState]struct{}, len(query.States))
	for _, state := range query.States {
		if !state.Valid() {
			return nil, fmt.Errorf("invalid lifecycle filter %q", state)
		}
		allowed[state] = struct{}{}
	}

	sessions, err := service.repository.ListByOwner(
		ctx,
		query.Actor.ID,
		query.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list owner sessions: %w", err)
	}

	filtered := make([]domain.Session, 0, len(sessions))
	for _, session := range sessions {
		if len(allowed) == 0 {
			if session.LifecycleState == domain.StateDeleted {
				continue
			}
		} else if _, ok := allowed[session.LifecycleState]; !ok {
			continue
		}
		filtered = append(filtered, session)
	}
	return filtered, nil
}

// TransitionCommand identifies a requested lifecycle transition.
type TransitionCommand struct {
	Actor          domain.Actor
	SessionID      string
	To             domain.LifecycleState
	CorrelationID  string
	IdempotencyKey string
}

// Transition validates and persists one direct lifecycle transition.
func (service *Service) Transition(
	ctx context.Context,
	command TransitionCommand,
) (domain.Session, error) {
	if err := command.Actor.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("validate actor: %w", err)
	}

	idempotencyKey, requestHash, err := transitionRequestIdentity(command)
	if err != nil {
		return domain.Session{}, err
	}

	if replayed, found, err := service.replaySession(
		ctx,
		idempotencyKey,
		requestHash,
		command.Actor,
	); err != nil || found {
		return replayed, err
	}

	session, err := service.repository.Get(
		ctx,
		strings.TrimSpace(command.SessionID),
	)
	if err != nil {
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}

	if err := authorizeOwner(command.Actor, session); err != nil {
		return domain.Session{}, err
	}

	now := service.clock.Now().UTC()
	from := session.LifecycleState
	expectedVersion := session.Version

	if err := session.Transition(command.To, now); err != nil {
		return domain.Session{}, fmt.Errorf(
			"transition session: %w",
			err,
		)
	}

	eventID, err := service.newID(now, "state-change event")
	if err != nil {
		return domain.Session{}, err
	}

	correlationID, err := service.resolveCorrelationID(
		command.CorrelationID,
		now,
	)
	if err != nil {
		return domain.Session{}, err
	}

	event := domain.NewStateChangedEvent(
		eventID,
		correlationID,
		command.Actor,
		session,
		from,
		now,
	)

	if err := event.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf(
			"construct state-change event: %w",
			err,
		)
	}

	idempotency, err := domain.NewCompletedIdempotencyRecord(
		idempotencyKey,
		requestHash,
		session.ID,
		now,
		service.idempotencyRetention,
	)
	if err != nil {
		return domain.Session{}, fmt.Errorf(
			"construct idempotency record: %w",
			err,
		)
	}

	if err := service.repository.SaveWithEvent(
		ctx,
		session,
		expectedVersion,
		event,
		idempotency,
	); err != nil {
		if replayed, found, replayErr := service.replaySession(
			ctx,
			idempotencyKey,
			requestHash,
			command.Actor,
		); replayErr != nil || found {
			return replayed, replayErr
		}

		return domain.Session{}, fmt.Errorf(
			"persist state transition: %w",
			err,
		)
	}

	return session, nil
}

func createRequestIdentity(
	command CreateCommand,
) (string, string, error) {
	key := strings.TrimSpace(command.IdempotencyKey)
	if key == "" {
		return "", "", fmt.Errorf("idempotency key is required")
	}
	description, err := domain.NormalizeSessionDescription(command.Description)
	if err != nil {
		return "", "", err
	}

	hash, err := hashRequest(struct {
		CommandType string `json:"command_type"`
		ActorType   string `json:"actor_type"`
		ActorID     string `json:"actor_id"`
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
		Description string `json:"description,omitempty"`
		GameType    string `json:"game_type"`
		GuildID     string `json:"guild_id"`
		ChannelID   string `json:"channel_id"`
	}{
		CommandType: "CreateSession",
		ActorType:   string(command.Actor.Type),
		ActorID:     strings.TrimSpace(command.Actor.ID),
		Slug:        strings.TrimSpace(command.Slug),
		DisplayName: strings.TrimSpace(command.DisplayName),
		Description: description,
		GameType:    strings.ToLower(strings.TrimSpace(command.GameType)),
		GuildID:     strings.TrimSpace(command.GuildID),
		ChannelID:   strings.TrimSpace(command.ChannelID),
	})
	if err != nil {
		return "", "", fmt.Errorf("hash create-session request: %w", err)
	}

	return key, hash, nil
}

func updateDescriptionRequestIdentity(command UpdateDescriptionCommand) (string, string, error) {
	key := strings.TrimSpace(command.IdempotencyKey)
	if key == "" {
		return "", "", fmt.Errorf("idempotency key is required")
	}
	description, err := domain.NormalizeSessionDescription(command.Description)
	if err != nil {
		return "", "", err
	}
	hash, err := hashRequest(struct {
		CommandType string `json:"command_type"`
		ActorID     string `json:"actor_id"`
		SessionID   string `json:"session_id"`
		GuildID     string `json:"guild_id"`
		Description string `json:"description"`
	}{
		CommandType: "UpdateSessionDescription",
		ActorID:     strings.TrimSpace(command.Actor.ID),
		SessionID:   strings.TrimSpace(command.SessionID),
		GuildID:     strings.TrimSpace(command.GuildID),
		Description: description,
	})
	if err != nil {
		return "", "", fmt.Errorf("hash update-description request: %w", err)
	}
	return key, hash, nil
}

func transitionRequestIdentity(
	command TransitionCommand,
) (string, string, error) {
	key := strings.TrimSpace(command.IdempotencyKey)
	if key == "" {
		return "", "", fmt.Errorf("idempotency key is required")
	}

	hash, err := hashRequest(struct {
		CommandType string `json:"command_type"`
		ActorType   string `json:"actor_type"`
		ActorID     string `json:"actor_id"`
		SessionID   string `json:"session_id"`
		To          string `json:"to"`
	}{
		CommandType: "TransitionSession",
		ActorType:   string(command.Actor.Type),
		ActorID:     strings.TrimSpace(command.Actor.ID),
		SessionID:   strings.TrimSpace(command.SessionID),
		To:          string(command.To),
	})
	if err != nil {
		return "", "", fmt.Errorf("hash transition-session request: %w", err)
	}

	return key, hash, nil
}

func configureRequestIdentity(command ConfigureCommand) (string, string, error) {
	key := strings.TrimSpace(command.IdempotencyKey)
	if key == "" {
		return "", "", fmt.Errorf("idempotency key is required")
	}
	creatorDLCs, err := domain.NormalizeCreatorDLCs(command.CreatorDLCs)
	if err != nil {
		return "", "", err
	}
	hash, err := hashRequest(struct {
		CommandType         string   `json:"command_type"`
		ActorID             string   `json:"actor_id"`
		SessionID           string   `json:"session_id"`
		GuildID             string   `json:"guild_id"`
		GameProfileID       string   `json:"game_profile_id"`
		SleepAfterSeconds   int64    `json:"sleep_after_seconds"`
		ArchiveAfterSeconds int64    `json:"archive_after_seconds"`
		TeamSpeakEnabled    bool     `json:"teamspeak_enabled"`
		Vanilla             bool     `json:"vanilla,omitempty"`
		CreatorDLCs         []string `json:"creator_dlcs,omitempty"`
		StartWhenReady      bool     `json:"start_when_ready,omitempty"`
	}{
		CommandType:         "ConfigureSession",
		ActorID:             strings.TrimSpace(command.Actor.ID),
		SessionID:           strings.TrimSpace(command.SessionID),
		GuildID:             strings.TrimSpace(command.GuildID),
		GameProfileID:       strings.ToLower(strings.TrimSpace(command.GameProfileID)),
		SleepAfterSeconds:   command.SleepAfterSeconds,
		ArchiveAfterSeconds: command.ArchiveAfterSeconds,
		TeamSpeakEnabled:    command.TeamSpeakEnabled,
		Vanilla:             command.Vanilla,
		CreatorDLCs:         creatorDLCs,
		StartWhenReady:      command.StartWhenReady,
	})
	if err != nil {
		return "", "", fmt.Errorf("hash configure-session request: %w", err)
	}
	return key, hash, nil
}

func hashRequest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal canonical request: %w", err)
	}

	digest := sha256.Sum256(encoded)

	return hex.EncodeToString(digest[:]), nil
}

func (service *Service) replaySession(
	ctx context.Context,
	key string,
	requestHash string,
	actor domain.Actor,
) (domain.Session, bool, error) {
	record, err := service.repository.GetIdempotency(ctx, key)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Session{}, false, nil
	}

	if err != nil {
		return domain.Session{}, true, fmt.Errorf(
			"get idempotency record: %w",
			err,
		)
	}

	if record.RequestHash != requestHash {
		return domain.Session{}, true, fmt.Errorf(
			"%w: key %s",
			domain.ErrIdempotencyConflict,
			key,
		)
	}

	if record.Status != domain.IdempotencyCompleted {
		return domain.Session{}, true, fmt.Errorf(
			"%w: key %s",
			domain.ErrCommandInProgress,
			key,
		)
	}

	session, err := service.repository.Get(ctx, record.ResultReference)
	if err != nil {
		return domain.Session{}, true, fmt.Errorf(
			"get idempotent command result: %w",
			err,
		)
	}

	if err := authorizeOwner(actor, session); err != nil {
		return domain.Session{}, true, err
	}

	return session, true, nil
}

func authorizeOwner(
	actor domain.Actor,
	session domain.Session,
) error {
	if actor.ID != session.OwnerDiscordUserID {
		return fmt.Errorf(
			"%w: actor %s does not own session %s",
			domain.ErrForbidden,
			actor.ID,
			session.ID,
		)
	}

	return nil
}

func authorizeLifecycleActor(actor domain.Actor, session domain.Session, canManageGuild bool) error {
	if canManageGuild {
		return nil
	}
	return authorizeOwner(actor, session)
}

func (service *Service) resolveCorrelationID(
	provided string,
	now time.Time,
) (string, error) {
	provided = strings.TrimSpace(provided)
	if provided != "" {
		return provided, nil
	}

	return service.newID(now, "correlation")
}

func (service *Service) newID(
	now time.Time,
	purpose string,
) (string, error) {
	id, err := service.ids.New(now)
	if err != nil {
		return "", fmt.Errorf(
			"generate %s ID: %w",
			purpose,
			err,
		)
	}

	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf(
			"generate %s ID: generator returned an empty ID",
			purpose,
		)
	}

	return id, nil
}
