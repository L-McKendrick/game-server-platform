package sqscommand

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

func TestQueueOrdersBySessionAndDeduplicatesByCommandID(t *testing.T) {
	t.Parallel()

	client := &fakeSQS{}
	queue := New(client, "https://sqs.us-west-2.amazonaws.com/123/commands.fifo")
	command := domain.CommandEnvelope{
		SchemaVersion: 1, CommandID: "command-1", CommandType: domain.CommandStartSession,
		RequestedAt: time.Date(2026, 8, 8, 23, 0, 0, 0, time.UTC),
		Actor:       domain.CommandActor{DiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"},
		SessionID:   "session-1", IdempotencyKey: "discord:command-1", CorrelationID: "correlation-1",
	}
	if err := queue.Enqueue(context.Background(), command); err != nil {
		t.Fatalf("Enqueue() returned error: %v", err)
	}
	if client.input == nil || *client.input.MessageGroupId != "session-1" {
		t.Fatalf("MessageGroupId was not set from the session")
	}
	if *client.input.MessageDeduplicationId != "command-1" {
		t.Fatalf("MessageDeduplicationId = %q", *client.input.MessageDeduplicationId)
	}
}
