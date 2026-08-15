package sessions

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const maximumSessionSelections = 25

// SelectQuery identifies sessions that an actor may select in one guild.
type SelectQuery struct {
	Actor            domain.Actor
	GuildID          string
	Search           string
	Limit            int
	AllowGuildMember bool
	IncludeDeleted   bool
}

// Selection exposes human-readable session identity while keeping the
// immutable ID available to trusted adapters as an opaque choice value.
type Selection struct {
	ID             string
	DisplayName    string
	Slug           string
	LifecycleState domain.LifecycleState
}

// ResolveQuery identifies one authorized session by an opaque selector value
// or exact slug within a guild.
type ResolveQuery struct {
	Actor            domain.Actor
	GuildID          string
	Reference        string
	CanManageGuild   bool
	AllowGuildMember bool
}

// Resolve accepts the immutable ID carried by autocomplete or an exact slug.
// Display names intentionally do not resolve because they are not unique.
func (service *Service) Resolve(ctx context.Context, query ResolveQuery) (Selection, error) {
	if err := query.Actor.Validate(); err != nil {
		return Selection{}, fmt.Errorf("validate actor: %w", err)
	}

	guildID := strings.TrimSpace(query.GuildID)
	reference := strings.TrimSpace(query.Reference)
	if guildID == "" {
		return Selection{}, fmt.Errorf("Discord guild ID is required")
	}
	if reference == "" {
		return Selection{}, fmt.Errorf("session reference is required: %w", domain.ErrNotFound)
	}
	// Autocomplete values are immutable IDs, so resolve them authoritatively
	// without depending on an eventually ordered list or bounded guild scan.
	session, err := service.repository.Get(ctx, reference)
	if err == nil && session.GuildID == guildID &&
		(query.AllowGuildMember || query.CanManageGuild || session.OwnerDiscordUserID == query.Actor.ID) {
		return selectionFromSession(session), nil
	}
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return Selection{}, fmt.Errorf("get selected session: %w", err)
	}

	sessions, err := service.selectableSessions(ctx, query.Actor, guildID, query.AllowGuildMember)
	if err != nil {
		return Selection{}, err
	}
	for _, session := range sessions {
		if session.GuildID != guildID || (session.ID != reference && session.Slug != reference) {
			continue
		}
		return selectionFromSession(session), nil
	}

	return Selection{}, domain.ErrNotFound
}

func selectionFromSession(session domain.Session) Selection {
	return Selection{
		ID:             session.ID,
		DisplayName:    session.DisplayName,
		Slug:           session.Slug,
		LifecycleState: session.LifecycleState,
	}
}

// Select returns sessions authorized for selection in the requested guild.
func (service *Service) Select(ctx context.Context, query SelectQuery) ([]Selection, error) {
	if err := query.Actor.Validate(); err != nil {
		return nil, fmt.Errorf("validate actor: %w", err)
	}

	guildID := strings.TrimSpace(query.GuildID)
	if guildID == "" {
		return nil, fmt.Errorf("Discord guild ID is required")
	}

	sessions, err := service.selectableSessions(ctx, query.Actor, guildID, query.AllowGuildMember)
	if err != nil {
		return nil, err
	}

	search := strings.ToLower(strings.TrimSpace(query.Search))
	selections := make([]Selection, 0, len(sessions))
	for _, session := range sessions {
		if session.GuildID != guildID ||
			(!query.IncludeDeleted && session.LifecycleState == domain.StateDeleted) ||
			!sessionMatchesSelectionSearch(session, search) {
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

func (service *Service) selectableSessions(
	ctx context.Context,
	actor domain.Actor,
	guildID string,
	allowGuildMember bool,
) ([]domain.Session, error) {
	if allowGuildMember {
		sessions, err := service.repository.ListByGuild(ctx, guildID, 100)
		if err != nil {
			return nil, fmt.Errorf("list guild sessions: %w", err)
		}
		return sessions, nil
	}
	sessions, err := service.repository.ListByOwner(ctx, actor.ID, 100)
	if err != nil {
		return nil, fmt.Errorf("list owner sessions: %w", err)
	}
	return sessions, nil
}

func sessionMatchesSelectionSearch(session domain.Session, search string) bool {
	if search == "" {
		return true
	}
	return strings.Contains(strings.ToLower(session.DisplayName), search) ||
		strings.Contains(strings.ToLower(session.Slug), search) ||
		strings.Contains(strings.ToLower(string(session.LifecycleState)), search)
}
