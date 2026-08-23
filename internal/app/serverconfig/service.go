package serverconfig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type Clock interface{ Now() time.Time }

type Service struct {
	repository ports.GuildServerConfigRepository
	queue      ports.ArtifactQueue
	clock      Clock
}

func NewService(repository ports.GuildServerConfigRepository, queue ports.ArtifactQueue, clock Clock) (*Service, error) {
	if repository == nil || queue == nil || clock == nil {
		return nil, fmt.Errorf("server configuration service dependencies are required")
	}
	return &Service{repository: repository, queue: queue, clock: clock}, nil
}

func (service *Service) Current(ctx context.Context, guildID string, isAdministrator bool) (domain.GuildServerConfig, bool, error) {
	if !isAdministrator {
		return domain.GuildServerConfig{}, false, domain.ErrForbidden
	}
	config, err := service.repository.GetGuildServerConfig(ctx, strings.TrimSpace(guildID))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.GuildServerConfig{}, false, nil
	}
	return config, err == nil, err
}

func (service *Service) RequestUpload(ctx context.Context, request domain.ArtifactIngestRequest, isAdministrator bool) error {
	if !isAdministrator {
		return domain.ErrForbidden
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if !request.IsServerConfig() {
		return fmt.Errorf("server configuration upload purpose is required")
	}
	current, found, err := service.Current(ctx, request.GuildID, true)
	if err != nil {
		return err
	}
	revision := int64(0)
	if found {
		revision = current.Revision
	}
	if revision != request.ExpectedServerConfigRevision {
		return domain.ErrConflict
	}
	return service.queue.Enqueue(ctx, request)
}

func (service *Service) Remove(ctx context.Context, guildID, actorID string, expectedRevision int64, isAdministrator bool) (domain.GuildServerConfig, error) {
	if !isAdministrator {
		return domain.GuildServerConfig{}, domain.ErrForbidden
	}
	if strings.TrimSpace(actorID) == "" {
		return domain.GuildServerConfig{}, fmt.Errorf("Administrator actor ID is required")
	}
	current, err := service.repository.GetGuildServerConfig(ctx, strings.TrimSpace(guildID))
	if err != nil {
		return domain.GuildServerConfig{}, err
	}
	if current.Revision == expectedRevision+1 && !current.Active() {
		return current, nil
	}
	if current.Revision != expectedRevision || !current.Active() {
		return domain.GuildServerConfig{}, domain.ErrConflict
	}
	removed := domain.GuildServerConfig{GuildID: current.GuildID, Revision: current.Revision + 1, UpdatedAt: service.clock.Now().UTC()}
	return service.repository.SaveGuildServerConfig(ctx, removed, expectedRevision)
}

type Processor struct {
	repository ports.GuildServerConfigRepository
	downloader ports.ArtifactDownloader
	objects    ports.PrivateObjectStore
	clock      Clock
}

func NewProcessor(repository ports.GuildServerConfigRepository, downloader ports.ArtifactDownloader, objects ports.PrivateObjectStore, clock Clock) (*Processor, error) {
	if repository == nil || downloader == nil || objects == nil || clock == nil {
		return nil, fmt.Errorf("server configuration processor dependencies are required")
	}
	return &Processor{repository: repository, downloader: downloader, objects: objects, clock: clock}, nil
}

func (processor *Processor) Process(ctx context.Context, request domain.ArtifactIngestRequest) error {
	if err := request.Validate(); err != nil || !request.IsServerConfig() {
		return fmt.Errorf("%w: server configuration request is invalid", domain.ErrPermanentArtifactRejection)
	}
	if strings.ContainsAny(request.GuildID, `/\\`) || strings.Contains(request.GuildID, "..") {
		return fmt.Errorf("%w: guild scope is invalid", domain.ErrPermanentArtifactRejection)
	}
	current, err := processor.repository.GetGuildServerConfig(ctx, request.GuildID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	currentRevision := int64(0)
	if err == nil {
		currentRevision = current.Revision
	}
	if currentRevision != request.ExpectedServerConfigRevision && currentRevision != request.ExpectedServerConfigRevision+1 {
		return domain.ErrConflict
	}
	body, err := processor.downloader.Download(ctx, request)
	if err != nil {
		// Discord attachment URLs contain expiring credentials. Do not wrap the
		// transport error because it may reproduce the full signed URL in logs.
		return fmt.Errorf("download private server configuration: transient attachment failure")
	}
	if int64(len(body)) != request.SizeBytes || len(body) == 0 || !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
		return fmt.Errorf("%w: server configuration must be non-empty UTF-8 text matching its declared size", domain.ErrPermanentArtifactRejection)
	}
	digest := sha256.Sum256(body)
	digestHex := hex.EncodeToString(digest[:])
	revision := request.ExpectedServerConfigRevision + 1
	objectKey := path.Join("guilds", request.GuildID, "server-config", "revisions", fmt.Sprintf("%06d-%s", revision, digestHex), "server.cfg")
	if currentRevision == revision {
		if current.Active() && current.SHA256 == digestHex && current.ObjectKey == objectKey {
			return nil
		}
		if err := processor.objects.Delete(ctx, objectKey); err != nil {
			return fmt.Errorf("remove superseded private server configuration: %w", err)
		}
		return domain.ErrConflict
	}
	if err := processor.objects.Put(ctx, objectKey, "text/plain; charset=utf-8", body, base64.StdEncoding.EncodeToString(digest[:])); err != nil {
		return fmt.Errorf("store private server configuration: %w", err)
	}
	config := domain.GuildServerConfig{
		GuildID: request.GuildID, Revision: revision, ObjectKey: objectKey, Filename: request.Filename,
		SHA256: digestHex, SizeBytes: int64(len(body)), UploadedBy: request.ActorID, UpdatedAt: processor.clock.Now().UTC(),
	}
	_, err = processor.repository.SaveGuildServerConfig(ctx, config, request.ExpectedServerConfigRevision)
	if errors.Is(err, domain.ErrConflict) {
		if deleteErr := processor.objects.Delete(ctx, objectKey); deleteErr != nil {
			return fmt.Errorf("remove superseded private server configuration: %w", deleteErr)
		}
	}
	return err
}
