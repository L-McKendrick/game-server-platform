package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
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
func (runner *testRunner) StartRollback(context.Context, domain.Session) (string, error) {
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
	service, err := NewService(repository, repository, repository, runner, notifications, &testIDs{values: []string{"stage-event", "health-event", "ready-event"}}, testClock{now}, 6*time.Hour)
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
	persistedWorkflow, err := repository.GetWorkflow(context.Background(), workflow.SessionID, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persistedWorkflow.CommandID != "command-1" || !persistedWorkflow.CommandDeadlineAt.Equal(now.Add(6*time.Hour)) {
		t.Fatalf("persisted command deadline = %#v", persistedWorkflow)
	}
	replayed, err := service.Handle(context.Background(), request)
	if err != nil || replayed.CommandID != dispatched.CommandID || runner.starts != 1 {
		t.Fatalf("replayed dispatch = %#v, starts = %d, err = %v", replayed, runner.starts, err)
	}
	prePromotion, err := repository.Get(context.Background(), workflow.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if prePromotion.PresetObjectKey != "sessions/session-1/input/preset.html" || prePromotion.PendingPresetRevision.Status != domain.PresetRevisionApplying {
		t.Fatalf("pending revision promoted before health success: %#v", prePromotion)
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
	if session.LifecycleState != domain.StateRunning || session.HealthStatus != domain.HealthHealthy || session.ActiveWorkflowID != "" || session.ActivePresetRevision.Number != 2 || !session.PendingPresetRevision.Empty() || session.PresetObjectKey != "sessions/session-1/input/preset-v2.html" {
		t.Fatalf("session = %#v", session)
	}
	if len(notifications.requests) != 4 {
		t.Fatalf("notifications = %#v", notifications.requests)
	}
	wantMilestones := []domain.ProgressMilestone{domain.ProgressHostPrepared, domain.ProgressHealthVerification, domain.ProgressCompleted}
	for index, milestone := range wantMilestones {
		request := notifications.requests[index]
		if request.Kind != domain.NotificationSessionCard || request.NotificationID != "card-progress-"+workflow.ID+"-"+progressIDPart(milestone) {
			t.Fatalf("notification %d = %#v", index, request)
		}
	}
	modlist := notifications.requests[3]
	if modlist.Kind != domain.NotificationSessionModlist || modlist.Attachment == nil || modlist.Attachment.ObjectKey != session.ActivePresetRevision.Modlist.ObjectKey || modlist.Attachment.Revision != session.Version {
		t.Fatalf("promoted modlist notification = %#v", modlist)
	}
	if session.Progress.Milestone != domain.ProgressCompleted || session.Progress.WorkflowID != workflow.ID {
		t.Fatalf("progress = %#v", session.Progress)
	}
}

func TestBootstrapServiceAtomicallyFinalizesWorkshopScenarioCollection(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC)
	source := domain.WorkshopMissionSource{
		Source:     domain.WorkshopReference{PublishedFileID: 100, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=100"},
		SourceKind: domain.WorkshopSourceCollection, ResolutionSHA256: strings.Repeat("a", 64),
		AcceptedItemIDs: []uint64{200, 300}, ResolvedAt: now,
	}
	repository, workflow := seedBootstrapWithMutation(t, now, func(session *domain.Session) {
		if err := session.RecordWorkshopMissionSource(source, now); err != nil {
			t.Fatal(err)
		}
	})
	firstDigest, secondDigest := strings.Repeat("b", 64), strings.Repeat("c", 64)
	manifest := fmt.Sprintf("%s\tOne.Altis.pbo\tsessions/session-1/input/missions/%s-One.Altis.pbo\t200\n%s\tTwo.Stratis.pbo\tsessions/session-1/input/missions/%s-Two.Stratis.pbo\t300\n", firstDigest, firstDigest, secondDigest, secondDigest)
	service, err := NewService(repository, repository, repository, &testRunner{}, nil, &testIDs{values: []string{"prepare-event", "ready-event"}}, testClock{now}, 6*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service.workshopMissionManifest = &workshopManifestReader{body: []byte(manifest)}
	request := TaskRequest{SessionID: workflow.SessionID, WorkflowID: workflow.ID, CorrelationID: workflow.CorrelationID, Action: ActionPrepare}
	if _, err := service.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	before, _ := repository.Get(context.Background(), workflow.SessionID)
	request.Action = ActionComplete
	if _, err := service.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	completed, _ := repository.Get(context.Background(), workflow.SessionID)
	if completed.Version != before.Version+1 || completed.LifecycleState != domain.StateRunning || completed.ActiveWorkflowID != "" || len(completed.MissionFiles) != len(before.MissionFiles)+2 {
		t.Fatalf("completed session = %#v", completed)
	}
	if completed.ConfiguredMission != before.ConfiguredMission || completed.CurrentMission != before.CurrentMission {
		t.Fatal("Workshop collection changed configured or current mission")
	}
	if _, err := service.Handle(context.Background(), request); err != nil {
		t.Fatalf("completion replay failed: %v", err)
	}
}

func TestObserveTimesOutPersistedNonterminalCommandWithoutStepFunctionCounter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repository, workflow := seedBootstrap(t, now)
	runner := &testRunner{commandID: "command-1", status: ports.BootstrapCommandStatus{Status: "InProgress"}}
	clock := &testClock{now: now}
	service, err := NewService(repository, repository, repository, runner, nil, &testIDs{values: []string{"prepare-event", "progress-event"}}, clock, 6*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Handle(context.Background(), TaskRequest{Action: ActionPrepare, SessionID: workflow.SessionID, WorkflowID: workflow.ID}); err != nil {
		t.Fatal(err)
	}
	dispatched, err := service.Handle(context.Background(), TaskRequest{Action: ActionDispatch, SessionID: workflow.SessionID, WorkflowID: workflow.ID})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(6 * time.Hour)
	result, err := service.Handle(context.Background(), TaskRequest{Action: ActionObserve, SessionID: workflow.SessionID, WorkflowID: workflow.ID, CommandID: dispatched.CommandID})
	if err != nil || !result.Done || result.Succeeded || result.ErrorCode != "ERR_BOOTSTRAP_TIMEOUT" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestObserveHonorsTerminalSSMResultAtPersistedDeadline(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repository, workflow := seedBootstrap(t, now)
	runner := &testRunner{commandID: "command-1", status: ports.BootstrapCommandStatus{Status: "Success"}}
	clock := &testClock{now: now}
	service, err := NewService(repository, repository, repository, runner, nil, &testIDs{values: []string{"prepare-event", "progress-event"}}, clock, 6*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Handle(context.Background(), TaskRequest{Action: ActionPrepare, SessionID: workflow.SessionID, WorkflowID: workflow.ID}); err != nil {
		t.Fatal(err)
	}
	dispatched, err := service.Handle(context.Background(), TaskRequest{Action: ActionDispatch, SessionID: workflow.SessionID, WorkflowID: workflow.ID})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = now.Add(6 * time.Hour)
	result, err := service.Handle(context.Background(), TaskRequest{Action: ActionObserve, SessionID: workflow.SessionID, WorkflowID: workflow.ID, CommandID: dispatched.CommandID})
	if err != nil || !result.Done || !result.Succeeded {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestObserveRejectsCommandDriftFromPersistedDispatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	repository, workflow := seedBootstrap(t, now)
	runner := &testRunner{commandID: "command-1", status: ports.BootstrapCommandStatus{Status: "InProgress"}}
	service, err := NewService(repository, repository, repository, runner, nil, &testIDs{values: []string{"prepare-event"}}, testClock{now}, 6*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Handle(context.Background(), TaskRequest{Action: ActionPrepare, SessionID: workflow.SessionID, WorkflowID: workflow.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Handle(context.Background(), TaskRequest{Action: ActionDispatch, SessionID: workflow.SessionID, WorkflowID: workflow.ID}); err != nil {
		t.Fatal(err)
	}
	_, err = service.Handle(context.Background(), TaskRequest{Action: ActionObserve, SessionID: workflow.SessionID, WorkflowID: workflow.ID, CommandID: "different-command"})
	if err == nil || !strings.Contains(err.Error(), domain.ErrConflict.Error()) {
		t.Fatalf("err = %v; want command conflict", err)
	}
}

func progressIDPart(milestone domain.ProgressMilestone) string {
	value := strings.ToLower(string(milestone))
	return strings.ReplaceAll(value, "_", "-")
}

func TestObserveSanitizesFailedCommand(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	repository, workflow := seedBootstrap(t, now)
	runner := &testRunner{status: ports.BootstrapCommandStatus{Status: "Failed", ErrorMessage: "installer exited"}}
	service, err := NewService(repository, repository, repository, runner, nil, &testIDs{values: []string{"stage-event"}}, testClock{now}, 6*time.Hour)
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

func TestObservePreservesStableSteamReauthorizationCode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	repository, workflow := seedBootstrap(t, now)
	runner := &testRunner{status: ports.BootstrapCommandStatus{Status: "Failed", ErrorCode: "ERR_STEAM_REAUTH_REQUIRED", ErrorMessage: "Steam authorization requires operator re-enrollment."}}
	service, err := NewService(repository, repository, repository, runner, nil, &testIDs{values: []string{"stage-event"}}, testClock{now}, 6*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Handle(context.Background(), TaskRequest{Action: ActionPrepare, SessionID: workflow.SessionID, WorkflowID: workflow.ID}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Handle(context.Background(), TaskRequest{Action: ActionObserve, SessionID: workflow.SessionID, WorkflowID: workflow.ID, CommandID: "command-1"})
	if err != nil || result.ErrorCode != "ERR_STEAM_REAUTH_REQUIRED" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestObservePersistsManagedBootstrapCheckpointsInOneProgressMutation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 2, 0, 0, 0, time.UTC)
	repository, workflow := seedBootstrap(t, now)
	runner := &testRunner{status: ports.BootstrapCommandStatus{
		Status: "InProgress",
		Checkpoints: []domain.ProgressMilestone{
			domain.ProgressHostPrepared, domain.ProgressGameServerInstalled,
			domain.ProgressModsApplied, domain.ProgressConfigurationReady,
		},
	}}
	notifications := &testNotifications{}
	service, err := NewService(repository, repository, repository, runner, notifications, &testIDs{values: []string{"prepare-event", "progress-event"}}, testClock{now}, 6*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := TaskRequest{Action: ActionPrepare, SessionID: workflow.SessionID, WorkflowID: workflow.ID}
	if _, err := service.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Action, request.CommandID = ActionObserve, "command-1"
	result, err := service.Handle(context.Background(), request)
	if err != nil || result.Done {
		t.Fatalf("observe = %#v, err = %v", result, err)
	}
	session, err := repository.Get(context.Background(), workflow.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	wantCompleted := []domain.ProgressMilestone{
		domain.ProgressAccepted, domain.ProgressHostPrepared,
		domain.ProgressGameServerInstalled, domain.ProgressModsApplied,
	}
	if session.Progress.Milestone != domain.ProgressConfigurationReady || !slices.Equal(session.Progress.CompletedMilestones, wantCompleted) {
		t.Fatalf("progress = %#v; want completed %#v", session.Progress, wantCompleted)
	}
	if len(notifications.requests) != 2 || notifications.requests[1].NotificationID != "card-progress-"+workflow.ID+"-configuration-ready" {
		t.Fatalf("notifications = %#v", notifications.requests)
	}
}

func TestObservePersistsSafeBootstrapActivity(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 11, 0, 0, 0, time.UTC)
	repository, workflow := seedBootstrap(t, now)
	runner := &testRunner{status: ports.BootstrapCommandStatus{
		Status: "InProgress", Activity: "Arma 3 server files",
		Checkpoints: []domain.ProgressMilestone{domain.ProgressHostPrepared, domain.ProgressGameServerInstalled},
	}}
	notifications := &testNotifications{}
	service, err := NewService(repository, repository, repository, runner, notifications, &testIDs{values: []string{"prepare-event", "progress-event", "activity-event"}}, testClock{now}, 6*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := TaskRequest{Action: ActionPrepare, SessionID: workflow.SessionID, WorkflowID: workflow.ID}
	if _, err := service.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Action, request.CommandID = ActionObserve, "command-1"
	if _, err := service.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	session, err := repository.Get(context.Background(), workflow.SessionID)
	if err != nil || session.Progress.Activity != "Arma 3 server files" {
		t.Fatalf("session activity = %q, err = %v", session.Progress.Activity, err)
	}
	if len(notifications.requests) != 3 || !strings.Contains(notifications.requests[2].NotificationID, "game-server-installed-activity-") {
		t.Fatalf("notifications = %#v", notifications.requests)
	}
}

func TestBootstrapFailureRollsBackAndRetainsFailedPendingRevision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	repository, workflow := seedBootstrap(t, now)
	runner := &testRunner{commandID: "rollback-command-1", status: ports.BootstrapCommandStatus{Status: "Success"}}
	service, err := NewService(repository, repository, repository, runner, nil, &testIDs{values: []string{"rollback-progress-event", "rollback-event", "failure-event"}}, testClock{now}, 6*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := TaskRequest{SessionID: workflow.SessionID, WorkflowID: workflow.ID, CorrelationID: workflow.CorrelationID}
	request.Action = ActionRollbackDispatch
	dispatched, err := service.Handle(context.Background(), request)
	if err != nil || dispatched.CommandID != "rollback-command-1" || runner.starts != 1 {
		t.Fatalf("rollback dispatch = %#v starts=%d err=%v", dispatched, runner.starts, err)
	}
	request.Action, request.CommandID = ActionRollbackObserve, dispatched.CommandID
	observed, err := service.Handle(context.Background(), request)
	if err != nil || !observed.Done || !observed.Succeeded {
		t.Fatalf("rollback observe = %#v err=%v", observed, err)
	}
	request.Action, request.ErrorCode, request.ErrorMessage = ActionFail, "ERR_MOD_INSTALL", strings.Repeat("installer diagnosis ", 100)
	if _, err := service.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	session, err := repository.Get(context.Background(), workflow.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.ActivePresetRevision.Number != 1 || session.PresetObjectKey != "sessions/session-1/input/preset.html" {
		t.Fatalf("active revision changed during failure: %#v", session.ActivePresetRevision)
	}
	pending := session.PendingPresetRevision
	if pending.Number != 2 || pending.Status != domain.PresetRevisionFailed || pending.RollbackDisposition != domain.PresetRollbackSucceeded || pending.FailureDetail == "" || len([]rune(pending.FailureDetail)) > domain.MaximumPresetRevisionFailureRunes {
		t.Fatalf("retained pending revision = %#v", pending)
	}
	if session.ActiveWorkflowID != "" || session.LifecycleState != domain.StateFailed {
		t.Fatalf("failed lifecycle state = %#v", session)
	}
}

func seedBootstrap(t *testing.T, now time.Time) (*memory.SessionRepository, domain.Workflow) {
	return seedBootstrapWithMutation(t, now, nil)
}

func seedBootstrapWithMutation(t *testing.T, now time.Time, mutate func(*domain.Session)) (*memory.SessionRepository, domain.Workflow) {
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
	if mutate != nil {
		mutate(&session)
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
	if _, err := session.StagePresetRevision(1, "sessions/session-1/input/preset-v2.html", domain.PresetModlistMetadata{ObjectKey: "sessions/session-1/input/modlists/v2/arma-modlist.html", Filename: "arma-modlist.html", SHA256: strings.Repeat("b", 64), SizeBytes: 1200, WorkshopCount: 2}, now); err != nil {
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
