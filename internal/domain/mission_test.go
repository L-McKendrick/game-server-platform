package domain

import (
	"strings"
	"testing"
	"time"
)

func TestMissionDefaultsAndLifecycleSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Mission test", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !session.ConfiguredMission.IsDefault() {
		t.Fatalf("configured mission = %#v", session.ConfiguredMission)
	}
	if err := session.Configure(SessionConfiguration{GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400, Vanilla: true}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateNew {
		t.Fatalf("state = %s; want NEW without an upload", session.LifecycleState)
	}
	if err := session.AcquireProvisioningWorkflowLock("workflow-1", time.Minute, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if !session.CurrentMission.IsDefault() {
		t.Fatalf("current mission = %#v", session.CurrentMission)
	}
}

func TestMissionRecordsAreImmutableAndCurrentCannotBeRemoved(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Mission test", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	key := "sessions/session-1/input/missions/" + strings.Repeat("a", 64) + "-Coop_Night.Altis.pbo"
	if err := session.AttachArtifact(ArtifactMission, key, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(session.MissionFiles) != 1 || session.MissionFiles[0].Filename != "Coop_Night.Altis.pbo" || session.ConfiguredMission.Template != "Coop_Night.Altis" {
		t.Fatalf("mission state = %#v / %#v", session.MissionFiles, session.ConfiguredMission)
	}
	session.SnapshotConfiguredMission()
	if err := session.RemoveMission(key, now.Add(2*time.Second)); err == nil {
		t.Fatal("removed the currently loaded mission")
	}
	if !session.MissionFiles[0].Active() {
		t.Fatal("current mission record was mutated")
	}
	session.CurrentMission = DefaultMissionSelection()
	if err := session.RemoveMission(key, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.MissionFiles[0].Active() || !session.ConfiguredMission.IsDefault() {
		t.Fatalf("removed mission state = %#v / %#v", session.MissionFiles[0], session.ConfiguredMission)
	}
}

func TestLiveMissionCopyTargetRequiresStableRunningInstanceAndExactAcceptedRevision(t *testing.T) {
	key := "sessions/session-1/input/missions/" + strings.Repeat("a", 64) + "-Coop.Altis.pbo"
	record := MissionRecord{ObjectKey: key, Filename: "Coop.Altis.pbo", Status: ArtifactAccepted}
	session := Session{LifecycleState: StateRunning, Infrastructure: Infrastructure{InstanceID: "i-current"}, MissionFiles: []MissionRecord{record}}
	if selected, ok := session.LiveMissionCopyTarget(key); !ok || selected.ObjectKey != key {
		t.Fatalf("target = %#v, %t", selected, ok)
	}
	session.ActiveWorkflowID = "wake-1"
	if _, ok := session.LiveMissionCopyTarget(key); ok {
		t.Fatal("workflow-conflicted session allowed live copy")
	}
	session.ActiveWorkflowID, session.LifecycleState = "", StateSleeping
	if _, ok := session.LiveMissionCopyTarget(key); ok {
		t.Fatal("sleeping session allowed live copy")
	}
	session.LifecycleState = StateRunning
	if _, ok := session.LiveMissionCopyTarget(strings.Replace(key, "a", "b", 1)); ok {
		t.Fatal("stale revision allowed live copy")
	}
}
