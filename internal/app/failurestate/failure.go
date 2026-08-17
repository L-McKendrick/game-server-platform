// Package failurestate centralizes construction of the active sanitized
// failure projection written by lifecycle workers.
package failurestate

import (
	"regexp"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

var stableCode = regexp.MustCompile(`^ERR_[A-Z0-9_]{1,60}$`)

func Record(session *domain.Session, workflow domain.Workflow, code, fallbackCode, stage, detail string, impact domain.ResourceCostImpact, now time.Time) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	if !stableCode.MatchString(code) {
		code = fallbackCode
	}
	stage = strings.TrimSpace(stage)
	if stage == "" || strings.EqualFold(stage, "failed") {
		stage = "Operation"
	}
	failure, err := domain.NewFailureRecord(domain.FailureRecordInput{
		Code: code, Stage: stage, RetryDisposition: domain.RetryNotScheduled,
		ResourceImpact: impact, Detail: detail, FailedAt: now.UTC(),
		SupportReference: domain.FailureSupportReference(workflow.CorrelationID),
	})
	if err != nil {
		return err
	}
	return session.SetFailure(failure)
}

func Impact(session domain.Session, uncertain bool) domain.ResourceCostImpact {
	if session.Infrastructure.Empty() && !uncertain {
		return domain.ResourceCostNone
	}
	if uncertain {
		return domain.ResourceCostUnknown
	}
	return domain.ResourceCostRetained
}
