package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
)

const smokeTestOwner = "local-smoke-test"

func main() {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	defer cancel()

	appConfig, err := config.Load()
	if err != nil {
		fmt.Fprintf(
			os.Stderr,
			"configuration error: %v\n",
			err,
		)
		os.Exit(1)
	}

	logger := logging.New(appConfig.LogLevel)
	slog.SetDefault(logger)

	awsConfig, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(appConfig.AWSRegion),
	)
	if err != nil {
		fail("load AWS configuration", err)
	}

	client := dynamodb.NewFromConfig(awsConfig)

	repository := dynamodbstore.New(
		client,
		appConfig.MetadataTable,
	)

	service, err := sessions.NewService(
		repository,
		identity.Generator{},
		sessions.SystemClock{},
	)
	if err != nil {
		fail("construct session service", err)
	}

	actor := domain.Actor{
		Type: domain.ActorTypeLocalTest,
		ID:   smokeTestOwner,
	}

	nowSuffix := time.Now().
		UTC().
		Format("20060102-150405")

	created, err := service.Create(
		ctx,
		sessions.CreateCommand{
			Actor:       actor,
			Slug:        "metadata-smoke-" + nowSuffix,
			DisplayName: "Metadata Smoke Test",
			GameType:    "arma3",
			GuildID:     "local-test-guild",
			ChannelID:   "local-test-channel",
		},
	)
	if err != nil {
		fail("create session", err)
	}

	slog.Info(
		"session created through application service",
		slog.String("session_id", created.ID),
		slog.String(
			"state",
			string(created.LifecycleState),
		),
		slog.Int64("version", created.Version),
	)

	loaded, err := service.Get(
		ctx,
		sessions.GetQuery{
			Actor:     actor,
			SessionID: created.ID,
		},
	)
	if err != nil {
		fail("get session", err)
	}

	transitioned, err := service.Transition(
		ctx,
		sessions.TransitionCommand{
			Actor:     actor,
			SessionID: loaded.ID,
			To:        domain.StateNew,
		},
	)
	if err != nil {
		fail("transition session", err)
	}

	slog.Info(
		"session transitioned through application service",
		slog.String("session_id", transitioned.ID),
		slog.String(
			"state",
			string(transitioned.LifecycleState),
		),
		slog.Int64("version", transitioned.Version),
	)

	ownedSessions, err := service.List(
		ctx,
		sessions.ListQuery{
			Actor: actor,
			Limit: 10,
		},
	)
	if err != nil {
		fail("list owner sessions", err)
	}

	slog.Info(
		"application metadata smoke test passed",
		slog.String("table", appConfig.MetadataTable),
		slog.String("session_id", transitioned.ID),
		slog.Int(
			"owner_session_count",
			len(ownedSessions),
		),
	)
}

func fail(operation string, err error) {
	slog.Error(
		operation+" failed",
		slog.Any("error", err),
	)

	os.Exit(1)
}
