package ssmmonitor

import (
	"context"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type monitorAPI struct {
	sendCalls int
}

func (api *monitorAPI) SendCommand(context.Context, *ssm.SendCommandInput, ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	api.sendCalls++
	return &ssm.SendCommandOutput{Command: &types.Command{CommandId: aws.String("command-1")}}, nil
}

func (api *monitorAPI) GetCommandInvocation(context.Context, *ssm.GetCommandInvocationInput, ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return &ssm.GetCommandInvocationOutput{}, nil
}

func TestStartAcceptsRunningHostLifecycleStates(t *testing.T) {
	t.Parallel()
	for _, state := range []domain.LifecycleState{domain.StateRunning, domain.StateWaking, domain.StateRestarting} {
		t.Run(string(state), func(t *testing.T) {
			api := &monitorAPI{}
			runner, err := New(api)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runner.Start(context.Background(), domain.Session{LifecycleState: state, Infrastructure: domain.Infrastructure{InstanceID: "i-1"}}); err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			if api.sendCalls != 1 {
				t.Fatalf("SendCommand calls = %d; want 1", api.sendCalls)
			}
		})
	}
}

func TestStartRejectsOfflineOrUnaddressableSession(t *testing.T) {
	t.Parallel()
	api := &monitorAPI{}
	runner, err := New(api)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range []domain.Session{
		{LifecycleState: domain.StateSleeping, Infrastructure: domain.Infrastructure{InstanceID: "i-1"}},
		{LifecycleState: domain.StateRestarting},
	} {
		if _, err := runner.Start(context.Background(), session); err == nil {
			t.Fatalf("Start() accepted %#v", session)
		}
	}
	if api.sendCalls != 0 {
		t.Fatalf("SendCommand calls = %d; want 0", api.sendCalls)
	}
}
