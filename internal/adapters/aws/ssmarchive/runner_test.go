package ssmarchive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeSSM struct {
	sent    *ssm.SendCommandInput
	observe *ssm.GetCommandInvocationOutput
}

func (client *fakeSSM) SendCommand(_ context.Context, input *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	client.sent = input
	return &ssm.SendCommandOutput{Command: &types.Command{CommandId: aws.String("command-1")}}, nil
}

func (client *fakeSSM) GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
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
	commandID, err := runner.Start(context.Background(), session, "archive-1")
	if err != nil || commandID != "command-1" {
		t.Fatalf("Start() = %q, %v", commandID, err)
	}
	script := client.sent.Parameters["commands"][0]
	for _, required := range []string{"flock --wait 13000", "head-object", "archive_paths=(config state logs arma3/mpmissions 'home/.local/share')", "home/.local/share/Steam/config", "loginusers.vdf", "ssfn*", "--exclude='home/.local/share/Steam/config'", "teamspeak3-server.service", "(^|:)2302$", "(^|:)9987$", "--checksum-sha256", "test \"$size_bytes\" -le 4294967296"} {
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
	}}
	runner, _ := New(client, "assets-bucket", "us-west-2", 3600)
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
