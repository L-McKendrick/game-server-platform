package reliability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

var ErrCancellationHonored = errors.New("workflow cancellation honored")

type Clock interface{ Now() time.Time }
type IDGenerator interface {
	New(time.Time) (string, error)
}

type Service struct {
	sessions    ports.SessionRepository
	workflows   ports.WorkflowRepository
	reliability ports.ReliabilityRepository
	ids         IDGenerator
	clock       Clock
	inspector   ports.WorkflowExecutionInspector
	deadLetters ports.DeadLetterManager
}

func (service *Service) WithDeadLetterManager(manager ports.DeadLetterManager) *Service {
	service.deadLetters = manager
	return service
}

func (service *Service) WithExecutionInspector(inspector ports.WorkflowExecutionInspector) *Service {
	service.inspector = inspector
	return service
}

func NewService(sessions ports.SessionRepository, workflows ports.WorkflowRepository, reliability ports.ReliabilityRepository, ids IDGenerator, clock Clock) (*Service, error) {
	if sessions == nil || workflows == nil || reliability == nil || ids == nil || clock == nil {
		return nil, fmt.Errorf("reliability dependencies are required")
	}
	return &Service{sessions: sessions, workflows: workflows, reliability: reliability, ids: ids, clock: clock}, nil
}

type CancellationCommand struct {
	SessionID, WorkflowID, RequestedBy, CorrelationID string
}

func (service *Service) RequestCancellation(ctx context.Context, command CancellationCommand) (domain.Workflow, error) {
	if strings.TrimSpace(command.SessionID) == "" || strings.TrimSpace(command.WorkflowID) == "" || strings.TrimSpace(command.RequestedBy) == "" || strings.TrimSpace(command.CorrelationID) == "" {
		return domain.Workflow{}, fmt.Errorf("cancellation identity is required")
	}
	session, err := service.sessions.Get(ctx, command.SessionID)
	if err != nil {
		return domain.Workflow{}, err
	}
	if session.OwnerDiscordUserID != command.RequestedBy || session.ActiveWorkflowID != command.WorkflowID {
		return domain.Workflow{}, domain.ErrForbidden
	}
	workflow, err := service.workflows.GetWorkflow(ctx, command.SessionID, command.WorkflowID)
	if err != nil {
		return domain.Workflow{}, err
	}
	expected := workflow.Status
	changed, err := workflow.RequestCancellation(command.RequestedBy, service.clock.Now().UTC())
	if err != nil {
		return domain.Workflow{}, err
	}
	if !changed {
		return workflow, nil
	}
	eventID, err := service.ids.New(service.clock.Now().UTC())
	if err != nil {
		return domain.Workflow{}, err
	}
	event := domain.SessionEvent{ID: eventID, SessionID: session.ID, Type: domain.EventWorkflowCancelRequested, OccurredAt: workflow.CancelRequestedAt,
		ActorType: string(domain.ActorTypeDiscordUser), ActorID: command.RequestedBy, CorrelationID: command.CorrelationID,
		Data: map[string]string{"workflow_id": workflow.ID, "workflow_type": workflow.Type, "status": string(workflow.Status)}}
	if err := service.reliability.SaveWorkflowCancellation(ctx, workflow, expected, event); err != nil {
		return domain.Workflow{}, err
	}
	return workflow, nil
}

// HonorAtInitialBoundary is called only before a worker's first external side
// effect. It atomically releases the session lock and marks the workflow
// cancelled. Callers stop processing when ErrCancellationHonored is returned.
func (service *Service) HonorAtInitialBoundary(ctx context.Context, sessionID, workflowID string) error {
	workflow, err := service.workflows.GetWorkflow(ctx, sessionID, workflowID)
	if err != nil {
		return err
	}
	if !workflow.CancellationRequested() {
		return nil
	}
	session, err := service.sessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	return HonorLoadedAtInitialBoundary(ctx, service.workflows, service.ids, service.clock, session, workflow)
}

func HonorLoadedAtInitialBoundary(ctx context.Context, workflows ports.WorkflowRepository, ids IDGenerator, clock Clock, session domain.Session, workflow domain.Workflow) error {
	if !workflow.CancellationRequested() {
		return nil
	}
	expectedVersion := session.Version
	if err := session.CancelWorkflowAtSafeBoundary(workflow, clock.Now().UTC()); err != nil {
		return err
	}
	workflow.Status, workflow.CompletedAt, workflow.CurrentStage = domain.WorkflowCancelled, clock.Now().UTC(), "Cancelled at safe boundary"
	eventID, err := ids.New(clock.Now().UTC())
	if err != nil {
		return err
	}
	event := domain.SessionEvent{ID: eventID, SessionID: session.ID, Type: domain.EventWorkflowCompleted, OccurredAt: workflow.CompletedAt,
		ActorType: string(domain.ActorTypeSystem), ActorID: "Reliability", CorrelationID: workflow.CorrelationID,
		Data: map[string]string{"workflow_id": workflow.ID, "workflow_type": workflow.Type, "status": string(workflow.Status)}}
	if err := workflows.CompleteWorkflow(ctx, session, expectedVersion, workflow, event); err != nil {
		return err
	}
	return ErrCancellationHonored
}

func IsCancellationHonored(err error) bool { return errors.Is(err, ErrCancellationHonored) }

type ReconciliationReport struct{ Inspected, Findings, Repaired int }

func (service *Service) ReconcileWorkflows(ctx context.Context, limit int32) (ReconciliationReport, error) {
	if service.inspector == nil {
		return ReconciliationReport{}, fmt.Errorf("workflow execution inspector is required")
	}
	sessions, err := service.reliability.ListActiveWorkflowSessions(ctx, limit)
	if err != nil {
		return ReconciliationReport{}, err
	}
	report := ReconciliationReport{}
	for _, session := range sessions {
		report.Inspected++
		finding, repair, err := service.inspectWorkflow(ctx, session)
		if err != nil {
			return report, err
		}
		if finding == nil {
			continue
		}
		report.Findings++
		if err := service.reliability.SaveReconciliationFinding(ctx, *finding); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
			return report, err
		}
		if repair {
			if err := service.repairWorkflow(ctx, session, *finding); err != nil {
				return report, err
			}
			report.Repaired++
		}
	}
	return report, nil
}

func (service *Service) inspectWorkflow(ctx context.Context, session domain.Session) (*domain.ReconciliationFinding, bool, error) {
	now := service.clock.Now().UTC()
	workflow, err := service.workflows.GetWorkflow(ctx, session.ID, session.ActiveWorkflowID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			finding, createErr := service.newFinding(session, "WORKFLOW_RECORD_MISSING", "The active lock has no workflow record.", domain.ReconciliationReportOnly, now)
			return &finding, false, createErr
		}
		return nil, false, err
	}
	if workflow.Status == domain.WorkflowSucceeded || workflow.Status == domain.WorkflowFailed || workflow.Status == domain.WorkflowCancelled {
		if session.ActiveWorkflowLeaseExpiresAt.After(now) {
			return nil, false, nil
		}
		finding, createErr := service.newFinding(session, "TERMINAL_WORKFLOW_LOCKED", "A terminal workflow retained an expired session lock.", domain.ReconciliationReleaseLock, now)
		return &finding, true, createErr
	}
	if strings.TrimSpace(workflow.ExecutionARN) == "" {
		if session.ActiveWorkflowLeaseExpiresAt.After(now) {
			return nil, false, nil
		}
		finding, createErr := service.newFinding(session, "EXECUTION_ARN_MISSING", "An expired workflow lock has no execution reference.", domain.ReconciliationMarkFailed, now)
		return &finding, true, createErr
	}
	status, found, err := service.inspector.Describe(ctx, workflow.ExecutionARN)
	if err != nil {
		return nil, false, err
	}
	if found && status == domain.ExecutionRunning {
		return nil, false, nil
	}
	if found && status == domain.ExecutionSucceeded {
		finding, createErr := service.newFinding(session, "EXECUTION_SUCCEEDED_METADATA_INCOMPLETE", "The execution succeeded but session completion is not recorded.", domain.ReconciliationReportOnly, now)
		return &finding, false, createErr
	}
	if session.ActiveWorkflowLeaseExpiresAt.After(now) {
		return nil, false, nil
	}
	code, detail := "EXECUTION_NOT_FOUND", "The expired workflow execution no longer exists."
	if found && status.TerminalFailure() {
		code, detail = "EXECUTION_TERMINAL_FAILURE", "The workflow execution ended unsuccessfully and retained an expired lock."
	}
	finding, createErr := service.newFinding(session, code, detail, domain.ReconciliationMarkFailed, now)
	return &finding, true, createErr
}

func (service *Service) newFinding(session domain.Session, code, detail string, action domain.ReconciliationAction, now time.Time) (domain.ReconciliationFinding, error) {
	id, err := service.ids.New(now)
	if err != nil {
		return domain.ReconciliationFinding{}, err
	}
	finding := domain.ReconciliationFinding{ID: id, SessionID: session.ID, WorkflowID: session.ActiveWorkflowID, Code: code, Detail: detail, Action: action, DetectedAt: now}
	return finding, finding.Validate()
}

func (service *Service) repairWorkflow(ctx context.Context, session domain.Session, finding domain.ReconciliationFinding) error {
	workflow, err := service.workflows.GetWorkflow(ctx, session.ID, session.ActiveWorkflowID)
	if err != nil {
		return err
	}
	expectedVersion := session.Version
	now := service.clock.Now().UTC()
	if err := session.FailActiveWorkflowForReconciliation(workflow.ID, now); err != nil {
		return err
	}
	workflow.Status, workflow.CompletedAt, workflow.ErrorCode, workflow.ErrorMessage, workflow.CurrentStage = domain.WorkflowFailed, now, "ERR_RECONCILED_WORKFLOW", finding.Detail, "Reconciled"
	eventID, err := service.ids.New(now)
	if err != nil {
		return err
	}
	event := domain.SessionEvent{ID: eventID, SessionID: session.ID, Type: domain.EventWorkflowReconciled, OccurredAt: now, ActorType: string(domain.ActorTypeSystem), ActorID: "Reliability", CorrelationID: workflow.CorrelationID,
		Data: map[string]string{"workflow_id": workflow.ID, "finding_id": finding.ID, "code": finding.Code, "action": string(finding.Action)}}
	return service.workflows.CompleteWorkflow(ctx, session, expectedVersion, workflow, event)
}

type DeadLetterResult struct {
	Operation  domain.DeadLetterOperation
	Inspection domain.DeadLetterInspection
}

func (service *Service) InspectDeadLetter(ctx context.Context, queue domain.DeadLetterQueue, requestedBy, correlationID string) (DeadLetterResult, error) {
	if service.deadLetters == nil {
		return DeadLetterResult{}, fmt.Errorf("dead-letter manager is required")
	}
	requestedBy, correlationID = strings.TrimSpace(requestedBy), strings.TrimSpace(correlationID)
	if requestedBy == "" || correlationID == "" {
		return DeadLetterResult{}, fmt.Errorf("operator and correlation ID are required")
	}
	inspection, sourceARN, err := service.deadLetters.Inspect(ctx, queue)
	if err != nil {
		return DeadLetterResult{}, err
	}
	now := service.clock.Now().UTC()
	id, err := service.ids.New(now)
	if err != nil {
		return DeadLetterResult{}, err
	}
	operation := domain.DeadLetterOperation{ID: id, RequestedBy: requestedBy, CorrelationID: correlationID, Queue: queue, Action: domain.DeadLetterInspected, SourceARN: sourceARN, StartedAt: now, CompletedAt: now}
	if err := service.reliability.SaveDeadLetterOperation(ctx, operation); err != nil {
		return DeadLetterResult{}, err
	}
	return DeadLetterResult{Operation: operation, Inspection: inspection}, nil
}

func (service *Service) RedriveDeadLetter(ctx context.Context, queue domain.DeadLetterQueue, requestedBy, correlationID string, maxMessagesPerSecond int32) (domain.DeadLetterOperation, error) {
	if service.deadLetters == nil {
		return domain.DeadLetterOperation{}, fmt.Errorf("dead-letter manager is required")
	}
	requestedBy, correlationID = strings.TrimSpace(requestedBy), strings.TrimSpace(correlationID)
	if requestedBy == "" || correlationID == "" {
		return domain.DeadLetterOperation{}, fmt.Errorf("operator and correlation ID are required")
	}
	operationID := deadLetterOperationID(queue, correlationID)
	existing, getErr := service.reliability.GetDeadLetterOperation(ctx, operationID)
	if getErr == nil {
		if existing.Queue != queue || existing.CorrelationID != correlationID || existing.RequestedBy != requestedBy || existing.Action != domain.DeadLetterRedriven {
			return domain.DeadLetterOperation{}, domain.ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(getErr, domain.ErrNotFound) {
		return domain.DeadLetterOperation{}, getErr
	}
	sourceARN, destinationARN, err := service.deadLetters.StartRedrive(ctx, queue, maxMessagesPerSecond)
	if err != nil {
		return domain.DeadLetterOperation{}, err
	}
	now := service.clock.Now().UTC()
	operation := domain.DeadLetterOperation{ID: operationID, RequestedBy: requestedBy, CorrelationID: correlationID, Queue: queue, Action: domain.DeadLetterRedriven, SourceARN: sourceARN, DestinationARN: destinationARN, StartedAt: now, CompletedAt: now}
	if err := service.reliability.SaveDeadLetterOperation(ctx, operation); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return service.reliability.GetDeadLetterOperation(ctx, operationID)
		}
		return domain.DeadLetterOperation{}, err
	}
	return operation, nil
}

func deadLetterOperationID(queue domain.DeadLetterQueue, correlationID string) string {
	sum := sha256.Sum256([]byte(string(queue) + "\x00" + strings.TrimSpace(correlationID)))
	return "dlq-" + hex.EncodeToString(sum[:12])
}
