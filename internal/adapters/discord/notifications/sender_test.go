package notifications

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/discord/componentid"
	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
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
		if request.Header.Get("Authorization") != "Bot bot-token" {
			t.Errorf("Authorization = %q; want bot token", request.Header.Get("Authorization"))
		}
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
		Embed: &domain.NotificationEmbed{Title: "ARMA 3 | Session", Description: "**ONLINE · HEALTHY**", Color: 0x23A55A,
			Fields: []domain.NotificationEmbedField{{Name: "CURRENT MISSION", Value: "Liberation RX on Altis"}}},
		CardRevision:  3,
		CorrelationID: "correlation-1", RequestedAt: time.Now().UTC(),
	}
	messageID, err := sender.SendCard(context.Background(), request, "")
	if err != nil || messageID != "message-1" {
		t.Fatalf("create = %q, %v", messageID, err)
	}
	retriedMessageID, err := sender.SendCard(context.Background(), request, "")
	if err != nil || retriedMessageID != messageID {
		t.Fatalf("retried create = %q, %v", retriedMessageID, err)
	}
	if _, err := sender.SendCard(context.Background(), request, messageID); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 3 || methods[0] != "POST /channels/channel-1/messages" ||
		methods[1] != "POST /channels/channel-1/messages" || methods[2] != "PATCH /channels/channel-1/messages/message-1" {
		t.Fatalf("methods = %#v", methods)
	}
	if payloads[0]["enforce_nonce"] != true || payloads[0]["nonce"] == "" || payloads[1]["nonce"] != payloads[0]["nonce"] {
		t.Fatalf("create payload = %#v", payloads[0])
	}
	if _, exists := payloads[2]["nonce"]; exists {
		t.Fatalf("edit payload contains nonce: %#v", payloads[2])
	}
	if payloads[0]["content"] != "" {
		t.Fatalf("embed card retained duplicate plain content: %#v", payloads[0])
	}
	embeds, ok := payloads[0]["embeds"].([]any)
	if !ok || len(embeds) != 1 || embeds[0].(map[string]any)["title"] != "ARMA 3 | Session" {
		t.Fatalf("embed payload = %#v", payloads[0]["embeds"])
	}
	rows, ok := payloads[0]["components"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("components = %#v; want one action row", payloads[0]["components"])
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("action row = %#v", rows[0])
	}
	buttons, ok := row["components"].([]any)
	if !ok || len(buttons) != 2 {
		t.Fatalf("buttons = %#v; want Show players and Refresh", row["components"])
	}
	wantActions := []string{componentid.ActionShowPlayers, componentid.ActionRefresh}
	primary := 0
	for index, raw := range buttons {
		button, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("button %d = %#v", index, raw)
		}
		reference, err := componentid.Parse(fmt.Sprint(button["custom_id"]))
		if err != nil || reference.Action != wantActions[index] || reference.Revision != 3 {
			t.Fatalf("button %d reference = %#v, %v", index, reference, err)
		}
		if reference.Token != sessioncard.ControlToken("session-1") || strings.Contains(reference.Token, "session-1") {
			t.Fatalf("button %d session token = %q", index, reference.Token)
		}
		if button["style"] == float64(1) {
			primary++
		}
	}
	if primary != 1 {
		t.Fatalf("primary button count = %d; want 1", primary)
	}
	if fmt.Sprint(payloads[2]["components"]) != fmt.Sprint(payloads[0]["components"]) {
		t.Fatalf("edit controls = %#v; want same revision-bound controls as create", payloads[2]["components"])
	}
}

func TestSenderClearsControlsWhenEditingTerminatedCard(t *testing.T) {
	t.Parallel()
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPatch {
			t.Errorf("method = %s; want PATCH", request.Method)
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"message-1"}`))
	}))
	defer server.Close()
	sender := New(&fakeSecrets{}, "secret")
	sender.apiBase = server.URL
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "card-terminated", Kind: domain.NotificationSessionCard,
		SessionID: "session-1", GuildID: "guild-1", ChannelID: "channel-1", Content: "terminated",
		CardRevision: 9, SuppressCardControls: true, CorrelationID: "correlation-1", RequestedAt: time.Now().UTC(),
	}
	if _, err := sender.SendCard(context.Background(), request, "message-1"); err != nil {
		t.Fatal(err)
	}
	components, exists := payload["components"]
	if !exists {
		t.Fatalf("terminated PATCH omitted components: %#v", payload)
	}
	if values, ok := components.([]any); !ok || len(values) != 0 {
		t.Fatalf("terminated components = %#v; want explicit empty array", components)
	}
}

func TestSenderRecreatesDeletedCardAndPreservesRevisionControls(t *testing.T) {
	t.Parallel()
	var methods []string
	var payloads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		methods = append(methods, request.Method+" "+request.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		payloads = append(payloads, payload)
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPatch {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"message":"Unknown Message"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"id":"replacement-card"}`))
	}))
	defer server.Close()

	sender := New(&fakeSecrets{}, "secret")
	sender.apiBase = server.URL
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "card-repair-1", Kind: domain.NotificationSessionCard,
		SessionID: "session-1", GuildID: "guild-1", ChannelID: "channel-1", Content: "current card",
		CardRevision: 7, CorrelationID: "correlation-repair", RequestedAt: time.Now().UTC(),
	}
	messageID, err := sender.SendCard(context.Background(), request, "deleted-card")
	if err != nil || messageID != "replacement-card" {
		t.Fatalf("SendCard() = %q, %v", messageID, err)
	}
	if len(methods) != 2 || methods[0] != "PATCH /channels/channel-1/messages/deleted-card" ||
		methods[1] != "POST /channels/channel-1/messages" {
		t.Fatalf("methods = %#v", methods)
	}
	if payloads[0]["nonce"] != nil || payloads[1]["enforce_nonce"] != true || payloads[1]["nonce"] == "" {
		t.Fatalf("repair payloads = %#v", payloads)
	}
	if fmt.Sprint(payloads[0]["components"]) != fmt.Sprint(payloads[1]["components"]) {
		t.Fatalf("replacement controls = %#v; want %#v", payloads[1]["components"], payloads[0]["components"])
	}
}

func TestSenderDoesNotCreateDuplicateCardForRateLimitOrPartialOutage(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls++
				if request.Method != http.MethodPatch {
					t.Errorf("method = %s; want PATCH only", request.Method)
				}
				writer.WriteHeader(status)
				_, _ = writer.Write([]byte(`{"message":"temporary failure"}`))
			}))
			defer server.Close()
			sender := New(&fakeSecrets{}, "secret")
			sender.apiBase = server.URL
			request := domain.NotificationRequest{
				SchemaVersion: 1, NotificationID: "card-outage", Kind: domain.NotificationSessionCard,
				SessionID: "session-1", GuildID: "guild-1", ChannelID: "channel-1", Content: "card",
				CardRevision: 2, CorrelationID: "correlation-outage", RequestedAt: time.Now().UTC(),
			}
			if _, err := sender.SendCard(context.Background(), request, "existing-card"); err == nil {
				t.Fatalf("SendCard() returned nil error for status %d", status)
			}
			if calls != 1 {
				t.Fatalf("calls = %d; want no fallback create for status %d", calls, status)
			}
		})
	}
}

func TestSenderFallsBackToPlainTextWhenDiscordRejectsEmbedPermission(t *testing.T) {
	t.Parallel()
	var payloads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		payloads = append(payloads, payload)
		if len(payloads) == 1 {
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(`{"code":50013,"message":"Missing Permissions"}`))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"plain-card"}`))
	}))
	defer server.Close()
	sender := New(&fakeSecrets{}, "secret")
	sender.apiBase = server.URL
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "card-fallback", Kind: domain.NotificationSessionCard,
		SessionID: "session-1", GuildID: "guild-1", ChannelID: "channel-1", Content: "plain fallback",
		Embed:        &domain.NotificationEmbed{Title: "ARMA 3 | Session", Description: "ONLINE", Color: 0x23A55A},
		CardRevision: 1, CorrelationID: "correlation-fallback", RequestedAt: time.Now().UTC(),
	}
	messageID, err := sender.SendCard(context.Background(), request, "")
	if err != nil || messageID != "plain-card" {
		t.Fatalf("fallback send = %q, %v", messageID, err)
	}
	if len(payloads) != 2 || payloads[0]["content"] != "" || payloads[1]["content"] != "plain fallback" {
		t.Fatalf("fallback payloads = %#v", payloads)
	}
	if embeds, ok := payloads[1]["embeds"].([]any); !ok || len(embeds) != 0 {
		t.Fatalf("fallback embeds = %#v", payloads[1]["embeds"])
	}
}

func TestNotificationNonceIsStablePerDeliveryAndChangesForRepair(t *testing.T) {
	t.Parallel()
	request := domain.NotificationRequest{SessionID: "session-1", NotificationID: "card-created"}
	created := notificationNonce("card", request)
	if created == "" || created != notificationNonce("card", request) {
		t.Fatalf("created nonce = %q; want stable non-empty value", created)
	}
	request.NotificationID = "card-admin-repair-interaction-1"
	if repaired := notificationNonce("card", request); repaired == created {
		t.Fatalf("repair nonce = %q; want value distinct from initial delivery", repaired)
	}
}

func TestSenderRecreatesDeletedModlistWithSanitizedMultipartAttachment(t *testing.T) {
	t.Parallel()
	body := []byte("<!DOCTYPE html><html><body>sanitized</body></html>")
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	var methods []string
	var payloads []map[string]any
	var uploadedBodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		methods = append(methods, request.Method+" "+request.URL.Path)
		reader, err := request.MultipartReader()
		if err != nil {
			t.Errorf("MultipartReader: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		var payload map[string]any
		for {
			part, nextErr := reader.NextPart()
			if errors.Is(nextErr, io.EOF) {
				break
			}
			if nextErr != nil {
				t.Errorf("NextPart: %v", nextErr)
				break
			}
			partBody, readErr := io.ReadAll(part)
			if readErr != nil {
				t.Errorf("ReadAll: %v", readErr)
			}
			switch part.FormName() {
			case "payload_json":
				if err := json.Unmarshal(partBody, &payload); err != nil {
					t.Errorf("payload JSON: %v", err)
				}
			case "files[0]":
				if part.FileName() != "saturday-arma-modlist.html" || part.Header.Get("Content-Type") != "text/html; charset=utf-8" {
					t.Errorf("file metadata = name %q content-type %q", part.FileName(), part.Header.Get("Content-Type"))
				}
				uploadedBodies = append(uploadedBodies, partBody)
			}
		}
		payloads = append(payloads, payload)
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPatch {
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{"message":"Unknown Message"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"id":"replacement-message"}`))
	}))
	defer server.Close()

	sender := New(&fakeSecrets{}, "secret")
	sender.apiBase = server.URL
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "modlist-preset-1", Kind: domain.NotificationSessionModlist,
		SessionID: "session-1", GuildID: "guild-1", ChannelID: "channel-1", Content: "Active modlist",
		Attachment: &domain.NotificationAttachment{
			ObjectKey: "sessions/session-1/input/modlists/digest/saturday-arma-modlist.html",
			Filename:  "saturday-arma-modlist.html", ContentType: "text/html; charset=utf-8",
			SHA256: digest, SizeBytes: int64(len(body)), Revision: 2,
		},
		CorrelationID: "correlation-1", RequestedAt: time.Now().UTC(),
	}
	messageID, err := sender.SendModlist(context.Background(), request, body, "deleted-message")
	if err != nil || messageID != "replacement-message" {
		t.Fatalf("SendModlist() = %q, %v", messageID, err)
	}
	if len(methods) != 2 || methods[0] != "PATCH /channels/channel-1/messages/deleted-message" ||
		methods[1] != "POST /channels/channel-1/messages" {
		t.Fatalf("methods = %#v", methods)
	}
	if len(uploadedBodies) != 2 || string(uploadedBodies[0]) != string(body) || string(uploadedBodies[1]) != string(body) {
		t.Fatalf("uploaded bodies = %#v", uploadedBodies)
	}
	if payloads[0]["content"] != "Active modlist" || payloads[0]["nonce"] != nil ||
		payloads[1]["enforce_nonce"] != true || strings.TrimSpace(fmt.Sprint(payloads[1]["nonce"])) == "" {
		t.Fatalf("payloads = %#v", payloads)
	}
}

func TestSenderRejectsModlistChecksumMismatchBeforeDiscord(t *testing.T) {
	t.Parallel()
	sender := New(&fakeSecrets{}, "secret")
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "modlist-1", Kind: domain.NotificationSessionModlist,
		SessionID: "session-1", GuildID: "guild-1", ChannelID: "channel-1", Content: "Active modlist",
		Attachment: &domain.NotificationAttachment{
			ObjectKey: "sessions/session-1/input/modlists/digest/saturday-arma-modlist.html",
			Filename:  "saturday-arma-modlist.html", ContentType: "text/html; charset=utf-8",
			SHA256: strings.Repeat("a", 64), SizeBytes: 4, Revision: 1,
		},
		CorrelationID: "correlation-1", RequestedAt: time.Now().UTC(),
	}
	if _, err := sender.SendModlist(context.Background(), request, []byte("body"), ""); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("checksum mismatch error = %v", err)
	}
}
