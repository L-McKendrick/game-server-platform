package dynamodbstore

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
)

func TestLegacyPresetWorkshopRecoveryRequiresUniqueExactTrustedSource(t *testing.T) {
	now := time.Date(2026, 9, 18, 20, 0, 0, 0, time.UTC)
	source := domain.WorkshopModSource{Source: domain.WorkshopReference{PublishedFileID: 20, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=20"}, SourceKind: domain.WorkshopSourceItem, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItems: []domain.WorkshopModItem{{PublishedFileID: 20}}, ResolvedAt: now, PresetObjectKey: "sessions/session-1/input/presets/" + strings.Repeat("b", 64) + "-a.html", ModlistObjectKey: "sessions/session-1/input/modlists/" + strings.Repeat("b", 64) + "/a.html", ManifestObjectKey: "sessions/session-1/input/workshop-sources/" + strings.Repeat("a", 64) + ".json", ArtifactSHA256: strings.Repeat("b", 64)}
	for _, scenario := range []string{"active", "pending", "missing", "unrelated", "ambiguous", "explicit", "partial", "ordinary"} {
		t.Run(scenario, func(t *testing.T) {
			session := testSession(t, now)
			session.PresetObjectKey = source.PresetObjectKey
			session.PresetRevisionSequence = 2
			session.ActivePresetRevision = domain.PresetRevision{Number: 1, PresetObjectKey: source.PresetObjectKey, Status: domain.PresetRevisionActive, StagedAt: now, ActivatedAt: now}
			session.WorkshopModSources = []domain.WorkshopModSource{source}
			if scenario == "pending" {
				session.ActivePresetRevision.PresetObjectKey = "sessions/session-1/input/presets/ordinary.html"
				session.PresetObjectKey = session.ActivePresetRevision.PresetObjectKey
				session.PendingPresetRevision = domain.PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: source.PresetObjectKey, Status: domain.PresetRevisionPending, StagedAt: now}
			}
			if scenario == "missing" {
				session.WorkshopModSources = nil
			}
			if scenario == "unrelated" {
				session.ActivePresetRevision.PresetObjectKey = "sessions/session-1/input/presets/ordinary.html"
				session.PresetObjectKey = session.ActivePresetRevision.PresetObjectKey
			}
			if scenario == "ambiguous" || scenario == "explicit" {
				other := source
				other.ResolutionSHA256 = strings.Repeat("c", 64)
				other.ManifestObjectKey = "sessions/session-1/input/workshop-sources/" + other.ResolutionSHA256 + ".json"
				session.WorkshopModSources = append(session.WorkshopModSources, other)
			}
			if scenario == "explicit" || scenario == "partial" {
				session.ActivePresetRevision.WorkshopResolutionSHA256 = strings.Repeat("d", 64)
				if scenario == "explicit" {
					session.ActivePresetRevision.WorkshopSourceID = 40
				}
			}
			item := toSessionItem(session)
			if scenario != "explicit" && scenario != "partial" && scenario != "ordinary" {
				item.ActivePresetWorkshopSourceID, item.PendingPresetWorkshopSourceID = nil, nil
			}
			attributes, err := attributevalue.MarshalMap(item)
			if err != nil {
				t.Fatal(err)
			}
			var decoded sessionItem
			if err := attributevalue.UnmarshalMap(attributes, &decoded); err != nil {
				t.Fatal(err)
			}
			stored, err := fromSessionItem(decoded)
			if scenario == "ambiguous" {
				if !errors.Is(err, domain.ErrConflict) {
					t.Fatal("ambiguous source was guessed", err)
				}
				return
			}
			if scenario == "partial" {
				if err == nil {
					t.Fatal("partial explicit identity was repaired instead of rejected")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			revision := stored.ActivePresetRevision
			if scenario == "pending" {
				revision = stored.PendingPresetRevision
			}
			if scenario == "missing" || scenario == "unrelated" || scenario == "ordinary" {
				if revision.WorkshopResolutionSHA256 != "" || revision.WorkshopSourceID != 0 {
					t.Fatal("unbound source inferred")
				}
			} else if scenario == "explicit" {
				if revision != session.ActivePresetRevision {
					t.Fatal("explicit source replaced")
				}
			} else if revision.WorkshopResolutionSHA256 != source.ResolutionSHA256 || revision.WorkshopSourceID != source.Source.PublishedFileID {
				t.Fatal("exact source was not recovered")
			}
		})
	}
}
