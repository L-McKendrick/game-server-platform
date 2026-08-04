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
		appConfig.IdempotencyRetention,
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

	createCommand := sessions.CreateCommand{
		Actor:          actor,
		CorrelationID:  "metadata-smoke:create:" + nowSuffix,
		IdempotencyKey: "local:metadata-smoke:create:" + nowSuffix,
		Slug:           "metadata-smoke-" + nowSuffix,
		DisplayName:    "Metadata Smoke Test",
		GameType:       "arma3",
		GuildID:        "local-test-guild",
		ChannelID:      "local-test-channel",
	}

	created, err := service.Create(ctx, createCommand)
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

	replayedCreate, err := service.Create(ctx, createCommand)
	if err != nil {
		fail("replay create session", err)
	}

	if replayedCreate.ID != created.ID {
		fail(
			"verify create idempotency",
			fmt.Errorf(
				"replayed session ID %s does not match %s",
				replayedCreate.ID,
				created.ID,
			),
		)
	}

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

	transitionCommand := sessions.TransitionCommand{
		Actor:          actor,
		SessionID:      loaded.ID,
		To:             domain.StateNew,
		CorrelationID:  "metadata-smoke:transition:" + nowSuffix,
		IdempotencyKey: "local:metadata-smoke:transition:" + nowSuffix,
	}

	transitioned, err := service.Transition(ctx, transitionCommand)
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

	replayedTransition, err := service.Transition(ctx, transitionCommand)
	if err != nil {
		fail("replay session transition", err)
	}

	if replayedTransition.Version != transitioned.Version {
		fail(
			"verify transition idempotency",
			fmt.Errorf(
				"replayed version %d does not match %d",
				replayedTransition.Version,
				transitioned.Version,
			),
		)
	}

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
