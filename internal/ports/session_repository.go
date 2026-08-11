package ports

import (
	"context"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// ArtifactQueue accepts validated, bounded attachment-ingest requests.
type ArtifactQueue interface {
	Enqueue(ctx context.Context, request domain.ArtifactIngestRequest) error
}

type CommandQueue interface {
	Enqueue(ctx context.Context, command domain.CommandEnvelope) error
}

type NotificationQueue interface {
	Enqueue(ctx context.Context, request domain.NotificationRequest) error
}

type ObjectStore interface {
	Put(ctx context.Context, key string, contentType string, body []byte, sha256Base64 string) error
}

type ArtifactDownloader interface {
	Download(ctx context.Context, request domain.ArtifactIngestRequest) ([]byte, error)
}

type AccessPolicyRepository interface {
	GetAccessPolicy(ctx context.Context, guildID string) (domain.GuildAccessPolicy, error)
	SaveAccessPolicy(ctx context.Context, policy domain.GuildAccessPolicy, expectedVersion int64) error
}

type WorkflowRepository interface {
	AcquireWorkflow(
		ctx context.Context,
		session domain.Session,
		expectedVersion int64,
		workflow domain.Workflow,
		event domain.SessionEvent,
	) error

	CompleteWorkflow(
		ctx context.Context,
		session domain.Session,
		expectedVersion int64,
		workflow domain.Workflow,
		event domain.SessionEvent,
	) error

	GetWorkflow(ctx context.Context, sessionID string, workflowID string) (domain.Workflow, error)
	SetWorkflowExecution(ctx context.Context, workflow domain.Workflow, expectedStatus domain.WorkflowStatus) error
}

type WorkflowStarter interface {
	Start(ctx context.Context, workflow domain.Workflow) (string, error)
}

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
