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

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/s3objects"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/sqsnotification"
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
	if strings.TrimSpace(cfg.SessionAssetsBucket) == "" || strings.TrimSpace(cfg.NotificationQueueURL) == "" {
		return nil, fmt.Errorf("SESSION_ASSETS_BUCKET and NOTIFICATION_QUEUE_URL are required")
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
	service, err := artifacts.NewService(
		repository, downloader, objects,
		sqsnotification.New(sqs.NewFromConfig(awsCfg), cfg.NotificationQueueURL),
		identity.Generator{}, clock, cfg.IdempotencyRetention,
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
