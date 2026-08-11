package sfnworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
)

type API interface {
	StartExecution(context.Context, *sfn.StartExecutionInput, ...func(*sfn.Options)) (*sfn.StartExecutionOutput, error)
}

type Starter struct {
	client        API
	stateMachines map[string]string
}

var _ ports.WorkflowStarter = (*Starter)(nil)

func New(client API, stateMachines map[string]string) *Starter {
	return &Starter{client: client, stateMachines: stateMachines}
}

func (starter *Starter) Start(ctx context.Context, workflow domain.Workflow) (string, error) {
	if err := workflow.Validate(); err != nil {
		return "", err
	}
	if starter == nil || starter.client == nil {
		return "", fmt.Errorf("Step Functions client is required")
	}
	stateMachineARN := strings.TrimSpace(starter.stateMachines[workflow.Type])
	if stateMachineARN == "" {
		return "", fmt.Errorf("no state machine configured for workflow type %s", workflow.Type)
	}
	input, err := json.Marshal(map[string]any{
		"session_id":               workflow.SessionID,
		"workflow_id":              workflow.ID,
		"requested_by":             workflow.RequestedBy,
		"correlation_id":           workflow.CorrelationID,
		"expected_session_version": workflow.ExpectedVersion,
	})
	if err != nil {
		return "", err
	}
	result, err := starter.client.StartExecution(ctx, &sfn.StartExecutionInput{
		StateMachineArn: aws.String(stateMachineARN), Name: aws.String(workflow.ID), Input: aws.String(string(input)),
	})
	if err != nil {
		return "", fmt.Errorf("start Step Functions execution: %w", err)
	}
	return aws.ToString(result.ExecutionArn), nil
}
