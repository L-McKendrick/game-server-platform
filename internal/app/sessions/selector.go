package sessions

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
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
	States           []domain.LifecycleState
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

// CardControlQuery resolves a one-way public-card token only within the
// authorized Discord guild. It never accepts a visible name or slug fallback.
type CardControlQuery struct {
	Actor   domain.Actor
	GuildID string
	Token   string
}

func (service *Service) ResolveCardControl(ctx context.Context, query CardControlQuery) (domain.Session, error) {
	if err := query.Actor.Validate(); err != nil {
		return domain.Session{}, fmt.Errorf("validate actor: %w", err)
	}
	guildID, token := strings.TrimSpace(query.GuildID), strings.TrimSpace(query.Token)
	if guildID == "" || !sessioncard.ValidControlToken(token) {
		return domain.Session{}, domain.ErrNotFound
	}
	if repository, ok := service.repository.(ports.SessionCardControlRepository); ok {
		return repository.ResolveCardControl(ctx, guildID, token)
	}
	sessions, err := service.selectableSessions(ctx, query.Actor, guildID, true, allSessionLifecycleStates())
	if err != nil {
		return domain.Session{}, err
	}
	var matched domain.Session
	for _, session := range sessions {
		if session.GuildID != guildID || sessioncard.ControlToken(session.ID) != token {
			continue
		}
		if matched.ID != "" {
			return domain.Session{}, fmt.Errorf("card control token is ambiguous: %w", domain.ErrConflict)
		}
		matched = session
	}
	if matched.ID == "" {
		return domain.Session{}, domain.ErrNotFound
	}
	return matched, nil
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
	if repository, ok := service.repository.(ports.SessionSlugRepository); ok {
		session, slugErr := repository.GetByGuildSlug(ctx, guildID, reference)
		if slugErr == nil {
			if query.AllowGuildMember || query.CanManageGuild || session.OwnerDiscordUserID == query.Actor.ID {
				return selectionFromSession(session), nil
			}
			return Selection{}, domain.ErrNotFound
		}
		if !errors.Is(slugErr, domain.ErrNotFound) {
			return Selection{}, fmt.Errorf("get selected session by slug: %w", slugErr)
		}
	}

	sessions, err := service.selectableSessions(ctx, query.Actor, guildID, query.AllowGuildMember, allSessionLifecycleStates())
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

	states := append([]domain.LifecycleState(nil), query.States...)
	if len(states) == 0 {
		states = allSessionLifecycleStates()
		if !query.IncludeDeleted {
			states = slices.DeleteFunc(states, func(state domain.LifecycleState) bool { return state == domain.StateDeleted })
		}
	}
	sessions, err := service.selectableSessions(ctx, query.Actor, guildID, query.AllowGuildMember, states)
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
	states []domain.LifecycleState,
) ([]domain.Session, error) {
	normalized, err := normalizeSelectionStates(states)
	if err != nil {
		return nil, err
	}
	wanted := make(map[domain.LifecycleState]struct{}, len(normalized))
	for _, state := range normalized {
		wanted[state] = struct{}{}
	}
	result := make([]domain.Session, 0)
	seen := map[string]struct{}{}
	cursor := ""
	for {
		page, listErr := service.repository.ListGuildSessions(ctx, guildID, normalized, ports.GuildSessionPage{Size: 100, Cursor: cursor})
		if listErr != nil {
			return nil, fmt.Errorf("list guild sessions: %w", listErr)
		}
		for _, candidate := range page.Sessions {
			if _, duplicate := seen[candidate.ID]; duplicate {
				continue
			}
			session, getErr := service.repository.Get(ctx, candidate.ID)
			if errors.Is(getErr, domain.ErrNotFound) {
				continue
			}
			if getErr != nil {
				return nil, fmt.Errorf("revalidate guild session %s: %w", candidate.ID, getErr)
			}
			if session.GuildID != guildID {
				continue
			}
			if _, eligible := wanted[session.LifecycleState]; !eligible {
				continue
			}
			if !allowGuildMember && session.OwnerDiscordUserID != actor.ID {
				continue
			}
			seen[session.ID] = struct{}{}
			result = append(result, session)
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			return nil, fmt.Errorf("guild session pagination did not advance")
		}
		cursor = page.NextCursor
	}
	return result, nil
}

func normalizeSelectionStates(states []domain.LifecycleState) ([]domain.LifecycleState, error) {
	if len(states) == 0 {
		return nil, fmt.Errorf("at least one lifecycle state is required")
	}
	result := append([]domain.LifecycleState(nil), states...)
	for _, state := range result {
		if !state.Valid() {
			return nil, fmt.Errorf("invalid lifecycle state %q", state)
		}
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}

func allSessionLifecycleStates() []domain.LifecycleState {
	return []domain.LifecycleState{
		domain.StateDraft, domain.StateNew, domain.StateValidating, domain.StateProvisioning,
		domain.StateBootstrapping, domain.StateInstalling, domain.StateReady, domain.StateRunning,
		domain.StateIdle, domain.StateStopping, domain.StateSleeping, domain.StateWaking,
		domain.StateRestarting, domain.StateWarning1, domain.StateWarning2, domain.StateArchiving,
		domain.StateDestroying, domain.StateArchived, domain.StateRestoring, domain.StateDeleting,
		domain.StateDeleted, domain.StateFailed,
	}
}

func sessionMatchesSelectionSearch(session domain.Session, search string) bool {
	if search == "" {
		return true
	}
	return strings.Contains(strings.ToLower(session.DisplayName), search) ||
		strings.Contains(strings.ToLower(session.Slug), search) ||
		strings.Contains(strings.ToLower(string(session.LifecycleState)), search)
}
