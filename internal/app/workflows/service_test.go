package workflows

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	notifications := memory.NewNotificationQueue()
	service, err := NewService(repository, repository, starter, allowAuthorizer{}, &workflowIDs{ids: []string{"workflow-start-event"}}, workflowClock{now}, 2*time.Hour, WithNotificationQueue(notifications))
	if err != nil {
		t.Fatalf("NewService() returned error: %v", err)
	}
	command := workflowCommand(now)
	command.Parameters = map[string]string{
		domain.ServerConfigModeParameter: domain.ServerConfigModeCustom, domain.ServerConfigRevisionParameter: "2",
		domain.ServerConfigObjectParameter: "guilds/guild-1/server-config/revisions/000002-" + strings.Repeat("a", 64) + "/server.cfg", domain.ServerConfigSHAParameter: strings.Repeat("a", 64),
	}

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
	if session.ServerConfigRevision != 2 || session.ServerConfigObjectKey != command.Parameters[domain.ServerConfigObjectParameter] {
		t.Fatalf("server config snapshot = revision %d key %q", session.ServerConfigRevision, session.ServerConfigObjectKey)
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
	requests := notifications.Requests()
	if len(requests) != 1 || requests[0].NotificationID != "card-progress-command-1-accepted" || requests[0].Kind != domain.NotificationSessionCard {
		t.Fatalf("notifications = %#v", requests)
	}
}

func TestStartFailureMarksWorkflowFailedAndReleasesLock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 22, 0, 0, 0, time.UTC)
	repository := seedWorkflowRepository(t, now)
	starter := &workflowStarter{err: errors.New("Step Functions unavailable")}
	notifications := memory.NewNotificationQueue()
	service, err := NewService(
		repository, repository, starter, allowAuthorizer{},
		&workflowIDs{ids: []string{"workflow-start-event", "workflow-failed-event"}},
		workflowClock{now}, 2*time.Hour, WithNotificationQueue(notifications),
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
	if session.Progress.Milestone != domain.ProgressAccepted || session.Progress.State != domain.ProgressActionRequired || session.Progress.WorkflowID != workflow.ID {
		t.Fatalf("progress = %#v", session.Progress)
	}
	if session.Failure.Code != "ERR_WORKFLOW_START_FAILED" || session.Failure.RetryDisposition != domain.RetryNotScheduled ||
		strings.Contains(session.Failure.Detail, "Step Functions unavailable") {
		t.Fatalf("sanitized start failure = %#v", session.Failure)
	}
	requests := notifications.Requests()
	if len(requests) != 2 || requests[0].NotificationID != "card-progress-command-1-accepted" || requests[1].NotificationID != "card-progress-command-1-accepted-action-required" {
		t.Fatalf("notifications = %#v", requests)
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

func TestStart_ResumesTrustedBootstrapContinuationWithoutDiscordRoleReplay(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	repository, command := seedBootstrapContinuation(t, now)
	starter := &workflowStarter{arn: "arn:aws:states:us-west-2:123456789012:execution:BootstrapGameServer:" + command.CommandID}
	service, err := NewService(repository, repository, starter, rejectAuthorizer{}, &workflowIDs{}, workflowClock{now}, 8*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := service.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if workflow.Status != domain.WorkflowRunning || workflow.ExecutionARN != starter.arn || starter.calls != 1 {
		t.Fatalf("workflow = %#v, starter calls = %d", workflow, starter.calls)
	}
}

func TestStart_RejectsForgedBootstrapContinuation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*domain.CommandEnvelope)
	}{
		{name: "wrong command ID", mutate: func(command *domain.CommandEnvelope) { command.CommandID = "bootstrap-forged" }},
		{name: "wrong requester", mutate: func(command *domain.CommandEnvelope) { command.Actor.DiscordUserID = "attacker" }},
		{name: "wrong guild", mutate: func(command *domain.CommandEnvelope) { command.Actor.GuildID = "other-guild" }},
		{name: "wrong correlation", mutate: func(command *domain.CommandEnvelope) { command.CorrelationID = "other-correlation" }},
		{name: "wrong idempotency", mutate: func(command *domain.CommandEnvelope) { command.IdempotencyKey = "discord:forged" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository, command := seedBootstrapContinuation(t, now)
			test.mutate(&command)
			starter := &workflowStarter{arn: "unexpected"}
			service, err := NewService(repository, repository, starter, rejectAuthorizer{}, &workflowIDs{}, workflowClock{now}, 8*time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Start(context.Background(), command); !errors.Is(err, domain.ErrForbidden) {
				t.Fatalf("Start() error = %v; want forbidden", err)
			}
			if starter.calls != 0 {
				t.Fatalf("starter calls = %d", starter.calls)
			}
		})
	}
}

func TestStart_TerminationIsOwnerOnlyAndAcquiresDeletingState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	repository := seedRunningWorkflowRepository(t, now)
	starter := &workflowStarter{arn: "arn:aws:states:us-west-2:123456789012:execution:DestroySession:terminate-owner"}
	service, err := NewService(repository, repository, starter, allowAuthorizer{}, &workflowIDs{ids: []string{"termination-started"}}, workflowClock{now}, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.CommandEnvelope{
		SchemaVersion: 1, CommandID: "terminate-admin", CommandType: domain.CommandDestroySession, RequestedAt: now,
		Actor:     domain.CommandActor{DiscordUserID: "admin-1", GuildID: "guild-1", ChannelID: "channel-1", CanManageGuild: true},
		SessionID: "running-session", IdempotencyKey: "discord:terminate-admin", CorrelationID: "terminate-admin", Parameters: map[string]string{},
	}
	if _, err := service.Start(context.Background(), admin); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("administrator termination error = %v; want forbidden", err)
	}
	owner := admin
	owner.CommandID, owner.IdempotencyKey, owner.CorrelationID = "terminate-owner", "discord:terminate-owner", "terminate-owner"
	owner.Actor.DiscordUserID, owner.Actor.CanManageGuild = "owner-1", false
	workflow, err := service.Start(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	session, err := repository.Get(context.Background(), "running-session")
	if err != nil {
		t.Fatal(err)
	}
	if workflow.Type != domain.TerminationWorkflowType || session.LifecycleState != domain.StateDeleting || session.ActiveWorkflowID != owner.CommandID {
		t.Fatalf("workflow = %#v, session = %#v", workflow, session)
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

func seedBootstrapContinuation(t *testing.T, now time.Time) (*memory.SessionRepository, domain.CommandEnvelope) {
	t.Helper()
	repository := seedWorkflowRepository(t, now)
	session, err := repository.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	expectedVersion := session.Version
	if err := session.AcquireProvisioningWorkflowLock("provision-1", 2*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	provision := domain.Workflow{
		ID: "provision-1", SessionID: session.ID, Type: domain.ProvisionWorkflowType, Status: domain.WorkflowRunning,
		RequestedBy: session.OwnerDiscordUserID, CorrelationID: "start-correlation", ExpectedVersion: expectedVersion,
		ExecutionARN: "arn:provision", CurrentStage: "Started", StartedAt: now, LeaseExpiresAt: now.Add(2 * time.Hour),
	}
	actor := domain.Actor{Type: domain.ActorTypeDiscordUser, ID: session.OwnerDiscordUserID}
	if err := repository.AcquireWorkflow(context.Background(), session, expectedVersion, provision,
		domain.NewWorkflowEvent("provision-start", domain.EventWorkflowStarted, provision.CorrelationID, actor, session, provision, now)); err != nil {
		t.Fatal(err)
	}
	expectedVersion = session.Version
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus =
		domain.StateRunning, domain.StateProvisioning, domain.StateProvisioning, domain.HealthStarting
	session.Infrastructure = domain.Infrastructure{
		CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"},
		InstanceProfile: "profile", AMIID: "ami-1", InstanceType: "c7i.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.1", LastObservedAt: now,
	}
	if err := session.CompleteInfrastructureProvisioning(provision.ID, now); err != nil {
		t.Fatal(err)
	}
	provision.Status, provision.CurrentStage, provision.CompletedAt = domain.WorkflowSucceeded, "InfrastructureReady", now
	if err := repository.CompleteWorkflow(context.Background(), session, expectedVersion, provision,
		domain.NewProvisioningEvent("provision-complete", domain.EventInfrastructureReady, "InfrastructureReady", provision, session, now)); err != nil {
		t.Fatal(err)
	}
	continuationID := domain.BootstrapContinuationCommandID(provision.ID)
	expectedVersion = session.Version
	if err := session.AcquireBootstrapWorkflowLock(continuationID, 8*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	bootstrap := domain.Workflow{
		ID: continuationID, SessionID: session.ID, Type: domain.BootstrapWorkflowType, Status: domain.WorkflowPending,
		RequestedBy: session.OwnerDiscordUserID, CorrelationID: provision.CorrelationID, ExpectedVersion: expectedVersion,
		StartedAt: now, LeaseExpiresAt: now.Add(8 * time.Hour),
	}
	if err := repository.AcquireWorkflow(context.Background(), session, expectedVersion, bootstrap,
		domain.NewWorkflowEvent("bootstrap-start", domain.EventWorkflowStarted, bootstrap.CorrelationID,
			domain.Actor{Type: domain.ActorTypeSystem, ID: domain.ProvisionWorkflowType}, session, bootstrap, now)); err != nil {
		t.Fatal(err)
	}
	return repository, domain.CommandEnvelope{
		SchemaVersion: 1, CommandID: continuationID, CommandType: domain.CommandBootstrapServer, RequestedAt: now,
		Actor:     domain.CommandActor{DiscordUserID: session.OwnerDiscordUserID, GuildID: session.GuildID, ChannelID: session.ChannelID},
		SessionID: session.ID, IdempotencyKey: "workflow-continuation:" + provision.ID, CorrelationID: provision.CorrelationID,
		Parameters: map[string]string{domain.BootstrapContinuationParameter: provision.ID},
	}
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
