package sleepwake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type managedCompute struct {
	managed bool
	err     error
}

func (compute managedCompute) FindInstance(context.Context, domain.ComputeLaunchRequest) (domain.ComputeObservation, bool, error) {
	return domain.ComputeObservation{}, false, nil
}

func (compute managedCompute) EnsureInstance(context.Context, domain.ComputeLaunchRequest, string) (domain.ComputeObservation, error) {
	return domain.ComputeObservation{}, nil
}

func (compute managedCompute) ObserveInstance(context.Context, string) (domain.ComputeObservation, error) {
	return domain.ComputeObservation{}, nil
}

func (compute managedCompute) IsManaged(context.Context, string) (bool, error) {
	return compute.managed, compute.err
}

func (managedCompute) StopInstance(context.Context, string) error  { return nil }
func (managedCompute) StartInstance(context.Context, string) error { return nil }

func TestServiceCheckManaged(t *testing.T) {
	tests := []struct {
		name    string
		compute managedCompute
		want    bool
		wantErr bool
	}{
		{name: "offline node remains pending", compute: managedCompute{}, want: false},
		{name: "online node advances", compute: managedCompute{managed: true}, want: true},
		{name: "adapter failure is returned", compute: managedCompute{err: errors.New("describe failed")}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &Service{compute: test.compute}
			result, err := service.checkManaged(context.Background(), domain.Session{ID: "session-1", Infrastructure: domain.Infrastructure{InstanceID: "i-test"}}, domain.Workflow{ID: "wake-1"})
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("check managed: %v", err)
			}
			if result.Managed != test.want {
				t.Fatalf("managed = %v, want %v", result.Managed, test.want)
			}
			if result.SessionID != "session-1" || result.WorkflowID != "wake-1" {
				t.Fatalf("unexpected task identity: %+v", result)
			}
		})
	}
}

type presetRunner struct {
	starts int
	status ports.BootstrapCommandStatus
}

func (runner *presetRunner) Start(context.Context, domain.Session) (string, error) {
	runner.starts++
	return "mods-command-1", nil
}

func (runner *presetRunner) StartContent(context.Context, domain.Session, domain.WorkshopTarget, bool) (string, error) {
	runner.starts++
	return "mods-command-1", nil
}

func (runner *presetRunner) StartRollback(context.Context, domain.Session) (string, error) {
	runner.starts++
	return "rollback-command-1", nil
}

func (runner *presetRunner) Observe(context.Context, string, string) (ports.BootstrapCommandStatus, error) {
	return runner.status, nil
}

func TestWakeDispatchesApplyingPresetBeforeHealth(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 23, 0, 0, 0, time.UTC)
	runner := &presetRunner{status: ports.BootstrapCommandStatus{Status: "Success"}}
	service := &Service{presetRunner: runner, contentRunner: runner}
	session := domain.Session{ID: "session-1", ActiveWorkflowID: "wake-1", LifecycleState: domain.StateWaking, Infrastructure: domain.Infrastructure{InstanceID: "i-1"}, PendingPresetRevision: domain.PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/v2.html", Status: domain.PresetRevisionApplying, StagedAt: now, ApplyWorkflowID: "wake-1", ApplyStartedAt: now}}
	workflow := domain.Workflow{ID: "wake-1", Type: domain.WakeWorkflowType}
	dispatched, err := service.dispatchMods(context.Background(), session, workflow)
	if err != nil || dispatched.CommandID != "mods-command-1" || runner.starts != 1 || dispatched.Done || dispatched.Succeeded {
		t.Fatalf("dispatch = %#v starts=%d err=%v", dispatched, runner.starts, err)
	}
	observed, err := service.observeMods(context.Background(), session, workflow, dispatched.CommandID)
	if err != nil || !observed.Done || !observed.Succeeded {
		t.Fatalf("observe = %#v err=%v", observed, err)
	}
	session.PendingPresetRevision = domain.PresetRevision{}
	skipped, err := service.dispatchMods(context.Background(), session, workflow)
	if err != nil || !skipped.Done || !skipped.Succeeded || runner.starts != 1 {
		t.Fatalf("no-pending dispatch = %#v starts=%d err=%v", skipped, runner.starts, err)
	}
}

func TestWakeDispatchesWorkshopMissionsWithoutPendingMods(t *testing.T) {
	runner := &presetRunner{status: ports.BootstrapCommandStatus{Status: "Success"}}
	service := &Service{presetRunner: runner, contentRunner: runner}
	session := domain.Session{ID: "session-1", ActiveWorkflowID: "wake-1", LifecycleState: domain.StateWaking, Infrastructure: domain.Infrastructure{InstanceID: "i-1"}, WorkshopMissionSources: []domain.WorkshopMissionSource{{}}}
	workflow := domain.Workflow{ID: "wake-1", Type: domain.WakeWorkflowType}
	result, err := service.dispatchContent(context.Background(), session, workflow)
	if err != nil || result.CommandID != "mods-command-1" || runner.starts != 1 {
		t.Fatalf("dispatch = %#v starts=%d err=%v", result, runner.starts, err)
	}
}
