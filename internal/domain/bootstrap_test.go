package domain

import (
	"testing"
	"time"
)

func TestBootstrapLifecycleReachesRunningOnlyAfterHealthGate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	session := bootstrappingSession(t, now)

	if err := session.AcquireBootstrapWorkflowLock("workflow-2", 6*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginBootstrapInstallation("workflow-2", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateInstalling || session.HealthStatus != HealthStarting {
		t.Fatalf("installing session = %#v", session)
	}
	if err := session.CompleteBootstrap("workflow-2", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.LifecycleState != StateRunning || session.HealthStatus != HealthHealthy || session.ActiveWorkflowID != "" {
		t.Fatalf("completed session = %#v", session)
	}
}

func TestBootstrapFailureRetainsInfrastructureAndCanRetry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	session := bootstrappingSession(t, now)
	instanceID := session.Infrastructure.InstanceID
	volumeID := session.Infrastructure.DataVolumeID

	if err := session.AcquireBootstrapWorkflowLock("workflow-2", 6*time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginBootstrapInstallation("workflow-2", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := session.FailBootstrap("workflow-2", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.Infrastructure.InstanceID != instanceID || session.Infrastructure.DataVolumeID != volumeID || !session.CanStartBootstrap() {
		t.Fatalf("failed session cannot resume: %#v", session)
	}
}

func bootstrappingSession(t *testing.T, now time.Time) Session {
	t.Helper()
	session := readySessionForProvisioning(t, now)
	if err := session.AcquireProvisioningWorkflowLock("workflow-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginInfrastructureProvisioning("workflow-1", "slot-0", now); err != nil {
		t.Fatal(err)
	}
	if err := session.RecordInfrastructureLaunch("workflow-1", Infrastructure{
		CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1",
		SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile-1", AMIID: "ami-1",
		InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1",
		PublicIPv4: "203.0.113.10", LastObservedAt: now,
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteInfrastructureProvisioning("workflow-1", now); err != nil {
		t.Fatal(err)
	}
	return session
}
