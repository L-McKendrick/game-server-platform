package dynamodbstore

import (
	"context"
	"errors"
	"fmt"
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
	sessionSortKey = "METADATA"
	ownerIndexName = "gsi1"
	schemaVersion  = 1
)

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

	TransactWriteItems(
		ctx context.Context,
		params *dynamodb.TransactWriteItemsInput,
		optFns ...func(*dynamodb.Options),
	) (*dynamodb.TransactWriteItemsOutput, error)
}

// Repository stores sessions and events in one DynamoDB table.
type Repository struct {
	client    API
	tableName string
}

var _ ports.SessionRepository = (*Repository)(nil)

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

	SessionID          string `dynamodbav:"session_id"`
	Slug               string `dynamodbav:"slug"`
	DisplayName        string `dynamodbav:"display_name"`
	GameType           string `dynamodbav:"game_type"`
	OwnerDiscordUserID string `dynamodbav:"owner_discord_user_id"`
	GuildID            string `dynamodbav:"guild_id"`
	ChannelID          string `dynamodbav:"channel_id"`

	DesiredState   string `dynamodbav:"desired_state"`
	ObservedState  string `dynamodbav:"observed_state"`
	LifecycleState string `dynamodbav:"lifecycle_state"`
	HealthStatus   string `dynamodbav:"health_status"`

	Version   int64  `dynamodbav:"version"`
	CreatedAt string `dynamodbav:"created_at"`
	UpdatedAt string `dynamodbav:"updated_at"`

	GSI1PK string `dynamodbav:"gsi1pk"`
	GSI1SK string `dynamodbav:"gsi1sk"`
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

// Create atomically creates the session and its initial event.
func (repository *Repository) Create(
	ctx context.Context,
	session domain.Session,
	event domain.SessionEvent,
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

	sessionAttributes, err := attributevalue.MarshalMap(toSessionItem(session))
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	eventAttributes, err := attributevalue.MarshalMap(toEventItem(event))
	if err != nil {
		return fmt.Errorf("marshal session event: %w", err)
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
			},
		},
	)
	if err != nil {
		var transactionCanceled *types.TransactionCanceledException
		if errors.As(err, &transactionCanceled) {
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

// SaveWithEvent atomically writes a version-checked session and an event.
func (repository *Repository) SaveWithEvent(
	ctx context.Context,
	session domain.Session,
	expectedVersion int64,
	event domain.SessionEvent,
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

	sessionAttributes, err := attributevalue.MarshalMap(toSessionItem(session))
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	eventAttributes, err := attributevalue.MarshalMap(toEventItem(event))
	if err != nil {
		return fmt.Errorf("marshal session event: %w", err)
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
	switch {
	case strings.TrimSpace(event.ID) == "":
		return fmt.Errorf("event ID is required")
	case strings.TrimSpace(event.SessionID) == "":
		return fmt.Errorf("event session ID is required")
	case event.SessionID != sessionID:
		return fmt.Errorf(
			"event session ID %q does not match session %q",
			event.SessionID,
			sessionID,
		)
	case strings.TrimSpace(string(event.Type)) == "":
		return fmt.Errorf("event type is required")
	case event.OccurredAt.IsZero():
		return fmt.Errorf("event occurrence timestamp is required")
	default:
		return nil
	}
}

func toSessionItem(session domain.Session) sessionItem {
	return sessionItem{
		PK:            sessionPartitionKey(session.ID),
		SK:            sessionSortKey,
		EntityType:    "Session",
		SchemaVersion: schemaVersion,

		SessionID:          session.ID,
		Slug:               session.Slug,
		DisplayName:        session.DisplayName,
		GameType:           session.GameType,
		OwnerDiscordUserID: session.OwnerDiscordUserID,
		GuildID:            session.GuildID,
		ChannelID:          session.ChannelID,

		DesiredState:   string(session.DesiredState),
		ObservedState:  string(session.ObservedState),
		LifecycleState: string(session.LifecycleState),
		HealthStatus:   string(session.HealthStatus),

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

	session := domain.Session{
		ID:                 item.SessionID,
		Slug:               item.Slug,
		DisplayName:        item.DisplayName,
		GameType:           item.GameType,
		OwnerDiscordUserID: item.OwnerDiscordUserID,
		GuildID:            item.GuildID,
		ChannelID:          item.ChannelID,

		DesiredState:   domain.LifecycleState(item.DesiredState),
		ObservedState:  domain.LifecycleState(item.ObservedState),
		LifecycleState: domain.LifecycleState(item.LifecycleState),
		HealthStatus:   domain.HealthStatus(item.HealthStatus),

		Version:   item.Version,
		CreatedAt: createdAt.UTC(),
		UpdatedAt: updatedAt.UTC(),
	}

	if err := session.Validate(); err != nil {
		return domain.Session{}, err
	}

	return session, nil
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
