package main

import (
	"context"
	"fmt"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ec2compute"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsnotification"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ssmbootstrap"
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
	"strconv"
	"strings"
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
	timeout, err := sleepwakePositiveInt32("BOOTSTRAP_COMMAND_TIMEOUT_SECONDS", 21600)
	if err != nil {
		return nil, err
	}
	presetRunner, err := ssmbootstrap.New(ssm.NewFromConfig(awsCfg), ssmbootstrap.Config{
		Region: cfg.AWSRegion, AssetsBucket: cfg.SessionAssetsBucket,
		BootstrapScriptKey: strings.TrimSpace(os.Getenv("BOOTSTRAP_SCRIPT_KEY")), MetadataTableName: cfg.MetadataTable, SteamAuthSecretID: strings.TrimSpace(os.Getenv("STEAM_AUTH_SECRET_ID")),
		TeamSpeakVersion: sleepwakeEnv("TEAMSPEAK_VERSION", "3.13.8"), TimeoutSeconds: timeout,
	})
	if err != nil {
		return nil, err
	}
	service, err := sleepwake.NewService(repo, repo, repo, ec2compute.New(ec2.NewFromConfig(awsCfg), ssm.NewFromConfig(awsCfg)), monitor, sqsnotification.New(sqs.NewFromConfig(awsCfg), cfg.NotificationQueueURL), identity.Generator{}, appsession.SystemClock{}, sleepwake.WithPresetRevisionRunner(presetRunner))
	if err != nil {
		return nil, err
	}
	return &handler{service, logging.New(cfg.LogLevel)}, nil
}

func sleepwakePositiveInt32(name string, fallback int32) (int32, error) {
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

func sleepwakeEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
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
