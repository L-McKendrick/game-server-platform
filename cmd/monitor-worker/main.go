package main

import (
	"context"
	"fmt"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsnotification"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ssmmonitor"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/steamquery"
	"github.com/L-McKendrick/game-server-platform/internal/app/monitoring"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"log/slog"
	"os"
	"strings"
	"time"
)

type handler struct {
	service *monitoring.Service
	logger  *slog.Logger
}

func main() {
	h, err := build(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "monitor worker startup error:", err)
		os.Exit(1)
	}
	lambda.Start(h.Handle)
}
func build(ctx context.Context) (*handler, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.NotificationQueueURL) == "" {
		return nil, fmt.Errorf("NOTIFICATION_QUEUE_URL is required")
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, err
	}
	repo := dynamodbstore.New(dynamodb.NewFromConfig(awsCfg), cfg.MetadataTable)
	runner, err := ssmmonitor.New(ssm.NewFromConfig(awsCfg))
	if err != nil {
		return nil, err
	}
	playerQuery, err := steamquery.New(2303, 1500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	service, err := monitoring.NewService(repo, runner, sqsnotification.New(sqs.NewFromConfig(awsCfg), cfg.NotificationQueueURL), identity.Generator{}, appsession.SystemClock{}, monitoring.WithPlayerQuery(playerQuery))
	if err != nil {
		return nil, err
	}
	return &handler{service, logging.New(cfg.LogLevel)}, nil
}
func (h *handler) Handle(ctx context.Context) (map[string]int, error) {
	count, err := h.service.Run(ctx)
	if err != nil {
		h.logger.Error("monitoring run failed", slog.Any("error", err))
		return nil, err
	}
	h.logger.Info("monitoring run completed", slog.Int("sessions", count))
	return map[string]int{"sessions": count}, nil
}
