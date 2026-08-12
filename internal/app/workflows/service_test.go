package workflows

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type workflowClock struct{ now time.Time }

func (clock workflowClock) Now() time.Time { return clock.now }

type workflowIDs struct {
	ids   []string
	index int
}

func (generator *workflowIDs) New(time.Time) (string, error) {
	if generator.index >= len(generator.ids) {
		return "", fmt.Errorf("no test IDs remaining")
	}
	id := generator.ids[generator.index]
	generator.index++
	return id, nil
}

type workflowStarter struct {
	arn   string
	err   error
	calls int
}

type allowAuthorizer struct{}

func (allowAuthorizer) Authorize(context.Context, string, string, string, []string) error { return nil }

func (starter *workflowStarter) Start(context.Context, domain.Workflow) (string, error) {
	starter.calls++
	return starter.arn, starter.err
}

func TestStartAcquiresLockStartsExecutionAndReplaysSameWorkflow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 22, 0, 0, 0, time.UTC)
	repository := seedWorkflowRepository(t, now)
	starter := &workflowStarter{arn: "arn:aws:states:us-west-2:123456789012:execution:ProvisionSession:command-1"}
	service, err := NewService(repository, repository, starter, allowAuthorizer{}, &workflowIDs{ids: []string{"workflow-start-event"}}, workflowClock{now}, 2*time.Hour)
	if err != nil {
		t.Fatalf("NewService() returned error: %v", err)
	}
	command := workflowCommand(now)

	workflow, err := service.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("Start() returned error: %v", err)
	}
	if workflow.ID != command.CommandID || workflow.Status != domain.WorkflowRunning || workflow.ExecutionARN != starter.arn {
		t.Fatalf("workflow = %#v", workflow)
	}
	session, err := repository.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("Get() returned error: %v", err)
	}
	if session.ActiveWorkflowID != command.CommandID || session.ActiveWorkflowType != "ProvisionSession" {
		t.Fatalf("session lock = %#v", session)
	}

	replayed, err := service.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("replayed Start() returned error: %v", err)
	}
	if replayed.ID != workflow.ID || starter.calls != 1 {
		t.Fatalf("replay = %#v, starter calls = %d; want same workflow and one call", replayed, starter.calls)
	}
}

func TestStartFailureMarksWorkflowFailedAndReleasesLock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 22, 0, 0, 0, time.UTC)
	repository := seedWorkflowRepository(t, now)
	starter := &workflowStarter{err: errors.New("Step Functions unavailable")}
	service, err := NewService(
		repository, repository, starter, allowAuthorizer{},
		&workflowIDs{ids: []string{"workflow-start-event", "workflow-failed-event"}},
		workflowClock{now}, 2*time.Hour,
	)
	if err != nil {
		t.Fatalf("NewService() returned error: %v", err)
	}

	if _, err := service.Start(context.Background(), workflowCommand(now)); err == nil {
		t.Fatal("Start() returned nil error; want Step Functions failure")
	}
	session, err := repository.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("Get() returned error: %v", err)
	}
	if session.ActiveWorkflowID != "" {
		t.Fatalf("active workflow ID = %q; want released lock", session.ActiveWorkflowID)
	}
	if session.LifecycleState != domain.StateNew {
		t.Fatalf("lifecycle state = %q; want NEW after start failure", session.LifecycleState)
	}
	workflow, err := repository.GetWorkflow(context.Background(), "session-1", "command-1")
	if err != nil {
		t.Fatalf("GetWorkflow() returned error: %v", err)
	}
	if workflow.Status != domain.WorkflowFailed || workflow.ErrorCode != "ERR_WORKFLOW_START_FAILED" {
		t.Fatalf("failed workflow = %#v", workflow)
	}
}

func TestStart_AllowsGuildAdministratorForSleepWorkflow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	repository := seedRunningWorkflowRepository(t, now)
	starter := &workflowStarter{arn: "arn:aws:states:us-west-2:123456789012:execution:SleepSession:command-admin"}
	service, err := NewService(repository, repository, starter, rejectAuthorizer{}, &workflowIDs{ids: []string{"workflow-start-event"}}, workflowClock{now}, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	command := domain.CommandEnvelope{
		SchemaVersion: 1, CommandID: "command-admin", CommandType: domain.CommandSleepSession, RequestedAt: now,
		Actor:     domain.CommandActor{DiscordUserID: "admin-1", GuildID: "guild-1", ChannelID: "channel-1", CanManageGuild: true},
		SessionID: "running-session", IdempotencyKey: "discord:command-admin", CorrelationID: "correlation-admin", Parameters: map[string]string{},
	}
	workflow, err := service.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if workflow.Type != domain.SleepWorkflowType || starter.calls != 1 {
		t.Fatalf("workflow = %#v, starter calls = %d", workflow, starter.calls)
	}
}

func seedWorkflowRepository(t *testing.T, now time.Time) *memory.SessionRepository {
	t.Helper()
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "session-1", Slug: "saturday-arma", DisplayName: "Saturday Arma", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatalf("NewSession() returned error: %v", err)
	}
	if err := session.Configure(domain.SessionConfiguration{
		GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400,
	}, now); err != nil {
		t.Fatalf("Configure() returned error: %v", err)
	}
	if err := session.AttachArtifact(domain.ArtifactMission, "sessions/session-1/input/mission.pbo", now); err != nil {
		t.Fatalf("AttachArtifact(mission) returned error: %v", err)
	}
	if err := session.AttachArtifact(domain.ArtifactPreset, "sessions/session-1/input/preset.html", now); err != nil {
		t.Fatalf("AttachArtifact(preset) returned error: %v", err)
	}
	actor := domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}
	event := domain.NewSessionCreatedEvent("create-event", "correlation-create", actor, session, now)
	idempotency, err := domain.NewCompletedIdempotencyRecord("discord:create", "create-hash", session.ID, now, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewCompletedIdempotencyRecord() returned error: %v", err)
	}
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	return repository
}

type rejectAuthorizer struct{}

func (rejectAuthorizer) Authorize(context.Context, string, string, string, []string) error {
	return domain.ErrForbidden
}

func seedRunningWorkflowRepository(t *testing.T, now time.Time) *memory.SessionRepository {
	t.Helper()
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "running-session", Slug: "running-session", DisplayName: "Running Session", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = domain.StateRunning, domain.StateRunning, domain.StateRunning, domain.HealthHealthy
	session.Infrastructure = domain.Infrastructure{
		CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"},
		InstanceProfile: "instance-profile", AMIID: "ami-1", InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.1", LastObservedAt: now,
	}
	event := domain.NewSessionCreatedEvent("running-event", "running-correlation", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now)
	idempotency, err := domain.NewCompletedIdempotencyRecord("running-create", "running-hash", session.ID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatal(err)
	}
	return repository
}

func workflowCommand(now time.Time) domain.CommandEnvelope {
	return domain.CommandEnvelope{
		SchemaVersion: 1, CommandID: "command-1", CommandType: domain.CommandStartSession, RequestedAt: now,
		Actor:     domain.CommandActor{DiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1", Roles: []string{"role-1"}},
		SessionID: "session-1", IdempotencyKey: "discord:command-1", CorrelationID: "correlation-workflow",
		Parameters: map[string]string{},
	}
}
