package hosttransfer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"strings"
)

type CommandClient interface {
	ListCommands(context.Context, *ssm.ListCommandsInput, ...func(*ssm.Options)) (*ssm.ListCommandsOutput, error)
	SendCommand(context.Context, *ssm.SendCommandInput, ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
}

func CommandScope(script string) (domain.HostAccessScope, error) {
	var scope domain.HostAccessScope
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(line, "# GSP_HOST_ACCESS:") {
			if scope.SessionID != "" || len(line) > 2048 {
				return scope, domain.ErrConflict
			}
			payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "# GSP_HOST_ACCESS:"))
			if err != nil || json.Unmarshal(payload, &scope) != nil || scope.Validate() != nil {
				return domain.HostAccessScope{}, domain.ErrConflict
			}
		}
	}
	if scope.SessionID == "" {
		return scope, domain.ErrNotFound
	}
	return scope, nil
}

func RenewCommand(ctx context.Context, client CommandClient, renewal ports.HostCommandRenewal, instanceID, commandID string) error {
	listed, err := client.ListCommands(ctx, &ssm.ListCommandsInput{CommandId: aws.String(commandID)})
	if err != nil || listed == nil || len(listed.Commands) != 1 {
		return fmt.Errorf("host access command inspection unavailable")
	}
	command := listed.Commands[0]
	if aws.ToString(command.CommandId) != commandID || len(command.InstanceIds) != 1 || command.InstanceIds[0] != instanceID || aws.ToString(command.DocumentName) != "AWS-RunShellScript" || len(command.Parameters["commands"]) != 1 {
		return domain.ErrConflict
	}
	scope, err := CommandScope(command.Parameters["commands"][0])
	if err != nil || scope.InstanceID != instanceID {
		return domain.ErrConflict
	}
	reference, err := renewal.RefreshHostCommand(ctx, scope)
	if err != nil {
		return err
	}
	if reference.URL == "" {
		return nil
	}
	if !reference.Scope.Equal(scope) {
		return domain.ErrConflict
	}
	comment := fmt.Sprintf("gsp:host-refresh:%s:%d", scope.AttemptID, reference.Generation)
	var token *string
	for {
		page, err := client.ListCommands(ctx, &ssm.ListCommandsInput{InstanceId: aws.String(instanceID), MaxResults: aws.Int32(50), NextToken: token})
		if err != nil || page == nil {
			return fmt.Errorf("host refresh recovery unavailable")
		}
		for _, candidate := range page.Commands {
			if aws.ToString(candidate.Comment) != comment || len(candidate.InstanceIds) != 1 || candidate.InstanceIds[0] != instanceID || aws.ToString(candidate.DocumentName) != "AWS-RunShellScript" {
				continue
			}
			if candidate.Status == types.CommandStatusSuccess {
				return renewal.RecordHostRefresh(ctx, scope, reference.Generation)
			}
			if candidate.Status == types.CommandStatusPending || candidate.Status == types.CommandStatusInProgress {
				return nil
			}
		}
		if page.NextToken == nil || aws.ToString(page.NextToken) == "" {
			break
		}
		token = page.NextToken
	}
	shell, err := ReferenceShell(reference)
	if err != nil {
		return err
	}
	script := "#!/usr/bin/env bash\n" + "set -Eeuo pipefail\numask 077\n" + shell
	if len(script) > domain.MaxHostAccessCommandBytes {
		return fmt.Errorf("host refresh command exceeds budget")
	}
	_, err = client.SendCommand(ctx, &ssm.SendCommandInput{DocumentName: aws.String("AWS-RunShellScript"), InstanceIds: []string{instanceID}, Comment: aws.String(comment), Parameters: map[string][]string{"commands": {script}, "executionTimeout": {"30"}}, TimeoutSeconds: aws.Int32(30)})
	if err != nil {
		return fmt.Errorf("host refresh delivery ambiguous")
	}
	return nil
}
