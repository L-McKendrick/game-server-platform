package sessions

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type LifecycleTimeoutDefaultsCommand struct {
	Actor                                          domain.Actor
	GuildID, CorrelationID, IdempotencyKey, Reason string
	IsAdministrator                                bool
	SleepAfterSeconds, ArchiveAfterSeconds         int64
}

type LifecycleTimeoutExtensionCommand struct {
	Actor                                                     domain.Actor
	GuildID, SessionID, CorrelationID, IdempotencyKey, Reason string
	IsAdministrator                                           bool
	SleepExtensionSeconds, ArchiveExtensionSeconds            int64
}

func validateTimeoutAdmin(actor domain.Actor, administrator bool, correlationID, idempotencyKey, reason string) error {
	if err := actor.Validate(); err != nil || actor.Type != domain.ActorTypeDiscordUser || !administrator {
		return domain.ErrForbidden
	}
	if strings.TrimSpace(correlationID) == "" || strings.TrimSpace(idempotencyKey) == "" || strings.TrimSpace(reason) == "" || len([]rune(reason)) > 200 {
		return fmt.Errorf("timeout request requires an idempotency key, correlation ID, and reason of at most 200 characters")
	}
	return nil
}

func (service *Service) ConfigureLifecycleTimeoutDefaults(ctx context.Context, command LifecycleTimeoutDefaultsCommand) (domain.GuildLifecycleTimeoutPolicy, error) {
	if err := validateTimeoutAdmin(command.Actor, command.IsAdministrator, command.CorrelationID, command.IdempotencyKey, command.Reason); err != nil {
		return domain.GuildLifecycleTimeoutPolicy{}, err
	}
	if err := domain.ValidateLifecycleTimeouts(command.SleepAfterSeconds, command.ArchiveAfterSeconds); err != nil {
		return domain.GuildLifecycleTimeoutPolicy{}, err
	}
	repository, ok := service.repository.(ports.LifecycleTimeoutPolicyRepository)
	if !ok {
		return domain.GuildLifecycleTimeoutPolicy{}, domain.ErrFeatureDisabled
	}
	hash, err := hashRequest(command)
	if err != nil {
		return domain.GuildLifecycleTimeoutPolicy{}, err
	}
	if record, getErr := service.repository.GetIdempotency(ctx, command.IdempotencyKey); getErr == nil {
		if record.RequestHash != hash {
			return domain.GuildLifecycleTimeoutPolicy{}, domain.ErrIdempotencyConflict
		}
		return service.LifecycleTimeoutDefaults(ctx, command.GuildID)
	} else if !errors.Is(getErr, domain.ErrNotFound) {
		return domain.GuildLifecycleTimeoutPolicy{}, getErr
	}
	previous, err := service.LifecycleTimeoutDefaults(ctx, command.GuildID)
	if err != nil {
		return domain.GuildLifecycleTimeoutPolicy{}, err
	}
	now := service.clock.Now().UTC()
	policy := domain.GuildLifecycleTimeoutPolicy{GuildID: strings.TrimSpace(command.GuildID), SleepAfterSeconds: command.SleepAfterSeconds, ArchiveAfterSeconds: command.ArchiveAfterSeconds, Version: previous.Version + 1, UpdatedBy: command.Actor.ID, UpdatedAt: now}
	auditID, err := service.ids.New(now)
	if err != nil {
		return domain.GuildLifecycleTimeoutPolicy{}, err
	}
	audit := domain.LifecycleTimeoutPolicyAudit{ID: auditID, GuildID: policy.GuildID, ActorID: command.Actor.ID, CorrelationID: command.CorrelationID, Reason: strings.TrimSpace(command.Reason), PreviousSleepAfterSeconds: previous.SleepAfterSeconds, PreviousArchiveAfterSeconds: previous.ArchiveAfterSeconds, SleepAfterSeconds: policy.SleepAfterSeconds, ArchiveAfterSeconds: policy.ArchiveAfterSeconds, OccurredAt: now}
	record, err := domain.NewCompletedIdempotencyRecord(command.IdempotencyKey, hash, policy.GuildID, now, service.idempotencyRetention)
	if err != nil {
		return domain.GuildLifecycleTimeoutPolicy{}, err
	}
	if err := repository.SaveLifecycleTimeoutPolicy(ctx, policy, previous.Version, audit, record); err != nil {
		if replay, replayErr := service.repository.GetIdempotency(ctx, command.IdempotencyKey); replayErr == nil && replay.RequestHash == hash {
			return service.LifecycleTimeoutDefaults(ctx, command.GuildID)
		}
		return domain.GuildLifecycleTimeoutPolicy{}, err
	}
	return policy, nil
}

func (service *Service) ExtendLifecycleTimeouts(ctx context.Context, command LifecycleTimeoutExtensionCommand) (domain.Session, error) {
	if err := validateTimeoutAdmin(command.Actor, command.IsAdministrator, command.CorrelationID, command.IdempotencyKey, command.Reason); err != nil {
		return domain.Session{}, err
	}
	if command.SleepExtensionSeconds <= 0 || command.ArchiveExtensionSeconds <= 0 {
		return domain.Session{}, fmt.Errorf("both timeout additions must be positive")
	}
	hash, err := hashRequest(command)
	if err != nil {
		return domain.Session{}, err
	}
	if record, getErr := service.repository.GetIdempotency(ctx, command.IdempotencyKey); getErr == nil {
		if record.RequestHash != hash {
			return domain.Session{}, domain.ErrIdempotencyConflict
		}
		return service.repository.Get(ctx, record.ResultReference)
	} else if !errors.Is(getErr, domain.ErrNotFound) {
		return domain.Session{}, getErr
	}
	session, err := service.repository.Get(ctx, command.SessionID)
	if err != nil {
		return domain.Session{}, err
	}
	if session.GuildID != command.GuildID {
		return domain.Session{}, domain.ErrForbidden
	}
	if session.LifecycleState != domain.StateRunning && session.LifecycleState != domain.StateIdle {
		return domain.Session{}, fmt.Errorf("only running or idle sessions can be extended: %w", domain.ErrInvalidTransition)
	}
	previousSleep, previousArchive := session.SleepAfterSeconds, session.ArchiveAfterSeconds
	newSleep, newArchive := previousSleep+command.SleepExtensionSeconds, previousArchive+command.ArchiveExtensionSeconds
	if newSleep < previousSleep || newArchive < previousArchive {
		return domain.Session{}, fmt.Errorf("timeout addition overflow")
	}
	if err := domain.ValidateLifecycleTimeouts(newSleep, newArchive); err != nil {
		return domain.Session{}, err
	}
	now := service.clock.Now().UTC()
	expected := session.Version
	session.SleepAfterSeconds, session.ArchiveAfterSeconds = newSleep, newArchive
	session.Version++
	session.UpdatedAt = now
	eventID, err := service.ids.New(now)
	if err != nil {
		return domain.Session{}, err
	}
	event := domain.SessionEvent{ID: eventID, SessionID: session.ID, Type: domain.EventLifecycleTimeoutsExtended, OccurredAt: now, ActorType: string(command.Actor.Type), ActorID: command.Actor.ID, CorrelationID: command.CorrelationID, Data: map[string]string{
		"reason": strings.TrimSpace(command.Reason), "request_id": command.IdempotencyKey,
		"previous_sleep_after_seconds": strconv.FormatInt(previousSleep, 10), "sleep_after_seconds": strconv.FormatInt(newSleep, 10),
		"previous_archive_after_seconds": strconv.FormatInt(previousArchive, 10), "archive_after_seconds": strconv.FormatInt(newArchive, 10),
	}}
	record, err := domain.NewCompletedIdempotencyRecord(command.IdempotencyKey, hash, session.ID, now, service.idempotencyRetention)
	if err != nil {
		return domain.Session{}, err
	}
	if err := service.repository.SaveWithEvent(ctx, session, expected, event, record); err != nil {
		if replay, replayErr := service.repository.GetIdempotency(ctx, command.IdempotencyKey); replayErr == nil && replay.RequestHash == hash {
			return service.repository.Get(ctx, replay.ResultReference)
		}
		return domain.Session{}, err
	}
	return session, nil
}
