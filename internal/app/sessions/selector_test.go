package sessions

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
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
