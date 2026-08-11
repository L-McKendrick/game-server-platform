package sqsartifact

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

func TestQueueUsesSessionOrderingAndInteractionDeduplication(t *testing.T) {
	t.Parallel()

	client := &fakeSQS{}
	queue := New(client, "https://sqs.us-west-2.amazonaws.com/123/ingest.fifo")
	request := domain.ArtifactIngestRequest{
		SchemaVersion: 1, SessionID: "session-1", Kind: domain.ArtifactMission,
		AttachmentID: "attachment-1", Filename: "mission.pbo", SizeBytes: 1024,
		SourceURL: "https://cdn.discordapp.com/attachments/1/2/mission.pbo",
		ActorID:   "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
		CorrelationID: "correlation-1", IdempotencyKey: "discord:interaction-1",
		RequestedAt: time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
	}
	if err := queue.Enqueue(context.Background(), request); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if client.input == nil || *client.input.MessageGroupId != "session-1" {
		t.Fatalf("MessageGroupId was not set from the session")
	}
	if *client.input.MessageDeduplicationId != "discord:interaction-1" {
		t.Errorf("MessageDeduplicationId = %q", *client.input.MessageDeduplicationId)
	}
}
