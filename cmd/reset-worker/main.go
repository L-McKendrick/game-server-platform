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
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/resetcleanup"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/discord/notifications"
	appreset "github.com/L-McKendrick/game-server-platform/internal/app/reset"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
)

type handler struct {
	worker *appreset.Worker
	logger *slog.Logger
}

func main() {
	handler, err := build(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "reset worker startup error:", err)
		os.Exit(1)
	}
	lambda.Start(handler.Handle)
}

func build(ctx context.Context) (*handler, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if cfg.SessionAssetsBucket == "" || cfg.DiscordSecretName == "" {
		return nil, fmt.Errorf("SESSION_ASSETS_BUCKET and DISCORD_SECRET_NAME are required")
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, err
	}
	logger := logging.New(cfg.LogLevel)
	repository := dynamodbstore.New(dynamodb.NewFromConfig(awsCfg), cfg.MetadataTable)
	cleaner, err := resetcleanup.New(
		ec2.NewFromConfig(awsCfg), s3.NewFromConfig(awsCfg), sqs.NewFromConfig(awsCfg), dynamodb.NewFromConfig(awsCfg),
		sfn.NewFromConfig(awsCfg), cloudwatchlogs.NewFromConfig(awsCfg),
		notifications.New(secretsmanager.NewFromConfig(awsCfg), cfg.DiscordSecretName),
		resetcleanup.Config{
			Project: env("PROJECT_NAME", "game-server-platform"), Environment: cfg.Environment,
			Bucket: cfg.SessionAssetsBucket, Table: cfg.MetadataTable,
			QueueURLs:        splitCSV(os.Getenv("RESET_RUNTIME_QUEUE_URLS")),
			StateMachineARNs: splitCSV(os.Getenv("RESET_STATE_MACHINE_ARNS")),
			LogGroups:        splitCSV(os.Getenv("RESET_LOG_GROUPS")),
			PollInterval:     5 * time.Second, PollTimeout: 5 * time.Minute,
		},
	)
	if err != nil {
		return nil, err
	}
	worker, err := appreset.NewWorker(repository, cleaner, appsession.SystemClock{})
	if err != nil {
		return nil, err
	}
	return &handler{worker: worker, logger: logger}, nil
}

func (handler *handler) Handle(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	response := events.SQSEventResponse{}
	for _, message := range event.Records {
		var request domain.ResetRequest
		if err := json.Unmarshal([]byte(message.Body), &request); err != nil {
			handler.logger.Error("invalid reset queue message", slog.String("message_id", message.MessageId))
			response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
			continue
		}
		operation, err := handler.worker.Handle(ctx, request)
		if err != nil {
			handler.logger.Error("reset worker could not persist a safe outcome", slog.String("message_id", message.MessageId), slog.String("operation_id", request.OperationID), slog.Any("error", err))
			response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
			continue
		}
		handler.logger.Info("reset request handled", slog.String("operation_id", operation.ID), slog.String("status", string(operation.Status)))
	}
	return response, nil
}

func splitCSV(value string) []string {
	values := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
