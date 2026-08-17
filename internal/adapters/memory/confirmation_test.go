package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestConfirmationConsumptionIsAtomicSingleUseAndStateBound(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 14, 0, 0, 0, time.UTC)
	repository := NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	event := domain.NewSessionCreatedEvent("event-1", "correlation-1", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now)
	idempotency, _ := domain.NewCompletedIdempotencyRecord("create-1", "hash-1", session.ID, now, time.Hour)
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatal(err)
	}
	confirmation, err := domain.NewConfirmation("confirmation-1", domain.ConfirmationCode("confirmation-1"), session, domain.ConfirmationTerminate, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateConfirmation(context.Background(), confirmation); err != nil {
		t.Fatal(err)
	}
	consumed, snapshot, err := repository.ConsumeConfirmation(context.Background(), confirmation.Code, "owner-1", "guild-1", now.Add(time.Minute))
	if err != nil || consumed.Status != domain.ConfirmationConsumed || snapshot.Version != session.Version {
		t.Fatalf("consume = %#v, snapshot %#v, err %v", consumed, snapshot, err)
	}
	if _, _, err := repository.ConsumeConfirmation(context.Background(), confirmation.Code, "owner-1", "guild-1", now.Add(2*time.Minute)); !errors.Is(err, domain.ErrConfirmationConsumed) {
		t.Fatalf("replay error = %v", err)
	}
}

func TestConfirmationConsumptionRejectsActorExpiryAndStateDrift(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 15, 0, 0, 0, time.UTC)
	repository := NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-2", Slug: "session-2", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	event := domain.NewSessionCreatedEvent("event-2", "correlation-2", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now)
	idempotency, _ := domain.NewCompletedIdempotencyRecord("create-2", "hash-2", session.ID, now, time.Hour)
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatal(err)
	}
	confirmation, _ := domain.NewConfirmation("confirmation-2", domain.ConfirmationCode("confirmation-2"), session, domain.ConfirmationTerminate, now)
	if err := repository.CreateConfirmation(context.Background(), confirmation); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.ConsumeConfirmation(context.Background(), confirmation.Code, "owner-2", "guild-1", now.Add(time.Minute)); !errors.Is(err, domain.ErrConfirmationMismatch) {
		t.Fatalf("actor mismatch error = %v", err)
	}

	repository.mu.Lock()
	changed := repository.sessions[session.ID]
	changed.Version++
	changed.UpdatedAt = now.Add(2 * time.Minute)
	repository.sessions[session.ID] = changed
	repository.mu.Unlock()
	if _, _, err := repository.ConsumeConfirmation(context.Background(), confirmation.Code, "owner-1", "guild-1", now.Add(3*time.Minute)); !errors.Is(err, domain.ErrConfirmationStateDrift) {
		t.Fatalf("state drift error = %v", err)
	}

	expired, _ := domain.NewConfirmation("confirmation-3", domain.ConfirmationCode("confirmation-3"), changed, domain.ConfirmationTerminate, now)
	if err := repository.CreateConfirmation(context.Background(), expired); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.ConsumeConfirmation(context.Background(), expired.Code, "owner-1", "guild-1", expired.ExpiresAt); !errors.Is(err, domain.ErrConfirmationExpired) {
		t.Fatalf("expiry error = %v", err)
	}
}

func TestConfirmationCancellationIsOwnerGuildBoundAndPermanent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 17, 0, 0, 0, time.UTC)
	repository := NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-3", Slug: "session-3", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	event := domain.NewSessionCreatedEvent("event-3", "correlation-3", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now)
	idempotency, _ := domain.NewCompletedIdempotencyRecord("create-3", "hash-3", session.ID, now, time.Hour)
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatal(err)
	}
	confirmation, _ := domain.NewConfirmation("confirmation-4", domain.ConfirmationCode("confirmation-4"), session, domain.ConfirmationTerminate, now)
	if err := repository.CreateConfirmation(context.Background(), confirmation); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CancelConfirmation(context.Background(), confirmation.Code, "owner-1", "guild-2", now.Add(time.Minute)); !errors.Is(err, domain.ErrConfirmationMismatch) {
		t.Fatalf("guild mismatch error = %v", err)
	}
	cancelled, err := repository.CancelConfirmation(context.Background(), confirmation.Code, "owner-1", "guild-1", now.Add(2*time.Minute))
	if err != nil || cancelled.Status != domain.ConfirmationCancelled {
		t.Fatalf("cancelled = %#v, err %v", cancelled, err)
	}
	if _, _, err := repository.ConsumeConfirmation(context.Background(), confirmation.Code, "owner-1", "guild-1", now.Add(3*time.Minute)); !errors.Is(err, domain.ErrConfirmationCancelled) {
		t.Fatalf("cancelled consume error = %v", err)
	}
}
