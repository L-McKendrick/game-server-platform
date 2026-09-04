package workshop

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type testClock struct{ now time.Time }

func (clock testClock) Now() time.Time { return clock.now }

type testCatalog struct {
	items    map[uint64]domain.WorkshopItem
	children map[uint64][]domain.WorkshopCollectionChild
}

type oversizedCollectionCatalog struct {
	childMetadataRequested bool
	rootMetadataRequested  bool
}

func (catalog *oversizedCollectionCatalog) Item(_ context.Context, id uint64) (domain.WorkshopItem, error) {
	catalog.rootMetadataRequested = true
	return domain.WorkshopItem{PublishedFileID: id, ConsumerAppID: domain.Arma3WorkshopAppID, Available: true, Collection: true}, nil
}

func (catalog *oversizedCollectionCatalog) Items(_ context.Context, _ []uint64) ([]domain.WorkshopItem, error) {
	catalog.childMetadataRequested = true
	return nil, nil
}

func (catalog *oversizedCollectionCatalog) CollectionChildren(_ context.Context, _ uint64) ([]domain.WorkshopCollectionChild, error) {
	children := make([]domain.WorkshopCollectionChild, domain.MaximumWorkshopCollectionChildren+1)
	for index := range children {
		children[index].PublishedFileID = uint64(index + 1)
	}
	return children, nil
}

func TestResolveRejectsOversizedCollectionBeforeChildMetadata(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	catalog := &oversizedCollectionCatalog{}
	service, err := New(catalog, testClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Resolve(context.Background(), domain.WorkshopSourceRequest{
		MessageType: "workshop_resolution", SchemaVersion: 1, SessionID: "session-1", Target: domain.WorkshopTargetMission,
		SourceURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=10", ActorID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
		CorrelationID: "correlation-1", IdempotencyKey: "workshop-limit", RequestedAt: now,
	})
	if err == nil || !strings.Contains(err.Error(), "maximum is 50") {
		t.Fatalf("Resolve() error = %v; want collection limit rejection", err)
	}
	if catalog.childMetadataRequested {
		t.Fatal("oversized collection requested child metadata")
	}
	if catalog.rootMetadataRequested {
		t.Fatal("collection requested unused root-item metadata")
	}
}

func (catalog testCatalog) Item(_ context.Context, id uint64) (domain.WorkshopItem, error) {
	return catalog.items[id], nil
}
func (catalog testCatalog) Items(_ context.Context, ids []uint64) ([]domain.WorkshopItem, error) {
	items := make([]domain.WorkshopItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, catalog.items[id])
	}
	return items, nil
}
func (catalog testCatalog) CollectionChildren(_ context.Context, id uint64) ([]domain.WorkshopCollectionChild, error) {
	children, ok := catalog.children[id]
	if !ok {
		return nil, domain.ErrWorkshopNotCollection
	}
	return children, nil
}

func TestResolveMixedCollectionClassifiesForRequestedTarget(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	catalog := testCatalog{
		items: map[uint64]domain.WorkshopItem{
			10: {PublishedFileID: 10, ConsumerAppID: domain.Arma3WorkshopAppID, Available: true, Collection: true},
			20: {PublishedFileID: 20, ConsumerAppID: domain.Arma3WorkshopAppID, Available: true, Tags: []string{"Mod"}},
			30: {PublishedFileID: 30, ConsumerAppID: domain.Arma3WorkshopAppID, Available: true, Tags: []string{"Scenario", "Multiplayer"}},
		},
		children: map[uint64][]domain.WorkshopCollectionChild{10: {{PublishedFileID: 30}, {PublishedFileID: 20}}},
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

func TestResolveExcludesNestedCollectionsWithoutExpandingThem(t *testing.T) {
	now := time.Date(2026, 9, 3, 6, 0, 0, 0, time.UTC)
	catalog := testCatalog{
		items: map[uint64]domain.WorkshopItem{
			10: {PublishedFileID: 10, ConsumerAppID: domain.Arma3WorkshopAppID, Available: true},
			20: {PublishedFileID: 20, ConsumerAppID: domain.Arma3WorkshopAppID, Available: true, Tags: []string{"Mod"}},
		},
		children: map[uint64][]domain.WorkshopCollectionChild{10: {{PublishedFileID: 30, Collection: true}, {PublishedFileID: 20}}},
	}
	service, _ := New(catalog, testClock{now: now})
	resolution, err := service.Resolve(context.Background(), domain.WorkshopSourceRequest{MessageType: "workshop_resolution", SchemaVersion: 1, SessionID: "session-1", Target: domain.WorkshopTargetMods, SourceURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=10", ActorID: "owner", GuildID: "guild", ChannelID: "channel", CorrelationID: "correlation", IdempotencyKey: "key", RequestedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if resolution.SourceKind != domain.WorkshopSourceCollection || len(resolution.Items) != 2 || resolution.Items[0].PublishedFileID != 20 || !resolution.Items[0].MatchesTarget || resolution.Items[1].Class != domain.WorkshopItemNestedCollection || resolution.Items[1].MatchesTarget {
		t.Fatalf("resolution = %#v", resolution)
	}
}

func TestResolveObservedReArmaCollectionFixture(t *testing.T) {
	now := time.Date(2026, 9, 3, 7, 0, 0, 0, time.UTC)
	ids := []uint64{3791892274, 3770609240, 3742163031, 450814997, 2623341670, 3407948300, 3525204764}
	catalog := testCatalog{items: map[uint64]domain.WorkshopItem{3368879130: {PublishedFileID: 3368879130, ConsumerAppID: domain.Arma3WorkshopAppID, Available: true, Tags: []string{"Mod"}}}, children: map[uint64][]domain.WorkshopCollectionChild{3368879130: {}}}
	for _, id := range ids {
		catalog.children[3368879130] = append(catalog.children[3368879130], domain.WorkshopCollectionChild{PublishedFileID: id})
		catalog.items[id] = domain.WorkshopItem{PublishedFileID: id, ConsumerAppID: domain.Arma3WorkshopAppID, Available: true, Title: "Observed direct mod", Tags: []string{"Mod"}}
	}
	service, _ := New(catalog, testClock{now: now})
	resolution, err := service.Resolve(context.Background(), domain.WorkshopSourceRequest{MessageType: "workshop_resolution", SchemaVersion: 1, SessionID: "session-1", Target: domain.WorkshopTargetMods, SourceURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=3368879130", ActorID: "owner", GuildID: "guild", ChannelID: "channel", CorrelationID: "correlation", IdempotencyKey: "rearma", RequestedAt: now})
	if err != nil || resolution.SourceKind != domain.WorkshopSourceCollection || len(resolution.Items) != 7 {
		t.Fatalf("resolution = %#v, %v", resolution, err)
	}
	for _, item := range resolution.Items {
		if item.Class != domain.WorkshopItemClientMod || !item.MatchesTarget {
			t.Fatalf("item = %#v", item)
		}
	}
}
