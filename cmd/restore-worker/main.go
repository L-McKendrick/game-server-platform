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
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ec2compute"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/s3archive"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsnotification"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ssmbootstrap"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ssmrestore"
	"github.com/L-McKendrick/game-server-platform/internal/app/restore"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
)

type handler struct {
	service *restore.Service
	logger  *slog.Logger
}

func main() {
	handler, err := build(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "restore worker startup error:", err)
		os.Exit(1)
	}
	lambda.Start(handler.Handle)
}

func build(ctx context.Context) (*handler, error) {
	base, err := config.Load()
	if err != nil {
		return nil, err
	}
	if base.NotificationQueueURL == "" || base.SessionAssetsBucket == "" {
		return nil, fmt.Errorf("notification queue and session assets bucket are required")
	}
	workerConfig, err := loadConfig()
	if err != nil {
		return nil, err
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(base.AWSRegion))
	if err != nil {
		return nil, err
	}
	repository := dynamodbstore.New(dynamodb.NewFromConfig(awsConfig), base.MetadataTable)
	ssmClient := ssm.NewFromConfig(awsConfig)
	bootstrap, err := ssmbootstrap.New(ssmClient, ssmbootstrap.Config{Region: base.AWSRegion, AssetsBucket: base.SessionAssetsBucket, BootstrapScriptKey: strings.TrimSpace(os.Getenv("BOOTSTRAP_SCRIPT_KEY")), SteamSecretID: strings.TrimSpace(os.Getenv("STEAM_SECRET_ID")), TeamSpeakVersion: env("TEAMSPEAK_VERSION", "3.13.7"), TimeoutSeconds: 43200})
	if err != nil {
		return nil, err
	}
	restoreRunner, err := ssmrestore.New(ssmClient, base.SessionAssetsBucket, base.AWSRegion, 14400)
	if err != nil {
		return nil, err
	}
	store, err := s3archive.New(s3.NewFromConfig(awsConfig), base.SessionAssetsBucket)
	if err != nil {
		return nil, err
	}
	service, err := restore.NewService(repository, repository, repository, ec2compute.New(ec2.NewFromConfig(awsConfig), ssmClient), bootstrap, restoreRunner, store, sqsnotification.New(sqs.NewFromConfig(awsConfig), base.NotificationQueueURL), identity.Generator{}, appsession.SystemClock{}, workerConfig)
	if err != nil {
		return nil, err
	}
	return &handler{service: service, logger: logging.New(base.LogLevel)}, nil
}

func (handler *handler) Handle(ctx context.Context, request restore.TaskRequest) (restore.TaskResult, error) {
	result, err := handler.service.Handle(ctx, request)
	if err != nil {
		handler.logger.Error("restore stage failed", slog.String("action", request.Action), slog.String("session_id", request.SessionID), slog.Any("error", err))
		return restore.TaskResult{}, err
	}
	handler.logger.Info("restore stage complete", slog.String("action", request.Action), slog.String("session_id", request.SessionID), slog.String("state", result.State))
	return result, nil
}

func loadConfig() (restore.Config, error) {
	root, err := positiveInt("PROVISIONING_ROOT_VOLUME_GIB", 30)
	if err != nil {
		return restore.Config{}, err
	}
	data, err := positiveInt("PROVISIONING_DATA_VOLUME_GIB", 100)
	if err != nil {
		return restore.Config{}, err
	}
	maximum, err := positiveInt("MAX_PROVISIONED_SESSIONS", 1)
	if err != nil {
		return restore.Config{}, err
	}
	return restore.Config{Project: env("PROJECT_NAME", "game-server-platform"), Environment: env("APP_ENV", "dev"), AMIID: strings.TrimSpace(os.Getenv("PROVISIONING_AMI_ID")), InstanceType: env("PROVISIONING_INSTANCE_TYPE", "c7i.large"), SubnetID: strings.TrimSpace(os.Getenv("PROVISIONING_SUBNET_ID")), GameSecurityGroupID: strings.TrimSpace(os.Getenv("PROVISIONING_GAME_SECURITY_GROUP_ID")), VoiceSecurityGroupID: strings.TrimSpace(os.Getenv("PROVISIONING_VOICE_SECURITY_GROUP_ID")), InstanceProfile: strings.TrimSpace(os.Getenv("PROVISIONING_INSTANCE_PROFILE")), RootVolumeGiB: int32(root), DataVolumeGiB: int32(data), MaxProvisioned: maximum}, nil
}

func positiveInt(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}
func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
