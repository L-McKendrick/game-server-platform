package app

import (
	"context"
	"errors"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"time"
)

func (delivery HostCommandDelivery) PrepareArchiveScope(ctx context.Context, sessionID string, timeout time.Duration) (domain.HostAccessScope, error) {
	scope, err := delivery.Issuer.ResolveWorkflowScope(ctx, sessionID, "archive", timeout)
	if err != nil {
		return domain.HostAccessScope{}, err
	}
	_, workflow, err := delivery.Issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC())
	if err != nil {
		return domain.HostAccessScope{}, err
	}
	if workflow.Type != domain.ArchiveWorkflowType {
		return domain.HostAccessScope{}, domain.ErrConflict
	}
	return scope, nil
}

func (delivery HostCommandDelivery) PrepareArchiveUpload(ctx context.Context, scope domain.HostAccessScope, checksum string, size int64) (ports.HostAccessReference, error) {
	object := domain.HostObjectRequest{Purpose: domain.HostObjectArchiveUpload, Slot: "archive", Key: "sessions/" + scope.SessionID + "/archives/" + scope.OperationID + "/session.tar.gz", SHA256: checksum, MinBytes: size, MaxBytes: size, ContentType: "application/gzip"}
	return delivery.Issuer.Issue(ctx, scope, []domain.HostObjectRequest{object})
}

// VerifyArchiveUpload compares privileged HEAD identity against the immutable
// prepared inventory, never against host-selected latest-object metadata alone.
func (delivery HostCommandDelivery) VerifyArchiveUpload(ctx context.Context, scope domain.HostAccessScope) (domain.HostOutputVersion, error) {
	stored, err := delivery.Issuer.recoverCommandManifest(ctx, scope)
	if err != nil {
		return domain.HostOutputVersion{}, err
	}
	if len(stored.Objects) != 1 || len(stored.Environment) != 0 || stored.Objects[0].Object.Purpose != domain.HostObjectArchiveUpload {
		return domain.HostOutputVersion{}, domain.ErrConflict
	}
	obj := stored.Objects[0].Object
	pin, err := delivery.Issuer.Inputs.InspectWorkflowArchive(ctx, scope, obj.Key, "", delivery.ArchiveInspector)
	if err != nil {
		return domain.HostOutputVersion{}, err
	}
	if pin.SHA256 != obj.SHA256 || pin.SizeBytes != obj.MaxBytes {
		return domain.HostOutputVersion{}, domain.ErrConflict
	}
	return pin, nil
}

func (delivery HostCommandDelivery) refreshArchiveUpload(ctx context.Context, scope domain.HostAccessScope) (ports.HostAccessReference, error) {
	stored, err := delivery.Issuer.recoverCommandManifest(ctx, scope)
	if err != nil {
		return ports.HostAccessReference{}, err
	}
	if len(stored.Objects) != 1 || len(stored.Environment) != 0 || stored.Objects[0].Object.Purpose != domain.HostObjectArchiveUpload {
		return ports.HostAccessReference{}, domain.ErrConflict
	}
	obj := stored.Objects[0].Object
	if _, err := delivery.VerifyArchiveUpload(ctx, scope); err == nil {
		return ports.HostAccessReference{}, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return ports.HostAccessReference{}, err
	}
	return delivery.PrepareArchiveUpload(ctx, scope, obj.SHA256, obj.MaxBytes)
}
