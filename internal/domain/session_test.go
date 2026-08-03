package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewSessionCreatesDraft(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 2, 20, 0, 0, 0, time.UTC)

	session, err := NewSession(
		NewSessionInput{
			ID:                 "01JTESTSESSION",
			Slug:               "saturday-arma",
			DisplayName:        "Saturday Arma",
			GameType:           "arma3",
			OwnerDiscordUserID: "owner-1",
			GuildID:            "guild-1",
			ChannelID:          "channel-1",
		},
		now,
	)
	if err != nil {
		t.Fatalf("NewSession() returned error: %v", err)
	}

	if session.LifecycleState != StateDraft {
		t.Errorf(
			"LifecycleState = %q; want %q",
			session.LifecycleState,
			StateDraft,
		)
	}

	if session.Version != 1 {
		t.Errorf("Version = %d; want 1", session.Version)
	}
}

func TestTransitionIncrementsVersion(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 2, 20, 0, 0, 0, time.UTC)

	session, err := NewSession(
		NewSessionInput{
			ID:                 "01JTESTSESSION",
			Slug:               "saturday-arma",
			DisplayName:        "Saturday Arma",
			GameType:           "arma3",
			OwnerDiscordUserID: "owner-1",
			GuildID:            "guild-1",
			ChannelID:          "channel-1",
		},
		now,
	)
	if err != nil {
		t.Fatalf("NewSession() returned error: %v", err)
	}

	if err := session.Transition(StateNew, now.Add(time.Second)); err != nil {
		t.Fatalf("Transition() returned error: %v", err)
	}

	if session.LifecycleState != StateNew {
		t.Errorf(
			"LifecycleState = %q; want %q",
			session.LifecycleState,
			StateNew,
		)
	}

	if session.Version != 2 {
		t.Errorf("Version = %d; want 2", session.Version)
	}
}

func TestTransitionRejectsInvalidTransition(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 2, 20, 0, 0, 0, time.UTC)

	session, err := NewSession(
		NewSessionInput{
			ID:                 "01JTESTSESSION",
			Slug:               "saturday-arma",
			DisplayName:        "Saturday Arma",
			GameType:           "arma3",
			OwnerDiscordUserID: "owner-1",
			GuildID:            "guild-1",
			ChannelID:          "channel-1",
		},
		now,
	)
	if err != nil {
		t.Fatalf("NewSession() returned error: %v", err)
	}

	err = session.Transition(StateRunning, now.Add(time.Second))
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf(
			"Transition() error = %v; want ErrInvalidTransition",
			err,
		)
	}
}
