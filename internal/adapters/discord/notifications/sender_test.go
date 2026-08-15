package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type fakeSecrets struct {
	calls int
	fail  bool
}

func (fake *fakeSecrets) GetSecretValue(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	fake.calls++
	if fake.fail {
		fake.fail = false
		return nil, errors.New("temporary Secrets Manager failure")
	}
	return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(`{"token":"bot-token"}`)}, nil
}

func TestSenderRetriesSecretReadAndDisablesMentions(t *testing.T) {
	t.Parallel()

	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/channels/channel-1/messages" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bot bot-token" {
			t.Errorf("Authorization header was not set from the secret")
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	secrets := &fakeSecrets{fail: true}
	sender := New(secrets, "/game-server-platform/dev/discord-bot-token")
	sender.apiBase = server.URL
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "notification-1", SessionID: "session-1",
		GuildID: "guild-1", ChannelID: "channel-1", Content: "Session is ready.",
		CorrelationID: "correlation-1", RequestedAt: time.Date(2026, 8, 8, 23, 0, 0, 0, time.UTC),
	}

	if err := sender.Send(context.Background(), request); err == nil {
		t.Fatal("first Send() returned nil error; want transient secret error")
	}
	if err := sender.Send(context.Background(), request); err != nil {
		t.Fatalf("second Send() returned error: %v", err)
	}
	if secrets.calls != 2 {
		t.Fatalf("secret calls = %d; want 2", secrets.calls)
	}
	allowedMentions, ok := received["allowed_mentions"].(map[string]any)
	if !ok {
		t.Fatalf("allowed_mentions = %#v", received["allowed_mentions"])
	}
	parse, ok := allowedMentions["parse"].([]any)
	if !ok || len(parse) != 0 {
		t.Fatalf("allowed_mentions.parse = %#v; want empty array", allowedMentions["parse"])
	}
}

func TestSenderCreatesCardWithEnforcedNonceAndEditsKnownMessage(t *testing.T) {
	t.Parallel()
	var methods []string
	var payloads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		methods = append(methods, request.Method+" "+request.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
		}
		payloads = append(payloads, payload)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"message-1"}`))
	}))
	defer server.Close()
	sender := New(&fakeSecrets{}, "secret")
	sender.apiBase = server.URL
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "card-create-1", Kind: domain.NotificationSessionCard,
		SessionID: "session-1", GuildID: "guild-1", ChannelID: "channel-1", Content: "card",
		CorrelationID: "correlation-1", RequestedAt: time.Now().UTC(),
	}
	messageID, err := sender.SendCard(context.Background(), request, "")
	if err != nil || messageID != "message-1" {
		t.Fatalf("create = %q, %v", messageID, err)
	}
	if _, err := sender.SendCard(context.Background(), request, messageID); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || methods[0] != "POST /channels/channel-1/messages" || methods[1] != "PATCH /channels/channel-1/messages/message-1" {
		t.Fatalf("methods = %#v", methods)
	}
	if payloads[0]["enforce_nonce"] != true || payloads[0]["nonce"] == "" {
		t.Fatalf("create payload = %#v", payloads[0])
	}
	if _, exists := payloads[1]["nonce"]; exists {
		t.Fatalf("edit payload contains nonce: %#v", payloads[1])
	}
}
