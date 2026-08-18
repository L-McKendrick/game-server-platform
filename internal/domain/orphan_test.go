package domain

import (
	"testing"
	"time"
)

func TestOrphanFindingFailsClosedWithoutImmutableTags(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	resource := ResourceObservation{Kind: ResourceEC2Instance, ID: "i-1", SessionID: "session-1", Project: "project", Environment: "dev", CreatedAt: now.Add(-48 * time.Hour), Tags: map[string]string{"Project": "other", "Environment": "dev", "SessionId": "session-1"}}
	if resource.ImmutableTagsMatch("project", "dev") {
		t.Fatal("mismatched project tag was accepted")
	}
	finding := OrphanFinding{ID: "finding-1", Resource: resource, Reason: "Tags do not authorize cleanup.", Disposition: OrphanReportOnly, DetectedAt: now, UpdatedAt: now}
	if err := finding.Validate(); err != nil {
		t.Fatal(err)
	}
	if finding.CanQuarantine(now.Add(7*24*time.Hour)) || finding.CanClean(now.Add(7*24*time.Hour)) {
		t.Fatal("report-only finding became destructive")
	}
}

func TestOrphanRequiresQuarantineBeforeCleanup(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	resource := ResourceObservation{Kind: ResourceEBSVolume, ID: "vol-1", SessionID: "missing", Project: "project", Environment: "dev", CreatedAt: now.Add(-48 * time.Hour), Tags: map[string]string{"Project": "project", "Environment": "dev", "SessionId": "missing"}}
	finding := OrphanFinding{ID: "finding-1", Resource: resource, Reason: "No session metadata exists.", Disposition: OrphanDetected, DetectedAt: now, EligibleAfter: now.Add(24 * time.Hour), UpdatedAt: now}
	if finding.CanClean(now.Add(48 * time.Hour)) {
		t.Fatal("detected orphan skipped quarantine")
	}
	finding.Disposition, finding.UpdatedAt, finding.UpdatedBy = OrphanQuarantined, now.Add(24*time.Hour), "operator"
	if !finding.CanClean(now.Add(48 * time.Hour)) {
		t.Fatal("quarantined eligible volume cannot clean")
	}
}
