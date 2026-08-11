package sqscommand

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

type API interface {
	SendMessage(context.Context, *sqs.SendMessageInput, ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

type Queue struct {
	client API
	url    string
}

var _ ports.CommandQueue = (*Queue)(nil)

func New(client API, queueURL string) *Queue {
	return &Queue{client: client, url: strings.TrimSpace(queueURL)}
}

func (queue *Queue) Enqueue(ctx context.Context, command domain.CommandEnvelope) error {
	if err := command.Validate(); err != nil {
		return err
	}
	if queue == nil || queue.client == nil || queue.url == "" {
		return fmt.Errorf("command queue is not configured")
	}
	body, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	_, err = queue.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl: aws.String(queue.url), MessageBody: aws.String(string(body)),
		MessageGroupId: aws.String(command.SessionID), MessageDeduplicationId: aws.String(command.CommandID),
	})
	if err != nil {
		return fmt.Errorf("send command: %w", err)
	}
	return nil
}
