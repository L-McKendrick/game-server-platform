package resourceinventory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type EC2API interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	DescribeSecurityGroups(context.Context, *ec2.DescribeSecurityGroupsInput, ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
}
type S3API interface {
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

type Inventory struct {
	ec2                          EC2API
	s3                           S3API
	project, environment, bucket string
}

var _ ports.ResourceInventory = (*Inventory)(nil)

func New(ec2Client EC2API, s3Client S3API, project, environment, bucket string) (*Inventory, error) {
	project, environment, bucket = strings.TrimSpace(project), strings.TrimSpace(environment), strings.TrimSpace(bucket)
	if ec2Client == nil || s3Client == nil || project == "" || environment == "" || bucket == "" {
		return nil, fmt.Errorf("inventory clients, project, environment, and bucket are required")
	}
	return &Inventory{ec2: ec2Client, s3: s3Client, project: project, environment: environment, bucket: bucket}, nil
}

func filters(project, environment string) []ec2types.Filter {
	return []ec2types.Filter{{Name: aws.String("tag:Project"), Values: []string{project}}, {Name: aws.String("tag:Environment"), Values: []string{environment}}}
}
func tagMap(tags []ec2types.Tag) map[string]string {
	result := map[string]string{}
	for _, tag := range tags {
		result[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return result
}

func (inventory *Inventory) List(ctx context.Context) ([]domain.ResourceObservation, error) {
	result := make([]domain.ResourceObservation, 0)
	instances, err := inventory.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: filters(inventory.project, inventory.environment)})
	if err != nil {
		return nil, fmt.Errorf("inventory instances: %w", err)
	}
	for _, reservation := range instances.Reservations {
		for _, instance := range reservation.Instances {
			tags := tagMap(instance.Tags)
			created := aws.ToTime(instance.LaunchTime)
			if created.IsZero() {
				created = time.Unix(1, 0).UTC()
			}
			result = append(result, domain.ResourceObservation{Kind: domain.ResourceEC2Instance, ID: aws.ToString(instance.InstanceId), SessionID: tags["SessionId"], Project: tags["Project"], Environment: tags["Environment"], State: string(instance.State.Name), CreatedAt: created, Tags: tags})
		}
	}
	volumes, err := inventory.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{Filters: filters(inventory.project, inventory.environment)})
	if err != nil {
		return nil, fmt.Errorf("inventory volumes: %w", err)
	}
	for _, volume := range volumes.Volumes {
		tags := tagMap(volume.Tags)
		created := aws.ToTime(volume.CreateTime)
		if created.IsZero() {
			created = time.Unix(1, 0).UTC()
		}
		related := make([]string, 0, len(volume.Attachments))
		for _, attachment := range volume.Attachments {
			if id := aws.ToString(attachment.InstanceId); id != "" {
				related = append(related, id)
			}
		}
		result = append(result, domain.ResourceObservation{Kind: domain.ResourceEBSVolume, ID: aws.ToString(volume.VolumeId), SessionID: tags["SessionId"], Project: tags["Project"], Environment: tags["Environment"], State: string(volume.State), CreatedAt: created, Tags: tags, RelatedIDs: related})
	}
	groups, err := inventory.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{Filters: filters(inventory.project, inventory.environment)})
	if err != nil {
		return nil, fmt.Errorf("inventory security groups: %w", err)
	}
	for _, group := range groups.SecurityGroups {
		tags := tagMap(group.Tags)
		result = append(result, domain.ResourceObservation{Kind: domain.ResourceSecurityGroup, ID: aws.ToString(group.GroupId), SessionID: tags["SessionId"], Project: tags["Project"], Environment: tags["Environment"], State: "available", CreatedAt: time.Unix(1, 0).UTC(), Tags: tags})
	}
	objects, err := inventory.s3Prefixes(ctx)
	if err != nil {
		return nil, err
	}
	return append(result, objects...), nil
}

func (inventory *Inventory) s3Prefixes(ctx context.Context) ([]domain.ResourceObservation, error) {
	oldest := map[string]time.Time{}
	var token *string
	for {
		output, err := inventory.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(inventory.bucket), Prefix: aws.String("sessions/"), ContinuationToken: token})
		if err != nil {
			return nil, fmt.Errorf("inventory session objects: %w", err)
		}
		for _, object := range output.Contents {
			parts := strings.Split(strings.TrimPrefix(aws.ToString(object.Key), "sessions/"), "/")
			if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" {
				continue
			}
			modified := aws.ToTime(object.LastModified)
			current := oldest[parts[0]]
			if current.IsZero() || (!modified.IsZero() && modified.Before(current)) {
				oldest[parts[0]] = modified
			}
		}
		if !aws.ToBool(output.IsTruncated) || output.NextContinuationToken == nil {
			break
		}
		token = output.NextContinuationToken
	}
	result := make([]domain.ResourceObservation, 0, len(oldest))
	for sessionID, created := range oldest {
		if created.IsZero() {
			created = time.Unix(1, 0).UTC()
		}
		result = append(result, domain.ResourceObservation{Kind: domain.ResourceS3Prefix, ID: "sessions/" + sessionID + "/", ARN: "arn:aws:s3:::" + inventory.bucket + "/sessions/" + sessionID + "/*", SessionID: sessionID, Project: inventory.project, Environment: inventory.environment, State: "present", CreatedAt: created, Tags: map[string]string{}})
	}
	return result, nil
}
