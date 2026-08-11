package artifacts

import (
	"context"
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
	if len(objects.objects) != 1 || !strings.HasPrefix(objects.objects[0].key, "sessions/session-1/input/missions/") {
		t.Fatalf("stored objects = %#v; want one mission object", objects.objects)
	}
	session, err := repository.Get(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("Get() returned error: %v", err)
	}
	if session.MissionObjectKey != objects.objects[0].key || session.Version != 2 {
		t.Fatalf("persisted session = %#v", session)
	}
	if len(notifications.requests) != 1 || notifications.requests[0].NotificationID != "artifact-mission-attachment-1" {
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
	if len(notifications.requests) != 1 || !strings.Contains(notifications.requests[0].Content, "was rejected") {
		t.Fatalf("notifications = %#v", notifications.requests)
	}
	events := repository.Events("session-1")
	if len(events) != 2 || events[1].Type != domain.EventArtifactRejected {
		t.Fatalf("events = %#v; want ArtifactRejected", events)
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
