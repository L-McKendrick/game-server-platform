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

var _ ports.ProvisioningRepository = (*Repository)(nil)
var _ ports.BootstrapRepository = (*Repository)(nil)

func (repository *Repository) SaveBootstrapStage(ctx context.Context, session domain.Session, expectedVersion int64, event domain.SessionEvent) error {
	return repository.SaveProvisioningStage(ctx, session, expectedVersion, event)
}

type capacitySlotItem struct {
	PK            string `dynamodbav:"pk"`
	SK            string `dynamodbav:"sk"`
	EntityType    string `dynamodbav:"entity_type"`
	SchemaVersion int    `dynamodbav:"schema_version"`
	SlotID        string `dynamodbav:"slot_id"`
	SessionID     string `dynamodbav:"session_id"`
	WorkflowID    string `dynamodbav:"workflow_id"`
	AcquiredAt    string `dynamodbav:"acquired_at"`
}

func (repository *Repository) CheckCapacity(ctx context.Context, sessionID string, limit int) error {
	if err := repository.validate(); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || limit < 1 || limit > 20 {
		return fmt.Errorf("valid session and capacity limit are required")
	}
	for slot := 0; slot < limit; slot++ {
		output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{
			TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true),
			Key: map[string]types.AttributeValue{
				"pk": &types.AttributeValueMemberS{Value: "CAPACITY#PROVISIONED"},
				"sk": &types.AttributeValueMemberS{Value: fmt.Sprintf("SLOT#slot-%d", slot)},
			},
		})
		if err != nil {
			return fmt.Errorf("check capacity slot: %w", err)
		}
		if len(output.Item) == 0 {
			return nil
		}
		var existing capacitySlotItem
		if err := attributevalue.UnmarshalMap(output.Item, &existing); err != nil {
			return fmt.Errorf("decode capacity slot: %w", err)
		}
		if existing.SessionID == sessionID {
			return nil
		}
	}
	return domain.ErrQuotaExceeded
}

func (repository *Repository) SaveProvisioningStage(ctx context.Context, session domain.Session, expectedVersion int64, event domain.SessionEvent) error {
	if err := repository.validate(); err != nil {
		return err
	}
	if err := session.Validate(); err != nil {
		return err
	}
	if session.Version != expectedVersion+1 {
		return fmt.Errorf("session version must advance exactly once")
	}
	if err := validateEvent(session.ID, event); err != nil {
		return err
	}
	sessionAttributes, err := attributevalue.MarshalMap(toSessionItem(session))
	if err != nil {
		return err
	}
	eventAttributes, err := attributevalue.MarshalMap(toEventItem(event))
	if err != nil {
		return err
	}
	_, err = repository.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		ClientRequestToken: aws.String(event.ID),
		TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{
				TableName: aws.String(repository.tableName), Item: sessionAttributes,
				ConditionExpression:      aws.String("#version = :expected_version AND active_workflow_id = :workflow_id"),
				ExpressionAttributeNames: map[string]string{"#version": "version"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":expected_version": &types.AttributeValueMemberN{Value: strconv.FormatInt(expectedVersion, 10)},
					":workflow_id":      &types.AttributeValueMemberS{Value: session.ActiveWorkflowID},
				},
			}},
			{Put: &types.Put{
				TableName: aws.String(repository.tableName), Item: eventAttributes,
				ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)"),
			}},
		},
	})
	if err != nil {
		var cancelled *types.TransactionCanceledException
		if errors.As(err, &cancelled) {
			return domain.ErrConflict
		}
		return fmt.Errorf("save provisioning stage: %w", err)
	}
	return nil
}

func (repository *Repository) AcquireCapacitySlot(ctx context.Context, sessionID string, workflowID string, limit int, now time.Time) (string, error) {
	if err := repository.validate(); err != nil {
		return "", err
	}
	sessionID = strings.TrimSpace(sessionID)
	workflowID = strings.TrimSpace(workflowID)
	if sessionID == "" || workflowID == "" || limit < 1 || limit > 20 {
		return "", fmt.Errorf("valid session, workflow, and capacity limit are required")
	}
	for slot := 0; slot < limit; slot++ {
		slotID := fmt.Sprintf("slot-%d", slot)
		item, err := attributevalue.MarshalMap(capacitySlotItem{
			PK: "CAPACITY#PROVISIONED", SK: "SLOT#" + slotID, EntityType: "CapacitySlot",
			SchemaVersion: schemaVersion, SlotID: slotID, SessionID: sessionID,
			WorkflowID: workflowID, AcquiredAt: now.UTC().Format(time.RFC3339Nano),
		})
		if err != nil {
			return "", err
		}
		_, err = repository.client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(repository.tableName), Item: item,
			ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)"),
		})
		if err == nil {
			return slotID, nil
		}
		var conditional *types.ConditionalCheckFailedException
		if !errors.As(err, &conditional) {
			return "", fmt.Errorf("reserve capacity slot: %w", err)
		}
		existing, getErr := repository.client.GetItem(ctx, &dynamodb.GetItemInput{
			TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true),
			Key: map[string]types.AttributeValue{
				"pk": &types.AttributeValueMemberS{Value: "CAPACITY#PROVISIONED"},
				"sk": &types.AttributeValueMemberS{Value: "SLOT#" + slotID},
			},
		})
		if getErr != nil {
			return "", fmt.Errorf("read capacity slot: %w", getErr)
		}
		var existingItem capacitySlotItem
		if err := attributevalue.UnmarshalMap(existing.Item, &existingItem); err != nil {
			return "", err
		}
		if existingItem.SessionID == sessionID {
			return slotID, nil
		}
	}
	return "", domain.ErrQuotaExceeded
}

func (repository *Repository) ReleaseCapacitySlot(ctx context.Context, slotID string, sessionID string) error {
	slotID = strings.TrimSpace(slotID)
	sessionID = strings.TrimSpace(sessionID)
	if slotID == "" || sessionID == "" {
		return fmt.Errorf("slot and session IDs are required")
	}
	_, err := repository.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(repository.tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: "CAPACITY#PROVISIONED"},
			"sk": &types.AttributeValueMemberS{Value: "SLOT#" + slotID},
		},
		ConditionExpression: aws.String("session_id = :session_id"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":session_id": &types.AttributeValueMemberS{Value: sessionID},
		},
	})
	if err != nil {
		var conditional *types.ConditionalCheckFailedException
		if errors.As(err, &conditional) {
			return nil
		}
		return fmt.Errorf("release capacity slot: %w", err)
	}
	return nil
}
