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
	"time"

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
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ssmbootstrap"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/ssmlivemission"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/httpartifact"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/steamworkshop"
	"github.com/L-McKendrick/game-server-platform/internal/app/artifacts"
	"github.com/L-McKendrick/game-server-platform/internal/app/serverconfig"
	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	appworkshop "github.com/L-McKendrick/game-server-platform/internal/app/workshop"
	"github.com/L-McKendrick/game-server-platform/internal/app/workshopcontent"
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
	contentSync      *workshopcontent.Service
	sessions         ports.SessionRepository
	notifications    ports.NotificationQueue
	logger           *slog.Logger
}

func main() {
	handler, err := build(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "artifact worker startup error: %v\n", err)
		os.Exit(1)
	}
	lambda.Start(handler.HandleEvent)
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
	ssmClient := ssm.NewFromConfig(awsCfg)
	liveMissionCopier, err := ssmlivemission.New(ssmClient, ssmlivemission.Config{Region: cfg.AWSRegion, AssetsBucket: cfg.SessionAssetsBucket})
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
	timeout, err := artifactPositiveInt32("BOOTSTRAP_COMMAND_TIMEOUT_SECONDS", 21600)
	if err != nil {
		return nil, err
	}
	contentRunner, err := ssmbootstrap.New(ssmClient, ssmbootstrap.Config{Region: cfg.AWSRegion, AssetsBucket: cfg.SessionAssetsBucket, BootstrapScriptKey: strings.TrimSpace(os.Getenv("BOOTSTRAP_SCRIPT_KEY")), MetadataTableName: cfg.MetadataTable, SteamAuthSecretID: strings.TrimSpace(os.Getenv("STEAM_AUTH_SECRET_ID")), TeamSpeakVersion: artifactEnv("TEAMSPEAK_VERSION", "3.13.8"), TimeoutSeconds: timeout})
	if err != nil {
		return nil, err
	}
	contentSync, err := workshopcontent.New(repository, repository, contentRunner, identity.Generator{}, clock, workshopcontent.WithWorkshopMissionManifest(objects))
	if err != nil {
		return nil, err
	}
	return &handler{service: service, serverConfig: serverConfig, workshop: workshopService, workshopRecorder: workshopRecorder, contentSync: contentSync, sessions: repository, notifications: sqsnotification.New(queueClient, cfg.NotificationQueueURL), logger: logger}, nil
}

func artifactPositiveInt32(name string, fallback int32) (int32, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return int32(value), nil
}
func artifactEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func workshopMissionRevision(ctx context.Context, sessionID string, sessions ports.SessionRepository) (string, error) {
	if sessions == nil {
		return "", fmt.Errorf("session repository is not configured")
	}
	session, err := sessions.Get(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return session.WorkshopMissionRevision()
}

func (handler *handler) HandleEvent(ctx context.Context, raw json.RawMessage) (any, error) {
	var event struct {
		Source string `json:"source"`
		Detail struct {
			CommandID string `json:"command-id"`
			Status    string `json:"status"`
		} `json:"detail"`
	}
	if json.Unmarshal(raw, &event) == nil && event.Source == "aws.ssm" {
		switch event.Detail.Status {
		case "Success", "Failed", "TimedOut", "Cancelled":
			done, err := handler.contentSync.HandleTerminal(ctx, event.Detail.CommandID)
			if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) {
				return false, nil
			}
			return done, err
		default:
			return false, nil
		}
	}
	var sqsEvent events.SQSEvent
	if err := json.Unmarshal(raw, &sqsEvent); err != nil {
		return nil, err
	}
	return handler.Handle(ctx, sqsEvent)
}

func (handler *handler) Handle(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	response := events.SQSEventResponse{}
	for _, message := range event.Records {
		var envelope struct {
			MessageType string `json:"message_type"`
		}
		if err := json.Unmarshal([]byte(message.Body), &envelope); err == nil && envelope.MessageType == "workshop_resolution" {
			request, err := decodeWorkshopRequest(message.Body)
			if err != nil {
				handler.logger.Error("invalid Workshop queue message", slog.String("message_id", message.MessageId), slog.Any("error", err))
				if workshopFinalAttempt(message) && request.SessionID != "" && request.Target != "" && request.IdempotencyKey != "" {
					if clearErr := handler.workshopRecorder.ClearResolution(ctx, request, "invalid_queue_message"); clearErr != nil {
						handler.logger.Error("clear invalid Workshop request marker", slog.String("message_id", message.MessageId), slog.String("session_id", request.SessionID), slog.Any("error", clearErr))
						response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
					}
				} else {
					response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
				}
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
				detail := workshopResolutionUserMessage(resolveErr, true)
				if err := handler.workshopRecorder.ClearResolution(ctx, request, detail); err != nil {
					response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
					continue
				}
				if err := handler.enqueueWorkshopStatusCard(ctx, request); err != nil {
					handler.logger.Warn("Workshop failure card refresh deferred", slog.String("session_id", request.SessionID), slog.String("correlation_id", request.CorrelationID), slog.Any("error", err))
				}
				continue
			}
			if request.Target == domain.WorkshopTargetMission {
				source, recordErr := handler.workshopRecorder.RecordMissionResolution(ctx, request, resolution)
				if recordErr != nil {
					handler.logWorkshopRecordFailure(request, recordErr)
					if permanentWorkshopRecordError(recordErr) || workshopFinalAttempt(message) {
						detail := workshopRecordUserMessage(recordErr, domain.WorkshopTargetMission, workshopFinalAttempt(message))
						if err := handler.workshopRecorder.ClearResolution(ctx, request, detail); err != nil {
							response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
							continue
						}
						if err := handler.enqueueWorkshopStatusCard(ctx, request); err != nil {
							handler.logger.Warn("Workshop failure card refresh deferred", slog.String("session_id", request.SessionID), slog.Any("error", err))
						}
					} else {
						response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
					}
					continue
				}
				content := fmt.Sprintf("Workshop mission source accepted: %d scenario(s), %d excluded. Download will be staged without changing the current mission.", len(source.AcceptedItemIDs), len(source.ExcludedItems))
				handler.logger.Info("Workshop mission resolution recorded", slog.String("session_id", request.SessionID), slog.String("source_kind", string(source.SourceKind)), slog.Int("accepted_count", len(source.AcceptedItemIDs)), slog.Int("excluded_count", len(source.ExcludedItems)), slog.String("status_summary", content), slog.String("correlation_id", request.CorrelationID))
				revision, revisionErr := workshopMissionRevision(ctx, request.SessionID, handler.sessions)
				if revisionErr == nil {
					_, revisionErr = handler.contentSync.Start(ctx, request.SessionID, request.Target, revision, request.ActorID, request.CorrelationID, request.IdempotencyKey)
				}
				if revisionErr != nil && !errors.Is(revisionErr, domain.ErrInvalidTransition) {
					handler.logger.Warn("Workshop mission content sync dispatch deferred", slog.String("session_id", request.SessionID), slog.Any("error", revisionErr))
				}
				continue
			}
			if request.Target == domain.WorkshopTargetMods {
				result, recordErr := handler.workshopRecorder.RecordModResolution(ctx, request, resolution)
				if recordErr != nil {
					handler.logWorkshopRecordFailure(request, recordErr)
					if permanentWorkshopRecordError(recordErr) || workshopFinalAttempt(message) {
						detail := workshopRecordUserMessage(recordErr, domain.WorkshopTargetMods, workshopFinalAttempt(message))
						if err := handler.workshopRecorder.ClearResolution(ctx, request, detail); err != nil {
							response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
							continue
						}
						if err := handler.enqueueWorkshopStatusCard(ctx, request); err != nil {
							handler.logger.Warn("Workshop failure card refresh deferred", slog.String("session_id", request.SessionID), slog.Any("error", err))
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
					handler.logger.Warn("Workshop success card refresh deferred", slog.String("session_id", request.SessionID), slog.Any("error", err))
				}
				if result.Revision.Status == domain.PresetRevisionActive {
					if err := handler.enqueueWorkshopModlist(ctx, request, result); err != nil {
						handler.logger.Warn("Workshop modlist refresh deferred", slog.String("session_id", request.SessionID), slog.Any("error", err))
					}
				}
				handler.logger.Info("Workshop mod resolution recorded", slog.String("session_id", request.SessionID), slog.String("source_kind", string(source.SourceKind)), slog.Int("accepted_count", len(source.AcceptedItems)), slog.Int("excluded_count", len(source.ExcludedItems)), slog.Int64("preset_revision", result.Revision.Number), slog.String("revision_status", string(result.Revision.Status)), slog.String("correlation_id", request.CorrelationID))
				if _, syncErr := handler.contentSync.Start(ctx, request.SessionID, request.Target, result.Revision.WorkshopResolutionSHA256, request.ActorID, request.CorrelationID, request.IdempotencyKey); syncErr != nil && !errors.Is(syncErr, domain.ErrInvalidTransition) {
					handler.logger.Warn("Workshop content sync dispatch deferred", slog.String("session_id", request.SessionID), slog.Any("error", syncErr))
				}
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

// decodeWorkshopRequest retains the strict domain validation boundary while
// accepting legacy Unix timestamps emitted by earlier queue producers.
func decodeWorkshopRequest(body string) (domain.WorkshopSourceRequest, error) {
	var request domain.WorkshopSourceRequest
	if err := json.Unmarshal([]byte(body), &request); err == nil {
		return request, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		return request, err
	}
	rawTime, ok := fields["requested_at"]
	if !ok {
		return request, fmt.Errorf("Workshop request time is missing")
	}
	delete(fields, "requested_at")
	remainder, err := json.Marshal(fields)
	if err != nil {
		return request, err
	}
	if err := json.Unmarshal(remainder, &request); err != nil {
		return request, err
	}
	var unixSeconds int64
	if err := json.Unmarshal(rawTime, &unixSeconds); err != nil {
		var encoded string
		if stringErr := json.Unmarshal(rawTime, &encoded); stringErr != nil {
			return request, fmt.Errorf("Workshop request time is invalid")
		}
		parsed, parseErr := strconv.ParseInt(encoded, 10, 64)
		if parseErr != nil {
			return request, fmt.Errorf("Workshop request time is invalid")
		}
		unixSeconds = parsed
	}
	if unixSeconds <= 0 {
		return request, fmt.Errorf("Workshop request time is invalid")
	}
	if unixSeconds > 10_000_000_000 {
		request.RequestedAt = time.UnixMilli(unixSeconds).UTC()
	} else {
		request.RequestedAt = time.Unix(unixSeconds, 0).UTC()
	}
	return request, nil
}

const maximumWorkshopReceiveCount = 5

func permanentWorkshopRecordError(err error) bool {
	return errors.Is(err, domain.ErrPermanentWorkshopRejection) || errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrIdempotencyConflict) || errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrWorkflowLocked) || errors.Is(err, domain.ErrInvalidTransition) || errors.Is(err, domain.ErrWorkshopSnapshotLimit) || errors.Is(err, domain.ErrPersistenceInvariant)
}

func (handler *handler) logWorkshopRecordFailure(request domain.WorkshopSourceRequest, err error) {
	disposition := "retryable_persistence"
	if permanentWorkshopRecordError(err) {
		disposition = "terminal"
	}
	handler.logger.Warn("Workshop resolution persistence failed", slog.String("session_id", request.SessionID), slog.String("target", string(request.Target)), slog.String("disposition", disposition), slog.String("error", domain.SanitizeDiagnostic(err.Error())), slog.String("correlation_id", request.CorrelationID))
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
				return "Steam Workshop metadata could not be read within the bounded request. Your session was left unchanged. Wait a few minutes, confirm the Workshop page is public, then submit the link again."
			}
		case domain.WorkshopMetadataRejected:
			return "Steam rejected the Workshop metadata request. Confirm this is a canonical public Steam Community shared-file link for an Arma 3 item or collection, then submit it again."
		case domain.WorkshopMetadataCollectionLimit:
			return fmt.Sprintf("This Workshop collection contains more than %d direct items. Split it into smaller collections of at most %d items, then submit the applicable links separately.", domain.MaximumWorkshopCollectionChildren, domain.MaximumWorkshopCollectionChildren)
		}
	}
	return "Workshop link could not be accepted. Use the canonical public Steam Community shared-file link and confirm the item or collection is for Arma 3."
}

func workshopRecordUserMessage(err error, target domain.WorkshopTarget, exhausted bool) string {
	if errors.Is(err, domain.ErrWorkshopNestedOnly) {
		return "This Workshop collection contains only other collections. Nested collections are not supported; submit a collection whose direct children are downloadable Arma 3 mods."
	}
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
		return "This session has reached its bounded Workshop source-history limit. Its active content was left unchanged. Use an uploaded preset for the next revision or create a new session; contact an administrator if history must be retained differently."
	}
	if errors.Is(err, domain.ErrPersistenceInvariant) {
		return "The Workshop content was validated, but the platform could not safely save it. Your active content was left unchanged. Contact an operator and provide the session name; submitting the link repeatedly will not help."
	}
	if exhausted && !errors.Is(err, domain.ErrPermanentWorkshopRejection) && !errors.Is(err, domain.ErrIdempotencyConflict) {
		return "The Workshop content was validated, but the platform could not safely save it after several attempts. Your active content was left unchanged. Submit the link again; if it repeats, contact an operator."
	}
	if target == domain.WorkshopTargetMods {
		return "No usable mod preset could be created from this Workshop source. Confirm it contains public Arma 3 mods (not scenarios)."
	}
	return "No usable multiplayer scenario could be created from this Workshop source. Confirm each desired item is public, has Data Type `Scenario`, and includes the `Multiplayer` gameplay tag."
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

func (handler *handler) enqueueWorkshopStatusCard(ctx context.Context, request domain.WorkshopSourceRequest) error {
	session, err := handler.sessions.Get(ctx, request.SessionID)
	if err != nil {
		return err
	}
	now := appsession.SystemClock{}.Now()
	projection := sessioncard.Project(session, sessioncard.Options{Now: now})
	return handler.notifications.Enqueue(ctx, domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "card-workshop-outcome-" + request.IdempotencyKey,
		SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID,
		Content: sessioncard.RenderPublic(projection), Embed: sessioncard.RenderPublicEmbed(projection), Kind: domain.NotificationSessionCard, CardRevision: session.Version,
		CorrelationID: request.CorrelationID, RequestedAt: now,
	})
}
