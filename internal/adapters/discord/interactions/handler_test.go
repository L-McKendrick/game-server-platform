package interactions

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	appaccess "github.com/L-McKendrick/game-server-platform/internal/app/access"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

var testNow = time.Date(2026, 8, 3, 20, 0, 0, 0, time.UTC)

type fixedClock struct {
	now time.Time
}

type discardCommandQueue struct{}

func (discardCommandQueue) Enqueue(context.Context, domain.CommandEnvelope) error { return nil }

func (clock fixedClock) Now() time.Time {
	return clock.now
}

type sequenceGenerator struct {
	ids   []string
	index int
}

func (generator *sequenceGenerator) New(_ time.Time) (string, error) {
	if generator.index >= len(generator.ids) {
		return "", fmt.Errorf("no test IDs remaining")
	}

	id := generator.ids[generator.index]
	generator.index++
	return id, nil
}

func TestHandlerAcknowledgesValidPing(t *testing.T) {
	t.Parallel()

	handler, _, privateKey := newTestHandler(t, []string{"correlation-ping"}, nil)
	body := []byte(`{"id":"ping-1","application_id":"app-1","type":1}`)

	response := executeSignedRequest(t, handler, privateKey, body, testNow)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}

	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Type != interactionResponsePong {
		t.Errorf("response type = %d; want %d", decoded.Type, interactionResponsePong)
	}
	if decoded.Data != nil {
		t.Errorf("PING response data = %#v; want nil", decoded.Data)
	}
}

func TestHandlerAcknowledgesAutocompleteWithExplicitEmptyChoices(t *testing.T) {
	t.Parallel()

	handler, _, privateKey := newTestHandler(t, nil, nil)
	body := marshalPayload(map[string]any{
		"id": "autocomplete-1", "application_id": "app-1",
		"type":     interactionTypeApplicationCommandAutocomplete,
		"guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{"role-1"}},
		"data": map[string]any{
			"name": "rb",
			"options": []any{map[string]any{
				"type": applicationCommandOptionSubcommand, "name": "status",
				"options": []any{map[string]any{
					"type": applicationCommandOptionString, "name": "session", "value": "sat", "focused": true,
				}},
			}},
		},
	})

	response := executeSignedRequest(t, handler, privateKey, body, testNow)

	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if response.Code != http.StatusOK || decoded.Type != interactionResponseAutocompleteResult ||
		decoded.Data == nil || decoded.Data.Choices == nil || len(*decoded.Data.Choices) != 0 {
		t.Fatalf("autocomplete response = %#v; body = %s", decoded, response.Body.String())
	}
}

func TestHandlerReturnsGuildVisibleStatusAutocompleteChoices(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(t, nil, nil)
	seedAutocompleteSession(t, repository, "session-visible", "Saturday Arma", "saturday-arma", "owner-1", "guild-1")
	seedAutocompleteSession(t, repository, "session-other-owner", "Saturday Private", "saturday-private", "owner-2", "guild-1")
	seedAutocompleteSession(t, repository, "session-other-guild", "Saturday Elsewhere", "saturday-elsewhere", "owner-1", "guild-2")
	seedHandlerSessionState(t, repository, "session-terminated", "Saturday Terminated", "saturday-terminated", "owner-1", "guild-1", domain.StateDeleted)
	body := marshalPayload(map[string]any{
		"id": "autocomplete-visible", "application_id": "app-1",
		"type": interactionTypeApplicationCommandAutocomplete, "guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{"role-1"}},
		"data": map[string]any{"name": "rb", "options": []any{map[string]any{
			"type": applicationCommandOptionSubcommand, "name": "status", "options": []any{map[string]any{
				"type": applicationCommandOptionString, "name": "session", "value": "SAT", "focused": true,
			}},
		}}},
	})

	response := executeSignedRequest(t, handler, privateKey, body, testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if response.Code != http.StatusOK || decoded.Type != interactionResponseAutocompleteResult ||
		decoded.Data == nil || decoded.Data.Choices == nil || len(*decoded.Data.Choices) != 2 {
		t.Fatalf("autocomplete response = %#v; body = %s", decoded, response.Body.String())
	}
	values := map[any]bool{}
	for _, choice := range *decoded.Data.Choices {
		values[choice.Value] = true
		if strings.Contains(choice.Name, "session-visible") || strings.Contains(choice.Name, "session-other-owner") {
			t.Fatalf("choice label exposes immutable ID: %q", choice.Name)
		}
	}
	if !values["session-visible"] || !values["session-other-owner"] || values["session-other-guild"] || values["session-terminated"] {
		t.Fatalf("choice values = %#v; want active guild sessions only", values)
	}
}

func TestHandlerAutocompleteDoesNotDiscloseSessionsBeforeAccessAuthorization(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(t, nil, nil)
	seedAutocompleteSession(t, repository, "session-secret", "Secret Session", "secret-session", "owner-1", "guild-1")
	body := marshalPayload(map[string]any{
		"id": "autocomplete-unauthorized", "application_id": "app-1",
		"type": interactionTypeApplicationCommandAutocomplete, "guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{}},
		"data": map[string]any{"name": "rb", "options": []any{map[string]any{
			"type": applicationCommandOptionSubcommand, "name": "status", "options": []any{map[string]any{
				"type": applicationCommandOptionString, "name": "session", "value": "secret", "focused": true,
			}},
		}}},
	})

	response := executeSignedRequest(t, handler, privateKey, body, testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if response.Code != http.StatusOK || decoded.Type != interactionResponseAutocompleteResult ||
		decoded.Data == nil || decoded.Data.Choices == nil || len(*decoded.Data.Choices) != 0 {
		t.Fatalf("unauthorized autocomplete response = %#v", decoded)
	}
	if strings.Contains(response.Body.String(), "session-secret") || strings.Contains(response.Body.String(), "Secret Session") {
		t.Fatalf("unauthorized autocomplete leaked session data: %s", response.Body.String())
	}
}

func TestHandlerAutocompleteHonorsDiscordChoiceLimits(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(t, nil, nil)
	for index := 0; index < 30; index++ {
		suffix := fmt.Sprintf("%02d", index)
		seedAutocompleteSession(
			t, repository, "session-"+suffix, "Session "+suffix, "session-"+suffix, "owner-1", "guild-1",
		)
	}
	body := marshalPayload(map[string]any{
		"id": "autocomplete-limit", "application_id": "app-1",
		"type": interactionTypeApplicationCommandAutocomplete, "guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{"role-1"}},
		"data": map[string]any{"name": "rb", "options": []any{map[string]any{
			"type": applicationCommandOptionSubcommand, "name": "status", "options": []any{map[string]any{
				"type": applicationCommandOptionString, "name": "session", "value": "", "focused": true,
			}},
		}}},
	})

	response := executeSignedRequest(t, handler, privateKey, body, testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || decoded.Data.Choices == nil || len(*decoded.Data.Choices) != maximumAutocompleteChoices {
		t.Fatalf("choice count response = %#v", decoded)
	}
	for _, choice := range *decoded.Data.Choices {
		if utf8.RuneCountInString(choice.Name) > maximumAutocompleteLabelRunes {
			t.Fatalf("choice label has %d runes: %q", utf8.RuneCountInString(choice.Name), choice.Name)
		}
	}
}

func TestHandlerRecognizesModalSubmission(t *testing.T) {
	t.Parallel()

	handler, _, privateKey := newTestHandler(t, nil, nil)
	body := marshalPayload(map[string]any{
		"id": "modal-1", "application_id": "app-1",
		"type":     interactionTypeModalSubmit,
		"guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{"role-1"}},
		"data": map[string]any{
			"custom_id": "rb:v1:create:1:Abcdef12",
			"components": []any{map[string]any{
				"type": componentTypeLabel,
				"component": map[string]any{
					"type": componentTypeTextInput, "custom_id": "name", "value": "Saturday Arma",
				},
			}},
		},
	})

	response := executeSignedRequest(t, handler, privateKey, body, testNow)

	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if response.Code != http.StatusOK || decoded.Type != interactionResponseChannelMessageWithSource ||
		decoded.Data == nil || !strings.Contains(decoded.Data.Content, "modal is not supported or has expired") {
		t.Fatalf("modal response = %#v; body = %s", decoded, response.Body.String())
	}
}

func TestHandlerRejectsInvalidSignature(t *testing.T) {
	t.Parallel()

	handler, _, _ := newTestHandler(t, []string{"unused"}, nil)
	body := []byte(`{"id":"ping-1","application_id":"app-1","type":1}`)
	request := httptest.NewRequest(http.MethodPost, "/discord/interactions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Signature-Timestamp", strconv.FormatInt(testNow.Unix(), 10))
	request.Header.Set("X-Signature-Ed25519", strings.Repeat("0", ed25519.SignatureSize*2))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestHandlerRejectsStaleTimestamp(t *testing.T) {
	t.Parallel()

	handler, _, privateKey := newTestHandler(t, []string{"unused"}, nil)
	body := []byte(`{"id":"ping-1","application_id":"app-1","type":1}`)

	response := executeSignedRequest(
		t,
		handler,
		privateKey,
		body,
		testNow.Add(-10*time.Minute),
	)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestHandlerRejectsMalformedAndOversizedSignedPayloads(t *testing.T) {
	t.Parallel()

	t.Run("malformed JSON", func(t *testing.T) {
		handler, _, privateKey := newTestHandler(t, nil, nil)
		response := executeSignedRequest(t, handler, privateKey, []byte(`{"application_id":"app-1"`), testNow)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid interaction payload") {
			t.Fatalf("response = %d %q; want invalid-payload rejection", response.Code, response.Body.String())
		}
	})

	t.Run("oversized body", func(t *testing.T) {
		handler, _, privateKey := newTestHandler(t, nil, nil)
		handler.maxRequestBytes = 32
		body := []byte(`{"id":"ping-1","application_id":"app-1","type":1}`)
		response := executeSignedRequest(t, handler, privateKey, body, testNow)
		if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "request body is too large") {
			t.Fatalf("response = %d %q; want body-limit rejection", response.Code, response.Body.String())
		}
	})
}

func TestHandlerRejectsStaleOrMalformedComponentsWithoutEchoingState(t *testing.T) {
	t.Parallel()

	for _, customID := range []string{
		"rb:v1:refresh:1:StaleTok",
		"rb:v1:refresh:01:MalformedTok",
	} {
		t.Run(customID, func(t *testing.T) {
			handler, _, privateKey := newTestHandler(t, nil, nil)
			body := marshalPayload(map[string]any{
				"id": "component-1", "application_id": "app-1", "type": interactionTypeMessageComponent,
				"guild_id": "guild-1", "channel_id": "channel-1",
				"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{"role-1"}},
				"data":   map[string]any{"custom_id": customID, "component_type": componentTypeButton},
			})

			response := executeSignedRequest(t, handler, privateKey, body, testNow)
			var decoded interactionResponse
			decodeResponse(t, response, &decoded)
			if response.Code != http.StatusOK || decoded.Data == nil ||
				!strings.Contains(decoded.Data.Content, "not supported or has expired") ||
				strings.Contains(decoded.Data.Content, customID) {
				t.Fatalf("component response = %#v", decoded)
			}
			if decoded.Data.Flags != messageFlagEphemeral || decoded.Data.AllowedMentions == nil ||
				len(decoded.Data.AllowedMentions.Parse) != 0 {
				t.Fatalf("component response safety = %#v", decoded.Data)
			}
		})
	}
}

func TestHandlerAuthorizesComponentBeforeReturningStaleResponse(t *testing.T) {
	t.Parallel()

	handler, _, privateKey := newTestHandler(t, nil, nil)
	customID := "rb:v1:refresh:1:SecretTk"
	body := marshalPayload(map[string]any{
		"id": "component-unauthorized", "application_id": "app-1", "type": interactionTypeMessageComponent,
		"guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{}},
		"data":   map[string]any{"custom_id": customID, "component_type": componentTypeButton},
	})

	response := executeSignedRequest(t, handler, privateKey, body, testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "not authorized") ||
		strings.Contains(decoded.Data.Content, customID) || strings.Contains(decoded.Data.Content, "expired") {
		t.Fatalf("unauthorized component response = %#v", decoded)
	}
}

func TestHandlerCreatesAndReplaysSession(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(
		t,
		[]string{"correlation-1", "correlation-2"},
		[]string{"session-1", "event-1"},
	)
	body := createCommandBody("interaction-create-1", "owner-1", "guild-1", "channel-1")

	first := executeSignedRequest(t, handler, privateKey, body, testNow)
	second := executeSignedRequest(t, handler, privateKey, body, testNow)

	for index, response := range []*httptest.ResponseRecorder{first, second} {
		if response.Code != http.StatusOK {
			t.Fatalf("response %d status = %d; body = %s", index+1, response.Code, response.Body.String())
		}

		var decoded interactionResponse
		decodeResponse(t, response, &decoded)
		if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "saturday-arma") || strings.Contains(decoded.Data.Content, "session-1") {
			t.Fatalf("response %d content = %#v; want slug without immutable ID", index+1, decoded.Data)
		}
		if decoded.Data.Flags != messageFlagEphemeral {
			t.Errorf("response %d flags = %d; want %d", index+1, decoded.Data.Flags, messageFlagEphemeral)
		}
	}

	sessions, err := repository.ListByOwner(context.Background(), "owner-1", 10)
	if err != nil {
		t.Fatalf("ListByOwner() returned error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session count = %d; want 1", len(sessions))
	}
	if eventCount := len(repository.Events("session-1")); eventCount != 1 {
		t.Errorf("event count = %d; want 1", eventCount)
	}
}

func TestHandlerRejectsLegacySessionCommand(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(
		t,
		[]string{"correlation-legacy"},
		[]string{"unused-session", "unused-event"},
	)
	body := bytes.Replace(
		createCommandBody("interaction-legacy", "owner-1", "guild-1", "channel-1"),
		[]byte(`"name":"rb"`),
		[]byte(`"name":"session"`),
		1,
	)

	response := executeSignedRequest(t, handler, privateKey, body, testNow)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "supported `/rb` subcommands") {
		t.Fatalf("response content = %#v; want /rb guidance", decoded.Data)
	}
	sessions, err := repository.ListByOwner(context.Background(), "owner-1", 10)
	if err != nil {
		t.Fatalf("ListByOwner() returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("session count = %d; want 0", len(sessions))
	}
}

func TestHandlerGuildManagerConfiguresRolesWithSelectMenu(t *testing.T) {
	t.Parallel()

	handler, _, privateKey := newTestHandler(
		t,
		[]string{"correlation-admin-command", "correlation-admin-select", "correlation-create"},
		[]string{"session-1", "event-1"},
	)
	menuResponse := executeSignedRequest(
		t,
		handler,
		privateKey,
		adminAccessCommandBody("interaction-admin", "guild-manager", "guild-1", "channel-other"),
		testNow,
	)

	if menuResponse.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d; body = %s", menuResponse.Code, http.StatusOK, menuResponse.Body.String())
	}
	var menu interactionResponse
	decodeResponse(t, menuResponse, &menu)
	if menu.Type != interactionResponseChannelMessageWithSource || menu.Data == nil || menu.Data.Components == nil ||
		len(*menu.Data.Components) != 1 || (*menu.Data.Components)[0].Components[0].Type != componentTypeRoleSelect {
		t.Fatalf("admin menu response = %#v", menu)
	}

	selectionResponse := executeSignedRequest(
		t, handler, privateKey,
		adminRoleSelectionBody("interaction-select", "guild-manager", "guild-1", "channel-other", []string{"role-2", "role-1"}),
		testNow,
	)
	var selection interactionResponse
	decodeResponse(t, selectionResponse, &selection)
	if selection.Type != interactionResponseUpdateMessage || selection.Data == nil ||
		!strings.Contains(selection.Data.Content, "Access settings updated") ||
		!strings.Contains(selection.Data.Content, "<@&role-1>") ||
		!strings.Contains(selection.Data.Content, "<@&role-2>") ||
		selection.Data.Components == nil || len(*selection.Data.Components) != 0 {
		t.Fatalf("admin selection response = %#v", selection)
	}

	createResponse := executeSignedRequest(
		t, handler, privateKey,
		createCommandBody("interaction-create-after-access", "owner-1", "guild-1", "different-channel"),
		testNow,
	)
	var created interactionResponse
	decodeResponse(t, createResponse, &created)
	if created.Data == nil || !strings.Contains(created.Data.Content, "saturday-arma") || strings.Contains(created.Data.Content, "session-1") {
		t.Fatalf("command after role configuration = %#v", created)
	}
}

func TestHandlerRejectsAccessConfigurationWithoutGuildManagementPermission(t *testing.T) {
	t.Parallel()

	handler, _, privateKey := newTestHandler(t, nil, nil)
	response := executeSignedRequest(
		t, handler, privateKey,
		adminAccessCommandBodyWithPermissions("interaction-admin", "member-1", "guild-1", "channel-1", "0"),
		testNow,
	)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "Manage Server") || decoded.Data.Components != nil {
		t.Fatalf("unauthorized admin response = %#v", decoded)
	}
}

func TestHandlerListsAndShowsSessionStatus(t *testing.T) {
	t.Parallel()

	handler, _, privateKey := newTestHandler(
		t,
		[]string{"correlation-create", "correlation-list", "correlation-status"},
		[]string{"session-1", "event-1"},
	)

	createResponse := executeSignedRequest(
		t,
		handler,
		privateKey,
		createCommandBody("interaction-create", "owner-1", "guild-1", "channel-1"),
		testNow,
	)
	if createResponse.Code != http.StatusOK {
		t.Fatalf("create status = %d; body = %s", createResponse.Code, createResponse.Body.String())
	}

	listResponse := executeSignedRequest(
		t,
		handler,
		privateKey,
		listCommandBody("interaction-list", "owner-1", "guild-1", "channel-1"),
		testNow,
	)
	var listDecoded interactionResponse
	decodeResponse(t, listResponse, &listDecoded)
	if listDecoded.Data == nil || !strings.Contains(listDecoded.Data.Content, "Saturday Arma") {
		t.Fatalf("list content = %#v; want created session", listDecoded.Data)
	}

	statusResponse := executeSignedRequest(
		t,
		handler,
		privateKey,
		statusCommandBody("interaction-status", "owner-1", "guild-1", "channel-1", "saturday-arma"),
		testNow,
	)
	var statusDecoded interactionResponse
	decodeResponse(t, statusResponse, &statusDecoded)
	if statusDecoded.Data == nil || !strings.Contains(statusDecoded.Data.Content, "Status: Setting up") || strings.Contains(statusDecoded.Data.Content, "session-1") {
		t.Fatalf("status content = %#v; want readable status without immutable ID", statusDecoded.Data)
	}
}

func TestHandlerListsActiveSessionsWithLifecycleFilterAndPagination(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(t, []string{"correlation-page-2", "correlation-deleted"}, nil)
	for index := 1; index <= 7; index++ {
		suffix := strconv.Itoa(index)
		seedAutocompleteSession(t, repository, "session-"+suffix, "Session "+suffix, "session-"+suffix, "owner-1", "guild-1")
	}
	seedHandlerSessionState(t, repository, "session-deleted", "Deleted Session", "deleted-session", "owner-1", "guild-1", domain.StateDeleted)

	pageResponse := executeSignedRequest(t, handler, privateKey, commandBody(
		"interaction-page-2", "owner-1", "guild-1", "channel-1", "list",
		[]any{map[string]any{"type": applicationCommandOptionInteger, "name": "page", "value": 2}},
	), testNow)
	var pageDecoded interactionResponse
	decodeResponse(t, pageResponse, &pageDecoded)
	if pageDecoded.Data == nil || !strings.Contains(pageDecoded.Data.Content, "Page 2 of 2") ||
		strings.Count(pageDecoded.Data.Content, "Slug:") != 2 || strings.Contains(pageDecoded.Data.Content, "deleted-session") {
		t.Fatalf("page response = %#v", pageDecoded.Data)
	}

	deletedResponse := executeSignedRequest(t, handler, privateKey, commandBody(
		"interaction-deleted", "owner-1", "guild-1", "channel-1", "list",
		[]any{map[string]any{"type": applicationCommandOptionString, "name": "state", "value": "deleted"}},
	), testNow)
	var deletedDecoded interactionResponse
	decodeResponse(t, deletedResponse, &deletedDecoded)
	if deletedDecoded.Data == nil || !strings.Contains(deletedDecoded.Data.Content, "Terminated records") ||
		!strings.Contains(deletedDecoded.Data.Content, "deleted-session") || strings.Contains(deletedDecoded.Data.Content, "session-1`") {
		t.Fatalf("deleted response = %#v", deletedDecoded.Data)
	}
}

func TestHandlerAllowsGuildAdministratorToRequestAnotherOwnersSleep(t *testing.T) {
	t.Parallel()
	handler, repository, privateKey := newTestHandler(t, []string{"correlation-sleep"}, nil)
	seedRunningHandlerSession(t, repository)
	body := marshalPayload(map[string]any{
		"id": "interaction-sleep", "application_id": "app-1", "type": interactionTypeApplicationCommand,
		"guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "admin-1"}, "permissions": "8"},
		"data": map[string]any{"name": "rb", "options": []any{map[string]any{
			"type": applicationCommandOptionSubcommand, "name": "sleep",
			"options": []any{map[string]any{"type": applicationCommandOptionString, "name": "session", "value": "running-session"}},
		}}},
	})
	response := executeSignedRequest(t, handler, privateKey, body, testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "Sleep request accepted") {
		t.Fatalf("response = %#v", decoded.Data)
	}
}

func TestHandlerArchiveRequiresExplicitConfirmation(t *testing.T) {
	t.Parallel()
	handler, repository, privateKey := newTestHandler(t, []string{"correlation-archive"}, nil)
	seedRunningHandlerSession(t, repository)
	body := marshalPayload(map[string]any{
		"id": "interaction-archive", "application_id": "app-1", "type": interactionTypeApplicationCommand,
		"guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{"role-1"}},
		"data": map[string]any{"name": "rb", "options": []any{map[string]any{
			"type": applicationCommandOptionSubcommand, "name": "archive",
			"options": []any{
				map[string]any{"type": applicationCommandOptionString, "name": "session", "value": "running-session"},
				map[string]any{"type": applicationCommandOptionBoolean, "name": "confirm", "value": false},
			},
		}}},
	})
	response := executeSignedRequest(t, handler, privateKey, body, testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "removes the current EC2 instance and EBS volumes") {
		t.Fatalf("response = %#v", decoded.Data)
	}
}

func TestHandlerArchiveAcceptsOwnerConfirmationAndWarnsAboutInterruption(t *testing.T) {
	t.Parallel()
	handler, repository, privateKey := newTestHandler(t, []string{"correlation-archive"}, nil)
	seedRunningHandlerSession(t, repository)
	body := marshalPayload(map[string]any{
		"id": "interaction-archive", "application_id": "app-1", "type": interactionTypeApplicationCommand,
		"guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{"role-1"}},
		"data": map[string]any{"name": "rb", "options": []any{map[string]any{
			"type": applicationCommandOptionSubcommand, "name": "archive",
			"options": []any{
				map[string]any{"type": applicationCommandOptionString, "name": "session", "value": "running-session"},
				map[string]any{"type": applicationCommandOptionBoolean, "name": "confirm", "value": true},
			},
		}}},
	})
	response := executeSignedRequest(t, handler, privateKey, body, testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "Archive request accepted") || !strings.Contains(decoded.Data.Content, "removed only after") {
		t.Fatalf("response = %#v", decoded.Data)
	}
}

func TestHandlerTerminateRequiresOwnerConfirmationAndWarnsIrreversible(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, interactionID string
		confirmed           bool
		want                string
	}{
		{name: "confirmation required", interactionID: "interaction-terminate-no", confirmed: false, want: "immediate and irreversible"},
		{name: "confirmed request", interactionID: "interaction-terminate-yes", confirmed: true, want: "Terminate request accepted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, repository, privateKey := newTestHandler(t, []string{"correlation-terminate"}, nil)
			seedRunningHandlerSession(t, repository)
			body := commandBody(test.interactionID, "owner-1", "guild-1", "channel-1", "terminate", []any{
				map[string]any{"type": applicationCommandOptionString, "name": "session", "value": "running-session"},
				map[string]any{"type": applicationCommandOptionBoolean, "name": "confirm", "value": test.confirmed},
			})
			response := executeSignedRequest(t, handler, privateKey, body, testNow)
			var decoded interactionResponse
			decodeResponse(t, response, &decoded)
			if decoded.Data == nil || !strings.Contains(decoded.Data.Content, test.want) {
				t.Fatalf("response = %#v; want %q", decoded.Data, test.want)
			}
		})
	}
}

func TestHandlerConfiguresAndAcceptsMissionAttachment(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(
		t,
		[]string{"correlation-create", "correlation-configure", "correlation-upload"},
		[]string{"session-1", "event-create", "event-configure"},
	)
	executeSignedRequest(t, handler, privateKey, createCommandBody("interaction-create", "owner-1", "guild-1", "channel-1"), testNow)

	configuredResponse := executeSignedRequest(
		t,
		handler,
		privateKey,
		configureCommandBody("interaction-configure", "owner-1", "guild-1", "channel-1", "session-1"),
		testNow,
	)
	var configuredDecoded interactionResponse
	decodeResponse(t, configuredResponse, &configuredDecoded)
	if configuredDecoded.Data == nil || !strings.Contains(configuredDecoded.Data.Content, "Configuration: `1`") {
		t.Fatalf("configure content = %#v", configuredDecoded.Data)
	}
	stored, err := repository.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("repository.Get() error = %v", err)
	}
	if stored.ConfigurationRevision != 1 || !stored.TeamSpeakEnabled {
		t.Errorf("stored configuration = %#v", stored)
	}
	if !stored.Vanilla || configuredDecoded.Data == nil || !strings.Contains(configuredDecoded.Data.Content, "Mode: Vanilla") {
		t.Errorf("vanilla configuration/response = %#v / %#v", stored, configuredDecoded.Data)
	}

	uploadResponse := executeSignedRequest(
		t,
		handler,
		privateKey,
		uploadCommandBody("interaction-upload", "owner-1", "guild-1", "channel-1", "session-1"),
		testNow,
	)
	var uploadDecoded interactionResponse
	decodeResponse(t, uploadResponse, &uploadDecoded)
	if uploadDecoded.Data == nil || !strings.Contains(uploadDecoded.Data.Content, "accepted for validation") {
		t.Fatalf("upload content = %#v", uploadDecoded.Data)
	}
}

func TestHandlerRejectsUnapprovedGuildWithoutCallingService(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(t, []string{"unused"}, nil)
	response := executeSignedRequest(
		t,
		handler,
		privateKey,
		createCommandBody("interaction-create", "owner-1", "guild-other", "channel-1"),
		testNow,
	)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d", response.Code, http.StatusOK)
	}

	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "not enabled") {
		t.Fatalf("content = %#v; want guild rejection", decoded.Data)
	}

	sessions, err := repository.ListByOwner(context.Background(), "owner-1", 10)
	if err != nil {
		t.Fatalf("ListByOwner() returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("session count = %d; want 0", len(sessions))
	}
}

func TestHandlerShowsGuildSessionStatusToApprovedNonOwner(t *testing.T) {
	t.Parallel()

	handler, _, privateKey := newTestHandler(
		t,
		[]string{"correlation-create", "correlation-status"},
		[]string{"session-1", "event-1"},
	)

	executeSignedRequest(
		t,
		handler,
		privateKey,
		createCommandBody("interaction-create", "owner-1", "guild-1", "channel-1"),
		testNow,
	)

	response := executeSignedRequest(
		t,
		handler,
		privateKey,
		statusCommandBody("interaction-status", "owner-2", "guild-1", "channel-1", "session-1"),
		testNow,
	)

	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "Saturday Arma") || strings.Contains(decoded.Data.Content, "session-1") {
		t.Fatalf("content = %#v; want readable guild-visible status without ID", decoded.Data)
	}
}

func TestHandlerDoesNotBroadenMutationAuthorizationWithGuildStatusAccess(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(
		t, []string{"correlation-create", "correlation-configure"}, []string{"session-1", "event-1"},
	)
	executeSignedRequest(t, handler, privateKey, createCommandBody("interaction-create", "owner-1", "guild-1", "channel-1"), testNow)
	response := executeSignedRequest(
		t, handler, privateKey,
		configureCommandBody("interaction-configure", "owner-2", "guild-1", "channel-1", "session-1"), testNow,
	)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "Session not found") {
		t.Fatalf("mutation response = %#v; want owner-scoped rejection", decoded.Data)
	}
	session, err := repository.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if session.ConfigurationRevision != 0 {
		t.Fatalf("configuration revision = %d; want unchanged", session.ConfigurationRevision)
	}
}

func TestParsePublicKeyRejectsWrongLength(t *testing.T) {
	t.Parallel()

	_, err := ParsePublicKey("abcd")
	if err == nil {
		t.Fatal("ParsePublicKey() returned nil error; want length error")
	}
}

func newTestHandler(
	t *testing.T,
	correlationIDs []string,
	serviceIDs []string,
) (*Handler, *memory.SessionRepository, ed25519.PrivateKey) {
	t.Helper()

	seed := bytes.Repeat([]byte{7}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)

	repository := memory.NewSessionRepository()
	service, err := appsession.NewService(
		repository,
		&sequenceGenerator{ids: serviceIDs},
		fixedClock{now: testNow},
		7*24*time.Hour,
		appsession.WithArtifactQueue(memory.NewArtifactQueue()),
		appsession.WithCommandQueue(discardCommandQueue{}),
	)
	if err != nil {
		t.Fatalf("NewService() returned error: %v", err)
	}
	accessService, err := appaccess.NewService(
		memory.NewAccessPolicyRepository(),
		[]string{"role-1"},
		[]string{"channel-1"},
		fixedClock{now: testNow},
	)
	if err != nil {
		t.Fatalf("access.NewService() returned error: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := NewHandler(
		service,
		accessService,
		&sequenceGenerator{ids: correlationIDs},
		fixedClock{now: testNow},
		logger,
		Config{
			PublicKey:       publicKey,
			ApplicationID:   "app-1",
			AllowedGuildIDs: []string{"guild-1"},
			MaxRequestBytes: defaultMaxRequestBytes,
			SignatureMaxAge: defaultSignatureMaxAge,
		},
	)
	if err != nil {
		t.Fatalf("NewHandler() returned error: %v", err)
	}

	return handler, repository, privateKey
}

func seedAutocompleteSession(
	t *testing.T,
	repository *memory.SessionRepository,
	sessionID string,
	displayName string,
	slug string,
	ownerID string,
	guildID string,
) {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: sessionID, Slug: slug, DisplayName: displayName, GameType: "arma3",
		OwnerDiscordUserID: ownerID, GuildID: guildID, ChannelID: "channel-1",
	}, testNow)
	if err != nil {
		t.Fatalf("NewSession() returned error: %v", err)
	}
	event := domain.NewSessionCreatedEvent("event-"+sessionID, "correlation-"+sessionID, testActorForInteraction(ownerID), session, testNow)
	idempotency, err := domain.NewCompletedIdempotencyRecord(
		"autocomplete:"+sessionID, "hash-"+sessionID, sessionID, testNow, time.Hour,
	)
	if err != nil {
		t.Fatalf("NewCompletedIdempotencyRecord() returned error: %v", err)
	}
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatalf("seed autocomplete session: %v", err)
	}
}

func seedHandlerSessionState(
	t *testing.T,
	repository *memory.SessionRepository,
	sessionID string,
	displayName string,
	slug string,
	ownerID string,
	guildID string,
	state domain.LifecycleState,
) {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: sessionID, Slug: slug, DisplayName: displayName, GameType: "arma3",
		OwnerDiscordUserID: ownerID, GuildID: guildID, ChannelID: "channel-1",
	}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState = state, state, state
	event := domain.NewSessionCreatedEvent("event-"+sessionID, "correlation-"+sessionID, testActorForInteraction(ownerID), session, testNow)
	idempotency, err := domain.NewCompletedIdempotencyRecord("state:"+sessionID, "hash-"+sessionID, sessionID, testNow, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatalf("seed state session: %v", err)
	}
}

func testActorForInteraction(userID string) domain.Actor {
	return domain.Actor{Type: domain.ActorTypeDiscordUser, ID: userID}
}

func executeSignedRequest(
	t *testing.T,
	handler *Handler,
	privateKey ed25519.PrivateKey,
	body []byte,
	signedAt time.Time,
) *httptest.ResponseRecorder {
	t.Helper()

	timestamp := strconv.FormatInt(signedAt.Unix(), 10)
	message := append([]byte(timestamp), body...)
	signature := ed25519.Sign(privateKey, message)

	request := httptest.NewRequest(http.MethodPost, "/discord/interactions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Signature-Timestamp", timestamp)
	request.Header.Set("X-Signature-Ed25519", hex.EncodeToString(signature))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()

	if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, response.Body.String())
	}
}

func createCommandBody(interactionID, ownerID, guildID, channelID string) []byte {
	return marshalPayload(map[string]any{
		"id":             interactionID,
		"application_id": "app-1",
		"type":           interactionTypeApplicationCommand,
		"guild_id":       guildID,
		"channel_id":     channelID,
		"member": map[string]any{
			"user":  map[string]any{"id": ownerID},
			"roles": []string{"role-1"},
		},
		"data": map[string]any{
			"name": "rb",
			"options": []any{
				map[string]any{
					"type": applicationCommandOptionSubcommand,
					"name": "create",
					"options": []any{
						map[string]any{"type": applicationCommandOptionString, "name": "name", "value": "Saturday Arma"},
						map[string]any{"type": applicationCommandOptionString, "name": "game", "value": "arma3"},
					},
				},
			},
		},
	})
}

func listCommandBody(interactionID, ownerID, guildID, channelID string) []byte {
	return commandBody(interactionID, ownerID, guildID, channelID, "list", nil)
}

func statusCommandBody(interactionID, ownerID, guildID, channelID, sessionID string) []byte {
	return commandBody(
		interactionID,
		ownerID,
		guildID,
		channelID,
		"status",
		[]any{
			map[string]any{
				"type":  applicationCommandOptionString,
				"name":  "session",
				"value": sessionID,
			},
		},
	)
}

func configureCommandBody(interactionID, ownerID, guildID, channelID, sessionID string) []byte {
	return commandBody(interactionID, ownerID, guildID, channelID, "configure", []any{
		map[string]any{"type": applicationCommandOptionString, "name": "session", "value": sessionID},
		map[string]any{"type": applicationCommandOptionString, "name": "profile", "value": "arma3-default"},
		map[string]any{"type": applicationCommandOptionInteger, "name": "sleep-minutes", "value": 60},
		map[string]any{"type": applicationCommandOptionInteger, "name": "archive-days", "value": 14},
		map[string]any{"type": applicationCommandOptionBoolean, "name": "teamspeak", "value": true},
		map[string]any{"type": applicationCommandOptionBoolean, "name": "vanilla", "value": true},
	})
}

func uploadCommandBody(interactionID, ownerID, guildID, channelID, sessionID string) []byte {
	body := map[string]any{
		"id": interactionID, "application_id": "app-1", "type": interactionTypeApplicationCommand,
		"guild_id": guildID, "channel_id": channelID,
		"member": map[string]any{"user": map[string]any{"id": ownerID}, "roles": []string{"role-1"}},
		"data": map[string]any{
			"name": "rb",
			"options": []any{map[string]any{
				"type": applicationCommandOptionSubcommand, "name": "upload-mission",
				"options": []any{
					map[string]any{"type": applicationCommandOptionString, "name": "session", "value": sessionID},
					map[string]any{"type": applicationCommandOptionAttachment, "name": "file", "value": "attachment-1"},
				},
			}},
			"resolved": map[string]any{"attachments": map[string]any{
				"attachment-1": map[string]any{
					"id": "attachment-1", "filename": "operation.pbo", "size": 1024,
					"url":          "https://cdn.discordapp.com/attachments/1/2/operation.pbo",
					"content_type": "application/octet-stream",
				},
			}},
		},
	}
	return marshalPayload(body)
}

func commandBody(
	interactionID string,
	ownerID string,
	guildID string,
	channelID string,
	subcommand string,
	options []any,
) []byte {
	return marshalPayload(map[string]any{
		"id":             interactionID,
		"application_id": "app-1",
		"type":           interactionTypeApplicationCommand,
		"guild_id":       guildID,
		"channel_id":     channelID,
		"member": map[string]any{
			"user":  map[string]any{"id": ownerID},
			"roles": []string{"role-1"},
		},
		"data": map[string]any{
			"name": "rb",
			"options": []any{
				map[string]any{
					"type":    applicationCommandOptionSubcommand,
					"name":    subcommand,
					"options": options,
				},
			},
		},
	})
}

func adminAccessCommandBody(interactionID, ownerID, guildID, channelID string) []byte {
	return adminAccessCommandBodyWithPermissions(interactionID, ownerID, guildID, channelID, "32")
}

func adminAccessCommandBodyWithPermissions(interactionID, ownerID, guildID, channelID, permissions string) []byte {
	return marshalPayload(map[string]any{
		"id": interactionID, "application_id": "app-1", "type": interactionTypeApplicationCommand,
		"guild_id": guildID, "channel_id": channelID,
		"member": map[string]any{"user": map[string]any{"id": ownerID}, "roles": []string{}, "permissions": permissions},
		"data": map[string]any{
			"name": "admin",
			"options": []any{map[string]any{
				"type": applicationCommandOptionSubcommand, "name": "access",
			}},
		},
	})
}

func adminRoleSelectionBody(interactionID, ownerID, guildID, channelID string, roleIDs []string) []byte {
	return marshalPayload(map[string]any{
		"id": interactionID, "application_id": "app-1", "type": interactionTypeMessageComponent,
		"guild_id": guildID, "channel_id": channelID,
		"member": map[string]any{"user": map[string]any{"id": ownerID}, "roles": []string{}, "permissions": "32"},
		"data": map[string]any{
			"custom_id": adminRoleSelectCustomID, "component_type": componentTypeRoleSelect, "values": roleIDs,
		},
	})
}

func seedRunningHandlerSession(t *testing.T, repository *memory.SessionRepository) {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "running-session", Slug: "running-session", DisplayName: "Running Session", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = domain.StateRunning, domain.StateRunning, domain.StateRunning, domain.HealthHealthy
	session.Infrastructure = domain.Infrastructure{
		CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"},
		InstanceProfile: "instance-profile", AMIID: "ami-1", InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.1", LastObservedAt: testNow,
	}
	event := domain.NewSessionCreatedEvent("running-event", "running-correlation", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, testNow)
	idempotency, err := domain.NewCompletedIdempotencyRecord("running-create", "running-hash", session.ID, testNow, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatal(err)
	}
}

func marshalPayload(value any) []byte {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}

var _ SessionService = (*appsession.Service)(nil)
