package app

import (
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"testing"
)

func TestHostPresetAuthorizationPreservesOwnedRollback(t *testing.T) {
	session := domain.Session{ActiveWorkflowID: "workflow", ActivePresetRevision: domain.PresetRevision{Number: 1, PresetObjectKey: "sessions/session/input/presets/active.html"}, PendingPresetRevision: domain.PresetRevision{Number: 2, PresetObjectKey: "sessions/session/input/presets/pending.html", Status: domain.PresetRevisionApplying, ApplyWorkflowID: "workflow"}}
	workflow := domain.Workflow{ID: "workflow", Type: domain.RestartWorkflowType}
	object := domain.HostObjectRequest{Purpose: domain.HostObjectClientPreset, Key: session.ActivePresetRevision.PresetObjectKey}
	normal := HostObjectIssuer{}
	rollback := HostObjectIssuer{Rollback: true}
	if normal.accepts(session, workflow, object) || !rollback.accepts(session, workflow, object) {
		t.Fatal("active rollback reference not isolated from applying reference")
	}
	object.Key = session.PendingPresetRevision.PresetObjectKey
	if !normal.accepts(session, workflow, object) || rollback.accepts(session, workflow, object) {
		t.Fatal("pending revision authorized during rollback")
	}
	before := HostContentSnapshot(session)
	session.ActivePresetRevision.PresetObjectKey = "sessions/session/input/presets/replaced.html"
	if HostContentSnapshot(session) == before {
		t.Fatal("rollback base change did not invalidate snapshot")
	}
	session.PendingPresetRevision.ApplyWorkflowID = "other"
	object.Key = session.ActivePresetRevision.PresetObjectKey
	if rollback.accepts(session, workflow, object) {
		t.Fatal("rollback authorized without owned applying revision")
	}
}
