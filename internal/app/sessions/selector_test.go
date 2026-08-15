package sessions

import (
	"context"
	"strconv"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
)

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
