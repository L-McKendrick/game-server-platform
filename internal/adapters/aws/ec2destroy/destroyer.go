package ec2destroy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"

	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type API interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	TerminateInstances(context.Context, *ec2.TerminateInstancesInput, ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error)
	DeleteVolume(context.Context, *ec2.DeleteVolumeInput, ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error)
}

type Destroyer struct {
	client               API
	project, environment string
}

var _ ports.InfrastructureDestroyer = (*Destroyer)(nil)

func New(client API, project string, environment string) (*Destroyer, error) {
	project, environment = strings.TrimSpace(project), strings.TrimSpace(environment)
	if client == nil || project == "" || environment == "" {
		return nil, fmt.Errorf("EC2 client, project, and environment are required")
	}
	return &Destroyer{client: client, project: project, environment: environment}, nil
}

func (destroyer *Destroyer) TerminateInstance(ctx context.Context, sessionID string, instanceID string) error {
	instance, found, err := destroyer.instance(ctx, sessionID, instanceID)
	if err != nil || !found {
		return err
	}
	if instance.State != nil && instance.State.Name == ec2types.InstanceStateNameTerminated {
		return nil
	}
	_, err = destroyer.client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return fmt.Errorf("terminate managed instance: %w", err)
	}
	return nil
}

func (destroyer *Destroyer) InstanceTerminated(ctx context.Context, sessionID string, instanceID string) (bool, error) {
	instance, found, err := destroyer.instance(ctx, sessionID, instanceID)
	if err != nil || !found {
		return !found, err
	}
	return instance.State != nil && instance.State.Name == ec2types.InstanceStateNameTerminated, nil
}

func (destroyer *Destroyer) DeleteVolume(ctx context.Context, sessionID string, volumeID string) error {
	volume, found, err := destroyer.volume(ctx, sessionID, volumeID)
	if err != nil || !found {
		return err
	}
	if volume.State != ec2types.VolumeStateAvailable {
		return fmt.Errorf("managed data volume is not detached")
	}
	_, err = destroyer.client.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: aws.String(volumeID)})
	if err != nil && !notFound(err) {
		return fmt.Errorf("delete managed data volume: %w", err)
	}
	return nil
}

func (destroyer *Destroyer) VolumeDeleted(ctx context.Context, sessionID string, volumeID string) (bool, error) {
	_, found, err := destroyer.volume(ctx, sessionID, volumeID)
	return !found, err
}

func (destroyer *Destroyer) instance(ctx context.Context, sessionID string, instanceID string) (ec2types.Instance, bool, error) {
	sessionID, instanceID = strings.TrimSpace(sessionID), strings.TrimSpace(instanceID)
	if sessionID == "" || instanceID == "" {
		return ec2types.Instance{}, false, fmt.Errorf("session and instance IDs are required")
	}
	output, err := destroyer.client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		if notFound(err) {
			return ec2types.Instance{}, false, nil
		}
		return ec2types.Instance{}, false, fmt.Errorf("describe managed instance: %w", err)
	}
	for _, reservation := range output.Reservations {
		for _, instance := range reservation.Instances {
			if aws.ToString(instance.InstanceId) == instanceID {
				if err := destroyer.validateTags(instance.Tags, sessionID); err != nil {
					return ec2types.Instance{}, false, err
				}
				return instance, true, nil
			}
		}
	}
	return ec2types.Instance{}, false, nil
}

func (destroyer *Destroyer) volume(ctx context.Context, sessionID string, volumeID string) (ec2types.Volume, bool, error) {
	sessionID, volumeID = strings.TrimSpace(sessionID), strings.TrimSpace(volumeID)
	if sessionID == "" || volumeID == "" {
		return ec2types.Volume{}, false, fmt.Errorf("session and volume IDs are required")
	}
	output, err := destroyer.client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{volumeID}})
	if err != nil {
		if notFound(err) {
			return ec2types.Volume{}, false, nil
		}
		return ec2types.Volume{}, false, fmt.Errorf("describe managed volume: %w", err)
	}
	for _, volume := range output.Volumes {
		if aws.ToString(volume.VolumeId) == volumeID {
			if err := destroyer.validateTags(volume.Tags, sessionID); err != nil {
				return ec2types.Volume{}, false, err
			}
			return volume, true, nil
		}
	}
	return ec2types.Volume{}, false, nil
}

func (destroyer *Destroyer) validateTags(tags []ec2types.Tag, sessionID string) error {
	want := map[string]string{"Project": destroyer.project, "Environment": destroyer.environment, "SessionId": sessionID}
	seen := map[string]string{}
	for _, tag := range tags {
		seen[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	for key, value := range want {
		if seen[key] != value {
			return fmt.Errorf("refusing destructive action: %s tag mismatch", key)
		}
	}
	return nil
}

func notFound(err error) bool {
	var api smithy.APIError
	return errors.As(err, &api) && (api.ErrorCode() == "InvalidInstanceID.NotFound" || api.ErrorCode() == "InvalidVolume.NotFound")
}
