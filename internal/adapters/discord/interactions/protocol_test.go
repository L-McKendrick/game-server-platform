package interactions

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestComponentCustomIDRoundTrip(t *testing.T) {
	t.Parallel()

	customID, err := newComponentCustomID("view-details", 42, "Abcdef12_-")
	if err != nil {
		t.Fatalf("newComponentCustomID() returned error: %v", err)
	}
	if customID != "rb:v1:view-details:42:Abcdef12_-" {
		t.Fatalf("custom ID = %q", customID)
	}
	reference, err := parseComponentCustomID(customID)
	if err != nil {
		t.Fatalf("parseComponentCustomID() returned error: %v", err)
	}
	if reference.Action != "view-details" || reference.Revision != 42 || reference.Token != "Abcdef12_-" {
		t.Fatalf("component reference = %#v", reference)
	}
}

func TestComponentCustomIDRejectsUnsafeOrNonCanonicalValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		action   string
		revision uint64
		token    string
	}{
		{name: "unsafe action", action: "view:details", revision: 1, token: "Abcdef12"},
		{name: "zero revision", action: "view", revision: 0, token: "Abcdef12"},
		{name: "short token", action: "view", revision: 1, token: "short"},
		{name: "oversized ID", action: strings.Repeat("a", 32), revision: math.MaxUint64, token: strings.Repeat("A", 64)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if customID, err := newComponentCustomID(test.action, test.revision, test.token); err == nil {
				t.Fatalf("newComponentCustomID() = %q; want error", customID)
			}
		})
	}

	for _, customID := range []string{
		" rb:v1:view:1:Abcdef12",
		"rb:v2:view:1:Abcdef12",
		"rb:v1:view:01:Abcdef12",
		"rb:v1:view:1:Abcdef12:extra",
	} {
		if reference, err := parseComponentCustomID(customID); err == nil {
			t.Fatalf("parseComponentCustomID(%q) = %#v; want error", customID, reference)
		}
	}
}

func TestProtocolDecodesAutocompleteAndModalFileUpload(t *testing.T) {
	t.Parallel()

	autocompleteBody := []byte(`{
		"id":"autocomplete-1","application_id":"app-1","type":4,
		"app_permissions":"52224",
		"guild_id":"guild-1","channel_id":"channel-1",
		"data":{"name":"rb","options":[{"type":1,"name":"status","options":[
			{"type":3,"name":"session","value":"sat","focused":true}
		]}]}
	}`)
	var autocomplete interactionPayload
	if err := json.Unmarshal(autocompleteBody, &autocomplete); err != nil {
		t.Fatalf("decode autocomplete payload: %v", err)
	}
	focused := autocomplete.Data.Options[0].Options[0]
	if autocomplete.Type != interactionTypeApplicationCommandAutocomplete || autocomplete.AppPermissions != "52224" || !focused.Focused || string(focused.Value) != `"sat"` {
		t.Fatalf("autocomplete payload = %#v", autocomplete)
	}

	modalBody := []byte(`{
		"id":"modal-1","application_id":"app-1","type":5,
		"guild_id":"guild-1","channel_id":"channel-1",
		"data":{
			"custom_id":"rb:v1:create:3:Abcdef12",
			"components":[{"type":18,"id":1,"component":{
				"type":19,"id":2,"custom_id":"mission","values":["attachment-1"]
			}}],
			"resolved":{"attachments":{"attachment-1":{
				"id":"attachment-1","filename":"mission.pbo","size":1024,
				"url":"https://cdn.discordapp.com/mission.pbo"
			}}}
		}
	}`)
	var modal interactionPayload
	if err := json.Unmarshal(modalBody, &modal); err != nil {
		t.Fatalf("decode modal payload: %v", err)
	}
	fileUpload := modal.Data.Components[0].Component
	attachment := modal.Data.Resolved.Attachments["attachment-1"]
	if modal.Type != interactionTypeModalSubmit || modal.Data.CustomID == "" || fileUpload == nil ||
		fileUpload.Type != componentTypeFileUpload || len(fileUpload.Values) != 1 || attachment.Filename != "mission.pbo" {
		t.Fatalf("modal payload = %#v", modal)
	}
}

func TestProtocolEncodesComponentsV2AndEmptyAutocompleteChoices(t *testing.T) {
	t.Parallel()

	customID, err := newComponentCustomID("refresh", 7, "Refresh1_")
	if err != nil {
		t.Fatal(err)
	}
	components := []interactionComponent{{
		Type: componentTypeContainer,
		Components: []interactionComponent{
			{Type: componentTypeTextDisplay, Content: "## Session ready"},
			{Type: componentTypeActionRow, Components: []interactionComponent{{
				Type: componentTypeButton, Style: buttonStylePrimary, Label: "Refresh", CustomID: customID,
			}}},
			{Type: componentTypeActionRow, Components: []interactionComponent{{
				Type: componentTypeStringSelect, CustomID: customID + "-select", Placeholder: "Choose a session",
				Options: []interactionSelectOption{{Label: "Saturday Arma", Value: "opaque-choice"}},
			}}},
			{Type: componentTypeFile, File: &interactionUnfurledMediaItem{URL: "attachment://modlist.html"}},
		},
	}}
	response := interactionResponse{
		Type: interactionResponseChannelMessageWithSource,
		Data: &interactionResponseData{
			Flags:           messageFlagEphemeral | messageFlagComponentsV2,
			AllowedMentions: &interactionAllowedMentions{Parse: []string{}},
			Components:      &components,
		},
	}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("encode Components V2 response: %v", err)
	}
	for _, required := range []string{`"flags":32832`, `"type":17`, `"type":10`, `"type":2`, `"type":3`, `"type":13`, `"allowed_mentions":{"parse":[]}`} {
		if !strings.Contains(string(body), required) {
			t.Fatalf("Components V2 response %s does not contain %s", body, required)
		}
	}
	var wire struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode Components V2 response: %v", err)
	}
	if _, found := wire.Data["content"]; found {
		t.Fatalf("Components V2 response contains top-level legacy content: %s", body)
	}

	choices := []applicationCommandChoice{}
	autocomplete, err := json.Marshal(interactionResponse{
		Type: interactionResponseAutocompleteResult,
		Data: &interactionResponseData{Choices: &choices},
	})
	if err != nil {
		t.Fatalf("encode autocomplete response: %v", err)
	}
	if !strings.Contains(string(autocomplete), `"choices":[]`) {
		t.Fatalf("autocomplete response = %s; want explicit empty choices", autocomplete)
	}
}
