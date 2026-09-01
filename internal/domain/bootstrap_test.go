package domain

import (
	"strings"
	"testing"
	"time"
)

func TestBootstrapLifecycleReachesRunningOnlyAfterHealthGate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	session := bootstrappingSession(t, now)

	if err := session.AcquireBootstrapWorkflowLock("workflow-2", 6*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginBootstrapInstallation("workflow-2", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateInstalling || session.HealthStatus != HealthStarting {
		t.Fatalf("installing session = %#v", session)
	}
	if err := session.CompleteBootstrap("workflow-2", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateRunning || session.HealthStatus != HealthHealthy || session.ActiveWorkflowID != "" {
		t.Fatalf("completed session = %#v", session)
	}
}

func TestCompleteBootstrapWithWorkshopMissionsIsOneAtomicMutation(t *testing.T) {
	now := time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC)
	source := WorkshopMissionSource{
		Source:     WorkshopReference{PublishedFileID: 100, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=100"},
		SourceKind: WorkshopSourceCollection, ResolutionSHA256: strings.Repeat("a", 64),
		AcceptedItemIDs: []uint64{200, 300}, ResolvedAt: now,
	}
	session := bootstrappingSessionWithWorkshopSource(t, now, source)
	if err := session.AcquireBootstrapWorkflowLock("workflow-workshop", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginBootstrapInstallation("workflow-workshop", now); err != nil {
		t.Fatal(err)
	}
	before := session.Version
	missionCount := len(session.MissionFiles)
	configured, current := session.ConfiguredMission, session.CurrentMission
	missions := []MissionRecord{
		{ObjectKey: "sessions/session-1/input/missions/" + strings.Repeat("b", 64) + "-One.Altis.pbo", Filename: "One.Altis.pbo", Status: ArtifactAccepted, WorkshopItemID: 200, WorkshopSources: []WorkshopReference{source.Source}},
		{ObjectKey: "sessions/session-1/input/missions/" + strings.Repeat("c", 64) + "-Two.Stratis.pbo", Filename: "Two.Stratis.pbo", Status: ArtifactAccepted, WorkshopItemID: 300, WorkshopSources: []WorkshopReference{source.Source}},
	}
	if err := session.CompleteBootstrapWithWorkshopMissions("workflow-workshop", missions, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.Version != before+1 || len(session.MissionFiles) != missionCount+2 || session.LifecycleState != StateRunning || session.ActiveWorkflowID != "" {
		t.Fatalf("session = %#v", session)
	}
	if session.ConfiguredMission != configured || session.CurrentMission != current {
		t.Fatal("Workshop mission import changed mission selection")
	}
}

func TestCompleteBootstrapWithWorkshopMissionsRejectsCollectionAtomically(t *testing.T) {
	now := time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC)
	source := WorkshopMissionSource{Source: WorkshopReference{PublishedFileID: 100, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=100"}, SourceKind: WorkshopSourceCollection, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{200}, ResolvedAt: now}
	session := bootstrappingSessionWithWorkshopSource(t, now, source)
	if err := session.AcquireBootstrapWorkflowLock("workflow-workshop", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginBootstrapInstallation("workflow-workshop", now); err != nil {
		t.Fatal(err)
	}
	before := session
	missionCount := len(session.MissionFiles)
	missions := []MissionRecord{
		{ObjectKey: "sessions/session-1/input/missions/" + strings.Repeat("b", 64) + "-One.Altis.pbo", Filename: "One.Altis.pbo", Status: ArtifactAccepted, WorkshopItemID: 200, WorkshopSources: []WorkshopReference{source.Source}},
		{ObjectKey: "sessions/session-1/input/missions/" + strings.Repeat("c", 64) + "-Bad.Altis.pbo", Filename: "Bad.Altis.pbo", Status: ArtifactAccepted, WorkshopItemID: 999, WorkshopSources: []WorkshopReference{source.Source}},
	}
	if err := session.CompleteBootstrapWithWorkshopMissions("workflow-workshop", missions, now.Add(time.Minute)); err == nil {
		t.Fatal("unauthorized collection child was accepted")
	}
	if session.Version != before.Version || len(session.MissionFiles) != missionCount || session.LifecycleState != before.LifecycleState || session.ActiveWorkflowID != before.ActiveWorkflowID {
		t.Fatalf("failed atomic completion mutated session: %#v", session)
	}
}

func TestBootstrapFailureRetainsInfrastructureAndCanRetry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	session := bootstrappingSession(t, now)
	instanceID := session.Infrastructure.InstanceID
	volumeID := session.Infrastructure.DataVolumeID

	if err := session.AcquireBootstrapWorkflowLock("workflow-2", 6*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginBootstrapInstallation("workflow-2", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := session.FailBootstrap("workflow-2", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.Infrastructure.InstanceID != instanceID || session.Infrastructure.DataVolumeID != volumeID || !session.CanStartBootstrap() {
		t.Fatalf("failed session cannot resume: %#v", session)
	}
}

func bootstrappingSession(t *testing.T, now time.Time) Session {
	t.Helper()
	session := readySessionForProvisioning(t, now)
	if err := session.AcquireProvisioningWorkflowLock("workflow-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginInfrastructureProvisioning("workflow-1", "slot-0", now); err != nil {
		t.Fatal(err)
	}
	if err := session.RecordInfrastructureLaunch("workflow-1", Infrastructure{
		CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1",
		SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile-1", AMIID: "ami-1",
		InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1",
		PublicIPv4: "203.0.113.10", LastObservedAt: now,
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteInfrastructureProvisioning("workflow-1", now); err != nil {
		t.Fatal(err)
	}
	return session
}

func bootstrappingSessionWithWorkshopSource(t *testing.T, now time.Time, source WorkshopMissionSource) Session {
	t.Helper()
	session := readySessionForProvisioning(t, now)
	if err := session.RecordWorkshopMissionSource(source, now); err != nil {
		t.Fatal(err)
	}
	if err := session.AcquireProvisioningWorkflowLock("workflow-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginInfrastructureProvisioning("workflow-1", "slot-0", now); err != nil {
		t.Fatal(err)
	}
	if err := session.RecordInfrastructureLaunch("workflow-1", Infrastructure{
		CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1",
		SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile-1", AMIID: "ami-1",
		InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1",
		PublicIPv4: "203.0.113.10", LastObservedAt: now,
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteInfrastructureProvisioning("workflow-1", now); err != nil {
		t.Fatal(err)
	}
	return session
}
