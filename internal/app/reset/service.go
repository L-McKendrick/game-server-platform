package reset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type Clock interface{ Now() time.Time }

type Service struct {
	repository  ports.ResetRepository
	queue       ports.ResetQueue
	clock       Clock
	environment string
	enabled     bool
}

func NewService(repository ports.ResetRepository, queue ports.ResetQueue, clock Clock, environment string, enabled bool) (*Service, error) {
	if repository == nil || clock == nil || strings.TrimSpace(environment) == "" {
		return nil, fmt.Errorf("reset repository, clock, and environment are required")
	}
	if enabled && queue == nil {
		return nil, fmt.Errorf("reset queue is required when reset is enabled")
	}
	return &Service{repository: repository, queue: queue, clock: clock, environment: strings.TrimSpace(environment), enabled: enabled}, nil
}

func (service *Service) Enabled() bool { return service.enabled }

func (service *Service) Active(ctx context.Context) (domain.ResetOperation, bool, error) {
	operation, err := service.repository.GetActiveReset(ctx, service.environment)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ResetOperation{}, false, nil
	}
	return operation, err == nil, err
}

func (service *Service) Latest(ctx context.Context) (domain.ResetOperation, bool, error) {
	operation, err := service.repository.GetLatestReset(ctx, service.environment)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ResetOperation{}, false, nil
	}
	return operation, err == nil, err
}

func (service *Service) Prepare(ctx context.Context, confirmationID, guildID, actorID string, isAdministrator bool) (domain.ResetConfirmation, error) {
	if !isAdministrator {
		return domain.ResetConfirmation{}, domain.ErrForbidden
	}
	if !service.enabled {
		return domain.ResetConfirmation{}, domain.ErrFeatureDisabled
	}
	if _, active, err := service.Active(ctx); err != nil {
		return domain.ResetConfirmation{}, err
	} else if active {
		return domain.ResetConfirmation{}, domain.ErrCommandInProgress
	}
	confirmation, err := domain.NewResetConfirmation(confirmationID, service.environment, guildID, actorID, service.clock.Now())
	if err != nil {
		return domain.ResetConfirmation{}, err
	}
	if err := service.repository.CreateResetConfirmation(ctx, confirmation); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return service.repository.GetResetConfirmation(ctx, confirmation.ID)
		}
		return domain.ResetConfirmation{}, err
	}
	return confirmation, nil
}

func (service *Service) Start(ctx context.Context, confirmationID, operationID, correlationID, guildID, actorID, phrase string, isAdministrator bool) (domain.ResetOperation, error) {
	if !isAdministrator {
		return domain.ResetOperation{}, domain.ErrForbidden
	}
	if !service.enabled {
		return domain.ResetOperation{}, domain.ErrFeatureDisabled
	}
	now := service.clock.Now().UTC()
	operation, err := domain.NewResetOperation(operationID, service.environment, guildID, actorID, correlationID, now)
	if err != nil {
		return domain.ResetOperation{}, err
	}
	operation, err = service.repository.ConsumeResetConfirmation(ctx, confirmationID, actorID, guildID, phrase, operation, now)
	if err != nil {
		if errors.Is(err, domain.ErrConfirmationConsumed) || errors.Is(err, domain.ErrAlreadyExists) {
			return service.repository.GetResetOperation(ctx, operationID)
		}
		return domain.ResetOperation{}, err
	}
	request := domain.ResetRequest{SchemaVersion: 1, OperationID: operation.ID, Environment: operation.Environment, GuildID: operation.GuildID, RequestedAt: now}
	if err := service.queue.Enqueue(ctx, request); err != nil {
		return operation, fmt.Errorf("%w: reset request may already be queued", domain.ErrConfirmationDispatchUncertain)
	}
	return operation, nil
}

func (service *Service) Status(ctx context.Context, operationID, guildID string, isAdministrator bool) (domain.ResetOperation, error) {
	if !isAdministrator {
		return domain.ResetOperation{}, domain.ErrForbidden
	}
	operation, err := service.repository.GetResetOperation(ctx, operationID)
	if err != nil {
		return domain.ResetOperation{}, err
	}
	if operation.Environment != service.environment || operation.GuildID != strings.TrimSpace(guildID) {
		return domain.ResetOperation{}, domain.ErrForbidden
	}
	return operation, nil
}
