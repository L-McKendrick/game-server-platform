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

var _ ports.AccessPolicyRepository = (*Repository)(nil)

type accessPolicyItem struct {
	PK                  string   `dynamodbav:"pk"`
	SK                  string   `dynamodbav:"sk"`
	EntityType          string   `dynamodbav:"entity_type"`
	SchemaVersion       int      `dynamodbav:"schema_version"`
	GuildID             string   `dynamodbav:"guild_id"`
	AllowedRoleIDs      []string `dynamodbav:"allowed_role_ids"`
	AllowedChannelIDs   []string `dynamodbav:"allowed_channel_ids"`
	PublicCardChannelID string   `dynamodbav:"public_card_channel_id,omitempty"`
	Version             int64    `dynamodbav:"version"`
	UpdatedBy           string   `dynamodbav:"updated_by"`
	UpdatedAt           string   `dynamodbav:"updated_at"`
}

func (repository *Repository) GetAccessPolicy(ctx context.Context, guildID string) (domain.GuildAccessPolicy, error) {
	guildID = strings.TrimSpace(guildID)
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: "GUILD#" + guildID},
			"sk": &types.AttributeValueMemberS{Value: "ACCESS#CURRENT"},
		},
	})
	if err != nil {
		return domain.GuildAccessPolicy{}, fmt.Errorf("get access policy: %w", err)
	}
	if len(output.Item) == 0 {
		return domain.GuildAccessPolicy{}, fmt.Errorf("%w: guild access policy", domain.ErrNotFound)
	}
	var item accessPolicyItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.GuildAccessPolicy{}, err
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, item.UpdatedAt)
	if err != nil {
		return domain.GuildAccessPolicy{}, err
	}
	policy := domain.GuildAccessPolicy{
		GuildID: item.GuildID, AllowedRoleIDs: item.AllowedRoleIDs,
		AllowedChannelIDs: item.AllowedChannelIDs, PublicCardChannelID: item.PublicCardChannelID, Version: item.Version,
		UpdatedBy: item.UpdatedBy, UpdatedAt: updatedAt.UTC(),
	}
	return policy, policy.Validate()
}

func (repository *Repository) SaveAccessPolicy(ctx context.Context, policy domain.GuildAccessPolicy, expectedVersion int64) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	attributes, err := attributevalue.MarshalMap(accessPolicyItem{
		PK: "GUILD#" + policy.GuildID, SK: "ACCESS#CURRENT", EntityType: "GuildAccessPolicy",
		SchemaVersion: schemaVersion, GuildID: policy.GuildID,
		AllowedRoleIDs: policy.AllowedRoleIDs, AllowedChannelIDs: policy.AllowedChannelIDs, PublicCardChannelID: policy.PublicCardChannelID,
		Version: policy.Version, UpdatedBy: policy.UpdatedBy, UpdatedAt: policy.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	condition := "attribute_not_exists(pk) AND attribute_not_exists(sk)"
	values := map[string]types.AttributeValue{}
	if expectedVersion > 0 {
		condition = "#version = :expected_version"
		values[":expected_version"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(expectedVersion, 10)}
	}
	input := &dynamodb.PutItemInput{
		TableName: aws.String(repository.tableName), Item: attributes,
		ConditionExpression: aws.String(condition),
	}
	if expectedVersion > 0 {
		input.ExpressionAttributeNames = map[string]string{"#version": "version"}
		input.ExpressionAttributeValues = values
	}
	_, err = repository.client.PutItem(ctx, input)
	if err != nil {
		var conditional *types.ConditionalCheckFailedException
		if errors.As(err, &conditional) {
			return fmt.Errorf("%w: access policy version", domain.ErrConflict)
		}
		return err
	}
	return nil
}
