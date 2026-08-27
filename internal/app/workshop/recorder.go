package workshop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type IDGenerator interface {
	New(time.Time) (string, error)
}

type Recorder struct {
	repository ports.SessionRepository
	ids        IDGenerator
	clock      Clock
	retention  time.Duration
}

func NewRecorder(repository ports.SessionRepository, ids IDGenerator, clock Clock, retention time.Duration) (*Recorder, error) {
	if repository == nil || ids == nil || clock == nil || retention <= 0 {
		return nil, fmt.Errorf("Workshop recorder dependencies are required")
	}
	return &Recorder{repository: repository, ids: ids, clock: clock, retention: retention}, nil
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
