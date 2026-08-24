package workflows

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
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
	trustedContinuation, err := service.validateBootstrapContinuation(ctx, command, workflowType)
	if err != nil {
		return domain.Workflow{}, err
	}
	trustedAutomation := command.Actor.System && command.Actor.DiscordUserID == domain.InactivityMonitorActorID && (workflowType == domain.SleepWorkflowType || workflowType == domain.ArchiveWorkflowType)
	canManageLifecycle := command.Actor.CanManageGuild && isOwnerOrAdminLifecycle(workflowType)
	if !trustedContinuation && !trustedAutomation && !canManageLifecycle {
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
	if session.GuildID != command.Actor.GuildID || (session.OwnerDiscordUserID != command.Actor.DiscordUserID && !canManageLifecycle && !trustedAutomation) {
		return domain.Workflow{}, domain.ErrForbidden
	}
	now := service.clock.Now().UTC()
	if trustedAutomation {
		var automationErr error
		if workflowType == domain.SleepWorkflowType {
			automationErr = domain.ValidateAutomaticSleepCommand(command, session, now)
		} else {
			automationErr = domain.ValidateAutomaticArchiveCommand(command, session, now)
		}
		if automationErr != nil {
			return domain.Workflow{}, automationErr
		}
	}
	workflowID := command.CommandID
	expectedVersion := session.Version
	if !trustedContinuation && (workflowType == domain.ProvisionWorkflowType || workflowType == domain.BootstrapWorkflowType) {
		if err := applyServerConfigSnapshot(&session, command); err != nil {
			return domain.Workflow{}, err
		}
	}
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
	actorType := domain.ActorTypeDiscordUser
	if trustedAutomation {
		actorType = domain.ActorTypeSystem
	}
	actor := domain.Actor{Type: actorType, ID: command.Actor.DiscordUserID}
	eventType := domain.EventWorkflowStarted
	if workflow.Type == domain.ArchiveWorkflowType {
		eventType = domain.EventArchiveStarted
	} else if workflow.Type == domain.RestoreWorkflowType {
		eventType = domain.EventRestoreStarted
	} else if workflow.Type == domain.TerminationWorkflowType {
		eventType = domain.EventTerminationStarted
	}
	event := domain.NewWorkflowEvent(eventID, eventType, command.CorrelationID, actor, session, workflow, now)
	if workflow.Type == domain.ProvisionWorkflowType || workflow.Type == domain.BootstrapWorkflowType {
		event.Data["server_config_revision"] = strconv.FormatInt(session.ServerConfigRevision, 10)
	}
	if err := service.workflows.AcquireWorkflow(ctx, session, expectedVersion, workflow, event); err != nil {
		return domain.Workflow{}, err
	}
	return service.startExecution(ctx, session, workflow, actor)
}

func applyServerConfigSnapshot(session *domain.Session, command domain.CommandEnvelope) error {
	mode := strings.TrimSpace(command.Parameters[domain.ServerConfigModeParameter])
	switch mode {
	case "":
		// Backward compatibility for commands already queued before snapshots
		// were introduced: retain the persisted session selection.
		return nil
	case domain.ServerConfigModeGenerated:
		if command.Parameters[domain.ServerConfigRevisionParameter] != "" || command.Parameters[domain.ServerConfigObjectParameter] != "" || command.Parameters[domain.ServerConfigSHAParameter] != "" {
			return domain.ErrForbidden
		}
		return session.SelectGeneratedServerConfig()
	case domain.ServerConfigModeCustom:
		revision, err := strconv.ParseInt(strings.TrimSpace(command.Parameters[domain.ServerConfigRevisionParameter]), 10, 64)
		objectKey := strings.TrimSpace(command.Parameters[domain.ServerConfigObjectParameter])
		sha256 := strings.TrimSpace(command.Parameters[domain.ServerConfigSHAParameter])
		expectedPrefix := "guilds/" + command.Actor.GuildID + "/server-config/revisions/"
		if err != nil || revision < 1 || !strings.HasPrefix(objectKey, expectedPrefix) || len(sha256) != 64 {
			return domain.ErrForbidden
		}
		return session.SelectServerConfigSnapshot(revision, objectKey, sha256)
	default:
		return domain.ErrForbidden
	}
}

func (service *Service) validateBootstrapContinuation(ctx context.Context, command domain.CommandEnvelope, workflowType string) (bool, error) {
	provisionID := command.Parameters[domain.BootstrapContinuationParameter]
	if provisionID == "" {
		return false, nil
	}
	if workflowType != domain.BootstrapWorkflowType || command.CommandID != domain.BootstrapContinuationCommandID(provisionID) ||
		command.IdempotencyKey != "workflow-continuation:"+provisionID {
		return false, domain.ErrForbidden
	}
	provision, err := service.workflows.GetWorkflow(ctx, command.SessionID, provisionID)
	if err != nil {
		return false, err
	}
	if provision.Type != domain.ProvisionWorkflowType || provision.Status != domain.WorkflowSucceeded ||
		provision.RequestedBy != command.Actor.DiscordUserID || provision.CorrelationID != command.CorrelationID {
		return false, domain.ErrForbidden
	}
	session, err := service.sessions.Get(ctx, command.SessionID)
	if err != nil {
		return false, err
	}
	if session.OwnerDiscordUserID != command.Actor.DiscordUserID || session.GuildID != command.Actor.GuildID ||
		session.ChannelID != command.Actor.ChannelID || session.ActiveWorkflowID != command.CommandID ||
		session.ActiveWorkflowType != domain.BootstrapWorkflowType {
		return false, domain.ErrForbidden
	}
	return true, nil
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
	actorType := domain.ActorTypeDiscordUser
	if workflow.RequestedBy == domain.InactivityMonitorActorID {
		actorType = domain.ActorTypeSystem
	}
	actor := domain.Actor{Type: actorType, ID: workflow.RequestedBy}
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
