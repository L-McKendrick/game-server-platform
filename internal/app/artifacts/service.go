package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

var workshopIDPattern = regexp.MustCompile(`(?i)(?:[?&]id=|data-publishedfileid=["'])([0-9]{6,20})`)

type Clock interface{ Now() time.Time }
type IDGenerator interface {
	New(time.Time) (string, error)
}

type Service struct {
	repository    ports.SessionRepository
	downloader    ports.ArtifactDownloader
	objects       ports.ObjectStore
	notifications ports.NotificationQueue
	ids           IDGenerator
	clock         Clock
	retention     time.Duration
}

func NewService(
	repository ports.SessionRepository,
	downloader ports.ArtifactDownloader,
	objects ports.ObjectStore,
	notifications ports.NotificationQueue,
	ids IDGenerator,
	clock Clock,
	retention time.Duration,
) (*Service, error) {
	if repository == nil || downloader == nil || objects == nil || notifications == nil || ids == nil || clock == nil {
		return nil, fmt.Errorf("artifact service dependencies are required")
	}
	if retention <= 0 {
		return nil, fmt.Errorf("idempotency retention must be positive")
	}
	return &Service{repository, downloader, objects, notifications, ids, clock, retention}, nil
}

func (service *Service) Process(ctx context.Context, request domain.ArtifactIngestRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("validate ingest request: %w", err)
	}
	session, err := service.repository.Get(ctx, request.SessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if session.OwnerDiscordUserID != request.ActorID || session.GuildID != request.GuildID {
		return fmt.Errorf("artifact requester does not own session: %w", domain.ErrForbidden)
	}

	body, err := service.downloader.Download(ctx, request)
	if err != nil {
		return fmt.Errorf("download Discord attachment: %w", err)
	}
	if err := validateContent(request, body); err != nil {
		return service.reject(ctx, session, request, err)
	}

	digest := sha256.Sum256(body)
	digestHex := hex.EncodeToString(digest[:])
	if replayed, replayErr := service.replayed(ctx, artifactIdempotencyKey(request), digestHex); replayErr != nil {
		return replayErr
	} else if replayed {
		return service.notify(ctx, session, request)
	}
	directory := "missions"
	if request.Kind == domain.ArtifactPreset {
		directory = "presets"
	}
	objectKey := path.Join(
		"sessions",
		request.SessionID,
		"input",
		directory,
		digestHex+"-"+request.Filename,
	)
	if err := service.objects.Put(
		ctx,
		objectKey,
		request.ContentType,
		body,
		base64.StdEncoding.EncodeToString(digest[:]),
	); err != nil {
		return fmt.Errorf("store validated artifact: %w", err)
	}

	now := service.clock.Now().UTC()
	expectedVersion := session.Version
	if err := session.AttachArtifact(request.Kind, objectKey, now); err != nil {
		return fmt.Errorf("attach artifact metadata: %w", err)
	}
	eventID, err := service.ids.New(now)
	if err != nil {
		return fmt.Errorf("generate artifact event ID: %w", err)
	}
	event := domain.NewArtifactEvent(
		eventID,
		domain.EventArtifactValidated,
		request.CorrelationID,
		domain.Actor{Type: domain.ActorTypeDiscordUser, ID: request.ActorID},
		session,
		request.Kind,
		objectKey,
		now,
	)
	idempotency, err := domain.NewCompletedIdempotencyRecord(
		artifactIdempotencyKey(request), digestHex, session.ID, now, service.retention,
	)
	if err != nil {
		return fmt.Errorf("create artifact idempotency: %w", err)
	}
	if err := service.repository.SaveWithEvent(ctx, session, expectedVersion, event, idempotency); err != nil {
		replayed, replayErr := service.replayed(ctx, idempotency.Key, digestHex)
		if replayErr != nil {
			return replayErr
		}
		if replayed {
			persisted, getErr := service.repository.Get(ctx, request.SessionID)
			if getErr != nil {
				return getErr
			}
			return service.notify(ctx, persisted, request)
		}
		return fmt.Errorf("persist artifact metadata: %w", err)
	}
	return service.notify(ctx, session, request)
}

func (service *Service) reject(
	ctx context.Context,
	session domain.Session,
	request domain.ArtifactIngestRequest,
	reason error,
) error {
	now := service.clock.Now().UTC()
	hash := sha256.Sum256([]byte(reason.Error()))
	requestHash := hex.EncodeToString(hash[:])
	if replayed, replayErr := service.replayed(ctx, artifactIdempotencyKey(request), requestHash); replayErr != nil {
		return replayErr
	} else if replayed {
		return service.notify(ctx, session, request)
	}
	expectedVersion := session.Version
	if err := session.RejectArtifact(request.Kind, reason.Error(), now); err != nil {
		return err
	}
	eventID, err := service.ids.New(now)
	if err != nil {
		return err
	}
	event := domain.NewArtifactEvent(
		eventID, domain.EventArtifactRejected, request.CorrelationID,
		domain.Actor{Type: domain.ActorTypeDiscordUser, ID: request.ActorID},
		session, request.Kind, "", now,
	)
	event.Data["reason"] = reason.Error()
	idempotency, err := domain.NewCompletedIdempotencyRecord(
		artifactIdempotencyKey(request), requestHash, session.ID, now, service.retention,
	)
	if err != nil {
		return err
	}
	if err := service.repository.SaveWithEvent(ctx, session, expectedVersion, event, idempotency); err != nil {
		replayed, replayErr := service.replayed(ctx, idempotency.Key, requestHash)
		if replayErr != nil {
			return replayErr
		}
		if replayed {
			persisted, getErr := service.repository.Get(ctx, request.SessionID)
			if getErr != nil {
				return getErr
			}
			return service.notify(ctx, persisted, request)
		}
		return fmt.Errorf("persist artifact rejection: %w", err)
	}
	return service.notify(ctx, session, request)
}

func (service *Service) notify(ctx context.Context, session domain.Session, request domain.ArtifactIngestRequest) error {
	now := service.clock.Now().UTC()
	notificationID := "card-artifact-" + strings.ToLower(string(request.Kind)) + "-" + request.AttachmentID
	return service.notifications.Enqueue(ctx, domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: notificationID, SessionID: request.SessionID,
		GuildID: request.GuildID, ChannelID: request.ChannelID,
		Content: sessioncard.RenderSetup(session, now), Kind: domain.NotificationSessionCard,
		CorrelationID: request.CorrelationID, RequestedAt: now,
	})
}

func (service *Service) replayed(ctx context.Context, key string, requestHash string) (bool, error) {
	record, err := service.repository.GetIdempotency(ctx, key)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if record.RequestHash != requestHash {
		return true, domain.ErrIdempotencyConflict
	}
	return true, nil
}

func artifactIdempotencyKey(request domain.ArtifactIngestRequest) string {
	return "artifact:" + strings.ToLower(string(request.Kind)) + ":" + request.AttachmentID
}

func validateContent(request domain.ArtifactIngestRequest, body []byte) error {
	if len(body) == 0 {
		return fmt.Errorf("file is empty")
	}
	if int64(len(body)) != request.SizeBytes {
		return fmt.Errorf("downloaded size does not match Discord metadata")
	}
	if request.Kind == domain.ArtifactMission {
		if len(body) < 16 {
			return fmt.Errorf("mission file is too small to be a valid PBO")
		}
		return nil
	}

	text := string(body)
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "<html") && !strings.Contains(lower, "<!doctype html") {
		return fmt.Errorf("launcher preset must be an HTML document")
	}
	if strings.Contains(lower, "file://") || strings.Contains(lower, "local mod") {
		return fmt.Errorf("launcher preset contains an unsupported local-mod path")
	}
	matches := workshopIDPattern.FindAllStringSubmatch(text, -1)
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		seen[match[1]] = struct{}{}
	}
	if len(seen) > 250 {
		return fmt.Errorf("launcher preset references more than 250 Workshop items")
	}
	return nil
}
