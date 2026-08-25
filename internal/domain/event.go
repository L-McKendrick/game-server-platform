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
	EventWorkflowCancelRequested   EventType = "WorkflowCancellationRequested"
	EventWorkflowReconciled        EventType = "WorkflowReconciled"
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
	EventPresetRevisionStaged      EventType = "PresetRevisionStaged"
	EventPresetRevisionApplying    EventType = "PresetRevisionApplying"
	EventPresetRevisionActivated   EventType = "PresetRevisionActivated"
	EventPresetRevisionFailed      EventType = "PresetRevisionFailed"
	EventPresetRevisionRolledBack  EventType = "PresetRevisionRolledBack"
	EventPlayerActivityObserved    EventType = "PlayerActivityObserved"
)

func NewTerminationEvent(eventID string, eventType EventType, stage string, workflow Workflow, session Session, objectsDeleted int, now time.Time) SessionEvent {
	data := map[string]string{
		"workflow_id": workflow.ID, "stage": strings.TrimSpace(stage), "state": string(session.LifecycleState),
		"objects_deleted": fmt.Sprintf("%d", objectsDeleted),
		"display_name":    session.DisplayName, "slug": session.Slug, "description": session.Description,
	}
	addProgressEventData(data, session, workflow)
	addPresetApplicationEventData(data, session, now)
	addPresetIntentEventData(data, session)
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
	addPresetApplicationEventData(data, session, now)
	addPresetIntentEventData(data, session)
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
	addPresetApplicationEventData(data, session, now)
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
		Data: map[string]string{"from_health": string(from), "to_health": string(session.HealthStatus), "arma_service": fmt.Sprintf("%t", observation.ArmaService), "arma_udp_2302": fmt.Sprintf("%t", observation.ArmaUDP), "teamspeak_service": fmt.Sprintf("%t", observation.TeamSpeakService), "teamspeak_udp_9987": fmt.Sprintf("%t", observation.TeamSpeakUDP), "disk_used_percent": fmt.Sprintf("%d", observation.DiskUsedPercent), "memory_available_bytes": fmt.Sprintf("%d", observation.MemoryAvailableBytes)},
	}
}

func NewPlayerActivityObservedEvent(eventID string, session Session, now time.Time) SessionEvent {
	data := map[string]string{
		"known":       fmt.Sprintf("%t", session.PlayerCountKnown),
		"observed_at": session.PlayerCountObservedAt.UTC().Format(time.RFC3339Nano),
	}
	if session.PlayerCountKnown {
		data["player_count"] = fmt.Sprintf("%d", session.PlayerCount)
	}
	if !session.IdleSince.IsZero() {
		data["idle_since"] = session.IdleSince.UTC().Format(time.RFC3339Nano)
	}
	return SessionEvent{
		ID: eventID, SessionID: session.ID, Type: EventPlayerActivityObserved, OccurredAt: now.UTC(),
		ActorType: string(ActorTypeSystem), ActorID: "MonitorGameServer", CorrelationID: eventID, Data: data,
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
	addPresetApplicationEventData(data, session, now)
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
	event := SessionEvent{
		ID: eventID, SessionID: session.ID, Type: eventType, OccurredAt: now.UTC(),
		ActorType: string(actor.Type), ActorID: actor.ID, CorrelationID: correlationID,
		Data: map[string]string{
			"artifact_kind": string(kind),
			"object_key":    objectKey,
			"state":         string(session.LifecycleState),
		},
	}
	if kind == ArtifactPreset {
		revision := session.EffectiveActivePresetRevision()
		if !revision.Empty() {
			event.Data["preset_revision"] = fmt.Sprintf("%d", revision.Number)
			event.Data["preset_revision_status"] = string(revision.Status)
		}
	}
	return event
}

// NewPresetRevisionEvent records every accepted preset revision transition as
// immutable audit history without exposing Discord attachment URLs.
func NewPresetRevisionEvent(eventID string, eventType EventType, correlationID string, actor Actor, session Session, revision PresetRevision, now time.Time) SessionEvent {
	data := map[string]string{
		"preset_revision":        fmt.Sprintf("%d", revision.Number),
		"base_preset_revision":   fmt.Sprintf("%d", revision.BaseRevision),
		"preset_revision_status": string(revision.Status),
		"preset_object_key":      revision.PresetObjectKey,
	}
	if revision.Modlist.SHA256 != "" {
		data["modlist_sha256"] = revision.Modlist.SHA256
	}
	if revision.ApplyWorkflowID != "" {
		data["workflow_id"] = revision.ApplyWorkflowID
	}
	if revision.FailureDetail != "" {
		data["failure_detail"] = revision.FailureDetail
	}
	if revision.RollbackDisposition != "" {
		data["rollback_disposition"] = string(revision.RollbackDisposition)
		data["rollback_detail"] = revision.RollbackDetail
	}
	return SessionEvent{ID: eventID, SessionID: session.ID, Type: eventType, OccurredAt: now.UTC(), ActorType: string(actor.Type), ActorID: actor.ID, CorrelationID: correlationID, Data: data}
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
	addPresetApplicationEventData(data, session, now)
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
	completed := make([]string, len(session.Progress.CompletedMilestones))
	for index, milestone := range session.Progress.CompletedMilestones {
		completed[index] = string(milestone)
	}
	data["progress_completed_milestones"] = strings.Join(completed, ",")
	skipped := make([]string, len(session.Progress.SkippedMilestones))
	for index, milestone := range session.Progress.SkippedMilestones {
		skipped[index] = string(milestone)
	}
	data["progress_skipped_milestones"] = strings.Join(skipped, ",")
	data["progress_state"] = string(session.Progress.State)
	data["progress_started_at"] = session.Progress.StartedAt.UTC().Format(time.RFC3339Nano)
	data["progress_last_progress_at"] = session.Progress.LastProgressAt.UTC().Format(time.RFC3339Nano)
	data["progress_updated_at"] = data["progress_last_progress_at"]
}

func addPresetApplicationEventData(data map[string]string, session Session, now time.Time) {
	if pending := session.PendingPresetRevision; pending.Status == PresetRevisionApplying && pending.ApplyStartedAt.Equal(now.UTC()) {
		data["revision_event_type"] = string(EventPresetRevisionApplying)
		data["preset_revision"] = fmt.Sprintf("%d", pending.Number)
		data["base_preset_revision"] = fmt.Sprintf("%d", pending.BaseRevision)
	}
	if active := session.ActivePresetRevision; !active.Empty() && active.ActivatedAt.Equal(now.UTC()) {
		data["revision_event_type"] = string(EventPresetRevisionActivated)
		data["preset_revision"] = fmt.Sprintf("%d", active.Number)
		data["base_preset_revision"] = fmt.Sprintf("%d", active.BaseRevision)
	}
	if pending := session.PendingPresetRevision; pending.Status == PresetRevisionFailed && pending.FailedAt.Equal(now.UTC()) {
		data["revision_event_type"] = string(EventPresetRevisionFailed)
		data["preset_revision"] = fmt.Sprintf("%d", pending.Number)
		data["base_preset_revision"] = fmt.Sprintf("%d", pending.BaseRevision)
		data["rollback_disposition"] = string(pending.RollbackDisposition)
	}
	if pending := session.PendingServerPresetRevision; pending.Status == PresetRevisionApplying && pending.ApplyStartedAt.Equal(now.UTC()) {
		data["server_revision_event_type"] = string(EventPresetRevisionApplying)
		data["server_preset_revision"] = fmt.Sprintf("%d", pending.Number)
		data["base_server_preset_revision"] = fmt.Sprintf("%d", pending.BaseRevision)
	}
	if active := session.ActiveServerPresetRevision; !active.Empty() && active.ActivatedAt.Equal(now.UTC()) {
		data["server_revision_event_type"] = string(EventPresetRevisionActivated)
		data["server_preset_revision"] = fmt.Sprintf("%d", active.Number)
		data["base_server_preset_revision"] = fmt.Sprintf("%d", active.BaseRevision)
	}
	if pending := session.PendingServerPresetRevision; pending.Status == PresetRevisionFailed && pending.FailedAt.Equal(now.UTC()) {
		data["server_revision_event_type"] = string(EventPresetRevisionFailed)
		data["server_preset_revision"] = fmt.Sprintf("%d", pending.Number)
		data["base_server_preset_revision"] = fmt.Sprintf("%d", pending.BaseRevision)
		data["server_rollback_disposition"] = string(pending.RollbackDisposition)
	}
}

func addPresetIntentEventData(data map[string]string, session Session) {
	if active := session.EffectiveActivePresetRevision(); !active.Empty() {
		data["active_preset_revision"] = fmt.Sprintf("%d", active.Number)
	}
	if pending := session.PendingPresetRevision; !pending.Empty() {
		data["pending_preset_revision"] = fmt.Sprintf("%d", pending.Number)
		data["pending_preset_revision_status"] = string(pending.Status)
	}
	if active := session.EffectiveActiveServerPresetRevision(); !active.Empty() {
		data["active_server_preset_revision"] = fmt.Sprintf("%d", active.Number)
	}
	if pending := session.PendingServerPresetRevision; !pending.Empty() {
		data["pending_server_preset_revision"] = fmt.Sprintf("%d", pending.Number)
		data["pending_server_preset_revision_status"] = string(pending.Status)
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
			"display_name":           session.DisplayName,
			"slug":                   session.Slug,
			"description":            session.Description,
			"configuration_revision": fmt.Sprintf("%d", session.ConfigurationRevision),
			"game_profile_id":        session.GameProfileID,
			"sleep_after_seconds":    fmt.Sprintf("%d", session.SleepAfterSeconds),
			"archive_after_seconds":  fmt.Sprintf("%d", session.ArchiveAfterSeconds),
			"teamspeak_enabled":      fmt.Sprintf("%t", session.TeamSpeakEnabled),
			"vanilla":                fmt.Sprintf("%t", session.Vanilla),
			"creator_dlcs":           strings.Join(session.CreatorDLCs, ","),
			"start_when_ready":       fmt.Sprintf("%t", session.StartWhenReady),
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
