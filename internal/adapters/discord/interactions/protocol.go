package interactions

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	interactionTypePing               = 1
	interactionTypeApplicationCommand = 2
	interactionTypeMessageComponent   = 3

	interactionResponsePong                     = 1
	interactionResponseChannelMessageWithSource = 4
	interactionResponseUpdateMessage            = 7

	applicationCommandOptionSubcommand = 1
	applicationCommandOptionString     = 3
	applicationCommandOptionInteger    = 4
	applicationCommandOptionBoolean    = 5
	applicationCommandOptionAttachment = 11
	applicationCommandOptionChannel    = 7
	applicationCommandOptionRole       = 8

	messageFlagEphemeral = 1 << 6

	componentTypeActionRow  = 1
	componentTypeRoleSelect = 6

	administratorPermission = uint64(1 << 3)
	manageGuildPermission   = uint64(1 << 5)

	adminRoleSelectCustomID = "admin:access:roles"
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
	User        *interactionUser `json:"user,omitempty"`
	Roles       []string         `json:"roles,omitempty"`
	Permissions string           `json:"permissions,omitempty"`
}

type interactionUser struct {
	ID string `json:"id"`
}

type applicationCommandData struct {
	Name          string                      `json:"name,omitempty"`
	Options       []applicationCommandOption  `json:"options,omitempty"`
	Resolved      *applicationCommandResolved `json:"resolved,omitempty"`
	CustomID      string                      `json:"custom_id,omitempty"`
	ComponentType int                         `json:"component_type,omitempty"`
	Values        []string                    `json:"values,omitempty"`
}

type applicationCommandResolved struct {
	Attachments map[string]interactionAttachment `json:"attachments,omitempty"`
}

type interactionAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	URL         string `json:"url"`
	ContentType string `json:"content_type,omitempty"`
}

type applicationCommandOption struct {
	Type    int                        `json:"type"`
	Name    string                     `json:"name"`
	Value   json.RawMessage            `json:"value,omitempty"`
	Options []applicationCommandOption `json:"options,omitempty"`
}

func integerOption(options []applicationCommandOption, name string, fallback int64) (int64, error) {
	for _, option := range options {
		if option.Name != name {
			continue
		}
		if option.Type != applicationCommandOptionInteger {
			return 0, fmt.Errorf("option %q must be an integer", name)
		}
		var value int64
		if err := json.Unmarshal(option.Value, &value); err != nil {
			return 0, fmt.Errorf("decode option %q: %w", name, err)
		}
		return value, nil
	}
	return fallback, nil
}

func booleanOption(options []applicationCommandOption, name string, fallback bool) (bool, error) {
	for _, option := range options {
		if option.Name != name {
			continue
		}
		if option.Type != applicationCommandOptionBoolean {
			return false, fmt.Errorf("option %q must be a boolean", name)
		}
		var value bool
		if err := json.Unmarshal(option.Value, &value); err != nil {
			return false, fmt.Errorf("decode option %q: %w", name, err)
		}
		return value, nil
	}
	return fallback, nil
}

func attachmentOption(data *applicationCommandData, options []applicationCommandOption, name string) (interactionAttachment, error) {
	if data == nil || data.Resolved == nil {
		return interactionAttachment{}, fmt.Errorf("resolved attachment data is required")
	}
	for _, option := range options {
		if option.Name != name {
			continue
		}
		if option.Type != applicationCommandOptionAttachment {
			return interactionAttachment{}, fmt.Errorf("option %q must be an attachment", name)
		}
		var attachmentID string
		if err := json.Unmarshal(option.Value, &attachmentID); err != nil {
			return interactionAttachment{}, fmt.Errorf("decode option %q: %w", name, err)
		}
		attachment, found := data.Resolved.Attachments[attachmentID]
		if !found {
			return interactionAttachment{}, fmt.Errorf("attachment %q was not resolved", attachmentID)
		}
		return attachment, nil
	}
	return interactionAttachment{}, fmt.Errorf("option %q is required", name)
}

type interactionResponse struct {
	Type int                      `json:"type"`
	Data *interactionResponseData `json:"data,omitempty"`
}

type interactionResponseData struct {
	Content         string                     `json:"content"`
	Flags           int                        `json:"flags,omitempty"`
	AllowedMentions interactionAllowedMentions `json:"allowed_mentions"`
	Components      *[]interactionComponent    `json:"components,omitempty"`
}

type interactionComponent struct {
	Type        int                    `json:"type"`
	CustomID    string                 `json:"custom_id,omitempty"`
	Placeholder string                 `json:"placeholder,omitempty"`
	MinValues   *int                   `json:"min_values,omitempty"`
	MaxValues   *int                   `json:"max_values,omitempty"`
	Components  []interactionComponent `json:"components,omitempty"`
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
	return payload.namedSubcommand("session")
}

func (payload interactionPayload) namedSubcommand(commandName string) (applicationCommandOption, error) {
	if payload.Data == nil {
		return applicationCommandOption{}, fmt.Errorf("application command data is required")
	}

	if strings.TrimSpace(payload.Data.Name) != commandName {
		return applicationCommandOption{}, fmt.Errorf(
			"unsupported command %q",
			payload.Data.Name,
		)
	}

	if len(payload.Data.Options) != 1 {
		return applicationCommandOption{}, fmt.Errorf(
			"%s command requires exactly one subcommand", commandName,
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

func snowflakeOption(options []applicationCommandOption, name string, optionType int) (string, error) {
	for _, option := range options {
		if option.Name != name {
			continue
		}
		if option.Type != optionType {
			return "", fmt.Errorf("option %q has the wrong type", name)
		}
		var value string
		if err := json.Unmarshal(option.Value, &value); err != nil {
			return "", err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return "", fmt.Errorf("option %q is required", name)
		}
		return value, nil
	}
	return "", fmt.Errorf("option %q is required", name)
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
