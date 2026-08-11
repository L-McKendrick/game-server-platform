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
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ec2compute"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsnotification"
	"github.com/L-McKendrick/game-server-platform/internal/app/provisioning"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
)

type handler struct {
	service *provisioning.Service
	logger  *slog.Logger
}

func main() {
	handler, err := build(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "provisioning worker startup error: %v\n", err)
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
	provisioningConfig, err := loadProvisioningConfig()
	if err != nil {
		return nil, err
	}
	logger := logging.New(baseConfig.LogLevel)
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(baseConfig.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	repository := dynamodbstore.New(dynamodb.NewFromConfig(awsConfig), baseConfig.MetadataTable)
	service, err := provisioning.NewService(
		repository, repository, repository,
		ec2compute.New(ec2.NewFromConfig(awsConfig), ssm.NewFromConfig(awsConfig)),
		sqsnotification.New(sqs.NewFromConfig(awsConfig), baseConfig.NotificationQueueURL),
		identity.Generator{}, appsession.SystemClock{}, provisioningConfig,
	)
	if err != nil {
		return nil, err
	}
	return &handler{service: service, logger: logger}, nil
}

func (handler *handler) Handle(ctx context.Context, request provisioning.TaskRequest) (provisioning.TaskResult, error) {
	result, err := handler.service.Handle(ctx, request)
	if err != nil {
		handler.logger.Error("provisioning stage failed", slog.String("action", request.Action), slog.String("session_id", request.SessionID), slog.String("workflow_id", request.WorkflowID), slog.Any("error", err))
		return provisioning.TaskResult{}, err
	}
	if result.Warning != "" {
		handler.logger.Warn("provisioning stage completed with warning", slog.String("action", request.Action), slog.String("session_id", request.SessionID), slog.String("warning", result.Warning))
	} else {
		handler.logger.Info("provisioning stage completed", slog.String("action", request.Action), slog.String("session_id", request.SessionID), slog.String("state", result.State))
	}
	return result, nil
}

func loadProvisioningConfig() (provisioning.Config, error) {
	rootVolume, err := positiveInt32("PROVISIONING_ROOT_VOLUME_GIB", 30)
	if err != nil {
		return provisioning.Config{}, err
	}
	dataVolume, err := positiveInt32("PROVISIONING_DATA_VOLUME_GIB", 100)
	if err != nil {
		return provisioning.Config{}, err
	}
	maximum, err := positiveInt("MAX_PROVISIONED_SESSIONS", 1)
	if err != nil {
		return provisioning.Config{}, err
	}
	return provisioning.Config{
		Project: env("PROJECT_NAME", "game-server-platform"), Environment: env("APP_ENV", "dev"),
		AMIID: strings.TrimSpace(os.Getenv("PROVISIONING_AMI_ID")), InstanceType: env("PROVISIONING_INSTANCE_TYPE", "c7i.large"),
		SubnetID:             strings.TrimSpace(os.Getenv("PROVISIONING_SUBNET_ID")),
		GameSecurityGroupID:  strings.TrimSpace(os.Getenv("PROVISIONING_GAME_SECURITY_GROUP_ID")),
		VoiceSecurityGroupID: strings.TrimSpace(os.Getenv("PROVISIONING_VOICE_SECURITY_GROUP_ID")),
		InstanceProfile:      strings.TrimSpace(os.Getenv("PROVISIONING_INSTANCE_PROFILE")),
		RootVolumeGiB:        rootVolume, DataVolumeGiB: dataVolume, MaxProvisioned: maximum,
	}, nil
}

func positiveInt32(name string, fallback int32) (int32, error) {
	value, err := positiveInt(name, int(fallback))
	return int32(value), err
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

func env(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
