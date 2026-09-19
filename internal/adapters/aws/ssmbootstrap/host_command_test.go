package ssmbootstrap

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type fakeHostCommandAccess struct{ values map[string]string }

func (access *fakeHostCommandAccess) PrepareHostCommand(ctx context.Context, sessionID, stage string, rollback bool, timeout time.Duration, build ports.HostCommandEnvironment) (ports.HostAccessReference, error) {
	values, err := build(ctx, nil)
	if err != nil {
		return ports.HostAccessReference{}, err
	}
	access.values = values
	mode := ""
	if rollback {
		mode = "rollback"
	}
	return ports.HostAccessReference{SchemaVersion: domain.HostAccessSchemaVersion, Scope: domain.HostAccessScope{SessionID: sessionID, GuildID: "guild-1", OperationID: values["WORKFLOW_ID_B64"], InstanceID: "i-1", AttemptID: stage, SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: time.Now().Add(timeout), CommandMode: mode}, Generation: 1, URL: "https://assets.example.test/manifest", SHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), SizeBytes: 100, ExpiresAt: time.Now().Add(10 * time.Minute)}, nil
}

// Existing context regressions inspect the environment builder directly. Asset
// delivery is covered separately by the compact SSM command and HTTPS tests.
func testContextCommand(runner *Runner, session domain.Session) (string, error) {
	values, _, err := runner.commandEnvironment(context.Background(), session, false, false, "bootstrap", true, nil)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	output.WriteString(bashShebang)
	for key, value := range values {
		output.WriteString("export " + key + "='" + base64.StdEncoding.EncodeToString([]byte(value)) + "'\n")
	}
	output.WriteString(fmt.Sprintf("export VANILLA_MODE=%t\nexport TEAMSPEAK_ENABLED=%t\n", session.Vanilla, session.TeamSpeakEnabled))
	return output.String(), nil
}

func TestCompactCommandHandlesLargeAcceptedInventory(t *testing.T) {
	config := testConfig()
	runner, err := New(&fakeSSM{}, config)
	if err != nil {
		t.Fatal(err)
	}
	session := domain.Session{ID: "session-1", GuildID: "guild-1", ActiveWorkflowID: "workflow-1", Vanilla: true, ConfiguredMission: domain.DefaultMissionSelection(), CurrentMission: domain.DefaultMissionSelection(), Infrastructure: domain.Infrastructure{InstanceID: "i-1", DataVolumeID: "vol-1"}}
	for index := 0; index < 1000; index++ {
		filename := fmt.Sprintf("mission-%04d.Altis.pbo", index)
		session.MissionFiles = append(session.MissionFiles, domain.MissionRecord{ObjectKey: "sessions/session-1/input/missions/" + strings.Repeat("a", 64) + "-" + filename, Filename: filename, Status: domain.ArtifactAccepted})
	}
	script, _, err := runner.commandMode(context.Background(), session, false, false, "bootstrap", true)
	if err != nil || len(script) > domain.MaxHostAccessCommandBytes {
		t.Fatal("large inventory exceeded compact command budget", err)
	}
	values := config.HostAccess.(*fakeHostCommandAccess).values
	if strings.Count(values["MISSION_MANIFEST_B64"], "\n") != 1000 || len(values["MISSION_MANIFEST_B64"]) < 100000 {
		t.Fatal("large inventory truncated")
	}
	if strings.Contains(script, session.MissionFiles[999].ObjectKey) || strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(values["MISSION_MANIFEST_B64"]))) {
		t.Fatal("large inventory leaked into SSM command")
	}
	assertBashSyntax(t, []byte(script))
}
