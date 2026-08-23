package archive

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/app/failurestate"
	appreliability "github.com/L-McKendrick/game-server-platform/internal/app/reliability"
	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const (
	ActionDispatch           = "dispatch"
	ActionObserve            = "observe"
	ActionVerify             = "verify"
	ActionRecordVerified     = "record_verified"
	ActionTerminate          = "terminate_instance"
	ActionObserveTermination = "observe_termination"
	ActionDeleteVolume       = "delete_volume"
	ActionObserveVolume      = "observe_volume"
	ActionComplete           = "complete"
	ActionFail               = "fail"
)

type Clock interface{ Now() time.Time }
type IDGenerator interface {
	New(time.Time) (string, error)
}

type TaskRequest struct {
	Action            string `json:"action"`
	SessionID         string `json:"session_id"`
	WorkflowID        string `json:"workflow_id"`
	CorrelationID     string `json:"correlation_id"`
	CommandID         string `json:"command_id,omitempty"`
	ObjectKey         string `json:"object_key,omitempty"`
	SHA256            string `json:"sha256,omitempty"`
	SizeBytes         int64  `json:"size_bytes,omitempty"`
	ManifestObjectKey string `json:"manifest_object_key,omitempty"`
	ManifestSHA256    string `json:"manifest_sha256,omitempty"`
	ManifestSizeBytes int64  `json:"manifest_size_bytes,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	ErrorMessage      string `json:"error_message,omitempty"`
}

type TaskResult struct {
	SessionID         string `json:"session_id"`
	WorkflowID        string `json:"workflow_id"`
	State             string `json:"state"`
	CommandID         string `json:"command_id,omitempty"`
	Done              bool   `json:"done"`
	Succeeded         bool   `json:"succeeded"`
	ObjectKey         string `json:"object_key,omitempty"`
	SHA256            string `json:"sha256,omitempty"`
	SizeBytes         int64  `json:"size_bytes,omitempty"`
	ManifestObjectKey string `json:"manifest_object_key,omitempty"`
	ManifestSHA256    string `json:"manifest_sha256,omitempty"`
	ManifestSizeBytes int64  `json:"manifest_size_bytes,omitempty"`
	InstanceID        string `json:"instance_id,omitempty"`
	DataVolumeID      string `json:"data_volume_id,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	ErrorMessage      string `json:"error_message,omitempty"`
}

type Service struct {
	sessions      ports.SessionRepository
	workflows     ports.WorkflowRepository
	stages        ports.ProvisioningRepository
	runner        ports.ArchiveRunner
	store         ports.ArchiveStore
	destroyer     ports.InfrastructureDestroyer
	notifications ports.NotificationQueue
	ids           IDGenerator
	clock         Clock
}

func NewService(sessions ports.SessionRepository, workflows ports.WorkflowRepository, stages ports.ProvisioningRepository, runner ports.ArchiveRunner, store ports.ArchiveStore, destroyer ports.InfrastructureDestroyer, notifications ports.NotificationQueue, ids IDGenerator, clock Clock) (*Service, error) {
	if sessions == nil || workflows == nil || stages == nil || runner == nil || store == nil || destroyer == nil || ids == nil || clock == nil {
		return nil, fmt.Errorf("archive service dependencies are required")
	}
	return &Service{sessions: sessions, workflows: workflows, stages: stages, runner: runner, store: store, destroyer: destroyer, notifications: notifications, ids: ids, clock: clock}, nil
}

func (service *Service) Handle(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.load(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	if request.Action == ActionDispatch {
		if err := appreliability.HonorLoadedAtInitialBoundary(ctx, service.workflows, service.ids, service.clock, session, workflow); err != nil {
			return TaskResult{}, err
		}
	}
	switch request.Action {
	case ActionDispatch:
		commandID, err := service.runner.Start(ctx, session, workflow.ID)
		if err != nil {
			return TaskResult{}, err
		}
		result := taskResult(session, workflow)
		result.CommandID = commandID
		return result, nil
	case ActionObserve:
		return service.observe(ctx, session, workflow, request.CommandID)
	case ActionVerify:
		return service.verify(ctx, session, workflow, request)
	case ActionRecordVerified:
		return service.recordVerified(ctx, session, workflow, request)
	case ActionTerminate:
		return service.terminate(ctx, session, workflow)
	case ActionObserveTermination:
		return service.observeTermination(ctx, session, workflow)
	case ActionDeleteVolume:
		return service.deleteVolume(ctx, session, workflow)
	case ActionObserveVolume:
		return service.observeVolume(ctx, session, workflow)
	case ActionComplete:
		return service.complete(ctx, session, workflow, request)
	case ActionFail:
		return service.fail(ctx, session, workflow, request)
	default:
		return TaskResult{}, fmt.Errorf("unsupported archive action %q", request.Action)
	}
}

func (service *Service) observe(ctx context.Context, session domain.Session, workflow domain.Workflow, commandID string) (TaskResult, error) {
	status, err := service.runner.Observe(ctx, session.Infrastructure.InstanceID, commandID)
	if err != nil {
		return TaskResult{}, err
	}
	result := taskResult(session, workflow)
	result.CommandID = commandID
	result.Done = terminal(status.Status)
	result.Succeeded = status.Status == "Success"
	result.ObjectKey, result.SHA256, result.SizeBytes = status.ObjectKey, status.SHA256, status.SizeBytes
	if result.Succeeded {
		updated, progressErr := service.advanceProgress(ctx, session, workflow, domain.ProgressArchiveVerified)
		if progressErr != nil {
			return TaskResult{}, progressErr
		}
		session = updated
		result = taskResult(session, workflow)
		result.CommandID, result.Done, result.Succeeded = commandID, true, true
		result.ObjectKey, result.SHA256, result.SizeBytes = status.ObjectKey, status.SHA256, status.SizeBytes
	}
	if result.Done && !result.Succeeded {
		result.ErrorCode = "ERR_ARCHIVE_COMMAND"
		result.ErrorMessage = bounded(status.ErrorMessage, "archive command failed")
	}
	return result, nil
}

func (service *Service) verify(ctx context.Context, session domain.Session, workflow domain.Workflow, request TaskRequest) (TaskResult, error) {
	expectedKey := "sessions/" + session.ID + "/archives/" + workflow.ID + "/session.tar.gz"
	if request.ObjectKey != expectedKey {
		return TaskResult{}, fmt.Errorf("archive object key does not match workflow")
	}
	archiveObject := ports.ArchiveObject{Key: request.ObjectKey, SHA256: request.SHA256, SizeBytes: request.SizeBytes, ContentType: "application/gzip"}
	if err := service.store.Verify(ctx, archiveObject); err != nil {
		return TaskResult{}, err
	}
	manifest := domain.ArchiveManifest{
		SchemaVersion: 1, ArchiveID: workflow.ID, SessionID: session.ID,
		SessionName: session.DisplayName, SessionSlug: session.Slug, Description: session.Description,
		CreatedAt: workflow.StartedAt.UTC().Format(time.RFC3339Nano), Format: "tar+gzip",
		ObjectKey: request.ObjectKey, SHA256: request.SHA256, SizeBytes: request.SizeBytes,
		ContentRoots: archiveRoots(session), GameProfileID: session.GameProfileID,
		ConfigurationRevision: session.ConfigurationRevision, MissionObjectKey: session.MissionObjectKey,
		MissionFiles: append([]domain.MissionRecord(nil), session.MissionFiles...), ConfiguredMission: session.ConfiguredMission, CurrentMission: session.CurrentMission,
		PresetObjectKey: session.PresetObjectKey, SourceInstanceID: session.Infrastructure.InstanceID,
		PresetRevisionSequence: session.EffectivePresetRevisionSequence(),
		ActivePresetRevision:   domain.ArchivePresetRevisionSnapshot(session.EffectiveActivePresetRevision()),
		PendingPresetRevision:  domain.ArchivePresetRevisionSnapshot(session.PendingPresetRevision),
		Vanilla:                session.Vanilla,
		CreatorDLCs:            append([]string(nil), session.CreatorDLCs...),
		SourceDataVolumeID:     session.Infrastructure.DataVolumeID,
	}
	if err := manifest.Validate(); err != nil {
		return TaskResult{}, err
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return TaskResult{}, fmt.Errorf("marshal archive manifest: %w", err)
	}
	digest := sha256.Sum256(body)
	manifestKey := "sessions/" + session.ID + "/archives/" + workflow.ID + "/manifest.v1.json"
	manifestObject := ports.ArchiveObject{Key: manifestKey, SHA256: base64.StdEncoding.EncodeToString(digest[:]), SizeBytes: int64(len(body)), ContentType: "application/json"}
	if err := service.store.Put(ctx, manifestObject, body); err != nil {
		return TaskResult{}, err
	}
	if err := service.store.Verify(ctx, manifestObject); err != nil {
		return TaskResult{}, err
	}
	result := taskResult(session, workflow)
	result.Done, result.Succeeded = true, true
	result.ObjectKey, result.SHA256, result.SizeBytes = request.ObjectKey, request.SHA256, request.SizeBytes
	result.ManifestObjectKey = manifestKey
	result.ManifestSHA256 = manifestObject.SHA256
	result.ManifestSizeBytes = manifestObject.SizeBytes
	return result, nil
}

func (service *Service) recordVerified(ctx context.Context, session domain.Session, workflow domain.Workflow, request TaskRequest) (TaskResult, error) {
	if session.LifecycleState == domain.StateDestroying && session.Archive.ID == workflow.ID {
		return taskResult(session, workflow), nil
	}
	metadata := metadataFromRequest(workflow.ID, request, service.clock.Now().UTC())
	expectedVersion := session.Version
	if err := session.RecordVerifiedArchive(workflow.ID, metadata, service.clock.Now()); err != nil {
		return TaskResult{}, err
	}
	eventID, err := service.ids.New(service.clock.Now())
	if err != nil {
		return TaskResult{}, err
	}
	event := domain.NewArchiveEvent(eventID, domain.EventArchiveVerified, "VerifiedBeforeDestruction", workflow, session, metadata, service.clock.Now())
	if err := service.stages.SaveProvisioningStage(ctx, session, expectedVersion, event); err != nil {
		return TaskResult{}, err
	}
	service.notify(ctx, session, workflow)
	return taskResult(session, workflow), nil
}

func (service *Service) terminate(ctx context.Context, session domain.Session, workflow domain.Workflow) (TaskResult, error) {
	if err := service.verifyRecorded(ctx, session); err != nil {
		return TaskResult{}, err
	}
	if err := service.destroyer.TerminateInstance(ctx, session.ID, session.Infrastructure.InstanceID); err != nil {
		return TaskResult{}, err
	}
	return taskResult(session, workflow), nil
}

func (service *Service) observeTermination(ctx context.Context, session domain.Session, workflow domain.Workflow) (TaskResult, error) {
	done, err := service.destroyer.InstanceTerminated(ctx, session.ID, session.Infrastructure.InstanceID)
	if err != nil {
		return TaskResult{}, err
	}
	result := taskResult(session, workflow)
	result.Done, result.Succeeded = done, done
	return result, nil
}

func (service *Service) deleteVolume(ctx context.Context, session domain.Session, workflow domain.Workflow) (TaskResult, error) {
	if err := service.destroyer.DeleteVolume(ctx, session.ID, session.Infrastructure.DataVolumeID); err != nil {
		return TaskResult{}, err
	}
	return taskResult(session, workflow), nil
}

func (service *Service) observeVolume(ctx context.Context, session domain.Session, workflow domain.Workflow) (TaskResult, error) {
	done, err := service.destroyer.VolumeDeleted(ctx, session.ID, session.Infrastructure.DataVolumeID)
	if err != nil {
		return TaskResult{}, err
	}
	result := taskResult(session, workflow)
	result.Done, result.Succeeded = done, done
	return result, nil
}

func (service *Service) verifyRecorded(ctx context.Context, session domain.Session) error {
	if session.LifecycleState != domain.StateDestroying || session.Archive.Validate() != nil {
		return fmt.Errorf("verified archive is not durably recorded")
	}
	if err := service.store.Verify(ctx, ports.ArchiveObject{Key: session.Archive.ObjectKey, SHA256: session.Archive.SHA256, SizeBytes: session.Archive.SizeBytes, ContentType: "application/gzip"}); err != nil {
		return err
	}
	return service.store.Verify(ctx, ports.ArchiveObject{Key: session.Archive.ManifestObjectKey, SHA256: session.Archive.ManifestSHA256, SizeBytes: session.Archive.ManifestSizeBytes, ContentType: "application/json"})
}

func (service *Service) complete(ctx context.Context, session domain.Session, workflow domain.Workflow, request TaskRequest) (TaskResult, error) {
	if workflow.Status == domain.WorkflowSucceeded {
		result := taskResult(session, workflow)
		result.ObjectKey, result.SHA256, result.SizeBytes = session.Archive.ObjectKey, session.Archive.SHA256, session.Archive.SizeBytes
		result.ManifestObjectKey, result.ManifestSHA256 = session.Archive.ManifestObjectKey, session.Archive.ManifestSHA256
		return result, nil
	}
	expectedVersion := session.Version
	now := service.clock.Now().UTC()
	metadata := session.Archive
	slotID := session.Infrastructure.CapacitySlotID
	if slotID != "" {
		if err := service.stages.ReleaseCapacitySlot(ctx, slotID, session.ID); err != nil {
			return TaskResult{}, err
		}
	}
	session.ClearFailure()
	if err := session.CompleteArchive(workflow.ID, now); err != nil {
		return TaskResult{}, err
	}
	workflow.Status, workflow.CurrentStage, workflow.CompletedAt = domain.WorkflowSucceeded, "Verified", now
	eventID, err := service.ids.New(now)
	if err != nil {
		return TaskResult{}, err
	}
	event := domain.NewArchiveEvent(eventID, domain.EventInfrastructureDestroyed, "Archived", workflow, session, metadata, now)
	if err := service.workflows.CompleteWorkflow(ctx, session, expectedVersion, workflow, event); err != nil {
		return TaskResult{}, err
	}
	service.notify(ctx, session, workflow)
	result := taskResult(session, workflow)
	result.Done, result.Succeeded = true, true
	result.ObjectKey, result.SHA256, result.SizeBytes, result.ManifestObjectKey, result.ManifestSHA256, result.ManifestSizeBytes = metadata.ObjectKey, metadata.SHA256, metadata.SizeBytes, metadata.ManifestObjectKey, metadata.ManifestSHA256, metadata.ManifestSizeBytes
	return result, nil
}

func (service *Service) fail(ctx context.Context, session domain.Session, workflow domain.Workflow, request TaskRequest) (TaskResult, error) {
	if workflow.Status == domain.WorkflowFailed {
		return taskResult(session, workflow), nil
	}
	expectedVersion := session.Version
	now := service.clock.Now().UTC()
	if err := failurestate.Record(&session, workflow, request.ErrorCode, "ERR_ARCHIVE_FAILED", workflow.CurrentStage,
		"Archive processing stopped before every guarded stage was verified.", failurestate.Impact(session, true), now); err != nil {
		return TaskResult{}, err
	}
	if err := session.FailArchive(workflow.ID, now); err != nil {
		return TaskResult{}, err
	}
	workflow.Status, workflow.CurrentStage, workflow.CompletedAt = domain.WorkflowFailed, "Failed", now
	workflow.ErrorCode = bounded(request.ErrorCode, "ERR_ARCHIVE_FAILED")
	workflow.ErrorMessage = bounded(request.ErrorMessage, "archive workflow failed")
	eventID, err := service.ids.New(now)
	if err != nil {
		return TaskResult{}, err
	}
	event := domain.NewArchiveEvent(eventID, domain.EventArchiveFailed, "Failed", workflow, session, domain.ArchiveMetadata{}, now)
	if err := service.workflows.CompleteWorkflow(ctx, session, expectedVersion, workflow, event); err != nil {
		return TaskResult{}, err
	}
	service.notify(ctx, session, workflow)
	return taskResult(session, workflow), nil
}

func (service *Service) load(ctx context.Context, request TaskRequest) (domain.Session, domain.Workflow, error) {
	if strings.TrimSpace(request.SessionID) == "" || strings.TrimSpace(request.WorkflowID) == "" {
		return domain.Session{}, domain.Workflow{}, fmt.Errorf("session and workflow IDs are required")
	}
	session, err := service.sessions.Get(ctx, request.SessionID)
	if err != nil {
		return domain.Session{}, domain.Workflow{}, err
	}
	workflow, err := service.workflows.GetWorkflow(ctx, session.ID, request.WorkflowID)
	if err != nil {
		return domain.Session{}, domain.Workflow{}, err
	}
	active := session.ActiveWorkflowID == workflow.ID
	completed := workflow.Status == domain.WorkflowSucceeded && session.Archive.ID == workflow.ID
	failed := workflow.Status == domain.WorkflowFailed && session.LifecycleState == domain.StateFailed
	if workflow.Type != domain.ArchiveWorkflowType || (!active && !completed && !failed) {
		return domain.Session{}, domain.Workflow{}, domain.ErrConflict
	}
	return session, workflow, nil
}

func (service *Service) notify(ctx context.Context, session domain.Session, workflow domain.Workflow) {
	_ = sessioncard.EnqueueProgress(ctx, service.notifications, session, workflow, service.clock.Now().UTC())
}

func (service *Service) advanceProgress(ctx context.Context, session domain.Session, workflow domain.Workflow, milestone domain.ProgressMilestone) (domain.Session, error) {
	expected, now := session.Version, service.clock.Now().UTC()
	changed, err := session.AdvanceProgress(workflow.ID, milestone, now)
	if err != nil || !changed {
		return session, err
	}
	id, err := service.ids.New(now)
	if err != nil {
		return domain.Session{}, err
	}
	event := domain.NewProgressMilestoneEvent(id, workflow, session, now)
	if err := service.stages.SaveProvisioningStage(ctx, session, expected, event); err != nil {
		return domain.Session{}, err
	}
	service.notify(ctx, session, workflow)
	return session, nil
}

func taskResult(session domain.Session, workflow domain.Workflow) TaskResult {
	return TaskResult{SessionID: session.ID, WorkflowID: workflow.ID, State: string(session.LifecycleState), InstanceID: session.Infrastructure.InstanceID, DataVolumeID: session.Infrastructure.DataVolumeID}
}

func metadataFromRequest(id string, request TaskRequest, now time.Time) domain.ArchiveMetadata {
	return domain.ArchiveMetadata{ID: id, ObjectKey: request.ObjectKey, ManifestObjectKey: request.ManifestObjectKey, ManifestSHA256: request.ManifestSHA256, ManifestSizeBytes: request.ManifestSizeBytes, SHA256: request.SHA256, SizeBytes: request.SizeBytes, Format: "tar+gzip", VerifiedAt: now.UTC()}
}

func terminal(status string) bool {
	return status == "Success" || status == "Failed" || status == "TimedOut" || status == "Cancelled"
}
func archiveRoots(session domain.Session) []string {
	roots := []string{
		"/srv/game-server/config", "/srv/game-server/state", "/srv/game-server/logs",
		"/srv/game-server/arma3/mpmissions", "/srv/game-server/home/.local/share",
	}
	if session.TeamSpeakEnabled {
		roots = append(roots, "/srv/game-server/teamspeak")
	}
	return roots
}
func bounded(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if len(value) > 500 {
		return value[:500]
	}
	return value
}
