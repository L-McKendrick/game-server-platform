package domain

import (
	"testing"
	"time"
)

func TestNewCompletedIdempotencyRecord(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 20, 0, 0, 0, time.UTC)

	record, err := NewCompletedIdempotencyRecord(
		"discord:interaction-1",
		"request-hash",
		"session-1",
		now,
		7*24*time.Hour,
	)
	if err != nil {
		t.Fatalf("NewCompletedIdempotencyRecord() returned error: %v", err)
	}

	if record.Status != IdempotencyCompleted {
		t.Errorf("Status = %q; want %q", record.Status, IdempotencyCompleted)
	}

	if record.ResultReference != "session-1" {
		t.Errorf(
			"ResultReference = %q; want %q",
			record.ResultReference,
			"session-1",
		)
	}

	wantExpiration := now.Add(7 * 24 * time.Hour).Unix()
	if record.ExpiresAtEpoch != wantExpiration {
		t.Errorf(
			"ExpiresAtEpoch = %d; want %d",
			record.ExpiresAtEpoch,
			wantExpiration,
		)
	}
}

func TestCompletedIdempotencyRecordRequiresResultReference(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 20, 0, 0, 0, time.UTC)

	_, err := NewCompletedIdempotencyRecord(
		"discord:interaction-1",
		"request-hash",
		"",
		now,
		7*24*time.Hour,
	)
	if err == nil {
		t.Fatal("NewCompletedIdempotencyRecord() returned nil error; expected error")
	}
}
