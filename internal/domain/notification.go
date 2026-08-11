package domain

import (
	"fmt"
	"strings"
	"time"
)

type NotificationRequest struct {
	SchemaVersion  int       `json:"schema_version"`
	NotificationID string    `json:"notification_id"`
	SessionID      string    `json:"session_id"`
	GuildID        string    `json:"guild_id"`
	ChannelID      string    `json:"channel_id"`
	Content        string    `json:"content"`
	CorrelationID  string    `json:"correlation_id"`
	RequestedAt    time.Time `json:"requested_at"`
}

func (request NotificationRequest) Validate() error {
	switch {
	case request.SchemaVersion != 1:
		return fmt.Errorf("unsupported notification schema version %d", request.SchemaVersion)
	case strings.TrimSpace(request.NotificationID) == "":
		return fmt.Errorf("notification ID is required")
	case strings.TrimSpace(request.SessionID) == "":
		return fmt.Errorf("notification session ID is required")
	case strings.TrimSpace(request.GuildID) == "":
		return fmt.Errorf("notification guild ID is required")
	case strings.TrimSpace(request.ChannelID) == "":
		return fmt.Errorf("notification channel ID is required")
	case strings.TrimSpace(request.Content) == "" || len(request.Content) > 1900:
		return fmt.Errorf("notification content must contain 1 to 1900 characters")
	case strings.TrimSpace(request.CorrelationID) == "":
		return fmt.Errorf("notification correlation ID is required")
	case request.RequestedAt.IsZero():
		return fmt.Errorf("notification request timestamp is required")
	default:
		return nil
	}
}
