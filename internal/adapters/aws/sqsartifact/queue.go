package sqsartifact

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type API interface {
	SendMessage(context.Context, *sqs.SendMessageInput, ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

type Queue struct {
	client   API
	queueURL string
}

var _ ports.ArtifactQueue = (*Queue)(nil)
var _ ports.WorkshopQueue = (*Queue)(nil)

func New(client API, queueURL string) *Queue {
	return &Queue{client: client, queueURL: strings.TrimSpace(queueURL)}
}

func (queue *Queue) EnqueueWorkshop(ctx context.Context, request domain.WorkshopSourceRequest) error {
	if queue == nil || queue.client == nil || queue.queueURL == "" {
		return fmt.Errorf("SQS Workshop queue is not configured")
	}
	if err := request.Validate(); err != nil {
		return fmt.Errorf("validate Workshop request: %w", err)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal Workshop request: %w", err)
	}
	_, err = queue.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl: aws.String(queue.queueURL), MessageBody: aws.String(string(body)),
		MessageGroupId: aws.String(request.SessionID), MessageDeduplicationId: aws.String(request.IdempotencyKey),
	})
	if err != nil {
		return fmt.Errorf("send Workshop request: %w", err)
	}
	return nil
}

func (queue *Queue) Enqueue(ctx context.Context, request domain.ArtifactIngestRequest) error {
	if queue == nil || queue.client == nil {
		return fmt.Errorf("SQS artifact queue client is required")
	}
	if queue.queueURL == "" {
		return fmt.Errorf("SQS artifact queue URL is required")
	}
	if err := request.Validate(); err != nil {
		return fmt.Errorf("validate artifact request: %w", err)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal artifact request: %w", err)
	}
	messageGroupID := request.SessionID
	if request.IsServerConfig() {
		messageGroupID = "guild-config:" + request.GuildID
	}
	_, err = queue.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               aws.String(queue.queueURL),
		MessageBody:            aws.String(string(body)),
		MessageGroupId:         aws.String(messageGroupID),
		MessageDeduplicationId: aws.String(request.IdempotencyKey),
	})
	if err != nil {
		return fmt.Errorf("send artifact request: %w", err)
	}
	return nil
}
