package domain

import (
	"fmt"
	"time"
)

// PlayerActivityObservation is a bounded point-in-time player count. Known is
// false when the authoritative query is missing, stale, malformed, or failed;
// callers must never translate those conditions into an empty server.
type PlayerActivityObservation struct {
	Known       bool
	PlayerCount int
	ObservedAt  time.Time
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
