package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type cardSender struct {
	existing        string
	calls           int
	failures        int
	cardMessage     string
	cardRequests    []domain.NotificationRequest
	modlistCalls    int
	modlistExisting []string
	modlistMessage  string
}

func (*cardSender) Send(context.Context, domain.NotificationRequest) error { return nil }
func (sender *cardSender) SendModlist(_ context.Context, _ domain.NotificationRequest, _ []byte, existing string) (string, error) {
	sender.modlistCalls++
	sender.modlistExisting = append(sender.modlistExisting, existing)
	if sender.modlistMessage != "" {
		return sender.modlistMessage, nil
	}
	return "modlist-message-1", nil
}
func (sender *cardSender) SendCard(_ context.Context, request domain.NotificationRequest, existing string) (string, error) {
	sender.existing, sender.calls = existing, sender.calls+1
	sender.cardRequests = append(sender.cardRequests, request)
	if sender.failures > 0 {
		sender.failures--
		return "", errors.New("temporary Discord failure")
	}
	if sender.cardMessage != "" {
		return sender.cardMessage, nil
	}
	return "message-1", nil
}

type modlistObjectReader struct {
	body []byte
	err  error
}

func (reader modlistObjectReader) Get(context.Context, string) ([]byte, error) {
	return append([]byte(nil), reader.body...), reader.err
}

func TestDeliverModlistPersistsStableMessageAndLinksCard(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	repository := seedCardSession(t, now, "session-modlist")
	body := []byte("<!DOCTYPE html><html><body>sanitized</body></html>")
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	sender := &cardSender{}
	handler := &handler{
		sender: sender, cards: repository, objects: modlistObjectReader{body: body},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "modlist-preset-attachment-1", Kind: domain.NotificationSessionModlist,
		SessionID: "session-modlist", GuildID: "guild-1", ChannelID: "channel-1", Content: "Active modlist",
		Attachment: &domain.NotificationAttachment{
			ObjectKey: "sessions/session-modlist/input/modlists/digest/session-modlist-modlist.html",
			Filename:  "session-modlist-modlist.html", ContentType: "text/html; charset=utf-8",
			SHA256: digest, SizeBytes: int64(len(body)), Revision: 1,
		},
		CorrelationID: "correlation-modlist", RequestedAt: now,
	}

	if err := handler.deliverModlist(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	reference, err := repository.GetModlistReference(context.Background(), request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if reference.MessageID != "modlist-message-1" || reference.ObjectKey != request.Attachment.ObjectKey ||
		reference.DeliveredRevision != 1 || reference.ContentSHA256 != digest {
		t.Fatalf("modlist reference = %#v", reference)
	}
	if sender.calls != 1 || len(sender.cardRequests) != 1 ||
		!strings.Contains(sender.cardRequests[0].Content, "https://discord.com/channels/guild-1/channel-1/modlist-message-1") {
		t.Fatalf("card deliveries = %#v; want one linked card", sender.cardRequests)
	}

	if err := handler.deliverModlist(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if sender.modlistCalls != 2 || len(sender.modlistExisting) != 2 || sender.modlistExisting[1] != "modlist-message-1" {
		t.Fatalf("modlist replay calls=%d existing=%#v", sender.modlistCalls, sender.modlistExisting)
	}
	if sender.calls != 1 {
		t.Fatalf("card calls=%d; want unchanged linked card replay skipped", sender.calls)
	}
}

func TestDeliverModlistRejectsObjectMetadataMismatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 12, 30, 0, 0, time.UTC)
	repository := seedCardSession(t, now, "session-modlist-mismatch")
	body := []byte("<!DOCTYPE html><html></html>")
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	sender := &cardSender{}
	handler := &handler{sender: sender, cards: repository, objects: modlistObjectReader{body: append(body, 'x')}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "modlist-mismatch", Kind: domain.NotificationSessionModlist,
		SessionID: "session-modlist-mismatch", GuildID: "guild-1", ChannelID: "channel-1", Content: "Active modlist",
		Attachment: &domain.NotificationAttachment{
			ObjectKey: "sessions/session-modlist-mismatch/input/modlists/digest/session-modlist-modlist.html",
			Filename:  "session-modlist-modlist.html", ContentType: "text/html; charset=utf-8",
			SHA256: digest, SizeBytes: int64(len(body)), Revision: 1,
		},
		CorrelationID: "correlation-mismatch", RequestedAt: now,
	}
	if err := handler.deliverModlist(context.Background(), request); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("metadata mismatch error = %v", err)
	}
	if sender.modlistCalls != 0 {
		t.Fatalf("modlist calls=%d; want mismatch rejected before Discord", sender.modlistCalls)
	}
}

func TestDeliverCardCreatesOnceSkipsReplayAndEditsNewRevision(t *testing.T) {
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
		Content: "card", CardRevision: 1, CorrelationID: "correlation-1", RequestedAt: now,
	}
	if err := handler.deliverCard(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetCardReference(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MessageID != "message-1" || stored.ChannelID != "channel-1" ||
		stored.DeliveredRevision != 1 || stored.DeliveredNotificationID != "card-1" || stored.ContentSHA256 == "" {
		t.Fatalf("stored card = %#v", stored)
	}
	if err := handler.deliverCard(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if sender.calls != 1 {
		t.Fatalf("sender calls=%d; want successful replay to skip Discord", sender.calls)
	}

	request.NotificationID = "card-2"
	request.CardRevision = 2
	request.Content = "updated card"
	if err := handler.deliverCard(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if sender.calls != 2 || sender.existing != "message-1" {
		t.Fatalf("sender calls=%d existing=%q; want edit of persisted message", sender.calls, sender.existing)
	}
	stored, err = repository.GetCardReference(context.Background(), session.ID)
	if err != nil || stored.DeliveredRevision != 2 || stored.DeliveredNotificationID != "card-2" {
		t.Fatalf("updated card = %#v, %v", stored, err)
	}

	request.NotificationID = "card-stale"
	request.CardRevision = 1
	request.Content = "stale card"
	if err := handler.deliverCard(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if sender.calls != 2 {
		t.Fatalf("sender calls=%d; want stale revision to be ignored", sender.calls)
	}
}

func TestDeliverCardRejectsChangedContentForDeliveredNotification(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	repository := seedCardSession(t, now, "session-conflict")
	sender := &cardSender{}
	handler := &handler{sender: sender, cards: repository, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "card-conflict", Kind: domain.NotificationSessionCard,
		SessionID: "session-conflict", GuildID: "guild-1", ChannelID: "channel-1",
		Content: "first card", CardRevision: 1, CorrelationID: "correlation-conflict", RequestedAt: now,
	}
	if err := handler.deliverCard(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Content = "different card"
	if err := handler.deliverCard(context.Background(), request); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("changed replay error = %v; want ErrIdempotencyConflict", err)
	}
	if sender.calls != 1 {
		t.Fatalf("sender calls=%d; want conflicting replay rejected before Discord", sender.calls)
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
		Content: "card", CardRevision: 1, CorrelationID: "correlation-recovery", RequestedAt: now,
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

func TestDeliverCardPersistsReplacementAfterDeletedMessage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 13, 0, 0, 0, time.UTC)
	repository := seedCardSession(t, now, "session-deleted-card")
	if err := repository.SaveCardReference(context.Background(), domain.SessionCardReference{
		SessionID: "session-deleted-card", ChannelID: "channel-1", MessageID: "deleted-card",
		DeliveredRevision: 1, DeliveredNotificationID: "card-old", ContentSHA256: strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	sender := &cardSender{cardMessage: "replacement-card"}
	handler := &handler{sender: sender, cards: repository, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "card-repair", Kind: domain.NotificationSessionCard,
		SessionID: "session-deleted-card", GuildID: "guild-1", ChannelID: "channel-1", Content: "current card",
		CardRevision: 1, CorrelationID: "correlation-repair", RequestedAt: now,
	}
	if err := handler.deliverCard(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	reference, err := repository.GetCardReference(context.Background(), request.SessionID)
	if err != nil || reference.MessageID != "replacement-card" || reference.DeliveredNotificationID != "card-repair" {
		t.Fatalf("replacement reference = %#v, %v", reference, err)
	}
	if sender.calls != 1 || sender.existing != "deleted-card" {
		t.Fatalf("sender calls=%d existing=%q", sender.calls, sender.existing)
	}
}

func TestDeliverCardEditOutagePreservesReferenceForRetry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 13, 30, 0, 0, time.UTC)
	repository := seedCardSession(t, now, "session-edit-outage")
	original := domain.SessionCardReference{
		SessionID: "session-edit-outage", ChannelID: "channel-1", MessageID: "existing-card",
		DeliveredRevision: 1, DeliveredNotificationID: "card-old", ContentSHA256: strings.Repeat("a", 64),
	}
	if err := repository.SaveCardReference(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	sender := &cardSender{failures: 1, cardMessage: "existing-card"}
	handler := &handler{sender: sender, cards: repository, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: "card-current", Kind: domain.NotificationSessionCard,
		SessionID: "session-edit-outage", GuildID: "guild-1", ChannelID: "channel-1", Content: "current card",
		CardRevision: 1, CorrelationID: "correlation-outage", RequestedAt: now,
	}
	if err := handler.deliverCard(context.Background(), request); err == nil {
		t.Fatal("first edit returned nil error; want retryable failure")
	}
	unchanged, err := repository.GetCardReference(context.Background(), request.SessionID)
	if err != nil || unchanged != original {
		t.Fatalf("reference after outage = %#v, %v; want %#v", unchanged, err, original)
	}
	if err := handler.deliverCard(context.Background(), request); err != nil {
		t.Fatalf("retry returned error: %v", err)
	}
	updated, err := repository.GetCardReference(context.Background(), request.SessionID)
	if err != nil || updated.DeliveredNotificationID != request.NotificationID || updated.MessageID != original.MessageID {
		t.Fatalf("reference after retry = %#v, %v", updated, err)
	}
	if sender.calls != 2 || sender.existing != original.MessageID {
		t.Fatalf("sender calls=%d existing=%q", sender.calls, sender.existing)
	}
}

func seedCardSession(t *testing.T, now time.Time, sessionID string) *memory.SessionRepository {
	t.Helper()
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: sessionID, Slug: "session", DisplayName: "Session", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(domain.ArtifactPreset, "sessions/"+sessionID+"/input/presets/source.html", now); err != nil {
		t.Fatal(err)
	}
	event := domain.NewSessionCreatedEvent("event-"+sessionID, "correlation-"+sessionID, domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now)
	record, err := domain.NewCompletedIdempotencyRecord("create-"+sessionID, "hash-"+sessionID, session.ID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, record); err != nil {
		t.Fatal(err)
	}
	return repository
}
