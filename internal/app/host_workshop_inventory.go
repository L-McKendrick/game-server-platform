package app

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// WorkflowWorkshopInventory fixes output slots from accepted operation intent.
// It cannot authorize a host-selected key, item or content-addressed namespace.
func (issuer HostObjectIssuer) WorkflowWorkshopInventory(ctx context.Context, scope domain.HostAccessScope) ([]domain.HostObjectRequest, error) {
	session, workflow, err := issuer.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if issuer.Rollback != (scope.CommandMode == "rollback") {
		return nil, domain.ErrConflict
	}
	switch workflow.Type {
	case domain.BootstrapWorkflowType, domain.WakeWorkflowType, domain.RestoreWorkflowType, domain.RestartWorkflowType, domain.WorkshopContentSyncWorkflowType:
	default:
		return nil, fmt.Errorf("Workshop output workflow unsupported")
	}
	if workflow.Type == domain.RestartWorkflowType && !session.HasApplyingPresetRevision(workflow.ID) && len(session.PendingWorkshopMissionItemIDs()) == 0 {
		return nil, nil
	}
	ids := make(map[uint64]bool)
	if workflow.Type == domain.WorkshopContentSyncWorkflowType && workflow.ContentTarget == string(domain.WorkshopTargetMods) {
		// Mod-only synchronization must not receive mission upload slots even
		// when unrelated mission intent remains pending on the session.
	} else if !issuer.Rollback && (workflow.Type == domain.RestartWorkflowType || workflow.Type == domain.WorkshopContentSyncWorkflowType) {
		for _, id := range session.PendingWorkshopMissionItemIDs() {
			if id > 0 {
				ids[id] = true
			}
		}
	} else {
		for _, source := range session.WorkshopMissionSources {
			for _, id := range source.AcceptedItemIDs {
				if id > 0 {
					ids[id] = true
				}
			}
		}
	}
	if len(ids) > domain.MaximumWorkshopMissionItems {
		return nil, fmt.Errorf("Workshop output item bound exceeded")
	}
	ordered := make([]uint64, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	objects := []domain.HostObjectRequest{{Purpose: domain.HostObjectWorkshopResult, Slot: "workshop-result", Key: scope.StagingKey("workshop-result"), ContentType: "application/json", MinBytes: 1, MaxBytes: domain.MaxHostResultBytes}}
	for _, id := range ordered {
		slot := "mission-" + strconv.FormatUint(id, 10)
		minimum, maximum := int64(16), domain.MaximumWorkshopMissionBytes
		var resolvedSize int64
		for _, source := range session.WorkshopMissionSources {
			for _, item := range source.AcceptedItems {
				if item.PublishedFileID != id {
					continue
				}
				if item.FileSize < 16 || item.FileSize > domain.MaximumWorkshopMissionBytes || (resolvedSize != 0 && resolvedSize != item.FileSize) {
					return nil, fmt.Errorf("Workshop resolved upload size invalid or conflicting")
				}
				resolvedSize = item.FileSize
			}
		}
		if resolvedSize > 0 {
			minimum, maximum = resolvedSize, resolvedSize
		}
		objects = append(objects, domain.HostObjectRequest{Purpose: domain.HostObjectWorkshopMission, Slot: slot, Key: scope.StagingKey(slot), ContentType: "application/octet-stream", MinBytes: minimum, MaxBytes: maximum})
	}
	if len(ordered) > 0 {
		objects = append(objects, domain.HostObjectRequest{Purpose: domain.HostObjectWorkshopResolution, Slot: "workshop-resolution", Key: scope.StagingKey("workshop-resolution"), ContentType: "text/tab-separated-values", MinBytes: 1, MaxBytes: domain.MaxHostResultBytes})
	}
	for _, object := range objects {
		if object.Validate(scope) != nil || !authorizedHostOutput(session, workflow, object) {
			return nil, domain.ErrConflict
		}
	}
	return objects, nil
}
