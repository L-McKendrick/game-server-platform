package ssmbootstrap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/hosttransfer"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

func (runner *Runner) WithHostAccess(access ports.HostCommandAccess) *Runner {
	runner.config.HostAccess = access
	return runner
}

func (runner *Runner) capabilityCommand(ctx context.Context, session domain.Session, rollback, pending bool, purpose string, needsSteam bool, commandContext ...map[string]string) (string, string, error) {
	stage := purpose
	if rollback {
		stage = "rollback"
	}
	exchangeReference := ""
	reference, err := runner.config.HostAccess.PrepareHostCommand(ctx, session.ID, stage, rollback, time.Duration(runner.config.TimeoutSeconds)*time.Second, func(ctx context.Context, prior map[string]string) (map[string]string, error) {
		values, exchange, err := runner.commandEnvironment(ctx, session, rollback, pending, purpose, needsSteam, prior)
		if err != nil {
			return nil, err
		}
		for _, extra := range commandContext {
			for key, value := range extra {
				values[key] = value
			}
		}
		exchangeReference = exchange
		return values, err
	})
	if err != nil {
		return "", "", err
	}
	if reference.Scope.SessionID != session.ID || reference.Scope.InstanceID != session.Infrastructure.InstanceID || reference.Scope.OperationID != session.ActiveWorkflowID {
		return "", "", domain.ErrConflict
	}
	shell, err := hosttransfer.ReferenceShell(reference)
	if err != nil {
		return "", "", err
	}
	var command strings.Builder
	command.WriteString(bashShebang + "set -Eeuo pipefail\numask 077\nbootstrap_script=''\n")
	command.WriteString("trap '[ -z \"$bootstrap_script\" ] || rm -f -- \"$bootstrap_script\"; [ -z \"${HOST_ACCESS_ATTEMPT_DIR:-}\" ] || rm -rf -- \"$HOST_ACCESS_ATTEMPT_DIR\"' EXIT\n")
	command.WriteString(shell)
	scopePayload, _ := json.Marshal(reference.Scope)
	command.WriteString("# GSP_HOST_ACCESS:" + base64.StdEncoding.EncodeToString(scopePayload) + "\n")
	command.WriteString(fmt.Sprintf("export TEAMSPEAK_ENABLED=%t VANILLA_MODE=%t\n", session.TeamSpeakEnabled, session.Vanilla))
	command.WriteString("bootstrap_script=\"$(mktemp /run/gsp-bootstrap.XXXXXX)\"\n")
	command.WriteString("download_key=\"$(printf '%s' '" + base64.StdEncoding.EncodeToString([]byte(runner.config.BootstrapScriptKey)) + "' | base64 -d)\"\n")
	command.WriteString("python3 \"$HOST_ACCESS_HELPER\" fetch \"$HOST_ACCESS_MANIFEST\" \"$download_key\" \"$bootstrap_script\"\nchmod 700 \"$bootstrap_script\"\n\"$bootstrap_script\"\n")
	if command.Len() > domain.MaxHostAccessCommandBytes {
		return "", "", fmt.Errorf("bootstrap host command exceeds budget")
	}
	return command.String(), exchangeReference, nil
}
