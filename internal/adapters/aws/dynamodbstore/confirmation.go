package dynamodbstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var _ ports.ConfirmationRepository = (*Repository)(nil)

type confirmationItem struct {
	PK                 string `dynamodbav:"pk"`
	SK                 string `dynamodbav:"sk"`
	EntityType         string `dynamodbav:"entity_type"`
	SchemaVersion      int    `dynamodbav:"schema_version"`
	ConfirmationID     string `dynamodbav:"confirmation_id"`
	Code               string `dynamodbav:"code"`
	SessionID          string `dynamodbav:"session_id"`
	OwnerDiscordUserID string `dynamodbav:"owner_discord_user_id"`
	GuildID            string `dynamodbav:"guild_id"`
	Action             string `dynamodbav:"action"`
	BoundState         string `dynamodbav:"bound_state"`
	BoundVersion       int64  `dynamodbav:"bound_version"`
	Status             string `dynamodbav:"status"`
	CreatedAt          string `dynamodbav:"created_at"`
	ExpiresAt          string `dynamodbav:"expires_at"`
	ExpiresAtEpoch     int64  `dynamodbav:"expires_at_epoch"`
	ConsumedAt         string `dynamodbav:"consumed_at,omitempty"`
	CancelledAt        string `dynamodbav:"cancelled_at,omitempty"`
}

func confirmationPartitionKey(code string) string {
	return "CONFIRMATION#" + strings.ToUpper(strings.TrimSpace(code))
}

func (repository *Repository) CreateConfirmation(ctx context.Context, confirmation domain.Confirmation) error {
	if err := repository.validate(); err != nil {
		return err
	}
	if err := confirmation.Validate(); err != nil {
		return err
	}
	attributes, err := attributevalue.MarshalMap(toConfirmationItem(confirmation))
	if err != nil {
		return err
	}
	_, err = repository.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{
			TableName: aws.String(repository.tableName), Item: attributes,
			ConditionExpression:      aws.String("attribute_not_exists(pk) OR (confirmation_id <> :id AND (#status = :cancelled OR expires_at_epoch <= :created OR (#status = :consumed AND (session_id <> :session OR bound_version <> :version))))"),
			ExpressionAttributeNames: map[string]string{"#status": "status"},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":id":        &types.AttributeValueMemberS{Value: confirmation.ID},
				":cancelled": &types.AttributeValueMemberS{Value: string(domain.ConfirmationCancelled)},
				":consumed":  &types.AttributeValueMemberS{Value: string(domain.ConfirmationConsumed)},
				":created":   &types.AttributeValueMemberN{Value: strconv.FormatInt(confirmation.CreatedAt.UTC().Unix(), 10)},
				":session":   &types.AttributeValueMemberS{Value: confirmation.SessionID},
				":version":   &types.AttributeValueMemberN{Value: strconv.FormatInt(confirmation.BoundVersion, 10)},
			},
		}},
		{ConditionCheck: confirmationSessionCheck(repository.tableName, confirmation)},
	}})
	if err != nil {
		var cancelled *types.TransactionCanceledException
		if errors.As(err, &cancelled) {
			if _, getErr := repository.GetConfirmation(ctx, confirmation.Code); getErr == nil {
				return domain.ErrAlreadyExists
			}
			return domain.ErrConfirmationStateDrift
		}
		return fmt.Errorf("create confirmation transaction: %w", err)
	}
	return nil
}

func (repository *Repository) GetConfirmation(ctx context.Context, code string) (domain.Confirmation, error) {
	if err := repository.validate(); err != nil {
		return domain.Confirmation{}, err
	}
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: confirmationPartitionKey(code)},
			"sk": &types.AttributeValueMemberS{Value: "METADATA"},
		},
	})
	if err != nil {
		return domain.Confirmation{}, err
	}
	if len(output.Item) == 0 {
		return domain.Confirmation{}, domain.ErrNotFound
	}
	var item confirmationItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.Confirmation{}, err
	}
	return fromConfirmationItem(item)
}

func (repository *Repository) ConsumeConfirmation(ctx context.Context, code, ownerDiscordUserID, guildID string, now time.Time) (domain.Confirmation, domain.Session, error) {
	confirmation, err := repository.GetConfirmation(ctx, code)
	if err != nil {
		return domain.Confirmation{}, domain.Session{}, err
	}
	if err := confirmation.CheckActor(ownerDiscordUserID, guildID); err != nil {
		return domain.Confirmation{}, domain.Session{}, err
	}
	if err := confirmation.CheckPending(now); err != nil {
		return domain.Confirmation{}, domain.Session{}, err
	}
	session, err := repository.Get(ctx, confirmation.SessionID)
	if err != nil {
		return domain.Confirmation{}, domain.Session{}, err
	}
	confirmation.Status, confirmation.ConsumedAt = domain.ConfirmationConsumed, now.UTC()
	attributes, err := attributevalue.MarshalMap(toConfirmationItem(confirmation))
	if err != nil {
		return domain.Confirmation{}, domain.Session{}, err
	}
	_, err = repository.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{
			TableName: aws.String(repository.tableName), Item: attributes,
			ConditionExpression:      aws.String("#status = :pending AND owner_discord_user_id = :owner AND guild_id = :guild AND expires_at_epoch > :now"),
			ExpressionAttributeNames: map[string]string{"#status": "status"},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pending": &types.AttributeValueMemberS{Value: string(domain.ConfirmationPending)},
				":owner":   &types.AttributeValueMemberS{Value: strings.TrimSpace(ownerDiscordUserID)},
				":guild":   &types.AttributeValueMemberS{Value: strings.TrimSpace(guildID)},
				":now":     &types.AttributeValueMemberN{Value: strconv.FormatInt(now.UTC().Unix(), 10)},
			},
		}},
		{ConditionCheck: confirmationSessionCheck(repository.tableName, confirmation)},
	}})
	if err != nil {
		var cancelled *types.TransactionCanceledException
		if errors.As(err, &cancelled) {
			return domain.Confirmation{}, domain.Session{}, domain.ErrConfirmationStateDrift
		}
		return domain.Confirmation{}, domain.Session{}, fmt.Errorf("consume confirmation transaction: %w", err)
	}
	return confirmation, session, nil
}

func (repository *Repository) CancelConfirmation(ctx context.Context, code, ownerDiscordUserID, guildID string, now time.Time) (domain.Confirmation, error) {
	confirmation, err := repository.GetConfirmation(ctx, code)
	if err != nil {
		return domain.Confirmation{}, err
	}
	if err := confirmation.CheckActor(ownerDiscordUserID, guildID); err != nil {
		return domain.Confirmation{}, err
	}
	if err := confirmation.CheckPending(now); err != nil {
		return domain.Confirmation{}, err
	}
	confirmation.Status, confirmation.CancelledAt = domain.ConfirmationCancelled, now.UTC()
	attributes, err := attributevalue.MarshalMap(toConfirmationItem(confirmation))
	if err != nil {
		return domain.Confirmation{}, err
	}
	_, err = repository.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(repository.tableName), Item: attributes,
		ConditionExpression:      aws.String("#status = :pending AND owner_discord_user_id = :owner AND guild_id = :guild AND expires_at_epoch > :now"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pending": &types.AttributeValueMemberS{Value: string(domain.ConfirmationPending)},
			":owner":   &types.AttributeValueMemberS{Value: strings.TrimSpace(ownerDiscordUserID)},
			":guild":   &types.AttributeValueMemberS{Value: strings.TrimSpace(guildID)},
			":now":     &types.AttributeValueMemberN{Value: strconv.FormatInt(now.UTC().Unix(), 10)},
		},
	})
	if err != nil {
		return domain.Confirmation{}, domain.ErrConfirmationStateDrift
	}
	return confirmation, nil
}

func confirmationSessionCheck(tableName string, confirmation domain.Confirmation) *types.ConditionCheck {
	return &types.ConditionCheck{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: sessionPartitionKey(confirmation.SessionID)},
			"sk": &types.AttributeValueMemberS{Value: sessionSortKey},
		},
		ConditionExpression:      aws.String("#version = :version AND lifecycle_state = :state AND owner_discord_user_id = :owner AND guild_id = :guild"),
		ExpressionAttributeNames: map[string]string{"#version": "version"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":version": &types.AttributeValueMemberN{Value: strconv.FormatInt(confirmation.BoundVersion, 10)},
			":state":   &types.AttributeValueMemberS{Value: string(confirmation.BoundState)},
			":owner":   &types.AttributeValueMemberS{Value: confirmation.OwnerDiscordUserID},
			":guild":   &types.AttributeValueMemberS{Value: confirmation.GuildID},
		},
	}
}

func toConfirmationItem(confirmation domain.Confirmation) confirmationItem {
	return confirmationItem{
		PK: confirmationPartitionKey(confirmation.Code), SK: "METADATA", EntityType: "Confirmation", SchemaVersion: schemaVersion,
		ConfirmationID: confirmation.ID, Code: confirmation.Code, SessionID: confirmation.SessionID,
		OwnerDiscordUserID: confirmation.OwnerDiscordUserID, GuildID: confirmation.GuildID,
		Action: string(confirmation.Action), BoundState: string(confirmation.BoundState), BoundVersion: confirmation.BoundVersion,
		Status: string(confirmation.Status), CreatedAt: optionalTimestamp(confirmation.CreatedAt), ExpiresAt: optionalTimestamp(confirmation.ExpiresAt),
		ExpiresAtEpoch: confirmation.ExpiresAt.UTC().Unix(), ConsumedAt: optionalTimestamp(confirmation.ConsumedAt), CancelledAt: optionalTimestamp(confirmation.CancelledAt),
	}
}

func fromConfirmationItem(item confirmationItem) (domain.Confirmation, error) {
	createdAt, err := parseOptionalTimestamp(item.CreatedAt)
	if err != nil {
		return domain.Confirmation{}, err
	}
	expiresAt, err := parseOptionalTimestamp(item.ExpiresAt)
	if err != nil {
		return domain.Confirmation{}, err
	}
	consumedAt, err := parseOptionalTimestamp(item.ConsumedAt)
	if err != nil {
		return domain.Confirmation{}, err
	}
	cancelledAt, err := parseOptionalTimestamp(item.CancelledAt)
	if err != nil {
		return domain.Confirmation{}, err
	}
	confirmation := domain.Confirmation{
		ID: item.ConfirmationID, Code: item.Code, SessionID: item.SessionID,
		OwnerDiscordUserID: item.OwnerDiscordUserID, GuildID: item.GuildID,
		Action: domain.ConfirmationAction(item.Action), BoundState: domain.LifecycleState(item.BoundState), BoundVersion: item.BoundVersion,
		Status: domain.ConfirmationStatus(item.Status), CreatedAt: createdAt, ExpiresAt: expiresAt, ConsumedAt: consumedAt, CancelledAt: cancelledAt,
	}
	return confirmation, confirmation.Validate()
}
