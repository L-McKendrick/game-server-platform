package sqsnotification

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

var _ ports.NotificationQueue = (*Queue)(nil)

func New(client API, queueURL string) *Queue {
	return &Queue{client: client, url: strings.TrimSpace(queueURL)}
}

func (queue *Queue) Enqueue(ctx context.Context, request domain.NotificationRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if queue == nil || queue.client == nil || queue.url == "" {
		return fmt.Errorf("notification queue is not configured")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}
	_, err = queue.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl: aws.String(queue.url), MessageBody: aws.String(string(body)),
		MessageGroupId: aws.String(request.ChannelID), MessageDeduplicationId: aws.String(request.NotificationID),
	})
	if err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	return nil
}
