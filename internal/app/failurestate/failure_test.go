package failurestate

import (
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestRecordUsesSafeFallbackAndNeverSchedulesRetry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 18, 0, 0, 0, time.UTC)
	session := domain.Session{}
	workflow := domain.Workflow{CorrelationID: "raw-correlation"}
	if err := Record(&session, workflow, "provider said no", "ERR_SAFE_FALLBACK", "Failed", "Safe failure detail.", domain.ResourceCostNone, now); err != nil {
		t.Fatal(err)
	}
	if session.Failure.Code != "ERR_SAFE_FALLBACK" || session.Failure.Stage != "Operation" ||
		session.Failure.RetryDisposition != domain.RetryNotScheduled || session.Failure.SupportReference == workflow.CorrelationID {
		t.Fatalf("failure = %#v", session.Failure)
	}
}

func TestImpactClassifiesAbsentRetainedAndUncertainResources(t *testing.T) {
	t.Parallel()
	if Impact(domain.Session{}, false) != domain.ResourceCostNone || Impact(domain.Session{}, true) != domain.ResourceCostUnknown {
		t.Fatal("empty resource impact was classified incorrectly")
	}
	session := domain.Session{Infrastructure: domain.Infrastructure{InstanceID: "i-1"}}
	if Impact(session, false) != domain.ResourceCostRetained || Impact(session, true) != domain.ResourceCostUnknown {
		t.Fatal("retained resource impact was classified incorrectly")
	}
}
