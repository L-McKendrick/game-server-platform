package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// DiscordConfig contains values required by the Discord interaction adapter.
type DiscordConfig struct {
	PublicKeyHex    string
	ApplicationID   string
	AllowedGuildIDs []string
	ListenAddress   string
	MaxRequestBytes int64
	SignatureMaxAge time.Duration
}

// LoadDiscord reads Discord-specific configuration from environment variables.
func LoadDiscord() (DiscordConfig, error) {
	maxRequestBytes, err := parsePositiveInt64(
		"DISCORD_MAX_REQUEST_BYTES",
		getEnv("DISCORD_MAX_REQUEST_BYTES", "65536"),
	)
	if err != nil {
		return DiscordConfig{}, err
	}

	signatureMaxAgeSeconds, err := parsePositiveInt64(
		"DISCORD_SIGNATURE_MAX_AGE_SECONDS",
		getEnv("DISCORD_SIGNATURE_MAX_AGE_SECONDS", "300"),
	)
	if err != nil {
		return DiscordConfig{}, err
	}

	config := DiscordConfig{
		PublicKeyHex:    strings.TrimSpace(os.Getenv("DISCORD_PUBLIC_KEY")),
		ApplicationID:   strings.TrimSpace(os.Getenv("DISCORD_APPLICATION_ID")),
		AllowedGuildIDs: splitNonEmptyCSV(os.Getenv("DISCORD_ALLOWED_GUILD_IDS")),
		ListenAddress:   getEnv("DISCORD_LISTEN_ADDRESS", "127.0.0.1:8080"),
		MaxRequestBytes: maxRequestBytes,
		SignatureMaxAge: time.Duration(signatureMaxAgeSeconds) * time.Second,
	}

	switch {
	case config.PublicKeyHex == "":
		return DiscordConfig{}, fmt.Errorf("DISCORD_PUBLIC_KEY is required")
	case config.ApplicationID == "":
		return DiscordConfig{}, fmt.Errorf("DISCORD_APPLICATION_ID is required")
	case len(config.AllowedGuildIDs) == 0:
		return DiscordConfig{}, fmt.Errorf("DISCORD_ALLOWED_GUILD_IDS requires at least one guild ID")
	case strings.TrimSpace(config.ListenAddress) == "":
		return DiscordConfig{}, fmt.Errorf("DISCORD_LISTEN_ADDRESS cannot be empty")
	}

	if _, _, err := net.SplitHostPort(config.ListenAddress); err != nil {
		return DiscordConfig{}, fmt.Errorf(
			"DISCORD_LISTEN_ADDRESS %q must use host:port format: %w",
			config.ListenAddress,
			err,
		)
	}

	return config, nil
}

func parsePositiveInt64(name string, value string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf(
			"%s %q must be a positive whole number",
			name,
			value,
		)
	}

	return parsed, nil
}

func splitNonEmptyCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if _, exists := seen[part]; exists {
			continue
		}

		seen[part] = struct{}{}
		result = append(result, part)
	}

	return result
}
