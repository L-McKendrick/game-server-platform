package ssmmonitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"strings"
)

type API interface {
	SendCommand(context.Context, *ssm.SendCommandInput, ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
}
type Runner struct{ client API }

var _ ports.MonitoringRunner = (*Runner)(nil)

func New(client API) (*Runner, error) {
	if client == nil {
		return nil, fmt.Errorf("SSM client is required")
	}
	return &Runner{client}, nil
}
func (runner *Runner) Start(ctx context.Context, session domain.Session) (string, error) {
	if session.LifecycleState != domain.StateRunning || strings.TrimSpace(session.Infrastructure.InstanceID) == "" {
		return "", fmt.Errorf("running instance is required")
	}
	voice := "false"
	if session.TeamSpeakEnabled {
		voice = "true"
	}
	script := "set -eu\narma_service=false; systemctl is-active --quiet arma3-server.service && arma_service=true\narma_udp=false; ss -H -lun | awk '{print $4}' | grep -Eq '(^|:)2302$' && arma_udp=true\nts_service=false; ts_udp=false\nif " + voice + "; then systemctl is-active --quiet teamspeak.service && ts_service=true; ss -H -lun | awk '{print $4}' | grep -Eq '(^|:)9987$' && ts_udp=true; fi\ndisk=$(df -P /srv/game-server | awk 'NR==2 {gsub(\"%\",\"\",$5); print $5}')\nmem=$(awk '/MemAvailable:/ {print $2*1024}' /proc/meminfo)\nprintf '{\"arma_service\":%s,\"arma_udp\":%s,\"teamspeak_service\":%s,\"teamspeak_udp\":%s,\"disk_used_percent\":%s,\"memory_available_bytes\":%s,\"player_count\":0}\\n' \"$arma_service\" \"$arma_udp\" \"$ts_service\" \"$ts_udp\" \"${disk:-0}\" \"${mem:-0}\"\n"
	output, err := runner.client.SendCommand(ctx, &ssm.SendCommandInput{DocumentName: aws.String("AWS-RunShellScript"), InstanceIds: []string{session.Infrastructure.InstanceID}, Comment: aws.String("game-server-platform monitor " + session.ID), Parameters: map[string][]string{"commands": {script}, "executionTimeout": {"60"}}, TimeoutSeconds: aws.Int32(60)})
	if err != nil {
		return "", fmt.Errorf("send monitoring command: %w", err)
	}
	if output.Command == nil || strings.TrimSpace(aws.ToString(output.Command.CommandId)) == "" {
		return "", fmt.Errorf("Systems Manager returned no command ID")
	}
	return aws.ToString(output.Command.CommandId), nil
}
func (runner *Runner) Observe(ctx context.Context, instanceID, commandID string) (ports.MonitoringCommandStatus, error) {
	output, err := runner.client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{CommandId: aws.String(strings.TrimSpace(commandID)), InstanceId: aws.String(strings.TrimSpace(instanceID))})
	if err != nil {
		var pending *types.InvocationDoesNotExist
		if errors.As(err, &pending) {
			return ports.MonitoringCommandStatus{Status: "Pending"}, nil
		}
		return ports.MonitoringCommandStatus{}, err
	}
	status := string(output.Status)
	result := ports.MonitoringCommandStatus{Status: status, ErrorMessage: bounded(aws.ToString(output.StandardErrorContent))}
	if status != "Success" {
		return result, nil
	}
	var wire struct {
		ArmaService          bool  `json:"arma_service"`
		ArmaUDP              bool  `json:"arma_udp"`
		TeamSpeakService     bool  `json:"teamspeak_service"`
		TeamSpeakUDP         bool  `json:"teamspeak_udp"`
		DiskUsedPercent      int   `json:"disk_used_percent"`
		MemoryAvailableBytes int64 `json:"memory_available_bytes"`
		PlayerCount          int   `json:"player_count"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(aws.ToString(output.StandardOutputContent))), &wire); err != nil {
		return ports.MonitoringCommandStatus{}, fmt.Errorf("decode monitoring output: %w", err)
	}
	result.Observation = domain.HealthObservation{ArmaService: wire.ArmaService, ArmaUDP: wire.ArmaUDP, TeamSpeakService: wire.TeamSpeakService, TeamSpeakUDP: wire.TeamSpeakUDP, DiskUsedPercent: wire.DiskUsedPercent, MemoryAvailableBytes: wire.MemoryAvailableBytes, PlayerCount: wire.PlayerCount}
	return result, nil
}
func bounded(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		return value[len(value)-500:]
	}
	return value
}
