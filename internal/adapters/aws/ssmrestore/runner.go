package ssmrestore

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

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
	if session.LifecycleState != domain.StateRestoring || session.ActiveWorkflowType != domain.RestoreWorkflowType || session.Infrastructure.InstanceID == "" || session.Archive.Validate() != nil {
		return "", fmt.Errorf("%w: active restore workflow, instance, and verified archive are required", domain.ErrInvalidTransition)
	}
	output, err := runner.client.SendCommand(ctx, &ssm.SendCommandInput{
		DocumentName: aws.String("AWS-RunShellScript"), InstanceIds: []string{session.Infrastructure.InstanceID},
		Comment:        aws.String("game-server-platform restore " + session.ID),
		Parameters:     map[string][]string{"commands": {runner.command(session)}, "executionTimeout": {fmt.Sprintf("%d", runner.timeout)}},
		TimeoutSeconds: aws.Int32(60), OutputS3BucketName: aws.String(runner.bucket),
		OutputS3KeyPrefix: aws.String("sessions/" + session.ID + "/logs/restore/" + session.Archive.ID), OutputS3Region: aws.String(runner.region),
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
	message := strings.TrimSpace(aws.ToString(output.StandardErrorContent))
	if len(message) > 500 {
		message = message[len(message)-500:]
	}
	return ports.BootstrapCommandStatus{Status: string(output.Status), ErrorMessage: message}, nil
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
		"object_key=$(printf '%s' '" + encode(session.Archive.ObjectKey) + "' | base64 -d)\n" +
		"expected_sha=$(printf '%s' '" + encode(session.Archive.SHA256) + "' | base64 -d)\n" +
		fmt.Sprintf("expected_size=%d\n", session.Archive.SizeBytes) +
		"archive_file=$(mktemp /var/tmp/gsp-restore.XXXXXX.tar.gz)\ntrap 'rm -f -- \"$archive_file\"' EXIT\n" +
		"exec 9>/run/gsp-restore.lock\nflock --wait 13000 9\n" +
		"aws s3 cp \"s3://$bucket/$object_key\" \"$archive_file\" --region \"$region\" --only-show-errors\n" +
		"actual_size=$(stat -c '%s' \"$archive_file\")\n[ \"$actual_size\" = \"$expected_size\" ]\n" +
		"actual_sha=$(openssl dgst -sha256 -binary \"$archive_file\" | openssl base64 -A)\n[ \"$actual_sha\" = \"$expected_sha\" ]\n" +
		"export GSP_ARCHIVE_FILE=\"$archive_file\" GSP_TEAMSPEAK_ENABLED=" + voice + "\n" +
		"python3 - <<'PY'\nimport os, pathlib, tarfile\narchive=os.environ['GSP_ARCHIVE_FILE']\nvoice=os.environ['GSP_TEAMSPEAK_ENABLED']=='true'\nallowed=[('config',),('state',),('logs',),('arma3','mpmissions'),('home','.local','share')]\nif voice: allowed.append(('teamspeak',))\ntotal=count=0\nwith tarfile.open(archive, 'r:gz') as bundle:\n    for member in bundle.getmembers():\n        path=pathlib.PurePosixPath(member.name)\n        parts=path.parts\n        if path.is_absolute() or not parts or '..' in parts or member.issym() or member.islnk() or member.isdev() or member.isfifo(): raise SystemExit('unsafe archive member')\n        if not any(parts[:len(root)] == root for root in allowed): raise SystemExit('unexpected archive root')\n        total += member.size; count += 1\n        if total > 21474836480 or count > 200000: raise SystemExit('archive expansion limit exceeded')\nPY\n" +
		"systemctl stop arma3-server.service\nif " + voice + "; then systemctl stop teamspeak3-server.service; fi\n" +
		"rm -rf -- /srv/game-server/config /srv/game-server/state /srv/game-server/logs /srv/game-server/arma3/mpmissions /srv/game-server/home/.local/share\n" +
		"if " + voice + "; then rm -rf -- /srv/game-server/teamspeak; fi\n" +
		"tar --no-same-owner --no-same-permissions --xattrs --acls -xzf \"$archive_file\" -C /srv/game-server\n" +
		"chown -R steam:steam /srv/game-server/config /srv/game-server/state /srv/game-server/logs /srv/game-server/arma3/mpmissions /srv/game-server/home\n" +
		"if " + voice + "; then chown -R teamspeak:teamspeak /srv/game-server/teamspeak; fi\n" +
		"systemctl start arma3-server.service\nif " + voice + "; then systemctl start teamspeak3-server.service; fi\n" +
		"ready=false\nfor _ in $(seq 1 60); do arma_ready=false; voice_ready=true; systemctl is-active --quiet arma3-server.service && ss -H -lun | awk '{print $4}' | grep -Eq '(^|:)2302$' && arma_ready=true; if " + voice + "; then voice_ready=false; systemctl is-active --quiet teamspeak3-server.service && ss -H -lun | awk '{print $4}' | grep -Eq '(^|:)9987$' && voice_ready=true; fi; if $arma_ready && $voice_ready; then ready=true; break; fi; sleep 10; done\n$ready\n"
}
