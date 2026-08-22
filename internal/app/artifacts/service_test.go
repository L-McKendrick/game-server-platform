package artifacts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type testClock struct{ now time.Time }

func (clock testClock) Now() time.Time { return clock.now }

type testIDs struct {
	ids   []string
	index int
}

func (generator *testIDs) New(time.Time) (string, error) {
	if generator.index >= len(generator.ids) {
		return "", fmt.Errorf("no test IDs remaining")
	}
	id := generator.ids[generator.index]
	generator.index++
	return id, nil
}

type testDownloader struct {
	body  []byte
	calls int
}

func (downloader *testDownloader) Download(context.Context, domain.ArtifactIngestRequest) ([]byte, error) {
	downloader.calls++
	return append([]byte(nil), downloader.body...), nil
}

type storedObject struct {
	key      string
	contents []byte
}

type testObjectStore struct{ objects []storedObject }

func (store *testObjectStore) Put(_ context.Context, key string, _ string, body []byte, _ string) error {
	store.objects = append(store.objects, storedObject{key: key, contents: append([]byte(nil), body...)})
	return nil
}

type testNotifications struct{ requests []domain.NotificationRequest }

func (queue *testNotifications) Enqueue(_ context.Context, request domain.NotificationRequest) error {
	queue.requests = append(queue.requests, request)
	return nil
}

func TestProcessStoresValidatedMissionAndPersistsMetadata(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 21, 0, 0, 0, time.UTC)
	repository := seededRepository(t, now)
	downloader := &testDownloader{body: []byte("0123456789abcdef")}
	objects := &testObjectStore{}
	notifications := &testNotifications{}
	service, err := NewService(repository, downloader, objects, notifications, &testIDs{ids: []string{"artifact-event-1"}}, testClock{now}, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewService() returned error: %v", err)
	}
	request := missionRequest(now, int64(len(downloader.body)))

	if err := service.Process(context.Background(), request); err != nil {
		t.Fatalf("Process() returned error: %v", err)
	}
	if len(objects.objects) != 1 || objects.objects[0].key != "sessions/session-1/input/missions/operation.pbo" {
		t.Fatalf("stored objects = %#v; want one mission object", objects.objects)
	}
	session, err := repository.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("Get() returned error: %v", err)
	}
	if session.MissionObjectKey != objects.objects[0].key || session.MissionArtifactStatus != domain.ArtifactAccepted || session.Version != 2 {
		t.Fatalf("persisted session = %#v", session)
	}
	if len(notifications.requests) != 1 || notifications.requests[0].NotificationID != "card-artifact-mission-attachment-1" ||
		notifications.requests[0].Kind != domain.NotificationSessionCard || notifications.requests[0].CardRevision != session.Version {
		t.Fatalf("notifications = %#v", notifications.requests)
	}

	if err := service.Process(context.Background(), request); err != nil {
		t.Fatalf("replayed Process() returned error: %v", err)
	}
	if downloader.calls != 2 {
		t.Fatalf("download calls = %d; want 2 to verify replay content", downloader.calls)
	}
	if len(objects.objects) != 1 {
		t.Fatalf("object writes after replay = %d; want 1", len(objects.objects))
	}
	if len(notifications.requests) != 2 || notifications.requests[0].NotificationID != notifications.requests[1].NotificationID {
		t.Fatalf("replay notifications = %#v; want deterministic ID", notifications.requests)
	}
}

func TestProcessSanitizesMissionFilenameBeforeObjectStorage(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 21, 0, 0, 0, time.UTC)
	repository := seededRepository(t, now)
	downloader := &testDownloader{body: []byte("0123456789abcdef")}
	objects := &testObjectStore{}
	service, err := NewService(repository, downloader, objects, &testNotifications{}, &testIDs{ids: []string{"artifact-event-1"}}, testClock{now}, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := missionRequest(now, int64(len(downloader.body)))
	request.Filename = `operation";passwordAdmin="unsafe.Stratis.pbo`

	if err := service.Process(context.Background(), request); err != nil {
		t.Fatalf("Process() returned error: %v", err)
	}
	want := "sessions/session-1/input/missions/operation_passwordAdmin_unsafe.Stratis.pbo"
	if len(objects.objects) != 1 || objects.objects[0].key != want {
		t.Fatalf("stored objects = %#v; want %q", objects.objects, want)
	}
}

func TestProcessRejectsInvalidPresetWithoutWritingObject(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 21, 0, 0, 0, time.UTC)
	repository := seededRepository(t, now)
	downloader := &testDownloader{body: []byte("not an html preset")}
	objects := &testObjectStore{}
	notifications := &testNotifications{}
	service, err := NewService(repository, downloader, objects, notifications, &testIDs{ids: []string{"reject-event-1"}}, testClock{now}, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewService() returned error: %v", err)
	}
	request := missionRequest(now, int64(len(downloader.body)))
	request.Kind = domain.ArtifactPreset
	request.Filename = "preset.html"

	if err := service.Process(context.Background(), request); err != nil {
		t.Fatalf("Process() returned error: %v", err)
	}
	if len(objects.objects) != 0 {
		t.Fatalf("object writes = %d; want 0", len(objects.objects))
	}
	if len(notifications.requests) != 1 || !strings.Contains(notifications.requests[0].Content, "Preset:** Rejected") {
		t.Fatalf("notifications = %#v", notifications.requests)
	}
	rejected, err := repository.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.PresetArtifactStatus != domain.ArtifactRejected || rejected.PresetArtifactIssue == "" || rejected.LifecycleState != domain.StateDraft {
		t.Fatalf("rejected session = %#v; want recoverable rejected draft", rejected)
	}
	events := repository.Events("session-1")
	if len(events) != 2 || events[1].Type != domain.EventArtifactRejected {
		t.Fatalf("events = %#v; want ArtifactRejected", events)
	}
}

func TestProcessAcceptsPresetWithRepeatedWorkshopReferences(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 21, 0, 0, 0, time.UTC)
	repository := seededRepository(t, now)
	downloader := &testDownloader{body: []byte(`<html><a href="https://steamcommunity.com/sharedfiles/filedetails/?id=450814997" data-publishedfileid="450814997">mod</a></html>`)}
	objects := &testObjectStore{}
	notifications := &testNotifications{}
	service, err := NewService(repository, downloader, objects, notifications, &testIDs{ids: []string{"preset-event-1"}}, testClock{now}, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewService() returned error: %v", err)
	}
	request := missionRequest(now, int64(len(downloader.body)))
	request.Kind = domain.ArtifactPreset
	request.Filename = "preset.html"

	if err := service.Process(context.Background(), request); err != nil {
		t.Fatalf("Process() returned error: %v", err)
	}
	if len(objects.objects) != 2 || !strings.HasPrefix(objects.objects[0].key, "sessions/session-1/input/presets/") ||
		!strings.HasPrefix(objects.objects[1].key, "sessions/session-1/input/modlists/") ||
		strings.Contains(string(objects.objects[1].contents), "data-publishedfileid") {
		t.Fatalf("stored objects = %#v; want source preset and sanitized modlist", objects.objects)
	}
	session, err := repository.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("Get() returned error: %v", err)
	}
	if session.PresetObjectKey != objects.objects[0].key {
		t.Fatalf("preset object key = %q; want %q", session.PresetObjectKey, objects.objects[0].key)
	}
	if len(notifications.requests) != 1 || notifications.requests[0].Kind != domain.NotificationSessionModlist ||
		notifications.requests[0].Attachment == nil || notifications.requests[0].Attachment.ObjectKey != objects.objects[1].key ||
		notifications.requests[0].Attachment.Filename != "saturday-arma-modlist.html" {
		t.Fatalf("modlist notification = %#v", notifications.requests)
	}
	if err := service.Process(context.Background(), request); err != nil {
		t.Fatalf("replayed Process() returned error: %v", err)
	}
	if len(objects.objects) != 3 || objects.objects[1].key != objects.objects[2].key ||
		len(notifications.requests) != 2 || notifications.requests[0].NotificationID != notifications.requests[1].NotificationID {
		t.Fatalf("replay objects=%#v notifications=%#v; want durable modlist repair with deterministic delivery", objects.objects, notifications.requests)
	}
}

func TestProcessStagesRunningPresetRevisionWithoutPromotingModlist(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 22, 0, 0, 0, time.UTC)
	repository := seededPresetRevisionRepository(t, now)
	body := []byte(`<html><a href="https://steamcommunity.com/sharedfiles/filedetails/?id=450814997">Mod</a></html>`)
	downloader := &testDownloader{body: body}
	objects, notifications := &testObjectStore{}, &testNotifications{}
	service, err := NewService(repository, downloader, objects, notifications, &testIDs{ids: []string{"revision-event-2"}}, testClock{now.Add(time.Minute)}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := missionRequest(now, int64(len(body)))
	request.Kind, request.Filename, request.AttachmentID = domain.ArtifactPreset, "revision.html", "revision-attachment"
	request.Purpose, request.ExpectedActivePresetRevision = domain.ArtifactPurposePresetRevision, 1
	request.ChannelID = "channel-other"
	if err := service.Process(context.Background(), request); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("cross-channel worker error = %v; want forbidden", err)
	}
	request.ChannelID = "channel-1"
	if err := service.Process(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.Get(context.Background(), request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LifecycleState != domain.StateRunning || stored.PresetObjectKey != stored.ActivePresetRevision.PresetObjectKey || stored.PendingPresetRevision.Number != 2 || stored.PendingPresetRevision.Status != domain.PresetRevisionPending {
		t.Fatalf("staged session = %#v", stored)
	}
	if len(notifications.requests) != 1 || notifications.requests[0].Kind != domain.NotificationSessionCard {
		t.Fatalf("pending revision notifications = %#v; active modlist must not be promoted", notifications.requests)
	}
	events := repository.Events(request.SessionID)
	if events[len(events)-1].Type != domain.EventPresetRevisionStaged {
		t.Fatalf("last event = %#v", events[len(events)-1])
	}
}

func TestProcessRejectsInvalidRunningPresetRevisionWithoutChangingActive(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 22, 0, 0, 0, time.UTC)
	repository := seededPresetRevisionRepository(t, now)
	body := []byte("not html")
	downloader := &testDownloader{body: body}
	objects, notifications := &testObjectStore{}, &testNotifications{}
	service, err := NewService(repository, downloader, objects, notifications, &testIDs{ids: []string{"revision-rejected"}}, testClock{now.Add(time.Minute)}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := missionRequest(now, int64(len(body)))
	request.Kind, request.Filename, request.AttachmentID = domain.ArtifactPreset, "revision.html", "revision-invalid"
	request.Purpose, request.ExpectedActivePresetRevision = domain.ArtifactPurposePresetRevision, 1
	if err := service.Process(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.Get(context.Background(), request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LifecycleState != domain.StateRunning || stored.ActivePresetRevision.Number != 1 || !stored.PendingPresetRevision.Empty() || stored.PresetArtifactStatus != domain.ArtifactAccepted {
		t.Fatalf("invalid revision changed active session = %#v", stored)
	}
	if len(objects.objects) != 0 {
		t.Fatalf("invalid revision wrote objects: %#v", objects.objects)
	}
}

func seededPresetRevisionRepository(t *testing.T, now time.Time) *memory.SessionRepository {
	t.Helper()
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "saturday-arma", DisplayName: "Saturday Arma", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState = domain.StateRunning, domain.StateRunning, domain.StateRunning
	session.PresetObjectKey = "sessions/session-1/input/presets/v1.html"
	session.PresetArtifactStatus = domain.ArtifactAccepted
	session.PresetRevisionSequence = 1
	session.ActivePresetRevision = domain.PresetRevision{Number: 1, PresetObjectKey: session.PresetObjectKey, Status: domain.PresetRevisionActive, StagedAt: now, ActivatedAt: now}
	actor := domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}
	event := domain.NewSessionCreatedEvent("create-event", "correlation-create", actor, session, now)
	record, err := domain.NewCompletedIdempotencyRecord("seed:revision", "seed-hash", session.ID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, record); err != nil {
		t.Fatal(err)
	}
	return repository
}

func TestProcessVanillaPresetDoesNotPublishActiveModlist(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 13, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "session-vanilla", Slug: "vanilla", DisplayName: "Vanilla", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.Vanilla = true
	event := domain.NewSessionCreatedEvent("create-vanilla", "correlation-vanilla", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now)
	record, err := domain.NewCompletedIdempotencyRecord("create-vanilla", "hash-vanilla", session.ID, now, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, record); err != nil {
		t.Fatal(err)
	}
	body := []byte(`<html><a href="https://steamcommunity.com/sharedfiles/filedetails/?id=450814997">mod</a></html>`)
	downloader := &testDownloader{body: body}
	objects := &testObjectStore{}
	notifications := &testNotifications{}
	service, err := NewService(repository, downloader, objects, notifications, &testIDs{ids: []string{"preset-vanilla"}}, testClock{now}, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := missionRequest(now, int64(len(body)))
	request.SessionID, request.Kind, request.Filename = session.ID, domain.ArtifactPreset, "preset.html"
	if err := service.Process(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(objects.objects) != 1 || !strings.Contains(objects.objects[0].key, "/presets/") {
		t.Fatalf("vanilla objects = %#v; want only validated source preset", objects.objects)
	}
	if len(notifications.requests) != 1 || notifications.requests[0].Kind != domain.NotificationSessionCard || notifications.requests[0].Attachment != nil {
		t.Fatalf("vanilla notifications = %#v; want card without active modlist", notifications.requests)
	}
}

func TestProcessKeepsPartiallySuccessfulModdedDraftRecoverable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 22, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "session-partial", Slug: "partial", DisplayName: "Partial", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Configure(domain.SessionConfiguration{
		GameProfileID: "arma3-default", SleepAfterSeconds: 1800, ArchiveAfterSeconds: 7 * 86400,
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := session.PrepareCreationArtifacts(true, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	actor := domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}
	event := domain.NewSessionCreatedEvent("create-partial", "correlation-partial", actor, session, now)
	record, err := domain.NewCompletedIdempotencyRecord("create-partial", "hash-partial", session.ID, now, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, event, record); err != nil {
		t.Fatal(err)
	}

	downloader := &testDownloader{body: []byte("0123456789abcdef")}
	objects := &testObjectStore{}
	notifications := &testNotifications{}
	service, err := NewService(repository, downloader, objects, notifications, &testIDs{ids: []string{"mission-accepted", "preset-rejected"}}, testClock{now}, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mission := missionRequest(now, int64(len(downloader.body)))
	mission.SessionID = session.ID
	if err := service.Process(context.Background(), mission); err != nil {
		t.Fatalf("mission Process() returned error: %v", err)
	}

	downloader.body = []byte("not an html preset")
	preset := missionRequest(now, int64(len(downloader.body)))
	preset.SessionID = session.ID
	preset.Kind = domain.ArtifactPreset
	preset.AttachmentID = "attachment-preset"
	preset.Filename = "preset.html"
	preset.IdempotencyKey = "discord:preset-partial"
	if err := service.Process(context.Background(), preset); err != nil {
		t.Fatalf("preset Process() returned error: %v", err)
	}

	stored, err := repository.Get(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LifecycleState != domain.StateDraft || stored.MissionArtifactStatus != domain.ArtifactAccepted ||
		stored.PresetArtifactStatus != domain.ArtifactRejected || stored.MissionObjectKey == "" ||
		stored.PresetObjectKey != "" || stored.PresetArtifactIssue == "" {
		t.Fatalf("partially processed session = %#v; want accepted mission and recoverable rejected preset", stored)
	}
	if len(notifications.requests) != 2 ||
		!strings.Contains(notifications.requests[1].Content, "Mission:** Accepted") ||
		!strings.Contains(notifications.requests[1].Content, "Preset:** Rejected") {
		t.Fatalf("partial-failure card refreshes = %#v", notifications.requests)
	}
}

func seededRepository(t *testing.T, now time.Time) *memory.SessionRepository {
	t.Helper()
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{
		ID: "session-1", Slug: "saturday-arma", DisplayName: "Saturday Arma", GameType: "arma3",
		OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatalf("NewSession() returned error: %v", err)
	}
	actor := domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}
	event := domain.NewSessionCreatedEvent("create-event", "correlation-create", actor, session, now)
	idempotency, err := domain.NewCompletedIdempotencyRecord("discord:create", "create-hash", session.ID, now, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewCompletedIdempotencyRecord() returned error: %v", err)
	}
	if err := repository.Create(context.Background(), session, event, idempotency); err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	return repository
}

func missionRequest(now time.Time, size int64) domain.ArtifactIngestRequest {
	return domain.ArtifactIngestRequest{
		SchemaVersion: 1, SessionID: "session-1", Kind: domain.ArtifactMission,
		AttachmentID: "attachment-1", Filename: "operation.pbo", ContentType: "application/octet-stream",
		SizeBytes: size, SourceURL: "https://cdn.discordapp.com/attachments/1/2/operation.pbo",
		ActorID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
		CorrelationID: "correlation-artifact", IdempotencyKey: "discord:artifact-1", RequestedAt: now,
	}
}
