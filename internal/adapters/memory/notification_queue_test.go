package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestNotificationQueueKeepsIdempotencyKeyPayloadImmutable(t *testing.T) {
	t.Parallel()
	queue := NewNotificationQueue()
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "card-1", SessionID: "session-1",
		GuildID: "guild-1", ChannelID: "channel-1", Content: "card",
		Kind: domain.NotificationSessionCard, CardRevision: 2,
		CorrelationID: "correlation-1", RequestedAt: time.Date(2026, 8, 15, 2, 0, 0, 0, time.UTC),
	}
	if err := queue.Enqueue(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	replay := request
	replay.RequestedAt = replay.RequestedAt.Add(time.Minute)
	if err := queue.Enqueue(context.Background(), replay); err != nil {
		t.Fatalf("equivalent replay error = %v", err)
	}
	conflict := request
	conflict.Content = "different card"
	if err := queue.Enqueue(context.Background(), conflict); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay error = %v; want ErrIdempotencyConflict", err)
	}
	if requests := queue.Requests(); len(requests) != 1 || requests[0].Content != "card" {
		t.Fatalf("queued requests = %#v", requests)
	}
}

func TestNotificationQueueRejectsChangedAttachmentForSameDeliveryKey(t *testing.T) {
	t.Parallel()
	queue := NewNotificationQueue()
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "modlist-1", SessionID: "session-1",
		GuildID: "guild-1", ChannelID: "channel-1", Content: "modlist",
		Kind: domain.NotificationSessionModlist,
		Attachment: &domain.NotificationAttachment{
			ObjectKey: "sessions/session-1/input/modlists/digest/session-modlist.html",
			Filename:  "session-modlist.html", ContentType: "text/html; charset=utf-8",
			SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 12, Revision: 1,
		},
		CorrelationID: "correlation-1", RequestedAt: time.Date(2026, 8, 15, 2, 0, 0, 0, time.UTC),
	}
	if err := queue.Enqueue(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	changed := request
	copy := *request.Attachment
	copy.SHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	changed.Attachment = &copy
	if err := queue.Enqueue(context.Background(), changed); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("changed attachment error = %v; want ErrIdempotencyConflict", err)
	}
	request.Attachment.SHA256 = strings.Repeat("c", 64)
	returned := queue.Requests()
	if len(returned) != 1 || returned[0].Attachment == nil || returned[0].Attachment.SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("queued attachment changed through caller alias: %#v", returned)
	}
	returned[0].Attachment.SHA256 = strings.Repeat("d", 64)
	if reread := queue.Requests(); reread[0].Attachment.SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("queued attachment changed through returned alias: %#v", reread)
	}
}
