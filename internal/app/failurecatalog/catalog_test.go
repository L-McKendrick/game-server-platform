package failurecatalog

import (
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestLookupRendersActionableKnownFailureWithoutRawDetail(t *testing.T) {
	t.Parallel()
	failure, err := domain.NewFailureRecord(domain.FailureRecordInput{
		Code: "ERR_ARCHIVE_FAILED", Stage: "Archive verification",
		RetryDisposition: domain.RetryNotScheduled, ResourceImpact: domain.ResourceCostRetained,
		Detail: "Safe bounded audit detail.", FailedAt: time.Now(), SupportReference: "support_ABC123",
	})
	if err != nil {
		t.Fatal(err)
	}
	presentation := Lookup(failure)
	joined := presentation.WhatHappened + presentation.LikelyReason + presentation.PlatformAction + presentation.UserAction + presentation.RetryDisposition + presentation.BillingImpact
	for _, required := range []string{"archive", "platform", "operator", "No retry is scheduled", "incur cost"} {
		if !strings.Contains(strings.ToLower(joined), strings.ToLower(required)) {
			t.Fatalf("presentation %q omitted %q", joined, required)
		}
	}
	if strings.Contains(joined, failure.Detail) {
		t.Fatal("catalog copied persisted detail into fixed presentation")
	}
}

func TestLookupUsesSafeUnknownFallback(t *testing.T) {
	t.Parallel()
	presentation := Lookup(domain.FailureRecord{
		Code: "ERR_NEW_PROVIDER_CASE", Stage: "Unknown", RetryDisposition: domain.RetryNotScheduled,
		ResourceImpact: domain.ResourceCostUnknown, Detail: "Bounded.", FailedAt: time.Now(), SupportReference: "support_ABC123",
	})
	if !strings.Contains(presentation.LikelyReason, "without exposing unsafe diagnostics") ||
		!strings.Contains(presentation.BillingImpact, "may remain") || presentation.SupportReference != "support_ABC123" {
		t.Fatalf("unknown presentation = %#v", presentation)
	}
}

func TestEveryKnownErrorCategoryIsActionableAndTruthful(t *testing.T) {
	t.Parallel()
	for code := range entries {
		failure := domain.FailureRecord{
			Code: code, Stage: "Operation", RetryDisposition: domain.RetryNotScheduled,
			ResourceImpact: domain.ResourceCostRetained, Detail: "Safe detail.",
			FailedAt: time.Date(2026, 8, 17, 17, 0, 0, 0, time.UTC), SupportReference: "ref_ABC123",
		}
		presentation := Lookup(failure)
		if presentation.WhatHappened == "" || presentation.LikelyReason == "" || presentation.PlatformAction == "" ||
			presentation.UserAction == "" || presentation.RetryDisposition != "No retry is scheduled." ||
			!strings.Contains(presentation.BillingImpact, "incur cost") || presentation.SupportReference != failure.SupportReference {
			t.Errorf("%s presentation = %#v", code, presentation)
		}
	}
	commandFailure := Lookup(domain.FailureRecord{Code: "ERR_BOOTSTRAP_COMMAND_TIMEDOUT", RetryDisposition: domain.RetryNotScheduled, ResourceImpact: domain.ResourceCostRetained})
	if !strings.Contains(commandFailure.WhatHappened, "Game and content setup") {
		t.Fatalf("dynamic bootstrap category = %#v", commandFailure)
	}
}

func TestRetryAndBillingWordingMatchesPersistedDisposition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		retry  domain.RetryDisposition
		impact domain.ResourceCostImpact
		want   []string
	}{
		{domain.RetryNotScheduled, domain.ResourceCostNone, []string{"No retry is scheduled", "No billable"}},
		{domain.RetryNotScheduled, domain.ResourceCostRetained, []string{"No retry is scheduled", "continue to incur cost"}},
		{domain.RetryNotScheduled, domain.ResourceCostUnknown, []string{"No retry is scheduled", "cleanup is not confirmed"}},
		{domain.RetryScheduled, domain.ResourceCostNone, []string{"retry is scheduled", "No billable"}},
	}
	for _, test := range tests {
		presentation := Lookup(domain.FailureRecord{Code: "ERR_ARCHIVE_FAILED", RetryDisposition: test.retry, ResourceImpact: test.impact})
		text := presentation.RetryDisposition + " " + presentation.BillingImpact
		for _, want := range test.want {
			if !strings.Contains(text, want) {
				t.Errorf("retry=%q impact=%q text=%q; want %q", test.retry, test.impact, text, want)
			}
		}
	}
}
