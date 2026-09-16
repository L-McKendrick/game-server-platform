package dynamodbstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type GuildSessionIndexBackfillResult struct {
	Scanned    int32
	Updated    int
	Conflicted int
	NextCursor string
}

type GuildSessionIndexVerification struct {
	SourceCount  int
	IndexedCount int
	MissingKeys  int
	Mismatched   map[string][2]int
}

func (verification GuildSessionIndexVerification) Valid() bool {
	return verification.MissingKeys == 0 && verification.SourceCount == verification.IndexedCount && len(verification.Mismatched) == 0
}

// BackfillGuildSessionIndexPage conditionally projects one exhaustive scan
// page. A concurrent session change wins; replay then observes and projects the
// new authoritative version instead of restoring stale index keys.
func (repository *Repository) BackfillGuildSessionIndexPage(ctx context.Context, cursor string, limit int32) (GuildSessionIndexBackfillResult, error) {
	if err := repository.validate(); err != nil {
		return GuildSessionIndexBackfillResult{}, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		return GuildSessionIndexBackfillResult{}, fmt.Errorf("backfill page limit must not exceed 1000")
	}
	startKey, err := decodeMigrationCursor(cursor)
	if err != nil {
		return GuildSessionIndexBackfillResult{}, err
	}
	output, err := repository.client.Scan(ctx, &dynamodb.ScanInput{
		TableName: repository.tableNamePointer(), Limit: aws.Int32(limit), ExclusiveStartKey: startKey,
		FilterExpression:          aws.String("entity_type = :type"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":type": &types.AttributeValueMemberS{Value: "Session"}},
	})
	if err != nil {
		return GuildSessionIndexBackfillResult{}, fmt.Errorf("scan session metadata for guild index: %w", err)
	}
	result := GuildSessionIndexBackfillResult{Scanned: output.ScannedCount}
	for _, attributes := range output.Items {
		var item sessionItem
		if err := attributevalue.UnmarshalMap(attributes, &item); err != nil {
			return result, fmt.Errorf("decode session metadata for guild index: %w", err)
		}
		session, err := fromSessionItem(item)
		if err != nil {
			return result, err
		}
		projected := toSessionItem(session)
		_, err = repository.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: repository.tableNamePointer(),
			Key: map[string]types.AttributeValue{
				"pk": &types.AttributeValueMemberS{Value: item.PK},
				"sk": &types.AttributeValueMemberS{Value: item.SK},
			},
			UpdateExpression:         aws.String("SET gsi2pk = :gsi2pk, gsi2sk = :gsi2sk"),
			ConditionExpression:      aws.String("entity_type = :type AND #version = :version AND lifecycle_state = :state AND updated_at = :updated"),
			ExpressionAttributeNames: map[string]string{"#version": "version"},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":type":    &types.AttributeValueMemberS{Value: "Session"},
				":version": &types.AttributeValueMemberN{Value: strconv.FormatInt(item.Version, 10)},
				":state":   &types.AttributeValueMemberS{Value: item.LifecycleState},
				":updated": &types.AttributeValueMemberS{Value: item.UpdatedAt},
				":gsi2pk":  &types.AttributeValueMemberS{Value: projected.GSI2PK},
				":gsi2sk":  &types.AttributeValueMemberS{Value: projected.GSI2SK},
			},
		})
		if err != nil {
			var conditional *types.ConditionalCheckFailedException
			if errors.As(err, &conditional) {
				result.Conflicted++
				continue
			}
			return result, fmt.Errorf("backfill guild index for session %s: %w", item.SessionID, err)
		}
		result.Updated++
	}
	if len(output.LastEvaluatedKey) != 0 {
		result.NextCursor, err = encodeMigrationCursor(output.LastEvaluatedKey)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// VerifyGuildSessionIndex exhaustively compares authoritative source counts to
// every guild/state index range. It does not enable reads.
func (repository *Repository) VerifyGuildSessionIndex(ctx context.Context) (GuildSessionIndexVerification, error) {
	verification := GuildSessionIndexVerification{Mismatched: map[string][2]int{}}
	if err := repository.validate(); err != nil {
		return verification, err
	}
	source := map[string]int{}
	var startKey map[string]types.AttributeValue
	for {
		output, err := repository.client.Scan(ctx, &dynamodb.ScanInput{
			TableName: repository.tableNamePointer(), ConsistentRead: aws.Bool(true), ExclusiveStartKey: startKey,
			FilterExpression:          aws.String("entity_type = :type"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":type": &types.AttributeValueMemberS{Value: "Session"}},
		})
		if err != nil {
			return verification, fmt.Errorf("scan authoritative sessions for verification: %w", err)
		}
		for _, attributes := range output.Items {
			var item sessionItem
			if err := attributevalue.UnmarshalMap(attributes, &item); err != nil {
				return verification, err
			}
			verification.SourceCount++
			key := item.GuildID + "\x00" + item.LifecycleState
			source[key]++
			expectedPK := "GUILD#" + item.GuildID
			expectedSK := "STATE#" + item.LifecycleState + "#UPDATED#" + sortTimestampMust(item.UpdatedAt) + "#SESSION#" + item.SessionID
			if item.GSI2PK != expectedPK || item.GSI2SK != expectedSK {
				verification.MissingKeys++
			}
		}
		startKey = output.LastEvaluatedKey
		if len(startKey) == 0 {
			break
		}
	}
	indexedCounts := map[string]int{}
	startKey = nil
	for {
		output, err := repository.client.Scan(ctx, &dynamodb.ScanInput{
			TableName: repository.tableNamePointer(), IndexName: aws.String(guildSessionIndexName), ExclusiveStartKey: startKey,
			FilterExpression:          aws.String("entity_type = :type"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":type": &types.AttributeValueMemberS{Value: "Session"}},
		})
		if err != nil {
			return verification, fmt.Errorf("scan guild session index for verification: %w", err)
		}
		for _, attributes := range output.Items {
			var item sessionItem
			if err := attributevalue.UnmarshalMap(attributes, &item); err != nil {
				return verification, err
			}
			indexedCounts[item.GuildID+"\x00"+item.LifecycleState]++
			verification.IndexedCount++
		}
		startKey = output.LastEvaluatedKey
		if len(startKey) == 0 {
			break
		}
	}
	keys := make([]string, 0, len(source)+len(indexedCounts))
	seen := map[string]struct{}{}
	for key := range source {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range indexedCounts {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if source[key] != indexedCounts[key] {
			parts := splitCountKey(key)
			verification.Mismatched[parts[0]+"/"+parts[1]] = [2]int{source[key], indexedCounts[key]}
		}
	}
	return verification, nil
}

// EnableGuildSessionIndex writes the explicit cutover marker only after a fresh
// exhaustive verification succeeds.
func (repository *Repository) EnableGuildSessionIndex(ctx context.Context, now time.Time) (GuildSessionIndexVerification, error) {
	verification, err := repository.VerifyGuildSessionIndex(ctx)
	if err != nil {
		return verification, err
	}
	if !verification.Valid() {
		return verification, fmt.Errorf("guild session index verification failed")
	}
	_, err = repository.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: repository.tableNamePointer(),
		Item: map[string]types.AttributeValue{
			"pk":            &types.AttributeValueMemberS{Value: guildSessionIndexPK},
			"sk":            &types.AttributeValueMemberS{Value: guildSessionIndexSK},
			"entity_type":   &types.AttributeValueMemberS{Value: "GuildSessionIndexCutover"},
			"status":        &types.AttributeValueMemberS{Value: "READY"},
			"verified_at":   &types.AttributeValueMemberS{Value: now.UTC().Format(time.RFC3339Nano)},
			"source_count":  &types.AttributeValueMemberN{Value: strconv.Itoa(verification.SourceCount)},
			"indexed_count": &types.AttributeValueMemberN{Value: strconv.Itoa(verification.IndexedCount)},
		},
	})
	if err != nil {
		return verification, fmt.Errorf("enable guild session index: %w", err)
	}
	return verification, nil
}

func encodeMigrationCursor(key map[string]types.AttributeValue) (string, error) {
	plain := map[string]string{}
	for _, name := range []string{"pk", "sk"} {
		value, ok := key[name].(*types.AttributeValueMemberS)
		if !ok {
			return "", fmt.Errorf("migration cursor missing %s", name)
		}
		plain[name] = value.Value
	}
	data, _ := json.Marshal(plain)
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeMigrationCursor(cursor string) (map[string]types.AttributeValue, error) {
	if cursor == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("decode migration cursor: %w", err)
	}
	plain := map[string]string{}
	if err := json.Unmarshal(data, &plain); err != nil || plain["pk"] == "" || plain["sk"] == "" {
		return nil, fmt.Errorf("decode migration cursor: invalid key")
	}
	return map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: plain["pk"]},
		"sk": &types.AttributeValueMemberS{Value: plain["sk"]},
	}, nil
}

func sortTimestampMust(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "INVALID"
	}
	return sortTimestamp(parsed)
}

func splitCountKey(key string) [2]string {
	for i := range key {
		if key[i] == 0 {
			return [2]string{key[:i], key[i+1:]}
		}
	}
	return [2]string{key, ""}
}

func (repository *Repository) tableNamePointer() *string { return aws.String(repository.tableName) }
