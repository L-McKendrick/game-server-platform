package access

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

// AllowedRoles returns the effective role set shown by the administration UI.
// A persisted empty set intentionally disables normal-member access while
// preserving the separate Administrator/Manage Server recovery path.
func (service *Service) AllowedRoles(ctx context.Context, guildID string) ([]string, int64, error) {
	policy, err := service.repository.GetAccessPolicy(ctx, strings.TrimSpace(guildID))
	if err == nil {
		return append([]string(nil), policy.AllowedRoleIDs...), policy.Version, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, 0, err
	}
	roles := make([]string, 0, len(service.fallbackRoles))
	for roleID := range service.fallbackRoles {
		roles = append(roles, roleID)
	}
	sort.Strings(roles)
	return roles, 0, nil
}

// PublicCardChannel returns the configured destination for new public cards.
// An empty value means the channel where the session is created.
func (service *Service) PublicCardChannel(ctx context.Context, guildID string) (string, error) {
	policy, err := service.repository.GetAccessPolicy(ctx, strings.TrimSpace(guildID))
	if errors.Is(err, domain.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(policy.PublicCardChannelID), nil
}

func (service *Service) ConfigurePublicCardChannel(ctx context.Context, guildID, userID string, canManageGuild bool, channelID string) (domain.GuildAccessPolicy, error) {
	if !canManageGuild {
		return domain.GuildAccessPolicy{}, domain.ErrForbidden
	}
	guildID, userID, channelID = strings.TrimSpace(guildID), strings.TrimSpace(userID), strings.TrimSpace(channelID)
	current, err := service.repository.GetAccessPolicy(ctx, guildID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.GuildAccessPolicy{}, err
	}
	if err == nil && current.PublicCardChannelID == channelID {
		return current, nil
	}
	policy := current
	if errors.Is(err, domain.ErrNotFound) {
		policy.AllowedRoleIDs = sortedKeys(service.fallbackRoles)
		policy.AllowedChannelIDs = sortedKeys(service.fallbackChannels)
	}
	policy.GuildID = guildID
	policy.PublicCardChannelID = channelID
	policy.Version++
	policy.UpdatedBy = userID
	policy.UpdatedAt = service.clock.Now().UTC()
	if err := policy.Validate(); err != nil {
		return domain.GuildAccessPolicy{}, err
	}
	expectedVersion := policy.Version - 1
	if err := service.repository.SaveAccessPolicy(ctx, policy, expectedVersion); err != nil {
		return domain.GuildAccessPolicy{}, err
	}
	return policy, nil
}

// ClearRoles removes normal-member access only when the policy revision shown
// by the confirmation is still current. Version zero binds the deployment
// fallback before the guild has persisted its first policy.
func (service *Service) ClearRoles(ctx context.Context, guildID, userID string, canManageGuild bool, expectedVersion int64) (domain.GuildAccessPolicy, error) {
	if !canManageGuild {
		return domain.GuildAccessPolicy{}, domain.ErrForbidden
	}
	current, err := service.repository.GetAccessPolicy(ctx, strings.TrimSpace(guildID))
	if err == nil {
		if current.Version != expectedVersion {
			return domain.GuildAccessPolicy{}, domain.ErrConflict
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.GuildAccessPolicy{}, err
	} else if expectedVersion != 0 {
		return domain.GuildAccessPolicy{}, domain.ErrConflict
	}
	policy := domain.GuildAccessPolicy{
		GuildID: strings.TrimSpace(guildID), Version: expectedVersion + 1,
		UpdatedBy: strings.TrimSpace(userID), UpdatedAt: service.clock.Now().UTC(),
	}
	if err == nil {
		policy.AllowedChannelIDs = append([]string(nil), current.AllowedChannelIDs...)
		policy.PublicCardChannelID = current.PublicCardChannelID
	}
	if err := policy.Validate(); err != nil {
		return domain.GuildAccessPolicy{}, err
	}
	if err := service.repository.SaveAccessPolicy(ctx, policy, expectedVersion); err != nil {
		return domain.GuildAccessPolicy{}, err
	}
	return policy, nil
}

func (service *Service) Configure(ctx context.Context, guildID string, userID string, canManageGuild bool, roleIDs []string, channelIDs []string) (domain.GuildAccessPolicy, error) {
	if !canManageGuild {
		return domain.GuildAccessPolicy{}, domain.ErrForbidden
	}
	expectedVersion := int64(0)
	normalizedRoles, normalizedChannels := unique(roleIDs), unique(channelIDs)
	current, err := service.repository.GetAccessPolicy(ctx, guildID)
	if err == nil {
		if slices.Equal(unique(current.AllowedRoleIDs), normalizedRoles) && slices.Equal(unique(current.AllowedChannelIDs), normalizedChannels) {
			return current, nil
		}
		expectedVersion = current.Version
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.GuildAccessPolicy{}, err
	}
	policy := domain.GuildAccessPolicy{
		GuildID: strings.TrimSpace(guildID), AllowedRoleIDs: normalizedRoles, AllowedChannelIDs: normalizedChannels,
		Version: expectedVersion + 1, UpdatedBy: strings.TrimSpace(userID), UpdatedAt: service.clock.Now().UTC(),
	}
	if expectedVersion > 0 {
		policy.PublicCardChannelID = current.PublicCardChannelID
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

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
