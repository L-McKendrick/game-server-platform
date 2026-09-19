package ssmbootstrap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/hosttransfer"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

func hostScopeMarker(script string) (domain.HostAccessScope, error) {
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

func (runner *Runner) publishHostWorkshop(ctx context.Context, instanceID, commandID string) error {
	publisher, enabled := runner.config.HostAccess.(ports.HostWorkshopPublication)
	if !enabled {
		return nil
	}
	client, ok := runner.client.(commandAPI)
	if !ok {
		return fmt.Errorf("Workshop publication requires owned command inspection")
	}
	listed, err := client.ListCommands(ctx, &ssm.ListCommandsInput{CommandId: aws.String(commandID)})
	if err != nil || listed == nil || len(listed.Commands) != 1 {
		return fmt.Errorf("Workshop owned command inspection unavailable")
	}
	command := listed.Commands[0]
	if aws.ToString(command.CommandId) != commandID || len(command.InstanceIds) != 1 || command.InstanceIds[0] != instanceID || aws.ToString(command.DocumentName) != documentName || len(command.Parameters["commands"]) != 1 {
		return domain.ErrConflict
	}
	scope, err := hostScopeMarker(command.Parameters["commands"][0])
	if err != nil || scope.InstanceID != instanceID {
		return domain.ErrConflict
	}
	return publisher.PublishHostWorkshop(ctx, scope)
}

// renewHostAccess runs only from existing observations of the exact active SSM
// command. Prepared generations are retried through an owned refresh comment;
// delivery is recorded only after the install command reports success.
func (runner *Runner) renewHostAccess(ctx context.Context, instanceID, commandID string) error {
	renewal, enabled := runner.config.HostAccess.(ports.HostCommandRenewal)
	if !enabled {
		return nil
	}
	client, ok := runner.client.(hosttransfer.CommandClient)
	if !ok {
		return fmt.Errorf("host access observation requires command inspection")
	}
	return hosttransfer.RenewCommand(ctx, client, renewal, instanceID, commandID)
}
