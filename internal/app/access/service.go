package access

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type Clock interface{ Now() time.Time }

type Service struct {
	repository       ports.AccessPolicyRepository
	fallbackRoles    map[string]struct{}
	fallbackChannels map[string]struct{}
	clock            Clock
}

func NewService(repository ports.AccessPolicyRepository, fallbackRoles []string, fallbackChannels []string, clock Clock) (*Service, error) {
	if repository == nil || clock == nil {
		return nil, fmt.Errorf("access service dependencies are required")
	}
	service := &Service{
		repository:    repository,
		fallbackRoles: stringSet(fallbackRoles), fallbackChannels: stringSet(fallbackChannels), clock: clock,
	}
	return service, nil
}

func (service *Service) Authorize(ctx context.Context, guildID string, channelID string, userID string, roles []string) error {
	policy, err := service.repository.GetAccessPolicy(ctx, strings.TrimSpace(guildID))
	if err == nil {
		roleAllowed := contains(policy.AllowedRoleIDs, guildID) || intersects(policy.AllowedRoleIDs, roles)
		if (len(policy.AllowedChannelIDs) == 0 || contains(policy.AllowedChannelIDs, channelID)) && roleAllowed {
			return nil
		}
		return domain.ErrForbidden
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	if len(service.fallbackRoles) == 0 {
		return domain.ErrForbidden
	}
	if _, channelAllowed := service.fallbackChannels[strings.TrimSpace(channelID)]; len(service.fallbackChannels) > 0 && !channelAllowed {
		return domain.ErrForbidden
	}
	for _, role := range roles {
		if _, roleAllowed := service.fallbackRoles[strings.TrimSpace(role)]; roleAllowed {
			return nil
		}
	}
	return domain.ErrForbidden
}

func (service *Service) Configure(ctx context.Context, guildID string, userID string, canManageGuild bool, roleIDs []string, channelIDs []string) (domain.GuildAccessPolicy, error) {
	if !canManageGuild {
		return domain.GuildAccessPolicy{}, domain.ErrForbidden
	}
	expectedVersion := int64(0)
	if current, err := service.repository.GetAccessPolicy(ctx, guildID); err == nil {
		expectedVersion = current.Version
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.GuildAccessPolicy{}, err
	}
	policy := domain.GuildAccessPolicy{
		GuildID: strings.TrimSpace(guildID), AllowedRoleIDs: unique(roleIDs), AllowedChannelIDs: unique(channelIDs),
		Version: expectedVersion + 1, UpdatedBy: strings.TrimSpace(userID), UpdatedAt: service.clock.Now().UTC(),
	}
	if err := policy.Validate(); err != nil {
		return domain.GuildAccessPolicy{}, err
	}
	if err := service.repository.SaveAccessPolicy(ctx, policy, expectedVersion); err != nil {
		return domain.GuildAccessPolicy{}, err
	}
	return policy, nil
}

func stringSet(values []string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}
func contains(values []string, target string) bool {
	_, found := stringSet(values)[strings.TrimSpace(target)]
	return found
}
func intersects(first []string, second []string) bool {
	set := stringSet(first)
	for _, value := range second {
		if _, found := set[strings.TrimSpace(value)]; found {
			return true
		}
	}
	return false
}
func unique(values []string) []string {
	set := stringSet(values)
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
