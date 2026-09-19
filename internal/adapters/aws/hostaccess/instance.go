package hostaccess

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type InstanceClient interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

type InstanceAuthority struct {
	Client               InstanceClient
	Project, Environment string
}

// VerifyHostInstance checks an exact instance, never a discovery-index result.
func (authority InstanceAuthority) VerifyHostInstance(ctx context.Context, sessionID, instanceID string) error {
	denied := fmt.Errorf("host instance authority unavailable or mismatched")
	if authority.Client == nil || authority.Project == "" || authority.Environment == "" || sessionID == "" || instanceID == "" {
		return denied
	}
	output, err := authority.Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil || output == nil {
		return denied
	}
	var instances []types.Instance
	for _, reservation := range output.Reservations {
		instances = append(instances, reservation.Instances...)
	}
	if len(instances) != 1 {
		return denied
	}
	instance := instances[0]
	if aws.ToString(instance.InstanceId) != instanceID || instance.State == nil || instance.State.Name != types.InstanceStateNameRunning {
		return denied
	}
	expected := map[string]string{"Project": authority.Project, "Environment": authority.Environment, "SessionId": sessionID}
	seen := make(map[string]bool)
	for _, tag := range instance.Tags {
		key := aws.ToString(tag.Key)
		if value, required := expected[key]; required {
			if seen[key] || aws.ToString(tag.Value) != value {
				return denied
			}
			seen[key] = true
		}
	}
	if len(seen) != len(expected) {
		return denied
	}
	return nil
}
