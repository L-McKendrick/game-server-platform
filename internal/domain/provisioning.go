package domain

import (
	"fmt"
	"strings"
	"time"
)

// Infrastructure records disposable AWS resources without making them the
// identity of a session. Empty fields represent a session with no active
// compute plane.
type Infrastructure struct {
	CapacitySlotID   string
	AvailabilityZone string
	SubnetID         string
	SecurityGroupIDs []string
	InstanceProfile  string
	AMIID            string
	InstanceType     string
	InstanceID       string
	DataVolumeID     string
	PublicIPv4       string
	LastObservedAt   time.Time
}

func (infrastructure Infrastructure) Empty() bool {
	return infrastructure.CapacitySlotID == "" && infrastructure.InstanceID == "" && infrastructure.DataVolumeID == "" &&
		infrastructure.AvailabilityZone == "" && infrastructure.SubnetID == "" && len(infrastructure.SecurityGroupIDs) == 0 &&
		infrastructure.InstanceProfile == "" && infrastructure.AMIID == "" && infrastructure.InstanceType == "" &&
		infrastructure.PublicIPv4 == "" && infrastructure.LastObservedAt.IsZero()
}

// ComputeLaunchRequest contains the bounded, operator-controlled launch
// parameters for one session. Discord input never supplies these values.
type ComputeLaunchRequest struct {
	SessionID        string
	SessionName      string
	SessionSlug      string
	GameType         string
	Environment      string
	Project          string
	AMIID            string
	InstanceType     string
	SubnetID         string
	SecurityGroupIDs []string
	InstanceProfile  string
	RootVolumeGiB    int32
	DataVolumeGiB    int32
	ClientToken      string
}

// ComputeObservation is the latest EC2 view used for idempotent reconciliation.
type ComputeObservation struct {
	InstanceID       string
	DataVolumeID     string
	AvailabilityZone string
	PublicIPv4       string
	State            string
}

func (infrastructure Infrastructure) Validate() error {
	if infrastructure.Empty() {
		return nil
	}
	if strings.TrimSpace(infrastructure.CapacitySlotID) == "" {
		return fmt.Errorf("infrastructure capacity slot is required")
	}
	if infrastructure.InstanceID == "" {
		if infrastructure.DataVolumeID != "" || infrastructure.PublicIPv4 != "" || !infrastructure.LastObservedAt.IsZero() {
			return fmt.Errorf("observed infrastructure fields require an instance ID")
		}
		return nil
	}
	switch {
	case strings.TrimSpace(infrastructure.AvailabilityZone) == "":
		return fmt.Errorf("instance availability zone is required")
	case strings.TrimSpace(infrastructure.SubnetID) == "":
		return fmt.Errorf("instance subnet ID is required")
	case len(infrastructure.SecurityGroupIDs) == 0:
		return fmt.Errorf("at least one instance security group is required")
	case strings.TrimSpace(infrastructure.InstanceProfile) == "":
		return fmt.Errorf("instance profile is required")
	case strings.TrimSpace(infrastructure.AMIID) == "":
		return fmt.Errorf("instance AMI ID is required")
	case strings.TrimSpace(infrastructure.InstanceType) == "":
		return fmt.Errorf("instance type is required")
	default:
		return nil
	}
}

// AcquireProvisioningWorkflowLock atomically expresses the user's RUNNING
// intent and takes the single mutating-workflow lease.
func (session *Session) AcquireProvisioningWorkflowLock(workflowID string, lease time.Duration, now time.Time) error {
	workflowID = strings.TrimSpace(workflowID)
	now = now.UTC()
	if !session.CanStartInfrastructureProvisioning() {
		return fmt.Errorf("%w: provisioning requires NEW or a resource-free FAILED session", ErrInvalidTransition)
	}
	if workflowID == "" || lease <= 0 {
		return fmt.Errorf("workflow ID and positive lease are required")
	}
	if session.ActiveWorkflowID != "" && session.ActiveWorkflowLeaseExpiresAt.After(now) {
		return fmt.Errorf("%w: session %s is locked by workflow %s", ErrWorkflowLocked, session.ID, session.ActiveWorkflowID)
	}
	session.ActiveWorkflowID = workflowID
	session.ActiveWorkflowType = "ProvisionSession"
	session.ActiveWorkflowStartedAt = now
	session.ActiveWorkflowLeaseExpiresAt = now.Add(lease)
	if err := session.beginProgress(workflowID, "ProvisionSession", now); err != nil {
		return err
	}
	session.DesiredState = StateRunning
	session.ObservedState = StateValidating
	session.LifecycleState = StateValidating
	session.HealthStatus = HealthStarting
	session.Version++
	session.UpdatedAt = now
	return session.Validate()
}

// CanStartInfrastructureProvisioning permits a clean retry after a failure
// only when reconciliation found no capacity reservation or compute resources.
func (session Session) CanStartInfrastructureProvisioning() bool {
	if session.LifecycleState == StateNew {
		return true
	}
	return session.LifecycleState == StateFailed &&
		session.ActiveWorkflowID == "" &&
		session.Infrastructure.CapacitySlotID == "" &&
		session.Infrastructure.InstanceID == "" &&
		session.Infrastructure.DataVolumeID == ""
}

// BeginInfrastructureProvisioning records the capacity reservation before any
// cost-bearing AWS resource is launched.
func (session *Session) BeginInfrastructureProvisioning(workflowID string, capacitySlotID string, now time.Time) error {
	if err := session.requireProvisioningWorkflow(workflowID); err != nil {
		return err
	}
	if session.LifecycleState != StateValidating && session.LifecycleState != StateProvisioning {
		return fmt.Errorf("%w: infrastructure preparation requires VALIDATING", ErrInvalidTransition)
	}
	capacitySlotID = strings.TrimSpace(capacitySlotID)
	if capacitySlotID == "" {
		return fmt.Errorf("capacity slot ID is required")
	}
	session.Infrastructure.CapacitySlotID = capacitySlotID
	session.ObservedState = StateProvisioning
	session.LifecycleState = StateProvisioning
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

// RecordInfrastructureLaunch stores identifiers immediately after launch so a
// retry discovers and reuses the same instance instead of creating another.
func (session *Session) RecordInfrastructureLaunch(workflowID string, infrastructure Infrastructure, now time.Time) error {
	if err := session.requireProvisioningWorkflow(workflowID); err != nil {
		return err
	}
	if session.LifecycleState != StateProvisioning {
		return fmt.Errorf("%w: instance launch requires PROVISIONING", ErrInvalidTransition)
	}
	if infrastructure.CapacitySlotID == "" {
		infrastructure.CapacitySlotID = session.Infrastructure.CapacitySlotID
	}
	if err := infrastructure.Validate(); err != nil {
		return err
	}
	session.Infrastructure = infrastructure
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

// CompleteInfrastructureProvisioning records the Phase 5 boundary. The
// session is not READY or RUNNING until the Phase 6 bootstrap and health gates
// succeed.
func (session *Session) CompleteInfrastructureProvisioning(workflowID string, now time.Time) error {
	if err := session.requireProvisioningWorkflow(workflowID); err != nil {
		return err
	}
	if session.LifecycleState != StateProvisioning || session.Infrastructure.InstanceID == "" || session.Infrastructure.DataVolumeID == "" {
		return fmt.Errorf("%w: complete infrastructure is required", ErrInvalidTransition)
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressInfrastructureReady, now); err != nil {
		return err
	}
	session.ObservedState = StateBootstrapping
	session.LifecycleState = StateBootstrapping
	session.ActiveWorkflowID = ""
	session.ActiveWorkflowType = ""
	session.ActiveWorkflowStartedAt = time.Time{}
	session.ActiveWorkflowLeaseExpiresAt = time.Time{}
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

// FailInfrastructureProvisioning preserves discovered resource identifiers for
// explicit reconciliation and cleanup while releasing the workflow lease.
func (session *Session) FailInfrastructureProvisioning(workflowID string, now time.Time) error {
	if err := session.requireProvisioningWorkflow(workflowID); err != nil {
		return err
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressFailed, now); err != nil {
		return err
	}
	session.ObservedState = StateFailed
	session.LifecycleState = StateFailed
	session.HealthStatus = HealthUnknown
	session.ActiveWorkflowID = ""
	session.ActiveWorkflowType = ""
	session.ActiveWorkflowStartedAt = time.Time{}
	session.ActiveWorkflowLeaseExpiresAt = time.Time{}
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

// AbortProvisioningWorkflowStart restores NEW when Step Functions could not be
// started and therefore no provisioning task could have created resources.
func (session *Session) AbortProvisioningWorkflowStart(workflowID string, now time.Time) error {
	if err := session.requireProvisioningWorkflow(workflowID); err != nil {
		return err
	}
	if session.Infrastructure.InstanceID != "" || session.Infrastructure.CapacitySlotID != "" {
		return fmt.Errorf("%w: provisioning resources already exist", ErrConflict)
	}
	if err := session.setProgressWithoutVersion(workflowID, ProgressFailed, now); err != nil {
		return err
	}
	session.DesiredState = StateNew
	session.ObservedState = StateNew
	session.LifecycleState = StateNew
	session.HealthStatus = HealthUnknown
	session.ActiveWorkflowID = ""
	session.ActiveWorkflowType = ""
	session.ActiveWorkflowStartedAt = time.Time{}
	session.ActiveWorkflowLeaseExpiresAt = time.Time{}
	session.Version++
	session.UpdatedAt = now.UTC()
	return session.Validate()
}

func (session Session) requireProvisioningWorkflow(workflowID string) error {
	if session.ActiveWorkflowID != strings.TrimSpace(workflowID) || session.ActiveWorkflowType != "ProvisionSession" {
		return fmt.Errorf("%w: workflow does not hold the provisioning lease", ErrConflict)
	}
	return nil
}
