package ec2destroy

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type fakeAPI struct {
	instance            ec2types.Instance
	volume              ec2types.Volume
	terminated, deleted bool
}

func (fake *fakeAPI) DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: []ec2types.Instance{fake.instance}}}}, nil
}
func (fake *fakeAPI) TerminateInstances(context.Context, *ec2.TerminateInstancesInput, ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	fake.terminated = true
	return &ec2.TerminateInstancesOutput{}, nil
}
func (fake *fakeAPI) DescribeVolumes(context.Context, *ec2.DescribeVolumesInput, ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return &ec2.DescribeVolumesOutput{Volumes: []ec2types.Volume{fake.volume}}, nil
}
func (fake *fakeAPI) DeleteVolume(context.Context, *ec2.DeleteVolumeInput, ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error) {
	fake.deleted = true
	return &ec2.DeleteVolumeOutput{}, nil
}

func TestDestroyer_RequiresAllImmutableTags(t *testing.T) {
	tags := []ec2types.Tag{{Key: aws.String("Project"), Value: aws.String("game-server-platform")}, {Key: aws.String("Environment"), Value: aws.String("dev")}, {Key: aws.String("SessionId"), Value: aws.String("another-session")}}
	api := &fakeAPI{instance: ec2types.Instance{InstanceId: aws.String("i-1"), Tags: tags}}
	destroyer, _ := New(api, "game-server-platform", "dev")
	if err := destroyer.TerminateInstance(context.Background(), "session-1", "i-1"); err == nil {
		t.Fatal("TerminateInstance() accepted mismatched SessionId tag")
	}
	if api.terminated {
		t.Fatal("instance was terminated despite tag mismatch")
	}
}

func TestDestroyer_DeletesOnlyDetachedTaggedVolume(t *testing.T) {
	tags := []ec2types.Tag{{Key: aws.String("Project"), Value: aws.String("game-server-platform")}, {Key: aws.String("Environment"), Value: aws.String("dev")}, {Key: aws.String("SessionId"), Value: aws.String("session-1")}}
	api := &fakeAPI{volume: ec2types.Volume{VolumeId: aws.String("vol-1"), State: ec2types.VolumeStateInUse, Tags: tags}}
	destroyer, _ := New(api, "game-server-platform", "dev")
	if err := destroyer.DeleteVolume(context.Background(), "session-1", "vol-1"); err == nil {
		t.Fatal("DeleteVolume() accepted attached volume")
	}
	api.volume.State = ec2types.VolumeStateAvailable
	if err := destroyer.DeleteVolume(context.Background(), "session-1", "vol-1"); err != nil {
		t.Fatal(err)
	}
	if !api.deleted {
		t.Fatal("detached tagged volume was not deleted")
	}
}
