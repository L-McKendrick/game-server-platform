package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

const (
	MaximumSessionDescriptionRunes = 64
	MaximumGeneratedSlugLength     = 64
)

// Session represents the persistent platform identity of a game server.
type Session struct {
	ID                    string
	Slug                  string
	DisplayName           string
	Description           string
	GameType              string
	OwnerDiscordUserID    string
	GuildID               string
	ChannelID             string
	GameProfileID         string
	SleepAfterSeconds     int64
	ArchiveAfterSeconds   int64
	TeamSpeakEnabled      bool
	Vanilla               bool
	CreatorDLCs           []string
	StartWhenReady        bool
	ConfigurationRevision int64
	ServerConfigRevision  int64
	ServerConfigObjectKey string
	ServerConfigSHA256    string
	MissionObjectKey      string
	MissionFiles          []MissionRecord
	ConfiguredMission     MissionSelection
	CurrentMission        MissionSelection
	// PresetObjectKey remains a write-through compatibility projection of the
	// active preset revision for older workers and persisted rows.
	PresetObjectKey        string
	PresetRevisionSequence int64
	ActivePresetRevision   PresetRevision
	PendingPresetRevision  PresetRevision
	MissionArtifactStatus  ArtifactStatus
	PresetArtifactStatus   ArtifactStatus
	MissionArtifactIssue   string
	PresetArtifactIssue    string
	Infrastructure         Infrastructure
	Archive                ArchiveMetadata
	ArchiveSourceState     LifecycleState
	Progress               SessionProgress
	Failure                FailureRecord

	ActiveWorkflowID             string
	ActiveWorkflowType           string
	ActiveWorkflowStartedAt      time.Time
	ActiveWorkflowLeaseExpiresAt time.Time

	DesiredState          LifecycleState
	ObservedState         LifecycleState
	LifecycleState        LifecycleState
	HealthStatus          HealthStatus
	MonitoringCommandID   string
	MonitoringStartedAt   time.Time
	PlayerCountKnown      bool
	PlayerCount           int
	PlayerCountObservedAt time.Time
	IdleSince             time.Time

	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AttachArtifact records a validated durable object and advances a complete
// draft to NEW without tying session identity to the source attachment URL.
func (session *Session) AttachArtifact(kind ArtifactKind, objectKey string, now time.Time) error {
	missionEditable := kind == ArtifactMission && session.ActiveWorkflowID == "" && session.LifecycleState != StateDeleting && session.LifecycleState != StateDeleted && session.LifecycleState != StateArchiving && session.LifecycleState != StateDestroying
	if session.LifecycleState != StateDraft && session.LifecycleState != StateNew && !missionEditable {
		return fmt.Errorf("%w: artifacts are only editable while a session is DRAFT", ErrInvalidTransition)
	}
	objectKey = strings.TrimSpace(objectKey)
	if objectKey == "" {
		return fmt.Errorf("artifact object key is required")
	}
	switch kind {
	case ArtifactMission:
		session.MissionObjectKey = objectKey
		session.ConfiguredMission = MissionSelection{Template: missionTemplateFromObjectKey(objectKey), ObjectKey: objectKey}
		found := false
		for index := range session.MissionFiles {
			if session.MissionFiles[index].ObjectKey == objectKey {
				session.MissionFiles[index].Status, session.MissionFiles[index].Issue = ArtifactAccepted, ""
				found = true
				break
			}
		}
		if !found {
			session.MissionFiles = append(session.MissionFiles, MissionRecord{ObjectKey: objectKey, Filename: missionFilenameFromObjectKey(objectKey), Status: ArtifactAccepted, AddedAt: now.UTC()})
		}
		session.MissionArtifactStatus = ArtifactAccepted
		session.MissionArtifactIssue = ""
	case ArtifactPreset:
		if session.ActivePresetRevision.Empty() {
			number := session.EffectivePresetRevisionSequence() + 1
			session.ActivePresetRevision = PresetRevision{
				Number: number, PresetObjectKey: objectKey, Status: PresetRevisionActive,
				StagedAt: now.UTC(), ActivatedAt: now.UTC(),
			}
			session.PresetRevisionSequence = number
		}
		session.PresetObjectKey = objectKey
		session.PresetArtifactStatus = ArtifactAccepted
		session.PresetArtifactIssue = ""
	default:
		return fmt.Errorf("unsupported artifact kind %q", kind)
	}

	session.Version++
	session.UpdatedAt = now.UTC()
	session.markReadyWhenComplete()
	return session.Validate()
}

// RejectArtifact records the latest validation outcome while leaving the
// draft editable and any independently accepted artifact intact.
func (session *Session) RejectArtifact(kind ArtifactKind, issue string, now time.Time) error {
	missionEditable := kind == ArtifactMission && session.ActiveWorkflowID == "" && session.LifecycleState != StateDeleting && session.LifecycleState != StateDeleted && session.LifecycleState != StateArchiving && session.LifecycleState != StateDestroying
	if session.LifecycleState != StateDraft && session.LifecycleState != StateNew && !missionEditable {
		return fmt.Errorf("%w: artifacts are only editable while a session is DRAFT", ErrInvalidTransition)
	}
	issue = sanitizeFailureDetail(issue)
	if utf8.RuneCountInString(issue) > 160 {
		issue = string([]rune(issue)[:160])
	}
	if issue == "" {
		issue = "The uploaded file did not pass validation."
	}
	switch kind {
	case ArtifactMission:
		session.MissionArtifactStatus, session.MissionArtifactIssue = ArtifactRejected, issue
	case ArtifactPreset:
		session.PresetArtifactStatus, session.PresetArtifactIssue = ArtifactRejected, issue
	default:
		return fmt.Errorf("unsupported artifact kind %q", kind)
	}
	if err := session.RecordMutation(now); err != nil {
		return err
	}
	session.markReadyWhenComplete()
	return session.Validate()
}

// PrepareCreationArtifacts records which uploads must finish before readiness.
func (session *Session) PrepareCreationArtifacts(hasPreset bool, now time.Time) error {
	return session.PrepareOptionalCreationArtifacts(true, hasPreset, now)
}

// PrepareOptionalCreationArtifacts records only the uploads supplied by the
// creation form. An omitted mission retains the built-in configured default.
func (session *Session) PrepareOptionalCreationArtifacts(hasMission, hasPreset bool, now time.Time) error {
	if session.LifecycleState != StateDraft && session.LifecycleState != StateNew {
		return fmt.Errorf("%w: artifacts are only editable while a session is DRAFT", ErrInvalidTransition)
	}
	if hasMission {
		session.DesiredState, session.ObservedState, session.LifecycleState = StateDraft, StateDraft, StateDraft
		session.MissionArtifactStatus, session.MissionArtifactIssue = ArtifactPending, ""
	} else {
		session.MissionArtifactStatus, session.MissionArtifactIssue = "", ""
	}
	if hasPreset {
		session.DesiredState, session.ObservedState, session.LifecycleState = StateDraft, StateDraft, StateDraft
		session.PresetArtifactStatus, session.PresetArtifactIssue = ArtifactPending, ""
	} else {
		session.PresetArtifactStatus, session.PresetArtifactIssue = "", ""
	}
	return session.RecordMutation(now)
}

// PrepareReplacementArtifacts marks only absent or rejected draft inputs as
// pending. Accepted and already-pending artifacts cannot be replaced through
// the setup recovery flow.
func (session *Session) PrepareReplacementArtifacts(mission, preset bool, now time.Time) error {
	if session.LifecycleState != StateDraft {
		return fmt.Errorf("%w: artifacts are only editable while a session is DRAFT", ErrInvalidTransition)
	}
	if !mission && !preset {
		return fmt.Errorf("at least one replacement artifact is required")
	}
	if mission {
		if session.MissionObjectKey != "" || (session.MissionArtifactStatus != "" && session.MissionArtifactStatus != ArtifactRejected) {
			return fmt.Errorf("%w: mission is not missing or rejected", ErrConflict)
		}
		session.MissionArtifactStatus, session.MissionArtifactIssue = ArtifactPending, ""
	}
	if preset {
		if session.PresetObjectKey != "" || (session.PresetArtifactStatus != "" && session.PresetArtifactStatus != ArtifactRejected) {
			return fmt.Errorf("%w: preset is not missing or rejected", ErrConflict)
		}
		session.PresetArtifactStatus, session.PresetArtifactIssue = ArtifactPending, ""
	}
	return session.RecordMutation(now)
}

// AcquireWorkflowLock applies an in-memory workflow lease. Durable adapters
// must additionally use a conditional write to prevent races.
func (session *Session) AcquireWorkflowLock(workflowID string, workflowType string, lease time.Duration, now time.Time) error {
	workflowID = strings.TrimSpace(workflowID)
	workflowType = strings.TrimSpace(workflowType)
	now = now.UTC()
	if workflowID == "" || workflowType == "" {
		return fmt.Errorf("workflow ID and type are required")
	}
	if lease <= 0 {
		return fmt.Errorf("workflow lease must be positive")
	}
	if session.ActiveWorkflowID != "" && session.ActiveWorkflowLeaseExpiresAt.After(now) {
		return fmt.Errorf("%w: session %s is locked by workflow %s", ErrWorkflowLocked, session.ID, session.ActiveWorkflowID)
	}
	session.ActiveWorkflowID = workflowID
	session.ActiveWorkflowType = workflowType
	session.ActiveWorkflowStartedAt = now
	session.ActiveWorkflowLeaseExpiresAt = now.Add(lease)
	if err := session.beginProgress(workflowID, workflowType, now); err != nil {
		return err
	}
	session.Version++
	session.UpdatedAt = now
	return session.Validate()
}

// ReleaseWorkflowLock releases the matching active workflow lease.
func (session *Session) ReleaseWorkflowLock(workflowID string, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) {
		return fmt.Errorf("%w: workflow %s does not hold the session lock", ErrConflict, workflowID)
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressFailed, now); err != nil {
		return err
	}
	session.ActiveWorkflowID = ""
	session.ActiveWorkflowType = ""
	session.ActiveWorkflowStartedAt = time.Time{}
	session.ActiveWorkflowLeaseExpiresAt = time.Time{}
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

// RecordMutation advances optimistic-concurrency metadata for an event that
// does not otherwise change the user-visible session fields.
func (session *Session) RecordMutation(now time.Time) error {
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

// SessionConfiguration contains the owner-controlled runtime policy for a session.
type SessionConfiguration struct {
	GameProfileID       string
	SleepAfterSeconds   int64
	ArchiveAfterSeconds int64
	TeamSpeakEnabled    bool
	Vanilla             bool
	CreatorDLCs         []string
	StartWhenReady      bool
}

// NewSessionInput contains the required information for a new draft session.
type NewSessionInput struct {
	ID                 string
	Slug               string
	DisplayName        string
	Description        string
	GameType           string
	OwnerDiscordUserID string
	GuildID            string
	ChannelID          string
}

// NewSession creates a valid draft session.
func NewSession(input NewSessionInput, now time.Time) (Session, error) {
	now = now.UTC()
	description, err := NormalizeSessionDescription(input.Description)
	if err != nil {
		return Session{}, err
	}

	session := Session{
		ID:                    strings.TrimSpace(input.ID),
		Slug:                  strings.TrimSpace(input.Slug),
		DisplayName:           strings.TrimSpace(input.DisplayName),
		Description:           description,
		GameType:              strings.ToLower(strings.TrimSpace(input.GameType)),
		OwnerDiscordUserID:    strings.TrimSpace(input.OwnerDiscordUserID),
		GuildID:               strings.TrimSpace(input.GuildID),
		ChannelID:             strings.TrimSpace(input.ChannelID),
		GameProfileID:         "arma3-default",
		SleepAfterSeconds:     1800,
		ArchiveAfterSeconds:   7 * 24 * 60 * 60,
		ConfigurationRevision: 0,
		ConfiguredMission:     DefaultMissionSelection(),

		DesiredState:   StateDraft,
		ObservedState:  StateDraft,
		LifecycleState: StateDraft,
		HealthStatus:   HealthUnknown,

		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := session.Validate(); err != nil {
		return Session{}, err
	}

	return session, nil
}

// GenerateSessionSlug derives a stable, URL-style lowercase slug from a
// display name. Existing explicit slugs continue to pass through NewSession
// unchanged; this helper is only for new creation flows that omit one.
func GenerateSessionSlug(displayName string) string {
	var builder strings.Builder
	separatorPending := false
	for _, character := range strings.ToLower(displayName) {
		isASCIIAlpha := character >= 'a' && character <= 'z'
		isASCIIDigit := character >= '0' && character <= '9'
		if isASCIIAlpha || isASCIIDigit {
			if separatorPending && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			separatorPending = false
			builder.WriteRune(character)
			continue
		}
		if builder.Len() > 0 {
			separatorPending = true
		}
	}

	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return "session"
	}
	if len(slug) > MaximumGeneratedSlugLength {
		slug = strings.TrimRight(slug[:MaximumGeneratedSlugLength], "-")
	}
	return slug
}

// Validate verifies the session's domain invariants.
func (session Session) Validate() error {
	creatorDLCs, creatorDLCErr := NormalizeCreatorDLCs(session.CreatorDLCs)
	if creatorDLCErr != nil {
		return creatorDLCErr
	}
	if !slices.Equal(creatorDLCs, session.CreatorDLCs) {
		return fmt.Errorf("Creator DLC selection must use canonical catalog order")
	}
	if err := session.Infrastructure.Validate(); err != nil {
		return err
	}
	if !session.Archive.Empty() {
		if err := session.Archive.Validate(); err != nil {
			return err
		}
	}
	if err := session.Progress.Validate(); err != nil {
		return err
	}
	if err := session.Failure.Validate(); err != nil {
		return err
	}
	if err := session.ActivePresetRevision.Validate(); err != nil {
		return fmt.Errorf("active preset revision: %w", err)
	}
	if err := session.PendingPresetRevision.Validate(); err != nil {
		return fmt.Errorf("pending preset revision: %w", err)
	}
	if err := session.ConfiguredMission.Validate(); err != nil {
		return fmt.Errorf("configured mission: %w", err)
	}
	if session.CurrentMission.Template != "" {
		if err := session.CurrentMission.Validate(); err != nil {
			return fmt.Errorf("current mission: %w", err)
		}
	}
	switch {
	case session.ID == "":
		return fmt.Errorf("session ID is required")
	case session.Slug == "":
		return fmt.Errorf("session slug is required")
	case !slugPattern.MatchString(session.Slug):
		return fmt.Errorf(
			"session slug %q must contain lowercase letters, numbers, and single hyphens",
			session.Slug,
		)
	case session.DisplayName == "":
		return fmt.Errorf("session display name is required")
	case utf8.RuneCountInString(session.Description) > MaximumSessionDescriptionRunes:
		return fmt.Errorf("session description must contain at most %d characters", MaximumSessionDescriptionRunes)
	case session.Description != normalizeSessionDescription(session.Description):
		return fmt.Errorf("session description must be normalized")
	case session.GameType == "":
		return fmt.Errorf("game type is required")
	case session.OwnerDiscordUserID == "":
		return fmt.Errorf("owner Discord user ID is required")
	case session.GuildID == "":
		return fmt.Errorf("Discord guild ID is required")
	case session.ChannelID == "":
		return fmt.Errorf("Discord channel ID is required")
	case !session.MissionArtifactStatus.Valid():
		return fmt.Errorf("invalid mission artifact status %q", session.MissionArtifactStatus)
	case !session.PresetArtifactStatus.Valid():
		return fmt.Errorf("invalid preset artifact status %q", session.PresetArtifactStatus)
	case session.MissionArtifactStatus != ArtifactRejected && session.MissionArtifactIssue != "":
		return fmt.Errorf("mission artifact issue requires rejected status")
	case session.PresetArtifactStatus != ArtifactRejected && session.PresetArtifactIssue != "":
		return fmt.Errorf("preset artifact issue requires rejected status")
	case strings.TrimSpace(session.GameProfileID) == "":
		return fmt.Errorf("game profile ID is required")
	case session.SleepAfterSeconds < 600:
		return fmt.Errorf("sleep policy must be at least 600 seconds")
	case session.ArchiveAfterSeconds < 86400:
		return fmt.Errorf("archive policy must be at least 86400 seconds")
	case session.ConfigurationRevision < 0:
		return fmt.Errorf("configuration revision cannot be negative")
	case session.Vanilla && len(session.CreatorDLCs) != 0:
		return fmt.Errorf("vanilla session cannot load Creator DLC")
	case session.ServerConfigRevision < 0 || (session.ServerConfigObjectKey == "" && session.ServerConfigSHA256 != "") || (session.ServerConfigObjectKey != "" && (session.ServerConfigRevision < 1 || len(session.ServerConfigSHA256) != 64)):
		return fmt.Errorf("server configuration snapshot is invalid")
	case session.PresetRevisionSequence < 0:
		return fmt.Errorf("preset revision sequence cannot be negative")
	case !session.ActivePresetRevision.Empty() && session.ActivePresetRevision.Status != PresetRevisionActive:
		return fmt.Errorf("active preset revision must have ACTIVE status")
	case !session.ActivePresetRevision.Empty() && session.PresetObjectKey != session.ActivePresetRevision.PresetObjectKey:
		return fmt.Errorf("legacy preset object key must mirror the active preset revision")
	case !session.ActivePresetRevision.Empty() && session.ActivePresetRevision.Number > session.PresetRevisionSequence:
		return fmt.Errorf("active preset revision exceeds the session revision sequence")
	case !session.PendingPresetRevision.Empty() && session.PendingPresetRevision.Status == PresetRevisionActive:
		return fmt.Errorf("pending preset revision cannot have ACTIVE status")
	case !session.PendingPresetRevision.Empty() && session.PendingPresetRevision.Number > session.PresetRevisionSequence:
		return fmt.Errorf("pending preset revision exceeds the session revision sequence")
	case !session.PendingPresetRevision.Empty() && session.PendingPresetRevision.BaseRevision != session.ActivePresetRevision.Number:
		return fmt.Errorf("pending preset revision must bind to the active revision")
	case !session.PendingPresetRevision.Empty() && session.PendingPresetRevision.Number <= session.ActivePresetRevision.Number:
		return fmt.Errorf("pending preset revision must follow the active revision")
	case session.ActiveWorkflowID == "" && (session.ActiveWorkflowType != "" || !session.ActiveWorkflowStartedAt.IsZero() || !session.ActiveWorkflowLeaseExpiresAt.IsZero()):
		return fmt.Errorf("workflow lock fields require an active workflow ID")
	case session.ActiveWorkflowID != "" && session.ActiveWorkflowType == "":
		return fmt.Errorf("active workflow type is required")
	case session.ActiveWorkflowID != "" && session.ActiveWorkflowStartedAt.IsZero():
		return fmt.Errorf("active workflow start timestamp is required")
	case session.ActiveWorkflowID != "" && !session.ActiveWorkflowLeaseExpiresAt.After(session.ActiveWorkflowStartedAt):
		return fmt.Errorf("active workflow lease must expire after it starts")
	case session.ActiveWorkflowID != "" && !session.Progress.Empty() && session.Progress.WorkflowID != session.ActiveWorkflowID:
		return fmt.Errorf("active workflow and progress workflow IDs must match")
	case session.ActiveWorkflowID != "" && !session.Progress.Empty() && session.Progress.WorkflowType != session.ActiveWorkflowType:
		return fmt.Errorf("active workflow and progress workflow types must match")
	case session.ActiveWorkflowID != "" && !session.Progress.Empty() && !session.Progress.StartedAt.Equal(session.ActiveWorkflowStartedAt):
		return fmt.Errorf("active workflow and progress start timestamps must match")
	case session.ArchiveSourceState != "" && (session.ActiveWorkflowType != ArchiveWorkflowType || session.LifecycleState != StateArchiving):
		return fmt.Errorf("archive source state requires an active archive workflow")
	case !session.DesiredState.Valid():
		return fmt.Errorf("invalid desired state %q", session.DesiredState)
	case !session.ObservedState.Valid():
		return fmt.Errorf("invalid observed state %q", session.ObservedState)
	case !session.LifecycleState.Valid():
		return fmt.Errorf("invalid lifecycle state %q", session.LifecycleState)
	case !session.HealthStatus.Valid():
		return fmt.Errorf("invalid health status %q", session.HealthStatus)
	case session.MonitoringCommandID == "" && !session.MonitoringStartedAt.IsZero():
		return fmt.Errorf("monitoring start timestamp requires a command ID")
	case session.MonitoringCommandID != "" && session.MonitoringStartedAt.IsZero():
		return fmt.Errorf("monitoring command requires a start timestamp")
	case session.PlayerCountKnown && session.PlayerCountObservedAt.IsZero():
		return fmt.Errorf("known player count requires an observation timestamp")
	case !session.PlayerCountKnown && session.PlayerCount != 0:
		return fmt.Errorf("unknown player count cannot retain a count")
	case session.PlayerCount < 0 || session.PlayerCount > 255:
		return fmt.Errorf("player count must be between 0 and 255")
	case !session.IdleSince.IsZero() && (!session.PlayerCountKnown || session.PlayerCount != 0):
		return fmt.Errorf("idle timestamp requires a known zero-player observation")
	case !session.IdleSince.IsZero() && session.PlayerCountObservedAt.Before(session.IdleSince):
		return fmt.Errorf("idle timestamp cannot follow the player observation")
	case session.Version < 1:
		return fmt.Errorf("session version must be at least 1")
	case session.CreatedAt.IsZero():
		return fmt.Errorf("created timestamp is required")
	case session.UpdatedAt.IsZero():
		return fmt.Errorf("updated timestamp is required")
	case session.UpdatedAt.Before(session.CreatedAt):
		return fmt.Errorf("updated timestamp cannot precede created timestamp")
	case !session.Progress.Empty() && session.Progress.StartedAt.Before(session.CreatedAt):
		return fmt.Errorf("progress start timestamp cannot precede session creation")
	case !session.Progress.Empty() && session.Progress.LastProgressAt.Before(session.CreatedAt):
		return fmt.Errorf("progress timestamp cannot precede session creation")
	case !session.Progress.Empty() && session.Progress.LastProgressAt.After(session.UpdatedAt):
		return fmt.Errorf("progress timestamp cannot follow session update")
	case !session.Failure.Empty() && session.Failure.FailedAt.After(session.UpdatedAt):
		return fmt.Errorf("failure timestamp cannot follow session update")
	default:
		return nil
	}
}

// SetFailure replaces the currently visible sanitized failure without
// advancing session version metadata. Workflow lifecycle methods own the
// surrounding atomic mutation and event.
func (session *Session) SetFailure(failure FailureRecord) error {
	if failure.Empty() {
		return fmt.Errorf("failure record is required")
	}
	if err := failure.Validate(); err != nil {
		return err
	}
	session.Failure = failure
	return nil
}

// ClearFailure removes only the active presentation projection. Immutable
// workflow and session events retain prior failure audit history.
func (session *Session) ClearFailure() {
	session.Failure = FailureRecord{}
}

// NormalizeSessionDescription makes an optional user description safe for
// durable single-line storage while preserving ordinary Unicode text.
func NormalizeSessionDescription(value string) (string, error) {
	normalized := normalizeSessionDescription(value)
	if utf8.RuneCountInString(normalized) > MaximumSessionDescriptionRunes {
		return "", fmt.Errorf("session description must contain at most %d characters", MaximumSessionDescriptionRunes)
	}
	return normalized, nil
}

func normalizeSessionDescription(value string) string {
	var builder strings.Builder
	spacePending := false
	for _, character := range value {
		switch {
		case unicode.IsSpace(character):
			if builder.Len() > 0 {
				spacePending = true
			}
		case unicode.IsControl(character), unicode.Is(unicode.Cf, character):
			continue
		default:
			if spacePending {
				builder.WriteByte(' ')
				spacePending = false
			}
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

// SetDescription replaces the optional normalized description and records a
// metadata mutation. Deleted session tombstones are immutable.
func (session *Session) SetDescription(description string, now time.Time) (string, error) {
	if session.LifecycleState == StateDeleted {
		return "", fmt.Errorf("%w: a deleted session description cannot be changed", ErrInvalidTransition)
	}
	normalized, err := NormalizeSessionDescription(description)
	if err != nil {
		return "", err
	}
	previous := session.Description
	updated := *session
	updated.Description = normalized
	if err := updated.RecordMutation(now); err != nil {
		return "", err
	}
	*session = updated
	return previous, nil
}

// UpdateCreatorDLCs records the desired Creator DLC set without claiming a
// running process changed in place. Lifecycle workers apply it at the next safe
// start boundary.
func (session *Session) UpdateCreatorDLCs(values []string, preparePreset bool, now time.Time) error {
	if session.Vanilla {
		return fmt.Errorf("%w: vanilla sessions cannot load Creator DLC", ErrInvalidTransition)
	}
	if session.ActiveWorkflowID != "" {
		return fmt.Errorf("%w: wait for the active lifecycle operation before changing mods", ErrWorkflowLocked)
	}
	switch session.LifecycleState {
	case StateDeleting, StateDeleted, StateArchiving, StateDestroying, StateRestoring, StateWaking, StateStopping:
		return fmt.Errorf("%w: mods cannot be changed in lifecycle state %s", ErrInvalidTransition, session.LifecycleState)
	}
	normalized, err := NormalizeCreatorDLCs(values)
	if err != nil {
		return err
	}
	session.CreatorDLCs = normalized
	if preparePreset {
		if session.LifecycleState != StateDraft || !session.ActivePresetRevision.Empty() {
			return fmt.Errorf("%w: initial preset preparation requires a draft without an active preset", ErrInvalidTransition)
		}
		session.PresetArtifactStatus, session.PresetArtifactIssue = ArtifactPending, ""
	}
	session.ConfigurationRevision++
	session.markReadyWhenComplete()
	return session.RecordMutation(now)
}

// BeginMonitoring records a short, read-only Systems Manager probe. Monitoring
// never changes the session lifecycle and may only run for a live server.
func (session *Session) BeginMonitoring(commandID string, now time.Time) error {
	if session.LifecycleState != StateRunning || session.Infrastructure.InstanceID == "" {
		return fmt.Errorf("%w: monitoring requires a running managed instance", ErrInvalidTransition)
	}
	if session.MonitoringCommandID != "" {
		return fmt.Errorf("%w: monitoring command is already pending", ErrConflict)
	}
	commandID = strings.TrimSpace(commandID)
	if commandID == "" {
		return fmt.Errorf("monitoring command ID is required")
	}
	session.MonitoringCommandID, session.MonitoringStartedAt = commandID, now.UTC()
	return session.RecordMutation(now)
}

// CompleteMonitoring records the latest classified health result and clears
// the durable pending probe marker.
func (session *Session) CompleteMonitoring(health HealthStatus, activity PlayerActivityObservation, now time.Time) (HealthStatus, error) {
	if session.MonitoringCommandID == "" {
		return HealthUnknown, fmt.Errorf("%w: no monitoring command is pending", ErrConflict)
	}
	if !health.Valid() {
		return HealthUnknown, fmt.Errorf("invalid health status %q", health)
	}
	previous := session.HealthStatus
	session.MonitoringCommandID, session.MonitoringStartedAt = "", time.Time{}
	session.HealthStatus = health
	if err := session.RecordPlayerActivity(activity); err != nil {
		return HealthUnknown, err
	}
	return previous, session.RecordMutation(now)
}

// Configure replaces the current validated configuration while the session is a draft.
func (session *Session) Configure(configuration SessionConfiguration, now time.Time) error {
	if session.LifecycleState != StateDraft {
		return fmt.Errorf(
			"%w: configuration is only editable while a session is DRAFT",
			ErrInvalidTransition,
		)
	}
	if err := session.applyConfiguration(configuration); err != nil {
		return err
	}
	session.Version++
	session.UpdatedAt = now.UTC()
	session.markReadyWhenComplete()

	return session.Validate()
}

// ConfigureDraftSetup atomically changes draft identity and configuration so
// persistence adapters observe one optimistic-concurrency version increment.
func (session *Session) ConfigureDraftSetup(displayName, description string, configuration SessionConfiguration, replaceMission, replacePreset bool, now time.Time) error {
	if session.LifecycleState != StateDraft {
		return fmt.Errorf("%w: setup is only editable while a session is DRAFT", ErrInvalidTransition)
	}
	displayName = normalizeSessionDescription(displayName)
	if count := utf8.RuneCountInString(displayName); count < 1 || count > 100 {
		return fmt.Errorf("display name must contain 1 to 100 characters")
	}
	normalizedDescription, err := NormalizeSessionDescription(description)
	if err != nil {
		return err
	}
	if replaceMission {
		if session.MissionObjectKey != "" || (session.MissionArtifactStatus != "" && session.MissionArtifactStatus != ArtifactRejected) {
			return fmt.Errorf("%w: mission is not missing or rejected", ErrConflict)
		}
	}
	if replacePreset {
		if session.PresetObjectKey != "" || (session.PresetArtifactStatus != "" && session.PresetArtifactStatus != ArtifactRejected) {
			return fmt.Errorf("%w: preset is not missing or rejected", ErrConflict)
		}
	}
	if err := session.applyConfiguration(configuration); err != nil {
		return err
	}
	if replaceMission {
		session.MissionArtifactStatus, session.MissionArtifactIssue = ArtifactPending, ""
	}
	if replacePreset {
		session.PresetArtifactStatus, session.PresetArtifactIssue = ArtifactPending, ""
	}
	session.DisplayName = displayName
	session.Description = normalizedDescription
	session.Version++
	session.UpdatedAt = now.UTC()
	session.markReadyWhenComplete()
	return session.Validate()
}

func (session *Session) applyConfiguration(configuration SessionConfiguration) error {

	configuration.GameProfileID = strings.ToLower(strings.TrimSpace(configuration.GameProfileID))
	if configuration.GameProfileID != "arma3-default" {
		return fmt.Errorf("unsupported game profile %q", configuration.GameProfileID)
	}
	if configuration.SleepAfterSeconds < 600 || configuration.SleepAfterSeconds > 86400 {
		return fmt.Errorf("sleep policy must be between 600 and 86400 seconds")
	}
	if configuration.ArchiveAfterSeconds < 86400 || configuration.ArchiveAfterSeconds > 90*86400 {
		return fmt.Errorf("archive policy must be between 1 and 90 days")
	}
	creatorDLCs, err := NormalizeCreatorDLCs(configuration.CreatorDLCs)
	if err != nil {
		return err
	}
	if configuration.Vanilla && len(creatorDLCs) != 0 {
		return fmt.Errorf("vanilla session cannot load Creator DLC")
	}

	session.GameProfileID = configuration.GameProfileID
	session.SleepAfterSeconds = configuration.SleepAfterSeconds
	session.ArchiveAfterSeconds = configuration.ArchiveAfterSeconds
	session.TeamSpeakEnabled = configuration.TeamSpeakEnabled
	session.Vanilla = configuration.Vanilla
	session.CreatorDLCs = creatorDLCs
	session.StartWhenReady = configuration.StartWhenReady
	session.ConfigurationRevision++
	return nil
}

func (session *Session) markReadyWhenComplete() {
	if session.LifecycleState == StateDraft && session.ConfigurationRevision > 0 &&
		(session.ConfiguredMission.IsDefault() || artifactAccepted(session.MissionArtifactStatus, session.MissionObjectKey)) &&
		((session.Vanilla && session.PresetArtifactStatus != ArtifactPending) ||
			artifactAccepted(session.PresetArtifactStatus, session.PresetObjectKey) ||
			(!session.Vanilla && len(session.CreatorDLCs) > 0 && session.PresetArtifactStatus == "")) {
		session.DesiredState = StateNew
		session.ObservedState = StateNew
		session.LifecycleState = StateNew
	}
}

func artifactAccepted(status ArtifactStatus, objectKey string) bool {
	return status == ArtifactAccepted || (status == "" && strings.TrimSpace(objectKey) != "")
}

// Transition performs a synchronous lifecycle transition.
//
// Later workflow phases will update desired and observed state separately.
// For the metadata vertical slice, this method advances all three together.
func (session *Session) Transition(
	to LifecycleState,
	now time.Time,
) error {
	from := session.LifecycleState

	if !from.CanTransition(to) {
		return fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition,
			from,
			to,
		)
	}

	session.DesiredState = to
	session.ObservedState = to
	session.LifecycleState = to
	session.Version++
	session.UpdatedAt = now.UTC()

	return session.Validate()
}
