package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/dynamodbstore"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/discord/notifications"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type handler struct {
	sender notificationSender
	cards  ports.SessionCardRepository
	logger *slog.Logger
}

type notificationSender interface {
	Send(context.Context, domain.NotificationRequest) error
	SendCard(context.Context, domain.NotificationRequest, string) (string, error)
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
	if strings.TrimSpace(cfg.DiscordSecretName) == "" {
		return nil, fmt.Errorf("DISCORD_SECRET_NAME is required")
	}
	logger := logging.New(cfg.LogLevel)
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, err
	}
	return &handler{
		sender: notifications.New(secretsmanager.NewFromConfig(awsCfg), cfg.DiscordSecretName),
		cards:  dynamodbstore.New(dynamodb.NewFromConfig(awsCfg), cfg.MetadataTable), logger: logger,
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
	if reference.ChannelID != "" && reference.ChannelID != request.ChannelID {
		return fmt.Errorf("persisted session card belongs to another channel")
	}
	messageID, err := handler.sender.SendCard(ctx, request, reference.MessageID)
	if err != nil {
		return err
	}
	if reference.MessageID == messageID && reference.ChannelID == request.ChannelID {
		return nil
	}
	return handler.cards.SaveCardReference(ctx, domain.SessionCardReference{
		SessionID: session.ID, ChannelID: request.ChannelID, MessageID: messageID,
	})
}
