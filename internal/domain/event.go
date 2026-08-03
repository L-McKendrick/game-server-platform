package domain

import (
	"fmt"
	"strings"
	"time"
)

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

// Validate verifies that an event contains its required audit fields.
func (event SessionEvent) Validate() error {
	switch {
	case strings.TrimSpace(event.ID) == "":
		return fmt.Errorf("event ID is required")
	case strings.TrimSpace(event.SessionID) == "":
		return fmt.Errorf("event session ID is required")
	case strings.TrimSpace(string(event.Type)) == "":
		return fmt.Errorf("event type is required")
	case event.OccurredAt.IsZero():
		return fmt.Errorf("event occurrence timestamp is required")
	case strings.TrimSpace(event.ActorType) == "":
		return fmt.Errorf("event actor type is required")
	case strings.TrimSpace(event.ActorID) == "":
		return fmt.Errorf("event actor ID is required")
	case strings.TrimSpace(event.CorrelationID) == "":
		return fmt.Errorf("event correlation ID is required")
	default:
		return nil
	}
}

// NewSessionCreatedEvent creates the initial session event.
func NewSessionCreatedEvent(
	eventID string,
	correlationID string,
	actor Actor,
	session Session,
	now time.Time,
) SessionEvent {
	return SessionEvent{
		ID:            eventID,
		SessionID:     session.ID,
		Type:          EventSessionCreated,
		OccurredAt:    now.UTC(),
		ActorType:     string(actor.Type),
		ActorID:       actor.ID,
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
	actor Actor,
	session Session,
	from LifecycleState,
	now time.Time,
) SessionEvent {
	return SessionEvent{
		ID:            eventID,
		SessionID:     session.ID,
		Type:          EventStateChanged,
		OccurredAt:    now.UTC(),
		ActorType:     string(actor.Type),
		ActorID:       actor.ID,
		CorrelationID: correlationID,
		Data: map[string]string{
			"from": string(from),
			"to":   string(session.LifecycleState),
		},
	}
}
