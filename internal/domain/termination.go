package domain

import (
	"fmt"
	"strings"
	"time"
)

const TerminationWorkflowType = "DestroySession"

// CanTerminate permits irreversible deletion only while no other mutating
// workflow owns the session. A failed termination remains retryable because
// authoritative resource identifiers and artifact keys are retained.
func (session Session) CanTerminate() bool {
	return session.ActiveWorkflowID == "" && session.LifecycleState != StateDeleting && session.LifecycleState != StateDeleted
}

func (session *Session) BeginTermination(workflowID string, lease time.Duration, now time.Time) error {
	if !session.CanTerminate() {
		return fmt.Errorf("%w: termination requires an unlocked non-deleted session", ErrInvalidTransition)
	}
	if err := session.AcquireWorkflowLock(strings.TrimSpace(workflowID), TerminationWorkflowType, lease, now); err != nil {
		return err
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressRuntimeRemoved, now); err != nil {
		return err
	}
	session.ArchiveSourceState = ""
	session.DesiredState, session.ObservedState, session.LifecycleState = StateDeleted, StateDeleting, StateDeleting
	session.HealthStatus = HealthUnknown
	return session.Validate()
}

func (session *Session) CompleteTermination(workflowID string, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || session.ActiveWorkflowType != TerminationWorkflowType || session.LifecycleState != StateDeleting {
		return ErrConflict
	}
	if err := session.completeProgressWithoutVersion(workflowID, now); err != nil {
		return err
	}
	session.Infrastructure = Infrastructure{}
	session.Archive = ArchiveMetadata{}
	session.MissionObjectKey = ""
	session.PresetObjectKey = ""
	session.PresetRevisionSequence = 0
	session.ActivePresetRevision = PresetRevision{}
	session.PendingPresetRevision = PresetRevision{}
	session.MonitoringCommandID = ""
	session.MonitoringStartedAt = time.Time{}
	session.DesiredState, session.ObservedState, session.LifecycleState = StateDeleted, StateDeleted, StateDeleted
	session.HealthStatus = HealthStopped
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session *Session) FailTermination(workflowID string, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || session.ActiveWorkflowType != TerminationWorkflowType {
		return ErrConflict
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressFailed, now); err != nil {
		return err
	}
	session.DesiredState, session.ObservedState, session.LifecycleState = StateDeleted, StateFailed, StateFailed
	session.HealthStatus = HealthUnhealthy
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session *Session) AbortTerminationWorkflowStart(workflowID string, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || session.ActiveWorkflowType != TerminationWorkflowType || session.LifecycleState != StateDeleting {
		return ErrConflict
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressFailed, now); err != nil {
		return err
	}
	session.DesiredState, session.ObservedState, session.LifecycleState = StateDeleted, StateFailed, StateFailed
	session.HealthStatus = HealthUnhealthy
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}
