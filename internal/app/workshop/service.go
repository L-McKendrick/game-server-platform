package workshop

import (
	"context"
	"errors"
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
	resolution := domain.WorkshopResolution{SchemaVersion: 1, Target: request.Target, Source: reference}
	children, collectionErr := service.catalog.CollectionChildren(ctx, reference.PublishedFileID)
	if collectionErr == nil {
		resolution.SourceKind = domain.WorkshopSourceCollection
		if len(children) == 0 || len(children) > domain.MaximumWorkshopCollectionChildren {
			if len(children) > domain.MaximumWorkshopCollectionChildren {
				return domain.WorkshopResolution{}, domain.WorkshopMetadataError{Code: domain.WorkshopMetadataCollectionLimit, Detail: fmt.Sprintf("Workshop collection contains %d direct children; maximum is %d", len(children), domain.MaximumWorkshopCollectionChildren)}
			}
			return domain.WorkshopResolution{}, fmt.Errorf("Workshop collection must contain at least one child")
		}
		seen := make(map[uint64]struct{}, len(children))
		uniqueChildren := make([]uint64, 0, len(children))
		for _, child := range children {
			childID := child.PublishedFileID
			if childID == 0 {
				return domain.WorkshopResolution{}, fmt.Errorf("Workshop collection contains an invalid child ID")
			}
			if _, duplicate := seen[childID]; duplicate {
				continue
			}
			seen[childID] = struct{}{}
			if child.Collection {
				resolution.Items = append(resolution.Items, domain.WorkshopItem{PublishedFileID: childID, Available: true, Collection: true, Class: domain.WorkshopItemNestedCollection, Issue: "nested collections are not supported"})
				continue
			}
			uniqueChildren = append(uniqueChildren, childID)
		}
		if len(uniqueChildren) > 0 {
			items, itemErr := service.catalog.Items(ctx, uniqueChildren)
			if itemErr != nil {
				return domain.WorkshopResolution{}, fmt.Errorf("resolve Workshop collection children: %w", itemErr)
			}
			if len(items) != len(uniqueChildren) {
				return domain.WorkshopResolution{}, domain.WorkshopMetadataError{Code: domain.WorkshopMetadataInvalidResponse, Retryable: true, Detail: "Steam returned incomplete collection metadata"}
			}
			for _, item := range items {
				resolution.Items = append(resolution.Items, domain.ClassifyWorkshopItem(item, request.Target))
			}
		}
	} else if errors.Is(collectionErr, domain.ErrWorkshopNotCollection) {
		root, err := service.catalog.Item(ctx, reference.PublishedFileID)
		if err != nil {
			return domain.WorkshopResolution{}, fmt.Errorf("resolve Workshop item metadata: %w", err)
		}
		resolution.SourceKind = domain.WorkshopSourceItem
		resolution.Items = []domain.WorkshopItem{domain.ClassifyWorkshopItem(root, request.Target)}
	} else {
		return domain.WorkshopResolution{}, fmt.Errorf("resolve Workshop collection metadata: %w", collectionErr)
	}
	if err := resolution.Finalize(service.clock.Now()); err != nil {
		return domain.WorkshopResolution{}, err
	}
	return resolution, nil
}
