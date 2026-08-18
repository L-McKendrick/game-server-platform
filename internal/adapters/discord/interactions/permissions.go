package interactions

import (
	"strconv"
	"strings"
)

const (
	viewChannelPermission  = uint64(1 << 10)
	sendMessagesPermission = uint64(1 << 11)
	embedLinksPermission   = uint64(1 << 14)
	attachFilesPermission  = uint64(1 << 15)
)

// discordChannelCapabilities is derived from Discord's app_permissions value,
// which already includes channel role and member overwrites for the app. An
// omitted value remains unknown for older captured payloads; malformed values
// fail closed.
type discordChannelCapabilities struct {
	known       bool
	canSend     bool
	canEdit     bool
	components  bool
	embeds      bool
	attachments bool
}

func (payload interactionPayload) channelCapabilities() discordChannelCapabilities {
	value := strings.TrimSpace(payload.AppPermissions)
	if value == "" {
		return discordChannelCapabilities{}
	}
	permissions, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return discordChannelCapabilities{known: true}
	}
	view := permissions&viewChannelPermission != 0
	send := view && permissions&sendMessagesPermission != 0
	return discordChannelCapabilities{
		known:       true,
		canSend:     send,
		canEdit:     view,
		components:  send,
		embeds:      send && permissions&embedLinksPermission != 0,
		attachments: send && permissions&attachFilesPermission != 0,
	}
}

func (capabilities discordChannelCapabilities) setupBlockedMessage(edit bool) string {
	if !capabilities.known {
		return ""
	}
	if edit {
		if !capabilities.canEdit {
			return "I cannot edit the public session card in this channel. Ask a server administrator to restore View Channel for the bot, then run `/rb setup` again."
		}
		return ""
	}
	if !capabilities.canSend {
		return "I cannot publish the required public session card in this channel. Ask a server administrator to restore View Channel and Send Messages for the bot, then run `/rb create` again."
	}
	return ""
}

func (capabilities discordChannelCapabilities) plainTextNotice() string {
	if !capabilities.known || !capabilities.canSend || (capabilities.components && capabilities.embeds && capabilities.attachments) {
		return ""
	}
	return " The public card will use its plain-text form because one or more optional component, embed, or attachment capabilities are unavailable."
}
