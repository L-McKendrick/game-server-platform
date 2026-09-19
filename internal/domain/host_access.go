package domain

import (
	"fmt"
	"path"
	"strings"
	"time"
)

const (
	HostAccessRuntimeConfigurationVersion = "scoped-host-access-v1"
	HostAccessLifetime                    = 15 * time.Minute
	HostAccessCredentialMargin            = time.Minute
	HostAccessRefreshBefore               = 5 * time.Minute
	HostAccessSchemaVersion               = 1
	MaxHostAccessManifestBytes            = 4 * 1024 * 1024
	MaxHostAccessReferenceBytes           = 8 * 1024
	MaxHostAccessCommandBytes             = 24 * 1024
	MaxHostProgressBytes                  = 16 * 1024
	MaxHostResultBytes                    = 256 * 1024
	MaxHostDiagnosticBytes                = 1024 * 1024
)

// HostAccessScope identifies one authorized command attempt. OperationID is an
// existing workflow ID, or an independently authorized live-copy identity.
// These fields constrain issuance; possession of a URL still grants its exact
// signed operation regardless of the caller's machine or workflow identity.
type HostAccessScope struct {
	SessionID      string    `json:"session_id"`
	GuildID        string    `json:"guild_id"`
	OperationID    string    `json:"operation_id"`
	AttemptID      string    `json:"attempt_id"`
	InstanceID     string    `json:"instance_id"`
	SnapshotSHA256 string    `json:"snapshot_sha256"`
	DeadlineAt     time.Time `json:"deadline_at"`
	CommandMode    string    `json:"command_mode,omitempty"`
}

// Equal compares deadline instants across JSON recovery, which removes Go's
// monotonic clock and may normalize a timezone representation.
func (scope HostAccessScope) Equal(other HostAccessScope) bool {
	return scope.SessionID == other.SessionID && scope.GuildID == other.GuildID &&
		scope.OperationID == other.OperationID && scope.AttemptID == other.AttemptID &&
		scope.InstanceID == other.InstanceID && scope.SnapshotSHA256 == other.SnapshotSHA256 && scope.CommandMode == other.CommandMode && scope.DeadlineAt.Equal(other.DeadlineAt)
}

func (scope HostAccessScope) Validate() error {
	if scope.CommandMode != "" && scope.CommandMode != "rollback" {
		return fmt.Errorf("host command mode invalid")
	}
	for _, id := range []string{scope.SessionID, scope.GuildID, scope.OperationID, scope.AttemptID, scope.InstanceID} {
		if !archiveIDPattern.MatchString(id) {
			return fmt.Errorf("host access identity must be one bounded path segment")
		}
	}
	if !validHexSHA256(scope.SnapshotSHA256) || scope.DeadlineAt.IsZero() {
		return fmt.Errorf("host access content snapshot and deadline are required")
	}
	return nil
}

// HostAccessRollbackAllowed uses the existing persisted rollback stage, not a
// caller assertion, to authorize a separate command after application timeout.
func (session Session) HostAccessRollbackAllowed(workflowID string) bool {
	return session.ActiveWorkflowID == workflowID && session.HasApplyingPresetRevision(workflowID) &&
		session.Progress.WorkflowID == workflowID && session.Progress.WorkflowType == session.ActiveWorkflowType &&
		session.Progress.State == ProgressRollingBack && session.Progress.StartedAt.Equal(session.ActiveWorkflowStartedAt)
}

// StagingKey names a fixed worker-selected slot, never a host-selected prefix.
// Validate the scope and slot before using this result for issuance.
func (scope HostAccessScope) StagingKey(slot string) string {
	return path.Join("sessions", scope.SessionID, "runtime", "host-access", scope.OperationID, scope.AttemptID, slot)
}

// HostAccessExpiry clips a short-lived capability to both the operation deadline
// and the signer's actual credential expiry. A zero credential expiry means the
// provider reports non-expiring credentials, not permission to extend a lease.
func HostAccessExpiry(now, deadline, credentialExpiry time.Time) (time.Time, error) {
	if now.IsZero() || deadline.IsZero() {
		return time.Time{}, fmt.Errorf("host access clock and deadline are required")
	}
	expiry := minTime(now.Add(HostAccessLifetime), deadline)
	if !credentialExpiry.IsZero() {
		expiry = minTime(expiry, credentialExpiry.Add(-HostAccessCredentialMargin))
	}
	// Signing uses whole seconds. Never round a deadline or credential window up.
	expiry = expiry.UTC().Truncate(time.Second)
	if expiry.Sub(now) < HostAccessCredentialMargin {
		return time.Time{}, fmt.Errorf("host access validity window is too short")
	}
	return expiry, nil
}

func minTime(left, right time.Time) time.Time {
	if right.Before(left) {
		return right
	}
	return left
}

type HostObjectPurpose string

const (
	HostObjectManifest           HostObjectPurpose = "manifest"
	HostObjectBootstrapScript    HostObjectPurpose = "bootstrap_script"
	HostObjectMission            HostObjectPurpose = "mission"
	HostObjectClientPreset       HostObjectPurpose = "client_preset"
	HostObjectServerPreset       HostObjectPurpose = "server_preset"
	HostObjectServerConfig       HostObjectPurpose = "server_config"
	HostObjectRestoreArchive     HostObjectPurpose = "restore_archive"
	HostObjectArchiveUpload      HostObjectPurpose = "archive_upload"
	HostObjectWorkshopMission    HostObjectPurpose = "workshop_mission"
	HostObjectWorkshopResolution HostObjectPurpose = "workshop_resolution"
	HostObjectWorkshopResult     HostObjectPurpose = "workshop_result"
	HostObjectProgress           HostObjectPurpose = "progress"
	HostObjectDiagnostic         HostObjectPurpose = "diagnostic"
)

type HostObjectAction string

const (
	HostObjectRead   HostObjectAction = "READ"
	HostObjectCreate HostObjectAction = "CREATE"
	HostObjectStage  HostObjectAction = "STAGE"
)

func (purpose HostObjectPurpose) Action() HostObjectAction {
	switch purpose {
	case HostObjectManifest, HostObjectBootstrapScript, HostObjectMission, HostObjectClientPreset,
		HostObjectServerPreset, HostObjectServerConfig, HostObjectRestoreArchive:
		return HostObjectRead
	case HostObjectArchiveUpload:
		return HostObjectCreate
	case HostObjectWorkshopMission, HostObjectWorkshopResolution, HostObjectWorkshopResult, HostObjectProgress, HostObjectDiagnostic:
		return HostObjectStage
	default:
		return ""
	}
}

// HostObjectRequest contains non-secret, worker-selected object constraints.
// Validation checks structural isolation, not accepted-content authority. The
// issuer must also strongly reload and authorize every exact referenced object.
// SHA256 uses the S3 base64 representation; snapshot digests use hexadecimal.
type HostObjectRequest struct {
	Purpose     HostObjectPurpose `json:"purpose"`
	Slot        string            `json:"slot"`
	Key         string            `json:"key"`
	SHA256      string            `json:"sha256,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	MinBytes    int64             `json:"min_bytes,omitempty"`
	MaxBytes    int64             `json:"max_bytes"`
	VersionID   string            `json:"version_id,omitempty"`
}

func (object HostObjectRequest) Validate(scope HostAccessScope) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if !archiveIDPattern.MatchString(object.Slot) || object.Key == "" || len(object.Key) > 1024 ||
		object.Key != path.Clean(object.Key) || strings.HasPrefix(object.Key, "/") ||
		strings.ContainsAny(object.Key, "\\*?%\x00\r\n\t") {
		return fmt.Errorf("host object requires a bounded slot and an exact canonical key")
	}
	if object.MaxBytes < 1 || object.MinBytes < 0 || object.MinBytes > object.MaxBytes ||
		(object.SHA256 != "" && !validSHA256(object.SHA256)) {
		return fmt.Errorf("host object size or checksum constraints are invalid")
	}
	if strings.ContainsAny(object.ContentType+object.VersionID, "\x00\r\n") {
		return fmt.Errorf("host object metadata contains unsafe characters")
	}
	sessionPrefix := "sessions/" + scope.SessionID + "/"
	switch object.Purpose.Action() {
	case HostObjectRead:
		if object.MinBytes != 0 {
			return fmt.Errorf("host read cannot carry upload constraints")
		}
		switch object.Purpose {
		case HostObjectManifest:
			if object.Slot != "manifest" || object.Key != scope.StagingKey("manifest") || object.MaxBytes > MaxHostAccessManifestBytes {
				return fmt.Errorf("host manifest is outside the authorized attempt")
			}
		case HostObjectBootstrapScript:
			if !strings.HasPrefix(object.Key, "platform/bootstrap/") {
				return fmt.Errorf("host script is outside the bootstrap namespace")
			}
		case HostObjectServerConfig:
			if !strings.HasPrefix(object.Key, "guilds/"+scope.GuildID+"/server-config/revisions/") || object.MaxBytes > MaximumServerConfigBytes {
				return fmt.Errorf("host configuration is outside its guild or bounds")
			}
		case HostObjectRestoreArchive:
			if !strings.HasPrefix(object.Key, sessionPrefix+"archives/") || object.MaxBytes > MaxArchiveSizeBytes {
				return fmt.Errorf("host archive is outside its session or bounds")
			}
		default:
			if !strings.HasPrefix(object.Key, sessionPrefix+"input/") {
				return fmt.Errorf("host input is outside its session")
			}
		}
		if object.SHA256 == "" && object.Purpose != HostObjectClientPreset && object.Purpose != HostObjectServerPreset {
			return fmt.Errorf("host read requires a trusted checksum")
		}
	case HostObjectCreate:
		if object.Key != sessionPrefix+"archives/"+scope.OperationID+"/session.tar.gz" || object.MinBytes != object.MaxBytes ||
			object.MaxBytes > MaxArchiveSizeBytes || object.SHA256 == "" || object.ContentType != "application/gzip" || object.VersionID != "" {
			return fmt.Errorf("host archive upload requires an exact key, length, checksum, and gzip type")
		}
	case HostObjectStage:
		if object.Key != scope.StagingKey(object.Slot) || object.MinBytes < 1 || object.ContentType == "" || object.VersionID != "" {
			return fmt.Errorf("host upload requires a fixed attempt slot and bounded form")
		}
		limit := int64(MaxHostResultBytes)
		switch object.Purpose {
		case HostObjectWorkshopMission:
			limit = MaximumWorkshopMissionBytes
		case HostObjectProgress:
			limit = MaxHostProgressBytes
		case HostObjectDiagnostic:
			limit = MaxHostDiagnosticBytes
		}
		if object.MaxBytes > limit {
			return fmt.Errorf("host upload exceeds its purpose limit")
		}
	default:
		return fmt.Errorf("host object purpose is unsupported")
	}
	return nil
}
