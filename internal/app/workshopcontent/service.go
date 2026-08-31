package workshopcontent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/app/failurestate"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const commandLease = 7 * time.Hour

type Clock interface{ Now() time.Time }
type IDGenerator interface {
	New(time.Time) (string, error)
}

type Service struct {
	sessions  ports.SessionRepository
	workflows ports.WorkflowRepository
	runner    ports.WorkshopContentRunner
	ids       IDGenerator
	clock     Clock
}

func New(s ports.SessionRepository, w ports.WorkflowRepository, r ports.WorkshopContentRunner, ids IDGenerator, clock Clock) (*Service, error) {
	if s == nil || w == nil || r == nil || ids == nil || clock == nil {
		return nil, fmt.Errorf("Workshop content dependencies are required")
	}
	return &Service{sessions: s, workflows: w, runner: r, ids: ids, clock: clock}, nil
}

func (s *Service) Start(ctx context.Context, sessionID string, target domain.WorkshopTarget, digest, requestedBy, correlationID, idempotencyKey string) (domain.Workflow, error) {
	session, err := s.sessions.Get(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return domain.Workflow{}, err
	}
	digest = strings.TrimSpace(digest)
	if !contentDigestMatches(session, target, digest) {
		return domain.Workflow{}, fmt.Errorf("%w: Workshop content snapshot changed", domain.ErrConflict)
	}
	workflowID := contentWorkflowID(session.ID, target, digest, idempotencyKey)
	if existing, getErr := s.workflows.GetWorkflow(ctx, session.ID, workflowID); getErr == nil {
		if existing.Type != domain.WorkshopContentSyncWorkflowType || existing.ContentTarget != string(target) || existing.ContentDigest != digest || existing.RequestedBy != strings.TrimSpace(requestedBy) {
			return domain.Workflow{}, domain.ErrIdempotencyConflict
		}
		return existing, nil
	} else if !errors.Is(getErr, domain.ErrNotFound) {
		return domain.Workflow{}, getErr
	}
	if session.LifecycleState != domain.StateRunning && session.LifecycleState != domain.StateIdle {
		return domain.Workflow{}, domain.ErrInvalidTransition
	}
	if target != domain.WorkshopTargetMission && target != domain.WorkshopTargetMods {
		return domain.Workflow{}, fmt.Errorf("invalid Workshop target")
	}
	now := s.clock.Now().UTC()
	expected := session.Version
	if err = session.AcquireWorkflowLock(workflowID, domain.WorkshopContentSyncWorkflowType, commandLease, now); err != nil {
		return domain.Workflow{}, err
	}
	workflow := domain.Workflow{ID: workflowID, SessionID: session.ID, Type: domain.WorkshopContentSyncWorkflowType, Status: domain.WorkflowRunning, RequestedBy: strings.TrimSpace(requestedBy), CorrelationID: strings.TrimSpace(correlationID), ExpectedVersion: expected, CurrentStage: "Dispatching", ContentTarget: string(target), ContentDigest: digest, InstanceID: session.Infrastructure.InstanceID, StartedAt: now, LeaseExpiresAt: now.Add(commandLease)}
	eventID, err := s.ids.New(now)
	if err != nil {
		return domain.Workflow{}, err
	}
	event := domain.SessionEvent{ID: eventID, SessionID: session.ID, Type: domain.EventWorkflowStarted, OccurredAt: now, ActorType: string(domain.ActorTypeSystem), ActorID: domain.WorkshopContentSyncWorkflowType, CorrelationID: workflow.CorrelationID, Data: map[string]string{"workflow_id": workflow.ID, "target": string(target)}}
	if err = s.workflows.AcquireWorkflow(ctx, session, expected, workflow, event); err != nil {
		return domain.Workflow{}, err
	}
	commandID, err := s.runner.StartContent(ctx, session, target, false)
	if err != nil {
		if finishErr := s.finish(ctx, session, workflow, false, "ERR_WORKSHOP_SYNC_DISPATCH", "The managed host could not start the Workshop synchronization command."); finishErr != nil {
			return domain.Workflow{}, fmt.Errorf("dispatch Workshop sync: %v; persist failure: %w", err, finishErr)
		}
		return domain.Workflow{}, err
	}
	workflow.CommandID, workflow.CommandDeadlineAt, workflow.CurrentStage = commandID, now.Add(commandLease-time.Hour), "Downloading"
	if err = s.workflows.SetWorkflowExecution(ctx, workflow, domain.WorkflowRunning); err != nil {
		if cancelErr := s.runner.CancelContentCommand(ctx, commandID, workflow.InstanceID); cancelErr != nil {
			return domain.Workflow{}, fmt.Errorf("persist Workshop command: %v; cancel untracked command: %w", err, cancelErr)
		}
		if finishErr := s.finish(ctx, session, workflow, false, "ERR_WORKSHOP_SYNC_TRACKING", "The managed-host command was cancelled because its durable tracking record could not be updated."); finishErr != nil {
			return domain.Workflow{}, fmt.Errorf("persist Workshop command: %v; finalize cancelled command: %w", err, finishErr)
		}
		return domain.Workflow{}, err
	}
	return workflow, nil
}

func contentWorkflowID(sessionID string, target domain.WorkshopTarget, contentDigest, idempotencyKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(sessionID) + "\x00" + string(target) + "\x00" + strings.TrimSpace(contentDigest) + "\x00" + strings.TrimSpace(idempotencyKey)))
	return "wsync-" + hex.EncodeToString(sum[:12])
}

func contentDigestMatches(session domain.Session, target domain.WorkshopTarget, digest string) bool {
	if len(digest) != 64 {
		return false
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return false
	}
	if target == domain.WorkshopTargetMission {
		current, err := session.WorkshopMissionRevision()
		return err == nil && current == digest
	}
	if target == domain.WorkshopTargetMods {
		return session.PendingPresetRevision.WorkshopResolutionSHA256 == digest || session.ActivePresetRevision.WorkshopResolutionSHA256 == digest
	}
	return false
}

func (s *Service) HandleTerminal(ctx context.Context, commandID string) (bool, error) {
	sessionID, workflowID, instanceID, err := s.runner.ResolveContentCommand(ctx, strings.TrimSpace(commandID))
	if err != nil {
		return false, err
	}
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		return false, err
	}
	workflow, err := s.workflows.GetWorkflow(ctx, sessionID, workflowID)
	if err != nil {
		return false, err
	}
	if workflow.Status != domain.WorkflowRunning {
		return true, nil
	}
	if session.ActiveWorkflowID != workflow.ID || session.Infrastructure.InstanceID != instanceID || workflow.InstanceID != instanceID || (workflow.CommandID != "" && workflow.CommandID != commandID) || !contentDigestMatches(session, domain.WorkshopTarget(workflow.ContentTarget), workflow.ContentDigest) {
		return false, domain.ErrConflict
	}
	if workflow.CommandID == "" {
		workflow.CommandID, workflow.CommandDeadlineAt, workflow.CurrentStage = commandID, workflow.StartedAt.Add(commandLease-time.Hour), "Downloading"
		if err := s.workflows.SetWorkflowExecution(ctx, workflow, domain.WorkflowRunning); err != nil {
			return false, err
		}
	}
	status, err := s.runner.Observe(ctx, instanceID, commandID)
	if err != nil {
		return false, err
	}
	switch status.Status {
	case "Success":
		return true, s.finish(ctx, session, workflow, true, "", "")
	case "Failed", "TimedOut", "Cancelled":
		return true, s.finish(ctx, session, workflow, false, status.ErrorCode, status.ErrorMessage)
	default:
		return false, nil
	}
}

func (s *Service) ReconcileActive(ctx context.Context, session domain.Session, workflow domain.Workflow) (bool, error) {
	if workflow.Type != domain.WorkshopContentSyncWorkflowType {
		return false, nil
	}
	if strings.TrimSpace(workflow.CommandID) == "" {
		commandID, err := s.runner.FindContentCommand(ctx, session.ID, workflow.ID, workflow.InstanceID)
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return s.HandleTerminal(ctx, commandID)
	}
	done, err := s.HandleTerminal(ctx, workflow.CommandID)
	if err != nil || done {
		return done, err
	}
	if !workflow.CommandDeadlineAt.IsZero() && !workflow.CommandDeadlineAt.After(s.clock.Now().UTC()) {
		if err := s.runner.CancelContentCommand(ctx, workflow.CommandID, workflow.InstanceID); err != nil {
			return false, err
		}
		return true, s.finish(ctx, session, workflow, false, "ERR_WORKSHOP_DOWNLOAD_TIMEOUT", "Workshop synchronization exceeded its bounded command runtime.")
	}
	return false, nil
}

func (s *Service) finish(ctx context.Context, session domain.Session, workflow domain.Workflow, success bool, code, message string) error {
	if session.ActiveWorkflowID != workflow.ID {
		return domain.ErrConflict
	}
	expected, now := session.Version, s.clock.Now().UTC()
	var releaseErr error
	if success {
		if session.Failure.Stage == "Workshop content sync" {
			session.ClearFailure()
		}
		releaseErr = session.CompleteWorkflowLock(workflow.ID, now)
	} else {
		if strings.TrimSpace(code) == "" {
			code = "ERR_WORKSHOP_ITEM_DOWNLOAD"
		}
		if strings.TrimSpace(message) == "" {
			message = "Workshop synchronization stopped before the recorded snapshot was available on the managed host."
		}
		if err := failurestate.Record(&session, workflow, code, "ERR_WORKSHOP_ITEM_DOWNLOAD", "Workshop content sync", message, failurestate.Impact(session, false), now); err != nil {
			return err
		}
		releaseErr = session.ReleaseWorkflowLock(workflow.ID, now)
	}
	if releaseErr != nil {
		return releaseErr
	}
	workflow.CompletedAt = now
	workflow.ErrorCode = strings.TrimSpace(code)
	workflow.ErrorMessage = domain.SanitizeDiagnostic(message)
	if success {
		workflow.Status, workflow.CurrentStage = domain.WorkflowSucceeded, "Available"
	} else {
		workflow.Status, workflow.CurrentStage = domain.WorkflowFailed, "Action required"
	}
	eventID, err := s.ids.New(now)
	if err != nil {
		return err
	}
	eventType := domain.EventWorkflowCompleted
	if !success {
		eventType = domain.EventWorkflowFailed
	}
	event := domain.SessionEvent{ID: eventID, SessionID: session.ID, Type: eventType, OccurredAt: now, ActorType: string(domain.ActorTypeSystem), ActorID: domain.WorkshopContentSyncWorkflowType, CorrelationID: workflow.CorrelationID, Data: map[string]string{"workflow_id": workflow.ID, "status": string(workflow.Status)}}
	return s.workflows.CompleteWorkflow(ctx, session, expected, workflow, event)
}
