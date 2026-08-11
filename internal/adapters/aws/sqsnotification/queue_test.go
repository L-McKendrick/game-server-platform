package sqsnotification

import (
	"context"
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
		GuildID: "guild-1", ChannelID: "channel-1", Content: "Session is ready.",
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
}
