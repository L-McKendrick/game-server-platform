package ssmrestore

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

type testSSM struct{}

func (testSSM) SendCommand(context.Context, *ssm.SendCommandInput, ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	return &ssm.SendCommandOutput{}, nil
}
func (testSSM) GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return &ssm.GetCommandInvocationOutput{}, nil
}

func TestRestoreExtractionPrecedesBootstrapAndInvalidatesArchivedInstallMarkers(t *testing.T) {
	t.Parallel()
	runner, err := New(testSSM{}, "assets", "us-west-2", 3600)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 17, 23, 0, 0, 0, time.UTC)
	session := domain.Session{ID: "session-1", TeamSpeakEnabled: true, Archive: domain.ArchiveMetadata{ID: "archive-1", ObjectKey: "sessions/session-1/archives/archive-1/session.tar.gz", ManifestObjectKey: "sessions/session-1/archives/archive-1/manifest.v1.json", SHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), ManifestSHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), SizeBytes: 42, ManifestSizeBytes: 42, Format: "tar+gzip", VerifiedAt: now}}
	script := runner.command(session)
	for _, required := range []string{"tar --no-same-owner", "install_workshop*.complete", "systemctl stop arma3-server.service 2>/dev/null || true"} {
		if !strings.Contains(script, required) {
			t.Errorf("restore script missing %q", required)
		}
	}
	if strings.Contains(script, "systemctl start arma3-server.service") || strings.Contains(script, "ready=false") {
		t.Fatal("restore extraction started or health-checked service before bootstrap applied authoritative preset")
	}
}
