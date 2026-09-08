package ssmbootstrap

import (
	"context"
	"encoding/base64"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRestartCommandBindsHostModeAndPendingServerMods(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	for _, pending := range []bool{false, true} {
		fake := &fakeSSM{}
		runner, err := New(fake, testConfig())
		if err != nil {
			t.Fatal(err)
		}
		session := domain.Session{ID: "session-1", DisplayName: "Test", MissionObjectKey: "sessions/session-1/input/mission.pbo", PresetObjectKey: "sessions/session-1/input/preset.html", LifecycleState: domain.StateRestarting, ActiveWorkflowID: "restart-1", ActiveWorkflowType: domain.RestartWorkflowType, Infrastructure: domain.Infrastructure{InstanceID: "i-1", DataVolumeID: "vol-1"}}
		if pending {
			session.PendingServerPresetRevision = domain.PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/server-presets/v2.html", Status: domain.PresetRevisionApplying, StagedAt: now, ApplyWorkflowID: "restart-1", ApplyStartedAt: now}
		}
		_, err = runner.StartContent(context.Background(), session, domain.WorkshopTarget("all"), true)
		if err != nil {
			t.Fatal(err)
		}
		script := fake.sent.Parameters["commands"][0]
		if !strings.HasPrefix(script, bashShebang+"export GSP_OPERATION_MODE=restart\n") || !strings.Contains(*fake.sent.Comment, "gsp:restart:") {
			t.Fatal("wrong restart command boundary")
		}
		want := "export RESTART_DOWNLOADS=false"
		if pending {
			want = "export RESTART_DOWNLOADS=true"
			if !strings.Contains(script, base64.StdEncoding.EncodeToString([]byte(session.PendingServerPresetRevision.PresetObjectKey))) {
				t.Fatal("pending server mods omitted")
			}
		}
		if !strings.Contains(script, want) {
			t.Fatal("wrong download gate")
		}
		assertBashSyntax(t, []byte(script))
	}
}

func TestRestartHostLeavesVoiceAloneAndSuppressesCompletedReplay(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(source), "\r\n", "\n")
	a := strings.Index(s, "launch_and_verify() {")
	b := strings.Index(s[a:], "\n}\n") + a + 3
	c := strings.Index(s, "if [ \"$GSP_OPERATION_MODE\" = restart ]; then\n  [ -d")
	d := strings.Index(s[c:], "if [ \"$GSP_OPERATION_MODE\" = workshop_sync ]; then") + c
	if a < 0 || b < a || c < 0 || d < c {
		t.Fatal("restart shell boundaries missing")
	}
	harness := `set -euo pipefail
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
ROOT="$work"; STATE_DIR="$work/state"; LOG_DIR="$work/log"; WORKFLOW_ID=restart-1
mkdir -p "$ROOT/arma3"; printf '#!/usr/bin/env bash\nexit 0\n' > "$ROOT/arma3/arma3server_x64"; chmod +x "$ROOT/arma3/arma3server_x64"
GSP_OPERATION_MODE=restart; RESTART_DOWNLOADS=false; VANILLA_MODE=false
STEAM_AUTH_ACTIVE=false; STEAM_AUTH_LOCK_OWNER=""; TEAMSPEAK_ENABLED=true
log(){ :; }; checkpoint(){ :; }; scrub_persistent_steam_auth(){ :; }
begin_steam_auth(){ echo unexpected-auth >&2; return 1; }
sync_workshop_content(){ :; }; deploy_content(){ :; }
systemctl(){ printf '%s\n' "$*" >> "$work/services"; }
ss(){ printf 'UNCONN 0 0 0.0.0.0:2302\n'; }
` + s[a:b] + "\n(" + s[c:d] + ")\n(" + s[c:d] + ")\n" + `
[ "$(grep -c '^restart arma3-server.service$' "$work/services")" = 1 ]
! grep -q teamspeak "$work/services"
`
	bash, err := bashExecutable()
	if err != nil {
		t.Skip("bash unavailable")
	}
	path := filepath.Join(t.TempDir(), "restart.sh")
	if err := os.WriteFile(path, []byte(harness), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, bash, path).CombinedOutput(); err != nil {
		t.Fatalf("restart shell: %v: %s", err, out)
	}
}

func TestRestartReusesPreviouslyDispatchedCommand(t *testing.T) {
	client := &fakeSSM{commands: &ssm.ListCommandsOutput{Commands: []types.Command{{Comment: aws.String("gsp:restart:session-1:restart-1"), InstanceIds: []string{"i-1"}, CommandId: aws.String("existing-1")}}}}
	runner, err := New(client, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	id, err := runner.StartContent(context.Background(), domain.Session{ID: "session-1", ActiveWorkflowID: "restart-1", ActiveWorkflowType: domain.RestartWorkflowType, LifecycleState: domain.StateRestarting, Infrastructure: domain.Infrastructure{InstanceID: "i-1"}}, domain.WorkshopTarget("all"), true)
	if err != nil || id != "existing-1" || client.sent != nil {
		t.Fatalf("id=%s err=%v sent=%#v", id, err, client.sent)
	}
}

func TestRestartPreservesNewWorkshopMissionOverPreviouslyAcceptedFile(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(source), "\r\n", "\n")
	deployStart := strings.Index(s, "deploy_content() {")
	restartStart := strings.Index(s, "if [ \"$GSP_OPERATION_MODE\" = restart ]; then\n  [ -d")
	if deployStart < 0 || restartStart < 0 {
		t.Fatal("restart shell boundaries missing")
	}
	deployEnd := strings.Index(s[deployStart:], "  safe_name=")
	restartEnd := strings.Index(s[restartStart:], "if [ \"$GSP_OPERATION_MODE\" = workshop_sync ]; then")
	if deployEnd < 0 || restartEnd < 0 {
		t.Fatal("mission deployment shell boundaries missing")
	}
	bash, err := bashExecutable()
	if err != nil {
		t.Skip("bash unavailable")
	}
	for _, mode := range []string{"manifest", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			harness := `set -Eeuo pipefail
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
ROOT="$work"; STATE_DIR="$work/state"; LOG_DIR="$work/log"; WORKFLOW_ID=restart-mission
mkdir -p "$ROOT/arma3/mpmissions"; printf '#!/usr/bin/env bash\nexit 0\n' > "$ROOT/arma3/arma3server_x64"; chmod +x "$ROOT/arma3/arma3server_x64"
printf old > "$work/accepted.pbo"
MISSION_KEY=missions/scenario.Altis.pbo; MISSION_TEMPLATE=scenario.Altis
MISSION_MANIFEST="$(printf '%s\tscenario.Altis.pbo\t%s' "$(sha256sum "$work/accepted.pbo" | cut -d' ' -f1)" "$MISSION_KEY")"
if [ MODE = legacy ]; then MISSION_MANIFEST=''; fi
GSP_OPERATION_MODE=restart; RESTART_DOWNLOADS=true; STEAM_AUTH_ACTIVE=false
ASSETS_BUCKET=assets; AWS_REGION=test
log(){ :; }; checkpoint(){ :; }; chown(){ :; }; systemctl(){ :; }
begin_steam_auth(){ STEAM_AUTH_ACTIVE=true; }; persist_steam_auth(){ :; }; cleanup_steam_auth(){ STEAM_AUTH_ACTIVE=false; }
aws(){ cp "$work/accepted.pbo" "$4"; }
sync_workshop_content(){ printf new > "$ROOT/arma3/mpmissions/scenario.Altis.pbo"; }
launch_and_verify(){ [ "$(cat "$ROOT/arma3/mpmissions/scenario.Altis.pbo")" = new ]; }
` + s[deployStart:deployStart+deployEnd] + "}\n" + s[restartStart:restartStart+restartEnd]
			harness = strings.ReplaceAll(harness, "[ MODE = legacy ]", "[ "+mode+" = legacy ]")
			path := filepath.Join(t.TempDir(), "restart-mission.sh")
			if err := os.WriteFile(path, []byte(harness), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if out, err := exec.CommandContext(ctx, bash, path).CombinedOutput(); err != nil {
				t.Fatalf("restarted mission must contain the newly synchronized revision: %v: %s", err, out)
			}
		})
	}
}
