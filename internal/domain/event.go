package domain

import "time"

// EventType is the stable machine-readable event name.
type EventType string

const (
	EventSessionCreated EventType = "SessionCreated"
	EventStateChanged   EventType = "SessionStateChanged"
)

// SessionEvent is an immutable fact about a session.
type SessionEvent struct {
	ID            string
	SessionID     string
	Type          EventType
	OccurredAt    time.Time
	ActorType     string
	ActorID       string
	CorrelationID string
	Data          map[string]string
}

// NewSessionCreatedEvent creates the initial session event.
func NewSessionCreatedEvent(
	eventID string,
	correlationID string,
	session Session,
	now time.Time,
) SessionEvent {
	return SessionEvent{
		ID:            eventID,
		SessionID:     session.ID,
		Type:          EventSessionCreated,
		OccurredAt:    now.UTC(),
		ActorType:     "user",
		ActorID:       session.OwnerDiscordUserID,
		CorrelationID: correlationID,
		Data: map[string]string{
			"slug":      session.Slug,
			"game_type": session.GameType,
			"state":     string(session.LifecycleState),
		},
	}
}

// NewStateChangedEvent records an accepted lifecycle transition.
func NewStateChangedEvent(
	eventID string,
	correlationID string,
	session Session,
	from LifecycleState,
	now time.Time,
) SessionEvent {
	return SessionEvent{
		ID:            eventID,
		SessionID:     session.ID,
		Type:          EventStateChanged,
		OccurredAt:    now.UTC(),
		ActorType:     "system",
		ActorID:       "metadata-smoke",
		CorrelationID: correlationID,
		Data: map[string]string{
			"from": string(from),
			"to":   string(session.LifecycleState),
		},
	}
}
