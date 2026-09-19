package memory

import (
	"context"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

func (repository *SessionRepository) GetHostAccessAttempt(ctx context.Context, sessionID, operationID, attemptID string) (domain.HostAccessAttempt, error) {
	if err := ctx.Err(); err != nil {
		return domain.HostAccessAttempt{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	attempt, found := repository.hostAccess[workflowKey(sessionID, operationID+"#"+attemptID)]
	if !found {
		return domain.HostAccessAttempt{}, domain.ErrNotFound
	}
	return attempt, nil
}

func (repository *SessionRepository) SaveHostAccessAttempt(ctx context.Context, next domain.HostAccessAttempt, expectedRevision, sessionVersion int64, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	key := workflowKey(next.Scope.SessionID, next.Scope.OperationID+"#"+next.Scope.AttemptID)
	if err := domain.ValidateHostAccessUpdate(repository.hostAccess[key], next, expectedRevision); err != nil {
		return err
	}
	session, found := repository.sessions[next.Scope.SessionID]
	workflow, workflowFound := repository.workflows[workflowKey(next.Scope.SessionID, next.Scope.OperationID)]
	rollback := next.Scope.CommandMode == "rollback"
	if (rollback && !session.HostAccessRollbackAllowed(next.Scope.OperationID)) || (!rollback && session.Progress.State == domain.ProgressRollingBack) {
		return domain.ErrConflict
	}
	if now.IsZero() || !next.Scope.DeadlineAt.After(now) || !found || !workflowFound || session.Version != sessionVersion || sessionVersion < 1 ||
		session.GuildID != next.Scope.GuildID || session.Infrastructure.InstanceID != next.Scope.InstanceID || session.ActiveWorkflowID != next.Scope.OperationID ||
		session.ActiveWorkflowLeaseExpiresAt.Before(next.Scope.DeadlineAt) || workflow.Status != domain.WorkflowRunning ||
		!workflow.CancelRequestedAt.IsZero() || !workflow.CompletedAt.IsZero() || workflow.LeaseExpiresAt.Before(next.Scope.DeadlineAt) ||
		(!rollback && !workflow.CommandDeadlineAt.IsZero() && workflow.CommandDeadlineAt.Before(next.Scope.DeadlineAt)) {
		return domain.ErrConflict
	}
	if repository.hostAccess == nil {
		repository.hostAccess = make(map[string]domain.HostAccessAttempt)
	}
	repository.hostAccess[key] = next
	return nil
}

var _ ports.HostAccessAttemptRepository = (*SessionRepository)(nil)
