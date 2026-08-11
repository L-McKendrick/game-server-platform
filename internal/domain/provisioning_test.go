package domain

import (
	"testing"
	"time"
)

func TestProvisioningLifecycleStopsAtBootstrapBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	session := readySessionForProvisioning(t, now)

	if err := session.AcquireProvisioningWorkflowLock("workflow-1", 2*time.Hour, now); err != nil {
		t.Fatalf("AcquireProvisioningWorkflowLock() returned error: %v", err)
	}
	if session.LifecycleState != StateValidating || session.DesiredState != StateRunning {
		t.Fatalf("state = %s desired = %s", session.LifecycleState, session.DesiredState)
	}
	if err := session.BeginInfrastructureProvisioning("workflow-1", "slot-0", now.Add(time.Minute)); err != nil {
		t.Fatalf("BeginInfrastructureProvisioning() returned error: %v", err)
	}
	infrastructure := Infrastructure{
		CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1",
		SecurityGroupIDs: []string{"sg-game"}, InstanceProfile: "game-instance",
		AMIID: "ami-1", InstanceType: "c7i.large", InstanceID: "i-1", DataVolumeID: "vol-1",
		PublicIPv4: "203.0.113.10", LastObservedAt: now.Add(2 * time.Minute),
	}
	if err := session.RecordInfrastructureLaunch("workflow-1", infrastructure, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("RecordInfrastructureLaunch() returned error: %v", err)
	}
	if err := session.CompleteInfrastructureProvisioning("workflow-1", now.Add(3*time.Minute)); err != nil {
		t.Fatalf("CompleteInfrastructureProvisioning() returned error: %v", err)
	}
	if session.LifecycleState != StateBootstrapping || session.ActiveWorkflowID != "" {
		t.Fatalf("completed session = %#v", session)
	}
	if session.LifecycleState == StateReady || session.LifecycleState == StateRunning {
		t.Fatal("Phase 5 must not claim the game server is ready")
	}
}

func TestProvisioningRejectsDraftSession(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-1", Slug: "saturday-arma", DisplayName: "Saturday Arma", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AcquireProvisioningWorkflowLock("workflow-1", time.Hour, now); err == nil {
		t.Fatal("AcquireProvisioningWorkflowLock() returned nil error for DRAFT session")
	}
}

func readySessionForProvisioning(t *testing.T, now time.Time) Session {
	t.Helper()
	session, err := NewSession(NewSessionInput{
		ID: "session-1", Slug: "saturday-arma", DisplayName: "Saturday Arma", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Configure(SessionConfiguration{
		GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400,
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(ArtifactMission, "sessions/session-1/input/mission.pbo", now); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(ArtifactPreset, "sessions/session-1/input/preset.html", now); err != nil {
		t.Fatal(err)
	}
	return session
}
