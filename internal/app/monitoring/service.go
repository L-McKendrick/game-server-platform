package monitoring

import (
	"context"
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
	sessions, err := service.repo.ListRunning(ctx, 25)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, session := range sessions {
		if err := service.monitor(ctx, session); err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
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
