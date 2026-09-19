package app

import (
	"strings"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestWorkshopResolutionRequiresExactApprovedVerifiedPayloadSet(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong-session-key", "wrong-name", "wrong-digest", "wrong-size", "wrong-item", "duplicate", "missing-item", "missing-pin", "null-version", "missing-metadata", "conflicting-source", "unapproved-source", "noncanonical-id"} {
		t.Run(scenario, func(t *testing.T) {
			digest := strings.Repeat("a", 64)
			key := "sessions/session/input/missions/" + digest + "-scenario.pbo"
			body := digest + "\tscenario.pbo\t" + key + "\t42\n"
			item := domain.WorkshopMissionItem{PublishedFileID: 42, Filename: "scenario.pbo", FileSize: 16}
			session := domain.Session{ID: "session", WorkshopMissionSources: []domain.WorkshopMissionSource{{AcceptedItemIDs: []uint64{42}, AcceptedItems: []domain.WorkshopMissionItem{item}}}}
			pin := domain.HostOutputVersion{VersionID: "verified-v1", SHA256: hostBase64Digest(digest), SizeBytes: 16}
			pins := map[string]domain.HostOutputVersion{"mission-42": pin}
			expected := []uint64{42}
			switch scenario {
			case "wrong-session-key":
				body = strings.ReplaceAll(body, "sessions/session/", "sessions/other/")
			case "wrong-name":
				body = strings.ReplaceAll(body, "scenario.pbo", "other.pbo")
			case "wrong-digest":
				body = strings.ReplaceAll(body, digest, strings.Repeat("b", 64))
			case "wrong-size":
				pin.SizeBytes++
			case "wrong-item":
				body = strings.ReplaceAll(body, "\t42\n", "\t43\n")
			case "duplicate":
				body += body
			case "missing-item":
				expected = append(expected, 43)
				extra := item
				extra.PublishedFileID = 43
				extra.Filename = "extra.pbo"
				session.WorkshopMissionSources[0].AcceptedItemIDs = append(session.WorkshopMissionSources[0].AcceptedItemIDs, 43)
				session.WorkshopMissionSources[0].AcceptedItems = append(session.WorkshopMissionSources[0].AcceptedItems, extra)
			case "missing-pin":
				delete(pins, "mission-42")
			case "null-version":
				pin.VersionID = "null"
			case "missing-metadata":
				session.WorkshopMissionSources[0].AcceptedItems = nil
			case "conflicting-source":
				item.FileSize++
				session.WorkshopMissionSources = append(session.WorkshopMissionSources, domain.WorkshopMissionSource{AcceptedItemIDs: []uint64{42}, AcceptedItems: []domain.WorkshopMissionItem{item}})
			case "unapproved-source":
				session.WorkshopMissionSources[0].AcceptedItemIDs = []uint64{43}
			case "noncanonical-id":
				body = strings.ReplaceAll(body, "\t42\n", "\t042\n")
			}
			if scenario != "missing-pin" {
				pins["mission-42"] = pin
			}
			keys, err := validatedWorkshopMissionKeys(session, expected, []byte(body), pins)
			if scenario == "valid" {
				if err != nil || keys["mission-42"] != key || len(keys) != 1 {
					t.Fatal("valid resolution rejected", err)
				}
			} else if err == nil || keys != nil {
				t.Fatal("invalid resolution accepted")
			}
		})
	}
}

func TestWorkshopResolutionRejectsCaseCollidingNames(t *testing.T) {
	digest := strings.Repeat("a", 64)
	session := domain.Session{ID: "session", WorkshopMissionSources: []domain.WorkshopMissionSource{{AcceptedItemIDs: []uint64{42, 43}, AcceptedItems: []domain.WorkshopMissionItem{{PublishedFileID: 42, Filename: "scenario.pbo", FileSize: 16}, {PublishedFileID: 43, Filename: "SCENARIO.pbo", FileSize: 16}}}}}
	pin := domain.HostOutputVersion{VersionID: "verified", SHA256: hostBase64Digest(digest), SizeBytes: 16}
	body := digest + "\tscenario.pbo\tsessions/session/input/missions/" + digest + "-scenario.pbo\t42\n" + digest + "\tSCENARIO.pbo\tsessions/session/input/missions/" + digest + "-SCENARIO.pbo\t43\n"
	if keys, err := validatedWorkshopMissionKeys(session, []uint64{42, 43}, []byte(body), map[string]domain.HostOutputVersion{"mission-42": pin, "mission-43": pin}); err == nil || keys != nil {
		t.Fatal("case-colliding deployment accepted")
	}
}
