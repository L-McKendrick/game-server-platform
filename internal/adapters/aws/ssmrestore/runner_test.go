package ssmrestore

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
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
	session := domain.Session{ID: "session-1", TeamSpeakEnabled: true, Infrastructure: domain.Infrastructure{DataVolumeID: "vol-data"}, Archive: domain.ArchiveMetadata{ID: "archive-1", ObjectKey: "sessions/session-1/archives/archive-1/session.tar.gz", ManifestObjectKey: "sessions/session-1/archives/archive-1/manifest.v1.json", SHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), ManifestSHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), SizeBytes: 42, ManifestSizeBytes: 42, Format: "tar+gzip", VerifiedAt: now}}
	script := runner.command(session)
	for _, required := range []string{"tar --no-same-owner", "install_workshop*.complete", "systemctl stop arma3-server.service 2>/dev/null || true", "home/.local/share/Steam/config", "loginusers.vdf", "ssfn*", "aws-cli/2.*", "ERR_AWS_CLI_PREREQUISITE", "ERR_RESTORE_DATA_VOLUME", base64.StdEncoding.EncodeToString([]byte("vol-data")), "mkfs.xfs", "mountpoint -q", "findmnt", "mktemp \"$root/.gsp-restore.", "mkdir -p /srv/game-server/config", "id steam", "useradd --home-dir /srv/game-server/home --no-create-home --shell /bin/bash steam", "id teamspeak", "useradd --home-dir /srv/game-server/teamspeak --no-create-home --shell /sbin/nologin teamspeak"} {
		if !strings.Contains(script, required) {
			t.Errorf("restore script missing %q", required)
		}
	}
	if strings.Contains(script, "systemctl start arma3-server.service") || strings.Contains(script, "\nready=false\n") {
		t.Fatal("restore extraction started or health-checked service before bootstrap applied authoritative preset")
	}
	if strings.Index(script, "aws --version") > strings.Index(script, "aws s3 cp") {
		t.Fatal("restore attempted archive access before verifying AWS CLI v2")
	}
	if strings.Index(script, "id steam") > strings.Index(script, "tar --no-same-owner") || strings.Index(script, "id teamspeak") > strings.Index(script, "tar --no-same-owner") {
		t.Fatal("restore applied archive ownership before creating its service accounts")
	}
	if strings.Index(script, "mountpoint -q") > strings.Index(script, "aws s3 cp") || strings.Index(script, "mountpoint -q") > strings.Index(script, "tar --no-same-owner") {
		t.Fatal("restore accessed the archive before mounting the recorded data volume")
	}
	if strings.Index(script, "mkdir -p /srv/game-server/config") < strings.Index(script, "tar --no-same-owner") || strings.Index(script, "mkdir -p /srv/game-server/config") > strings.Index(script, "chown -R steam:steam") {
		t.Fatal("restore did not recreate optional empty archive roots before ownership")
	}
}

type failedSSM struct{ testSSM }

func (failedSSM) GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return &ssm.GetCommandInvocationOutput{Status: types.CommandInvocationStatusFailed, StandardErrorContent: aws.String("ERR_AWS_CLI_PREREQUISITE: AWS CLI v2 is required")}, nil
}

func TestObserveClassifiesAWSCLIPrerequisiteFailure(t *testing.T) {
	t.Parallel()
	runner, err := New(failedSSM{}, "assets", "us-west-2", 3600)
	if err != nil {
		t.Fatal(err)
	}
	status, err := runner.Observe(context.Background(), "i-1", "command-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.ErrorCode != "ERR_AWS_CLI_PREREQUISITE" || status.Status != "Failed" {
		t.Fatalf("status = %#v", status)
	}
}

type failedDataVolumeSSM struct{ testSSM }

func (failedDataVolumeSSM) GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return &ssm.GetCommandInvocationOutput{Status: types.CommandInvocationStatusFailed, StandardErrorContent: aws.String("package noise\nERR_RESTORE_DATA_VOLUME: recorded data volume was not found")}, nil
}

func TestObserveClassifiesDataVolumeFailure(t *testing.T) {
	t.Parallel()
	runner, err := New(failedDataVolumeSSM{}, "assets", "us-west-2", 3600)
	if err != nil {
		t.Fatal(err)
	}
	status, err := runner.Observe(context.Background(), "i-1", "command-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.ErrorCode != "ERR_RESTORE_DATA_VOLUME" || !strings.Contains(status.ErrorMessage, "data volume") {
		t.Fatalf("status = %#v", status)
	}
}
