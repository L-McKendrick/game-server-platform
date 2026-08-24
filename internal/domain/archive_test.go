package domain

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestSessionArchiveLifecycle_RecordsBackupBeforeRemovingInfrastructure(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	session := archiveTestSession(t, now)
	originalVersion := session.Version

	if err := session.BeginArchive("archive-1", time.Hour, now); err != nil {
		t.Fatalf("BeginArchive() error = %v", err)
	}
	if session.LifecycleState != StateArchiving || session.ArchiveSourceState != StateRunning || session.Version != originalVersion+1 {
		t.Fatalf("archive start session = %#v", session)
	}

	metadata := ArchiveMetadata{
		ID: "archive-1", ObjectKey: "sessions/session-1/archives/archive-1/session.tar.gz",
		ManifestObjectKey: "sessions/session-1/archives/archive-1/manifest.v1.json",
		ManifestSHA256:    base64.StdEncoding.EncodeToString(make([]byte, 32)),
		ManifestSizeBytes: 123,
		SHA256:            base64.StdEncoding.EncodeToString(make([]byte, 32)), SizeBytes: 42, Format: "tar+gzip", VerifiedAt: now.Add(time.Minute),
	}
	if err := session.RecordVerifiedArchive("archive-1", metadata, now.Add(time.Minute)); err != nil {
		t.Fatalf("RecordVerifiedArchive() error = %v", err)
	}
	if session.LifecycleState != StateDestroying || session.Archive != metadata || session.ActiveWorkflowID == "" {
		t.Fatalf("verified archive state = %#v", session)
	}
	if err := session.CompleteArchive("archive-1", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("CompleteArchive() error = %v", err)
	}
	if session.LifecycleState != StateArchived || session.HealthStatus != HealthStopped || session.ActiveWorkflowID != "" {
		t.Fatalf("completed archive state = %#v", session)
	}
	if session.Archive != metadata {
		t.Fatalf("archive metadata = %#v; want %#v", session.Archive, metadata)
	}
	if !session.Infrastructure.Empty() {
		t.Fatalf("infrastructure retained: %#v", session.Infrastructure)
	}
}

func TestArchiveManifestPresetIntentMatchesRestoreTransitionAndRejectsDrift(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 2, 0, 0, 0, time.UTC)
	active := PresetRevision{
		Number: 1, PresetObjectKey: "sessions/session-1/input/presets/v1.html", Status: PresetRevisionActive, StagedAt: now.Add(-time.Hour), ActivatedAt: now.Add(-time.Hour),
		Modlist: PresetModlistMetadata{ObjectKey: "sessions/session-1/input/modlists/v1/session-1-modlist.html", Filename: "session-1-modlist.html", SHA256: strings.Repeat("a", 64), SizeBytes: 512, WorkshopCount: 2},
	}
	pending := PresetRevision{
		Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/presets/v2.html", Status: PresetRevisionPending, StagedAt: now,
		Modlist: PresetModlistMetadata{ObjectKey: "sessions/session-1/input/modlists/v2/session-1-modlist.html", Filename: "session-1-modlist.html", SHA256: strings.Repeat("b", 64), SizeBytes: 640, WorkshopCount: 3},
	}
	manifest := ArchiveManifest{
		SchemaVersion: 1, ArchiveID: "archive-1", SessionID: "session-1", CreatedAt: now.Format(time.RFC3339Nano), Format: "tar+gzip",
		ObjectKey: "sessions/session-1/archives/archive-1/session.tar.gz", SHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), SizeBytes: 42,
		ContentRoots: []string{"/srv/game-server/config"}, GameProfileID: "arma3-default", PresetObjectKey: active.PresetObjectKey,
		PresetRevisionSequence: 2, ActivePresetRevision: ArchivePresetRevisionSnapshot(active), PendingPresetRevision: ArchivePresetRevisionSnapshot(pending),
		SourceInstanceID: "i-1", SourceDataVolumeID: "vol-1",
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	session := Session{ID: "session-1", PresetObjectKey: active.PresetObjectKey, PresetRevisionSequence: 2, ActivePresetRevision: active, PendingPresetRevision: pending}
	if !manifest.PresetRevisionIntentMatches(session) {
		t.Fatal("matching archived revision intent was rejected")
	}
	session.ActiveWorkflowID, session.ActiveWorkflowType = "restore-1", RestoreWorkflowType
	session.PendingPresetRevision.Status = PresetRevisionApplying
	session.PendingPresetRevision.ApplyWorkflowID = "restore-1"
	session.PendingPresetRevision.ApplyStartedAt = now.Add(time.Minute)
	if !manifest.PresetRevisionIntentMatches(session) {
		t.Fatal("restore-owned pending application was rejected")
	}
	session.PendingPresetRevision.PresetObjectKey = "sessions/session-1/input/presets/drift.html"
	if manifest.PresetRevisionIntentMatches(session) {
		t.Fatal("drifted restore revision intent was accepted")
	}
	legacy := manifest
	legacy.PresetRevisionSequence, legacy.ActivePresetRevision, legacy.PendingPresetRevision = 0, nil, nil
	if !legacy.PresetRevisionIntentMatches(session) {
		t.Fatal("legacy manifest without revision intent was rejected")
	}
}

func TestArchiveManifestPreservesPendingFirstServerPresetAcrossRestore(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	pending := PresetRevision{Number: 1, PresetObjectKey: "sessions/session-1/input/server-presets/v1.html", Status: PresetRevisionPending, StagedAt: now}
	manifest := ArchiveManifest{SchemaVersion: 1, ArchiveID: "archive-1", SessionID: "session-1", CreatedAt: now.Format(time.RFC3339Nano), Format: "tar+gzip", ObjectKey: "sessions/session-1/archives/archive-1/session.tar.gz", SHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), SizeBytes: 42, ContentRoots: []string{"/srv/game-server/config"}, GameProfileID: "arma3-default", ServerPresetRevisionSequence: 1, PendingServerPresetRevision: ArchivePresetRevisionSnapshot(pending), SourceInstanceID: "i-1", SourceDataVolumeID: "vol-1"}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	session := Session{ID: "session-1", ServerPresetRevisionSequence: 1, PendingServerPresetRevision: pending}
	if !manifest.ServerPresetRevisionIntentMatches(session) {
		t.Fatal("matching pending server preset was rejected")
	}
	session.ActiveWorkflowID, session.ActiveWorkflowType = "restore-1", RestoreWorkflowType
	session.PendingServerPresetRevision.Status, session.PendingServerPresetRevision.ApplyWorkflowID, session.PendingServerPresetRevision.ApplyStartedAt = PresetRevisionApplying, "restore-1", now.Add(time.Minute)
	if !manifest.ServerPresetRevisionIntentMatches(session) {
		t.Fatal("restore-owned server preset application was rejected")
	}
}

func TestArchiveEventCapturesRevisionIntentWithoutFailureText(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 2, 0, 0, 0, time.UTC)
	session := Session{ID: "session-1", PresetObjectKey: "sessions/session-1/input/presets/v1.html", PresetRevisionSequence: 2,
		ActivePresetRevision:  PresetRevision{Number: 1, PresetObjectKey: "sessions/session-1/input/presets/v1.html", Status: PresetRevisionActive, StagedAt: now, ActivatedAt: now},
		PendingPresetRevision: PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/presets/v2.html", Status: PresetRevisionFailed, StagedAt: now, FailedAt: now, FailureDetail: "redacted diagnosis", RollbackDisposition: PresetRollbackUnverified, RollbackAt: now, RollbackDetail: "not verified"},
	}
	workflow := Workflow{ID: "archive-1", Type: ArchiveWorkflowType, CorrelationID: "correlation-1"}
	event := NewArchiveEvent("event-1", EventArchiveVerified, "Verified", workflow, session, ArchiveMetadata{}, now)
	if event.Data["active_preset_revision"] != "1" || event.Data["pending_preset_revision"] != "2" || event.Data["pending_preset_revision_status"] != string(PresetRevisionFailed) {
		t.Fatalf("archive event data = %#v", event.Data)
	}
	if _, found := event.Data["failure_detail"]; found {
		t.Fatalf("archive event exposed free-form failure text: %#v", event.Data)
	}
}

func TestSessionArchiveLifecycle_FailurePreservesResourcesAndFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	session := archiveTestSession(t, now)
	if err := session.BeginArchive("archive-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.FailArchive("archive-1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateFailed || session.HealthStatus != HealthUnhealthy || session.Infrastructure.InstanceID == "" || session.Infrastructure.DataVolumeID == "" {
		t.Fatalf("failed archive session = %#v", session)
	}
}

func TestSessionArchiveLifecycle_AbortsCleanlyWhenWorkflowCannotStart(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	session := archiveTestSession(t, now)
	if err := session.BeginArchive("archive-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.AbortArchiveWorkflowStart("archive-1", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateRunning || session.ActiveWorkflowID != "" || session.ArchiveSourceState != "" || session.HealthStatus != HealthHealthy {
		t.Fatalf("aborted archive session = %#v", session)
	}
}

func TestSessionRestoreLifecycle_ReplacesDisposableInfrastructure(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	session := archiveTestSession(t, now)
	metadata := ArchiveMetadata{ID: "archive-1", ObjectKey: "sessions/session-1/archives/archive-1/session.tar.gz", ManifestObjectKey: "sessions/session-1/archives/archive-1/manifest.v1.json", ManifestSHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), ManifestSizeBytes: 123, SHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), SizeBytes: 42, Format: "tar+gzip", VerifiedAt: now}
	session.Infrastructure = Infrastructure{}
	session.Archive = metadata
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateArchived, StateArchived, StateArchived, HealthStopped
	if err := session.BeginRestore("restore-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.RecordRestoreCapacity("restore-1", "slot-1", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	infrastructure := Infrastructure{CapacitySlotID: "slot-1", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile", AMIID: "ami-2", InstanceType: "c7i-flex.large", InstanceID: "i-2", DataVolumeID: "vol-2", PublicIPv4: "203.0.113.2", LastObservedAt: now}
	if err := session.RecordRestoreInfrastructure("restore-1", infrastructure, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteRestore("restore-1", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateRunning || session.HealthStatus != HealthHealthy || session.Infrastructure.InstanceID != "i-2" || session.Archive != metadata || session.ActiveWorkflowID != "" {
		t.Fatalf("restored session = %#v", session)
	}
}

func TestArchiveManifestReadableIdentityIsAdditiveAndValidated(t *testing.T) {
	t.Parallel()
	manifest := ArchiveManifest{
		SchemaVersion: 1, ArchiveID: "archive-1", SessionID: "session-1",
		CreatedAt: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		Format:    "tar+gzip", ObjectKey: "sessions/session-1/archives/archive-1/session.tar.gz",
		SHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), SizeBytes: 42,
		ContentRoots: []string{"/srv/game-server/config"}, GameProfileID: "arma3-default",
		SourceInstanceID: "i-1", SourceDataVolumeID: "vol-1",
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("legacy manifest validation failed: %v", err)
	}
	manifest.SessionName, manifest.SessionSlug, manifest.Description = "Saturday Arma", "saturday-arma", "Weekly co-op night"
	if err := manifest.Validate(); err != nil {
		t.Fatalf("readable manifest validation failed: %v", err)
	}
	manifest.SessionSlug = ""
	if err := manifest.Validate(); err == nil {
		t.Fatal("manifest accepted partial readable identity")
	}
	manifest.SessionSlug, manifest.Description = "saturday-arma", "two\nlines"
	if err := manifest.Validate(); err == nil {
		t.Fatal("manifest accepted an unnormalized description")
	}
}

func archiveTestSession(t *testing.T, now time.Time) Session {
	t.Helper()
	session, err := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateRunning, StateRunning, StateRunning, HealthHealthy
	session.Infrastructure = Infrastructure{CapacitySlotID: "slot-1", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile", AMIID: "ami-1", InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.1", LastObservedAt: now}
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
	return session
}
