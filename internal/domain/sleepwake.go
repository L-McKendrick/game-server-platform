package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	SleepWorkflowType   = "SleepSession"
	WakeWorkflowType    = "WakeSession"
	RestartWorkflowType = "RestartSession"
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
	session.SleepingSince = now.UTC()
	session.IdleSince = time.Time{}
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
	return session.completeRunning(workflowID, publicIPv4, WakeWorkflowType, now)
}

func (session *Session) completeRunning(workflowID string, publicIPv4 string, workflowType string, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || session.ActiveWorkflowType != workflowType {
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
	session.SleepingSince = time.Time{}
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

// CompleteWakeWithWorkshopMissions attaches scenarios synchronized while the
// host was sleeping and completes wake as one optimistic-concurrency mutation.
func (session *Session) CompleteWakeWithWorkshopMissions(workflowID string, publicIPv4 string, missions []MissionRecord, now time.Time) error {
	candidate, err := session.withWorkshopMissions(missions, now)
	if err != nil {
		return err
	}
	if err = candidate.CompleteWake(workflowID, publicIPv4, now); err != nil {
		return err
	}
	*session = candidate
	return nil
}

func (session *Session) FailSleepWake(workflowID string, now time.Time) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || (session.ActiveWorkflowType != SleepWorkflowType && session.ActiveWorkflowType != WakeWorkflowType && session.ActiveWorkflowType != RestartWorkflowType) {
		return ErrConflict
	}
	if session.ActiveWorkflowType == RestartWorkflowType {
		if err := session.FailPresetRevisionApplication(workflowID, "Restart did not complete; runtime configuration requires verification.", now); err != nil {
			return err
		}
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressFailed, now); err != nil {
		return err
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateFailed, StateFailed, StateFailed, HealthUnhealthy
	session.SleepingSince = time.Time{}
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

// CanRestart requires a stable managed game server and the exclusive session lease.
func (session Session) CanRestart() bool { return session.CanSleep() }

func (session *Session) BeginRestart(workflowID string, lease time.Duration, now time.Time) error {
	if !session.CanRestart() {
		return fmt.Errorf("%w: restart requires a stable running or idle managed session", ErrInvalidTransition)
	}
	if err := session.AcquireWorkflowLock(strings.TrimSpace(workflowID), RestartWorkflowType, lease, now); err != nil {
		return err
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressModsApplied, now); err != nil {
		return err
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = StateRunning, StateRestarting, StateRestarting, HealthStarting
	session.beginPresetRevisionApplication(workflowID, now)
	return session.Validate()
}

func (session *Session) CompleteRestart(workflowID string, missions []MissionRecord, now time.Time) error {
	candidate, err := session.withWorkshopMissions(missions, now)
	if err != nil {
		return err
	}
	if err := candidate.completeRunning(workflowID, session.Infrastructure.PublicIPv4, RestartWorkflowType, now); err != nil {
		return err
	}
	candidate.IdleSince = time.Time{}
	*session = candidate
	return nil
}
