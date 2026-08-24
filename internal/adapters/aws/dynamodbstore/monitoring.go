package dynamodbstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var _ ports.MonitoringRepository = (*Repository)(nil)

func (repository *Repository) ListRunning(ctx context.Context, limit int32) ([]domain.Session, error) {
	if err := repository.validate(); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		limit = 25
	}
	sessions := make([]domain.Session, 0, limit)
	var startKey map[string]types.AttributeValue
	for len(sessions) < int(limit) {
		output, err := repository.client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(repository.tableName), Limit: aws.Int32(limit), ExclusiveStartKey: startKey, FilterExpression: aws.String("entity_type = :type AND lifecycle_state = :state"), ExpressionAttributeValues: map[string]types.AttributeValue{":type": &types.AttributeValueMemberS{Value: "Session"}, ":state": &types.AttributeValueMemberS{Value: string(domain.StateRunning)}}})
		if err != nil {
			return nil, fmt.Errorf("scan running sessions: %w", err)
		}
		for _, attributes := range output.Items {
			var item sessionItem
			if err := attributevalue.UnmarshalMap(attributes, &item); err != nil {
				return nil, fmt.Errorf("unmarshal running session: %w", err)
			}
			session, err := fromSessionItem(item)
			if err != nil {
				return nil, err
			}
			sessions = append(sessions, session)
			if len(sessions) == int(limit) {
				break
			}
		}
		if len(output.LastEvaluatedKey) == 0 {
			break
		}
		startKey = output.LastEvaluatedKey
	}
	return sessions, nil
}

func (repository *Repository) SaveMonitoring(ctx context.Context, session domain.Session, expectedVersion int64, events []domain.SessionEvent) error {
	if err := repository.validate(); err != nil {
		return err
	}
	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate session: %w", err)
	}
	if session.Version != expectedVersion+1 {
		return domain.ErrConflict
	}
	attributes, err := attributevalue.MarshalMap(toSessionItem(session))
	if err != nil {
		return err
	}
	items := []types.TransactWriteItem{{Put: &types.Put{TableName: aws.String(repository.tableName), Item: attributes, ConditionExpression: aws.String("#version = :version"), ExpressionAttributeNames: map[string]string{"#version": "version"}, ExpressionAttributeValues: map[string]types.AttributeValue{":version": &types.AttributeValueMemberN{Value: strconv.FormatInt(expectedVersion, 10)}}}}}
	token := session.ID + "-" + strconv.FormatInt(session.Version, 10)
	for _, event := range events {
		if err := validateEvent(session.ID, event); err != nil {
			return err
		}
		eventAttributes, err := attributevalue.MarshalMap(toEventItem(event))
		if err != nil {
			return err
		}
		items = append(items, types.TransactWriteItem{Put: &types.Put{TableName: aws.String(repository.tableName), Item: eventAttributes, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")}})
		token = event.ID
	}
	_, err = repository.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{ClientRequestToken: aws.String(token), TransactItems: items})
	if err != nil {
		var canceled *types.TransactionCanceledException
		if errors.As(err, &canceled) {
			return domain.ErrConflict
		}
		return fmt.Errorf("save monitoring: %w", err)
	}
	return nil
}
