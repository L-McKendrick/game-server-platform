package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/discord/notifications"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type handler struct {
	sender *notifications.Sender
	logger *slog.Logger
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
	return &handler{sender: notifications.New(secretsmanager.NewFromConfig(awsCfg), cfg.DiscordSecretName), logger: logger}, nil
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
		if err := handler.sender.Send(ctx, request); err != nil {
			handler.logger.Error(
				"Discord notification failed",
				slog.String("message_id", message.MessageId),
				slog.String("notification_id", request.NotificationID),
				slog.String("correlation_id", request.CorrelationID),
				slog.Any("error", err),
			)
			response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
			continue
		}
		handler.logger.Info("Discord notification delivered", slog.String("notification_id", request.NotificationID), slog.String("correlation_id", request.CorrelationID))
	}
	return response, nil
}
