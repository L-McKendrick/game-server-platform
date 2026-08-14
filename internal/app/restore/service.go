package restore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/app/provisioning"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const (
	ActionVerifyArchive     = "verify_archive"
	ActionPrepare           = "prepare"
	ActionEnsure            = "ensure_instance"
	ActionObserveInstance   = "observe_instance"
	ActionCheckManaged      = "check_managed"
	ActionDispatchBootstrap = "dispatch_bootstrap"
	ActionObserveBootstrap  = "observe_bootstrap"
	ActionDispatchRestore   = "dispatch_restore"
	ActionObserveRestore    = "observe_restore"
	ActionComplete          = "complete"
	ActionFail              = "fail"
)

type Clock interface{ Now() time.Time }
type IDGenerator interface {
	New(time.Time) (string, error)
}
type Config = provisioning.Config

type TaskRequest struct {
	Action        string `json:"action"`
	SessionID     string `json:"session_id"`
	WorkflowID    string `json:"workflow_id"`
	CorrelationID string `json:"correlation_id"`
	CommandID     string `json:"command_id,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
}

type TaskResult struct {
	SessionID    string `json:"session_id"`
	WorkflowID   string `json:"workflow_id"`
	State        string `json:"state"`
	CommandID    string `json:"command_id,omitempty"`
	Ready        bool   `json:"ready,omitempty"`
	Managed      bool   `json:"managed,omitempty"`
	Done         bool   `json:"done,omitempty"`
	Succeeded    bool   `json:"succeeded,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	Warning      string `json:"warning,omitempty"`
}

type Service struct {
	sessions      ports.SessionRepository
	stages        ports.ProvisioningRepository
	workflows     ports.WorkflowRepository
	compute       ports.ComputeProvisioner
	bootstrap     ports.BootstrapRunner
	restore       ports.RestoreRunner
	store         ports.ArchiveStore
	notifications ports.NotificationQueue
	ids           IDGenerator
	clock         Clock
	config        Config
}

func NewService(sessions ports.SessionRepository, stages ports.ProvisioningRepository, workflows ports.WorkflowRepository, compute ports.ComputeProvisioner, bootstrap ports.BootstrapRunner, restore ports.RestoreRunner, store ports.ArchiveStore, notifications ports.NotificationQueue, ids IDGenerator, clock Clock, config Config) (*Service, error) {
	if sessions == nil || stages == nil || workflows == nil || compute == nil || bootstrap == nil || restore == nil || store == nil || ids == nil || clock == nil {
		return nil, fmt.Errorf("restore service dependencies are required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Service{sessions, stages, workflows, compute, bootstrap, restore, store, notifications, ids, clock, config}, nil
}

func (service *Service) Handle(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.load(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	switch request.Action {
	case ActionVerifyArchive:
		return service.verifyArchive(ctx, session, workflow)
	case ActionPrepare:
		return service.prepare(ctx, session, workflow)
	case ActionEnsure:
		return service.ensure(ctx, session, workflow)
	case ActionObserveInstance:
		return service.observeInstance(ctx, session, workflow)
	case ActionCheckManaged:
		return service.checkManaged(ctx, session, workflow)
	case ActionDispatchBootstrap:
		return service.dispatch(ctx, session, workflow, service.bootstrap)
	case ActionObserveBootstrap:
		return service.observeCommand(ctx, session, workflow, request.CommandID, service.bootstrap)
	case ActionDispatchRestore:
		return service.dispatch(ctx, session, workflow, service.restore)
	case ActionObserveRestore:
		return service.observeCommand(ctx, session, workflow, request.CommandID, service.restore)
	case ActionComplete:
		return service.complete(ctx, session, workflow)
	case ActionFail:
		return service.fail(ctx, session, workflow, request)
	default:
		return TaskResult{}, fmt.Errorf("unsupported restore action %q", request.Action)
	}
}

type commandRunner interface {
	Start(context.Context, domain.Session) (string, error)
	Observe(context.Context, string, string) (ports.BootstrapCommandStatus, error)
}

func (service *Service) verifyArchive(ctx context.Context, session domain.Session, workflow domain.Workflow) (TaskResult, error) {
	archive := session.Archive
	manifestObject := ports.ArchiveObject{Key: archive.ManifestObjectKey, SHA256: archive.ManifestSHA256, SizeBytes: archive.ManifestSizeBytes, ContentType: "application/json"}
	body, err := service.store.Get(ctx, manifestObject)
	if err != nil {
		return TaskResult{}, err
	}
	var manifest domain.ArchiveManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return TaskResult{}, fmt.Errorf("decode archive manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return TaskResult{}, err
	}
	if manifest.SessionID != session.ID || manifest.ArchiveID != archive.ID || manifest.ObjectKey != archive.ObjectKey || manifest.SHA256 != archive.SHA256 || manifest.SizeBytes != archive.SizeBytes || manifest.GameProfileID != session.GameProfileID || manifest.ConfigurationRevision != session.ConfigurationRevision || manifest.MissionObjectKey != session.MissionObjectKey || manifest.PresetObjectKey != session.PresetObjectKey || manifest.Vanilla != session.Vanilla {
		return TaskResult{}, fmt.Errorf("archive manifest does not match authoritative session metadata")
	}
	if err := service.store.Verify(ctx, ports.ArchiveObject{Key: archive.ObjectKey, SHA256: archive.SHA256, SizeBytes: archive.SizeBytes, ContentType: "application/gzip"}); err != nil {
		return TaskResult{}, err
	}
	return result(session, workflow), nil
}

func (service *Service) prepare(ctx context.Context, session domain.Session, workflow domain.Workflow) (TaskResult, error) {
	if session.Infrastructure.CapacitySlotID != "" {
		return result(session, workflow), nil
	}
	slot, err := service.stages.AcquireCapacitySlot(ctx, session.ID, workflow.ID, service.config.MaxProvisioned, service.clock.Now())
	if err != nil {
		return TaskResult{}, err
	}
	expected := session.Version
	if err := session.RecordRestoreCapacity(workflow.ID, slot, service.clock.Now()); err != nil {
		return TaskResult{}, err
	}
	if err := service.saveStage(ctx, session, expected, workflow, "CapacityReserved"); err != nil {
		return TaskResult{}, err
	}
	return result(session, workflow), nil
}

func (service *Service) ensure(ctx context.Context, session domain.Session, workflow domain.Workflow) (TaskResult, error) {
	observation, err := service.compute.EnsureInstance(ctx, service.launchRequest(session, workflow), session.Infrastructure.InstanceID)
	if err != nil {
		return TaskResult{}, err
	}
	if observation.InstanceID != "" && session.Infrastructure.InstanceID != observation.InstanceID {
		infrastructure := service.infrastructure(session, observation)
		expected := session.Version
		if err := session.RecordRestoreInfrastructure(workflow.ID, infrastructure, service.clock.Now()); err != nil {
			return TaskResult{}, err
		}
		if err := service.saveStage(ctx, session, expected, workflow, "InstanceLaunched"); err != nil {
			return TaskResult{}, err
		}
	}
	return result(session, workflow), nil
}

func (service *Service) observeInstance(ctx context.Context, session domain.Session, workflow domain.Workflow) (TaskResult, error) {
	observation, err := service.compute.ObserveInstance(ctx, session.Infrastructure.InstanceID)
	if err != nil {
		return TaskResult{}, err
	}
	ready := observation.State == "running" && observation.DataVolumeID != ""
	if ready && (session.Infrastructure.DataVolumeID != observation.DataVolumeID || session.Infrastructure.PublicIPv4 != observation.PublicIPv4) {
		expected := session.Version
		if err := session.RecordRestoreInfrastructure(workflow.ID, service.infrastructure(session, observation), service.clock.Now()); err != nil {
			return TaskResult{}, err
		}
		if err := service.saveStage(ctx, session, expected, workflow, "InstanceRunning"); err != nil {
			return TaskResult{}, err
		}
	}
	response := result(session, workflow)
	response.Ready = ready
	return response, nil
}

func (service *Service) checkManaged(ctx context.Context, session domain.Session, workflow domain.Workflow) (TaskResult, error) {
	managed, err := service.compute.IsManaged(ctx, session.Infrastructure.InstanceID)
	if err != nil {
		return TaskResult{}, err
	}
	response := result(session, workflow)
	response.Managed = managed
	return response, nil
}

func (service *Service) dispatch(ctx context.Context, session domain.Session, workflow domain.Workflow, runner commandRunner) (TaskResult, error) {
	commandID, err := runner.Start(ctx, session)
	if err != nil {
		return TaskResult{}, err
	}
	response := result(session, workflow)
	response.CommandID = commandID
	return response, nil
}

func (service *Service) observeCommand(ctx context.Context, session domain.Session, workflow domain.Workflow, commandID string, runner commandRunner) (TaskResult, error) {
	status, err := runner.Observe(ctx, session.Infrastructure.InstanceID, commandID)
	if err != nil {
		return TaskResult{}, err
	}
	response := result(session, workflow)
	response.CommandID = commandID
	response.Done = terminal(status.Status)
	response.Succeeded = status.Status == "Success"
	if response.Done && !response.Succeeded {
		response.ErrorCode = "ERR_RESTORE_COMMAND"
		response.ErrorMessage = bounded(status.ErrorMessage, "restore command failed")
	}
	return response, nil
}

func (service *Service) complete(ctx context.Context, session domain.Session, workflow domain.Workflow) (TaskResult, error) {
	if workflow.Status == domain.WorkflowSucceeded {
		response := result(session, workflow)
		response.Succeeded = true
		return response, nil
	}
	expected, now := session.Version, service.clock.Now().UTC()
	if err := session.CompleteRestore(workflow.ID, now); err != nil {
		return TaskResult{}, err
	}
	workflow.Status, workflow.CurrentStage, workflow.CompletedAt = domain.WorkflowSucceeded, "Restored", now
	eventID, err := service.ids.New(now)
	if err != nil {
		return TaskResult{}, err
	}
	event := domain.NewRestoreEvent(eventID, domain.EventSessionRestored, "Restored", workflow, session, now)
	if err := service.workflows.CompleteWorkflow(ctx, session, expected, workflow, event); err != nil {
		return TaskResult{}, err
	}
	response := result(session, workflow)
	response.Succeeded = true
	if service.notifications != nil {
		id, idErr := service.ids.New(now)
		if idErr == nil {
			response.Warning = notificationWarning(service.notifications.Enqueue(ctx, domain.NotificationRequest{SchemaVersion: 1, NotificationID: id, SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID, Content: "**Session restored**\nSession: `" + session.ID + "`\nA new host and data volume passed archive, bootstrap, and service-health verification.", CorrelationID: workflow.CorrelationID, RequestedAt: now}))
		}
	}
	return response, nil
}

func (service *Service) fail(ctx context.Context, session domain.Session, workflow domain.Workflow, request TaskRequest) (TaskResult, error) {
	if workflow.Status == domain.WorkflowFailed {
		return result(session, workflow), nil
	}
	slotID, release := session.Infrastructure.CapacitySlotID, false
	if session.Infrastructure.InstanceID == "" && slotID != "" {
		observation, found, err := service.compute.FindInstance(ctx, service.launchRequest(session, workflow))
		if err == nil && found {
			session.Infrastructure = service.infrastructure(session, observation)
		} else if err == nil {
			session.Infrastructure = domain.Infrastructure{}
			release = true
		}
	}
	expected, now := session.Version, service.clock.Now().UTC()
	if release {
		if err := service.stages.ReleaseCapacitySlot(ctx, slotID, session.ID); err != nil {
			return TaskResult{}, err
		}
	}
	if err := session.FailRestore(workflow.ID, now); err != nil {
		return TaskResult{}, err
	}
	workflow.Status, workflow.CurrentStage, workflow.CompletedAt = domain.WorkflowFailed, "Failed", now
	workflow.ErrorCode, workflow.ErrorMessage = bounded(request.ErrorCode, "ERR_RESTORE_FAILED"), bounded(request.ErrorMessage, "restore workflow failed")
	eventID, err := service.ids.New(now)
	if err != nil {
		return TaskResult{}, err
	}
	event := domain.NewRestoreEvent(eventID, domain.EventRestoreFailed, "Failed", workflow, session, now)
	if err := service.workflows.CompleteWorkflow(ctx, session, expected, workflow, event); err != nil {
		return TaskResult{}, err
	}
	response := result(session, workflow)
	return response, nil
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
	active := session.ActiveWorkflowID == workflow.ID && session.ActiveWorkflowType == domain.RestoreWorkflowType
	completed := workflow.Status == domain.WorkflowSucceeded && session.LifecycleState == domain.StateRunning
	failed := workflow.Status == domain.WorkflowFailed && session.LifecycleState == domain.StateFailed
	if workflow.Type != domain.RestoreWorkflowType || (!active && !completed && !failed) {
		return domain.Session{}, domain.Workflow{}, domain.ErrConflict
	}
	return session, workflow, nil
}

func (service *Service) saveStage(ctx context.Context, session domain.Session, expected int64, workflow domain.Workflow, stage string) error {
	id, err := service.ids.New(service.clock.Now())
	if err != nil {
		return err
	}
	return service.stages.SaveProvisioningStage(ctx, session, expected, domain.NewRestoreEvent(id, domain.EventRestoreStage, stage, workflow, session, service.clock.Now()))
}

func (service *Service) launchRequest(session domain.Session, workflow domain.Workflow) domain.ComputeLaunchRequest {
	securityGroups := []string{service.config.GameSecurityGroupID}
	if session.TeamSpeakEnabled && service.config.VoiceSecurityGroupID != "" {
		securityGroups = append(securityGroups, service.config.VoiceSecurityGroupID)
	}
	token := "restore-" + session.ID + "-" + workflow.ID
	if len(token) > 64 {
		token = token[:64]
	}
	return domain.ComputeLaunchRequest{SessionID: session.ID, GameType: session.GameType, Environment: service.config.Environment, Project: service.config.Project, AMIID: service.config.AMIID, InstanceType: service.config.InstanceType, SubnetID: service.config.SubnetID, SecurityGroupIDs: securityGroups, InstanceProfile: service.config.InstanceProfile, RootVolumeGiB: service.config.RootVolumeGiB, DataVolumeGiB: service.config.DataVolumeGiB, ClientToken: token}
}

func (service *Service) infrastructure(session domain.Session, observation domain.ComputeObservation) domain.Infrastructure {
	securityGroups := []string{service.config.GameSecurityGroupID}
	if session.TeamSpeakEnabled && service.config.VoiceSecurityGroupID != "" {
		securityGroups = append(securityGroups, service.config.VoiceSecurityGroupID)
	}
	return domain.Infrastructure{CapacitySlotID: session.Infrastructure.CapacitySlotID, AvailabilityZone: observation.AvailabilityZone, SubnetID: service.config.SubnetID, SecurityGroupIDs: securityGroups, InstanceProfile: service.config.InstanceProfile, AMIID: service.config.AMIID, InstanceType: service.config.InstanceType, InstanceID: observation.InstanceID, DataVolumeID: observation.DataVolumeID, PublicIPv4: observation.PublicIPv4, LastObservedAt: service.clock.Now().UTC()}
}

func result(session domain.Session, workflow domain.Workflow) TaskResult {
	return TaskResult{SessionID: session.ID, WorkflowID: workflow.ID, State: string(session.LifecycleState)}
}
func terminal(status string) bool {
	return status == "Success" || status == "Failed" || status == "TimedOut" || status == "Cancelled"
}
func bounded(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if len(value) > 500 {
		value = value[:500]
	}
	return value
}
func notificationWarning(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
