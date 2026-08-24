package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	AutomaticSleepAfter             = 30 * time.Minute
	AutomaticArchiveAfter           = 72 * time.Hour
	MaximumActivityEvidenceAge      = 10 * time.Minute
	InactivityMonitorActorID        = "inactivity-monitor"
	AutomaticIdleSinceParameter     = "automatic_idle_since"
	AutomaticSleepingSinceParameter = "automatic_sleeping_since"
)

// PlayerActivityObservation is a bounded point-in-time player count. Known is
// false when the authoritative query is missing, stale, malformed, or failed;
// callers must never translate those conditions into an empty server.
type PlayerActivityObservation struct {
	Known       bool
	PlayerCount int
	ObservedAt  time.Time
}

func (session Session) AutomaticArchiveDue(now time.Time) bool {
	now = now.UTC()
	return session.LifecycleState == StateSleeping && session.ActiveWorkflowID == "" &&
		session.Infrastructure.InstanceID != "" && session.Infrastructure.DataVolumeID != "" &&
		!session.SleepingSince.IsZero() && !session.SleepingSince.After(now) && now.Sub(session.SleepingSince) >= AutomaticArchiveAfter
}

func AutomaticArchiveCommandID(sessionID string, sleepingSince time.Time) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(sessionID) + "\x00" + sleepingSince.UTC().Format(time.RFC3339Nano)))
	return "auto-archive-" + hex.EncodeToString(sum[:])[:22]
}

func ValidateAutomaticArchiveCommand(command CommandEnvelope, session Session, now time.Time) error {
	if !command.Actor.System || command.Actor.DiscordUserID != InactivityMonitorActorID || command.CommandType != CommandArchiveSession {
		return ErrForbidden
	}
	sleepingSince, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(command.Parameters[AutomaticSleepingSinceParameter]))
	if err != nil || !sleepingSince.Equal(session.SleepingSince) || command.CommandID != AutomaticArchiveCommandID(session.ID, sleepingSince) ||
		command.IdempotencyKey != "automatic-archive:"+command.CommandID || command.CorrelationID != command.CommandID {
		return ErrIdempotencyConflict
	}
	if !session.AutomaticArchiveDue(now) {
		return fmt.Errorf("automatic archive is no longer due: %w", ErrInvalidTransition)
	}
	return nil
}

func (session Session) AutomaticSleepDue(now time.Time) bool {
	now = now.UTC()
	return session.LifecycleState == StateRunning && session.ActiveWorkflowID == "" &&
		session.PlayerCountKnown && session.PlayerCount == 0 && !session.IdleSince.IsZero() &&
		!session.PlayerCountObservedAt.IsZero() && !session.PlayerCountObservedAt.After(now) &&
		now.Sub(session.PlayerCountObservedAt) <= MaximumActivityEvidenceAge &&
		now.Sub(session.IdleSince) >= AutomaticSleepAfter
}

func AutomaticSleepCommandID(sessionID string, idleSince time.Time) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(sessionID) + "\x00" + idleSince.UTC().Format(time.RFC3339Nano)))
	return "auto-sleep-" + hex.EncodeToString(sum[:])[:24]
}

func ValidateAutomaticSleepCommand(command CommandEnvelope, session Session, now time.Time) error {
	if !command.Actor.System || command.Actor.DiscordUserID != InactivityMonitorActorID || command.CommandType != CommandSleepSession {
		return ErrForbidden
	}
	idleSince, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(command.Parameters[AutomaticIdleSinceParameter]))
	if err != nil || !idleSince.Equal(session.IdleSince) || command.CommandID != AutomaticSleepCommandID(session.ID, idleSince) ||
		command.IdempotencyKey != "automatic-sleep:"+command.CommandID || command.CorrelationID != command.CommandID {
		return ErrIdempotencyConflict
	}
	if !session.AutomaticSleepDue(now) {
		return fmt.Errorf("automatic sleep is no longer due: %w", ErrInvalidTransition)
	}
	return nil
}

// RecordPlayerActivity updates the durable evidence used by inactivity policy.
// Any unknown observation breaks zero-player continuity. A positive count also
// clears the idle window; a known zero begins or continues it.
func (session *Session) RecordPlayerActivity(observation PlayerActivityObservation) error {
	if observation.ObservedAt.IsZero() {
		return fmt.Errorf("player activity observation timestamp is required")
	}
	observedAt := observation.ObservedAt.UTC()
	if !session.PlayerCountObservedAt.IsZero() && observedAt.Before(session.PlayerCountObservedAt) {
		return fmt.Errorf("%w: player activity observation is older than persisted evidence", ErrConflict)
	}
	if observation.Known && (observation.PlayerCount < 0 || observation.PlayerCount > 255) {
		return fmt.Errorf("player count must be between 0 and 255")
	}

	if !observation.Known {
		session.PlayerCountKnown = false
		session.PlayerCount = 0
		session.PlayerCountObservedAt = observedAt
		session.IdleSince = time.Time{}
		return nil
	}

	wasContinuousZero := session.PlayerCountKnown && session.PlayerCount == 0 && !session.IdleSince.IsZero()
	session.PlayerCountKnown = true
	session.PlayerCount = observation.PlayerCount
	session.PlayerCountObservedAt = observedAt
	if observation.PlayerCount > 0 {
		session.IdleSince = time.Time{}
	} else if !wasContinuousZero {
		session.IdleSince = observedAt
	}
	return nil
}
