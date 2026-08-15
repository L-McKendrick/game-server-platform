package sessions

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const maximumSessionSelections = 25

// SelectQuery identifies sessions that an actor may select in one guild.
type SelectQuery struct {
	Actor   domain.Actor
	GuildID string
	Search  string
	Limit   int
}

// Selection exposes human-readable session identity while keeping the
// immutable ID available to trusted adapters as an opaque choice value.
type Selection struct {
	ID             string
	DisplayName    string
	Slug           string
	LifecycleState domain.LifecycleState
}

// Select returns owner-authorized sessions in the requested guild.
func (service *Service) Select(ctx context.Context, query SelectQuery) ([]Selection, error) {
	if err := query.Actor.Validate(); err != nil {
		return nil, fmt.Errorf("validate actor: %w", err)
	}

	guildID := strings.TrimSpace(query.GuildID)
	if guildID == "" {
		return nil, fmt.Errorf("Discord guild ID is required")
	}

	sessions, err := service.repository.ListByOwner(ctx, query.Actor.ID, 100)
	if err != nil {
		return nil, fmt.Errorf("list owner sessions: %w", err)
	}

	search := strings.ToLower(strings.TrimSpace(query.Search))
	selections := make([]Selection, 0, len(sessions))
	for _, session := range sessions {
		if session.GuildID != guildID || !sessionMatchesSelectionSearch(session, search) {
			continue
		}
		selections = append(selections, Selection{
			ID:             session.ID,
			DisplayName:    session.DisplayName,
			Slug:           session.Slug,
			LifecycleState: session.LifecycleState,
		})
	}

	sort.SliceStable(selections, func(first, second int) bool {
		firstName := strings.ToLower(selections[first].DisplayName)
		secondName := strings.ToLower(selections[second].DisplayName)
		if firstName == secondName {
			return selections[first].Slug < selections[second].Slug
		}
		return firstName < secondName
	})

	limit := query.Limit
	if limit <= 0 || limit > maximumSessionSelections {
		limit = maximumSessionSelections
	}
	if len(selections) > limit {
		selections = selections[:limit]
	}
	return selections, nil
}

func sessionMatchesSelectionSearch(session domain.Session, search string) bool {
	if search == "" {
		return true
	}
	return strings.Contains(strings.ToLower(session.DisplayName), search) ||
		strings.Contains(strings.ToLower(session.Slug), search) ||
		strings.Contains(strings.ToLower(string(session.LifecycleState)), search)
}
