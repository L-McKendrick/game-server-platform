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

type GuildServerConfigRepository interface {
	GetGuildServerConfig(ctx context.Context, guildID string) (domain.GuildServerConfig, error)
	SaveGuildServerConfig(ctx context.Context, config domain.GuildServerConfig, expectedRevision int64) (domain.GuildServerConfig, error)
}

type CommandQueue interface {
	Enqueue(ctx context.Context, command domain.CommandEnvelope) error
}

type ResetQueue interface {
	Enqueue(ctx context.Context, request domain.ResetRequest) error
}

type ResetRepository interface {
	CreateResetConfirmation(ctx context.Context, confirmation domain.ResetConfirmation) error
	GetResetConfirmation(ctx context.Context, confirmationID string) (domain.ResetConfirmation, error)
	ConsumeResetConfirmation(ctx context.Context, confirmationID, actorID, guildID, phrase string, operation domain.ResetOperation, now time.Time) (domain.ResetOperation, error)
	GetResetOperation(ctx context.Context, operationID string) (domain.ResetOperation, error)
	GetActiveReset(ctx context.Context, environment string) (domain.ResetOperation, error)
	GetLatestReset(ctx context.Context, environment string) (domain.ResetOperation, error)
	SaveResetOperation(ctx context.Context, operation domain.ResetOperation, expectedVersion int64) error
}

type ResetCleanupResult struct {
	DeletedSessions int
	DeletedObjects  int
}

type ResetCleaner interface {
	Cleanup(ctx context.Context, operation domain.ResetOperation) (ResetCleanupResult, error)
}

type NotificationQueue interface {
	Enqueue(ctx context.Context, request domain.NotificationRequest) error
}

// ConfirmationRepository stores durable destructive-action confirmations.
// Creation and consumption must atomically revalidate the bound session.
type ConfirmationRepository interface {
	CreateConfirmation(ctx context.Context, confirmation domain.Confirmation) error
	GetConfirmation(ctx context.Context, code string) (domain.Confirmation, error)
	ConsumeConfirmation(ctx context.Context, code, ownerDiscordUserID, guildID string, now time.Time) (domain.Confirmation, domain.Session, error)
	CancelConfirmation(ctx context.Context, code, ownerDiscordUserID, guildID string, now time.Time) (domain.Confirmation, error)
}

// SessionCardRepository stores replaceable Discord delivery metadata without
// coupling card delivery to lifecycle-version writes.
type SessionCardRepository interface {
	Get(ctx context.Context, sessionID string) (domain.Session, error)
	GetCardReference(ctx context.Context, sessionID string) (domain.SessionCardReference, error)
	SaveCardReference(ctx context.Context, reference domain.SessionCardReference) error
	GetModlistReference(ctx context.Context, sessionID string) (domain.SessionModlistReference, error)
	SaveModlistReference(ctx context.Context, reference domain.SessionModlistReference) error
}

type ObjectStore interface {
	Put(ctx context.Context, key string, contentType string, body []byte, sha256Base64 string) error
}

// PrivateObjectStore supports compensating deletion when a conditional
// metadata write definitively loses a race after a private object was stored.
type PrivateObjectStore interface {
	ObjectStore
	Delete(ctx context.Context, key string) error
}

type ObjectReader interface {
	Get(ctx context.Context, key string) ([]byte, error)
}

type ArchiveCommandStatus struct {
	Status       string
	ErrorMessage string
	ObjectKey    string
	SHA256       string
	SizeBytes    int64
}

type ArchiveRunner interface {
	Start(ctx context.Context, session domain.Session, archiveID string) (string, error)
	Observe(ctx context.Context, instanceID string, commandID string) (ArchiveCommandStatus, error)
}

type ArchiveObject struct {
	Key         string
	SHA256      string
	SizeBytes   int64
	ContentType string
}

type ArchiveStore interface {
	Put(ctx context.Context, object ArchiveObject, body []byte) error
	Verify(ctx context.Context, object ArchiveObject) error
	Get(ctx context.Context, object ArchiveObject) ([]byte, error)
}

// SessionObjectCleaner permanently removes every version and delete marker
// under the platform-owned sessions/<id>/ prefix.
type SessionObjectCleaner interface {
	DeleteSessionObjects(ctx context.Context, sessionID string) (int, error)
}

// InfrastructureDestroyer removes only resources whose immutable platform
// tags match the requested session. Implementations must be idempotent.
type InfrastructureDestroyer interface {
	TerminateInstance(ctx context.Context, sessionID string, instanceID string) error
	InstanceTerminated(ctx context.Context, sessionID string, instanceID string) (bool, error)
	DeleteVolume(ctx context.Context, sessionID string, volumeID string) error
	VolumeDeleted(ctx context.Context, sessionID string, volumeID string) (bool, error)
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

// ReliabilityRepository owns atomic workflow-control and operator-audit state.
// Cloud resource inventory and orphan cleanup use a separate Phase 10.2 port.
type ReliabilityRepository interface {
	SaveWorkflowCancellation(ctx context.Context, workflow domain.Workflow, expectedStatus domain.WorkflowStatus, event domain.SessionEvent) error
	ListActiveWorkflowSessions(ctx context.Context, limit int32) ([]domain.Session, error)
	SaveReconciliationFinding(ctx context.Context, finding domain.ReconciliationFinding) error
	ListReconciliationFindings(ctx context.Context, sessionID string, limit int32) ([]domain.ReconciliationFinding, error)
	SaveDeadLetterOperation(ctx context.Context, operation domain.DeadLetterOperation) error
	GetDeadLetterOperation(ctx context.Context, operationID string) (domain.DeadLetterOperation, error)
}

type WorkflowStarter interface {
	Start(ctx context.Context, workflow domain.Workflow) (string, error)
}

type WorkflowExecutionInspector interface {
	Describe(ctx context.Context, executionARN string) (domain.WorkflowExecutionStatus, bool, error)
}

type DeadLetterManager interface {
	Inspect(ctx context.Context, queue domain.DeadLetterQueue) (domain.DeadLetterInspection, string, error)
	StartRedrive(ctx context.Context, queue domain.DeadLetterQueue, maxMessagesPerSecond int32) (sourceARN string, destinationARN string, error error)
}

type ResourceInventory interface {
	List(ctx context.Context) ([]domain.ResourceObservation, error)
}

type OrphanCleaner interface {
	Quarantine(ctx context.Context, finding domain.OrphanFinding) error
	Cleanup(ctx context.Context, finding domain.OrphanFinding) error
}

type OrphanRepository interface {
	ListSessionsForInventory(ctx context.Context, limit int32) ([]domain.Session, error)
	SaveOrphanFinding(ctx context.Context, finding domain.OrphanFinding) error
	GetOrphanFinding(ctx context.Context, findingID string) (domain.OrphanFinding, error)
	ListOrphanFindings(ctx context.Context, limit int32) ([]domain.OrphanFinding, error)
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
	ListInactivityCandidates(ctx context.Context, limit int32) ([]domain.Session, error)
	SaveMonitoring(ctx context.Context, session domain.Session, expectedVersion int64, events []domain.SessionEvent) error
}

type MonitoringCommandStatus struct {
	Status       string
	ErrorMessage string
	Observation  domain.HealthObservation
}
type MonitoringRunner interface {
	Start(ctx context.Context, session domain.Session) (string, error)
	Observe(ctx context.Context, instanceID string, commandID string) (MonitoringCommandStatus, error)
}

type BootstrapCommandStatus struct {
	Status       string
	ErrorCode    string
	ErrorMessage string
	// Checkpoints contains only allowlisted progress facts parsed from the
	// managed command's output. Raw output never crosses this port.
	Checkpoints []domain.ProgressMilestone
}

// BootstrapRunner starts and observes one idempotent Systems Manager command.
// The command itself owns durable per-stage markers on the session data volume.
type BootstrapRunner interface {
	Start(ctx context.Context, session domain.Session) (string, error)
	Observe(ctx context.Context, instanceID string, commandID string) (BootstrapCommandStatus, error)
}

// PresetRevisionRunner can reinstall the previous active preset after a
// candidate fails, while retaining the same observe contract.
type PresetRevisionRunner interface {
	BootstrapRunner
	StartRollback(ctx context.Context, session domain.Session) (string, error)
}

type RestoreRunner interface {
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

	CheckCapacity(
		ctx context.Context,
		sessionID string,
		limit int,
	) error

	ListByOwner(
		ctx context.Context,
		ownerDiscordUserID string,
		limit int32,
	) ([]domain.Session, error)

	ListByGuild(
		ctx context.Context,
		guildID string,
		limit int32,
	) ([]domain.Session, error)
}
