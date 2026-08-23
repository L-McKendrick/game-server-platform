package memory

import (
	"context"
	"sync"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type ResetQueue struct {
	mu       sync.Mutex
	Requests []domain.ResetRequest
}

func (queue *ResetQueue) Enqueue(ctx context.Context, request domain.ResetRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := request.Validate(); err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.Requests = append(queue.Requests, request)
	return nil
}
