package ssmrestore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/hosttransfer"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type API interface {
	SendCommand(context.Context, *ssm.SendCommandInput, ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
}

type Runner struct {
	client         API
	bucket, region string
	timeout        int32
	access         ArchiveAccess
}

type ArchiveAccess interface {
	PrepareRestoreArchive(context.Context, string, time.Duration) (ports.HostAccessReference, error)
	ports.HostCommandRenewal
}

func (runner *Runner) WithArchiveAccess(access ArchiveAccess) *Runner {
	runner.access = access
	return runner
}

var _ ports.RestoreRunner = (*Runner)(nil)

func New(client API, bucket string, region string, timeout int32) (*Runner, error) {
	bucket, region = strings.TrimSpace(bucket), strings.TrimSpace(region)
	if client == nil || bucket == "" || region == "" {
		return nil, fmt.Errorf("SSM client, archive bucket, and region are required")
	}
	if timeout < 300 || timeout > 86400 {
		return nil, fmt.Errorf("restore timeout must be between 300 and 86400 seconds")
	}
	return &Runner{client: client, bucket: bucket, region: region, timeout: timeout}, nil
}

func (runner *Runner) Start(ctx context.Context, session domain.Session) (string, error) {
	if session.LifecycleState != domain.StateRestoring || session.ActiveWorkflowType != domain.RestoreWorkflowType || session.Infrastructure.InstanceID == "" || session.Infrastructure.DataVolumeID == "" || session.Archive.Validate() != nil {
		return "", fmt.Errorf("%w: active restore workflow, instance, data volume, and verified archive are required", domain.ErrInvalidTransition)
	}
	if runner.access == nil {
		return "", fmt.Errorf("scoped restore archive delivery is required")
	}
	reference, err := runner.access.PrepareRestoreArchive(ctx, session.ID, time.Duration(runner.timeout)*time.Second)
	if err != nil {
		return "", fmt.Errorf("prepare restore archive delivery: %w", err)
	}
	if reference.Scope.SessionID != session.ID || reference.Scope.OperationID != session.ActiveWorkflowID || reference.Scope.InstanceID != session.Infrastructure.InstanceID {
		return "", domain.ErrConflict
	}
	client, ok := runner.client.(hosttransfer.CommandClient)
	if !ok {
		return "", fmt.Errorf("restore command ownership inspection is required")
	}
	comment := "gsp:restore:" + reference.Scope.AttemptID
	var token *string
	for {
		page, listErr := client.ListCommands(ctx, &ssm.ListCommandsInput{InstanceId: aws.String(reference.Scope.InstanceID), MaxResults: aws.Int32(50), NextToken: token})
		if listErr != nil || page == nil {
			return "", fmt.Errorf("restore command recovery unavailable")
		}
		for _, candidate := range page.Commands {
			if aws.ToString(candidate.Comment) != comment || aws.ToString(candidate.DocumentName) != "AWS-RunShellScript" || len(candidate.InstanceIds) != 1 || candidate.InstanceIds[0] != reference.Scope.InstanceID || len(candidate.Parameters["commands"]) != 1 {
				continue
			}
			scope, markerErr := hosttransfer.CommandScope(candidate.Parameters["commands"][0])
			if markerErr != nil || !scope.Equal(reference.Scope) || aws.ToString(candidate.CommandId) == "" {
				return "", domain.ErrConflict
			}
			return aws.ToString(candidate.CommandId), nil
		}
		if aws.ToString(page.NextToken) == "" {
			break
		}
		token = page.NextToken
	}
	accessShell, err := hosttransfer.ReferenceShell(reference)
	if err != nil {
		return "", err
	}
	scopeJSON, _ := json.Marshal(reference.Scope)
	script := "#!/usr/bin/env bash\nset -Eeuo pipefail\numask 077\ntrap '[ -z \"${HOST_ACCESS_ATTEMPT_DIR:-}\" ] || rm -rf -- \"$HOST_ACCESS_ATTEMPT_DIR\"' EXIT\n" + accessShell + "# GSP_HOST_ACCESS:" + base64.StdEncoding.EncodeToString(scopeJSON) + "\n" + runner.command(session)
	if len(script) > domain.MaxHostAccessCommandBytes {
		return "", fmt.Errorf("restore command exceeds access budget")
	}
	output, err := runner.client.SendCommand(ctx, &ssm.SendCommandInput{
		DocumentName: aws.String("AWS-RunShellScript"), InstanceIds: []string{session.Infrastructure.InstanceID},
		Comment:        aws.String(comment),
		Parameters:     map[string][]string{"commands": {script}, "executionTimeout": {fmt.Sprintf("%d", runner.timeout)}},
		TimeoutSeconds: aws.Int32(60),
	})
	if err != nil {
		return "", fmt.Errorf("send restore command: %w", err)
	}
	if output.Command == nil || strings.TrimSpace(aws.ToString(output.Command.CommandId)) == "" {
		return "", fmt.Errorf("Systems Manager returned no command ID")
	}
	return aws.ToString(output.Command.CommandId), nil
}

func (runner *Runner) Observe(ctx context.Context, instanceID string, commandID string) (ports.BootstrapCommandStatus, error) {
	instanceID, commandID = strings.TrimSpace(instanceID), strings.TrimSpace(commandID)
	if instanceID == "" || commandID == "" {
		return ports.BootstrapCommandStatus{}, fmt.Errorf("instance and command IDs are required")
	}
	output, err := runner.client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{CommandId: aws.String(commandID), InstanceId: aws.String(instanceID)})
	if err != nil {
		var pending *types.InvocationDoesNotExist
		if errors.As(err, &pending) {
			return ports.BootstrapCommandStatus{Status: "Pending"}, nil
		}
		return ports.BootstrapCommandStatus{}, fmt.Errorf("observe restore command: %w", err)
	}
	stderr := aws.ToString(output.StandardErrorContent)
	if runner.access != nil && (output.Status == types.CommandInvocationStatusPending || output.Status == types.CommandInvocationStatusInProgress || output.Status == types.CommandInvocationStatusDelayed) {
		client, ok := runner.client.(hosttransfer.CommandClient)
		if !ok {
			return ports.BootstrapCommandStatus{}, fmt.Errorf("restore command ownership inspection is required")
		}
		if err := hosttransfer.RenewCommand(ctx, client, runner.access, instanceID, commandID); err != nil {
			return ports.BootstrapCommandStatus{}, err
		}
	}
	message := domain.SanitizeDiagnosticTail(stderr)
	errorCode := ""
	if strings.Contains(stderr, "ERR_AWS_CLI_PREREQUISITE:") {
		errorCode = "ERR_AWS_CLI_PREREQUISITE"
		message = "A verified AWS CLI v2 was unavailable on the replacement server."
	} else if strings.Contains(stderr, "ERR_RESTORE_DATA_VOLUME:") {
		errorCode = "ERR_RESTORE_DATA_VOLUME"
		message = "The replacement data volume could not be prepared safely for archive restoration."
	}
	return ports.BootstrapCommandStatus{Status: string(output.Status), ErrorCode: errorCode, ErrorMessage: message}, nil
}

func (runner *Runner) command(session domain.Session) string {
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	voice := "false"
	if session.TeamSpeakEnabled {
		voice = "true"
	}
	return "#!/usr/bin/env bash\nset -Eeuo pipefail\numask 077\n" +
		"bucket=$(printf '%s' '" + encode(runner.bucket) + "' | base64 -d)\n" +
		"region=$(printf '%s' '" + encode(runner.region) + "' | base64 -d)\n" +
		"data_volume_id=$(printf '%s' '" + encode(session.Infrastructure.DataVolumeID) + "' | base64 -d)\n" +
		"object_key=$(printf '%s' '" + encode(session.Archive.ObjectKey) + "' | base64 -d)\n" +
		"expected_sha=$(printf '%s' '" + encode(session.Archive.SHA256) + "' | base64 -d)\n" +
		fmt.Sprintf("expected_size=%d\n", session.Archive.SizeBytes) +
		"root=/srv/game-server\n" +
		"aws_cli_tmp=''\narchive_file=''\n" +
		"trap '[ -z \"$archive_file\" ] || rm -f -- \"$archive_file\"; [ -z \"${HOST_ACCESS_ATTEMPT_DIR:-}\" ] || rm -rf -- \"$HOST_ACCESS_ATTEMPT_DIR\"' EXIT\n" +
		"exec 9>/run/gsp-restore.lock\nflock --wait 13000 9\n" +
		"command -v mkfs.xfs >/dev/null 2>&1 || { apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y xfsprogs; }\n" +
		"serial=${data_volume_id//-/}\ndevice=''\n" +
		"for _ in $(seq 1 30); do device=$(lsblk -ndo PATH,SERIAL | awk -v serial=\"$serial\" '$2 == serial {print $1; exit}'); [ -n \"$device\" ] && break; sleep 2; done\n" +
		"[ -b \"$device\" ] || { echo 'ERR_RESTORE_DATA_VOLUME: recorded data volume was not found' >&2; exit 1; }\n" +
		"if ! blkid \"$device\" >/dev/null 2>&1; then mkfs.xfs -f \"$device\"; fi\n" +
		"[ \"$(blkid -s TYPE -o value \"$device\")\" = xfs ] || { echo 'ERR_RESTORE_DATA_VOLUME: unsupported data volume filesystem' >&2; exit 1; }\n" +
		"mkdir -p \"$root\"\n" +
		"uuid=$(blkid -s UUID -o value \"$device\")\n" +
		"grep -q \"UUID=$uuid \" /etc/fstab || printf 'UUID=%s %s xfs defaults,nofail 0 2\\n' \"$uuid\" \"$root\" >> /etc/fstab\n" +
		"if ! mountpoint -q \"$root\"; then mount \"$device\" \"$root\"; fi\n" +
		"mounted_source=$(findmnt -n -o SOURCE --target \"$root\")\n" +
		"[ \"$(readlink -f \"$mounted_source\")\" = \"$(readlink -f \"$device\")\" ] || { echo 'ERR_RESTORE_DATA_VOLUME: restore root is mounted from another device' >&2; exit 1; }\n" +
		"archive_file=$(mktemp \"$root/.gsp-restore.XXXXXX.tar.gz\")\n" +
		"python3 \"$HOST_ACCESS_HELPER\" fetch \"$HOST_ACCESS_MANIFEST\" \"$object_key\" \"$archive_file\"\n" +
		"actual_size=$(stat -c '%s' \"$archive_file\")\n[ \"$actual_size\" = \"$expected_size\" ]\n" +
		"actual_sha=$(openssl dgst -sha256 -binary \"$archive_file\" | openssl base64 -A)\n[ \"$actual_sha\" = \"$expected_sha\" ]\n" +
		"export GSP_ARCHIVE_FILE=\"$archive_file\" GSP_TEAMSPEAK_ENABLED=" + voice + "\n" +
		"python3 - <<'PY'\nimport os, pathlib, tarfile\narchive=os.environ['GSP_ARCHIVE_FILE']\nvoice=os.environ['GSP_TEAMSPEAK_ENABLED']=='true'\nallowed=[('config',),('state',),('logs',),('arma3','mpmissions'),('home','.local','share')]\nif voice: allowed.append(('teamspeak',))\ntotal=count=0\nwith tarfile.open(archive, 'r:gz') as bundle:\n    for member in bundle.getmembers():\n        path=pathlib.PurePosixPath(member.name)\n        parts=path.parts\n        if path.is_absolute() or not parts or '..' in parts or member.issym() or member.islnk() or member.isdev() or member.isfifo(): raise SystemExit('unsafe archive member')\n        if not any(parts[:len(root)] == root for root in allowed): raise SystemExit('unexpected archive root')\n        total += member.size; count += 1\n        if total > 21474836480 or count > 200000: raise SystemExit('archive expansion limit exceeded')\nPY\n" +
		"id steam >/dev/null 2>&1 || useradd --home-dir /srv/game-server/home --no-create-home --shell /bin/bash steam\n" +
		"if " + voice + "; then id teamspeak >/dev/null 2>&1 || useradd --home-dir /srv/game-server/teamspeak --no-create-home --shell /sbin/nologin teamspeak; fi\n" +
		"systemctl stop arma3-server.service 2>/dev/null || true\nif " + voice + "; then systemctl stop teamspeak3-server.service 2>/dev/null || true; fi\n" +
		"rm -rf -- /srv/game-server/config /srv/game-server/state /srv/game-server/logs /srv/game-server/arma3/mpmissions /srv/game-server/home/.local/share\n" +
		"if " + voice + "; then rm -rf -- /srv/game-server/teamspeak; fi\n" +
		"tar --no-same-owner --no-same-permissions --xattrs --acls -xzf \"$archive_file\" -C /srv/game-server\n" +
		"mkdir -p /srv/game-server/config /srv/game-server/state /srv/game-server/logs /srv/game-server/arma3/mpmissions /srv/game-server/home /srv/game-server/steamcmd\n" +
		"if " + voice + "; then mkdir -p /srv/game-server/teamspeak; fi\n" +
		"rm -rf -- /srv/game-server/home/.local/share/Steam/config /srv/game-server/home/.local/share/Steam/logs /srv/game-server/home/Steam/config /srv/game-server/home/Steam/logs /srv/game-server/steamcmd/config /srv/game-server/steamcmd/logs\n" +
		"find /srv/game-server/home /srv/game-server/steamcmd -type f \\( -name 'ssfn*' -o -name 'loginusers.vdf' \\) -delete 2>/dev/null || true\n" +
		"rm -f -- /srv/game-server/state/install_steamcmd.complete /srv/game-server/state/install_arma.complete /srv/game-server/state/install_workshop*.complete /srv/game-server/state/sync_workshop_content.complete /srv/game-server/state/sync_workshop_content.*.complete /srv/game-server/state/deploy_content.complete /srv/game-server/state/deploy_content.revision-*.complete /srv/game-server/state/install_teamspeak.complete\n" +
		"chown -R steam:steam /srv/game-server/config /srv/game-server/state /srv/game-server/logs /srv/game-server/arma3/mpmissions /srv/game-server/home\n" +
		"if " + voice + "; then chown -R teamspeak:teamspeak /srv/game-server/teamspeak; fi\n"
}
