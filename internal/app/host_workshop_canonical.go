package app

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// canonicalWorkshopResolution gives full and pending-only command uploads the
// same sorted durable representation. New keys must already be byte-verified.
// Omitted items can come only from current trusted materialization records;
// removed records remain evidence without becoming new attachment intent.
func canonicalWorkshopResolution(session domain.Session, newKeys map[string]string) ([]byte, error) {
	ids := session.WorkshopMissionItemIDs()
	if len(ids) == 0 || len(ids) > domain.MaximumWorkshopMissionItems {
		return nil, fmt.Errorf("Workshop canonical inventory invalid")
	}
	pending := make(map[uint64]bool)
	for _, id := range session.PendingWorkshopMissionItemIDs() {
		pending[id] = true
	}
	used := 0
	names := make(map[string]bool)
	var body strings.Builder
	for _, id := range ids {
		var metadata domain.WorkshopMissionItem
		var resolvedAt time.Time
		for _, source := range session.WorkshopMissionSources {
			accepted := false
			for _, approved := range source.AcceptedItemIDs {
				if approved == id {
					accepted = true
					break
				}
			}
			if !accepted {
				continue
			}
			if source.ResolvedAt.After(resolvedAt) {
				resolvedAt = source.ResolvedAt
			}
			for _, item := range source.AcceptedItems {
				if item.PublishedFileID != id {
					continue
				}
				if metadata.PublishedFileID != 0 && metadata != item {
					return nil, fmt.Errorf("Workshop canonical source metadata conflicts")
				}
				metadata = item
			}
		}
		filename, err := domain.NormalizeMissionFilename(metadata.Filename)
		if id == 0 || metadata.PublishedFileID != id || err != nil || filename != metadata.Filename || metadata.FileSize < 16 || metadata.FileSize > domain.MaximumWorkshopMissionBytes || resolvedAt.IsZero() || names[strings.ToLower(filename)] {
			return nil, fmt.Errorf("Workshop canonical metadata invalid")
		}
		slot := "mission-" + strconv.FormatUint(id, 10)
		key, newlyVerified := newKeys[slot]
		if newlyVerified {
			used++
		} else {
			if pending[id] {
				return nil, fmt.Errorf("Workshop canonical pending item not verified")
			}
			var selectedAt time.Time
			for _, record := range session.MissionFiles {
				if record.WorkshopItemID != id || record.Status != domain.ArtifactAccepted || record.AddedAt.Before(resolvedAt) || record.Filename != filename {
					continue
				}
				provenanceMatches := len(record.WorkshopSources) > 0
				for _, source := range session.WorkshopSourcesForItem(id) {
					if !slices.Contains(record.WorkshopSources, source) {
						provenanceMatches = false
						break
					}
				}
				if !provenanceMatches {
					continue
				}
				if record.AddedAt.After(selectedAt) {
					key, selectedAt = record.ObjectKey, record.AddedAt
				} else if record.AddedAt.Equal(selectedAt) && key != record.ObjectKey {
					return nil, fmt.Errorf("Workshop canonical materialization conflicts")
				}
			}
		}
		name := path.Base(key)
		if len(name) != 65+len(filename) || name[64:] != "-"+filename {
			return nil, fmt.Errorf("Workshop canonical accepted key invalid")
		}
		digest := name[:64]
		if hostBase64Digest(digest) == "" || digest != strings.ToLower(digest) || key != path.Join("sessions", session.ID, "input", "missions", digest+"-"+filename) {
			return nil, fmt.Errorf("Workshop canonical destination unauthorized")
		}
		fmt.Fprintf(&body, "%s\t%s\t%s\t%d\n", digest, filename, key, id)
		names[strings.ToLower(filename)] = true
	}
	if used != len(newKeys) || body.Len() > domain.MaxHostResultBytes {
		return nil, fmt.Errorf("Workshop canonical output inventory invalid")
	}
	return []byte(body.String()), nil
}
