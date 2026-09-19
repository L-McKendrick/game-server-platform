package ports

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// HostObjectSigner is a transport boundary, not an authorization service.
// Trusted issuers must authorize exact objects against current session/workflow
// records and managed-instance tags before invoking it. No public endpoint or
// host-originated arbitrary-key request may call this boundary directly.
type HostObjectSigner interface {
	Sign(ctx context.Context, scope domain.HostAccessScope, object domain.HostObjectRequest) (HostObjectCapability, error)
}

// HostAccessAttemptRepository stores recovery metadata only. CAS must atomically
// check the strongly read session version, live workflow lock/status and revision.
// Records are keyed by session, operation AND attempt so normal application and
// rollback can coexist. An expected revision of zero creates; reads are consistent.
type HostAccessAttemptRepository interface {
	GetHostAccessAttempt(context.Context, string, string, string) (domain.HostAccessAttempt, error)
	SaveHostAccessAttempt(context.Context, domain.HostAccessAttempt, int64, int64, time.Time) error
}

type HostAccessAttemptLister interface {
	ListHostAccessAttempts(context.Context, string) ([]domain.HostAccessAttempt, error)
}

type HostAccessMaintenance interface {
	CleanupSessionHostAttempts(context.Context, string) (int, error)
}

// HostManifestExchange is privileged temporary, encrypted, versioned storage.
// Payloads contain bearer URLs and must never be logged. Reads are bounded.
type HostManifestExchange interface {
	PutHostManifest(context.Context, domain.HostAccessScope, []byte) (string, error)
	GetHostManifest(context.Context, domain.HostAccessScope, string) ([]byte, error)
}

// HostAccessAttemptCleaner removes every version/delete marker in an expired
// attempt namespace only. It never deletes accepted inputs or durable archives.
type HostAccessAttemptCleaner interface {
	DeleteHostAccessAttempt(context.Context, domain.HostAccessScope) (int, error)
}

type LiveMissionAccess interface {
	SignLiveMission(context.Context, string, string) (HostAccessManifest, error)
}

// HostReadInspector hashes bounded accepted bytes and returns their immutable
// version. Callers must authorize the exact key before inspecting it.
type HostReadInspector interface {
	InspectHostRead(context.Context, string, string, int64) (string, string, int64, error)
}

// HostArchiveInspector checks a single-PUT immutable gzip object's checksum,
// length and version without downloading its potentially 4-GiB payload.
// The trusted caller must authorize the exact archive key before inspection.
type HostArchiveInspector interface {
	InspectHostArchive(context.Context, string, string) (domain.HostOutputVersion, error)
}

// HostWorkshopStore is a trusted storage boundary. Application authorization
// derives both keys; hosts receive only fixed staging POST capabilities.
type HostWorkshopStore interface {
	ReadHostOutput(context.Context, string, string, string, int64) ([]byte, domain.HostOutputVersion, error)
	PromoteHostOutput(context.Context, string, string, string, domain.HostOutputVersion) (string, error)
	PublishHostDocument(context.Context, string, string, []byte) (string, error)
}

// HostCommandEnvironment builds trusted command context. On recovery, prior
// privileged context is supplied so external exchange URLs can remain immutable.
type HostCommandEnvironment func(context.Context, map[string]string) (map[string]string, error)

type HostCommandAccess interface {
	PrepareHostCommand(context.Context, string, string, bool, time.Duration, HostCommandEnvironment) (HostAccessReference, error)
}

type HostCommandRenewal interface {
	RefreshHostCommand(context.Context, domain.HostAccessScope) (HostAccessReference, error)
	RecordHostRefresh(context.Context, domain.HostAccessScope, int64) error
}

type HostWorkshopPublication interface {
	PublishHostWorkshop(context.Context, domain.HostAccessScope) error
}

// HostObjectCapability is ephemeral privileged transport data. Headers contains
// every required signed header; Form contains every required POST policy field.
// Never persist it in workflow records, accepted assets, ordinary logs or events.
type HostObjectCapability struct {
	Object    domain.HostObjectRequest `json:"object"`
	Method    string                   `json:"method"`
	URL       string                   `json:"url"`
	Headers   map[string]string        `json:"headers,omitempty"`
	Form      map[string]string        `json:"form,omitempty"`
	ExpiresAt time.Time                `json:"expires_at"`
}

func (capability HostObjectCapability) String() string {
	return fmt.Sprintf("host object capability purpose=%s slot=%s", capability.Object.Purpose, capability.Object.Slot)
}

// HostAccessManifest is a versioned envelope for one authorized attempt. Large
// manifests travel through short-lived encrypted S3 exchange objects, referenced
// by a compact SSM command. The envelope is not durable session content.
type HostAccessManifest struct {
	SchemaVersion int                    `json:"schema_version"`
	Scope         domain.HostAccessScope `json:"scope"`
	Objects       []HostObjectCapability `json:"objects"`
	// Environment is trusted-worker command context in the privileged exchange,
	// never durable metadata. Values are base64-encoded only when exported on host.
	Environment map[string]string `json:"environment,omitempty"`
}

var hostEnvironmentName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*_B64$`)

func ValidateHostEnvironment(environment map[string]string) error {
	for name := range environment {
		if !hostEnvironmentName.MatchString(name) {
			return fmt.Errorf("host environment name invalid")
		}
	}
	return nil
}

func (manifest HostAccessManifest) String() string {
	return fmt.Sprintf("host access manifest operation=%s attempt=%s objects=%d", manifest.Scope.OperationID, manifest.Scope.AttemptID, len(manifest.Objects))
}

// HostAccessReference is the bounded, digest-verified SSM handoff to a manifest.
// Refreshed references are installed atomically in a root-only /run directory;
// generation prevents out-of-order SSM deliveries replacing a newer reference.
type HostAccessReference struct {
	SchemaVersion int                    `json:"schema_version"`
	Scope         domain.HostAccessScope `json:"scope"`
	Generation    int64                  `json:"generation"`
	URL           string                 `json:"url"`
	SHA256        string                 `json:"sha256"`
	SizeBytes     int64                  `json:"size_bytes"`
	ExpiresAt     time.Time              `json:"expires_at"`
}

func (reference HostAccessReference) String() string {
	return fmt.Sprintf("host access reference operation=%s attempt=%s generation=%d", reference.Scope.OperationID, reference.Scope.AttemptID, reference.Generation)
}

// Encode validates the ephemeral envelope before it is sent to an exact host.
// Its size bound is independent of the much smaller SSM handoff reference.
func (manifest HostAccessManifest) Encode(now time.Time) ([]byte, error) {
	if err := ValidateHostEnvironment(manifest.Environment); err != nil {
		return nil, err
	}
	if err := manifest.Scope.Validate(); err != nil {
		return nil, err
	}
	if manifest.SchemaVersion != domain.HostAccessSchemaVersion || len(manifest.Objects) == 0 {
		return nil, fmt.Errorf("host access manifest schema and objects are required")
	}
	slots := make(map[string]bool, len(manifest.Objects))
	for _, capability := range manifest.Objects {
		if err := capability.Object.Validate(manifest.Scope); err != nil {
			return nil, err
		}
		if slots[capability.Object.Slot] {
			return nil, fmt.Errorf("duplicate host access slot")
		}
		slots[capability.Object.Slot] = true
		if err := validateHostAccessURL(capability.URL); err != nil {
			return nil, err
		}
		if err := validateHostAccessWindow(now, manifest.Scope.DeadlineAt, capability.ExpiresAt); err != nil {
			return nil, err
		}
		validTransport := false
		switch capability.Object.Purpose.Action() {
		case domain.HostObjectRead:
			validTransport = capability.Method == "GET" && len(capability.Form) == 0
		case domain.HostObjectCreate:
			validTransport = capability.Method == "PUT" && len(capability.Form) == 0 &&
				capability.Headers["Content-Length"] == strconv.FormatInt(capability.Object.MaxBytes, 10) &&
				capability.Headers["Content-Type"] == capability.Object.ContentType &&
				capability.Headers["If-None-Match"] == "*" && capability.Headers["X-Amz-Checksum-Sha256"] == capability.Object.SHA256
		case domain.HostObjectStage:
			validTransport = capability.Method == "POST" && len(capability.Headers) == 0 &&
				capability.Form["key"] == capability.Object.Key && capability.Form["Content-Type"] == capability.Object.ContentType &&
				capability.Form["policy"] != "" && capability.Form["X-Amz-Signature"] != ""
		}
		if !validTransport {
			return nil, fmt.Errorf("host access transport contradicts object purpose")
		}
	}
	return encodeHostAccess(manifest, domain.MaxHostAccessManifestBytes)
}

func (reference HostAccessReference) Encode(now time.Time) ([]byte, error) {
	if err := reference.Scope.Validate(); err != nil {
		return nil, err
	}
	digest, err := base64.StdEncoding.DecodeString(reference.SHA256)
	if err != nil || len(digest) != 32 || reference.SchemaVersion != domain.HostAccessSchemaVersion || reference.Generation < 1 ||
		reference.SizeBytes < 1 || reference.SizeBytes > domain.MaxHostAccessManifestBytes {
		return nil, fmt.Errorf("host access reference identity or manifest bounds are invalid")
	}
	if err := validateHostAccessURL(reference.URL); err != nil {
		return nil, err
	}
	if err := validateHostAccessWindow(now, reference.Scope.DeadlineAt, reference.ExpiresAt); err != nil {
		return nil, err
	}
	return encodeHostAccess(reference, domain.MaxHostAccessReferenceBytes)
}

func validateHostAccessURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("host access requires an HTTPS capability without userinfo or fragment")
	}
	return nil
}

func validateHostAccessWindow(now, deadline, expiry time.Time) error {
	if now.IsZero() || !expiry.After(now) || expiry.After(deadline) || expiry.After(now.Add(domain.HostAccessLifetime)) {
		return fmt.Errorf("host access expiry exceeds the current operation window")
	}
	return nil
}

func encodeHostAccess(value any, limit int) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode host access envelope")
	}
	if len(body) > limit {
		return nil, fmt.Errorf("host access envelope exceeds %d bytes", limit)
	}
	return body, nil
}
