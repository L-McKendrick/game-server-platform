package ssmlivemission

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type fakeSSM struct {
	sent   *ssm.SendCommandInput
	status types.CommandInvocationStatus
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
	copier, err := New(client, Config{Region: "us-west-2", AssetsBucket: "assets", PollInterval: time.Millisecond, MaxPolls: 1})
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
	for _, required := range []string{"aws s3 cp", "sha256sum --check --status", "chown steam:steam", "mv -f", "gsp-mission-copy.lock"} {
		if !strings.Contains(script, required) {
			t.Errorf("script missing %q", required)
		}
	}
	for _, forbidden := range []string{"systemctl restart", "systemctl stop", "server.cfg"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("script contains forbidden service mutation %q", forbidden)
		}
	}
}

func TestCopyRejectsStateOrInstanceDriftBeforeSSM(t *testing.T) {
	client := &fakeSSM{status: types.CommandInvocationStatusSuccess}
	copier, _ := New(client, Config{Region: "us-west-2", AssetsBucket: "assets", PollInterval: time.Millisecond, MaxPolls: 1})
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
