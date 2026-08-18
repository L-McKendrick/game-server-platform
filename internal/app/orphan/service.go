package orphan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type Clock interface{ Now() time.Time }
type Config struct {
	Project, Environment         string
	MinimumAge, QuarantinePeriod time.Duration
	SessionLimit                 int32
}

func (config Config) Validate() error {
	if strings.TrimSpace(config.Project) == "" || strings.TrimSpace(config.Environment) == "" {
		return fmt.Errorf("project and environment are required")
	}
	if config.MinimumAge < time.Hour || config.QuarantinePeriod < time.Hour {
		return fmt.Errorf("orphan age gates must be at least one hour")
	}
	if config.SessionLimit < 1 || config.SessionLimit > 1000 {
		return fmt.Errorf("session inventory limit must be between 1 and 1000")
	}
	return nil
}

type Service struct {
	repository ports.OrphanRepository
	inventory  ports.ResourceInventory
	cleaner    ports.OrphanCleaner
	clock      Clock
	config     Config
}

func NewService(repository ports.OrphanRepository, inventory ports.ResourceInventory, cleaner ports.OrphanCleaner, clock Clock, config Config) (*Service, error) {
	if repository == nil || inventory == nil || cleaner == nil || clock == nil {
		return nil, fmt.Errorf("orphan service dependencies are required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Service{repository: repository, inventory: inventory, cleaner: cleaner, clock: clock, config: config}, nil
}

type ScanReport struct{ Resources, Findings, Actionable, ReportOnly int }

func (service *Service) Scan(ctx context.Context) (ScanReport, error) {
	sessions, err := service.repository.ListSessionsForInventory(ctx, service.config.SessionLimit)
	if err != nil {
		return ScanReport{}, err
	}
	resources, err := service.inventory.List(ctx)
	if err != nil {
		return ScanReport{}, err
	}
	byID := make(map[string]domain.Session, len(sessions))
	referencedInstances := map[string]bool{}
	for _, session := range sessions {
		byID[session.ID] = session
		if session.Infrastructure.InstanceID != "" {
			referencedInstances[session.Infrastructure.InstanceID] = true
		}
	}
	report := ScanReport{Resources: len(resources)}
	now := service.clock.Now().UTC()
	for _, resource := range resources {
		finding, ok := service.classify(resource, byID, referencedInstances, now)
		if !ok {
			continue
		}
		existing, getErr := service.repository.GetOrphanFinding(ctx, finding.ID)
		if getErr == nil {
			// Preserve the original age gate and every operator transition. A
			// recurring scheduled scan must never reset quarantine or cleanup.
			finding = existing
		} else if !errors.Is(getErr, domain.ErrNotFound) {
			return report, getErr
		}
		if getErr != nil {
			if err := service.repository.SaveOrphanFinding(ctx, finding); err != nil {
				return report, err
			}
		}
		report.Findings++
		if finding.Disposition == domain.OrphanReportOnly {
			report.ReportOnly++
		} else {
			report.Actionable++
		}
	}
	return report, nil
}

func (service *Service) classify(resource domain.ResourceObservation, sessions map[string]domain.Session, referencedInstances map[string]bool, now time.Time) (domain.OrphanFinding, bool) {
	if resource.Kind == domain.ResourceSecurityGroup && resource.SessionID == "" {
		return domain.OrphanFinding{}, false
	}
	session, exists := sessions[resource.SessionID]
	if exists && resourceReferenced(resource, session, referencedInstances) {
		return domain.OrphanFinding{}, false
	}
	reason, disposition := "Resource ownership or tags are insufficient for automatic action.", domain.OrphanReportOnly
	if resource.Kind == domain.ResourceS3Prefix {
		if !exists || session.LifecycleState == domain.StateDeleted {
			reason = "A session object prefix is unreferenced; version-aware storage cleanup requires operator review."
		}
		disposition = domain.OrphanReportOnly
	} else if resource.Kind == domain.ResourceSecurityGroup || resource.Kind == domain.ResourceSchedule {
		reason, disposition = "Shared or scheduled resources are report-only in the current platform.", domain.OrphanReportOnly
	} else if !resource.ImmutableTagsMatch(service.config.Project, service.config.Environment) {
		reason, disposition = "Immutable Project, Environment, and SessionId tags do not all match.", domain.OrphanReportOnly
	} else if len(resource.RelatedIDs) > 0 {
		reason, disposition = "The volume is still attached; its owning instance must be reconciled first.", domain.OrphanReportOnly
	} else if !exists {
		reason, disposition = "No authoritative session metadata exists for the fully tagged resource.", domain.OrphanDetected
	} else if session.LifecycleState == domain.StateDeleted || session.LifecycleState == domain.StateArchived {
		reason, disposition = "The session lifecycle does not permit this unreferenced runtime resource.", domain.OrphanDetected
	} else {
		reason, disposition = "The resource is not referenced by its active session; lifecycle state requires operator review.", domain.OrphanReportOnly
	}
	id := findingID(resource, reason)
	eligible := time.Time{}
	if disposition == domain.OrphanDetected {
		eligible = resource.CreatedAt.Add(service.config.MinimumAge)
		if eligible.Before(now) {
			eligible = now
		}
	}
	finding := domain.OrphanFinding{ID: id, Resource: resource, Reason: reason, Disposition: disposition, DetectedAt: now, EligibleAfter: eligible, UpdatedAt: now}
	return finding, finding.Validate() == nil
}

func resourceReferenced(resource domain.ResourceObservation, session domain.Session, referencedInstances map[string]bool) bool {
	switch resource.Kind {
	case domain.ResourceEC2Instance:
		return resource.ID == session.Infrastructure.InstanceID
	case domain.ResourceEBSVolume:
		if resource.ID == session.Infrastructure.DataVolumeID {
			return true
		}
		for _, related := range resource.RelatedIDs {
			if referencedInstances[related] {
				return true
			}
		}
		return false
	case domain.ResourceSecurityGroup:
		return slices.Contains(session.Infrastructure.SecurityGroupIDs, resource.ID)
	case domain.ResourceS3Prefix:
		return session.LifecycleState != domain.StateDeleted
	default:
		return false
	}
}

func findingID(resource domain.ResourceObservation, reason string) string {
	sum := sha256.Sum256([]byte(string(resource.Kind) + "\x00" + resource.ID + "\x00" + reason))
	return "orphan-" + hex.EncodeToString(sum[:12])
}

func (service *Service) Inspect(ctx context.Context, limit int32) ([]domain.OrphanFinding, error) {
	return service.repository.ListOrphanFindings(ctx, limit)
}

func (service *Service) Quarantine(ctx context.Context, findingID, requestedBy string) (domain.OrphanFinding, error) {
	requestedBy = strings.TrimSpace(requestedBy)
	if requestedBy == "" {
		return domain.OrphanFinding{}, fmt.Errorf("operator identity is required")
	}
	finding, err := service.repository.GetOrphanFinding(ctx, findingID)
	if err != nil {
		return domain.OrphanFinding{}, err
	}
	now := service.clock.Now().UTC()
	if !finding.CanQuarantine(now) {
		return domain.OrphanFinding{}, fmt.Errorf("%w: finding is not eligible for quarantine", domain.ErrInvalidTransition)
	}
	found, err := service.revalidate(ctx, finding)
	if err != nil {
		return domain.OrphanFinding{}, err
	}
	if !found {
		return domain.OrphanFinding{}, errors.New("resource disappeared before quarantine")
	}
	if err := service.cleaner.Quarantine(ctx, finding); err != nil {
		return domain.OrphanFinding{}, err
	}
	finding.Disposition, finding.EligibleAfter, finding.UpdatedAt, finding.UpdatedBy = domain.OrphanQuarantined, now.Add(service.config.QuarantinePeriod), now, requestedBy
	if err := service.repository.SaveOrphanFinding(ctx, finding); err != nil {
		return domain.OrphanFinding{}, err
	}
	return finding, nil
}

func (service *Service) Cleanup(ctx context.Context, findingID, requestedBy string) (domain.OrphanFinding, error) {
	requestedBy = strings.TrimSpace(requestedBy)
	if requestedBy == "" {
		return domain.OrphanFinding{}, fmt.Errorf("operator identity is required")
	}
	finding, err := service.repository.GetOrphanFinding(ctx, findingID)
	if err != nil {
		return domain.OrphanFinding{}, err
	}
	now := service.clock.Now().UTC()
	if !finding.CanClean(now) {
		return domain.OrphanFinding{}, fmt.Errorf("%w: finding is not eligible for cleanup", domain.ErrInvalidTransition)
	}
	found, err := service.revalidate(ctx, finding)
	if err != nil {
		return domain.OrphanFinding{}, err
	}
	if found {
		if err := service.cleaner.Cleanup(ctx, finding); err != nil {
			return domain.OrphanFinding{}, err
		}
	}
	finding.Disposition, finding.UpdatedAt, finding.UpdatedBy = domain.OrphanCleaned, now, requestedBy
	if err := service.repository.SaveOrphanFinding(ctx, finding); err != nil {
		return domain.OrphanFinding{}, err
	}
	return finding, nil
}

func (service *Service) revalidate(ctx context.Context, finding domain.OrphanFinding) (bool, error) {
	sessions, err := service.repository.ListSessionsForInventory(ctx, service.config.SessionLimit)
	if err != nil {
		return false, err
	}
	byID := map[string]domain.Session{}
	referencedInstances := map[string]bool{}
	for _, session := range sessions {
		byID[session.ID] = session
		if session.Infrastructure.InstanceID != "" {
			referencedInstances[session.Infrastructure.InstanceID] = true
		}
	}
	resources, err := service.inventory.List(ctx)
	if err != nil {
		return false, err
	}
	for _, current := range resources {
		if current.Kind == finding.Resource.Kind && current.ID == finding.Resource.ID {
			if !current.ImmutableTagsMatch(service.config.Project, service.config.Environment) {
				return false, fmt.Errorf("immutable tags changed")
			}
			if session, exists := byID[current.SessionID]; exists && resourceReferenced(current, session, referencedInstances) {
				return false, fmt.Errorf("resource is now referenced")
			}
			return true, nil
		}
	}
	return false, nil
}
