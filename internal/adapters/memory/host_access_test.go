package memory

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestHostAccessConcurrentCreationHasOneWinner(t *testing.T) {
	now := time.Now().UTC()
	repository := NewSessionRepository()
	session := domain.Session{ID: "session", GuildID: "guild", Version: 7, ActiveWorkflowID: "operation", ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour)}
	session.Infrastructure.InstanceID = "i-123"
	repository.sessions[session.ID] = session
	repository.workflows[workflowKey("session", "operation")] = domain.Workflow{Status: domain.WorkflowRunning, LeaseExpiresAt: now.Add(time.Hour)}
	attempt := domain.HostAccessAttempt{Scope: domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: now.Add(30 * time.Minute)}, Revision: 1, Generation: 1, ManifestVersionID: "version", ManifestSHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), ManifestSizeBytes: 8, ExpiresAt: now.Add(10 * time.Minute), DispatchState: domain.HostAccessPrepared}
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for pass := 0; pass < 2; pass++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			results <- repository.SaveHostAccessAttempt(context.Background(), attempt, 0, 7, now)
		}()
	}
	workers.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("creation winners=%d", winners)
	}
	next := attempt
	next.Revision++
	next.DispatchState = domain.HostAccessDispatched
	if err := repository.SaveHostAccessAttempt(context.Background(), next, 1, 6, now); err == nil {
		t.Fatal("accepted stale session version")
	}
	workflow := repository.workflows[workflowKey("session", "operation")]
	workflow.CancelRequestedAt = now
	repository.workflows[workflowKey("session", "operation")] = workflow
	if err := repository.SaveHostAccessAttempt(context.Background(), next, 1, 7, now); err == nil {
		t.Fatal("accepted cancelled workflow")
	}
	workflow.CancelRequestedAt = time.Time{}
	repository.workflows[workflowKey("session", "operation")] = workflow
	next.DispatchState = domain.HostAccessFinished
	if err := repository.SaveHostAccessAttempt(context.Background(), next, 1, 7, now); err != nil {
		t.Fatal(err)
	}
	rollback := attempt
	rollback.Scope.AttemptID = "rollback"
	if err := repository.SaveHostAccessAttempt(context.Background(), rollback, 0, 7, now); err != nil {
		t.Fatal("new command attempt could not coexist with finished application", err)
	}
	finished, err := repository.GetHostAccessAttempt(context.Background(), "session", "operation", "attempt")
	if err != nil || finished.DispatchState != domain.HostAccessFinished || finished.Revision != 2 {
		t.Fatal("new command replaced finished attempt")
	}
	separate, err := repository.GetHostAccessAttempt(context.Background(), "session", "operation", "rollback")
	if err != nil || separate.Scope.AttemptID != "rollback" || separate.Revision != 1 {
		t.Fatal("new command attempt not independently recoverable")
	}
	session.ActiveWorkflowType = domain.BootstrapWorkflowType
	session.ActiveWorkflowStartedAt = now
	session.Progress = domain.SessionProgress{WorkflowID: "operation", WorkflowType: domain.BootstrapWorkflowType, State: domain.ProgressRollingBack, StartedAt: now}
	session.PendingPresetRevision = domain.PresetRevision{Status: domain.PresetRevisionApplying, ApplyWorkflowID: "operation"}
	repository.sessions[session.ID] = session
	workflow.CommandDeadlineAt = now.Add(-time.Minute)
	repository.workflows[workflowKey("session", "operation")] = workflow
	afterTimeout := attempt
	afterTimeout.Scope.AttemptID = "rollback-after-timeout"
	afterTimeout.Scope.CommandMode = "rollback"
	if err := repository.SaveHostAccessAttempt(context.Background(), afterTimeout, 0, 7, now); err != nil {
		t.Fatal("owned rollback blocked by prior application deadline", err)
	}
	normal := attempt
	normal.Scope.AttemptID = "obsolete-application"
	if err := repository.SaveHostAccessAttempt(context.Background(), normal, 0, 7, now); err == nil {
		t.Fatal("normal application accepted during rollback")
	}
}
