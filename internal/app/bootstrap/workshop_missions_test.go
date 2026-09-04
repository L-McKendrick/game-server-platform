package bootstrap

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type workshopManifestReader struct {
	body []byte
	key  string
}

func (reader *workshopManifestReader) Get(_ context.Context, key string) ([]byte, error) {
	reader.key = key
	return reader.body, nil
}

func TestWorkshopMissionsReturnsAuthorizedImmutableRecords(t *testing.T) {
	now := time.Date(2026, 8, 26, 15, 0, 0, 0, time.UTC)
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
	if err != nil {
		t.Fatal(err)
	}
	source := domain.WorkshopMissionSource{Source: domain.WorkshopReference{PublishedFileID: 100, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=100"}, SourceKind: domain.WorkshopSourceCollection, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{200}, ResolvedAt: now}
	if err := session.RecordWorkshopMissionSource(source, now); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("b", 64)
	filename := "Coop.Altis.pbo"
	key := fmt.Sprintf("sessions/session-1/input/missions/%s-%s", digest, filename)
	reader := &workshopManifestReader{body: []byte(fmt.Sprintf("%s\t%s\t%s\t200\n", digest, filename, key))}
	service := &Service{workshopMissionManifest: reader}
	version := session.Version
	missions, err := service.workshopMissions(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if len(missions) != 1 || missions[0].WorkshopItemID != 200 || len(missions[0].WorkshopSources) != 1 {
		t.Fatalf("missions = %#v", missions)
	}
	if session.Version != version || len(session.MissionFiles) != 0 {
		t.Fatal("manifest parsing mutated the session before workflow completion")
	}
	revision, _ := session.WorkshopMissionRevision()
	if reader.key != "sessions/session-1/workshop-resolutions/"+revision+".tsv" {
		t.Fatalf("manifest key = %q", reader.key)
	}
}

func TestWorkshopMissionsRejectsIncompleteOrUnauthorizedManifest(t *testing.T) {
	now := time.Now().UTC()
	session, _ := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
	source := domain.WorkshopMissionSource{Source: domain.WorkshopReference{PublishedFileID: 100, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=100"}, SourceKind: domain.WorkshopSourceCollection, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{200}, ResolvedAt: now}
	_ = session.RecordWorkshopMissionSource(source, now)
	for _, body := range []string{"", strings.Repeat("b", 64) + "\tCoop.Altis.pbo\tsessions/other/input/missions/bad-Coop.Altis.pbo\t200\n"} {
		service := &Service{workshopMissionManifest: &workshopManifestReader{body: []byte(body)}}
		if _, err := service.workshopMissions(context.Background(), session); err == nil {
			t.Fatalf("accepted manifest %q", body)
		}
	}
}

func TestWorkshopMissionsAcceptsOnlyPendingItemsFromPartialRefreshManifest(t *testing.T) {
	now := time.Date(2026, 9, 4, 6, 0, 0, 0, time.UTC)
	session, _ := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
	session.WorkshopMissionSources = []domain.WorkshopMissionSource{
		{Source: domain.WorkshopReference{PublishedFileID: 100, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=100"}, SourceKind: domain.WorkshopSourceItem, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{100}, AcceptedItems: []domain.WorkshopMissionItem{{PublishedFileID: 100, Filename: "Current.Altis.pbo", FileSize: 100}}, ResolvedAt: now},
		{Source: domain.WorkshopReference{PublishedFileID: 200, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=200"}, SourceKind: domain.WorkshopSourceItem, ResolutionSHA256: strings.Repeat("b", 64), AcceptedItemIDs: []uint64{200}, AcceptedItems: []domain.WorkshopMissionItem{{PublishedFileID: 200, Filename: "Pending.Stratis.pbo", FileSize: 200}}, ResolvedAt: now.Add(2 * time.Minute)},
	}
	session.MissionFiles = []domain.MissionRecord{{ObjectKey: "sessions/session-1/input/missions/" + strings.Repeat("c", 64) + "-Current.Altis.pbo", Filename: "Current.Altis.pbo", Status: domain.ArtifactAccepted, WorkshopItemID: 100, AddedAt: now.Add(time.Minute)}}
	digest := strings.Repeat("d", 64)
	key := "sessions/session-1/input/missions/" + digest + "-Pending.Stratis.pbo"
	reader := &workshopManifestReader{body: []byte(digest + "\tPending.Stratis.pbo\t" + key + "\t200\n")}
	service := &Service{workshopMissionManifest: reader}

	missions, err := service.workshopMissions(context.Background(), session)
	if err != nil || len(missions) != 1 || missions[0].WorkshopItemID != 200 {
		t.Fatalf("partial refresh missions = %#v, err = %v", missions, err)
	}
}
