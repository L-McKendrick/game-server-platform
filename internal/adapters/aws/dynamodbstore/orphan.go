package dynamodbstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var _ ports.OrphanRepository = (*Repository)(nil)

type orphanItem struct {
	PK            string            `dynamodbav:"pk"`
	SK            string            `dynamodbav:"sk"`
	EntityType    string            `dynamodbav:"entity_type"`
	SchemaVersion int               `dynamodbav:"schema_version"`
	FindingID     string            `dynamodbav:"finding_id"`
	Kind          string            `dynamodbav:"resource_kind"`
	ResourceID    string            `dynamodbav:"resource_id"`
	ResourceARN   string            `dynamodbav:"resource_arn,omitempty"`
	SessionID     string            `dynamodbav:"session_id,omitempty"`
	Project       string            `dynamodbav:"project,omitempty"`
	Environment   string            `dynamodbav:"environment,omitempty"`
	State         string            `dynamodbav:"resource_state,omitempty"`
	CreatedAt     string            `dynamodbav:"resource_created_at"`
	Tags          map[string]string `dynamodbav:"resource_tags,omitempty"`
	Reason        string            `dynamodbav:"reason"`
	Disposition   string            `dynamodbav:"disposition"`
	RelatedIDs    []string          `dynamodbav:"related_ids,omitempty"`
	DetectedAt    string            `dynamodbav:"detected_at"`
	EligibleAfter string            `dynamodbav:"eligible_after,omitempty"`
	UpdatedAt     string            `dynamodbav:"updated_at"`
	UpdatedBy     string            `dynamodbav:"updated_by,omitempty"`
}

func (repository *Repository) ListSessionsForInventory(ctx context.Context, limit int32) ([]domain.Session, error) {
	if limit < 1 || limit > maximumGuildScanItems {
		return nil, fmt.Errorf("limit must be between 1 and %d", maximumGuildScanItems)
	}
	output, err := repository.client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(repository.tableName), Limit: aws.Int32(limit), FilterExpression: aws.String("entity_type = :session"), ExpressionAttributeValues: map[string]types.AttributeValue{":session": &types.AttributeValueMemberS{Value: "Session"}}})
	if err != nil {
		return nil, err
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

func toOrphanItem(finding domain.OrphanFinding) orphanItem {
	return orphanItem{PK: "RELIABILITY#ORPHANS", SK: "FINDING#" + finding.ID, EntityType: "OrphanFinding", SchemaVersion: schemaVersion,
		FindingID: finding.ID, Kind: string(finding.Resource.Kind), ResourceID: finding.Resource.ID, ResourceARN: finding.Resource.ARN, SessionID: finding.Resource.SessionID,
		Project: finding.Resource.Project, Environment: finding.Resource.Environment, State: finding.Resource.State, CreatedAt: optionalTimestamp(finding.Resource.CreatedAt), Tags: finding.Resource.Tags, RelatedIDs: finding.Resource.RelatedIDs,
		Reason: finding.Reason, Disposition: string(finding.Disposition), DetectedAt: optionalTimestamp(finding.DetectedAt), EligibleAfter: optionalTimestamp(finding.EligibleAfter), UpdatedAt: optionalTimestamp(finding.UpdatedAt), UpdatedBy: finding.UpdatedBy}
}

func fromOrphanItem(item orphanItem) (domain.OrphanFinding, error) {
	created, err := parseOptionalTimestamp(item.CreatedAt)
	if err != nil {
		return domain.OrphanFinding{}, err
	}
	detected, err := parseOptionalTimestamp(item.DetectedAt)
	if err != nil {
		return domain.OrphanFinding{}, err
	}
	eligible, err := parseOptionalTimestamp(item.EligibleAfter)
	if err != nil {
		return domain.OrphanFinding{}, err
	}
	updated, err := parseOptionalTimestamp(item.UpdatedAt)
	if err != nil {
		return domain.OrphanFinding{}, err
	}
	finding := domain.OrphanFinding{ID: item.FindingID, Resource: domain.ResourceObservation{Kind: domain.ResourceKind(item.Kind), ID: item.ResourceID, ARN: item.ResourceARN, SessionID: item.SessionID, Project: item.Project, Environment: item.Environment, State: item.State, CreatedAt: created, Tags: item.Tags, RelatedIDs: item.RelatedIDs}, Reason: item.Reason, Disposition: domain.OrphanDisposition(item.Disposition), DetectedAt: detected, EligibleAfter: eligible, UpdatedAt: updated, UpdatedBy: item.UpdatedBy}
	return finding, finding.Validate()
}

func (repository *Repository) SaveOrphanFinding(ctx context.Context, finding domain.OrphanFinding) error {
	if err := finding.Validate(); err != nil {
		return err
	}
	attributes, err := attributevalue.MarshalMap(toOrphanItem(finding))
	if err != nil {
		return err
	}
	_, err = repository.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(repository.tableName), Item: attributes,
		ConditionExpression: aws.String("attribute_not_exists(pk) OR resource_id = :resource_id"), ExpressionAttributeValues: map[string]types.AttributeValue{":resource_id": &types.AttributeValueMemberS{Value: finding.Resource.ID}}})
	return err
}

func (repository *Repository) GetOrphanFinding(ctx context.Context, findingID string) (domain.OrphanFinding, error) {
	output, err := repository.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(repository.tableName), ConsistentRead: aws.Bool(true), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "RELIABILITY#ORPHANS"}, "sk": &types.AttributeValueMemberS{Value: "FINDING#" + strings.TrimSpace(findingID)}}})
	if err != nil {
		return domain.OrphanFinding{}, err
	}
	if len(output.Item) == 0 {
		return domain.OrphanFinding{}, domain.ErrNotFound
	}
	var item orphanItem
	if err := attributevalue.UnmarshalMap(output.Item, &item); err != nil {
		return domain.OrphanFinding{}, err
	}
	return fromOrphanItem(item)
}

func (repository *Repository) ListOrphanFindings(ctx context.Context, limit int32) ([]domain.OrphanFinding, error) {
	if limit < 1 {
		return nil, fmt.Errorf("limit must be positive")
	}
	output, err := repository.client.Query(ctx, &dynamodb.QueryInput{TableName: aws.String(repository.tableName), Limit: aws.Int32(limit), KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"), ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "RELIABILITY#ORPHANS"}, ":prefix": &types.AttributeValueMemberS{Value: "FINDING#"}}})
	if err != nil {
		return nil, err
	}
	result := make([]domain.OrphanFinding, 0, len(output.Items))
	for _, attributes := range output.Items {
		var item orphanItem
		if err := attributevalue.UnmarshalMap(attributes, &item); err != nil {
			return nil, err
		}
		finding, err := fromOrphanItem(item)
		if err != nil {
			return nil, err
		}
		result = append(result, finding)
	}
	return result, nil
}
