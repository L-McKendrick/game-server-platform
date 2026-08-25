package sessioncard

import (
	"context"
	"strings"
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
		Progress: domain.SessionProgress{WorkflowID: "workflow-1", WorkflowType: domain.ProvisionWorkflowType, Milestone: domain.ProgressInfrastructureReady,
			CompletedMilestones: []domain.ProgressMilestone{domain.ProgressAccepted}, StartedAt: now.Add(-time.Minute), LastProgressAt: now},
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
	if request.Embed == nil || !strings.Contains(request.Embed.Title, "SETTING UP") ||
		!strings.HasPrefix(request.Embed.Description, "**ARMA 3 | Saturday Arma**") || request.Embed.Color != embedColorSetup {
		t.Fatalf("progress embed = %#v", request.Embed)
	}
}

func TestEnqueueProgressActivityChangesOncePerSafeTarget(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	session := domain.Session{
		ID: "session-1", Version: 7, DisplayName: "Saturday Arma", Slug: "saturday-arma", GameType: "arma3",
		GuildID: "guild-1", ChannelID: "channel-1", LifecycleState: domain.StateInstalling,
		Progress: domain.SessionProgress{
			WorkflowID: "workflow-1", WorkflowType: domain.BootstrapWorkflowType,
			Milestone: domain.ProgressGameServerInstalled, Activity: "Arma 3 server files", State: domain.ProgressActive,
			CompletedMilestones: []domain.ProgressMilestone{domain.ProgressAccepted, domain.ProgressHostPrepared},
			StartedAt:           now.Add(-time.Minute), LastProgressAt: now,
		},
		UpdatedAt: now,
	}
	workflow := domain.Workflow{ID: "workflow-1", SessionID: session.ID, Type: domain.BootstrapWorkflowType, Status: domain.WorkflowRunning, CorrelationID: "correlation-1"}
	queue := memory.NewNotificationQueue()
	if err := EnqueueProgress(context.Background(), queue, session, workflow, now); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueProgress(context.Background(), queue, session, workflow, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	session.Progress.Activity = "Workshop content (2 items)"
	session.Version++
	if err := EnqueueProgress(context.Background(), queue, session, workflow, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	requests := queue.Requests()
	if len(requests) != 2 || requests[0].NotificationID == requests[1].NotificationID || !strings.Contains(requests[1].Content, "Workshop content") {
		t.Fatalf("activity requests = %#v", requests)
	}
}

func TestEnqueueProgressSuppressesWaitingNoiseButPublishesActionRequired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 6, 0, 0, 0, time.UTC)
	session := domain.Session{
		ID: "session-1", Version: 3, DisplayName: "Saturday Arma", Slug: "saturday-arma", GameType: "arma3",
		GuildID: "guild-1", ChannelID: "channel-1", LifecycleState: domain.StateProvisioning,
		Progress: domain.SessionProgress{
			WorkflowID: "workflow-1", WorkflowType: domain.ProvisionWorkflowType,
			Milestone: domain.ProgressComputeReady, State: domain.ProgressActive,
			CompletedMilestones: []domain.ProgressMilestone{domain.ProgressAccepted, domain.ProgressCapacityReserved},
			StartedAt:           now.Add(-time.Minute), LastProgressAt: now,
		},
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}
	workflow := domain.Workflow{ID: "workflow-1", SessionID: session.ID, Type: domain.ProvisionWorkflowType, Status: domain.WorkflowRunning, CorrelationID: "correlation-1", StartedAt: now.Add(-time.Minute)}
	queue := memory.NewNotificationQueue()
	if err := EnqueueProgress(context.Background(), queue, session, workflow, now); err != nil {
		t.Fatal(err)
	}
	session.Progress.State = domain.ProgressWaiting
	session.Version++
	if err := EnqueueProgress(context.Background(), queue, session, workflow, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(queue.Requests()) != 1 {
		t.Fatalf("waiting state created card noise: %#v", queue.Requests())
	}
	session.Progress.State = domain.ProgressActionRequired
	session.Version++
	if err := EnqueueProgress(context.Background(), queue, session, workflow, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	requests := queue.Requests()
	if len(requests) != 2 || requests[1].NotificationID != "card-progress-workflow-1-compute-ready-action-required" {
		t.Fatalf("terminal progress requests = %#v", requests)
	}
}

func TestEnqueueActivatedModlistRequiresMatchingCompletedActivation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 1, 0, 0, 0, time.UTC)
	session := domain.Session{
		ID: "session-1", Version: 8, DisplayName: "Saturday Arma", GuildID: "guild-1", ChannelID: "channel-1",
		PresetObjectKey: "sessions/session-1/input/presets/v2.html", PresetRevisionSequence: 2,
		ActivePresetRevision: domain.PresetRevision{
			Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/presets/v2.html", Status: domain.PresetRevisionActive,
			StagedAt: now.Add(-time.Hour), ActivatedAt: now,
			Modlist: domain.PresetModlistMetadata{ObjectKey: "sessions/session-1/input/modlists/v2/saturday-arma-modlist.html", Filename: "saturday-arma-modlist.html", SHA256: strings.Repeat("a", 64), SizeBytes: 512, WorkshopCount: 3},
		},
	}
	workflow := domain.Workflow{ID: "wake-1", SessionID: session.ID, Type: domain.WakeWorkflowType, Status: domain.WorkflowSucceeded, CorrelationID: "correlation-1", CompletedAt: now}
	queue := memory.NewNotificationQueue()
	if err := EnqueueActivatedModlist(context.Background(), queue, session, workflow, now); err != nil {
		t.Fatal(err)
	}
	if err := EnqueueActivatedModlist(context.Background(), queue, session, workflow, now.Add(time.Minute)); err != nil {
		t.Fatalf("idempotent replay error = %v", err)
	}
	requests := queue.Requests()
	if len(requests) != 1 || requests[0].Kind != domain.NotificationSessionModlist || requests[0].Attachment == nil || requests[0].Attachment.ObjectKey != session.ActivePresetRevision.Modlist.ObjectKey || requests[0].Attachment.Revision != session.Version {
		t.Fatalf("active modlist requests = %#v", requests)
	}

	queue = memory.NewNotificationQueue()
	workflow.CompletedAt = now.Add(time.Second)
	if err := EnqueueActivatedModlist(context.Background(), queue, session, workflow, now); err != nil || len(queue.Requests()) != 0 {
		t.Fatalf("non-activating workflow published attachment: requests=%#v err=%v", queue.Requests(), err)
	}
	session.PendingPresetRevision = domain.PresetRevision{Number: 3, BaseRevision: 2, Status: domain.PresetRevisionPending, PresetObjectKey: "sessions/session-1/input/presets/v3.html", StagedAt: now}
	if err := EnqueueActivatedModlist(context.Background(), queue, session, workflow, now); err != nil || len(queue.Requests()) != 0 {
		t.Fatalf("pending revision published attachment: requests=%#v err=%v", queue.Requests(), err)
	}
}

func TestEnqueueProgressRejectsMismatchedWorkflow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	session := domain.Session{Progress: domain.SessionProgress{
		WorkflowID: "workflow-1", WorkflowType: domain.ProvisionWorkflowType, Milestone: domain.ProgressAccepted, StartedAt: now, LastProgressAt: now,
	}}
	workflow := domain.Workflow{ID: "workflow-2", Type: "ProvisionSession"}
	if err := EnqueueProgress(context.Background(), memory.NewNotificationQueue(), session, workflow, now); err == nil {
		t.Fatal("mismatched workflow was accepted")
	}
	if err := EnqueueProgress(context.Background(), nil, session, workflow, now); err != nil {
		t.Fatalf("nil queue error = %v", err)
	}
}
