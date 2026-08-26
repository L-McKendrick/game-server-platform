package workshop

import (
	"context"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type testClock struct{ now time.Time }

func (clock testClock) Now() time.Time { return clock.now }

type testCatalog struct {
	items    map[uint64]domain.WorkshopItem
	children map[uint64][]uint64
}

func (catalog testCatalog) Item(_ context.Context, id uint64) (domain.WorkshopItem, error) {
	return catalog.items[id], nil
}
func (catalog testCatalog) CollectionChildren(_ context.Context, id uint64) ([]uint64, error) {
	return catalog.children[id], nil
}

func TestResolveMixedCollectionClassifiesForRequestedTarget(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	catalog := testCatalog{
		items: map[uint64]domain.WorkshopItem{
			10: {PublishedFileID: 10, ConsumerAppID: domain.Arma3WorkshopAppID, Available: true, Collection: true},
			20: {PublishedFileID: 20, ConsumerAppID: domain.Arma3WorkshopAppID, Available: true, Tags: []string{"Mod"}},
			30: {PublishedFileID: 30, ConsumerAppID: domain.Arma3WorkshopAppID, Available: true, Tags: []string{"Scenario", "Multiplayer"}},
		},
		children: map[uint64][]uint64{10: {30, 20}},
	}
	service, err := New(catalog, testClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := service.Resolve(context.Background(), domain.WorkshopSourceRequest{
		MessageType: "workshop_resolution", SchemaVersion: 1, SessionID: "session-1", Target: domain.WorkshopTargetMission,
		SourceURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=10",
		ActorID:   "owner-1", GuildID: "guild-1", ChannelID: "channel-1", CorrelationID: "correlation-1",
		IdempotencyKey: "workshop-1", RequestedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolution.SourceKind != domain.WorkshopSourceCollection || len(resolution.Items) != 2 || resolution.Items[0].MatchesTarget || !resolution.Items[1].MatchesTarget {
		t.Fatalf("resolution = %#v", resolution)
	}
}
