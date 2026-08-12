package domain

import (
	"fmt"
	"strings"
	"time"
)

// EventType is the stable machine-readable event name.
type EventType string

const (
	EventSessionCreated      EventType = "SessionCreated"
	EventStateChanged        EventType = "SessionStateChanged"
	EventSessionConfigured   EventType = "SessionConfigured"
	EventArtifactRequested   EventType = "ArtifactUploadRequested"
	EventArtifactValidated   EventType = "ArtifactValidated"
	EventArtifactRejected    EventType = "ArtifactRejected"
	EventWorkflowStarted     EventType = "WorkflowStarted"
	EventWorkflowFailed      EventType = "WorkflowFailed"
	EventWorkflowCompleted   EventType = "WorkflowCompleted"
	EventProvisioningStage   EventType = "ProvisioningStageCompleted"
	EventInfrastructureReady EventType = "InfrastructureReady"
	EventProvisioningFailed  EventType = "InfrastructureProvisioningFailed"
	EventBootstrapStage      EventType = "BootstrapStageCompleted"
	EventGameServerReady     EventType = "GameServerReady"
	EventBootstrapFailed     EventType = "GameServerBootstrapFailed"
)

// NewProvisioningEvent records a stable workflow stage without exposing cloud
// account identifiers or credentials.
func NewProvisioningEvent(eventID string, eventType EventType, stage string, workflow Workflow, session Session, now time.Time) SessionEvent {
	return SessionEvent{
		ID: eventID, SessionID: session.ID, Type: eventType, OccurredAt: now.UTC(),
		ActorType: string(ActorTypeSystem), ActorID: "ProvisionSession", CorrelationID: workflow.CorrelationID,
		Data: map[string]string{
			"workflow_id": workflow.ID, "stage": strings.TrimSpace(stage),
			"state": string(session.LifecycleState), "instance_id": session.Infrastructure.InstanceID,
			"volume_id": session.Infrastructure.DataVolumeID,
		},
	}
}

// NewBootstrapEvent records progress without including commands, output, or
// credentials from the managed node.
func NewBootstrapEvent(eventID string, eventType EventType, stage string, workflow Workflow, session Session, now time.Time) SessionEvent {
	return SessionEvent{
		ID: eventID, SessionID: session.ID, Type: eventType, OccurredAt: now.UTC(),
		ActorType: string(ActorTypeSystem), ActorID: BootstrapWorkflowType, CorrelationID: workflow.CorrelationID,
		Data: map[string]string{
			"workflow_id": workflow.ID, "stage": strings.TrimSpace(stage),
			"state": string(session.LifecycleState), "instance_id": session.Infrastructure.InstanceID,
		},
	}
}

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

func NewArtifactEvent(
	eventID string,
	eventType EventType,
	correlationID string,
	actor Actor,
	session Session,
	kind ArtifactKind,
	objectKey string,
	now time.Time,
) SessionEvent {
	return SessionEvent{
		ID: eventID, SessionID: session.ID, Type: eventType, OccurredAt: now.UTC(),
		ActorType: string(actor.Type), ActorID: actor.ID, CorrelationID: correlationID,
		Data: map[string]string{
			"artifact_kind": string(kind),
			"object_key":    objectKey,
			"state":         string(session.LifecycleState),
		},
	}
}

func NewWorkflowEvent(
	eventID string,
	eventType EventType,
	correlationID string,
	actor Actor,
	session Session,
	workflow Workflow,
	now time.Time,
) SessionEvent {
	return SessionEvent{
		ID: eventID, SessionID: session.ID, Type: eventType, OccurredAt: now.UTC(),
		ActorType: string(actor.Type), ActorID: actor.ID, CorrelationID: correlationID,
		Data: map[string]string{
			"workflow_id":     workflow.ID,
			"workflow_type":   workflow.Type,
			"workflow_status": string(workflow.Status),
		},
	}
}

// NewSessionConfiguredEvent records an immutable configuration revision.
func NewSessionConfiguredEvent(
	eventID string,
	correlationID string,
	actor Actor,
	session Session,
	now time.Time,
) SessionEvent {
	return SessionEvent{
		ID:            eventID,
		SessionID:     session.ID,
		Type:          EventSessionConfigured,
		OccurredAt:    now.UTC(),
		ActorType:     string(actor.Type),
		ActorID:       actor.ID,
		CorrelationID: correlationID,
		Data: map[string]string{
			"configuration_revision": fmt.Sprintf("%d", session.ConfigurationRevision),
			"game_profile_id":        session.GameProfileID,
			"sleep_after_seconds":    fmt.Sprintf("%d", session.SleepAfterSeconds),
			"archive_after_seconds":  fmt.Sprintf("%d", session.ArchiveAfterSeconds),
			"teamspeak_enabled":      fmt.Sprintf("%t", session.TeamSpeakEnabled),
		},
	}
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
