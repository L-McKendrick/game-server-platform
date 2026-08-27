package domain

import (
	"strings"
	"testing"
	"time"
)

func TestNewWorkshopModSourceAcceptsClientModsAndExcludesServerAndScenarioItems(t *testing.T) {
	now := time.Now().UTC()
	resolution := WorkshopResolution{SchemaVersion: 1, Target: WorkshopTargetMods, SourceKind: WorkshopSourceCollection, Source: WorkshopReference{PublishedFileID: 10, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=10"}, ResolutionSHA256: strings.Repeat("a", 64), ResolvedAt: now, Items: []WorkshopItem{
		{PublishedFileID: 20, Title: "Client Mod", Class: WorkshopItemClientMod, MatchesTarget: true},
		{PublishedFileID: 30, Title: "Server Mod", Class: WorkshopItemServerMod},
		{PublishedFileID: 40, Title: "Scenario", Class: WorkshopItemMultiplayerScenario},
	}}
	source, err := NewWorkshopModSource(resolution)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.AcceptedItems) != 1 || source.AcceptedItems[0].PublishedFileID != 20 || len(source.ExcludedItems) != 2 {
		t.Fatalf("source = %#v", source)
	}
}

func TestClassifyWorkshopServerModIsExplicitlyExcludedFromClientPreset(t *testing.T) {
	item := ClassifyWorkshopItem(WorkshopItem{PublishedFileID: 30, ConsumerAppID: Arma3WorkshopAppID, Available: true, Tags: []string{"Server"}}, WorkshopTargetMods)
	if item.Class != WorkshopItemServerMod || item.MatchesTarget || !strings.Contains(item.Issue, "server-mod") {
		t.Fatalf("item = %#v", item)
	}
}

func TestRecordWorkshopModSourceReplayAndReplacement(t *testing.T) {
	now := time.Now().UTC()
	session, _ := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
	source := WorkshopModSource{Source: WorkshopReference{PublishedFileID: 20, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=20"}, SourceKind: WorkshopSourceItem, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItems: []WorkshopModItem{{PublishedFileID: 20, Title: "Mod"}}, ResolvedAt: now, PresetObjectKey: "sessions/session-1/input/presets/" + strings.Repeat("b", 64) + "-a.html", ModlistObjectKey: "sessions/session-1/input/modlists/" + strings.Repeat("b", 64) + "/a.html", ManifestObjectKey: "sessions/session-1/input/workshop-sources/" + strings.Repeat("a", 64) + ".json", ArtifactSHA256: strings.Repeat("b", 64)}
	if err := session.RecordWorkshopModSource(source, now); err != nil {
		t.Fatal(err)
	}
	version := session.Version
	if err := session.RecordWorkshopModSource(source, now.Add(time.Minute)); err != nil || session.Version != version {
		t.Fatal("replay mutated session")
	}
	source.ResolutionSHA256 = strings.Repeat("c", 64)
	source.ManifestObjectKey = "sessions/session-1/input/workshop-sources/" + strings.Repeat("c", 64) + ".json"
	if err := session.RecordWorkshopModSource(source, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(session.WorkshopModSources) != 2 || session.WorkshopModSources[1].ResolutionSHA256 != strings.Repeat("c", 64) {
		t.Fatalf("sources = %#v", session.WorkshopModSources)
	}
	if session.WorkshopModSources[0].AcceptedItems[0].Title != "" {
		t.Fatal("hot session projection retained an untrusted Workshop title")
	}
}

func TestAttachWorkshopModSourceActivatesInitialDraftRevisionWithProvenance(t *testing.T) {
	now := time.Now().UTC()
	session, _ := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
	source, metadata := workshopModArtifacts(now, "a", "b")
	revision, err := session.AttachWorkshopModSource(source, 0, metadata, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if revision.Status != PresetRevisionActive || session.PresetObjectKey != source.PresetObjectKey || revision.WorkshopSourceID != source.Source.PublishedFileID || revision.WorkshopResolutionSHA256 != source.ResolutionSHA256 {
		t.Fatalf("revision = %#v, session = %#v", revision, session)
	}
}

func TestAttachWorkshopModSourceStagesEstablishedRevisionAndRejectsStaleOrImplicitRefresh(t *testing.T) {
	now := time.Now().UTC()
	session, _ := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
	activeSource, activeMetadata := workshopModArtifacts(now, "a", "b")
	if _, err := session.AttachWorkshopModSource(activeSource, 0, activeMetadata, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	session.LifecycleState, session.DesiredState, session.ObservedState = StateRunning, StateRunning, StateRunning
	nextSource, nextMetadata := workshopModArtifacts(now.Add(2*time.Minute), "c", "d")
	nextSource.Source = WorkshopReference{PublishedFileID: 30, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=30"}
	nextSource.AcceptedItems[0].PublishedFileID = 30
	revision, err := session.AttachWorkshopModSource(nextSource, 1, nextMetadata, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if revision.Status != PresetRevisionPending || revision.BaseRevision != 1 || session.PresetObjectKey != activeSource.PresetObjectKey {
		t.Fatalf("revision = %#v, active key = %q", revision, session.PresetObjectKey)
	}
	thirdSource, thirdMetadata := workshopModArtifacts(now.Add(3*time.Minute), "e", "f")
	thirdSource.Source = nextSource.Source
	thirdSource.AcceptedItems[0].PublishedFileID = 30
	if _, err := session.AttachWorkshopModSource(thirdSource, 1, thirdMetadata, now.Add(3*time.Minute)); err == nil {
		t.Fatal("implicit collection refresh replaced an outstanding pending revision")
	}
	if _, err := session.AttachWorkshopModSource(thirdSource, 0, thirdMetadata, now.Add(3*time.Minute)); err == nil {
		t.Fatal("stale active revision was accepted")
	}
}

func workshopModArtifacts(now time.Time, resolutionByte, artifactByte string) (WorkshopModSource, PresetModlistMetadata) {
	resolution, artifact := strings.Repeat(resolutionByte, 64), strings.Repeat(artifactByte, 64)
	source := WorkshopModSource{Source: WorkshopReference{PublishedFileID: 20, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=20"}, SourceKind: WorkshopSourceItem, ResolutionSHA256: resolution, AcceptedItems: []WorkshopModItem{{PublishedFileID: 20, Title: "Mod"}}, ResolvedAt: now.UTC(), PresetObjectKey: "sessions/session-1/input/presets/" + artifact + "-workshop.html", ModlistObjectKey: "sessions/session-1/input/modlists/" + artifact + "/modlist.html", ManifestObjectKey: "sessions/session-1/input/workshop-sources/" + resolution + ".json", ArtifactSHA256: artifact}
	return source, PresetModlistMetadata{ObjectKey: source.ModlistObjectKey, Filename: "session-modlist.html", SHA256: artifact, SizeBytes: 100, WorkshopCount: 1}
}
