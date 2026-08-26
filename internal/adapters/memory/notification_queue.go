package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type NotificationQueue struct {
	mu       sync.Mutex
	requests []domain.NotificationRequest
}

var _ ports.NotificationQueue = (*NotificationQueue)(nil)

func NewNotificationQueue() *NotificationQueue { return &NotificationQueue{} }

func (queue *NotificationQueue) Enqueue(ctx context.Context, request domain.NotificationRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := request.Validate(); err != nil {
		return fmt.Errorf("validate notification: %w", err)
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	for _, existing := range queue.requests {
		if existing.NotificationID == request.NotificationID {
			if !sameNotificationPayload(existing, request) {
				return fmt.Errorf("%w: notification %s", domain.ErrIdempotencyConflict, request.NotificationID)
			}
			return nil
		}
	}
	queue.requests = append(queue.requests, cloneNotificationRequest(request))
	return nil
}

func sameNotificationPayload(left domain.NotificationRequest, right domain.NotificationRequest) bool {
	return left.SchemaVersion == right.SchemaVersion &&
		left.NotificationID == right.NotificationID &&
		left.SessionID == right.SessionID &&
		left.GuildID == right.GuildID &&
		left.ChannelID == right.ChannelID &&
		left.Content == right.Content &&
		left.Kind == right.Kind &&
		left.CardRevision == right.CardRevision && left.SuppressCardControls == right.SuppressCardControls &&
		sameNotificationAttachment(left.Attachment, right.Attachment)
}

func sameNotificationAttachment(left, right *domain.NotificationAttachment) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (queue *NotificationQueue) Requests() []domain.NotificationRequest {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	requests := make([]domain.NotificationRequest, len(queue.requests))
	for index, request := range queue.requests {
		requests[index] = cloneNotificationRequest(request)
	}
	return requests
}

func cloneNotificationRequest(request domain.NotificationRequest) domain.NotificationRequest {
	if request.Attachment != nil {
		attachment := *request.Attachment
		request.Attachment = &attachment
	}
	return request
}
