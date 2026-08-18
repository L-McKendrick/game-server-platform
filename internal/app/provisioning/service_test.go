package provisioning

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type testClock struct{ now time.Time }

func (clock testClock) Now() time.Time { return clock.now }

type testIDs struct{ next int }

func (ids *testIDs) New(time.Time) (string, error) {
	ids.next++
	return fmt.Sprintf("event-%d", ids.next), nil
}

type testCompute struct{}

func (testCompute) FindInstance(context.Context, domain.ComputeLaunchRequest) (domain.ComputeObservation, bool, error) {
	return domain.ComputeObservation{}, false, nil
}
func (testCompute) EnsureInstance(context.Context, domain.ComputeLaunchRequest, string) (domain.ComputeObservation, error) {
	return domain.ComputeObservation{InstanceID: "i-1", AvailabilityZone: "us-west-2a", State: "pending"}, nil
}
func (testCompute) ObserveInstance(context.Context, string) (domain.ComputeObservation, error) {
	return domain.ComputeObservation{InstanceID: "i-1", DataVolumeID: "vol-1", AvailabilityZone: "us-west-2a", PublicIPv4: "203.0.113.10", State: "running"}, nil
}
func (testCompute) IsManaged(context.Context, string) (bool, error) { return true, nil }
func (testCompute) StopInstance(context.Context, string) error      { return nil }
func (testCompute) StartInstance(context.Context, string) error     { return nil }

type discoveredCompute struct{ testCompute }

func (discoveredCompute) FindInstance(context.Context, domain.ComputeLaunchRequest) (domain.ComputeObservation, bool, error) {
	return domain.ComputeObservation{
		InstanceID: "i-ambiguous", DataVolumeID: "vol-ambiguous",
		AvailabilityZone: "us-west-2a", PublicIPv4: "203.0.113.11", State: "running",
	}, true, nil
}

type testNotifications struct{ requests []domain.NotificationRequest }

func (queue *testNotifications) Enqueue(_ context.Context, request domain.NotificationRequest) error {
	queue.requests = append(queue.requests, request)
	return nil
}

func TestLaunchRequestIncludesReadableSessionIdentity(t *testing.T) {
	t.Parallel()
	service := Service{config: Config{Project: "game-server-platform", Environment: "dev", GameSecurityGroupID: "sg-game"}}
	request := service.launchRequest(domain.Session{ID: "session-1", DisplayName: "Saturday Arma", Slug: "saturday-arma", GameType: "arma3"})
	if request.SessionID != "session-1" || request.SessionName != "Saturday Arma" || request.SessionSlug != "saturday-arma" {
		t.Fatalf("launch identity = %#v", request)
	}
}

func TestProvisioningStagesCreateInfrastructureAndStopBeforeBootstrap(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	repository, workflow := seededRepository(t, now)
	notifications := &testNotifications{}
	service, err := NewService(repository, repository, repository, testCompute{}, notifications, &testIDs{}, testClock{now}, Config{
		Project: "game-server-platform", Environment: "dev", AMIID: "ami-1", InstanceType: "c7i.large",
		SubnetID: "subnet-1", GameSecurityGroupID: "sg-game", VoiceSecurityGroupID: "sg-voice",
		InstanceProfile: "instance-profile", RootVolumeGiB: 30, DataVolumeGiB: 100, MaxProvisioned: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := TaskRequest{SessionID: "session-1", WorkflowID: workflow.ID, CorrelationID: workflow.CorrelationID}
	for _, action := range []string{ActionPrepare, ActionEnsure, ActionObserve, ActionCheckManaged, ActionComplete} {
		request.Action = action
		result, err := service.Handle(context.Background(), request)
		if err != nil {
			t.Fatalf("Handle(%s) returned error: %v", action, err)
		}
		if action == ActionObserve && !result.Ready {
			t.Fatal("observe result is not ready")
		}
		if action == ActionCheckManaged && !result.Managed {
			t.Fatal("managed check is false")
		}
	}
	session, err := repository.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != domain.StateBootstrapping || session.ActiveWorkflowID != "" {
		t.Fatalf("session = %#v", session)
	}
	if session.Infrastructure.InstanceID != "i-1" || session.Infrastructure.DataVolumeID != "vol-1" {
		t.Fatalf("infrastructure = %#v", session.Infrastructure)
	}
	completed, err := repository.GetWorkflow(context.Background(), session.ID, workflow.ID)
	if err != nil || completed.Status != domain.WorkflowSucceeded {
		t.Fatalf("workflow = %#v, error = %v", completed, err)
	}
	if len(notifications.requests) != 3 {
		t.Fatalf("notifications = %d; want 3", len(notifications.requests))
	}
	wantNotificationIDs := []string{
		"card-progress-" + workflow.ID + "-capacity-reserved",
		"card-progress-" + workflow.ID + "-compute-ready",
		"card-progress-" + workflow.ID + "-completed",
	}
	for index, want := range wantNotificationIDs {
		if notifications.requests[index].NotificationID != want {
			t.Fatalf("notification %d = %#v; want %q", index, notifications.requests[index], want)
		}
	}
	notification := notifications.requests[2]
	if notification.Kind != domain.NotificationSessionCard || notification.NotificationID != "card-progress-"+workflow.ID+"-completed" ||
		notification.CardRevision != session.Version {
		t.Fatalf("notification = %#v", notification)
	}
	if session.Progress.Milestone != domain.ProgressCompleted || session.Progress.WorkflowID != workflow.ID {
		t.Fatalf("progress = %#v", session.Progress)
	}
}

func TestProvisioningFailureRetainsCapacityWhenAmbiguousLaunchIsDiscovered(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	repository, workflow := seededRepository(t, now)
	service, err := NewService(repository, repository, repository, discoveredCompute{}, nil, &testIDs{}, testClock{now}, Config{
		Project: "game-server-platform", Environment: "dev", AMIID: "ami-1", InstanceType: "c7i.large",
		SubnetID: "subnet-1", GameSecurityGroupID: "sg-game", VoiceSecurityGroupID: "sg-voice",
		InstanceProfile: "instance-profile", RootVolumeGiB: 30, DataVolumeGiB: 100, MaxProvisioned: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := TaskRequest{SessionID: "session-1", WorkflowID: workflow.ID, CorrelationID: workflow.CorrelationID}
	request.Action = ActionPrepare
	if _, err := service.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Action = ActionFail
	request.ErrorCode = "ERR_AMBIGUOUS_LAUNCH"
	request.ErrorMessage = "launch response was lost"
	if _, err := service.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	session, err := repository.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != domain.StateFailed || session.Infrastructure.InstanceID != "i-ambiguous" || session.Infrastructure.CapacitySlotID == "" {
		t.Fatalf("failed session infrastructure = %#v", session)
	}
	if session.Failure.Code != "ERR_AMBIGUOUS_LAUNCH" || session.Failure.RetryDisposition != domain.RetryNotScheduled ||
		session.Failure.ResourceImpact != domain.ResourceCostUnknown || strings.Contains(session.Failure.Detail, request.ErrorMessage) {
		t.Fatalf("sanitized failure = %#v", session.Failure)
	}
}

func seededRepository(t *testing.T, now time.Time) (*memory.SessionRepository, domain.Workflow) {
	t.Helper()
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "session-1", Slug: "saturday-arma", DisplayName: "Saturday Arma", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Configure(domain.SessionConfiguration{GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400}, now); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(domain.ArtifactMission, "sessions/session-1/input/mission.pbo", now); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(domain.ArtifactPreset, "sessions/session-1/input/preset.html", now); err != nil {
		t.Fatal(err)
	}
	actor := domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}
	event := domain.NewSessionCreatedEvent("create-event", "create-correlation", actor, session, now)
	idempotency, err := domain.NewCompletedIdempotencyRecord("create", "hash", session.ID, now, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatal(err)
	}
	expectedVersion := session.Version
	if err := session.AcquireProvisioningWorkflowLock("workflow-1", 2*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	workflow := domain.Workflow{
		ID: "workflow-1", SessionID: session.ID, Type: "ProvisionSession", Status: domain.WorkflowRunning,
		RequestedBy: "owner-1", CorrelationID: "correlation-1", ExpectedVersion: expectedVersion,
		ExecutionARN: "arn:execution", CurrentStage: "Started", StartedAt: now, LeaseExpiresAt: now.Add(2 * time.Hour),
	}
	workflowEvent := domain.NewWorkflowEvent("workflow-event", domain.EventWorkflowStarted, workflow.CorrelationID, actor, session, workflow, now)
	if err := repository.AcquireWorkflow(context.Background(), session, expectedVersion, workflow, workflowEvent); err != nil {
		t.Fatal(err)
	}
	return repository, workflow
}
