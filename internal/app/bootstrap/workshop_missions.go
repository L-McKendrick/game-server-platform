package bootstrap

import (
	"context"

	"github.com/L-McKendrick/game-server-platform/internal/app/workshopmanifest"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func (service *Service) workshopMissions(ctx context.Context, session domain.Session) ([]domain.MissionRecord, error) {
	return workshopmanifest.Load(ctx, service.workshopMissionManifest, session)
}
