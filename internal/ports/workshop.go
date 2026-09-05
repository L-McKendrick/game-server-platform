package ports

import (
	"context"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// WorkshopCatalog resolves public Steam Workshop metadata. Implementations do
// not download content or mutate subscriptions.
type WorkshopCatalog interface {
	Item(ctx context.Context, publishedFileID uint64) (domain.WorkshopItem, error)
	Items(ctx context.Context, publishedFileIDs []uint64) ([]domain.WorkshopItem, error)
	CollectionChildren(ctx context.Context, publishedFileID uint64) ([]domain.WorkshopCollectionChild, error)
}

type WorkshopQueue interface {
	EnqueueWorkshop(ctx context.Context, request domain.WorkshopSourceRequest) error
}
