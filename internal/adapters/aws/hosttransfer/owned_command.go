package hosttransfer

import (
	"context"
	"fmt"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func OwnedCommand(ctx context.Context, client CommandClient, instanceID, commandID string) (types.Command, domain.HostAccessScope, error) {
	output, err := client.ListCommands(ctx, &ssm.ListCommandsInput{CommandId: aws.String(commandID)})
	if err != nil || output == nil || len(output.Commands) != 1 {
		return types.Command{}, domain.HostAccessScope{}, fmt.Errorf("owned command inspection unavailable")
	}
	command := output.Commands[0]
	if aws.ToString(command.CommandId) != commandID || aws.ToString(command.DocumentName) != "AWS-RunShellScript" || len(command.InstanceIds) != 1 || command.InstanceIds[0] != instanceID || len(command.Parameters["commands"]) != 1 {
		return types.Command{}, domain.HostAccessScope{}, domain.ErrConflict
	}
	scope, err := CommandScope(command.Parameters["commands"][0])
	if err != nil || scope.InstanceID != instanceID {
		return types.Command{}, domain.HostAccessScope{}, domain.ErrConflict
	}
	return command, scope, nil
}

func FindOwnedCommand(ctx context.Context, client CommandClient, scope domain.HostAccessScope, comment string) (string, error) {
	var token *string
	for {
		output, err := client.ListCommands(ctx, &ssm.ListCommandsInput{InstanceId: aws.String(scope.InstanceID), MaxResults: aws.Int32(50), NextToken: token})
		if err != nil || output == nil {
			return "", fmt.Errorf("owned command recovery unavailable")
		}
		for _, command := range output.Commands {
			if aws.ToString(command.Comment) != comment {
				continue
			}
			if aws.ToString(command.DocumentName) != "AWS-RunShellScript" || len(command.InstanceIds) != 1 || command.InstanceIds[0] != scope.InstanceID || len(command.Parameters["commands"]) != 1 {
				return "", domain.ErrConflict
			}
			current, err := CommandScope(command.Parameters["commands"][0])
			if err != nil || !current.Equal(scope) || aws.ToString(command.CommandId) == "" {
				return "", domain.ErrConflict
			}
			return aws.ToString(command.CommandId), nil
		}
		if aws.ToString(output.NextToken) == "" {
			return "", nil
		}
		token = output.NextToken
	}
}
