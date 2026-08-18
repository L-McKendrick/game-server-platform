package dynamodbstore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var _ ports.ReliabilityRepository = (*Repository)(nil)

type reconciliationItem struct {
	PK            string `dynamodbav:"pk"`
	SK            string `dynamodbav:"sk"`
	EntityType    string `dynamodbav:"entity_type"`
	SchemaVersion int    `dynamodbav:"schema_version"`
	FindingID     string `dynamodbav:"finding_id"`
	SessionID     string `dynamodbav:"session_id"`
	WorkflowID    string `dynamodbav:"workflow_id,omitempty"`
	Code          string `dynamodbav:"code"`
	Detail        string `dynamodbav:"detail,omitempty"`
	Action        string `dynamodbav:"action"`
	DetectedAt    string `dynamodbav:"detected_at"`
	ResolvedAt    string `dynamodbav:"resolved_at,omitempty"`
}

type deadLetterOperationItem struct {
	PK             string `dynamodbav:"pk"`
	SK             string `dynamodbav:"sk"`
	EntityType     string `dynamodbav:"entity_type"`
	SchemaVersion  int    `dynamodbav:"schema_version"`
	OperationID    string `dynamodbav:"operation_id"`
	RequestedBy    string `dynamodbav:"requested_by"`
	CorrelationID  string `dynamodbav:"correlation_id"`
	SourceARN      string `dynamodbav:"source_arn"`
	DestinationARN string `dynamodbav:"destination_arn,omitempty"`
	Queue          string `dynamodbav:"queue"`
	Action         string `dynamodbav:"action"`
	StartedAt      string `dynamodbav:"started_at"`
	CompletedAt    string `dynamodbav:"completed_at,omitempty"`
	MovedMessages  int64  `dynamodbav:"moved_messages,omitempty"`
}

func (repository *Repository) SaveWorkflowCancellation(ctx context.Context, workflow domain.Workflow, expectedStatus domain.WorkflowStatus, event domain.SessionEvent) error {
	if err := repository.validate(); err != nil {
		return err
	}
	if err := workflow.Validate(); err != nil {
		return err
	}
	if err := validateEvent(workflow.SessionID, event); err != nil {
		return err
	}
	workflowAttributes, err := attributevalue.MarshalMap(toWorkflowItem(workflow))
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
			{Put: &types.Put{TableName: aws.String(repository.tableName), Item: workflowAttributes,
				ConditionExpression:       aws.String("#status = :expected_status AND attribute_not_exists(cancel_requested_at)"),
				ExpressionAttributeNames:  map[string]string{"#status": "status"},
				ExpressionAttributeValues: map[string]types.AttributeValue{":expected_status": &types.AttributeValueMemberS{Value: string(expectedStatus)}}}},
			{Put: &types.Put{TableName: aws.String(repository.tableName), Item: eventAttributes, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")}},
		},
	})
	if err != nil {
		var cancelled *types.TransactionCanceledException
		if errors.As(err, &cancelled) {
			return domain.ErrConflict
		}
		return fmt.Errorf("save workflow cancellation: %w", err)
	}
	return nil
}

func (repository *Repository) ListActiveWorkflowSessions(ctx context.Context, limit int32) ([]domain.Session, error) {
	if err := repository.validate(); err != nil {
		return nil, err
	}
	if limit < 1 || limit > maximumGuildScanItems {
		return nil, fmt.Errorf("limit must be between 1 and %d", maximumGuildScanItems)
	}
	output, err := repository.client.Scan(ctx, &dynamodb.ScanInput{
		TableName: aws.String(repository.tableName), Limit: aws.Int32(limit),
		FilterExpression:          aws.String("entity_type = :session AND attribute_exists(active_workflow_id)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":session": &types.AttributeValueMemberS{Value: "Session"}},
	})
	if err != nil {
		return nil, fmt.Errorf("scan active workflow sessions: %w", err)
	}
	result := make([]domain.Session, 0, len(output.Items))
	for _, attributes := range output.Items {
		var item sessionItem
		if err := attributevalue.UnmarshalMap(attributes, &item); err != nil {
			return nil, err
		}
		session, err := fromSessionItem(item)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	return result, nil
}

func (repository *Repository) SaveReconciliationFinding(ctx context.Context, finding domain.ReconciliationFinding) error {
	if err := finding.Validate(); err != nil {
		return err
	}
	item := reconciliationItem{PK: sessionPartitionKey(finding.SessionID), SK: "RECONCILIATION#" + fixedTimestamp(finding.DetectedAt) + "#" + finding.ID,
		EntityType: "ReconciliationFinding", SchemaVersion: schemaVersion, FindingID: finding.ID, SessionID: finding.SessionID,
		WorkflowID: finding.WorkflowID, Code: finding.Code, Detail: finding.Detail, Action: string(finding.Action),
		DetectedAt: optionalTimestamp(finding.DetectedAt), ResolvedAt: optionalTimestamp(finding.ResolvedAt)}
	attributes, err := attributevalue.MarshalMap(item)
	if err != nil {
		return err
	}
	_, err = repository.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(repository.tableName), Item: attributes, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")})
	var conflict *types.ConditionalCheckFailedException
	if errors.As(err, &conflict) {
		return domain.ErrAlreadyExists
	}
	return err
}

func (repository *Repository) ListReconciliationFindings(ctx context.Context, sessionID string, limit int32) ([]domain.ReconciliationFinding, error) {
	if strings.TrimSpace(sessionID) == "" || limit < 1 {
		return nil, fmt.Errorf("session ID and positive limit are required")
	}
	output, err := repository.client.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(repository.tableName), Limit: aws.Int32(limit), ScanIndexForward: aws.Bool(false),
		KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: sessionPartitionKey(sessionID)}, ":prefix": &types.AttributeValueMemberS{Value: "RECONCILIATION#"},
		}})
	if err != nil {
		return nil, err
	}
	result := make([]domain.ReconciliationFinding, 0, len(output.Items))
	for _, attributes := range output.Items {
		var item reconciliationItem
		if err := attributevalue.UnmarshalMap(attributes, &item); err != nil {
			return nil, err
		}
		detected, err := parseOptionalTimestamp(item.DetectedAt)
		if err != nil {
			return nil, err
		}
		resolved, err := parseOptionalTimestamp(item.ResolvedAt)
		if err != nil {
			return nil, err
		}
		finding := domain.ReconciliationFinding{ID: item.FindingID, SessionID: item.SessionID, WorkflowID: item.WorkflowID, Code: item.Code, Detail: item.Detail, Action: domain.ReconciliationAction(item.Action), DetectedAt: detected, ResolvedAt: resolved}
		if err := finding.Validate(); err != nil {
			return nil, err
		}
		result = append(result, finding)
	}
	return result, nil
}

func (repository *Repository) SaveDeadLetterOperation(ctx context.Context, operation domain.DeadLetterOperation) error {
	if err := operation.Validate(); err != nil {
		return err
	}
	item := deadLetterOperationItem{PK: "RELIABILITY#DLQ", SK: "OPERATION#" + operation.ID, EntityType: "DeadLetterOperation", SchemaVersion: schemaVersion,
		OperationID: operation.ID, RequestedBy: operation.RequestedBy, CorrelationID: operation.CorrelationID, SourceARN: operation.SourceARN,
		DestinationARN: operation.DestinationARN, Queue: string(operation.Queue), Action: string(operation.Action), StartedAt: optionalTimestamp(operation.StartedAt), CompletedAt: optionalTimestamp(operation.CompletedAt), MovedMessages: operation.MovedMessages}
	attributes, err := attributevalue.MarshalMap(item)
	if err != nil {
		return err
	}
	_, err = repository.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(repository.tableName), Item: attributes, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")})
	var conflict *types.ConditionalCheckFailedException
	if errors.As(err, &conflict) {
		return domain.ErrAlreadyExists
	}
	return err
}

func (repository *Repository) GetDeadLetterOperation(ctx context.Context, operationID string) (domain.DeadLetterOperation, error) {
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: "RELIABILITY#DLQ"}, "sk": &types.AttributeValueMemberS{Value: "OPERATION#" + strings.TrimSpace(operationID)},
	}})
	if err != nil {
		return domain.DeadLetterOperation{}, err
	}
	if len(output.Item) == 0 {
		return domain.DeadLetterOperation{}, domain.ErrNotFound
	}
	var item deadLetterOperationItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.DeadLetterOperation{}, err
	}
	started, err := parseOptionalTimestamp(item.StartedAt)
	if err != nil {
		return domain.DeadLetterOperation{}, err
	}
	completed, err := parseOptionalTimestamp(item.CompletedAt)
	if err != nil {
		return domain.DeadLetterOperation{}, err
	}
	operation := domain.DeadLetterOperation{ID: item.OperationID, RequestedBy: item.RequestedBy, CorrelationID: item.CorrelationID, SourceARN: item.SourceARN, DestinationARN: item.DestinationARN, Queue: domain.DeadLetterQueue(item.Queue), Action: domain.DeadLetterAction(item.Action), StartedAt: started, CompletedAt: completed, MovedMessages: item.MovedMessages}
	return operation, operation.Validate()
}
