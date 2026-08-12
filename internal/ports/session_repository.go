package ports

import (
	"context"
	"time"

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

type ProvisioningRepository interface {
	SaveProvisioningStage(ctx context.Context, session domain.Session, expectedVersion int64, event domain.SessionEvent) error
	AcquireCapacitySlot(ctx context.Context, sessionID string, workflowID string, limit int, now time.Time) (string, error)
	ReleaseCapacitySlot(ctx context.Context, slotID string, sessionID string) error
}

type BootstrapRepository interface {
	SaveBootstrapStage(ctx context.Context, session domain.Session, expectedVersion int64, event domain.SessionEvent) error
}

type MonitoringRepository interface {
	ListRunning(ctx context.Context, limit int32) ([]domain.Session, error)
	SaveMonitoring(ctx context.Context, session domain.Session, expectedVersion int64, event *domain.SessionEvent) error
}

type MonitoringCommandStatus struct { Status string; ErrorMessage string; Observation domain.HealthObservation }
type MonitoringRunner interface {
	Start(ctx context.Context, session domain.Session) (string, error)
	Observe(ctx context.Context, instanceID string, commandID string) (MonitoringCommandStatus, error)
}

type BootstrapCommandStatus struct {
	Status       string
	ErrorMessage string
}

// BootstrapRunner starts and observes one idempotent Systems Manager command.
// The command itself owns durable per-stage markers on the session data volume.
type BootstrapRunner interface {
	Start(ctx context.Context, session domain.Session) (string, error)
	Observe(ctx context.Context, instanceID string, commandID string) (BootstrapCommandStatus, error)
}

type ComputeProvisioner interface {
	FindInstance(ctx context.Context, request domain.ComputeLaunchRequest) (domain.ComputeObservation, bool, error)
	EnsureInstance(ctx context.Context, request domain.ComputeLaunchRequest, knownInstanceID string) (domain.ComputeObservation, error)
	ObserveInstance(ctx context.Context, instanceID string) (domain.ComputeObservation, error)
	IsManaged(ctx context.Context, instanceID string) (bool, error)
	StopInstance(ctx context.Context, instanceID string) error
	StartInstance(ctx context.Context, instanceID string) error
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
