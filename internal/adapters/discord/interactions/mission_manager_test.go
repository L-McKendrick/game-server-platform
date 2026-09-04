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

func TestWriteMissionManagerShowsPendingWorkshopMissionsWithoutDuplicateControls(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Workshop missions", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.WorkshopMissionSources = []domain.WorkshopMissionSource{
		{AcceptedItemIDs: []uint64{100, 200}, AcceptedItems: []domain.WorkshopMissionItem{{PublishedFileID: 100, Filename: "Ready.Altis.pbo"}, {PublishedFileID: 200, Filename: "Pending.Enoch.pbo"}}, ResolvedAt: now.Add(-time.Minute)},
		{AcceptedItemIDs: []uint64{200, 300, 400}, ResolvedAt: now.Add(-time.Minute)},
	}
	session.MissionFiles = []domain.MissionRecord{{ObjectKey: "sessions/session-1/input/missions/hash-Ready.Altis.pbo", Filename: "Ready.Altis.pbo", Status: domain.ArtifactAccepted, AddedAt: now, WorkshopItemID: 100}}
	session.MissionFiles = append(session.MissionFiles, domain.MissionRecord{ObjectKey: "sessions/session-1/input/missions/hash-Removed.Altis.pbo", Filename: "Removed.Altis.pbo", Status: domain.ArtifactAccepted, AddedAt: now, RemovedAt: now.Add(time.Minute), WorkshopItemID: 300})

	recorder := httptest.NewRecorder()
	writeMissionManager(recorder, session, 0)
	var response interactionResponse
	decodeResponse(t, recorder, &response)
	components := (*response.Data.Components)[0].Components
	labels := make([]string, 0)
	for _, row := range components {
		if len(row.Components) == 0 || !strings.Contains(row.Components[0].Label, "Workshop") {
			continue
		}
		labels = append(labels, row.Components[0].Label)
		if strings.Contains(row.Components[0].Label, "awaiting download") && len(row.Components) != 1 {
			t.Fatalf("pending Workshop row has mutation controls: %#v", row.Components)
		}
	}
	joined := strings.Join(labels, "\n")
	for _, want := range []string{"Ready.Altis.pbo — ACCEPTED, Workshop #100", "Pending.Enoch.pbo — awaiting download, Workshop #200", "Workshop item #400 — awaiting download, Workshop #400"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Workshop mission labels = %q; missing %q", joined, want)
		}
	}
	if strings.Count(joined, "Workshop #200") != 1 {
		t.Fatalf("pending collection item was duplicated: %q", joined)
	}
	if strings.Contains(joined, "Workshop #300") {
		t.Fatalf("removed Workshop mission reappeared as pending: %q", joined)
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
