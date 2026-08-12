package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type testClock struct{ now time.Time }

func (clock testClock) Now() time.Time { return clock.now }

type testIDs struct {
	values []string
	index  int
}

func (ids *testIDs) New(time.Time) (string, error) {
	if ids.index >= len(ids.values) {
		return "", fmt.Errorf("no IDs remaining")
	}
	value := ids.values[ids.index]
	ids.index++
	return value, nil
}

type testRunner struct {
	commandID string
	status    ports.BootstrapCommandStatus
	starts    int
}

func (runner *testRunner) Start(context.Context, domain.Session) (string, error) {
	runner.starts++
	return runner.commandID, nil
}
func (runner *testRunner) Observe(context.Context, string, string) (ports.BootstrapCommandStatus, error) {
	return runner.status, nil
}

type testNotifications struct{ requests []domain.NotificationRequest }

func (queue *testNotifications) Enqueue(_ context.Context, request domain.NotificationRequest) error {
	queue.requests = append(queue.requests, request)
	return nil
}

func TestBootstrapServiceCompletesOnlyAfterSuccessfulManagedCommand(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	repository, workflow := seedBootstrap(t, now)
	runner := &testRunner{commandID: "command-1", status: ports.BootstrapCommandStatus{Status: "Success"}}
	notifications := &testNotifications{}
	service, err := NewService(repository, repository, repository, runner, notifications, &testIDs{values: []string{"stage-event", "ready-event", "notification-1"}}, testClock{now})
	if err != nil {
		t.Fatal(err)
	}
	request := TaskRequest{SessionID: workflow.SessionID, WorkflowID: workflow.ID, CorrelationID: workflow.CorrelationID}

	request.Action = ActionPrepare
	if _, err := service.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Action = ActionDispatch
	dispatched, err := service.Handle(context.Background(), request)
	if err != nil || dispatched.CommandID != "command-1" || runner.starts != 1 {
		t.Fatalf("dispatch = %#v, err = %v", dispatched, err)
	}
	request.Action, request.CommandID = ActionObserve, dispatched.CommandID
	observed, err := service.Handle(context.Background(), request)
	if err != nil || !observed.Done || !observed.Succeeded {
		t.Fatalf("observe = %#v, err = %v", observed, err)
	}
	request.Action = ActionComplete
	if _, err := service.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}

	session, err := repository.Get(context.Background(), workflow.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != domain.StateRunning || session.HealthStatus != domain.HealthHealthy || session.ActiveWorkflowID != "" {
		t.Fatalf("session = %#v", session)
	}
	if len(notifications.requests) != 1 {
		t.Fatalf("notifications = %#v", notifications.requests)
	}
}

func TestObserveSanitizesFailedCommand(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	repository, workflow := seedBootstrap(t, now)
	runner := &testRunner{status: ports.BootstrapCommandStatus{Status: "Failed", ErrorMessage: "installer exited"}}
	service, err := NewService(repository, repository, repository, runner, nil, &testIDs{values: []string{"stage-event"}}, testClock{now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Handle(context.Background(), TaskRequest{Action: ActionPrepare, SessionID: workflow.SessionID, WorkflowID: workflow.ID}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Handle(context.Background(), TaskRequest{Action: ActionObserve, SessionID: workflow.SessionID, WorkflowID: workflow.ID, CommandID: "command-1"})
	if err != nil || !result.Done || result.Succeeded || result.ErrorCode != "ERR_BOOTSTRAP_COMMAND_FAILED" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	if _, found := fields["done"]; !found {
		t.Fatal("done must be explicit for the Step Functions choice contract")
	}
	if succeeded, found := fields["succeeded"]; !found || succeeded != false {
		t.Fatalf("succeeded must be explicit false, payload = %s", payload)
	}
}

func seedBootstrap(t *testing.T, now time.Time) (*memory.SessionRepository, domain.Workflow) {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "arma", DisplayName: "Arma", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
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
	if err := session.AcquireProvisioningWorkflowLock("provision", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginInfrastructureProvisioning("provision", "slot-0", now); err != nil {
		t.Fatal(err)
	}
	if err := session.RecordInfrastructureLaunch("provision", domain.Infrastructure{CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile-1", AMIID: "ami-1", InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.1", LastObservedAt: now}, now); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteInfrastructureProvisioning("provision", now); err != nil {
		t.Fatal(err)
	}
	repository := memory.NewSessionRepository()
	actor := domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}
	event := domain.NewSessionCreatedEvent("create-event", "create-correlation", actor, session, now)
	idempotency, _ := domain.NewCompletedIdempotencyRecord("create", "hash", session.ID, now, time.Hour)
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatal(err)
	}
	session, _ = repository.Get(context.Background(), session.ID)
	expectedVersion := session.Version
	if err := session.AcquireBootstrapWorkflowLock("bootstrap-1", 6*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	workflow := domain.Workflow{ID: "bootstrap-1", SessionID: session.ID, Type: domain.BootstrapWorkflowType, Status: domain.WorkflowRunning, RequestedBy: "owner-1", CorrelationID: "bootstrap-correlation", ExpectedVersion: expectedVersion, ExecutionARN: "arn:execution", CurrentStage: "Started", StartedAt: now, LeaseExpiresAt: now.Add(6 * time.Hour)}
	workflowEvent := domain.NewWorkflowEvent("workflow-event", domain.EventWorkflowStarted, workflow.CorrelationID, actor, session, workflow, now)
	if err := repository.AcquireWorkflow(context.Background(), session, expectedVersion, workflow, workflowEvent); err != nil {
		t.Fatal(err)
	}
	return repository, workflow
}
