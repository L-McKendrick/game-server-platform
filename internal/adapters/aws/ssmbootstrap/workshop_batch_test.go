package ssmbootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func workshopPresetFunctions(t *testing.T) string {
	t.Helper()
	prefix := ""
	if runtime.GOOS == "windows" {
		python, err := exec.LookPath("python")
		if err != nil {
			t.Skip("Python unavailable for preset extraction harness")
		}
		// Windows Python writes CRLF; the deployed Linux interpreter writes LF.
		prefix = "python3(){ '" + strings.ReplaceAll(filepath.ToSlash(python), "'", "'\"'\"'") + "' \"$@\" | tr -d '\\r'; }\n"
	}
	return prefix + workshopFunctions(t, "extract_workshop_ids() {", "\ninstall_workshop() (")
}

func workshopFunctions(t *testing.T, start, end string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "bootstrap", "arma3-bootstrap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	a, b := strings.Index(s, start), strings.Index(s, end)
	if a < 0 || b <= a {
		t.Fatalf("missing functions %s .. %s", start, end)
	}
	return s[a:b]
}

func TestWorkshopBatchConfirmationRetriesAndCleanup(t *testing.T) {
	for _, scenario := range []string{"success", "transient", "timeout", "unconfirmed", "guard", "private", "removed", "mixed-private", "mixed-removed", "invalid", "empty", "login-failure"} {
		t.Run(scenario, func(t *testing.T) {
			harness := `set -euo pipefail
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
ROOT="$work/root"; STEAM_AUTH_ROOT="$work/auth"; mkdir -p "$STEAM_AUTH_ROOT"
scenario=` + scenario + `
log(){ :; }; activity(){ printf '%s\n' "$1" >> "$work/activity"; }
mark_steam_authorization_valid(){ :; }; mark_steam_reauthorization_required(){ : > "$work/reauth"; }
require_workshop_space(){ :; }
steam_login_file(){ [ "$scenario" != login-failure ] || return 42; echo 'login test' >> "$1"; }
mktemp(){ if [[ "${1:-}" == /run/* ]]; then command mktemp "$work/temporary.XXXXXX"; else command mktemp "$@"; fi; }
runuser(){
  local runfile="${@: -1}" id calls
  calls=$(( $(cat "$work/calls") + 1 )); echo "$calls" > "$work/calls"
  cp "$runfile" "$work/script-$calls"
  case "$scenario" in
    guard) echo 'Steam Guard secret-must-not-escape'; return 1 ;;
    private) echo 'ERROR! access denied secret-must-not-escape'; return 0 ;;
    removed) echo 'ERROR! item removed secret-must-not-escape'; return 0 ;;
    mixed-private) echo 'Connection established'; echo 'Success. Downloaded item 111111111 to somewhere'; echo 'ERROR! access denied secret-must-not-escape'; return 1 ;;
    mixed-removed) echo 'Connection established'; echo 'Success. Downloaded item 111111111 to somewhere'; echo 'ERROR! item removed secret-must-not-escape'; return 1 ;;
    unconfirmed) echo 'Success. Downloaded item 999999999 to somewhere'; return 0 ;;
    timeout) echo 'ERROR! connection timed out secret-must-not-escape'; return 1 ;;
  esac
  while read -r id; do
    if [ "$scenario" = transient ] && [ "$calls" = 1 ] && [ "$id" = 222222222 ]; then
      echo 'ERROR! connection timed out'; return 1
    fi
    echo "Downloading item $id ..."
    echo "Success. Downloaded item $id to somewhere"
  done < <(awk '$1 == "workshop_download_item" {print $3}' "$runfile")
}
` + workshopFunctions(t, "arma_download_activity() {", "\nprepare_workshop_staging() {") + `
# This test drives the actual output classifier; sampler lifecycle is tested separately.
sample_arma_download(){ exec sleep 30; }
echo 0 > "$work/calls"
printf '111111111\t1\t3\n222222222\t2\t3\n111111111\t3\t3\n' > "$work/requests"
case "$scenario" in
 invalid) printf '111111111;quit\t1\t1\n' > "$work/requests" ;;
 empty) : > "$work/requests" ;;
esac
code=0
download_workshop_batch "$work/requests" 2>"$work/error" || code=$?
calls="$(cat "$work/calls")"
case "$scenario" in
 success) [ "$code" = 0 ] && [ "$calls" = 1 ]; [ "$(grep -c '^login ' "$work/script-1")" = 1 ]; [ "$(grep -c '^workshop_download_item ' "$work/script-1")" = 2 ]; [ "$(grep -c '^quit$' "$work/script-1")" = 1 ] ;;
 transient) [ "$code" = 0 ] && [ "$calls" = 2 ]; if grep -q '111111111' "$work/script-2"; then exit 1; fi; grep -q '222222222' "$work/script-2" ;;
 timeout) [ "$code" = 1 ] && [ "$calls" = 3 ]; grep -q ERR_WORKSHOP_DOWNLOAD_TIMEOUT "$work/error" ;;
 unconfirmed) [ "$code" = 1 ] && [ "$calls" = 1 ]; grep -q ERR_WORKSHOP_ITEM_DOWNLOAD "$work/error" ;;
 guard) [ "$code" = 42 ] && [ "$calls" = 1 ]; [ -f "$work/reauth" ] ;;
 private|mixed-private) [ "$code" = 1 ] && [ "$calls" = 1 ]; grep -q ERR_WORKSHOP_VISIBILITY "$work/error" ;;
 removed|mixed-removed) [ "$code" = 1 ] && [ "$calls" = 1 ]; grep -q ERR_WORKSHOP_ITEM_REMOVED "$work/error" ;;
 invalid) [ "$code" = 1 ] && [ "$calls" = 0 ] ;;
 empty) [ "$code" = 0 ] && [ "$calls" = 0 ] ;;
 login-failure) [ "$code" = 42 ] && [ "$calls" = 0 ] ;;
esac
if grep -q secret-must-not-escape "$work/error"; then echo 'raw Steam output escaped' >&2; exit 1; fi
[ -z "$(find "$work" -name 'temporary.*' -print -quit)" ]
[ ! -f "$STEAM_AUTH_ROOT/steamcmd-output.$$.log" ]
printf 'Downloading item 222222222 ...\n' > "$work/output"
[ "$(workshop_download_activity "$work/output" "$work/requests")" = "$(awk '$1 == "222222222" {printf "WORKSHOP_ITEM:%s:%s:%s",$1,$2,$3;exit}' "$work/requests")" ]
`
			runWorkshopHarness(t, harness)
		})
	}
}

func TestWorkshopModsBatchSkipsCacheAndKeepsPerItemValidation(t *testing.T) {
	harness := `set -euo pipefail
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
ROOT="$work/root"; WORKSHOP_STAGING_ROOT="$work/staging"
mkdir -p "$ROOT/config" "$ROOT/arma3"
VANILLA_MODE=false; WORKSHOP_PROMOTE_MODS=true; PRESET_REVISION=1; SERVER_PRESET_REVISION=1
PRESET_KEY=client; SERVER_PRESET_KEY=server; CREATOR_DLC_MODS=''; WORKSHOP_MOD_MANIFEST=''; WORKSHOP_MOD_RESOLUTION=''
log(){ :; }; chown(){ :; }; lowercase_tree(){ :; }; record_workshop_sync_result(){ :; }
cp(){
 if [ "${COPY_FAIL:-false}" = true ] && [[ "${@: -1}" == */.pending-* ]]; then return 1; fi
 command cp "$@"
}
find(){
 if [ "${UNSAFE:-false}" = true ] && [[ "$1" == */222222222 ]] && [[ " $* " == *' -type l '* ]]; then
   printf '%s/unsafe\n' "$1"
 else command find "$@"; fi
}
asset_read(){
 if [ "$1" = client ]; then
   printf '<tr data-type="ModContainer"><td>?id=111111111</td><td data-publishedfileid="222222222"></td><td>?id=111111111</td></tr>\n<tr data-type="DlcContainer"><td data-publishedfileid="1227700"></td></tr><footer>?id=999999999</footer>\n' > "$2"
 else printf '<tr data-type="ModContainer"><td>?id=222222222</td><td>?id=333333333</td></tr>\n' > "$2"; fi
}
mktemp(){ if [[ "${1:-}" == /run/* ]]; then command mktemp "$work/temporary.XXXXXX"; else command mktemp "$@"; fi; }
download_workshop_batch(){
 cp "$1" "$work/requests"
 [ "$(wc -l < "$1")" = 2 ]
 grep -qx $'222222222\t2\t3' "$1"
 grep -qx $'333333333\t3\t3' "$1"
 while read -r id index count; do
   mkdir -p "$WORKSHOP_STAGING_ROOT/steamapps/workshop/content/107410/$id"
   echo payload > "$WORKSHOP_STAGING_ROOT/steamapps/workshop/content/107410/$id/mod.pbo"
 done < "$1"
}
` + workshopFunctions(t, "ensure_workshop_revision_root() {", "\nrecord_workshop_sync_result() {") +
		workshopPresetFunctions(t) + workshopFunctions(t, "install_workshop() (", "\nsync_workshop_content() {") + `
cached="$ROOT/workshop/mod-revisions/client-1"
mkdir -p "$cached/111111111"; echo payload > "$cached/111111111/mod.pbo"
echo '111111111:0' > "$cached/.snapshot-111111111"
install_workshop
[ "$(cat "$ROOT/config/mods.txt")" = '@workshop_111111111;@workshop_222222222' ]
[ "$(cat "$ROOT/config/server-mods.txt")" = '@workshop_333333333' ]
[ -f "$ROOT/workshop/mod-revisions/server-1/.snapshot-333333333" ]
# Force one item to download again and prove unsafe payloads are not marked complete.
rm "$cached/.snapshot-222222222" "$ROOT/workshop/mod-revisions/server-1/.snapshot-333333333"
UNSAFE=true
if install_workshop; then echo 'unsafe batch unexpectedly succeeded' >&2; exit 1; fi
[ ! -f "$cached/.snapshot-222222222" ]
UNSAFE=false; COPY_FAIL=true
if install_workshop; then echo 'copy failure unexpectedly succeeded' >&2; exit 1; fi
[ ! -f "$cached/.snapshot-222222222" ]
[ -z "$(find "$cached" -name '.pending-*' -print -quit)" ]
`
	runWorkshopHarness(t, harness)
}

func TestWorkshopPresetExtractionMatchesTypedRows(t *testing.T) {
	harness := `set -euo pipefail
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
` + workshopPresetFunctions(t) + `
cat > "$work/preset" <<'HTML'
<TR data-type='modcontainer'><td>?id=111111111</td><td>?id=222222222</td>
<td data-publishedfileid='111111111'></td></TR>
<tr data-type="DlcContainer"><td>?id=1227700</td></tr>
<a href="?id=999999999">footer</a>
HTML
[ "$(extract_workshop_ids "$work/preset")" = "$(printf '111111111\n222222222')" ]
printf '<tr data-type="DlcContainer"><td>?id=1227700</td></tr>' > "$work/preset"
[ -z "$(extract_workshop_ids "$work/preset")" ]
if extract_workshop_ids "$work/missing" 2>/dev/null; then echo 'missing input accepted' >&2; exit 1; fi
`
	runWorkshopHarness(t, harness)
}

func TestWorkshopMissionBatchValidationAndReplay(t *testing.T) {
	for _, scenario := range []string{"success", "size-drift", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			harness := `set -euo pipefail
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
ROOT="$work/root"; WORKSHOP_STAGING_ROOT="$work/staging"; SESSION_ID=session
mkdir -p "$ROOT/arma3/mpmissions"
scenario=` + scenario + `
WORKSHOP_MISSION_REVISION=` + strings.Repeat("a", 64) + `
WORKSHOP_MISSION_MANIFEST="$(printf '111111111\t%s\tfirst.pbo\t16\n222222222\t%s\tsecond.pbo\t16\n' "$WORKSHOP_MISSION_REVISION" "$WORKSHOP_MISSION_REVISION")"
log(){ :; }; chown(){ :; }; require_workshop_space(){ :; }; record_workshop_sync_result(){ :; }
find(){
 if [ "$scenario" = symlink ] && [[ "$1" == */111111111 ]] && [[ " $* " == *' -type l '* ]]; then
   printf '%s/unsafe\n' "$1"
 else command find "$@"; fi
}
asset_upload(){ printf '%s\n' "$1" >> "$work/uploads"; }
mktemp(){ if [[ "${1:-}" == /run/* ]]; then command mktemp "$work/temporary.XXXXXX"; else command mktemp "$@"; fi; }
download_workshop_batch(){
 cp "$1" "$work/requests"
 while read -r id index count; do
   mkdir -p "$WORKSHOP_STAGING_ROOT/steamapps/workshop/content/107410/$id"
   printf '1234567890123456' > "$WORKSHOP_STAGING_ROOT/steamapps/workshop/content/107410/$id/mission.pbo"
 done < "$1"
 case "$scenario" in
 size-drift) printf x >> "$WORKSHOP_STAGING_ROOT/steamapps/workshop/content/107410/111111111/mission.pbo" ;;
 symlink) : ;; # Emulate link enumeration because Git Bash may copy links on Windows.
 esac
}
` + workshopFunctions(t, "install_workshop_missions() (", "\ninstall_steamcmd() {") + `
if [ "$scenario" = success ]; then
 install_workshop_missions
 [ "$(wc -l < "$work/requests")" = 2 ]
 [ -f "$ROOT/arma3/mpmissions/first.pbo" ] && [ -f "$ROOT/arma3/mpmissions/second.pbo" ]
 install_workshop_missions
 [ ! -s "$work/requests" ]
 [ "$(grep -c '^workshop-resolution$' "$work/uploads")" = 2 ]
else
 if install_workshop_missions 2>"$work/error"; then echo 'invalid mission batch succeeded' >&2; exit 1; fi
 [ ! -f "$ROOT/arma3/mpmissions/first.pbo" ]
 [ ! -f "$work/uploads" ]
 grep -q ERR_WORKSHOP_SCENARIO "$work/error"
fi
[ -z "$(find "$work" -name 'temporary.*' -print -quit)" ]
`
			runWorkshopHarness(t, harness)
		})
	}
}

func TestWorkshopBatchSamplerStopsAndIgnoresUnrequestedItems(t *testing.T) {
	harness := `set -euo pipefail
work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
STEAM_AUTH_ROOT="$work"; ROOT="$work"
activity(){ printf '%s\n' "$1" >> "$work/activity"; }
log(){ :; }; mark_steam_authorization_valid(){ :; }
runuser(){
 printf 'Downloading item 111111111 ...\n'; sleep 1
 printf 'Success. Downloaded item 111111111 to somewhere\n'
}
` + workshopFunctions(t, "arma_download_activity() {", "\nprepare_workshop_staging() {") + `
printf '111111111\t2\t3\n' > "$work/requests"
run_steamcmd "$work/runfile" workshop "$work/requests" "$work/success"
grep -qx WORKSHOP_ITEM:111111111:2:3 "$work/activity"
grep -qx 111111111 "$work/success"
if jobs -pr | grep -q .; then echo 'sampler still running' >&2; exit 1; fi
[ ! -f "$work/steamcmd-output.$$.log" ]
printf 'Downloading item 999999999 ...\n' > "$work/output"
[ -z "$(workshop_download_activity "$work/output" "$work/requests")" ]
`
	runWorkshopHarness(t, harness)
}
