package ec2orphans

import (
	"context"
	"fmt"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type API interface {
	CreateTags(context.Context, *ec2.CreateTagsInput, ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	TerminateInstances(context.Context, *ec2.TerminateInstancesInput, ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DeleteVolume(context.Context, *ec2.DeleteVolumeInput, ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error)
}

type Cleaner struct {
	client               API
	project, environment string
}

var _ ports.OrphanCleaner = (*Cleaner)(nil)

func New(client API, project, environment string) (*Cleaner, error) {
	project, environment = strings.TrimSpace(project), strings.TrimSpace(environment)
	if client == nil || project == "" || environment == "" {
		return nil, fmt.Errorf("EC2 client, project, and environment are required")
	}
	return &Cleaner{client: client, project: project, environment: environment}, nil
}

func (cleaner *Cleaner) Quarantine(ctx context.Context, finding domain.OrphanFinding) error {
	if err := cleaner.validateFinding(finding); err != nil {
		return err
	}
	if finding.Resource.Kind != domain.ResourceEC2Instance && finding.Resource.Kind != domain.ResourceEBSVolume {
		return fmt.Errorf("resource kind is report-only")
	}
	_, err := cleaner.client.CreateTags(ctx, &ec2.CreateTagsInput{Resources: []string{finding.Resource.ID}, Tags: []ec2types.Tag{{Key: aws.String("OrphanStatus"), Value: aws.String("Quarantined")}, {Key: aws.String("OrphanFindingId"), Value: aws.String(finding.ID)}}})
	return err
}

func (cleaner *Cleaner) Cleanup(ctx context.Context, finding domain.OrphanFinding) error {
	if err := cleaner.validateFinding(finding); err != nil {
		return err
	}
	switch finding.Resource.Kind {
	case domain.ResourceEC2Instance:
		output, err := cleaner.client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{finding.Resource.ID}})
		if err != nil {
			return err
		}
		for _, reservation := range output.Reservations {
			for _, instance := range reservation.Instances {
				if err := cleaner.validateLiveTags(instance.Tags, finding); err != nil {
					return err
				}
				if instance.State.Name == ec2types.InstanceStateNameTerminated {
					return nil
				}
				_, err = cleaner.client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{finding.Resource.ID}})
				return err
			}
		}
		return nil
	case domain.ResourceEBSVolume:
		output, err := cleaner.client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{finding.Resource.ID}})
		if err != nil {
			return err
		}
		if len(output.Volumes) == 0 {
			return nil
		}
		volume := output.Volumes[0]
		if err := cleaner.validateLiveTags(volume.Tags, finding); err != nil {
			return err
		}
		if volume.State != ec2types.VolumeStateAvailable {
			return fmt.Errorf("refusing orphan cleanup: volume is not detached")
		}
		_, err = cleaner.client.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: aws.String(finding.Resource.ID)})
		return err
	default:
		return fmt.Errorf("resource kind is report-only")
	}
}

func (cleaner *Cleaner) validateFinding(finding domain.OrphanFinding) error {
	if err := finding.Validate(); err != nil {
		return err
	}
	if !finding.Resource.ImmutableTagsMatch(cleaner.project, cleaner.environment) {
		return fmt.Errorf("refusing orphan action: immutable tag mismatch")
	}
	return nil
}
func (cleaner *Cleaner) validateLiveTags(tags []ec2types.Tag, finding domain.OrphanFinding) error {
	seen := map[string]string{}
	for _, tag := range tags {
		seen[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	want := map[string]string{"Project": cleaner.project, "Environment": cleaner.environment, "SessionId": finding.Resource.SessionID, "OrphanStatus": "Quarantined", "OrphanFindingId": finding.ID}
	for key, value := range want {
		if seen[key] != value {
			return fmt.Errorf("refusing orphan cleanup: %s tag mismatch", key)
		}
	}
	return nil
}
