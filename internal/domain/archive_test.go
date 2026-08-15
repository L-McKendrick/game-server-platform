package domain

import (
	"encoding/base64"
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
