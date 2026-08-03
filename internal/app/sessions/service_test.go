package sessions

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type fixedClock struct {
	now time.Time
}

func (clock fixedClock) Now() time.Time {
	return clock.now
}

type sequenceIDGenerator struct {
	ids   []string
	index int
}

func (generator *sequenceIDGenerator) New(
	_ time.Time,
) (string, error) {
	if generator.index >= len(generator.ids) {
		return "", fmt.Errorf("no test IDs remaining")
	}

	id := generator.ids[generator.index]
	generator.index++

	return id, nil
}

func TestCreatePersistsDraftSessionAndEvent(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(
		t,
		repository,
		"session-1",
		"event-1",
	)

	actor := testActor("owner-1")

	session, err := service.Create(
		context.Background(),
		CreateCommand{
			Actor:         actor,
			CorrelationID: "correlation-1",
			Slug:          "saturday-arma",
			DisplayName:   "Saturday Arma",
			GameType:      "arma3",
			GuildID:       "guild-1",
			ChannelID:     "channel-1",
		},
	)
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	if session.ID != "session-1" {
		t.Errorf("ID = %q; want %q", session.ID, "session-1")
	}

	if session.LifecycleState != domain.StateDraft {
		t.Errorf(
			"LifecycleState = %q; want %q",
			session.LifecycleState,
			domain.StateDraft,
		)
	}

	if session.Version != 1 {
		t.Errorf("Version = %d; want 1", session.Version)
	}

	events := repository.Events(session.ID)
	if len(events) != 1 {
		t.Fatalf("event count = %d; want 1", len(events))
	}

	if events[0].Type != domain.EventSessionCreated {
		t.Errorf(
			"event type = %q; want %q",
			events[0].Type,
			domain.EventSessionCreated,
		)
	}

	if events[0].ActorID != actor.ID {
		t.Errorf(
			"event actor = %q; want %q",
			events[0].ActorID,
			actor.ID,
		)
	}
}

func TestGetRejectsNonOwner(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(
		t,
		repository,
		"session-1",
		"event-1",
	)

	created := mustCreateSession(
		t,
		service,
		testActor("owner-1"),
		"correlation-create",
		"one-session",
	)

	_, err := service.Get(
		context.Background(),
		GetQuery{
			Actor:     testActor("owner-2"),
			SessionID: created.ID,
		},
	)

	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf(
			"Get() error = %v; want ErrForbidden",
			err,
		)
	}
}

func TestTransitionAdvancesStateAndStoresEvent(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(
		t,
		repository,
		"session-1",
		"event-create",
		"event-transition",
	)

	actor := testActor("owner-1")

	created := mustCreateSession(
		t,
		service,
		actor,
		"correlation-create",
		"saturday-arma",
	)

	transitioned, err := service.Transition(
		context.Background(),
		TransitionCommand{
			Actor:         actor,
			SessionID:     created.ID,
			To:            domain.StateNew,
			CorrelationID: "correlation-transition",
		},
	)
	if err != nil {
		t.Fatalf("Transition() returned error: %v", err)
	}

	if transitioned.LifecycleState != domain.StateNew {
		t.Errorf(
			"LifecycleState = %q; want %q",
			transitioned.LifecycleState,
			domain.StateNew,
		)
	}

	if transitioned.Version != 2 {
		t.Errorf("Version = %d; want 2", transitioned.Version)
	}

	events := repository.Events(created.ID)
	if len(events) != 2 {
		t.Fatalf("event count = %d; want 2", len(events))
	}

	if events[1].Type != domain.EventStateChanged {
		t.Errorf(
			"event type = %q; want %q",
			events[1].Type,
			domain.EventStateChanged,
		)
	}

	if events[1].Data["from"] != string(domain.StateDraft) {
		t.Errorf(
			"from state = %q; want %q",
			events[1].Data["from"],
			domain.StateDraft,
		)
	}

	if events[1].Data["to"] != string(domain.StateNew) {
		t.Errorf(
			"to state = %q; want %q",
			events[1].Data["to"],
			domain.StateNew,
		)
	}
}

func TestTransitionRejectsInvalidStateChange(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(
		t,
		repository,
		"session-1",
		"event-create",
	)

	actor := testActor("owner-1")

	created := mustCreateSession(
		t,
		service,
		actor,
		"correlation-create",
		"saturday-arma",
	)

	_, err := service.Transition(
		context.Background(),
		TransitionCommand{
			Actor:         actor,
			SessionID:     created.ID,
			To:            domain.StateRunning,
			CorrelationID: "correlation-transition",
		},
	)

	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf(
			"Transition() error = %v; want ErrInvalidTransition",
			err,
		)
	}

	events := repository.Events(created.ID)
	if len(events) != 1 {
		t.Errorf(
			"event count after rejected transition = %d; want 1",
			len(events),
		)
	}

	stored, err := repository.Get(
		context.Background(),
		created.ID,
	)
	if err != nil {
		t.Fatalf("repository.Get() returned error: %v", err)
	}

	if stored.Version != 1 {
		t.Errorf("stored version = %d; want 1", stored.Version)
	}
}

func TestListReturnsOnlyActorSessions(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(
		t,
		repository,
		"session-owner-1",
		"event-owner-1",
		"session-owner-2",
		"event-owner-2",
	)

	ownerOne := testActor("owner-1")
	ownerTwo := testActor("owner-2")

	mustCreateSession(
		t,
		service,
		ownerOne,
		"correlation-owner-1",
		"owner-one-session",
	)

	mustCreateSession(
		t,
		service,
		ownerTwo,
		"correlation-owner-2",
		"owner-two-session",
	)

	sessions, err := service.List(
		context.Background(),
		ListQuery{
			Actor: ownerOne,
			Limit: 25,
		},
	)
	if err != nil {
		t.Fatalf("List() returned error: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("session count = %d; want 1", len(sessions))
	}

	if sessions[0].OwnerDiscordUserID != ownerOne.ID {
		t.Errorf(
			"owner = %q; want %q",
			sessions[0].OwnerDiscordUserID,
			ownerOne.ID,
		)
	}
}

func newTestService(
	t *testing.T,
	repository *memory.SessionRepository,
	ids ...string,
) *Service {
	t.Helper()

	service, err := NewService(
		repository,
		&sequenceIDGenerator{ids: ids},
		fixedClock{
			now: time.Date(
				2026,
				8,
				3,
				10,
				0,
				0,
				0,
				time.UTC,
			),
		},
	)
	if err != nil {
		t.Fatalf("NewService() returned error: %v", err)
	}

	return service
}

func mustCreateSession(
	t *testing.T,
	service *Service,
	actor domain.Actor,
	correlationID string,
	slug string,
) domain.Session {
	t.Helper()

	session, err := service.Create(
		context.Background(),
		CreateCommand{
			Actor:         actor,
			CorrelationID: correlationID,
			Slug:          slug,
			DisplayName:   "Test Session",
			GameType:      "arma3",
			GuildID:       "guild-1",
			ChannelID:     "channel-1",
		},
	)
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	return session
}

func testActor(id string) domain.Actor {
	return domain.Actor{
		Type: domain.ActorTypeDiscordUser,
		ID:   id,
	}
}
