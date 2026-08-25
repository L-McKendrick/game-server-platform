package domain

import (
	"testing"
	"time"
)

func TestSessionTerminationLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	session := archiveTestSession(t, now)
	session.DisplayName = "Saturday Arma"
	session.Slug = "saturday-arma"
	session.Description = "Weekly co-op night"
	session.MissionObjectKey = "sessions/session-1/input/mission.pbo"
	session.MissionFiles = []MissionRecord{{ObjectKey: session.MissionObjectKey, Filename: "mission.pbo", Status: ArtifactAccepted, AddedAt: now}}
	session.ConfiguredMission = UploadedMissionSelection(session.MissionObjectKey)
	session.CurrentMission = session.ConfiguredMission
	session.PresetObjectKey = "sessions/session-1/input/preset.html"
	session.PresetRevisionSequence = 2
	session.ActivePresetRevision = PresetRevision{Number: 1, PresetObjectKey: session.PresetObjectKey, Status: PresetRevisionActive, StagedAt: now, ActivatedAt: now}
	session.PendingPresetRevision = PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/preset-v2.html", Status: PresetRevisionPending, StagedAt: now}
	session.ServerPresetObjectKey = "sessions/session-1/input/server-presets/v1.html"
	session.ServerPresetRevisionSequence = 1
	session.ActiveServerPresetRevision = PresetRevision{Number: 1, PresetObjectKey: session.ServerPresetObjectKey, Status: PresetRevisionActive, StagedAt: now, ActivatedAt: now}

	if err := session.BeginTermination("terminate-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateDeleting || session.ActiveWorkflowType != TerminationWorkflowType {
		t.Fatalf("terminating session = %#v", session)
	}
	if err := session.CompleteTermination("terminate-1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateDeleted || session.HealthStatus != HealthStopped || !session.Infrastructure.Empty() || !session.Archive.Empty() || session.MissionObjectKey != "" || session.PresetObjectKey != "" || session.PresetRevisionSequence != 0 || !session.ActivePresetRevision.Empty() || !session.PendingPresetRevision.Empty() || session.ServerPresetObjectKey != "" || session.ServerPresetRevisionSequence != 0 || !session.ActiveServerPresetRevision.Empty() || !session.PendingServerPresetRevision.Empty() || session.ActiveWorkflowID != "" {
		t.Fatalf("terminated session = %#v", session)
	}
	if session.CanTerminate() {
		t.Fatal("deleted session remained terminable")
	}
	if session.DisplayName != "Saturday Arma" || session.Slug != "saturday-arma" || session.Description != "Weekly co-op night" {
		t.Fatalf("deleted session lost readable tombstone identity: %#v", session)
	}
	if len(session.MissionFiles) != 1 || session.ConfiguredMission.Template != "mission" || session.CurrentMission.Template != "mission" {
		t.Fatalf("deleted session lost mission audit history: %#v", session)
	}
}

func TestSessionTerminationFailureRetainsRecoveryIdentifiers(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	session := archiveTestSession(t, now)
	session.MissionObjectKey = "sessions/session-1/input/mission.pbo"
	if err := session.BeginTermination("terminate-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.FailTermination("terminate-1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateFailed || session.Infrastructure.InstanceID == "" || session.Infrastructure.DataVolumeID == "" || session.MissionObjectKey == "" || session.ActiveWorkflowID != "" || !session.CanTerminate() {
		t.Fatalf("failed termination session = %#v", session)
	}
}
