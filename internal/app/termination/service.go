package termination

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const (
	ActionTerminateInstance  = "terminate_instance"
	ActionObserveTermination = "observe_termination"
	ActionDeleteVolume       = "delete_volume"
	ActionObserveVolume      = "observe_volume"
	ActionDeleteObjects      = "delete_objects"
	ActionComplete           = "complete"
	ActionFail               = "fail"
)

type Clock interface{ Now() time.Time }
type IDGenerator interface {
	New(time.Time) (string, error)
}

type TaskRequest struct {
	Action         string `json:"action"`
	SessionID      string `json:"session_id"`
	WorkflowID     string `json:"workflow_id"`
	CorrelationID  string `json:"correlation_id"`
	ObjectsDeleted int    `json:"objects_deleted"`
	ErrorCode      string `json:"error_code,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
}

type TaskResult struct {
	SessionID      string `json:"session_id"`
	WorkflowID     string `json:"workflow_id"`
	State          string `json:"state"`
	Done           bool   `json:"done"`
	Succeeded      bool   `json:"succeeded"`
	ObjectsDeleted int    `json:"objects_deleted,omitempty"`
}

type Service struct {
	sessions      ports.SessionRepository
	workflows     ports.WorkflowRepository
	stages        ports.ProvisioningRepository
	destroyer     ports.InfrastructureDestroyer
	cleaner       ports.SessionObjectCleaner
	notifications ports.NotificationQueue
	ids           IDGenerator
	clock         Clock
}

func NewService(sessions ports.SessionRepository, workflows ports.WorkflowRepository, stages ports.ProvisioningRepository, destroyer ports.InfrastructureDestroyer, cleaner ports.SessionObjectCleaner, notifications ports.NotificationQueue, ids IDGenerator, clock Clock) (*Service, error) {
	if sessions == nil || workflows == nil || stages == nil || destroyer == nil || cleaner == nil || ids == nil || clock == nil {
		return nil, fmt.Errorf("termination service dependencies are required")
	}
	return &Service{sessions: sessions, workflows: workflows, stages: stages, destroyer: destroyer, cleaner: cleaner, notifications: notifications, ids: ids, clock: clock}, nil
}

func (service *Service) Handle(ctx context.Context, request TaskRequest) (TaskResult, error) {
	session, workflow, err := service.load(ctx, request)
	if err != nil {
		return TaskResult{}, err
	}
	switch request.Action {
	case ActionTerminateInstance:
		if session.Infrastructure.InstanceID != "" {
			err = service.destroyer.TerminateInstance(ctx, session.ID, session.Infrastructure.InstanceID)
		}
	case ActionObserveTermination:
		return service.observeInstance(ctx, session, workflow)
	case ActionDeleteVolume:
		if session.Infrastructure.DataVolumeID != "" {
			err = service.destroyer.DeleteVolume(ctx, session.ID, session.Infrastructure.DataVolumeID)
		}
	case ActionObserveVolume:
		return service.observeVolume(ctx, session, workflow)
	case ActionDeleteObjects:
		return service.deleteObjects(ctx, session, workflow)
	case ActionComplete:
		return service.complete(ctx, session, workflow, request.ObjectsDeleted)
	case ActionFail:
		return service.fail(ctx, session, workflow, request)
	default:
		return TaskResult{}, fmt.Errorf("unsupported termination action %q", request.Action)
	}
	if err != nil {
		return TaskResult{}, err
	}
	return taskResult(session, workflow), nil
}

func (service *Service) observeInstance(ctx context.Context, session domain.Session, workflow domain.Workflow) (TaskResult, error) {
	done := session.Infrastructure.InstanceID == ""
	var err error
	if !done {
		done, err = service.destroyer.InstanceTerminated(ctx, session.ID, session.Infrastructure.InstanceID)
	}
	result := taskResult(session, workflow)
	result.Done, result.Succeeded = done, done
	return result, err
}

func (service *Service) observeVolume(ctx context.Context, session domain.Session, workflow domain.Workflow) (TaskResult, error) {
	done := session.Infrastructure.DataVolumeID == ""
	var err error
	if !done {
		done, err = service.destroyer.VolumeDeleted(ctx, session.ID, session.Infrastructure.DataVolumeID)
	}
	result := taskResult(session, workflow)
	result.Done, result.Succeeded = done, done
	return result, err
}

func (service *Service) deleteObjects(ctx context.Context, session domain.Session, workflow domain.Workflow) (TaskResult, error) {
	count, err := service.cleaner.DeleteSessionObjects(ctx, session.ID)
	if err != nil {
		return TaskResult{}, err
	}
	result := taskResult(session, workflow)
	result.Done, result.Succeeded, result.ObjectsDeleted = true, true, count
	return result, nil
}

func (service *Service) complete(ctx context.Context, session domain.Session, workflow domain.Workflow, objectsDeleted int) (TaskResult, error) {
	if workflow.Status == domain.WorkflowSucceeded && session.LifecycleState == domain.StateDeleted {
		result := taskResult(session, workflow)
		result.Done, result.Succeeded = true, true
		return result, nil
	}
	expectedVersion := session.Version
	now := service.clock.Now().UTC()
	if slotID := session.Infrastructure.CapacitySlotID; slotID != "" {
		if err := service.stages.ReleaseCapacitySlot(ctx, slotID, session.ID); err != nil {
			return TaskResult{}, err
		}
	}
	if err := session.CompleteTermination(workflow.ID, now); err != nil {
		return TaskResult{}, err
	}
	workflow.Status, workflow.CurrentStage, workflow.CompletedAt = domain.WorkflowSucceeded, "Deleted", now
	eventID, err := service.ids.New(now)
	if err != nil {
		return TaskResult{}, err
	}
	event := domain.NewTerminationEvent(eventID, domain.EventSessionTerminated, "Deleted", workflow, session, objectsDeleted, now)
	if err := service.workflows.CompleteWorkflow(ctx, session, expectedVersion, workflow, event); err != nil {
		return TaskResult{}, err
	}
	service.notify(ctx, session, workflow)
	result := taskResult(session, workflow)
	result.Done, result.Succeeded, result.ObjectsDeleted = true, true, objectsDeleted
	return result, nil
}

func (service *Service) fail(ctx context.Context, session domain.Session, workflow domain.Workflow, request TaskRequest) (TaskResult, error) {
	if workflow.Status == domain.WorkflowFailed {
		return taskResult(session, workflow), nil
	}
	if workflow.Status == domain.WorkflowSucceeded && session.LifecycleState == domain.StateDeleted {
		result := taskResult(session, workflow)
		result.Done, result.Succeeded = true, true
		return result, nil
	}
	expectedVersion := session.Version
	now := service.clock.Now().UTC()
	if err := session.FailTermination(workflow.ID, now); err != nil {
		return TaskResult{}, err
	}
	workflow.Status, workflow.CurrentStage, workflow.CompletedAt = domain.WorkflowFailed, "Failed", now
	workflow.ErrorCode = bounded(request.ErrorCode, "ERR_TERMINATION_FAILED")
	workflow.ErrorMessage = bounded(request.ErrorMessage, "termination workflow failed")
	eventID, err := service.ids.New(now)
	if err != nil {
		return TaskResult{}, err
	}
	event := domain.NewTerminationEvent(eventID, domain.EventTerminationFailed, "Failed", workflow, session, request.ObjectsDeleted, now)
	if err := service.workflows.CompleteWorkflow(ctx, session, expectedVersion, workflow, event); err != nil {
		return TaskResult{}, err
	}
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
	active := session.ActiveWorkflowID == workflow.ID && session.ActiveWorkflowType == domain.TerminationWorkflowType
	completed := workflow.Status == domain.WorkflowSucceeded && session.LifecycleState == domain.StateDeleted
	failed := workflow.Status == domain.WorkflowFailed && session.LifecycleState == domain.StateFailed
	if workflow.Type != domain.TerminationWorkflowType || (!active && !completed && !failed) {
		return domain.Session{}, domain.Workflow{}, domain.ErrConflict
	}
	return session, workflow, nil
}

func (service *Service) notify(ctx context.Context, session domain.Session, workflow domain.Workflow) {
	if service.notifications == nil {
		return
	}
	id, err := service.ids.New(service.clock.Now())
	if err != nil {
		return
	}
	content := "**Session terminated**\nSession: `" + session.ID + "`\nTagged EC2/EBS infrastructure and every stored session artifact/version were deleted. Only the audit tombstone remains."
	_ = service.notifications.Enqueue(ctx, domain.NotificationRequest{SchemaVersion: 1, NotificationID: id, SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID, Content: content, CorrelationID: workflow.CorrelationID, RequestedAt: service.clock.Now().UTC()})
}

func taskResult(session domain.Session, workflow domain.Workflow) TaskResult {
	return TaskResult{SessionID: session.ID, WorkflowID: workflow.ID, State: string(session.LifecycleState)}
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
