package reliability

import (
	"context"
	"errors"
	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"testing"
	"time"
)

type maintenanceSessions struct{ ports.ReliabilityRepository }

func (maintenanceSessions) ListActiveWorkflowSessions(context.Context, int32) ([]domain.Session, error) {
	return []domain.Session{{ID: "one", ActiveWorkflowID: "workflow"}, {ID: "two", ActiveWorkflowID: "workflow"}}, nil
}

type maintenanceWorkflows struct{ ports.WorkflowRepository }

func (maintenanceWorkflows) GetWorkflow(context.Context, string, string) (domain.Workflow, error) {
	return domain.Workflow{Status: domain.WorkflowRunning, ExecutionARN: "execution"}, nil
}

type maintenanceInspector struct{ calls int }

func (inspector *maintenanceInspector) Describe(context.Context, string) (domain.WorkflowExecutionStatus, bool, error) {
	inspector.calls++
	return domain.ExecutionRunning, true, nil
}

type failedMaintenance struct {
	calls int
	err   error
}

func (maintenance *failedMaintenance) CleanupSessionHostAttempts(context.Context, string) (int, error) {
	maintenance.calls++
	return 0, maintenance.err
}

func TestCleanupFailureDoesNotPreventWorkflowInspectionAndRemainsVisible(t *testing.T) {
	failed := errors.New("cleanup failed")
	maintenance := &failedMaintenance{err: failed}
	inspector := &maintenanceInspector{}
	service, err := NewService(memory.NewSessionRepository(), maintenanceWorkflows{}, maintenanceSessions{}, &ids{}, fixedClock{time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	service.WithHostAccessMaintenance(maintenance).WithExecutionInspector(inspector)
	report, err := service.ReconcileWorkflows(context.Background(), 10)
	if !errors.Is(err, failed) || report.Inspected != 2 || inspector.calls != 2 || maintenance.calls != 2 {
		t.Fatal("cleanup hid failure or starved workflow reconciliation", report, err)
	}
}
