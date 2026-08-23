package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	SleepWorkflowType = "SleepSession"
	WakeWorkflowType  = "WakeSession"
)

func (session Session) CanSleep() bool {
	return session.ActiveWorkflowID == "" && session.Infrastructure.InstanceID != "" && (session.LifecycleState == StateRunning || session.LifecycleState == StateIdle)
}

func (session Session) CanWake() bool {
	return session.ActiveWorkflowID == "" && session.Infrastructure.InstanceID != "" && session.LifecycleState == StateSleeping
}

func (session *Session) BeginSleep(workflowID string, lease time.Duration, now time.Time) error {
	if !session.CanSleep() {
		return fmt.Errorf("%w: sleep requires a running managed session", ErrInvalidTransition)
	}
	if err := session.AcquireWorkflowLock(strings.TrimSpace(workflowID), SleepWorkflowType, lease, now); err != nil {
		return err
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressInstanceStopped, now); err != nil {
		return err
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateSleeping, StateStopping, StateStopping, HealthStarting
	return session.Validate()
}

func (session *Session) CompleteSleep(workflowID string, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || session.ActiveWorkflowType != SleepWorkflowType {
		return ErrConflict
	}
	if err := session.completeProgressWithoutVersion(workflowID, now); err != nil {
		return err
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateSleeping, StateSleeping, StateSleeping, HealthStopped
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session *Session) BeginWake(workflowID string, lease time.Duration, now time.Time) error {
	if !session.CanWake() {
		return fmt.Errorf("%w: wake requires a sleeping managed session", ErrInvalidTransition)
	}
	if err := session.AcquireWorkflowLock(strings.TrimSpace(workflowID), WakeWorkflowType, lease, now); err != nil {
		return err
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressComputeReady, now); err != nil {
		return err
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateRunning, StateWaking, StateWaking, HealthStarting
	session.SnapshotConfiguredMission()
	session.beginPresetRevisionApplication(workflowID, now)
	return session.Validate()
}

func (session *Session) CompleteWake(workflowID string, publicIPv4 string, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || session.ActiveWorkflowType != WakeWorkflowType {
		return ErrConflict
	}
	if err := session.completeProgressWithoutVersion(workflowID, now); err != nil {
		return err
	}
	if _, _, err := session.promotePresetRevision(workflowID, now); err != nil {
		return err
	}
	session.Infrastructure.PublicIPv4, session.Infrastructure.LastObservedAt = strings.TrimSpace(publicIPv4), now.UTC()
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateRunning, StateRunning, StateRunning, HealthHealthy
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session *Session) FailSleepWake(workflowID string, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || (session.ActiveWorkflowType != SleepWorkflowType && session.ActiveWorkflowType != WakeWorkflowType) {
		return ErrConflict
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressFailed, now); err != nil {
		return err
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateFailed, StateFailed, StateFailed, HealthUnhealthy
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}
