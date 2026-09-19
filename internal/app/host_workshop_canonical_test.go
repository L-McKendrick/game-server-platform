package app

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestCanonicalWorkshopResolutionMatchesFullAndPendingUploads(t *testing.T) {
	now := time.Now().UTC()
	firstKey := "sessions/session/input/missions/" + strings.Repeat("a", 64) + "-first.pbo"
	secondKey := "sessions/session/input/missions/" + strings.Repeat("b", 64) + "-second.pbo"
	for _, scenario := range []string{"valid", "removed", "stale-record", "foreign-record", "extra-upload", "missing-upload", "conflicting-metadata", "wrong-provenance"} {
		t.Run(scenario, func(t *testing.T) {
			session := domain.Session{ID: "session", WorkshopMissionSources: []domain.WorkshopMissionSource{{AcceptedItemIDs: []uint64{43, 42}, AcceptedItems: []domain.WorkshopMissionItem{{PublishedFileID: 43, Filename: "second.pbo", FileSize: 16}, {PublishedFileID: 42, Filename: "first.pbo", FileSize: 16}}, ResolvedAt: now}}, MissionFiles: []domain.MissionRecord{{WorkshopItemID: 42, Filename: "first.pbo", ObjectKey: firstKey, Status: domain.ArtifactAccepted, AddedAt: now.Add(time.Second)}}}
			pending := map[string]string{"mission-43": secondKey}
			session.WorkshopMissionSources[0].Source = domain.WorkshopReference{PublishedFileID: 100, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=100"}
			session.MissionFiles[0].WorkshopSources = []domain.WorkshopReference{session.WorkshopMissionSources[0].Source}
			switch scenario {
			case "wrong-provenance":
				session.MissionFiles[0].WorkshopSources = nil
			case "removed":
				session.MissionFiles[0].RemovedAt = now.Add(2 * time.Second)
			case "stale-record":
				session.MissionFiles[0].AddedAt = now.Add(-time.Second)
			case "foreign-record":
				session.MissionFiles[0].ObjectKey = strings.Replace(firstKey, "sessions/session/", "sessions/other/", 1)
			case "extra-upload":
				pending["mission-44"] = secondKey
			case "missing-upload":
				delete(pending, "mission-43")
			case "conflicting-metadata":
				session.WorkshopMissionSources = append(session.WorkshopMissionSources, domain.WorkshopMissionSource{AcceptedItemIDs: []uint64{42}, AcceptedItems: []domain.WorkshopMissionItem{{PublishedFileID: 42, Filename: "first.pbo", FileSize: 17}}, ResolvedAt: now})
			}
			body, err := canonicalWorkshopResolution(session, pending)
			if scenario != "valid" && scenario != "removed" {
				if err == nil || body != nil {
					t.Fatal("invalid canonical resolution accepted")
				}
				return
			}
			full, fullErr := canonicalWorkshopResolution(session, map[string]string{"mission-42": firstKey, "mission-43": secondKey})
			want := strings.Repeat("a", 64) + "\tfirst.pbo\t" + firstKey + "\t42\n" + strings.Repeat("b", 64) + "\tsecond.pbo\t" + secondKey + "\t43\n"
			if err != nil || fullErr != nil || !bytes.Equal(body, full) || string(body) != want {
				t.Fatal("full/pending canonical identity changed", err, fullErr)
			}
		})
	}
}
