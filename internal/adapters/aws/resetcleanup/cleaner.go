package resetcleanup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/smithy-go"
)

type EC2API interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	TerminateInstances(context.Context, *ec2.TerminateInstancesInput, ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	DeleteVolume(context.Context, *ec2.DeleteVolumeInput, ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error)
}
type S3API interface {
	ListObjectVersions(context.Context, *s3.ListObjectVersionsInput, ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error)
	DeleteObjects(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}
type SQSAPI interface {
	PurgeQueue(context.Context, *sqs.PurgeQueueInput, ...func(*sqs.Options)) (*sqs.PurgeQueueOutput, error)
}
type DynamoAPI interface {
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}
type SFNAPI interface {
	ListExecutions(context.Context, *sfn.ListExecutionsInput, ...func(*sfn.Options)) (*sfn.ListExecutionsOutput, error)
	StopExecution(context.Context, *sfn.StopExecutionInput, ...func(*sfn.Options)) (*sfn.StopExecutionOutput, error)
}
type LogsAPI interface {
	DescribeLogStreams(context.Context, *cloudwatchlogs.DescribeLogStreamsInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogStreamsOutput, error)
	DeleteLogStream(context.Context, *cloudwatchlogs.DeleteLogStreamInput, ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DeleteLogStreamOutput, error)
}
type MessageDeleter interface {
	DeleteMessage(ctx context.Context, channelID, messageID string) error
}

type Config struct {
	Project, Environment, Bucket, Table    string
	QueueURLs, StateMachineARNs, LogGroups []string
	PollInterval, PollTimeout              time.Duration
}

type Cleaner struct {
	ec2      EC2API
	s3       S3API
	sqs      SQSAPI
	dynamo   DynamoAPI
	sfn      SFNAPI
	logs     LogsAPI
	messages MessageDeleter
	config   Config
}

func New(ec2Client EC2API, s3Client S3API, sqsClient SQSAPI, dynamoClient DynamoAPI, sfnClient SFNAPI, logsClient LogsAPI, messages MessageDeleter, config Config) (*Cleaner, error) {
	config.Project, config.Environment, config.Bucket, config.Table = strings.TrimSpace(config.Project), strings.TrimSpace(config.Environment), strings.TrimSpace(config.Bucket), strings.TrimSpace(config.Table)
	if ec2Client == nil || s3Client == nil || sqsClient == nil || dynamoClient == nil || sfnClient == nil || logsClient == nil || messages == nil || config.Project == "" || config.Environment == "" || config.Bucket == "" || config.Table == "" {
		return nil, fmt.Errorf("reset cleanup clients and scope are required")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = 5 * time.Second
	}
	if config.PollTimeout <= 0 {
		config.PollTimeout = 5 * time.Minute
	}
	return &Cleaner{ec2: ec2Client, s3: s3Client, sqs: sqsClient, dynamo: dynamoClient, sfn: sfnClient, logs: logsClient, messages: messages, config: config}, nil
}

func (cleaner *Cleaner) Cleanup(ctx context.Context, operation domain.ResetOperation) (ports.ResetCleanupResult, error) {
	if operation.Environment != cleaner.config.Environment {
		return ports.ResetCleanupResult{}, domain.ErrForbidden
	}
	items, sessions, messageRefs, err := cleaner.inventoryMetadata(ctx, operation.ID)
	if err != nil {
		return ports.ResetCleanupResult{}, err
	}
	if err := cleaner.stopExecutions(ctx); err != nil {
		return ports.ResetCleanupResult{}, err
	}
	if err := cleaner.purgeQueues(ctx); err != nil {
		return ports.ResetCleanupResult{}, err
	}
	if err := cleaner.terminateInstances(ctx); err != nil {
		return ports.ResetCleanupResult{}, err
	}
	if err := cleaner.deleteVolumes(ctx); err != nil {
		return ports.ResetCleanupResult{}, err
	}
	for _, ref := range messageRefs {
		if err := cleaner.messages.DeleteMessage(ctx, ref[0], ref[1]); err != nil {
			return ports.ResetCleanupResult{}, err
		}
	}
	objects, err := cleaner.deleteSessionObjects(ctx)
	if err != nil {
		return ports.ResetCleanupResult{}, err
	}
	if err := cleaner.deleteMetadata(ctx, items); err != nil {
		return ports.ResetCleanupResult{}, err
	}
	if err := cleaner.deleteOldLogStreams(ctx, operation.StartedAt); err != nil {
		return ports.ResetCleanupResult{}, err
	}
	if err := cleaner.verifyNoRuntimeDrift(ctx, operation.ID); err != nil {
		return ports.ResetCleanupResult{DeletedSessions: sessions, DeletedObjects: objects}, err
	}
	return ports.ResetCleanupResult{DeletedSessions: sessions, DeletedObjects: objects}, nil
}

func (cleaner *Cleaner) verifyNoRuntimeDrift(ctx context.Context, currentResetOperationID string) error {
	items, _, _, err := cleaner.inventoryMetadata(ctx, currentResetOperationID)
	if err != nil {
		return err
	}
	if len(items) != 0 {
		return fmt.Errorf("runtime metadata reappeared during reset")
	}
	instances, err := cleaner.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: cleaner.filters()})
	if err != nil {
		return err
	}
	for _, reservation := range instances.Reservations {
		for _, instance := range reservation.Instances {
			if !exactTags(instance.Tags, cleaner.config.Project, cleaner.config.Environment) {
				return fmt.Errorf("reset verification found instance with incomplete immutable tags")
			}
			if instance.State.Name != ec2types.InstanceStateNameTerminated {
				return fmt.Errorf("owned instance reappeared during reset")
			}
		}
	}
	volumes, err := cleaner.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{Filters: cleaner.filters()})
	if err != nil {
		return err
	}
	if len(volumes.Volumes) != 0 {
		return fmt.Errorf("owned volume remained after reset")
	}
	objects, err := cleaner.s3.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(cleaner.config.Bucket), Prefix: aws.String("sessions/")})
	if err != nil {
		return err
	}
	if len(objects.Versions) != 0 || len(objects.DeleteMarkers) != 0 || aws.ToBool(objects.IsTruncated) {
		return fmt.Errorf("session object version reappeared during reset")
	}
	for _, arn := range cleaner.config.StateMachineARNs {
		var token *string
		for {
			executions, err := cleaner.sfn.ListExecutions(ctx, &sfn.ListExecutionsInput{StateMachineArn: aws.String(arn), StatusFilter: sfntypes.ExecutionStatusRunning, NextToken: token})
			if err != nil {
				return err
			}
			if len(executions.Executions) != 0 {
				return fmt.Errorf("workflow execution reappeared during reset")
			}
			if executions.NextToken == nil {
				break
			}
			token = executions.NextToken
		}
	}
	return nil
}

func (cleaner *Cleaner) filters() []ec2types.Filter {
	return []ec2types.Filter{{Name: aws.String("tag:Project"), Values: []string{cleaner.config.Project}}, {Name: aws.String("tag:Environment"), Values: []string{cleaner.config.Environment}}}
}
func exactTags(tags []ec2types.Tag, project, environment string) bool {
	values := map[string]string{}
	for _, tag := range tags {
		values[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return values["Project"] == project && values["Environment"] == environment && strings.TrimSpace(values["SessionId"]) != ""
}

func (cleaner *Cleaner) terminateInstances(ctx context.Context) error {
	output, err := cleaner.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: cleaner.filters()})
	if err != nil {
		return err
	}
	ids := []string{}
	for _, reservation := range output.Reservations {
		for _, instance := range reservation.Instances {
			if !exactTags(instance.Tags, cleaner.config.Project, cleaner.config.Environment) {
				return fmt.Errorf("reset refused instance with incomplete immutable tags")
			}
			if instance.State.Name != ec2types.InstanceStateNameTerminated {
				ids = append(ids, aws.ToString(instance.InstanceId))
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if _, err := cleaner.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: ids}); err != nil && !hasAPIErrorCode(err, "InvalidInstanceID.NotFound") {
		return err
	}
	deadline := time.Now().Add(cleaner.config.PollTimeout)
	for time.Now().Before(deadline) {
		observed, err := cleaner.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: ids})
		if err != nil && hasAPIErrorCode(err, "InvalidInstanceID.NotFound") {
			return nil
		}
		if err != nil {
			return err
		}
		remaining := 0
		for _, reservation := range observed.Reservations {
			for _, instance := range reservation.Instances {
				if instance.State.Name != ec2types.InstanceStateNameTerminated {
					remaining++
				}
			}
		}
		if remaining == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cleaner.config.PollInterval):
		}
	}
	return fmt.Errorf("timed out verifying tagged instances terminated")
}

func (cleaner *Cleaner) deleteVolumes(ctx context.Context) error {
	output, err := cleaner.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{Filters: cleaner.filters()})
	if err != nil {
		return err
	}
	for _, volume := range output.Volumes {
		if !exactTags(volume.Tags, cleaner.config.Project, cleaner.config.Environment) {
			return fmt.Errorf("reset refused volume with incomplete immutable tags")
		}
		if volume.State == ec2types.VolumeStateInUse {
			return fmt.Errorf("tagged volume remained attached after instance termination")
		}
		if _, err := cleaner.ec2.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: volume.VolumeId}); err != nil && !hasAPIErrorCode(err, "InvalidVolume.NotFound") {
			return err
		}
	}
	return nil
}

func (cleaner *Cleaner) deleteSessionObjects(ctx context.Context) (int, error) {
	var keyMarker, versionMarker *string
	deleted := 0
	for {
		output, err := cleaner.s3.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(cleaner.config.Bucket), Prefix: aws.String("sessions/"), KeyMarker: keyMarker, VersionIdMarker: versionMarker})
		if err != nil {
			return deleted, err
		}
		objects := make([]s3types.ObjectIdentifier, 0, len(output.Versions)+len(output.DeleteMarkers))
		for _, version := range output.Versions {
			objects = append(objects, s3types.ObjectIdentifier{Key: version.Key, VersionId: version.VersionId})
		}
		for _, marker := range output.DeleteMarkers {
			objects = append(objects, s3types.ObjectIdentifier{Key: marker.Key, VersionId: marker.VersionId})
		}
		if len(objects) > 0 {
			result, err := cleaner.s3.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String(cleaner.config.Bucket), Delete: &s3types.Delete{Objects: objects, Quiet: aws.Bool(true)}})
			if err != nil {
				return deleted, err
			}
			if len(result.Errors) > 0 {
				return deleted, fmt.Errorf("S3 did not delete every session object version")
			}
			deleted += len(objects)
		}
		if !aws.ToBool(output.IsTruncated) {
			break
		}
		if output.NextKeyMarker == nil {
			return deleted, fmt.Errorf("S3 version listing truncated without continuation marker")
		}
		keyMarker, versionMarker = output.NextKeyMarker, output.NextVersionIdMarker
	}
	return deleted, nil
}

type metadataKey struct{ pk, sk string }

func (cleaner *Cleaner) inventoryMetadata(ctx context.Context, currentResetOperationID string) ([]metadataKey, int, [][2]string, error) {
	keys := []metadataKey{}
	sessions := 0
	refs := [][2]string{}
	var start map[string]ddbtypes.AttributeValue
	for {
		output, err := cleaner.dynamo.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String(cleaner.config.Table), ExclusiveStartKey: start})
		if err != nil {
			return nil, 0, nil, err
		}
		for _, item := range output.Items {
			entity := attributeString(item, "entity_type")
			if entity == "GuildAccessPolicy" || entity == "ResetLock" || entity == "ResetAudit" || entity == "GuildServerConfig" ||
				(entity == "ResetOperation" && attributeString(item, "operation_id") == strings.TrimSpace(currentResetOperationID)) {
				continue
			}
			pk, sk := attributeString(item, "pk"), attributeString(item, "sk")
			if pk == "" || sk == "" {
				return nil, 0, nil, fmt.Errorf("metadata item missing key")
			}
			keys = append(keys, metadataKey{pk, sk})
			if entity == "Session" {
				sessions++
			}
			if entity == "SessionCard" || entity == "SessionModlist" {
				channel, message := attributeString(item, "channel_id"), attributeString(item, "message_id")
				if channel != "" && message != "" {
					refs = append(refs, [2]string{channel, message})
				}
			}
		}
		if len(output.LastEvaluatedKey) == 0 {
			break
		}
		start = output.LastEvaluatedKey
	}
	return keys, sessions, refs, nil
}
func attributeString(item map[string]ddbtypes.AttributeValue, name string) string {
	if value, ok := item[name].(*ddbtypes.AttributeValueMemberS); ok {
		return value.Value
	}
	return ""
}
func (cleaner *Cleaner) deleteMetadata(ctx context.Context, keys []metadataKey) error {
	for _, key := range keys {
		if _, err := cleaner.dynamo.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(cleaner.config.Table), Key: map[string]ddbtypes.AttributeValue{"pk": &ddbtypes.AttributeValueMemberS{Value: key.pk}, "sk": &ddbtypes.AttributeValueMemberS{Value: key.sk}}}); err != nil {
			return err
		}
	}
	return nil
}

func (cleaner *Cleaner) purgeQueues(ctx context.Context) error {
	for _, url := range cleaner.config.QueueURLs {
		if strings.TrimSpace(url) != "" {
			if _, err := cleaner.sqs.PurgeQueue(ctx, &sqs.PurgeQueueInput{QueueUrl: aws.String(url)}); err != nil && !hasAPIErrorCode(err, "PurgeQueueInProgress") {
				return err
			}
		}
	}
	return nil
}
func (cleaner *Cleaner) stopExecutions(ctx context.Context) error {
	for _, arn := range cleaner.config.StateMachineARNs {
		var token *string
		for {
			output, err := cleaner.sfn.ListExecutions(ctx, &sfn.ListExecutionsInput{StateMachineArn: aws.String(arn), StatusFilter: sfntypes.ExecutionStatusRunning, NextToken: token})
			if err != nil {
				return err
			}
			for _, execution := range output.Executions {
				if _, err := cleaner.sfn.StopExecution(ctx, &sfn.StopExecutionInput{ExecutionArn: execution.ExecutionArn, Error: aws.String("PlatformReset"), Cause: aws.String("Administrator-confirmed runtime reset")}); err != nil && !hasAPIErrorCode(err, "ExecutionDoesNotExist", "ExecutionNotRunning") {
					return err
				}
			}
			if output.NextToken == nil {
				break
			}
			token = output.NextToken
		}
	}
	return nil
}
func (cleaner *Cleaner) deleteOldLogStreams(ctx context.Context, cutoff time.Time) error {
	cutoffMillis := cutoff.UTC().UnixMilli()
	for _, group := range cleaner.config.LogGroups {
		var token *string
		for {
			output, err := cleaner.logs.DescribeLogStreams(ctx, &cloudwatchlogs.DescribeLogStreamsInput{LogGroupName: aws.String(group), NextToken: token})
			if err != nil {
				return err
			}
			for _, stream := range output.LogStreams {
				if stream.LastEventTimestamp == nil || *stream.LastEventTimestamp <= cutoffMillis {
					if _, err := cleaner.logs.DeleteLogStream(ctx, &cloudwatchlogs.DeleteLogStreamInput{LogGroupName: aws.String(group), LogStreamName: stream.LogStreamName}); err != nil && !hasAPIErrorCode(err, "ResourceNotFoundException") {
						return err
					}
				}
			}
			if output.NextToken == nil {
				break
			}
			token = output.NextToken
		}
	}
	return nil
}

func hasAPIErrorCode(err error, codes ...string) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	for _, code := range codes {
		if apiErr.ErrorCode() == code {
			return true
		}
	}
	return false
}
