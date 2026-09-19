package ssmarchive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/hosttransfer"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func (runner *Runner) uploadPrepared(ctx context.Context, instanceID, commandID string, result ports.ArchiveCommandStatus) (ports.ArchiveCommandStatus, error) {
	if runner.access == nil {
		return ports.ArchiveCommandStatus{}, fmt.Errorf("scoped archive mediation is required")
	}
	client, ok := runner.client.(hosttransfer.CommandClient)
	if !ok {
		return ports.ArchiveCommandStatus{}, fmt.Errorf("archive ownership inspection required")
	}
	command, scope, err := hosttransfer.OwnedCommand(ctx, client, instanceID, commandID)
	if err != nil || aws.ToString(command.Comment) != "gsp:archive-prepare:"+scope.AttemptID {
		return ports.ArchiveCommandStatus{}, domain.ErrConflict
	}
	expected := "sessions/" + scope.SessionID + "/archives/" + scope.OperationID + "/session.tar.gz"
	if result.ObjectKey != expected {
		return ports.ArchiveCommandStatus{}, domain.ErrConflict
	}
	comment := "gsp:archive-upload:" + scope.AttemptID
	uploadID, err := hosttransfer.FindOwnedCommand(ctx, client, scope, comment)
	if err != nil {
		return ports.ArchiveCommandStatus{}, err
	}
	if uploadID != "" {
		observed, err := runner.client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{CommandId: aws.String(uploadID), InstanceId: aws.String(instanceID)})
		if err != nil {
			var missing *types.InvocationDoesNotExist
			if errors.As(err, &missing) {
				return ports.ArchiveCommandStatus{Status: "Pending"}, nil
			}
			return ports.ArchiveCommandStatus{}, fmt.Errorf("archive upload observation unavailable")
		}
		status := string(observed.Status)
		if status == "Pending" || status == "InProgress" || status == "Delayed" {
			if err := hosttransfer.RenewCommand(ctx, client, runner.access, instanceID, uploadID); err != nil {
				return ports.ArchiveCommandStatus{}, err
			}
			return ports.ArchiveCommandStatus{Status: "InProgress"}, nil
		}
		if status == "Success" {
			// A successful HTTP response cannot replace trusted immutable HEAD
			// acceptance. Inspecting earlier would HEAD an intentionally absent
			// create-only key, which returns 403 without ListBucket.
			pin, inspectErr := runner.access.VerifyArchiveUpload(ctx, scope)
			if inspectErr != nil {
				if errors.Is(inspectErr, domain.ErrNotFound) {
					return ports.ArchiveCommandStatus{}, fmt.Errorf("archive upload succeeded without verified object")
				}
				return ports.ArchiveCommandStatus{}, inspectErr
			}
			if pin.SHA256 != result.SHA256 || pin.SizeBytes != result.SizeBytes {
				return ports.ArchiveCommandStatus{}, domain.ErrConflict
			}
			return result, nil
		}
		return ports.ArchiveCommandStatus{Status: status, ErrorMessage: domain.SanitizeDiagnosticTail(aws.ToString(observed.StandardErrorContent))}, nil
	}
	reference, err := runner.access.PrepareArchiveUpload(ctx, scope, result.SHA256, result.SizeBytes)
	if err != nil {
		return ports.ArchiveCommandStatus{}, err
	}
	if !reference.Scope.Equal(scope) {
		return ports.ArchiveCommandStatus{}, domain.ErrConflict
	}
	shell, err := hosttransfer.ReferenceShell(reference)
	if err != nil {
		return ports.ArchiveCommandStatus{}, err
	}
	marker, _ := json.Marshal(scope)
	script := "#!/usr/bin/env bash\nset -Eeuo pipefail\numask 077\ntrap '[ -z \"${HOST_ACCESS_ATTEMPT_DIR:-}\" ] || rm -rf -- \"$HOST_ACCESS_ATTEMPT_DIR\"' EXIT\n" + shell + "# GSP_HOST_ACCESS:" + base64.StdEncoding.EncodeToString(marker) + "\n" +
		"exec 9>/run/gsp-archive.lock\nflock --wait 13000 9\narchive_dir='/var/tmp/gsp-archive/" + scope.AttemptID + "'\npython3 \"$HOST_ACCESS_HELPER\" archive-upload \"$HOST_ACCESS_MANIFEST\" \"$archive_dir/session.tar.gz\"\nrm -rf -- \"$archive_dir\"\n"
	if len(script) > domain.MaxHostAccessCommandBytes {
		return ports.ArchiveCommandStatus{}, fmt.Errorf("archive upload command exceeds budget")
	}
	output, err := client.SendCommand(ctx, &ssm.SendCommandInput{DocumentName: aws.String("AWS-RunShellScript"), InstanceIds: []string{instanceID}, Comment: aws.String(comment), Parameters: map[string][]string{"commands": {script}, "executionTimeout": {fmt.Sprint(runner.timeout)}}, TimeoutSeconds: aws.Int32(60)})
	if err != nil || output == nil || output.Command == nil || aws.ToString(output.Command.CommandId) == "" {
		return ports.ArchiveCommandStatus{}, fmt.Errorf("archive upload delivery ambiguous")
	}
	return ports.ArchiveCommandStatus{Status: "InProgress"}, nil
}
