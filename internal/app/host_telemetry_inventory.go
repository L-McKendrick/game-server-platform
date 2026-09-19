package app

import (
	"context"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// WorkflowCommandOutputInventory keeps advisory telemetry separate from accepted
// Workshop outputs while preserving every capability during renewal.
func (issuer HostObjectIssuer) WorkflowCommandOutputInventory(ctx context.Context, scope domain.HostAccessScope) ([]domain.HostObjectRequest, error) {
	outputs, err := issuer.WorkflowWorkshopInventory(ctx, scope)
	if err != nil {
		return nil, err
	}
	return append(outputs, domain.HostObjectRequest{Purpose: domain.HostObjectProgress, Slot: "progress", Key: scope.StagingKey("progress"), ContentType: "text/plain", MinBytes: 1, MaxBytes: domain.MaxHostProgressBytes}), nil
}
