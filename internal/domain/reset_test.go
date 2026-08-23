package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestResetConfirmationIsExactBoundedAndSingleUse(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	confirmation, err := NewResetConfirmation("confirm-1", "dev", "guild-1", "admin-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if confirmation.Phrase() != "RESET dev "+ResetCode("confirm-1") || !strings.HasPrefix(confirmation.Phrase(), "RESET dev ") {
		t.Fatalf("phrase = %q", confirmation.Phrase())
	}
	if err := confirmation.Check("admin-1", "guild-1", confirmation.Phrase(), now.Add(9*time.Minute)); err != nil {
		t.Fatalf("valid confirmation: %v", err)
	}
	if !errors.Is(confirmation.Check("admin-1", "guild-1", strings.ToLower(confirmation.Phrase()), now), ErrConfirmationMismatch) {
		t.Fatal("lowercase phrase was accepted")
	}
	if !errors.Is(confirmation.Check("admin-2", "guild-1", confirmation.Phrase(), now), ErrConfirmationMismatch) {
		t.Fatal("different Administrator was accepted")
	}
	if !errors.Is(confirmation.Check("admin-1", "guild-1", confirmation.Phrase(), now.Add(ResetConfirmationLifetime)), ErrConfirmationExpired) {
		t.Fatal("expired confirmation was accepted")
	}
}

func TestResetOperationRequiresTerminalAuditTimestamp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	operation, err := NewResetOperation("reset-1", "dev", "guild-1", "admin-1", "correlation-1", now)
	if err != nil || !operation.Active() {
		t.Fatalf("operation = %#v err=%v", operation, err)
	}
	operation.Status = ResetSucceeded
	if operation.Validate() == nil {
		t.Fatal("terminal operation without completion timestamp validated")
	}
}
