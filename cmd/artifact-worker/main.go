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
	"github.com/L-McKendrick/game-server-platform/internal/adapters/steamworkshop"
	"github.com/L-McKendrick/game-server-platform/internal/app/artifacts"
	"github.com/L-McKendrick/game-server-platform/internal/app/serverconfig"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	appworkshop "github.com/L-McKendrick/game-server-platform/internal/app/workshop"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type handler struct {
	service          *artifacts.Service
	serverConfig     *serverconfig.Processor
	workshop         *appworkshop.Service
	workshopRecorder *appworkshop.Recorder
	notifications    ports.NotificationQueue
	logger           *slog.Logger
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
	workshopService, err := appworkshop.New(steamworkshop.New(), clock)
	if err != nil {
		return nil, err
	}
	workshopRecorder, err := appworkshop.NewRecorder(repository, identity.Generator{}, clock, cfg.IdempotencyRetention)
	if err != nil {
		return nil, err
	}
	return &handler{service: service, serverConfig: serverConfig, workshop: workshopService, workshopRecorder: workshopRecorder, notifications: sqsnotification.New(queueClient, cfg.NotificationQueueURL), logger: logger}, nil
}

func (handler *handler) Handle(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	response := events.SQSEventResponse{}
	for _, message := range event.Records {
		var envelope struct {
			MessageType string `json:"message_type"`
		}
		if err := json.Unmarshal([]byte(message.Body), &envelope); err == nil && envelope.MessageType == "workshop_resolution" {
			var request domain.WorkshopSourceRequest
			if err := json.Unmarshal([]byte(message.Body), &request); err != nil {
				response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
				continue
			}
			resolution, resolveErr := handler.workshop.Resolve(ctx, request)
			if resolveErr != nil {
				var metadataErr domain.WorkshopMetadataError
				if errors.As(resolveErr, &metadataErr) && metadataErr.Retryable {
					response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
					continue
				}
				if err := handler.notifyWorkshop(ctx, request, "Workshop link rejected: "+resolveErr.Error()); err != nil {
					response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
				}
				continue
			}
			if request.Target == domain.WorkshopTargetMission {
				source, recordErr := handler.workshopRecorder.RecordMissionResolution(ctx, request, resolution)
				if recordErr != nil {
					if errors.Is(recordErr, domain.ErrPermanentWorkshopRejection) || errors.Is(recordErr, domain.ErrForbidden) || errors.Is(recordErr, domain.ErrIdempotencyConflict) {
						if err := handler.notifyWorkshop(ctx, request, "Workshop mission source rejected: "+recordErr.Error()); err != nil {
							response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
						}
					} else {
						response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
					}
					continue
				}
				content := fmt.Sprintf("Workshop mission source accepted: %d scenario(s), %d excluded. Download will be staged without changing the current mission.", len(source.AcceptedItemIDs), len(source.ExcludedItems))
				if err := handler.notifyWorkshop(ctx, request, content); err != nil {
					response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
				}
				continue
			}
			matched := 0
			for _, item := range resolution.Items {
				if item.MatchesTarget {
					matched++
				}
			}
			content := fmt.Sprintf("Workshop link validated: %d matching %s item(s), %d excluded. Download is not enabled yet.", matched, request.Target, len(resolution.Items)-matched)
			if err := handler.notifyWorkshop(ctx, request, content); err != nil {
				response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
			}
			continue
		}
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

func (handler *handler) notifyWorkshop(ctx context.Context, request domain.WorkshopSourceRequest, content string) error {
	return handler.notifications.Enqueue(ctx, domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "workshop-resolution-" + request.IdempotencyKey,
		SessionID: request.SessionID, GuildID: request.GuildID, ChannelID: request.ChannelID,
		Content: domain.SanitizeDiagnostic(content), CorrelationID: request.CorrelationID, RequestedAt: appsession.SystemClock{}.Now(),
	})
}
