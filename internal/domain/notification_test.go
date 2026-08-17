package domain

import (
	"strings"
	"testing"
	"time"
)

func TestNotificationRequestCardRevisionValidation(t *testing.T) {
	t.Parallel()
	base := NotificationRequest{
		SchemaVersion: 1, NotificationID: "notification-1", SessionID: "session-1",
		GuildID: "guild-1", ChannelID: "channel-1", Content: "card",
		CorrelationID: "correlation-1", RequestedAt: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
	}

	card := base
	card.Kind = NotificationSessionCard
	card.CardRevision = 3
	if err := card.Validate(); err != nil {
		t.Fatalf("valid card notification error = %v", err)
	}

	legacyCard := card
	legacyCard.CardRevision = 0
	if err := legacyCard.Validate(); err != nil {
		t.Fatalf("legacy card notification error = %v", err)
	}

	nonCard := base
	nonCard.CardRevision = 1
	if err := nonCard.Validate(); err == nil || !strings.Contains(err.Error(), "only valid") {
		t.Fatalf("non-card revision error = %v", err)
	}

	card.CardRevision = -1
	if err := card.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be negative") {
		t.Fatalf("negative card revision error = %v", err)
	}
}

func TestSessionCardReferenceAllowsLegacyMetadataAndRejectsNegativeRevision(t *testing.T) {
	t.Parallel()
	reference := SessionCardReference{SessionID: "session-1", ChannelID: "channel-1", MessageID: "message-1"}
	if err := reference.Validate(); err != nil {
		t.Fatalf("legacy reference error = %v", err)
	}
	reference.DeliveredRevision = -1
	if err := reference.Validate(); err == nil {
		t.Fatal("negative delivered revision returned nil error")
	}
}

func TestSessionModlistNotificationAndReferenceValidation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	digest := strings.Repeat("a", 64)
	attachment := &NotificationAttachment{
		ObjectKey: "sessions/session-1/input/modlists/digest/saturday-arma-modlist.html",
		Filename:  "saturday-arma-modlist.html", ContentType: "text/html; charset=utf-8",
		SHA256: digest, SizeBytes: 512, Revision: 3,
	}
	request := NotificationRequest{
		SchemaVersion: 1, NotificationID: "modlist-1", SessionID: "session-1",
		GuildID: "guild-1", ChannelID: "channel-1", Content: "Active modlist",
		Kind: NotificationSessionModlist, Attachment: attachment,
		CorrelationID: "correlation-1", RequestedAt: now,
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid modlist notification error = %v", err)
	}
	reference := SessionModlistReference{
		SessionID: "session-1", ChannelID: "channel-1", MessageID: "message-1",
		ObjectKey: attachment.ObjectKey, Filename: attachment.Filename,
		DeliveredRevision: 3, DeliveredNotificationID: request.NotificationID, ContentSHA256: digest,
	}
	if err := reference.Validate(); err != nil {
		t.Fatalf("valid modlist reference error = %v", err)
	}

	withoutAttachment := request
	withoutAttachment.Attachment = nil
	if err := withoutAttachment.Validate(); err == nil || !strings.Contains(err.Error(), "attachment is required") {
		t.Fatalf("missing attachment error = %v", err)
	}
	wrongKind := request
	wrongKind.Kind = NotificationSessionCard
	if err := wrongKind.Validate(); err == nil || !strings.Contains(err.Error(), "only valid") {
		t.Fatalf("wrong-kind attachment error = %v", err)
	}
	oversized := request
	copyAttachment := *attachment
	copyAttachment.SizeBytes = MaximumNotificationAttachmentBytes + 1
	oversized.Attachment = &copyAttachment
	if err := oversized.Validate(); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("oversized attachment error = %v", err)
	}
	unsafeName := request
	copyAttachment = *attachment
	copyAttachment.Filename = "../../preset.html"
	unsafeName.Attachment = &copyAttachment
	if err := unsafeName.Validate(); err == nil || !strings.Contains(err.Error(), "filename") {
		t.Fatalf("unsafe filename error = %v", err)
	}
}
