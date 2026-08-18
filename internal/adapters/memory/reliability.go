package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

var _ ports.ReliabilityRepository = (*SessionRepository)(nil)

func (repository *SessionRepository) SaveWorkflowCancellation(ctx context.Context, workflow domain.Workflow, expectedStatus domain.WorkflowStatus, event domain.SessionEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := workflow.Validate(); err != nil {
		return err
	}
	if err := validateEvent(workflow.SessionID, event); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	key := workflowKey(workflow.SessionID, workflow.ID)
	current, ok := repository.workflows[key]
	if !ok {
		return domain.ErrNotFound
	}
	if current.Status != expectedStatus || !current.CancelRequestedAt.IsZero() {
		return domain.ErrConflict
	}
	repository.workflows[key] = workflow
	repository.events[workflow.SessionID] = append(repository.events[workflow.SessionID], cloneEvent(event))
	return nil
}

func (repository *SessionRepository) ListActiveWorkflowSessions(ctx context.Context, limit int32) ([]domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, fmt.Errorf("limit must be positive")
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	result := make([]domain.Session, 0)
	for _, session := range repository.sessions {
		if session.ActiveWorkflowID != "" {
			result = append(result, session)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if len(result) > int(limit) {
		result = result[:limit]
	}
	return result, nil
}

func (repository *SessionRepository) SaveReconciliationFinding(ctx context.Context, finding domain.ReconciliationFinding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := finding.Validate(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, existing := range repository.reconciliation[finding.SessionID] {
		if existing.ID == finding.ID {
			return domain.ErrAlreadyExists
		}
	}
	repository.reconciliation[finding.SessionID] = append(repository.reconciliation[finding.SessionID], finding)
	return nil
}

func (repository *SessionRepository) ListReconciliationFindings(ctx context.Context, sessionID string, limit int32) ([]domain.ReconciliationFinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(sessionID) == "" || limit < 1 {
		return nil, fmt.Errorf("session ID and positive limit are required")
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	items := append([]domain.ReconciliationFinding(nil), repository.reconciliation[sessionID]...)
	sort.Slice(items, func(i, j int) bool { return items[i].DetectedAt.After(items[j].DetectedAt) })
	if len(items) > int(limit) {
		items = items[:limit]
	}
	return items, nil
}

func (repository *SessionRepository) SaveDeadLetterOperation(ctx context.Context, operation domain.DeadLetterOperation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := operation.Validate(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, ok := repository.deadLetters[operation.ID]; ok {
		return domain.ErrAlreadyExists
	}
	repository.deadLetters[operation.ID] = operation
	return nil
}

func (repository *SessionRepository) GetDeadLetterOperation(ctx context.Context, operationID string) (domain.DeadLetterOperation, error) {
	if err := ctx.Err(); err != nil {
		return domain.DeadLetterOperation{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	operation, ok := repository.deadLetters[strings.TrimSpace(operationID)]
	if !ok {
		return domain.DeadLetterOperation{}, domain.ErrNotFound
	}
	return operation, nil
}
