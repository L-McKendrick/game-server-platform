package workshop

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type recorderObjectStore struct {
	puts   int
	bodies [][]byte
}

func (store *recorderObjectStore) Put(_ context.Context, _ string, _ string, body []byte, _ string) error {
	store.puts++
	store.bodies = append(store.bodies, append([]byte(nil), body...))
	return nil
}

type recorderIDs struct{ next int }

func (ids *recorderIDs) New(time.Time) (string, error) {
	ids.next++
	return "event-" + string(rune('0'+ids.next)), nil
}

func TestRecordModResolutionPersistsOneVersionAndReplays(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 3, 10, 26, 17, 0, time.UTC)
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.BeginWorkshopResolution(domain.WorkshopTargetMods, "request-1", now); err != nil {
		t.Fatal(err)
	}
	session.CreatorDLCs = []string{domain.CreatorDLCReactionForces}
	createRecord, err := domain.NewCompletedIdempotencyRecord("create-1", "create-hash", session.ID, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	createEvent := domain.NewSessionCreatedEvent("create-event", "create-correlation", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner"}, session, now)
	if err := repository.Create(ctx, session, createEvent, createRecord); err != nil {
		t.Fatal(err)
	}

	objects := &recorderObjectStore{}
	recorder, err := NewRecorder(repository, objects, &recorderIDs{}, testClock{now: now.Add(time.Second)}, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := domain.WorkshopSourceRequest{MessageType: "workshop_resolution", SchemaVersion: 1, SessionID: session.ID, Target: domain.WorkshopTargetMods, SourceURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=3368879130", ActorID: "owner", GuildID: "guild", ChannelID: "channel", CorrelationID: "correlation-1", IdempotencyKey: "request-1", RequestedAt: now}
	resolution := domain.WorkshopResolution{SchemaVersion: 1, Target: domain.WorkshopTargetMods, SourceKind: domain.WorkshopSourceCollection, Source: domain.WorkshopReference{PublishedFileID: 3368879130, CanonicalURL: request.SourceURL}, Items: []domain.WorkshopItem{{PublishedFileID: 450814997, Title: "CBA_A3", Class: domain.WorkshopItemClientMod, MatchesTarget: true}}, ResolutionSHA256: strings.Repeat("a", 64), ResolvedAt: now}

	result, err := recorder.RecordModResolution(ctx, request, resolution)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := repository.Get(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	presets := 0
	for _, body := range objects.bodies {
		if strings.HasPrefix(string(body), "<?xml") {
			presets++
			if !strings.Contains(string(body), `name="arma:Type" content="preset" />`) || !strings.Contains(string(body), "/app/2647760") {
				t.Fatalf("Workshop preset omits Launcher metadata or configured cDLC: %s", body)
			}
		}
	}
	if presets != 2 {
		t.Fatalf("stored %d preset bodies; want private and public exports", presets)
	}
	if persisted.Version != session.Version+1 || persisted.WorkshopResolutionRequestKey != "" || len(persisted.WorkshopModSources) != 1 || result.Revision.Number != 1 {
		t.Fatalf("persisted = %#v, result = %#v", persisted, result)
	}
	version, puts := persisted.Version, objects.puts
	if _, err := recorder.RecordModResolution(ctx, request, resolution); err != nil {
		t.Fatal(err)
	}
	replayed, _ := repository.Get(ctx, session.ID)
	if replayed.Version != version || objects.puts != puts {
		t.Fatalf("replay changed version or objects: version=%d puts=%d", replayed.Version, objects.puts)
	}
}
