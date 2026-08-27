package bootstrap

import (
	"context"
	"encoding/hex"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func (service *Service) importWorkshopMissions(ctx context.Context, session *domain.Session, now time.Time) error {
	ids := session.WorkshopMissionItemIDs()
	if len(ids) == 0 {
		return nil
	}
	if service.workshopMissionManifest == nil {
		return fmt.Errorf("Workshop mission manifest reader is required")
	}
	revision, err := session.WorkshopMissionRevision()
	if err != nil {
		return fmt.Errorf("resolve Workshop mission revision: %w", err)
	}
	manifestKey := path.Join("sessions", session.ID, "workshop-resolutions", revision+".tsv")
	body, err := service.workshopMissionManifest.Get(ctx, manifestKey)
	if err != nil {
		return fmt.Errorf("read Workshop mission manifest: %w", err)
	}
	seen := map[uint64]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		fields := strings.Split(strings.TrimSuffix(line, "\r"), "\t")
		if len(fields) != 4 {
			return fmt.Errorf("Workshop mission manifest is malformed")
		}
		digest, filename, objectKey := fields[0], fields[1], fields[2]
		itemID, parseErr := strconv.ParseUint(fields[3], 10, 64)
		if parseErr != nil || itemID == 0 || seen[itemID] || len(digest) != 64 {
			return fmt.Errorf("Workshop mission manifest identity is invalid")
		}
		if _, decodeErr := hex.DecodeString(digest); decodeErr != nil {
			return fmt.Errorf("Workshop mission checksum is invalid")
		}
		normalized, normalizeErr := domain.NormalizeMissionFilename(filename)
		expectedKey := path.Join("sessions", session.ID, "input", "missions", digest+"-"+filename)
		sources := session.WorkshopSourcesForItem(itemID)
		if normalizeErr != nil || normalized != filename || objectKey != expectedKey || len(sources) == 0 {
			return fmt.Errorf("Workshop mission manifest entry is unauthorized")
		}
		if err := session.AttachWorkshopMission(domain.MissionRecord{ObjectKey: objectKey, Filename: filename, Status: domain.ArtifactAccepted, WorkshopItemID: itemID, WorkshopSources: sources}, now); err != nil {
			return err
		}
		seen[itemID] = true
	}
	if len(seen) != len(ids) {
		return fmt.Errorf("Workshop mission manifest is incomplete")
	}
	for _, id := range ids {
		if !seen[id] {
			return fmt.Errorf("Workshop mission manifest omitted an accepted item")
		}
	}
	return nil
}
