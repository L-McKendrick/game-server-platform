package domain

import (
	"fmt"
	"strings"
	"time"
)

const BootstrapWorkflowType = "BootstrapGameServer"

// CanStartBootstrap permits the Phase 6 workflow at the provisioning boundary,
// or after a failed bootstrap that retained its complete compute plane.
func (session Session) CanStartBootstrap() bool {
	if session.ActiveWorkflowID != "" || session.Infrastructure.InstanceID == "" ||
		session.Infrastructure.DataVolumeID == "" || session.Infrastructure.CapacitySlotID == "" {
		return false
	}
	return session.LifecycleState == StateBootstrapping || session.LifecycleState == StateFailed
}

// AcquireBootstrapWorkflowLock expresses the existing RUNNING intent while
// retaining provisioned resource identifiers for an idempotent bootstrap.
func (session *Session) AcquireBootstrapWorkflowLock(workflowID string, lease time.Duration, now time.Time) error {
	if !session.CanStartBootstrap() {
		return fmt.Errorf("%w: bootstrap requires complete provisioned infrastructure", ErrInvalidTransition)
	}
	if err := session.AcquireWorkflowLock(workflowID, BootstrapWorkflowType, lease, now); err != nil {
		return err
	}
	session.DesiredState = StateRunning
	session.ObservedState = StateBootstrapping
	session.LifecycleState = StateBootstrapping
	session.HealthStatus = HealthStarting
	session.beginPresetRevisionApplication(workflowID, now)
	return session.Validate()
}

// BeginBootstrapInstallation records that the remote resumable installer has
// started. Durable stage markers on the data volume remain the execution truth.
func (session *Session) BeginBootstrapInstallation(workflowID string, now time.Time) error {
	if err := session.requireBootstrapWorkflow(workflowID); err != nil {
		return err
	}
	if session.LifecycleState != StateBootstrapping && session.LifecycleState != StateInstalling {
		return fmt.Errorf("%w: bootstrap installation requires BOOTSTRAPPING", ErrInvalidTransition)
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressGameContentSetup, now); err != nil {
		return err
	}
	session.ObservedState = StateInstalling
	session.LifecycleState = StateInstalling
	session.HealthStatus = HealthStarting
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

// CompleteBootstrap marks the server playable only after the service and UDP
// health gates succeed on the managed node.
func (session *Session) CompleteBootstrap(workflowID string, now time.Time) error {
	if err := session.requireBootstrapWorkflow(workflowID); err != nil {
		return err
	}
	if session.LifecycleState != StateInstalling {
		return fmt.Errorf("%w: bootstrap completion requires INSTALLING", ErrInvalidTransition)
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressCompleted, now); err != nil {
		return err
	}
	if _, _, err := session.promotePresetRevision(workflowID, now); err != nil {
		return err
	}
	session.DesiredState = StateRunning
	session.ObservedState = StateRunning
	session.LifecycleState = StateRunning
	session.HealthStatus = HealthHealthy
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

// FailBootstrap preserves the compute plane and durable stage markers so the
// same session can resume without reinstalling completed content.
func (session *Session) FailBootstrap(workflowID string, now time.Time) error {
	if err := session.requireBootstrapWorkflow(workflowID); err != nil {
		return err
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressFailed, now); err != nil {
		return err
	}
	session.ObservedState = StateFailed
	session.LifecycleState = StateFailed
	session.HealthStatus = HealthUnhealthy
	session.clearWorkflowLock()
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session Session) requireBootstrapWorkflow(workflowID string) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || session.ActiveWorkflowType != BootstrapWorkflowType {
		return fmt.Errorf("%w: workflow does not hold the bootstrap lease", ErrConflict)
	}
	return nil
}

func (session *Session) clearWorkflowLock() {
	session.ActiveWorkflowID = ""
	session.ActiveWorkflowType = ""
	session.ActiveWorkflowStartedAt = time.Time{}
	session.ActiveWorkflowLeaseExpiresAt = time.Time{}
}
