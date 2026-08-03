package config

import (
	"log/slog"
	"testing"
)

func TestLoadUsesDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("LOG_LEVEL", "")

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
	t.Setenv("LOG_LEVEL", "debug")

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

	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v; want %v", cfg.LogLevel, slog.LevelDebug)
	}
}

func TestLoadRejectsInvalidLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "banana")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() returned nil error; expected an error")
	}
}
