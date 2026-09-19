package ssmbootstrap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkshopCheckpointsReplaySameWorkflowAndPublishNewWorkflow(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(source), "\r\n", "\n")
	start := strings.Index(s, "for stage in install_steamcmd install_arma sync_workshop_content deploy_content install_teamspeak; do")
	end := strings.Index(s, "\nprogress_stage launch_and_verify\nlog \"starting stage launch_and_verify\"")
	if start < 0 || end < start {
		t.Fatal("stage checkpoint boundaries missing")
	}
	loop := s[start:end]
	harness := `set -euo pipefail
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
STATE_DIR="$work/state"; mkdir -p "$STATE_DIR"
WORKSHOP_MISSION_REVISION=mission; PRESET_REVISION=1; SERVER_PRESET_REVISION=2; MOD_CONFIG_REVISION=3
CONTENT_REVISION=` + strings.Repeat("a", 64) + `
PRESET_ROLLBACK=false; STEAM_AUTH_ACTIVE=false
log(){ :; }; checkpoint(){ :; }; progress_stage(){ :; }
begin_steam_auth(){ STEAM_AUTH_ACTIVE=true; }; persist_steam_auth(){ :; }; cleanup_steam_auth(){ STEAM_AUTH_ACTIVE=false; }
install_steamcmd(){ printf 'install\n' >> "$work/actions"; }; install_arma(){ :; }; deploy_content(){ :; }; install_teamspeak(){ :; }
sync_workshop_content(){ printf 'publish:%s\n' "$WORKFLOW_ID" >> "$work/actions"; }
WORKFLOW_ID=workflow-1
` + loop + "\n" + loop + `
WORKFLOW_ID=workflow-2
` + loop + `
[ "$(grep -c '^install$' "$work/actions")" = 1 ]
[ "$(grep -c '^publish:workflow-1$' "$work/actions")" = 1 ]
[ "$(grep -c '^publish:workflow-2$' "$work/actions")" = 1 ]
`
	runWorkshopHarness(t, harness)
}

func TestConfigurationOnlyRestartDoesNotUploadWorkshopOutputs(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(source), "\r\n", "\n")
	start := strings.Index(s, "sync_workshop_content() {")
	if start < 0 {
		t.Fatal("sync function missing")
	}
	end := strings.Index(s[start:], "\n}\n") + start + 3
	if end <= start {
		t.Fatal("sync boundary missing")
	}
	harness := `set -euo pipefail
GSP_OPERATION_MODE=restart; RESTART_DOWNLOADS=false
progress_stage(){ echo unexpected-sync >&2; return 1; }
` + s[start:end] + "\nsync_workshop_content\n"
	runWorkshopHarness(t, harness)
}

func runWorkshopHarness(t *testing.T, harness string) {
	t.Helper()
	bash, err := bashExecutable()
	if err != nil {
		t.Skip("bash unavailable")
	}
	file := filepath.Join(t.TempDir(), "workshop-resume.sh")
	if err := os.WriteFile(file, []byte(harness), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, bash, file).CombinedOutput(); err != nil {
		t.Fatalf("Workshop resume: %v: %s", err, output)
	}
}
