package app

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// validatedWorkshopMissionKeys derives durable destinations only after every
// resolution row matches approved metadata and independently hashed staged PBOs.
// expected comes from the trusted fixed-slot command inventory, not host rows.
func validatedWorkshopMissionKeys(session domain.Session, expected []uint64, body []byte, pins map[string]domain.HostOutputVersion) (map[string]string, error) {
	if len(expected) == 0 || len(expected) > domain.MaximumWorkshopMissionItems || len(body) == 0 || len(body) > domain.MaxHostResultBytes {
		return nil, fmt.Errorf("Workshop resolution inventory invalid")
	}
	approved := make(map[uint64]domain.WorkshopMissionItem, len(expected))
	for _, id := range expected {
		if id == 0 {
			return nil, domain.ErrConflict
		}
		if _, duplicate := approved[id]; duplicate {
			return nil, domain.ErrConflict
		}
		var metadata domain.WorkshopMissionItem
		for _, source := range session.WorkshopMissionSources {
			accepted := false
			for _, approvedID := range source.AcceptedItemIDs {
				if approvedID == id {
					accepted = true
					break
				}
			}
			if !accepted {
				continue
			}
			for _, item := range source.AcceptedItems {
				if item.PublishedFileID != id {
					continue
				}
				if metadata.PublishedFileID != 0 && metadata != item {
					return nil, fmt.Errorf("Workshop source metadata conflicts")
				}
				metadata = item
			}
		}
		name, err := domain.NormalizeMissionFilename(metadata.Filename)
		if metadata.PublishedFileID != id || err != nil || name != metadata.Filename || metadata.FileSize < 16 || metadata.FileSize > domain.MaximumWorkshopMissionBytes {
			return nil, fmt.Errorf("Workshop source deployment metadata missing or invalid")
		}
		metadata.Filename = strings.TrimSuffix(name, path.Ext(name)) + ".pbo"
		approved[id] = metadata
	}
	destinations := make(map[string]string, len(expected))
	names := make(map[string]bool, len(expected))
	for _, line := range strings.Split(strings.TrimSuffix(string(body), "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			return nil, fmt.Errorf("Workshop resolution row malformed")
		}
		checksum, filename, claimedKey := fields[0], fields[1], fields[2]
		id, err := strconv.ParseUint(fields[3], 10, 64)
		if err != nil || fields[3] != strconv.FormatUint(id, 10) {
			return nil, domain.ErrConflict
		}
		metadata, accepted := approved[id]
		slot := "mission-" + fields[3]
		pin, verified := pins[slot]
		digest, err := base64.StdEncoding.DecodeString(pin.SHA256)
		if !accepted || !verified || err != nil || len(digest) != 32 || checksum != hex.EncodeToString(digest) || filename != metadata.Filename || pin.SizeBytes != metadata.FileSize || pin.VersionID == "" || pin.VersionID == "null" {
			return nil, fmt.Errorf("Workshop resolution differs from verified approved payload")
		}
		if _, duplicate := destinations[slot]; duplicate || names[strings.ToLower(filename)] {
			return nil, fmt.Errorf("Workshop resolution duplicate item or filename")
		}
		destination := path.Join("sessions", session.ID, "input", "missions", checksum+"-"+filename)
		if claimedKey != destination {
			return nil, fmt.Errorf("Workshop resolution destination unauthorized")
		}
		destinations[slot] = destination
		names[strings.ToLower(filename)] = true
	}
	if len(destinations) != len(expected) {
		return nil, fmt.Errorf("Workshop resolution omitted approved item")
	}
	return destinations, nil
}
