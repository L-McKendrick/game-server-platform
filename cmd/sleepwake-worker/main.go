package main

import (
	"context"
	"fmt"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ec2compute"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsnotification"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ssmmonitor"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/app/sleepwake"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"log/slog"
	"os"
)

type handler struct {
	service *sleepwake.Service
	logger  *slog.Logger
}

func main() {
	h, err := build(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "sleep/wake worker startup error:", err)
		os.Exit(1)
	}
	lambda.Start(h.Handle)
}
func build(ctx context.Context) (*handler, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, err
	}
	repo := dynamodbstore.New(dynamodb.NewFromConfig(awsCfg), cfg.MetadataTable)
	monitor, err := ssmmonitor.New(ssm.NewFromConfig(awsCfg))
	if err != nil {
		return nil, err
	}
	service, err := sleepwake.NewService(repo, repo, repo, ec2compute.New(ec2.NewFromConfig(awsCfg), ssm.NewFromConfig(awsCfg)), monitor, sqsnotification.New(sqs.NewFromConfig(awsCfg), cfg.NotificationQueueURL), identity.Generator{}, appsession.SystemClock{})
	if err != nil {
		return nil, err
	}
	return &handler{service, logging.New(cfg.LogLevel)}, nil
}
func (h *handler) Handle(ctx context.Context, r sleepwake.TaskRequest) (sleepwake.TaskResult, error) {
	out, err := h.service.Handle(ctx, r)
	if err != nil {
		h.logger.Error("sleep/wake stage failed", slog.String("action", r.Action), slog.Any("error", err))
		return out, err
	}
	h.logger.Info("sleep/wake stage complete", slog.String("action", r.Action), slog.String("state", out.State))
	return out, nil
}
