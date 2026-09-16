package monitoring

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	workflowapp "github.com/L-McKendrick/game-server-platform/internal/app/workflows"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type monitoringRepo struct {
	session domain.Session
	events  []domain.SessionEvent
}

func (repo *monitoringRepo) ListInactivityCandidates(context.Context, int32) ([]domain.Session, error) {
	return []domain.Session{repo.session}, nil
}
func (repo *monitoringRepo) SaveMonitoring(_ context.Context, session domain.Session, expected int64, events []domain.SessionEvent) error {
	if repo.session.Version != expected || session.Version != expected+1 {
		return domain.ErrConflict
	}
	repo.session = session
	repo.events = append(repo.events, events...)
	return nil
}

type multiMonitoringRepo struct {
	sessions []domain.Session
	events   []domain.SessionEvent
}

func (repo *multiMonitoringRepo) ListInactivityCandidates(context.Context, int32) ([]domain.Session, error) {
	return append([]domain.Session(nil), repo.sessions...), nil
}

func (repo *multiMonitoringRepo) SaveMonitoring(_ context.Context, session domain.Session, expected int64, events []domain.SessionEvent) error {
	for index := range repo.sessions {
		if repo.sessions[index].ID != session.ID {
			continue
		}
		if repo.sessions[index].Version != expected || session.Version != expected+1 {
			return domain.ErrConflict
		}
		repo.sessions[index] = session
		repo.events = append(repo.events, events...)
		return nil
	}
	return domain.ErrNotFound
}

type monitoringRunner struct{ status ports.MonitoringCommandStatus }

func (runner monitoringRunner) Start(context.Context, domain.Session) (string, error) {
	return "command-1", nil
}
func (runner monitoringRunner) Observe(context.Context, string, string) (ports.MonitoringCommandStatus, error) {
	return runner.status, nil
}

type monitoringQuery struct {
	status domain.PlayerStatus
	err    error
}

type monitoringCommands struct {
	commands []domain.CommandEnvelope
	err      error
}

type durationNotifications struct {
	requests []domain.NotificationRequest
	err      error
}

func (queue *durationNotifications) Enqueue(_ context.Context, request domain.NotificationRequest) error {
	if queue.err != nil {
		return queue.err
	}
	queue.requests = append(queue.requests, request)
	return nil
}

func TestMaximumDurationWarningRetriesFailedEnqueue(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	session := runningMonitoringSession(t, now)
	session.MaximumDuration.StartedAt = now.Add(-23 * time.Hour)
	session.MaximumDuration.DeadlineAt = now.Add(45 * time.Minute)
	session.MaximumDuration.Seconds = int64(session.MaximumDuration.DeadlineAt.Sub(session.MaximumDuration.StartedAt) / time.Second)
	session.SleepAfterSeconds, session.IdleSince, session.PlayerCountKnown, session.PlayerCount, session.PlayerCountObservedAt = 60*60, now.Add(-15*time.Minute), true, 0, now
	repo := &monitoringRepo{session: session}
	warnings := &durationNotifications{err: errors.New("queue unavailable")}
	service, err := NewService(repo, monitoringRunner{}, warnings, &monitoringIDs{}, monitoringClock{now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err == nil {
		t.Fatal("expected enqueue failure")
	}
	if repo.session.MaximumDurationWarningLevel != 1 || repo.session.MaximumDurationWarningQueuedLevel != 0 || len(repo.events) != 1 {
		t.Fatalf("pending warning = %#v, events = %#v", repo.session, repo.events)
	}
	warnings.err = nil
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	durationCount, eventCount := 0, 0
	for _, request := range warnings.requests {
		if request.Kind == domain.NotificationSessionDuration {
			durationCount++
		}
	}
	for _, event := range repo.events {
		if event.Type == domain.EventLifecycleTimeoutWarning {
			eventCount++
		}
	}
	if durationCount != 1 || repo.session.MaximumDurationWarningQueuedLevel != 1 || eventCount != 1 {
		t.Fatalf("duration warnings = %d, queued level = %d, duration events = %d", durationCount, repo.session.MaximumDurationWarningQueuedLevel, eventCount)
	}
}

func TestRunContinuesAfterOneSessionFails(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	warning := runningMonitoringSession(t, now)
	warning.ID, warning.Slug = "warning-session", "warning-session"
	warning.MaximumDuration.StartedAt = now.Add(-23 * time.Hour)
	warning.MaximumDuration.DeadlineAt = now.Add(45 * time.Minute)
	warning.MaximumDuration.Seconds = int64(warning.MaximumDuration.DeadlineAt.Sub(warning.MaximumDuration.StartedAt) / time.Second)
	warning.SleepAfterSeconds, warning.IdleSince, warning.PlayerCountKnown, warning.PlayerCount, warning.PlayerCountObservedAt = 60*60, now.Add(-15*time.Minute), true, 0, now
	expired := runningMonitoringSession(t, now)
	expired.ID, expired.Slug = "expired-session", "expired-session"
	expired.DesiredState, expired.ObservedState, expired.LifecycleState, expired.HealthStatus = domain.StateSleeping, domain.StateSleeping, domain.StateSleeping, domain.HealthStopped
	expired.SleepingSince = now.Add(-24 * time.Hour)
	expired.ArchiveAfterSeconds = 24 * 60 * 60
	expired.MaximumDuration.StartedAt = now.Add(-24 * time.Hour)
	expired.MaximumDuration.DeadlineAt = now
	repo := &multiMonitoringRepo{sessions: []domain.Session{warning, expired}}
	commands := &monitoringCommands{}
	service, err := NewService(repo, monitoringRunner{}, &durationNotifications{err: errors.New("queue unavailable")}, &monitoringIDs{}, monitoringClock{now}, WithCommandQueue(commands))
	if err != nil {
		t.Fatal(err)
	}
	completed, err := service.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), warning.ID) {
		t.Fatalf("run error = %v", err)
	}
	if completed != 1 || len(commands.commands) != 1 || commands.commands[0].SessionID != expired.ID || commands.commands[0].CommandType != domain.CommandArchiveSession {
		t.Fatalf("completed=%d commands=%#v", completed, commands.commands)
	}
}

func TestMaximumDurationWarningIsBoundedAndDeadlineQueuesSleep(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	session := runningMonitoringSession(t, now)
	session.MaximumDuration.StartedAt = now.Add(-23 * time.Hour)
	session.MaximumDuration.DeadlineAt = now.Add(45 * time.Minute)
	session.MaximumDuration.Seconds = int64(session.MaximumDuration.DeadlineAt.Sub(session.MaximumDuration.StartedAt) / time.Second)
	session.SleepAfterSeconds, session.IdleSince, session.PlayerCountKnown, session.PlayerCount, session.PlayerCountObservedAt = 60*60, now.Add(-15*time.Minute), true, 0, now
	repo := &monitoringRepo{session: session}
	warnings := &durationNotifications{}
	commands := &monitoringCommands{}
	service, err := NewService(repo, monitoringRunner{status: ports.MonitoringCommandStatus{Status: "Success", Observation: domain.HealthObservation{ArmaService: true, ArmaUDP: true}}}, warnings, &monitoringIDs{}, monitoringClock{now}, WithPlayerQuery(monitoringQuery{status: domain.PlayerStatus{PlayerCount: 0}}), WithCommandQueue(commands))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(warnings.requests) != 1 || warnings.requests[0].Kind != domain.NotificationSessionDuration || len(warnings.requests[0].AllowedUserIDs) != 1 || warnings.requests[0].AllowedUserIDs[0] != session.OwnerDiscordUserID {
		t.Fatalf("warnings = %#v", warnings.requests)
	}
	if repo.session.MaximumDurationWarningLevel != 1 {
		t.Fatalf("warning marker = %#v", repo.session)
	}
	service.clock = monitoringClock{now.Add(time.Hour)}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(commands.commands) != 1 || commands.commands[0].CommandType != domain.CommandSleepSession {
		t.Fatalf("deadline commands = %#v", commands.commands)
	}
	if err := domain.ValidateAutomaticSleepCommand(commands.commands[0], repo.session, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestMaximumDurationWarningsAdvanceAndResetOnlyForNewDeadline(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	session := runningMonitoringSession(t, now)
	session.MaximumDuration.StartedAt = now.Add(-23 * time.Hour)
	session.MaximumDuration.DeadlineAt = now.Add(45 * time.Minute)
	session.MaximumDuration.Seconds = int64(session.MaximumDuration.DeadlineAt.Sub(session.MaximumDuration.StartedAt) / time.Second)
	session.SleepAfterSeconds, session.IdleSince, session.PlayerCountKnown, session.PlayerCount, session.PlayerCountObservedAt = 60*60, now.Add(-15*time.Minute), true, 0, now
	repo := &monitoringRepo{session: session}
	warnings := &durationNotifications{}
	service, err := NewService(repo, monitoringRunner{status: ports.MonitoringCommandStatus{Status: "Success", Observation: domain.HealthObservation{ArmaService: true, ArmaUDP: true}}}, warnings, &monitoringIDs{}, monitoringClock{now}, WithPlayerQuery(monitoringQuery{status: domain.PlayerStatus{PlayerCount: 0}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{now, now.Add(31 * time.Minute), now.Add(31 * time.Minute)} {
		service.clock = monitoringClock{at}
		if _, err := service.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	durationWarnings := func() []domain.NotificationRequest {
		var result []domain.NotificationRequest
		for _, request := range warnings.requests {
			if request.Kind == domain.NotificationSessionDuration {
				result = append(result, request)
			}
		}
		return result
	}
	if requests := durationWarnings(); len(requests) != 2 || repo.session.MaximumDurationWarningLevel != 2 || repo.session.MaximumDurationWarningQueuedLevel != 2 || requests[0].NotificationID == requests[1].NotificationID {
		t.Fatalf("duration warnings = %d, levels = %d/%d", len(requests), repo.session.MaximumDurationWarningLevel, repo.session.MaximumDurationWarningQueuedLevel)
	}
	repo.session.SleepAfterSeconds = 75 * 60
	service.clock = monitoringClock{now.Add(40 * time.Minute)}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(durationWarnings()) != 3 || repo.session.MaximumDurationWarningLevel != 1 || repo.session.MaximumDurationWarningQueuedLevel != 1 {
		t.Fatalf("extension duration warnings = %d, levels = %d/%d", len(durationWarnings()), repo.session.MaximumDurationWarningLevel, repo.session.MaximumDurationWarningQueuedLevel)
	}
}

func TestFailedDurationDeadlineAlertsWithoutLaunchingUnverifiedCleanup(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	session := runningMonitoringSession(t, now)
	session.DesiredState, session.ObservedState, session.LifecycleState = domain.StateFailed, domain.StateFailed, domain.StateFailed
	session.MaximumDuration.StartedAt = now.Add(-24 * time.Hour)
	session.MaximumDuration.DeadlineAt = now
	repo := &monitoringRepo{session: session}
	warnings := &durationNotifications{}
	commands := &monitoringCommands{}
	service, err := NewService(repo, monitoringRunner{}, warnings, &monitoringIDs{}, monitoringClock{now}, WithCommandQueue(commands))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(warnings.requests) != 1 || repo.session.MaximumDurationWarningLevel != 3 || len(commands.commands) != 0 {
		t.Fatalf("warnings=%#v commands=%#v", warnings.requests, commands.commands)
	}
}

func TestFailedInitialCreationDeadlineQueuesExistingTerminationWorkflow(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	session := runningMonitoringSession(t, now)
	session.DesiredState, session.ObservedState, session.LifecycleState = domain.StateRunning, domain.StateFailed, domain.StateFailed
	session.MaximumDuration.StartedAt = now.Add(-24 * time.Hour)
	session.MaximumDuration.DeadlineAt = now
	session.Progress = domain.SessionProgress{WorkflowID: "bootstrap", WorkflowType: domain.BootstrapWorkflowType, Milestone: domain.ProgressFailed, State: domain.ProgressActionRequired, StartedAt: now.Add(-time.Hour), LastProgressAt: now}
	var err error
	session.Failure, err = domain.NewFailureRecord(domain.FailureRecordInput{Code: "ERR_BOOTSTRAP_FAILED", Stage: "Setup", RetryDisposition: domain.RetryNotScheduled, ResourceImpact: domain.ResourceCostRetained, Detail: "Setup failed", FailedAt: now, SupportReference: "ref_123456"})
	if err != nil {
		t.Fatal(err)
	}
	repo := &monitoringRepo{session: session}
	alerts := &durationNotifications{}
	commands := &monitoringCommands{}
	service, err := NewService(repo, monitoringRunner{}, alerts, &monitoringIDs{}, monitoringClock{now}, WithCommandQueue(commands))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(commands.commands) != 1 || commands.commands[0].CommandType != domain.CommandDestroySession || len(alerts.requests) != 0 {
		t.Fatalf("commands=%#v alerts=%#v", commands.commands, alerts.requests)
	}
	if err := domain.ValidateAutomaticTerminationCommand(commands.commands[0], session, now); err != nil {
		t.Fatal(err)
	}
}

func (queue *monitoringCommands) Enqueue(_ context.Context, command domain.CommandEnvelope) error {
	queue.commands = append(queue.commands, command)
	return queue.err
}

func (query monitoringQuery) Query(context.Context, string) (domain.PlayerStatus, error) {
	return query.status, query.err
}

type monitoringClock struct{ now time.Time }

func (clock monitoringClock) Now() time.Time { return clock.now }

type monitoringIDs struct{ next int }

func (ids *monitoringIDs) New(time.Time) (string, error) {
	ids.next++
	return "event-" + string(rune('0'+ids.next)), nil
}

type lifecycleWorkflowStarter struct{ calls int }

func (starter *lifecycleWorkflowStarter) Start(_ context.Context, workflow domain.Workflow) (string, error) {
	starter.calls++
	return "arn:aws:states:us-west-2:123456789012:execution:" + workflow.Type + ":" + workflow.ID, nil
}

type lifecycleAuthorizer struct{}

func (lifecycleAuthorizer) Authorize(context.Context, string, string, string, []string) error {
	return domain.ErrForbidden
}

func runningMonitoringSession(t *testing.T, now time.Time) domain.Session {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState = domain.StateRunning, domain.StateRunning, domain.StateRunning
	session.HealthStatus = domain.HealthHealthy
	session.Infrastructure = domain.Infrastructure{CapacitySlotID: "slot-1", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile", AMIID: "ami-1", InstanceType: "c7i.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.1", LastObservedAt: now.Add(-time.Minute)}
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestRunPersistsKnownPlayerActivityAndImmutableEvidence(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	repo := &monitoringRepo{session: runningMonitoringSession(t, now)}
	ids := &monitoringIDs{}
	service, err := NewService(repo, monitoringRunner{status: ports.MonitoringCommandStatus{Status: "Success", Observation: domain.HealthObservation{ArmaService: true, ArmaUDP: true}}}, nil, ids, monitoringClock{now}, WithPlayerQuery(monitoringQuery{status: domain.PlayerStatus{PlayerCount: 0}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !repo.session.PlayerCountKnown || !repo.session.IdleSince.Equal(now) || len(repo.events) != 1 || repo.events[0].Type != domain.EventPlayerActivityObserved {
		t.Fatalf("monitoring result session=%#v events=%#v", repo.session, repo.events)
	}
}

func TestRunTreatsFailedPlayerQueryAsUnknownAndBreaksIdleContinuity(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	session := runningMonitoringSession(t, now)
	if err := session.RecordPlayerActivity(domain.PlayerActivityObservation{Known: true, PlayerCount: 0, ObservedAt: now.Add(-10 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	repo := &monitoringRepo{session: session}
	service, err := NewService(repo, monitoringRunner{status: ports.MonitoringCommandStatus{Status: "Success", Observation: domain.HealthObservation{ArmaService: true, ArmaUDP: true}}}, nil, &monitoringIDs{}, monitoringClock{now}, WithPlayerQuery(monitoringQuery{err: errors.New("query unavailable")}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.session.PlayerCountKnown || !repo.session.IdleSince.IsZero() || repo.events[0].Data["known"] != "false" {
		t.Fatalf("failed query became zero activity: session=%#v event=%#v", repo.session, repo.events[0])
	}
}

func TestRunQueuesDeterministicAutomaticSleepOnlyWhenDue(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	session := runningMonitoringSession(t, now)
	session.PlayerCountKnown, session.PlayerCount = true, 0
	session.IdleSince, session.PlayerCountObservedAt = now.Add(-30*time.Minute), now.Add(-5*time.Minute)
	repo := &monitoringRepo{session: session}
	queue := &monitoringCommands{}
	service, err := NewService(repo, monitoringRunner{status: ports.MonitoringCommandStatus{Status: "Success", Observation: domain.HealthObservation{ArmaService: true, ArmaUDP: true}}}, nil, &monitoringIDs{}, monitoringClock{now}, WithPlayerQuery(monitoringQuery{status: domain.PlayerStatus{PlayerCount: 0}}), WithCommandQueue(queue))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(queue.commands) != 1 {
		t.Fatalf("commands = %#v", queue.commands)
	}
	command := queue.commands[0]
	wantID := domain.AutomaticSleepCommandID(session.ID, session.AutomaticSleepDeadline())
	if command.CommandID != wantID || !command.Actor.System || command.Parameters[domain.AutomaticIdleSinceParameter] != session.IdleSince.Format(time.RFC3339Nano) {
		t.Fatalf("automatic sleep command = %#v", command)
	}
}

func TestRunReturnsAutomaticSleepQueueFailureForScheduledRetry(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	session := runningMonitoringSession(t, now)
	session.PlayerCountKnown, session.PlayerCount = true, 0
	session.IdleSince, session.PlayerCountObservedAt = now.Add(-30*time.Minute), now.Add(-5*time.Minute)
	repo := &monitoringRepo{session: session}
	queue := &monitoringCommands{err: errors.New("queue unavailable")}
	service, err := NewService(repo, monitoringRunner{status: ports.MonitoringCommandStatus{Status: "Success", Observation: domain.HealthObservation{ArmaService: true, ArmaUDP: true}}}, nil, &monitoringIDs{}, monitoringClock{now}, WithPlayerQuery(monitoringQuery{status: domain.PlayerStatus{PlayerCount: 0}}), WithCommandQueue(queue))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err == nil || len(queue.commands) != 1 {
		t.Fatalf("queue failure error=%v commands=%#v", err, queue.commands)
	}
}

func TestRunQueuesDeterministicArchiveAfterSeventyTwoSleepingHours(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	session := runningMonitoringSession(t, now)
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = domain.StateSleeping, domain.StateSleeping, domain.StateSleeping, domain.HealthStopped
	session.SleepingSince = now.Add(-72 * time.Hour)
	session.ArchiveAfterSeconds = 72 * 60 * 60
	repo := &monitoringRepo{session: session}
	queue := &monitoringCommands{}
	service, err := NewService(repo, monitoringRunner{}, nil, &monitoringIDs{}, monitoringClock{now}, WithCommandQueue(queue))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(queue.commands) != 1 {
		t.Fatalf("commands = %#v", queue.commands)
	}
	command := queue.commands[0]
	wantID := domain.AutomaticArchiveCommandID(session.ID, session.AutomaticArchiveDeadline())
	if command.CommandID != wantID || command.CommandType != domain.CommandArchiveSession || command.Parameters[domain.AutomaticSleepingSinceParameter] != session.SleepingSince.Format(time.RFC3339Nano) {
		t.Fatalf("automatic archive command = %#v", command)
	}
	failing := &monitoringCommands{err: errors.New("queue unavailable")}
	service, _ = NewService(repo, monitoringRunner{}, nil, &monitoringIDs{}, monitoringClock{now}, WithCommandQueue(failing))
	if _, err := service.Run(context.Background()); err == nil || len(failing.commands) != 1 {
		t.Fatalf("archive queue failure error=%v commands=%#v", err, failing.commands)
	}
}

func TestRunAutomaticSleepCommandStartsExistingWorkflowAndReplaysSafely(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	session := runningMonitoringSession(t, now)
	session.PlayerCountKnown, session.PlayerCount = true, 0
	session.IdleSince, session.PlayerCountObservedAt = now.Add(-30*time.Minute), now.Add(-5*time.Minute)
	monitorRepo := &monitoringRepo{session: session}
	queue := &monitoringCommands{}
	monitorService, err := NewService(
		monitorRepo,
		monitoringRunner{status: ports.MonitoringCommandStatus{Status: "Success", Observation: domain.HealthObservation{ArmaService: true, ArmaUDP: true}}},
		nil,
		&monitoringIDs{},
		monitoringClock{now},
		WithPlayerQuery(monitoringQuery{status: domain.PlayerStatus{PlayerCount: 0}}),
		WithCommandQueue(queue),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := monitorService.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := monitorService.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(queue.commands) != 1 {
		t.Fatalf("automatic commands = %#v", queue.commands)
	}

	repository := memory.NewSessionRepository()
	created := domain.NewSessionCreatedEvent("created", "created", domain.Actor{Type: domain.ActorTypeSystem, ID: "test"}, monitorRepo.session, now)
	idempotency, err := domain.NewCompletedIdempotencyRecord("create", "request-hash", monitorRepo.session.ID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), monitorRepo.session, created, idempotency); err != nil {
		t.Fatal(err)
	}
	starter := &lifecycleWorkflowStarter{}
	notifications := memory.NewNotificationQueue()
	workflowService, err := workflowapp.NewService(
		repository,
		repository,
		starter,
		lifecycleAuthorizer{},
		&monitoringIDs{},
		monitoringClock{now},
		2*time.Hour,
		workflowapp.WithNotificationQueue(notifications),
	)
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := workflowService.Start(context.Background(), queue.commands[0])
	if err != nil {
		t.Fatalf("start automatic sleep: %v", err)
	}
	replayed, err := workflowService.Start(context.Background(), queue.commands[0])
	if err != nil {
		t.Fatalf("replay automatic sleep: %v", err)
	}
	stored, err := repository.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	requests := notifications.Requests()
	if workflow.Type != domain.SleepWorkflowType || replayed.ID != workflow.ID || starter.calls != 1 ||
		stored.ActiveWorkflowID != workflow.ID || stored.LifecycleState != domain.StateStopping ||
		len(requests) != 1 || requests[0].Kind != domain.NotificationSessionCard {
		t.Fatalf("workflow=%#v replay=%#v session=%#v calls=%d notifications=%#v", workflow, replayed, stored, starter.calls, requests)
	}
}
