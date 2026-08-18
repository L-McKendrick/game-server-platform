package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var commandDefinitionPaths = []string{
	"deploy/discord/rb-command.json",
	"deploy/discord/admin-command.json",
}

const discordAPIBaseURL = "https://discord.com/api/v10"

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "Discord command registration failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	applicationID := strings.TrimSpace(os.Getenv("DISCORD_APPLICATION_ID"))
	guildID := strings.TrimSpace(os.Getenv("DISCORD_GUILD_ID"))
	botToken := strings.TrimSpace(os.Getenv("DISCORD_BOT_TOKEN"))
	switch {
	case applicationID == "":
		return fmt.Errorf("DISCORD_APPLICATION_ID is required")
	case guildID == "":
		return fmt.Errorf("DISCORD_GUILD_ID is required")
	case botToken == "":
		return fmt.Errorf("DISCORD_BOT_TOKEN is required")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	if err := registerCommands(
		ctx,
		client,
		discordAPIBaseURL,
		applicationID,
		guildID,
		botToken,
		commandDefinitionPaths,
	); err != nil {
		return err
	}

	fmt.Printf("Registered development /rb and /admin commands for guild %s.\n", guildID)
	return nil
}

func registerCommands(
	ctx context.Context,
	client *http.Client,
	apiBaseURL string,
	applicationID string,
	guildID string,
	botToken string,
	definitionPaths []string,
) error {
	commands := make([]json.RawMessage, 0, len(definitionPaths))
	for _, definitionPath := range definitionPaths {
		definition, err := os.ReadFile(definitionPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", definitionPath, err)
		}
		var command json.RawMessage
		if err := json.Unmarshal(definition, &command); err != nil {
			return fmt.Errorf("validate %s: %w", definitionPath, err)
		}
		commands = append(commands, command)
	}
	body, err := json.Marshal(commands)
	if err != nil {
		return fmt.Errorf("encode bulk command request: %w", err)
	}

	requestURL := fmt.Sprintf(
		"%s/applications/%s/guilds/%s/commands",
		strings.TrimRight(apiBaseURL, "/"),
		applicationID,
		guildID,
	)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, requestURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create Discord request: %w", err)
	}
	request.Header.Set("Authorization", "Bot "+botToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "game-server-platform/phase-12")

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("register development command: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return fmt.Errorf("read Discord response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Discord returned %s: %s", response.Status, strings.TrimSpace(string(responseBody)))
	}

	return nil
}
