package domain

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

func TestGenerateSessionSlugProducesStableReadableValue(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"Saturday Arma":         "saturday-arma",
		"  ARMA 3: Friday!  ":   "arma-3-friday",
		"Café / Co-op Night":    "caf-co-op-night",
		"世界":                    "session",
		strings.Repeat("A", 80): strings.Repeat("a", MaximumGeneratedSlugLength),
	}
	for input, expected := range tests {
		if got := GenerateSessionSlug(input); got != expected {
			t.Errorf("GenerateSessionSlug(%q) = %q; want %q", input, got, expected)
		}
	}
}

func TestNewSessionNormalizesOptionalDescription(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-1", Slug: "saturday-arma", DisplayName: "Saturday Arma",
		Description: "  Weekly\nco-op\t​night  ", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatalf("NewSession() returned error: %v", err)
	}
	if session.Description != "Weekly co-op night" {
		t.Fatalf("Description = %q; want %q", session.Description, "Weekly co-op night")
	}
}

func TestNormalizeSessionDescriptionCountsUnicodeCharacters(t *testing.T) {
	t.Parallel()

	description := strings.Repeat("界", MaximumSessionDescriptionRunes)
	normalized, err := NormalizeSessionDescription(description)
	if err != nil {
		t.Fatalf("NormalizeSessionDescription() returned error: %v", err)
	}
	if got := utf8.RuneCountInString(normalized); got != MaximumSessionDescriptionRunes {
		t.Fatalf("description length = %d; want %d", got, MaximumSessionDescriptionRunes)
	}
	if _, err := NormalizeSessionDescription(description + "x"); err == nil {
		t.Fatal("NormalizeSessionDescription() accepted more than 64 characters")
	}
}

func TestSetDescriptionAdvancesVersionAndRejectsDeletedSession(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-1", Slug: "saturday-arma", DisplayName: "Saturday Arma", Description: "First",
		GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := session.SetDescription(" Updated\n description ", now.Add(time.Second))
	if err != nil {
		t.Fatalf("SetDescription() returned error: %v", err)
	}
	if previous != "First" || session.Description != "Updated description" || session.Version != 2 {
		t.Fatalf("updated session = %#v, previous = %q", session, previous)
	}
	session.LifecycleState = StateDeleted
	if _, err := session.SetDescription("Too late", now.Add(2*time.Second)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("SetDescription(deleted) error = %v; want ErrInvalidTransition", err)
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

func TestSessionConfigurationPersistsCanonicalCreatorDLCSelection(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 23, 6, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-cdlc", Slug: "creator-dlc", DisplayName: "Creator DLC", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	err = session.Configure(SessionConfiguration{
		GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400,
		CreatorDLCs: []string{CreatorDLCReactionForces, CreatorDLCGlobalMobilization},
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{CreatorDLCGlobalMobilization, CreatorDLCReactionForces}
	if !slices.Equal(session.CreatorDLCs, want) {
		t.Fatalf("CreatorDLCs = %#v; want %#v", session.CreatorDLCs, want)
	}
	if err := session.Validate(); err != nil {
		t.Fatalf("configured session validation failed: %v", err)
	}
}

func TestVanillaSessionRejectsCreatorDLCSelection(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 23, 6, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-vanilla-cdlc", Slug: "vanilla-cdlc", DisplayName: "Vanilla cDLC", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	err = session.Configure(SessionConfiguration{
		GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400,
		Vanilla: true, CreatorDLCs: []string{CreatorDLCWesternSahara},
	}, now.Add(time.Second))
	if err == nil {
		t.Fatal("Configure() accepted Creator DLC for a vanilla session")
	}
}

func TestVanillaSessionWaitsForSubmittedOptionalPresetOutcome(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-vanilla-preset", Slug: "vanilla-preset", DisplayName: "Vanilla Preset", GameType: "arma3",
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
	if err := session.PrepareCreationArtifacts(true, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(ArtifactMission, "sessions/session-vanilla-preset/input/missions/mission.pbo", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateDraft {
		t.Fatalf("state = %s; want DRAFT while preset is pending", session.LifecycleState)
	}
	if err := session.RejectArtifact(ArtifactPreset, "invalid preset", now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateNew || session.PresetArtifactStatus != ArtifactRejected {
		t.Fatalf("session = %#v; want ready vanilla session with recorded optional rejection", session)
	}
}

func TestModdedSessionRequiresPresetOrCreatorDLC(t *testing.T) {
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
	if err := session.UpdateCreatorDLCs([]string{CreatorDLCWesternSahara}, false, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateNew {
		t.Fatalf("cDLC-only modded session state = %s; want NEW", session.LifecycleState)
	}
}

func TestCreatorDLCAndPresetSubmissionWaitsForPresetValidation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 23, 16, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{ID: "session-combined", Slug: "combined", DisplayName: "Combined", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Configure(SessionConfiguration{GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 86400, StartWhenReady: true}, now); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(ArtifactMission, "sessions/session-combined/input/missions/mission.pbo", now); err != nil {
		t.Fatal(err)
	}
	if err := session.UpdateCreatorDLCs([]string{CreatorDLCWesternSahara}, true, now); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateDraft || session.PresetArtifactStatus != ArtifactPending {
		t.Fatalf("combined submission session = %#v; want pending draft", session)
	}
	if err := session.AttachArtifact(ArtifactPreset, "sessions/session-combined/input/presets/preset.html", now); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateNew {
		t.Fatalf("validated combined session state = %s; want NEW", session.LifecycleState)
	}
}

func TestLegacyArtifactObjectKeysRemainAcceptedForReadiness(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-legacy-artifacts", Slug: "legacy-artifacts", DisplayName: "Legacy Artifacts", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.MissionObjectKey = "sessions/session-legacy-artifacts/input/mission.pbo"
	session.PresetObjectKey = "sessions/session-legacy-artifacts/input/preset.html"
	if err := session.Configure(SessionConfiguration{
		GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400,
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateNew {
		t.Fatalf("legacy artifact session state = %s; want NEW", session.LifecycleState)
	}
}

func TestDraftSetupIdentityKeepsSlugAndOnlyRejectedArtifactsCanBeReplaced(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 18, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-repair", Slug: "original-slug", DisplayName: "Original Name", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Configure(SessionConfiguration{GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(ArtifactMission, "sessions/session-repair/input/mission.pbo", now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.RejectArtifact(ArtifactPreset, "bad preset", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.ConfigureDraftSetup(" Updated   Name ", " Updated   description ", SessionConfiguration{
		GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400,
	}, false, false, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.DisplayName != "Updated Name" || session.Description != "Updated description" || session.Slug != "original-slug" {
		t.Fatalf("updated identity = %#v; want normalized identity and stable slug", session)
	}
	if err := session.PrepareReplacementArtifacts(false, true, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.PresetArtifactStatus != ArtifactPending || session.MissionArtifactStatus != ArtifactAccepted {
		t.Fatalf("replacement statuses = mission %q preset %q", session.MissionArtifactStatus, session.PresetArtifactStatus)
	}
	if err := session.PrepareReplacementArtifacts(true, false, now.Add(6*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("accepted mission replacement error = %v; want ErrConflict", err)
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
