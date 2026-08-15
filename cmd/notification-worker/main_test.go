package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type cardSender struct {
	existing string
	calls    int
	failures int
}

func (*cardSender) Send(context.Context, domain.NotificationRequest) error { return nil }
func (sender *cardSender) SendCard(_ context.Context, _ domain.NotificationRequest, existing string) (string, error) {
	sender.existing, sender.calls = existing, sender.calls+1
	if sender.failures > 0 {
		sender.failures--
		return "", errors.New("temporary Discord failure")
	}
	return "message-1", nil
}

func TestDeliverCardCreatesOnceThenEditsPersistedMessage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "session-1", Slug: "session", DisplayName: "Session", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	event := domain.NewSessionCreatedEvent("event-1", "correlation-1", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now)
	record, err := domain.NewCompletedIdempotencyRecord("create-1", "hash-1", session.ID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, record); err != nil {
		t.Fatal(err)
	}
	sender := &cardSender{}
	handler := &handler{sender: sender, cards: repository, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "card-1", Kind: domain.NotificationSessionCard,
		SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID,
		Content: "card", CorrelationID: "correlation-1", RequestedAt: now,
	}
	if err := handler.deliverCard(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetCardReference(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MessageID != "message-1" || stored.ChannelID != "channel-1" {
		t.Fatalf("stored card = %#v", stored)
	}
	if err := handler.deliverCard(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if sender.calls != 2 || sender.existing != "message-1" {
		t.Fatalf("sender calls=%d existing=%q; want edit of persisted message", sender.calls, sender.existing)
	}
}

func TestDeliverCardFailureLeavesDeliveryRecoverable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "session-recovery", Slug: "recovery", DisplayName: "Recovery", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	event := domain.NewSessionCreatedEvent("event-recovery", "correlation-recovery", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now)
	record, err := domain.NewCompletedIdempotencyRecord("create-recovery", "hash-recovery", session.ID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, record); err != nil {
		t.Fatal(err)
	}
	sender := &cardSender{failures: 1}
	handler := &handler{sender: sender, cards: repository, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "card-recovery", Kind: domain.NotificationSessionCard,
		SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID,
		Content: "card", CorrelationID: "correlation-recovery", RequestedAt: now,
	}

	if err := handler.deliverCard(context.Background(), request); err == nil {
		t.Fatal("first delivery returned nil error; want retryable Discord failure")
	}
	if _, err := repository.GetCardReference(context.Background(), session.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("card reference after failed delivery error = %v; want ErrNotFound", err)
	}
	if err := handler.deliverCard(context.Background(), request); err != nil {
		t.Fatalf("recovered delivery returned error: %v", err)
	}
	reference, err := repository.GetCardReference(context.Background(), session.ID)
	if err != nil || reference.MessageID != "message-1" {
		t.Fatalf("recovered card reference = %#v, %v", reference, err)
	}
	if sender.calls != 2 || sender.existing != "" {
		t.Fatalf("sender calls=%d existing=%q; want a fresh retry after the failed create", sender.calls, sender.existing)
	}
}
