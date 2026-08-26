package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

// ArtifactQueue retains requests for local development and tests.
type ArtifactQueue struct {
	mu       sync.Mutex
	requests []domain.ArtifactIngestRequest
	workshop []domain.WorkshopSourceRequest
}

var _ ports.ArtifactQueue = (*ArtifactQueue)(nil)
var _ ports.WorkshopQueue = (*ArtifactQueue)(nil)

func NewArtifactQueue() *ArtifactQueue { return &ArtifactQueue{} }

func (queue *ArtifactQueue) Enqueue(ctx context.Context, request domain.ArtifactIngestRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := request.Validate(); err != nil {
		return fmt.Errorf("validate artifact request: %w", err)
	}

	queue.mu.Lock()
	defer queue.mu.Unlock()
	for _, existing := range queue.requests {
		if existing.IdempotencyKey == request.IdempotencyKey {
			return nil
		}
	}
	queue.requests = append(queue.requests, request)
	return nil
}

func (queue *ArtifactQueue) EnqueueWorkshop(ctx context.Context, request domain.WorkshopSourceRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := request.Validate(); err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	for _, existing := range queue.workshop {
		if existing.IdempotencyKey == request.IdempotencyKey {
			return nil
		}
	}
	queue.workshop = append(queue.workshop, request)
	return nil
}

func (queue *ArtifactQueue) WorkshopRequests() []domain.WorkshopSourceRequest {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return append([]domain.WorkshopSourceRequest(nil), queue.workshop...)
}

func (queue *ArtifactQueue) Requests() []domain.ArtifactIngestRequest {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return append([]domain.ArtifactIngestRequest(nil), queue.requests...)
}
