package sessions

import (
	"context"
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
	repository ports.SessionRepository
	ids        IDGenerator
	clock      Clock
}

// NewService creates a session application service.
func NewService(
	repository ports.SessionRepository,
	ids IDGenerator,
	clock Clock,
) (*Service, error) {
	switch {
	case repository == nil:
		return nil, fmt.Errorf("session repository is required")
	case ids == nil:
		return nil, fmt.Errorf("ID generator is required")
	case clock == nil:
		return nil, fmt.Errorf("clock is required")
	default:
		return &Service{
			repository: repository,
			ids:        ids,
			clock:      clock,
		}, nil
	}
}

// CreateCommand contains the user-controlled values needed to create a session.
type CreateCommand struct {
	Actor         domain.Actor
	CorrelationID string

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

	if err := service.repository.Create(
		ctx,
		session,
		event,
	); err != nil {
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
	Actor         domain.Actor
	SessionID     string
	To            domain.LifecycleState
	CorrelationID string
}

// Transition validates and persists one direct lifecycle transition.
func (service *Service) Transition(
	ctx context.Context,
	command TransitionCommand,
) (domain.Session, error) {
	if err := command.Actor.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("validate actor: %w", err)
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

	if err := service.repository.SaveWithEvent(
		ctx,
		session,
		expectedVersion,
		event,
	); err != nil {
		return domain.Session{}, fmt.Errorf(
			"persist state transition: %w",
			err,
		)
	}

	return session, nil
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
