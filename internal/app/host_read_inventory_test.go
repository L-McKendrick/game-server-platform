package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestWorkflowReadInventoryPinsLegacyAndRejectsDrift(t *testing.T) {
	now := time.Now().UTC()
	modernKey := "sessions/session/input/missions/" + strings.Repeat("a", 64) + "-modern.pbo"
	legacyKey := "sessions/session/input/missions/legacy.pbo"
	records := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", PresetObjectKey: "sessions/session/input/presets/v1.html", ActiveWorkflowID: "operation", ActiveWorkflowType: domain.BootstrapWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour), MissionFiles: []domain.MissionRecord{{ObjectKey: legacyKey, Filename: "legacy.pbo", Status: domain.ArtifactAccepted}, {ObjectKey: modernKey, Filename: "modern.pbo", Status: domain.ArtifactAccepted}}}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.BootstrapWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
	records.session.Infrastructure.InstanceID = "i-123"
	scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: HostContentSnapshot(records.session), DeadlineAt: now.Add(30 * time.Minute)}
	issuer := HostObjectIssuer{Authority: HostAccessAuthority{Records: records, Instances: instanceAuthority(true)}, Script: domain.HostObjectRequest{Purpose: domain.HostObjectBootstrapScript, Slot: "bootstrap-script", Key: "platform/bootstrap/script.sh", SHA256: hostBase64Digest(strings.Repeat("b", 64)), MaxBytes: 1024 * 1024}}
	inspector := &legacyInspector{}
	configured, objects, err := issuer.WorkflowReadInventory(context.Background(), scope, inspector)
	if err != nil || len(objects) != 4 || inspector.calls != 2 || len(configured.VerifiedLegacyReads) != 2 || issuer.VerifiedLegacyReads != nil {
		t.Fatal("inventory did not derive isolated accepted reads", err)
	}
	if objects[1].Key != modernKey || objects[2].Key != legacyKey || objects[2].VersionID != "version-1" || objects[2].SHA256 == "" {
		t.Fatal("mission inventory order or pinned legacy identity invalid")
	}
	_, replayed, err := configured.WorkflowReadInventory(context.Background(), scope, nil)
	if err != nil || !reflect.DeepEqual(objects, replayed) || inspector.calls != 2 {
		t.Fatal("replay reinspected or replaced legacy bytes", err)
	}
	records.session.Version = 7
	configured.Signer = manifestSigner{expiry: now.Add(10 * time.Minute)}
	recovery := &manifestRecovery{}
	manifestIssuer := HostManifestIssuer{Inputs: configured, Attempts: recovery, Exchange: recovery}
	environment := map[string]string{"STEAM_EXCHANGE_REFERENCE_B64": "exchange-1", "STEAM_EXCHANGE_GET_URL_B64": "https://exchange.example.test/original"}
	if _, err := manifestIssuer.IssueWithEnvironment(context.Background(), scope, objects, environment); err != nil {
		t.Fatal(err)
	}
	// A fresh worker has no command-local legacy references. Recovery must obtain
	// them from the persisted version, without reading a newer accepted object.
	manifestIssuer.Inputs.VerifiedLegacyReads = nil
	recoveredInputs, recoveredObjects, recoveredEnvironment, err := manifestIssuer.RecoverReadInventory(context.Background(), scope)
	if err != nil || !reflect.DeepEqual(objects, recoveredObjects) || !reflect.DeepEqual(environment, recoveredEnvironment) || len(recoveredInputs.VerifiedLegacyReads) != 2 || inspector.calls != 2 || recovery.writes != 1 {
		t.Fatal("fresh worker did not recover pinned inventory/context", err)
	}
	manifestIssuer.Inputs = recoveredInputs
	if _, err := manifestIssuer.IssueWithEnvironment(context.Background(), scope, recoveredObjects, recoveredEnvironment); err != nil || recovery.writes != 1 {
		t.Fatal("recovered inventory did not replay original publication", err)
	}
	manifestIssuer.Inputs.Script.Key = "platform/bootstrap/new-script.sh"
	if _, objects, values, err := manifestIssuer.RecoverReadInventory(context.Background(), scope); err == nil || objects != nil || values != nil {
		t.Fatal("changed deployment recovered old command context")
	}
	broken := issuer
	broken.Script.SHA256 = ""
	if _, objects, err := broken.WorkflowReadInventory(context.Background(), scope, inspector); err == nil || objects != nil || inspector.calls != 2 {
		t.Fatal("missing script digest accepted or legacy read started")
	}
	records.session.ConfigurationRevision++
	if _, objects, err := configured.WorkflowReadInventory(context.Background(), scope, inspector); err == nil || objects != nil || inspector.calls != 2 {
		t.Fatal("content drift released inventory or started read")
	}
}
