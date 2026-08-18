package sqsdlq

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type API interface {
	GetQueueAttributes(context.Context, *sqs.GetQueueAttributesInput, ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error)
	StartMessageMoveTask(context.Context, *sqs.StartMessageMoveTaskInput, ...func(*sqs.Options)) (*sqs.StartMessageMoveTaskOutput, error)
}

type Endpoint struct{ DLQURL, DLQARN, DestinationARN string }

type Manager struct {
	client    API
	endpoints map[domain.DeadLetterQueue]Endpoint
}

var _ ports.DeadLetterManager = (*Manager)(nil)

func New(client API, endpoints map[domain.DeadLetterQueue]Endpoint) (*Manager, error) {
	if client == nil {
		return nil, fmt.Errorf("SQS client is required")
	}
	copyEndpoints := make(map[domain.DeadLetterQueue]Endpoint, len(endpoints))
	for queue, endpoint := range endpoints {
		if !queue.Valid() || strings.TrimSpace(endpoint.DLQURL) == "" || strings.TrimSpace(endpoint.DLQARN) == "" || strings.TrimSpace(endpoint.DestinationARN) == "" {
			return nil, fmt.Errorf("complete dead-letter endpoints are required for %s", queue)
		}
		copyEndpoints[queue] = endpoint
	}
	return &Manager{client: client, endpoints: copyEndpoints}, nil
}

func (manager *Manager) Inspect(ctx context.Context, queue domain.DeadLetterQueue) (domain.DeadLetterInspection, string, error) {
	endpoint, ok := manager.endpoints[queue]
	if !ok {
		return domain.DeadLetterInspection{}, "", fmt.Errorf("dead-letter queue %s is not configured", queue)
	}
	output, err := manager.client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{QueueUrl: aws.String(endpoint.DLQURL), AttributeNames: []types.QueueAttributeName{
		types.QueueAttributeNameApproximateNumberOfMessages, types.QueueAttributeNameApproximateNumberOfMessagesNotVisible, types.QueueAttributeNameApproximateNumberOfMessagesDelayed,
	}})
	if err != nil {
		return domain.DeadLetterInspection{}, "", fmt.Errorf("inspect dead-letter queue: %w", err)
	}
	parse := func(name types.QueueAttributeName) (int64, error) {
		value := output.Attributes[string(name)]
		if value == "" {
			return 0, nil
		}
		return strconv.ParseInt(value, 10, 64)
	}
	visible, err := parse(types.QueueAttributeNameApproximateNumberOfMessages)
	if err != nil {
		return domain.DeadLetterInspection{}, "", err
	}
	inFlight, err := parse(types.QueueAttributeNameApproximateNumberOfMessagesNotVisible)
	if err != nil {
		return domain.DeadLetterInspection{}, "", err
	}
	delayed, err := parse(types.QueueAttributeNameApproximateNumberOfMessagesDelayed)
	if err != nil {
		return domain.DeadLetterInspection{}, "", err
	}
	return domain.DeadLetterInspection{Visible: visible, InFlight: inFlight, Delayed: delayed}, endpoint.DLQARN, nil
}

func (manager *Manager) StartRedrive(ctx context.Context, queue domain.DeadLetterQueue, maxMessagesPerSecond int32) (string, string, error) {
	endpoint, ok := manager.endpoints[queue]
	if !ok {
		return "", "", fmt.Errorf("dead-letter queue %s is not configured", queue)
	}
	if maxMessagesPerSecond < 1 || maxMessagesPerSecond > 500 {
		return "", "", fmt.Errorf("redrive rate must be between 1 and 500 messages per second")
	}
	_, err := manager.client.StartMessageMoveTask(ctx, &sqs.StartMessageMoveTaskInput{SourceArn: aws.String(endpoint.DLQARN), DestinationArn: aws.String(endpoint.DestinationARN), MaxNumberOfMessagesPerSecond: aws.Int32(maxMessagesPerSecond)})
	if err != nil {
		return "", "", fmt.Errorf("start dead-letter redrive: %w", err)
	}
	return endpoint.DLQARN, endpoint.DestinationARN, nil
}
