package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	logger := logging.New(cfg.LogLevel)
	slog.SetDefault(logger)

	logger.Info(
		"game server platform started",
		slog.String("component", "control-plane"),
		slog.String("environment", cfg.Environment),
		slog.String("aws_region", cfg.AWSRegion),
	)
}
