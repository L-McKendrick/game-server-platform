package monitoring

import (
	"context"
	"errors"
	"fmt"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"time"
)

type Clock interface{ Now() time.Time }
type IDGenerator interface {
	New(time.Time) (string, error)
}
type Service struct {
	repo          ports.MonitoringRepository
	runner        ports.MonitoringRunner
	notifications ports.NotificationQueue
	ids           IDGenerator
	clock         Clock
	players       ports.PlayerQuery
	commands      ports.CommandQueue
}

type Option func(*Service)

func WithPlayerQuery(query ports.PlayerQuery) Option {
	return func(service *Service) { service.players = query }
}

func WithCommandQueue(queue ports.CommandQueue) Option {
	return func(service *Service) { service.commands = queue }
}

func NewService(repo ports.MonitoringRepository, runner ports.MonitoringRunner, notifications ports.NotificationQueue, ids IDGenerator, clock Clock, options ...Option) (*Service, error) {
	if repo == nil || runner == nil || ids == nil || clock == nil {
		return nil, fmt.Errorf("monitoring dependencies are required")
	}
	service := &Service{repo: repo, runner: runner, notifications: notifications, ids: ids, clock: clock}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}
func (service *Service) Run(ctx context.Context) (int, error) {
	sessions, err := service.repo.ListInactivityCandidates(ctx, 25)
	if err != nil {
		return 0, err
	}
	completed := 0
	var sessionErrors []error
	for _, session := range sessions {
		if err := service.runSession(ctx, session); err != nil {
			sessionErrors = append(sessionErrors, fmt.Errorf("monitor session %s: %w", session.ID, err))
			continue
		}
		completed++
	}
	return completed, errors.Join(sessionErrors...)
}

func (service *Service) runSession(ctx context.Context, session domain.Session) error {
	if session.LifecycleState == domain.StateFailed {
		if session.FailedInitialCreation() {
			warned, err := service.warnIfDue(ctx, session)
			if err != nil {
				return err
			}
			session = warned
		}
		if action := session.MaximumDeadlineAction(service.clock.Now().UTC()); action == domain.CommandDestroySession {
			return service.enforceDeadline(ctx, session, action)
		}
		return service.alertFailedDeadline(ctx, session)
	}
	warned, err := service.warnIfDue(ctx, session)
	if err != nil {
		return err
	}
	session = warned
	if action := session.MaximumDeadlineAction(service.clock.Now().UTC()); action != "" {
		return service.enforceDeadline(ctx, session, action)
	}
	if session.LifecycleState == domain.StateSleeping {
		return service.archiveIfDue(ctx, session)
	}
	return service.monitor(ctx, session)
}

func (service *Service) alertFailedDeadline(ctx context.Context, session domain.Session) error {
	if service.notifications == nil || !session.MaximumDuration.Expired(service.clock.Now().UTC()) || (session.Infrastructure.InstanceID == "" && session.Infrastructure.DataVolumeID == "") || (session.MaximumDurationWarningDeadline.Equal(session.MaximumDuration.DeadlineAt) && session.MaximumDurationWarningLevel == 3) {
		return nil
	}
	now := service.clock.Now().UTC()
	deadline := session.MaximumDuration.DeadlineAt
	id := domain.MaximumDeadlineCommandID(session.ID, deadline, "failed-alert")
	content := fmt.Sprintf("<@%s> `%s` has reached its maximum duration, but its failed state prevented automatic sleep/archive. Resources may still incur cost. Give the support reference to an operator for safe cleanup.", session.OwnerDiscordUserID, session.Slug)
	if err := service.notifications.Enqueue(ctx, domain.NotificationRequest{SchemaVersion: 1, NotificationID: id, SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID, Content: content, Kind: domain.NotificationSessionDuration, AllowedUserIDs: []string{session.OwnerDiscordUserID}, CorrelationID: id, RequestedAt: now}); err != nil {
		return err
	}
	expected := session.Version
	session.MaximumDurationWarningDeadline = deadline
	session.MaximumDurationWarningLevel = 3
	session.Version++
	session.UpdatedAt = now
	event := domain.SessionEvent{ID: id, SessionID: session.ID, Type: domain.EventMaximumDurationWarning, OccurredAt: now, ActorType: string(domain.ActorTypeSystem), ActorID: domain.InactivityMonitorActorID, CorrelationID: id, Data: map[string]string{"deadline": deadline.Format(time.RFC3339Nano), "level": "failed-attention"}}
	return service.repo.SaveMonitoring(ctx, session, expected, []domain.SessionEvent{event})
}

func (service *Service) warnIfDue(ctx context.Context, session domain.Session) (domain.Session, error) {
	now := service.clock.Now().UTC()
	deadline := session.MaximumDuration.DeadlineAt
	if deadline.IsZero() || !deadline.After(now) || service.notifications == nil {
		return session, nil
	}
	if session.LifecycleState != domain.StateRunning && session.LifecycleState != domain.StateIdle && session.LifecycleState != domain.StateSleeping && !session.FailedInitialCreation() {
		return session, nil
	}
	remaining := deadline.Sub(now)
	level := 0
	if remaining <= time.Hour {
		level = 1
	}
	if remaining <= 15*time.Minute {
		level = 2
	}
	if level == 0 || (session.MaximumDurationWarningDeadline.Equal(deadline) && session.MaximumDurationWarningQueuedLevel >= level) {
		return session, nil
	}
	if !session.MaximumDurationWarningDeadline.Equal(deadline) || session.MaximumDurationWarningLevel < level {
		previous := session.Version
		session.MaximumDurationWarningDeadline = deadline
		session.MaximumDurationWarningLevel = level
		session.MaximumDurationWarningQueuedLevel = 0
		session.Version++
		session.UpdatedAt = now
		eventID := domain.MaximumDeadlineCommandID(session.ID, deadline, fmt.Sprintf("warning-%d", level))
		event := domain.SessionEvent{ID: eventID, SessionID: session.ID, Type: domain.EventMaximumDurationWarning, OccurredAt: now, ActorType: string(domain.ActorTypeSystem), ActorID: domain.InactivityMonitorActorID, CorrelationID: eventID, Data: map[string]string{"deadline": deadline.Format(time.RFC3339Nano), "level": fmt.Sprintf("%d", level)}}
		if err := service.repo.SaveMonitoring(ctx, session, previous, []domain.SessionEvent{event}); err != nil {
			return session, err
		}
	}
	eventID := domain.MaximumDeadlineCommandID(session.ID, deadline, fmt.Sprintf("warning-%d", level))
	content := fmt.Sprintf("<@%s> `%s` reaches its maximum duration at <t:%d:F>. The platform will sleep and archive it automatically; contact an administrator before then if you need an extension.", session.OwnerDiscordUserID, session.Slug, deadline.Unix())
	if session.FailedInitialCreation() {
		content = fmt.Sprintf("<@%s> `%s` did not finish initial setup. Its retained resources and session files will be permanently deleted at <t:%d:F> unless an administrator extends the deadline. Save anything you need before then.", session.OwnerDiscordUserID, session.Slug, deadline.Unix())
	}
	request := domain.NotificationRequest{SchemaVersion: 1, NotificationID: eventID, SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID, Content: content, Kind: domain.NotificationSessionDuration, AllowedUserIDs: []string{session.OwnerDiscordUserID}, CorrelationID: eventID, RequestedAt: now}
	if err := service.notifications.Enqueue(ctx, request); err != nil {
		return session, fmt.Errorf("enqueue maximum-duration warning: %w", err)
	}
	previous := session.Version
	session.MaximumDurationWarningQueuedLevel = level
	session.Version++
	session.UpdatedAt = now
	if err := service.repo.SaveMonitoring(ctx, session, previous, nil); err != nil {
		return session, err
	}
	return session, nil
}

func (service *Service) enforceDeadline(ctx context.Context, session domain.Session, action string) error {
	if service.commands == nil {
		return fmt.Errorf("maximum-duration enforcement command queue is required")
	}
	now := service.clock.Now().UTC()
	commandID := domain.MaximumDeadlineCommandID(session.ID, session.MaximumDuration.DeadlineAt, action)
	return service.commands.Enqueue(ctx, domain.CommandEnvelope{
		SchemaVersion: 1, CommandID: commandID, CommandType: action, RequestedAt: now,
		Actor:     domain.CommandActor{DiscordUserID: domain.InactivityMonitorActorID, GuildID: session.GuildID, ChannelID: session.ChannelID, System: true},
		SessionID: session.ID, IdempotencyKey: "maximum-duration:" + commandID, CorrelationID: commandID,
		Parameters: map[string]string{domain.MaximumDeadlineParameter: session.MaximumDuration.DeadlineAt.UTC().Format(time.RFC3339Nano)},
	})
}

func (service *Service) archiveIfDue(ctx context.Context, session domain.Session) error {
	if service.commands == nil || !session.AutomaticArchiveDue(service.clock.Now().UTC()) {
		return nil
	}
	now := service.clock.Now().UTC()
	commandID := domain.AutomaticArchiveCommandID(session.ID, session.SleepingSince)
	return service.commands.Enqueue(ctx, domain.CommandEnvelope{
		SchemaVersion: 1, CommandID: commandID, CommandType: domain.CommandArchiveSession, RequestedAt: now,
		Actor:     domain.CommandActor{DiscordUserID: domain.InactivityMonitorActorID, GuildID: session.GuildID, ChannelID: session.ChannelID, System: true},
		SessionID: session.ID, IdempotencyKey: "automatic-archive:" + commandID, CorrelationID: commandID,
		Parameters: map[string]string{domain.AutomaticSleepingSinceParameter: session.SleepingSince.UTC().Format(time.RFC3339Nano)},
	})
}
func (service *Service) monitor(ctx context.Context, session domain.Session) error {
	expected := session.Version
	now := service.clock.Now().UTC()
	if session.MonitoringCommandID == "" {
		id, err := service.runner.Start(ctx, session)
		if err != nil {
			return err
		}
		if err = session.BeginMonitoring(id, now); err != nil {
			return err
		}
		return service.repo.SaveMonitoring(ctx, session, expected, nil)
	}
	status, err := service.runner.Observe(ctx, session.Infrastructure.InstanceID, session.MonitoringCommandID)
	if err != nil {
		return err
	}
	if status.Status == "Pending" || status.Status == "InProgress" || status.Status == "Delayed" {
		return nil
	}
	health := status.Observation.Classify(session.TeamSpeakEnabled)
	if status.Status != "Success" {
		health = domain.HealthUnhealthy
	}
	activity := domain.PlayerActivityObservation{ObservedAt: now}
	if status.Status == "Success" && service.players != nil && session.Infrastructure.PublicIPv4 != "" {
		queryContext, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		players, queryErr := service.players.Query(queryContext, session.Infrastructure.PublicIPv4)
		cancel()
		if queryErr == nil {
			activity.Known = true
			activity.PlayerCount = players.PlayerCount
		}
	}
	from, err := session.CompleteMonitoring(health, activity, now)
	if err != nil {
		return err
	}
	events := make([]domain.SessionEvent, 0, 2)
	if from != health {
		id, err := service.ids.New(now)
		if err != nil {
			return err
		}
		events = append(events, domain.NewHealthChangedEvent(id, session, from, status.Observation, now))
	}
	activityEventID, err := service.ids.New(now)
	if err != nil {
		return err
	}
	events = append(events, domain.NewPlayerActivityObservedEvent(activityEventID, session, now))
	if err := service.repo.SaveMonitoring(ctx, session, expected, events); err != nil {
		return err
	}
	if service.commands != nil && session.AutomaticSleepDue(now) {
		commandID := domain.AutomaticSleepCommandID(session.ID, session.IdleSince)
		command := domain.CommandEnvelope{
			SchemaVersion: 1, CommandID: commandID, CommandType: domain.CommandSleepSession, RequestedAt: now,
			Actor:     domain.CommandActor{DiscordUserID: domain.InactivityMonitorActorID, GuildID: session.GuildID, ChannelID: session.ChannelID, System: true},
			SessionID: session.ID, IdempotencyKey: "automatic-sleep:" + commandID, CorrelationID: commandID,
			Parameters: map[string]string{domain.AutomaticIdleSinceParameter: session.IdleSince.UTC().Format(time.RFC3339Nano)},
		}
		if err := service.commands.Enqueue(ctx, command); err != nil {
			return fmt.Errorf("enqueue automatic sleep: %w", err)
		}
	}
	if from != health && service.notifications != nil {
		id, err := service.ids.New(now)
		if err == nil {
			_ = service.notifications.Enqueue(ctx, domain.NotificationRequest{SchemaVersion: 1, NotificationID: id, SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID, Content: fmt.Sprintf("**Game server health changed**\\nSession: `%s`\\nHealth: `%s` -> `%s`", session.ID, from, health), CorrelationID: events[0].CorrelationID, RequestedAt: now})
		}
	}
	return nil
}
