package interactions

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestWriteMissionManagerPaginatesFiveAndProtectsCurrentMission(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 23, 17, 0, 0, 0, time.UTC)
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Mission manager", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 6; index++ {
		key := "sessions/session-1/input/missions/mission-" + string(rune('a'+index)) + ".Altis.pbo"
		session.MissionFiles = append(session.MissionFiles, domain.MissionRecord{ObjectKey: key, Filename: "mission-" + string(rune('a'+index)) + ".Altis.pbo", Status: domain.ArtifactAccepted, AddedAt: now})
	}
	session.ConfiguredMission = domain.UploadedMissionSelection(session.MissionFiles[0].ObjectKey)
	session.MissionObjectKey = session.MissionFiles[0].ObjectKey
	session.CurrentMission = session.ConfiguredMission
	recorder := httptest.NewRecorder()
	writeMissionManager(recorder, session, 0)
	var response interactionResponse
	decodeResponse(t, recorder, &response)
	if response.Data == nil || !strings.Contains(response.Data.Content, "Page 1/2") || strings.Contains(response.Data.Content, "mission-f") {
		t.Fatalf("mission manager content = %#v", response.Data)
	}
	defaultButtons, removeButtons, nextButtons := 0, 0, 0
	for _, row := range *response.Data.Components {
		for _, control := range row.Components {
			switch control.Label {
			case "Default":
				defaultButtons++
			case "Remove":
				removeButtons++
			case "Next":
				nextButtons++
			}
		}
	}
	if defaultButtons != 6 || removeButtons != 4 || nextButtons != 1 {
		t.Fatalf("controls default=%d remove=%d next=%d", defaultButtons, removeButtons, nextButtons)
	}
	if len(*response.Data.Components) > 5 {
		t.Fatalf("mission manager emitted %d action rows", len(*response.Data.Components))
	}
}

func TestMissionControlIdentifierRejectsMalformedState(t *testing.T) {
	t.Parallel()
	if _, _, _, _, _, err := parseMissionCustomID("rb:missions:v1:remove:session:bad:0:1"); err == nil {
		t.Fatal("malformed mission index was accepted")
	}
	customID := missionCustomID("session-1", "default", 2, 1, 7)
	action, sessionID, index, page, version, err := parseMissionCustomID(customID)
	if err != nil || action != "default" || sessionID != "session-1" || index != 2 || page != 1 || version != 7 {
		t.Fatalf("parsed mission control = %q %q %d %d %d %v", action, sessionID, index, page, version, err)
	}
}
