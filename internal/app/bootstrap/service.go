package bootstrap

import (
	"context"
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
	ActionPrepare          = "prepare"
	ActionDispatch         = "dispatch"
	ActionObserve          = "observe"
	ActionComplete         = "complete"
	ActionFail             = "fail"
	ActionRollbackDispatch = "dispatch_rollback"
	ActionRollbackObserve  = "observe_rollback"
)

type Clock interface{ Now() time.Time }
type IDGenerator interface {
	New(time.Time) (string, error)
}

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
	CommandID    string `json:"command_id,omitempty"`
	State        string `json:"state,omitempty"`
	Status       string `json:"status,omitempty"`
	Done         bool   `json:"done"`
	Succeeded    bool   `json:"succeeded"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	Warning      string `json:"warning,omitempty"`
}

type Service struct {
	sessions      ports.SessionRepository
	stages        ports.BootstrapRepository
	workflows     ports.WorkflowRepository
	runner        ports.PresetRevisionRunner
	notifications ports.NotificationQueue
	ids           IDGenerator
	clock         Clock
}

func NewService(sessions ports.SessionRepository, stages ports.BootstrapRepository, workflows ports.WorkflowRepository, runner ports.PresetRevisionRunner, notifications ports.NotificationQueue, ids IDGenerator, clock Clock) (*Service, error) {
	if sessions == nil || stages == nil || workflows == nil || runner == nil || ids == nil || clock == nil {
		return nil, fmt.Errorf("bootstrap dependencies are required")
	}
	return &Service{sessions: sessions, stages: stages, workflows: workflows, runner: runner, notifications: notifications, ids: ids, clock: clock}, nil
}

func (service *Service) Handle(ctx context.Context, request TaskRequest) (TaskResult, error) {
	if strings.TrimSpace(request.SessionID) == "" || strings.TrimSpace(request.WorkflowID) == "" {
		return TaskResult{}, fmt.Errorf("session and workflow IDs are required")
	}
	if request.Action == ActionPrepare {
		session, workflow, err := service.load(ctx, request)
		if err != nil {
			return TaskResult{}, err
		}
		if err := appreliability.HonorLoadedAtInitialBoundary(ctx, service.workflows, service.ids, service.clock, session, workflow); err != nil {
			return TaskResult{}, err
		}
	}
	switch request.Action {
	case ActionPrepare:
		return service.prepare(ctx, request)
	case ActionDispatch:
		return service.dispatch(ctx, request)
	case ActionObserve:
		return service.observe(ctx, request)
	case ActionComplete:
		return service.complete(ctx, request)
	case ActionFail:
		return service.fail(ctx, request)
	case ActionRollbackDispatch:
		return service.dispatchRollback(ctx, request)
	case ActionRollbackObserve:
		return service.observeRollback(ctx, request)
	default:
		return TaskResult{}, fmt.Errorf("unsupported bootstrap action %q", request.Action)
	}
}

func (service *Service) dispatchRollback(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.load(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	result := taskResult(session, workflow)
	if !session.HasApplyingPresetRevision(workflow.ID) {
		result.Done, result.Succeeded = true, true
		return result, nil
	}
	expected := session.Version
	changed, progressErr := session.SetProgressState(workflow.ID, domain.ProgressRollingBack, service.clock.Now())
	if progressErr != nil {
		return TaskResult{}, progressErr
	}
	if changed {
		if progressErr := service.saveProgress(ctx, session, expected, workflow); progressErr != nil {
			return TaskResult{}, progressErr
		}
		result = taskResult(session, workflow)
		service.notify(ctx, &result, session, workflow)
	}
	commandID, err := service.runner.StartRollback(ctx, session)
	if err != nil {
		return TaskResult{}, fmt.Errorf("start preset rollback: %w", err)
	}
	result.CommandID, result.Status = commandID, "PENDING"
	return result, nil
}

func (service *Service) observeRollback(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.load(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	result := taskResult(session, workflow)
	if !session.HasApplyingPresetRevision(workflow.ID) {
		result.Done, result.Succeeded = true, true
		return result, nil
	}
	status, err := service.runner.Observe(ctx, session.Infrastructure.InstanceID, strings.TrimSpace(request.CommandID))
	if err != nil {
		return TaskResult{}, fmt.Errorf("observe preset rollback: %w", err)
	}
	result.CommandID, result.Status = request.CommandID, status.Status
	result.Done = status.Status == "Success" || status.Status == "Failed" || status.Status == "TimedOut" || status.Status == "Cancelled"
	result.Succeeded = status.Status == "Success"
	if !result.Done {
		return result, nil
	}
	expected, now := session.Version, service.clock.Now().UTC()
	changed, err := session.RecordPresetRevisionRollback(workflow.ID, result.Succeeded, status.ErrorMessage, now)
	if err != nil {
		return TaskResult{}, err
	}
	if changed {
		id, err := service.ids.New(now)
		if err != nil {
			return TaskResult{}, err
		}
		event := domain.NewPresetRevisionEvent(id, domain.EventPresetRevisionRolledBack, workflow.CorrelationID, domain.Actor{Type: domain.ActorTypeSystem, ID: domain.BootstrapWorkflowType}, session, session.PendingPresetRevision, now)
		if err := service.stages.SaveBootstrapStage(ctx, session, expected, event); err != nil {
			return TaskResult{}, err
		}
	}
	if !result.Succeeded {
		result.ErrorCode, result.ErrorMessage = "ERR_MOD_ROLLBACK_"+strings.ToUpper(status.Status), bounded(status.ErrorMessage, 500, "known-good mod rollback failed")
	}
	return result, nil
}

func (service *Service) prepare(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.load(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	if session.LifecycleState == domain.StateInstalling {
		return taskResult(session, workflow), nil
	}
	expectedVersion := session.Version
	if err := session.BeginBootstrapInstallation(workflow.ID, service.clock.Now()); err != nil {
		return TaskResult{}, err
	}
	if err := service.saveStage(ctx, session, expectedVersion, workflow, "InstallationStarted"); err != nil {
		return TaskResult{}, err
	}
	result := taskResult(session, workflow)
	service.notify(ctx, &result, session, workflow)
	return result, nil
}

func (service *Service) dispatch(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.load(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	if session.LifecycleState != domain.StateInstalling || session.Infrastructure.InstanceID == "" {
		return TaskResult{}, fmt.Errorf("%w: bootstrap dispatch requires an installing managed instance", domain.ErrInvalidTransition)
	}
	commandID, err := service.runner.Start(ctx, session)
	if err != nil {
		return TaskResult{}, fmt.Errorf("start bootstrap command: %w", err)
	}
	result := taskResult(session, workflow)
	result.CommandID = commandID
	result.Status = "PENDING"
	return result, nil
}

func (service *Service) observe(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.load(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	commandID := strings.TrimSpace(request.CommandID)
	if commandID == "" {
		return TaskResult{}, fmt.Errorf("bootstrap command ID is required")
	}
	status, err := service.runner.Observe(ctx, session.Infrastructure.InstanceID, commandID)
	if err != nil {
		return TaskResult{}, fmt.Errorf("observe bootstrap command: %w", err)
	}
	result := taskResult(session, workflow)
	result.CommandID = commandID
	result.Status = status.Status
	checkpoints := status.Checkpoints
	if status.Status == "Success" {
		ordered, _ := domain.MilestonesForWorkflow(domain.BootstrapWorkflowType)
		checkpoints = ordered[1 : len(ordered)-1]
	}
	expectedVersion := session.Version
	var skipped []domain.ProgressMilestone
	if session.Vanilla {
		skipped = []domain.ProgressMilestone{domain.ProgressModsApplied}
	}
	progressChanged, progressErr := session.ApplyProgressSequence(workflow.ID, checkpoints, skipped, service.clock.Now())
	if progressErr != nil {
		return TaskResult{}, progressErr
	}
	if progressChanged {
		if progressErr := service.saveProgress(ctx, session, expectedVersion, workflow); progressErr != nil {
			return TaskResult{}, progressErr
		}
		service.notify(ctx, &result, session, workflow)
	}
	switch status.Status {
	case "Success":
		result.Done, result.Succeeded = true, true
	case "Cancelled", "Cancelling", "Failed", "TimedOut":
		result.Done = true
		result.ErrorCode = strings.TrimSpace(status.ErrorCode)
		if result.ErrorCode == "" {
			result.ErrorCode = "ERR_BOOTSTRAP_COMMAND_" + strings.ToUpper(status.Status)
		}
		result.ErrorMessage = bounded(status.ErrorMessage, 500, "managed bootstrap command failed")
	}
	return result, nil
}

func (service *Service) complete(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.loadRecords(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	if workflow.Status == domain.WorkflowSucceeded {
		result := taskResult(session, workflow)
		if err := sessioncard.EnqueueActivatedModlist(ctx, service.notifications, session, workflow, service.clock.Now().UTC()); err != nil {
			result.Warning = err.Error()
		}
		return result, nil
	}
	if session.ActiveWorkflowID != workflow.ID || workflow.Type != domain.BootstrapWorkflowType {
		return TaskResult{}, domain.ErrConflict
	}
	expectedVersion := session.Version
	now := service.clock.Now().UTC()
	session.ClearFailure()
	if err := session.CompleteBootstrap(workflow.ID, now); err != nil {
		return TaskResult{}, err
	}
	workflow.Status = domain.WorkflowSucceeded
	workflow.CurrentStage = "GameServerReady"
	workflow.CompletedAt = now
	event, err := service.event(now, domain.EventGameServerReady, "GameServerReady", workflow, session)
	if err != nil {
		return TaskResult{}, err
	}
	if err := service.workflows.CompleteWorkflow(ctx, session, expectedVersion, workflow, event); err != nil {
		return TaskResult{}, err
	}
	result := taskResult(session, workflow)
	service.notify(ctx, &result, session, workflow)
	if err := sessioncard.EnqueueActivatedModlist(ctx, service.notifications, session, workflow, now); err != nil {
		if result.Warning != "" {
			result.Warning += "; "
		}
		result.Warning += err.Error()
	}
	return result, nil
}

func (service *Service) fail(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.loadRecords(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	if workflow.Status == domain.WorkflowFailed {
		return taskResult(session, workflow), nil
	}
	if session.ActiveWorkflowID != workflow.ID || workflow.Type != domain.BootstrapWorkflowType {
		return TaskResult{}, domain.ErrConflict
	}
	expectedVersion := session.Version
	now := service.clock.Now().UTC()
	if err := session.FailPresetRevisionApplication(workflow.ID, request.ErrorMessage, now); err != nil {
		return TaskResult{}, err
	}
	if err := failurestate.Record(&session, workflow, request.ErrorCode, "ERR_BOOTSTRAP_FAILED", workflow.CurrentStage,
		"Game and content setup stopped before health verification completed.", failurestate.Impact(session, false), now); err != nil {
		return TaskResult{}, err
	}
	if err := session.FailBootstrap(workflow.ID, now); err != nil {
		return TaskResult{}, err
	}
	workflow.Status = domain.WorkflowFailed
	workflow.CurrentStage = "Failed"
	workflow.ErrorCode = bounded(request.ErrorCode, 100, "ERR_BOOTSTRAP_FAILED")
	workflow.ErrorMessage = bounded(request.ErrorMessage, 500, "game server bootstrap failed")
	workflow.CompletedAt = now
	event, err := service.event(now, domain.EventBootstrapFailed, "Failed", workflow, session)
	if err != nil {
		return TaskResult{}, err
	}
	if err := service.workflows.CompleteWorkflow(ctx, session, expectedVersion, workflow, event); err != nil {
		return TaskResult{}, err
	}
	result := taskResult(session, workflow)
	service.notify(ctx, &result, session, workflow)
	return result, nil
}

func (service *Service) notify(ctx context.Context, result *TaskResult, session domain.Session, workflow domain.Workflow) {
	if err := sessioncard.EnqueueProgress(ctx, service.notifications, session, workflow, service.clock.Now().UTC()); err != nil {
		result.Warning = err.Error()
	}
}

func (service *Service) saveProgress(ctx context.Context, session domain.Session, expectedVersion int64, workflow domain.Workflow) error {
	now := service.clock.Now().UTC()
	event, err := service.event(now, domain.EventProgressMilestone, string(session.Progress.Milestone), workflow, session)
	if err != nil {
		return err
	}
	return service.stages.SaveBootstrapStage(ctx, session, expectedVersion, event)
}

func (service *Service) saveStage(ctx context.Context, session domain.Session, expectedVersion int64, workflow domain.Workflow, stage string) error {
	now := service.clock.Now().UTC()
	event, err := service.event(now, domain.EventBootstrapStage, stage, workflow, session)
	if err != nil {
		return err
	}
	return service.stages.SaveBootstrapStage(ctx, session, expectedVersion, event)
}

func (service *Service) event(now time.Time, eventType domain.EventType, stage string, workflow domain.Workflow, session domain.Session) (domain.SessionEvent, error) {
	id, err := service.ids.New(now)
	if err != nil {
		return domain.SessionEvent{}, err
	}
	return domain.NewBootstrapEvent(id, eventType, stage, workflow, session, now), nil
}

func (service *Service) load(ctx context.Context, request TaskRequest) (domain.Session, domain.Workflow, error) {
	session, workflow, err := service.loadRecords(ctx, request)
	if err != nil {
		return domain.Session{}, domain.Workflow{}, err
	}
	if session.ActiveWorkflowID != workflow.ID || workflow.Type != domain.BootstrapWorkflowType {
		return domain.Session{}, domain.Workflow{}, domain.ErrConflict
	}
	return session, workflow, nil
}

func (service *Service) loadRecords(ctx context.Context, request TaskRequest) (domain.Session, domain.Workflow, error) {
	session, err := service.sessions.Get(ctx, strings.TrimSpace(request.SessionID))
	if err != nil {
		return domain.Session{}, domain.Workflow{}, err
	}
	workflow, err := service.workflows.GetWorkflow(ctx, session.ID, strings.TrimSpace(request.WorkflowID))
	return session, workflow, err
}

func taskResult(session domain.Session, workflow domain.Workflow) TaskResult {
	return TaskResult{SessionID: session.ID, WorkflowID: workflow.ID, State: string(session.LifecycleState)}
}

func bounded(value string, maximum int, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if len(value) > maximum {
		value = value[:maximum]
	}
	return value
}
