package domain

import (
	"strings"
	"testing"
	"time"
)

func TestCompleteWakeWithWorkshopMissionsIsOneAtomicMutation(t *testing.T) {
	now := time.Date(2026, 9, 1, 5, 0, 0, 0, time.UTC)
	source := WorkshopMissionSource{Source: WorkshopReference{PublishedFileID: 100, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=100"}, SourceKind: WorkshopSourceItem, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{100}, ResolvedAt: now}
	session := bootstrappingSessionWithWorkshopSource(t, now, source)
	if err := session.AcquireBootstrapWorkflowLock("bootstrap", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginBootstrapInstallation("bootstrap", now); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteBootstrap("bootstrap", now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginSleep("sleep", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteSleep("sleep", now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginWake("wake", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	before := session.Version
	mission := MissionRecord{ObjectKey: "sessions/session-1/input/missions/" + strings.Repeat("b", 64) + "-Wake.Altis.pbo", Filename: "Wake.Altis.pbo", Status: ArtifactAccepted, WorkshopItemID: 100, WorkshopSources: []WorkshopReference{source.Source}}
	if err := session.CompleteWakeWithWorkshopMissions("wake", "203.0.113.20", []MissionRecord{mission}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.Version != before+1 || session.LifecycleState != StateRunning || session.ActiveWorkflowID != "" || session.Infrastructure.PublicIPv4 != "203.0.113.20" {
		t.Fatalf("session = %#v", session)
	}
	if got := session.MissionFiles[len(session.MissionFiles)-1]; got.WorkshopItemID != 100 || got.Filename != mission.Filename {
		t.Fatalf("mission = %#v", got)
	}
}
