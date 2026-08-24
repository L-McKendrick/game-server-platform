package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	MinimumRetryAttempts = 1
	MaximumRetryAttempts = 5
)

// RetryPolicy is the provider-neutral upper bound applied to transient work.
// Attempts include the initial call; terminal and validation errors are never
// made retryable merely by attaching this policy.
type RetryPolicy struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaximumDelay time.Duration
}

func (policy RetryPolicy) Validate() error {
	switch {
	case policy.MaxAttempts < MinimumRetryAttempts || policy.MaxAttempts > MaximumRetryAttempts:
		return fmt.Errorf("retry attempts must be between %d and %d", MinimumRetryAttempts, MaximumRetryAttempts)
	case policy.InitialDelay <= 0:
		return fmt.Errorf("retry initial delay must be positive")
	case policy.MaximumDelay < policy.InitialDelay:
		return fmt.Errorf("retry maximum delay cannot precede the initial delay")
	case policy.MaximumDelay > 15*time.Minute:
		return fmt.Errorf("retry maximum delay must not exceed 15 minutes")
	default:
		return nil
	}
}

var DefaultTransientRetryPolicy = RetryPolicy{
	MaxAttempts: 3, InitialDelay: 2 * time.Second, MaximumDelay: 30 * time.Second,
}

// Delay returns the deterministic exponential ceiling for the next attempt.
// Providers may add jitter without exceeding MaximumDelay.
func (policy RetryPolicy) Delay(attempt int) (time.Duration, error) {
	if err := policy.Validate(); err != nil {
		return 0, err
	}
	if attempt < 1 || attempt >= policy.MaxAttempts {
		return 0, fmt.Errorf("attempt %d has no retry", attempt)
	}
	delay := policy.InitialDelay
	for current := 1; current < attempt; current++ {
		if delay >= policy.MaximumDelay/2 {
			return policy.MaximumDelay, nil
		}
		delay *= 2
	}
	if delay > policy.MaximumDelay {
		delay = policy.MaximumDelay
	}
	return delay, nil
}

// RequestCancellation records intent only. Workers honor it at a declared safe
// boundary; recording the request never interrupts an in-flight mutation.
func (workflow *Workflow) RequestCancellation(requestedBy string, now time.Time) (bool, error) {
	requestedBy = strings.TrimSpace(requestedBy)
	if requestedBy == "" || now.IsZero() {
		return false, fmt.Errorf("cancellation requester and timestamp are required")
	}
	if workflow.Status != WorkflowPending && workflow.Status != WorkflowRunning {
		return false, fmt.Errorf("%w: workflow %s is terminal", ErrInvalidTransition, workflow.ID)
	}
	if !workflow.CancelRequestedAt.IsZero() {
		if workflow.CancelRequestedBy != requestedBy {
			return false, fmt.Errorf("%w: cancellation was requested by another actor", ErrConflict)
		}
		return false, nil
	}
	workflow.CancelRequestedAt = now.UTC()
	workflow.CancelRequestedBy = requestedBy
	return true, workflow.Validate()
}

func (workflow Workflow) CancellationRequested() bool {
	return !workflow.CancelRequestedAt.IsZero()
}

// CancelWorkflowAtSafeBoundary restores the last authoritative lifecycle state
// only while the workflow is still before its first external mutation. Later
// boundaries must finish or compensate their consistency-critical stage.
func (session *Session) CancelWorkflowAtSafeBoundary(workflow Workflow, now time.Time) error {
	if session.ActiveWorkflowID != workflow.ID || session.ActiveWorkflowType != workflow.Type || !workflow.CancellationRequested() {
		return fmt.Errorf("%w: cancellation does not match the active workflow", ErrConflict)
	}
	switch workflow.Type {
	case ProvisionWorkflowType:
		if session.LifecycleState != StateValidating || !session.Infrastructure.Empty() {
			return fmt.Errorf("%w: provisioning crossed its safe cancellation boundary", ErrInvalidTransition)
		}
		session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateNew, StateNew, StateNew, HealthUnknown
	case BootstrapWorkflowType:
		if session.LifecycleState != StateBootstrapping {
			return fmt.Errorf("%w: bootstrap crossed its safe cancellation boundary", ErrInvalidTransition)
		}
		session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateRunning, StateFailed, StateFailed, HealthUnknown
	case SleepWorkflowType:
		if session.LifecycleState != StateStopping {
			return fmt.Errorf("%w: sleep crossed its safe cancellation boundary", ErrInvalidTransition)
		}
		session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateRunning, StateRunning, StateRunning, HealthHealthy
	case WakeWorkflowType:
		if session.LifecycleState != StateWaking {
			return fmt.Errorf("%w: wake crossed its safe cancellation boundary", ErrInvalidTransition)
		}
		session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateSleeping, StateSleeping, StateSleeping, HealthStopped
	case ArchiveWorkflowType:
		if session.LifecycleState != StateArchiving || session.ArchiveSourceState == "" || !session.Archive.Empty() {
			return fmt.Errorf("%w: archive crossed its safe cancellation boundary", ErrInvalidTransition)
		}
		source := session.ArchiveSourceState
		session.ArchiveSourceState = ""
		session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = source, source, source, HealthHealthy
		if source == StateSleeping {
			session.HealthStatus = HealthStopped
		}
	case RestoreWorkflowType:
		if session.LifecycleState != StateRestoring || !session.Infrastructure.Empty() {
			return fmt.Errorf("%w: restore crossed its safe cancellation boundary", ErrInvalidTransition)
		}
		session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateArchived, StateArchived, StateArchived, HealthStopped
	default:
		return fmt.Errorf("%w: workflow type %s is not cancellable", ErrInvalidTransition, workflow.Type)
	}
	if _, err := session.SetProgressState(workflow.ID, ProgressCancelled, now); err != nil {
		return err
	}
	session.clearWorkflowLock()
	return session.Validate()
}

// WorkflowRetry records only bounded automatic retry state. Attempt zero is
// accepted for legacy workflow rows and is interpreted as the initial attempt.
type WorkflowRetry struct {
	Attempt       int
	MaxAttempts   int
	LastAttemptAt time.Time
	NextAttemptAt time.Time
}

func (retry WorkflowRetry) Empty() bool {
	return retry.Attempt == 0 && retry.MaxAttempts == 0 && retry.LastAttemptAt.IsZero() && retry.NextAttemptAt.IsZero()
}

func (retry WorkflowRetry) Validate() error {
	if retry.Empty() {
		return nil
	}
	switch {
	case retry.Attempt < 1:
		return fmt.Errorf("workflow retry attempt must be positive")
	case retry.MaxAttempts < retry.Attempt || retry.MaxAttempts > MaximumRetryAttempts:
		return fmt.Errorf("workflow retry maximum is invalid")
	case retry.LastAttemptAt.IsZero():
		return fmt.Errorf("workflow retry last-attempt timestamp is required")
	case !retry.NextAttemptAt.IsZero() && !retry.NextAttemptAt.After(retry.LastAttemptAt):
		return fmt.Errorf("workflow next retry must follow the last attempt")
	default:
		return nil
	}
}

type ReconciliationAction string

const (
	ReconciliationReportOnly  ReconciliationAction = "REPORT_ONLY"
	ReconciliationReleaseLock ReconciliationAction = "RELEASE_STALE_LOCK"
	ReconciliationMarkFailed  ReconciliationAction = "MARK_WORKFLOW_FAILED"
)

func (action ReconciliationAction) Valid() bool {
	return action == ReconciliationReportOnly || action == ReconciliationReleaseLock || action == ReconciliationMarkFailed
}

type ReconciliationFinding struct {
	ID, SessionID, WorkflowID, Code, Detail string
	Action                                  ReconciliationAction
	DetectedAt, ResolvedAt                  time.Time
}

type WorkflowExecutionStatus string

const (
	ExecutionRunning   WorkflowExecutionStatus = "RUNNING"
	ExecutionSucceeded WorkflowExecutionStatus = "SUCCEEDED"
	ExecutionFailed    WorkflowExecutionStatus = "FAILED"
	ExecutionTimedOut  WorkflowExecutionStatus = "TIMED_OUT"
	ExecutionAborted   WorkflowExecutionStatus = "ABORTED"
)

func (status WorkflowExecutionStatus) TerminalFailure() bool {
	return status == ExecutionFailed || status == ExecutionTimedOut || status == ExecutionAborted
}

func (session *Session) FailActiveWorkflowForReconciliation(workflowID string, now time.Time) error {
	switch session.ActiveWorkflowType {
	case ProvisionWorkflowType:
		return session.FailInfrastructureProvisioning(workflowID, now)
	case BootstrapWorkflowType:
		return session.FailBootstrap(workflowID, now)
	case SleepWorkflowType, WakeWorkflowType:
		return session.FailSleepWake(workflowID, now)
	case ArchiveWorkflowType:
		return session.FailArchive(workflowID, now)
	case RestoreWorkflowType:
		return session.FailRestore(workflowID, now)
	case TerminationWorkflowType:
		return session.FailTermination(workflowID, now)
	default:
		return session.ReleaseWorkflowLock(workflowID, now)
	}
}

func (finding ReconciliationFinding) Validate() error {
	switch {
	case strings.TrimSpace(finding.ID) == "":
		return fmt.Errorf("reconciliation finding ID is required")
	case strings.TrimSpace(finding.SessionID) == "":
		return fmt.Errorf("reconciliation session ID is required")
	case strings.TrimSpace(finding.Code) == "" || len(finding.Code) > 64:
		return fmt.Errorf("reconciliation code is invalid")
	case len(strings.TrimSpace(finding.Detail)) > 500:
		return fmt.Errorf("reconciliation detail exceeds 500 characters")
	case !finding.Action.Valid():
		return fmt.Errorf("reconciliation action is invalid")
	case finding.DetectedAt.IsZero():
		return fmt.Errorf("reconciliation detection timestamp is required")
	case !finding.ResolvedAt.IsZero() && finding.ResolvedAt.Before(finding.DetectedAt):
		return fmt.Errorf("reconciliation resolution precedes detection")
	default:
		return nil
	}
}

type DeadLetterQueue string

const (
	DeadLetterCommands      DeadLetterQueue = "COMMANDS"
	DeadLetterNotifications DeadLetterQueue = "NOTIFICATIONS"
	DeadLetterArtifacts     DeadLetterQueue = "ARTIFACTS"
)

func (queue DeadLetterQueue) Valid() bool {
	return queue == DeadLetterCommands || queue == DeadLetterNotifications || queue == DeadLetterArtifacts
}

type DeadLetterAction string

const (
	DeadLetterInspected DeadLetterAction = "INSPECTED"
	DeadLetterRedriven  DeadLetterAction = "REDRIVE_STARTED"
)

type DeadLetterOperation struct {
	ID, RequestedBy, CorrelationID, SourceARN, DestinationARN string
	Queue                                                     DeadLetterQueue
	Action                                                    DeadLetterAction
	StartedAt, CompletedAt                                    time.Time
	MovedMessages                                             int64
}

type DeadLetterInspection struct {
	Visible, InFlight, Delayed int64
}

func (operation DeadLetterOperation) Validate() error {
	switch {
	case strings.TrimSpace(operation.ID) == "" || strings.TrimSpace(operation.RequestedBy) == "" || strings.TrimSpace(operation.CorrelationID) == "":
		return fmt.Errorf("dead-letter operation identity is required")
	case !operation.Queue.Valid():
		return fmt.Errorf("dead-letter queue is invalid")
	case operation.Action != DeadLetterInspected && operation.Action != DeadLetterRedriven:
		return fmt.Errorf("dead-letter action is invalid")
	case strings.TrimSpace(operation.SourceARN) == "":
		return fmt.Errorf("dead-letter source ARN is required")
	case operation.Action == DeadLetterRedriven && strings.TrimSpace(operation.DestinationARN) == "":
		return fmt.Errorf("dead-letter destination ARN is required")
	case operation.StartedAt.IsZero():
		return fmt.Errorf("dead-letter start timestamp is required")
	case !operation.CompletedAt.IsZero() && operation.CompletedAt.Before(operation.StartedAt):
		return fmt.Errorf("dead-letter completion precedes start")
	case operation.MovedMessages < 0:
		return fmt.Errorf("dead-letter moved-message count cannot be negative")
	default:
		return nil
	}
}
