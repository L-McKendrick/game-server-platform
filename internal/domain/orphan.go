package domain

import (
	"fmt"
	"strings"
	"time"
)

type ResourceKind string

const (
	ResourceEC2Instance   ResourceKind = "EC2_INSTANCE"
	ResourceEBSVolume     ResourceKind = "EBS_VOLUME"
	ResourceSecurityGroup ResourceKind = "SECURITY_GROUP"
	ResourceS3Prefix      ResourceKind = "S3_PREFIX"
	ResourceSchedule      ResourceKind = "SCHEDULE"
)

func (kind ResourceKind) Valid() bool {
	switch kind {
	case ResourceEC2Instance, ResourceEBSVolume, ResourceSecurityGroup, ResourceS3Prefix, ResourceSchedule:
		return true
	default:
		return false
	}
}

type ResourceObservation struct {
	Kind                                            ResourceKind
	ID, ARN, SessionID, Project, Environment, State string
	CreatedAt                                       time.Time
	Tags                                            map[string]string
	RelatedIDs                                      []string
}

func (observation ResourceObservation) Validate() error {
	switch {
	case !observation.Kind.Valid():
		return fmt.Errorf("resource kind is invalid")
	case strings.TrimSpace(observation.ID) == "":
		return fmt.Errorf("resource ID is required")
	case observation.CreatedAt.IsZero():
		return fmt.Errorf("resource creation timestamp is required")
	default:
		return nil
	}
}

func (observation ResourceObservation) ImmutableTagsMatch(project, environment string) bool {
	return strings.TrimSpace(observation.SessionID) != "" && observation.Project == strings.TrimSpace(project) && observation.Environment == strings.TrimSpace(environment) &&
		observation.Tags["Project"] == strings.TrimSpace(project) && observation.Tags["Environment"] == strings.TrimSpace(environment) && observation.Tags["SessionId"] == observation.SessionID
}

type OrphanDisposition string

const (
	OrphanReportOnly  OrphanDisposition = "REPORT_ONLY"
	OrphanDetected    OrphanDisposition = "DETECTED"
	OrphanQuarantined OrphanDisposition = "QUARANTINED"
	OrphanCleaned     OrphanDisposition = "CLEANED"
)

func (disposition OrphanDisposition) Valid() bool {
	return disposition == OrphanReportOnly || disposition == OrphanDetected || disposition == OrphanQuarantined || disposition == OrphanCleaned
}

type OrphanFinding struct {
	ID                                   string
	Resource                             ResourceObservation
	Reason                               string
	Disposition                          OrphanDisposition
	DetectedAt, EligibleAfter, UpdatedAt time.Time
	UpdatedBy                            string
}

func (finding OrphanFinding) Validate() error {
	if err := finding.Resource.Validate(); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(finding.ID) == "":
		return fmt.Errorf("orphan finding ID is required")
	case strings.TrimSpace(finding.Reason) == "" || len(finding.Reason) > 500:
		return fmt.Errorf("orphan reason is invalid")
	case !finding.Disposition.Valid():
		return fmt.Errorf("orphan disposition is invalid")
	case finding.DetectedAt.IsZero() || finding.UpdatedAt.IsZero():
		return fmt.Errorf("orphan timestamps are required")
	case finding.UpdatedAt.Before(finding.DetectedAt):
		return fmt.Errorf("orphan update precedes detection")
	case finding.Disposition != OrphanReportOnly && (finding.EligibleAfter.IsZero() || finding.EligibleAfter.Before(finding.DetectedAt)):
		return fmt.Errorf("actionable orphan requires a valid eligibility time")
	case (finding.Disposition == OrphanQuarantined || finding.Disposition == OrphanCleaned) && strings.TrimSpace(finding.UpdatedBy) == "":
		return fmt.Errorf("orphan mutation actor is required")
	default:
		return nil
	}
}

func (finding OrphanFinding) CanQuarantine(now time.Time) bool {
	return finding.Disposition == OrphanDetected && !now.UTC().Before(finding.EligibleAfter)
}

func (finding OrphanFinding) CanClean(now time.Time) bool {
	return finding.Disposition == OrphanQuarantined && !now.UTC().Before(finding.EligibleAfter) &&
		(finding.Resource.Kind == ResourceEC2Instance || finding.Resource.Kind == ResourceEBSVolume)
}
