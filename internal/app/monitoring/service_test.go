package monitoring

import (
	"context"
	"errors"
	"testing"
	"time"

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
	wantID := domain.AutomaticSleepCommandID(session.ID, session.IdleSince)
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
	wantID := domain.AutomaticArchiveCommandID(session.ID, session.SleepingSince)
	if command.CommandID != wantID || command.CommandType != domain.CommandArchiveSession || command.Parameters[domain.AutomaticSleepingSinceParameter] != session.SleepingSince.Format(time.RFC3339Nano) {
		t.Fatalf("automatic archive command = %#v", command)
	}
	failing := &monitoringCommands{err: errors.New("queue unavailable")}
	service, _ = NewService(repo, monitoringRunner{}, nil, &monitoringIDs{}, monitoringClock{now}, WithCommandQueue(failing))
	if _, err := service.Run(context.Background()); err == nil || len(failing.commands) != 1 {
		t.Fatalf("archive queue failure error=%v commands=%#v", err, failing.commands)
	}
}
