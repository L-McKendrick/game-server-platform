package domain

import (
	"strings"
	"testing"
	"time"
)

func TestNewFailureRecordSanitizesAndBoundsDetail(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	record, err := NewFailureRecord(FailureRecordInput{
		Code: "err_bootstrap_command_failed", Stage: "  Game\nsetup ",
		RetryDisposition: RetryNotScheduled, ResourceImpact: ResourceCostRetained,
		Detail:   "token=super-secret instance i-0123456789abcdef0 AKIA1234567890ABCDEF https://example.invalid/?sig=secret 10.0.0.8 " + strings.Repeat("x", 300),
		FailedAt: now, SupportReference: "support_ABC123",
	})
	if err != nil {
		t.Fatalf("NewFailureRecord() returned error: %v", err)
	}
	if record.Code != "ERR_BOOTSTRAP_COMMAND_FAILED" || record.Stage != "Game setup" {
		t.Fatalf("normalized record = %#v", record)
	}
	if strings.Contains(record.Detail, "super-secret") || strings.Contains(record.Detail, "i-0123456789abcdef0") ||
		strings.Contains(record.Detail, "AKIA1234567890ABCDEF") || strings.Contains(record.Detail, "example.invalid") || strings.Contains(record.Detail, "10.0.0.8") {
		t.Fatalf("detail was not redacted: %q", record.Detail)
	}
	if len([]rune(record.Detail)) > MaximumFailureDetailRunes {
		t.Fatalf("detail contains %d runes", len([]rune(record.Detail)))
	}
}

func TestFailureRecordRejectsPartialOrInvalidProjection(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tests := []FailureRecord{
		{Code: "ERR_BAD"},
		{Code: "BAD", Stage: "Setup", RetryDisposition: RetryNotScheduled, ResourceImpact: ResourceCostNone, Detail: "Stopped.", FailedAt: now, SupportReference: "ABC123"},
		{Code: "ERR_BAD", Stage: "Setup", RetryDisposition: "MAYBE", ResourceImpact: ResourceCostNone, Detail: "Stopped.", FailedAt: now, SupportReference: "ABC123"},
	}
	for _, record := range tests {
		if err := record.Validate(); err == nil {
			t.Fatalf("Validate() accepted %#v", record)
		}
	}
}
