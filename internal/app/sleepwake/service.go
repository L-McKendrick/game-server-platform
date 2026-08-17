package sleepwake

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const (
	ActionPrepare        = "prepare"
	ActionDispatch       = "dispatch"
	ActionObserve        = "observe"
	ActionCheckManaged   = "check_managed"
	ActionHealthDispatch = "dispatch_health"
	ActionHealthObserve  = "observe_health"
	ActionComplete       = "complete"
	ActionFail           = "fail"
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
	State        string `json:"state"`
	CommandID    string `json:"command_id,omitempty"`
	Managed      bool   `json:"managed"`
	Done         bool   `json:"done"`
	Succeeded    bool   `json:"succeeded"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}
type Service struct {
	sessions      ports.SessionRepository
	stages        ports.ProvisioningRepository
	workflows     ports.WorkflowRepository
	compute       ports.ComputeProvisioner
	monitor       ports.MonitoringRunner
	notifications ports.NotificationQueue
	ids           IDGenerator
	clock         Clock
}

func NewService(s ports.SessionRepository, st ports.ProvisioningRepository, w ports.WorkflowRepository, c ports.ComputeProvisioner, m ports.MonitoringRunner, n ports.NotificationQueue, ids IDGenerator, clock Clock) (*Service, error) {
	if s == nil || st == nil || w == nil || c == nil || m == nil || ids == nil || clock == nil {
		return nil, fmt.Errorf("sleep/wake dependencies are required")
	}
	return &Service{sessions: s, stages: st, workflows: w, compute: c, monitor: m, notifications: n, ids: ids, clock: clock}, nil
}
func (s *Service) Handle(ctx context.Context, r TaskRequest) (TaskResult, error) {
	session, wf, err := s.load(ctx, r)
	if err != nil {
		return TaskResult{}, err
	}
	switch r.Action {
	case ActionPrepare:
		return result(session, wf), nil
	case ActionDispatch:
		if wf.Type == domain.SleepWorkflowType {
			err = s.compute.StopInstance(ctx, session.Infrastructure.InstanceID)
		} else {
			err = s.compute.StartInstance(ctx, session.Infrastructure.InstanceID)
		}
		if err != nil {
			return TaskResult{}, err
		}
		return result(session, wf), nil
	case ActionObserve:
		return s.observe(ctx, session, wf)
	case ActionCheckManaged:
		return s.checkManaged(ctx, session, wf)
	case ActionHealthDispatch:
		expected := session.Version
		changed, progressErr := session.AdvanceProgress(wf.ID, domain.ProgressHealthVerification, s.clock.Now())
		if progressErr != nil {
			return TaskResult{}, progressErr
		}
		if changed {
			if progressErr := s.saveProgress(ctx, session, expected, wf); progressErr != nil {
				return TaskResult{}, progressErr
			}
			s.notify(ctx, session, wf)
		}
		commandID, err := s.monitor.Start(ctx, session)
		if err != nil {
			return TaskResult{}, err
		}
		out := result(session, wf)
		out.CommandID = commandID
		return out, nil
	case ActionHealthObserve:
		status, err := s.monitor.Observe(ctx, session.Infrastructure.InstanceID, r.CommandID)
		if err != nil {
			return TaskResult{}, err
		}
		out := result(session, wf)
		out.CommandID = r.CommandID
		out.Done = status.Status == "Success" || status.Status == "Failed" || status.Status == "TimedOut" || status.Status == "Cancelled"
		out.Succeeded = status.Status == "Success" && status.Observation.Classify(session.TeamSpeakEnabled) == domain.HealthHealthy
		if out.Done && !out.Succeeded {
			out.ErrorCode = "ERR_WAKE_HEALTH"
			out.ErrorMessage = bounded(status.ErrorMessage, "post-wake health check failed")
		}
		return out, nil
	case ActionComplete:
		return s.complete(ctx, session, wf)
	case ActionFail:
		return s.fail(ctx, session, wf, r)
	default:
		return TaskResult{}, fmt.Errorf("unsupported sleep/wake action %q", r.Action)
	}
}

func (s *Service) checkManaged(ctx context.Context, session domain.Session, wf domain.Workflow) (TaskResult, error) {
	managed, err := s.compute.IsManaged(ctx, session.Infrastructure.InstanceID)
	if err != nil {
		return TaskResult{}, err
	}
	out := result(session, wf)
	out.Managed = managed
	return out, nil
}

func (s *Service) observe(ctx context.Context, session domain.Session, wf domain.Workflow) (TaskResult, error) {
	o, err := s.compute.ObserveInstance(ctx, session.Infrastructure.InstanceID)
	if err != nil {
		return TaskResult{}, err
	}
	r := result(session, wf)
	if wf.Type == domain.SleepWorkflowType {
		r.Done = o.State == "stopped"
		r.Succeeded = r.Done
		return r, nil
	}
	r.Done = o.State == "running"
	r.Succeeded = r.Done
	return r, nil
}
func (s *Service) complete(ctx context.Context, session domain.Session, wf domain.Workflow) (TaskResult, error) {
	if wf.Status == domain.WorkflowSucceeded {
		return result(session, wf), nil
	}
	expected := session.Version
	now := s.clock.Now().UTC()
	var err error
	if wf.Type == domain.SleepWorkflowType {
		err = session.CompleteSleep(wf.ID, now)
	} else {
		observation, observeErr := s.compute.ObserveInstance(ctx, session.Infrastructure.InstanceID)
		if observeErr != nil {
			return TaskResult{}, observeErr
		}
		if observation.State != "running" {
			return TaskResult{}, fmt.Errorf("instance is not running")
		}
		err = session.CompleteWake(wf.ID, observation.PublicIPv4, now)
	}
	if err != nil {
		return TaskResult{}, err
	}
	wf.Status = domain.WorkflowSucceeded
	wf.CurrentStage = string(session.LifecycleState)
	wf.CompletedAt = now
	eventType := domain.EventSessionSleeping
	if wf.Type == domain.WakeWorkflowType {
		eventType = domain.EventSessionWoken
	}
	eventID, err := s.ids.New(now)
	if err != nil {
		return TaskResult{}, err
	}
	event := domain.NewProvisioningEvent(eventID, eventType, wf.CurrentStage, wf, session, now)
	if err = s.workflows.CompleteWorkflow(ctx, session, expected, wf, event); err != nil {
		return TaskResult{}, err
	}
	s.notify(ctx, session, wf)
	return result(session, wf), nil
}
func (s *Service) fail(ctx context.Context, session domain.Session, wf domain.Workflow, r TaskRequest) (TaskResult, error) {
	if wf.Status == domain.WorkflowFailed {
		return result(session, wf), nil
	}
	expected := session.Version
	now := s.clock.Now().UTC()
	if err := session.FailSleepWake(wf.ID, now); err != nil {
		return TaskResult{}, err
	}
	wf.Status = domain.WorkflowFailed
	wf.CurrentStage = "Failed"
	wf.ErrorCode = bounded(r.ErrorCode, "ERR_SLEEP_WAKE_FAILED")
	wf.ErrorMessage = bounded(r.ErrorMessage, "sleep/wake workflow failed")
	wf.CompletedAt = now
	id, err := s.ids.New(now)
	if err != nil {
		return TaskResult{}, err
	}
	event := domain.NewProvisioningEvent(id, domain.EventSleepWakeFailed, "Failed", wf, session, now)
	if err = s.workflows.CompleteWorkflow(ctx, session, expected, wf, event); err != nil {
		return TaskResult{}, err
	}
	s.notify(ctx, session, wf)
	return result(session, wf), nil
}
func (s *Service) load(ctx context.Context, r TaskRequest) (domain.Session, domain.Workflow, error) {
	if strings.TrimSpace(r.SessionID) == "" || strings.TrimSpace(r.WorkflowID) == "" {
		return domain.Session{}, domain.Workflow{}, fmt.Errorf("session and workflow IDs are required")
	}
	session, err := s.sessions.Get(ctx, r.SessionID)
	if err != nil {
		return domain.Session{}, domain.Workflow{}, err
	}
	wf, err := s.workflows.GetWorkflow(ctx, session.ID, r.WorkflowID)
	if err != nil {
		return domain.Session{}, domain.Workflow{}, err
	}
	if session.ActiveWorkflowID != wf.ID || (wf.Type != domain.SleepWorkflowType && wf.Type != domain.WakeWorkflowType) {
		return domain.Session{}, domain.Workflow{}, domain.ErrConflict
	}
	return session, wf, nil
}
func (s *Service) notify(ctx context.Context, session domain.Session, wf domain.Workflow) {
	_ = sessioncard.EnqueueProgress(ctx, s.notifications, session, wf, s.clock.Now().UTC())
}

func (s *Service) saveProgress(ctx context.Context, session domain.Session, expectedVersion int64, workflow domain.Workflow) error {
	now := s.clock.Now().UTC()
	id, err := s.ids.New(now)
	if err != nil {
		return err
	}
	event := domain.NewProgressMilestoneEvent(id, workflow, session, now)
	return s.stages.SaveProvisioningStage(ctx, session, expectedVersion, event)
}
func result(s domain.Session, w domain.Workflow) TaskResult {
	return TaskResult{SessionID: s.ID, WorkflowID: w.ID, State: string(s.LifecycleState)}
}
func bounded(v, f string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return f
	}
	if len(v) > 500 {
		return v[:500]
	}
	return v
}
