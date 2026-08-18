package domain

import (
	"errors"
	"testing"
	"time"
)

func TestRetryPolicyIsBounded(t *testing.T) {
	t.Parallel()
	policy := RetryPolicy{MaxAttempts: 4, InitialDelay: 2 * time.Second, MaximumDelay: 5 * time.Second}
	wants := []time.Duration{2 * time.Second, 4 * time.Second, 5 * time.Second}
	for attempt, want := range wants {
		got, err := policy.Delay(attempt + 1)
		if err != nil || got != want {
			t.Fatalf("Delay(%d) = %s, %v; want %s", attempt+1, got, err, want)
		}
	}
	if _, err := policy.Delay(4); err == nil {
		t.Fatal("terminal attempt returned a retry delay")
	}
}

func TestWorkflowCancellationIsCooperativeAndIdempotent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	workflow := Workflow{
		ID: "workflow-1", SessionID: "session-1", Type: "ProvisionSession", Status: WorkflowRunning,
		RequestedBy: "owner-1", CorrelationID: "correlation-1", ExpectedVersion: 1,
		StartedAt: now, LeaseExpiresAt: now.Add(time.Hour),
	}
	changed, err := workflow.RequestCancellation("owner-1", now.Add(time.Minute))
	if err != nil || !changed || !workflow.CancellationRequested() || workflow.Status != WorkflowRunning {
		t.Fatalf("RequestCancellation() = %t, %v, workflow %#v", changed, err, workflow)
	}
	changed, err = workflow.RequestCancellation("owner-1", now.Add(2*time.Minute))
	if err != nil || changed {
		t.Fatalf("replay = %t, %v; want no-op", changed, err)
	}
	workflow.Status = WorkflowSucceeded
	workflow.CancelRequestedAt, workflow.CancelRequestedBy = time.Time{}, ""
	if _, err := workflow.RequestCancellation("owner-1", now.Add(3*time.Minute)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal cancellation error = %v", err)
	}
}

func TestReliabilityRecordsRejectUnsafeValues(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	finding := ReconciliationFinding{ID: "finding-1", SessionID: "session-1", Code: "STALE_LOCK", Action: ReconciliationReportOnly, DetectedAt: now}
	if err := finding.Validate(); err != nil {
		t.Fatal(err)
	}
	finding.Action = "DELETE"
	if err := finding.Validate(); err == nil {
		t.Fatal("unsafe reconciliation action validated")
	}
	operation := DeadLetterOperation{ID: "op-1", RequestedBy: "operator", CorrelationID: "corr-1", Queue: DeadLetterCommands, Action: DeadLetterRedriven, SourceARN: "arn:dlq", StartedAt: now}
	if err := operation.Validate(); err == nil {
		t.Fatal("redrive without destination validated")
	}
}
