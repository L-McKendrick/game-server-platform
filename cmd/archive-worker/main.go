package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ec2destroy"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/s3archive"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsnotification"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ssmarchive"
	"github.com/L-McKendrick/game-server-platform/internal/app/archive"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

type handler struct {
	service *archive.Service
	logger  *slog.Logger
}

func main() {
	handler, err := build(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "archive worker startup error:", err)
		os.Exit(1)
	}
	lambda.Start(handler.Handle)
}

func build(ctx context.Context) (*handler, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if cfg.SessionAssetsBucket == "" {
		return nil, fmt.Errorf("SESSION_ASSETS_BUCKET is required")
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, err
	}
	repository := dynamodbstore.New(dynamodb.NewFromConfig(awsCfg), cfg.MetadataTable)
	runner, err := ssmarchive.New(ssm.NewFromConfig(awsCfg), cfg.SessionAssetsBucket, cfg.AWSRegion, 14400)
	if err != nil {
		return nil, err
	}
	store, err := s3archive.New(s3.NewFromConfig(awsCfg), cfg.SessionAssetsBucket)
	if err != nil {
		return nil, err
	}
	destroyer, err := ec2destroy.New(ec2.NewFromConfig(awsCfg), env("PROJECT_NAME", "game-server-platform"), cfg.Environment)
	if err != nil {
		return nil, err
	}
	service, err := archive.NewService(repository, repository, repository, runner, store, destroyer, sqsnotification.New(sqs.NewFromConfig(awsCfg), cfg.NotificationQueueURL), identity.Generator{}, appsession.SystemClock{})
	if err != nil {
		return nil, err
	}
	return &handler{service: service, logger: logging.New(cfg.LogLevel)}, nil
}

func env(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func (handler *handler) Handle(ctx context.Context, request archive.TaskRequest) (archive.TaskResult, error) {
	result, err := handler.service.Handle(ctx, request)
	if err != nil {
		handler.logger.Error("archive stage failed", slog.String("action", request.Action), slog.String("session_id", request.SessionID), slog.Any("error", err))
		return result, err
	}
	handler.logger.Info("archive stage complete", slog.String("action", request.Action), slog.String("state", result.State))
	return result, nil
}
