package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Session represents the persistent platform identity of a game server.
type Session struct {
	ID                 string
	Slug               string
	DisplayName        string
	GameType           string
	OwnerDiscordUserID string
	GuildID            string
	ChannelID          string

	DesiredState   LifecycleState
	ObservedState  LifecycleState
	LifecycleState LifecycleState
	HealthStatus   HealthStatus

	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewSessionInput contains the required information for a new draft session.
type NewSessionInput struct {
	ID                 string
	Slug               string
	DisplayName        string
	GameType           string
	OwnerDiscordUserID string
	GuildID            string
	ChannelID          string
}

// NewSession creates a valid draft session.
func NewSession(input NewSessionInput, now time.Time) (Session, error) {
	now = now.UTC()

	session := Session{
		ID:                 strings.TrimSpace(input.ID),
		Slug:               strings.TrimSpace(input.Slug),
		DisplayName:        strings.TrimSpace(input.DisplayName),
		GameType:           strings.ToLower(strings.TrimSpace(input.GameType)),
		OwnerDiscordUserID: strings.TrimSpace(input.OwnerDiscordUserID),
		GuildID:            strings.TrimSpace(input.GuildID),
		ChannelID:          strings.TrimSpace(input.ChannelID),

		DesiredState:   StateDraft,
		ObservedState:  StateDraft,
		LifecycleState: StateDraft,
		HealthStatus:   HealthUnknown,

		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := session.Validate(); err != nil {
		return Session{}, err
	}

	return session, nil
}

// Validate verifies the session's domain invariants.
func (session Session) Validate() error {
	switch {
	case session.ID == "":
		return fmt.Errorf("session ID is required")
	case session.Slug == "":
		return fmt.Errorf("session slug is required")
	case !slugPattern.MatchString(session.Slug):
		return fmt.Errorf(
			"session slug %q must contain lowercase letters, numbers, and single hyphens",
			session.Slug,
		)
	case session.DisplayName == "":
		return fmt.Errorf("session display name is required")
	case session.GameType == "":
		return fmt.Errorf("game type is required")
	case session.OwnerDiscordUserID == "":
		return fmt.Errorf("owner Discord user ID is required")
	case session.GuildID == "":
		return fmt.Errorf("Discord guild ID is required")
	case session.ChannelID == "":
		return fmt.Errorf("Discord channel ID is required")
	case !session.DesiredState.Valid():
		return fmt.Errorf("invalid desired state %q", session.DesiredState)
	case !session.ObservedState.Valid():
		return fmt.Errorf("invalid observed state %q", session.ObservedState)
	case !session.LifecycleState.Valid():
		return fmt.Errorf("invalid lifecycle state %q", session.LifecycleState)
	case !session.HealthStatus.Valid():
		return fmt.Errorf("invalid health status %q", session.HealthStatus)
	case session.Version < 1:
		return fmt.Errorf("session version must be at least 1")
	case session.CreatedAt.IsZero():
		return fmt.Errorf("created timestamp is required")
	case session.UpdatedAt.IsZero():
		return fmt.Errorf("updated timestamp is required")
	case session.UpdatedAt.Before(session.CreatedAt):
		return fmt.Errorf("updated timestamp cannot precede created timestamp")
	default:
		return nil
	}
}

// Transition performs a synchronous lifecycle transition.
//
// Later workflow phases will update desired and observed state separately.
// For the metadata vertical slice, this method advances all three together.
func (session *Session) Transition(
	to LifecycleState,
	now time.Time,
) error {
	from := session.LifecycleState

	if !from.CanTransition(to) {
		return fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition,
			from,
			to,
		)
	}

	session.DesiredState = to
	session.ObservedState = to
	session.LifecycleState = to
	session.Version++
	session.UpdatedAt = now.UTC()

	return session.Validate()
}
