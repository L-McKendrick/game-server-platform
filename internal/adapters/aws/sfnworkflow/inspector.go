package sfnworkflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sfn/types"
)

type DescribeAPI interface {
	DescribeExecution(context.Context, *sfn.DescribeExecutionInput, ...func(*sfn.Options)) (*sfn.DescribeExecutionOutput, error)
}

type Inspector struct{ client DescribeAPI }

var _ ports.WorkflowExecutionInspector = (*Inspector)(nil)

func NewInspector(client DescribeAPI) *Inspector { return &Inspector{client: client} }

func (inspector *Inspector) Describe(ctx context.Context, executionARN string) (domain.WorkflowExecutionStatus, bool, error) {
	if inspector == nil || inspector.client == nil || strings.TrimSpace(executionARN) == "" {
		return "", false, fmt.Errorf("workflow execution inspector and ARN are required")
	}
	output, err := inspector.client.DescribeExecution(ctx, &sfn.DescribeExecutionInput{ExecutionArn: aws.String(strings.TrimSpace(executionARN))})
	if err != nil {
		var missing *types.ExecutionDoesNotExist
		if errors.As(err, &missing) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("describe workflow execution: %w", err)
	}
	return domain.WorkflowExecutionStatus(output.Status), true, nil
}
