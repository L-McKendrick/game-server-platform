package dynamodbstore

import (
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestConfirmationItemRoundTripPreservesBindingsAndTerminalState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 15, 0, 0, 0, time.UTC)
	session := domain.Session{ID: "session-1", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", LifecycleState: domain.StateRunning, Version: 9}
	confirmation, err := domain.NewConfirmation("confirmation-1", domain.ConfirmationCode("confirmation-1"), session, domain.ConfirmationArchive, now)
	if err != nil {
		t.Fatal(err)
	}
	confirmation.Status, confirmation.ConsumedAt = domain.ConfirmationConsumed, now.Add(time.Minute)
	stored, err := fromConfirmationItem(toConfirmationItem(confirmation))
	if err != nil {
		t.Fatal(err)
	}
	if stored != confirmation {
		t.Fatalf("confirmation = %#v; want %#v", stored, confirmation)
	}
}

func TestConfirmationSessionCheckBindsOwnerGuildStateAndVersion(t *testing.T) {
	t.Parallel()
	confirmation := domain.Confirmation{SessionID: "session-1", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", BoundState: domain.StateRunning, BoundVersion: 12}
	check := confirmationSessionCheck("table", confirmation)
	if check.ConditionExpression == nil || len(check.ExpressionAttributeValues) != 4 {
		t.Fatalf("condition check = %#v", check)
	}
}
