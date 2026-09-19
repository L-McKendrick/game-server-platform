package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type inputSigner struct {
	mutate func()
	calls  int
}

type legacyInspector struct {
	calls  int
	mutate func()
}

func (inspector *legacyInspector) InspectHostRead(context.Context, string, string, int64) (string, string, int64, error) {
	inspector.calls++
	if inspector.mutate != nil {
		inspector.mutate()
	}
	return hostBase64Digest(strings.Repeat("a", 64)), "version-1", 8, nil
}

func TestLegacyInputInspectionAuthorizesBeforeReadAndPinsIssuance(t *testing.T) {
	for _, purpose := range []domain.HostObjectPurpose{domain.HostObjectClientPreset, domain.HostObjectMission} {
		t.Run(string(purpose), func(t *testing.T) {
			now := time.Now().UTC()
			key := "sessions/session/input/presets/v1.html"
			r := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", ActiveWorkflowID: "operation", ActiveWorkflowType: domain.BootstrapWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour), PresetObjectKey: key}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.BootstrapWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
			r.session.Infrastructure.InstanceID = "i-123"
			inspect := HostObjectIssuer.InspectLegacyPreset
			if purpose == domain.HostObjectMission {
				key = "sessions/session/input/missions/scenario.Altis.pbo"
				r.session.MissionFiles = []domain.MissionRecord{{ObjectKey: key, Filename: "scenario.Altis.pbo", Status: domain.ArtifactAccepted}}
				inspect = HostObjectIssuer.InspectLegacyMission
			}
			scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: HostContentSnapshot(r.session), DeadlineAt: now.Add(30 * time.Minute)}
			signer := &inputSigner{}
			issuer := HostObjectIssuer{Authority: HostAccessAuthority{Records: r, Instances: instanceAuthority(true)}, Signer: signer}
			object := domain.HostObjectRequest{Purpose: purpose, Slot: "accepted-input", Key: key, MaxBytes: 10 * 1024 * 1024}
			inspector := &legacyInspector{}
			wrong := object
			wrong.Key = "sessions/session/input/presets/unaccepted.html"
			if _, err := inspect(issuer, context.Background(), scope, wrong, inspector); err == nil || inspector.calls != 0 {
				t.Fatal("unaccepted key read")
			}
			verified, err := inspect(issuer, context.Background(), scope, object, inspector)
			if err != nil || verified.VersionID != "version-1" || verified.MaxBytes != 8 {
				t.Fatal("accepted legacy input not pinned", err)
			}
			if _, err := issuer.SignWorkflowObject(context.Background(), scope, verified); err == nil || signer.calls != 0 {
				t.Fatal("unregistered digest signed")
			}
			issuer.VerifiedLegacyReads = map[string]domain.HostObjectRequest{key: verified}
			if _, err := issuer.SignWorkflowObject(context.Background(), scope, verified); err != nil {
				t.Fatal(err)
			}
			drift := verified
			drift.VersionID = "version-2"
			if _, err := issuer.SignWorkflowObject(context.Background(), scope, drift); err == nil {
				t.Fatal("different version signed")
			}
			inspector.mutate = func() { r.workflow.CancelRequestedAt = time.Now() }
			issuer.VerifiedLegacyReads = nil
			if verified, err := inspect(issuer, context.Background(), scope, object, inspector); err == nil || verified.Key != "" {
				t.Fatal("cancelled inspection released reference")
			}
		})
	}
}

func (signer *inputSigner) Sign(_ context.Context, _ domain.HostAccessScope, object domain.HostObjectRequest) (ports.HostObjectCapability, error) {
	signer.calls++
	if signer.mutate != nil {
		signer.mutate()
	}
	return ports.HostObjectCapability{Object: object, Method: "GET", URL: "https://assets.example.test/object", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func TestInputIssuerAuthorizesExactMissionAndRechecksAfterSigning(t *testing.T) {
	for _, scenario := range []string{"accepted", "other-accepted-namespace-key", "wrong-digest", "changed-during-signing", "wrong-purpose"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			key := "sessions/session/input/missions/" + strings.Repeat("a", 64) + "-mission.pbo"
			r := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", ActiveWorkflowID: "operation", ActiveWorkflowType: domain.BootstrapWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour), MissionFiles: []domain.MissionRecord{{ObjectKey: key, Filename: "mission.pbo", Status: domain.ArtifactAccepted}}}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.BootstrapWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
			r.session.Infrastructure.InstanceID = "i-123"
			s := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: HostContentSnapshot(r.session), DeadlineAt: now.Add(30 * time.Minute)}
			object := domain.HostObjectRequest{Purpose: domain.HostObjectMission, Slot: "mission", Key: key, SHA256: hostBase64Digest(strings.Repeat("a", 64)), MaxBytes: 100}
			signer := &inputSigner{}
			switch scenario {
			case "other-accepted-namespace-key":
				object.Key = strings.ReplaceAll(key, "mission.pbo", "other.pbo")
			case "wrong-digest":
				object.SHA256 = hostBase64Digest(strings.Repeat("b", 64))
			case "changed-during-signing":
				signer.mutate = func() { r.workflow.CancelRequestedAt = time.Now() }
			case "wrong-purpose":
				object.Purpose = domain.HostObjectBootstrapScript
			}
			issuer := HostObjectIssuer{Authority: HostAccessAuthority{Records: r, Instances: instanceAuthority(true)}, Signer: signer}
			_, err := issuer.SignWorkflowObject(context.Background(), s, object)
			if (err == nil) != (scenario == "accepted") {
				t.Fatalf("error=%v", err)
			}
			if scenario != "accepted" && scenario != "changed-during-signing" && signer.calls != 0 {
				t.Fatal("signed unauthorized input")
			}
		})
	}
}
