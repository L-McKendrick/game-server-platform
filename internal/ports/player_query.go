package ports

import (
	"context"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// PlayerQuery obtains a live, non-persistent player view for one managed
// server. Implementations must treat an unavailable query as an error, never
// as an empty server.
type PlayerQuery interface {
	Query(ctx context.Context, host string) (domain.PlayerStatus, error)
}
