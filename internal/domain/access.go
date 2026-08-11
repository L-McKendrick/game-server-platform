package domain

import (
	"fmt"
	"strings"
	"time"
)

type GuildAccessPolicy struct {
	GuildID           string
	AllowedRoleIDs    []string
	AllowedChannelIDs []string
	Version           int64
	UpdatedBy         string
	UpdatedAt         time.Time
}

func (policy GuildAccessPolicy) Validate() error {
	switch {
	case strings.TrimSpace(policy.GuildID) == "":
		return fmt.Errorf("guild ID is required")
	case len(policy.AllowedRoleIDs) == 0:
		return fmt.Errorf("at least one allowed role ID is required")
	case policy.Version < 1:
		return fmt.Errorf("access policy version must be positive")
	case strings.TrimSpace(policy.UpdatedBy) == "":
		return fmt.Errorf("access policy updater is required")
	case policy.UpdatedAt.IsZero():
		return fmt.Errorf("access policy updated timestamp is required")
	default:
		return nil
	}
}
