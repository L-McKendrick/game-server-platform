package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

// SessionRepository is an in-memory implementation for tests and local tools.
type SessionRepository struct {
	mu          sync.RWMutex
	sessions    map[string]domain.Session
	events      map[string][]domain.SessionEvent
	idempotency map[string]domain.IdempotencyRecord
	workflows   map[string]domain.Workflow
	capacity    map[string]string
}

var _ ports.SessionRepository = (*SessionRepository)(nil)

// NewSessionRepository creates an empty repository.
func NewSessionRepository() *SessionRepository {
	return &SessionRepository{
		sessions:    make(map[string]domain.Session),
		events:      make(map[string][]domain.SessionEvent),
		idempotency: make(map[string]domain.IdempotencyRecord),
		workflows:   make(map[string]domain.Workflow),
		capacity:    make(map[string]string),
	}
}

// Create atomically stores a session and its initial event.
func (repository *SessionRepository) Create(
	ctx context.Context,
	session domain.Session,
	event domain.SessionEvent,
	idempotency domain.IdempotencyRecord,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate session: %w", err)
	}

	if err := validateEvent(session.ID, event); err != nil {
		return err
	}

	if err := validateIdempotency(session.ID, idempotency); err != nil {
		return err
	}

	repository.mu.Lock()
	defer repository.mu.Unlock()

	if _, exists := repository.idempotency[idempotency.Key]; exists {
		return fmt.Errorf(
			"%w: idempotency key %s",
			domain.ErrAlreadyExists,
			idempotency.Key,
		)
	}

	if _, exists := repository.sessions[session.ID]; exists {
		return fmt.Errorf(
			"%w: session %s",
			domain.ErrAlreadyExists,
			session.ID,
		)
	}
	for _, existing := range repository.sessions {
		if existing.GuildID == session.GuildID && existing.Slug == session.Slug {
			return fmt.Errorf("%w: %s", domain.ErrSlugConflict, session.Slug)
		}
	}

	repository.sessions[session.ID] = session
	repository.events[session.ID] = []domain.SessionEvent{
		cloneEvent(event),
	}
	repository.idempotency[idempotency.Key] = idempotency

	return nil
}

// Get returns one session by ID.
func (repository *SessionRepository) Get(
	ctx context.Context,
	sessionID string,
) (domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Session{}, err
	}

	repository.mu.RLock()
	defer repository.mu.RUnlock()

	session, exists := repository.sessions[sessionID]
	if !exists {
		return domain.Session{}, fmt.Errorf(
			"%w: session %s",
			domain.ErrNotFound,
			sessionID,
		)
	}

	return session, nil
}

// SaveWithEvent updates a session using optimistic concurrency.
func (repository *SessionRepository) SaveWithEvent(
	ctx context.Context,
	session domain.Session,
	expectedVersion int64,
	event domain.SessionEvent,
	idempotency domain.IdempotencyRecord,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate session: %w", err)
	}

	if err := validateEvent(session.ID, event); err != nil {
		return err
	}

	if err := validateIdempotency(session.ID, idempotency); err != nil {
		return err
	}

	if session.Version != expectedVersion+1 {
		return fmt.Errorf(
			"session version %d must equal expected version %d plus one",
			session.Version,
			expectedVersion,
		)
	}

	repository.mu.Lock()
	defer repository.mu.Unlock()

	if _, exists := repository.idempotency[idempotency.Key]; exists {
		return fmt.Errorf(
			"%w: idempotency key %s",
			domain.ErrConflict,
			idempotency.Key,
		)
	}

	current, exists := repository.sessions[session.ID]
	if !exists {
		return fmt.Errorf(
			"%w: session %s",
			domain.ErrNotFound,
			session.ID,
		)
	}

	if current.Version != expectedVersion {
		return fmt.Errorf(
			"%w: session %s expected version %d but found %d",
			domain.ErrConflict,
			session.ID,
			expectedVersion,
			current.Version,
		)
	}

	repository.sessions[session.ID] = session
	repository.events[session.ID] = append(
		repository.events[session.ID],
		cloneEvent(event),
	)
	repository.idempotency[idempotency.Key] = idempotency

	return nil
}

// GetIdempotency returns a durable command result by external key.
func (repository *SessionRepository) GetIdempotency(
	ctx context.Context,
	key string,
) (domain.IdempotencyRecord, error) {
	if err := ctx.Err(); err != nil {
		return domain.IdempotencyRecord{}, err
	}

	repository.mu.RLock()
	defer repository.mu.RUnlock()

	record, exists := repository.idempotency[key]
	if !exists {
		return domain.IdempotencyRecord{}, fmt.Errorf(
			"%w: idempotency key %s",
			domain.ErrNotFound,
			key,
		)
	}

	return record, nil
}

// ListByOwner returns an owner's sessions, newest first.
func (repository *SessionRepository) ListByOwner(
	ctx context.Context,
	ownerDiscordUserID string,
	limit int32,
) ([]domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 25
	}

	if limit > 100 {
		limit = 100
	}

	repository.mu.RLock()
	defer repository.mu.RUnlock()

	sessions := make([]domain.Session, 0)

	for _, session := range repository.sessions {
		if session.OwnerDiscordUserID == ownerDiscordUserID {
			sessions = append(sessions, session)
		}
	}

	sort.Slice(
		sessions,
		func(first, second int) bool {
			if sessions[first].UpdatedAt.Equal(
				sessions[second].UpdatedAt,
			) {
				return sessions[first].ID > sessions[second].ID
			}

			return sessions[first].UpdatedAt.After(
				sessions[second].UpdatedAt,
			)
		},
	)

	if len(sessions) > int(limit) {
		sessions = sessions[:limit]
	}

	return sessions, nil
}

// ListByGuild returns sessions in one Discord guild, newest first.
func (repository *SessionRepository) ListByGuild(
	ctx context.Context,
	guildID string,
	limit int32,
) ([]domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}

	repository.mu.RLock()
	defer repository.mu.RUnlock()
	sessions := make([]domain.Session, 0)
	for _, session := range repository.sessions {
		if session.GuildID == guildID {
			sessions = append(sessions, session)
		}
	}
	sort.Slice(sessions, func(first, second int) bool {
		if sessions[first].UpdatedAt.Equal(sessions[second].UpdatedAt) {
			return sessions[first].ID > sessions[second].ID
		}
		return sessions[first].UpdatedAt.After(sessions[second].UpdatedAt)
	})
	if len(sessions) > int(limit) {
		sessions = sessions[:limit]
	}
	return sessions, nil
}

// Events returns a copy of the events stored for a session.
//
// This method is intentionally not part of the production repository
// interface. It exists to support application-layer assertions.
func (repository *SessionRepository) Events(
	sessionID string,
) []domain.SessionEvent {
	repository.mu.RLock()
	defer repository.mu.RUnlock()

	stored := repository.events[sessionID]
	events := make([]domain.SessionEvent, 0, len(stored))

	for _, event := range stored {
		events = append(events, cloneEvent(event))
	}

	return events
}

func validateEvent(
	sessionID string,
	event domain.SessionEvent,
) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate event: %w", err)
	}

	if event.SessionID != sessionID {
		return fmt.Errorf(
			"event session ID %q does not match session %q",
			event.SessionID,
			sessionID,
		)
	}

	return nil
}

func validateIdempotency(
	sessionID string,
	record domain.IdempotencyRecord,
) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("validate idempotency record: %w", err)
	}

	if record.Status != domain.IdempotencyCompleted {
		return fmt.Errorf("metadata mutation requires a completed idempotency record")
	}

	if record.ResultReference != sessionID {
		return fmt.Errorf(
			"idempotency result reference %q does not match session %q",
			record.ResultReference,
			sessionID,
		)
	}

	return nil
}

func cloneEvent(
	event domain.SessionEvent,
) domain.SessionEvent {
	cloned := event

	if event.Data != nil {
		cloned.Data = make(map[string]string, len(event.Data))

		for key, value := range event.Data {
			cloned.Data[key] = value
		}
	}

	return cloned
}
