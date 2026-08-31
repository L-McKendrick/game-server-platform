package workshop

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/app/modlist"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type IDGenerator interface {
	New(time.Time) (string, error)
}

type Recorder struct {
	repository ports.SessionRepository
	objects    ports.ObjectStore
	ids        IDGenerator
	clock      Clock
	retention  time.Duration
}

type ModResolutionResult struct {
	Source   domain.WorkshopModSource
	Session  domain.Session
	Revision domain.PresetRevision
}

func NewRecorder(repository ports.SessionRepository, objects ports.ObjectStore, ids IDGenerator, clock Clock, retention time.Duration) (*Recorder, error) {
	if repository == nil || objects == nil || ids == nil || clock == nil || retention <= 0 {
		return nil, fmt.Errorf("Workshop recorder dependencies are required")
	}
	return &Recorder{repository: repository, objects: objects, ids: ids, clock: clock, retention: retention}, nil
}

func (recorder *Recorder) ClearResolution(ctx context.Context, request domain.WorkshopSourceRequest, reason string) error {
	session, err := recorder.repository.Get(ctx, request.SessionID)
	if err != nil {
		return err
	}
	expected, now := session.Version, recorder.clock.Now().UTC()
	if err := session.FinishWorkshopResolution(request.Target, request.IdempotencyKey, now); err != nil {
		return err
	}
	if session.Version == expected {
		return nil
	}
	eventID, err := recorder.ids.New(now)
	if err != nil {
		return err
	}
	event := domain.SessionEvent{ID: eventID, SessionID: session.ID, Type: domain.EventWorkshopResolutionCleared, OccurredAt: now, ActorType: string(domain.ActorTypeSystem), ActorID: "artifact-worker", CorrelationID: request.CorrelationID, Data: map[string]string{"target": string(request.Target), "reason": strings.TrimSpace(reason)}}
	record, err := domain.NewCompletedIdempotencyRecord("workshop-clear:"+request.IdempotencyKey, string(request.Target), session.ID, now, recorder.retention)
	if err != nil {
		return err
	}
	return recorder.repository.SaveWithEvent(ctx, session, expected, event, record)
}

func (recorder *Recorder) RecordModResolution(ctx context.Context, request domain.WorkshopSourceRequest, resolution domain.WorkshopResolution) (ModResolutionResult, error) {
	if err := request.Validate(); err != nil || request.Target != domain.WorkshopTargetMods {
		return ModResolutionResult{}, fmt.Errorf("%w: invalid mod request", domain.ErrPermanentWorkshopRejection)
	}
	source, err := domain.NewWorkshopModSource(resolution)
	if err != nil {
		return ModResolutionResult{}, fmt.Errorf("%w: %v", domain.ErrPermanentWorkshopRejection, err)
	}
	key, hash := "workshop-mods:"+strings.TrimSpace(request.IdempotencyKey), source.ResolutionSHA256
	if record, getErr := recorder.repository.GetIdempotency(ctx, key); getErr == nil {
		if record.RequestHash != hash {
			return ModResolutionResult{}, domain.ErrIdempotencyConflict
		}
		session, sessionErr := recorder.repository.Get(ctx, request.SessionID)
		if sessionErr != nil {
			return ModResolutionResult{}, sessionErr
		}
		return ModResolutionResult{Source: source, Session: session, Revision: workshopRevisionForSource(session, source)}, nil
	} else if !errors.Is(getErr, domain.ErrNotFound) {
		return ModResolutionResult{}, getErr
	}
	session, err := recorder.repository.Get(ctx, request.SessionID)
	if err != nil {
		return ModResolutionResult{}, err
	}
	if session.OwnerDiscordUserID != request.ActorID || session.GuildID != request.GuildID || session.ChannelID != request.ChannelID {
		return ModResolutionResult{}, domain.ErrForbidden
	}
	mods := make([]modlist.WorkshopMod, 0, len(source.AcceptedItems))
	for _, item := range source.AcceptedItems {
		mods = append(mods, modlist.WorkshopMod{ID: item.PublishedFileID, Name: item.Title})
	}
	artifact, err := modlist.GenerateWorkshop(mods, session.ID, session.DisplayName, session.Slug)
	if err != nil {
		return ModResolutionResult{}, fmt.Errorf("%w: %v", domain.ErrPermanentWorkshopRejection, err)
	}
	source.ArtifactSHA256, source.ModlistObjectKey = artifact.SHA256Hex, artifact.ObjectKey
	source.PresetObjectKey = path.Join("sessions", session.ID, "input", "presets", artifact.SHA256Hex+fmt.Sprintf("-workshop-%d.html", source.Source.PublishedFileID))
	source.ManifestObjectKey = path.Join("sessions", session.ID, "input", "workshop-sources", source.ResolutionSHA256+".json")
	expected, now := session.Version, recorder.clock.Now().UTC()
	metadata := domain.PresetModlistMetadata{ObjectKey: artifact.ObjectKey, Filename: artifact.Filename, SHA256: artifact.SHA256Hex, SizeBytes: int64(len(artifact.Body)), WorkshopCount: artifact.WorkshopCount}
	revision, err := session.AttachWorkshopModSource(source, request.ExpectedActivePresetRevision, metadata, now)
	if err != nil {
		if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrWorkflowLocked) || errors.Is(err, domain.ErrInvalidTransition) || errors.Is(err, domain.ErrWorkshopSnapshotLimit) {
			return ModResolutionResult{}, err
		}
		return ModResolutionResult{}, fmt.Errorf("%w: %v", domain.ErrPermanentWorkshopRejection, err)
	}
	if err := recorder.objects.Put(ctx, source.PresetObjectKey, artifact.ContentType, artifact.Body, artifact.SHA256Base64); err != nil {
		return ModResolutionResult{}, fmt.Errorf("store Workshop preset: %w", err)
	}
	if err := recorder.objects.Put(ctx, source.ModlistObjectKey, artifact.ContentType, artifact.Body, artifact.SHA256Base64); err != nil {
		return ModResolutionResult{}, fmt.Errorf("store Workshop modlist: %w", err)
	}
	manifest, err := json.Marshal(source)
	if err != nil {
		return ModResolutionResult{}, err
	}
	manifestDigest := sha256.Sum256(manifest)
	if err := recorder.objects.Put(ctx, source.ManifestObjectKey, "application/json", manifest, base64.StdEncoding.EncodeToString(manifestDigest[:])); err != nil {
		return ModResolutionResult{}, fmt.Errorf("store Workshop source manifest: %w", err)
	}
	eventID, err := recorder.ids.New(now)
	if err != nil {
		return ModResolutionResult{}, err
	}
	event := domain.NewWorkshopModResolvedEvent(eventID, request.CorrelationID, domain.Actor{Type: domain.ActorTypeDiscordUser, ID: request.ActorID}, session, source, now)
	event.Data["preset_revision"] = fmt.Sprintf("%d", revision.Number)
	event.Data["preset_revision_status"] = string(revision.Status)
	record, err := domain.NewCompletedIdempotencyRecord(key, hash, session.ID, now, recorder.retention)
	if err != nil {
		return ModResolutionResult{}, err
	}
	if err := recorder.repository.SaveWithEvent(ctx, session, expected, event, record); err != nil {
		if replay, replayErr := recorder.repository.GetIdempotency(ctx, key); replayErr == nil && replay.RequestHash == hash {
			persisted, getErr := recorder.repository.Get(ctx, request.SessionID)
			if getErr != nil {
				return ModResolutionResult{}, getErr
			}
			return ModResolutionResult{Source: source, Session: persisted, Revision: workshopRevisionForSource(persisted, source)}, nil
		}
		return ModResolutionResult{}, err
	}
	return ModResolutionResult{Source: source, Session: session, Revision: revision}, nil
}

func workshopRevisionForSource(session domain.Session, source domain.WorkshopModSource) domain.PresetRevision {
	for _, persisted := range session.WorkshopModSources {
		if persisted.Source == source.Source && persisted.ResolutionSHA256 == source.ResolutionSHA256 {
			source = persisted
			break
		}
	}
	if session.PendingPresetRevision.WorkshopResolutionSHA256 == source.ResolutionSHA256 {
		return session.PendingPresetRevision
	}
	if session.ActivePresetRevision.WorkshopResolutionSHA256 == source.ResolutionSHA256 {
		return session.ActivePresetRevision
	}
	return domain.PresetRevision{}
}

func (recorder *Recorder) RecordMissionResolution(ctx context.Context, request domain.WorkshopSourceRequest, resolution domain.WorkshopResolution) (domain.WorkshopMissionSource, error) {
	if err := request.Validate(); err != nil || request.Target != domain.WorkshopTargetMission {
		return domain.WorkshopMissionSource{}, fmt.Errorf("%w: invalid mission request", domain.ErrPermanentWorkshopRejection)
	}
	source, err := domain.NewWorkshopMissionSource(resolution)
	if err != nil {
		return domain.WorkshopMissionSource{}, fmt.Errorf("%w: %v", domain.ErrPermanentWorkshopRejection, err)
	}
	key := "workshop-mission:" + strings.TrimSpace(request.IdempotencyKey)
	hash := source.ResolutionSHA256
	if record, getErr := recorder.repository.GetIdempotency(ctx, key); getErr == nil {
		if record.RequestHash != hash {
			return domain.WorkshopMissionSource{}, domain.ErrIdempotencyConflict
		}
		return source, nil
	} else if !errors.Is(getErr, domain.ErrNotFound) {
		return domain.WorkshopMissionSource{}, getErr
	}
	session, err := recorder.repository.Get(ctx, request.SessionID)
	if err != nil {
		return domain.WorkshopMissionSource{}, err
	}
	if session.OwnerDiscordUserID != request.ActorID || session.GuildID != request.GuildID || session.ChannelID != request.ChannelID {
		return domain.WorkshopMissionSource{}, domain.ErrForbidden
	}
	expected := session.Version
	now := recorder.clock.Now().UTC()
	if err := session.RecordWorkshopMissionSource(source, now); err != nil {
		return domain.WorkshopMissionSource{}, fmt.Errorf("%w: %v", domain.ErrPermanentWorkshopRejection, err)
	}
	eventID, err := recorder.ids.New(now)
	if err != nil {
		return domain.WorkshopMissionSource{}, err
	}
	event := domain.NewWorkshopMissionResolvedEvent(eventID, request.CorrelationID, domain.Actor{Type: domain.ActorTypeDiscordUser, ID: request.ActorID}, session, source, now)
	record, err := domain.NewCompletedIdempotencyRecord(key, hash, session.ID, now, recorder.retention)
	if err != nil {
		return domain.WorkshopMissionSource{}, err
	}
	if err := recorder.repository.SaveWithEvent(ctx, session, expected, event, record); err != nil {
		if replay, replayErr := recorder.repository.GetIdempotency(ctx, key); replayErr == nil && replay.RequestHash == hash {
			return source, nil
		}
		return domain.WorkshopMissionSource{}, err
	}
	return source, nil
}
