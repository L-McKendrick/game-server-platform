package workflows

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/app/failurestate"
	"github.com/L-McKendrick/game-server-platform/internal/app/sessioncard"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type Clock interface{ Now() time.Time }
type IDGenerator interface {
	New(time.Time) (string, error)
}
type Authorizer interface {
	Authorize(ctx context.Context, guildID string, channelID string, userID string, roles []string) error
}

type Service struct {
	sessions      ports.SessionRepository
	workflows     ports.WorkflowRepository
	starter       ports.WorkflowStarter
	authorizer    Authorizer
	ids           IDGenerator
	clock         Clock
	lease         time.Duration
	notifications ports.NotificationQueue
}

type Option func(*Service)

func WithNotificationQueue(queue ports.NotificationQueue) Option {
	return func(service *Service) { service.notifications = queue }
}

func NewService(sessions ports.SessionRepository, workflows ports.WorkflowRepository, starter ports.WorkflowStarter, authorizer Authorizer, ids IDGenerator, clock Clock, lease time.Duration, options ...Option) (*Service, error) {
	if sessions == nil || workflows == nil || starter == nil || authorizer == nil || ids == nil || clock == nil {
		return nil, fmt.Errorf("workflow service dependencies are required")
	}
	if lease <= 0 {
		return nil, fmt.Errorf("workflow lease must be positive")
	}
	service := &Service{sessions: sessions, workflows: workflows, starter: starter, authorizer: authorizer, ids: ids, clock: clock, lease: lease}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

// Start validates a normalized command, acquires the metadata lock before
// starting Step Functions, and records the execution ARN conditionally.
func (service *Service) Start(ctx context.Context, command domain.CommandEnvelope) (domain.Workflow, error) {
	workflowType, err := command.WorkflowType()
	if err != nil {
		return domain.Workflow{}, err
	}
	canManageLifecycle := command.Actor.CanManageGuild && isOwnerOrAdminLifecycle(workflowType)
	if !canManageLifecycle {
		if err := service.authorizer.Authorize(
			ctx,
			command.Actor.GuildID,
			command.Actor.ChannelID,
			command.Actor.DiscordUserID,
			command.Actor.Roles,
		); err != nil {
			return domain.Workflow{}, err
		}
	}
	if existing, err := service.workflows.GetWorkflow(ctx, command.SessionID, command.CommandID); err == nil {
		if existing.Type != workflowType || existing.RequestedBy != command.Actor.DiscordUserID || existing.CorrelationID != command.CorrelationID {
			return domain.Workflow{}, domain.ErrIdempotencyConflict
		}
		if existing.Status != domain.WorkflowPending || existing.ExecutionARN != "" {
			return existing, nil
		}
		return service.resumePending(ctx, existing)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.Workflow{}, err
	}
	session, err := service.sessions.Get(ctx, command.SessionID)
	if err != nil {
		return domain.Workflow{}, err
	}
	if session.GuildID != command.Actor.GuildID || (session.OwnerDiscordUserID != command.Actor.DiscordUserID && !canManageLifecycle) {
		return domain.Workflow{}, domain.ErrForbidden
	}
	now := service.clock.Now().UTC()
	workflowID := command.CommandID
	expectedVersion := session.Version
	if err := acquireWorkflowLock(&session, workflowID, workflowType, service.lease, now); err != nil {
		return domain.Workflow{}, err
	}
	workflow := domain.Workflow{
		ID: workflowID, SessionID: session.ID, Type: workflowType, Status: domain.WorkflowPending,
		RequestedBy: command.Actor.DiscordUserID, CorrelationID: command.CorrelationID,
		ExpectedVersion: expectedVersion, StartedAt: now, LeaseExpiresAt: now.Add(service.lease),
	}
	eventID, err := service.ids.New(now)
	if err != nil {
		return domain.Workflow{}, err
	}
	actor := domain.Actor{Type: domain.ActorTypeDiscordUser, ID: command.Actor.DiscordUserID}
	eventType := domain.EventWorkflowStarted
	if workflow.Type == domain.ArchiveWorkflowType {
		eventType = domain.EventArchiveStarted
	} else if workflow.Type == domain.RestoreWorkflowType {
		eventType = domain.EventRestoreStarted
	} else if workflow.Type == domain.TerminationWorkflowType {
		eventType = domain.EventTerminationStarted
	}
	event := domain.NewWorkflowEvent(eventID, eventType, command.CorrelationID, actor, session, workflow, now)
	if err := service.workflows.AcquireWorkflow(ctx, session, expectedVersion, workflow, event); err != nil {
		return domain.Workflow{}, err
	}
	return service.startExecution(ctx, session, workflow, actor)
}

func isOwnerOrAdminLifecycle(workflowType string) bool {
	return workflowType == domain.SleepWorkflowType || workflowType == domain.WakeWorkflowType
}

func acquireWorkflowLock(session *domain.Session, workflowID string, workflowType string, lease time.Duration, now time.Time) error {
	if workflowType == "ProvisionSession" {
		return session.AcquireProvisioningWorkflowLock(workflowID, lease, now)
	}
	if workflowType == domain.BootstrapWorkflowType {
		return session.AcquireBootstrapWorkflowLock(workflowID, lease, now)
	}
	if workflowType == domain.SleepWorkflowType {
		return session.BeginSleep(workflowID, lease, now)
	}
	if workflowType == domain.WakeWorkflowType {
		return session.BeginWake(workflowID, lease, now)
	}
	if workflowType == domain.ArchiveWorkflowType {
		return session.BeginArchive(workflowID, lease, now)
	}
	if workflowType == domain.RestoreWorkflowType {
		return session.BeginRestore(workflowID, lease, now)
	}
	if workflowType == domain.TerminationWorkflowType {
		return session.BeginTermination(workflowID, lease, now)
	}
	return session.AcquireWorkflowLock(workflowID, workflowType, lease, now)
}

func (service *Service) resumePending(ctx context.Context, workflow domain.Workflow) (domain.Workflow, error) {
	session, err := service.sessions.Get(ctx, workflow.SessionID)
	if err != nil {
		return domain.Workflow{}, err
	}
	actor := domain.Actor{Type: domain.ActorTypeDiscordUser, ID: workflow.RequestedBy}
	return service.startExecution(ctx, session, workflow, actor)
}

func (service *Service) startExecution(ctx context.Context, session domain.Session, workflow domain.Workflow, actor domain.Actor) (domain.Workflow, error) {
	_ = sessioncard.EnqueueProgress(ctx, service.notifications, session, workflow, service.clock.Now().UTC())
	executionARN, err := service.starter.Start(ctx, workflow)
	if err != nil {
		return domain.Workflow{}, service.failStart(ctx, session, workflow, actor, err)
	}
	workflow.ExecutionARN = executionARN
	workflow.Status = domain.WorkflowRunning
	workflow.CurrentStage = "Started"
	if err := service.workflows.SetWorkflowExecution(ctx, workflow, domain.WorkflowPending); err != nil {
		return domain.Workflow{}, err
	}
	return workflow, nil
}

func (service *Service) failStart(ctx context.Context, session domain.Session, workflow domain.Workflow, actor domain.Actor, startErr error) error {
	now := service.clock.Now().UTC()
	expectedVersion := session.Version
	if err := failurestate.Record(&session, workflow, "ERR_WORKFLOW_START_FAILED", "ERR_WORKFLOW_START_FAILED", "Workflow start",
		"The operation could not be handed to the workflow service.", failurestate.Impact(session, false), now); err != nil {
		return err
	}
	var releaseErr error
	if workflow.Type == "ProvisionSession" {
		releaseErr = session.AbortProvisioningWorkflowStart(workflow.ID, now)
	} else if workflow.Type == domain.ArchiveWorkflowType {
		releaseErr = session.AbortArchiveWorkflowStart(workflow.ID, now)
	} else if workflow.Type == domain.RestoreWorkflowType {
		releaseErr = session.AbortRestoreWorkflowStart(workflow.ID, now)
	} else if workflow.Type == domain.TerminationWorkflowType {
		releaseErr = session.AbortTerminationWorkflowStart(workflow.ID, now)
	} else {
		releaseErr = session.ReleaseWorkflowLock(workflow.ID, now)
	}
	if releaseErr != nil {
		return releaseErr
	}
	workflow.Status = domain.WorkflowFailed
	workflow.ErrorCode = "ERR_WORKFLOW_START_FAILED"
	workflow.ErrorMessage = startErr.Error()
	workflow.CompletedAt = now
	eventID, err := service.ids.New(now)
	if err != nil {
		return err
	}
	event := domain.NewWorkflowEvent(eventID, domain.EventWorkflowFailed, workflow.CorrelationID, actor, session, workflow, now)
	if err := service.workflows.CompleteWorkflow(ctx, session, expectedVersion, workflow, event); err != nil {
		return err
	}
	_ = sessioncard.EnqueueProgress(ctx, service.notifications, session, workflow, now)
	return fmt.Errorf("start workflow: %w", startErr)
}
