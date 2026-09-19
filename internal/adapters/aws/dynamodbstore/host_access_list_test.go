package dynamodbstore

import (
	"context"
	"encoding/json"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"strings"
	"testing"
	"time"
)

type attemptQuery struct {
	API
	pages  []*dynamodb.QueryOutput
	inputs []*dynamodb.QueryInput
}

func (client *attemptQuery) Query(_ context.Context, input *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	client.inputs = append(client.inputs, input)
	return client.pages[len(client.inputs)-1], nil
}

func TestAttemptQueryPaginatesOneStrongSessionPartitionAndRejectsForeignPayload(t *testing.T) {
	now := time.Now().UTC()
	scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: now.Add(time.Hour)}
	attempt := domain.HostAccessAttempt{Scope: scope, Revision: 1, Generation: 1, ManifestVersionID: "version", ManifestSHA256: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", ManifestSizeBytes: 100, ExpiresAt: now.Add(10 * time.Minute), DispatchState: domain.HostAccessPrepared}
	body, _ := json.Marshal(attempt)
	item := hostAccessKey("session", "operation", "attempt")
	item["payload"] = &types.AttributeValueMemberS{Value: string(body)}
	cursor := hostAccessKey("session", "previous", "previous")
	client := &attemptQuery{pages: []*dynamodb.QueryOutput{{LastEvaluatedKey: cursor}, {Items: []map[string]types.AttributeValue{item}}}}
	repository := New(client, "metadata")
	result, err := repository.ListHostAccessAttempts(context.Background(), "session")
	if err != nil || len(result) != 1 || len(client.inputs) != 2 || len(client.inputs[1].ExclusiveStartKey) == 0 {
		t.Fatal("attempt pages not fully queried", err)
	}
	for _, input := range client.inputs {
		if !aws.ToBool(input.ConsistentRead) || input.IndexName != nil || input.ExpressionAttributeValues[":pk"].(*types.AttributeValueMemberS).Value != sessionPartitionKey("session") || input.ExpressionAttributeValues[":prefix"].(*types.AttributeValueMemberS).Value != "HOSTACCESS#" {
			t.Fatal("query escaped strong session partition")
		}
	}
	attempt.Scope.SessionID = "foreign"
	body, _ = json.Marshal(attempt)
	item["payload"] = &types.AttributeValueMemberS{Value: string(body)}
	client.inputs = nil
	if _, err := repository.ListHostAccessAttempts(context.Background(), "session"); err == nil {
		t.Fatal("foreign partition payload accepted")
	}
}
