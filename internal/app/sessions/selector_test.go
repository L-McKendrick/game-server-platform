package sessions

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

func TestResolveAcceptsOpaqueIDOrExactSlugButNotDisplayName(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(t, repository, "session-1", "event-1")
	_, err := service.Create(context.Background(), CreateCommand{
		Actor: testActor("owner-1"), CorrelationID: "create-1", IdempotencyKey: "resolve:create-1",
		Slug: "saturday-arma", DisplayName: "Saturday Arma", GameType: "arma3",
		GuildID: "guild-1", ChannelID: "channel-1",
	})
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	for _, reference := range []string{"session-1", "saturday-arma"} {
		selection, err := service.Resolve(context.Background(), ResolveQuery{
			Actor: testActor("owner-1"), GuildID: "guild-1", Reference: reference,
		})
		if err != nil {
			t.Fatalf("Resolve(%q) returned error: %v", reference, err)
		}
		if selection.ID != "session-1" || selection.Slug != "saturday-arma" {
			t.Fatalf("Resolve(%q) = %#v", reference, selection)
		}
	}

	_, err = service.Resolve(context.Background(), ResolveQuery{
		Actor: testActor("owner-1"), GuildID: "guild-1", Reference: "Saturday Arma",
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Resolve(display name) error = %v; want ErrNotFound", err)
	}
}

func TestResolveDoesNotCrossOwnerOrGuildBoundaries(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(t, repository, "session-1", "event-1")
	_, err := service.Create(context.Background(), CreateCommand{
		Actor: testActor("owner-1"), CorrelationID: "create-1", IdempotencyKey: "resolve:scope",
		Slug: "private-session", DisplayName: "Private Session", GameType: "arma3",
		GuildID: "guild-1", ChannelID: "channel-1",
	})
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	queries := []ResolveQuery{
		{Actor: testActor("owner-2"), GuildID: "guild-1", Reference: "private-session"},
		{Actor: testActor("owner-1"), GuildID: "guild-2", Reference: "private-session"},
	}
	for _, query := range queries {
		if _, err := service.Resolve(context.Background(), query); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Resolve(%#v) error = %v; want ErrNotFound", query, err)
		}
	}
}

func TestResolveExactSlugBeyondBoundedGuildListing(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	ids := make([]string, 0, 202)
	for index := 0; index <= 100; index++ {
		suffix := fmt.Sprintf("%03d", index)
		ids = append(ids, "session-"+suffix, "event-"+suffix)
	}
	service := newTestService(t, repository, ids...)
	for index := 0; index <= 100; index++ {
		suffix := fmt.Sprintf("%03d", index)
		_, err := service.Create(context.Background(), CreateCommand{
			Actor: testActor("owner-1"), CorrelationID: "create-" + suffix,
			IdempotencyKey: "resolve:large-guild:" + suffix, Slug: "test-" + suffix,
			DisplayName: "Test " + suffix, GameType: "arma3", GuildID: "guild-1", ChannelID: "channel-1",
		})
		if err != nil {
			t.Fatalf("Create(%s) returned error: %v", suffix, err)
		}
	}
	page, err := repository.ListGuildSessions(context.Background(), "guild-1", allSessionLifecycleStates(), ports.GuildSessionPage{Size: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range page.Sessions {
		if session.Slug == "test-000" {
			t.Fatal("regression setup did not place exact slug beyond bounded listing")
		}
	}
	selection, err := service.Resolve(context.Background(), ResolveQuery{
		Actor: testActor("admin-1"), GuildID: "guild-1", Reference: "test-000", CanManageGuild: true, AllowGuildMember: true,
	})
	if err != nil || selection.ID != "session-000" {
		t.Fatalf("Resolve(test-000) = %#v, %v", selection, err)
	}
}

func TestSelectReturnsMatchingOwnerSessionsOnlyFromRequestedGuild(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(t, repository,
		"session-bravo", "event-bravo",
		"session-alpha", "event-alpha",
		"session-other-guild", "event-other-guild",
		"session-other-owner", "event-other-owner",
	)
	owner := testActor("owner-1")
	create := func(actorID, correlationID, slug, name, guildID string) {
		t.Helper()
		_, err := service.Create(context.Background(), CreateCommand{
			Actor:          testActor(actorID),
			CorrelationID:  correlationID,
			IdempotencyKey: "selector:" + correlationID,
			Slug:           slug,
			DisplayName:    name,
			GameType:       "arma3",
			GuildID:        guildID,
			ChannelID:      "channel-1",
		})
		if err != nil {
			t.Fatalf("Create() returned error: %v", err)
		}
	}

	create("owner-1", "bravo", "bravo-night", "Bravo Night", "guild-1")
	create("owner-1", "alpha", "alpha-night", "Alpha Night", "guild-1")
	create("owner-1", "other-guild", "private-night", "Private Night", "guild-2")
	create("owner-2", "other-owner", "other-night", "Other Night", "guild-1")

	selections, err := service.Select(context.Background(), SelectQuery{
		Actor: owner, GuildID: "guild-1", Search: "NIGHT", Limit: 25,
	})
	if err != nil {
		t.Fatalf("Select() returned error: %v", err)
	}
	if len(selections) != 2 {
		t.Fatalf("selection count = %d; want 2: %#v", len(selections), selections)
	}
	if selections[0].ID != "session-alpha" || selections[0].DisplayName != "Alpha Night" ||
		selections[0].Slug != "alpha-night" || selections[0].LifecycleState != "DRAFT" {
		t.Errorf("first selection = %#v", selections[0])
	}
	if selections[1].ID != "session-bravo" {
		t.Errorf("second selection ID = %q; want session-bravo", selections[1].ID)
	}
}

func TestSelectExcludesDeletedSessionsUnlessExplicitlyIncluded(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	active, err := domain.NewSession(domain.NewSessionInput{
		ID: "session-active", Slug: "active-session", DisplayName: "Active Session", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := domain.NewSession(domain.NewSessionInput{
		ID: "session-deleted", Slug: "deleted-session", DisplayName: "Deleted Session", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	deleted.DesiredState, deleted.ObservedState, deleted.LifecycleState = domain.StateDeleted, domain.StateDeleted, domain.StateDeleted

	for _, session := range []domain.Session{active, deleted} {
		event := domain.NewSessionCreatedEvent("event-"+session.ID, "correlation-"+session.ID, testActor("owner-1"), session, now)
		idempotency, recordErr := domain.NewCompletedIdempotencyRecord(
			"selector:"+session.ID, "hash-"+session.ID, session.ID, now, time.Hour,
		)
		if recordErr != nil {
			t.Fatal(recordErr)
		}
		if createErr := repository.Create(context.Background(), session, event, idempotency); createErr != nil {
			t.Fatal(createErr)
		}
	}

	service := newTestService(t, repository)
	query := SelectQuery{Actor: testActor("owner-1"), GuildID: "guild-1", Limit: 25}
	selections, err := service.Select(context.Background(), query)
	if err != nil {
		t.Fatalf("Select() returned error: %v", err)
	}
	if len(selections) != 1 || selections[0].ID != active.ID {
		t.Fatalf("default selections = %#v; want active session only", selections)
	}

	query.IncludeDeleted = true
	selections, err = service.Select(context.Background(), query)
	if err != nil {
		t.Fatalf("Select(include deleted) returned error: %v", err)
	}
	if len(selections) != 2 {
		t.Fatalf("included selections = %#v; want active and deleted sessions", selections)
	}
}

func TestSelectBoundsResultLimit(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	ids := make([]string, 0, 60)
	for index := 0; index < 30; index++ {
		suffix := strconv.Itoa(index)
		ids = append(ids, "session-"+suffix, "event-"+suffix)
	}
	service := newTestService(t, repository, ids...)
	for index := 0; index < 30; index++ {
		suffix := strconv.Itoa(index)
		_, err := service.Create(context.Background(), CreateCommand{
			Actor: testActor("owner-1"), CorrelationID: "correlation-" + suffix,
			IdempotencyKey: "selector:limit:" + suffix, Slug: "session-" + suffix,
			DisplayName: "Session " + suffix, GameType: "arma3", GuildID: "guild-1", ChannelID: "channel-1",
		})
		if err != nil {
			t.Fatalf("Create() returned error: %v", err)
		}
	}

	selections, err := service.Select(context.Background(), SelectQuery{
		Actor: testActor("owner-1"), GuildID: "guild-1", Limit: 100,
	})
	if err != nil {
		t.Fatalf("Select() returned error: %v", err)
	}
	if len(selections) != maximumSessionSelections {
		t.Fatalf("selection count = %d; want %d", len(selections), maximumSessionSelections)
	}
}

func TestSelectAppliesAuthorizationAndLimitAfterAllGuildPages(t *testing.T) {
	t.Parallel()
	base := memory.NewSessionRepository()
	repository := &pagedGuildRepository{SessionRepository: base, authoritative: map[string]domain.Session{}}
	first := ports.GuildSessionPageResult{NextCursor: "next"}
	for index := 0; index < 100; index++ {
		id := fmt.Sprintf("other-%03d", index)
		session := selectorCandidate(id, "other-owner", domain.StateRunning)
		first.Sessions = append(first.Sessions, session)
		repository.authoritative[id] = session
	}
	owned := selectorCandidate("owned-after-first-page", "owner-1", domain.StateRunning)
	repository.authoritative[owned.ID] = owned
	repository.pages = []ports.GuildSessionPageResult{first, {Sessions: []domain.Session{owned}}}
	service := newRepositoryTestService(t, repository)

	selections, err := service.Select(context.Background(), SelectQuery{
		Actor: testActor("owner-1"), GuildID: "guild-1", Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(selections) != 1 || selections[0].ID != owned.ID || repository.calls != 2 {
		t.Fatalf("selections = %#v, pages = %d", selections, repository.calls)
	}
}

func TestListUsesGuildStatePagesBeforeOwnerLimit(t *testing.T) {
	t.Parallel()
	other := selectorCandidate("other-owner-first", "other-owner", domain.StateRunning)
	owned := selectorCandidate("test-62", "owner-1", domain.StateRunning)
	repository := &pagedGuildRepository{
		SessionRepository: memory.NewSessionRepository(),
		pages: []ports.GuildSessionPageResult{
			{Sessions: []domain.Session{other}, NextCursor: "next"},
			{Sessions: []domain.Session{owned}},
		},
		authoritative: map[string]domain.Session{other.ID: other, owned.ID: owned},
	}
	service := newRepositoryTestService(t, repository)
	sessions, err := service.List(context.Background(), ListQuery{
		Actor: testActor("owner-1"), GuildID: "guild-1", States: []domain.LifecycleState{domain.StateRunning}, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != owned.ID || repository.calls != 2 {
		t.Fatalf("sessions=%#v pages=%d", sessions, repository.calls)
	}
}

func TestSelectRevalidatesEventuallyConsistentCandidates(t *testing.T) {
	t.Parallel()
	candidate := selectorCandidate("stale-running", "owner-1", domain.StateRunning)
	current := candidate
	current.LifecycleState = domain.StateSleeping
	repository := &pagedGuildRepository{
		SessionRepository: memory.NewSessionRepository(),
		pages:             []ports.GuildSessionPageResult{{Sessions: []domain.Session{candidate}}},
		authoritative:     map[string]domain.Session{candidate.ID: current},
	}
	service := newRepositoryTestService(t, repository)
	selections, err := service.Select(context.Background(), SelectQuery{
		Actor: testActor("owner-1"), GuildID: "guild-1", AllowGuildMember: true,
		States: []domain.LifecycleState{domain.StateRunning, domain.StateIdle}, Limit: 25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(selections) != 0 {
		t.Fatalf("stale candidate survived authoritative revalidation: %#v", selections)
	}
}

func TestSelectMergesEligibleStatesBeforeApplyingDiscordLimit(t *testing.T) {
	t.Parallel()
	repository := &pagedGuildRepository{SessionRepository: memory.NewSessionRepository(), authoritative: map[string]domain.Session{}}
	page := ports.GuildSessionPageResult{}
	for index := 0; index < 25; index++ {
		id := fmt.Sprintf("running-%02d", index)
		session := selectorCandidate(id, "owner-1", domain.StateRunning)
		session.DisplayName = "Zulu " + id
		page.Sessions = append(page.Sessions, session)
		repository.authoritative[id] = session
	}
	idle := selectorCandidate("idle-visible", "owner-1", domain.StateIdle)
	idle.DisplayName = "Alpha idle"
	page.Sessions = append(page.Sessions, idle)
	repository.authoritative[idle.ID] = idle
	repository.pages = []ports.GuildSessionPageResult{page}
	service := newRepositoryTestService(t, repository)

	selections, err := service.Select(context.Background(), SelectQuery{
		Actor: testActor("admin"), GuildID: "guild-1", AllowGuildMember: true, Limit: 25,
		States: []domain.LifecycleState{domain.StateRunning, domain.StateIdle},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(selections) != 25 || selections[0].ID != idle.ID {
		t.Fatalf("merged selections = %#v", selections)
	}
}

type pagedGuildRepository struct {
	*memory.SessionRepository
	pages         []ports.GuildSessionPageResult
	authoritative map[string]domain.Session
	calls         int
}

func (repository *pagedGuildRepository) ListGuildSessions(context.Context, string, []domain.LifecycleState, ports.GuildSessionPage) (ports.GuildSessionPageResult, error) {
	if repository.calls >= len(repository.pages) {
		return ports.GuildSessionPageResult{}, nil
	}
	page := repository.pages[repository.calls]
	repository.calls++
	return page, nil
}

func (repository *pagedGuildRepository) Get(_ context.Context, sessionID string) (domain.Session, error) {
	session, ok := repository.authoritative[sessionID]
	if !ok {
		return domain.Session{}, domain.ErrNotFound
	}
	return session, nil
}

func selectorCandidate(id, owner string, state domain.LifecycleState) domain.Session {
	return domain.Session{ID: id, Slug: id, DisplayName: id, OwnerDiscordUserID: owner, GuildID: "guild-1", LifecycleState: state}
}

func newRepositoryTestService(t *testing.T, repository ports.SessionRepository) *Service {
	t.Helper()
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now: time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
