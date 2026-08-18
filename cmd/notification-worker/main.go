package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/s3objects"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/discord/notifications"
	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type handler struct {
	sender  notificationSender
	cards   ports.SessionCardRepository
	objects ports.ObjectReader
	logger  *slog.Logger
}

type notificationSender interface {
	Send(context.Context, domain.NotificationRequest) error
	SendCard(context.Context, domain.NotificationRequest, string) (string, error)
	SendModlist(context.Context, domain.NotificationRequest, []byte, string) (string, error)
}

func main() {
	handler, err := build(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "notification worker startup error: %v\n", err)
		os.Exit(1)
	}
	lambda.Start(handler.Handle)
}

func build(ctx context.Context) (*handler, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.DiscordSecretName) == "" || strings.TrimSpace(cfg.SessionAssetsBucket) == "" {
		return nil, fmt.Errorf("DISCORD_SECRET_NAME and SESSION_ASSETS_BUCKET are required")
	}
	logger := logging.New(cfg.LogLevel)
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, err
	}
	return &handler{
		sender:  notifications.New(secretsmanager.NewFromConfig(awsCfg), cfg.DiscordSecretName),
		cards:   dynamodbstore.New(dynamodb.NewFromConfig(awsCfg), cfg.MetadataTable),
		objects: s3objects.New(s3.NewFromConfig(awsCfg), cfg.SessionAssetsBucket), logger: logger,
	}, nil
}

func (handler *handler) Handle(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
	response := events.SQSEventResponse{}
	for _, message := range event.Records {
		var request domain.NotificationRequest
		if err := json.Unmarshal([]byte(message.Body), &request); err != nil {
			handler.logger.Error("invalid notification queue message", slog.String("message_id", message.MessageId), slog.Any("error", err))
			response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
			continue
		}
		var deliveryErr error
		if request.Kind == domain.NotificationSessionCard {
			deliveryErr = handler.deliverCard(ctx, request)
		} else if request.Kind == domain.NotificationSessionModlist {
			deliveryErr = handler.deliverModlist(ctx, request)
		} else {
			deliveryErr = handler.sender.Send(ctx, request)
		}
		if deliveryErr != nil {
			handler.logger.Error(
				"Discord notification failed",
				slog.String("message_id", message.MessageId),
				slog.String("notification_id", request.NotificationID),
				slog.String("correlation_id", request.CorrelationID),
				slog.Any("error", deliveryErr),
			)
			response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
			continue
		}
		handler.logger.Info("Discord notification delivered", slog.String("notification_id", request.NotificationID), slog.String("correlation_id", request.CorrelationID))
	}
	return response, nil
}

func (handler *handler) deliverCard(ctx context.Context, request domain.NotificationRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("validate session card notification: %w", err)
	}
	session, err := handler.cards.Get(ctx, request.SessionID)
	if err != nil {
		return fmt.Errorf("get session card reference: %w", err)
	}
	if session.GuildID != request.GuildID || session.ChannelID != request.ChannelID {
		return fmt.Errorf("session card destination does not match session metadata")
	}
	reference, err := handler.cards.GetCardReference(ctx, session.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("get persisted session card: %w", err)
	}
	if reference.SessionID != "" && reference.SessionID != request.SessionID {
		return fmt.Errorf("persisted session card belongs to another session")
	}
	if reference.ChannelID != "" && reference.ChannelID != request.ChannelID {
		return fmt.Errorf("persisted session card belongs to another channel")
	}
	if modlist, modlistErr := handler.cards.GetModlistReference(ctx, session.ID); modlistErr == nil {
		if sessioncard.IsActiveModlistReference(session, modlist) {
			messageURL := sessioncard.DiscordMessageURL(session.GuildID, modlist.ChannelID, modlist.MessageID)
			request.Content = sessioncard.WithModlistLink(request.Content, messageURL)
			request.Embed = sessioncard.WithModlistLinkEmbed(request.Embed, session.DisplayName, messageURL)
			if err := request.Validate(); err != nil {
				return fmt.Errorf("validate enriched session card notification: %w", err)
			}
		}
	} else if !errors.Is(modlistErr, domain.ErrNotFound) {
		return fmt.Errorf("get persisted session modlist: %w", modlistErr)
	}
	contentDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(request.Content)))
	if reference.DeliveredNotificationID == request.NotificationID && reference.ContentSHA256 != "" {
		if reference.ContentSHA256 != contentDigest {
			return fmt.Errorf("%w: session card notification content changed", domain.ErrIdempotencyConflict)
		}
		return nil
	}
	if reference.DeliveredRevision > 0 && (request.CardRevision == 0 || request.CardRevision < reference.DeliveredRevision) {
		return nil
	}
	messageID, err := handler.sender.SendCard(ctx, request, reference.MessageID)
	if err != nil {
		return err
	}
	if reference.MessageID == messageID && reference.ChannelID == request.ChannelID {
		if reference.DeliveredRevision == request.CardRevision &&
			reference.DeliveredNotificationID == request.NotificationID &&
			reference.ContentSHA256 == contentDigest {
			return nil
		}
	}
	return handler.cards.SaveCardReference(ctx, domain.SessionCardReference{
		SessionID: session.ID, ChannelID: request.ChannelID, MessageID: messageID,
		DeliveredRevision: request.CardRevision, DeliveredNotificationID: request.NotificationID,
		ContentSHA256: contentDigest,
	})
}

func (handler *handler) deliverModlist(ctx context.Context, request domain.NotificationRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("validate session modlist notification: %w", err)
	}
	if handler.objects == nil {
		return fmt.Errorf("session modlist object reader is not configured")
	}
	session, err := handler.cards.Get(ctx, request.SessionID)
	if err != nil {
		return fmt.Errorf("get session for modlist: %w", err)
	}
	if session.GuildID != request.GuildID || session.ChannelID != request.ChannelID {
		return fmt.Errorf("session modlist destination does not match session metadata")
	}
	reference, err := handler.cards.GetModlistReference(ctx, session.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("get persisted session modlist: %w", err)
	}
	if reference.SessionID != "" && reference.SessionID != request.SessionID {
		return fmt.Errorf("persisted session modlist belongs to another session")
	}
	if reference.ChannelID != "" && reference.ChannelID != request.ChannelID {
		return fmt.Errorf("persisted session modlist belongs to another channel")
	}
	attachment := request.Attachment
	if !sessioncard.IsActiveModlistAttachment(session, *attachment) {
		if attachment.Revision < session.Version {
			return nil
		}
		return fmt.Errorf("%w: queued modlist is not the active preset revision", domain.ErrIdempotencyConflict)
	}
	if reference.DeliveredRevision > attachment.Revision {
		return nil
	}
	if reference.DeliveredRevision == attachment.Revision && reference.ContentSHA256 != "" &&
		(reference.ContentSHA256 != attachment.SHA256 || reference.ObjectKey != attachment.ObjectKey || reference.Filename != attachment.Filename) {
		return fmt.Errorf("%w: session modlist revision content changed", domain.ErrIdempotencyConflict)
	}
	body, err := handler.objects.Get(ctx, attachment.ObjectKey)
	if err != nil {
		return fmt.Errorf("read sanitized session modlist: %w", err)
	}
	if int64(len(body)) != attachment.SizeBytes {
		return fmt.Errorf("sanitized session modlist size does not match notification metadata")
	}
	bodyDigest := fmt.Sprintf("%x", sha256.Sum256(body))
	if bodyDigest != attachment.SHA256 {
		return fmt.Errorf("sanitized session modlist checksum does not match notification metadata")
	}
	messageID, err := handler.sender.SendModlist(ctx, request, body, reference.MessageID)
	if err != nil {
		return err
	}
	updated := domain.SessionModlistReference{
		SessionID: session.ID, ChannelID: request.ChannelID, MessageID: messageID,
		ObjectKey: attachment.ObjectKey, Filename: attachment.Filename, DeliveredRevision: attachment.Revision,
		DeliveredNotificationID: request.NotificationID, ContentSHA256: attachment.SHA256,
	}
	if updated != reference {
		if err := handler.cards.SaveModlistReference(ctx, updated); err != nil {
			return err
		}
	}
	messageURL := sessioncard.DiscordMessageURL(session.GuildID, request.ChannelID, messageID)
	projection := sessioncard.Project(session, sessioncard.Options{Now: request.RequestedAt, ModlistURL: messageURL})
	card := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "card-modlist-" + attachment.SHA256[:12] + "-" + messageID,
		SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID,
		Content: sessioncard.RenderPublic(projection), Embed: sessioncard.RenderPublicEmbed(projection),
		Kind: domain.NotificationSessionCard, CardRevision: session.Version,
		CorrelationID: request.CorrelationID, RequestedAt: request.RequestedAt,
	}
	return handler.deliverCard(ctx, card)
}
