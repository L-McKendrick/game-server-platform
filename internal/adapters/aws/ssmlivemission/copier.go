package ssmlivemission

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const documentName = "AWS-RunShellScript"

var storedMissionName = regexp.MustCompile(`^([0-9a-f]{64})-(.+\.[pP][bB][oO])$`)

type API interface {
	SendCommand(context.Context, *ssm.SendCommandInput, ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
}

type Config struct {
	Region       string
	AssetsBucket string
	PollInterval time.Duration
	MaxPolls     int
}

type Copier struct {
	client API
	config Config
}

var _ ports.LiveMissionCopier = (*Copier)(nil)

func New(client API, config Config) (*Copier, error) {
	config.Region, config.AssetsBucket = strings.TrimSpace(config.Region), strings.TrimSpace(config.AssetsBucket)
	if client == nil || config.Region == "" || config.AssetsBucket == "" {
		return nil, fmt.Errorf("SSM client, AWS region, and assets bucket are required")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.MaxPolls <= 0 {
		config.MaxPolls = 30
	}
	return &Copier{client: client, config: config}, nil
}

func (copier *Copier) Copy(ctx context.Context, session domain.Session, mission domain.MissionRecord) error {
	eligible, ok := session.LiveMissionCopyTarget(mission.ObjectKey)
	if !ok || eligible.ObjectKey != mission.ObjectKey {
		return fmt.Errorf("%w: mission is not eligible for live copy", domain.ErrInvalidTransition)
	}
	match := storedMissionName.FindStringSubmatch(path.Base(mission.ObjectKey))
	expectedPrefix := path.Join("sessions", session.ID, "input", "missions") + "/"
	if !strings.HasPrefix(mission.ObjectKey, expectedPrefix) || len(match) != 3 || match[2] != mission.Filename {
		return fmt.Errorf("accepted mission object key is malformed")
	}
	script := command(copier.config.AssetsBucket, copier.config.Region, mission.ObjectKey, mission.Filename, match[1])
	output, err := copier.client.SendCommand(ctx, &ssm.SendCommandInput{
		DocumentName:   aws.String(documentName),
		InstanceIds:    []string{session.Infrastructure.InstanceID},
		Comment:        aws.String("game-server-platform live mission copy " + session.ID),
		Parameters:     map[string][]string{"commands": {script}, "executionTimeout": {"120"}},
		TimeoutSeconds: aws.Int32(30),
	})
	if err != nil {
		return fmt.Errorf("send live mission copy command: %w", err)
	}
	commandID := ""
	if output.Command != nil {
		commandID = strings.TrimSpace(aws.ToString(output.Command.CommandId))
	}
	if commandID == "" {
		return fmt.Errorf("Systems Manager returned no command ID")
	}
	for attempt := 0; attempt < copier.config.MaxPolls; attempt++ {
		invocation, observeErr := copier.client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{CommandId: aws.String(commandID), InstanceId: aws.String(session.Infrastructure.InstanceID)})
		if observeErr != nil {
			var pending *types.InvocationDoesNotExist
			if !errors.As(observeErr, &pending) {
				return fmt.Errorf("observe live mission copy command: %w", observeErr)
			}
		} else {
			switch invocation.Status {
			case types.CommandInvocationStatusSuccess:
				return nil
			case types.CommandInvocationStatusCancelled, types.CommandInvocationStatusCancelling, types.CommandInvocationStatusFailed, types.CommandInvocationStatusTimedOut:
				return fmt.Errorf("live mission copy command ended with status %s", invocation.Status)
			}
		}
		timer := time.NewTimer(copier.config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("live mission copy command did not complete within the bounded polling window")
}

func command(bucket, region, objectKey, filename, checksum string) string {
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	return "#!/usr/bin/env bash\nset -Eeuo pipefail\numask 077\n" +
		"bucket=\"$(printf '%s' '" + encode(bucket) + "' | base64 -d)\"\n" +
		"region=\"$(printf '%s' '" + encode(region) + "' | base64 -d)\"\n" +
		"object_key=\"$(printf '%s' '" + encode(objectKey) + "' | base64 -d)\"\n" +
		"filename=\"$(printf '%s' '" + encode(filename) + "' | base64 -d)\"\n" +
		"checksum=\"$(printf '%s' '" + encode(checksum) + "' | base64 -d)\"\n" +
		"target_dir=/srv/game-server/arma3/mpmissions\nmkdir -p \"$target_dir\"\n" +
		"exec 9>/run/lock/gsp-mission-copy.lock\nflock --wait 30 9\n" +
		"pending=\"$(mktemp \"$target_dir/.gsp-mission.XXXXXX\")\"\ntrap 'rm -f \"$pending\"' EXIT\n" +
		"aws s3 cp \"s3://$bucket/$object_key\" \"$pending\" --region \"$region\" --only-show-errors\n" +
		"printf '%s  %s\\n' \"$checksum\" \"$pending\" | sha256sum --check --status\n" +
		"chown steam:steam \"$pending\"\nchmod 0644 \"$pending\"\n" +
		"mv -f \"$pending\" \"$target_dir/$filename\"\ntrap - EXIT\n"
}
