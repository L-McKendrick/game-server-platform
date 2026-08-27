package domain

import (
	"strings"
	"testing"
	"time"
)

func TestNewWorkshopMissionSourceFiltersMixedCollection(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	source, err := NewWorkshopMissionSource(WorkshopResolution{SchemaVersion: 1, Target: WorkshopTargetMission, SourceKind: WorkshopSourceCollection, Source: WorkshopReference{PublishedFileID: 10, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=10"}, ResolvedAt: now, ResolutionSHA256: strings.Repeat("a", 64), Items: []WorkshopItem{{PublishedFileID: 20, Class: WorkshopItemClientMod}, {PublishedFileID: 30, Class: WorkshopItemMultiplayerScenario, MatchesTarget: true}, {PublishedFileID: 40, Class: WorkshopItemScenario}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(source.AcceptedItemIDs) != 1 || source.AcceptedItemIDs[0] != 30 || len(source.ExcludedItems) != 2 {
		t.Fatalf("source = %#v", source)
	}
}

func TestNewWorkshopMissionSourceRejectsNoEligibleOrTooManyItems(t *testing.T) {
	now := time.Now().UTC()
	base := WorkshopResolution{SchemaVersion: 1, Target: WorkshopTargetMission, SourceKind: WorkshopSourceCollection, Source: WorkshopReference{PublishedFileID: 10, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=10"}, ResolvedAt: now, ResolutionSHA256: strings.Repeat("b", 64), Items: []WorkshopItem{{PublishedFileID: 1, Class: WorkshopItemClientMod}}}
	if _, err := NewWorkshopMissionSource(base); err == nil {
		t.Fatal("expected no-eligible-item rejection")
	}
	base.Items = nil
	for id := uint64(1); id <= MaximumWorkshopMissionItems+1; id++ {
		base.Items = append(base.Items, WorkshopItem{PublishedFileID: id, Class: WorkshopItemMultiplayerScenario, MatchesTarget: true})
	}
	if _, err := NewWorkshopMissionSource(base); err == nil {
		t.Fatal("expected item-limit rejection")
	}
}

func TestRecordWorkshopMissionSourceIsReplaySafeAndDoesNotChangeCurrentMission(t *testing.T) {
	now := time.Now().UTC()
	session, err := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.CurrentMission = MissionSelection{Template: "existing.VR", ObjectKey: "sessions/session-1/input/missions/existing.pbo"}
	source := WorkshopMissionSource{Source: WorkshopReference{PublishedFileID: 10, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=10"}, SourceKind: WorkshopSourceItem, ResolutionSHA256: strings.Repeat("c", 64), AcceptedItemIDs: []uint64{10}, ResolvedAt: now}
	if err := session.RecordWorkshopMissionSource(source, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	version := session.Version
	if err := session.RecordWorkshopMissionSource(source, now.Add(2*time.Minute)); err != nil || session.Version != version {
		t.Fatalf("replay changed session: %v", err)
	}
	source.ResolutionSHA256 = strings.Repeat("d", 64)
	if err := session.RecordWorkshopMissionSource(source, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(session.WorkshopMissionSources) != 1 || session.CurrentMission.Template != "existing.VR" {
		t.Fatalf("session = %#v", session)
	}
}
