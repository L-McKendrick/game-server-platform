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
	mu       sync.RWMutex
	sessions map[string]domain.Session
	events   map[string][]domain.SessionEvent
}

var _ ports.SessionRepository = (*SessionRepository)(nil)

// NewSessionRepository creates an empty repository.
func NewSessionRepository() *SessionRepository {
	return &SessionRepository{
		sessions: make(map[string]domain.Session),
		events:   make(map[string][]domain.SessionEvent),
	}
}

// Create atomically stores a session and its initial event.
func (repository *SessionRepository) Create(
	ctx context.Context,
	session domain.Session,
	event domain.SessionEvent,
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

	repository.mu.Lock()
	defer repository.mu.Unlock()

	if _, exists := repository.sessions[session.ID]; exists {
		return fmt.Errorf(
			"%w: session %s",
			domain.ErrAlreadyExists,
			session.ID,
		)
	}

	repository.sessions[session.ID] = session
	repository.events[session.ID] = []domain.SessionEvent{
		cloneEvent(event),
	}

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

	if session.Version != expectedVersion+1 {
		return fmt.Errorf(
			"session version %d must equal expected version %d plus one",
			session.Version,
			expectedVersion,
		)
	}

	repository.mu.Lock()
	defer repository.mu.Unlock()

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

	return nil
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
