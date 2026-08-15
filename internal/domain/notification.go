package domain

import (
	"fmt"
	"strings"
	"time"
)

type NotificationKind string

type SessionCardReference struct {
	SessionID string
	ChannelID string
	MessageID string
}

func (reference SessionCardReference) Validate() error {
	switch {
	case strings.TrimSpace(reference.SessionID) == "":
		return fmt.Errorf("card session ID is required")
	case strings.TrimSpace(reference.ChannelID) == "":
		return fmt.Errorf("card channel ID is required")
	case strings.TrimSpace(reference.MessageID) == "":
		return fmt.Errorf("card message ID is required")
	default:
		return nil
	}
}

const (
	NotificationMessage     NotificationKind = ""
	NotificationSessionCard NotificationKind = "SESSION_CARD"
)

type NotificationRequest struct {
	SchemaVersion  int              `json:"schema_version"`
	NotificationID string           `json:"notification_id"`
	SessionID      string           `json:"session_id"`
	GuildID        string           `json:"guild_id"`
	ChannelID      string           `json:"channel_id"`
	Content        string           `json:"content"`
	Kind           NotificationKind `json:"kind,omitempty"`
	CorrelationID  string           `json:"correlation_id"`
	RequestedAt    time.Time        `json:"requested_at"`
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
	case request.Kind != NotificationMessage && request.Kind != NotificationSessionCard:
		return fmt.Errorf("unsupported notification kind %q", request.Kind)
	case strings.TrimSpace(request.CorrelationID) == "":
		return fmt.Errorf("notification correlation ID is required")
	case request.RequestedAt.IsZero():
		return fmt.Errorf("notification request timestamp is required")
	default:
		return nil
	}
}
