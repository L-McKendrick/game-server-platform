package interactions

import (
	"strconv"
	"strings"
	"testing"
)

func TestChannelCapabilitiesPlanCardDelivery(t *testing.T) {
	t.Parallel()

	full := viewChannelPermission | sendMessagesPermission | embedLinksPermission | attachFilesPermission
	tests := []struct {
		name          string
		value         string
		wantKnown     bool
		wantSend      bool
		wantEdit      bool
		wantPlainText bool
	}{
		{name: "unknown legacy payload", value: "", wantKnown: false},
		{name: "malformed permissions fail closed", value: "invalid", wantKnown: true},
		{name: "full presentation", value: permissionString(full), wantKnown: true, wantSend: true, wantEdit: true},
		{name: "content only", value: permissionString(viewChannelPermission | sendMessagesPermission), wantKnown: true, wantSend: true, wantEdit: true, wantPlainText: true},
		{name: "edit existing only", value: permissionString(viewChannelPermission), wantKnown: true, wantEdit: true},
		{name: "no channel access", value: "0", wantKnown: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capabilities := (interactionPayload{AppPermissions: test.value}).channelCapabilities()
			if capabilities.known != test.wantKnown || capabilities.canSend != test.wantSend || capabilities.canEdit != test.wantEdit {
				t.Fatalf("capabilities = %#v", capabilities)
			}
			if got := capabilities.plainTextNotice() != ""; got != test.wantPlainText {
				t.Fatalf("plain-text notice present = %t; want %t", got, test.wantPlainText)
			}
		})
	}
}

func TestChannelCapabilitiesReturnActionablePermissionFailures(t *testing.T) {
	t.Parallel()

	create := (interactionPayload{AppPermissions: permissionString(viewChannelPermission)}).channelCapabilities().setupBlockedMessage(false)
	if !strings.Contains(create, "Send Messages") || !strings.Contains(create, "/rb create") {
		t.Fatalf("create failure = %q", create)
	}
	setup := (interactionPayload{AppPermissions: "0"}).channelCapabilities().setupBlockedMessage(true)
	if !strings.Contains(setup, "View Channel") || !strings.Contains(setup, "/rb setup") {
		t.Fatalf("setup failure = %q", setup)
	}
	for name, capabilities := range map[string]discordChannelCapabilities{
		"components":  {known: true, canSend: true, canEdit: true, embeds: true, attachments: true},
		"embeds":      {known: true, canSend: true, canEdit: true, components: true, attachments: true},
		"attachments": {known: true, canSend: true, canEdit: true, components: true, embeds: true},
	} {
		t.Run(name, func(t *testing.T) {
			if capabilities.plainTextNotice() == "" {
				t.Fatal("missing optional capability did not select plain text")
			}
		})
	}
}

func permissionString(value uint64) string {
	return strconv.FormatUint(value, 10)
}
