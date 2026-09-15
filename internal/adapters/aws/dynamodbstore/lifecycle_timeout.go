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

var _ ports.LifecycleTimeoutPolicyRepository = (*Repository)(nil)

type lifecycleTimeoutPolicyItem struct {
	PK                  string `dynamodbav:"pk"`
	SK                  string `dynamodbav:"sk"`
	EntityType          string `dynamodbav:"entity_type"`
	SchemaVersion       int    `dynamodbav:"schema_version"`
	GuildID             string `dynamodbav:"guild_id"`
	SleepAfterSeconds   int64  `dynamodbav:"sleep_after_seconds"`
	ArchiveAfterSeconds int64  `dynamodbav:"archive_after_seconds"`
	Version             int64  `dynamodbav:"version"`
	UpdatedBy           string `dynamodbav:"updated_by"`
	UpdatedAt           string `dynamodbav:"updated_at"`
}

type lifecycleTimeoutAuditItem struct {
	PK                          string `dynamodbav:"pk"`
	SK                          string `dynamodbav:"sk"`
	EntityType                  string `dynamodbav:"entity_type"`
	SchemaVersion               int    `dynamodbav:"schema_version"`
	ID                          string `dynamodbav:"id"`
	GuildID                     string `dynamodbav:"guild_id"`
	ActorID                     string `dynamodbav:"actor_id"`
	CorrelationID               string `dynamodbav:"correlation_id"`
	Reason                      string `dynamodbav:"reason"`
	OccurredAt                  string `dynamodbav:"occurred_at"`
	PreviousSleepAfterSeconds   int64  `dynamodbav:"previous_sleep_after_seconds"`
	PreviousArchiveAfterSeconds int64  `dynamodbav:"previous_archive_after_seconds"`
	SleepAfterSeconds           int64  `dynamodbav:"sleep_after_seconds"`
	ArchiveAfterSeconds         int64  `dynamodbav:"archive_after_seconds"`
}

func (repository *Repository) GetLifecycleTimeoutPolicy(ctx context.Context, guildID string) (domain.GuildLifecycleTimeoutPolicy, error) {
	guildID = strings.TrimSpace(guildID)
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: "GUILD#" + guildID}, "sk": &types.AttributeValueMemberS{Value: "LIFECYCLE_TIMEOUTS#CURRENT"},
	}})
	if err != nil {
		return domain.GuildLifecycleTimeoutPolicy{}, fmt.Errorf("get lifecycle timeout policy: %w", err)
	}
	if output == nil || len(output.Item) == 0 {
		return domain.GuildLifecycleTimeoutPolicy{}, fmt.Errorf("%w: lifecycle timeout policy", domain.ErrNotFound)
	}
	var item lifecycleTimeoutPolicyItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.GuildLifecycleTimeoutPolicy{}, err
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, item.UpdatedAt)
	if err != nil {
		return domain.GuildLifecycleTimeoutPolicy{}, err
	}
	policy := domain.GuildLifecycleTimeoutPolicy{GuildID: item.GuildID, SleepAfterSeconds: item.SleepAfterSeconds, ArchiveAfterSeconds: item.ArchiveAfterSeconds, Version: item.Version, UpdatedBy: item.UpdatedBy, UpdatedAt: updatedAt.UTC()}
	return policy, policy.Validate()
}

func (repository *Repository) SaveLifecycleTimeoutPolicy(ctx context.Context, policy domain.GuildLifecycleTimeoutPolicy, expectedVersion int64, audit domain.LifecycleTimeoutPolicyAudit, idempotency domain.IdempotencyRecord) error {
	if err := policy.Validate(); err != nil {
		return fmt.Errorf("validate lifecycle timeout policy: %w", err)
	}
	if policy.Version < 1 {
		return fmt.Errorf("persisted lifecycle timeout policy version must be positive")
	}
	if err := audit.Validate(); err != nil {
		return fmt.Errorf("validate lifecycle timeout audit: %w", err)
	}
	if err := idempotency.Validate(); err != nil {
		return fmt.Errorf("validate lifecycle timeout idempotency: %w", err)
	}
	if idempotency.ResultReference != policy.GuildID {
		return fmt.Errorf("lifecycle timeout idempotency result must reference guild")
	}
	attributes, err := attributevalue.MarshalMap(lifecycleTimeoutPolicyItem{PK: "GUILD#" + policy.GuildID, SK: "LIFECYCLE_TIMEOUTS#CURRENT", EntityType: "GuildLifecycleTimeoutPolicy", SchemaVersion: schemaVersion, GuildID: policy.GuildID, SleepAfterSeconds: policy.SleepAfterSeconds, ArchiveAfterSeconds: policy.ArchiveAfterSeconds, Version: policy.Version, UpdatedBy: policy.UpdatedBy, UpdatedAt: policy.UpdatedAt.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	policyPut := &types.Put{TableName: aws.String(repository.tableName), Item: attributes, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")}
	if expectedVersion > 0 {
		policyPut.ConditionExpression = aws.String("#version = :expected_version")
		policyPut.ExpressionAttributeNames = map[string]string{"#version": "version"}
		policyPut.ExpressionAttributeValues = map[string]types.AttributeValue{":expected_version": &types.AttributeValueMemberN{Value: strconv.FormatInt(expectedVersion, 10)}}
	}
	auditAttributes, err := attributevalue.MarshalMap(lifecycleTimeoutAuditItem{PK: "GUILD#" + policy.GuildID, SK: "LIFECYCLE_TIMEOUTS#AUDIT#" + audit.ID, EntityType: "LifecycleTimeoutPolicyAudit", SchemaVersion: schemaVersion, ID: audit.ID, GuildID: audit.GuildID, ActorID: audit.ActorID, CorrelationID: audit.CorrelationID, Reason: audit.Reason, OccurredAt: audit.OccurredAt.UTC().Format(time.RFC3339Nano), PreviousSleepAfterSeconds: audit.PreviousSleepAfterSeconds, PreviousArchiveAfterSeconds: audit.PreviousArchiveAfterSeconds, SleepAfterSeconds: audit.SleepAfterSeconds, ArchiveAfterSeconds: audit.ArchiveAfterSeconds})
	if err != nil {
		return err
	}
	idempotencyAttributes, err := attributevalue.MarshalMap(toIdempotencyItem(idempotency))
	if err != nil {
		return err
	}
	_, err = repository.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{ClientRequestToken: aws.String(audit.ID), TransactItems: []types.TransactWriteItem{
		{Put: policyPut},
		{Put: &types.Put{TableName: aws.String(repository.tableName), Item: auditAttributes, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")}},
		{Put: &types.Put{TableName: aws.String(repository.tableName), Item: idempotencyAttributes, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")}},
	}})
	if err != nil {
		var conditional *types.ConditionalCheckFailedException
		var canceled *types.TransactionCanceledException
		if errors.As(err, &conditional) || errors.As(err, &canceled) {
			return fmt.Errorf("%w: lifecycle timeout policy version", domain.ErrConflict)
		}
		return err
	}
	return nil
}
