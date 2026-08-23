package resetcleanup

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

func TestExactTagsRequiresAllImmutableOwnershipTags(t *testing.T) {
	t.Parallel()
	valid := []types.Tag{{Key: aws.String("Project"), Value: aws.String("game-server-platform")}, {Key: aws.String("Environment"), Value: aws.String("dev")}, {Key: aws.String("SessionId"), Value: aws.String("session-1")}}
	if !exactTags(valid, "game-server-platform", "dev") {
		t.Fatal("complete exact tags were rejected")
	}
	for _, tags := range [][]types.Tag{valid[:2], {{Key: aws.String("Project"), Value: aws.String("other")}, {Key: aws.String("Environment"), Value: aws.String("dev")}, {Key: aws.String("SessionId"), Value: aws.String("session-1")}}} {
		if exactTags(tags, "game-server-platform", "dev") {
			t.Fatalf("ambiguous tags accepted: %#v", tags)
		}
	}
}

func TestHasAPIErrorCodeOnlyAcceptsAllowlistedIdempotentOutcomes(t *testing.T) {
	t.Parallel()
	err := &smithy.GenericAPIError{Code: "InvalidVolume.NotFound", Message: "sensitive provider detail"}
	if !hasAPIErrorCode(err, "InvalidVolume.NotFound") || hasAPIErrorCode(err, "UnauthorizedOperation") {
		t.Fatal("API error code classification did not stay allowlisted")
	}
}

type fakeS3 struct {
	pages   []*s3.ListObjectVersionsOutput
	deleted [][]s3types.ObjectIdentifier
	index   int
}

func (fake *fakeS3) ListObjectVersions(context.Context, *s3.ListObjectVersionsInput, ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error) {
	if fake.index >= len(fake.pages) {
		return nil, errors.New("unexpected page")
	}
	page := fake.pages[fake.index]
	fake.index++
	return page, nil
}

func (fake *fakeS3) DeleteObjects(_ context.Context, input *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	fake.deleted = append(fake.deleted, append([]s3types.ObjectIdentifier(nil), input.Delete.Objects...))
	return &s3.DeleteObjectsOutput{}, nil
}

func TestDeleteSessionObjectsDeletesEveryVersionAndMarkerAcrossPages(t *testing.T) {
	t.Parallel()
	client := &fakeS3{pages: []*s3.ListObjectVersionsOutput{
		{Versions: []s3types.ObjectVersion{{Key: aws.String("sessions/one"), VersionId: aws.String("v1")}}, IsTruncated: aws.Bool(true), NextKeyMarker: aws.String("sessions/one"), NextVersionIdMarker: aws.String("v1")},
		{DeleteMarkers: []s3types.DeleteMarkerEntry{{Key: aws.String("sessions/two"), VersionId: aws.String("v2")}}},
	}}
	cleaner := &Cleaner{s3: client, config: Config{Bucket: "bucket"}}
	deleted, err := cleaner.deleteSessionObjects(context.Background())
	if err != nil || deleted != 2 || len(client.deleted) != 2 {
		t.Fatalf("deleted=%d batches=%d err=%v", deleted, len(client.deleted), err)
	}
}

func TestDeleteSessionObjectsFailsClosedOnBrokenPagination(t *testing.T) {
	t.Parallel()
	client := &fakeS3{pages: []*s3.ListObjectVersionsOutput{{IsTruncated: aws.Bool(true)}}}
	cleaner := &Cleaner{s3: client, config: Config{Bucket: "bucket"}}
	if _, err := cleaner.deleteSessionObjects(context.Background()); err == nil {
		t.Fatal("truncated listing without continuation marker succeeded")
	}
}

type fakeDynamo struct {
	pages []*dynamodb.ScanOutput
	index int
}

func (fake *fakeDynamo) Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	page := fake.pages[fake.index]
	fake.index++
	return page, nil
}

func (*fakeDynamo) DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

func stringAttribute(value string) ddbtypes.AttributeValue {
	return &ddbtypes.AttributeValueMemberS{Value: value}
}

func TestInventoryMetadataPreservesControlPlaneAndResetAudit(t *testing.T) {
	t.Parallel()
	client := &fakeDynamo{pages: []*dynamodb.ScanOutput{{Items: []map[string]ddbtypes.AttributeValue{
		{"pk": stringAttribute("SESSION#1"), "sk": stringAttribute("METADATA"), "entity_type": stringAttribute("Session")},
		{"pk": stringAttribute("GUILD#1"), "sk": stringAttribute("ACCESS"), "entity_type": stringAttribute("GuildAccessPolicy")},
		{"pk": stringAttribute("RESET#dev"), "sk": stringAttribute("AUDIT#LATEST"), "entity_type": stringAttribute("ResetAudit")},
		{"pk": stringAttribute("RESET_OPERATION#old"), "sk": stringAttribute("METADATA"), "entity_type": stringAttribute("ResetOperation"), "operation_id": stringAttribute("old-reset")},
		{"pk": stringAttribute("RESET_OPERATION#current"), "sk": stringAttribute("METADATA"), "entity_type": stringAttribute("ResetOperation"), "operation_id": stringAttribute("current-reset")},
		{"pk": stringAttribute("GUILD#1"), "sk": stringAttribute("SERVER_CFG"), "entity_type": stringAttribute("GuildServerConfig")},
		{"pk": stringAttribute("STEAM_AUTH#CACHE"), "sk": stringAttribute("STATE")},
	}}}}
	cleaner := &Cleaner{dynamo: client}
	items, sessions, _, err := cleaner.inventoryMetadata(context.Background(), "current-reset")
	if err != nil || sessions != 1 || len(items) != 2 || items[0].pk != "SESSION#1" || items[1].pk != "RESET_OPERATION#old" {
		t.Fatalf("items=%#v sessions=%d err=%v", items, sessions, err)
	}
}

func TestInventoryMetadataFailsClosedOnUnknownEntity(t *testing.T) {
	t.Parallel()
	client := &fakeDynamo{pages: []*dynamodb.ScanOutput{{Items: []map[string]ddbtypes.AttributeValue{
		{"pk": stringAttribute("FUTURE#1"), "sk": stringAttribute("STATE"), "entity_type": stringAttribute("FutureControlRecord")},
	}}}}
	cleaner := &Cleaner{dynamo: client}
	if _, _, _, err := cleaner.inventoryMetadata(context.Background(), "current-reset"); err == nil {
		t.Fatal("unknown metadata entity was selected for deletion")
	}
}

type fakeEC2 struct {
	instancePages []*ec2.DescribeInstancesOutput
	volumePages   []*ec2.DescribeVolumesOutput
	instanceIndex int
	volumeIndex   int
}

func (fake *fakeEC2) DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	page := fake.instancePages[fake.instanceIndex]
	fake.instanceIndex++
	return page, nil
}
func (*fakeEC2) TerminateInstances(context.Context, *ec2.TerminateInstancesInput, ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	return &ec2.TerminateInstancesOutput{}, nil
}
func (fake *fakeEC2) DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	page := fake.volumePages[fake.volumeIndex]
	fake.volumeIndex++
	return page, nil
}
func (*fakeEC2) DeleteVolume(context.Context, *ec2.DeleteVolumeInput, ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error) {
	return &ec2.DeleteVolumeOutput{}, nil
}

func TestOwnedComputeDiscoveryReadsEveryPage(t *testing.T) {
	t.Parallel()
	client := &fakeEC2{
		instancePages: []*ec2.DescribeInstancesOutput{
			{Reservations: []types.Reservation{{Instances: []types.Instance{{InstanceId: aws.String("i-1")}}}}, NextToken: aws.String("instances-2")},
			{Reservations: []types.Reservation{{Instances: []types.Instance{{InstanceId: aws.String("i-2")}}}}},
		},
		volumePages: []*ec2.DescribeVolumesOutput{
			{Volumes: []types.Volume{{VolumeId: aws.String("vol-1")}}, NextToken: aws.String("volumes-2")},
			{Volumes: []types.Volume{{VolumeId: aws.String("vol-2")}}},
		},
	}
	cleaner := &Cleaner{ec2: client}
	instances, instanceErr := cleaner.listOwnedInstances(context.Background())
	volumes, volumeErr := cleaner.listOwnedVolumes(context.Background())
	if instanceErr != nil || volumeErr != nil || len(instances) != 2 || len(volumes) != 2 {
		t.Fatalf("instances=%d instanceErr=%v volumes=%d volumeErr=%v", len(instances), instanceErr, len(volumes), volumeErr)
	}
}
