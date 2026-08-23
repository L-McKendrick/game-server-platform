package reset

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type failingQueue struct{ err error }

func (queue failingQueue) Enqueue(context.Context, domain.ResetRequest) error { return queue.err }

func TestServiceRequiresAdministratorAndEnabledGate(t *testing.T) {
	t.Parallel()
	repository := memory.NewSessionRepository()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	disabled, err := NewService(repository, nil, fixedClock{now}, "dev", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disabled.Prepare(context.Background(), "confirm-1", "guild-1", "admin-1", true); !errors.Is(err, domain.ErrFeatureDisabled) {
		t.Fatalf("disabled Prepare error = %v", err)
	}
	enabled, err := NewService(repository, &memory.ResetQueue{}, fixedClock{now}, "dev", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enabled.Prepare(context.Background(), "confirm-2", "guild-1", "manager-1", false); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-Administrator Prepare error = %v", err)
	}
}

func TestServiceStartsOnceAndHoldsEnvironmentLock(t *testing.T) {
	t.Parallel()
	repository := memory.NewSessionRepository()
	queue := &memory.ResetQueue{}
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	service, _ := NewService(repository, queue, fixedClock{now}, "dev", true)
	confirmation, err := service.Prepare(context.Background(), "confirm-1", "guild-1", "admin-1", true)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := service.Start(context.Background(), confirmation.ID, "operation-1", "correlation-1", "guild-1", "admin-1", confirmation.Phrase(), true)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Start(context.Background(), confirmation.ID, "operation-1", "correlation-1", "guild-1", "admin-1", confirmation.Phrase(), true)
	if err != nil || replayed.ID != operation.ID || len(queue.Requests) != 1 {
		t.Fatalf("replay = %#v err=%v requests=%d", replayed, err, len(queue.Requests))
	}
	if _, err := service.Prepare(context.Background(), "confirm-2", "guild-2", "admin-2", true); !errors.Is(err, domain.ErrCommandInProgress) {
		t.Fatalf("second reset Prepare error = %v", err)
	}
}

func TestServiceReportsUncertainDispatchWithoutSchedulingRetry(t *testing.T) {
	t.Parallel()
	repository := memory.NewSessionRepository()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	service, _ := NewService(repository, failingQueue{err: errors.New("network")}, fixedClock{now}, "dev", true)
	confirmation, _ := service.Prepare(context.Background(), "confirm-1", "guild-1", "admin-1", true)
	operation, err := service.Start(context.Background(), confirmation.ID, "operation-1", "correlation-1", "guild-1", "admin-1", confirmation.Phrase(), true)
	if !errors.Is(err, domain.ErrConfirmationDispatchUncertain) || operation.ID != "operation-1" {
		t.Fatalf("Start operation=%#v err=%v", operation, err)
	}
	if _, active, activeErr := service.Active(context.Background()); activeErr != nil || !active {
		t.Fatalf("uncertain operation active=%v err=%v", active, activeErr)
	}
}

type fakeCleaner struct {
	result domainResult
	err    error
	calls  int
}

type domainResult struct{ sessions, objects int }

func (cleaner *fakeCleaner) Cleanup(context.Context, domain.ResetOperation) (result ports.ResetCleanupResult, err error) {
	cleaner.calls++
	return ports.ResetCleanupResult{DeletedSessions: cleaner.result.sessions, DeletedObjects: cleaner.result.objects}, cleaner.err
}

func TestWorkerPersistsTerminalResultAndDoesNotReplayCleanup(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		err    error
		status domain.ResetStatus
	}{
		{name: "success", status: domain.ResetSucceeded},
		{name: "incomplete", err: errors.New("secret detail must not persist"), status: domain.ResetFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := memory.NewSessionRepository()
			queue := &memory.ResetQueue{}
			now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
			service, _ := NewService(repository, queue, fixedClock{now}, "dev", true)
			confirmation, _ := service.Prepare(context.Background(), "confirm-1", "guild-1", "admin-1", true)
			operation, _ := service.Start(context.Background(), confirmation.ID, "operation-1", "correlation-1", "guild-1", "admin-1", confirmation.Phrase(), true)
			cleaner := &fakeCleaner{result: domainResult{sessions: 2, objects: 3}, err: test.err}
			worker, _ := NewWorker(repository, cleaner, fixedClock{now: now.Add(time.Minute)})
			request := queue.Requests[0]
			completed, err := worker.Handle(context.Background(), request)
			if err != nil || completed.Status != test.status || completed.DeletedSessions != 2 || completed.DeletedObjects != 3 {
				t.Fatalf("completed=%#v err=%v", completed, err)
			}
			if test.err != nil && (completed.ErrorCode != "ERR_RESET_INCOMPLETE" || completed.ErrorDetail == test.err.Error()) {
				t.Fatalf("failure detail was not safely redacted: %#v", completed)
			}
			if _, err := worker.Handle(context.Background(), request); err != nil || cleaner.calls != 1 {
				t.Fatalf("terminal replay err=%v calls=%d", err, cleaner.calls)
			}
			latest, found, err := service.Latest(context.Background())
			if err != nil || !found || latest.ID != operation.ID {
				t.Fatalf("latest=%#v found=%v err=%v", latest, found, err)
			}
		})
	}
}
