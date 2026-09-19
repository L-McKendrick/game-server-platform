package app

import (
	"strconv"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// Fixed output slots cannot publish durable Workshop content. Trusted byte and
// provenance verification precede promotion; archive bounds come from preparation.
func authorizedHostOutput(session domain.Session, workflow domain.Workflow, object domain.HostObjectRequest) bool {
	switch workflow.Type {
	case domain.BootstrapWorkflowType, domain.WakeWorkflowType, domain.RestartWorkflowType, domain.RestoreWorkflowType, domain.WorkshopContentSyncWorkflowType, domain.ArchiveWorkflowType:
	default:
		return false
	}
	switch object.Purpose {
	case domain.HostObjectProgress:
		return object.Slot == "progress" && object.ContentType == "text/plain"
	case domain.HostObjectDiagnostic:
		return object.Slot == "diagnostic" && object.ContentType == "text/plain"
	case domain.HostObjectArchiveUpload:
		return workflow.Type == domain.ArchiveWorkflowType && object.Slot == "archive"
	case domain.HostObjectWorkshopResult:
		return workflow.Type != domain.ArchiveWorkflowType && object.Slot == "workshop-result" && object.ContentType == "application/json"
	case domain.HostObjectWorkshopResolution:
		if workflow.Type == domain.ArchiveWorkflowType || object.Slot != "workshop-resolution" || object.ContentType != "text/tab-separated-values" {
			return false
		}
		for _, source := range session.WorkshopMissionSources {
			for _, id := range source.AcceptedItemIDs {
				if id > 0 {
					return true
				}
			}
		}
		return false
	case domain.HostObjectWorkshopMission:
		if workflow.Type == domain.ArchiveWorkflowType || object.ContentType != "application/octet-stream" {
			return false
		}
		for _, source := range session.WorkshopMissionSources {
			for _, id := range source.AcceptedItemIDs {
				if id > 0 && object.Slot == "mission-"+strconv.FormatUint(id, 10) {
					return true
				}
			}
		}
	}
	return false
}
