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

var _ ports.ResetRepository = (*Repository)(nil)

type resetConfirmationItem struct {
	PK             string `dynamodbav:"pk"`
	SK             string `dynamodbav:"sk"`
	EntityType     string `dynamodbav:"entity_type"`
	SchemaVersion  int    `dynamodbav:"schema_version"`
	ID             string `dynamodbav:"confirmation_id"`
	Code           string `dynamodbav:"code"`
	Environment    string `dynamodbav:"environment"`
	GuildID        string `dynamodbav:"guild_id"`
	RequestedBy    string `dynamodbav:"requested_by"`
	CreatedAt      string `dynamodbav:"created_at"`
	ExpiresAt      string `dynamodbav:"expires_at"`
	ExpiresAtEpoch int64  `dynamodbav:"expires_at_epoch"`
	ConsumedAt     string `dynamodbav:"consumed_at,omitempty"`
}

type resetOperationItem struct {
	PK              string `dynamodbav:"pk"`
	SK              string `dynamodbav:"sk"`
	EntityType      string `dynamodbav:"entity_type"`
	SchemaVersion   int    `dynamodbav:"schema_version"`
	ID              string `dynamodbav:"operation_id"`
	Environment     string `dynamodbav:"environment"`
	GuildID         string `dynamodbav:"guild_id"`
	RequestedBy     string `dynamodbav:"requested_by"`
	CorrelationID   string `dynamodbav:"correlation_id"`
	Status          string `dynamodbav:"status"`
	Stage           string `dynamodbav:"stage"`
	Version         int64  `dynamodbav:"version"`
	StartedAt       string `dynamodbav:"started_at"`
	UpdatedAt       string `dynamodbav:"updated_at"`
	CompletedAt     string `dynamodbav:"completed_at,omitempty"`
	DeletedSessions int    `dynamodbav:"deleted_sessions,omitempty"`
	DeletedObjects  int    `dynamodbav:"deleted_objects,omitempty"`
	ErrorCode       string `dynamodbav:"error_code,omitempty"`
	ErrorDetail     string `dynamodbav:"error_detail,omitempty"`
}

func resetPK(environment string) string    { return "RESET#" + strings.TrimSpace(environment) }
func resetOperationPK(id string) string    { return "RESET_OPERATION#" + strings.TrimSpace(id) }
func resetConfirmationPK(id string) string { return "RESET_CONFIRMATION#" + strings.TrimSpace(id) }

func (repository *Repository) CreateResetConfirmation(ctx context.Context, confirmation domain.ResetConfirmation) error {
	if err := confirmation.Validate(); err != nil {
		return err
	}
	item, err := attributevalue.MarshalMap(toResetConfirmationItem(confirmation))
	if err != nil {
		return err
	}
	_, err = repository.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(repository.tableName), Item: item,
		ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)"),
	})
	if err != nil {
		var conditional *types.ConditionalCheckFailedException
		if errors.As(err, &conditional) {
			return domain.ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (repository *Repository) GetResetConfirmation(ctx context.Context, confirmationID string) (domain.ResetConfirmation, error) {
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: resetConfirmationPK(confirmationID)}, "sk": &types.AttributeValueMemberS{Value: "METADATA"},
	}})
	if err != nil {
		return domain.ResetConfirmation{}, err
	}
	if len(output.Item) == 0 {
		return domain.ResetConfirmation{}, domain.ErrNotFound
	}
	var item resetConfirmationItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.ResetConfirmation{}, err
	}
	return fromResetConfirmationItem(item)
}

func (repository *Repository) ConsumeResetConfirmation(ctx context.Context, confirmationID, actorID, guildID, phrase string, operation domain.ResetOperation, now time.Time) (domain.ResetOperation, error) {
	confirmation, err := repository.GetResetConfirmation(ctx, confirmationID)
	if err != nil {
		return domain.ResetOperation{}, err
	}
	if err := confirmation.Check(actorID, guildID, phrase, now); err != nil {
		return domain.ResetOperation{}, err
	}
	confirmation.ConsumedAt = now.UTC()
	confirmationAttributes, err := attributevalue.MarshalMap(toResetConfirmationItem(confirmation))
	if err != nil {
		return domain.ResetOperation{}, err
	}
	operationAttributes, err := attributevalue.MarshalMap(toResetOperationItem(operation))
	if err != nil {
		return domain.ResetOperation{}, err
	}
	lock := map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: resetPK(operation.Environment)}, "sk": &types.AttributeValueMemberS{Value: "LOCK#ACTIVE"},
		"entity_type": &types.AttributeValueMemberS{Value: "ResetLock"}, "schema_version": &types.AttributeValueMemberN{Value: strconv.Itoa(schemaVersion)},
		"operation_id": &types.AttributeValueMemberS{Value: operation.ID}, "environment": &types.AttributeValueMemberS{Value: operation.Environment},
	}
	_, err = repository.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{Put: &types.Put{TableName: aws.String(repository.tableName), Item: confirmationAttributes,
			ConditionExpression: aws.String("attribute_not_exists(consumed_at) AND requested_by = :actor AND guild_id = :guild AND expires_at_epoch > :now"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":actor": &types.AttributeValueMemberS{Value: strings.TrimSpace(actorID)}, ":guild": &types.AttributeValueMemberS{Value: strings.TrimSpace(guildID)}, ":now": &types.AttributeValueMemberN{Value: strconv.FormatInt(now.UTC().Unix(), 10)},
			}}},
		{Put: &types.Put{TableName: aws.String(repository.tableName), Item: operationAttributes, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")}},
		{Put: &types.Put{TableName: aws.String(repository.tableName), Item: lock, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")}},
	}})
	if err != nil {
		var cancelled *types.TransactionCanceledException
		if errors.As(err, &cancelled) {
			if existing, getErr := repository.GetResetOperation(ctx, operation.ID); getErr == nil {
				return existing, domain.ErrAlreadyExists
			}
			if active, activeErr := repository.GetActiveReset(ctx, operation.Environment); activeErr == nil && active.ID != operation.ID {
				return domain.ResetOperation{}, domain.ErrCommandInProgress
			}
			return domain.ResetOperation{}, domain.ErrConfirmationStateDrift
		}
		return domain.ResetOperation{}, fmt.Errorf("start reset transaction: %w", err)
	}
	return operation, nil
}

func (repository *Repository) GetResetOperation(ctx context.Context, operationID string) (domain.ResetOperation, error) {
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: resetOperationPK(operationID)}, "sk": &types.AttributeValueMemberS{Value: "METADATA"},
	}})
	if err != nil {
		return domain.ResetOperation{}, err
	}
	if len(output.Item) == 0 {
		return domain.ResetOperation{}, domain.ErrNotFound
	}
	return unmarshalResetOperation(output.Item)
}

func (repository *Repository) GetActiveReset(ctx context.Context, environment string) (domain.ResetOperation, error) {
	lock, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: resetPK(environment)}, "sk": &types.AttributeValueMemberS{Value: "LOCK#ACTIVE"},
	}})
	if err != nil {
		return domain.ResetOperation{}, err
	}
	if len(lock.Item) == 0 {
		return domain.ResetOperation{}, domain.ErrNotFound
	}
	id, ok := lock.Item["operation_id"].(*types.AttributeValueMemberS)
	if !ok || strings.TrimSpace(id.Value) == "" {
		return domain.ResetOperation{}, domain.ErrConflict
	}
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: resetOperationPK(id.Value)}, "sk": &types.AttributeValueMemberS{Value: "METADATA"},
	}})
	if err != nil {
		return domain.ResetOperation{}, err
	}
	if len(output.Item) == 0 {
		return domain.ResetOperation{}, domain.ErrConflict
	}
	operation, err := unmarshalResetOperation(output.Item)
	if err != nil {
		return domain.ResetOperation{}, err
	}
	if !operation.Active() {
		return domain.ResetOperation{}, domain.ErrConflict
	}
	return operation, nil
}

func (repository *Repository) GetLatestReset(ctx context.Context, environment string) (domain.ResetOperation, error) {
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: resetPK(environment)}, "sk": &types.AttributeValueMemberS{Value: "AUDIT#LATEST"},
	}})
	if err != nil {
		return domain.ResetOperation{}, err
	}
	if len(output.Item) == 0 {
		return domain.ResetOperation{}, domain.ErrNotFound
	}
	id, ok := output.Item["operation_id"].(*types.AttributeValueMemberS)
	if !ok || strings.TrimSpace(id.Value) == "" {
		return domain.ResetOperation{}, domain.ErrConflict
	}
	return repository.GetResetOperation(ctx, id.Value)
}

func (repository *Repository) SaveResetOperation(ctx context.Context, operation domain.ResetOperation, expectedVersion int64) error {
	if err := operation.Validate(); err != nil {
		return err
	}
	attributes, err := attributevalue.MarshalMap(toResetOperationItem(operation))
	if err != nil {
		return err
	}
	put := &types.Put{TableName: aws.String(repository.tableName), Item: attributes, ConditionExpression: aws.String("#version = :expected"),
		ExpressionAttributeNames: map[string]string{"#version": "version"}, ExpressionAttributeValues: map[string]types.AttributeValue{":expected": &types.AttributeValueMemberN{Value: strconv.FormatInt(expectedVersion, 10)}}}
	if operation.Active() {
		_, err = repository.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: put.TableName, Item: put.Item, ConditionExpression: put.ConditionExpression, ExpressionAttributeNames: put.ExpressionAttributeNames, ExpressionAttributeValues: put.ExpressionAttributeValues})
	} else {
		audit := map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: resetPK(operation.Environment)}, "sk": &types.AttributeValueMemberS{Value: "AUDIT#LATEST"},
			"entity_type": &types.AttributeValueMemberS{Value: "ResetAudit"}, "schema_version": &types.AttributeValueMemberN{Value: strconv.Itoa(schemaVersion)},
			"operation_id": &types.AttributeValueMemberS{Value: operation.ID}, "environment": &types.AttributeValueMemberS{Value: operation.Environment},
		}
		_, err = repository.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
			{Put: put}, {Put: &types.Put{TableName: aws.String(repository.tableName), Item: audit}}, {Delete: &types.Delete{TableName: aws.String(repository.tableName), Key: map[string]types.AttributeValue{
				"pk": &types.AttributeValueMemberS{Value: resetPK(operation.Environment)}, "sk": &types.AttributeValueMemberS{Value: "LOCK#ACTIVE"},
			}, ConditionExpression: aws.String("operation_id = :id"), ExpressionAttributeValues: map[string]types.AttributeValue{":id": &types.AttributeValueMemberS{Value: operation.ID}}}},
		}})
	}
	if err != nil {
		return domain.ErrConflict
	}
	return nil
}

func toResetConfirmationItem(value domain.ResetConfirmation) resetConfirmationItem {
	return resetConfirmationItem{PK: resetConfirmationPK(value.ID), SK: "METADATA", EntityType: "ResetConfirmation", SchemaVersion: schemaVersion,
		ID: value.ID, Code: value.Code, Environment: value.Environment, GuildID: value.GuildID, RequestedBy: value.RequestedBy,
		CreatedAt: optionalTimestamp(value.CreatedAt), ExpiresAt: optionalTimestamp(value.ExpiresAt), ExpiresAtEpoch: value.ExpiresAt.UTC().Unix(), ConsumedAt: optionalTimestamp(value.ConsumedAt)}
}

func fromResetConfirmationItem(item resetConfirmationItem) (domain.ResetConfirmation, error) {
	created, err := parseOptionalTimestamp(item.CreatedAt)
	if err != nil {
		return domain.ResetConfirmation{}, err
	}
	expires, err := parseOptionalTimestamp(item.ExpiresAt)
	if err != nil {
		return domain.ResetConfirmation{}, err
	}
	consumed, err := parseOptionalTimestamp(item.ConsumedAt)
	if err != nil {
		return domain.ResetConfirmation{}, err
	}
	value := domain.ResetConfirmation{ID: item.ID, Code: item.Code, Environment: item.Environment, GuildID: item.GuildID, RequestedBy: item.RequestedBy, CreatedAt: created, ExpiresAt: expires, ConsumedAt: consumed}
	return value, value.Validate()
}

func toResetOperationItem(value domain.ResetOperation) resetOperationItem {
	return resetOperationItem{PK: resetOperationPK(value.ID), SK: "METADATA", EntityType: "ResetOperation", SchemaVersion: schemaVersion,
		ID: value.ID, Environment: value.Environment, GuildID: value.GuildID, RequestedBy: value.RequestedBy, CorrelationID: value.CorrelationID,
		Status: string(value.Status), Stage: value.Stage, Version: value.Version, StartedAt: optionalTimestamp(value.StartedAt), UpdatedAt: optionalTimestamp(value.UpdatedAt), CompletedAt: optionalTimestamp(value.CompletedAt),
		DeletedSessions: value.DeletedSessions, DeletedObjects: value.DeletedObjects, ErrorCode: value.ErrorCode, ErrorDetail: value.ErrorDetail}
}

func unmarshalResetOperation(attributes map[string]types.AttributeValue) (domain.ResetOperation, error) {
	var item resetOperationItem
	if err := attributevalue.UnmarshalMap(attributes, &item); err != nil {
		return domain.ResetOperation{}, err
	}
	started, err := parseOptionalTimestamp(item.StartedAt)
	if err != nil {
		return domain.ResetOperation{}, err
	}
	updated, err := parseOptionalTimestamp(item.UpdatedAt)
	if err != nil {
		return domain.ResetOperation{}, err
	}
	completed, err := parseOptionalTimestamp(item.CompletedAt)
	if err != nil {
		return domain.ResetOperation{}, err
	}
	value := domain.ResetOperation{ID: item.ID, Environment: item.Environment, GuildID: item.GuildID, RequestedBy: item.RequestedBy, CorrelationID: item.CorrelationID,
		Status: domain.ResetStatus(item.Status), Stage: item.Stage, Version: item.Version, StartedAt: started, UpdatedAt: updated, CompletedAt: completed,
		DeletedSessions: item.DeletedSessions, DeletedObjects: item.DeletedObjects, ErrorCode: item.ErrorCode, ErrorDetail: item.ErrorDetail}
	return value, value.Validate()
}
