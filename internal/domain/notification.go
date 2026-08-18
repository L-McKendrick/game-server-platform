package domain

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type NotificationKind string

type SessionCardReference struct {
	SessionID               string
	ChannelID               string
	MessageID               string
	DeliveredRevision       int64
	DeliveredNotificationID string
	ContentSHA256           string
}

const MaximumNotificationAttachmentBytes int64 = 1024 * 1024

var notificationFilenamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}-modlist\.html$`)

// SessionModlistReference is replaceable Discord delivery metadata. The S3
// object remains authoritative so a deleted Discord message can be recreated.
type SessionModlistReference struct {
	SessionID               string
	ChannelID               string
	MessageID               string
	ObjectKey               string
	Filename                string
	DeliveredRevision       int64
	DeliveredNotificationID string
	ContentSHA256           string
}

func (reference SessionModlistReference) Validate() error {
	switch {
	case strings.TrimSpace(reference.SessionID) == "":
		return fmt.Errorf("modlist session ID is required")
	case strings.TrimSpace(reference.ChannelID) == "":
		return fmt.Errorf("modlist channel ID is required")
	case strings.TrimSpace(reference.MessageID) == "":
		return fmt.Errorf("modlist message ID is required")
	case !validManagedObjectKey(reference.ObjectKey):
		return fmt.Errorf("modlist object key is invalid")
	case !notificationFilenamePattern.MatchString(strings.TrimSpace(reference.Filename)):
		return fmt.Errorf("modlist filename is invalid")
	case reference.DeliveredRevision < 1:
		return fmt.Errorf("modlist delivered revision must be positive")
	case strings.TrimSpace(reference.DeliveredNotificationID) == "":
		return fmt.Errorf("modlist delivered notification ID is required")
	case !validHexSHA256(reference.ContentSHA256):
		return fmt.Errorf("modlist content SHA-256 is invalid")
	default:
		return nil
	}
}

func (reference SessionCardReference) Validate() error {
	switch {
	case strings.TrimSpace(reference.SessionID) == "":
		return fmt.Errorf("card session ID is required")
	case strings.TrimSpace(reference.ChannelID) == "":
		return fmt.Errorf("card channel ID is required")
	case strings.TrimSpace(reference.MessageID) == "":
		return fmt.Errorf("card message ID is required")
	case reference.DeliveredRevision < 0:
		return fmt.Errorf("card delivered revision cannot be negative")
	default:
		return nil
	}
}

const (
	NotificationMessage        NotificationKind = ""
	NotificationSessionCard    NotificationKind = "SESSION_CARD"
	NotificationSessionModlist NotificationKind = "SESSION_MODLIST"
)

type NotificationAttachment struct {
	ObjectKey   string `json:"object_key"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
	Revision    int64  `json:"revision"`
}

// NotificationEmbed is the bounded transport-neutral rich-card projection used
// by adapters that support embeds. Content remains the mandatory plain-text
// fallback for destinations without rich-card capability.
type NotificationEmbed struct {
	Title       string                   `json:"title"`
	Description string                   `json:"description"`
	Color       int                      `json:"color"`
	Fields      []NotificationEmbedField `json:"fields,omitempty"`
}

type NotificationEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

func (embed NotificationEmbed) Validate() error {
	if utf8.RuneCountInString(embed.Title) < 1 || utf8.RuneCountInString(embed.Title) > 256 {
		return fmt.Errorf("notification embed title must contain 1 to 256 characters")
	}
	if utf8.RuneCountInString(embed.Description) > 4096 {
		return fmt.Errorf("notification embed description exceeds 4096 characters")
	}
	if embed.Color < 0 || embed.Color > 0xFFFFFF {
		return fmt.Errorf("notification embed color is invalid")
	}
	if len(embed.Fields) > 25 {
		return fmt.Errorf("notification embed exceeds 25 fields")
	}
	total := utf8.RuneCountInString(embed.Title) + utf8.RuneCountInString(embed.Description)
	for _, field := range embed.Fields {
		if utf8.RuneCountInString(field.Name) < 1 || utf8.RuneCountInString(field.Name) > 256 {
			return fmt.Errorf("notification embed field name must contain 1 to 256 characters")
		}
		if utf8.RuneCountInString(field.Value) < 1 || utf8.RuneCountInString(field.Value) > 1024 {
			return fmt.Errorf("notification embed field value must contain 1 to 1024 characters")
		}
		total += utf8.RuneCountInString(field.Name) + utf8.RuneCountInString(field.Value)
	}
	if total > 6000 {
		return fmt.Errorf("notification embed exceeds 6000 characters")
	}
	return nil
}

func (attachment NotificationAttachment) Validate() error {
	switch {
	case !validManagedObjectKey(attachment.ObjectKey):
		return fmt.Errorf("notification attachment object key is invalid")
	case !notificationFilenamePattern.MatchString(strings.TrimSpace(attachment.Filename)):
		return fmt.Errorf("notification attachment filename is invalid")
	case strings.TrimSpace(attachment.ContentType) != "text/html; charset=utf-8":
		return fmt.Errorf("notification attachment content type is invalid")
	case !validHexSHA256(attachment.SHA256):
		return fmt.Errorf("notification attachment SHA-256 is invalid")
	case attachment.SizeBytes <= 0 || attachment.SizeBytes > MaximumNotificationAttachmentBytes:
		return fmt.Errorf("notification attachment size is invalid")
	case attachment.Revision < 1:
		return fmt.Errorf("notification attachment revision must be positive")
	default:
		return nil
	}
}

type NotificationRequest struct {
	SchemaVersion  int                     `json:"schema_version"`
	NotificationID string                  `json:"notification_id"`
	SessionID      string                  `json:"session_id"`
	GuildID        string                  `json:"guild_id"`
	ChannelID      string                  `json:"channel_id"`
	Content        string                  `json:"content"`
	Kind           NotificationKind        `json:"kind,omitempty"`
	CardRevision   int64                   `json:"card_revision,omitempty"`
	Embed          *NotificationEmbed      `json:"embed,omitempty"`
	Attachment     *NotificationAttachment `json:"attachment,omitempty"`
	CorrelationID  string                  `json:"correlation_id"`
	RequestedAt    time.Time               `json:"requested_at"`
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
	case request.Kind != NotificationMessage && request.Kind != NotificationSessionCard && request.Kind != NotificationSessionModlist:
		return fmt.Errorf("unsupported notification kind %q", request.Kind)
	case request.CardRevision < 0:
		return fmt.Errorf("card revision cannot be negative")
	case request.Kind != NotificationSessionCard && request.CardRevision != 0:
		return fmt.Errorf("card revision is only valid for session-card notifications")
	case request.Kind != NotificationSessionCard && request.Embed != nil:
		return fmt.Errorf("notification embed is only valid for session-card notifications")
	case request.Kind == NotificationSessionModlist && request.Attachment == nil:
		return fmt.Errorf("session-modlist notification attachment is required")
	case request.Kind != NotificationSessionModlist && request.Attachment != nil:
		return fmt.Errorf("notification attachment is only valid for session-modlist notifications")
	case strings.TrimSpace(request.CorrelationID) == "":
		return fmt.Errorf("notification correlation ID is required")
	case request.RequestedAt.IsZero():
		return fmt.Errorf("notification request timestamp is required")
	default:
		if request.Embed != nil {
			if err := request.Embed.Validate(); err != nil {
				return err
			}
		}
		if request.Attachment != nil {
			return request.Attachment.Validate()
		}
		return nil
	}
}

func validHexSHA256(value string) bool {
	digest, err := hex.DecodeString(strings.TrimSpace(value))
	return err == nil && len(digest) == 32
}
