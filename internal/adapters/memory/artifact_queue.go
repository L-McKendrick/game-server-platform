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
}

var _ ports.ArtifactQueue = (*ArtifactQueue)(nil)

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

func (queue *ArtifactQueue) Requests() []domain.ArtifactIngestRequest {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return append([]domain.ArtifactIngestRequest(nil), queue.requests...)
}
