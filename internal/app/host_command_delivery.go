package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

// HostCommandDelivery composes existing authority, recovery and issuance for
// trusted lifecycle adapters. It has no public or managed-host request endpoint.
type HostCommandDelivery struct {
	Issuer           HostManifestIssuer
	Inspector        ports.HostReadInspector
	WorkshopStore    ports.HostWorkshopStore
	ArchiveInspector ports.HostArchiveInspector
}

var _ ports.HostCommandAccess = HostCommandDelivery{}

func (delivery HostCommandDelivery) RefreshHostCommand(ctx context.Context, scope domain.HostAccessScope) (ports.HostAccessReference, error) {
	issuer := delivery.Issuer
	issuer.Inputs.Rollback = scope.CommandMode == "rollback"
	if _, _, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC()); err != nil {
		return ports.HostAccessReference{}, err
	}
	current, err := issuer.Attempts.GetHostAccessAttempt(ctx, scope.SessionID, scope.OperationID, scope.AttemptID)
	if err != nil {
		return ports.HostAccessReference{}, err
	}
	if current.Validate() != nil || !current.Scope.Equal(scope) || current.DispatchState == domain.HostAccessFinished {
		return ports.HostAccessReference{}, domain.ErrConflict
	}
	if current.ExpiresAt.Sub(time.Now().UTC()) > domain.HostAccessRefreshBefore && (current.Generation == 1 || current.DispatchState != domain.HostAccessPrepared) {
		return ports.HostAccessReference{}, nil
	}
	inputs, objects, environment, err := issuer.RecoverCommandInventory(ctx, scope)
	if err != nil && scope.CommandMode == "" {
		stored, recoverErr := issuer.recoverCommandManifest(ctx, scope)
		if recoverErr == nil && len(stored.Objects) == 1 && stored.Objects[0].Object.Purpose == domain.HostObjectRestoreArchive {
			return delivery.restoreArchiveReference(ctx, scope)
		}
		if recoverErr == nil && len(stored.Objects) == 1 && stored.Objects[0].Object.Purpose == domain.HostObjectArchiveUpload {
			return delivery.refreshArchiveUpload(ctx, scope)
		}
	}
	if err != nil {
		return ports.HostAccessReference{}, err
	}
	issuer.Inputs = inputs
	reference, err := issuer.IssueWithEnvironment(ctx, scope, objects, environment)
	if err == nil && reference.Generation == current.Generation && (current.Generation == 1 || current.DispatchState != domain.HostAccessPrepared) {
		return ports.HostAccessReference{}, nil
	}
	return reference, err
}

func (delivery HostCommandDelivery) RecordHostRefresh(ctx context.Context, scope domain.HostAccessScope, generation int64) error {
	issuer := delivery.Issuer
	issuer.Inputs.Rollback = scope.CommandMode == "rollback"
	return issuer.RecordDispatch(ctx, scope, generation, domain.HostAccessDispatched)
}

func (delivery HostCommandDelivery) PrepareHostCommand(ctx context.Context, sessionID, stage string, rollback bool, timeout time.Duration, environment ports.HostCommandEnvironment) (ports.HostAccessReference, error) {
	if environment == nil {
		return ports.HostAccessReference{}, fmt.Errorf("host command context required")
	}
	issuer := delivery.Issuer
	issuer.Inputs.Rollback = rollback
	scope, err := issuer.ResolveWorkflowScope(ctx, sessionID, stage, timeout)
	if err != nil {
		return ports.HostAccessReference{}, err
	}
	inputs, objects, prior, err := issuer.RecoverCommandInventory(ctx, scope)
	if errors.Is(err, domain.ErrNotFound) {
		inputs, objects, err = issuer.Inputs.WorkflowReadInventory(ctx, scope, delivery.Inspector)
		if err == nil {
			var outputs []domain.HostObjectRequest
			outputs, err = inputs.WorkflowCommandOutputInventory(ctx, scope)
			objects = append(objects, outputs...)
		}
	}
	if err != nil {
		return ports.HostAccessReference{}, err
	}
	issuer.Inputs = inputs
	values, err := environment(ctx, prior)
	if err != nil {
		return ports.HostAccessReference{}, fmt.Errorf("host command context preparation failed")
	}
	return issuer.IssueWithEnvironment(ctx, scope, objects, values)
}
