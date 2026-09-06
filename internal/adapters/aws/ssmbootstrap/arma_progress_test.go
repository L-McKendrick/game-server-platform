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

func TestArmaPercentageIsBoundedAndCleared(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"ARMA_SERVER:0", "Arma 3 server files (0%)"}, {"ARMA_SERVER:88", "Arma 3 server files (88%)"}, {"ARMA_SERVER:100", "Arma 3 server files (100%)"},
		{"ARMA_SERVER:101", ""}, {"ARMA_SERVER:-1", ""}, {"ARMA_SERVER:01", ""}, {"ARMA_SERVER:88 password=secret", ""},
		{"ARMA_SERVER:88\nGSP_ACTIVITY:ARMA_SERVER", "Arma 3 server files"}, {"ARMA_SERVER:88\nGSP_ACTIVITY:", ""},
	} {
		if got := parseActivity("GSP_ACTIVITY:" + tt.input); got != tt.want {
			t.Fatalf("%q: %q", tt.input, got)
		}
	}
}

func TestArmaSamplerUsesLatestPhaseAndStopsPromptly(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(source), "\r\n", "\n")
	start, end := strings.Index(s, "arma_download_activity() {"), strings.Index(s, "\ndownload_workshop_item() {")
	if start < 0 || end < start {
		t.Fatal("missing Steam functions")
	}
	harness := `set -euo pipefail
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
` + s[start:end] + `
printf 'Update state (0x61) downloading, progress: 40.20 (4 / 10)\rUpdate state (0x61) downloading, progress: 88.40 (8 / 10)\n' > "$work/output"
[ "$(arma_download_activity "$work/output")" = ARMA_SERVER:88 ]
printf 'Update state (0x81) verifying, progress: 2.00 (2 / 100)\n' >> "$work/output"
[ "$(arma_download_activity "$work/output")" = ARMA_SERVER ]
printf 'Update state (0x61) downloading, progress: 101.00 (2 / 100)\n' > "$work/output"
[ "$(arma_download_activity "$work/output")" = ARMA_SERVER ]
log() { :; }
activity() { printf '%s\n' "$1" >> "$work/activity"; }
runuser() { printf 'Logged in OK\n'; sleep 0.2; return 0; }
STEAM_AUTH_ROOT="$work"; ROOT="$work"; STEAM_AUTH_VALID=false
run_steamcmd "$work/runfile" arma
[ "$STEAM_AUTH_VALID" = true ]
[ -s "$work/activity" ]
! jobs -pr | grep -q .
[ ! -f "$work/steamcmd-output.$$.log" ]
runuser() { printf 'connection timeout\n'; return 1; }
code=0; run_steamcmd "$work/runfile" arma || code=$?
[ "$code" = 75 ]
! jobs -pr | grep -q .
mark_steam_reauthorization_required() { :; }
runuser() { printf 'Steam Guard secret-do-not-publish\n'; return 1; }
code=0; run_steamcmd "$work/runfile" arma 2>/dev/null || code=$?
[ "$code" = 42 ]
[ "$STEAM_AUTH_VALID" = false ]
! grep -q secret "$work/activity"
! jobs -pr | grep -q .

# Exercise the real publisher with a slow external command, not an activity stub.
mkdir "$work/bin"
printf '#!/usr/bin/env bash\ntouch "$UPLOAD_STARTED"\nexec sleep 30\n' > "$work/bin/aws"
chmod +x "$work/bin/aws"
export PATH="$work/bin:$PATH" UPLOAD_STARTED="$work/upload-started"
PROGRESS_FILE="$work/progress"; ASSETS_BUCKET=test; PROGRESS_KEY=test; AWS_REGION=test
activity() { printf '%s\n' "$1" > "$PROGRESS_FILE"; publish_progress; }
runuser() { for n in $(seq 1 100); do [ ! -f "$UPLOAD_STARTED" ] || return 0; sleep 0.01; done; return 1; }
run_steamcmd "$work/runfile" arma
[ -f "$UPLOAD_STARTED" ]
! jobs -pr | grep -q .
`
	bash, err := bashExecutable()
	if err != nil {
		t.Skip("bash unavailable")
	}
	path := filepath.Join(t.TempDir(), "arma-progress.sh")
	if err := os.WriteFile(path, []byte(harness), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := time.Now()
	if output, err := exec.CommandContext(ctx, bash, path).CombinedOutput(); err != nil {
		t.Fatalf("sampler: %v: %s", err, output)
	}
	if time.Since(started) > 4*time.Second {
		t.Fatal("sampler retained a background process after SteamCMD returned")
	}
}
