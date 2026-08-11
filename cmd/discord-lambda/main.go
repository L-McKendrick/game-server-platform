package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/lambdahttp"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsartifact"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/discord/interactions"
	appaccess "github.com/L-McKendrick/game-server-platform/internal/app/access"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
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
	if strings.TrimSpace(baseConfig.ArtifactQueueURL) == "" {
		return nil, fmt.Errorf("ARTIFACT_QUEUE_URL is required")
	}
	discordConfig, err := config.LoadDiscord()
	if err != nil {
		return nil, fmt.Errorf("load Discord configuration: %w", err)
	}
	logger := logging.New(baseConfig.LogLevel)
	slog.SetDefault(logger)

	publicKey, err := interactions.ParsePublicKey(discordConfig.PublicKeyHex)
	if err != nil {
		return nil, err
	}
	awsConfiguration, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(baseConfig.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	repository := dynamodbstore.New(dynamodb.NewFromConfig(awsConfiguration), baseConfig.MetadataTable)
	artifactQueue := sqsartifact.New(sqs.NewFromConfig(awsConfiguration), baseConfig.ArtifactQueueURL)
	ids := identity.Generator{}
	clock := appsession.SystemClock{}
	service, err := appsession.NewService(
		repository,
		ids,
		clock,
		baseConfig.IdempotencyRetention,
		appsession.WithArtifactQueue(artifactQueue),
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
	handler, err := interactions.NewHandler(service, accessService, ids, clock, logger, interactions.Config{
		PublicKey:       publicKey,
		ApplicationID:   discordConfig.ApplicationID,
		AllowedGuildIDs: discordConfig.AllowedGuildIDs,
		MaxRequestBytes: discordConfig.MaxRequestBytes,
		SignatureMaxAge: discordConfig.SignatureMaxAge,
	})
	if err != nil {
		return nil, fmt.Errorf("create Discord interaction handler: %w", err)
	}
	return lambdahttp.New(handler)
}
