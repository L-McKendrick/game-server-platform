package sessions

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
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

type recordingCommandQueue struct{ commands []domain.CommandEnvelope }

func (queue *recordingCommandQueue) Enqueue(_ context.Context, command domain.CommandEnvelope) error {
	queue.commands = append(queue.commands, command)
	return nil
}

type failingCommandQueue struct{ err error }

func (queue failingCommandQueue) Enqueue(context.Context, domain.CommandEnvelope) error {
	return queue.err
}

func TestRequestArtifactIngestAllowsOwnerToQueueRunningPresetRevision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 21, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	queue := memory.NewArtifactQueue()
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now}, time.Hour, WithArtifactQueue(queue))
	if err != nil {
		t.Fatal(err)
	}
	session := seedPresetRevisionSession(t, repository, now, "owner-1")
	request := domain.ArtifactIngestRequest{SchemaVersion: 1, SessionID: session.ID, Kind: domain.ArtifactPreset, AttachmentID: "attachment-2", Filename: "revision.html", ContentType: "text/html", SizeBytes: 100, SourceURL: "https://cdn.discordapp.com/attachments/1/2/revision.html", ActorID: "owner-1", GuildID: session.GuildID, ChannelID: session.ChannelID, CorrelationID: "correlation-mods", IdempotencyKey: "discord:mods", RequestedAt: now, Purpose: domain.ArtifactPurposePresetRevision, ExpectedActivePresetRevision: 1}
	if err := service.RequestArtifactIngest(context.Background(), testActor("owner-1"), request); err != nil {
		t.Fatal(err)
	}
	if requests := queue.Requests(); len(requests) != 1 || !requests[0].IsPresetRevision() {
		t.Fatalf("queued requests = %#v", requests)
	}
	request.ActorID = "owner-2"
	if err := service.RequestArtifactIngest(context.Background(), testActor("owner-2"), request); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-owner error = %v; want forbidden", err)
	}
	request.ActorID = "owner-1"
	request.ChannelID = "channel-other"
	if err := service.RequestArtifactIngest(context.Background(), testActor("owner-1"), request); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-channel error = %v; want forbidden", err)
	}
	request.ChannelID = session.ChannelID
	request.ExpectedActivePresetRevision = 2
	if err := service.RequestArtifactIngest(context.Background(), testActor("owner-1"), request); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale base error = %v; want conflict", err)
	}
}

func seedPresetRevisionSession(t *testing.T, repository *memory.SessionRepository, now time.Time, owner string) domain.Session {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-mods", Slug: "session-mods", DisplayName: "Session Mods", GameType: "arma3", OwnerDiscordUserID: owner, GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState = domain.StateRunning, domain.StateRunning, domain.StateRunning
	session.PresetObjectKey = "sessions/session-mods/input/presets/v1.html"
	session.PresetRevisionSequence = 1
	session.ActivePresetRevision = domain.PresetRevision{Number: 1, PresetObjectKey: session.PresetObjectKey, Status: domain.PresetRevisionActive, StagedAt: now, ActivatedAt: now}
	event := domain.NewSessionCreatedEvent("event-mods", "correlation-create-mods", testActor(owner), session, now)
	record, err := domain.NewCompletedIdempotencyRecord("seed:mods", "hash-mods", session.ID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, record); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestRequestSessionCardPinsRenderedRevision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 15, 1, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	notifications := memory.NewNotificationQueue()
	service, err := NewService(
		repository,
		&sequenceIDGenerator{ids: []string{"session-card", "event-card"}},
		fixedClock{now: now}, time.Hour,
		WithNotificationQueue(notifications),
	)
	if err != nil {
		t.Fatal(err)
	}
	actor := testActor("owner-1")
	session := mustCreateSession(t, service, actor, "correlation-create-card", "card-session")
	command := SessionCardCommand{
		Actor: actor, SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID,
		CorrelationID: "correlation-card", NotificationID: "notification-card", Content: "card",
		CardRevision: session.Version,
	}
	if err := service.RequestSessionCard(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	requests := notifications.Requests()
	if len(requests) != 1 || requests[0].CardRevision != session.Version {
		t.Fatalf("card requests = %#v", requests)
	}
	command.NotificationID = "notification-future"
	command.CardRevision = session.Version + 1
	if err := service.RequestSessionCard(context.Background(), command); err == nil {
		t.Fatal("future card revision returned nil error")
	}
}

func TestGuildMemberCardRefreshRequiresCurrentRevision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 14, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	notifications := memory.NewNotificationQueue()
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now: now}, time.Hour, WithNotificationQueue(notifications))
	if err != nil {
		t.Fatal(err)
	}
	session := seedReadableCardSession(t, repository, now)
	member := testActor("member-2")
	command := SessionCardCommand{
		Actor: member, SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID,
		CorrelationID: "correlation-refresh", NotificationID: "card-refresh-1", Content: "refreshed card",
		CardRevision: session.Version, AllowGuildMember: true, RequireCurrentRevision: true,
	}
	if err := service.RequestSessionCard(context.Background(), command); err != nil {
		t.Fatalf("guild-member refresh error = %v", err)
	}
	command.NotificationID = "card-refresh-stale"
	command.CardRevision--
	if err := service.RequestSessionCard(context.Background(), command); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale refresh error = %v; want ErrConflict", err)
	}
	command.CardRevision = session.Version
	command.GuildID = "guild-other"
	if err := service.RequestSessionCard(context.Background(), command); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-guild refresh error = %v; want ErrNotFound", err)
	}
}

func TestGetActiveModlistAuthorizesGuildReadAndPinsRevision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 14, 30, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now: now}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	session := seedReadableCardSession(t, repository, now)
	resolved, err := service.ResolveCardControl(context.Background(), CardControlQuery{
		Actor: testActor("member-2"), GuildID: session.GuildID, Token: sessioncard.ControlToken(session.ID),
	})
	if err != nil || resolved.ID != session.ID {
		t.Fatalf("ResolveCardControl() = %#v, %v", resolved, err)
	}
	if _, err := service.ResolveCardControl(context.Background(), CardControlQuery{
		Actor: testActor("member-2"), GuildID: "guild-other", Token: sessioncard.ControlToken(session.ID),
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-guild control resolution error = %v; want ErrNotFound", err)
	}
	reference := domain.SessionModlistReference{
		SessionID: session.ID, ChannelID: session.ChannelID, MessageID: "modlist-message-1",
		ObjectKey: "sessions/card-session/input/modlists/digest/card-session-modlist.html",
		Filename:  "card-session-modlist.html", DeliveredRevision: 1,
		DeliveredNotificationID: "modlist-1", ContentSHA256: strings.Repeat("a", 64),
	}
	if err := repository.SaveModlistReference(context.Background(), reference); err != nil {
		t.Fatal(err)
	}
	stored, err := service.GetActiveModlist(context.Background(), ActiveModlistQuery{
		Actor: testActor("member-2"), SessionID: session.ID, GuildID: session.GuildID, ExpectedRevision: session.Version,
	})
	if err != nil || stored.ChannelID != reference.ChannelID || stored.MessageID != reference.MessageID || stored.Filename != reference.Filename {
		t.Fatalf("GetActiveModlist() = %#v, %v; want safe fields from %#v", stored, err, reference)
	}
	if _, err := service.GetActiveModlist(context.Background(), ActiveModlistQuery{
		Actor: testActor("member-2"), SessionID: session.ID, GuildID: session.GuildID, ExpectedRevision: session.Version - 1,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale modlist query error = %v; want ErrConflict", err)
	}
}

func seedReadableCardSession(t *testing.T, repository *memory.SessionRepository, now time.Time) domain.Session {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "card-session", Slug: "card-session", DisplayName: "Card Session", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.PresetArtifactStatus = domain.ArtifactAccepted
	session.PresetObjectKey = "sessions/card-session/input/presets/source.html"
	if err := session.RecordMutation(now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	actor := testActor("owner-1")
	event := domain.NewSessionCreatedEvent("event-card-session", "correlation-card-session", actor, session, now)
	record, err := domain.NewCompletedIdempotencyRecord("seed:card-session", "hash-card-session", session.ID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, record); err != nil {
		t.Fatal(err)
	}
	return session
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
			Actor:          actor,
			CorrelationID:  "correlation-1",
			IdempotencyKey: "discord:create-1",
			Slug:           "saturday-arma",
			DisplayName:    "Saturday Arma",
			Description:    "  Weekly\nco-op night  ",
			GameType:       "arma3",
			GuildID:        "guild-1",
			ChannelID:      "channel-1",
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
	if session.Description != "Weekly co-op night" {
		t.Errorf("Description = %q; want normalized description", session.Description)
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
	if events[0].Data["description"] != session.Description {
		t.Errorf("creation event description = %q; want %q", events[0].Data["description"], session.Description)
	}
}

func TestCreateGeneratesStableSlugAndReadableCollisionSuffixes(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(t, repository,
		"session-1", "event-1",
		"session-2", "event-2",
		"session-3", "event-3",
	)
	actor := testActor("owner-1")

	for index, expectedSlug := range []string{"friday-operations", "friday-operations-2", "friday-operations-3"} {
		session, err := service.Create(context.Background(), CreateCommand{
			Actor: actor, CorrelationID: fmt.Sprintf("correlation-%d", index+1),
			IdempotencyKey: fmt.Sprintf("discord:generated-%d", index+1),
			DisplayName:    "Friday Operations", GameType: "arma3",
			GuildID: "guild-1", ChannelID: "channel-1",
		})
		if err != nil {
			t.Fatalf("Create(%d) returned error: %v", index+1, err)
		}
		if session.Slug != expectedSlug {
			t.Fatalf("Create(%d) slug = %q; want %q", index+1, session.Slug, expectedSlug)
		}
		events := repository.Events(session.ID)
		if len(events) != 1 || events[0].Data["slug"] != expectedSlug {
			t.Fatalf("Create(%d) events = %#v", index+1, events)
		}
	}
}

func TestCreatePreservesExplicitLegacySlug(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(t, repository,
		"session-1", "event-1",
		"session-2", "event-2",
		"session-3", "event-3",
	)
	session, err := service.Create(context.Background(), CreateCommand{
		Actor: testActor("owner-1"), CorrelationID: "correlation-1",
		IdempotencyKey: "discord:legacy-slug", Slug: "legacy-explicit-slug",
		DisplayName: "A Different Display Name", GameType: "arma3",
		GuildID: "guild-1", ChannelID: "channel-1",
	})
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	if session.Slug != "legacy-explicit-slug" {
		t.Fatalf("Slug = %q; want explicit legacy slug", session.Slug)
	}

	generated, err := service.Create(context.Background(), CreateCommand{
		Actor: testActor("owner-2"), CorrelationID: "correlation-2",
		IdempotencyKey: "discord:generated-around-legacy",
		DisplayName:    "Legacy Explicit Slug", GameType: "arma3",
		GuildID: "guild-1", ChannelID: "channel-1",
	})
	if err != nil {
		t.Fatalf("generated collision Create() returned error: %v", err)
	}
	if generated.Slug != "legacy-explicit-slug-2" {
		t.Fatalf("generated collision slug = %q; want legacy-explicit-slug-2", generated.Slug)
	}
	storedLegacy, err := repository.Get(context.Background(), session.ID)
	if err != nil || storedLegacy.Slug != "legacy-explicit-slug" {
		t.Fatalf("legacy slug changed: session = %#v, error = %v", storedLegacy, err)
	}

	_, err = service.Create(context.Background(), CreateCommand{
		Actor: testActor("owner-3"), CorrelationID: "correlation-3",
		IdempotencyKey: "discord:legacy-conflict", Slug: "legacy-explicit-slug",
		DisplayName: "Another Session", GameType: "arma3",
		GuildID: "guild-1", ChannelID: "channel-1",
	})
	if !errors.Is(err, domain.ErrSlugConflict) {
		t.Fatalf("duplicate explicit slug error = %v; want ErrSlugConflict", err)
	}
}

func TestSlugCollisionSuffixStaysWithinGeneratedLimit(t *testing.T) {
	t.Parallel()
	base := strings.Repeat("a", domain.MaximumGeneratedSlugLength)
	if got := slugCollisionCandidate(base, 12); len(got) != domain.MaximumGeneratedSlugLength || !strings.HasSuffix(got, "-12") {
		t.Fatalf("slugCollisionCandidate() = %q (length %d)", got, len(got))
	}
}

func TestUpdateDescriptionPersistsNormalizedValueAndImmutableEvent(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(t, repository, "session-1", "event-create", "event-description")
	actor := testActor("owner-1")
	created := mustCreateSession(t, service, actor, "correlation-create", "saturday-arma")

	updated, err := service.UpdateDescription(context.Background(), UpdateDescriptionCommand{
		Actor: actor, SessionID: created.ID, GuildID: "guild-1",
		CorrelationID: "correlation-description", IdempotencyKey: "discord:description-1",
		Description: "  Friday\n​operations  ",
	})
	if err != nil {
		t.Fatalf("UpdateDescription() returned error: %v", err)
	}
	if updated.Description != "Friday operations" || updated.Version != created.Version+1 {
		t.Fatalf("updated session = %#v", updated)
	}

	events := repository.Events(created.ID)
	if len(events) != 2 || events[1].Type != domain.EventSessionDescriptionChanged {
		t.Fatalf("events = %#v; want description change event", events)
	}
	if events[1].Data["previous_description"] != "" || events[1].Data["description"] != "Friday operations" {
		t.Fatalf("description event data = %#v", events[1].Data)
	}

	replayed, err := service.UpdateDescription(context.Background(), UpdateDescriptionCommand{
		Actor: actor, SessionID: created.ID, GuildID: "guild-1",
		CorrelationID: "different-correlation", IdempotencyKey: "discord:description-1",
		Description: "Friday   operations",
	})
	if err != nil {
		t.Fatalf("replayed UpdateDescription() returned error: %v", err)
	}
	if replayed.Version != updated.Version || len(repository.Events(created.ID)) != 2 {
		t.Fatalf("replay created another mutation: %#v", replayed)
	}
}

func TestUpdateDescriptionEnforcesOwnerAndGuild(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(t, repository, "session-1", "event-create")
	owner := testActor("owner-1")
	created := mustCreateSession(t, service, owner, "correlation-create", "saturday-arma")

	tests := []UpdateDescriptionCommand{
		{Actor: testActor("other-owner"), SessionID: created.ID, GuildID: "guild-1", IdempotencyKey: "discord:description-owner", Description: "No"},
		{Actor: owner, SessionID: created.ID, GuildID: "other-guild", IdempotencyKey: "discord:description-guild", Description: "No"},
	}
	for _, command := range tests {
		if _, err := service.UpdateDescription(context.Background(), command); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("UpdateDescription(%q, %q) error = %v; want ErrForbidden", command.Actor.ID, command.GuildID, err)
		}
	}
	if len(repository.Events(created.ID)) != 1 {
		t.Fatal("unauthorized update wrote an event")
	}
}

func TestRequestStartQueuesNormalizedCommandForReadyOwnerSession(t *testing.T) {
	t.Parallel()
	repository := memory.NewSessionRepository()
	queue := &recordingCommandQueue{}
	clock := fixedClock{now: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)}
	service, err := NewService(
		repository, &sequenceIDGenerator{ids: []string{"session-1", "create-event", "transition-event"}},
		clock, 7*24*time.Hour, WithCommandQueue(queue),
	)
	if err != nil {
		t.Fatal(err)
	}
	actor := testActor("owner-1")
	session := mustCreateSession(t, service, actor, "create-correlation", "saturday-arma")
	if _, err := service.Transition(context.Background(), TransitionCommand{
		Actor: actor, SessionID: session.ID, To: domain.StateNew,
		CorrelationID: "ready-correlation", IdempotencyKey: "test:ready",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.RequestStart(context.Background(), StartCommand{
		Actor: actor, Roles: []string{"role-1"}, SessionID: session.ID,
		GuildID: "guild-1", ChannelID: "channel-1", CommandID: "interaction-1",
		CorrelationID: "start-correlation", IdempotencyKey: "discord:interaction-1",
	}); err != nil {
		t.Fatalf("RequestStart() returned error: %v", err)
	}
	if len(queue.commands) != 1 || queue.commands[0].CommandType != domain.CommandStartSession {
		t.Fatalf("queued commands = %#v", queue.commands)
	}
	if queue.commands[0].Actor.DiscordUserID != actor.ID || queue.commands[0].SessionID != session.ID {
		t.Fatalf("queued command = %#v", queue.commands[0])
	}
}

func TestRequestStartRoutesProvisionedSessionToBootstrap(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	queue := &recordingCommandQueue{}
	actor := testActor("owner-1")
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "session-bootstrap", Slug: "bootstrap", DisplayName: "Bootstrap", GameType: "arma3",
		OwnerDiscordUserID: actor.ID, GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Configure(domain.SessionConfiguration{GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400}, now); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(domain.ArtifactMission, "sessions/session-bootstrap/input/mission.pbo", now); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(domain.ArtifactPreset, "sessions/session-bootstrap/input/preset.html", now); err != nil {
		t.Fatal(err)
	}
	if err := session.AcquireProvisioningWorkflowLock("provision", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginInfrastructureProvisioning("provision", "slot-0", now); err != nil {
		t.Fatal(err)
	}
	if err := session.RecordInfrastructureLaunch("provision", domain.Infrastructure{
		CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1",
		SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile-1", AMIID: "ami-1",
		InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1",
		PublicIPv4: "203.0.113.1", LastObservedAt: now,
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteInfrastructureProvisioning("provision", now); err != nil {
		t.Fatal(err)
	}
	event := domain.NewSessionCreatedEvent("event-bootstrap", "correlation-create", actor, session, now)
	idempotency, err := domain.NewCompletedIdempotencyRecord("create-bootstrap", "hash-bootstrap", session.ID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now: now}, time.Hour, WithCommandQueue(queue))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RequestStart(context.Background(), StartCommand{
		Actor: actor, Roles: []string{"role-1"}, SessionID: session.ID,
		GuildID: "guild-1", ChannelID: "channel-1", CommandID: "interaction-bootstrap",
		CorrelationID: "correlation-bootstrap", IdempotencyKey: "discord:interaction-bootstrap",
	}); err != nil {
		t.Fatal(err)
	}
	if len(queue.commands) != 1 || queue.commands[0].CommandType != domain.CommandBootstrapServer {
		t.Fatalf("commands = %#v", queue.commands)
	}
}

func TestRequestStartReturnsPendingBootstrapProgressWithoutQueueingDuplicate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	queue := &recordingCommandQueue{}
	actor := testActor("owner-1")
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "session-continuation", Slug: "continuation", DisplayName: "Continuation", GameType: "arma3",
		OwnerDiscordUserID: actor.ID, GuildID: "guild-1", ChannelID: "channel-1",
	}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Configure(domain.SessionConfiguration{GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400}, now.Add(-55*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(domain.ArtifactMission, "sessions/session-continuation/input/mission.pbo", now.Add(-50*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := session.AttachArtifact(domain.ArtifactPreset, "sessions/session-continuation/input/preset.html", now.Add(-49*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := session.AcquireProvisioningWorkflowLock("provision", time.Hour, now.Add(-45*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginInfrastructureProvisioning("provision", "slot-0", now.Add(-44*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := session.RecordInfrastructureLaunch("provision", domain.Infrastructure{
		CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1",
		SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile-1", AMIID: "ami-1",
		InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1",
		PublicIPv4: "203.0.113.1", LastObservedAt: now.Add(-40 * time.Minute),
	}, now.Add(-40*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := session.CompleteInfrastructureProvisioning("provision", now.Add(-35*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := session.AcquireBootstrapWorkflowLock("bootstrap-continuation", 8*time.Hour, now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	event := domain.NewSessionCreatedEvent("event-continuation", "correlation-create", actor, session, now)
	idempotency, err := domain.NewCompletedIdempotencyRecord("create-continuation", "hash-continuation", session.ID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now: now}, time.Hour, WithCommandQueue(queue))
	if err != nil {
		t.Fatal(err)
	}
	err = service.RequestStart(context.Background(), StartCommand{
		Actor: actor, Roles: []string{"role-1"}, SessionID: session.ID,
		GuildID: session.GuildID, ChannelID: session.ChannelID, CommandID: "duplicate-start",
		CorrelationID: "duplicate-correlation", IdempotencyKey: "discord:duplicate-start",
	})
	var active domain.OperationInProgressError
	if !errors.As(err, &active) || active.WorkflowType != domain.BootstrapWorkflowType || active.Milestone != domain.ProgressAccepted {
		t.Fatalf("RequestStart() error = %#v; want pending bootstrap progress", err)
	}
	if len(queue.commands) != 0 {
		t.Fatalf("duplicate queued commands = %#v", queue.commands)
	}
}

func TestRequestLifecycle_AllowsGuildAdministratorForAnotherOwnersRunningSession(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	queue := &recordingCommandQueue{}
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now: now}, time.Hour, WithCommandQueue(queue))
	if err != nil {
		t.Fatal(err)
	}
	seedRunningSession(t, repository, now)

	err = service.RequestLifecycle(context.Background(), LifecycleCommand{
		Actor: testActor("admin-1"), Roles: []string{"unrelated-role"}, CanManageGuild: true,
		SessionID: "running-session", GuildID: "guild-1", ChannelID: "channel-1", CommandID: "interaction-1",
		CorrelationID: "lifecycle-correlation", IdempotencyKey: "discord:interaction-1", CommandType: domain.CommandSleepSession,
	})
	if err != nil {
		t.Fatalf("RequestLifecycle() error = %v", err)
	}
	if len(queue.commands) != 1 || !queue.commands[0].Actor.CanManageGuild || queue.commands[0].Actor.DiscordUserID != "admin-1" {
		t.Fatalf("queued commands = %#v", queue.commands)
	}
}

func TestRequestLifecycleReturnsExistingProgressWithoutQueueingDuplicate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 13, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	queue := &recordingCommandQueue{}
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now: now}, time.Hour, WithCommandQueue(queue))
	if err != nil {
		t.Fatal(err)
	}
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "active-session", Slug: "active-session", DisplayName: "Active Session", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = domain.StateRunning, domain.StateRunning, domain.StateRunning, domain.HealthHealthy
	session.Infrastructure = domain.Infrastructure{CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile", AMIID: "ami-1", InstanceType: "c7i.large", InstanceID: "i-1", DataVolumeID: "vol-1", LastObservedAt: now}
	if err := session.BeginSleep("workflow-active", time.Hour, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	event := domain.NewSessionCreatedEvent("event-active", "correlation-active", testActor("owner-1"), session, now)
	idempotency, _ := domain.NewCompletedIdempotencyRecord("create-active", "hash-active", session.ID, now, time.Hour)
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatal(err)
	}

	err = service.RequestLifecycle(context.Background(), LifecycleCommand{
		Actor: testActor("owner-1"), SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID,
		CommandID: "duplicate", CorrelationID: "correlation-duplicate", IdempotencyKey: "discord:duplicate", CommandType: domain.CommandSleepSession,
	})
	var active domain.OperationInProgressError
	if !errors.As(err, &active) || active.WorkflowType != domain.SleepWorkflowType || active.Milestone != domain.ProgressInstanceStopped {
		t.Fatalf("RequestLifecycle() error = %#v; want active sleep progress", err)
	}
	if len(queue.commands) != 0 {
		t.Fatalf("duplicate queued commands = %#v", queue.commands)
	}
}

func TestRequestLifecycle_RejectsAnotherOwnerWithoutGuildPermission(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	queue := &recordingCommandQueue{}
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now: now}, time.Hour, WithCommandQueue(queue))
	if err != nil {
		t.Fatal(err)
	}
	seedRunningSession(t, repository, now)

	err = service.RequestLifecycle(context.Background(), LifecycleCommand{
		Actor: testActor("member-1"), SessionID: "running-session", GuildID: "guild-1", ChannelID: "channel-1", CommandID: "interaction-1",
		CorrelationID: "lifecycle-correlation", IdempotencyKey: "discord:interaction-1", CommandType: domain.CommandSleepSession,
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("RequestLifecycle() error = %v; want ErrForbidden", err)
	}
	if len(queue.commands) != 0 {
		t.Fatalf("queued commands = %#v; want none", queue.commands)
	}
}

func TestRequestLifecycle_ArchiveRemainsOwnerOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	queue := &recordingCommandQueue{}
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now: now}, time.Hour, WithCommandQueue(queue))
	if err != nil {
		t.Fatal(err)
	}
	seedRunningSession(t, repository, now)

	err = service.RequestLifecycle(context.Background(), LifecycleCommand{
		Actor: testActor("admin-1"), CanManageGuild: true,
		SessionID: "running-session", GuildID: "guild-1", ChannelID: "channel-1", CommandID: "interaction-archive",
		CorrelationID: "archive-correlation", IdempotencyKey: "discord:interaction-archive", CommandType: domain.CommandArchiveSession,
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("RequestLifecycle() error = %v; want ErrForbidden", err)
	}
	if len(queue.commands) != 0 {
		t.Fatalf("queued commands = %#v; want none", queue.commands)
	}
}

func TestRequestLifecycleRequiresDurableConfirmationForDestructiveOwnerAction(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 16, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	queue := &recordingCommandQueue{}
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now: now}, time.Hour, WithCommandQueue(queue), WithConfirmationRepository(repository))
	if err != nil {
		t.Fatal(err)
	}
	seedRunningSession(t, repository, now)
	err = service.RequestLifecycle(context.Background(), LifecycleCommand{
		Actor: testActor("owner-1"), SessionID: "running-session", GuildID: "guild-1", ChannelID: "channel-1",
		CommandID: "direct-archive", CorrelationID: "correlation-direct", IdempotencyKey: "direct:archive", CommandType: domain.CommandArchiveSession,
	})
	if !errors.Is(err, domain.ErrConfirmationRequired) || len(queue.commands) != 0 {
		t.Fatalf("direct archive error = %v; commands = %#v", err, queue.commands)
	}
}

func TestDestructiveConfirmationQueuesOnlyAfterAtomicConsumption(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 16, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	queue := &recordingCommandQueue{}
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now: now}, time.Hour,
		WithCommandQueue(queue), WithConfirmationRepository(repository))
	if err != nil {
		t.Fatal(err)
	}
	seedRunningSession(t, repository, now)
	actor := testActor("owner-1")
	confirmation, err := service.RequestConfirmation(context.Background(), ConfirmationRequest{
		Actor: actor, SessionID: "running-session", GuildID: "guild-1", RequestID: "interaction-archive", Action: domain.ConfirmationArchive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.commands) != 0 || confirmation.Status != domain.ConfirmationPending {
		t.Fatalf("premature commands = %#v; confirmation = %#v", queue.commands, confirmation)
	}
	consumed, err := service.Confirm(context.Background(), ConfirmCommand{
		Actor: actor, Roles: []string{"role-1", "role-2"}, GuildID: "guild-1", ChannelID: "channel-1", Code: confirmation.Code,
		CommandID: "interaction-confirm", CorrelationID: "correlation-confirm", IdempotencyKey: "discord:interaction-confirm",
	})
	if err != nil {
		t.Fatal(err)
	}
	if consumed.Status != domain.ConfirmationConsumed || len(queue.commands) != 1 || queue.commands[0].CommandType != domain.CommandArchiveSession || queue.commands[0].SessionID != "running-session" {
		t.Fatalf("consumed = %#v; commands = %#v", consumed, queue.commands)
	}
	if roles := queue.commands[0].Actor.Roles; !slices.Equal(roles, []string{"role-1", "role-2"}) {
		t.Fatalf("queued command roles = %#v", roles)
	}
	if _, err := service.Confirm(context.Background(), ConfirmCommand{Actor: actor, GuildID: "guild-1", ChannelID: "channel-1", Code: confirmation.Code, CommandID: "replay", CorrelationID: "correlation-replay", IdempotencyKey: "discord:replay"}); !errors.Is(err, domain.ErrConfirmationConsumed) {
		t.Fatalf("confirmation replay error = %v", err)
	}
	if len(queue.commands) != 1 {
		t.Fatalf("replay queued commands = %#v", queue.commands)
	}
	if _, err := service.RequestConfirmation(context.Background(), ConfirmationRequest{Actor: actor, SessionID: "running-session", GuildID: "guild-1", RequestID: "interaction-archive", Action: domain.ConfirmationArchive}); !errors.Is(err, domain.ErrConfirmationConsumed) {
		t.Fatalf("creation replay after consumption error = %v", err)
	}
}

func TestConfirmedQueueFailureIsTruthfulAndNeverReusesCode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 16, 30, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now: now}, time.Hour,
		WithCommandQueue(failingCommandQueue{err: errors.New("ambiguous queue failure")}), WithConfirmationRepository(repository))
	if err != nil {
		t.Fatal(err)
	}
	seedRunningSession(t, repository, now)
	actor := testActor("owner-1")
	confirmation, err := service.RequestConfirmation(context.Background(), ConfirmationRequest{Actor: actor, SessionID: "running-session", GuildID: "guild-1", RequestID: "interaction-terminate-fail", Action: domain.ConfirmationTerminate})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Confirm(context.Background(), ConfirmCommand{Actor: actor, GuildID: "guild-1", ChannelID: "channel-1", Code: confirmation.Code, CommandID: "confirm-fail", CorrelationID: "correlation-fail", IdempotencyKey: "discord:confirm-fail"})
	if !errors.Is(err, domain.ErrConfirmationDispatchUncertain) {
		t.Fatalf("queue failure = %v", err)
	}
	if _, _, err := repository.ConsumeConfirmation(context.Background(), confirmation.Code, "owner-1", "guild-1", now); !errors.Is(err, domain.ErrConfirmationConsumed) {
		t.Fatalf("queue-failed confirmation replay error = %v", err)
	}
}

func TestRequestLifecycle_TerminationRemainsOwnerOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	queue := &recordingCommandQueue{}
	service, err := NewService(repository, &sequenceIDGenerator{}, fixedClock{now: now}, time.Hour, WithCommandQueue(queue))
	if err != nil {
		t.Fatal(err)
	}
	seedRunningSession(t, repository, now)

	err = service.RequestLifecycle(context.Background(), LifecycleCommand{
		Actor: testActor("admin-1"), CanManageGuild: true,
		SessionID: "running-session", GuildID: "guild-1", ChannelID: "channel-1", CommandID: "interaction-terminate",
		CorrelationID: "terminate-correlation", IdempotencyKey: "discord:interaction-terminate", CommandType: domain.CommandDestroySession,
	})
	if !errors.Is(err, domain.ErrForbidden) || len(queue.commands) != 0 {
		t.Fatalf("RequestLifecycle() error = %v, commands = %#v; want owner-only rejection", err, queue.commands)
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

func TestGetAllowsExplicitGuildMemberReadWithoutBroadeningOwnerDefault(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(t, repository, "session-1", "event-1")
	created := mustCreateSession(t, service, testActor("owner-1"), "correlation-create", "guild-visible")

	got, err := service.Get(context.Background(), GetQuery{
		Actor: testActor("member-2"), SessionID: created.ID,
		GuildID: created.GuildID, AllowGuildMember: true,
	})
	if err != nil || got.ID != created.ID {
		t.Fatalf("Get(guild member) = %#v, %v", got, err)
	}
	_, err = service.Get(context.Background(), GetQuery{
		Actor: testActor("member-2"), SessionID: created.ID,
		GuildID: "other-guild", AllowGuildMember: true,
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get(other guild) error = %v; want ErrNotFound", err)
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
			Actor:          actor,
			SessionID:      created.ID,
			To:             domain.StateNew,
			CorrelationID:  "correlation-transition",
			IdempotencyKey: "discord:transition-1",
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
			Actor:          actor,
			SessionID:      created.ID,
			To:             domain.StateRunning,
			CorrelationID:  "correlation-transition",
			IdempotencyKey: "discord:transition-1",
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

func TestConfigurePersistsRevisionAndEvent(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(t, repository, "session-1", "event-create", "event-configure")
	actor := testActor("owner-1")
	created := mustCreateSession(t, service, actor, "correlation-create", "saturday-arma")

	configured, err := service.Configure(context.Background(), ConfigureCommand{
		Actor:               actor,
		SessionID:           created.ID,
		GuildID:             "guild-1",
		CorrelationID:       "correlation-configure",
		IdempotencyKey:      "discord:configure-1",
		GameProfileID:       "arma3-default",
		SleepAfterSeconds:   3600,
		ArchiveAfterSeconds: 14 * 86400,
		TeamSpeakEnabled:    true,
		Vanilla:             true,
	})
	if err != nil {
		t.Fatalf("Configure() returned error: %v", err)
	}
	if configured.ConfigurationRevision != 1 || configured.Version != 2 {
		t.Errorf("revision/version = %d/%d; want 1/2", configured.ConfigurationRevision, configured.Version)
	}
	if configured.SleepAfterSeconds != 3600 || !configured.TeamSpeakEnabled || !configured.Vanilla {
		t.Errorf("configuration was not applied: %#v", configured)
	}
	events := repository.Events(created.ID)
	if len(events) != 2 || events[1].Type != domain.EventSessionConfigured {
		t.Fatalf("events = %#v; want SessionConfigured", events)
	}
	if events[1].Data["vanilla"] != "true" {
		t.Fatalf("configured event vanilla = %q; want true", events[1].Data["vanilla"])
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

func TestCreateReplaysSameIdempotencyKey(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(
		t,
		repository,
		"session-1",
		"event-1",
	)

	command := CreateCommand{
		Actor:          testActor("owner-1"),
		CorrelationID:  "correlation-1",
		IdempotencyKey: "discord:create-replay",
		Slug:           "saturday-arma",
		DisplayName:    "Saturday Arma",
		GameType:       "arma3",
		GuildID:        "guild-1",
		ChannelID:      "channel-1",
	}

	first, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatalf("first Create() returned error: %v", err)
	}

	second, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatalf("second Create() returned error: %v", err)
	}

	if second.ID != first.ID {
		t.Errorf("replayed session ID = %q; want %q", second.ID, first.ID)
	}

	if eventCount := len(repository.Events(first.ID)); eventCount != 1 {
		t.Errorf("event count = %d; want 1", eventCount)
	}
}

func TestCreateRejectsIdempotencyKeyReuseWithDifferentRequest(t *testing.T) {
	t.Parallel()

	repository := memory.NewSessionRepository()
	service := newTestService(
		t,
		repository,
		"session-1",
		"event-1",
	)

	command := CreateCommand{
		Actor:          testActor("owner-1"),
		CorrelationID:  "correlation-1",
		IdempotencyKey: "discord:create-conflict",
		Slug:           "saturday-arma",
		DisplayName:    "Saturday Arma",
		GameType:       "arma3",
		GuildID:        "guild-1",
		ChannelID:      "channel-1",
	}

	if _, err := service.Create(context.Background(), command); err != nil {
		t.Fatalf("first Create() returned error: %v", err)
	}

	command.DisplayName = "Different Session"

	_, err := service.Create(context.Background(), command)
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf(
			"second Create() error = %v; want ErrIdempotencyConflict",
			err,
		)
	}
}

func TestTransitionReplaysSameIdempotencyKey(t *testing.T) {
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

	command := TransitionCommand{
		Actor:          actor,
		SessionID:      created.ID,
		To:             domain.StateNew,
		CorrelationID:  "correlation-transition",
		IdempotencyKey: "discord:transition-replay",
	}

	first, err := service.Transition(context.Background(), command)
	if err != nil {
		t.Fatalf("first Transition() returned error: %v", err)
	}

	second, err := service.Transition(context.Background(), command)
	if err != nil {
		t.Fatalf("second Transition() returned error: %v", err)
	}

	if second.Version != first.Version {
		t.Errorf("replayed version = %d; want %d", second.Version, first.Version)
	}

	if eventCount := len(repository.Events(created.ID)); eventCount != 2 {
		t.Errorf("event count = %d; want 2", eventCount)
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
		7*24*time.Hour,
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
			Actor:          actor,
			CorrelationID:  correlationID,
			IdempotencyKey: "test:create:" + correlationID,
			Slug:           slug,
			DisplayName:    "Test Session",
			GameType:       "arma3",
			GuildID:        "guild-1",
			ChannelID:      "channel-1",
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

func seedRunningSession(t *testing.T, repository *memory.SessionRepository, now time.Time) {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "running-session", Slug: "running-session", DisplayName: "Running Session", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = domain.StateRunning, domain.StateRunning, domain.StateRunning, domain.HealthHealthy
	session.Infrastructure = domain.Infrastructure{
		CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"},
		InstanceProfile: "instance-profile", AMIID: "ami-1", InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.1", LastObservedAt: now,
	}
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
	event := domain.NewSessionCreatedEvent("running-session-event", "running-session-correlation", testActor("owner-1"), session, now)
	idempotency, err := domain.NewCompletedIdempotencyRecord("running-session-create", "running-session-hash", session.ID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatal(err)
	}
}
