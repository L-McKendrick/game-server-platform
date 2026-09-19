package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

// InspectWorkflowArchive authorizes the key before privileged HEAD inspection.
// Archive replay callers must additionally match the returned pin to their
// persisted preparation digest/size before accepting or completing the backup.
func (issuer HostObjectIssuer) InspectWorkflowArchive(ctx context.Context, scope domain.HostAccessScope, key, version string, inspector ports.HostArchiveInspector) (domain.HostOutputVersion, error) {
	if inspector == nil || issuer.Rollback || scope.CommandMode != "" {
		return domain.HostOutputVersion{}, fmt.Errorf("archive inspection configuration invalid")
	}
	session, workflow, err := issuer.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC())
	if err != nil {
		return domain.HostOutputVersion{}, err
	}
	switch workflow.Type {
	case domain.ArchiveWorkflowType:
		if key != path.Join("sessions", scope.SessionID, "archives", scope.OperationID, "session.tar.gz") {
			return domain.HostOutputVersion{}, domain.ErrConflict
		}
	case domain.RestoreWorkflowType:
		read := domain.HostObjectRequest{Purpose: domain.HostObjectRestoreArchive, Slot: "restore-archive", Key: key, SHA256: session.Archive.SHA256, MaxBytes: session.Archive.SizeBytes, VersionID: version}
		if session.Archive.Validate() != nil || key != session.Archive.ObjectKey || read.Validate(scope) != nil {
			return domain.HostOutputVersion{}, domain.ErrConflict
		}
	default:
		return domain.HostOutputVersion{}, domain.ErrConflict
	}
	pin, err := inspector.InspectHostArchive(ctx, key, version)
	if err != nil {
		return domain.HostOutputVersion{}, err
	}
	digest, decodeErr := base64.StdEncoding.DecodeString(pin.SHA256)
	if decodeErr != nil || len(digest) != 32 || base64.StdEncoding.EncodeToString(digest) != pin.SHA256 || pin.VersionID == "" || pin.VersionID == "null" || (version != "" && pin.VersionID != version) || pin.SizeBytes < 1 || pin.SizeBytes > domain.MaxArchiveSizeBytes {
		return domain.HostOutputVersion{}, fmt.Errorf("archive inspected identity invalid")
	}
	if workflow.Type == domain.RestoreWorkflowType && (pin.SHA256 != session.Archive.SHA256 || pin.SizeBytes != session.Archive.SizeBytes) {
		return domain.HostOutputVersion{}, domain.ErrConflict
	}
	if _, _, err := issuer.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC()); err != nil {
		return domain.HostOutputVersion{}, err
	}
	return pin, nil
}
