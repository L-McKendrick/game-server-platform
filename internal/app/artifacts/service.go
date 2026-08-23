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

	"github.com/L-McKendrick/game-server-platform/internal/app/modlist"
	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

var workshopIDPattern = regexp.MustCompile(`(?i)(?:[?&]id=|data-publishedfileid=["'])([0-9]{6,20})`)

type Clock interface{ Now() time.Time }
type IDGenerator interface {
	New(time.Time) (string, error)
}
type AutoStarter interface {
	RequestStart(context.Context, appsession.StartCommand) error
}
type Option func(*Service)

func WithAutoStarter(starter AutoStarter) Option {
	return func(service *Service) { service.autoStarter = starter }
}

type Service struct {
	repository    ports.SessionRepository
	downloader    ports.ArtifactDownloader
	objects       ports.ObjectStore
	notifications ports.NotificationQueue
	ids           IDGenerator
	clock         Clock
	retention     time.Duration
	autoStarter   AutoStarter
}

func NewService(
	repository ports.SessionRepository,
	downloader ports.ArtifactDownloader,
	objects ports.ObjectStore,
	notifications ports.NotificationQueue,
	ids IDGenerator,
	clock Clock,
	retention time.Duration,
	options ...Option,
) (*Service, error) {
	if repository == nil || downloader == nil || objects == nil || notifications == nil || ids == nil || clock == nil {
		return nil, fmt.Errorf("artifact service dependencies are required")
	}
	if retention <= 0 {
		return nil, fmt.Errorf("idempotency retention must be positive")
	}
	service := &Service{repository: repository, downloader: downloader, objects: objects, notifications: notifications, ids: ids, clock: clock, retention: retention}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

func (service *Service) Process(ctx context.Context, request domain.ArtifactIngestRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("validate ingest request: %w", err)
	}
	session, err := service.repository.Get(ctx, request.SessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if session.OwnerDiscordUserID != request.ActorID || session.GuildID != request.GuildID || session.ChannelID != request.ChannelID {
		return fmt.Errorf("artifact requester does not own session: %w", domain.ErrForbidden)
	}

	body, err := service.downloader.Download(ctx, request)
	if err != nil {
		return fmt.Errorf("download Discord attachment: %w", err)
	}
	if err := validateContent(request, body); err != nil {
		return service.reject(ctx, session, request, err)
	}
	var publicModlist *modlist.Artifact
	if request.Kind == domain.ArtifactPreset && !session.Vanilla {
		generated, generateErr := modlist.Generate(body, session.ID, session.DisplayName, session.Slug)
		if generateErr != nil {
			return service.reject(ctx, session, request, generateErr)
		}
		publicModlist = &generated
	}

	digest := sha256.Sum256(body)
	digestHex := hex.EncodeToString(digest[:])
	requestHash := artifactRequestHash(request, digestHex)
	if replayed, replayErr := service.replayed(ctx, artifactIdempotencyKey(request), requestHash); replayErr != nil {
		return replayErr
	} else if replayed {
		if err := service.storePublicModlist(ctx, publicModlist); err != nil {
			return err
		}
		persisted, getErr := service.repository.Get(ctx, request.SessionID)
		if getErr != nil {
			return getErr
		}
		if err := service.autoStart(ctx, persisted, request); err != nil {
			return err
		}
		return service.notify(ctx, persisted, request, publicModlist)
	}
	directory := "missions"
	if request.Kind == domain.ArtifactPreset {
		directory = "presets"
	}
	objectFilename := request.Filename
	if request.Kind == domain.ArtifactMission {
		objectFilename, err = domain.NormalizeMissionFilename(request.Filename)
		if err != nil {
			return fmt.Errorf("normalize mission filename: %w", err)
		}
	} else {
		objectFilename = digestHex + "-" + request.Filename
	}
	objectKey := path.Join(
		"sessions",
		request.SessionID,
		"input",
		directory,
		objectFilename,
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
	if err := service.storePublicModlist(ctx, publicModlist); err != nil {
		return err
	}
	if request.IsPresetRevision() {
		return service.stagePresetRevision(ctx, session, request, objectKey, *publicModlist, requestHash)
	}

	now := service.clock.Now().UTC()
	expectedVersion := session.Version
	if err := session.AttachArtifact(request.Kind, objectKey, now); err != nil {
		return fmt.Errorf("attach artifact metadata: %w", err)
	}
	if request.Kind == domain.ArtifactPreset && publicModlist != nil {
		session.ActivePresetRevision.Modlist = presetModlistMetadata(*publicModlist)
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
		artifactIdempotencyKey(request), requestHash, session.ID, now, service.retention,
	)
	if err != nil {
		return fmt.Errorf("create artifact idempotency: %w", err)
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
			if err := service.autoStart(ctx, persisted, request); err != nil {
				return err
			}
			return service.notify(ctx, persisted, request, publicModlist)
		}
		return fmt.Errorf("persist artifact metadata: %w", err)
	}
	if err := service.autoStart(ctx, session, request); err != nil {
		return err
	}
	return service.notify(ctx, session, request, publicModlist)
}

func (service *Service) autoStart(ctx context.Context, session domain.Session, request domain.ArtifactIngestRequest) error {
	if service.autoStarter == nil || !session.StartWhenReady || request.IsPresetRevision() || !session.CanStartInfrastructureProvisioning() {
		return nil
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", session.ID, session.ConfigurationRevision)))
	commandID := hex.EncodeToString(digest[:16])
	if err := service.autoStarter.RequestStart(ctx, appsession.StartCommand{
		Actor:     domain.Actor{Type: domain.ActorTypeDiscordUser, ID: session.OwnerDiscordUserID},
		SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID,
		CommandID: commandID, CorrelationID: request.CorrelationID,
		IdempotencyKey: "auto-start:" + commandID,
	}); err != nil {
		return fmt.Errorf("automatically request session start: %w", err)
	}
	return nil
}

func (service *Service) reject(
	ctx context.Context,
	session domain.Session,
	request domain.ArtifactIngestRequest,
	reason error,
) error {
	if request.IsPresetRevision() {
		return service.rejectPresetRevision(ctx, session, request, reason)
	}
	now := service.clock.Now().UTC()
	hash := sha256.Sum256([]byte(reason.Error()))
	requestHash := hex.EncodeToString(hash[:])
	if replayed, replayErr := service.replayed(ctx, artifactIdempotencyKey(request), requestHash); replayErr != nil {
		return replayErr
	} else if replayed {
		return service.notify(ctx, session, request, nil)
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
	event.Data["reason"] = domain.SanitizeDiagnostic(reason.Error())
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
			return service.notify(ctx, persisted, request, nil)
		}
		return fmt.Errorf("persist artifact rejection: %w", err)
	}
	return service.notify(ctx, session, request, nil)
}

func (service *Service) stagePresetRevision(ctx context.Context, session domain.Session, request domain.ArtifactIngestRequest, presetObjectKey string, publicModlist modlist.Artifact, requestHash string) error {
	now := service.clock.Now().UTC()
	expectedVersion := session.Version
	revision, err := session.StagePresetRevision(request.ExpectedActivePresetRevision, presetObjectKey, presetModlistMetadata(publicModlist), now)
	if err != nil {
		return fmt.Errorf("stage preset revision: %w", err)
	}
	eventID, err := service.ids.New(now)
	if err != nil {
		return fmt.Errorf("generate preset revision event ID: %w", err)
	}
	event := domain.NewPresetRevisionEvent(eventID, domain.EventPresetRevisionStaged, request.CorrelationID, domain.Actor{Type: domain.ActorTypeDiscordUser, ID: request.ActorID}, session, revision, now)
	idempotency, err := domain.NewCompletedIdempotencyRecord(artifactIdempotencyKey(request), requestHash, session.ID, now, service.retention)
	if err != nil {
		return fmt.Errorf("create preset revision idempotency: %w", err)
	}
	if err := service.repository.SaveWithEvent(ctx, session, expectedVersion, event, idempotency); err != nil {
		replayed, replayErr := service.replayed(ctx, idempotency.Key, requestHash)
		if replayErr != nil {
			return replayErr
		}
		if !replayed {
			return fmt.Errorf("persist preset revision: %w", err)
		}
		persisted, getErr := service.repository.Get(ctx, request.SessionID)
		if getErr != nil {
			return getErr
		}
		return service.notify(ctx, persisted, request, nil)
	}
	return service.notify(ctx, session, request, nil)
}

func (service *Service) rejectPresetRevision(ctx context.Context, session domain.Session, request domain.ArtifactIngestRequest, reason error) error {
	now := service.clock.Now().UTC()
	hash := sha256.Sum256([]byte(reason.Error()))
	requestHash := artifactRequestHash(request, hex.EncodeToString(hash[:]))
	if replayed, replayErr := service.replayed(ctx, artifactIdempotencyKey(request), requestHash); replayErr != nil {
		return replayErr
	} else if replayed {
		return service.notify(ctx, session, request, nil)
	}
	expectedVersion := session.Version
	if err := session.RecordMutation(now); err != nil {
		return err
	}
	eventID, err := service.ids.New(now)
	if err != nil {
		return err
	}
	event := domain.NewArtifactEvent(eventID, domain.EventArtifactRejected, request.CorrelationID, domain.Actor{Type: domain.ActorTypeDiscordUser, ID: request.ActorID}, session, request.Kind, "", now)
	event.Data["reason"] = domain.SanitizeDiagnostic(reason.Error())
	event.Data["purpose"] = string(request.Purpose)
	event.Data["expected_active_preset_revision"] = fmt.Sprintf("%d", request.ExpectedActivePresetRevision)
	idempotency, err := domain.NewCompletedIdempotencyRecord(artifactIdempotencyKey(request), requestHash, session.ID, now, service.retention)
	if err != nil {
		return err
	}
	if err := service.repository.SaveWithEvent(ctx, session, expectedVersion, event, idempotency); err != nil {
		replayed, replayErr := service.replayed(ctx, idempotency.Key, requestHash)
		if replayErr != nil {
			return replayErr
		}
		if !replayed {
			return fmt.Errorf("persist preset revision rejection: %w", err)
		}
	}
	return service.notify(ctx, session, request, nil)
}

func presetModlistMetadata(artifact modlist.Artifact) domain.PresetModlistMetadata {
	return domain.PresetModlistMetadata{ObjectKey: artifact.ObjectKey, Filename: artifact.Filename, SHA256: artifact.SHA256Hex, SizeBytes: int64(len(artifact.Body)), WorkshopCount: artifact.WorkshopCount}
}

func (service *Service) storePublicModlist(ctx context.Context, artifact *modlist.Artifact) error {
	if artifact == nil {
		return nil
	}
	if err := service.objects.Put(ctx, artifact.ObjectKey, artifact.ContentType, artifact.Body, artifact.SHA256Base64); err != nil {
		return fmt.Errorf("store sanitized modlist: %w", err)
	}
	return nil
}

func (service *Service) notify(ctx context.Context, session domain.Session, request domain.ArtifactIngestRequest, publicModlist *modlist.Artifact) error {
	now := service.clock.Now().UTC()
	if request.Kind == domain.ArtifactPreset && !request.IsPresetRevision() && publicModlist != nil && !session.Vanilla && session.PresetArtifactStatus == domain.ArtifactAccepted {
		return service.notifications.Enqueue(ctx, domain.NotificationRequest{
			SchemaVersion: 1, NotificationID: "modlist-preset-" + request.AttachmentID,
			SessionID: request.SessionID, GuildID: request.GuildID, ChannelID: request.ChannelID,
			Content: sessioncard.RenderModlistMessage(session, publicModlist.Filename, publicModlist.WorkshopCount, now),
			Kind:    domain.NotificationSessionModlist,
			Attachment: &domain.NotificationAttachment{
				ObjectKey: publicModlist.ObjectKey, Filename: publicModlist.Filename, ContentType: publicModlist.ContentType,
				SHA256: publicModlist.SHA256Hex, SizeBytes: int64(len(publicModlist.Body)), Revision: session.Version,
			},
			CorrelationID: request.CorrelationID, RequestedAt: now,
		})
	}
	notificationID := "card-artifact-" + strings.ToLower(string(request.Kind)) + "-" + request.AttachmentID
	projection := sessioncard.Project(session, sessioncard.Options{Now: now})
	return service.notifications.Enqueue(ctx, domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: notificationID, SessionID: request.SessionID,
		GuildID: request.GuildID, ChannelID: request.ChannelID,
		Content: sessioncard.RenderPublic(projection), Embed: sessioncard.RenderPublicEmbed(projection), Kind: domain.NotificationSessionCard,
		CardRevision:  session.Version,
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

func artifactRequestHash(request domain.ArtifactIngestRequest, contentHash string) string {
	if !request.IsPresetRevision() {
		return contentHash
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d", contentHash, request.Purpose, request.ExpectedActivePresetRevision)))
	return hex.EncodeToString(digest[:])
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
