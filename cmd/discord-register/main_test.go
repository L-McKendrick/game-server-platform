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

func TestRegisterCommandsBulkOverwritesGuildCommandsWithRBAndAdmin(t *testing.T) {
	t.Parallel()

	type commandOption struct {
		Name         string          `json:"name"`
		Options      []commandOption `json:"options"`
		Autocomplete bool            `json:"autocomplete"`
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
			filepath.Join(root, "deploy", "discord", "admin-command.json"),
		},
	)
	if err != nil {
		t.Fatalf("registerCommands() returned error: %v", err)
	}
	if len(received) != 2 || received[0].Name != "rb" || received[1].Name != "admin" {
		t.Fatalf("registered commands = %#v; want rb and admin only", received)
	}
	targeting := map[string]bool{
		"status": true, "setup": true,
		"start": true, "sleep": true, "wake": true, "archive": true, "restore": true, "terminate": true,
	}
	for _, subcommand := range received[0].Options {
		if subcommand.Name == "create" && len(subcommand.Options) != 0 {
			t.Errorf("create options = %#v; want optionless modal launcher", subcommand.Options)
		}
		if subcommand.Name == "list" {
			if len(subcommand.Options) != 2 || subcommand.Options[0].Name != "state" || subcommand.Options[1].Name != "page" {
				t.Errorf("list options = %#v; want state filter and page", subcommand.Options)
			}
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
}
