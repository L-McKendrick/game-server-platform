package sessions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestLifecycleTimeoutDefaultsAreAuditedAndReplaySafe(t *testing.T) {
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	service, err := NewService(repository, &sequenceIDGenerator{ids: []string{"timeout-audit"}}, fixedClock{now}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	command := LifecycleTimeoutDefaultsCommand{Actor: testActor("admin"), GuildID: "guild", CorrelationID: "correlation", IdempotencyKey: "defaults-key", Reason: "new policy", IsAdministrator: true, SleepAfterSeconds: 45 * 60, ArchiveAfterSeconds: 14 * 86400}
	command.IsAdministrator = false
	if _, err := service.ConfigureLifecycleTimeoutDefaults(context.Background(), command); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-admin error = %v", err)
	}
	command.IsAdministrator = true
	first, err := service.ConfigureLifecycleTimeoutDefaults(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ConfigureLifecycleTimeoutDefaults(context.Background(), command)
	if err != nil || second != first {
		t.Fatalf("replay = %#v, %v", second, err)
	}
	audits := repository.LifecycleTimeoutAudits("guild")
	if len(audits) != 1 || audits[0].PreviousSleepAfterSeconds != domain.DefaultSleepAfterSeconds || audits[0].Reason != "new policy" {
		t.Fatalf("audits = %#v", audits)
	}
	command.SleepAfterSeconds = 60 * 60
	if _, err := service.ConfigureLifecycleTimeoutDefaults(context.Background(), command); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay error = %v", err)
	}
}

func TestLifecycleTimeoutExtensionRequiresActiveSessionAndNeverShortens(t *testing.T) {
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "timeout-session", Slug: "timeout-session", DisplayName: "Timeout", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := domain.NewCompletedIdempotencyRecord("seed", "hash", session.ID, now, time.Hour)
	if err := repository.Create(context.Background(), session, domain.NewSessionCreatedEvent("created", "seed-correlation", testActor("owner"), session, now), seed); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, &sequenceIDGenerator{ids: []string{"extension-event"}}, fixedClock{now}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	command := LifecycleTimeoutExtensionCommand{Actor: testActor("admin"), GuildID: "guild", SessionID: session.ID, CorrelationID: "correlation", IdempotencyKey: "extension-key", Reason: "event runs longer", IsAdministrator: true, SleepExtensionSeconds: 15 * 60, ArchiveExtensionSeconds: 2 * 86400}
	if _, err := service.ExtendLifecycleTimeouts(context.Background(), command); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("draft extension error = %v", err)
	}
	running := session
	running.LifecycleState, running.DesiredState, running.ObservedState = domain.StateRunning, domain.StateRunning, domain.StateRunning
	running.Infrastructure = domain.Infrastructure{CapacitySlotID: "slot-1", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile", AMIID: "ami-1", InstanceType: "c7i.large", InstanceID: "i-test", DataVolumeID: "vol-test", LastObservedAt: now}
	running.Version++
	update, _ := domain.NewCompletedIdempotencyRecord("running", "hash", session.ID, now, time.Hour)
	if err := repository.SaveWithEvent(context.Background(), running, session.Version, domain.SessionEvent{ID: "running-event", SessionID: session.ID, Type: domain.EventStateChanged, OccurredAt: now, ActorType: string(domain.ActorTypeSystem), ActorID: "test", CorrelationID: "running", Data: map[string]string{}}, update); err != nil {
		t.Fatal(err)
	}
	first, err := service.ExtendLifecycleTimeouts(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ExtendLifecycleTimeouts(context.Background(), command)
	if err != nil || second.Version != first.Version {
		t.Fatalf("replay = %#v, %v", second, err)
	}
	if first.SleepAfterSeconds != domain.DefaultSleepAfterSeconds+15*60 || first.ArchiveAfterSeconds != domain.DefaultArchiveAfterSeconds+2*86400 {
		t.Fatalf("timeouts = %d/%d", first.SleepAfterSeconds, first.ArchiveAfterSeconds)
	}
	events := repository.Events(session.ID)
	if events[len(events)-1].Type != domain.EventLifecycleTimeoutsExtended || events[len(events)-1].Data["reason"] != "event runs longer" {
		t.Fatalf("event = %#v", events[len(events)-1])
	}
}
