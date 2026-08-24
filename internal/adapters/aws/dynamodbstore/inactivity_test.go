package dynamodbstore

import (
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestSessionItemRoundTripPreservesInactivityEvidenceAndLegacyDefaults(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RecordPlayerActivity(domain.PlayerActivityObservation{Known: true, PlayerCount: 0, ObservedAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = domain.StateSleeping, domain.StateSleeping, domain.StateSleeping, domain.HealthStopped
	session.SleepingSince = now.Add(2 * time.Minute)
	roundTrip, err := fromSessionItem(toSessionItem(session))
	if err != nil {
		t.Fatal(err)
	}
	if !roundTrip.PlayerCountKnown || roundTrip.PlayerCount != 0 || !roundTrip.PlayerCountObservedAt.Equal(now.Add(time.Minute)) || !roundTrip.IdleSince.Equal(now.Add(time.Minute)) || !roundTrip.SleepingSince.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("round-trip evidence = %#v", roundTrip)
	}

	legacy := toSessionItem(session)
	legacy.PlayerCountKnown, legacy.PlayerCount, legacy.PlayerCountObservedAt, legacy.IdleSince, legacy.SleepingSince = false, 0, "", "", ""
	roundTrip, err = fromSessionItem(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.PlayerCountKnown || !roundTrip.PlayerCountObservedAt.IsZero() || !roundTrip.IdleSince.IsZero() || !roundTrip.SleepingSince.IsZero() {
		t.Fatalf("legacy evidence = %#v", roundTrip)
	}
}
