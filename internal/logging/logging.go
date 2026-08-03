package logging

import (
	"log/slog"
	"os"
)

// New creates the application's structured JSON logger.
func New(level slog.Level) *slog.Logger {
	options := &slog.HandlerOptions{
		AddSource: true,
		Level:     level,
	}

	handler := slog.NewJSONHandler(os.Stdout, options)

	return slog.New(handler)
}
