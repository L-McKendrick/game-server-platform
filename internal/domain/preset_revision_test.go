package domain

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestAttachPresetCreatesFirstActiveRevisionAndCompatibilityPointer(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 20, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session 1", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	objectKey := "sessions/session-1/input/presets/preset.html"
	if err := session.AttachArtifact(ArtifactPreset, objectKey, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.PresetObjectKey != objectKey || session.PresetRevisionSequence != 1 {
		t.Fatalf("compatibility pointer/sequence = %q/%d", session.PresetObjectKey, session.PresetRevisionSequence)
	}
	active := session.ActivePresetRevision
	if active.Number != 1 || active.BaseRevision != 0 || active.Status != PresetRevisionActive || active.PresetObjectKey != objectKey || !active.ActivatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("active revision = %#v", active)
	}
}

func TestServerOnlyPresetCanCompleteModdedDraftReadiness(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{ID: "server-only", Slug: "server-only", DisplayName: "Server Only", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Configure(SessionConfiguration{GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 86400}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.UpdateModOptions(nil, false, true, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateDraft || session.ServerPresetArtifactStatus != ArtifactPending {
		t.Fatalf("prepared draft = %#v", session)
	}
	if err := session.AttachArtifact(ArtifactServerPreset, "sessions/server-only/input/server-presets/server.html", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateNew || session.ServerPresetArtifactStatus != ArtifactAccepted || session.ActiveServerPresetRevision.Number != 1 {
		t.Fatalf("ready server-only session = %#v", session)
	}
}

func TestEstablishedSessionCanStageItsFirstClientPresetRevision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{ID: "cdlc-only", Slug: "cdlc-only", DisplayName: "CDLC Only", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Configure(SessionConfiguration{GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 86400, CreatorDLCs: []string{CreatorDLCReactionForces}}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateNew || !session.EffectiveActivePresetRevision().Empty() {
		t.Fatalf("initial established session = %#v", session)
	}
	modlist := PresetModlistMetadata{ObjectKey: "sessions/cdlc-only/input/modlists/v1.html", Filename: "modlist.html", SHA256: strings.Repeat("a", 64), SizeBytes: 128, WorkshopCount: 1}
	revision, err := session.StagePresetRevision(0, "sessions/cdlc-only/input/presets/v1.html", modlist, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if revision.Number != 1 || revision.BaseRevision != 0 || revision.Status != PresetRevisionPending || !session.ActivePresetRevision.Empty() {
		t.Fatalf("first staged client preset = %#v session=%#v", revision, session)
	}
}

func TestPendingPresetPromotesOnlyAfterLifecycleHealthSuccess(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 23, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name     string
		prepare  func(*Session) error
		complete func(*Session) error
	}{
		{name: "bootstrap", prepare: func(session *Session) error {
			session.DesiredState, session.ObservedState, session.LifecycleState = StateRunning, StateBootstrapping, StateBootstrapping
			if err := session.AcquireBootstrapWorkflowLock("workflow-1", time.Hour, now.Add(2*time.Minute)); err != nil {
				return err
			}
			return session.BeginBootstrapInstallation("workflow-1", now.Add(3*time.Minute))
		}, complete: func(session *Session) error { return session.CompleteBootstrap("workflow-1", now.Add(4*time.Minute)) }},
		{name: "wake", prepare: func(session *Session) error {
			session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateSleeping, StateSleeping, StateSleeping, HealthStopped
			return session.BeginWake("workflow-1", time.Hour, now.Add(2*time.Minute))
		}, complete: func(session *Session) error {
			return session.CompleteWake("workflow-1", "203.0.113.8", now.Add(4*time.Minute))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := presetLifecycleSession(t, now)
			if err := test.prepare(&session); err != nil {
				t.Fatal(err)
			}
			if session.PendingPresetRevision.Status != PresetRevisionApplying || session.PresetObjectKey != "sessions/session-1/input/presets/v1.html" || session.PresetObjectKeyForApplication() != "sessions/session-1/input/presets/v2.html" {
				t.Fatalf("pre-health authority = active %q pending %#v", session.PresetObjectKey, session.PendingPresetRevision)
			}
			if err := test.complete(&session); err != nil {
				t.Fatal(err)
			}
			if session.ActivePresetRevision.Number != 2 || !session.PendingPresetRevision.Empty() || session.PresetObjectKey != "sessions/session-1/input/presets/v2.html" || !session.ActivePresetRevision.ActivatedAt.Equal(now.Add(4*time.Minute)) {
				t.Fatalf("post-health promotion = %#v", session)
			}
		})
	}
}

func TestFailedPresetApplicationRetainsDiagnosisAndRollbackDisposition(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	session := presetLifecycleSession(t, now)
	session.DesiredState, session.ObservedState, session.LifecycleState = StateRunning, StateBootstrapping, StateBootstrapping
	if err := session.AcquireBootstrapWorkflowLock("workflow-1", time.Hour, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	activeBefore := session.ActivePresetRevision
	versionBefore := session.Version
	changed, err := session.RecordPresetRevisionRollback("workflow-1", true, "ignored success detail", now.Add(3*time.Minute))
	if err != nil || !changed || session.Version != versionBefore+1 {
		t.Fatalf("rollback result changed=%v version=%d err=%v", changed, session.Version, err)
	}
	if changed, err = session.RecordPresetRevisionRollback("workflow-1", false, "replayed failure", now.Add(4*time.Minute)); err != nil || changed {
		t.Fatalf("rollback replay changed=%v err=%v", changed, err)
	}
	failure := strings.Repeat("diagnosis ", MaximumPresetRevisionFailureRunes)
	if err := session.FailPresetRevisionApplication("workflow-1", failure, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	pending := session.PendingPresetRevision
	if pending.Status != PresetRevisionFailed || pending.RollbackDisposition != PresetRollbackSucceeded || pending.ApplyWorkflowID != "" || !pending.ApplyStartedAt.IsZero() || len([]rune(pending.FailureDetail)) > MaximumPresetRevisionFailureRunes {
		t.Fatalf("failed pending revision = %#v", pending)
	}
	if session.ActivePresetRevision != activeBefore || session.PresetObjectKey != activeBefore.PresetObjectKey {
		t.Fatalf("active authority drifted: active=%#v pointer=%q", session.ActivePresetRevision, session.PresetObjectKey)
	}
}

func TestFailedPresetApplicationRecordsUnverifiedRollback(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	session := presetLifecycleSession(t, now)
	session.DesiredState, session.ObservedState, session.LifecycleState = StateSleeping, StateSleeping, StateSleeping
	if err := session.BeginWake("workflow-1", time.Hour, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := session.FailPresetRevisionApplication("workflow-1", "token=secret i-1234567890abcdef0 installer failed", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.PendingPresetRevision.RollbackDisposition != PresetRollbackUnverified || session.PendingPresetRevision.RollbackDetail == "" {
		t.Fatalf("rollback disposition = %#v", session.PendingPresetRevision)
	}
	if strings.Contains(session.PendingPresetRevision.FailureDetail, "secret") || strings.Contains(session.PendingPresetRevision.FailureDetail, "i-1234567890abcdef0") {
		t.Fatalf("failure diagnosis was not redacted: %q", session.PendingPresetRevision.FailureDetail)
	}
}

func TestRestorePromotesPendingPresetOnlyOnCompletion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 23, 0, 0, 0, time.UTC)
	session := presetLifecycleSession(t, now)
	session.Infrastructure = Infrastructure{}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateArchived, StateArchived, StateArchived, HealthStopped
	session.Archive = ArchiveMetadata{ID: "archive-1", ObjectKey: "sessions/session-1/archives/archive-1/session.tar.gz", ManifestObjectKey: "sessions/session-1/archives/archive-1/manifest.v1.json", SHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), ManifestSHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), SizeBytes: 42, ManifestSizeBytes: 42, Format: "tar+gzip", VerifiedAt: now}
	if err := session.BeginRestore("restore-1", time.Hour, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.PendingPresetRevision.Status != PresetRevisionApplying || session.PresetObjectKey != session.ActivePresetRevision.PresetObjectKey {
		t.Fatalf("restore started = %#v", session)
	}
	session.Infrastructure = testPresetInfrastructure(now.Add(3 * time.Minute))
	if err := session.CompleteRestore("restore-1", now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.ActivePresetRevision.Number != 2 || !session.PendingPresetRevision.Empty() || session.PresetObjectKey != "sessions/session-1/input/presets/v2.html" {
		t.Fatalf("restored promotion = %#v", session)
	}
}

func presetLifecycleSession(t *testing.T, now time.Time) Session {
	t.Helper()
	session, err := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session 1", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.PresetObjectKey = "sessions/session-1/input/presets/v1.html"
	session.PresetRevisionSequence = 2
	session.ActivePresetRevision = PresetRevision{Number: 1, PresetObjectKey: session.PresetObjectKey, Status: PresetRevisionActive, StagedAt: now, ActivatedAt: now}
	session.PendingPresetRevision = PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/presets/v2.html", Modlist: PresetModlistMetadata{ObjectKey: "sessions/session-1/input/modlists/v2/modlist.html", Filename: "session-1-modlist.html", SHA256: strings.Repeat("b", 64), SizeBytes: 1200, WorkshopCount: 4}, Status: PresetRevisionPending, StagedAt: now.Add(time.Minute)}
	session.Infrastructure = testPresetInfrastructure(now)
	return session
}

func testPresetInfrastructure(now time.Time) Infrastructure {
	return Infrastructure{CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile-1", AMIID: "ami-1", InstanceType: "c7i.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.7", LastObservedAt: now}
}

func TestEffectiveActivePresetRevisionMigratesLegacyPointer(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 20, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{ID: "legacy", Slug: "legacy", DisplayName: "Legacy", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.PresetObjectKey = "sessions/legacy/input/preset.html"
	active := session.EffectiveActivePresetRevision()
	if active.Number != 1 || active.Status != PresetRevisionActive || active.PresetObjectKey != session.PresetObjectKey || session.EffectivePresetRevisionSequence() != 1 {
		t.Fatalf("legacy active revision = %#v", active)
	}
	if err := session.Validate(); err != nil {
		t.Fatalf("legacy session validation = %v", err)
	}
}

func TestPresetRevisionValidationRejectsDriftAndUnboundedFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 20, 0, 0, 0, time.UTC)
	revision := PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/presets/v2.html", Status: PresetRevisionFailed, StagedAt: now, FailedAt: now.Add(time.Minute), FailureDetail: strings.Repeat("x", MaximumPresetRevisionFailureRunes+1)}
	if err := revision.Validate(); err == nil {
		t.Fatal("Validate() accepted unbounded failure detail")
	}
	revision.FailureDetail = "installation failed"
	if err := revision.Validate(); err != nil {
		t.Fatalf("Validate() rejected failed revision: %v", err)
	}
}

func TestNewPresetRevisionEventContainsImmutableRevisionFacts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 20, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session 1", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	revision := PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/presets/v2.html", Status: PresetRevisionPending, StagedAt: now}
	event := NewPresetRevisionEvent("event-1", EventPresetRevisionStaged, "correlation-1", Actor{Type: ActorTypeDiscordUser, ID: "owner-1"}, session, revision, now)
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	if event.Data["preset_revision"] != "2" || event.Data["base_preset_revision"] != "1" || event.Data["preset_revision_status"] != "PENDING" {
		t.Fatalf("event data = %#v", event.Data)
	}
}

func TestStagePresetRevisionPreservesRunningServiceAndActiveAuthority(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 20, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session 1", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState = StateRunning, StateRunning, StateRunning
	session.PresetObjectKey = "sessions/session-1/input/presets/v1.html"
	session.PresetRevisionSequence = 1
	session.ActivePresetRevision = PresetRevision{Number: 1, PresetObjectKey: session.PresetObjectKey, Status: PresetRevisionActive, StagedAt: now, ActivatedAt: now}
	previousVersion := session.Version
	revision, err := session.StagePresetRevision(1, "sessions/session-1/input/presets/v2.html", PresetModlistMetadata{ObjectKey: "sessions/session-1/input/modlists/v2/modlist.html", Filename: "session-1-modlist.html", SHA256: strings.Repeat("b", 64), SizeBytes: 1200, WorkshopCount: 4}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateRunning || session.PresetObjectKey != session.ActivePresetRevision.PresetObjectKey || session.PresetObjectKey == revision.PresetObjectKey {
		t.Fatalf("staging interrupted or promoted the session: %#v", session)
	}
	if revision.Number != 2 || revision.BaseRevision != 1 || revision.Status != PresetRevisionPending || session.Version != previousVersion+1 {
		t.Fatalf("pending revision = %#v session version=%d", revision, session.Version)
	}
	if _, err := session.StagePresetRevision(1, "sessions/session-1/input/presets/v3.html", revision.Modlist, now.Add(2*time.Minute)); err == nil {
		t.Fatal("second pending revision was accepted")
	}
}

func TestServerPresetRevisionUsesIndependentLifecycleAuthority(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	session := presetLifecycleSession(t, now)
	session.DesiredState, session.ObservedState, session.LifecycleState = StateRunning, StateRunning, StateRunning
	revision, err := session.StageServerPresetRevision(0, "sessions/session-1/input/server-presets/server-v1.html", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if revision.Number != 1 || revision.BaseRevision != 0 || session.ServerPresetObjectKey != "" || session.PendingServerPresetRevision.Status != PresetRevisionPending {
		t.Fatalf("staged server preset = %#v session=%#v", revision, session)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateSleeping, StateSleeping, StateSleeping, HealthStopped
	if err := session.BeginWake("wake-server", time.Hour, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.ServerPresetObjectKeyForApplication() != revision.PresetObjectKey || session.PresetObjectKeyForApplication() != session.PendingPresetRevision.PresetObjectKey {
		t.Fatalf("application selection = %#v", session)
	}
	if err := session.CompleteWake("wake-server", "203.0.113.9", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.ActiveServerPresetRevision.Number != 1 || !session.PendingServerPresetRevision.Empty() || session.ServerPresetObjectKey != revision.PresetObjectKey || session.ActivePresetRevision.Number != 2 {
		t.Fatalf("promoted revisions = %#v", session)
	}
}

func TestFailedServerPresetApplicationKeepsActiveAuthorityAndRollback(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	session := presetLifecycleSession(t, now)
	activeKey := "sessions/session-1/input/server-presets/v1.html"
	session.ServerPresetObjectKey = activeKey
	session.ServerPresetRevisionSequence = 2
	session.ActiveServerPresetRevision = PresetRevision{Number: 1, PresetObjectKey: activeKey, Status: PresetRevisionActive, StagedAt: now, ActivatedAt: now}
	session.PendingServerPresetRevision = PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/server-presets/v2.html", Status: PresetRevisionPending, StagedAt: now.Add(time.Minute)}
	session.PendingPresetRevision = PresetRevision{}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateSleeping, StateSleeping, StateSleeping, HealthStopped
	if err := session.BeginWake("wake-server", time.Hour, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if changed, err := session.RecordPresetRevisionRollback("wake-server", true, "", now.Add(3*time.Minute)); err != nil || !changed {
		t.Fatalf("rollback changed=%v err=%v", changed, err)
	}
	if err := session.FailPresetRevisionApplication("wake-server", "server mod health failed", now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.ServerPresetObjectKey != activeKey || session.ActiveServerPresetRevision.Number != 1 || session.PendingServerPresetRevision.Status != PresetRevisionFailed || session.PendingServerPresetRevision.RollbackDisposition != PresetRollbackSucceeded {
		t.Fatalf("failed server preset rollback = %#v", session)
	}
}
