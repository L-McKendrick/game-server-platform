package dynamodbstore

import (
	"context"
	"encoding/json"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"strings"
)

func (repository *Repository) ListHostAccessAttempts(ctx context.Context, sessionID string) ([]domain.HostAccessAttempt, error) {
	if err := repository.validate(); err != nil {
		return nil, err
	}
	if sessionID == "" || strings.ContainsAny(sessionID, "/\\") || strings.Contains(sessionID, "..") {
		return nil, domain.ErrConflict
	}
	var result []domain.HostAccessAttempt
	var cursor map[string]types.AttributeValue
	seen := map[string]bool{}
	for {
		page, err := repository.client.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true), KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: sessionPartitionKey(sessionID)}, ":prefix": &types.AttributeValueMemberS{Value: "HOSTACCESS#"}}, ExclusiveStartKey: cursor})
		if err != nil {
			return nil, err
		}
		if page == nil {
			return nil, domain.ErrConflict
		}
		for _, item := range page.Items {
			payload, ok := item["payload"].(*types.AttributeValueMemberS)
			if !ok {
				return nil, domain.ErrConflict
			}
			var attempt domain.HostAccessAttempt
			if json.Unmarshal([]byte(payload.Value), &attempt) != nil || attempt.Validate() != nil || attempt.Scope.SessionID != sessionID {
				return nil, domain.ErrConflict
			}
			expected := hostAccessKey(sessionID, attempt.Scope.OperationID, attempt.Scope.AttemptID)
			for _, key := range []string{"pk", "sk"} {
				actual, ok := item[key].(*types.AttributeValueMemberS)
				if !ok || actual.Value != expected[key].(*types.AttributeValueMemberS).Value {
					return nil, domain.ErrConflict
				}
			}
			identity := attempt.Scope.OperationID + "#" + attempt.Scope.AttemptID
			if seen[identity] {
				return nil, domain.ErrConflict
			}
			seen[identity] = true
			result = append(result, attempt)
		}
		if len(page.LastEvaluatedKey) == 0 {
			return result, nil
		}
		next, _ := json.Marshal(page.LastEvaluatedKey)
		key := "cursor:" + string(next)
		if seen[key] {
			return nil, domain.ErrConflict
		}
		seen[key] = true
		cursor = page.LastEvaluatedKey
	}
}
