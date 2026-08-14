package ssmbootstrap

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

const documentName = "AWS-RunShellScript"

type API interface {
	SendCommand(context.Context, *ssm.SendCommandInput, ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
}

type Config struct {
	Region             string
	AssetsBucket       string
	BootstrapScriptKey string
	SteamSecretID      string
	TeamSpeakVersion   string
	TimeoutSeconds     int32
}

type Runner struct {
	client API
	config Config
}

var _ ports.BootstrapRunner = (*Runner)(nil)

func New(client API, config Config) (*Runner, error) {
	config.Region = strings.TrimSpace(config.Region)
	config.AssetsBucket = strings.TrimSpace(config.AssetsBucket)
	config.BootstrapScriptKey = strings.TrimSpace(config.BootstrapScriptKey)
	config.SteamSecretID = strings.TrimSpace(config.SteamSecretID)
	config.TeamSpeakVersion = strings.TrimSpace(config.TeamSpeakVersion)
	if client == nil || config.Region == "" || config.AssetsBucket == "" || config.BootstrapScriptKey == "" || config.SteamSecretID == "" || config.TeamSpeakVersion == "" {
		return nil, fmt.Errorf("SSM client, region, asset bucket, bootstrap script key, Steam secret, and TeamSpeak version are required")
	}
	if config.TimeoutSeconds < 900 || config.TimeoutSeconds > 172800 {
		return nil, fmt.Errorf("bootstrap timeout must be between 900 and 172800 seconds")
	}
	return &Runner{client: client, config: config}, nil
}

func (runner *Runner) Start(ctx context.Context, session domain.Session) (string, error) {
	if !session.CanStartBootstrap() && session.LifecycleState != domain.StateInstalling && session.LifecycleState != domain.StateRestoring {
		return "", fmt.Errorf("%w: session is not bootstrap-ready", domain.ErrInvalidTransition)
	}
	script, err := runner.command(session)
	if err != nil {
		return "", err
	}
	output, err := runner.client.SendCommand(ctx, &ssm.SendCommandInput{
		DocumentName: aws.String(documentName),
		InstanceIds:  []string{session.Infrastructure.InstanceID},
		Comment:      aws.String("game-server-platform bootstrap " + session.ID),
		Parameters: map[string][]string{
			"commands":         {script},
			"executionTimeout": {fmt.Sprintf("%d", runner.config.TimeoutSeconds)},
		},
		TimeoutSeconds:     aws.Int32(60),
		OutputS3BucketName: aws.String(runner.config.AssetsBucket),
		OutputS3KeyPrefix:  aws.String("sessions/" + session.ID + "/logs/bootstrap"),
		OutputS3Region:     aws.String(runner.config.Region),
	})
	if err != nil {
		return "", fmt.Errorf("send Systems Manager bootstrap command: %w", err)
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
	output, err := runner.client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
		CommandId: aws.String(commandID), InstanceId: aws.String(instanceID),
	})
	if err != nil {
		var pending *types.InvocationDoesNotExist
		if errors.As(err, &pending) {
			return ports.BootstrapCommandStatus{Status: "Pending"}, nil
		}
		return ports.BootstrapCommandStatus{}, err
	}
	message := strings.TrimSpace(aws.ToString(output.StandardErrorContent))
	if len(message) > 500 {
		message = message[len(message)-500:]
	}
	return ports.BootstrapCommandStatus{Status: string(output.Status), ErrorMessage: message}, nil
}

func (runner *Runner) command(session domain.Session) (string, error) {
	if session.Infrastructure.InstanceID == "" || session.Infrastructure.DataVolumeID == "" || session.MissionObjectKey == "" || (!session.Vanilla && session.PresetObjectKey == "") {
		return "", fmt.Errorf("instance, data volume, mission, and a preset for modded sessions are required")
	}
	steamSecretID := runner.config.SteamSecretID
	if session.Vanilla {
		steamSecretID = ""
	}
	values := map[string]string{
		"SESSION_ID_B64":        session.ID,
		"DISPLAY_NAME_B64":      session.DisplayName,
		"DATA_VOLUME_ID_B64":    session.Infrastructure.DataVolumeID,
		"MISSION_KEY_B64":       session.MissionObjectKey,
		"PRESET_KEY_B64":        session.PresetObjectKey,
		"ASSETS_BUCKET_B64":     runner.config.AssetsBucket,
		"STEAM_SECRET_ID_B64":   steamSecretID,
		"AWS_REGION_B64":        runner.config.Region,
		"TEAMSPEAK_VERSION_B64": runner.config.TeamSpeakVersion,
	}
	var command strings.Builder
	command.WriteString("#!/usr/bin/env bash\nset -Eeuo pipefail\numask 077\n")
	for _, key := range []string{"SESSION_ID_B64", "DISPLAY_NAME_B64", "DATA_VOLUME_ID_B64", "MISSION_KEY_B64", "PRESET_KEY_B64", "ASSETS_BUCKET_B64", "STEAM_SECRET_ID_B64", "AWS_REGION_B64", "TEAMSPEAK_VERSION_B64"} {
		command.WriteString("export " + key + "='" + base64.StdEncoding.EncodeToString([]byte(values[key])) + "'\n")
	}
	if session.TeamSpeakEnabled {
		command.WriteString("export TEAMSPEAK_ENABLED=true\n")
	} else {
		command.WriteString("export TEAMSPEAK_ENABLED=false\n")
	}
	if session.Vanilla {
		command.WriteString("export VANILLA_MODE=true\n")
	} else {
		command.WriteString("export VANILLA_MODE=false\n")
	}
	command.WriteString("bootstrap_script=\"$(mktemp /run/gsp-bootstrap.XXXXXX)\"\n")
	command.WriteString("aws_cli_tmp=''\n")
	command.WriteString("trap 'rm -f \"$bootstrap_script\"; [ -z \"$aws_cli_tmp\" ] || rm -rf -- \"$aws_cli_tmp\"' EXIT\n")
	command.WriteString("if ! command -v aws >/dev/null 2>&1; then\n")
	command.WriteString("  command -v apt-get >/dev/null 2>&1 || { echo 'AWS CLI bootstrap requires apt-get' >&2; exit 1; }\n")
	command.WriteString("  apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl unzip\n")
	command.WriteString("  aws_cli_tmp=\"$(mktemp -d /run/gsp-awscli.XXXXXX)\"\n")
	command.WriteString("  curl --fail --location --silent --show-error 'https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip' -o \"$aws_cli_tmp/awscliv2.zip\"\n")
	command.WriteString("  unzip -q \"$aws_cli_tmp/awscliv2.zip\" -d \"$aws_cli_tmp\"\n")
	command.WriteString("  \"$aws_cli_tmp/aws/install\" --bin-dir /usr/local/bin --install-dir /usr/local/aws-cli\n")
	command.WriteString("fi\n")
	command.WriteString("download_bucket=\"$(printf '%s' '" + base64.StdEncoding.EncodeToString([]byte(runner.config.AssetsBucket)) + "' | base64 -d)\"\n")
	command.WriteString("download_key=\"$(printf '%s' '" + base64.StdEncoding.EncodeToString([]byte(runner.config.BootstrapScriptKey)) + "' | base64 -d)\"\n")
	command.WriteString("download_region=\"$(printf '%s' '" + base64.StdEncoding.EncodeToString([]byte(runner.config.Region)) + "' | base64 -d)\"\n")
	command.WriteString("aws s3 cp \"s3://$download_bucket/$download_key\" \"$bootstrap_script\" --region \"$download_region\" --only-show-errors\n")
	command.WriteString("chmod 700 \"$bootstrap_script\"\n")
	command.WriteString("\"$bootstrap_script\"\n")
	return command.String(), nil
}
