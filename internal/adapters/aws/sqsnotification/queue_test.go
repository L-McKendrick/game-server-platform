package sqsnotification

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type fakeSQS struct{ input *sqs.SendMessageInput }

func (fake *fakeSQS) SendMessage(_ context.Context, input *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	fake.input = input
	return &sqs.SendMessageOutput{}, nil
}

func TestQueueOrdersByChannelAndDeduplicatesByNotificationID(t *testing.T) {
	t.Parallel()

	client := &fakeSQS{}
	queue := New(client, "https://sqs.us-west-2.amazonaws.com/123/notifications.fifo")
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "notification-1", SessionID: "session-1",
		GuildID: "guild-1", ChannelID: "channel-1", Content: "Session is ready.", Kind: domain.NotificationSessionCard, CardRevision: 7,
		CorrelationID: "correlation-1", RequestedAt: time.Date(2026, 8, 8, 23, 0, 0, 0, time.UTC),
	}
	if err := queue.Enqueue(context.Background(), request); err != nil {
		t.Fatalf("Enqueue() returned error: %v", err)
	}
	if client.input == nil || *client.input.MessageGroupId != "channel-1" {
		t.Fatalf("MessageGroupId was not set from the channel")
	}
	if *client.input.MessageDeduplicationId != "notification-1" {
		t.Fatalf("MessageDeduplicationId = %q", *client.input.MessageDeduplicationId)
	}
	var queued domain.NotificationRequest
	if err := json.Unmarshal([]byte(*client.input.MessageBody), &queued); err != nil {
		t.Fatal(err)
	}
	if queued.CardRevision != 7 || queued.Kind != domain.NotificationSessionCard {
		t.Fatalf("queued notification = %#v", queued)
	}
}

func TestQueuePreservesModlistAttachmentMetadata(t *testing.T) {
	t.Parallel()
	client := &fakeSQS{}
	queue := New(client, "https://sqs.us-west-2.amazonaws.com/123/notifications.fifo")
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "modlist-1", SessionID: "session-1",
		GuildID: "guild-1", ChannelID: "channel-1", Content: "Active modlist", Kind: domain.NotificationSessionModlist,
		Attachment: &domain.NotificationAttachment{
			ObjectKey: "sessions/session-1/input/modlists/digest/saturday-arma-modlist.html",
			Filename:  "saturday-arma-modlist.html", ContentType: "text/html; charset=utf-8",
			SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SizeBytes: 512, Revision: 4,
		},
		CorrelationID: "correlation-1", RequestedAt: time.Date(2026, 8, 16, 13, 0, 0, 0, time.UTC),
	}
	if err := queue.Enqueue(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	var queued domain.NotificationRequest
	if err := json.Unmarshal([]byte(*client.input.MessageBody), &queued); err != nil {
		t.Fatal(err)
	}
	if queued.Attachment == nil || *queued.Attachment != *request.Attachment {
		t.Fatalf("queued attachment = %#v; want %#v", queued.Attachment, request.Attachment)
	}
}
