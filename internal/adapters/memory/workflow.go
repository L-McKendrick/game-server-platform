package memory

import (
	"context"
	"fmt"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

var _ ports.WorkflowRepository = (*SessionRepository)(nil)

func workflowKey(sessionID string, workflowID string) string { return sessionID + "#" + workflowID }

func (repository *SessionRepository) AcquireWorkflow(ctx context.Context, session domain.Session, expectedVersion int64, workflow domain.Workflow, event domain.SessionEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := workflow.Validate(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, found := repository.sessions[session.ID]
	if !found {
		return domain.ErrNotFound
	}
	if current.Version != expectedVersion || (current.ActiveWorkflowID != "" && current.ActiveWorkflowLeaseExpiresAt.After(workflow.StartedAt)) {
		return domain.ErrWorkflowLocked
	}
	key := workflowKey(session.ID, workflow.ID)
	if _, exists := repository.workflows[key]; exists {
		return domain.ErrAlreadyExists
	}
	repository.sessions[session.ID] = session
	repository.workflows[key] = workflow
	repository.events[session.ID] = append(repository.events[session.ID], cloneEvent(event))
	return nil
}

func (repository *SessionRepository) CompleteWorkflow(ctx context.Context, session domain.Session, expectedVersion int64, workflow domain.Workflow, event domain.SessionEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, found := repository.sessions[session.ID]
	if !found {
		return domain.ErrNotFound
	}
	if current.Version != expectedVersion || current.ActiveWorkflowID != workflow.ID {
		return domain.ErrConflict
	}
	key := workflowKey(session.ID, workflow.ID)
	if _, found := repository.workflows[key]; !found {
		return domain.ErrNotFound
	}
	repository.sessions[session.ID] = session
	repository.workflows[key] = workflow
	repository.events[session.ID] = append(repository.events[session.ID], cloneEvent(event))
	return nil
}

func (repository *SessionRepository) GetWorkflow(ctx context.Context, sessionID string, workflowID string) (domain.Workflow, error) {
	if err := ctx.Err(); err != nil {
		return domain.Workflow{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	workflow, found := repository.workflows[workflowKey(sessionID, workflowID)]
	if !found {
		return domain.Workflow{}, fmt.Errorf("%w: workflow", domain.ErrNotFound)
	}
	return workflow, nil
}

func (repository *SessionRepository) SetWorkflowExecution(ctx context.Context, workflow domain.Workflow, expectedStatus domain.WorkflowStatus) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	key := workflowKey(workflow.SessionID, workflow.ID)
	current, found := repository.workflows[key]
	if !found {
		return domain.ErrNotFound
	}
	if current.Status != expectedStatus {
		return domain.ErrConflict
	}
	repository.workflows[key] = workflow
	return nil
}
