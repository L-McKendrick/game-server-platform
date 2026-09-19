package app

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// Match the shared bootstrap script's preset extraction, including legacy HTML.
var hostPresetItem = regexp.MustCompile(`(?i)id=[0-9]+|data-publishedfileid=["'][0-9]+`)

func hostPresetIDs(body []byte) ([]string, error) {
	seen := make(map[string]bool)
	var ids []string
	for _, match := range hostPresetItem.FindAllString(string(body), -1) {
		id := match[strings.LastIndexAny(match, "=\"'")+1:]
		parsed, err := strconv.ParseUint(id, 10, 64)
		if err != nil || parsed == 0 || id != strconv.FormatUint(parsed, 10) {
			return nil, fmt.Errorf("Workshop preset item identity invalid")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
		if len(ids) > domain.MaximumWorkshopModItems {
			return nil, fmt.Errorf("Workshop preset item bound exceeded")
		}
	}
	return ids, nil
}

func (delivery HostCommandDelivery) workshopExpectedItems(ctx context.Context, scope domain.HostAccessScope, objects []domain.HostObjectRequest, environment map[string]string) (string, []hostWorkshopResultItem, error) {
	target := environment["HOST_WORKSHOP_TARGET_B64"]
	if target == "" {
		target = "all"
	}
	if target != "all" && target != "mission" && target != "mods" {
		return "", nil, domain.ErrConflict
	}
	var expected []hostWorkshopResultItem
	for _, object := range objects {
		if object.Purpose == domain.HostObjectWorkshopMission {
			if target == "mods" {
				return "", nil, domain.ErrConflict
			}
			expected = append(expected, hostWorkshopResultItem{Target: "mission", PublishedFileID: strings.TrimPrefix(object.Slot, "mission-"), Revision: environment["WORKSHOP_MISSION_REVISION_B64"], Status: "succeeded"})
		}
	}
	if target == "mission" || environment["VANILLA_MODE_B64"] == "true" {
		return target, expected, nil
	}
	if delivery.WorkshopStore == nil {
		return "", nil, fmt.Errorf("Workshop trusted storage unavailable")
	}
	clientIDs := make(map[string]bool)
	modCount := 0
	// Client-first ordering matches the host's server-only duplicate filtering.
	for _, purpose := range []domain.HostObjectPurpose{domain.HostObjectClientPreset, domain.HostObjectServerPreset} {
		for _, object := range objects {
			if object.Purpose != purpose {
				continue
			}
			if _, _, err := delivery.Issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC()); err != nil {
				return "", nil, err
			}
			body, pin, err := delivery.WorkshopStore.ReadHostOutput(ctx, object.Key, object.VersionID, "", 10*1024*1024)
			if err != nil || pin.SHA256 != object.SHA256 || pin.SizeBytes > object.MaxBytes {
				return "", nil, fmt.Errorf("Workshop accepted preset digest or bounds changed")
			}
			if _, _, err := delivery.Issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC()); err != nil {
				return "", nil, err
			}
			ids, err := hostPresetIDs(body)
			if err != nil {
				return "", nil, err
			}
			revision, itemTarget := environment["PRESET_REVISION_B64"], "mod"
			if purpose == domain.HostObjectClientPreset && environment["WORKSHOP_MOD_RESOLUTION_B64"] != "" {
				revision = environment["WORKSHOP_MOD_RESOLUTION_B64"]
			}
			if purpose == domain.HostObjectServerPreset {
				revision, itemTarget = environment["SERVER_PRESET_REVISION_B64"], "server_mod"
			}
			for _, id := range ids {
				if purpose == domain.HostObjectServerPreset && clientIDs[id] {
					continue
				}
				if purpose == domain.HostObjectClientPreset {
					clientIDs[id] = true
				}
				modCount++
				if modCount > domain.MaximumWorkshopModItems {
					return "", nil, fmt.Errorf("Workshop combined mod item bound exceeded")
				}
				expected = append(expected, hostWorkshopResultItem{Target: itemTarget, PublishedFileID: id, Revision: revision, Status: "succeeded"})
			}
		}
	}
	return target, expected, nil
}
