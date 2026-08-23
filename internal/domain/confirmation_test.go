package domain

import (
	"errors"
	"testing"
	"time"
)

func TestPendingConfirmationCodeIsStableAndActorGuildScoped(t *testing.T) {
	t.Parallel()
	code := PendingConfirmationCode("guild-1", "owner-1")
	if code != PendingConfirmationCode(" guild-1 ", " owner-1 ") || !confirmationCodePattern.MatchString(code) {
		t.Fatalf("pending confirmation code = %q", code)
	}
	if code == PendingConfirmationCode("guild-1", "owner-2") || code == PendingConfirmationCode("guild-2", "owner-1") {
		t.Fatal("pending confirmation slot is not actor/guild scoped")
	}
}

func TestConfirmationIsBoundAndExpiresWithinTenMinutes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	session := Session{ID: "session-1", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", LifecycleState: StateRunning, Version: 7}
	confirmation, err := NewConfirmation("confirmation-1", ConfirmationCode("confirmation-1"), session, ConfirmationArchive, now)
	if err != nil {
		t.Fatal(err)
	}
	if confirmation.BoundState != StateRunning || confirmation.BoundVersion != 7 || confirmation.ExpiresAt.Sub(now) != ConfirmationLifetime {
		t.Fatalf("confirmation = %#v", confirmation)
	}
	if !errors.Is(confirmation.CheckPending(now.Add(ConfirmationLifetime)), ErrConfirmationExpired) {
		t.Fatal("confirmation remained valid at its expiry boundary")
	}
}

func TestConfirmationRejectsMismatchedActor(t *testing.T) {
	t.Parallel()
	confirmation := Confirmation{OwnerDiscordUserID: "owner-1", GuildID: "guild-1"}
	if !errors.Is(confirmation.CheckActor("owner-2", "guild-1"), ErrConfirmationMismatch) ||
		!errors.Is(confirmation.CheckActor("owner-1", "guild-2"), ErrConfirmationMismatch) {
		t.Fatal("mismatched confirmation actor or guild was accepted")
	}
}
