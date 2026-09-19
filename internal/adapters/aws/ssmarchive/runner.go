package ssmarchive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/hosttransfer"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type API interface {
	SendCommand(context.Context, *ssm.SendCommandInput, ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
}

type Runner struct {
	client  API
	bucket  string
	region  string
	timeout int32
	access  ArchiveAccess
}

type ArchiveAccess interface {
	PrepareArchiveScope(context.Context, string, time.Duration) (domain.HostAccessScope, error)
	PrepareArchiveUpload(context.Context, domain.HostAccessScope, string, int64) (ports.HostAccessReference, error)
	VerifyArchiveUpload(context.Context, domain.HostAccessScope) (domain.HostOutputVersion, error)
	ports.HostCommandRenewal
}

func (runner *Runner) WithArchiveAccess(access ArchiveAccess) *Runner {
	runner.access = access
	return runner
}

var _ ports.ArchiveRunner = (*Runner)(nil)

func New(client API, bucket string, region string, timeout int32) (*Runner, error) {
	bucket, region = strings.TrimSpace(bucket), strings.TrimSpace(region)
	if client == nil || bucket == "" || region == "" {
		return nil, fmt.Errorf("SSM client, archive bucket, and region are required")
	}
	if timeout < 300 || timeout > 86400 {
		return nil, fmt.Errorf("archive timeout must be between 300 and 86400 seconds")
	}
	return &Runner{client: client, bucket: bucket, region: region, timeout: timeout}, nil
}

func (runner *Runner) Start(ctx context.Context, session domain.Session, archiveID string) (string, error) {
	if session.LifecycleState != domain.StateArchiving || session.ActiveWorkflowType != domain.ArchiveWorkflowType || strings.TrimSpace(session.Infrastructure.InstanceID) == "" {
		return "", fmt.Errorf("%w: active archive workflow and instance are required", domain.ErrInvalidTransition)
	}
	archiveID = strings.TrimSpace(archiveID)
	if archiveID == "" || archiveID != session.ActiveWorkflowID {
		return "", fmt.Errorf("archive ID is required")
	}
	if runner.access == nil {
		return "", fmt.Errorf("scoped archive mediation is required")
	}
	client, ok := runner.client.(hosttransfer.CommandClient)
	if !ok {
		return "", fmt.Errorf("archive command ownership inspection required")
	}
	scope, err := runner.access.PrepareArchiveScope(ctx, session.ID, time.Duration(runner.timeout)*time.Second)
	if err != nil {
		return "", err
	}
	if scope.SessionID != session.ID || scope.OperationID != archiveID || scope.InstanceID != session.Infrastructure.InstanceID {
		return "", domain.ErrConflict
	}
	comment := "gsp:archive-prepare:" + scope.AttemptID
	if prior, err := hosttransfer.FindOwnedCommand(ctx, client, scope, comment); err != nil || prior != "" {
		return prior, err
	}
	objectKey := "sessions/" + session.ID + "/archives/" + archiveID + "/session.tar.gz"
	marker, _ := json.Marshal(scope)
	script := "#!/usr/bin/env bash\n# GSP_HOST_ACCESS:" + base64.StdEncoding.EncodeToString(marker) + "\narchive_dir='/var/tmp/gsp-archive/" + scope.AttemptID + "'\n" + fmt.Sprintf("archive_deadline=%d\n", scope.DeadlineAt.Unix()) + strings.TrimPrefix(runner.command(session, archiveID, objectKey), "#!/usr/bin/env bash\n")
	output, err := runner.client.SendCommand(ctx, &ssm.SendCommandInput{
		DocumentName: aws.String("AWS-RunShellScript"), InstanceIds: []string{session.Infrastructure.InstanceID},
		Comment:        aws.String(comment),
		Parameters:     map[string][]string{"commands": {script}, "executionTimeout": {fmt.Sprintf("%d", runner.timeout)}},
		TimeoutSeconds: aws.Int32(60),
	})
	if err != nil {
		return "", fmt.Errorf("send archive command: %w", err)
	}
	if output.Command == nil || strings.TrimSpace(aws.ToString(output.Command.CommandId)) == "" {
		return "", fmt.Errorf("Systems Manager returned no command ID")
	}
	return aws.ToString(output.Command.CommandId), nil
}

func (runner *Runner) Observe(ctx context.Context, instanceID string, commandID string) (ports.ArchiveCommandStatus, error) {
	instanceID, commandID = strings.TrimSpace(instanceID), strings.TrimSpace(commandID)
	if instanceID == "" || commandID == "" {
		return ports.ArchiveCommandStatus{}, fmt.Errorf("instance and command IDs are required")
	}
	output, err := runner.client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{CommandId: aws.String(commandID), InstanceId: aws.String(instanceID)})
	if err != nil {
		var pending *types.InvocationDoesNotExist
		if errors.As(err, &pending) {
			return ports.ArchiveCommandStatus{Status: "Pending"}, nil
		}
		return ports.ArchiveCommandStatus{}, fmt.Errorf("observe archive command: %w", err)
	}
	result := ports.ArchiveCommandStatus{Status: string(output.Status), ErrorMessage: bounded(aws.ToString(output.StandardErrorContent))}
	if result.Status != "Success" {
		return result, nil
	}
	var wire struct {
		ObjectKey string `json:"object_key"`
		SHA256    string `json:"sha256"`
		SizeBytes int64  `json:"size_bytes"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(aws.ToString(output.StandardOutputContent))), &wire); err != nil {
		return ports.ArchiveCommandStatus{}, fmt.Errorf("decode archive command output: %w", err)
	}
	digest, digestErr := base64.StdEncoding.DecodeString(wire.SHA256)
	if wire.ObjectKey == "" || digestErr != nil || len(digest) != 32 || base64.StdEncoding.EncodeToString(digest) != wire.SHA256 || wire.SizeBytes <= 0 || wire.SizeBytes > domain.MaxArchiveSizeBytes {
		return ports.ArchiveCommandStatus{}, fmt.Errorf("archive command returned incomplete metadata")
	}
	result.ObjectKey, result.SHA256, result.SizeBytes = wire.ObjectKey, wire.SHA256, wire.SizeBytes
	return runner.uploadPrepared(ctx, instanceID, commandID, result)
}

func (runner *Runner) command(session domain.Session, archiveID string, objectKey string) string {
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	voice := "false"
	if session.TeamSpeakEnabled {
		voice = "true"
	}
	return "#!/usr/bin/env bash\nset -Eeuo pipefail\numask 077\n" +
		"bucket=$(printf '%s' '" + encode(runner.bucket) + "' | base64 -d)\n" +
		"region=$(printf '%s' '" + encode(runner.region) + "' | base64 -d)\n" +
		"object_key=$(printf '%s' '" + encode(objectKey) + "' | base64 -d)\n" +
		"archive_id=$(printf '%s' '" + encode(archiveID) + "' | base64 -d)\n" +
		"exec 9>/run/gsp-archive.lock\nflock --wait 13000 9\n" +
		"test \"$(date +%s)\" -lt \"$archive_deadline\"\n" +
		"test ! -L /var/tmp/gsp-archive\nmkdir -p /var/tmp/gsp-archive\ntest \"$(stat -c '%u' /var/tmp/gsp-archive)\" = 0\nchmod 700 /var/tmp/gsp-archive\ntest ! -L \"$archive_dir\"\n" +
		"find /var/tmp/gsp-archive -mindepth 1 -maxdepth 1 -type d -user root ! -path \"$archive_dir\" -exec rm -rf -- {} +\n" +
		"mkdir -p \"$archive_dir\"\nchmod 700 \"$archive_dir\"\narchive_file=$(mktemp \"$archive_dir/.pending.XXXXXX\")\nkeep_archive=false\narma_active=false\nvoice_active=false\n" +
		"systemctl is-active --quiet arma3-server.service && arma_active=true\n" +
		"if " + voice + "; then systemctl is-active --quiet teamspeak3-server.service && voice_active=true; fi\n" +
		"$arma_active || { echo 'Arma service must be active before archiving' >&2; rm -f -- \"$archive_file\"; exit 1; }\n" +
		"if " + voice + "; then $voice_active || { echo 'TeamSpeak service must be active before archiving' >&2; rm -f -- \"$archive_file\"; exit 1; }; fi\n" +
		"services_stopped=false\nrestart_services() { restart_failed=false; systemctl start arma3-server.service || restart_failed=true; if $voice_active; then systemctl start teamspeak3-server.service || restart_failed=true; fi; ready=false; for _ in $(seq 1 60); do arma_ready=false; voice_ready=true; systemctl is-active --quiet arma3-server.service && ss -H -lun | awk '{print $4}' | grep -Eq '(^|:)2302$' && arma_ready=true; if $voice_active; then voice_ready=false; systemctl is-active --quiet teamspeak3-server.service && ss -H -lun | awk '{print $4}' | grep -Eq '(^|:)9987$' && voice_ready=true; fi; if $arma_ready && $voice_ready; then ready=true; break; fi; sleep 10; done; $ready || restart_failed=true; $restart_failed && return 1; services_stopped=false; return 0; }\n" +
		"cleanup() { code=$?; if $services_stopped; then restart_services || code=1; fi; if ! $keep_archive; then rm -f -- \"$archive_file\"; fi; exit $code; }\ntrap cleanup EXIT\n" +
		"services_stopped=true\nsystemctl stop arma3-server.service\nif $voice_active; then systemctl stop teamspeak3-server.service; fi\n" +
		"rm -rf -- /srv/game-server/home/.local/share/Steam/config /srv/game-server/home/.local/share/Steam/logs /srv/game-server/home/Steam/config /srv/game-server/home/Steam/logs /srv/game-server/steamcmd/config /srv/game-server/steamcmd/logs\n" +
		"find /srv/game-server/home /srv/game-server/steamcmd -type f \\( -name 'ssfn*' -o -name 'loginusers.vdf' \\) -delete 2>/dev/null || true\n" +
		"test -d /srv/game-server\narchive_paths=(config state logs arma3/mpmissions 'home/.local/share')\n" +
		"if " + voice + "; then archive_paths+=(teamspeak); fi\nfor path in \"${archive_paths[@]}\"; do test -e \"/srv/game-server/$path\"; done\n" +
		"tar --numeric-owner --xattrs --acls --exclude='home/.local/share/Steam/config' --exclude='home/.local/share/Steam/logs' --exclude='home/.local/share/Steam/ssfn*' --exclude='home/.local/share/Steam/loginusers.vdf' -czf \"$archive_file\" -C /srv/game-server \"${archive_paths[@]}\"\n" +
		"restart_services\n" +
		"sha256=$(openssl dgst -sha256 -binary \"$archive_file\" | openssl base64 -A)\nsize_bytes=$(stat -c '%s' \"$archive_file\")\ntest \"$size_bytes\" -le 4294967296\n" +
		"test \"$(date +%s)\" -lt \"$archive_deadline\"\n" +
		"mv -f -- \"$archive_file\" \"$archive_dir/session.tar.gz\"\nkeep_archive=true\n" +
		"printf '{\"object_key\":\"%s\",\"sha256\":\"%s\",\"size_bytes\":%s}\\n' \"$object_key\" \"$sha256\" \"$size_bytes\"\n"
}

func bounded(value string) string {
	value = domain.SanitizeDiagnosticTail(value)
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		return value[len(value)-500:]
	}
	return value
}
