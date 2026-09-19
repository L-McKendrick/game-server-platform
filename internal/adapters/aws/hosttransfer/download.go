package hosttransfer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

//go:embed download.py
var downloader []byte

// InstallShell installs a version-qualified root-only helper atomically. Caller
// commands must remain inside the bounded SSM command budget. It carries no AWS
// credentials and emits no capability URL diagnostics.
func InstallShell() string {
	digest := sha256.Sum256(downloader)
	var packed bytes.Buffer
	zipper := gzip.NewWriter(&packed)
	_, _ = zipper.Write(downloader)
	_ = zipper.Close()
	return "set +x\n" +
		"if ! command -v python3 >/dev/null 2>&1; then\n" +
		"  apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y python3 ca-certificates || { echo 'ERR_HOST_ACCESS: Python prerequisite unavailable' >&2; exit 1; }\nfi\n" +
		"host_access_dir=/run/gsp-host-access\nmkdir -p \"$host_access_dir\"\nchmod 700 \"$host_access_dir\"\n" +
		"HOST_ACCESS_HELPER=\"$host_access_dir/download-" + hex.EncodeToString(digest[:]) + ".py\"\n" +
		"if [ ! -f \"$HOST_ACCESS_HELPER\" ]; then\n" +
		"  host_helper_pending=\"$(mktemp \"$host_access_dir/.download.XXXXXX\")\"\n" +
		"  printf '%s' '" + base64.StdEncoding.EncodeToString(packed.Bytes()) + "' | base64 -d | python3 -c 'import gzip,sys;sys.stdout.buffer.write(gzip.decompress(sys.stdin.buffer.read()))' >\"$host_helper_pending\"\n" +
		"  chmod 600 \"$host_helper_pending\"\nmv -f \"$host_helper_pending\" \"$HOST_ACCESS_HELPER\"\nfi\nexport HOST_ACCESS_HELPER\n"
}

// ReferenceShell installs a compact reference and exports the root-only manifest
// and environment paths. The caller owns final command budget and lifetime cleanup.
func ReferenceShell(reference ports.HostAccessReference) (string, error) {
	payload, err := reference.Encode(time.Now().UTC())
	if err != nil {
		return "", err
	}
	directory := "/run/gsp-host-access/" + reference.Scope.SessionID + "/" + reference.Scope.OperationID + "/" + reference.Scope.AttemptID
	command := InstallShell() +
		"HOST_ACCESS_ATTEMPT_DIR='" + directory + "'\n" +
		"host_reference=\"$(printf '%s' '" + base64.StdEncoding.EncodeToString(payload) + "' | base64 -d)\"\n" +
		"python3 \"$HOST_ACCESS_HELPER\" install \"$host_reference\" \"$HOST_ACCESS_ATTEMPT_DIR\"\nunset host_reference\n" +
		"HOST_ACCESS_MANIFEST=\"$HOST_ACCESS_ATTEMPT_DIR/manifest.json\"\n" +
		"HOST_ACCESS_ENVIRONMENT=\"$HOST_ACCESS_ATTEMPT_DIR/environment.sh\"\n" +
		"python3 \"$HOST_ACCESS_HELPER\" environment \"$HOST_ACCESS_MANIFEST\" \"$HOST_ACCESS_ENVIRONMENT\"\n" +
		". \"$HOST_ACCESS_ENVIRONMENT\"\nexport HOST_ACCESS_ATTEMPT_DIR HOST_ACCESS_MANIFEST\n"
	if len(command) > domain.MaxHostAccessCommandBytes {
		return "", fmt.Errorf("host reference command exceeds budget")
	}
	return command, nil
}

// InlineMissionShell handles the single short live-copy read without adding an
// S3 exchange or recovery record. All other inventories use compact references.
func InlineMissionShell(manifest ports.HostAccessManifest) (string, error) {
	payload, err := manifest.Encode(time.Now().UTC())
	if err != nil {
		return "", err
	}
	if len(payload) > domain.MaxHostAccessReferenceBytes || len(manifest.Objects) != 1 || manifest.Objects[0].Object.Purpose != domain.HostObjectMission {
		return "", fmt.Errorf("inline mission manifest invalid or oversized")
	}
	scope := manifest.Scope
	command := InstallShell() + "HOST_ACCESS_ATTEMPT_DIR='/run/gsp-host-access/" + scope.SessionID + "/" + scope.OperationID + "/" + scope.AttemptID + "'\n" +
		"trap 'rm -rf -- \"$HOST_ACCESS_ATTEMPT_DIR\"' EXIT\n" +
		"host_manifest=\"$(printf '%s' '" + base64.StdEncoding.EncodeToString(payload) + "' | base64 -d)\"\n" +
		"python3 \"$HOST_ACCESS_HELPER\" inline \"$host_manifest\" \"$HOST_ACCESS_ATTEMPT_DIR\"\nunset host_manifest\n" +
		"HOST_ACCESS_MANIFEST=\"$HOST_ACCESS_ATTEMPT_DIR/manifest.json\"\n"
	if len(command) > domain.MaxHostAccessCommandBytes {
		return "", fmt.Errorf("inline mission command exceeds budget")
	}
	return command, nil
}
