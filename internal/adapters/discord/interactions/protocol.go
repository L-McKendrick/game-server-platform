package interactions

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/discord/componentid"
)

const (
	interactionTypePing                           = 1
	interactionTypeApplicationCommand             = 2
	interactionTypeMessageComponent               = 3
	interactionTypeApplicationCommandAutocomplete = 4
	interactionTypeModalSubmit                    = 5

	interactionResponsePong                             = 1
	interactionResponseChannelMessageWithSource         = 4
	interactionResponseDeferredChannelMessageWithSource = 5
	interactionResponseDeferredUpdateMessage            = 6
	interactionResponseUpdateMessage                    = 7
	interactionResponseAutocompleteResult               = 8
	interactionResponseModal                            = 9

	applicationCommandOptionSubcommand      = 1
	applicationCommandOptionSubcommandGroup = 2
	applicationCommandOptionString          = 3
	applicationCommandOptionInteger         = 4
	applicationCommandOptionBoolean         = 5
	applicationCommandOptionAttachment      = 11
	applicationCommandOptionChannel         = 7
	applicationCommandOptionRole            = 8

	messageFlagEphemeral    = 1 << 6
	messageFlagComponentsV2 = 1 << 15

	componentTypeActionRow     = 1
	componentTypeButton        = 2
	componentTypeStringSelect  = 3
	componentTypeTextInput     = 4
	componentTypeUserSelect    = 5
	componentTypeRoleSelect    = 6
	componentTypeMentionable   = 7
	componentTypeChannelSelect = 8
	componentTypeSection       = 9
	componentTypeTextDisplay   = 10
	componentTypeThumbnail     = 11
	componentTypeMediaGallery  = 12
	componentTypeFile          = 13
	componentTypeSeparator     = 14
	componentTypeContainer     = 17
	componentTypeLabel         = 18
	componentTypeFileUpload    = 19
	componentTypeRadioGroup    = 21
	componentTypeCheckboxGroup = 22
	componentTypeCheckbox      = 23

	buttonStylePrimary   = 1
	buttonStyleSecondary = 2
	buttonStyleSuccess   = 3
	buttonStyleDanger    = 4
	buttonStyleLink      = 5
	buttonStylePremium   = 6

	textInputStyleShort     = 1
	textInputStyleParagraph = 2

	separatorSpacingSmall = 1
	separatorSpacingLarge = 2

	componentCustomIDPrefix     = componentid.Prefix
	maximumComponentCustomIDLen = componentid.MaximumCustomIDLength

	adminMenuCustomID             = "rb:admin:menu:v1"
	adminRoleSelectCustomID       = "rb:admin:access:roles:v1"
	adminRoleClearPromptCustomID  = "rb:admin:access:clear:v1"
	adminRoleClearConfirmCustomID = "rb:admin:access:clear-confirm:v1"
	adminRoleClearCancelCustomID  = "rb:admin:access:clear-cancel:v1"
	adminRepairSelectCustomID     = "rb:admin:repair:session:v1"
	adminResetPrepareCustomID     = "rb:admin:reset:prepare:v1"
	adminResetModalPrefix         = "rb:admin:reset:confirm:v1:"
	adminResetPhraseCustomID      = "reset:phrase"

	adminMenuAccess = "access"
	adminMenuRepair = "repair-card"
	adminMenuReset  = "reset-platform"
)

type interactionPayload struct {
	ID             string                  `json:"id"`
	ApplicationID  string                  `json:"application_id"`
	Type           int                     `json:"type"`
	AppPermissions string                  `json:"app_permissions,omitempty"`
	Data           *applicationCommandData `json:"data,omitempty"`
	GuildID        string                  `json:"guild_id,omitempty"`
	ChannelID      string                  `json:"channel_id,omitempty"`
	Member         *interactionMember      `json:"member,omitempty"`
	User           *interactionUser        `json:"user,omitempty"`
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
	Components    []interactionComponent      `json:"components,omitempty"`
}

type applicationCommandResolved struct {
	Attachments map[string]interactionAttachment `json:"attachments,omitempty"`
	Users       map[string]json.RawMessage       `json:"users,omitempty"`
	Members     map[string]json.RawMessage       `json:"members,omitempty"`
	Roles       map[string]json.RawMessage       `json:"roles,omitempty"`
	Channels    map[string]json.RawMessage       `json:"channels,omitempty"`
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
	Focused bool                       `json:"focused,omitempty"`
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
	Content         string                      `json:"content,omitempty"`
	Flags           int                         `json:"flags,omitempty"`
	AllowedMentions *interactionAllowedMentions `json:"allowed_mentions,omitempty"`
	Components      *[]interactionComponent     `json:"components,omitempty"`
	Choices         *[]applicationCommandChoice `json:"choices,omitempty"`
	CustomID        string                      `json:"custom_id,omitempty"`
	Title           string                      `json:"title,omitempty"`
	Attachments     []interactionResponseFile   `json:"attachments,omitempty"`
}

type interactionComponent struct {
	Type          int                             `json:"type"`
	ID            int                             `json:"id,omitempty"`
	CustomID      string                          `json:"custom_id,omitempty"`
	Style         int                             `json:"style,omitempty"`
	Label         string                          `json:"label,omitempty"`
	Description   string                          `json:"description,omitempty"`
	Content       string                          `json:"content,omitempty"`
	Emoji         *interactionEmoji               `json:"emoji,omitempty"`
	URL           string                          `json:"url,omitempty"`
	SKUID         string                          `json:"sku_id,omitempty"`
	Disabled      bool                            `json:"disabled,omitempty"`
	Placeholder   string                          `json:"placeholder,omitempty"`
	MinValues     *int                            `json:"min_values,omitempty"`
	MaxValues     *int                            `json:"max_values,omitempty"`
	MinLength     *int                            `json:"min_length,omitempty"`
	MaxLength     *int                            `json:"max_length,omitempty"`
	Required      *bool                           `json:"required,omitempty"`
	Value         string                          `json:"value,omitempty"`
	Values        []string                        `json:"values,omitempty"`
	Options       []interactionSelectOption       `json:"options,omitempty"`
	ChannelTypes  []int                           `json:"channel_types,omitempty"`
	DefaultValues []interactionSelectDefaultValue `json:"default_values,omitempty"`
	Components    []interactionComponent          `json:"components,omitempty"`
	Component     *interactionComponent           `json:"component,omitempty"`
	Accessory     *interactionComponent           `json:"accessory,omitempty"`
	Media         *interactionUnfurledMediaItem   `json:"media,omitempty"`
	Items         []interactionMediaGalleryItem   `json:"items,omitempty"`
	File          *interactionUnfurledMediaItem   `json:"file,omitempty"`
	Spoiler       bool                            `json:"spoiler,omitempty"`
	Divider       *bool                           `json:"divider,omitempty"`
	Spacing       int                             `json:"spacing,omitempty"`
	AccentColor   *int                            `json:"accent_color,omitempty"`
}

type applicationCommandChoice struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type interactionResponseFile struct {
	ID          string `json:"id"`
	Filename    string `json:"filename,omitempty"`
	Description string `json:"description,omitempty"`
}

type interactionEmoji struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name,omitempty"`
	Animated bool   `json:"animated,omitempty"`
}

type interactionSelectOption struct {
	Label       string            `json:"label"`
	Value       string            `json:"value"`
	Description string            `json:"description,omitempty"`
	Emoji       *interactionEmoji `json:"emoji,omitempty"`
	Default     bool              `json:"default,omitempty"`
}

type interactionSelectDefaultValue struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type interactionUnfurledMediaItem struct {
	URL          string `json:"url"`
	ProxyURL     string `json:"proxy_url,omitempty"`
	Height       int    `json:"height,omitempty"`
	Width        int    `json:"width,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	AttachmentID string `json:"attachment_id,omitempty"`
}

type interactionMediaGalleryItem struct {
	Media       interactionUnfurledMediaItem `json:"media"`
	Description string                       `json:"description,omitempty"`
	Spoiler     bool                         `json:"spoiler,omitempty"`
}

type interactionAllowedMentions struct {
	Parse []string `json:"parse"`
}

type componentReference struct {
	Action   string
	Revision uint64
	Token    string
}

func newComponentCustomID(action string, revision uint64, token string) (string, error) {
	return componentid.New(action, revision, token)
}

func parseComponentCustomID(customID string) (componentReference, error) {
	reference, err := componentid.Parse(customID)
	if err != nil {
		return componentReference{}, err
	}
	return componentReference{Action: reference.Action, Revision: reference.Revision, Token: reference.Token}, nil
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
	return payload.namedSubcommand("rb")
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
			"rb command option %q is not a subcommand",
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
