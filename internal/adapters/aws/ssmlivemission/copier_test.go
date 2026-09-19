package ssmlivemission

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type fakeSSM struct {
	sent   *ssm.SendCommandInput
	status types.CommandInvocationStatus
}

type fakeAccess struct{ instance string }

func (access fakeAccess) SignLiveMission(_ context.Context, sessionID, key string) (ports.HostAccessManifest, error) {
	instance := access.instance
	if instance == "" {
		instance = "i-exact"
	}
	now := time.Now().UTC()
	return ports.HostAccessManifest{SchemaVersion: 1, Scope: domain.HostAccessScope{SessionID: sessionID, GuildID: "guild", OperationID: "live-copy", AttemptID: "attempt", InstanceID: instance, SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: now.Add(150 * time.Second)}, Objects: []ports.HostObjectCapability{{Object: domain.HostObjectRequest{Purpose: domain.HostObjectMission, Slot: "mission", Key: key, SHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), MaxBytes: 100 * 1024 * 1024}, Method: "GET", URL: "https://assets.example.test/mission?signature=bearer", ExpiresAt: now.Add(120 * time.Second)}}}, nil
}

func (fake *fakeSSM) SendCommand(_ context.Context, input *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	fake.sent = input
	return &ssm.SendCommandOutput{Command: &types.Command{CommandId: aws.String("command-1")}}, nil
}
func (fake *fakeSSM) GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return &ssm.GetCommandInvocationOutput{Status: fake.status}, nil
}

func TestCopyUsesExactManagedInstanceAndAtomicChecksumVerifiedPlacement(t *testing.T) {
	client := &fakeSSM{status: types.CommandInvocationStatusSuccess}
	copier, err := New(client, Config{Access: fakeAccess{}, PollInterval: time.Millisecond, MaxPolls: 1})
	if err != nil {
		t.Fatal(err)
	}
	key := "sessions/session-1/input/missions/" + strings.Repeat("a", 64) + "-Coop.Altis.pbo"
	mission := domain.MissionRecord{ObjectKey: key, Filename: "Coop.Altis.pbo", Status: domain.ArtifactAccepted}
	session := domain.Session{ID: "session-1", LifecycleState: domain.StateRunning, Infrastructure: domain.Infrastructure{InstanceID: "i-exact"}, MissionFiles: []domain.MissionRecord{mission}}
	if err := copier.Copy(context.Background(), session, mission); err != nil {
		t.Fatal(err)
	}
	if len(client.sent.InstanceIds) != 1 || client.sent.InstanceIds[0] != "i-exact" {
		t.Fatalf("instances = %#v", client.sent.InstanceIds)
	}
	script := client.sent.Parameters["commands"][0]
	for _, required := range []string{"HOST_ACCESS_HELPER", "fetch", "sha256sum --check --status", "chown steam:steam", "mv -f", "gsp-mission-copy.lock"} {
		if !strings.Contains(script, required) {
			t.Errorf("script missing %q", required)
		}
	}
	for _, forbidden := range []string{"aws s3", "s3://", "systemctl restart", "systemctl stop", "server.cfg"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("script contains forbidden service mutation %q", forbidden)
		}
	}
}

func TestCopyRejectsStateOrInstanceDriftBeforeSSM(t *testing.T) {
	client := &fakeSSM{status: types.CommandInvocationStatusSuccess}
	copier, _ := New(client, Config{Access: fakeAccess{}, PollInterval: time.Millisecond, MaxPolls: 1})
	key := "sessions/session-1/input/missions/" + strings.Repeat("a", 64) + "-Coop.Altis.pbo"
	mission := domain.MissionRecord{ObjectKey: key, Filename: "Coop.Altis.pbo", Status: domain.ArtifactAccepted}
	session := domain.Session{ID: "session-1", LifecycleState: domain.StateSleeping, Infrastructure: domain.Infrastructure{InstanceID: "i-old"}, MissionFiles: []domain.MissionRecord{mission}}
	if err := copier.Copy(context.Background(), session, mission); err == nil {
		t.Fatal("sleeping session was copied live")
	}
	if client.sent != nil {
		t.Fatal("SSM called for incompatible session")
	}
}

func TestCopyRejectsTrustedInstanceDriftBeforeDispatch(t *testing.T) {
	client := &fakeSSM{status: types.CommandInvocationStatusSuccess}
	copier, _ := New(client, Config{Access: fakeAccess{instance: "i-replacement"}})
	key := "sessions/session-1/input/missions/" + strings.Repeat("a", 64) + "-Coop.Altis.pbo"
	mission := domain.MissionRecord{ObjectKey: key, Filename: "Coop.Altis.pbo", Status: domain.ArtifactAccepted}
	session := domain.Session{ID: "session-1", LifecycleState: domain.StateRunning, Infrastructure: domain.Infrastructure{InstanceID: "i-exact"}, MissionFiles: []domain.MissionRecord{mission}}
	if err := copier.Copy(context.Background(), session, mission); err == nil || client.sent != nil {
		t.Fatal("dispatched to stale instance")
	}
}
