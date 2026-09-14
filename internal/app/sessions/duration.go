package sessions

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// DurationCommand is restricted to a signed guild Administrator at the
// interaction boundary and rechecked here before any durable mutation.
type DurationCommand struct {
	Actor                                                     domain.Actor
	GuildID, SessionID, CorrelationID, IdempotencyKey, Reason string
	IsAdministrator                                           bool
	Seconds                                                   int64
	ExtensionSeconds                                          int64
	NewDeadline                                               time.Time
}

func (service *Service) ConfigureMaximumDuration(ctx context.Context, command DurationCommand) (domain.Session, error) {
	return service.changeMaximumDuration(ctx, command, false)
}

func (service *Service) ExtendMaximumDuration(ctx context.Context, command DurationCommand) (domain.Session, error) {
	return service.changeMaximumDuration(ctx, command, true)
}

func (service *Service) changeMaximumDuration(ctx context.Context, command DurationCommand, extension bool) (domain.Session, error) {
	if err := command.Actor.Validate(); err != nil || command.Actor.Type != domain.ActorTypeDiscordUser || !command.IsAdministrator {
		return domain.Session{}, domain.ErrForbidden
	}
	if strings.TrimSpace(command.IdempotencyKey) == "" || strings.TrimSpace(command.CorrelationID) == "" || strings.TrimSpace(command.Reason) == "" || len([]rune(command.Reason)) > 200 {
		return domain.Session{}, fmt.Errorf("duration request requires an idempotency key, correlation ID, and reason of at most 200 characters")
	}
	hash, err := hashRequest(struct {
		Actor, Guild, Session, Reason string
		Seconds                       int64
		ExtensionSeconds              int64
		Deadline                      time.Time
		Extension                     bool
	}{command.Actor.ID, command.GuildID, command.SessionID, command.Reason, command.Seconds, command.ExtensionSeconds, command.NewDeadline.UTC(), extension})
	if err != nil {
		return domain.Session{}, err
	}
	if record, err := service.repository.GetIdempotency(ctx, command.IdempotencyKey); err == nil {
		if record.RequestHash != hash {
			return domain.Session{}, domain.ErrIdempotencyConflict
		}
		return service.repository.Get(ctx, record.ResultReference)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Session{}, err
	}
	session, err := service.repository.Get(ctx, command.SessionID)
	if err != nil {
		return domain.Session{}, err
	}
	if session.GuildID != command.GuildID {
		return domain.Session{}, domain.ErrForbidden
	}
	if session.LifecycleState == domain.StateDeleted {
		return domain.Session{}, domain.ErrInvalidTransition
	}
	if session.ActiveWorkflowID != "" || session.LifecycleState == domain.StateDeleting || session.LifecycleState == domain.StateArchiving || session.LifecycleState == domain.StateDestroying {
		return domain.Session{}, fmt.Errorf("a lifecycle operation is already in progress: %w", domain.ErrInvalidTransition)
	}
	now := service.clock.Now().UTC()
	previous := session.MaximumDuration.DeadlineAt
	previousSeconds := session.MaximumDuration.EffectiveSeconds()
	if extension {
		newDeadline := command.NewDeadline
		if command.ExtensionSeconds != 0 {
			if command.ExtensionSeconds < domain.MinimumMaximumDurationSeconds || command.ExtensionSeconds > domain.MaximumMaximumDurationSeconds || !command.NewDeadline.IsZero() {
				return domain.Session{}, fmt.Errorf("invalid extension hours")
			}
			newDeadline = session.MaximumDuration.DeadlineAt.Add(time.Duration(command.ExtensionSeconds) * time.Second)
		}
		if err := session.MaximumDuration.Extend(newDeadline, now); err != nil {
			return domain.Session{}, err
		}
	} else {
		if session.LifecycleState != domain.StateDraft && session.LifecycleState != domain.StateNew {
			return domain.Session{}, fmt.Errorf("maximum duration configuration requires a draft or new session: %w", domain.ErrInvalidTransition)
		}
		if command.Seconds < domain.MinimumMaximumDurationSeconds || command.Seconds > domain.MaximumMaximumDurationSeconds {
			return domain.Session{}, fmt.Errorf("maximum duration must be between 1 and 168 hours")
		}
		if !session.MaximumDuration.StartedAt.IsZero() {
			return domain.Session{}, fmt.Errorf("started sessions require an explicit extension: %w", domain.ErrInvalidTransition)
		}
		session.MaximumDuration.Seconds = command.Seconds
		if err := session.MaximumDuration.Validate(); err != nil {
			return domain.Session{}, err
		}
	}
	expected := session.Version
	session.Version++
	session.UpdatedAt = now
	eventID, err := service.ids.New(now)
	if err != nil {
		return domain.Session{}, err
	}
	eventType := domain.EventMaximumDurationConfigured
	if extension {
		eventType = domain.EventMaximumDurationExtended
	}
	event := domain.SessionEvent{ID: eventID, SessionID: session.ID, Type: eventType, OccurredAt: now, ActorType: string(command.Actor.Type), ActorID: command.Actor.ID, CorrelationID: command.CorrelationID, Data: map[string]string{
		"reason": strings.TrimSpace(command.Reason), "request_id": command.IdempotencyKey,
		"previous_seconds": strconv.FormatInt(previousSeconds, 10), "seconds": strconv.FormatInt(session.MaximumDuration.EffectiveSeconds(), 10),
		"previous_deadline": previous.UTC().Format(time.RFC3339Nano), "deadline": session.MaximumDuration.DeadlineAt.UTC().Format(time.RFC3339Nano),
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
	if service.notificationQueue != nil {
		content := fmt.Sprintf("<@%s> Maximum duration for `%s` was updated by an administrator.", session.OwnerDiscordUserID, session.Slug)
		if extension {
			content = fmt.Sprintf("<@%s> Maximum duration for `%s` was extended. New deadline: <t:%d:F>.", session.OwnerDiscordUserID, session.Slug, session.MaximumDuration.DeadlineAt.Unix())
		}
		_ = service.notificationQueue.Enqueue(ctx, domain.NotificationRequest{SchemaVersion: 1, NotificationID: "duration-" + eventID, SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID, Content: content, Kind: domain.NotificationSessionDuration, AllowedUserIDs: []string{session.OwnerDiscordUserID}, CorrelationID: command.CorrelationID, RequestedAt: now})
	}
	return session, nil
}
