package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestRegisterCommandsBulkOverwritesGuildCommandsWithRBAdminMenu(t *testing.T) {
	t.Parallel()

	type commandOption struct {
		Type         int             `json:"type"`
		Name         string          `json:"name"`
		Value        string          `json:"value"`
		Options      []commandOption `json:"options"`
		Choices      []commandOption `json:"choices"`
		Autocomplete bool            `json:"autocomplete"`
		Required     bool            `json:"required"`
	}
	var received []commandOption
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut {
			t.Errorf("method = %s; want PUT", request.Method)
		}
		if request.URL.Path != "/api/v10/applications/app-1/guilds/guild-1/commands" {
			t.Errorf("path = %q; want guild bulk-overwrite endpoint", request.URL.Path)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "Bot token-1" {
			t.Errorf("authorization = %q", authorization)
		}
		if contentType := request.Header.Get("Content-Type"); contentType != "application/json" {
			t.Errorf("content type = %q", contentType)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		} else if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[]`))
	}))
	defer server.Close()

	root := filepath.Join("..", "..")
	err := registerCommands(
		context.Background(),
		server.Client(),
		server.URL+"/api/v10",
		"app-1",
		"guild-1",
		"token-1",
		[]string{
			filepath.Join(root, "deploy", "discord", "rb-command.json"),
		},
	)
	if err != nil {
		t.Fatalf("registerCommands() returned error: %v", err)
	}
	if len(received) != 1 || received[0].Name != "rb" {
		t.Fatalf("registered commands = %#v; want rb only", received)
	}
	targeting := map[string]bool{
		"status": true, "setup": true, "mods": true,
		"start": true, "sleep": true, "wake": true, "archive": true, "restore": true, "terminate": true,
	}
	for _, subcommand := range received[0].Options {
		if subcommand.Name == "create" && (len(subcommand.Options) != 1 || subcommand.Options[0].Name != "game" ||
			subcommand.Options[0].Type != 3 || !subcommand.Options[0].Required || len(subcommand.Options[0].Choices) != 1 ||
			subcommand.Options[0].Choices[0].Name != "Arma 3" || subcommand.Options[0].Choices[0].Value != "arma-3") {
			t.Errorf("create options = %#v; want one required game choice", subcommand.Options)
		}
		if subcommand.Name == "list" {
			if len(subcommand.Options) != 2 || subcommand.Options[0].Name != "state" || subcommand.Options[1].Name != "page" {
				t.Errorf("list options = %#v; want state filter and page", subcommand.Options)
			}
		}
		if subcommand.Name == "help" {
			if len(subcommand.Options) != 1 || subcommand.Options[0].Name != "session" ||
				!subcommand.Options[0].Autocomplete || subcommand.Options[0].Required {
				t.Errorf("help options = %#v; want one optional autocomplete session", subcommand.Options)
			}
		}
		if subcommand.Name == "confirm" || subcommand.Name == "cancel-confirmation" {
			if len(subcommand.Options) != 0 {
				t.Errorf("%s options = %#v; want optionless server-resolved confirmation", subcommand.Name, subcommand.Options)
			}
		}
		if (subcommand.Name == "archive" || subcommand.Name == "terminate") && len(subcommand.Options) != 1 {
			t.Errorf("%s options = %#v; inline confirmation boolean must be absent", subcommand.Name, subcommand.Options)
		}
		if !targeting[subcommand.Name] {
			continue
		}
		if len(subcommand.Options) == 0 || subcommand.Options[0].Name != "session" || !subcommand.Options[0].Autocomplete {
			t.Errorf("%s session selector = %#v; want autocomplete session option", subcommand.Name, subcommand.Options)
		}
		delete(targeting, subcommand.Name)
	}
	if len(targeting) != 0 {
		t.Fatalf("session-targeting commands not verified: %#v", targeting)
	}
	var admin commandOption
	for _, option := range received[0].Options {
		if option.Name == "admin" {
			admin = option
			break
		}
	}
	if admin.Type != 1 || len(admin.Options) != 0 {
		t.Fatalf("rb admin definition = %#v", admin)
	}
}
