package sleepwake

import (
	"context"
	"fmt"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"testing"
	"time"
)

type restartClock struct{ now time.Time }

func (c restartClock) Now() time.Time { return c.now }

type restartIDs struct{ n int }

func (i *restartIDs) New(time.Time) (string, error) { i.n++; return fmt.Sprintf("event-%d", i.n), nil }

type restartCompute struct {
	managedCompute
	mutations int
}

func (c *restartCompute) StartInstance(context.Context, string) error { c.mutations++; return nil }
func (c *restartCompute) StopInstance(context.Context, string) error  { c.mutations++; return nil }
func (c *restartCompute) ObserveInstance(context.Context, string) (domain.ComputeObservation, error) {
	return domain.ComputeObservation{State: "running", PublicIPv4: "203.0.113.1"}, nil
}

type restartMonitor struct{ healthy bool }

func (m restartMonitor) Start(context.Context, domain.Session) (string, error) {
	return "health-1", nil
}
func (m restartMonitor) Observe(context.Context, string, string) (ports.MonitoringCommandStatus, error) {
	return ports.MonitoringCommandStatus{Status: "Success", Observation: domain.HealthObservation{ArmaService: m.healthy, ArmaUDP: m.healthy}}, nil
}

func TestRestartFinishesAndReplaysWithoutInstanceMutation(t *testing.T) {
	for _, healthy := range []bool{true, false} {
		t.Run(fmt.Sprint(healthy), func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
			repo := memory.NewSessionRepository()
			session, err := domain.NewSession(domain.NewSessionInput{ID: "restart-session", Slug: "restart-session", DisplayName: "Restart", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
			if err != nil {
				t.Fatal(err)
			}
			session.LifecycleState, session.DesiredState, session.ObservedState, session.HealthStatus = domain.StateRunning, domain.StateRunning, domain.StateRunning, domain.HealthHealthy
			session.TeamSpeakEnabled = true
			session.Infrastructure = domain.Infrastructure{CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile", AMIID: "ami-1", InstanceType: "c7i.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.1", LastObservedAt: now}
			actor := domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner"}
			record, _ := domain.NewCompletedIdempotencyRecord("create", "hash", session.ID, now, time.Hour)
			if err := repo.Create(ctx, session, domain.NewSessionCreatedEvent("created", "correlation", actor, session, now), record); err != nil {
				t.Fatal(err)
			}
			expected := session.Version
			if err := session.BeginRestart("restart-1", time.Hour, now); err != nil {
				t.Fatal(err)
			}
			wf := domain.Workflow{ID: "restart-1", SessionID: session.ID, Type: domain.RestartWorkflowType, Status: domain.WorkflowRunning, RequestedBy: "owner", CorrelationID: "correlation", ExpectedVersion: expected, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}
			if err := repo.AcquireWorkflow(ctx, session, expected, wf, domain.NewWorkflowEvent("started", domain.EventWorkflowStarted, "correlation", actor, session, wf, now)); err != nil {
				t.Fatal(err)
			}
			compute := &restartCompute{}
			runner := &presetRunner{status: ports.BootstrapCommandStatus{Status: "Success"}}
			service, err := NewService(repo, repo, repo, compute, restartMonitor{healthy}, nil, &restartIDs{}, restartClock{now}, WithPresetRevisionRunner(runner))
			if err != nil {
				t.Fatal(err)
			}
			call := func(action, command string) TaskResult {
				t.Helper()
				result, err := service.Handle(ctx, TaskRequest{Action: action, SessionID: session.ID, WorkflowID: wf.ID, CommandID: command, ErrorCode: "ERR_RESTART_FAILED"})
				if err != nil {
					t.Fatalf("%s: %v", action, err)
				}
				return result
			}
			if !call(ActionDispatch, "").Restart || compute.mutations != 0 {
				t.Fatal("restart mutated EC2")
			}
			dispatched := call(ActionContentDispatch, "")
			if dispatched.Done || dispatched.CommandID == "" || runner.starts != 1 {
				t.Fatal("no-change restart was skipped")
			}
			if !call(ActionContentObserve, dispatched.CommandID).Succeeded {
				t.Fatal("restart not observed")
			}
			call(ActionHealthDispatch, "")
			result := call(ActionHealthObserve, "health-1")
			if result.Succeeded != healthy {
				t.Fatalf("health=%#v", result)
			}
			terminal := ActionComplete
			if !healthy {
				terminal = ActionFail
				before := runner.starts
				if !call(ActionRollbackDispatch, "").Done || runner.starts != before {
					t.Fatal("restart attempted rollback")
				}
			}
			call(terminal, "")
			call(terminal, "")
			stored, _ := repo.Get(ctx, session.ID)
			want := domain.StateRunning
			if !healthy {
				want = domain.StateFailed
			}
			if stored.LifecycleState != want || stored.ActiveWorkflowID != "" || !stored.TeamSpeakEnabled || stored.Infrastructure.InstanceID != "i-1" || compute.mutations != 0 {
				t.Fatalf("session=%#v", stored)
			}
			if _, err := service.Handle(ctx, TaskRequest{Action: ActionContentDispatch, SessionID: session.ID, WorkflowID: wf.ID}); err == nil {
				t.Fatal("stale dispatch accepted")
			}
		})
	}
}

func TestRestartContentFailureNeverClaimsHealth(t *testing.T) {
	for _, status := range []string{"Failed", "TimedOut", "Cancelled"} {
		runner := &presetRunner{status: ports.BootstrapCommandStatus{Status: status, ErrorCode: "ERR_WORKSHOP_DISK_SPACE", ErrorMessage: "Disk capacity unavailable"}}
		service := &Service{contentRunner: runner}
		result, err := service.observeContent(context.Background(), domain.Session{ID: "session", ActiveWorkflowID: "restart"}, domain.Workflow{ID: "restart", Type: domain.RestartWorkflowType}, "command")
		if err != nil || !result.Done || result.Succeeded || result.ErrorCode != "ERR_WORKSHOP_DISK_SPACE" {
			t.Fatalf("status=%s result=%#v err=%v", status, result, err)
		}
	}
}
