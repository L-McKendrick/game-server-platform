package memory

import (
	"context"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

var _ ports.GuildServerConfigRepository = (*SessionRepository)(nil)

func (repository *SessionRepository) GetGuildServerConfig(ctx context.Context, guildID string) (domain.GuildServerConfig, error) {
	if err := ctx.Err(); err != nil {
		return domain.GuildServerConfig{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	config, exists := repository.serverConfigs[strings.TrimSpace(guildID)]
	if !exists {
		return domain.GuildServerConfig{}, domain.ErrNotFound
	}
	return config, nil
}

func (repository *SessionRepository) SaveGuildServerConfig(ctx context.Context, config domain.GuildServerConfig, expectedRevision int64) (domain.GuildServerConfig, error) {
	if err := ctx.Err(); err != nil {
		return domain.GuildServerConfig{}, err
	}
	if err := config.Validate(); err != nil {
		return domain.GuildServerConfig{}, err
	}
	if config.Revision != expectedRevision+1 {
		return domain.GuildServerConfig{}, domain.ErrConflict
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, exists := repository.serverConfigs[config.GuildID]
	currentRevision := int64(0)
	if exists {
		currentRevision = current.Revision
	}
	if currentRevision != expectedRevision {
		if current.Revision == config.Revision && current.ObjectKey == config.ObjectKey && current.SHA256 == config.SHA256 && current.Active() == config.Active() {
			return current, nil
		}
		return domain.GuildServerConfig{}, domain.ErrConflict
	}
	repository.serverConfigs[config.GuildID] = config
	return config, nil
}
