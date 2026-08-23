package serverconfig

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func configRequest(now time.Time, expected int64) domain.ArtifactIngestRequest {
	return domain.ArtifactIngestRequest{
		SchemaVersion: 1, Kind: domain.ArtifactServerConfig, AttachmentID: "attachment-1", Filename: "server.cfg",
		ContentType: "text/plain", SizeBytes: 25, SourceURL: "https://cdn.discordapp.com/attachments/1/2/server.cfg",
		ActorID: "admin-1", GuildID: "guild-1", ChannelID: "channel-1", CorrelationID: "correlation-1",
		IdempotencyKey: "discord:upload-1:server-config", RequestedAt: now,
		Purpose: domain.ArtifactPurposeServerConfig, ExpectedServerConfigRevision: expected,
	}
}

func TestServiceRequiresAdministratorAndCurrentRevision(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	repository, queue := memory.NewSessionRepository(), memory.NewArtifactQueue()
	service, _ := NewService(repository, queue, fixedClock{now})
	request := configRequest(now, 0)
	if err := service.RequestUpload(context.Background(), request, false); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-Administrator upload error = %v", err)
	}
	if err := service.RequestUpload(context.Background(), request, true); err != nil || len(queue.Requests()) != 1 {
		t.Fatalf("upload error=%v requests=%d", err, len(queue.Requests()))
	}
	active := domain.GuildServerConfig{GuildID: "guild-1", Revision: 1, ObjectKey: "guilds/guild-1/server-config/revisions/000001-" + strings.Repeat("a", 64) + "/server.cfg", Filename: "server.cfg", SHA256: strings.Repeat("a", 64), SizeBytes: 25, UploadedBy: "admin-1", UpdatedAt: now}
	if _, err := repository.SaveGuildServerConfig(context.Background(), active, 0); err != nil {
		t.Fatal(err)
	}
	if err := service.RequestUpload(context.Background(), request, true); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale upload error = %v", err)
	}
}

func TestServiceRemovalIsConfirmedRevisionBoundAndReplaySafe(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	service, _ := NewService(repository, memory.NewArtifactQueue(), fixedClock{now: now.Add(time.Minute)})
	active := domain.GuildServerConfig{GuildID: "guild-1", Revision: 1, ObjectKey: "guilds/guild-1/server-config/revisions/000001-" + strings.Repeat("a", 64) + "/server.cfg", Filename: "server.cfg", SHA256: strings.Repeat("a", 64), SizeBytes: 25, UploadedBy: "admin-1", UpdatedAt: now}
	_, _ = repository.SaveGuildServerConfig(context.Background(), active, 0)
	for attempt := 1; attempt <= 2; attempt++ {
		removed, err := service.Remove(context.Background(), "guild-1", "admin-1", 1, true)
		if err != nil || removed.Active() || removed.Revision != 2 {
			t.Fatalf("attempt %d removed=%#v err=%v", attempt, removed, err)
		}
	}
}

type downloader struct{ body []byte }

func (downloader downloader) Download(context.Context, domain.ArtifactIngestRequest) ([]byte, error) {
	return append([]byte(nil), downloader.body...), nil
}

type objectStore struct {
	key     string
	body    []byte
	deleted []string
}

func (store *objectStore) Delete(_ context.Context, key string) error {
	store.deleted = append(store.deleted, key)
	return nil
}

func (store *objectStore) Put(_ context.Context, key, _ string, body []byte, _ string) error {
	store.key, store.body = key, append([]byte(nil), body...)
	return nil
}

func TestProcessorStoresPrivateUTF8RevisionAndReplaysExactly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	body := []byte("hostname = \"Private\";\n")
	request := configRequest(now, 0)
	request.SizeBytes = int64(len(body))
	repository, objects := memory.NewSessionRepository(), &objectStore{}
	processor, _ := NewProcessor(repository, downloader{body}, objects, fixedClock{now})
	for attempt := 1; attempt <= 2; attempt++ {
		if err := processor.Process(context.Background(), request); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	config, err := repository.GetGuildServerConfig(context.Background(), "guild-1")
	if err != nil || config.Revision != 1 || !strings.HasPrefix(config.ObjectKey, "guilds/guild-1/server-config/revisions/000001-") || string(objects.body) != string(body) {
		t.Fatalf("config=%#v key=%q body=%q err=%v", config, objects.key, objects.body, err)
	}
}

func TestProcessorRejectsNonUTF8WithoutStoring(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	body := []byte{0xff, 0xfe}
	request := configRequest(now, 0)
	request.SizeBytes = int64(len(body))
	objects := &objectStore{}
	processor, _ := NewProcessor(memory.NewSessionRepository(), downloader{body}, objects, fixedClock{now})
	if err := processor.Process(context.Background(), request); !errors.Is(err, domain.ErrPermanentArtifactRejection) || objects.key != "" {
		t.Fatalf("invalid UTF-8 err=%v key=%q", err, objects.key)
	}
}

func TestProcessorDeletesPrivateObjectThatLosesRevisionRace(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	winnerBody := []byte("hostname = \"Winner\";\n")
	winnerRequest := configRequest(now, 0)
	winnerRequest.SizeBytes = int64(len(winnerBody))
	winnerObjects := &objectStore{}
	winner, _ := NewProcessor(repository, downloader{winnerBody}, winnerObjects, fixedClock{now})
	if err := winner.Process(context.Background(), winnerRequest); err != nil {
		t.Fatal(err)
	}

	loserBody := []byte("hostname = \"Loser\";\n")
	loserRequest := configRequest(now, 0)
	loserRequest.SizeBytes = int64(len(loserBody))
	loserObjects := &objectStore{}
	loser, _ := NewProcessor(repository, downloader{loserBody}, loserObjects, fixedClock{now})
	if err := loser.Process(context.Background(), loserRequest); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("losing upload error = %v", err)
	}
	if len(loserObjects.deleted) != 1 || loserObjects.deleted[0] == winnerObjects.key {
		t.Fatalf("deleted objects = %#v; winner = %q", loserObjects.deleted, winnerObjects.key)
	}
}
