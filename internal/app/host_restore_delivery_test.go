package app

import (
	"context"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRestoreArchiveDeliveryPreservesPinDeadlineAndRejectsDrift(t *testing.T) {
	now := time.Now().UTC()
	digest := hostBase64Digest(strings.Repeat("a", 64))
	records := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", Version: 7, ActiveWorkflowID: "operation", ActiveWorkflowType: domain.RestoreWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour)}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.RestoreWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
	records.session.Infrastructure.InstanceID = "i-123"
	records.session.Archive = domain.ArchiveMetadata{ID: "previous", ObjectKey: "sessions/session/archives/previous/session.tar.gz", ManifestObjectKey: "sessions/session/archives/previous/manifest.json", ManifestSHA256: digest, ManifestSizeBytes: 100, SHA256: digest, SizeBytes: 42, Format: "tar+gzip", VerifiedAt: now}
	recovery := &manifestRecovery{}
	inspector := &archivePinInspector{pin: domain.HostOutputVersion{VersionID: "pinned", SHA256: digest, SizeBytes: 42}}
	delivery := HostCommandDelivery{Issuer: HostManifestIssuer{Inputs: HostObjectIssuer{Authority: HostAccessAuthority{Records: records, Instances: instanceAuthority(true)}, Signer: manifestSigner{expiry: now.Add(10 * time.Minute)}}, Attempts: recovery, Exchange: recovery}, ArchiveInspector: inspector}
	first, err := delivery.PrepareRestoreArchive(context.Background(), "session", 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := delivery.PrepareRestoreArchive(context.Background(), "session", 40*time.Minute)
	if err != nil || !reflect.DeepEqual(first, replay) || recovery.writes != 1 {
		t.Fatal("restore replay changed identity", err)
	}
	recovery.current.ExpiresAt = now.Add(4 * time.Minute)
	delivery.Issuer.Inputs.Signer = manifestSigner{expiry: now.Add(12 * time.Minute)}
	refreshed, err := delivery.RefreshHostCommand(context.Background(), first.Scope)
	if err != nil || refreshed.Generation != 2 || !refreshed.Scope.Equal(first.Scope) {
		t.Fatal("restore renewal failed", err)
	}
	inspector.pin.VersionID = "changed"
	if ref, err := delivery.PrepareRestoreArchive(context.Background(), "session", 40*time.Minute); err == nil || ref.URL != "" {
		t.Fatal("changed archive version accepted")
	}
	records.workflow.CancelRequestedAt = time.Now()
	calls := inspector.calls
	if _, err := delivery.PrepareRestoreArchive(context.Background(), "session", 40*time.Minute); err == nil || inspector.calls != calls {
		t.Fatal("cancelled restore inspected archive")
	}
}
