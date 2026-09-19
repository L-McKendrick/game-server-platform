package domain

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func hostAccessTestScope() HostAccessScope {
	return HostAccessScope{
		SessionID: "session-1", GuildID: "guild-1", OperationID: "workflow-1", AttemptID: "attempt-1",
		InstanceID: "i-1234567890abcdef0", SnapshotSHA256: strings.Repeat("a", 64),
		DeadlineAt: time.Date(2026, 9, 17, 3, 0, 0, 0, time.UTC),
	}
}

func hostAccessTestDigest() string {
	digest := sha256.Sum256([]byte("test"))
	return base64.StdEncoding.EncodeToString(digest[:])
}

func TestHostAccessExpiryNeverExceedsLeaseOrCredentials(t *testing.T) {
	now := time.Date(2026, 9, 17, 1, 0, 0, 500000000, time.UTC)
	for _, test := range []struct {
		name                        string
		deadline, credentials, want time.Time
	}{
		{"short lifetime", now.Add(48 * time.Hour), time.Time{}, now.Add(HostAccessLifetime).Truncate(time.Second)},
		{"operation deadline", now.Add(3 * time.Minute), time.Time{}, now.Add(3 * time.Minute).Truncate(time.Second)},
		{"temporary credentials", now.Add(time.Hour), now.Add(4 * time.Minute), now.Add(3 * time.Minute).Truncate(time.Second)},
		{"earlier deadline", now.Add(2 * time.Minute), now.Add(4 * time.Minute), now.Add(2 * time.Minute).Truncate(time.Second)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := HostAccessExpiry(now, test.deadline, test.credentials)
			if err != nil || !got.Equal(test.want) {
				t.Fatalf("expiry = %s, error = %v", got, err)
			}
		})
	}
	for _, test := range []struct{ deadline, credentials time.Time }{
		{now.Add(time.Hour), now.Add(90 * time.Second)}, {now, time.Time{}},
		{now.Add(-time.Hour), time.Time{}}, {time.Time{}, time.Time{}},
	} {
		if _, err := HostAccessExpiry(now, test.deadline, test.credentials); err == nil {
			t.Fatal("unsafe validity window accepted")
		}
	}
}

func TestHostObjectsRejectCrossBoundaryAndUnboundedRequests(t *testing.T) {
	scope := hostAccessTestScope()
	progress := HostObjectRequest{Purpose: HostObjectProgress, Slot: "progress", Key: scope.StagingKey("progress"), ContentType: "text/plain", MinBytes: 1, MaxBytes: MaxHostProgressBytes}
	archive := HostObjectRequest{Purpose: HostObjectArchiveUpload, Slot: "archive", Key: "sessions/session-1/archives/workflow-1/session.tar.gz", SHA256: hostAccessTestDigest(), ContentType: "application/gzip", MinBytes: 4, MaxBytes: 4}
	mission := HostObjectRequest{Purpose: HostObjectMission, Slot: "mission", Key: "sessions/session-1/input/missions/test.pbo", SHA256: hostAccessTestDigest(), MaxBytes: 100}
	config := HostObjectRequest{Purpose: HostObjectServerConfig, Slot: "config", Key: "guilds/guild-1/server-config/revisions/1/server.cfg", SHA256: hostAccessTestDigest(), MaxBytes: MaximumServerConfigBytes}
	for _, object := range []HostObjectRequest{progress, archive, mission, config} {
		if err := object.Validate(scope); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name   string
		object HostObjectRequest
		edit   func(*HostObjectRequest)
	}{
		{"other session input", mission, func(o *HostObjectRequest) { o.Key = strings.ReplaceAll(o.Key, "session-1", "session-2") }},
		{"other guild config", config, func(o *HostObjectRequest) { o.Key = strings.ReplaceAll(o.Key, "guild-1", "guild-2") }},
		{"other attempt output", progress, func(o *HostObjectRequest) { o.Key = strings.ReplaceAll(o.Key, "attempt-1", "attempt-2") }},
		{"durable progress write", progress, func(o *HostObjectRequest) { o.Key = "sessions/session-1/runtime/bootstrap-progress-workflow-1.txt" }},
		{"wrong archive workflow", archive, func(o *HostObjectRequest) { o.Key = strings.ReplaceAll(o.Key, "workflow-1", "workflow-2") }},
		{"prefix delegation", mission, func(o *HostObjectRequest) { o.Key = "sessions/session-1/input/*" }},
		{"traversal", mission, func(o *HostObjectRequest) { o.Key = "sessions/session-1/input/../../session-2/input/a.pbo" }},
		{"encoded traversal", mission, func(o *HostObjectRequest) { o.Key = "sessions/session-1/input/%2e%2e/a.pbo" }},
		{"unknown purpose", progress, func(o *HostObjectRequest) { o.Purpose = "arbitrary" }},
		{"unbounded progress", progress, func(o *HostObjectRequest) { o.MaxBytes++ }},
		{"no size bound", progress, func(o *HostObjectRequest) { o.MaxBytes = 0 }},
		{"empty upload", progress, func(o *HostObjectRequest) { o.MinBytes = 0 }},
		{"range inversion", progress, func(o *HostObjectRequest) { o.MinBytes = o.MaxBytes + 1 }},
		{"unbound archive length", archive, func(o *HostObjectRequest) { o.MinBytes-- }},
		{"unbound archive digest", archive, func(o *HostObjectRequest) { o.SHA256 = "" }},
		{"archive exceeds bound", archive, func(o *HostObjectRequest) { o.MinBytes = MaxArchiveSizeBytes + 1; o.MaxBytes = o.MinBytes }},
		{"upload version selection", archive, func(o *HostObjectRequest) { o.VersionID = "other-version" }},
		{"wrong digest representation", mission, func(o *HostObjectRequest) { o.SHA256 = strings.Repeat("a", 64) }},
		{"header injection", config, func(o *HostObjectRequest) { o.ContentType = "text/plain\r\nAuthorization: test" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			object := test.object
			test.edit(&object)
			if err := object.Validate(scope); err == nil {
				t.Fatal("unsafe request accepted")
			}
		})
	}
	for _, id := range []string{"../session-2", "session/2", "", strings.Repeat("a", 65)} {
		badScope := scope
		badScope.SessionID = id
		if err := progress.Validate(badScope); err == nil {
			t.Fatal("unsafe scope accepted")
		}
	}
}

func TestHostObjectsCoverEveryInventoryPurpose(t *testing.T) {
	scope := hostAccessTestScope()
	for _, test := range []struct {
		purpose HostObjectPurpose
		key     string
		max     int64
	}{
		{HostObjectManifest, scope.StagingKey("manifest"), MaxHostAccessManifestBytes},
		{HostObjectBootstrapScript, "platform/bootstrap/arma3-digest.sh", 50000},
		{HostObjectClientPreset, "sessions/session-1/input/presets/client.html", 1000},
		{HostObjectServerPreset, "sessions/session-1/input/presets/server.html", 1000},
		{HostObjectRestoreArchive, "sessions/session-1/archives/old/session.tar.gz", MaxArchiveSizeBytes},
		{HostObjectWorkshopMission, scope.StagingKey("item-200"), MaximumWorkshopMissionBytes},
		{HostObjectWorkshopResolution, scope.StagingKey("resolution"), MaxHostResultBytes},
		{HostObjectWorkshopResult, scope.StagingKey("result"), MaxHostResultBytes},
		{HostObjectDiagnostic, scope.StagingKey("diagnostic"), MaxHostDiagnosticBytes},
	} {
		t.Run(string(test.purpose), func(t *testing.T) {
			object := HostObjectRequest{Purpose: test.purpose, Slot: "object", Key: test.key, MaxBytes: test.max, SHA256: hostAccessTestDigest()}
			if test.purpose == HostObjectManifest {
				object.Slot = "manifest"
			}
			if test.purpose.Action() == HostObjectStage {
				object.Slot = test.key[strings.LastIndex(test.key, "/")+1:]
				object.MinBytes, object.ContentType = 1, "application/octet-stream"
			}
			if err := object.Validate(scope); err != nil {
				t.Fatal(err)
			}
		})
	}
}
