package ec2compute

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type fakeEC2 struct {
	describeOutput *ec2.DescribeInstancesOutput
	runOutput      *ec2.RunInstancesOutput
	runInput       *ec2.RunInstancesInput
}

func (fake *fakeEC2) DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return fake.describeOutput, nil
}
func (fake *fakeEC2) RunInstances(_ context.Context, input *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	fake.runInput = input
	return fake.runOutput, nil
}

func (fake *fakeEC2) StopInstances(context.Context, *ec2.StopInstancesInput, ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	return &ec2.StopInstancesOutput{}, nil
}

func (fake *fakeEC2) StartInstances(context.Context, *ec2.StartInstancesInput, ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	return &ec2.StartInstancesOutput{}, nil
}

type fakeSSM struct{}

func (fakeSSM) DescribeInstanceInformation(context.Context, *ssm.DescribeInstanceInformationInput, ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error) {
	return &ssm.DescribeInstanceInformationOutput{}, nil
}

func TestEnsureInstanceUsesTaggedDiscoveryBeforeLaunch(t *testing.T) {
	t.Parallel()
	client := &fakeEC2{
		describeOutput: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{
				{Instances: []ec2types.Instance{
					{InstanceId: aws.String("i-existing"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning}},
				}},
			},
		},
	}
	result, err := New(client, fakeSSM{}).EnsureInstance(context.Background(), launchRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	if result.InstanceID != "i-existing" || client.runInput != nil {
		t.Fatalf("result = %#v, run input = %#v", result, client.runInput)
	}
}

func TestFindInstanceNeverLaunchesCompute(t *testing.T) {
	t.Parallel()
	client := &fakeEC2{describeOutput: &ec2.DescribeInstancesOutput{}}
	_, found, err := New(client, fakeSSM{}).FindInstance(context.Background(), launchRequest())
	if err != nil {
		t.Fatal(err)
	}
	if found || client.runInput != nil {
		t.Fatalf("found = %t, run input = %#v", found, client.runInput)
	}
}

func TestEnsureInstanceRequiresIMDSv2AndPreservesDataVolume(t *testing.T) {
	t.Parallel()
	client := &fakeEC2{
		describeOutput: &ec2.DescribeInstancesOutput{},
		runOutput:      &ec2.RunInstancesOutput{Instances: []ec2types.Instance{{InstanceId: aws.String("i-new")}}},
	}
	if _, err := New(client, fakeSSM{}).EnsureInstance(context.Background(), launchRequest(), ""); err != nil {
		t.Fatal(err)
	}
	if client.runInput == nil || client.runInput.MetadataOptions == nil || client.runInput.MetadataOptions.HttpTokens != ec2types.HttpTokensStateRequired {
		t.Fatalf("metadata options = %#v", client.runInput)
	}
	var data *ec2types.EbsBlockDevice
	for _, mapping := range client.runInput.BlockDeviceMappings {
		if aws.ToString(mapping.DeviceName) == dataDeviceName {
			data = mapping.Ebs
		}
	}
	if data == nil || aws.ToBool(data.DeleteOnTermination) || !aws.ToBool(data.Encrypted) {
		t.Fatalf("data volume mapping = %#v", data)
	}
}

func TestEnsureInstanceTagsInstanceAndVolumesWithReadableAndImmutableIdentity(t *testing.T) {
	t.Parallel()
	client := &fakeEC2{
		describeOutput: &ec2.DescribeInstancesOutput{},
		runOutput:      &ec2.RunInstancesOutput{Instances: []ec2types.Instance{{InstanceId: aws.String("i-new")}}},
	}
	if _, err := New(client, fakeSSM{}).EnsureInstance(context.Background(), launchRequest(), ""); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Name": "Saturday Arma", "SessionName": "Saturday Arma", "SessionSlug": "saturday-arma",
		"Project": "game-server-platform", "Environment": "dev", "SessionId": "session-1",
	}
	if len(client.runInput.TagSpecifications) != 2 {
		t.Fatalf("tag specifications = %d; want instance and volume", len(client.runInput.TagSpecifications))
	}
	for _, specification := range client.runInput.TagSpecifications {
		if specification.ResourceType != ec2types.ResourceTypeInstance && specification.ResourceType != ec2types.ResourceTypeVolume {
			t.Fatalf("resource type = %q; want instance or volume", specification.ResourceType)
		}
		got := make(map[string]string, len(specification.Tags))
		for _, tag := range specification.Tags {
			got[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
		}
		for key, value := range want {
			if got[key] != value {
				t.Errorf("%s tag %q = %q; want %q", specification.ResourceType, key, got[key], value)
			}
		}
	}
}

func launchRequest() domain.ComputeLaunchRequest {
	return domain.ComputeLaunchRequest{
		SessionID: "session-1", SessionName: "Saturday Arma", SessionSlug: "saturday-arma",
		GameType: "arma3", Environment: "dev", Project: "game-server-platform",
		AMIID: "ami-1", InstanceType: "c7i.large", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"},
		InstanceProfile: "profile-1", RootVolumeGiB: 30, DataVolumeGiB: 100,
	}
}
