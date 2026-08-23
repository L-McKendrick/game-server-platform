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

var _ ports.GuildServerConfigRepository = (*Repository)(nil)

type guildServerConfigItem struct {
	PK            string `dynamodbav:"pk"`
	SK            string `dynamodbav:"sk"`
	EntityType    string `dynamodbav:"entity_type"`
	SchemaVersion int    `dynamodbav:"schema_version"`
	GuildID       string `dynamodbav:"guild_id"`
	Revision      int64  `dynamodbav:"revision"`
	ObjectKey     string `dynamodbav:"object_key,omitempty"`
	Filename      string `dynamodbav:"filename,omitempty"`
	SHA256        string `dynamodbav:"sha256,omitempty"`
	SizeBytes     int64  `dynamodbav:"size_bytes,omitempty"`
	UploadedBy    string `dynamodbav:"uploaded_by,omitempty"`
	UpdatedAt     string `dynamodbav:"updated_at"`
}

func guildServerConfigPK(guildID string) string { return "GUILD#" + strings.TrimSpace(guildID) }

func (repository *Repository) GetGuildServerConfig(ctx context.Context, guildID string) (domain.GuildServerConfig, error) {
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: guildServerConfigPK(guildID)}, "sk": &types.AttributeValueMemberS{Value: "SERVER_CONFIG"},
	}})
	if err != nil {
		return domain.GuildServerConfig{}, err
	}
	if len(output.Item) == 0 {
		return domain.GuildServerConfig{}, domain.ErrNotFound
	}
	var item guildServerConfigItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.GuildServerConfig{}, err
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, item.UpdatedAt)
	if err != nil {
		return domain.GuildServerConfig{}, fmt.Errorf("parse guild server config updated_at: %w", err)
	}
	config := domain.GuildServerConfig{GuildID: item.GuildID, Revision: item.Revision, ObjectKey: item.ObjectKey, Filename: item.Filename, SHA256: item.SHA256, SizeBytes: item.SizeBytes, UploadedBy: item.UploadedBy, UpdatedAt: updatedAt.UTC()}
	return config, config.Validate()
}

func (repository *Repository) SaveGuildServerConfig(ctx context.Context, config domain.GuildServerConfig, expectedRevision int64) (domain.GuildServerConfig, error) {
	if err := config.Validate(); err != nil {
		return domain.GuildServerConfig{}, err
	}
	if expectedRevision < 0 || config.Revision != expectedRevision+1 {
		return domain.GuildServerConfig{}, domain.ErrConflict
	}
	item, err := attributevalue.MarshalMap(guildServerConfigItem{
		PK: guildServerConfigPK(config.GuildID), SK: "SERVER_CONFIG", EntityType: "GuildServerConfig", SchemaVersion: schemaVersion,
		GuildID: config.GuildID, Revision: config.Revision, ObjectKey: config.ObjectKey, Filename: config.Filename,
		SHA256: config.SHA256, SizeBytes: config.SizeBytes, UploadedBy: config.UploadedBy, UpdatedAt: config.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return domain.GuildServerConfig{}, err
	}
	condition := "#revision = :expected"
	if expectedRevision == 0 {
		condition = "attribute_not_exists(pk) OR #revision = :expected"
	}
	_, err = repository.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(repository.tableName), Item: item, ConditionExpression: aws.String(condition),
		ExpressionAttributeNames: map[string]string{"#revision": "revision"}, ExpressionAttributeValues: map[string]types.AttributeValue{":expected": &types.AttributeValueMemberN{Value: strconv.FormatInt(expectedRevision, 10)}},
	})
	if err == nil {
		return config, nil
	}
	var conditional *types.ConditionalCheckFailedException
	if !errors.As(err, &conditional) {
		return domain.GuildServerConfig{}, err
	}
	current, getErr := repository.GetGuildServerConfig(ctx, config.GuildID)
	if getErr == nil && current.Revision == config.Revision && current.ObjectKey == config.ObjectKey && current.SHA256 == config.SHA256 && current.Active() == config.Active() {
		return current, nil
	}
	return domain.GuildServerConfig{}, domain.ErrConflict
}
