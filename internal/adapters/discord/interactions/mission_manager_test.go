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
	if response.Data == nil || response.Data.Content != "" || response.Data.Flags != messageFlagEphemeral|messageFlagComponentsV2 || response.Data.Components == nil {
		t.Fatalf("mission manager content = %#v", response.Data)
	}
	topLevel := *response.Data.Components
	if len(topLevel) != 1 || topLevel[0].Type != componentTypeContainer {
		t.Fatalf("mission manager top-level components = %#v", topLevel)
	}
	components := topLevel[0].Components
	if len(components) != 9 || components[0].Type != componentTypeTextDisplay ||
		!strings.Contains(components[0].Content, "Page 1/2") || strings.Contains(components[0].Content, "mission-f") {
		t.Fatalf("mission manager components = %#v", components)
	}
	defaultButtons, removeButtons, nextButtons, missionRows := 0, 0, 0, 0
	for _, row := range components {
		for index, control := range row.Components {
			switch control.Label {
			case "Default":
				defaultButtons++
			case "Remove":
				removeButtons++
			case "Next":
				nextButtons++
			}
			if index == 0 && strings.Contains(control.Label, "mission-") {
				missionRows++
				if !control.Disabled || !strings.Contains(control.Label, string(domain.ArtifactAccepted)) {
					t.Fatalf("mission filename control = %#v", control)
				}
				wantControls := 3
				if strings.Contains(control.Label, "mission-a") {
					wantControls = 1
				}
				if len(row.Components) != wantControls {
					t.Fatalf("mission row %q controls = %#v", control.Label, row.Components)
				}
			}
		}
	}
	if defaultButtons != 5 || removeButtons != 4 || nextButtons != 1 || missionRows != 5 {
		t.Fatalf("controls default=%d remove=%d next=%d mission_rows=%d", defaultButtons, removeButtons, nextButtons, missionRows)
	}
	last := components[len(components)-1]
	if last.Type != componentTypeActionRow || len(last.Components) != 1 || last.Components[0].Label != "Add mission" {
		t.Fatalf("last mission manager row = %#v", last)
	}
}

func TestMissionButtonLabelKeepsStatusWithinDiscordLimit(t *testing.T) {
	t.Parallel()

	label := missionButtonLabel(strings.Repeat("mission-name-", 10)+".Altis.pbo", "ACCEPTED, configured")
	if len([]rune(label)) != maximumMissionButtonLabelRunes || !strings.HasSuffix(label, " — ACCEPTED, configured") || !strings.Contains(label, "…") {
		t.Fatalf("mission button label = %q (%d runes)", label, len([]rune(label)))
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
