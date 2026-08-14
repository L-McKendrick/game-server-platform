package domain

import (
	"testing"
	"time"
)

func TestSessionTerminationLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	session := archiveTestSession(t, now)
	session.MissionObjectKey = "sessions/session-1/input/mission.pbo"
	session.PresetObjectKey = "sessions/session-1/input/preset.html"

	if err := session.BeginTermination("terminate-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateDeleting || session.ActiveWorkflowType != TerminationWorkflowType {
		t.Fatalf("terminating session = %#v", session)
	}
	if err := session.CompleteTermination("terminate-1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateDeleted || session.HealthStatus != HealthStopped || !session.Infrastructure.Empty() || !session.Archive.Empty() || session.MissionObjectKey != "" || session.PresetObjectKey != "" || session.ActiveWorkflowID != "" {
		t.Fatalf("terminated session = %#v", session)
	}
	if session.CanTerminate() {
		t.Fatal("deleted session remained terminable")
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
