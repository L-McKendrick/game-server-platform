package domain

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPendingWorkshopMissionItemIDsTracksMaterializationAndRefresh(t *testing.T) {
	now := time.Date(2026, 9, 4, 3, 0, 0, 0, time.UTC)
	session := Session{
		WorkshopMissionSources: []WorkshopMissionSource{{AcceptedItemIDs: []uint64{30, 20}, ResolvedAt: now}},
		MissionFiles:           []MissionRecord{{WorkshopItemID: 20, ObjectKey: "mission-object", Filename: "Twenty.Stratis.pbo", Status: ArtifactAccepted, AddedAt: now.Add(time.Minute)}},
	}
	if got := session.PendingWorkshopMissionItemIDs(); !slices.Equal(got, []uint64{30}) {
		t.Fatalf("pending items = %v, want [30]", got)
	}
	session.MissionFiles[0].RemovedAt = now.Add(90 * time.Second)
	if got := session.PendingWorkshopMissionItemIDs(); !slices.Equal(got, []uint64{30}) {
		t.Fatalf("removed materialized item was requeued: %v", got)
	}

	session.WorkshopMissionSources[0].ResolvedAt = now.Add(2 * time.Minute)
	if got := session.PendingWorkshopMissionItemIDs(); !slices.Equal(got, []uint64{20, 30}) {
		t.Fatalf("pending items after refresh = %v, want [20 30]", got)
	}
}

func TestNewWorkshopMissionSourceFiltersMixedCollection(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	source, err := NewWorkshopMissionSource(WorkshopResolution{SchemaVersion: 1, Target: WorkshopTargetMission, SourceKind: WorkshopSourceCollection, Source: WorkshopReference{PublishedFileID: 10, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=10"}, ResolvedAt: now, ResolutionSHA256: strings.Repeat("a", 64), Items: []WorkshopItem{{PublishedFileID: 20, Class: WorkshopItemClientMod}, {PublishedFileID: 30, Filename: "Coop.Altis.pbo", FileSize: 123, Class: WorkshopItemMultiplayerScenario, MatchesTarget: true}, {PublishedFileID: 40, Class: WorkshopItemScenario}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(source.AcceptedItemIDs) != 1 || source.AcceptedItemIDs[0] != 30 || len(source.AcceptedItems) != 1 || source.AcceptedItems[0].Filename != "Coop.Altis.pbo" || len(source.ExcludedItems) != 2 {
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

func TestWorkshopMissionSourceRejectsOversizedCollectionSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	source := WorkshopMissionSource{
		Source:     WorkshopReference{PublishedFileID: 10, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=10"},
		SourceKind: WorkshopSourceCollection, ResolutionSHA256: strings.Repeat("a", 64), ResolvedAt: now,
		AcceptedItemIDs: []uint64{1},
	}
	for id := uint64(2); id <= MaximumWorkshopCollectionChildren+1; id++ {
		source.ExcludedItems = append(source.ExcludedItems, WorkshopResolutionItem{PublishedFileID: id, Class: WorkshopItemClientMod})
	}
	if err := source.Validate(); err == nil || !strings.Contains(err.Error(), "50-item limit") {
		t.Fatalf("Validate() error = %v; want collection limit", err)
	}
}

func TestRecordWorkshopMissionSourceRejectsArchivedSession(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	session := Session{LifecycleState: StateArchived}
	source := WorkshopMissionSource{
		Source:     WorkshopReference{PublishedFileID: 10, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=10"},
		SourceKind: WorkshopSourceItem, ResolutionSHA256: strings.Repeat("a", 64), ResolvedAt: now,
		AcceptedItemIDs: []uint64{10},
	}
	if err := session.RecordWorkshopMissionSource(source, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("RecordWorkshopMissionSource() error = %v; want invalid transition", err)
	}
}
