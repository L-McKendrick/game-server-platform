package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains values needed by the control-plane application.
type Config struct {
	Environment          string
	AWSRegion            string
	MetadataTable        string
	ArtifactQueueURL     string
	CommandQueueURL      string
	NotificationQueueURL string
	ResetQueueURL        string
	SessionAssetsBucket  string
	DiscordSecretName    string
	ProvisioningEnabled  bool
	ResetEnabled         bool
	IdempotencyRetention time.Duration
	LogLevel             slog.Level
}

// Load reads configuration from environment variables.
func Load() (Config, error) {
	logLevel, err := parseLogLevel(getEnv("LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}

	idempotencyRetention, err := parsePositiveHours(
		getEnv("IDEMPOTENCY_RETENTION_HOURS", "168"),
	)
	if err != nil {
		return Config{}, err
	}
	provisioningEnabled, err := strconv.ParseBool(getEnv("PROVISIONING_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("PROVISIONING_ENABLED must be true or false")
	}
	resetEnabled, err := strconv.ParseBool(getEnv("RESET_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("RESET_ENABLED must be true or false")
	}

	cfg := Config{
		Environment: getEnv("APP_ENV", "development"),
		AWSRegion:   getEnv("AWS_REGION", "us-west-2"),
		MetadataTable: getEnv(
			"METADATA_TABLE_NAME",
			"game-server-platform-dev-metadata",
		),
		ArtifactQueueURL:     strings.TrimSpace(os.Getenv("ARTIFACT_QUEUE_URL")),
		CommandQueueURL:      strings.TrimSpace(os.Getenv("COMMAND_QUEUE_URL")),
		NotificationQueueURL: strings.TrimSpace(os.Getenv("NOTIFICATION_QUEUE_URL")),
		ResetQueueURL:        strings.TrimSpace(os.Getenv("RESET_QUEUE_URL")),
		SessionAssetsBucket:  strings.TrimSpace(os.Getenv("SESSION_ASSETS_BUCKET")),
		DiscordSecretName:    strings.TrimSpace(os.Getenv("DISCORD_SECRET_NAME")),
		ProvisioningEnabled:  provisioningEnabled,
		ResetEnabled:         resetEnabled,
		IdempotencyRetention: idempotencyRetention,
		LogLevel:             logLevel,
	}

	if strings.TrimSpace(cfg.Environment) == "" {
		return Config{}, fmt.Errorf("APP_ENV cannot be empty")
	}

	if strings.TrimSpace(cfg.AWSRegion) == "" {
		return Config{}, fmt.Errorf("AWS_REGION cannot be empty")
	}

	if strings.TrimSpace(cfg.MetadataTable) == "" {
		return Config{}, fmt.Errorf("METADATA_TABLE_NAME cannot be empty")
	}

	return cfg, nil
}

func getEnv(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	return value
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf(
			"unsupported LOG_LEVEL %q; use debug, info, warn, or error",
			value,
		)
	}
}

func parsePositiveHours(value string) (time.Duration, error) {
	hours, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || hours <= 0 {
		return 0, fmt.Errorf(
			"IDEMPOTENCY_RETENTION_HOURS %q must be a positive whole number",
			value,
		)
	}

	return time.Duration(hours) * time.Hour, nil
}
