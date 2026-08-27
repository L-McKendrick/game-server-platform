package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
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
	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
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
	workshopRecorder, err := appworkshop.NewRecorder(repository, objects, identity.Generator{}, clock, cfg.IdempotencyRetention)
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
				code, retryable := "WORKSHOP_REJECTED", false
				if errors.As(resolveErr, &metadataErr) {
					code, retryable = string(metadataErr.Code), metadataErr.Retryable
				}
				handler.logger.Warn("Workshop resolution failed", slog.String("session_id", request.SessionID), slog.String("target", string(request.Target)), slog.String("error_code", code), slog.Bool("retryable", retryable), slog.String("correlation_id", request.CorrelationID))
				if errors.As(resolveErr, &metadataErr) && metadataErr.Retryable && !workshopFinalAttempt(message) {
					response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
					continue
				}
				if err := handler.notifyWorkshop(ctx, request, workshopResolutionUserMessage(resolveErr, workshopFinalAttempt(message))); err != nil {
					response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
				}
				continue
			}
			if request.Target == domain.WorkshopTargetMission {
				source, recordErr := handler.workshopRecorder.RecordMissionResolution(ctx, request, resolution)
				if recordErr != nil {
					if permanentWorkshopRecordError(recordErr) || workshopFinalAttempt(message) {
						if err := handler.notifyWorkshop(ctx, request, workshopRecordUserMessage(recordErr, domain.WorkshopTargetMission, workshopFinalAttempt(message))); err != nil {
							response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
						}
					} else {
						response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
					}
					continue
				}
				content := fmt.Sprintf("Workshop mission source accepted: %d scenario(s), %d excluded. Download will be staged without changing the current mission.", len(source.AcceptedItemIDs), len(source.ExcludedItems))
				handler.logger.Info("Workshop mission resolution recorded", slog.String("session_id", request.SessionID), slog.String("source_kind", string(source.SourceKind)), slog.Int("accepted_count", len(source.AcceptedItemIDs)), slog.Int("excluded_count", len(source.ExcludedItems)), slog.String("status_summary", content), slog.String("correlation_id", request.CorrelationID))
				continue
			}
			if request.Target == domain.WorkshopTargetMods {
				result, recordErr := handler.workshopRecorder.RecordModResolution(ctx, request, resolution)
				if recordErr != nil {
					if permanentWorkshopRecordError(recordErr) || workshopFinalAttempt(message) {
						if err := handler.notifyWorkshop(ctx, request, workshopRecordUserMessage(recordErr, domain.WorkshopTargetMods, workshopFinalAttempt(message))); err != nil {
							response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
						}
					} else {
						response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
					}
					continue
				}
				source := result.Source
				content := fmt.Sprintf("Workshop mod source accepted: %d client mod(s), %d excluded. Mod revision %d is %s.", len(source.AcceptedItems), len(source.ExcludedItems), result.Revision.Number, strings.ToLower(string(result.Revision.Status)))
				if summary := domain.WorkshopExclusionSummary(source.ExcludedItems, 8); summary != "" {
					content += " Excluded: " + summary + "."
				}
				if err := handler.enqueueWorkshopCard(ctx, request, result); err != nil {
					response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
					continue
				}
				if result.Revision.Status == domain.PresetRevisionActive {
					if err := handler.enqueueWorkshopModlist(ctx, request, result); err != nil {
						response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
						continue
					}
				}
				handler.logger.Info("Workshop mod resolution recorded", slog.String("session_id", request.SessionID), slog.String("source_kind", string(source.SourceKind)), slog.Int("accepted_count", len(source.AcceptedItems)), slog.Int("excluded_count", len(source.ExcludedItems)), slog.Int64("preset_revision", result.Revision.Number), slog.String("revision_status", string(result.Revision.Status)), slog.String("correlation_id", request.CorrelationID))
				continue
			}
			matched := 0
			for _, item := range resolution.Items {
				if item.MatchesTarget {
					matched++
				}
			}
			handler.logger.Info("Workshop link validated", slog.String("session_id", request.SessionID), slog.String("target", string(request.Target)), slog.Int("accepted_count", matched), slog.Int("excluded_count", len(resolution.Items)-matched), slog.String("correlation_id", request.CorrelationID))
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

const maximumWorkshopReceiveCount = 5

func permanentWorkshopRecordError(err error) bool {
	return errors.Is(err, domain.ErrPermanentWorkshopRejection) || errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrIdempotencyConflict) || errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrWorkflowLocked) || errors.Is(err, domain.ErrInvalidTransition) || errors.Is(err, domain.ErrWorkshopSnapshotLimit)
}

func workshopFinalAttempt(message events.SQSMessage) bool {
	count, err := strconv.Atoi(message.Attributes["ApproximateReceiveCount"])
	return err == nil && count >= maximumWorkshopReceiveCount
}

func workshopResolutionUserMessage(err error, exhausted bool) string {
	var metadataErr domain.WorkshopMetadataError
	if errors.As(err, &metadataErr) {
		switch metadataErr.Code {
		case domain.WorkshopMetadataUnavailable:
			return "Workshop link could not be used because the item or collection is unavailable or private. Make it Public in Steam Workshop, confirm the link opens while signed out, then submit it again."
		case domain.WorkshopMetadataRateLimited, domain.WorkshopMetadataTransient, domain.WorkshopMetadataInvalidResponse:
			if exhausted {
				return "Steam Workshop metadata could not be read after several automatic attempts. Your session was left unchanged. Wait a few minutes, confirm the Workshop page is public, then submit the link again."
			}
		case domain.WorkshopMetadataRejected:
			return "Steam rejected the Workshop metadata request. Confirm this is a canonical public Steam Community shared-file link for an Arma 3 item or collection, then submit it again."
		}
	}
	return "Workshop link could not be accepted. Use the canonical public Steam Community shared-file link and confirm the item or collection is for Arma 3."
}

func workshopRecordUserMessage(err error, target domain.WorkshopTarget, exhausted bool) string {
	if errors.Is(err, domain.ErrForbidden) {
		return "This Workshop request no longer matches the session owner or channel. Open the session in its configured server and channel, then submit the link again."
	}
	if errors.Is(err, domain.ErrIdempotencyConflict) || errors.Is(err, domain.ErrConflict) {
		return "The session's content changed while this Workshop link was processing. Review `/rb status`, then submit the link again to create a revision from the current state."
	}
	if errors.Is(err, domain.ErrWorkflowLocked) || errors.Is(err, domain.ErrInvalidTransition) {
		return "The session changed state while this Workshop link was processing. Wait for the current start, wake, restore, archive, or stop operation to finish, then submit the link again."
	}
	if errors.Is(err, domain.ErrWorkshopSnapshotLimit) {
		return "This session has reached its bounded Workshop source-history limit. Its active content was left unchanged. Use an uploaded preset for the next revision or create a new session; contact an operator if history must be retained differently."
	}
	if exhausted && !errors.Is(err, domain.ErrPermanentWorkshopRejection) && !errors.Is(err, domain.ErrIdempotencyConflict) {
		return "The Workshop content was validated, but the platform could not safely save it after several attempts. Your active content was left unchanged. Submit the link again; if it repeats, contact an operator."
	}
	if target == domain.WorkshopTargetMods {
		return "No usable client-mod preset could be created from this Workshop source. Confirm it contains public Arma 3 client mods; scenarios are not mods, and server-only items must use the server-mod workflow."
	}
	return "No usable multiplayer scenario could be created from this Workshop source. Confirm each desired item is public, has Data Type `Scenario`, and includes the `Multiplayer` or `Coop` gameplay tag."
}

func (handler *handler) enqueueWorkshopModlist(ctx context.Context, request domain.WorkshopSourceRequest, result appworkshop.ModResolutionResult) error {
	revision := result.Revision
	now := appsession.SystemClock{}.Now()
	return handler.notifications.Enqueue(ctx, domain.NotificationRequest{SchemaVersion: 1, NotificationID: fmt.Sprintf("modlist-workshop-%s-r%d", result.Session.ID, revision.Number), SessionID: result.Session.ID, GuildID: result.Session.GuildID, ChannelID: result.Session.ChannelID, Content: sessioncard.RenderModlistMessage(result.Session, revision.Modlist.Filename, revision.Modlist.WorkshopCount, now), Kind: domain.NotificationSessionModlist, Attachment: &domain.NotificationAttachment{ObjectKey: revision.Modlist.ObjectKey, Filename: revision.Modlist.Filename, ContentType: "text/html; charset=utf-8", SHA256: revision.Modlist.SHA256, SizeBytes: revision.Modlist.SizeBytes, Revision: result.Session.Version}, CorrelationID: request.CorrelationID, RequestedAt: now})
}

func (handler *handler) enqueueWorkshopCard(ctx context.Context, request domain.WorkshopSourceRequest, result appworkshop.ModResolutionResult) error {
	now := appsession.SystemClock{}.Now()
	projection := sessioncard.Project(result.Session, sessioncard.Options{Now: now})
	return handler.notifications.Enqueue(ctx, domain.NotificationRequest{SchemaVersion: 1, NotificationID: fmt.Sprintf("card-workshop-%s-r%d", result.Session.ID, result.Revision.Number), SessionID: result.Session.ID, GuildID: result.Session.GuildID, ChannelID: result.Session.ChannelID, Content: sessioncard.RenderPublic(projection), Embed: sessioncard.RenderPublicEmbed(projection), Kind: domain.NotificationSessionCard, CardRevision: result.Session.Version, CorrelationID: request.CorrelationID, RequestedAt: now})
}

func (handler *handler) notifyWorkshop(ctx context.Context, request domain.WorkshopSourceRequest, content string) error {
	return handler.notifications.Enqueue(ctx, domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "workshop-resolution-" + request.IdempotencyKey,
		SessionID: request.SessionID, GuildID: request.GuildID, ChannelID: request.ChannelID,
		Content: domain.SanitizeDiagnostic(content), CorrelationID: request.CorrelationID, RequestedAt: appsession.SystemClock{}.Now(),
	})
}
