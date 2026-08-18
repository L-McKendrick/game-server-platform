package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

var _ ports.OrphanRepository = (*SessionRepository)(nil)

func (repository *SessionRepository) ListSessionsForInventory(ctx context.Context, limit int32) ([]domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, fmt.Errorf("limit must be positive")
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	result := make([]domain.Session, 0, len(repository.sessions))
	for _, session := range repository.sessions {
		result = append(result, session)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	if len(result) > int(limit) {
		result = result[:limit]
	}
	return result, nil
}

func (repository *SessionRepository) SaveOrphanFinding(ctx context.Context, finding domain.OrphanFinding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := finding.Validate(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if current, ok := repository.orphans[finding.ID]; ok && current.Resource.ID != finding.Resource.ID {
		return domain.ErrConflict
	}
	repository.orphans[finding.ID] = finding
	return nil
}

func (repository *SessionRepository) GetOrphanFinding(ctx context.Context, findingID string) (domain.OrphanFinding, error) {
	if err := ctx.Err(); err != nil {
		return domain.OrphanFinding{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	finding, ok := repository.orphans[strings.TrimSpace(findingID)]
	if !ok {
		return domain.OrphanFinding{}, domain.ErrNotFound
	}
	return finding, nil
}

func (repository *SessionRepository) ListOrphanFindings(ctx context.Context, limit int32) ([]domain.OrphanFinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, fmt.Errorf("limit must be positive")
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	result := make([]domain.OrphanFinding, 0, len(repository.orphans))
	for _, finding := range repository.orphans {
		result = append(result, finding)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DetectedAt.After(result[j].DetectedAt) })
	if len(result) > int(limit) {
		result = result[:limit]
	}
	return result, nil
}
