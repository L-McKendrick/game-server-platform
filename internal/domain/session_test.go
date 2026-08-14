package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewSessionCreatesDraft(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 2, 20, 0, 0, 0, time.UTC)

	session, err := NewSession(
		NewSessionInput{
			ID:                 "01JTESTSESSION",
			Slug:               "saturday-arma",
			DisplayName:        "Saturday Arma",
			GameType:           "arma3",
			OwnerDiscordUserID: "owner-1",
			GuildID:            "guild-1",
			ChannelID:          "channel-1",
		},
		now,
	)
	if err != nil {
		t.Fatalf("NewSession() returned error: %v", err)
	}

	if session.LifecycleState != StateDraft {
		t.Errorf(
			"LifecycleState = %q; want %q",
			session.LifecycleState,
			StateDraft,
		)
	}

	if session.Version != 1 {
		t.Errorf("Version = %d; want 1", session.Version)
	}
}

func TestTransitionIncrementsVersion(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 2, 20, 0, 0, 0, time.UTC)

	session, err := NewSession(
		NewSessionInput{
			ID:                 "01JTESTSESSION",
			Slug:               "saturday-arma",
			DisplayName:        "Saturday Arma",
			GameType:           "arma3",
			OwnerDiscordUserID: "owner-1",
			GuildID:            "guild-1",
			ChannelID:          "channel-1",
		},
		now,
	)
	if err != nil {
		t.Fatalf("NewSession() returned error: %v", err)
	}

	if err := session.Transition(StateNew, now.Add(time.Second)); err != nil {
		t.Fatalf("Transition() returned error: %v", err)
	}

	if session.LifecycleState != StateNew {
		t.Errorf(
			"LifecycleState = %q; want %q",
			session.LifecycleState,
			StateNew,
		)
	}

	if session.Version != 2 {
		t.Errorf("Version = %d; want 2", session.Version)
	}
}

func TestTransitionRejectsInvalidTransition(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 2, 20, 0, 0, 0, time.UTC)

	session, err := NewSession(
		NewSessionInput{
			ID:                 "01JTESTSESSION",
			Slug:               "saturday-arma",
			DisplayName:        "Saturday Arma",
			GameType:           "arma3",
			OwnerDiscordUserID: "owner-1",
			GuildID:            "guild-1",
			ChannelID:          "channel-1",
		},
		now,
	)
	if err != nil {
		t.Fatalf("NewSession() returned error: %v", err)
	}

	err = session.Transition(StateRunning, now.Add(time.Second))
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf(
			"Transition() error = %v; want ErrInvalidTransition",
			err,
		)
	}
}

func TestSessionBecomesNewWhenConfigurationAndArtifactsAreComplete(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 20, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-1", Slug: "saturday-arma", DisplayName: "Saturday Arma", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatalf("NewSession() returned error: %v", err)
	}
	if err := session.AttachArtifact(ArtifactMission, "sessions/session-1/input/missions/mission.pbo", now.Add(time.Second)); err != nil {
		t.Fatalf("AttachArtifact(mission) returned error: %v", err)
	}
	if err := session.AttachArtifact(ArtifactPreset, "sessions/session-1/input/presets/preset.html", now.Add(2*time.Second)); err != nil {
		t.Fatalf("AttachArtifact(preset) returned error: %v", err)
	}
	if session.LifecycleState != StateDraft {
		t.Fatalf("state before configuration = %s; want DRAFT", session.LifecycleState)
	}
	if err := session.Configure(SessionConfiguration{
		GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400,
	}, now.Add(3*time.Second)); err != nil {
		t.Fatalf("Configure() returned error: %v", err)
	}
	if session.LifecycleState != StateNew || session.DesiredState != StateNew || session.ObservedState != StateNew {
		t.Fatalf("completed session states = %s/%s/%s; want NEW", session.DesiredState, session.ObservedState, session.LifecycleState)
	}
}

func TestVanillaSessionBecomesNewWithoutPreset(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 6, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-vanilla", Slug: "vanilla", DisplayName: "Vanilla", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Configure(SessionConfiguration{
		GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400, Vanilla: true,
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(ArtifactMission, "sessions/session-vanilla/input/missions/mission.pbo", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateNew || session.PresetObjectKey != "" || !session.Vanilla {
		t.Fatalf("vanilla session = %#v; want NEW without preset", session)
	}
}

func TestModdedSessionStillRequiresPreset(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 6, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-modded", Slug: "modded", DisplayName: "Modded", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Configure(SessionConfiguration{
		GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400,
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(ArtifactMission, "sessions/session-modded/input/missions/mission.pbo", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateDraft {
		t.Fatalf("modded session state = %s; want DRAFT until preset upload", session.LifecycleState)
	}
}

func TestSessionWorkflowLockRejectsConcurrentMutationAndCanBeReleased(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 20, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-1", Slug: "saturday-arma", DisplayName: "Saturday Arma", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatalf("NewSession() returned error: %v", err)
	}
	if err := session.AcquireWorkflowLock("workflow-1", "ProvisionSession", time.Hour, now.Add(time.Second)); err != nil {
		t.Fatalf("AcquireWorkflowLock() returned error: %v", err)
	}
	if err := session.AcquireWorkflowLock("workflow-2", "SleepSession", time.Hour, now.Add(2*time.Second)); !errors.Is(err, ErrWorkflowLocked) {
		t.Fatalf("concurrent AcquireWorkflowLock() error = %v; want ErrWorkflowLocked", err)
	}
	if err := session.ReleaseWorkflowLock("workflow-other", now.Add(3*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("ReleaseWorkflowLock(other) error = %v; want ErrConflict", err)
	}
	if err := session.ReleaseWorkflowLock("workflow-1", now.Add(4*time.Second)); err != nil {
		t.Fatalf("ReleaseWorkflowLock() returned error: %v", err)
	}
	if session.ActiveWorkflowID != "" || !session.ActiveWorkflowLeaseExpiresAt.IsZero() {
		t.Fatalf("workflow lock was not cleared: %#v", session)
	}
}
