package ssmbootstrap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestWorkshopActivityIsBoundedAndCleared(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"GSP_ACTIVITY:WORKSHOP_ITEM:450814997:3:7", "Workshop item 450814997 (3/7)"},
		{"GSP_ACTIVITY:WORKSHOP_ITEM:450814997:1:1", "Workshop item 450814997 (1/1)"},
		{"GSP_ACTIVITY:WORKSHOP_ITEM:450814997:3:7\nGSP_ACTIVITY:", ""},
		{"GSP_ACTIVITY:WORKSHOP_ITEM:450814997:3:7\nGSP_CHECKPOINT:CONFIGURATION_READY", ""},
		{"GSP_ACTIVITY:WORKSHOP_ITEM:450814997:8:7", ""},
		{"GSP_ACTIVITY:WORKSHOP_ITEM:450814997:0:7", ""},
		{"GSP_ACTIVITY:WORKSHOP_ITEM:450814997:1:251", ""},
		{"GSP_ACTIVITY:WORKSHOP_ITEM:0450814997:1:7", ""},
		{"GSP_ACTIVITY:WORKSHOP_ITEM:450814997:01:7", ""},
		{"GSP_ACTIVITY:WORKSHOP_ITEM:450814997:1:7 password=secret", ""},
	} {
		if got := parseActivity(test.input); got != test.want {
			t.Fatalf("%q: got %q want %q", test.input, got, test.want)
		}
	}
}

func TestSnapshotStagesAndOversizedFallback(t *testing.T) {
	for _, test := range []struct {
		body   string
		active bool
	}{
		{"GSP_STAGE:install_arma\n", true}, {"GSP_STAGE:sync_workshop_content\n", true},
		{"GSP_STAGE:install_steamcmd\n", false}, {"GSP_ACTIVITY:ARMA_SERVER\n", false},
		{"GSP_STAGE:install_arma\nGSP_STAGE:content_ready\n", false},
		{"GSP_STAGE:sync_workshop_content\nGSP_STAGE:unknown\n", false},
		{"GSP_STAGE:install_arma\n" + strings.Repeat("x", 16*1024), false},
	} {
		runner, err := New(&fakeSSM{invocation: &ssm.GetCommandInvocationOutput{Status: types.CommandInvocationStatusInProgress}}, testConfig())
		if err != nil {
			t.Fatal(err)
		}
		runner.WithProgressStore(&fakeProgress{body: test.body})
		got, err := runner.ObserveProgress(context.Background(), "i-1", "command-1", "session-1", "workflow-1")
		if err != nil || got.InstallationActive != test.active {
			t.Fatalf("active=%v want=%v error=%v", got.InstallationActive, test.active, err)
		}
	}
}

func TestHostSnapshotRetainsOnlyLatestActivityAndStage(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	source := strings.ReplaceAll(string(script), "\r\n", "\n")
	start := strings.Index(source, "checkpoint() {")
	end := strings.Index(source, "\nprepare_host() {")
	if start < 0 || end < start {
		t.Fatal("missing progress functions")
	}
	harness := `set -euo pipefail
PROGRESS_FILE="$(mktemp)"
trap 'rm -f "$PROGRESS_FILE"' EXIT
publish_progress() { :; }
` + source[start:end] + `
checkpoint MODS_APPLIED
progress_stage sync_workshop_content
activity WORKSHOP_ITEM:450814997:2:7
activity WORKSHOP_ITEM:450814997:2:7
activity WORKSHOP_ITEM:705986840:3:7
[ "$(grep -c '^GSP_ACTIVITY:' "$PROGRESS_FILE")" = 1 ]
grep -qx 'GSP_ACTIVITY:WORKSHOP_ITEM:705986840:3:7' "$PROGRESS_FILE"
activity ""
! grep -q '^GSP_ACTIVITY:WORKSHOP_ITEM:' "$PROGRESS_FILE"
progress_stage deploy_content
grep -qx 'GSP_CHECKPOINT:MODS_APPLIED' "$PROGRESS_FILE"
grep -qx 'GSP_STAGE:deploy_content' "$PROGRESS_FILE"
! grep -q '^GSP_ACTIVITY:' "$PROGRESS_FILE"
`
	bash, err := bashExecutable()
	if err != nil {
		t.Skip("bash unavailable")
	}
	path := filepath.Join(t.TempDir(), "progress.sh")
	if err := os.WriteFile(path, []byte(harness), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, bash, filepath.ToSlash(path)).CombinedOutput(); err != nil {
		t.Fatalf("snapshot harness: %v\n%s", err, output)
	}
}

func TestHostWorkshopCacheAndRetriesKeepItemPosition(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	source := strings.ReplaceAll(string(script), "\r\n", "\n")
	extract := func(start, end string) string {
		t.Helper()
		first, last := strings.Index(source, start), strings.Index(source, end)
		if first < 0 || last <= first {
			t.Fatalf("missing function %s", start)
		}
		return source[first:last]
	}
	harness := `set -euo pipefail
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "$TEST_ROOT"' EXIT
ROOT="$TEST_ROOT/root"
PROGRESS_FILE="$TEST_ROOT/progress"
WORKSHOP_STAGING_ROOT="$TEST_ROOT/staging"
touch "$PROGRESS_FILE"
publish_progress() { :; }
log() { :; }
chown() { :; }
require_workshop_space() { :; }
lowercase_tree() { :; }
steam_login_file() { :; }
mktemp() {
 if [[ "${1:-}" == /run/* ]]; then command mktemp "$TEST_ROOT/run.XXXXXX"; else command mktemp "$@"; fi
}
calls=0
run_steamcmd() {
 grep -qx 'GSP_ACTIVITY:WORKSHOP_ITEM:450814997:2:3' "$PROGRESS_FILE"
 calls=$((calls + 1))
 [ "$calls" -ge 3 ] || return 75
 mkdir -p "$WORKSHOP_STAGING_ROOT/steamapps/workshop/content/107410/450814997"
 printf payload > "$WORKSHOP_STAGING_ROOT/steamapps/workshop/content/107410/450814997/mod.pbo"
}
` + extract("checkpoint() {", "\nprepare_host() {") +
		extract("download_workshop_item() {", "\nprepare_workshop_staging() {") +
		extract("ensure_workshop_revision_root() {", "\nrecord_workshop_sync_result() {") + `
revision_root="$ROOT/workshop/mod-revisions/client-1"
ensure_staged_workshop_mod 450814997 0 "$revision_root" 3 2
[ "$calls" = 3 ]
[ -f "$revision_root/450814997/mod.pbo" ]
! grep -q '^GSP_ACTIVITY:WORKSHOP_ITEM:' "$PROGRESS_FILE"
# A valid materialized item is skipped without pretending to download it again.
ensure_staged_workshop_mod 450814997 0 "$revision_root" 3 2
[ "$calls" = 3 ]
! grep -q '^GSP_ACTIVITY:WORKSHOP_ITEM:' "$PROGRESS_FILE"
`
	bash, err := bashExecutable()
	if err != nil {
		t.Skip("bash unavailable")
	}
	path := filepath.Join(t.TempDir(), "workshop-progress.sh")
	if err := os.WriteFile(path, []byte(harness), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, bash, filepath.ToSlash(path)).CombinedOutput(); err != nil {
		t.Fatalf("Workshop harness: %v\n%s", err, output)
	}
}
