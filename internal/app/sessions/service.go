package sessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
	ids                  IDGenerator
	clock                Clock
	idempotencyRetention time.Duration
}

// NewService creates a session application service.
func NewService(
	repository ports.SessionRepository,
	ids IDGenerator,
	clock Clock,
	idempotencyRetention time.Duration,
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
		return &Service{
			repository:           repository,
			ids:                  ids,
			clock:                clock,
			idempotencyRetention: idempotencyRetention,
		}, nil
	}
}

// CreateCommand contains the user-controlled values needed to create a session.
type CreateCommand struct {
	Actor          domain.Actor
	CorrelationID  string
	IdempotencyKey string

	Slug        string
	DisplayName string
	GameType    string
	GuildID     string
	ChannelID   string
}

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

	session, err := domain.NewSession(
		domain.NewSessionInput{
			ID:                 sessionID,
			Slug:               command.Slug,
			DisplayName:        command.DisplayName,
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

	event := domain.NewSessionCreatedEvent(
		eventID,
		correlationID,
		command.Actor,
		session,
		now,
	)

	if err := event.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf(
			"construct creation event: %w",
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

	if err := service.repository.Create(
		ctx,
		session,
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
			"persist session: %w",
			err,
		)
	}

	return session, nil
}

// GetQuery identifies a session read operation.
type GetQuery struct {
	Actor     domain.Actor
	SessionID string
}

// Get returns a session when the requesting actor owns it.
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

	if err := authorizeOwner(query.Actor, session); err != nil {
		return domain.Session{}, err
	}

	return session, nil
}

// ListQuery identifies an owner-session query.
type ListQuery struct {
	Actor domain.Actor
	Limit int32
}

// List returns sessions owned by the requesting actor.
func (service *Service) List(
	ctx context.Context,
	query ListQuery,
) ([]domain.Session, error) {
	if err := query.Actor.Validate(); err != nil {
		return nil, fmt.Errorf("validate actor: %w", err)
	}

	sessions, err := service.repository.ListByOwner(
		ctx,
		query.Actor.ID,
		query.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list owner sessions: %w", err)
	}

	return sessions, nil
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

	hash, err := hashRequest(struct {
		CommandType string `json:"command_type"`
		ActorType   string `json:"actor_type"`
		ActorID     string `json:"actor_id"`
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
		GameType    string `json:"game_type"`
		GuildID     string `json:"guild_id"`
		ChannelID   string `json:"channel_id"`
	}{
		CommandType: "CreateSession",
		ActorType:   string(command.Actor.Type),
		ActorID:     strings.TrimSpace(command.Actor.ID),
		Slug:        strings.TrimSpace(command.Slug),
		DisplayName: strings.TrimSpace(command.DisplayName),
		GameType:    strings.ToLower(strings.TrimSpace(command.GameType)),
		GuildID:     strings.TrimSpace(command.GuildID),
		ChannelID:   strings.TrimSpace(command.ChannelID),
	})
	if err != nil {
		return "", "", fmt.Errorf("hash create-session request: %w", err)
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
