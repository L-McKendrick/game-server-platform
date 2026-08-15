package interactions

import (
	"context"
	"fmt"
	"unicode/utf8"

	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const (
	maximumAutocompleteChoices    = 25
	maximumAutocompleteLabelRunes = 100
)

func (handler *Handler) sessionAutocompleteChoices(
	ctx context.Context,
	payload interactionPayload,
	actor domain.Actor,
) ([]applicationCommandChoice, error) {
	subcommand, err := payload.subcommand()
	if err != nil {
		return nil, nil
	}
	focused, found := focusedSessionOption(subcommand.Options)
	if !found {
		return nil, nil
	}

	search, err := stringOption([]applicationCommandOption{focused}, focused.Name, false)
	if err != nil {
		return nil, nil
	}
	selections, err := handler.service.Select(ctx, appsession.SelectQuery{
		Actor:   actor,
		GuildID: payload.GuildID,
		Search:  search,
		Limit:   maximumAutocompleteChoices,
	})
	if err != nil {
		return nil, fmt.Errorf("select authorized sessions: %w", err)
	}

	choices := make([]applicationCommandChoice, 0, len(selections))
	for _, selection := range selections {
		choices = append(choices, applicationCommandChoice{
			Name:  sessionSelectionLabel(selection),
			Value: selection.ID,
		})
	}
	return choices, nil
}

func focusedSessionOption(options []applicationCommandOption) (applicationCommandOption, bool) {
	for _, option := range options {
		if !option.Focused || option.Type != applicationCommandOptionString {
			continue
		}
		if option.Name == "session" || option.Name == "session-id" {
			return option, true
		}
	}
	return applicationCommandOption{}, false
}

func sessionSelectionLabel(selection appsession.Selection) string {
	suffix := " — " + normalizeSingleLine(selection.Slug) + " — " + lifecyclePresentation(selection.LifecycleState)
	available := maximumAutocompleteLabelRunes - utf8.RuneCountInString(suffix)
	name := normalizeSingleLine(selection.DisplayName)
	if utf8.RuneCountInString(name) > available {
		if available <= 1 {
			name = "…"
		} else {
			runes := []rune(name)
			name = string(runes[:available-1]) + "…"
		}
	}
	return name + suffix
}
