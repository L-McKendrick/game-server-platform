package dynamodbstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var _ ports.WorkflowRepository = (*Repository)(nil)

type workflowItem struct {
	PK                 string `dynamodbav:"pk"`
	SK                 string `dynamodbav:"sk"`
	EntityType         string `dynamodbav:"entity_type"`
	SchemaVersion      int    `dynamodbav:"schema_version"`
	WorkflowID         string `dynamodbav:"workflow_id"`
	SessionID          string `dynamodbav:"session_id"`
	WorkflowType       string `dynamodbav:"workflow_type"`
	Status             string `dynamodbav:"status"`
	RequestedBy        string `dynamodbav:"requested_by"`
	CorrelationID      string `dynamodbav:"correlation_id"`
	ExpectedVersion    int64  `dynamodbav:"expected_session_version"`
	ExecutionARN       string `dynamodbav:"step_functions_execution_arn,omitempty"`
	CurrentStage       string `dynamodbav:"current_stage,omitempty"`
	ErrorCode          string `dynamodbav:"error_code,omitempty"`
	ErrorMessage       string `dynamodbav:"error_message,omitempty"`
	CommandID          string `dynamodbav:"command_id,omitempty"`
	CommandDeadlineAt  string `dynamodbav:"command_deadline_at,omitempty"`
	ContentTarget      string `dynamodbav:"content_target,omitempty"`
	ContentDigest      string `dynamodbav:"content_digest,omitempty"`
	InstanceID         string `dynamodbav:"instance_id,omitempty"`
	StartedAt          string `dynamodbav:"started_at"`
	CompletedAt        string `dynamodbav:"completed_at,omitempty"`
	LeaseExpiresAt     string `dynamodbav:"lease_expires_at"`
	CancelRequestedAt  string `dynamodbav:"cancel_requested_at,omitempty"`
	CancelRequestedBy  string `dynamodbav:"cancel_requested_by,omitempty"`
	RetryAttempt       int    `dynamodbav:"retry_attempt,omitempty"`
	RetryMaxAttempts   int    `dynamodbav:"retry_max_attempts,omitempty"`
	RetryLastAttemptAt string `dynamodbav:"retry_last_attempt_at,omitempty"`
	RetryNextAttemptAt string `dynamodbav:"retry_next_attempt_at,omitempty"`
}

func (repository *Repository) AcquireWorkflow(
	ctx context.Context,
	session domain.Session,
	expectedVersion int64,
	workflow domain.Workflow,
	event domain.SessionEvent,
) error {
	if err := repository.validate(); err != nil {
		return err
	}
	if err := session.Validate(); err != nil {
		return err
	}
	if err := workflow.Validate(); err != nil {
		return err
	}
	if workflow.SessionID != session.ID || event.SessionID != session.ID {
		return fmt.Errorf("workflow, event, and session IDs must match")
	}
	if err := validateEvent(session.ID, event); err != nil {
		return err
	}
	if session.Version != expectedVersion+1 || session.ActiveWorkflowID != workflow.ID {
		return fmt.Errorf("invalid workflow lock mutation")
	}
	sessionAttributes, err := attributevalue.MarshalMap(toSessionItem(session))
	if err != nil {
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
		ClientRequestToken: aws.String(workflow.ID),
		TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{
				TableName: aws.String(repository.tableName), Item: sessionAttributes,
				ConditionExpression:      aws.String("#version = :expected_version AND (attribute_not_exists(active_workflow_id) OR active_workflow_lease_expires_at < :now)"),
				ExpressionAttributeNames: map[string]string{"#version": "version"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":expected_version": &types.AttributeValueMemberN{Value: strconv.FormatInt(expectedVersion, 10)},
					":now":              &types.AttributeValueMemberS{Value: fixedTimestamp(workflow.StartedAt)},
				},
			}},
			{Put: &types.Put{TableName: aws.String(repository.tableName), Item: workflowAttributes, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")}},
			{Put: &types.Put{TableName: aws.String(repository.tableName), Item: eventAttributes, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")}},
		},
	})
	if err != nil {
		var cancelled *types.TransactionCanceledException
		if errors.As(err, &cancelled) {
			return domain.ErrWorkflowLocked
		}
		return fmt.Errorf("acquire workflow transaction: %w", err)
	}
	return nil
}

func (repository *Repository) CompleteWorkflow(
	ctx context.Context,
	session domain.Session,
	expectedVersion int64,
	workflow domain.Workflow,
	event domain.SessionEvent,
) error {
	if err := repository.validate(); err != nil {
		return err
	}
	if err := session.Validate(); err != nil {
		return err
	}
	if err := workflow.Validate(); err != nil {
		return err
	}
	if workflow.SessionID != session.ID || event.SessionID != session.ID {
		return fmt.Errorf("workflow, event, and session IDs must match")
	}
	if err := validateEvent(session.ID, event); err != nil {
		return err
	}
	if session.Version != expectedVersion+1 || session.ActiveWorkflowID != "" {
		return fmt.Errorf("invalid workflow release mutation")
	}
	sessionAttributes, err := attributevalue.MarshalMap(toSessionItem(session))
	if err != nil {
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
			{Put: &types.Put{
				TableName: aws.String(repository.tableName), Item: sessionAttributes,
				ConditionExpression:      aws.String("#version = :expected_version AND active_workflow_id = :workflow_id"),
				ExpressionAttributeNames: map[string]string{"#version": "version"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":expected_version": &types.AttributeValueMemberN{Value: strconv.FormatInt(expectedVersion, 10)},
					":workflow_id":      &types.AttributeValueMemberS{Value: workflow.ID},
				},
			}},
			{Put: &types.Put{TableName: aws.String(repository.tableName), Item: workflowAttributes, ConditionExpression: aws.String("attribute_exists(pk) AND attribute_exists(sk)")}},
			{Put: &types.Put{TableName: aws.String(repository.tableName), Item: eventAttributes, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")}},
		},
	})
	if err != nil {
		return fmt.Errorf("complete workflow transaction: %w", err)
	}
	return nil
}

func (repository *Repository) GetWorkflow(ctx context.Context, sessionID string, workflowID string) (domain.Workflow, error) {
	if err := repository.validate(); err != nil {
		return domain.Workflow{}, err
	}
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: sessionPartitionKey(strings.TrimSpace(sessionID))},
			"sk": &types.AttributeValueMemberS{Value: "WORKFLOW#" + strings.TrimSpace(workflowID)},
		},
	})
	if err != nil {
		return domain.Workflow{}, err
	}
	if len(output.Item) == 0 {
		return domain.Workflow{}, domain.ErrNotFound
	}
	var item workflowItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.Workflow{}, err
	}
	return fromWorkflowItem(item)
}

func (repository *Repository) SetWorkflowExecution(ctx context.Context, workflow domain.Workflow, expectedStatus domain.WorkflowStatus) error {
	if err := repository.validate(); err != nil {
		return err
	}
	if err := workflow.Validate(); err != nil {
		return err
	}
	attributes, err := attributevalue.MarshalMap(toWorkflowItem(workflow))
	if err != nil {
		return err
	}
	_, err = repository.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(repository.tableName), Item: attributes,
		ConditionExpression:      aws.String("#status = :expected_status"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":expected_status": &types.AttributeValueMemberS{Value: string(expectedStatus)},
		},
	})
	if err != nil {
		return fmt.Errorf("set workflow execution: %w", err)
	}
	return nil
}

func toWorkflowItem(workflow domain.Workflow) workflowItem {
	return workflowItem{
		PK: sessionPartitionKey(workflow.SessionID), SK: "WORKFLOW#" + workflow.ID,
		EntityType: "Workflow", SchemaVersion: schemaVersion,
		WorkflowID: workflow.ID, SessionID: workflow.SessionID, WorkflowType: workflow.Type,
		Status: string(workflow.Status), RequestedBy: workflow.RequestedBy,
		CorrelationID: workflow.CorrelationID, ExpectedVersion: workflow.ExpectedVersion,
		ExecutionARN: workflow.ExecutionARN, CurrentStage: workflow.CurrentStage,
		ErrorCode: workflow.ErrorCode, ErrorMessage: workflow.ErrorMessage,
		CommandID: workflow.CommandID, CommandDeadlineAt: optionalTimestamp(workflow.CommandDeadlineAt),
		ContentTarget: workflow.ContentTarget, ContentDigest: workflow.ContentDigest, InstanceID: workflow.InstanceID,
		StartedAt: optionalTimestamp(workflow.StartedAt), CompletedAt: optionalTimestamp(workflow.CompletedAt),
		LeaseExpiresAt:    optionalTimestamp(workflow.LeaseExpiresAt),
		CancelRequestedAt: optionalTimestamp(workflow.CancelRequestedAt), CancelRequestedBy: workflow.CancelRequestedBy,
		RetryAttempt: workflow.Retry.Attempt, RetryMaxAttempts: workflow.Retry.MaxAttempts,
		RetryLastAttemptAt: optionalTimestamp(workflow.Retry.LastAttemptAt), RetryNextAttemptAt: optionalTimestamp(workflow.Retry.NextAttemptAt),
	}
}

func fromWorkflowItem(item workflowItem) (domain.Workflow, error) {
	startedAt, err := parseOptionalTimestamp(item.StartedAt)
	if err != nil {
		return domain.Workflow{}, err
	}
	completedAt, err := parseOptionalTimestamp(item.CompletedAt)
	if err != nil {
		return domain.Workflow{}, err
	}
	commandDeadlineAt, err := parseOptionalTimestamp(item.CommandDeadlineAt)
	if err != nil {
		return domain.Workflow{}, err
	}
	leaseExpiresAt, err := parseOptionalTimestamp(item.LeaseExpiresAt)
	if err != nil {
		return domain.Workflow{}, err
	}
	cancelRequestedAt, err := parseOptionalTimestamp(item.CancelRequestedAt)
	if err != nil {
		return domain.Workflow{}, err
	}
	retryLastAttemptAt, err := parseOptionalTimestamp(item.RetryLastAttemptAt)
	if err != nil {
		return domain.Workflow{}, err
	}
	retryNextAttemptAt, err := parseOptionalTimestamp(item.RetryNextAttemptAt)
	if err != nil {
		return domain.Workflow{}, err
	}
	workflow := domain.Workflow{
		ID: item.WorkflowID, SessionID: item.SessionID, Type: item.WorkflowType,
		Status: domain.WorkflowStatus(item.Status), RequestedBy: item.RequestedBy,
		CorrelationID: item.CorrelationID, ExpectedVersion: item.ExpectedVersion,
		ExecutionARN: item.ExecutionARN, CurrentStage: item.CurrentStage,
		ErrorCode: item.ErrorCode, ErrorMessage: item.ErrorMessage,
		CommandID: item.CommandID, CommandDeadlineAt: commandDeadlineAt,
		ContentTarget: item.ContentTarget, ContentDigest: item.ContentDigest, InstanceID: item.InstanceID,
		StartedAt: startedAt, CompletedAt: completedAt, LeaseExpiresAt: leaseExpiresAt,
		CancelRequestedAt: cancelRequestedAt, CancelRequestedBy: item.CancelRequestedBy,
		Retry: domain.WorkflowRetry{Attempt: item.RetryAttempt, MaxAttempts: item.RetryMaxAttempts, LastAttemptAt: retryLastAttemptAt, NextAttemptAt: retryNextAttemptAt},
	}
	return workflow, workflow.Validate()
}
