package memory

import (
	"context"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

var _ ports.ResetRepository = (*SessionRepository)(nil)

func (repository *SessionRepository) CreateResetConfirmation(ctx context.Context, confirmation domain.ResetConfirmation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := confirmation.Validate(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.resetConfirmations[confirmation.ID]; exists {
		return domain.ErrAlreadyExists
	}
	repository.resetConfirmations[confirmation.ID] = confirmation
	return nil
}

func (repository *SessionRepository) GetResetConfirmation(ctx context.Context, confirmationID string) (domain.ResetConfirmation, error) {
	if err := ctx.Err(); err != nil {
		return domain.ResetConfirmation{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	confirmation, exists := repository.resetConfirmations[strings.TrimSpace(confirmationID)]
	if !exists {
		return domain.ResetConfirmation{}, domain.ErrNotFound
	}
	return confirmation, nil
}

func (repository *SessionRepository) ConsumeResetConfirmation(ctx context.Context, confirmationID, actorID, guildID, phrase string, operation domain.ResetOperation, now time.Time) (domain.ResetOperation, error) {
	if err := ctx.Err(); err != nil {
		return domain.ResetOperation{}, err
	}
	if err := operation.Validate(); err != nil {
		return domain.ResetOperation{}, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	confirmation, exists := repository.resetConfirmations[strings.TrimSpace(confirmationID)]
	if !exists {
		return domain.ResetOperation{}, domain.ErrNotFound
	}
	if err := confirmation.Check(actorID, guildID, phrase, now); err != nil {
		return domain.ResetOperation{}, err
	}
	if existingID := repository.activeResets[operation.Environment]; existingID != "" {
		if existingID == operation.ID {
			return repository.resetOperations[existingID], nil
		}
		return domain.ResetOperation{}, domain.ErrCommandInProgress
	}
	if existing, exists := repository.resetOperations[operation.ID]; exists {
		return existing, domain.ErrAlreadyExists
	}
	confirmation.ConsumedAt = now.UTC()
	repository.resetConfirmations[confirmation.ID] = confirmation
	repository.resetOperations[operation.ID] = operation
	repository.activeResets[operation.Environment] = operation.ID
	return operation, nil
}

func (repository *SessionRepository) GetResetOperation(ctx context.Context, operationID string) (domain.ResetOperation, error) {
	if err := ctx.Err(); err != nil {
		return domain.ResetOperation{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	operation, exists := repository.resetOperations[strings.TrimSpace(operationID)]
	if !exists {
		return domain.ResetOperation{}, domain.ErrNotFound
	}
	return operation, nil
}

func (repository *SessionRepository) GetActiveReset(ctx context.Context, environment string) (domain.ResetOperation, error) {
	if err := ctx.Err(); err != nil {
		return domain.ResetOperation{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	id := repository.activeResets[strings.TrimSpace(environment)]
	if id == "" {
		return domain.ResetOperation{}, domain.ErrNotFound
	}
	operation, exists := repository.resetOperations[id]
	if !exists || !operation.Active() {
		return domain.ResetOperation{}, domain.ErrNotFound
	}
	return operation, nil
}

func (repository *SessionRepository) GetLatestReset(ctx context.Context, environment string) (domain.ResetOperation, error) {
	if err := ctx.Err(); err != nil {
		return domain.ResetOperation{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	operation, exists := repository.latestResets[strings.TrimSpace(environment)]
	if !exists {
		return domain.ResetOperation{}, domain.ErrNotFound
	}
	return operation, nil
}

func (repository *SessionRepository) SaveResetOperation(ctx context.Context, operation domain.ResetOperation, expectedVersion int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := operation.Validate(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, exists := repository.resetOperations[operation.ID]
	if !exists {
		return domain.ErrNotFound
	}
	if current.Version != expectedVersion || operation.Version != expectedVersion+1 {
		return domain.ErrConflict
	}
	repository.resetOperations[operation.ID] = operation
	if operation.Active() {
		repository.activeResets[operation.Environment] = operation.ID
	} else if repository.activeResets[operation.Environment] == operation.ID {
		delete(repository.activeResets, operation.Environment)
		repository.latestResets[operation.Environment] = operation
	}
	return nil
}
