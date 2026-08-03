package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
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
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
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

	now := time.Now().UTC()

	sessionID := mustULID(now)
	createEventID := mustULID(now.Add(time.Millisecond))
	transitionEventID := mustULID(now.Add(2 * time.Millisecond))
	correlationID := mustULID(now.Add(3 * time.Millisecond))

	session, err := domain.NewSession(
		domain.NewSessionInput{
			ID:                 sessionID,
			Slug:               "metadata-smoke-" + strings.ToLower(sessionID[len(sessionID)-6:]),
			DisplayName:        "Metadata Smoke Test",
			GameType:           "arma3",
			OwnerDiscordUserID: smokeTestOwner,
			GuildID:            "local-test-guild",
			ChannelID:          "local-test-channel",
		},
		now,
	)
	if err != nil {
		fail("create session model", err)
	}

	createEvent := domain.NewSessionCreatedEvent(
		createEventID,
		correlationID,
		session,
		now,
	)

	if err := repository.Create(
		ctx,
		session,
		createEvent,
	); err != nil {
		fail("persist new session", err)
	}

	slog.Info(
		"session created",
		slog.String("session_id", session.ID),
		slog.String("state", string(session.LifecycleState)),
		slog.Int64("version", session.Version),
	)

	loaded, err := repository.Get(ctx, session.ID)
	if err != nil {
		fail("read session", err)
	}

	previousState := loaded.LifecycleState
	expectedVersion := loaded.Version

	if err := loaded.Transition(
		domain.StateNew,
		now.Add(time.Second),
	); err != nil {
		fail("transition session", err)
	}

	transitionEvent := domain.NewStateChangedEvent(
		transitionEventID,
		correlationID,
		loaded,
		previousState,
		now.Add(time.Second),
	)

	if err := repository.SaveWithEvent(
		ctx,
		loaded,
		expectedVersion,
		transitionEvent,
	); err != nil {
		fail("save transitioned session", err)
	}

	slog.Info(
		"session transitioned",
		slog.String("session_id", loaded.ID),
		slog.String("from", string(previousState)),
		slog.String("to", string(loaded.LifecycleState)),
		slog.Int64("version", loaded.Version),
	)

	sessions, err := repository.ListByOwner(
		ctx,
		smokeTestOwner,
		10,
	)
	if err != nil {
		fail("list sessions by owner", err)
	}

	slog.Info(
		"metadata smoke test passed",
		slog.String("table", appConfig.MetadataTable),
		slog.String("session_id", loaded.ID),
		slog.Int("owner_session_count", len(sessions)),
	)
}

func mustULID(now time.Time) string {
	id, err := identity.NewULID(now)
	if err != nil {
		fail("generate ULID", err)
	}

	return id
}

func fail(operation string, err error) {
	slog.Error(
		operation+" failed",
		slog.Any("error", err),
	)

	os.Exit(1)
}
