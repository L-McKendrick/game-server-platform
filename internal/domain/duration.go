package domain

import (
	"fmt"
	"time"
)

const (
	DefaultMaximumDurationSeconds int64 = 24 * 60 * 60
	MinimumMaximumDurationSeconds int64 = 60 * 60
	MaximumMaximumDurationSeconds int64 = 7 * 24 * 60 * 60
)

// MaximumDuration records the immutable start anchor and current deadline.
// A zero start/deadline is an unstarted legacy or draft session, not an
// immediately expired one. Future lifecycle code starts the clock only when
// resources are first committed, and never resets it on wake or restore.
type MaximumDuration struct {
	Seconds    int64
	StartedAt  time.Time
	DeadlineAt time.Time
}

func (policy MaximumDuration) EffectiveSeconds() int64 {
	if policy.Seconds == 0 {
		return DefaultMaximumDurationSeconds
	}
	return policy.Seconds
}

func (policy MaximumDuration) Validate() error {
	seconds := policy.EffectiveSeconds()
	if seconds < MinimumMaximumDurationSeconds || seconds > MaximumMaximumDurationSeconds {
		return fmt.Errorf("maximum session duration must be between 1 hour and 7 days")
	}
	if policy.StartedAt.IsZero() != policy.DeadlineAt.IsZero() {
		return fmt.Errorf("maximum session duration start and deadline must be set together")
	}
	if !policy.StartedAt.IsZero() && !policy.DeadlineAt.After(policy.StartedAt) {
		return fmt.Errorf("maximum session duration deadline must follow its start")
	}
	if !policy.StartedAt.IsZero() && policy.DeadlineAt.After(policy.StartedAt.Add(time.Duration(MaximumMaximumDurationSeconds)*time.Second)) {
		return fmt.Errorf("maximum session duration deadline exceeds the 7-day cap")
	}
	return nil
}

// Start anchors the wall-clock budget at the first accepted provisioning
// workflow. Retries, wake, and restore cannot restart it.
func (policy *MaximumDuration) Start(now time.Time) error {
	if !policy.StartedAt.IsZero() {
		return nil
	}
	policy.StartedAt = now.UTC()
	policy.DeadlineAt = policy.StartedAt.Add(time.Duration(policy.EffectiveSeconds()) * time.Second)
	return policy.Validate()
}

func (policy MaximumDuration) Expired(now time.Time) bool {
	return !policy.DeadlineAt.IsZero() && !now.UTC().Before(policy.DeadlineAt)
}

func (policy *MaximumDuration) Extend(deadline, now time.Time) error {
	deadline = deadline.UTC()
	if policy.StartedAt.IsZero() || !deadline.After(policy.DeadlineAt) || !deadline.After(now.UTC()) {
		return fmt.Errorf("extension must move an active deadline into the future")
	}
	previous := policy.DeadlineAt
	policy.DeadlineAt = deadline
	if err := policy.Validate(); err != nil {
		policy.DeadlineAt = previous
		return err
	}
	return nil
}
