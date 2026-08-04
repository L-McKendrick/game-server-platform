package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	apigatewayv2adapter "github.com/L-McKendrick/game-server-platform/internal/adapters/aws/apigatewayv2"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/discord/interactions"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

func main() {
	handler, err := buildHandler(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Discord Lambda initialization error: %v\n", err)
		os.Exit(1)
	}

	lambda.Start(handler.Handle)
}

func buildHandler(ctx context.Context) (*apigatewayv2adapter.Handler, error) {
	baseConfig, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load base configuration: %w", err)
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

	awsConfig, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(baseConfig.AWSRegion),
	)
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}

	repository := dynamodbstore.New(
		dynamodb.NewFromConfig(awsConfig),
		baseConfig.MetadataTable,
	)
	generator := identity.Generator{}
	clock := appsession.SystemClock{}

	service, err := appsession.NewService(
		repository,
		generator,
		clock,
		baseConfig.IdempotencyRetention,
	)
	if err != nil {
		return nil, fmt.Errorf("create session service: %w", err)
	}

	discordHandler, err := interactions.NewHandler(
		service,
		generator,
		clock,
		logger,
		interactions.Config{
			PublicKey:       publicKey,
			ApplicationID:   discordConfig.ApplicationID,
			AllowedGuildIDs: discordConfig.AllowedGuildIDs,
			MaxRequestBytes: discordConfig.MaxRequestBytes,
			SignatureMaxAge: discordConfig.SignatureMaxAge,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create Discord interaction handler: %w", err)
	}

	gatewayHandler, err := apigatewayv2adapter.New(discordHandler)
	if err != nil {
		return nil, fmt.Errorf("create API Gateway adapter: %w", err)
	}

	logger.Info(
		"Discord interaction Lambda initialized",
		slog.String("environment", baseConfig.Environment),
		slog.String("metadata_table", baseConfig.MetadataTable),
	)

	return gatewayHandler, nil
}
