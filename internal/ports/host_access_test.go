package ports

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func manifestFixture() (HostAccessManifest, time.Time) {
	now := time.Date(2026, 9, 17, 1, 0, 0, 0, time.UTC)
	digest := sha256.Sum256([]byte("mission"))
	return HostAccessManifest{
		SchemaVersion: domain.HostAccessSchemaVersion,
		Scope: domain.HostAccessScope{
			SessionID: "session-1", GuildID: "guild-1", OperationID: "workflow-1", AttemptID: "attempt-1",
			InstanceID: "i-1234567890abcdef0", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: now.Add(48 * time.Hour),
		},
		Objects: []HostObjectCapability{{
			Object: domain.HostObjectRequest{Purpose: domain.HostObjectMission, Slot: "mission-1", Key: "sessions/session-1/input/missions/a.pbo", SHA256: base64.StdEncoding.EncodeToString(digest[:]), MaxBytes: 100},
			Method: "GET", URL: "https://assets.s3.us-west-2.amazonaws.com/mission?signature=private-token", ExpiresAt: now.Add(domain.HostAccessLifetime),
		}},
	}, now
}

func TestHostAccessManifestRejectsInvalidWireContracts(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*HostAccessManifest)
	}{
		{"unknown schema", func(m *HostAccessManifest) { m.SchemaVersion++ }},
		{"empty manifest", func(m *HostAccessManifest) { m.Objects = nil }},
		{"duplicate slot", func(m *HostAccessManifest) { m.Objects = append(m.Objects, m.Objects[0]) }},
		{"upload for read", func(m *HostAccessManifest) { m.Objects[0].Method = "PUT" }},
		{"unexpected form", func(m *HostAccessManifest) { m.Objects[0].Form = map[string]string{"key": "other"} }},
		{"expired", func(m *HostAccessManifest) {
			m.Objects[0].ExpiresAt = m.Objects[0].ExpiresAt.Add(-domain.HostAccessLifetime)
		}},
		{"unbounded lifetime", func(m *HostAccessManifest) { m.Objects[0].ExpiresAt = m.Objects[0].ExpiresAt.Add(time.Second) }},
		{"past lease", func(m *HostAccessManifest) { m.Scope.DeadlineAt = m.Objects[0].ExpiresAt.Add(-time.Second) }},
		{"non HTTPS", func(m *HostAccessManifest) { m.Objects[0].URL = "http://assets/object" }},
		{"userinfo", func(m *HostAccessManifest) { m.Objects[0].URL = "https://user:password@assets/object" }},
		{"fragment", func(m *HostAccessManifest) { m.Objects[0].URL += "#fragment" }},
		{"oversize envelope", func(m *HostAccessManifest) {
			m.Objects[0].URL += strings.Repeat("x", domain.MaxHostAccessManifestBytes)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest, now := manifestFixture()
			test.edit(&manifest)
			if _, err := manifest.Encode(now); err == nil {
				t.Fatal("unsafe manifest accepted")
			}
		})
	}
}

func TestHostAccessLargeManifestUsesBoundedSSMReference(t *testing.T) {
	manifest, now := manifestFixture()
	prototype := manifest.Objects[0]
	// Exercise the 1,000-item snapshot scale with realistic large temporary
	// credential URLs; none of these per-object capabilities enters SSM inline.
	for index := 1; index < domain.MaximumWorkshopMissionSnapshotItems; index++ {
		capability := prototype
		capability.Object.Slot = fmt.Sprintf("mission-%d", index+1)
		capability.Object.Key = fmt.Sprintf("sessions/session-1/input/missions/%d.pbo", index)
		capability.URL += strings.Repeat("x", 2048)
		manifest.Objects = append(manifest.Objects, capability)
	}
	body, err := manifest.Encode(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) <= domain.MaxHostAccessCommandBytes {
		t.Fatal("fixture does not exercise an oversized inline manifest")
	}
	digest := sha256.Sum256(body)
	reference := HostAccessReference{
		SchemaVersion: domain.HostAccessSchemaVersion, Scope: manifest.Scope, Generation: 1,
		URL: prototype.URL, SHA256: base64.StdEncoding.EncodeToString(digest[:]), SizeBytes: int64(len(body)), ExpiresAt: prototype.ExpiresAt,
	}
	encodedReference, err := reference.Encode(now)
	if err != nil {
		t.Fatal(err)
	}
	// Reference base64 plus a conservative 8-KiB fetch/verification script
	// remains below the project's 24-KiB SSM command budget (AWS document: 64 KiB).
	if base64.StdEncoding.EncodedLen(len(encodedReference))+8*1024 > domain.MaxHostAccessCommandBytes {
		t.Fatal("manifest reference exceeds the compact SSM transport budget")
	}
	for _, test := range []struct {
		name string
		edit func(*HostAccessReference)
	}{
		{"oversize reference", func(r *HostAccessReference) { r.URL += strings.Repeat("x", domain.MaxHostAccessReferenceBytes) }},
		{"oversize manifest", func(r *HostAccessReference) { r.SizeBytes = domain.MaxHostAccessManifestBytes + 1 }},
		{"missing generation", func(r *HostAccessReference) { r.Generation = 0 }},
		{"missing integrity", func(r *HostAccessReference) { r.SHA256 = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := reference
			test.edit(&changed)
			if _, err := changed.Encode(now); err == nil {
				t.Fatal("unsafe reference accepted")
			}
		})
	}
}

func TestHostAccessFormattingRedactsTransportSecrets(t *testing.T) {
	manifest, now := manifestFixture()
	capability := manifest.Objects[0]
	capability.Headers = map[string]string{"X-Private": "private-header"}
	capability.Form = map[string]string{"policy": "private-policy"}
	reference := HostAccessReference{Scope: manifest.Scope, URL: capability.URL, ExpiresAt: now.Add(time.Minute)}
	for _, value := range []any{capability, manifest, reference} {
		formatted := fmt.Sprintf("%v %+v", value, value)
		for _, secret := range []string{"private-token", "private-header", "private-policy"} {
			if strings.Contains(formatted, secret) {
				t.Fatal("transport secret leaked by ordinary formatting")
			}
		}
	}
}

func TestHostAccessManifestPreservesRequiredUploadTransport(t *testing.T) {
	manifest, now := manifestFixture()
	digest := manifest.Objects[0].Object.SHA256
	archive := HostObjectCapability{
		Object: domain.HostObjectRequest{Purpose: domain.HostObjectArchiveUpload, Slot: "archive", Key: "sessions/session-1/archives/workflow-1/session.tar.gz", MinBytes: 4, MaxBytes: 4, SHA256: digest, ContentType: "application/gzip"},
		Method: "PUT", URL: "https://assets.s3.us-west-2.amazonaws.com/archive?signature=test", ExpiresAt: now.Add(domain.HostAccessLifetime),
		Headers: map[string]string{"Content-Length": "4", "Content-Type": "application/gzip", "If-None-Match": "*", "X-Amz-Checksum-Sha256": digest},
	}
	progress := HostObjectCapability{
		Object: domain.HostObjectRequest{Purpose: domain.HostObjectProgress, Slot: "progress", Key: manifest.Scope.StagingKey("progress"), MinBytes: 1, MaxBytes: domain.MaxHostProgressBytes, ContentType: "text/plain"},
		Method: "POST", URL: "https://assets.s3.us-west-2.amazonaws.com", ExpiresAt: archive.ExpiresAt,
		Form: map[string]string{"key": manifest.Scope.StagingKey("progress"), "Content-Type": "text/plain", "policy": "test-policy", "X-Amz-Signature": "test-signature"},
	}
	manifest.Objects = append(manifest.Objects, archive, progress)
	if _, err := manifest.Encode(now); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Content-Length", "Content-Type", "If-None-Match", "X-Amz-Checksum-Sha256"} {
		t.Run("missing "+field, func(t *testing.T) {
			removed := archive.Headers[field]
			delete(archive.Headers, field)
			defer func() { archive.Headers[field] = removed }()
			if _, err := manifest.Encode(now); err == nil {
				t.Fatal("lost signed header accepted")
			}
		})
	}
	progress.Form["key"] = "sessions/session-2/progress"
	if _, err := manifest.Encode(now); err == nil {
		t.Fatal("wrong upload form key accepted")
	}
}
