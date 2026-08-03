package domain

import (
	"fmt"
	"strings"
)

// ActorType identifies the source or category of an operation.
type ActorType string

const (
	ActorTypeDiscordUser ActorType = "discord_user"
	ActorTypeSystem      ActorType = "system"
	ActorTypeLocalTest   ActorType = "local_test"
)

// Actor identifies who requested or performed an operation.
type Actor struct {
	Type ActorType
	ID   string
}

// Validate verifies that the actor can be recorded in an event.
func (actor Actor) Validate() error {
	switch {
	case strings.TrimSpace(string(actor.Type)) == "":
		return fmt.Errorf("actor type is required")
	case strings.TrimSpace(actor.ID) == "":
		return fmt.Errorf("actor ID is required")
	default:
		return nil
	}
}
