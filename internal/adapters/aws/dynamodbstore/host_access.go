package dynamodbstore

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func hostAccessKey(sessionID, operationID, attemptID string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: sessionPartitionKey(sessionID)}, "sk": &types.AttributeValueMemberS{Value: "HOSTACCESS#" + operationID + "#" + attemptID}}
}

func (repository *Repository) GetHostAccessAttempt(ctx context.Context, sessionID, operationID, attemptID string) (domain.HostAccessAttempt, error) {
	if err := repository.validate(); err != nil {
		return domain.HostAccessAttempt{}, err
	}
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(repository.tableName), Key: hostAccessKey(sessionID, operationID, attemptID), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return domain.HostAccessAttempt{}, err
	}
	if len(output.Item) == 0 {
		return domain.HostAccessAttempt{}, domain.ErrNotFound
	}
	payload, ok := output.Item["payload"].(*types.AttributeValueMemberS)
	if !ok {
		return domain.HostAccessAttempt{}, domain.ErrConflict
	}
	var attempt domain.HostAccessAttempt
	if err := json.Unmarshal([]byte(payload.Value), &attempt); err != nil {
		return domain.HostAccessAttempt{}, err
	}
	if attempt.Scope.SessionID != sessionID || attempt.Scope.OperationID != operationID || attempt.Scope.AttemptID != attemptID {
		return domain.HostAccessAttempt{}, domain.ErrConflict
	}
	return attempt, attempt.Validate()
}

func (repository *Repository) SaveHostAccessAttempt(ctx context.Context, next domain.HostAccessAttempt, expectedRevision, sessionVersion int64, now time.Time) error {
	if err := repository.validate(); err != nil {
		return err
	}
	if now.IsZero() || !next.Scope.DeadlineAt.After(now) || sessionVersion < 1 {
		return domain.ErrConflict
	}
	current, err := repository.GetHostAccessAttempt(ctx, next.Scope.SessionID, next.Scope.OperationID, next.Scope.AttemptID)
	if err != nil && !(expectedRevision == 0 && errors.Is(err, domain.ErrNotFound)) {
		return err
	}
	if err := domain.ValidateHostAccessUpdate(current, next, expectedRevision); err != nil {
		return err
	}
	payload, err := json.Marshal(next)
	if err != nil {
		return err
	}
	item := hostAccessKey(next.Scope.SessionID, next.Scope.OperationID, next.Scope.AttemptID)
	item["revision"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(next.Revision, 10)}
	item["payload"] = &types.AttributeValueMemberS{Value: string(payload)}
	condition := "revision = :revision"
	var revisionValues map[string]types.AttributeValue
	if expectedRevision == 0 {
		condition = "attribute_not_exists(pk)"
	} else {
		revisionValues = map[string]types.AttributeValue{":revision": &types.AttributeValueMemberN{Value: strconv.FormatInt(expectedRevision, 10)}}
	}
	scope := next.Scope
	// Legacy RFC3339Nano strings have variable fractional precision and cannot
	// safely compare arbitrary subsecond deadlines lexically. A whole-second
	// ceiling is conservative: it may reject an equal fractional lease, never
	// authorize a lease ending before the actual deadline.
	authorityDeadline := scope.DeadlineAt.UTC().Truncate(time.Second)
	if authorityDeadline.Before(scope.DeadlineAt) {
		authorityDeadline = authorityDeadline.Add(time.Second)
	}
	// Keep the zero fraction: a whole-second RFC3339 "Z" sorts after the
	// same second's positive fractions, incorrectly rejecting later leases.
	authorityDeadlineText := authorityDeadline.Format("2006-01-02T15:04:05.000000000Z")
	sessionCondition := "#version = :version AND guild_id = :guild AND instance_id = :instance AND active_workflow_id = :operation AND active_workflow_lease_expires_at >= :deadline"
	workflowCondition := "#status = :running AND attribute_not_exists(cancel_requested_at) AND attribute_not_exists(completed_at) AND lease_expires_at >= :deadline"
	if scope.CommandMode == "rollback" {
		session, readErr := repository.Get(ctx, scope.SessionID)
		if readErr != nil {
			return readErr
		}
		if session.Version != sessionVersion || !session.HostAccessRollbackAllowed(scope.OperationID) {
			return domain.ErrConflict
		}
		sessionCondition += " AND progress_state = :rollback AND progress_workflow_id = :operation"
	} else {
		sessionCondition += " AND (attribute_not_exists(progress_state) OR progress_state <> :rollback)"
		workflowCondition += " AND (attribute_not_exists(command_deadline_at) OR command_deadline_at >= :deadline)"
	}
	_, err = repository.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
		{ConditionCheck: &types.ConditionCheck{TableName: aws.String(repository.tableName), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: sessionPartitionKey(scope.SessionID)}, "sk": &types.AttributeValueMemberS{Value: sessionSortKey}}, ConditionExpression: aws.String(sessionCondition), ExpressionAttributeNames: map[string]string{"#version": "version"}, ExpressionAttributeValues: map[string]types.AttributeValue{
			":version": &types.AttributeValueMemberN{Value: strconv.FormatInt(sessionVersion, 10)}, ":guild": &types.AttributeValueMemberS{Value: scope.GuildID}, ":instance": &types.AttributeValueMemberS{Value: scope.InstanceID}, ":operation": &types.AttributeValueMemberS{Value: scope.OperationID}, ":deadline": &types.AttributeValueMemberS{Value: authorityDeadlineText}, ":rollback": &types.AttributeValueMemberS{Value: string(domain.ProgressRollingBack)},
		}}},
		{ConditionCheck: &types.ConditionCheck{TableName: aws.String(repository.tableName), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: sessionPartitionKey(scope.SessionID)}, "sk": &types.AttributeValueMemberS{Value: "WORKFLOW#" + scope.OperationID}}, ConditionExpression: aws.String(workflowCondition), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":running": &types.AttributeValueMemberS{Value: string(domain.WorkflowRunning)}, ":deadline": &types.AttributeValueMemberS{Value: authorityDeadlineText}}}},
		{Put: &types.Put{TableName: aws.String(repository.tableName), Item: item, ConditionExpression: aws.String(condition), ExpressionAttributeValues: revisionValues}},
	}})
	var cancelled *types.TransactionCanceledException
	if errors.As(err, &cancelled) {
		for _, reason := range cancelled.CancellationReasons {
			if aws.ToString(reason.Code) == "ConditionalCheckFailed" {
				return domain.ErrConflict
			}
		}
	}
	return err
}

var _ ports.HostAccessAttemptRepository = (*Repository)(nil)
