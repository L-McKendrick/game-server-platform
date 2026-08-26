package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/lambdahttp"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsartifact"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqscommand"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsnotification"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsreset"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/discord/interactions"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/steamquery"
	appaccess "github.com/L-McKendrick/game-server-platform/internal/app/access"
	appreliability "github.com/L-McKendrick/game-server-platform/internal/app/reliability"
	appreset "github.com/L-McKendrick/game-server-platform/internal/app/reset"
	appserverconfig "github.com/L-McKendrick/game-server-platform/internal/app/serverconfig"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

func main() {
	adapter, err := build(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Discord Lambda startup error: %v\n", err)
		os.Exit(1)
	}
	lambda.Start(adapter.Handle)
}

func build(ctx context.Context) (*lambdahttp.Adapter, error) {
	baseConfig, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load base configuration: %w", err)
	}
	if strings.TrimSpace(baseConfig.ArtifactQueueURL) == "" || strings.TrimSpace(baseConfig.NotificationQueueURL) == "" {
		return nil, fmt.Errorf("ARTIFACT_QUEUE_URL and NOTIFICATION_QUEUE_URL are required")
	}
	if baseConfig.ProvisioningEnabled && strings.TrimSpace(baseConfig.CommandQueueURL) == "" {
		return nil, fmt.Errorf("COMMAND_QUEUE_URL is required when provisioning is enabled")
	}
	if baseConfig.ResetEnabled && strings.TrimSpace(baseConfig.ResetQueueURL) == "" {
		return nil, fmt.Errorf("RESET_QUEUE_URL is required when reset is enabled")
	}
	discordConfig, err := config.LoadDiscord()
	if err != nil {
		return nil, fmt.Errorf("load Discord configuration: %w", err)
	}
	logger := logging.New(baseConfig.LogLevel)
	slog.SetDefault(logger)
	publicKey, err := interactions.ParsePublicKey(discordConfig.PublicKeyHex)
	if err != nil {
		return nil, fmt.Errorf("parse Discord public key: %w", err)
	}
	awsConfiguration, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(baseConfig.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	repository := dynamodbstore.New(dynamodb.NewFromConfig(awsConfiguration), baseConfig.MetadataTable)
	artifactQueue := sqsartifact.New(sqs.NewFromConfig(awsConfiguration), baseConfig.ArtifactQueueURL)
	ids := identity.Generator{}
	clock := appsession.SystemClock{}
	serviceOptions := []appsession.Option{
		appsession.WithArtifactQueue(artifactQueue),
		appsession.WithWorkshopQueue(artifactQueue),
		appsession.WithNotificationQueue(sqsnotification.New(sqs.NewFromConfig(awsConfiguration), baseConfig.NotificationQueueURL)),
		appsession.WithServerConfigRepository(repository),
	}
	reliabilityService, err := appreliability.NewService(repository, repository, repository, ids, clock)
	if err != nil {
		return nil, fmt.Errorf("create reliability service: %w", err)
	}
	serviceOptions = append(serviceOptions, appsession.WithReliabilityService(reliabilityService))
	if baseConfig.ProvisioningEnabled {
		commandQueue := sqscommand.New(sqs.NewFromConfig(awsConfiguration), baseConfig.CommandQueueURL)
		serviceOptions = append(serviceOptions, appsession.WithCommandQueue(commandQueue), appsession.WithConfirmationRepository(repository))
	}
	service, err := appsession.NewService(
		repository,
		ids,
		clock,
		baseConfig.IdempotencyRetention,
		serviceOptions...,
	)
	if err != nil {
		return nil, fmt.Errorf("create session service: %w", err)
	}
	accessService, err := appaccess.NewService(
		repository,
		discordConfig.AllowedRoleIDs,
		discordConfig.AllowedChannelIDs,
		clock,
	)
	if err != nil {
		return nil, fmt.Errorf("create access service: %w", err)
	}
	var resetQueue ports.ResetQueue
	if baseConfig.ResetEnabled {
		resetQueue = sqsreset.New(sqs.NewFromConfig(awsConfiguration), baseConfig.ResetQueueURL)
	}
	resetService, err := appreset.NewService(repository, resetQueue, clock, baseConfig.Environment, baseConfig.ResetEnabled)
	if err != nil {
		return nil, fmt.Errorf("create reset service: %w", err)
	}
	serverConfigService, err := appserverconfig.NewService(repository, artifactQueue, clock)
	if err != nil {
		return nil, fmt.Errorf("create server configuration service: %w", err)
	}
	playerQuery, err := steamquery.New(2303, 1500*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("create Steam player query: %w", err)
	}
	handler, err := interactions.NewHandler(service, accessService, ids, clock, logger, interactions.Config{
		PublicKey:       publicKey,
		ApplicationID:   discordConfig.ApplicationID,
		AllowedGuildIDs: discordConfig.AllowedGuildIDs,
		MaxRequestBytes: discordConfig.MaxRequestBytes,
		SignatureMaxAge: discordConfig.SignatureMaxAge,
		PlayerQuery:     playerQuery,
		ResetService:    resetService,
		ServerConfig:    serverConfigService,
	})
	if err != nil {
		return nil, fmt.Errorf("create Discord interaction handler: %w", err)
	}
	return lambdahttp.New(handler)
}
