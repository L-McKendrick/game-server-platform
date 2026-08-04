package interactions

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	interactionTypePing               = 1
	interactionTypeApplicationCommand = 2

	interactionResponsePong                     = 1
	interactionResponseChannelMessageWithSource = 4

	applicationCommandOptionSubcommand = 1
	applicationCommandOptionString     = 3

	messageFlagEphemeral = 1 << 6
)

type interactionPayload struct {
	ID            string                  `json:"id"`
	ApplicationID string                  `json:"application_id"`
	Type          int                     `json:"type"`
	Data          *applicationCommandData `json:"data,omitempty"`
	GuildID       string                  `json:"guild_id,omitempty"`
	ChannelID     string                  `json:"channel_id,omitempty"`
	Member        *interactionMember      `json:"member,omitempty"`
	User          *interactionUser        `json:"user,omitempty"`
}

type interactionMember struct {
	User  *interactionUser `json:"user,omitempty"`
	Roles []string         `json:"roles,omitempty"`
}

type interactionUser struct {
	ID string `json:"id"`
}

type applicationCommandData struct {
	Name    string                     `json:"name"`
	Options []applicationCommandOption `json:"options,omitempty"`
}

type applicationCommandOption struct {
	Type    int                        `json:"type"`
	Name    string                     `json:"name"`
	Value   json.RawMessage            `json:"value,omitempty"`
	Options []applicationCommandOption `json:"options,omitempty"`
}

type interactionResponse struct {
	Type int                      `json:"type"`
	Data *interactionResponseData `json:"data,omitempty"`
}

type interactionResponseData struct {
	Content         string                     `json:"content"`
	Flags           int                        `json:"flags,omitempty"`
	AllowedMentions interactionAllowedMentions `json:"allowed_mentions"`
}

type interactionAllowedMentions struct {
	Parse []string `json:"parse"`
}

func (payload interactionPayload) actorID() string {
	if payload.Member != nil && payload.Member.User != nil {
		return strings.TrimSpace(payload.Member.User.ID)
	}

	if payload.User != nil {
		return strings.TrimSpace(payload.User.ID)
	}

	return ""
}

func (payload interactionPayload) subcommand() (applicationCommandOption, error) {
	if payload.Data == nil {
		return applicationCommandOption{}, fmt.Errorf("application command data is required")
	}

	if strings.TrimSpace(payload.Data.Name) != "session" {
		return applicationCommandOption{}, fmt.Errorf(
			"unsupported command %q",
			payload.Data.Name,
		)
	}

	if len(payload.Data.Options) != 1 {
		return applicationCommandOption{}, fmt.Errorf(
			"session command requires exactly one subcommand",
		)
	}

	subcommand := payload.Data.Options[0]
	if subcommand.Type != applicationCommandOptionSubcommand {
		return applicationCommandOption{}, fmt.Errorf(
			"session command option %q is not a subcommand",
			subcommand.Name,
		)
	}

	return subcommand, nil
}

func stringOption(
	options []applicationCommandOption,
	name string,
	required bool,
) (string, error) {
	for _, option := range options {
		if option.Name != name {
			continue
		}

		if option.Type != applicationCommandOptionString {
			return "", fmt.Errorf("option %q must be a string", name)
		}

		var value string
		if err := json.Unmarshal(option.Value, &value); err != nil {
			return "", fmt.Errorf("decode option %q: %w", name, err)
		}

		value = strings.TrimSpace(value)
		if required && value == "" {
			return "", fmt.Errorf("option %q cannot be empty", name)
		}

		return value, nil
	}

	if required {
		return "", fmt.Errorf("option %q is required", name)
	}

	return "", nil
}
