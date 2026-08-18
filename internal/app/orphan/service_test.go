package orphan

import (
	"context"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type clock struct{ now time.Time }

func (clock clock) Now() time.Time { return clock.now }

type mutableClock struct{ now time.Time }

func (clock *mutableClock) Now() time.Time { return clock.now }

type inventory struct{ resources []domain.ResourceObservation }

func (inventory inventory) List(context.Context) ([]domain.ResourceObservation, error) {
	return inventory.resources, nil
}

type mutableInventory struct{ resources []domain.ResourceObservation }

func (inventory *mutableInventory) List(context.Context) ([]domain.ResourceObservation, error) {
	return inventory.resources, nil
}

type cleaner struct{ quarantined, cleaned int }

func (cleaner *cleaner) Quarantine(context.Context, domain.OrphanFinding) error {
	cleaner.quarantined++
	return nil
}

func TestRecurringScanPreservesQuarantineAndMissingCleanupIsIdempotent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	clean := &cleaner{}
	resource := domain.ResourceObservation{Kind: domain.ResourceEBSVolume, ID: "vol-orphan", SessionID: "missing", Project: "project", Environment: "dev", CreatedAt: now.Add(-48 * time.Hour), Tags: map[string]string{"Project": "project", "Environment": "dev", "SessionId": "missing"}}
	resources := &mutableInventory{resources: []domain.ResourceObservation{resource}}
	serviceClock := &mutableClock{now: now}
	service, err := NewService(repository, resources, clean, serviceClock, Config{Project: "project", Environment: "dev", MinimumAge: 24 * time.Hour, QuarantinePeriod: 24 * time.Hour, SessionLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	findings, _ := service.Inspect(context.Background(), 10)
	quarantined, err := service.Quarantine(context.Background(), findings[0].ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	preserved, _ := repository.GetOrphanFinding(context.Background(), quarantined.ID)
	if preserved.Disposition != domain.OrphanQuarantined || !preserved.EligibleAfter.Equal(quarantined.EligibleAfter) {
		t.Fatalf("recurring scan reset quarantine: %#v", preserved)
	}
	serviceClock.now = quarantined.EligibleAfter
	resources.resources = nil
	cleaned, err := service.Cleanup(context.Background(), quarantined.ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if cleaned.Disposition != domain.OrphanCleaned || clean.cleaned != 0 {
		t.Fatalf("missing cleanup result = %#v, cleaner calls = %d", cleaned, clean.cleaned)
	}
}
func (cleaner *cleaner) Cleanup(context.Context, domain.OrphanFinding) error {
	cleaner.cleaned++
	return nil
}

func TestScanReportsMalformedAndStagesFullyTaggedMissingSession(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	clean := &cleaner{}
	resources := []domain.ResourceObservation{
		{Kind: domain.ResourceEC2Instance, ID: "i-good", SessionID: "missing", Project: "project", Environment: "dev", CreatedAt: now.Add(-48 * time.Hour), Tags: map[string]string{"Project": "project", "Environment": "dev", "SessionId": "missing"}},
		{Kind: domain.ResourceEBSVolume, ID: "vol-bad", SessionID: "missing", Project: "wrong", Environment: "dev", CreatedAt: now.Add(-48 * time.Hour), Tags: map[string]string{"Project": "wrong", "Environment": "dev", "SessionId": "missing"}},
	}
	service, err := NewService(repository, inventory{resources}, clean, clock{now}, Config{Project: "project", Environment: "dev", MinimumAge: 24 * time.Hour, QuarantinePeriod: 24 * time.Hour, SessionLimit: 100})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Actionable != 1 || report.ReportOnly != 1 {
		t.Fatalf("report = %#v", report)
	}
	findings, _ := service.Inspect(context.Background(), 10)
	var actionable domain.OrphanFinding
	for _, finding := range findings {
		if finding.Disposition == domain.OrphanDetected {
			actionable = finding
		}
	}
	if actionable.ID == "" {
		t.Fatal("missing actionable finding")
	}
	if _, err := service.Quarantine(context.Background(), actionable.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	if clean.quarantined != 1 {
		t.Fatalf("quarantine calls = %d", clean.quarantined)
	}
}
