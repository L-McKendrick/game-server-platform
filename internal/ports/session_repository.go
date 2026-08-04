package ports

import (
	"context"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// SessionRepository provides durable access to session metadata.
type SessionRepository interface {
	Create(
		ctx context.Context,
		session domain.Session,
		event domain.SessionEvent,
		idempotency domain.IdempotencyRecord,
	) error

	Get(
		ctx context.Context,
		sessionID string,
	) (domain.Session, error)

	SaveWithEvent(
		ctx context.Context,
		session domain.Session,
		expectedVersion int64,
		event domain.SessionEvent,
		idempotency domain.IdempotencyRecord,
	) error

	GetIdempotency(
		ctx context.Context,
		key string,
	) (domain.IdempotencyRecord, error)

	ListByOwner(
		ctx context.Context,
		ownerDiscordUserID string,
		limit int32,
	) ([]domain.Session, error)
}
