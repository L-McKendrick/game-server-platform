package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Config contains values needed by the control-plane application.
type Config struct {
	Environment string
	AWSRegion   string
	LogLevel    slog.Level
}

// Load reads configuration from environment variables.
func Load() (Config, error) {
	logLevel, err := parseLogLevel(getEnv("LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Environment: getEnv("APP_ENV", "development"),
		AWSRegion:   getEnv("AWS_REGION", "us-west-2"),
		LogLevel:    logLevel,
	}

	if strings.TrimSpace(cfg.Environment) == "" {
		return Config{}, fmt.Errorf("APP_ENV cannot be empty")
	}

	if strings.TrimSpace(cfg.AWSRegion) == "" {
		return Config{}, fmt.Errorf("AWS_REGION cannot be empty")
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
