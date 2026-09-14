package sessions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestAdminDurationConfigurationAndExtensionAreAuditedAndReplaySafe(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	repo := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "duration-1", Slug: "duration-1", DisplayName: "Duration", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := domain.NewCompletedIdempotencyRecord("duration-seed", "hash", session.ID, now, time.Hour)
	if err := repo.Create(context.Background(), session, domain.NewSessionCreatedEvent("seed-event", "seed-correlation", testActor("owner"), session, now), seed); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repo, &sequenceIDGenerator{ids: []string{"config-event", "extend-event"}}, fixedClock{now}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	command := DurationCommand{Actor: testActor("admin"), GuildID: "guild", SessionID: session.ID, CorrelationID: "config-correlation", IdempotencyKey: "config-key", Reason: "event planned", IsAdministrator: true, Seconds: 2 * 3600}
	command.IsAdministrator = false
	if _, err := service.ConfigureMaximumDuration(context.Background(), command); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-admin = %v", err)
	}
	command.IsAdministrator = true
	configured, err := service.ConfigureMaximumDuration(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.ConfigureMaximumDuration(context.Background(), command)
	if err != nil || replayed.Version != configured.Version {
		t.Fatalf("replay = %#v, %v", replayed, err)
	}
	if len(repo.Events(session.ID)) != 2 || repo.Events(session.ID)[1].Type != domain.EventMaximumDurationConfigured {
		t.Fatal("missing immutable config audit")
	}
	command.Seconds = 3 * 3600
	if _, err := service.ConfigureMaximumDuration(context.Background(), command); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay = %v", err)
	}
	started := configured
	if err := started.MaximumDuration.Start(now); err != nil {
		t.Fatal(err)
	}
	started.Version++
	started.UpdatedAt = now
	update, _ := domain.NewCompletedIdempotencyRecord("duration-start", "hash", session.ID, now, time.Hour)
	if err := repo.SaveWithEvent(context.Background(), started, configured.Version, domain.SessionEvent{ID: "start-event", SessionID: session.ID, Type: domain.EventWorkflowStarted, OccurredAt: now, ActorType: string(domain.ActorTypeSystem), ActorID: "test", CorrelationID: "start", Data: map[string]string{}}, update); err != nil {
		t.Fatal(err)
	}
	extend := DurationCommand{Actor: testActor("admin"), GuildID: "guild", SessionID: session.ID, CorrelationID: "extend-correlation", IdempotencyKey: "extend-key", Reason: "event delay", IsAdministrator: true, ExtensionSeconds: 3600}
	first, err := service.ExtendMaximumDuration(context.Background(), extend)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ExtendMaximumDuration(context.Background(), extend)
	if err != nil || !second.MaximumDuration.DeadlineAt.Equal(first.MaximumDuration.DeadlineAt) || second.Version != first.Version {
		t.Fatalf("extension replay = %#v, %v", second, err)
	}
	if len(repo.Events(session.ID)) != 4 || repo.Events(session.ID)[3].Type != domain.EventMaximumDurationExtended {
		t.Fatal("missing extension audit")
	}
}
