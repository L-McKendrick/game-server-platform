package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadUsesDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("IDEMPOTENCY_RETENTION_HOURS", "")
	t.Setenv("RESET_ENABLED", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an unexpected error: %v", err)
	}

	if cfg.Environment != "development" {
		t.Errorf(
			"Environment = %q; want %q",
			cfg.Environment,
			"development",
		)
	}

	if cfg.AWSRegion != "us-west-2" {
		t.Errorf(
			"AWSRegion = %q; want %q",
			cfg.AWSRegion,
			"us-west-2",
		)
	}

	if cfg.MetadataTable != "game-server-platform-dev-metadata" {
		t.Errorf(
			"MetadataTable = %q; want %q",
			cfg.MetadataTable,
			"game-server-platform-dev-metadata",
		)
	}

	if cfg.IdempotencyRetention != 168*time.Hour {
		t.Errorf(
			"IdempotencyRetention = %v; want %v",
			cfg.IdempotencyRetention,
			168*time.Hour,
		)
	}

	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf(
			"LogLevel = %v; want %v",
			cfg.LogLevel,
			slog.LevelInfo,
		)
	}
}

func TestLoadReadsEnvironmentVariables(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("METADATA_TABLE_NAME", "test-metadata")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("IDEMPOTENCY_RETENTION_HOURS", "336")
	t.Setenv("RESET_ENABLED", "true")
	t.Setenv("RESET_QUEUE_URL", "https://sqs.example/reset.fifo")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned an unexpected error: %v", err)
	}

	if cfg.Environment != "test" {
		t.Errorf("Environment = %q; want %q", cfg.Environment, "test")
	}

	if cfg.AWSRegion != "us-east-1" {
		t.Errorf("AWSRegion = %q; want %q", cfg.AWSRegion, "us-east-1")
	}

	if cfg.MetadataTable != "test-metadata" {
		t.Errorf(
			"MetadataTable = %q; want %q",
			cfg.MetadataTable,
			"test-metadata",
		)
	}

	if cfg.IdempotencyRetention != 336*time.Hour {
		t.Errorf(
			"IdempotencyRetention = %v; want %v",
			cfg.IdempotencyRetention,
			336*time.Hour,
		)
	}

	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v; want %v", cfg.LogLevel, slog.LevelDebug)
	}
	if !cfg.ResetEnabled || cfg.ResetQueueURL != "https://sqs.example/reset.fifo" {
		t.Errorf("reset config = enabled:%v queue:%q", cfg.ResetEnabled, cfg.ResetQueueURL)
	}
}

func TestLoadRejectsInvalidResetGate(t *testing.T) {
	t.Setenv("RESET_ENABLED", "sometimes")
	if _, err := Load(); err == nil {
		t.Fatal("Load() returned nil error for invalid RESET_ENABLED")
	}
}

func TestLoadRejectsInvalidLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "banana")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() returned nil error; expected an error")
	}
}

func TestLoadRejectsInvalidIdempotencyRetention(t *testing.T) {
	t.Setenv("IDEMPOTENCY_RETENTION_HOURS", "zero")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() returned nil error; expected an error")
	}
}
