package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sfn"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sfnworkflow"
	appaccess "github.com/L-McKendrick/game-server-platform/internal/app/access"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/app/workflows"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
)

type handler struct {
	service *workflows.Service
	logger  *slog.Logger
}

func main() {
	handler, err := build(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "command worker startup error: %v\n", err)
		os.Exit(1)
	}
	lambda.Start(handler.Handle)
}

func build(ctx context.Context) (*handler, error) {
	baseConfig, err := config.Load()
	if err != nil {
		return nil, err
	}
	discordConfig, err := config.LoadDiscord()
	if err != nil {
		return nil, fmt.Errorf("load Discord configuration: %w", err)
	}
	provisionARN := strings.TrimSpace(os.Getenv("PROVISION_STATE_MACHINE_ARN"))
	if provisionARN == "" {
		return nil, fmt.Errorf("PROVISION_STATE_MACHINE_ARN is required")
	}
	bootstrapARN := strings.TrimSpace(os.Getenv("BOOTSTRAP_STATE_MACHINE_ARN"))
	if bootstrapARN == "" {
		return nil, fmt.Errorf("BOOTSTRAP_STATE_MACHINE_ARN is required")
	}
	sleepARN, wakeARN := strings.TrimSpace(os.Getenv("SLEEP_STATE_MACHINE_ARN")), strings.TrimSpace(os.Getenv("WAKE_STATE_MACHINE_ARN"))
	if sleepARN == "" || wakeARN == "" {
		return nil, fmt.Errorf("SLEEP_STATE_MACHINE_ARN and WAKE_STATE_MACHINE_ARN are required")
	}
	archiveARN := strings.TrimSpace(os.Getenv("ARCHIVE_STATE_MACHINE_ARN"))
	if archiveARN == "" {
		return nil, fmt.Errorf("ARCHIVE_STATE_MACHINE_ARN is required")
	}
	restoreARN := strings.TrimSpace(os.Getenv("RESTORE_STATE_MACHINE_ARN"))
	if restoreARN == "" {
		return nil, fmt.Errorf("RESTORE_STATE_MACHINE_ARN is required")
	}
	terminateARN := strings.TrimSpace(os.Getenv("TERMINATE_STATE_MACHINE_ARN"))
	if terminateARN == "" {
		return nil, fmt.Errorf("TERMINATE_STATE_MACHINE_ARN is required")
	}
	logger := logging.New(baseConfig.LogLevel)
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(baseConfig.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	repository := dynamodbstore.New(dynamodb.NewFromConfig(awsConfig), baseConfig.MetadataTable)
	clock := appsession.SystemClock{}
	authorizer, err := appaccess.NewService(repository, discordConfig.AllowedRoleIDs, discordConfig.AllowedChannelIDs, clock)
	if err != nil {
		return nil, err
	}
	service, err := workflows.NewService(
		repository, repository,
		sfnworkflow.New(sfn.NewFromConfig(awsConfig), map[string]string{
			"ProvisionSession": provisionARN, domain.BootstrapWorkflowType: bootstrapARN,
			domain.SleepWorkflowType: sleepARN, domain.WakeWorkflowType: wakeARN,
			domain.ArchiveWorkflowType:     archiveARN,
			domain.RestoreWorkflowType:     restoreARN,
			domain.TerminationWorkflowType: terminateARN,
		}),
		authorizer, identity.Generator{}, clock, 8*time.Hour,
	)
	if err != nil {
		return nil, err
	}
	return &handler{service: service, logger: logger}, nil
}

func (handler *handler) Handle(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	response := events.SQSEventResponse{}
	for _, message := range event.Records {
		var command domain.CommandEnvelope
		if err := json.Unmarshal([]byte(message.Body), &command); err != nil {
			handler.logger.Error("invalid command queue message", slog.String("message_id", message.MessageId), slog.Any("error", err))
			response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
			continue
		}
		workflow, err := handler.service.Start(ctx, command)
		if err != nil {
			handler.logger.Error("command processing failed", slog.String("message_id", message.MessageId), slog.String("session_id", command.SessionID), slog.String("correlation_id", command.CorrelationID), slog.Any("error", err))
			response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
			continue
		}
		handler.logger.Info("workflow started", slog.String("workflow_id", workflow.ID), slog.String("session_id", workflow.SessionID), slog.String("execution_arn", workflow.ExecutionARN))
	}
	return response, nil
}
