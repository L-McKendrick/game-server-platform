package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsnotification"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ssmbootstrap"
	"github.com/L-McKendrick/game-server-platform/internal/app/bootstrap"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
)

type handler struct {
	service *bootstrap.Service
	logger  *slog.Logger
}

func main() {
	handler, err := build(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap worker startup error: %v\n", err)
		os.Exit(1)
	}
	lambda.Start(handler.Handle)
}

func build(ctx context.Context) (*handler, error) {
	baseConfig, err := config.Load()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(baseConfig.NotificationQueueURL) == "" {
		return nil, fmt.Errorf("NOTIFICATION_QUEUE_URL is required")
	}
	timeout, err := positiveInt32("BOOTSTRAP_COMMAND_TIMEOUT_SECONDS", 21600)
	if err != nil {
		return nil, err
	}
	runnerConfig := ssmbootstrap.Config{
		Region: baseConfig.AWSRegion, AssetsBucket: strings.TrimSpace(os.Getenv("SESSION_ASSETS_BUCKET")),
		BootstrapScriptKey: strings.TrimSpace(os.Getenv("BOOTSTRAP_SCRIPT_KEY")),
		MetadataTableName:  baseConfig.MetadataTable,
		SteamAuthSecretID:  strings.TrimSpace(os.Getenv("STEAM_AUTH_SECRET_ID")),
		TeamSpeakVersion:   env("TEAMSPEAK_VERSION", "3.13.8"), TimeoutSeconds: timeout,
	}
	logger := logging.New(baseConfig.LogLevel)
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(baseConfig.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	repository := dynamodbstore.New(dynamodb.NewFromConfig(awsConfig), baseConfig.MetadataTable)
	runner, err := ssmbootstrap.New(ssm.NewFromConfig(awsConfig), runnerConfig)
	if err != nil {
		return nil, err
	}
	service, err := bootstrap.NewService(
		repository, repository, repository, runner,
		sqsnotification.New(sqs.NewFromConfig(awsConfig), baseConfig.NotificationQueueURL),
		identity.Generator{}, appsession.SystemClock{},
	)
	if err != nil {
		return nil, err
	}
	return &handler{service: service, logger: logger}, nil
}

func (handler *handler) Handle(ctx context.Context, request bootstrap.TaskRequest) (bootstrap.TaskResult, error) {
	result, err := handler.service.Handle(ctx, request)
	if err != nil {
		handler.logger.Error("bootstrap stage failed", slog.String("action", request.Action), slog.String("session_id", request.SessionID), slog.String("workflow_id", request.WorkflowID), slog.Any("error", err))
		return bootstrap.TaskResult{}, err
	}
	if result.Warning != "" {
		handler.logger.Warn("bootstrap stage completed with warning", slog.String("action", request.Action), slog.String("session_id", request.SessionID), slog.String("warning", result.Warning))
	} else {
		handler.logger.Info("bootstrap stage completed", slog.String("action", request.Action), slog.String("session_id", request.SessionID), slog.String("state", result.State), slog.String("command_status", result.Status))
	}
	return result, nil
}

func positiveInt32(name string, fallback int32) (int32, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return int32(value), nil
}

func env(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
