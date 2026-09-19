package app

import (
	"context"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestRollbackAccessUsesPersistedStageWithoutRevivingExpiredApplication(t *testing.T) {
	for _, scenario := range []string{"authorized", "normal-mode", "active-stage", "wrong-applying-owner", "cancelled", "expired-attempt", "expired-lease", "wrong-issuer-mode"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			r := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", ActiveWorkflowID: "operation", ActiveWorkflowType: domain.BootstrapWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour), Progress: domain.SessionProgress{WorkflowID: "operation", WorkflowType: domain.BootstrapWorkflowType, State: domain.ProgressRollingBack, StartedAt: now}, ActivePresetRevision: domain.PresetRevision{Number: 1, PresetObjectKey: "sessions/session/input/presets/active.html"}, PendingPresetRevision: domain.PresetRevision{Number: 2, PresetObjectKey: "sessions/session/input/presets/pending.html", Status: domain.PresetRevisionApplying, ApplyWorkflowID: "operation"}}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.BootstrapWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour), CommandID: "prior-application", CommandDeadlineAt: now.Add(-time.Minute)}}
			r.session.Infrastructure.InstanceID = "i-123"
			scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "rollback", InstanceID: "i-123", CommandMode: "rollback", DeadlineAt: now.Add(10 * time.Minute)}
			rollback := true
			switch scenario {
			case "normal-mode":
				scope.CommandMode = ""
				rollback = false
			case "active-stage":
				r.session.Progress.State = domain.ProgressActive
			case "wrong-applying-owner":
				r.session.PendingPresetRevision.ApplyWorkflowID = "other"
			case "cancelled":
				r.workflow.CancelRequestedAt = now
			case "expired-attempt":
				scope.DeadlineAt = now.Add(-time.Second)
			case "expired-lease":
				r.workflow.LeaseExpiresAt = now.Add(-time.Second)
			case "wrong-issuer-mode":
				rollback = false
			}
			scope.SnapshotSHA256 = HostContentSnapshot(r.session)
			signer := &inputSigner{}
			issuer := HostObjectIssuer{Authority: HostAccessAuthority{Records: r, Instances: instanceAuthority(true)}, Signer: signer, Rollback: rollback}
			object := domain.HostObjectRequest{Purpose: domain.HostObjectClientPreset, Slot: "client", Key: r.session.ActivePresetRevision.PresetObjectKey, MaxBytes: 100}
			_, err := issuer.SignWorkflowObject(context.Background(), scope, object)
			if (err == nil) != (scenario == "authorized") {
				t.Fatalf("error=%v", err)
			}
			if scenario != "authorized" && signer.calls != 0 {
				t.Fatal("denied command released signing authority")
			}
		})
	}
}
