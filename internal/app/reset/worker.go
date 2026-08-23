package reset

import (
	"context"
	"fmt"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type Worker struct {
	repository ports.ResetRepository
	cleaner    ports.ResetCleaner
	clock      Clock
}

func NewWorker(repository ports.ResetRepository, cleaner ports.ResetCleaner, clock Clock) (*Worker, error) {
	if repository == nil || cleaner == nil || clock == nil {
		return nil, fmt.Errorf("reset worker dependencies are required")
	}
	return &Worker{repository: repository, cleaner: cleaner, clock: clock}, nil
}

func (worker *Worker) Handle(ctx context.Context, request domain.ResetRequest) (domain.ResetOperation, error) {
	if err := request.Validate(); err != nil {
		return domain.ResetOperation{}, err
	}
	operation, err := worker.repository.GetResetOperation(ctx, request.OperationID)
	if err != nil {
		return domain.ResetOperation{}, err
	}
	if operation.Environment != request.Environment || operation.GuildID != request.GuildID {
		return domain.ResetOperation{}, domain.ErrForbidden
	}
	if !operation.Active() {
		return operation, nil
	}
	if operation.Status == domain.ResetPending {
		expected := operation.Version
		operation.Status, operation.Stage = domain.ResetRunning, "Cleaning runtime state"
		operation.Version++
		operation.UpdatedAt = worker.clock.Now().UTC()
		if err := worker.repository.SaveResetOperation(ctx, operation, expected); err != nil {
			return domain.ResetOperation{}, err
		}
	}
	result, cleanupErr := worker.cleaner.Cleanup(ctx, operation)
	expected := operation.Version
	operation.Version++
	operation.UpdatedAt = worker.clock.Now().UTC()
	operation.CompletedAt = operation.UpdatedAt
	operation.DeletedSessions = result.DeletedSessions
	operation.DeletedObjects = result.DeletedObjects
	if cleanupErr != nil {
		operation.Status, operation.Stage = domain.ResetFailed, "Action required"
		operation.ErrorCode = "ERR_RESET_INCOMPLETE"
		operation.ErrorDetail = "Runtime cleanup could not verify every item as absent. No automatic retry is scheduled; resources may remain and incur cost."
	} else {
		operation.Status, operation.Stage = domain.ResetSucceeded, "Runtime reset complete"
		operation.ErrorCode, operation.ErrorDetail = "", ""
	}
	if err := worker.repository.SaveResetOperation(ctx, operation, expected); err != nil {
		return domain.ResetOperation{}, err
	}
	// A cleanup failure is durably terminal and intentionally acknowledged so
	// SQS does not schedule an unapproved retry.
	return operation, nil
}

func boundedResetDetail(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		return value[:500]
	}
	return value
}
