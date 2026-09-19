package ssmarchive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestRunnerObserveRejectsInvalidPreparedArchiveMetadata(t *testing.T) {
	for _, scenario := range []string{"oversized", "zero", "negative", "invalid-digest", "short-digest", "missing-key"} {
		t.Run(scenario, func(t *testing.T) {
			metadata := map[string]any{"object_key": "sessions/s/archives/a/session.tar.gz", "sha256": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", "size_bytes": int64(42)}
			switch scenario {
			case "oversized":
				metadata["size_bytes"] = domain.MaxArchiveSizeBytes + 1
			case "zero":
				metadata["size_bytes"] = 0
			case "negative":
				metadata["size_bytes"] = -1
			case "invalid-digest":
				metadata["sha256"] = "invalid"
			case "short-digest":
				metadata["sha256"] = "YQ=="
			case "missing-key":
				metadata["object_key"] = ""
			}
			body, _ := json.Marshal(metadata)
			client := &fakeSSM{observe: &ssm.GetCommandInvocationOutput{Status: types.CommandInvocationStatusSuccess, StandardOutputContent: aws.String(string(body))}}
			runner, err := New(client, "assets", "us-west-2", 3600)
			if err != nil {
				t.Fatal(err)
			}
			if status, err := runner.Observe(context.Background(), "i-1", "command-1"); err == nil || status.Status == "Success" {
				t.Fatal("invalid archive metadata accepted")
			}
		})
	}
}

type fakeSSM struct {
	sent          *ssm.SendCommandInput
	observe       *ssm.GetCommandInvocationOutput
	uploadObserve *ssm.GetCommandInvocationOutput
	scope         domain.HostAccessScope
	uploadCommand bool
}

func (client *fakeSSM) ListCommands(_ context.Context, input *ssm.ListCommandsInput, _ ...func(*ssm.Options)) (*ssm.ListCommandsOutput, error) {
	if aws.ToString(input.CommandId) == "" {
		if client.uploadCommand {
			payload, _ := json.Marshal(client.scope)
			return &ssm.ListCommandsOutput{Commands: []types.Command{{CommandId: aws.String("upload-command"), DocumentName: aws.String("AWS-RunShellScript"), InstanceIds: []string{client.scope.InstanceID}, Comment: aws.String("gsp:archive-upload:" + client.scope.AttemptID), Parameters: map[string][]string{"commands": {"# GSP_HOST_ACCESS:" + base64.StdEncoding.EncodeToString(payload)}}}}}, nil
		}
		return &ssm.ListCommandsOutput{}, nil
	}
	payload, _ := json.Marshal(client.scope)
	return &ssm.ListCommandsOutput{Commands: []types.Command{{CommandId: input.CommandId, DocumentName: aws.String("AWS-RunShellScript"), InstanceIds: []string{client.scope.InstanceID}, Comment: aws.String("gsp:archive-prepare:" + client.scope.AttemptID), Parameters: map[string][]string{"commands": {"# GSP_HOST_ACCESS:" + base64.StdEncoding.EncodeToString(payload)}}}}}, nil
}

type fakeAccess struct {
	scope        domain.HostAccessScope
	missing      bool
	changed      bool
	verifyError  error
	verifyCalls  int
	prepareCalls int
}

func (access *fakeAccess) PrepareArchiveScope(context.Context, string, time.Duration) (domain.HostAccessScope, error) {
	return access.scope, nil
}
func (access *fakeAccess) PrepareArchiveUpload(context.Context, domain.HostAccessScope, string, int64) (ports.HostAccessReference, error) {
	access.prepareCalls++
	return ports.HostAccessReference{SchemaVersion: 1, Scope: access.scope, Generation: 1, URL: "https://assets.test/manifest", SHA256: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", SizeBytes: 100, ExpiresAt: time.Now().Add(10 * time.Minute)}, nil
}
func (access *fakeAccess) VerifyArchiveUpload(context.Context, domain.HostAccessScope) (domain.HostOutputVersion, error) {
	access.verifyCalls++
	if access.verifyError != nil {
		return domain.HostOutputVersion{}, access.verifyError
	}
	if access.missing {
		return domain.HostOutputVersion{}, domain.ErrNotFound
	}
	size := int64(42)
	if access.changed {
		size++
	}
	return domain.HostOutputVersion{VersionID: "pinned", SHA256: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", SizeBytes: size}, nil
}
func (*fakeAccess) RefreshHostCommand(context.Context, domain.HostAccessScope) (ports.HostAccessReference, error) {
	return ports.HostAccessReference{}, nil
}
func (*fakeAccess) RecordHostRefresh(context.Context, domain.HostAccessScope, int64) error {
	return nil
}
func testScope(session, operation, instance string) domain.HostAccessScope {
	return domain.HostAccessScope{SessionID: session, GuildID: "guild", OperationID: operation, InstanceID: instance, AttemptID: "attempt", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: time.Now().UTC().Add(time.Hour)}
}

func (client *fakeSSM) SendCommand(_ context.Context, input *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	client.sent = input
	return &ssm.SendCommandOutput{Command: &types.Command{CommandId: aws.String("command-1")}}, nil
}

func (client *fakeSSM) GetCommandInvocation(_ context.Context, input *ssm.GetCommandInvocationInput, _ ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	if aws.ToString(input.CommandId) == "upload-command" && client.uploadObserve != nil {
		return client.uploadObserve, nil
	}
	return client.observe, nil
}

func TestRunner_StartArchivesOnlyPortablePersistentPaths(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	session := archiveRunnerSession(t, now)
	client := &fakeSSM{}
	runner, err := New(client, "assets-bucket", "us-west-2", 3600)
	if err != nil {
		t.Fatal(err)
	}
	runner.WithArchiveAccess(&fakeAccess{scope: testScope(session.ID, "archive-1", "i-1")})
	commandID, err := runner.Start(context.Background(), session, "archive-1")
	if err != nil || commandID != "command-1" {
		t.Fatalf("Start() = %q, %v", commandID, err)
	}
	script := client.sent.Parameters["commands"][0]
	if !strings.HasPrefix(script, "#!/usr/bin/env bash\n# GSP_HOST_ACCESS:") || strings.Count(script, "#!/usr/bin/env bash") != 1 {
		t.Fatal("archive ownership preamble displaced the Bash interpreter boundary")
	}
	for _, required := range []string{"flock --wait 13000", "keep_archive=true", "archive_paths=(config state logs arma3/mpmissions 'home/.local/share')", "home/.local/share/Steam/config", "loginusers.vdf", "ssfn*", "--exclude='home/.local/share/Steam/config'", "teamspeak3-server.service", "(^|:)2302$", "(^|:)9987$", "test \"$size_bytes\" -le 4294967296"} {
		if !strings.Contains(script, required) {
			t.Fatalf("archive command missing %q", required)
		}
	}
	if strings.Contains(script, `-C /srv/game-server .`) || strings.Contains(script, "swapfile") || strings.Contains(script, "workshop") {
		t.Fatalf("archive command includes reinstallable or unbounded content")
	}
}

func TestRunner_ObserveReturnsUploadedObjectMetadata(t *testing.T) {
	client := &fakeSSM{observe: &ssm.GetCommandInvocationOutput{
		Status:                types.CommandInvocationStatusSuccess,
		StandardOutputContent: aws.String(`{"object_key":"sessions/s/archives/a/session.tar.gz","sha256":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","size_bytes":42}`),
	}, uploadCommand: true}
	runner, _ := New(client, "assets-bucket", "us-west-2", 3600)
	client.scope = testScope("s", "a", "i-1")
	runner.WithArchiveAccess(&fakeAccess{scope: client.scope})
	status, err := runner.Observe(context.Background(), "i-1", "command-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "Success" || status.SizeBytes != 42 || status.ObjectKey == "" || status.SHA256 == "" {
		t.Fatalf("Observe() = %#v", status)
	}
}

func archiveRunnerSession(t *testing.T, now time.Time) domain.Session {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = domain.StateRunning, domain.StateRunning, domain.StateRunning, domain.HealthHealthy
	session.TeamSpeakEnabled = true
	session.Infrastructure = domain.Infrastructure{CapacitySlotID: "slot-1", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile", AMIID: "ami-1", InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.1", LastObservedAt: now}
	if err := session.BeginArchive("archive-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestPreparedArchiveNeedsTrustedVerificationAndUsesBoundedUpload(t *testing.T) {
	for _, scenario := range []string{"first-dispatch", "changed", "foreign-key", "missing-access", "inspection-error", "upload-running", "upload-failed", "upload-success-missing"} {
		t.Run(scenario, func(t *testing.T) {
			client := &fakeSSM{scope: testScope("s", "a", "i-1"), observe: &ssm.GetCommandInvocationOutput{Status: types.CommandInvocationStatusSuccess, StandardOutputContent: aws.String(`{"object_key":"sessions/s/archives/a/session.tar.gz","sha256":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","size_bytes":42}`)}}
			runner, _ := New(client, "assets", "us-west-2", 3600)
			access := &fakeAccess{scope: client.scope, changed: scenario == "changed", verifyError: map[string]error{"inspection-error": errors.New("inspection denied")}[scenario]}
			if scenario == "first-dispatch" {
				access.verifyError = errors.New("preflight inspection must not run")
			}
			if scenario == "changed" || scenario == "inspection-error" || scenario == "upload-running" || scenario == "upload-failed" || scenario == "upload-success-missing" {
				client.uploadCommand = true
			}
			if scenario == "upload-running" || scenario == "upload-failed" || scenario == "upload-success-missing" {
				access.missing = true
				status := types.CommandInvocationStatusInProgress
				if scenario == "upload-failed" {
					status = types.CommandInvocationStatusFailed
				}
				if scenario == "upload-success-missing" {
					status = types.CommandInvocationStatusSuccess
				}
				client.uploadObserve = &ssm.GetCommandInvocationOutput{Status: status}
			}
			if scenario != "missing-access" {
				runner.WithArchiveAccess(access)
			}
			if scenario == "foreign-key" {
				client.scope.SessionID = "other"
			}
			status, err := runner.Observe(context.Background(), "i-1", "command-1")
			if scenario == "first-dispatch" {
				if err != nil || status.Status != "InProgress" || client.sent == nil {
					t.Fatal("prepared archive accepted before upload", err)
				}
				if access.verifyCalls != 0 || access.prepareCalls != 1 {
					t.Fatal("first upload performed an unnecessary preflight inspection")
				}
				script := client.sent.Parameters["commands"][0]
				if len(script) > domain.MaxHostAccessCommandBytes || !strings.Contains(script, " archive-upload ") || strings.Contains(script, "aws s3") {
					t.Fatal("upload bypassed bounded HTTPS")
				}
			} else if scenario == "upload-running" {
				if err != nil || status.Status != "InProgress" || access.verifyCalls != 0 || access.prepareCalls != 0 || client.sent != nil {
					t.Fatal("owned upload recovery did not wait for verified output", err)
				}
			} else if scenario == "upload-failed" {
				if err != nil || status.Status != "Failed" || access.verifyCalls != 0 || access.prepareCalls != 0 || client.sent != nil {
					t.Fatal("failed upload did not remain a command failure", err)
				}
			} else if err == nil || status.Status == "Success" || client.sent != nil {
				t.Fatal("unverified archive accepted/dispatched")
			}
		})
	}
}
