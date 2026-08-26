package domain

import (
	"testing"
	"time"
)

func TestParseWorkshopURLCanonicalizesItem(t *testing.T) {
	reference, err := ParseWorkshopURL("https://steamcommunity.com/sharedfiles/filedetails/?id=12345")
	if err != nil || reference.PublishedFileID != 12345 || reference.CanonicalURL != "https://steamcommunity.com/sharedfiles/filedetails/?id=12345" {
		t.Fatalf("ParseWorkshopURL() = %#v, %v", reference, err)
	}
	for _, invalid := range []string{
		"http://steamcommunity.com/sharedfiles/filedetails/?id=12345",
		"https://example.com/sharedfiles/filedetails/?id=12345",
		"https://steamcommunity.com/sharedfiles/filedetails/?id=12345&searchtext=x",
	} {
		if _, err := ParseWorkshopURL(invalid); err == nil {
			t.Fatalf("ParseWorkshopURL(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestClassifyWorkshopItemUsesTarget(t *testing.T) {
	scenario := WorkshopItem{PublishedFileID: 1, ConsumerAppID: Arma3WorkshopAppID, Available: true, Tags: []string{"Scenario", "Coop"}}
	if got := ClassifyWorkshopItem(scenario, WorkshopTargetMission); got.Class != WorkshopItemMultiplayerScenario || !got.MatchesTarget {
		t.Fatalf("scenario classification = %#v", got)
	}
	if got := ClassifyWorkshopItem(scenario, WorkshopTargetMods); got.MatchesTarget || got.Issue == "" {
		t.Fatalf("scenario mod-target classification = %#v", got)
	}
	mod := WorkshopItem{PublishedFileID: 2, ConsumerAppID: Arma3WorkshopAppID, Available: true, Tags: []string{"Mod"}}
	if got := ClassifyWorkshopItem(mod, WorkshopTargetMods); got.Class != WorkshopItemClientMod || !got.MatchesTarget {
		t.Fatalf("mod classification = %#v", got)
	}
}

func TestWorkshopResolutionFinalizeIsDeterministic(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	resolution := WorkshopResolution{
		SchemaVersion: 1, Target: WorkshopTargetMods, SourceKind: WorkshopSourceCollection,
		Source: WorkshopReference{PublishedFileID: 9},
		Items: []WorkshopItem{
			{PublishedFileID: 2, ConsumerAppID: Arma3WorkshopAppID, Class: WorkshopItemClientMod, MatchesTarget: true, Tags: []string{"Mod"}},
			{PublishedFileID: 1, ConsumerAppID: Arma3WorkshopAppID, Class: WorkshopItemScenario, Tags: []string{"Scenario"}},
		},
	}
	if err := resolution.Finalize(now); err != nil {
		t.Fatal(err)
	}
	if resolution.Items[0].PublishedFileID != 1 || len(resolution.ResolutionSHA256) != 64 || !resolution.ResolvedAt.Equal(now) {
		t.Fatalf("finalized resolution = %#v", resolution)
	}
}
