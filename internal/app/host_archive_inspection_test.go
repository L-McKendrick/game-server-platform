package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type archivePinInspector struct {
	calls  int
	pin    domain.HostOutputVersion
	mutate func()
	err    error
}

func (inspector *archivePinInspector) InspectHostArchive(context.Context, string, string) (domain.HostOutputVersion, error) {
	inspector.calls++
	if inspector.mutate != nil {
		inspector.mutate()
	}
	return inspector.pin, inspector.err
}

func TestArchiveInspectionRejectsUnownedKeysBeforeHEADAndChangedAuthorityAfter(t *testing.T) {
	for _, scenario := range []string{"valid", "foreign-session", "foreign-workflow", "wrong-instance", "cancelled-during-head", "changed-version", "invalid-digest", "oversized", "restore-valid", "restore-foreign-record", "restore-wrong-checksum", "restore-wrong-size"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			records := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", ActiveWorkflowID: "operation", ActiveWorkflowType: domain.ArchiveWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour)}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.ArchiveWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
			records.session.Infrastructure.InstanceID = "i-123"
			scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: HostContentSnapshot(records.session), DeadlineAt: now.Add(30 * time.Minute)}
			inspector := &archivePinInspector{pin: domain.HostOutputVersion{VersionID: "pinned", SHA256: hostBase64Digest(strings.Repeat("a", 64)), SizeBytes: 42}}
			key := "sessions/session/archives/operation/session.tar.gz"
			if strings.HasPrefix(scenario, "restore-") {
				records.workflow.Type = domain.RestoreWorkflowType
				records.session.ActiveWorkflowType = domain.RestoreWorkflowType
				key = "sessions/session/archives/previous/session.tar.gz"
				records.session.Archive = domain.ArchiveMetadata{ID: "previous", ObjectKey: key, ManifestObjectKey: "sessions/session/archives/previous/manifest.json", ManifestSHA256: inspector.pin.SHA256, ManifestSizeBytes: 100, SHA256: inspector.pin.SHA256, SizeBytes: 42, Format: "tar+gzip", VerifiedAt: now}
				if scenario == "restore-foreign-record" {
					key = "sessions/other/archives/previous/session.tar.gz"
					records.session.Archive.ObjectKey = key
				}
				if scenario == "restore-wrong-checksum" {
					inspector.pin.SHA256 = hostBase64Digest(strings.Repeat("b", 64))
				}
				if scenario == "restore-wrong-size" {
					inspector.pin.SizeBytes++
				}
				scope.SnapshotSHA256 = HostContentSnapshot(records.session)
			}
			switch scenario {
			case "foreign-session":
				key = strings.Replace(key, "sessions/session/", "sessions/other/", 1)
			case "foreign-workflow":
				key = strings.Replace(key, "archives/operation/", "archives/other/", 1)
			case "wrong-instance":
				scope.InstanceID = "i-other"
			case "cancelled-during-head":
				inspector.mutate = func() { records.workflow.CancelRequestedAt = time.Now() }
			case "changed-version":
				inspector.pin.VersionID = "later"
			case "invalid-digest":
				inspector.pin.SHA256 = "invalid"
			case "oversized":
				inspector.pin.SizeBytes = domain.MaxArchiveSizeBytes + 1
			}
			issuer := HostObjectIssuer{Authority: HostAccessAuthority{Records: records, Instances: instanceAuthority(true)}}
			pin, err := issuer.InspectWorkflowArchive(context.Background(), scope, key, "pinned", inspector)
			if scenario == "valid" || scenario == "restore-valid" {
				if err != nil || pin != inspector.pin {
					t.Fatal("owned archive inspection failed", err)
				}
			} else {
				if err == nil || pin.VersionID != "" {
					t.Fatal("unowned/stale archive pin returned")
				}
				if (scenario == "foreign-session" || scenario == "foreign-workflow" || scenario == "wrong-instance" || scenario == "restore-foreign-record") && inspector.calls != 0 {
					t.Fatal("unauthorized archive key read before rejection")
				}
			}
		})
	}
}
