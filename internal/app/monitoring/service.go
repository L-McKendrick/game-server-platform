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
}

func NewService(repo ports.MonitoringRepository, runner ports.MonitoringRunner, notifications ports.NotificationQueue, ids IDGenerator, clock Clock) (*Service, error) {
	if repo == nil || runner == nil || ids == nil || clock == nil {
		return nil, fmt.Errorf("monitoring dependencies are required")
	}
	return &Service{repo, runner, notifications, ids, clock}, nil
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
	from, err := session.CompleteMonitoring(health, now)
	if err != nil {
		return err
	}
	var event *domain.SessionEvent
	if from != health {
		id, err := service.ids.New(now)
		if err != nil {
			return err
		}
		value := domain.NewHealthChangedEvent(id, session, from, status.Observation, now)
		event = &value
	}
	if err := service.repo.SaveMonitoring(ctx, session, expected, event); err != nil {
		return err
	}
	if event != nil && service.notifications != nil {
		id, err := service.ids.New(now)
		if err == nil {
			_ = service.notifications.Enqueue(ctx, domain.NotificationRequest{SchemaVersion: 1, NotificationID: id, SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID, Content: fmt.Sprintf("**Game server health changed**\\nSession: `%s`\\nHealth: `%s` -> `%s`", session.ID, from, health), CorrelationID: event.CorrelationID, RequestedAt: now})
		}
	}
	return nil
}
