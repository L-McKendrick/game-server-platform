package sessioncard

import (
	"context"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestEnqueueProgressUsesMilestoneIdempotencyAndCardRevision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	session := domain.Session{
		ID: "session-1", Version: 7, DisplayName: "Saturday Arma", Slug: "saturday-arma", GameType: "arma3",
		GuildID: "guild-1", ChannelID: "channel-1", LifecycleState: domain.StateProvisioning, HealthStatus: domain.HealthStarting,
		Progress:  domain.SessionProgress{WorkflowID: "workflow-1", WorkflowType: "ProvisionSession", Milestone: domain.ProgressInfrastructureReady, UpdatedAt: now},
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}
	workflow := domain.Workflow{
		ID: "workflow-1", SessionID: session.ID, Type: "ProvisionSession", Status: domain.WorkflowRunning,
		CorrelationID: "correlation-1", StartedAt: now.Add(-time.Minute),
	}
	queue := memory.NewNotificationQueue()

	if err := EnqueueProgress(context.Background(), queue, session, workflow, now); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueProgress(context.Background(), queue, session, workflow, now.Add(time.Second)); err != nil {
		t.Fatalf("idempotent replay error = %v", err)
	}
	requests := queue.Requests()
	if len(requests) != 1 {
		t.Fatalf("requests = %#v", requests)
	}
	request := requests[0]
	if request.NotificationID != "card-progress-workflow-1-infrastructure-ready" || request.Kind != domain.NotificationSessionCard || request.CardRevision != session.Version {
		t.Fatalf("request = %#v", request)
	}
}

func TestEnqueueProgressRejectsMismatchedWorkflow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	session := domain.Session{Progress: domain.SessionProgress{
		WorkflowID: "workflow-1", WorkflowType: "ProvisionSession", Milestone: domain.ProgressAccepted, UpdatedAt: now,
	}}
	workflow := domain.Workflow{ID: "workflow-2", Type: "ProvisionSession"}
	if err := EnqueueProgress(context.Background(), memory.NewNotificationQueue(), session, workflow, now); err == nil {
		t.Fatal("mismatched workflow was accepted")
	}
	if err := EnqueueProgress(context.Background(), nil, session, workflow, now); err != nil {
		t.Fatalf("nil queue error = %v", err)
	}
}
