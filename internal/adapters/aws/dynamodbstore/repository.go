package dynamodbstore

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const (
	sessionSortKey        = "METADATA"
	sessionCardSortKey    = "DISCORD_CARD"
	sessionModlistSortKey = "DISCORD_MODLIST"
	idempotencySortKey    = "RESULT"
	ownerIndexName        = "gsi1"
	schemaVersion         = 3
	maximumGuildScanItems = int32(1000)
)

func marshalSessionJSON(value any) string {
	encoded, _ := json.Marshal(value)
	if string(encoded) == "null" || string(encoded) == "{}" {
		return ""
	}
	return string(encoded)
}

// API contains the DynamoDB operations used by the repository.
type API interface {
	GetItem(
		ctx context.Context,
		params *dynamodb.GetItemInput,
		optFns ...func(*dynamodb.Options),
	) (*dynamodb.GetItemOutput, error)

	Query(
		ctx context.Context,
		params *dynamodb.QueryInput,
		optFns ...func(*dynamodb.Options),
	) (*dynamodb.QueryOutput, error)

	PutItem(
		ctx context.Context,
		params *dynamodb.PutItemInput,
		optFns ...func(*dynamodb.Options),
	) (*dynamodb.PutItemOutput, error)

	DeleteItem(
		ctx context.Context,
		params *dynamodb.DeleteItemInput,
		optFns ...func(*dynamodb.Options),
	) (*dynamodb.DeleteItemOutput, error)

	TransactWriteItems(
		ctx context.Context,
		params *dynamodb.TransactWriteItemsInput,
		optFns ...func(*dynamodb.Options),
	) (*dynamodb.TransactWriteItemsOutput, error)
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// Repository stores sessions and events in one DynamoDB table.
type Repository struct {
	client    API
	tableName string
}

var _ ports.SessionRepository = (*Repository)(nil)
var _ ports.SessionCardRepository = (*Repository)(nil)

// New creates a DynamoDB session repository.
func New(client API, tableName string) *Repository {
	return &Repository{
		client:    client,
		tableName: strings.TrimSpace(tableName),
	}
}

type sessionItem struct {
	PK            string `dynamodbav:"pk"`
	SK            string `dynamodbav:"sk"`
	EntityType    string `dynamodbav:"entity_type"`
	SchemaVersion int    `dynamodbav:"schema_version"`

	SessionID                        string   `dynamodbav:"session_id"`
	Slug                             string   `dynamodbav:"slug"`
	DisplayName                      string   `dynamodbav:"display_name"`
	Description                      string   `dynamodbav:"description,omitempty"`
	GameType                         string   `dynamodbav:"game_type"`
	OwnerDiscordUserID               string   `dynamodbav:"owner_discord_user_id"`
	GuildID                          string   `dynamodbav:"guild_id"`
	ChannelID                        string   `dynamodbav:"channel_id"`
	GameProfileID                    string   `dynamodbav:"game_profile_id"`
	SleepAfterSeconds                int64    `dynamodbav:"sleep_after_seconds"`
	ArchiveAfterSeconds              int64    `dynamodbav:"archive_after_seconds"`
	TeamSpeakEnabled                 bool     `dynamodbav:"teamspeak_enabled"`
	Vanilla                          bool     `dynamodbav:"vanilla"`
	CreatorDLCs                      []string `dynamodbav:"creator_dlcs,omitempty"`
	StartWhenReady                   bool     `dynamodbav:"start_when_ready,omitempty"`
	ConfigurationRevision            int64    `dynamodbav:"configuration_revision"`
	ServerConfigRevision             int64    `dynamodbav:"server_config_revision,omitempty"`
	ServerConfigObjectKey            string   `dynamodbav:"server_config_object_key,omitempty"`
	ServerConfigSHA256               string   `dynamodbav:"server_config_sha256,omitempty"`
	MissionObjectKey                 string   `dynamodbav:"mission_object_key,omitempty"`
	MissionFilesJSON                 string   `dynamodbav:"mission_files_json,omitempty"`
	ConfiguredMissionJSON            string   `dynamodbav:"configured_mission_json,omitempty"`
	CurrentMissionJSON               string   `dynamodbav:"current_mission_json,omitempty"`
	PresetObjectKey                  string   `dynamodbav:"preset_object_key,omitempty"`
	PresetRevisionSequence           int64    `dynamodbav:"preset_revision_sequence,omitempty"`
	ActivePresetRevision             int64    `dynamodbav:"active_preset_revision,omitempty"`
	ActivePresetObjectKey            string   `dynamodbav:"active_preset_object_key,omitempty"`
	ActivePresetModlistKey           string   `dynamodbav:"active_preset_modlist_key,omitempty"`
	ActivePresetModlistName          string   `dynamodbav:"active_preset_modlist_name,omitempty"`
	ActivePresetModlistSHA           string   `dynamodbav:"active_preset_modlist_sha256,omitempty"`
	ActivePresetModlistSize          int64    `dynamodbav:"active_preset_modlist_size,omitempty"`
	ActivePresetWorkshopCount        int      `dynamodbav:"active_preset_workshop_count,omitempty"`
	ActivePresetStagedAt             string   `dynamodbav:"active_preset_staged_at,omitempty"`
	ActivePresetActivatedAt          string   `dynamodbav:"active_preset_activated_at,omitempty"`
	PendingPresetRevision            int64    `dynamodbav:"pending_preset_revision,omitempty"`
	PendingPresetBaseRevision        int64    `dynamodbav:"pending_preset_base_revision,omitempty"`
	PendingPresetObjectKey           string   `dynamodbav:"pending_preset_object_key,omitempty"`
	PendingPresetModlistKey          string   `dynamodbav:"pending_preset_modlist_key,omitempty"`
	PendingPresetModlistName         string   `dynamodbav:"pending_preset_modlist_name,omitempty"`
	PendingPresetModlistSHA          string   `dynamodbav:"pending_preset_modlist_sha256,omitempty"`
	PendingPresetModlistSize         int64    `dynamodbav:"pending_preset_modlist_size,omitempty"`
	PendingPresetWorkshopCount       int      `dynamodbav:"pending_preset_workshop_count,omitempty"`
	PendingPresetStatus              string   `dynamodbav:"pending_preset_status,omitempty"`
	PendingPresetStagedAt            string   `dynamodbav:"pending_preset_staged_at,omitempty"`
	PendingPresetWorkflowID          string   `dynamodbav:"pending_preset_workflow_id,omitempty"`
	PendingPresetApplyStartedAt      string   `dynamodbav:"pending_preset_apply_started_at,omitempty"`
	PendingPresetFailedAt            string   `dynamodbav:"pending_preset_failed_at,omitempty"`
	PendingPresetFailureDetail       string   `dynamodbav:"pending_preset_failure_detail,omitempty"`
	PendingPresetRollbackDisposition string   `dynamodbav:"pending_preset_rollback_disposition,omitempty"`
	PendingPresetRollbackAt          string   `dynamodbav:"pending_preset_rollback_at,omitempty"`
	PendingPresetRollbackDetail      string   `dynamodbav:"pending_preset_rollback_detail,omitempty"`
	MissionArtifactStatus            string   `dynamodbav:"mission_artifact_status,omitempty"`
	PresetArtifactStatus             string   `dynamodbav:"preset_artifact_status,omitempty"`
	MissionArtifactIssue             string   `dynamodbav:"mission_artifact_issue,omitempty"`
	PresetArtifactIssue              string   `dynamodbav:"preset_artifact_issue,omitempty"`
	CapacitySlotID                   string   `dynamodbav:"capacity_slot_id,omitempty"`
	AvailabilityZone                 string   `dynamodbav:"availability_zone,omitempty"`
	SubnetID                         string   `dynamodbav:"subnet_id,omitempty"`
	SecurityGroupIDs                 []string `dynamodbav:"security_group_ids,omitempty"`
	InstanceProfile                  string   `dynamodbav:"instance_profile,omitempty"`
	AMIID                            string   `dynamodbav:"ami_id,omitempty"`
	InstanceType                     string   `dynamodbav:"instance_type,omitempty"`
	InstanceID                       string   `dynamodbav:"instance_id,omitempty"`
	DataVolumeID                     string   `dynamodbav:"data_volume_id,omitempty"`
	PublicIPv4                       string   `dynamodbav:"public_ipv4,omitempty"`
	InfrastructureObservedAt         string   `dynamodbav:"infrastructure_observed_at,omitempty"`
	ArchiveID                        string   `dynamodbav:"archive_id,omitempty"`
	ArchiveObjectKey                 string   `dynamodbav:"archive_object_key,omitempty"`
	ArchiveManifestObjectKey         string   `dynamodbav:"archive_manifest_object_key,omitempty"`
	ArchiveManifestSHA256            string   `dynamodbav:"archive_manifest_sha256,omitempty"`
	ArchiveManifestSizeBytes         int64    `dynamodbav:"archive_manifest_size_bytes,omitempty"`
	ArchiveSHA256                    string   `dynamodbav:"archive_sha256,omitempty"`
	ArchiveSizeBytes                 int64    `dynamodbav:"archive_size_bytes,omitempty"`
	ArchiveFormat                    string   `dynamodbav:"archive_format,omitempty"`
	ArchiveVerifiedAt                string   `dynamodbav:"archive_verified_at,omitempty"`
	ArchiveSourceState               string   `dynamodbav:"archive_source_state,omitempty"`
	ProgressWorkflowID               string   `dynamodbav:"progress_workflow_id,omitempty"`
	ProgressWorkflowType             string   `dynamodbav:"progress_workflow_type,omitempty"`
	ProgressMilestone                string   `dynamodbav:"progress_milestone,omitempty"`
	ProgressCompletedMilestones      []string `dynamodbav:"progress_completed_milestones,omitempty"`
	ProgressSkippedMilestones        []string `dynamodbav:"progress_skipped_milestones,omitempty"`
	ProgressState                    string   `dynamodbav:"progress_state,omitempty"`
	ProgressStartedAt                string   `dynamodbav:"progress_started_at,omitempty"`
	ProgressLastProgressAt           string   `dynamodbav:"progress_last_progress_at,omitempty"`
	// ProgressUpdatedAt is retained as a write-through compatibility projection
	// while older deployed readers still consume the Phase 12.4 field.
	ProgressUpdatedAt       string `dynamodbav:"progress_updated_at,omitempty"`
	FailureCode             string `dynamodbav:"failure_code,omitempty"`
	FailureStage            string `dynamodbav:"failure_stage,omitempty"`
	FailureRetryDisposition string `dynamodbav:"failure_retry_disposition,omitempty"`
	FailureResourceImpact   string `dynamodbav:"failure_resource_impact,omitempty"`
	FailureDetail           string `dynamodbav:"failure_detail,omitempty"`
	FailureAt               string `dynamodbav:"failure_at,omitempty"`
	FailureSupportReference string `dynamodbav:"failure_support_reference,omitempty"`

	ActiveWorkflowID             string `dynamodbav:"active_workflow_id,omitempty"`
	ActiveWorkflowType           string `dynamodbav:"active_workflow_type,omitempty"`
	ActiveWorkflowStartedAt      string `dynamodbav:"active_workflow_started_at,omitempty"`
	ActiveWorkflowLeaseExpiresAt string `dynamodbav:"active_workflow_lease_expires_at,omitempty"`

	DesiredState          string `dynamodbav:"desired_state"`
	ObservedState         string `dynamodbav:"observed_state"`
	LifecycleState        string `dynamodbav:"lifecycle_state"`
	HealthStatus          string `dynamodbav:"health_status"`
	MonitoringCommandID   string `dynamodbav:"monitoring_command_id,omitempty"`
	MonitoringStartedAt   string `dynamodbav:"monitoring_started_at,omitempty"`
	PlayerCountKnown      bool   `dynamodbav:"player_count_known,omitempty"`
	PlayerCount           int    `dynamodbav:"player_count,omitempty"`
	PlayerCountObservedAt string `dynamodbav:"player_count_observed_at,omitempty"`
	IdleSince             string `dynamodbav:"idle_since,omitempty"`
	SleepingSince         string `dynamodbav:"sleeping_since,omitempty"`

	Version   int64  `dynamodbav:"version"`
	CreatedAt string `dynamodbav:"created_at"`
	UpdatedAt string `dynamodbav:"updated_at"`

	GSI1PK string `dynamodbav:"gsi1pk"`
	GSI1SK string `dynamodbav:"gsi1sk"`
}

type sessionCardItem struct {
	PK                      string `dynamodbav:"pk"`
	SK                      string `dynamodbav:"sk"`
	EntityType              string `dynamodbav:"entity_type"`
	SchemaVersion           int    `dynamodbav:"schema_version"`
	SessionID               string `dynamodbav:"session_id"`
	ChannelID               string `dynamodbav:"channel_id"`
	MessageID               string `dynamodbav:"message_id"`
	DeliveredRevision       int64  `dynamodbav:"delivered_revision,omitempty"`
	DeliveredNotificationID string `dynamodbav:"delivered_notification_id,omitempty"`
	ContentSHA256           string `dynamodbav:"content_sha256,omitempty"`
}

type sessionModlistItem struct {
	PK                      string `dynamodbav:"pk"`
	SK                      string `dynamodbav:"sk"`
	EntityType              string `dynamodbav:"entity_type"`
	SchemaVersion           int    `dynamodbav:"schema_version"`
	SessionID               string `dynamodbav:"session_id"`
	ChannelID               string `dynamodbav:"channel_id"`
	MessageID               string `dynamodbav:"message_id"`
	ObjectKey               string `dynamodbav:"object_key"`
	Filename                string `dynamodbav:"filename"`
	DeliveredRevision       int64  `dynamodbav:"delivered_revision"`
	DeliveredNotificationID string `dynamodbav:"delivered_notification_id"`
	ContentSHA256           string `dynamodbav:"content_sha256"`
}

type eventItem struct {
	PK            string            `dynamodbav:"pk"`
	SK            string            `dynamodbav:"sk"`
	EntityType    string            `dynamodbav:"entity_type"`
	SchemaVersion int               `dynamodbav:"schema_version"`
	EventID       string            `dynamodbav:"event_id"`
	SessionID     string            `dynamodbav:"session_id"`
	EventType     string            `dynamodbav:"event_type"`
	OccurredAt    string            `dynamodbav:"occurred_at"`
	ActorType     string            `dynamodbav:"actor_type"`
	ActorID       string            `dynamodbav:"actor_id"`
	CorrelationID string            `dynamodbav:"correlation_id"`
	Data          map[string]string `dynamodbav:"data"`
}

type idempotencyItem struct {
	PK              string `dynamodbav:"pk"`
	SK              string `dynamodbav:"sk"`
	EntityType      string `dynamodbav:"entity_type"`
	SchemaVersion   int    `dynamodbav:"schema_version"`
	IdempotencyKey  string `dynamodbav:"idempotency_key"`
	RequestHash     string `dynamodbav:"request_hash"`
	Status          string `dynamodbav:"status"`
	CreatedAt       string `dynamodbav:"created_at"`
	CompletedAt     string `dynamodbav:"completed_at,omitempty"`
	ResultReference string `dynamodbav:"result_reference,omitempty"`
	ExpiresAtEpoch  int64  `dynamodbav:"expires_at_epoch"`
}

type slugClaimItem struct {
	PK            string `dynamodbav:"pk"`
	SK            string `dynamodbav:"sk"`
	EntityType    string `dynamodbav:"entity_type"`
	SchemaVersion int    `dynamodbav:"schema_version"`
	GuildID       string `dynamodbav:"guild_id"`
	Slug          string `dynamodbav:"slug"`
	SessionID     string `dynamodbav:"session_id"`
}

// Create atomically creates the session and its initial event.
func (repository *Repository) Create(
	ctx context.Context,
	session domain.Session,
	event domain.SessionEvent,
	idempotency domain.IdempotencyRecord,
) error {
	if err := repository.validate(); err != nil {
		return err
	}

	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate session: %w", err)
	}

	if err := validateEvent(session.ID, event); err != nil {
		return err
	}

	if err := validateIdempotency(session.ID, idempotency); err != nil {
		return err
	}
	legacyCollision, err := repository.legacyGuildSlugExists(ctx, session.GuildID, session.Slug)
	if err != nil {
		return fmt.Errorf("check legacy session slug: %w", err)
	}
	if legacyCollision {
		return fmt.Errorf("%w: %s", domain.ErrSlugConflict, session.Slug)
	}

	sessionAttributes, err := attributevalue.MarshalMap(toSessionItem(session))
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	eventAttributes, err := attributevalue.MarshalMap(toEventItem(event))
	if err != nil {
		return fmt.Errorf("marshal session event: %w", err)
	}

	idempotencyAttributes, err := attributevalue.MarshalMap(
		toIdempotencyItem(idempotency),
	)
	if err != nil {
		return fmt.Errorf("marshal idempotency record: %w", err)
	}
	slugClaimAttributes, err := attributevalue.MarshalMap(toSlugClaimItem(session))
	if err != nil {
		return fmt.Errorf("marshal slug claim: %w", err)
	}

	_, err = repository.client.TransactWriteItems(
		ctx,
		&dynamodb.TransactWriteItemsInput{
			ClientRequestToken: aws.String(createTransactionToken(event.ID, session.Slug)),
			TransactItems: []types.TransactWriteItem{
				{
					Put: &types.Put{
						TableName: aws.String(repository.tableName),
						Item:      sessionAttributes,
						ConditionExpression: aws.String(
							"attribute_not_exists(pk) AND attribute_not_exists(sk)",
						),
					},
				},
				{
					Put: &types.Put{
						TableName: aws.String(repository.tableName),
						Item:      eventAttributes,
						ConditionExpression: aws.String(
							"attribute_not_exists(pk) AND attribute_not_exists(sk)",
						),
					},
				},
				{
					Put: &types.Put{
						TableName: aws.String(repository.tableName),
						Item:      idempotencyAttributes,
						ConditionExpression: aws.String(
							"attribute_not_exists(pk) AND attribute_not_exists(sk)",
						),
					},
				},
				{
					Put: &types.Put{
						TableName: aws.String(repository.tableName),
						Item:      slugClaimAttributes,
						ConditionExpression: aws.String(
							"attribute_not_exists(pk) AND attribute_not_exists(sk)",
						),
					},
				},
			},
		},
	)
	if err != nil {
		var transactionCanceled *types.TransactionCanceledException
		if errors.As(err, &transactionCanceled) {
			if len(transactionCanceled.CancellationReasons) > 3 &&
				aws.ToString(transactionCanceled.CancellationReasons[3].Code) == "ConditionalCheckFailed" {
				return fmt.Errorf("%w: %s", domain.ErrSlugConflict, session.Slug)
			}
			if claimed, lookupErr := repository.slugClaimExists(ctx, session.GuildID, session.Slug); lookupErr == nil && claimed {
				return fmt.Errorf("%w: %s", domain.ErrSlugConflict, session.Slug)
			}
			return fmt.Errorf(
				"%w: session %s",
				domain.ErrAlreadyExists,
				session.ID,
			)
		}

		return fmt.Errorf("create session transaction: %w", err)
	}

	return nil
}

func (repository *Repository) slugClaimExists(ctx context.Context, guildID string, slug string) (bool, error) {
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(repository.tableName),
		ConsistentRead: aws.Bool(true),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: "GUILD#" + guildID},
			"sk": &types.AttributeValueMemberS{Value: "SLUG#" + slug},
		},
	})
	if err != nil {
		return false, err
	}
	return output != nil && len(output.Item) > 0, nil
}

// legacyGuildSlugExists protects sessions created before guild-scoped slug
// claims were introduced. New concurrent writers are still serialized by the
// transactional claim in Create.
func (repository *Repository) legacyGuildSlugExists(ctx context.Context, guildID string, slug string) (bool, error) {
	var startKey map[string]types.AttributeValue
	var scanned int32
	pages := 0
	for scanned < maximumGuildScanItems && pages < 10 {
		pages++
		pageLimit := maximumGuildScanItems - scanned
		if pageLimit > 100 {
			pageLimit = 100
		}
		output, err := repository.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(repository.tableName),
			Limit:             aws.Int32(pageLimit),
			ExclusiveStartKey: startKey,
			FilterExpression:  aws.String("entity_type = :type AND guild_id = :guild AND slug = :slug"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":type":  &types.AttributeValueMemberS{Value: "Session"},
				":guild": &types.AttributeValueMemberS{Value: guildID},
				":slug":  &types.AttributeValueMemberS{Value: slug},
			},
		})
		if err != nil {
			return false, err
		}
		if len(output.Items) > 0 {
			return true, nil
		}
		scanned += output.ScannedCount
		startKey = output.LastEvaluatedKey
		if len(startKey) == 0 {
			return false, nil
		}
	}
	return false, nil
}

func toSlugClaimItem(session domain.Session) slugClaimItem {
	return slugClaimItem{
		PK:            "GUILD#" + session.GuildID,
		SK:            "SLUG#" + session.Slug,
		EntityType:    "SessionSlugClaim",
		SchemaVersion: schemaVersion,
		GuildID:       session.GuildID,
		Slug:          session.Slug,
		SessionID:     session.ID,
	}
}

func createTransactionToken(eventID string, slug string) string {
	digest := sha256.Sum256([]byte(slug))
	if len(eventID) > 27 {
		eventID = eventID[:27]
	}
	return fmt.Sprintf("%s-%x", eventID, digest[:4])
}

// Get returns the authoritative session metadata record.
func (repository *Repository) Get(
	ctx context.Context,
	sessionID string,
) (domain.Session, error) {
	if err := repository.validate(); err != nil {
		return domain.Session{}, err
	}

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return domain.Session{}, fmt.Errorf("session ID is required")
	}

	output, err := repository.client.GetItem(
		ctx,
		&dynamodb.GetItemInput{
			TableName:      aws.String(repository.tableName),
			ConsistentRead: aws.Bool(true),
			Key: map[string]types.AttributeValue{
				"pk": &types.AttributeValueMemberS{
					Value: sessionPartitionKey(sessionID),
				},
				"sk": &types.AttributeValueMemberS{
					Value: sessionSortKey,
				},
			},
		},
	)
	if err != nil {
		return domain.Session{}, fmt.Errorf("get session: %w", err)
	}

	if len(output.Item) == 0 {
		return domain.Session{}, fmt.Errorf(
			"%w: session %s",
			domain.ErrNotFound,
			sessionID,
		)
	}

	var item sessionItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.Session{}, fmt.Errorf("unmarshal session: %w", err)
	}

	session, err := fromSessionItem(item)
	if err != nil {
		return domain.Session{}, fmt.Errorf("decode session: %w", err)
	}

	return session, nil
}

// SaveCardReference updates delivery metadata without changing the session
// lifecycle version, so Discord delivery retries do not contend with workers.
func (repository *Repository) GetCardReference(ctx context.Context, sessionID string) (domain.SessionCardReference, error) {
	if err := repository.validate(); err != nil {
		return domain.SessionCardReference{}, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return domain.SessionCardReference{}, fmt.Errorf("session ID is required")
	}
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(repository.tableName), Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: sessionPartitionKey(sessionID)},
			"sk": &types.AttributeValueMemberS{Value: sessionCardSortKey},
		}, ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return domain.SessionCardReference{}, fmt.Errorf("get Discord card reference: %w", err)
	}
	if len(output.Item) == 0 {
		return domain.SessionCardReference{}, fmt.Errorf("%w: session card %s", domain.ErrNotFound, sessionID)
	}
	var item sessionCardItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.SessionCardReference{}, err
	}
	reference := domain.SessionCardReference{
		SessionID: item.SessionID, ChannelID: item.ChannelID, MessageID: item.MessageID,
		DeliveredRevision: item.DeliveredRevision, DeliveredNotificationID: item.DeliveredNotificationID,
		ContentSHA256: item.ContentSHA256,
	}
	if err := reference.Validate(); err != nil {
		return domain.SessionCardReference{}, err
	}
	return reference, nil
}

func (repository *Repository) SaveCardReference(ctx context.Context, reference domain.SessionCardReference) error {
	if err := repository.validate(); err != nil {
		return err
	}
	if err := reference.Validate(); err != nil {
		return err
	}
	attributes, err := attributevalue.MarshalMap(sessionCardItem{
		PK: sessionPartitionKey(reference.SessionID), SK: sessionCardSortKey,
		EntityType: "SessionCard", SchemaVersion: schemaVersion,
		SessionID: reference.SessionID, ChannelID: reference.ChannelID, MessageID: reference.MessageID,
		DeliveredRevision: reference.DeliveredRevision, DeliveredNotificationID: reference.DeliveredNotificationID,
		ContentSHA256: reference.ContentSHA256,
	})
	if err != nil {
		return fmt.Errorf("marshal Discord card reference: %w", err)
	}
	_, err = repository.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(repository.tableName), Item: attributes,
		ConditionExpression: aws.String("attribute_not_exists(pk) OR channel_id = :channel"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":channel": &types.AttributeValueMemberS{Value: reference.ChannelID},
		},
	})
	if err != nil {
		return fmt.Errorf("save Discord card reference: %w", err)
	}
	return nil
}

func (repository *Repository) GetModlistReference(ctx context.Context, sessionID string) (domain.SessionModlistReference, error) {
	if err := repository.validate(); err != nil {
		return domain.SessionModlistReference{}, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return domain.SessionModlistReference{}, fmt.Errorf("session ID is required")
	}
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(repository.tableName), Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: sessionPartitionKey(sessionID)},
			"sk": &types.AttributeValueMemberS{Value: sessionModlistSortKey},
		}, ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return domain.SessionModlistReference{}, fmt.Errorf("get Discord modlist reference: %w", err)
	}
	if len(output.Item) == 0 {
		return domain.SessionModlistReference{}, fmt.Errorf("%w: session modlist %s", domain.ErrNotFound, sessionID)
	}
	var item sessionModlistItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.SessionModlistReference{}, err
	}
	reference := domain.SessionModlistReference{
		SessionID: item.SessionID, ChannelID: item.ChannelID, MessageID: item.MessageID,
		ObjectKey: item.ObjectKey, Filename: item.Filename, DeliveredRevision: item.DeliveredRevision,
		DeliveredNotificationID: item.DeliveredNotificationID, ContentSHA256: item.ContentSHA256,
	}
	if err := reference.Validate(); err != nil {
		return domain.SessionModlistReference{}, err
	}
	return reference, nil
}

func (repository *Repository) SaveModlistReference(ctx context.Context, reference domain.SessionModlistReference) error {
	if err := repository.validate(); err != nil {
		return err
	}
	if err := reference.Validate(); err != nil {
		return err
	}
	attributes, err := attributevalue.MarshalMap(sessionModlistItem{
		PK: sessionPartitionKey(reference.SessionID), SK: sessionModlistSortKey,
		EntityType: "SessionModlist", SchemaVersion: schemaVersion,
		SessionID: reference.SessionID, ChannelID: reference.ChannelID, MessageID: reference.MessageID,
		ObjectKey: reference.ObjectKey, Filename: reference.Filename, DeliveredRevision: reference.DeliveredRevision,
		DeliveredNotificationID: reference.DeliveredNotificationID, ContentSHA256: reference.ContentSHA256,
	})
	if err != nil {
		return fmt.Errorf("marshal Discord modlist reference: %w", err)
	}
	_, err = repository.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(repository.tableName), Item: attributes,
		ConditionExpression: aws.String("attribute_not_exists(pk) OR channel_id = :channel"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":channel": &types.AttributeValueMemberS{Value: reference.ChannelID},
		},
	})
	if err != nil {
		return fmt.Errorf("save Discord modlist reference: %w", err)
	}
	return nil
}

// SaveWithEvent atomically writes a version-checked session and an event.
func (repository *Repository) SaveWithEvent(
	ctx context.Context,
	session domain.Session,
	expectedVersion int64,
	event domain.SessionEvent,
	idempotency domain.IdempotencyRecord,
) error {
	if err := repository.validate(); err != nil {
		return err
	}

	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate session: %w", err)
	}

	if expectedVersion < 1 {
		return fmt.Errorf("expected version must be at least 1")
	}

	if session.Version != expectedVersion+1 {
		return fmt.Errorf(
			"session version %d must equal expected version %d plus one",
			session.Version,
			expectedVersion,
		)
	}

	if err := validateEvent(session.ID, event); err != nil {
		return err
	}

	if err := validateIdempotency(session.ID, idempotency); err != nil {
		return err
	}

	sessionAttributes, err := attributevalue.MarshalMap(toSessionItem(session))
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	eventAttributes, err := attributevalue.MarshalMap(toEventItem(event))
	if err != nil {
		return fmt.Errorf("marshal session event: %w", err)
	}

	idempotencyAttributes, err := attributevalue.MarshalMap(
		toIdempotencyItem(idempotency),
	)
	if err != nil {
		return fmt.Errorf("marshal idempotency record: %w", err)
	}

	_, err = repository.client.TransactWriteItems(
		ctx,
		&dynamodb.TransactWriteItemsInput{
			ClientRequestToken: aws.String(event.ID),
			TransactItems: []types.TransactWriteItem{
				{
					Put: &types.Put{
						TableName: aws.String(repository.tableName),
						Item:      sessionAttributes,
						ConditionExpression: aws.String(
							"#version = :expected_version",
						),
						ExpressionAttributeNames: map[string]string{
							"#version": "version",
						},
						ExpressionAttributeValues: map[string]types.AttributeValue{
							":expected_version": &types.AttributeValueMemberN{
								Value: strconv.FormatInt(expectedVersion, 10),
							},
						},
					},
				},
				{
					Put: &types.Put{
						TableName: aws.String(repository.tableName),
						Item:      eventAttributes,
						ConditionExpression: aws.String(
							"attribute_not_exists(pk) AND attribute_not_exists(sk)",
						),
					},
				},
				{
					Put: &types.Put{
						TableName: aws.String(repository.tableName),
						Item:      idempotencyAttributes,
						ConditionExpression: aws.String(
							"attribute_not_exists(pk) AND attribute_not_exists(sk)",
						),
					},
				},
			},
		},
	)
	if err != nil {
		var transactionCanceled *types.TransactionCanceledException
		if errors.As(err, &transactionCanceled) {
			return fmt.Errorf(
				"%w: session %s expected version %d",
				domain.ErrConflict,
				session.ID,
				expectedVersion,
			)
		}

		return fmt.Errorf("save session transaction: %w", err)
	}

	return nil
}

// GetIdempotency returns a command result by external idempotency key.
func (repository *Repository) GetIdempotency(
	ctx context.Context,
	key string,
) (domain.IdempotencyRecord, error) {
	if err := repository.validate(); err != nil {
		return domain.IdempotencyRecord{}, err
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return domain.IdempotencyRecord{}, fmt.Errorf("idempotency key is required")
	}

	output, err := repository.client.GetItem(
		ctx,
		&dynamodb.GetItemInput{
			TableName:      aws.String(repository.tableName),
			ConsistentRead: aws.Bool(true),
			Key: map[string]types.AttributeValue{
				"pk": &types.AttributeValueMemberS{
					Value: idempotencyPartitionKey(key),
				},
				"sk": &types.AttributeValueMemberS{
					Value: idempotencySortKey,
				},
			},
		},
	)
	if err != nil {
		return domain.IdempotencyRecord{}, fmt.Errorf(
			"get idempotency record: %w",
			err,
		)
	}

	if len(output.Item) == 0 {
		return domain.IdempotencyRecord{}, fmt.Errorf(
			"%w: idempotency key %s",
			domain.ErrNotFound,
			key,
		)
	}

	var item idempotencyItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.IdempotencyRecord{}, fmt.Errorf(
			"unmarshal idempotency record: %w",
			err,
		)
	}

	record, err := fromIdempotencyItem(item)
	if err != nil {
		return domain.IdempotencyRecord{}, fmt.Errorf(
			"decode idempotency record: %w",
			err,
		)
	}

	return record, nil
}

// ListByOwner lists recent sessions belonging to one Discord user.
//
// This query uses a GSI and is not used for authoritative state mutations.
func (repository *Repository) ListByOwner(
	ctx context.Context,
	ownerDiscordUserID string,
	limit int32,
) ([]domain.Session, error) {
	if err := repository.validate(); err != nil {
		return nil, err
	}

	ownerDiscordUserID = strings.TrimSpace(ownerDiscordUserID)
	if ownerDiscordUserID == "" {
		return nil, fmt.Errorf("owner Discord user ID is required")
	}

	if limit <= 0 {
		limit = 25
	}

	if limit > 100 {
		limit = 100
	}

	output, err := repository.client.Query(
		ctx,
		&dynamodb.QueryInput{
			TableName:              aws.String(repository.tableName),
			IndexName:              aws.String(ownerIndexName),
			KeyConditionExpression: aws.String("#owner = :owner"),
			ExpressionAttributeNames: map[string]string{
				"#owner": "gsi1pk",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":owner": &types.AttributeValueMemberS{
					Value: ownerPartitionKey(ownerDiscordUserID),
				},
			},
			Limit:            aws.Int32(limit),
			ScanIndexForward: aws.Bool(false),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("query sessions by owner: %w", err)
	}

	sessions := make([]domain.Session, 0, len(output.Items))

	for _, attributes := range output.Items {
		var item sessionItem
		if err := attributevalue.UnmarshalMap(attributes, &item); err != nil {
			return nil, fmt.Errorf("unmarshal listed session: %w", err)
		}

		session, err := fromSessionItem(item)
		if err != nil {
			return nil, fmt.Errorf("decode listed session: %w", err)
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}

// ListByGuild returns recent session metadata from one guild. The bounded scan
// includes legacy sessions that predate guild slug claims and secondary-index
// attributes.
func (repository *Repository) ListByGuild(
	ctx context.Context,
	guildID string,
	limit int32,
) ([]domain.Session, error) {
	if err := repository.validate(); err != nil {
		return nil, err
	}
	guildID = strings.TrimSpace(guildID)
	if guildID == "" {
		return nil, fmt.Errorf("Discord guild ID is required")
	}
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}

	sessions := make([]domain.Session, 0, limit)
	var startKey map[string]types.AttributeValue
	var scanned int32
	pages := 0
	for scanned < maximumGuildScanItems && pages < 10 {
		pages++
		remainingScan := maximumGuildScanItems - scanned
		if remainingScan > 100 {
			remainingScan = 100
		}
		output, err := repository.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(repository.tableName),
			Limit:             aws.Int32(remainingScan),
			ExclusiveStartKey: startKey,
			FilterExpression:  aws.String("entity_type = :type AND guild_id = :guild"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":type":  &types.AttributeValueMemberS{Value: "Session"},
				":guild": &types.AttributeValueMemberS{Value: guildID},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("scan sessions by guild: %w", err)
		}
		scanned += output.ScannedCount
		for _, attributes := range output.Items {
			var item sessionItem
			if err := attributevalue.UnmarshalMap(attributes, &item); err != nil {
				return nil, fmt.Errorf("unmarshal guild session: %w", err)
			}
			session, err := fromSessionItem(item)
			if err != nil {
				return nil, fmt.Errorf("decode guild session: %w", err)
			}
			sessions = append(sessions, session)
		}
		startKey = output.LastEvaluatedKey
		if len(startKey) == 0 {
			break
		}
	}

	sort.Slice(sessions, func(first, second int) bool {
		if sessions[first].UpdatedAt.Equal(sessions[second].UpdatedAt) {
			return sessions[first].ID > sessions[second].ID
		}
		return sessions[first].UpdatedAt.After(sessions[second].UpdatedAt)
	})
	if int32(len(sessions)) > limit {
		sessions = sessions[:limit]
	}
	return sessions, nil
}

func (repository *Repository) validate() error {
	if repository == nil {
		return fmt.Errorf("DynamoDB repository is nil")
	}

	if repository.client == nil {
		return fmt.Errorf("DynamoDB client is required")
	}

	if repository.tableName == "" {
		return fmt.Errorf("DynamoDB table name is required")
	}

	return nil
}

func validateEvent(
	sessionID string,
	event domain.SessionEvent,
) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate event: %w", err)
	}

	if event.SessionID != sessionID {
		return fmt.Errorf(
			"event session ID %q does not match session %q",
			event.SessionID,
			sessionID,
		)
	}

	return nil
}

func validateIdempotency(
	sessionID string,
	record domain.IdempotencyRecord,
) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("validate idempotency record: %w", err)
	}

	if record.Status != domain.IdempotencyCompleted {
		return fmt.Errorf("metadata mutation requires a completed idempotency record")
	}

	if record.ResultReference != sessionID {
		return fmt.Errorf(
			"idempotency result reference %q does not match session %q",
			record.ResultReference,
			sessionID,
		)
	}

	return nil
}

func toSessionItem(session domain.Session) sessionItem {
	activePreset := session.EffectiveActivePresetRevision()
	presetSequence := session.EffectivePresetRevisionSequence()
	pendingPreset := session.PendingPresetRevision
	return sessionItem{
		PK:            sessionPartitionKey(session.ID),
		SK:            sessionSortKey,
		EntityType:    "Session",
		SchemaVersion: schemaVersion,

		SessionID:                        session.ID,
		Slug:                             session.Slug,
		DisplayName:                      session.DisplayName,
		Description:                      session.Description,
		GameType:                         session.GameType,
		OwnerDiscordUserID:               session.OwnerDiscordUserID,
		GuildID:                          session.GuildID,
		ChannelID:                        session.ChannelID,
		GameProfileID:                    session.GameProfileID,
		SleepAfterSeconds:                session.SleepAfterSeconds,
		ArchiveAfterSeconds:              session.ArchiveAfterSeconds,
		TeamSpeakEnabled:                 session.TeamSpeakEnabled,
		Vanilla:                          session.Vanilla,
		CreatorDLCs:                      append([]string(nil), session.CreatorDLCs...),
		StartWhenReady:                   session.StartWhenReady,
		ConfigurationRevision:            session.ConfigurationRevision,
		ServerConfigRevision:             session.ServerConfigRevision,
		ServerConfigObjectKey:            session.ServerConfigObjectKey,
		ServerConfigSHA256:               session.ServerConfigSHA256,
		MissionObjectKey:                 session.MissionObjectKey,
		MissionFilesJSON:                 marshalSessionJSON(session.MissionFiles),
		ConfiguredMissionJSON:            marshalSessionJSON(session.ConfiguredMission),
		CurrentMissionJSON:               marshalSessionJSON(session.CurrentMission),
		PresetObjectKey:                  session.PresetObjectKey,
		PresetRevisionSequence:           presetSequence,
		ActivePresetRevision:             activePreset.Number,
		ActivePresetObjectKey:            activePreset.PresetObjectKey,
		ActivePresetModlistKey:           activePreset.Modlist.ObjectKey,
		ActivePresetModlistName:          activePreset.Modlist.Filename,
		ActivePresetModlistSHA:           activePreset.Modlist.SHA256,
		ActivePresetModlistSize:          activePreset.Modlist.SizeBytes,
		ActivePresetWorkshopCount:        activePreset.Modlist.WorkshopCount,
		ActivePresetStagedAt:             optionalTimestamp(activePreset.StagedAt),
		ActivePresetActivatedAt:          optionalTimestamp(activePreset.ActivatedAt),
		PendingPresetRevision:            pendingPreset.Number,
		PendingPresetBaseRevision:        pendingPreset.BaseRevision,
		PendingPresetObjectKey:           pendingPreset.PresetObjectKey,
		PendingPresetModlistKey:          pendingPreset.Modlist.ObjectKey,
		PendingPresetModlistName:         pendingPreset.Modlist.Filename,
		PendingPresetModlistSHA:          pendingPreset.Modlist.SHA256,
		PendingPresetModlistSize:         pendingPreset.Modlist.SizeBytes,
		PendingPresetWorkshopCount:       pendingPreset.Modlist.WorkshopCount,
		PendingPresetStatus:              string(pendingPreset.Status),
		PendingPresetStagedAt:            optionalTimestamp(pendingPreset.StagedAt),
		PendingPresetWorkflowID:          pendingPreset.ApplyWorkflowID,
		PendingPresetApplyStartedAt:      optionalTimestamp(pendingPreset.ApplyStartedAt),
		PendingPresetFailedAt:            optionalTimestamp(pendingPreset.FailedAt),
		PendingPresetFailureDetail:       pendingPreset.FailureDetail,
		PendingPresetRollbackDisposition: string(pendingPreset.RollbackDisposition),
		PendingPresetRollbackAt:          optionalTimestamp(pendingPreset.RollbackAt),
		PendingPresetRollbackDetail:      pendingPreset.RollbackDetail,
		MissionArtifactStatus:            string(session.MissionArtifactStatus),
		PresetArtifactStatus:             string(session.PresetArtifactStatus),
		MissionArtifactIssue:             session.MissionArtifactIssue,
		PresetArtifactIssue:              session.PresetArtifactIssue,
		CapacitySlotID:                   session.Infrastructure.CapacitySlotID,
		AvailabilityZone:                 session.Infrastructure.AvailabilityZone,
		SubnetID:                         session.Infrastructure.SubnetID,
		SecurityGroupIDs:                 append([]string(nil), session.Infrastructure.SecurityGroupIDs...),
		InstanceProfile:                  session.Infrastructure.InstanceProfile,
		AMIID:                            session.Infrastructure.AMIID,
		InstanceType:                     session.Infrastructure.InstanceType,
		InstanceID:                       session.Infrastructure.InstanceID,
		DataVolumeID:                     session.Infrastructure.DataVolumeID,
		PublicIPv4:                       session.Infrastructure.PublicIPv4,
		InfrastructureObservedAt:         optionalTimestamp(session.Infrastructure.LastObservedAt),
		ArchiveID:                        session.Archive.ID,
		ArchiveObjectKey:                 session.Archive.ObjectKey,
		ArchiveManifestObjectKey:         session.Archive.ManifestObjectKey,
		ArchiveManifestSHA256:            session.Archive.ManifestSHA256,
		ArchiveManifestSizeBytes:         session.Archive.ManifestSizeBytes,
		ArchiveSHA256:                    session.Archive.SHA256,
		ArchiveSizeBytes:                 session.Archive.SizeBytes,
		ArchiveFormat:                    session.Archive.Format,
		ArchiveVerifiedAt:                optionalTimestamp(session.Archive.VerifiedAt),
		ArchiveSourceState:               string(session.ArchiveSourceState),
		ProgressWorkflowID:               session.Progress.WorkflowID,
		ProgressWorkflowType:             session.Progress.WorkflowType,
		ProgressMilestone:                string(session.Progress.Milestone),
		ProgressCompletedMilestones:      progressMilestoneStrings(session.Progress.CompletedMilestones),
		ProgressSkippedMilestones:        progressMilestoneStrings(session.Progress.SkippedMilestones),
		ProgressState:                    string(session.Progress.State),
		ProgressStartedAt:                optionalTimestamp(session.Progress.StartedAt),
		ProgressLastProgressAt:           optionalTimestamp(session.Progress.LastProgressAt),
		ProgressUpdatedAt:                optionalTimestamp(session.Progress.LastProgressAt),
		FailureCode:                      session.Failure.Code,
		FailureStage:                     session.Failure.Stage,
		FailureRetryDisposition:          string(session.Failure.RetryDisposition),
		FailureResourceImpact:            string(session.Failure.ResourceImpact),
		FailureDetail:                    session.Failure.Detail,
		FailureAt:                        optionalTimestamp(session.Failure.FailedAt),
		FailureSupportReference:          session.Failure.SupportReference,
		ActiveWorkflowID:                 session.ActiveWorkflowID,
		ActiveWorkflowType:               session.ActiveWorkflowType,
		ActiveWorkflowStartedAt:          fixedTimestamp(session.ActiveWorkflowStartedAt),
		ActiveWorkflowLeaseExpiresAt:     fixedTimestamp(session.ActiveWorkflowLeaseExpiresAt),

		DesiredState:          string(session.DesiredState),
		ObservedState:         string(session.ObservedState),
		LifecycleState:        string(session.LifecycleState),
		HealthStatus:          string(session.HealthStatus),
		MonitoringCommandID:   session.MonitoringCommandID,
		MonitoringStartedAt:   optionalTimestamp(session.MonitoringStartedAt),
		PlayerCountKnown:      session.PlayerCountKnown,
		PlayerCount:           session.PlayerCount,
		PlayerCountObservedAt: optionalTimestamp(session.PlayerCountObservedAt),
		IdleSince:             optionalTimestamp(session.IdleSince),
		SleepingSince:         optionalTimestamp(session.SleepingSince),

		Version:   session.Version,
		CreatedAt: session.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: session.UpdatedAt.UTC().Format(time.RFC3339Nano),

		GSI1PK: ownerPartitionKey(session.OwnerDiscordUserID),
		GSI1SK: fmt.Sprintf(
			"UPDATED#%s#SESSION#%s",
			sortTimestamp(session.UpdatedAt),
			session.ID,
		),
	}
}

func fromSessionItem(item sessionItem) (domain.Session, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, item.CreatedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse created_at: %w", err)
	}

	updatedAt, err := time.Parse(time.RFC3339Nano, item.UpdatedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse updated_at: %w", err)
	}

	// Schema version 1 records created before configuration support omitted
	// these fields. Preserve read compatibility during the deployment rollout.
	if item.GameProfileID == "" {
		item.GameProfileID = "arma3-default"
	}
	if item.SleepAfterSeconds == 0 {
		item.SleepAfterSeconds = 1800
	}
	if item.ArchiveAfterSeconds == 0 {
		item.ArchiveAfterSeconds = 7 * 24 * 60 * 60
	}
	activeWorkflowStartedAt, err := parseOptionalTimestamp(item.ActiveWorkflowStartedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse active workflow started_at: %w", err)
	}
	activeWorkflowLeaseExpiresAt, err := parseOptionalTimestamp(item.ActiveWorkflowLeaseExpiresAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse active workflow lease: %w", err)
	}
	infrastructureObservedAt, err := parseOptionalTimestamp(item.InfrastructureObservedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse infrastructure observed_at: %w", err)
	}
	archiveVerifiedAt, err := parseOptionalTimestamp(item.ArchiveVerifiedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse archive_verified_at: %w", err)
	}
	monitoringStartedAt, err := parseOptionalTimestamp(item.MonitoringStartedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse monitoring started_at: %w", err)
	}
	playerCountObservedAt, err := parseOptionalTimestamp(item.PlayerCountObservedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse player_count_observed_at: %w", err)
	}
	idleSince, err := parseOptionalTimestamp(item.IdleSince)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse idle_since: %w", err)
	}
	sleepingSince, err := parseOptionalTimestamp(item.SleepingSince)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse sleeping_since: %w", err)
	}
	progressLastProgressAt, err := parseOptionalTimestamp(item.ProgressLastProgressAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse progress last_progress_at: %w", err)
	}
	if progressLastProgressAt.IsZero() {
		progressLastProgressAt, err = parseOptionalTimestamp(item.ProgressUpdatedAt)
		if err != nil {
			return domain.Session{}, fmt.Errorf("parse progress updated_at: %w", err)
		}
	}
	progressStartedAt, err := parseOptionalTimestamp(item.ProgressStartedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse progress started_at: %w", err)
	}
	if progressStartedAt.IsZero() && item.ProgressWorkflowID != "" {
		progressStartedAt = activeWorkflowStartedAt
		if progressStartedAt.IsZero() {
			progressStartedAt = progressLastProgressAt
		}
	}
	failureAt, err := parseOptionalTimestamp(item.FailureAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse failure_at: %w", err)
	}
	activePresetStagedAt, err := parseOptionalTimestamp(item.ActivePresetStagedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse active preset staged_at: %w", err)
	}
	activePresetActivatedAt, err := parseOptionalTimestamp(item.ActivePresetActivatedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse active preset activated_at: %w", err)
	}
	pendingPresetStagedAt, err := parseOptionalTimestamp(item.PendingPresetStagedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse pending preset staged_at: %w", err)
	}
	pendingPresetApplyStartedAt, err := parseOptionalTimestamp(item.PendingPresetApplyStartedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse pending preset apply_started_at: %w", err)
	}
	pendingPresetFailedAt, err := parseOptionalTimestamp(item.PendingPresetFailedAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse pending preset failed_at: %w", err)
	}
	pendingPresetRollbackAt, err := parseOptionalTimestamp(item.PendingPresetRollbackAt)
	if err != nil {
		return domain.Session{}, fmt.Errorf("parse pending preset rollback_at: %w", err)
	}
	missionStatus := domain.ArtifactStatus(item.MissionArtifactStatus)
	if missionStatus == "" && strings.TrimSpace(item.MissionObjectKey) != "" {
		missionStatus = domain.ArtifactAccepted
	}
	var missionFiles []domain.MissionRecord
	var configuredMission, currentMission domain.MissionSelection
	if item.MissionFilesJSON != "" {
		if err := json.Unmarshal([]byte(item.MissionFilesJSON), &missionFiles); err != nil {
			return domain.Session{}, fmt.Errorf("decode mission files: %w", err)
		}
	}
	if item.ConfiguredMissionJSON != "" {
		if err := json.Unmarshal([]byte(item.ConfiguredMissionJSON), &configuredMission); err != nil {
			return domain.Session{}, fmt.Errorf("decode configured mission: %w", err)
		}
	}
	if item.CurrentMissionJSON != "" {
		if err := json.Unmarshal([]byte(item.CurrentMissionJSON), &currentMission); err != nil {
			return domain.Session{}, fmt.Errorf("decode current mission: %w", err)
		}
	}
	if configuredMission.Template == "" {
		if item.MissionObjectKey == "" {
			configuredMission = domain.DefaultMissionSelection()
		} else {
			configuredMission = domain.UploadedMissionSelection(item.MissionObjectKey)
		}
	}
	if len(missionFiles) == 0 && item.MissionObjectKey != "" {
		missionFiles = []domain.MissionRecord{{ObjectKey: item.MissionObjectKey, Filename: domain.UploadedMissionSelection(item.MissionObjectKey).Template + ".pbo", Status: missionStatus, AddedAt: createdAt}}
	}
	presetStatus := domain.ArtifactStatus(item.PresetArtifactStatus)
	if presetStatus == "" && strings.TrimSpace(item.PresetObjectKey) != "" {
		presetStatus = domain.ArtifactAccepted
	}
	progressMilestone := legacyProgressMilestone(item.ProgressWorkflowType, item.ProgressMilestone, item.ProgressState)
	progressCompleted := progressMilestones(item.ProgressCompletedMilestones)
	if item.ProgressState == "" && len(progressCompleted) == 0 && len(item.ProgressSkippedMilestones) == 0 {
		progressCompleted = legacyCompletedMilestones(item.ProgressWorkflowType, progressMilestone)
	}

	session := domain.Session{
		ID:                     item.SessionID,
		Slug:                   item.Slug,
		DisplayName:            item.DisplayName,
		Description:            item.Description,
		GameType:               item.GameType,
		OwnerDiscordUserID:     item.OwnerDiscordUserID,
		GuildID:                item.GuildID,
		ChannelID:              item.ChannelID,
		GameProfileID:          item.GameProfileID,
		SleepAfterSeconds:      item.SleepAfterSeconds,
		ArchiveAfterSeconds:    item.ArchiveAfterSeconds,
		TeamSpeakEnabled:       item.TeamSpeakEnabled,
		Vanilla:                item.Vanilla,
		CreatorDLCs:            append([]string(nil), item.CreatorDLCs...),
		StartWhenReady:         item.StartWhenReady,
		ConfigurationRevision:  item.ConfigurationRevision,
		ServerConfigRevision:   item.ServerConfigRevision,
		ServerConfigObjectKey:  item.ServerConfigObjectKey,
		ServerConfigSHA256:     item.ServerConfigSHA256,
		MissionObjectKey:       item.MissionObjectKey,
		MissionFiles:           missionFiles,
		ConfiguredMission:      configuredMission,
		CurrentMission:         currentMission,
		PresetObjectKey:        item.PresetObjectKey,
		PresetRevisionSequence: item.PresetRevisionSequence,
		PendingPresetRevision: domain.PresetRevision{
			Number: item.PendingPresetRevision, BaseRevision: item.PendingPresetBaseRevision, PresetObjectKey: item.PendingPresetObjectKey,
			Modlist: domain.PresetModlistMetadata{ObjectKey: item.PendingPresetModlistKey, Filename: item.PendingPresetModlistName, SHA256: item.PendingPresetModlistSHA, SizeBytes: item.PendingPresetModlistSize, WorkshopCount: item.PendingPresetWorkshopCount},
			Status:  domain.PresetRevisionStatus(item.PendingPresetStatus), StagedAt: pendingPresetStagedAt, ApplyWorkflowID: item.PendingPresetWorkflowID, ApplyStartedAt: pendingPresetApplyStartedAt, FailedAt: pendingPresetFailedAt, FailureDetail: item.PendingPresetFailureDetail,
			RollbackDisposition: domain.PresetRollbackDisposition(item.PendingPresetRollbackDisposition), RollbackAt: pendingPresetRollbackAt, RollbackDetail: item.PendingPresetRollbackDetail,
		},
		MissionArtifactStatus: missionStatus,
		PresetArtifactStatus:  presetStatus,
		MissionArtifactIssue:  item.MissionArtifactIssue,
		PresetArtifactIssue:   item.PresetArtifactIssue,
		Infrastructure: domain.Infrastructure{
			CapacitySlotID: item.CapacitySlotID, AvailabilityZone: item.AvailabilityZone,
			SubnetID: item.SubnetID, SecurityGroupIDs: append([]string(nil), item.SecurityGroupIDs...),
			InstanceProfile: item.InstanceProfile, AMIID: item.AMIID, InstanceType: item.InstanceType,
			InstanceID: item.InstanceID, DataVolumeID: item.DataVolumeID, PublicIPv4: item.PublicIPv4,
			LastObservedAt: infrastructureObservedAt,
		},
		Archive: domain.ArchiveMetadata{
			ID: item.ArchiveID, ObjectKey: item.ArchiveObjectKey,
			ManifestObjectKey: item.ArchiveManifestObjectKey, SHA256: item.ArchiveSHA256,
			ManifestSHA256:    item.ArchiveManifestSHA256,
			ManifestSizeBytes: item.ArchiveManifestSizeBytes,
			SizeBytes:         item.ArchiveSizeBytes, Format: item.ArchiveFormat, VerifiedAt: archiveVerifiedAt,
		},
		ArchiveSourceState: domain.LifecycleState(item.ArchiveSourceState),
		Progress: domain.SessionProgress{
			WorkflowID: item.ProgressWorkflowID, WorkflowType: item.ProgressWorkflowType,
			Milestone:           progressMilestone,
			CompletedMilestones: progressCompleted,
			SkippedMilestones:   progressMilestones(item.ProgressSkippedMilestones),
			State:               progressState(item.ProgressState, item.ProgressMilestone, item.ActiveWorkflowID),
			StartedAt:           progressStartedAt, LastProgressAt: progressLastProgressAt,
		},
		Failure: domain.FailureRecord{
			Code: item.FailureCode, Stage: item.FailureStage,
			RetryDisposition: domain.RetryDisposition(item.FailureRetryDisposition),
			ResourceImpact:   domain.ResourceCostImpact(item.FailureResourceImpact),
			Detail:           item.FailureDetail, FailedAt: failureAt,
			SupportReference: item.FailureSupportReference,
		},
		ActiveWorkflowID:             item.ActiveWorkflowID,
		ActiveWorkflowType:           item.ActiveWorkflowType,
		ActiveWorkflowStartedAt:      activeWorkflowStartedAt,
		ActiveWorkflowLeaseExpiresAt: activeWorkflowLeaseExpiresAt,

		DesiredState:        domain.LifecycleState(item.DesiredState),
		ObservedState:       domain.LifecycleState(item.ObservedState),
		LifecycleState:      domain.LifecycleState(item.LifecycleState),
		HealthStatus:        domain.HealthStatus(item.HealthStatus),
		MonitoringCommandID: item.MonitoringCommandID, MonitoringStartedAt: monitoringStartedAt,
		PlayerCountKnown: item.PlayerCountKnown, PlayerCount: item.PlayerCount,
		PlayerCountObservedAt: playerCountObservedAt, IdleSince: idleSince,
		SleepingSince: sleepingSince,

		Version:   item.Version,
		CreatedAt: createdAt.UTC(),
		UpdatedAt: updatedAt.UTC(),
	}
	if item.ActivePresetRevision > 0 {
		session.ActivePresetRevision = domain.PresetRevision{
			Number: item.ActivePresetRevision, PresetObjectKey: item.ActivePresetObjectKey,
			Modlist: domain.PresetModlistMetadata{ObjectKey: item.ActivePresetModlistKey, Filename: item.ActivePresetModlistName, SHA256: item.ActivePresetModlistSHA, SizeBytes: item.ActivePresetModlistSize, WorkshopCount: item.ActivePresetWorkshopCount},
			Status:  domain.PresetRevisionActive, StagedAt: activePresetStagedAt, ActivatedAt: activePresetActivatedAt,
		}
	}
	if item.ActivePresetRevision == 0 {
		session.ActivePresetRevision = session.EffectiveActivePresetRevision()
		if !session.ActivePresetRevision.Empty() && session.PresetRevisionSequence < session.ActivePresetRevision.Number {
			session.PresetRevisionSequence = session.ActivePresetRevision.Number
		}
	}

	if err := session.Validate(); err != nil {
		return domain.Session{}, err
	}

	return session, nil
}

func optionalTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func progressMilestoneStrings(milestones []domain.ProgressMilestone) []string {
	if len(milestones) == 0 {
		return nil
	}
	values := make([]string, len(milestones))
	for index, milestone := range milestones {
		values[index] = string(milestone)
	}
	return values
}

func progressMilestones(values []string) []domain.ProgressMilestone {
	if len(values) == 0 {
		return nil
	}
	milestones := make([]domain.ProgressMilestone, len(values))
	for index, value := range values {
		milestones[index] = domain.ProgressMilestone(value)
	}
	return milestones
}

func legacyProgressMilestone(workflowType, milestone, state string) domain.ProgressMilestone {
	value := domain.ProgressMilestone(milestone)
	if state != "" {
		return value
	}
	if _, known := domain.MilestonesForWorkflow(workflowType); !known {
		return value
	}
	switch value {
	case domain.ProgressGameContentSetup:
		if workflowType == domain.BootstrapWorkflowType {
			return domain.ProgressHostPrepared
		}
	case domain.ProgressFailed:
		return domain.ProgressAccepted
	}
	return value
}

func legacyCompletedMilestones(workflowType string, current domain.ProgressMilestone) []domain.ProgressMilestone {
	milestones, known := domain.MilestonesForWorkflow(workflowType)
	if !known {
		return nil
	}
	currentIndex := slices.Index(milestones, current)
	if currentIndex <= 0 {
		return nil
	}
	end := currentIndex
	if current == domain.ProgressCompleted {
		end++
	}
	return slices.Clone(milestones[:end])
}

func progressState(value, milestone, activeWorkflowID string) domain.ProgressState {
	if state := domain.ProgressState(value); state.Valid() {
		return state
	}
	switch domain.ProgressMilestone(milestone) {
	case domain.ProgressCompleted:
		return domain.ProgressCompletedState
	case domain.ProgressFailed:
		return domain.ProgressActionRequired
	}
	if strings.TrimSpace(activeWorkflowID) != "" {
		return domain.ProgressActive
	}
	// Legacy completed workflows often retained their last successful coarse
	// milestone instead of a terminal marker.
	if strings.TrimSpace(milestone) != "" {
		return domain.ProgressCompletedState
	}
	return ""
}

// fixedTimestamp preserves chronological ordering when DynamoDB compares lock
// leases as strings. RFC3339Nano's trimmed fractional seconds do not.
func fixedTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func parseOptionalTimestamp(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func toEventItem(event domain.SessionEvent) eventItem {
	return eventItem{
		PK:            sessionPartitionKey(event.SessionID),
		SK:            eventSortKey(event),
		EntityType:    "SessionEvent",
		SchemaVersion: schemaVersion,
		EventID:       event.ID,
		SessionID:     event.SessionID,
		EventType:     string(event.Type),
		OccurredAt:    event.OccurredAt.UTC().Format(time.RFC3339Nano),
		ActorType:     event.ActorType,
		ActorID:       event.ActorID,
		CorrelationID: event.CorrelationID,
		Data:          event.Data,
	}
}

func toIdempotencyItem(
	record domain.IdempotencyRecord,
) idempotencyItem {
	completedAt := ""
	if !record.CompletedAt.IsZero() {
		completedAt = record.CompletedAt.UTC().Format(time.RFC3339Nano)
	}

	return idempotencyItem{
		PK:              idempotencyPartitionKey(record.Key),
		SK:              idempotencySortKey,
		EntityType:      "IdempotencyRecord",
		SchemaVersion:   schemaVersion,
		IdempotencyKey:  record.Key,
		RequestHash:     record.RequestHash,
		Status:          string(record.Status),
		CreatedAt:       record.CreatedAt.UTC().Format(time.RFC3339Nano),
		CompletedAt:     completedAt,
		ResultReference: record.ResultReference,
		ExpiresAtEpoch:  record.ExpiresAtEpoch,
	}
}

func fromIdempotencyItem(
	item idempotencyItem,
) (domain.IdempotencyRecord, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, item.CreatedAt)
	if err != nil {
		return domain.IdempotencyRecord{}, fmt.Errorf(
			"parse idempotency created_at: %w",
			err,
		)
	}

	var completedAt time.Time
	if item.CompletedAt != "" {
		completedAt, err = time.Parse(time.RFC3339Nano, item.CompletedAt)
		if err != nil {
			return domain.IdempotencyRecord{}, fmt.Errorf(
				"parse idempotency completed_at: %w",
				err,
			)
		}
	}

	record := domain.IdempotencyRecord{
		Key:             item.IdempotencyKey,
		RequestHash:     item.RequestHash,
		Status:          domain.IdempotencyStatus(item.Status),
		CreatedAt:       createdAt.UTC(),
		CompletedAt:     completedAt.UTC(),
		ResultReference: item.ResultReference,
		ExpiresAtEpoch:  item.ExpiresAtEpoch,
	}

	if err := record.Validate(); err != nil {
		return domain.IdempotencyRecord{}, err
	}

	return record, nil
}

func idempotencyPartitionKey(key string) string {
	return "IDEMPOTENCY#" + key
}

func sessionPartitionKey(sessionID string) string {
	return "SESSION#" + sessionID
}

func ownerPartitionKey(ownerDiscordUserID string) string {
	return "OWNER#" + ownerDiscordUserID
}

func eventSortKey(event domain.SessionEvent) string {
	return fmt.Sprintf(
		"EVENT#%s#%s",
		sortTimestamp(event.OccurredAt),
		event.ID,
	)
}

func sortTimestamp(value time.Time) string {
	return value.UTC().Format("20060102T150405.000000000Z")
}
