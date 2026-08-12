package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Session represents the persistent platform identity of a game server.
type Session struct {
	ID                    string
	Slug                  string
	DisplayName           string
	GameType              string
	OwnerDiscordUserID    string
	GuildID               string
	ChannelID             string
	GameProfileID         string
	SleepAfterSeconds     int64
	ArchiveAfterSeconds   int64
	TeamSpeakEnabled      bool
	ConfigurationRevision int64
	MissionObjectKey      string
	PresetObjectKey       string
	Infrastructure        Infrastructure

	ActiveWorkflowID             string
	ActiveWorkflowType           string
	ActiveWorkflowStartedAt      time.Time
	ActiveWorkflowLeaseExpiresAt time.Time

	DesiredState        LifecycleState
	ObservedState       LifecycleState
	LifecycleState      LifecycleState
	HealthStatus        HealthStatus
	MonitoringCommandID string
	MonitoringStartedAt time.Time

	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AttachArtifact records a validated durable object and advances a complete
// draft to NEW without tying session identity to the source attachment URL.
func (session *Session) AttachArtifact(kind ArtifactKind, objectKey string, now time.Time) error {
	if session.LifecycleState != StateDraft {
		return fmt.Errorf("%w: artifacts are only editable while a session is DRAFT", ErrInvalidTransition)
	}
	objectKey = strings.TrimSpace(objectKey)
	if objectKey == "" {
		return fmt.Errorf("artifact object key is required")
	}
	switch kind {
	case ArtifactMission:
		session.MissionObjectKey = objectKey
	case ArtifactPreset:
		session.PresetObjectKey = objectKey
	default:
		return fmt.Errorf("unsupported artifact kind %q", kind)
	}

	session.Version++
	session.UpdatedAt = now.UTC()
	session.markReadyWhenComplete()
	return session.Validate()
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
	session.Version++
	session.UpdatedAt = now
	return session.Validate()
}

// ReleaseWorkflowLock releases the matching active workflow lease.
func (session *Session) ReleaseWorkflowLock(workflowID string, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) {
		return fmt.Errorf("%w: workflow %s does not hold the session lock", ErrConflict, workflowID)
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
}

// NewSessionInput contains the required information for a new draft session.
type NewSessionInput struct {
	ID                 string
	Slug               string
	DisplayName        string
	GameType           string
	OwnerDiscordUserID string
	GuildID            string
	ChannelID          string
}

// NewSession creates a valid draft session.
func NewSession(input NewSessionInput, now time.Time) (Session, error) {
	now = now.UTC()

	session := Session{
		ID:                    strings.TrimSpace(input.ID),
		Slug:                  strings.TrimSpace(input.Slug),
		DisplayName:           strings.TrimSpace(input.DisplayName),
		GameType:              strings.ToLower(strings.TrimSpace(input.GameType)),
		OwnerDiscordUserID:    strings.TrimSpace(input.OwnerDiscordUserID),
		GuildID:               strings.TrimSpace(input.GuildID),
		ChannelID:             strings.TrimSpace(input.ChannelID),
		GameProfileID:         "arma3-default",
		SleepAfterSeconds:     1800,
		ArchiveAfterSeconds:   7 * 24 * 60 * 60,
		ConfigurationRevision: 0,

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

// Validate verifies the session's domain invariants.
func (session Session) Validate() error {
	if err := session.Infrastructure.Validate(); err != nil {
		return err
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
	case session.GameType == "":
		return fmt.Errorf("game type is required")
	case session.OwnerDiscordUserID == "":
		return fmt.Errorf("owner Discord user ID is required")
	case session.GuildID == "":
		return fmt.Errorf("Discord guild ID is required")
	case session.ChannelID == "":
		return fmt.Errorf("Discord channel ID is required")
	case strings.TrimSpace(session.GameProfileID) == "":
		return fmt.Errorf("game profile ID is required")
	case session.SleepAfterSeconds < 600:
		return fmt.Errorf("sleep policy must be at least 600 seconds")
	case session.ArchiveAfterSeconds < 86400:
		return fmt.Errorf("archive policy must be at least 86400 seconds")
	case session.ConfigurationRevision < 0:
		return fmt.Errorf("configuration revision cannot be negative")
	case session.ActiveWorkflowID == "" && (session.ActiveWorkflowType != "" || !session.ActiveWorkflowStartedAt.IsZero() || !session.ActiveWorkflowLeaseExpiresAt.IsZero()):
		return fmt.Errorf("workflow lock fields require an active workflow ID")
	case session.ActiveWorkflowID != "" && session.ActiveWorkflowType == "":
		return fmt.Errorf("active workflow type is required")
	case session.ActiveWorkflowID != "" && session.ActiveWorkflowStartedAt.IsZero():
		return fmt.Errorf("active workflow start timestamp is required")
	case session.ActiveWorkflowID != "" && !session.ActiveWorkflowLeaseExpiresAt.After(session.ActiveWorkflowStartedAt):
		return fmt.Errorf("active workflow lease must expire after it starts")
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
	case session.Version < 1:
		return fmt.Errorf("session version must be at least 1")
	case session.CreatedAt.IsZero():
		return fmt.Errorf("created timestamp is required")
	case session.UpdatedAt.IsZero():
		return fmt.Errorf("updated timestamp is required")
	case session.UpdatedAt.Before(session.CreatedAt):
		return fmt.Errorf("updated timestamp cannot precede created timestamp")
	default:
		return nil
	}
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
func (session *Session) CompleteMonitoring(health HealthStatus, now time.Time) (HealthStatus, error) {
	if session.MonitoringCommandID == "" {
		return HealthUnknown, fmt.Errorf("%w: no monitoring command is pending", ErrConflict)
	}
	if !health.Valid() {
		return HealthUnknown, fmt.Errorf("invalid health status %q", health)
	}
	previous := session.HealthStatus
	session.MonitoringCommandID, session.MonitoringStartedAt = "", time.Time{}
	session.HealthStatus = health
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

	session.GameProfileID = configuration.GameProfileID
	session.SleepAfterSeconds = configuration.SleepAfterSeconds
	session.ArchiveAfterSeconds = configuration.ArchiveAfterSeconds
	session.TeamSpeakEnabled = configuration.TeamSpeakEnabled
	session.ConfigurationRevision++
	session.Version++
	session.UpdatedAt = now.UTC()
	session.markReadyWhenComplete()

	return session.Validate()
}

func (session *Session) markReadyWhenComplete() {
	if session.LifecycleState == StateDraft && session.ConfigurationRevision > 0 &&
		session.MissionObjectKey != "" && session.PresetObjectKey != "" {
		session.DesiredState = StateNew
		session.ObservedState = StateNew
		session.LifecycleState = StateNew
	}
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
