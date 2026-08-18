package reliability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type ids struct{ values []string }

func (generator *ids) New(time.Time) (string, error) {
	value := generator.values[0]
	generator.values = generator.values[1:]
	return value, nil
}

type deadLetters struct{ redrives int }

func (manager *deadLetters) Inspect(context.Context, domain.DeadLetterQueue) (domain.DeadLetterInspection, string, error) {
	return domain.DeadLetterInspection{Visible: 2}, "arn:dlq", nil
}

func (manager *deadLetters) StartRedrive(context.Context, domain.DeadLetterQueue, int32) (string, string, error) {
	manager.redrives++
	return "arn:dlq", "arn:queue", nil
}

func TestCancellationIsOwnerBoundAndHonoredBeforeProvisioning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Configure(domain.SessionConfiguration{GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 86400, Vanilla: true}, now); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(domain.ArtifactMission, "sessions/session-1/input/mission.pbo", now); err != nil {
		t.Fatal(err)
	}
	event := domain.NewSessionCreatedEvent("create", "create", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now)
	idempotency, _ := domain.NewCompletedIdempotencyRecord("create", "hash", session.ID, now, time.Hour)
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatal(err)
	}
	expected := session.Version
	if err := session.AcquireProvisioningWorkflowLock("workflow-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	workflow := domain.Workflow{ID: "workflow-1", SessionID: session.ID, Type: domain.ProvisionWorkflowType, Status: domain.WorkflowRunning, RequestedBy: "owner-1", CorrelationID: "corr", ExpectedVersion: expected, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}
	started := domain.SessionEvent{ID: "started", SessionID: session.ID, Type: domain.EventWorkflowStarted, OccurredAt: now, ActorType: string(domain.ActorTypeDiscordUser), ActorID: "owner-1", CorrelationID: "corr", Data: map[string]string{}}
	if err := repository.AcquireWorkflow(context.Background(), session, expected, workflow, started); err != nil {
		t.Fatal(err)
	}
	service, _ := NewService(repository, repository, repository, &ids{values: []string{"cancel-request", "cancel-complete"}}, fixedClock{now.Add(time.Minute)})
	if _, err := service.RequestCancellation(context.Background(), CancellationCommand{SessionID: session.ID, WorkflowID: workflow.ID, RequestedBy: "intruder", CorrelationID: "bad"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("intruder error = %v", err)
	}
	if _, err := service.RequestCancellation(context.Background(), CancellationCommand{SessionID: session.ID, WorkflowID: workflow.ID, RequestedBy: "owner-1", CorrelationID: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if err := service.HonorAtInitialBoundary(context.Background(), session.ID, workflow.ID); !IsCancellationHonored(err) {
		t.Fatalf("boundary error = %v", err)
	}
	got, _ := repository.Get(context.Background(), session.ID)
	if got.ActiveWorkflowID != "" || got.LifecycleState != domain.StateNew || got.Progress.State != domain.ProgressCancelled {
		t.Fatalf("cancelled session = %#v", got)
	}
	gotWorkflow, _ := repository.GetWorkflow(context.Background(), session.ID, workflow.ID)
	if gotWorkflow.Status != domain.WorkflowCancelled {
		t.Fatalf("workflow = %#v", gotWorkflow)
	}
}

func TestDeadLetterRedriveIsIdempotentByCorrelation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	manager := &deadLetters{}
	service, err := NewService(repository, repository, repository, &ids{}, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	service.WithDeadLetterManager(manager)
	first, err := service.RedriveDeadLetter(context.Background(), domain.DeadLetterCommands, "operator", "incident-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.RedriveDeadLetter(context.Background(), domain.DeadLetterCommands, "operator", "incident-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || manager.redrives != 1 {
		t.Fatalf("redrive was replayed: first=%#v second=%#v calls=%d", first, second, manager.redrives)
	}
	if _, err := service.RedriveDeadLetter(context.Background(), domain.DeadLetterCommands, "different-operator", "incident-1", 10); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("correlation identity mismatch error = %v", err)
	}
}
