package hostaccess_test

import (
	"context"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/hostaccess"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

type instanceClient struct {
	instance  types.Instance
	duplicate bool
}

func (client instanceClient) DescribeInstances(_ context.Context, input *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if len(input.InstanceIds) != 1 || input.InstanceIds[0] != "i-123" {
		panic("unexpected authority lookup")
	}
	instances := []types.Instance{client.instance}
	if client.duplicate {
		instances = append(instances, client.instance)
	}
	return &ec2.DescribeInstancesOutput{Reservations: []types.Reservation{{Instances: instances}}}, nil
}

func TestInstanceAuthorityRequiresExactRunningHostAndUniqueTags(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		edit   func(*instanceClient)
		denied bool
	}{
		{"current", func(*instanceClient) {}, false},
		{"wrong-instance", func(c *instanceClient) { c.instance.InstanceId = aws.String("i-other") }, true},
		{"stopped", func(c *instanceClient) { c.instance.State.Name = types.InstanceStateNameStopped }, true},
		{"missing-tag", func(c *instanceClient) { c.instance.Tags = c.instance.Tags[:2] }, true},
		{"wrong-session", func(c *instanceClient) { c.instance.Tags[2].Value = aws.String("other") }, true},
		{"duplicate-tag", func(c *instanceClient) { c.instance.Tags = append(c.instance.Tags, c.instance.Tags[2]) }, true},
		{"ambiguous-instance", func(c *instanceClient) { c.duplicate = true }, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			client := instanceClient{instance: types.Instance{InstanceId: aws.String("i-123"), State: &types.InstanceState{Name: types.InstanceStateNameRunning}, Tags: []types.Tag{
				{Key: aws.String("Project"), Value: aws.String("platform")}, {Key: aws.String("Environment"), Value: aws.String("dev")}, {Key: aws.String("SessionId"), Value: aws.String("session")},
			}}}
			scenario.edit(&client)
			err := (hostaccess.InstanceAuthority{Client: client, Project: "platform", Environment: "dev"}).VerifyHostInstance(context.Background(), "session", "i-123")
			if (err != nil) != scenario.denied {
				t.Fatalf("denied=%v error=%v", scenario.denied, err)
			}
		})
	}
}
