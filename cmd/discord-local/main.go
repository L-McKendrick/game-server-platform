package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/discord/interactions"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	appaccess "github.com/L-McKendrick/game-server-platform/internal/app/access"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/config"
	"github.com/L-McKendrick/game-server-platform/internal/identity"
	"github.com/L-McKendrick/game-server-platform/internal/logging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "discord local server error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	baseConfig, err := config.Load()
	if err != nil {
		return fmt.Errorf("load base configuration: %w", err)
	}

	discordConfig, err := config.LoadDiscord()
	if err != nil {
		return fmt.Errorf("load Discord configuration: %w", err)
	}

	logger := logging.New(baseConfig.LogLevel)
	slog.SetDefault(logger)

	publicKey, err := interactions.ParsePublicKey(discordConfig.PublicKeyHex)
	if err != nil {
		return err
	}

	repository := memory.NewSessionRepository()
	artifactQueue := memory.NewArtifactQueue()
	notificationQueue := memory.NewNotificationQueue()
	generator := identity.Generator{}
	clock := appsession.SystemClock{}

	service, err := appsession.NewService(
		repository,
		generator,
		clock,
		baseConfig.IdempotencyRetention,
		appsession.WithArtifactQueue(artifactQueue),
		appsession.WithWorkshopQueue(artifactQueue),
		appsession.WithNotificationQueue(notificationQueue),
	)
	if err != nil {
		return fmt.Errorf("create session service: %w", err)
	}
	accessService, err := appaccess.NewService(
		memory.NewAccessPolicyRepository(),
		discordConfig.AllowedRoleIDs,
		discordConfig.AllowedChannelIDs,
		clock,
	)
	if err != nil {
		return fmt.Errorf("create access service: %w", err)
	}

	handler, err := interactions.NewHandler(
		service,
		accessService,
		generator,
		clock,
		logger,
		interactions.Config{
			PublicKey:       publicKey,
			ApplicationID:   discordConfig.ApplicationID,
			AllowedGuildIDs: discordConfig.AllowedGuildIDs,
			MaxRequestBytes: discordConfig.MaxRequestBytes,
			SignatureMaxAge: discordConfig.SignatureMaxAge,
		},
	)
	if err != nil {
		return fmt.Errorf("create Discord interaction handler: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("POST /discord/interactions", handler)

	server := &http.Server{
		Addr:              discordConfig.ListenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownContext, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	serverError := make(chan error, 1)
	go func() {
		logger.Info(
			"local Discord interaction server started",
			slog.String("address", discordConfig.ListenAddress),
			slog.String("path", "/discord/interactions"),
			slog.String("storage", "memory"),
		)
		serverError <- server.ListenAndServe()
	}()

	select {
	case err := <-serverError:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-shutdownContext.Done():
		shutdownTimeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownTimeout); err != nil {
			return fmt.Errorf("shut down HTTP server: %w", err)
		}

		logger.Info("local Discord interaction server stopped")
		return nil
	}
}
