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

func TestImportWorkshopMissionsAttachesImmutableRecordsWithoutChangingSelection(t *testing.T) {
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
	configured, current := session.ConfiguredMission, session.CurrentMission
	if err := service.importWorkshopMissions(context.Background(), &session, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(session.MissionFiles) != 1 || session.MissionFiles[0].WorkshopItemID != 200 || len(session.MissionFiles[0].WorkshopSources) != 1 {
		t.Fatalf("missions = %#v", session.MissionFiles)
	}
	if session.ConfiguredMission != configured || session.CurrentMission != current {
		t.Fatal("Workshop import changed mission selection")
	}
	revision, _ := session.WorkshopMissionRevision()
	if reader.key != "sessions/session-1/workshop-resolutions/"+revision+".tsv" {
		t.Fatalf("manifest key = %q", reader.key)
	}
}

func TestImportWorkshopMissionsRejectsIncompleteOrUnauthorizedManifest(t *testing.T) {
	now := time.Now().UTC()
	session, _ := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
	source := domain.WorkshopMissionSource{Source: domain.WorkshopReference{PublishedFileID: 100, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=100"}, SourceKind: domain.WorkshopSourceCollection, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{200}, ResolvedAt: now}
	_ = session.RecordWorkshopMissionSource(source, now)
	for _, body := range []string{"", strings.Repeat("b", 64) + "\tCoop.Altis.pbo\tsessions/other/input/missions/bad-Coop.Altis.pbo\t200\n"} {
		service := &Service{workshopMissionManifest: &workshopManifestReader{body: []byte(body)}}
		copy := session
		if err := service.importWorkshopMissions(context.Background(), &copy, now); err == nil {
			t.Fatalf("accepted manifest %q", body)
		}
	}
}
