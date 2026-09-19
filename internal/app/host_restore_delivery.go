package app

import (
	"context"
	"errors"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

// PrepareRestoreArchive preserves the first pinned archive identity across
// command retries. Only trusted current restore records select the object.
func (delivery HostCommandDelivery) PrepareRestoreArchive(ctx context.Context, sessionID string, timeout time.Duration) (ports.HostAccessReference, error) {
	scope, err := delivery.Issuer.ResolveWorkflowScope(ctx, sessionID, "restore-archive", timeout)
	if err != nil {
		return ports.HostAccessReference{}, err
	}
	return delivery.restoreArchiveReference(ctx, scope)
}

func (delivery HostCommandDelivery) restoreArchiveReference(ctx context.Context, scope domain.HostAccessScope) (ports.HostAccessReference, error) {
	issuer := delivery.Issuer
	session, workflow, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC())
	if err != nil {
		return ports.HostAccessReference{}, err
	}
	if workflow.Type != domain.RestoreWorkflowType || session.Archive.Validate() != nil || scope.CommandMode != "" {
		return ports.HostAccessReference{}, domain.ErrConflict
	}
	object := domain.HostObjectRequest{Purpose: domain.HostObjectRestoreArchive, Slot: "restore-archive", Key: session.Archive.ObjectKey, SHA256: session.Archive.SHA256, MaxBytes: session.Archive.SizeBytes, ContentType: "application/gzip"}
	stored, err := issuer.recoverCommandManifest(ctx, scope)
	if err == nil {
		if len(stored.Objects) != 1 || len(stored.Environment) != 0 {
			return ports.HostAccessReference{}, domain.ErrConflict
		}
		pinned := stored.Objects[0].Object
		object.VersionID = pinned.VersionID
		if object != pinned {
			return ports.HostAccessReference{}, domain.ErrConflict
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return ports.HostAccessReference{}, err
	}
	pin, err := issuer.Inputs.InspectWorkflowArchive(ctx, scope, object.Key, object.VersionID, delivery.ArchiveInspector)
	if err != nil {
		return ports.HostAccessReference{}, err
	}
	object.VersionID = pin.VersionID
	return issuer.IssueWithEnvironment(ctx, scope, []domain.HostObjectRequest{object}, nil)
}
