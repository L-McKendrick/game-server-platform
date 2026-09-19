package app

import (
	"context"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"strconv"
	"strings"
	"testing"
	"time"
)

type archiveManifestSigner struct{ manifestSigner }

func (signer archiveManifestSigner) Sign(ctx context.Context, scope domain.HostAccessScope, object domain.HostObjectRequest) (ports.HostObjectCapability, error) {
	if object.Purpose != domain.HostObjectArchiveUpload {
		return signer.manifestSigner.Sign(ctx, scope, object)
	}
	return ports.HostObjectCapability{Object: object, Method: "PUT", URL: "https://assets.test/archive", ExpiresAt: signer.expiry, Headers: map[string]string{"Content-Length": strconv.FormatInt(object.MaxBytes, 10), "Content-Type": object.ContentType, "If-None-Match": "*", "X-Amz-Checksum-Sha256": object.SHA256}}, nil
}

func TestArchiveUploadPersistsPreparedIdentityAndRejectsReplayDrift(t *testing.T) {
	now := time.Now().UTC()
	records := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", Version: 7, ActiveWorkflowID: "operation", ActiveWorkflowType: domain.ArchiveWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour)}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.ArchiveWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
	records.session.Infrastructure.InstanceID = "i-123"
	recovery := &manifestRecovery{}
	digest := hostBase64Digest(strings.Repeat("a", 64))
	inspector := &archivePinInspector{pin: domain.HostOutputVersion{VersionID: "pinned", SHA256: digest, SizeBytes: 42}, err: domain.ErrNotFound}
	delivery := HostCommandDelivery{Issuer: HostManifestIssuer{Inputs: HostObjectIssuer{Authority: HostAccessAuthority{Records: records, Instances: instanceAuthority(true)}, Signer: archiveManifestSigner{manifestSigner{expiry: now.Add(10 * time.Minute)}}}, Attempts: recovery, Exchange: recovery}, ArchiveInspector: inspector}
	scope, err := delivery.PrepareArchiveScope(context.Background(), "session", 30*time.Minute)
	if err != nil || !scope.DeadlineAt.Equal(now.Add(30*time.Minute).Truncate(time.Second)) {
		t.Fatal("archive first deadline not anchored", err)
	}
	first, err := delivery.PrepareArchiveUpload(context.Background(), scope, digest, 42)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := delivery.PrepareArchiveUpload(context.Background(), scope, digest, 43); err == nil {
		t.Fatal("prepared byte length changed")
	}
	recovery.current.ExpiresAt = now.Add(4 * time.Minute)
	delivery.Issuer.Inputs.Signer = archiveManifestSigner{manifestSigner{expiry: now.Add(12 * time.Minute)}}
	renewed, err := delivery.RefreshHostCommand(context.Background(), scope)
	if err != nil || renewed.Generation != 2 || !renewed.Scope.Equal(first.Scope) {
		t.Fatal("archive refresh changed identity", err)
	}
	inspector.err = nil
	if pin, err := delivery.VerifyArchiveUpload(context.Background(), scope); err != nil || pin.VersionID != "pinned" {
		t.Fatal("prepared archive verification failed", err)
	}
	inspector.pin.SizeBytes++
	if _, err := delivery.VerifyArchiveUpload(context.Background(), scope); err == nil {
		t.Fatal("different bytes accepted")
	}
	records.workflow.CancelRequestedAt = time.Now()
	calls := inspector.calls
	if _, err := delivery.VerifyArchiveUpload(context.Background(), scope); err == nil || inspector.calls != calls {
		t.Fatal("cancelled archive inspected")
	}
}
