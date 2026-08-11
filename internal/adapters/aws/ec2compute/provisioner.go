package ec2compute

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const dataDeviceName = "/dev/sdf"

type EC2API interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	RunInstances(context.Context, *ec2.RunInstancesInput, ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
}

type SSMAPI interface {
	DescribeInstanceInformation(context.Context, *ssm.DescribeInstanceInformationInput, ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error)
}

type Provisioner struct {
	ec2 EC2API
	ssm SSMAPI
}

var _ ports.ComputeProvisioner = (*Provisioner)(nil)

func New(ec2Client EC2API, ssmClient SSMAPI) *Provisioner {
	return &Provisioner{ec2: ec2Client, ssm: ssmClient}
}

// FindInstance discovers an active instance by immutable platform tags without
// launching anything. Failure handling uses this before releasing a hard
// capacity slot so an ambiguous RunInstances response cannot exceed quota.
func (provisioner *Provisioner) FindInstance(ctx context.Context, request domain.ComputeLaunchRequest) (domain.ComputeObservation, bool, error) {
	if provisioner == nil || provisioner.ec2 == nil {
		return domain.ComputeObservation{}, false, fmt.Errorf("EC2 client is required")
	}
	if strings.TrimSpace(request.SessionID) == "" || strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.Environment) == "" {
		return domain.ComputeObservation{}, false, fmt.Errorf("session discovery tags are required")
	}
	existing, err := provisioner.findSessionInstances(ctx, request)
	if err != nil {
		return domain.ComputeObservation{}, false, err
	}
	if len(existing) > 1 {
		return domain.ComputeObservation{}, false, fmt.Errorf("multiple active instances discovered for session %s", request.SessionID)
	}
	if len(existing) == 0 {
		return domain.ComputeObservation{}, false, nil
	}
	return observation(existing[0]), true, nil
}

func (provisioner *Provisioner) EnsureInstance(ctx context.Context, request domain.ComputeLaunchRequest, knownInstanceID string) (domain.ComputeObservation, error) {
	if provisioner == nil || provisioner.ec2 == nil {
		return domain.ComputeObservation{}, fmt.Errorf("EC2 client is required")
	}
	if strings.TrimSpace(knownInstanceID) != "" {
		return provisioner.ObserveInstance(ctx, knownInstanceID)
	}
	existing, found, err := provisioner.FindInstance(ctx, request)
	if err != nil {
		return domain.ComputeObservation{}, err
	}
	if found {
		return existing, nil
	}
	if err := validateLaunchRequest(request); err != nil {
		return domain.ComputeObservation{}, err
	}
	tags := []ec2types.Tag{
		{Key: aws.String("Project"), Value: aws.String(request.Project)},
		{Key: aws.String("Environment"), Value: aws.String(request.Environment)},
		{Key: aws.String("ManagedBy"), Value: aws.String("game-server-platform")},
		{Key: aws.String("Component"), Value: aws.String("game-server")},
		{Key: aws.String("SessionId"), Value: aws.String(request.SessionID)},
		{Key: aws.String("GameType"), Value: aws.String(request.GameType)},
		{Key: aws.String("LifecycleState"), Value: aws.String("PROVISIONING")},
	}
	userData := base64.StdEncoding.EncodeToString([]byte("#cloud-config\npackage_update: false\nruncmd:\n  - [mkdir, -p, /srv/game-server]\n"))
	result, err := provisioner.ec2.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId: aws.String(request.AMIID), InstanceType: ec2types.InstanceType(request.InstanceType),
		MinCount: aws.Int32(1), MaxCount: aws.Int32(1), ClientToken: aws.String("provision-" + request.SessionID),
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{Name: aws.String(request.InstanceProfile)},
		NetworkInterfaces: []ec2types.InstanceNetworkInterfaceSpecification{{
			DeviceIndex: aws.Int32(0), AssociatePublicIpAddress: aws.Bool(true),
			SubnetId: aws.String(request.SubnetID), Groups: append([]string(nil), request.SecurityGroupIDs...),
		}},
		BlockDeviceMappings: []ec2types.BlockDeviceMapping{
			{DeviceName: aws.String("/dev/xvda"), Ebs: &ec2types.EbsBlockDevice{
				DeleteOnTermination: aws.Bool(true), Encrypted: aws.Bool(true),
				VolumeSize: aws.Int32(request.RootVolumeGiB), VolumeType: ec2types.VolumeTypeGp3,
			}},
			{DeviceName: aws.String(dataDeviceName), Ebs: &ec2types.EbsBlockDevice{
				DeleteOnTermination: aws.Bool(false), Encrypted: aws.Bool(true),
				VolumeSize: aws.Int32(request.DataVolumeGiB), VolumeType: ec2types.VolumeTypeGp3,
			}},
		},
		MetadataOptions: &ec2types.InstanceMetadataOptionsRequest{
			HttpEndpoint: ec2types.InstanceMetadataEndpointStateEnabled,
			HttpTokens:   ec2types.HttpTokensStateRequired,
		},
		InstanceInitiatedShutdownBehavior: ec2types.ShutdownBehaviorStop,
		TagSpecifications: []ec2types.TagSpecification{
			{ResourceType: ec2types.ResourceTypeInstance, Tags: tags},
			{ResourceType: ec2types.ResourceTypeVolume, Tags: tags},
		},
		UserData: aws.String(userData),
	})
	if err != nil {
		return domain.ComputeObservation{}, fmt.Errorf("run EC2 instance: %w", err)
	}
	if len(result.Instances) != 1 {
		return domain.ComputeObservation{}, fmt.Errorf("EC2 launch returned %d instances", len(result.Instances))
	}
	return observation(result.Instances[0]), nil
}

func (provisioner *Provisioner) ObserveInstance(ctx context.Context, instanceID string) (domain.ComputeObservation, error) {
	if provisioner == nil || provisioner.ec2 == nil || strings.TrimSpace(instanceID) == "" {
		return domain.ComputeObservation{}, fmt.Errorf("EC2 client and instance ID are required")
	}
	output, err := provisioner.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{strings.TrimSpace(instanceID)}})
	if err != nil {
		return domain.ComputeObservation{}, fmt.Errorf("describe EC2 instance: %w", err)
	}
	instances := flatten(output)
	if len(instances) != 1 {
		return domain.ComputeObservation{}, fmt.Errorf("expected one EC2 instance, found %d", len(instances))
	}
	return observation(instances[0]), nil
}

func (provisioner *Provisioner) IsManaged(ctx context.Context, instanceID string) (bool, error) {
	if provisioner == nil || provisioner.ssm == nil || strings.TrimSpace(instanceID) == "" {
		return false, fmt.Errorf("Systems Manager client and instance ID are required")
	}
	output, err := provisioner.ssm.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
		Filters: []ssmtypes.InstanceInformationStringFilter{{Key: aws.String("InstanceIds"), Values: []string{strings.TrimSpace(instanceID)}}},
	})
	if err != nil {
		return false, fmt.Errorf("describe Systems Manager node: %w", err)
	}
	for _, information := range output.InstanceInformationList {
		if aws.ToString(information.InstanceId) == strings.TrimSpace(instanceID) && information.PingStatus == ssmtypes.PingStatusOnline {
			return true, nil
		}
	}
	return false, nil
}

func (provisioner *Provisioner) findSessionInstances(ctx context.Context, request domain.ComputeLaunchRequest) ([]ec2types.Instance, error) {
	output, err := provisioner.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{Filters: []ec2types.Filter{
		{Name: aws.String("tag:Project"), Values: []string{request.Project}},
		{Name: aws.String("tag:Environment"), Values: []string{request.Environment}},
		{Name: aws.String("tag:SessionId"), Values: []string{request.SessionID}},
		{Name: aws.String("instance-state-name"), Values: []string{"pending", "running", "stopping", "stopped"}},
	}})
	if err != nil {
		return nil, fmt.Errorf("discover session instance: %w", err)
	}
	return flatten(output), nil
}

func validateLaunchRequest(request domain.ComputeLaunchRequest) error {
	switch {
	case request.SessionID == "", request.Project == "", request.Environment == "", request.GameType == "":
		return fmt.Errorf("session launch tags are required")
	case request.AMIID == "", request.InstanceType == "", request.SubnetID == "":
		return fmt.Errorf("AMI, instance type, and subnet are required")
	case len(request.SecurityGroupIDs) == 0, request.InstanceProfile == "":
		return fmt.Errorf("security group and instance profile are required")
	case request.RootVolumeGiB < 8, request.DataVolumeGiB < 20:
		return fmt.Errorf("volume sizes are below the supported minimum")
	default:
		return nil
	}
}

func flatten(output *ec2.DescribeInstancesOutput) []ec2types.Instance {
	if output == nil {
		return nil
	}
	instances := []ec2types.Instance{}
	for _, reservation := range output.Reservations {
		instances = append(instances, reservation.Instances...)
	}
	return instances
}

func observation(instance ec2types.Instance) domain.ComputeObservation {
	result := domain.ComputeObservation{
		InstanceID: aws.ToString(instance.InstanceId), PublicIPv4: aws.ToString(instance.PublicIpAddress),
	}
	if instance.State != nil {
		result.State = string(instance.State.Name)
	}
	if instance.Placement != nil {
		result.AvailabilityZone = aws.ToString(instance.Placement.AvailabilityZone)
	}
	for _, mapping := range instance.BlockDeviceMappings {
		if aws.ToString(mapping.DeviceName) == dataDeviceName && mapping.Ebs != nil {
			result.DataVolumeID = aws.ToString(mapping.Ebs.VolumeId)
			break
		}
	}
	return result
}
