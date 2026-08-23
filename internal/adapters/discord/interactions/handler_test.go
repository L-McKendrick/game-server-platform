package interactions

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	"github.com/L-McKendrick/game-server-platform/internal/adapters/discord/componentid"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	appaccess "github.com/L-McKendrick/game-server-platform/internal/app/access"
	appreset "github.com/L-McKendrick/game-server-platform/internal/app/reset"
	appserverconfig "github.com/L-McKendrick/game-server-platform/internal/app/serverconfig"
	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

var testNow = time.Date(2026, 8, 3, 20, 0, 0, 0, time.UTC)

type fixedClock struct {
	now time.Time
}

type sequencePlayerQuery struct {
	statuses []domain.PlayerStatus
	index    int
}

func (query *sequencePlayerQuery) Query(context.Context, string) (domain.PlayerStatus, error) {
	status := query.statuses[query.index]
	if query.index < len(query.statuses)-1 {
		query.index++
	}
	return status, nil
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

func TestHandlerOpensAndSubmitsPrivateModsModalForRunningSession(t *testing.T) {
	t.Parallel()
	handler, repository, queue, privateKey := newTestHandlerWithArtifactQueue(t, []string{"correlation-mods-open", "correlation-mods-submit"}, nil)
	seedHandlerPresetRevisionSession(t, repository)
	openBody := marshalPayload(map[string]any{
		"id": "mods-open", "application_id": "app-1", "type": interactionTypeApplicationCommand, "guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{"role-1"}},
		"data":   map[string]any{"name": "rb", "options": []any{map[string]any{"type": applicationCommandOptionSubcommand, "name": "mods", "options": []any{map[string]any{"type": applicationCommandOptionString, "name": "session", "value": "session-mods"}}}}},
	})
	opened := executeSignedRequest(t, handler, privateKey, openBody, testNow)
	var modal interactionResponse
	decodeResponse(t, opened, &modal)
	if modal.Type != interactionResponseModal || modal.Data == nil || modal.Data.CustomID != "rb:mods:v1:session-mods:1" || modal.Data.Components == nil || len(*modal.Data.Components) != 1 {
		t.Fatalf("mods modal response = %#v body=%s", modal, opened.Body.String())
	}
	submitBody := marshalPayload(map[string]any{
		"id": "mods-submit", "application_id": "app-1", "type": interactionTypeModalSubmit, "guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{"role-1"}},
		"data": map[string]any{
			"custom_id":  modal.Data.CustomID,
			"resolved":   map[string]any{"attachments": map[string]any{"attachment-mods": map[string]any{"id": "attachment-mods", "filename": "revision.html", "content_type": "text/html", "size": 2048, "url": "https://cdn.discordapp.com/attachments/1/2/revision.html"}}},
			"components": []any{map[string]any{"type": componentTypeLabel, "component": map[string]any{"type": componentTypeFileUpload, "custom_id": modsPresetCustomID, "values": []string{"attachment-mods"}}}},
		},
	})
	submitted := executeSignedRequest(t, handler, privateKey, submitBody, testNow)
	var response interactionResponse
	decodeResponse(t, submitted, &response)
	if response.Data == nil || !strings.Contains(response.Data.Content, "queued for validation") || !strings.Contains(response.Data.Content, "not interrupted") {
		t.Fatalf("mods submit response = %#v body=%s", response, submitted.Body.String())
	}
	requests := queue.Requests()
	if len(requests) != 1 || requests[0].Purpose != domain.ArtifactPurposePresetRevision || requests[0].ExpectedActivePresetRevision != 1 || requests[0].SessionID != "session-mods" {
		t.Fatalf("mods queue requests = %#v", requests)
	}
}

func TestHandlerCreatesConfiguredDraftAndQueuesModalUploadsIdempotently(t *testing.T) {
	t.Parallel()

	handler, repository, queue, cards, privateKey := newTestHandlerWithQueues(
		t,
		[]string{"correlation-modal-1", "correlation-modal-2"},
		[]string{"session-modal", "event-created", "event-configured", "event-artifacts"},
	)
	body := createModalSubmissionBody(
		"interaction-modal", "Saturday Arma", []string{createFeatureModded, createFeatureTeamSpeak}, true, "mission.pbo",
	)

	for attempt := 1; attempt <= 2; attempt++ {
		response := executeSignedRequest(t, handler, privateKey, body, testNow)
		var decoded interactionResponse
		decodeResponse(t, response, &decoded)
		if response.Code != http.StatusOK || decoded.Data == nil ||
			!strings.Contains(decoded.Data.Content, "Draft session created") ||
			!strings.Contains(decoded.Data.Content, "Mission queued for validation") ||
			!strings.Contains(decoded.Data.Content, "Preset queued for validation") ||
			!strings.Contains(decoded.Data.Content, "have not been validated yet") {
			t.Fatalf("attempt %d response = %#v", attempt, decoded)
		}
	}

	sessions, err := repository.ListByOwner(context.Background(), "owner-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %#v; want one idempotent draft", sessions)
	}
	session := sessions[0]
	if session.ID != "session-modal" || session.Description != "Weekly co-op" ||
		session.ConfigurationRevision != 1 || session.Vanilla || !session.TeamSpeakEnabled ||
		session.SleepAfterSeconds != defaultSleepMinutes*60 || session.ArchiveAfterSeconds != defaultArchiveDays*86400 {
		t.Fatalf("configured draft = %#v", session)
	}
	if events := repository.Events(session.ID); len(events) != 3 || events[2].Type != domain.EventArtifactRequested {
		t.Fatalf("events = %#v; want creation, configuration, and artifact preparation", events)
	}
	requests := queue.Requests()
	if len(requests) != 2 || requests[0].Kind != domain.ArtifactMission || requests[1].Kind != domain.ArtifactPreset ||
		requests[0].SessionID != session.ID || requests[1].SessionID != session.ID ||
		requests[0].IdempotencyKey == requests[1].IdempotencyKey {
		t.Fatalf("queued requests = %#v", requests)
	}
	cardRequests := cards.Requests()
	if len(cardRequests) != 1 || cardRequests[0].Kind != domain.NotificationSessionCard ||
		cardRequests[0].SessionID != session.ID || cardRequests[0].CardRevision != session.Version ||
		!strings.Contains(cardRequests[0].Content, "Setting up: Saturday Arma") {
		t.Fatalf("card requests = %#v; want one replay-safe public card", cardRequests)
	}
}

func TestHandlerAcceptsVanillaCreationWithoutPreset(t *testing.T) {
	t.Parallel()

	handler, repository, queue, privateKey := newTestHandlerWithArtifactQueue(
		t,
		[]string{"correlation-vanilla"},
		[]string{"session-vanilla", "event-created", "event-configured", "event-artifacts"},
	)
	response := executeSignedRequest(t, handler, privateKey, createModalSubmissionBody(
		"interaction-vanilla", "Vanilla Night", nil, false, "mission.pbo",
	), testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "Mode: Vanilla") ||
		strings.Contains(decoded.Data.Content, "add one before") {
		t.Fatalf("vanilla response = %#v", decoded.Data)
	}
	sessions, err := repository.ListByOwner(context.Background(), "owner-1", 10)
	if err != nil || len(sessions) != 1 || !sessions[0].Vanilla || len(queue.Requests()) != 1 {
		t.Fatalf("vanilla draft sessions=%#v requests=%#v err=%v", sessions, queue.Requests(), err)
	}
}

func TestHandlerKeepsModdedCreationWithoutPresetRecoverable(t *testing.T) {
	t.Parallel()

	handler, repository, queue, privateKey := newTestHandlerWithArtifactQueue(
		t,
		[]string{"correlation-modded-missing"},
		[]string{"session-modded-missing", "event-created", "event-configured", "event-artifacts"},
	)
	response := executeSignedRequest(t, handler, privateKey, createModalSubmissionBody(
		"interaction-modded-missing", "Modded Missing Preset", []string{createFeatureModded}, false, "mission.pbo",
	), testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "Mode: Modded") ||
		!strings.Contains(decoded.Data.Content, "add one before this modded session can become ready") {
		t.Fatalf("modded response = %#v", decoded.Data)
	}
	sessions, err := repository.ListByOwner(context.Background(), "owner-1", 10)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("modded draft sessions=%#v err=%v", sessions, err)
	}
	if sessions[0].Vanilla || sessions[0].LifecycleState != domain.StateDraft ||
		sessions[0].MissionArtifactStatus != domain.ArtifactPending || sessions[0].PresetArtifactStatus != "" {
		t.Fatalf("modded draft = %#v; want mission pending and missing preset", sessions[0])
	}
	if requests := queue.Requests(); len(requests) != 1 || requests[0].Kind != domain.ArtifactMission {
		t.Fatalf("modded queued requests = %#v; want mission only", requests)
	}
}

func TestParseCreateModalSubmissionEnforcesServerSideLimits(t *testing.T) {
	t.Parallel()

	decode := func(t *testing.T) interactionPayload {
		t.Helper()
		var payload interactionPayload
		if err := json.Unmarshal(createModalSubmissionBody(
			"interaction-limits", "Saturday Arma", []string{createFeatureModded}, true, "mission.pbo",
		), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	attachment := func(payload *interactionPayload, id string) interactionAttachment {
		t.Helper()
		return payload.Data.Resolved.Attachments[id]
	}
	setAttachment := func(payload *interactionPayload, id string, value interactionAttachment) {
		payload.Data.Resolved.Attachments[id] = value
	}

	maximum := decode(t)
	maximum.Data.Components[0].Component.Value = strings.Repeat("界", 100)
	maximum.Data.Components[1].Component.Value = strings.Repeat("界", 64)
	mission := attachment(&maximum, "attachment-mission")
	mission.Size = 100 * 1024 * 1024
	setAttachment(&maximum, "attachment-mission", mission)
	preset := attachment(&maximum, "attachment-preset")
	preset.Size = 10 * 1024 * 1024
	setAttachment(&maximum, "attachment-preset", preset)
	if _, err := parseCreateModalSubmission(maximum, testActorForInteraction("owner-1"), "correlation-limits", "discord:limits", testNow); err != nil {
		t.Fatalf("maximum permitted modal values returned error: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*interactionPayload)
		want   string
	}{
		{name: "name over 100 characters", want: "1 to 100 characters", mutate: func(payload *interactionPayload) {
			payload.Data.Components[0].Component.Value = strings.Repeat("界", 101)
		}},
		{name: "description over 64 characters", want: "at most 64 characters", mutate: func(payload *interactionPayload) {
			payload.Data.Components[1].Component.Value = strings.Repeat("界", 65)
		}},
		{name: "mission over 100 MiB", want: "no larger than 100 MiB", mutate: func(payload *interactionPayload) {
			value := attachment(payload, "attachment-mission")
			value.Size = 100*1024*1024 + 1
			setAttachment(payload, "attachment-mission", value)
		}},
		{name: "preset over 10 MiB", want: "no larger than 10 MiB", mutate: func(payload *interactionPayload) {
			value := attachment(payload, "attachment-preset")
			value.Size = 10*1024*1024 + 1
			setAttachment(payload, "attachment-preset", value)
		}},
		{name: "missing required mission", want: "single mission .pbo", mutate: func(payload *interactionPayload) {
			payload.Data.Components[3].Component.Values = nil
		}},
		{name: "more than one preset", want: "at most one Arma Launcher", mutate: func(payload *interactionPayload) {
			payload.Data.Components[4].Component.Values = []string{"attachment-preset", "attachment-preset-2"}
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			payload := decode(t)
			test.mutate(&payload)
			_, err := parseCreateModalSubmission(payload, testActorForInteraction("owner-1"), "correlation-limits", "discord:limits", testNow)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parse error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestHandlerRejectsInvalidCreationModalBeforePersisting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		displayName     string
		missionFilename string
		want            string
	}{
		{name: "empty name", displayName: "   ", missionFilename: "mission.pbo", want: "1 to 100 characters"},
		{name: "invalid mission", displayName: "Saturday Arma", missionFilename: "mission.txt", want: ".pbo file"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			handler, repository, queue, privateKey := newTestHandlerWithArtifactQueue(
				t, []string{"correlation-invalid"}, nil,
			)
			response := executeSignedRequest(t, handler, privateKey, createModalSubmissionBody(
				"interaction-invalid", test.displayName, []string{createFeatureModded}, false, test.missionFilename,
			), testNow)
			var decoded interactionResponse
			decodeResponse(t, response, &decoded)
			if decoded.Data == nil || !strings.Contains(decoded.Data.Content, test.want) {
				t.Fatalf("response = %#v; want %q", decoded.Data, test.want)
			}
			sessions, err := repository.ListByOwner(context.Background(), "owner-1", 10)
			if err != nil || len(sessions) != 0 || len(queue.Requests()) != 0 {
				t.Fatalf("invalid submission persisted sessions=%#v requests=%#v err=%v", sessions, queue.Requests(), err)
			}
		})
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

func TestHandlerExecutesAuthorizedRevisionBoundCardControls(t *testing.T) {
	t.Parallel()
	handler, repository, _, notifications, privateKey := newTestHandlerWithQueues(
		t, []string{"correlation-refresh-1", "correlation-refresh-2"}, nil,
	)
	session := seedCardControlSession(t, repository)
	token := sessioncard.ControlToken(session.ID)
	click := func(action string, revision uint64) interactionResponse {
		t.Helper()
		customID, err := componentid.New(action, revision, token)
		if err != nil {
			t.Fatal(err)
		}
		response := executeSignedRequest(t, handler, privateKey, cardControlBody(action, customID), testNow)
		var decoded interactionResponse
		decodeResponse(t, response, &decoded)
		if response.Code != http.StatusOK || decoded.Type != interactionResponseChannelMessageWithSource ||
			decoded.Data == nil || decoded.Data.Flags != messageFlagEphemeral || decoded.Data.AllowedMentions == nil ||
			len(decoded.Data.AllowedMentions.Parse) != 0 {
			t.Fatalf("%s response = %#v", action, decoded)
		}
		return decoded
	}

	details := click(componentid.ActionViewDetails, uint64(session.Version))
	if !strings.Contains(details.Data.Content, "Slug: `card-controls`") || strings.Contains(details.Data.Content, session.ID) {
		t.Fatalf("details = %q", details.Data.Content)
	}
	players := click(componentid.ActionShowPlayers, uint64(session.Version))
	if !strings.Contains(players.Data.Content, "Players: Card Controls") || !strings.Contains(players.Data.Content, "unavailable") || strings.Contains(players.Data.Content, "Slug:") {
		t.Fatalf("players = %q", players.Data.Content)
	}
	help := click(componentid.ActionHelp, uint64(session.Version))
	if !strings.Contains(help.Data.Content, "Card controls are read-only") || !strings.Contains(help.Data.Content, "/rb setup") {
		t.Fatalf("help = %q", help.Data.Content)
	}
	download := click(componentid.ActionDownload, uint64(session.Version))
	if !strings.Contains(download.Data.Content, "https://discord.com/channels/guild-1/channel-1/modlist-message-1") ||
		!strings.Contains(download.Data.Content, "card-controls-modlist.html") {
		t.Fatalf("download = %q", download.Data.Content)
	}
	refresh := click(componentid.ActionRefresh, uint64(session.Version-1))
	if !strings.Contains(refresh.Data.Content, "latest persisted revision was queued") {
		t.Fatalf("refresh = %q", refresh.Data.Content)
	}
	requests := notifications.Requests()
	if len(requests) != 1 || requests[0].Kind != domain.NotificationSessionCard ||
		requests[0].CardRevision != session.Version || !strings.HasPrefix(requests[0].NotificationID, "card-refresh-") {
		t.Fatalf("refresh notifications = %#v", requests)
	}
	currentRefresh := click(componentid.ActionRefresh, uint64(session.Version))
	if !strings.Contains(currentRefresh.Data.Content, "bounded live-player check") || len(notifications.Requests()) != 1 {
		t.Fatalf("bounded refresh = %q notifications=%#v", currentRefresh.Data.Content, notifications.Requests())
	}

	stale := click(componentid.ActionHelp, uint64(session.Version-1))
	if !strings.Contains(stale.Data.Content, "card changed") || len(notifications.Requests()) != 1 {
		t.Fatalf("stale help = %q notifications=%#v", stale.Data.Content, notifications.Requests())
	}
}

func TestHandlerRateLimitsChangedLiveRefreshWithinWindow(t *testing.T) {
	t.Parallel()
	handler, repository, _, notifications, privateKey := newTestHandlerWithQueues(
		t, []string{"correlation-live-refresh-1", "correlation-live-refresh-2"}, nil,
	)
	seedRunningHandlerSession(t, repository)
	session, err := repository.Get(context.Background(), "running-session")
	if err != nil {
		t.Fatal(err)
	}
	handler.playerQuery = &sequencePlayerQuery{statuses: []domain.PlayerStatus{
		{PlayerCount: 1, MaxPlayers: 32}, {PlayerCount: 2, MaxPlayers: 32},
	}}
	customID, err := componentid.New(componentid.ActionRefresh, uint64(session.Version), sessioncard.ControlToken(session.ID))
	if err != nil {
		t.Fatal(err)
	}
	body := cardControlBody(componentid.ActionRefresh, customID)
	first := executeSignedRequest(t, handler, privateKey, body, testNow)
	second := executeSignedRequest(t, handler, privateKey, body, testNow)
	var firstResponse, secondResponse interactionResponse
	decodeResponse(t, first, &firstResponse)
	decodeResponse(t, second, &secondResponse)
	if firstResponse.Data == nil || !strings.Contains(firstResponse.Data.Content, "refresh queued") ||
		secondResponse.Data == nil || !strings.Contains(secondResponse.Data.Content, "already queued") {
		t.Fatalf("refresh responses first=%#v second=%#v", firstResponse, secondResponse)
	}
	requests := notifications.Requests()
	if len(requests) != 1 || !strings.Contains(requests[0].Content, "**Players:** `1/32`") {
		t.Fatalf("rate-limited refresh requests = %#v", requests)
	}
}

func TestHandlerRejectsCardControlFromWrongChannel(t *testing.T) {
	t.Parallel()
	handler, repository, privateKey := newTestHandler(t, nil, nil)
	session := seedCardControlSession(t, repository)
	token := sessioncard.ControlToken(session.ID)
	customID, err := componentid.New(componentid.ActionViewDetails, uint64(session.Version), token)
	if err != nil {
		t.Fatal(err)
	}
	body := cardControlBody(componentid.ActionViewDetails, customID)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	payload["channel_id"] = "channel-other"
	response := executeSignedRequest(t, handler, privateKey, marshalPayload(payload), testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "not authorized") {
		t.Fatalf("wrong-channel response = %#v", decoded)
	}
}

func TestHandlerOpensCreateModalWithoutPersistingSession(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(t, nil, nil)
	body := createCommandBody("interaction-create-1", "owner-1", "guild-1", "channel-1")

	response := executeSignedRequest(t, handler, privateKey, body, testNow)
	if response.Code != http.StatusOK {
		t.Fatalf("response status = %d; body = %s", response.Code, response.Body.String())
	}
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Type != interactionResponseModal || decoded.Data == nil || decoded.Data.CustomID != createModalCustomID ||
		decoded.Data.Title != "Create Arma 3 session" || decoded.Data.Components == nil || len(*decoded.Data.Components) != 5 {
		t.Fatalf("create modal response = %#v", decoded)
	}
	components := *decoded.Data.Components
	wantTypes := []int{componentTypeTextInput, componentTypeTextInput, componentTypeCheckboxGroup, componentTypeFileUpload, componentTypeFileUpload}
	for index, component := range components {
		if component.Type != componentTypeLabel || component.Component == nil || component.Component.Type != wantTypes[index] {
			t.Fatalf("modal component %d = %#v; want label wrapping type %d", index, component, wantTypes[index])
		}
	}
	name, description := components[0].Component, components[1].Component
	if name.CustomID != createNameCustomID || name.Required == nil || !*name.Required ||
		name.MinLength == nil || *name.MinLength != 1 || name.MaxLength == nil || *name.MaxLength != 100 {
		t.Fatalf("name input = %#v", name)
	}
	if description.CustomID != createDescriptionCustomID || description.Required == nil || *description.Required ||
		description.MaxLength == nil || *description.MaxLength != 64 {
		t.Fatalf("description input = %#v", description)
	}
	features := components[2].Component
	if features.CustomID != createFeaturesCustomID || features.MinValues == nil || *features.MinValues != 0 ||
		features.MaxValues == nil || *features.MaxValues != 2 || len(features.Options) != 2 ||
		features.Options[0].Value != createFeatureModded || !features.Options[0].Default ||
		features.Options[1].Value != createFeatureTeamSpeak || features.Options[1].Default {
		t.Fatalf("feature defaults = %#v; want modded on and TeamSpeak off", features.Options)
	}
	mission, preset := components[3].Component, components[4].Component
	if mission.CustomID != createMissionCustomID || mission.Required == nil || !*mission.Required ||
		mission.MinValues == nil || *mission.MinValues != 1 || mission.MaxValues == nil || *mission.MaxValues != 1 ||
		preset.CustomID != createPresetCustomID || preset.Required == nil || *preset.Required ||
		preset.MinValues == nil || *preset.MinValues != 0 || preset.MaxValues == nil || *preset.MaxValues != 1 {
		t.Fatalf("file requirements mission=%#v preset=%#v", mission.Required, preset.Required)
	}

	sessions, err := repository.ListByOwner(context.Background(), "owner-1", 10)
	if err != nil {
		t.Fatalf("ListByOwner() returned error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("session count = %d; want 0", len(sessions))
	}
}

func TestSetupModalProtectsLegacyObjectBackedArtifacts(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"mission", "preset"} {
		description := setupArtifactDescription(kind, "", "sessions/legacy/input/artifact", false)
		if !strings.Contains(description, "cannot be replaced") {
			t.Fatalf("%s legacy artifact description = %q", kind, description)
		}
	}
}

func TestHandlerPreflightsCardPermissionsAndUsesPlainTextFallback(t *testing.T) {
	t.Parallel()

	t.Run("missing send blocks create before persistence", func(t *testing.T) {
		handler, repository, privateKey := newTestHandler(t, nil, nil)
		body := withAppPermissions(createCommandBody("interaction-no-send", "owner-1", "guild-1", "channel-1"), viewChannelPermission)
		response := executeSignedRequest(t, handler, privateKey, body, testNow)
		var decoded interactionResponse
		decodeResponse(t, response, &decoded)
		if decoded.Type != interactionResponseChannelMessageWithSource || decoded.Data == nil ||
			!strings.Contains(decoded.Data.Content, "Send Messages") || !strings.Contains(decoded.Data.Content, "/rb create") {
			t.Fatalf("permission response = %#v", decoded)
		}
		sessions, err := repository.ListByOwner(context.Background(), "owner-1", 10)
		if err != nil || len(sessions) != 0 {
			t.Fatalf("preflight persisted sessions=%#v err=%v", sessions, err)
		}
	})

	t.Run("permission loss blocks modal submission before persistence", func(t *testing.T) {
		handler, repository, _, _, privateKey := newTestHandlerWithQueues(t, []string{"correlation-drift"}, nil)
		body := withAppPermissions(createModalSubmissionBody(
			"interaction-drift", "Permission Drift", nil, false, "mission.pbo",
		), viewChannelPermission)
		response := executeSignedRequest(t, handler, privateKey, body, testNow)
		var decoded interactionResponse
		decodeResponse(t, response, &decoded)
		if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "Send Messages") {
			t.Fatalf("permission drift response = %#v", decoded)
		}
		sessions, err := repository.ListByOwner(context.Background(), "owner-1", 10)
		if err != nil || len(sessions) != 0 {
			t.Fatalf("permission drift persisted sessions=%#v err=%v", sessions, err)
		}
	})

	t.Run("missing edit blocks setup before opening modal", func(t *testing.T) {
		handler, repository, _, _, privateKey := newTestHandlerWithQueues(t, nil, nil)
		seedSetupDraft(t, repository, domain.ArtifactAccepted, domain.ArtifactRejected)
		body := withAppPermissions(setupCommandBody("interaction-no-edit", "session-setup"), 0)
		response := executeSignedRequest(t, handler, privateKey, body, testNow)
		var decoded interactionResponse
		decodeResponse(t, response, &decoded)
		if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "View Channel") || !strings.Contains(decoded.Data.Content, "/rb setup") {
			t.Fatalf("edit preflight response = %#v", decoded)
		}
	})

	t.Run("missing rich capabilities keeps content card", func(t *testing.T) {
		handler, _, _, cards, privateKey := newTestHandlerWithQueues(
			t, []string{"correlation-plain"}, []string{"session-plain", "event-created", "event-configured", "event-artifacts"},
		)
		body := withAppPermissions(createModalSubmissionBody(
			"interaction-plain", "Plain Card", nil, false, "mission.pbo",
		), viewChannelPermission|sendMessagesPermission)
		response := executeSignedRequest(t, handler, privateKey, body, testNow)
		var decoded interactionResponse
		decodeResponse(t, response, &decoded)
		if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "plain-text form") {
			t.Fatalf("fallback response = %#v", decoded)
		}
		requests := cards.Requests()
		if len(requests) != 1 || requests[0].Content == "" || requests[0].Kind != domain.NotificationSessionCard {
			t.Fatalf("fallback card requests = %#v", requests)
		}
	})
}

func TestHandlerSetupModalPrefillsDraftAndQueuesOnlyRejectedReplacement(t *testing.T) {
	t.Parallel()

	handler, repository, queue, cards, privateKey := newTestHandlerWithQueues(
		t, []string{"correlation-setup-open", "correlation-setup-submit", "correlation-setup-replay"},
		[]string{"event-setup-configured", "event-setup-replacement"},
	)
	seedSetupDraft(t, repository, domain.ArtifactAccepted, domain.ArtifactRejected)

	openResponse := executeSignedRequest(t, handler, privateKey, setupCommandBody("interaction-setup-open", "session-setup"), testNow)
	var modal interactionResponse
	decodeResponse(t, openResponse, &modal)
	if modal.Type != interactionResponseModal || modal.Data == nil || modal.Data.CustomID != setupModalCustomIDPrefix+"session-setup" ||
		modal.Data.Components == nil || len(*modal.Data.Components) != 5 {
		t.Fatalf("setup modal = %#v", modal)
	}
	components := *modal.Data.Components
	if components[0].Component == nil || components[0].Component.Value != "Original Setup" ||
		components[1].Component == nil || components[1].Component.Value != "Original description" ||
		components[3].Component == nil || components[3].Component.Required == nil || *components[3].Component.Required {
		t.Fatalf("prefilled setup components = %#v", components)
	}

	submitResponse := executeSignedRequest(t, handler, privateKey, setupModalSubmissionBody(
		"interaction-setup-submit", "session-setup", "Renamed Setup", []string{createFeatureModded, createFeatureTeamSpeak}, false, true,
	), testNow)
	var submitted interactionResponse
	decodeResponse(t, submitResponse, &submitted)
	if submitted.Data == nil || !strings.Contains(submitted.Data.Content, "Draft setup updated") ||
		!strings.Contains(submitted.Data.Content, "Replacement preset queued") ||
		!strings.Contains(submitted.Data.Content, "have not been accepted yet") {
		t.Fatalf("setup submission = %#v body=%s", submitted, submitResponse.Body.String())
	}
	replayResponse := executeSignedRequest(t, handler, privateKey, setupModalSubmissionBody(
		"interaction-setup-submit", "session-setup", "Renamed Setup", []string{createFeatureModded, createFeatureTeamSpeak}, false, true,
	), testNow)
	var replayed interactionResponse
	decodeResponse(t, replayResponse, &replayed)
	if replayed.Data == nil || !strings.Contains(replayed.Data.Content, "Draft setup updated") {
		t.Fatalf("setup replay = %#v body=%s", replayed, replayResponse.Body.String())
	}
	stored, err := repository.Get(context.Background(), "session-setup")
	if err != nil {
		t.Fatal(err)
	}
	if stored.DisplayName != "Renamed Setup" || stored.Description != "Updated description" || stored.Slug != "original-setup" ||
		!stored.TeamSpeakEnabled || stored.Vanilla || stored.MissionArtifactStatus != domain.ArtifactAccepted ||
		stored.PresetArtifactStatus != domain.ArtifactPending {
		t.Fatalf("updated setup = %#v", stored)
	}
	requests := queue.Requests()
	if len(requests) != 1 || requests[0].Kind != domain.ArtifactPreset {
		t.Fatalf("replacement requests = %#v; want preset only", requests)
	}
	if cardRequests := cards.Requests(); len(cardRequests) != 1 || cardRequests[0].SessionID != stored.ID ||
		cardRequests[0].CardRevision != stored.Version || !strings.Contains(cardRequests[0].Content, "Renamed Setup") {
		t.Fatalf("setup card refreshes = %#v", cardRequests)
	}
}

func TestHandlerSetupRejectsReplacementOfAcceptedArtifactBeforeMutation(t *testing.T) {
	t.Parallel()
	handler, repository, queue, _, privateKey := newTestHandlerWithQueues(t, []string{"correlation-setup"}, nil)
	seedSetupDraft(t, repository, domain.ArtifactAccepted, domain.ArtifactRejected)
	response := executeSignedRequest(t, handler, privateKey, setupModalSubmissionBody(
		"interaction-setup", "session-setup", "Changed Name", []string{createFeatureModded}, true, false,
	), testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "mission is already accepted or validating") {
		t.Fatalf("accepted replacement response = %#v body=%s", decoded, response.Body.String())
	}
	stored, err := repository.Get(context.Background(), "session-setup")
	if err != nil || stored.DisplayName != "Original Setup" || len(queue.Requests()) != 0 {
		t.Fatalf("rejected setup mutated session=%#v requests=%#v err=%v", stored, queue.Requests(), err)
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
		[]string{"correlation-admin-command", "correlation-admin-access", "correlation-admin-select", "correlation-admin-clear", "correlation-admin-clear-confirm", "correlation-admin-clear-replay"},
		nil,
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
		len(*menu.Data.Components) != 1 || (*menu.Data.Components)[0].Components[0].CustomID != adminMenuCustomID {
		t.Fatalf("admin menu response = %#v", menu)
	}
	accessResponse := executeSignedRequest(t, handler, privateKey, adminComponentBody(
		"interaction-admin-access", "guild-manager", "32", adminMenuCustomID, componentTypeStringSelect, []string{adminMenuAccess},
	), testNow)
	var access interactionResponse
	decodeResponse(t, accessResponse, &access)
	if access.Type != interactionResponseUpdateMessage || access.Data == nil || access.Data.Components == nil ||
		len(*access.Data.Components) != 3 || (*access.Data.Components)[1].Components[0].Type != componentTypeRoleSelect ||
		len((*access.Data.Components)[1].Components[0].DefaultValues) != 1 {
		t.Fatalf("admin access response = %#v", access)
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
		selection.Data.Components == nil || len(*selection.Data.Components) != 3 ||
		(*selection.Data.Components)[0].Components[0].CustomID != adminMenuCustomID {
		t.Fatalf("admin selection response = %#v", selection)
	}

	createResponse := executeSignedRequest(
		t, handler, privateKey,
		createCommandBody("interaction-create-after-access", "owner-1", "guild-1", "different-channel"),
		testNow,
	)
	var created interactionResponse
	decodeResponse(t, createResponse, &created)
	if created.Type != interactionResponseModal || created.Data == nil || created.Data.CustomID != createModalCustomID {
		t.Fatalf("command after role configuration = %#v", created)
	}

	promptResponse := executeSignedRequest(t, handler, privateKey, adminComponentBody(
		"interaction-clear-prompt", "guild-manager", "32", adminRoleClearPromptCustomID, componentTypeButton, nil,
	), testNow)
	var prompt interactionResponse
	decodeResponse(t, promptResponse, &prompt)
	if prompt.Type != interactionResponseUpdateMessage || prompt.Data == nil ||
		!strings.Contains(prompt.Data.Content, "Remove all normal role access?") || prompt.Data.Components == nil || len(*prompt.Data.Components) != 2 {
		t.Fatalf("clear prompt = %#v", prompt)
	}
	confirmCustomID := (*prompt.Data.Components)[1].Components[0].CustomID
	if !strings.HasPrefix(confirmCustomID, adminRoleClearConfirmCustomID+":") {
		t.Fatalf("revision-bound clear confirmation ID = %q", confirmCustomID)
	}
	confirmResponse := executeSignedRequest(t, handler, privateKey, adminComponentBody(
		"interaction-clear-confirm", "guild-manager", "32", confirmCustomID, componentTypeButton, nil,
	), testNow)
	var confirmed interactionResponse
	decodeResponse(t, confirmResponse, &confirmed)
	if confirmed.Type != interactionResponseUpdateMessage || confirmed.Data == nil ||
		!strings.Contains(confirmed.Data.Content, "All normal role access was removed") ||
		!strings.Contains(confirmed.Data.Content, "normal member access is disabled") || confirmed.Data.Components == nil || len(*confirmed.Data.Components) != 2 {
		t.Fatalf("clear confirmation = %#v", confirmed)
	}
	replayResponse := executeSignedRequest(t, handler, privateKey, adminComponentBody(
		"interaction-clear-replay", "guild-manager", "32", confirmCustomID, componentTypeButton, nil,
	), testNow)
	var replayed interactionResponse
	decodeResponse(t, replayResponse, &replayed)
	if replayed.Data == nil || !strings.Contains(replayed.Data.Content, "stale") || !strings.Contains(replayed.Data.Content, "/rb admin") {
		t.Fatalf("replayed clear confirmation = %#v", replayed)
	}
	deniedAfterClear := executeSignedRequest(t, handler, privateKey,
		createCommandBody("interaction-create-after-clear", "owner-1", "guild-1", "different-channel"), testNow)
	var denied interactionResponse
	decodeResponse(t, deniedAfterClear, &denied)
	if denied.Data == nil || !strings.Contains(denied.Data.Content, "not authorized") {
		t.Fatalf("normal access after clear = %#v", denied)
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

func TestHandlerGuildManagerQueuesIdempotentCurrentCardRepair(t *testing.T) {
	t.Parallel()
	handler, repository, _, notifications, privateKey := newTestHandlerWithQueues(
		t, []string{"correlation-repair-1", "correlation-repair-2"}, nil,
	)
	seedAutocompleteSession(t, repository, "session-repair", "Repair Me", "repair-me", "owner-2", "guild-1")
	body := adminComponentBody("interaction-repair", "manager-1", "32", adminRepairSelectCustomID, componentTypeStringSelect, []string{"session-repair"})
	for attempt := 1; attempt <= 2; attempt++ {
		response := executeSignedRequest(t, handler, privateKey, body, testNow)
		var decoded interactionResponse
		decodeResponse(t, response, &decoded)
		if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "repair queued") ||
			!strings.Contains(decoded.Data.Content, "original channel") {
			t.Fatalf("repair attempt %d response = %#v", attempt, decoded)
		}
	}
	requests := notifications.Requests()
	if len(requests) != 1 || requests[0].NotificationID != "card-admin-repair-interaction-repair" ||
		requests[0].SessionID != "session-repair" || requests[0].ChannelID != "channel-1" ||
		requests[0].CardRevision != 1 || strings.Contains(requests[0].Content, "session-repair") {
		t.Fatalf("repair notifications = %#v", requests)
	}

	denied := executeSignedRequest(t, handler, privateKey, adminComponentBody("interaction-repair-denied", "member-1", "0", adminRepairSelectCustomID, componentTypeStringSelect, []string{"session-repair"}), testNow)
	var deniedResponse interactionResponse
	decodeResponse(t, denied, &deniedResponse)
	if deniedResponse.Data == nil || !strings.Contains(deniedResponse.Data.Content, "Manage Server") || len(notifications.Requests()) != 1 {
		t.Fatalf("denied repair response = %#v notifications=%#v", deniedResponse, notifications.Requests())
	}
}

func TestHandlerListsAndShowsSessionStatus(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(t, []string{"correlation-list", "correlation-status"}, nil)
	seedAutocompleteSession(t, repository, "session-1", "Saturday Arma", "saturday-arma", "owner-1", "guild-1")

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

func TestHandlerHelpProvidesFirstRunOverviewAndGuildScopedNextAction(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(t, []string{"correlation-help-first", "correlation-help-overview", "correlation-help-session"}, nil)
	firstResponse := executeSignedRequest(t, handler, privateKey,
		commandBody("interaction-help-first", "owner-1", "guild-1", "channel-1", "help", nil), testNow)
	var first interactionResponse
	decodeResponse(t, firstResponse, &first)
	if first.Data == nil || first.Data.Flags&messageFlagEphemeral == 0 ||
		!strings.Contains(first.Data.Content, "Getting started") || !strings.Contains(first.Data.Content, "non-billable") ||
		!strings.Contains(first.Data.Content, "/rb start") || strings.Contains(first.Data.Content, "automatic retry") {
		t.Fatalf("first-run help = %#v", first.Data)
	}

	seedHandlerSessionState(t, repository, "opaque-help-session", "Help Session", "help-session", "owner-1", "guild-1", domain.StateNew)
	overviewResponse := executeSignedRequest(t, handler, privateKey,
		commandBody("interaction-help-overview", "owner-1", "guild-1", "channel-1", "help", nil), testNow)
	var overview interactionResponse
	decodeResponse(t, overviewResponse, &overview)
	if overview.Data == nil || !strings.Contains(overview.Data.Content, "Platform help") || !strings.Contains(overview.Data.Content, "runbook") {
		t.Fatalf("overview help = %#v", overview.Data)
	}

	selectedResponse := executeSignedRequest(t, handler, privateKey,
		commandBody("interaction-help-session", "member-1", "guild-1", "channel-1", "help", []any{map[string]any{
			"type": applicationCommandOptionString, "name": "session", "value": "opaque-help-session",
		}}), testNow)
	var selected interactionResponse
	decodeResponse(t, selectedResponse, &selected)
	if selected.Data == nil || !strings.Contains(selected.Data.Content, "Help Session") || !strings.Contains(selected.Data.Content, "/rb start") ||
		strings.Contains(selected.Data.Content, "opaque-help-session") || !strings.Contains(selected.Data.Content, "no command shown here is queued automatically") {
		t.Fatalf("selected help = %#v", selected.Data)
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

func TestHandlerArchiveCreatesThenConsumesDurableConfirmation(t *testing.T) {
	t.Parallel()
	handler, repository, privateKey := newTestHandler(t, []string{"correlation-archive", "correlation-confirm"}, nil)
	seedRunningHandlerSession(t, repository)
	body := marshalPayload(map[string]any{
		"id": "interaction-archive", "application_id": "app-1", "type": interactionTypeApplicationCommand,
		"guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{"role-1"}},
		"data": map[string]any{"name": "rb", "options": []any{map[string]any{
			"type": applicationCommandOptionSubcommand, "name": "archive",
			"options": []any{
				map[string]any{"type": applicationCommandOptionString, "name": "session", "value": "running-session"},
			},
		}}},
	})
	response := executeSignedRequest(t, handler, privateKey, body, testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	code := domain.ConfirmationCode("interaction-archive")
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "No destructive work has been queued") || !strings.Contains(decoded.Data.Content, code) {
		t.Fatalf("response = %#v", decoded.Data)
	}
	confirmation, err := repository.GetConfirmation(context.Background(), code)
	if err != nil || confirmation.Status != domain.ConfirmationPending {
		t.Fatalf("confirmation = %#v, err %v", confirmation, err)
	}

	confirmBody := commandBody("interaction-confirm", "owner-1", "guild-1", "channel-1", "confirm", []any{
		map[string]any{"type": applicationCommandOptionString, "name": "code", "value": code},
	})
	response = executeSignedRequest(t, handler, privateKey, confirmBody, testNow)
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "Archive request accepted") || !strings.Contains(decoded.Data.Content, "cannot be replayed") {
		t.Fatalf("response = %#v", decoded.Data)
	}
	confirmation, err = repository.GetConfirmation(context.Background(), code)
	if err != nil || confirmation.Status != domain.ConfirmationConsumed {
		t.Fatalf("consumed confirmation = %#v, err %v", confirmation, err)
	}
}

func TestHandlerTerminateConfirmationCanBeCancelled(t *testing.T) {
	t.Parallel()
	handler, repository, privateKey := newTestHandler(t, []string{"correlation-terminate", "correlation-cancel"}, nil)
	seedRunningHandlerSession(t, repository)
	body := commandBody("interaction-terminate", "owner-1", "guild-1", "channel-1", "terminate", []any{
		map[string]any{"type": applicationCommandOptionString, "name": "session", "value": "running-session"},
	})
	response := executeSignedRequest(t, handler, privateKey, body, testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	code := domain.ConfirmationCode("interaction-terminate")
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "irreversible") || !strings.Contains(decoded.Data.Content, code) {
		t.Fatalf("response = %#v", decoded.Data)
	}
	cancelBody := commandBody("interaction-cancel", "owner-1", "guild-1", "channel-1", "cancel-confirmation", []any{
		map[string]any{"type": applicationCommandOptionString, "name": "code", "value": code},
	})
	response = executeSignedRequest(t, handler, privateKey, cancelBody, testNow)
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "confirmation cancelled") || !strings.Contains(decoded.Data.Content, "No destructive work was queued") {
		t.Fatalf("response = %#v", decoded.Data)
	}
}

func TestConfirmationQueueUncertaintyNeverPromisesRetry(t *testing.T) {
	t.Parallel()
	err := confirmationUserError(domain.ErrConfirmationDispatchUncertain)
	var userErr userError
	if !errors.As(err, &userErr) || !strings.Contains(userErr.message, "No automatic retry is scheduled") ||
		!strings.Contains(userErr.message, "may remain and incur cost") || strings.Contains(userErr.message, "will retry") {
		t.Fatalf("confirmation uncertainty message = %v", err)
	}
}

func TestUnknownCommandErrorIsBoundedAndWarnsWithoutPromisingRetry(t *testing.T) {
	t.Parallel()
	handler := &Handler{}
	message := handler.commandErrorMessage(errors.New("sensitive internal failure"), "safe-reference")
	if !strings.Contains(message, "Reference: `safe-reference`") ||
		!strings.Contains(message, "No automatic retry is scheduled") ||
		!strings.Contains(message, "may remain and incur cost") ||
		strings.Contains(message, "sensitive internal failure") || strings.Contains(message, "will retry") {
		t.Fatalf("unknown command message = %q", message)
	}
}

func TestHandlerConfiguresAndAcceptsMissionAttachment(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(
		t,
		[]string{"correlation-configure", "correlation-upload"},
		[]string{"event-configure"},
	)
	seedAutocompleteSession(t, repository, "session-1", "Saturday Arma", "saturday-arma", "owner-1", "guild-1")

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

func TestHandlerRejectsNonGuildContextBeforeAuthorizationOrRouting(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(t, nil, nil)
	body := marshalPayload(map[string]any{
		"id": "interaction-dm", "application_id": "app-1", "type": interactionTypeApplicationCommand,
		"channel_id": "dm-channel", "user": map[string]any{"id": "owner-1"},
		"data": map[string]any{"name": "rb", "options": []any{map[string]any{
			"type": applicationCommandOptionSubcommand, "name": "help", "options": []any{},
		}}},
	})
	response := executeSignedRequest(t, handler, privateKey, body, testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || decoded.Data.Flags&messageFlagEphemeral == 0 || !strings.Contains(decoded.Data.Content, "only in a configured Discord server") {
		t.Fatalf("non-guild response = %#v", decoded)
	}
	sessions, err := repository.ListByOwner(context.Background(), "owner-1", 10)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("sessions = %#v error=%v", sessions, err)
	}
}

func TestHandlerShowsGuildSessionStatusToApprovedNonOwner(t *testing.T) {
	t.Parallel()

	handler, repository, privateKey := newTestHandler(t, []string{"correlation-status"}, nil)
	seedAutocompleteSession(t, repository, "session-1", "Saturday Arma", "saturday-arma", "owner-1", "guild-1")

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
		t, []string{"correlation-configure"}, nil,
	)
	seedAutocompleteSession(t, repository, "session-1", "Saturday Arma", "saturday-arma", "owner-1", "guild-1")
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
	handler, repository, _, privateKey := newTestHandlerWithArtifactQueue(t, correlationIDs, serviceIDs)
	return handler, repository, privateKey
}

func newTestHandlerWithArtifactQueue(
	t *testing.T,
	correlationIDs []string,
	serviceIDs []string,
) (*Handler, *memory.SessionRepository, *memory.ArtifactQueue, ed25519.PrivateKey) {
	handler, repository, artifacts, _, privateKey := newTestHandlerWithQueues(t, correlationIDs, serviceIDs)
	return handler, repository, artifacts, privateKey
}

func newTestHandlerWithQueues(
	t *testing.T,
	correlationIDs []string,
	serviceIDs []string,
) (*Handler, *memory.SessionRepository, *memory.ArtifactQueue, *memory.NotificationQueue, ed25519.PrivateKey) {
	t.Helper()

	seed := bytes.Repeat([]byte{7}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)

	repository := memory.NewSessionRepository()
	artifactQueue := memory.NewArtifactQueue()
	notificationQueue := memory.NewNotificationQueue()
	service, err := appsession.NewService(
		repository,
		&sequenceGenerator{ids: serviceIDs},
		fixedClock{now: testNow},
		7*24*time.Hour,
		appsession.WithArtifactQueue(artifactQueue),
		appsession.WithNotificationQueue(notificationQueue),
		appsession.WithCommandQueue(discardCommandQueue{}),
		appsession.WithConfirmationRepository(repository),
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

	return handler, repository, artifactQueue, notificationQueue, privateKey
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

func seedHandlerPresetRevisionSession(t *testing.T, repository *memory.SessionRepository) {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-mods", Slug: "session-mods", DisplayName: "Session Mods", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState = domain.StateRunning, domain.StateRunning, domain.StateRunning
	session.PresetObjectKey = "sessions/session-mods/input/presets/v1.html"
	session.PresetArtifactStatus = domain.ArtifactAccepted
	session.PresetRevisionSequence = 1
	session.ActivePresetRevision = domain.PresetRevision{Number: 1, PresetObjectKey: session.PresetObjectKey, Status: domain.PresetRevisionActive, StagedAt: testNow, ActivatedAt: testNow}
	event := domain.NewSessionCreatedEvent("event-session-mods", "correlation-session-mods", testActorForInteraction("owner-1"), session, testNow)
	record, err := domain.NewCompletedIdempotencyRecord("seed:session-mods", "hash-session-mods", session.ID, testNow, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, record); err != nil {
		t.Fatal(err)
	}
}

func seedCardControlSession(t *testing.T, repository *memory.SessionRepository) domain.Session {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "session-card-controls", Slug: "card-controls", DisplayName: "Card Controls", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	session.PresetArtifactStatus = domain.ArtifactAccepted
	session.PresetObjectKey = "sessions/session-card-controls/input/presets/source.html"
	if err := session.RecordMutation(testNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	event := domain.NewSessionCreatedEvent(
		"event-session-card-controls", "correlation-card-controls", testActorForInteraction("owner-1"), session, testNow,
	)
	record, err := domain.NewCompletedIdempotencyRecord(
		"seed:session-card-controls", "hash-session-card-controls", session.ID, testNow, time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, record); err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveModlistReference(context.Background(), domain.SessionModlistReference{
		SessionID: session.ID, ChannelID: session.ChannelID, MessageID: "modlist-message-1",
		ObjectKey: "sessions/session-card-controls/input/modlists/digest/card-controls-modlist.html",
		Filename:  "card-controls-modlist.html", DeliveredRevision: 1,
		DeliveredNotificationID: "modlist-card-controls", ContentSHA256: strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	return session
}

func cardControlBody(action string, customID string) []byte {
	return marshalPayload(map[string]any{
		"id": "component-" + action, "application_id": "app-1", "type": interactionTypeMessageComponent,
		"guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "member-2"}, "roles": []string{"role-1"}},
		"data":   map[string]any{"custom_id": customID, "component_type": componentTypeButton},
	})
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
				},
			},
		},
	})
}

func withAppPermissions(body []byte, permissions uint64) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		panic(err)
	}
	payload["app_permissions"] = strconv.FormatUint(permissions, 10)
	return marshalPayload(payload)
}

func setupCommandBody(interactionID, sessionID string) []byte {
	return commandBody(interactionID, "owner-1", "guild-1", "channel-1", "setup", []any{
		map[string]any{"type": applicationCommandOptionString, "name": "session", "value": sessionID},
	})
}

func setupModalSubmissionBody(interactionID, sessionID, displayName string, features []string, includeMission, includePreset bool) []byte {
	attachments := map[string]any{}
	missionValues, presetValues := []string{}, []string{}
	if includeMission {
		missionValues = []string{"attachment-mission"}
		attachments["attachment-mission"] = map[string]any{
			"id": "attachment-mission", "filename": "replacement.pbo", "size": 1024,
			"url": "https://cdn.discordapp.com/attachments/1/2/replacement.pbo",
		}
	}
	if includePreset {
		presetValues = []string{"attachment-preset"}
		attachments["attachment-preset"] = map[string]any{
			"id": "attachment-preset", "filename": "replacement.html", "size": 2048,
			"url": "https://cdn.discordapp.com/attachments/1/2/replacement.html",
		}
	}
	label := func(component map[string]any) map[string]any {
		return map[string]any{"type": componentTypeLabel, "component": component}
	}
	return marshalPayload(map[string]any{
		"id": interactionID, "application_id": "app-1", "type": interactionTypeModalSubmit,
		"guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{"role-1"}},
		"data": map[string]any{
			"custom_id": setupModalCustomIDPrefix + sessionID,
			"components": []any{
				label(map[string]any{"type": componentTypeTextInput, "custom_id": createNameCustomID, "value": displayName}),
				label(map[string]any{"type": componentTypeTextInput, "custom_id": createDescriptionCustomID, "value": " Updated   description "}),
				label(map[string]any{"type": componentTypeCheckboxGroup, "custom_id": createFeaturesCustomID, "values": features}),
				label(map[string]any{"type": componentTypeFileUpload, "custom_id": createMissionCustomID, "values": missionValues}),
				label(map[string]any{"type": componentTypeFileUpload, "custom_id": createPresetCustomID, "values": presetValues}),
			},
			"resolved": map[string]any{"attachments": attachments},
		},
	})
}

func seedSetupDraft(t *testing.T, repository *memory.SessionRepository, missionStatus, presetStatus domain.ArtifactStatus) {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "session-setup", Slug: "original-setup", DisplayName: "Original Setup", Description: "Original description",
		GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Configure(domain.SessionConfiguration{
		GameProfileID: defaultGameProfileID, SleepAfterSeconds: defaultSleepMinutes * 60,
		ArchiveAfterSeconds: defaultArchiveDays * 86400,
	}, testNow); err != nil {
		t.Fatal(err)
	}
	session.MissionArtifactStatus = missionStatus
	session.PresetArtifactStatus = presetStatus
	if missionStatus == domain.ArtifactAccepted {
		session.MissionObjectKey = "sessions/session-setup/input/mission.pbo"
	}
	if presetStatus == domain.ArtifactRejected {
		session.PresetArtifactIssue = "Preset rejected"
	}
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
	event := domain.NewSessionCreatedEvent("event-session-setup", "correlation-seed", testActorForInteraction("owner-1"), session, testNow)
	idempotency, err := domain.NewCompletedIdempotencyRecord("seed:session-setup", "hash-session-setup", session.ID, testNow, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatal(err)
	}
}

func createModalSubmissionBody(
	interactionID string,
	displayName string,
	features []string,
	includePreset bool,
	missionFilename string,
) []byte {
	attachments := map[string]any{
		"attachment-mission": map[string]any{
			"id": "attachment-mission", "filename": missionFilename, "size": 1024,
			"url": "https://cdn.discordapp.com/attachments/1/2/" + missionFilename,
		},
	}
	presetValues := []string{}
	if includePreset {
		presetValues = []string{"attachment-preset"}
		attachments["attachment-preset"] = map[string]any{
			"id": "attachment-preset", "filename": "preset.html", "size": 2048,
			"url": "https://cdn.discordapp.com/attachments/1/2/preset.html",
		}
	}
	label := func(component map[string]any) map[string]any {
		return map[string]any{"type": componentTypeLabel, "component": component}
	}
	return marshalPayload(map[string]any{
		"id": interactionID, "application_id": "app-1", "type": interactionTypeModalSubmit,
		"guild_id": "guild-1", "channel_id": "channel-1",
		"member": map[string]any{"user": map[string]any{"id": "owner-1"}, "roles": []string{"role-1"}},
		"data": map[string]any{
			"custom_id": createModalCustomID,
			"components": []any{
				label(map[string]any{"type": componentTypeTextInput, "custom_id": createNameCustomID, "value": displayName}),
				label(map[string]any{"type": componentTypeTextInput, "custom_id": createDescriptionCustomID, "value": " Weekly   co-op\n"}),
				label(map[string]any{"type": componentTypeCheckboxGroup, "custom_id": createFeaturesCustomID, "values": features}),
				label(map[string]any{"type": componentTypeFileUpload, "custom_id": createMissionCustomID, "values": []string{"attachment-mission"}}),
				label(map[string]any{"type": componentTypeFileUpload, "custom_id": createPresetCustomID, "values": presetValues}),
			},
			"resolved": map[string]any{"attachments": attachments},
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
	return rbAdminCommandBody(interactionID, ownerID, guildID, channelID, permissions)
}

func TestHandlerRBAdminMenuNavigatesOnlyImplementedPolicyAreas(t *testing.T) {
	t.Parallel()

	handler, repository, _, notifications, privateKey := newTestHandlerWithQueues(t,
		[]string{"admin-menu-id", "admin-repair-menu-id", "admin-repair-select-id"}, nil)
	seedAutocompleteSession(t, repository, "session-owned", "Owned Session", "owned-session", "owner-1", "guild-1")
	seedAutocompleteSession(t, repository, "session-other-owner", "Other Session", "other-session", "owner-2", "guild-1")
	seedAutocompleteSession(t, repository, "session-other-guild", "Elsewhere", "elsewhere", "owner-2", "guild-2")

	menuResponse := executeSignedRequest(t, handler, privateKey, adminMenuCommandBody("admin-menu", "manager-1", "32"), testNow)
	var menu interactionResponse
	decodeResponse(t, menuResponse, &menu)
	if menu.Type != interactionResponseChannelMessageWithSource || menu.Data == nil || menu.Data.Flags&messageFlagEphemeral == 0 ||
		menu.Data.Components == nil || len(*menu.Data.Components) != 1 {
		t.Fatalf("admin menu = %#v", menu)
	}
	navigation := (*menu.Data.Components)[0].Components[0]
	if navigation.CustomID != adminMenuCustomID || navigation.Type != componentTypeStringSelect || len(navigation.Options) != 2 {
		t.Fatalf("admin navigation = %#v", navigation)
	}
	values := map[string]bool{}
	for _, option := range navigation.Options {
		values[option.Value] = true
	}
	if !values[adminMenuAccess] || !values[adminMenuRepair] || values[adminMenuReset] || values[adminMenuServerConfig] || values["costs"] || values["schedule"] || values["duration"] {
		t.Fatalf("admin menu options = %#v", navigation.Options)
	}

	repairResponse := executeSignedRequest(t, handler, privateKey, adminComponentBody(
		"admin-repair-menu", "manager-1", "32", adminMenuCustomID, componentTypeStringSelect, []string{adminMenuRepair},
	), testNow)
	var repair interactionResponse
	decodeResponse(t, repairResponse, &repair)
	if repair.Type != interactionResponseUpdateMessage || repair.Data == nil || repair.Data.Components == nil || len(*repair.Data.Components) != 2 {
		t.Fatalf("repair view = %#v", repair)
	}
	selector := (*repair.Data.Components)[1].Components[0]
	if selector.CustomID != adminRepairSelectCustomID || len(selector.Options) != 2 {
		t.Fatalf("repair selector = %#v", selector)
	}
	for _, option := range selector.Options {
		if option.Value == "session-other-guild" {
			t.Fatalf("repair selector leaked another guild: %#v", selector.Options)
		}
	}

	queued := executeSignedRequest(t, handler, privateKey, adminComponentBody(
		"admin-repair-select", "manager-1", "32", adminRepairSelectCustomID, componentTypeStringSelect, []string{"session-other-owner"},
	), testNow)
	var queuedResponse interactionResponse
	decodeResponse(t, queued, &queuedResponse)
	if queuedResponse.Type != interactionResponseUpdateMessage || queuedResponse.Data == nil ||
		!strings.Contains(queuedResponse.Data.Content, "repair queued") || len(notifications.Requests()) != 1 {
		t.Fatalf("queued repair = %#v notifications=%#v", queuedResponse, notifications.Requests())
	}
}

func TestHandlerAdministratorResetFlowIsTypedReplaySafeAndFreezesSessionCommands(t *testing.T) {
	t.Parallel()
	handler, repository, privateKey := newTestHandler(t, []string{"menu-correlation", "view-correlation", "prepare-correlation", "start-correlation", "replay-correlation", "list-correlation"}, nil)
	queue := &memory.ResetQueue{}
	resetService, err := appreset.NewService(repository, queue, fixedClock{now: testNow}, "dev", true)
	if err != nil {
		t.Fatal(err)
	}
	handler.reset = resetService

	menuResponse := executeSignedRequest(t, handler, privateKey, adminMenuCommandBody("reset-menu", "admin-1", "8"), testNow)
	var menu interactionResponse
	decodeResponse(t, menuResponse, &menu)
	if menu.Data == nil || menu.Data.Components == nil || len((*menu.Data.Components)[0].Components[0].Options) != 4 {
		t.Fatalf("Administrator menu = %#v", menu)
	}

	viewResponse := executeSignedRequest(t, handler, privateKey, adminComponentBody("reset-view", "admin-1", "8", adminMenuCustomID, componentTypeStringSelect, []string{adminMenuReset}), testNow)
	var view interactionResponse
	decodeResponse(t, viewResponse, &view)
	if view.Data == nil || !strings.Contains(view.Data.Content, "Permanently removes") || !strings.Contains(view.Data.Content, "billing records") || view.Data.Components == nil {
		t.Fatalf("reset view = %#v", view)
	}

	prepareResponse := executeSignedRequest(t, handler, privateKey, adminComponentBody("reset-prepare", "admin-1", "8", adminResetPrepareCustomID, componentTypeButton, nil), testNow)
	var modal interactionResponse
	decodeResponse(t, prepareResponse, &modal)
	if modal.Type != interactionResponseModal || modal.Data == nil || !strings.HasPrefix(modal.Data.CustomID, adminResetModalPrefix) || modal.Data.Components == nil {
		t.Fatalf("reset modal = %#v", modal)
	}
	phrase := (*modal.Data.Components)[0].Components[0].Placeholder
	submitBody := marshalPayload(map[string]any{
		"id": "reset-submit", "application_id": "app-1", "type": interactionTypeModalSubmit,
		"guild_id": "guild-1", "channel_id": "channel-other",
		"member": map[string]any{"user": map[string]any{"id": "admin-1"}, "roles": []string{}, "permissions": "8"},
		"data": map[string]any{"custom_id": modal.Data.CustomID, "components": []any{map[string]any{
			"type": componentTypeActionRow, "components": []any{map[string]any{"type": componentTypeTextInput, "custom_id": adminResetPhraseCustomID, "value": phrase}},
		}}},
	})
	for attempt := 1; attempt <= 2; attempt++ {
		response := executeSignedRequest(t, handler, privateKey, submitBody, testNow)
		var submitted interactionResponse
		decodeResponse(t, response, &submitted)
		if submitted.Data == nil || !strings.Contains(submitted.Data.Content, "reset queued") || strings.Contains(submitted.Data.Content, phrase) {
			t.Fatalf("attempt %d submit = %#v", attempt, submitted)
		}
	}
	if len(queue.Requests) != 1 {
		t.Fatalf("reset queue requests = %d; want one", len(queue.Requests))
	}

	listResponse := executeSignedRequest(t, handler, privateKey, commandBody("list-while-reset", "admin-1", "guild-1", "channel-1", "list", nil), testNow)
	var list interactionResponse
	decodeResponse(t, listResponse, &list)
	if list.Data == nil || !strings.Contains(list.Data.Content, "reset is in progress") || !strings.Contains(list.Data.Content, "No session operation was queued") {
		t.Fatalf("command during reset = %#v", list)
	}
}

func TestHandlerAdministratorResetIsDisabledByDefault(t *testing.T) {
	t.Parallel()
	handler, _, privateKey := newTestHandler(t, []string{"menu-correlation", "view-correlation"}, nil)
	response := executeSignedRequest(t, handler, privateKey, adminComponentBody("reset-view", "admin-1", "8", adminMenuCustomID, componentTypeStringSelect, []string{adminMenuReset}), testNow)
	var decoded interactionResponse
	decodeResponse(t, response, &decoded)
	if decoded.Data == nil || !strings.Contains(decoded.Data.Content, "disabled in this deployment") {
		t.Fatalf("disabled reset response = %#v", decoded)
	}
}

func TestHandlerAdministratorCanUploadInspectAndRemovePrivateServerConfig(t *testing.T) {
	t.Parallel()
	handler, repository, privateKey := newTestHandler(t, []string{"view-correlation", "open-correlation", "submit-correlation", "active-correlation", "prompt-correlation", "remove-correlation", "replay-correlation"}, nil)
	queue := memory.NewArtifactQueue()
	service, err := appserverconfig.NewService(repository, queue, fixedClock{now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	handler.serverConfig = service

	viewResponse := executeSignedRequest(t, handler, privateKey, adminComponentBody("config-view", "admin-1", "8", adminMenuCustomID, componentTypeStringSelect, []string{adminMenuServerConfig}), testNow)
	var view interactionResponse
	decodeResponse(t, viewResponse, &view)
	if view.Data == nil || !strings.Contains(view.Data.Content, "generated safe default") || view.Data.Components == nil {
		t.Fatalf("default config view = %#v", view)
	}

	openResponse := executeSignedRequest(t, handler, privateKey, adminComponentBody("config-open", "admin-1", "8", adminServerConfigUploadPrefix+"0", componentTypeButton, nil), testNow)
	var modal interactionResponse
	decodeResponse(t, openResponse, &modal)
	if modal.Type != interactionResponseModal || modal.Data == nil || modal.Data.CustomID != adminServerConfigUploadPrefix+"0" || modal.Data.Components == nil {
		t.Fatalf("config modal = %#v", modal)
	}
	submitBody := marshalPayload(map[string]any{
		"id": "config-submit", "application_id": "app-1", "type": interactionTypeModalSubmit,
		"guild_id": "guild-1", "channel_id": "channel-other",
		"member": map[string]any{"user": map[string]any{"id": "admin-1"}, "roles": []string{}, "permissions": "8"},
		"data": map[string]any{
			"custom_id":  modal.Data.CustomID,
			"resolved":   map[string]any{"attachments": map[string]any{"config-attachment": map[string]any{"id": "config-attachment", "filename": "private.cfg", "content_type": "text/plain", "size": 32, "url": "https://cdn.discordapp.com/attachments/1/2/private.cfg"}}},
			"components": []any{map[string]any{"type": componentTypeLabel, "component": map[string]any{"type": componentTypeFileUpload, "custom_id": adminServerConfigFileCustomID, "values": []string{"config-attachment"}}}},
		},
	})
	submitResponse := executeSignedRequest(t, handler, privateKey, submitBody, testNow)
	var submitted interactionResponse
	decodeResponse(t, submitResponse, &submitted)
	requests := queue.Requests()
	if submitted.Data == nil || !strings.Contains(submitted.Data.Content, "queued for private validation") || strings.Contains(submitted.Data.Content, "cdn.discordapp.com") || len(requests) != 1 || !requests[0].IsServerConfig() {
		t.Fatalf("submitted=%#v requests=%#v", submitted, requests)
	}

	active := domain.GuildServerConfig{GuildID: "guild-1", Revision: 1, ObjectKey: "guilds/guild-1/server-config/revisions/000001-a/server.cfg", Filename: "private.cfg", SHA256: strings.Repeat("a", 64), SizeBytes: 32, UploadedBy: "admin-1", UpdatedAt: testNow}
	if _, err := repository.SaveGuildServerConfig(context.Background(), active, 0); err != nil {
		t.Fatal(err)
	}
	activeResponse := executeSignedRequest(t, handler, privateKey, adminComponentBody("config-active", "admin-1", "8", adminMenuCustomID, componentTypeStringSelect, []string{adminMenuServerConfig}), testNow)
	var activeView interactionResponse
	decodeResponse(t, activeResponse, &activeView)
	if activeView.Data == nil || !strings.Contains(activeView.Data.Content, "private.cfg") || strings.Contains(activeView.Data.Content, active.ObjectKey) || activeView.Data.Components == nil {
		t.Fatalf("active config view = %#v", activeView)
	}

	promptResponse := executeSignedRequest(t, handler, privateKey, adminComponentBody("config-prompt", "admin-1", "8", adminServerConfigRemovePrefix+"1", componentTypeButton, nil), testNow)
	var prompt interactionResponse
	decodeResponse(t, promptResponse, &prompt)
	if prompt.Data == nil || !strings.Contains(prompt.Data.Content, "Remove the active") || prompt.Data.Components == nil {
		t.Fatalf("remove prompt = %#v", prompt)
	}
	confirmID := adminServerConfigConfirmPrefix + "1"
	for attempt := 1; attempt <= 2; attempt++ {
		removedResponse := executeSignedRequest(t, handler, privateKey, adminComponentBody("config-remove", "admin-1", "8", confirmID, componentTypeButton, nil), testNow)
		var removed interactionResponse
		decodeResponse(t, removedResponse, &removed)
		if removed.Data == nil || !strings.Contains(removed.Data.Content, "future sessions use the generated default") {
			t.Fatalf("attempt %d removed = %#v", attempt, removed)
		}
	}
}

func TestHandlerRBAdminComponentsRecheckManageGuildAndRejectUnknownValues(t *testing.T) {
	t.Parallel()

	handler, _, privateKey := newTestHandler(t, []string{"admin-unknown-id", "admin-role-id", "admin-stale-id"}, nil)
	denied := executeSignedRequest(t, handler, privateKey, adminComponentBody(
		"admin-component-denied", "member-1", "0", adminMenuCustomID, componentTypeStringSelect, []string{adminMenuAccess},
	), testNow)
	var deniedResponse interactionResponse
	decodeResponse(t, denied, &deniedResponse)
	if deniedResponse.Data == nil || !strings.Contains(deniedResponse.Data.Content, "Manage Server") || deniedResponse.Data.Components != nil {
		t.Fatalf("denied admin component = %#v", deniedResponse)
	}

	unknown := executeSignedRequest(t, handler, privateKey, adminComponentBody(
		"admin-component-unknown", "manager-1", "32", adminMenuCustomID, componentTypeStringSelect, []string{"costs"},
	), testNow)
	var unknownResponse interactionResponse
	decodeResponse(t, unknown, &unknownResponse)
	if unknownResponse.Data == nil || !strings.Contains(unknownResponse.Data.Content, "not available") {
		t.Fatalf("unknown admin component = %#v", unknownResponse)
	}

	unverified := executeSignedRequest(t, handler, privateKey, adminComponentBody(
		"admin-role-unverified", "manager-1", "32", adminRoleSelectCustomID, componentTypeRoleSelect, []string{"forged-role"},
	), testNow)
	var unverifiedResponse interactionResponse
	decodeResponse(t, unverified, &unverifiedResponse)
	if unverifiedResponse.Data == nil || !strings.Contains(unverifiedResponse.Data.Content, "could not verify") {
		t.Fatalf("unverified role response = %#v", unverifiedResponse)
	}

	stale := executeSignedRequest(t, handler, privateKey, adminComponentBody(
		"admin-repair-stale", "manager-1", "32", adminRepairSelectCustomID, componentTypeStringSelect, []string{"missing-session"},
	), testNow)
	var staleResponse interactionResponse
	decodeResponse(t, stale, &staleResponse)
	if staleResponse.Data == nil || !strings.Contains(staleResponse.Data.Content, "control is stale") ||
		!strings.Contains(staleResponse.Data.Content, "/rb admin") || strings.Contains(staleResponse.Data.Content, "missing-session") {
		t.Fatalf("stale admin response = %#v", staleResponse)
	}
}

func rbAdminCommandBody(interactionID, ownerID, guildID, channelID, permissions string) []byte {
	return marshalPayload(map[string]any{
		"id": interactionID, "application_id": "app-1", "type": interactionTypeApplicationCommand,
		"guild_id": guildID, "channel_id": channelID,
		"member": map[string]any{"user": map[string]any{"id": ownerID}, "roles": []string{}, "permissions": permissions},
		"data": map[string]any{
			"name": "rb",
			"options": []any{map[string]any{
				"type": applicationCommandOptionSubcommand, "name": "admin",
			}},
		},
	})
}

func adminMenuCommandBody(interactionID, ownerID, permissions string) []byte {
	return rbAdminCommandBody(interactionID, ownerID, "guild-1", "channel-other", permissions)
}

func adminComponentBody(interactionID, ownerID, permissions, customID string, componentType int, values []string) []byte {
	return marshalPayload(map[string]any{
		"id": interactionID, "application_id": "app-1", "type": interactionTypeMessageComponent,
		"guild_id": "guild-1", "channel_id": "channel-other",
		"member": map[string]any{"user": map[string]any{"id": ownerID}, "roles": []string{}, "permissions": permissions},
		"data":   map[string]any{"custom_id": customID, "component_type": componentType, "values": values},
	})
}

func adminRoleSelectionBody(interactionID, ownerID, guildID, channelID string, roleIDs []string) []byte {
	resolved := map[string]any{}
	for _, roleID := range roleIDs {
		resolved[roleID] = map[string]any{"id": roleID, "name": "Allowed role"}
	}
	return marshalPayload(map[string]any{
		"id": interactionID, "application_id": "app-1", "type": interactionTypeMessageComponent,
		"guild_id": guildID, "channel_id": channelID,
		"member": map[string]any{"user": map[string]any{"id": ownerID}, "roles": []string{}, "permissions": "32"},
		"data": map[string]any{
			"custom_id": adminRoleSelectCustomID, "component_type": componentTypeRoleSelect, "values": roleIDs,
			"resolved": map[string]any{"roles": resolved},
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
