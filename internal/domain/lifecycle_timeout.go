package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	DefaultSleepAfterSeconds   int64 = 30 * 60
	MinimumSleepAfterSeconds   int64 = 10 * 60
	MaximumSleepAfterSeconds   int64 = 24 * 60 * 60
	DefaultArchiveAfterSeconds int64 = 7 * 24 * 60 * 60
	MinimumArchiveAfterSeconds int64 = 24 * 60 * 60
	MaximumArchiveAfterSeconds int64 = 90 * 24 * 60 * 60
)

// GuildLifecycleTimeoutPolicy supplies the values copied into future sessions.
// Existing sessions retain their snapshot until an administrator extends it.
type GuildLifecycleTimeoutPolicy struct {
	GuildID             string
	SleepAfterSeconds   int64
	ArchiveAfterSeconds int64
	Version             int64
	UpdatedBy           string
	UpdatedAt           time.Time
}

// LifecycleTimeoutPolicyAudit is the immutable reason and value record for a
// guild default change.
type LifecycleTimeoutPolicyAudit struct {
	ID, GuildID, ActorID, CorrelationID, Reason string
	PreviousSleepAfterSeconds                   int64
	PreviousArchiveAfterSeconds                 int64
	SleepAfterSeconds                           int64
	ArchiveAfterSeconds                         int64
	OccurredAt                                  time.Time
}

func (audit LifecycleTimeoutPolicyAudit) Validate() error {
	if strings.TrimSpace(audit.ID) == "" || strings.TrimSpace(audit.GuildID) == "" || strings.TrimSpace(audit.ActorID) == "" || strings.TrimSpace(audit.CorrelationID) == "" || strings.TrimSpace(audit.Reason) == "" || len([]rune(audit.Reason)) > 200 || audit.OccurredAt.IsZero() {
		return fmt.Errorf("lifecycle timeout policy audit metadata is invalid")
	}
	if err := ValidateLifecycleTimeouts(audit.SleepAfterSeconds, audit.ArchiveAfterSeconds); err != nil {
		return err
	}
	return ValidateLifecycleTimeouts(audit.PreviousSleepAfterSeconds, audit.PreviousArchiveAfterSeconds)
}

func DefaultGuildLifecycleTimeoutPolicy(guildID string) GuildLifecycleTimeoutPolicy {
	return GuildLifecycleTimeoutPolicy{GuildID: strings.TrimSpace(guildID), SleepAfterSeconds: DefaultSleepAfterSeconds, ArchiveAfterSeconds: DefaultArchiveAfterSeconds}
}

func ValidateLifecycleTimeouts(sleepAfterSeconds, archiveAfterSeconds int64) error {
	switch {
	case sleepAfterSeconds < MinimumSleepAfterSeconds || sleepAfterSeconds > MaximumSleepAfterSeconds:
		return fmt.Errorf("sleep timeout must be between 10 minutes and 24 hours")
	case archiveAfterSeconds < MinimumArchiveAfterSeconds || archiveAfterSeconds > MaximumArchiveAfterSeconds:
		return fmt.Errorf("archive timeout must be between 1 and 90 days")
	default:
		return nil
	}
}

func (policy GuildLifecycleTimeoutPolicy) Validate() error {
	if strings.TrimSpace(policy.GuildID) == "" {
		return fmt.Errorf("guild ID is required")
	}
	if err := ValidateLifecycleTimeouts(policy.SleepAfterSeconds, policy.ArchiveAfterSeconds); err != nil {
		return err
	}
	if policy.Version == 0 {
		if policy.UpdatedBy != "" || !policy.UpdatedAt.IsZero() {
			return fmt.Errorf("default lifecycle timeout policy cannot contain update metadata")
		}
		return nil
	}
	if policy.Version < 0 || strings.TrimSpace(policy.UpdatedBy) == "" || policy.UpdatedAt.IsZero() {
		return fmt.Errorf("persisted lifecycle timeout policy metadata is invalid")
	}
	return nil
}
