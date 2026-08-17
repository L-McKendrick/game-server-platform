package domain

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const ArchiveWorkflowType = "ArchiveSession"
const RestoreWorkflowType = "RestoreSession"
const MaxArchiveSizeBytes int64 = 4 * 1024 * 1024 * 1024

var archiveIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ArchiveMetadata identifies the latest portable, checksum-verified session backup.
type ArchiveMetadata struct {
	ID                string
	ObjectKey         string
	ManifestObjectKey string
	ManifestSHA256    string
	ManifestSizeBytes int64
	SHA256            string
	SizeBytes         int64
	Format            string
	VerifiedAt        time.Time
}

func (archive ArchiveMetadata) Empty() bool {
	return archive.ID == "" && archive.ObjectKey == "" && archive.ManifestObjectKey == "" && archive.ManifestSHA256 == "" && archive.ManifestSizeBytes == 0 &&
		archive.SHA256 == "" && archive.SizeBytes == 0 && archive.Format == "" && archive.VerifiedAt.IsZero()
}

func (archive ArchiveMetadata) Validate() error {
	switch {
	case !archiveIDPattern.MatchString(strings.TrimSpace(archive.ID)):
		return fmt.Errorf("archive ID is invalid")
	case !validManagedObjectKey(archive.ObjectKey):
		return fmt.Errorf("archive object key is invalid")
	case !validManagedObjectKey(archive.ManifestObjectKey):
		return fmt.Errorf("archive manifest object key is invalid")
	case !validSHA256(archive.ManifestSHA256):
		return fmt.Errorf("archive manifest SHA-256 is invalid")
	case archive.ManifestSizeBytes <= 0 || archive.ManifestSizeBytes > 1024*1024:
		return fmt.Errorf("archive manifest size is invalid")
	case !validSHA256(archive.SHA256):
		return fmt.Errorf("archive SHA-256 is invalid")
	case archive.SizeBytes <= 0:
		return fmt.Errorf("archive size must be positive")
	case archive.SizeBytes > MaxArchiveSizeBytes:
		return fmt.Errorf("archive size exceeds the Phase 9.1 limit")
	case archive.Format != "tar+gzip":
		return fmt.Errorf("unsupported archive format %q", archive.Format)
	case archive.VerifiedAt.IsZero():
		return fmt.Errorf("archive verification timestamp is required")
	default:
		return nil
	}
}

func validManagedObjectKey(key string) bool {
	key = strings.TrimSpace(key)
	return key != "" && !strings.HasPrefix(key, "/") && !strings.Contains(key, "..")
}

func validSHA256(value string) bool {
	digest, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	return err == nil && len(digest) == sha256.Size
}

func (session Session) CanArchive() bool {
	return session.ActiveWorkflowID == "" && session.Infrastructure.InstanceID != "" &&
		session.Infrastructure.DataVolumeID != "" && session.HealthStatus == HealthHealthy &&
		(session.LifecycleState == StateRunning || session.LifecycleState == StateIdle)
}

func (session *Session) BeginArchive(workflowID string, lease time.Duration, now time.Time) error {
	if !session.CanArchive() {
		return fmt.Errorf("%w: archive requires a running managed session", ErrInvalidTransition)
	}
	sourceState := session.LifecycleState
	if err := session.AcquireWorkflowLock(strings.TrimSpace(workflowID), ArchiveWorkflowType, lease, now); err != nil {
		return err
	}
	session.ArchiveSourceState = sourceState
	session.DesiredState, session.ObservedState, session.LifecycleState = StateArchiving, StateArchiving, StateArchiving
	session.HealthStatus = HealthStarting
	return session.Validate()
}

// RecordVerifiedArchive durably crosses the destructive boundary. Callers may
// terminate compute only after this transition has been conditionally saved.
func (session *Session) RecordVerifiedArchive(workflowID string, archive ArchiveMetadata, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || session.ActiveWorkflowType != ArchiveWorkflowType {
		return ErrConflict
	}
	if err := archive.Validate(); err != nil {
		return err
	}
	if session.ArchiveSourceState != StateRunning && session.ArchiveSourceState != StateIdle {
		return fmt.Errorf("%w: archive source state is invalid", ErrConflict)
	}
	session.Archive = archive
	session.ArchiveSourceState = ""
	session.DesiredState, session.ObservedState, session.LifecycleState = StateArchived, StateDestroying, StateDestroying
	session.HealthStatus = HealthStopped
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session *Session) CompleteArchive(workflowID string, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || session.ActiveWorkflowType != ArchiveWorkflowType {
		return ErrConflict
	}
	if session.LifecycleState != StateDestroying || session.Archive.Validate() != nil {
		return fmt.Errorf("%w: verified archive is required before destruction completes", ErrInvalidTransition)
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressCompleted, now); err != nil {
		return err
	}
	session.Infrastructure = Infrastructure{}
	session.DesiredState, session.ObservedState, session.LifecycleState = StateArchived, StateArchived, StateArchived
	session.HealthStatus = HealthStopped
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session *Session) AbortArchiveWorkflowStart(workflowID string, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || session.ActiveWorkflowType != ArchiveWorkflowType {
		return ErrConflict
	}
	if session.ArchiveSourceState != StateRunning && session.ArchiveSourceState != StateIdle {
		return fmt.Errorf("%w: archive source state is invalid", ErrConflict)
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressFailed, now); err != nil {
		return err
	}
	sourceState := session.ArchiveSourceState
	session.ArchiveSourceState = ""
	session.DesiredState, session.ObservedState, session.LifecycleState = sourceState, sourceState, sourceState
	session.HealthStatus = HealthHealthy
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session *Session) FailArchive(workflowID string, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || session.ActiveWorkflowType != ArchiveWorkflowType {
		return ErrConflict
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressFailed, now); err != nil {
		return err
	}
	session.ArchiveSourceState = ""
	session.DesiredState, session.ObservedState, session.LifecycleState = StateFailed, StateFailed, StateFailed
	session.HealthStatus = HealthUnhealthy
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session Session) CanRestore() bool {
	return session.ActiveWorkflowID == "" && session.LifecycleState == StateArchived &&
		session.Infrastructure.Empty() && session.Archive.Validate() == nil
}

func (session *Session) BeginRestore(workflowID string, lease time.Duration, now time.Time) error {
	if !session.CanRestore() {
		return fmt.Errorf("%w: restore requires an archived resource-free session", ErrInvalidTransition)
	}
	if err := session.AcquireWorkflowLock(strings.TrimSpace(workflowID), RestoreWorkflowType, lease, now); err != nil {
		return err
	}
	session.DesiredState, session.ObservedState, session.LifecycleState = StateRunning, StateRestoring, StateRestoring
	session.HealthStatus = HealthStarting
	return session.Validate()
}

func (session *Session) RecordRestoreCapacity(workflowID string, slotID string, now time.Time) error {
	if err := session.requireRestoreWorkflow(workflowID); err != nil {
		return err
	}
	if strings.TrimSpace(slotID) == "" {
		return fmt.Errorf("capacity slot ID is required")
	}
	session.Infrastructure = Infrastructure{CapacitySlotID: strings.TrimSpace(slotID)}
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session *Session) RecordRestoreInfrastructure(workflowID string, infrastructure Infrastructure, now time.Time) error {
	if err := session.requireRestoreWorkflow(workflowID); err != nil {
		return err
	}
	if infrastructure.CapacitySlotID == "" {
		infrastructure.CapacitySlotID = session.Infrastructure.CapacitySlotID
	}
	if err := infrastructure.Validate(); err != nil {
		return err
	}
	if infrastructure.InstanceID == "" {
		return fmt.Errorf("restore instance ID is required")
	}
	session.Infrastructure = infrastructure
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session *Session) CompleteRestore(workflowID string, now time.Time) error {
	if err := session.requireRestoreWorkflow(workflowID); err != nil {
		return err
	}
	if session.Infrastructure.InstanceID == "" || session.Infrastructure.DataVolumeID == "" {
		return fmt.Errorf("%w: restored infrastructure is incomplete", ErrInvalidTransition)
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressCompleted, now); err != nil {
		return err
	}
	session.DesiredState, session.ObservedState, session.LifecycleState = StateRunning, StateRunning, StateRunning
	session.HealthStatus = HealthHealthy
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session *Session) FailRestore(workflowID string, now time.Time) error {
	if err := session.requireRestoreWorkflow(workflowID); err != nil {
		return err
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressFailed, now); err != nil {
		return err
	}
	session.ObservedState, session.LifecycleState = StateFailed, StateFailed
	session.HealthStatus = HealthUnhealthy
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session *Session) AbortRestoreWorkflowStart(workflowID string, now time.Time) error {
	if err := session.requireRestoreWorkflow(workflowID); err != nil {
		return err
	}
	if !session.Infrastructure.Empty() {
		return fmt.Errorf("%w: restore resources already exist", ErrConflict)
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressFailed, now); err != nil {
		return err
	}
	session.DesiredState, session.ObservedState, session.LifecycleState = StateArchived, StateArchived, StateArchived
	session.HealthStatus = HealthStopped
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session Session) requireRestoreWorkflow(workflowID string) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || session.ActiveWorkflowType != RestoreWorkflowType || session.LifecycleState != StateRestoring {
		return ErrConflict
	}
	return nil
}

// ArchiveManifest is the versioned, portable description stored beside an archive.
type ArchiveManifest struct {
	SchemaVersion         int      `json:"schema_version"`
	ArchiveID             string   `json:"archive_id"`
	SessionID             string   `json:"session_id"`
	SessionName           string   `json:"session_name,omitempty"`
	SessionSlug           string   `json:"session_slug,omitempty"`
	Description           string   `json:"description,omitempty"`
	CreatedAt             string   `json:"created_at"`
	Format                string   `json:"format"`
	ObjectKey             string   `json:"object_key"`
	SHA256                string   `json:"sha256"`
	SizeBytes             int64    `json:"size_bytes"`
	ContentRoots          []string `json:"content_roots"`
	GameProfileID         string   `json:"game_profile_id"`
	ConfigurationRevision int64    `json:"configuration_revision"`
	MissionObjectKey      string   `json:"mission_object_key"`
	PresetObjectKey       string   `json:"preset_object_key"`
	Vanilla               bool     `json:"vanilla"`
	SourceInstanceID      string   `json:"source_instance_id"`
	SourceDataVolumeID    string   `json:"source_data_volume_id"`
}

func (manifest ArchiveManifest) Validate() error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("unsupported archive manifest schema version %d", manifest.SchemaVersion)
	}
	switch {
	case !archiveIDPattern.MatchString(strings.TrimSpace(manifest.ArchiveID)):
		return fmt.Errorf("manifest archive ID is invalid")
	case strings.TrimSpace(manifest.SessionID) == "":
		return fmt.Errorf("manifest session ID is required")
	case strings.TrimSpace(manifest.CreatedAt) == "":
		return fmt.Errorf("manifest creation timestamp is required")
	case manifest.Format != "tar+gzip":
		return fmt.Errorf("manifest archive format is invalid")
	case !validManagedObjectKey(manifest.ObjectKey):
		return fmt.Errorf("manifest archive object key is invalid")
	case !validSHA256(manifest.SHA256) || manifest.SizeBytes <= 0 || manifest.SizeBytes > MaxArchiveSizeBytes:
		return fmt.Errorf("manifest archive checksum and size are required")
	case len(manifest.ContentRoots) == 0:
		return fmt.Errorf("manifest content roots are invalid")
	case strings.TrimSpace(manifest.GameProfileID) == "":
		return fmt.Errorf("manifest game profile is required")
	case strings.TrimSpace(manifest.SourceInstanceID) == "" || strings.TrimSpace(manifest.SourceDataVolumeID) == "":
		return fmt.Errorf("manifest source infrastructure is required")
	}
	if !manifest.IncludesReadableIdentity() {
		return nil
	}
	if strings.TrimSpace(manifest.SessionName) == "" || !slugPattern.MatchString(manifest.SessionSlug) {
		return fmt.Errorf("manifest readable session identity is invalid")
	}
	description, err := NormalizeSessionDescription(manifest.Description)
	if err != nil || description != manifest.Description {
		return fmt.Errorf("manifest session description is invalid")
	}
	return nil
}

// IncludesReadableIdentity distinguishes additive Phase 12 manifests from
// legacy schema-v1 manifests, whose identity remains authoritative in metadata.
func (manifest ArchiveManifest) IncludesReadableIdentity() bool {
	return manifest.SessionName != "" || manifest.SessionSlug != "" || manifest.Description != ""
}
