// Package workshopmanifest validates the immutable mission result produced by
// the shared host synchronization path. Bootstrap and live synchronization use
// this one parser so they attach identical records and provenance.
package workshopmanifest

import (
	"context"
	"encoding/hex"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

func Load(ctx context.Context, reader ports.ObjectReader, session domain.Session) ([]domain.MissionRecord, error) {
	ids := session.PendingWorkshopMissionItemIDs()
	if len(ids) == 0 {
		return nil, nil
	}
	if reader == nil {
		return nil, fmt.Errorf("Workshop mission manifest reader is required")
	}
	revision, err := session.WorkshopMissionRevision()
	if err != nil {
		return nil, fmt.Errorf("resolve Workshop mission revision: %w", err)
	}
	manifestKey := path.Join("sessions", session.ID, "workshop-resolutions", revision+".tsv")
	body, err := reader.Get(ctx, manifestKey)
	if err != nil {
		return nil, fmt.Errorf("read Workshop mission manifest: %w", err)
	}
	if len(body) == 0 || len(body) > domain.MaxHostResultBytes {
		return nil, fmt.Errorf("Workshop mission manifest exceeds control-document bounds")
	}
	approved := make(map[uint64]bool)
	for _, id := range session.WorkshopMissionItemIDs() {
		if id == 0 {
			return nil, fmt.Errorf("Workshop mission approved identity invalid")
		}
		approved[id] = true
	}
	if len(approved) > domain.MaximumWorkshopMissionItems {
		return nil, fmt.Errorf("Workshop mission approved inventory exceeds bound")
	}
	pending := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		pending[id] = true
	}
	seen := map[uint64]bool{}
	names := map[string]bool{}
	missions := make([]domain.MissionRecord, 0, len(ids))
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		fields := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(fields) != 4 {
			return nil, fmt.Errorf("Workshop mission manifest is malformed")
		}
		digest, filename, objectKey := fields[0], fields[1], fields[2]
		itemID, parseErr := strconv.ParseUint(fields[3], 10, 64)
		if parseErr != nil || itemID == 0 || fields[3] != strconv.FormatUint(itemID, 10) || !approved[itemID] || seen[itemID] || len(digest) != 64 || digest != strings.ToLower(digest) {
			return nil, fmt.Errorf("Workshop mission manifest identity is invalid")
		}
		if _, decodeErr := hex.DecodeString(digest); decodeErr != nil {
			return nil, fmt.Errorf("Workshop mission checksum is invalid")
		}
		normalized, normalizeErr := domain.NormalizeMissionFilename(filename)
		expectedKey := path.Join("sessions", session.ID, "input", "missions", digest+"-"+filename)
		sources := session.WorkshopSourcesForItem(itemID)
		if normalizeErr != nil || normalized != filename || objectKey != expectedKey || len(sources) == 0 || names[strings.ToLower(filename)] {
			return nil, fmt.Errorf("Workshop mission manifest entry is unauthorized")
		}
		// A canonical resolution may include previously materialized approved
		// items. Selecting pending intent here preserves removals and avoids
		// reattaching every row during restart or source refresh.
		if pending[itemID] {
			missions = append(missions, domain.MissionRecord{ObjectKey: objectKey, Filename: filename, Status: domain.ArtifactAccepted, WorkshopItemID: itemID, WorkshopSources: sources})
		}
		seen[itemID] = true
		names[strings.ToLower(filename)] = true
	}
	if len(missions) != len(ids) {
		return nil, fmt.Errorf("Workshop mission manifest is incomplete")
	}
	for _, id := range ids {
		if !seen[id] {
			return nil, fmt.Errorf("Workshop mission manifest omitted an accepted item")
		}
	}
	return missions, nil
}
