package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/s3objects"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqscommand"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsnotification"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ssmlivemission"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/httpartifact"
	"github.com/L-McKendrick/game-server-platform/internal/app/artifacts"
	"github.com/L-McKendrick/game-server-platform/internal/app/serverconfig"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
)

type handler struct {
	service      *artifacts.Service
	serverConfig *serverconfig.Processor
	logger       *slog.Logger
}

func main() {
	handler, err := build(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "artifact worker startup error: %v\n", err)
		os.Exit(1)
	}
	lambda.Start(handler.Handle)
}

func build(ctx context.Context) (*handler, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.SessionAssetsBucket) == "" || strings.TrimSpace(cfg.NotificationQueueURL) == "" || strings.TrimSpace(cfg.CommandQueueURL) == "" {
		return nil, fmt.Errorf("SESSION_ASSETS_BUCKET, NOTIFICATION_QUEUE_URL, and COMMAND_QUEUE_URL are required")
	}
	logger := logging.New(cfg.LogLevel)
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, err
	}
	repository := dynamodbstore.New(dynamodb.NewFromConfig(awsCfg), cfg.MetadataTable)
	downloader := httpartifact.New()
	objects := s3objects.New(s3.NewFromConfig(awsCfg), cfg.SessionAssetsBucket)
	clock := appsession.SystemClock{}
	queueClient := sqs.NewFromConfig(awsCfg)
	liveMissionCopier, err := ssmlivemission.New(ssm.NewFromConfig(awsCfg), ssmlivemission.Config{Region: cfg.AWSRegion, AssetsBucket: cfg.SessionAssetsBucket})
	if err != nil {
		return nil, err
	}
	startService, err := appsession.NewService(repository, identity.Generator{}, clock, cfg.IdempotencyRetention,
		appsession.WithCommandQueue(sqscommand.New(queueClient, cfg.CommandQueueURL)),
		appsession.WithServerConfigRepository(repository))
	if err != nil {
		return nil, err
	}
	service, err := artifacts.NewService(
		repository, downloader, objects,
		sqsnotification.New(queueClient, cfg.NotificationQueueURL),
		identity.Generator{}, clock, cfg.IdempotencyRetention,
		artifacts.WithAutoStarter(startService),
		artifacts.WithLiveMissionCopier(liveMissionCopier),
	)
	if err != nil {
		return nil, err
	}
	serverConfig, err := serverconfig.NewProcessor(repository, downloader, objects, clock)
	if err != nil {
		return nil, err
	}
	return &handler{service: service, serverConfig: serverConfig, logger: logger}, nil
}

func (handler *handler) Handle(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	response := events.SQSEventResponse{}
	for _, message := range event.Records {
		var request domain.ArtifactIngestRequest
		if err := json.Unmarshal([]byte(message.Body), &request); err != nil {
			handler.logger.Error("invalid artifact queue message", slog.String("message_id", message.MessageId), slog.Any("error", err))
			response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
			continue
		}
		var processErr error
		if request.IsServerConfig() {
			processErr = handler.serverConfig.Process(ctx, request)
		} else {
			processErr = handler.service.Process(ctx, request)
		}
		if processErr != nil {
			if request.IsServerConfig() && (errors.Is(processErr, domain.ErrPermanentArtifactRejection) || errors.Is(processErr, domain.ErrConflict)) {
				handler.logger.Warn("server configuration upload rejected without retry", slog.String("message_id", message.MessageId), slog.String("guild_id", request.GuildID), slog.String("correlation_id", request.CorrelationID))
				continue
			}
			handler.logger.Error(
				"artifact processing failed",
				slog.String("message_id", message.MessageId),
				slog.String("session_id", request.SessionID),
				slog.String("correlation_id", request.CorrelationID),
				slog.Any("error", processErr),
			)
			response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
			continue
		}
		handler.logger.Info("artifact processed", slog.String("session_id", request.SessionID), slog.String("correlation_id", request.CorrelationID))
	}
	return response, nil
}
