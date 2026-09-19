package app

import (
	"context"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestWorkshopInventoryUsesFiniteApprovedSlots(t *testing.T) {
	now := time.Now().UTC()
	records := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", ActiveWorkflowID: "operation", ActiveWorkflowType: domain.BootstrapWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour), WorkshopMissionSources: []domain.WorkshopMissionSource{{AcceptedItemIDs: []uint64{43, 42, 43}}}}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.BootstrapWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
	records.session.Infrastructure.InstanceID = "i-123"
	scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: HostContentSnapshot(records.session), DeadlineAt: now.Add(30 * time.Minute)}
	issuer := HostObjectIssuer{Authority: HostAccessAuthority{Records: records, Instances: instanceAuthority(true)}}
	objects, err := issuer.WorkflowWorkshopInventory(context.Background(), scope)
	if err != nil || len(objects) != 4 {
		t.Fatal("approved inventory invalid", err)
	}
	for index, slot := range []string{"workshop-result", "mission-42", "mission-43", "workshop-resolution"} {
		if objects[index].Slot != slot || objects[index].Key != scope.StagingKey(slot) || objects[index].Validate(scope) != nil {
			t.Fatal("output escaped fixed slot")
		}
	}
	if objects[1].MinBytes != 16 || objects[1].MaxBytes != domain.MaximumWorkshopMissionBytes {
		t.Fatal("mission byte bounds changed")
	}
	originalSession, originalWorkflow := records.session, records.workflow
	records.session.WorkshopMissionSources[0].ResolvedAt = now
	records.session.MissionFiles = []domain.MissionRecord{{WorkshopItemID: 43, ObjectKey: "accepted", Status: domain.ArtifactAccepted, AddedAt: now.Add(time.Second)}}
	records.session.PendingPresetRevision = domain.PresetRevision{Number: 2, Status: domain.PresetRevisionApplying, ApplyWorkflowID: "operation"}
	records.session.ActiveWorkflowType = domain.RestartWorkflowType
	records.workflow.Type = domain.RestartWorkflowType
	records.session.Progress = domain.SessionProgress{WorkflowID: "operation", WorkflowType: domain.RestartWorkflowType, State: domain.ProgressRollingBack, StartedAt: now}
	issuer.Rollback = true
	scope.CommandMode = "rollback"
	scope.SnapshotSHA256 = HostContentSnapshot(records.session)
	objects, err = issuer.WorkflowWorkshopInventory(context.Background(), scope)
	if err != nil || len(objects) != 4 || objects[2].Slot != "mission-43" {
		t.Fatal("restart rollback omitted already materialized approved mission", err)
	}
	records.session, records.workflow = originalSession, originalWorkflow
	issuer.Rollback = false
	scope.CommandMode = ""
	scope.SnapshotSHA256 = HostContentSnapshot(records.session)
	records.workflow.Type = domain.WorkshopContentSyncWorkflowType
	records.session.ActiveWorkflowType = domain.WorkshopContentSyncWorkflowType
	records.workflow.ContentTarget = string(domain.WorkshopTargetMods)
	objects, err = issuer.WorkflowWorkshopInventory(context.Background(), scope)
	if err != nil || len(objects) != 1 || objects[0].Slot != "workshop-result" {
		t.Fatal("mod-only sync received mission staging slots", err)
	}
	records.workflow.Type = domain.BootstrapWorkflowType
	records.session.ActiveWorkflowType = domain.BootstrapWorkflowType
	records.session.WorkshopMissionSources[0].AcceptedItemIDs = nil
	for id := uint64(1); id <= uint64(domain.MaximumWorkshopMissionItems+1); id++ {
		records.session.WorkshopMissionSources[0].AcceptedItemIDs = append(records.session.WorkshopMissionSources[0].AcceptedItemIDs, id)
	}
	scope.SnapshotSHA256 = HostContentSnapshot(records.session)
	if objects, err := issuer.WorkflowWorkshopInventory(context.Background(), scope); err == nil || objects != nil {
		t.Fatal("oversized item set issued")
	}
	records.workflow.CancelRequestedAt = time.Now()
	if objects, err := issuer.WorkflowWorkshopInventory(context.Background(), scope); err == nil || objects != nil {
		t.Fatal("cancelled workflow issued outputs")
	}
}
