package domain

import (
	"testing"
	"time"
)

func TestRecordPlayerActivityRequiresContinuousKnownZero(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if err := session.RecordPlayerActivity(PlayerActivityObservation{Known: true, PlayerCount: 0, ObservedAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	started := session.IdleSince
	if started.IsZero() || !session.PlayerCountKnown || session.PlayerCount != 0 {
		t.Fatalf("first zero observation = %#v", session)
	}
	if err := session.RecordPlayerActivity(PlayerActivityObservation{Known: true, PlayerCount: 0, ObservedAt: now.Add(6 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if !session.IdleSince.Equal(started) {
		t.Fatalf("idle since = %s; want %s", session.IdleSince, started)
	}

	if err := session.RecordPlayerActivity(PlayerActivityObservation{ObservedAt: now.Add(11 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if session.PlayerCountKnown || !session.IdleSince.IsZero() {
		t.Fatalf("unknown observation retained idle evidence: %#v", session)
	}
	if err := session.RecordPlayerActivity(PlayerActivityObservation{Known: true, PlayerCount: 0, ObservedAt: now.Add(16 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if !session.IdleSince.Equal(now.Add(16 * time.Minute)) {
		t.Fatalf("zero after unknown did not begin a fresh window: %s", session.IdleSince)
	}
	if err := session.RecordPlayerActivity(PlayerActivityObservation{Known: true, PlayerCount: 2, ObservedAt: now.Add(21 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if !session.IdleSince.IsZero() || session.PlayerCount != 2 {
		t.Fatalf("player return did not clear idle evidence: %#v", session)
	}
}

func TestRecordPlayerActivityRejectsInvalidOrOlderEvidence(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	session, _ := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
	if err := session.RecordPlayerActivity(PlayerActivityObservation{Known: true, PlayerCount: 0, ObservedAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := session.RecordPlayerActivity(PlayerActivityObservation{Known: true, PlayerCount: 0, ObservedAt: now}); err == nil {
		t.Fatal("older observation accepted")
	}
	if err := session.RecordPlayerActivity(PlayerActivityObservation{Known: true, PlayerCount: 256, ObservedAt: now.Add(2 * time.Minute)}); err == nil {
		t.Fatal("out-of-range player count accepted")
	}
}

func TestAutomaticSleepDueRequiresFreshContinuousEvidence(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	session, _ := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now.Add(-time.Hour))
	session.DesiredState, session.ObservedState, session.LifecycleState = StateRunning, StateRunning, StateRunning
	session.Infrastructure = Infrastructure{CapacitySlotID: "slot-1", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile", AMIID: "ami-1", InstanceType: "c7i.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.1", LastObservedAt: now}
	session.PlayerCountKnown, session.PlayerCount = true, 0
	session.IdleSince, session.PlayerCountObservedAt = now.Add(-30*time.Minute), now
	if !session.AutomaticSleepDue(now) {
		t.Fatal("fresh 30-minute zero-player window was not due")
	}
	session.PlayerCountObservedAt = now.Add(-MaximumActivityEvidenceAge - time.Second)
	if session.AutomaticSleepDue(now) {
		t.Fatal("stale evidence was due")
	}
	session.PlayerCountObservedAt, session.PlayerCount = now, 1
	if session.AutomaticSleepDue(now) {
		t.Fatal("non-empty session was due")
	}
}
