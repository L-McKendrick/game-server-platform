package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

var _ ports.ProvisioningRepository = (*SessionRepository)(nil)
var _ ports.BootstrapRepository = (*SessionRepository)(nil)

func (repository *SessionRepository) SaveBootstrapStage(ctx context.Context, session domain.Session, expectedVersion int64, event domain.SessionEvent) error {
	return repository.SaveProvisioningStage(ctx, session, expectedVersion, event)
}

func (repository *SessionRepository) SaveProvisioningStage(ctx context.Context, session domain.Session, expectedVersion int64, event domain.SessionEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, found := repository.sessions[session.ID]
	if !found {
		return domain.ErrNotFound
	}
	if current.Version != expectedVersion || session.Version != expectedVersion+1 || current.ActiveWorkflowID != session.ActiveWorkflowID {
		return domain.ErrConflict
	}
	repository.sessions[session.ID] = session
	repository.events[session.ID] = append(repository.events[session.ID], cloneEvent(event))
	return nil
}

func (repository *SessionRepository) AcquireCapacitySlot(ctx context.Context, sessionID string, _ string, limit int, _ time.Time) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for slot := 0; slot < limit; slot++ {
		id := fmt.Sprintf("slot-%d", slot)
		if owner, found := repository.capacity[id]; !found || owner == sessionID {
			repository.capacity[id] = sessionID
			return id, nil
		}
	}
	return "", domain.ErrQuotaExceeded
}

func (repository *SessionRepository) ReleaseCapacitySlot(ctx context.Context, slotID string, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.capacity[slotID] == sessionID {
		delete(repository.capacity, slotID)
	}
	return nil
}
