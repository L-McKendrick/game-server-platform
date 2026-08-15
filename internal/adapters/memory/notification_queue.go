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
	for index, existing := range queue.requests {
		if existing.NotificationID == request.NotificationID {
			queue.requests[index] = request
			return nil
		}
	}
	queue.requests = append(queue.requests, request)
	return nil
}

func (queue *NotificationQueue) Requests() []domain.NotificationRequest {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return append([]domain.NotificationRequest(nil), queue.requests...)
}
