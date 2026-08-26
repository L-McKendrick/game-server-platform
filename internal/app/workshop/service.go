package workshop

import (
	"context"
	"fmt"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type Clock interface{ Now() time.Time }

type Service struct {
	catalog ports.WorkshopCatalog
	clock   Clock
}

func New(catalog ports.WorkshopCatalog, clock Clock) (*Service, error) {
	if catalog == nil || clock == nil {
		return nil, fmt.Errorf("Workshop service dependencies are required")
	}
	return &Service{catalog: catalog, clock: clock}, nil
}

func (service *Service) Resolve(ctx context.Context, request domain.WorkshopSourceRequest) (domain.WorkshopResolution, error) {
	if err := request.Validate(); err != nil {
		return domain.WorkshopResolution{}, fmt.Errorf("validate Workshop request: %w", err)
	}
	reference, _ := domain.ParseWorkshopURL(request.SourceURL)
	root, err := service.catalog.Item(ctx, reference.PublishedFileID)
	if err != nil {
		return domain.WorkshopResolution{}, fmt.Errorf("resolve Workshop item metadata: %w", err)
	}
	resolution := domain.WorkshopResolution{SchemaVersion: 1, Target: request.Target, Source: reference}
	if root.Collection {
		resolution.SourceKind = domain.WorkshopSourceCollection
		children, childErr := service.catalog.CollectionChildren(ctx, reference.PublishedFileID)
		if childErr != nil {
			return domain.WorkshopResolution{}, fmt.Errorf("resolve Workshop collection: %w", childErr)
		}
		if len(children) == 0 || len(children) > domain.MaximumWorkshopChildren {
			return domain.WorkshopResolution{}, fmt.Errorf("Workshop collection must contain between 1 and %d children", domain.MaximumWorkshopChildren)
		}
		seen := make(map[uint64]struct{}, len(children))
		for _, childID := range children {
			if childID == 0 {
				return domain.WorkshopResolution{}, fmt.Errorf("Workshop collection contains an invalid child ID")
			}
			if _, duplicate := seen[childID]; duplicate {
				continue
			}
			seen[childID] = struct{}{}
			item, itemErr := service.catalog.Item(ctx, childID)
			if itemErr != nil {
				return domain.WorkshopResolution{}, fmt.Errorf("resolve Workshop child %d: %w", childID, itemErr)
			}
			resolution.Items = append(resolution.Items, domain.ClassifyWorkshopItem(item, request.Target))
		}
	} else {
		resolution.SourceKind = domain.WorkshopSourceItem
		resolution.Items = []domain.WorkshopItem{domain.ClassifyWorkshopItem(root, request.Target)}
	}
	if err := resolution.Finalize(service.clock.Now()); err != nil {
		return domain.WorkshopResolution{}, err
	}
	return resolution, nil
}
