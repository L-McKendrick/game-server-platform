package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type AccessPolicyRepository struct {
	mu       sync.RWMutex
	policies map[string]domain.GuildAccessPolicy
}

var _ ports.AccessPolicyRepository = (*AccessPolicyRepository)(nil)

func NewAccessPolicyRepository() *AccessPolicyRepository {
	return &AccessPolicyRepository{policies: make(map[string]domain.GuildAccessPolicy)}
}

func (repository *AccessPolicyRepository) GetAccessPolicy(ctx context.Context, guildID string) (domain.GuildAccessPolicy, error) {
	if err := ctx.Err(); err != nil {
		return domain.GuildAccessPolicy{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	policy, found := repository.policies[guildID]
	if !found {
		return domain.GuildAccessPolicy{}, fmt.Errorf("%w: guild access policy", domain.ErrNotFound)
	}
	return policy, nil
}

func (repository *AccessPolicyRepository) SaveAccessPolicy(ctx context.Context, policy domain.GuildAccessPolicy, expectedVersion int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, found := repository.policies[policy.GuildID]
	if (!found && expectedVersion != 0) || (found && current.Version != expectedVersion) {
		return domain.ErrConflict
	}
	repository.policies[policy.GuildID] = policy
	return nil
}
