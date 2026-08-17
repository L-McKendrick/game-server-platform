package domain

import (
	"fmt"
	"strings"
	"time"
)

// EventType is the stable machine-readable event name.
type EventType string

const (
	EventSessionCreated            EventType = "SessionCreated"
	EventSessionDescriptionChanged EventType = "SessionDescriptionChanged"
	EventStateChanged              EventType = "SessionStateChanged"
	EventSessionConfigured         EventType = "SessionConfigured"
	EventArtifactRequested         EventType = "ArtifactUploadRequested"
	EventArtifactValidated         EventType = "ArtifactValidated"
	EventArtifactRejected          EventType = "ArtifactRejected"
	EventWorkflowStarted           EventType = "WorkflowStarted"
	EventWorkflowFailed            EventType = "WorkflowFailed"
	EventWorkflowCompleted         EventType = "WorkflowCompleted"
	EventProvisioningStage         EventType = "ProvisioningStageCompleted"
	EventInfrastructureReady       EventType = "InfrastructureReady"
	EventProvisioningFailed        EventType = "InfrastructureProvisioningFailed"
	EventBootstrapStage            EventType = "BootstrapStageCompleted"
	EventGameServerReady           EventType = "GameServerReady"
	EventBootstrapFailed           EventType = "GameServerBootstrapFailed"
	EventHealthChanged             EventType = "GameServerHealthChanged"
	EventSleepStarted              EventType = "GameServerSleepStarted"
	EventSessionSleeping           EventType = "GameServerSleeping"
	EventWakeStarted               EventType = "GameServerWakeStarted"
	EventSessionWoken              EventType = "GameServerWoken"
	EventSleepWakeFailed           EventType = "GameServerSleepWakeFailed"
	EventArchiveStarted            EventType = "SessionArchiveStarted"
	EventArchiveVerified           EventType = "SessionArchiveVerified"
	EventArchiveFailed             EventType = "SessionArchiveFailed"
	EventInfrastructureDestroyed   EventType = "SessionInfrastructureDestroyed"
	EventRestoreStarted            EventType = "SessionRestoreStarted"
	EventRestoreStage              EventType = "SessionRestoreStage"
	EventSessionRestored           EventType = "SessionRestored"
	EventRestoreFailed             EventType = "SessionRestoreFailed"
	EventTerminationStarted        EventType = "SessionTerminationStarted"
	EventSessionTerminated         EventType = "SessionTerminated"
	EventTerminationFailed         EventType = "SessionTerminationFailed"
	EventProgressMilestone         EventType = "SessionProgressMilestone"
)

func NewTerminationEvent(eventID string, eventType EventType, stage string, workflow Workflow, session Session, objectsDeleted int, now time.Time) SessionEvent {
	data := map[string]string{
		"workflow_id": workflow.ID, "stage": strings.TrimSpace(stage), "state": string(session.LifecycleState),
		"objects_deleted": fmt.Sprintf("%d", objectsDeleted),
		"display_name":    session.DisplayName, "slug": session.Slug, "description": session.Description,
	}
	addProgressEventData(data, session, workflow)
	return SessionEvent{
		ID: eventID, SessionID: session.ID, Type: eventType, OccurredAt: now.UTC(),
		ActorType: string(ActorTypeSystem), ActorID: TerminationWorkflowType, CorrelationID: workflow.CorrelationID,
		Data: data,
	}
}

func NewArchiveEvent(eventID string, eventType EventType, stage string, workflow Workflow, session Session, archive ArchiveMetadata, now time.Time) SessionEvent {
	data := map[string]string{
		"workflow_id": workflow.ID, "stage": strings.TrimSpace(stage), "state": string(session.LifecycleState),
		"archive_id": archive.ID, "archive_object_key": archive.ObjectKey,
		"archive_manifest_object_key": archive.ManifestObjectKey,
		"archive_manifest_sha256":     archive.ManifestSHA256,
		"archive_manifest_size_bytes": fmt.Sprintf("%d", archive.ManifestSizeBytes),
	}
	addProgressEventData(data, session, workflow)
	return SessionEvent{
		ID: eventID, SessionID: session.ID, Type: eventType, OccurredAt: now.UTC(),
		ActorType: string(ActorTypeSystem), ActorID: ArchiveWorkflowType, CorrelationID: workflow.CorrelationID,
		Data: data,
	}
}

// NewProvisioningEvent records a stable workflow stage without exposing cloud
// account identifiers or credentials.
func NewProvisioningEvent(eventID string, eventType EventType, stage string, workflow Workflow, session Session, now time.Time) SessionEvent {
	data := map[string]string{
		"workflow_id": workflow.ID, "stage": strings.TrimSpace(stage),
		"state": string(session.LifecycleState), "instance_id": session.Infrastructure.InstanceID,
		"volume_id": session.Infrastructure.DataVolumeID,
	}
	addProgressEventData(data, session, workflow)
	return SessionEvent{
		ID: eventID, SessionID: session.ID, Type: eventType, OccurredAt: now.UTC(),
		ActorType: string(ActorTypeSystem), ActorID: "ProvisionSession", CorrelationID: workflow.CorrelationID,
		Data: data,
	}
}

func NewRestoreEvent(eventID string, eventType EventType, stage string, workflow Workflow, session Session, now time.Time) SessionEvent {
	event := NewProvisioningEvent(eventID, eventType, stage, workflow, session, now)
	event.ActorID = RestoreWorkflowType
	event.Data["archive_id"] = session.Archive.ID
	return event
}

func NewHealthChangedEvent(eventID string, session Session, from HealthStatus, observation HealthObservation, now time.Time) SessionEvent {
	return SessionEvent{ID: eventID, SessionID: session.ID, Type: EventHealthChanged, OccurredAt: now.UTC(), ActorType: string(ActorTypeSystem), ActorID: "MonitorGameServer", CorrelationID: eventID,
		Data: map[string]string{"from_health": string(from), "to_health": string(session.HealthStatus), "arma_service": fmt.Sprintf("%t", observation.ArmaService), "arma_udp_2302": fmt.Sprintf("%t", observation.ArmaUDP), "teamspeak_service": fmt.Sprintf("%t", observation.TeamSpeakService), "teamspeak_udp_9987": fmt.Sprintf("%t", observation.TeamSpeakUDP), "disk_used_percent": fmt.Sprintf("%d", observation.DiskUsedPercent), "memory_available_bytes": fmt.Sprintf("%d", observation.MemoryAvailableBytes), "player_count": fmt.Sprintf("%d", observation.PlayerCount)},
	}
}

// NewBootstrapEvent records progress without including commands, output, or
// credentials from the managed node.
func NewBootstrapEvent(eventID string, eventType EventType, stage string, workflow Workflow, session Session, now time.Time) SessionEvent {
	data := map[string]string{
		"workflow_id": workflow.ID, "stage": strings.TrimSpace(stage),
		"state": string(session.LifecycleState), "instance_id": session.Infrastructure.InstanceID,
	}
	addProgressEventData(data, session, workflow)
	return SessionEvent{
		ID: eventID, SessionID: session.ID, Type: eventType, OccurredAt: now.UTC(),
		ActorType: string(ActorTypeSystem), ActorID: BootstrapWorkflowType, CorrelationID: workflow.CorrelationID,
		Data: data,
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
	data := map[string]string{
		"workflow_id":     workflow.ID,
		"workflow_type":   workflow.Type,
		"workflow_status": string(workflow.Status),
	}
	addProgressEventData(data, session, workflow)
	return SessionEvent{
		ID: eventID, SessionID: session.ID, Type: eventType, OccurredAt: now.UTC(),
		ActorType: string(actor.Type), ActorID: actor.ID, CorrelationID: correlationID,
		Data: data,
	}
}

func NewProgressMilestoneEvent(eventID string, workflow Workflow, session Session, now time.Time) SessionEvent {
	data := map[string]string{
		"workflow_id": workflow.ID, "workflow_type": workflow.Type,
		"state": string(session.LifecycleState),
	}
	addProgressEventData(data, session, workflow)
	return SessionEvent{
		ID: eventID, SessionID: session.ID, Type: EventProgressMilestone, OccurredAt: now.UTC(),
		ActorType: string(ActorTypeSystem), ActorID: workflow.Type, CorrelationID: workflow.CorrelationID,
		Data: data,
	}
}

func addProgressEventData(data map[string]string, session Session, workflow Workflow) {
	if session.Progress.WorkflowID != workflow.ID || session.Progress.Milestone == "" {
		return
	}
	data["progress_milestone"] = string(session.Progress.Milestone)
	data["progress_updated_at"] = session.Progress.UpdatedAt.UTC().Format(time.RFC3339Nano)
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
			"display_name":           session.DisplayName,
			"slug":                   session.Slug,
			"description":            session.Description,
			"configuration_revision": fmt.Sprintf("%d", session.ConfigurationRevision),
			"game_profile_id":        session.GameProfileID,
			"sleep_after_seconds":    fmt.Sprintf("%d", session.SleepAfterSeconds),
			"archive_after_seconds":  fmt.Sprintf("%d", session.ArchiveAfterSeconds),
			"teamspeak_enabled":      fmt.Sprintf("%t", session.TeamSpeakEnabled),
			"vanilla":                fmt.Sprintf("%t", session.Vanilla),
		},
	}
}

// NewSessionDescriptionChangedEvent records both sides of a user-visible
// description change without mutating earlier history.
func NewSessionDescriptionChangedEvent(
	eventID string,
	correlationID string,
	actor Actor,
	session Session,
	previous string,
	now time.Time,
) SessionEvent {
	return SessionEvent{
		ID:            eventID,
		SessionID:     session.ID,
		Type:          EventSessionDescriptionChanged,
		OccurredAt:    now.UTC(),
		ActorType:     string(actor.Type),
		ActorID:       actor.ID,
		CorrelationID: correlationID,
		Data: map[string]string{
			"previous_description": previous,
			"description":          session.Description,
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
			"display_name": session.DisplayName,
			"slug":         session.Slug,
			"description":  session.Description,
			"game_type":    session.GameType,
			"state":        string(session.LifecycleState),
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
