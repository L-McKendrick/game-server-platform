package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestWorkflowScopeRecoversOriginalDeadlineAndRejectsContentDrift(t *testing.T) {
	now := time.Now().UTC()
	key := "sessions/session/input/missions/" + strings.Repeat("a", 64) + "-mission.pbo"
	r := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", Version: 7, ActiveWorkflowID: "operation", ActiveWorkflowType: domain.BootstrapWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour), MissionFiles: []domain.MissionRecord{{ObjectKey: key, Filename: "mission.pbo", Status: domain.ArtifactAccepted}}}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.BootstrapWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour), CommandDeadlineAt: now.Add(20 * time.Minute)}}
	r.session.Infrastructure.InstanceID = "i-123"
	recovery := &manifestRecovery{}
	issuer := HostManifestIssuer{Inputs: HostObjectIssuer{Authority: HostAccessAuthority{Records: r, Instances: instanceAuthority(true)}, Signer: manifestSigner{expiry: now.Add(10 * time.Minute)}}, Attempts: recovery, Exchange: recovery}
	scope, err := issuer.ResolveWorkflowScope(context.Background(), "session", "bootstrap", 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if scope.DeadlineAt.After(r.workflow.CommandDeadlineAt) {
		t.Fatal("command deadline extended")
	}
	same, err := issuer.ResolveWorkflowScope(context.Background(), "session", "bootstrap", 30*time.Minute)
	if err != nil || same.AttemptID != scope.AttemptID {
		t.Fatal("dispatch retry changed identity")
	}
	other, err := issuer.ResolveWorkflowScope(context.Background(), "session", "workshop-sync", 30*time.Minute)
	if err != nil || other.AttemptID == scope.AttemptID {
		t.Fatal("distinct command stage reused attempt")
	}
	objects := []domain.HostObjectRequest{{Purpose: domain.HostObjectMission, Slot: "mission", Key: key, SHA256: hostBase64Digest(strings.Repeat("a", 64)), MaxBytes: 100}}
	if _, err := issuer.Issue(context.Background(), scope, objects); err != nil {
		t.Fatal(err)
	}
	r.workflow.CommandDeadlineAt = now.Add(40 * time.Minute)
	recovered, err := issuer.ResolveWorkflowScope(context.Background(), "session", "bootstrap", 40*time.Minute)
	if err != nil || !scope.Equal(recovered) {
		t.Fatal("recovery recalculated original deadline", err)
	}
	r.session.ConfigurationRevision++
	if _, err := issuer.ResolveWorkflowScope(context.Background(), "session", "bootstrap", 40*time.Minute); err == nil {
		t.Fatal("content drift silently allocated fresh attempt")
	}
	r.session.ConfigurationRevision--
	recovery.current.DispatchState = domain.HostAccessFinished
	if _, err := issuer.ResolveWorkflowScope(context.Background(), "session", "bootstrap", 40*time.Minute); err == nil {
		t.Fatal("finished attempt reopened")
	}
	if recovery.writes != 1 {
		t.Fatal("scope resolution published extra manifests")
	}
}
