package sessioncard

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

// EnqueueProgress publishes only persisted, normalized progress milestones.
// Delivery is deliberately secondary: callers invoke this after the session
// mutation commits and decide how to surface a queue failure.
func EnqueueProgress(ctx context.Context, queue ports.NotificationQueue, session domain.Session, workflow domain.Workflow, now time.Time) error {
	if queue == nil {
		return nil
	}
	if session.Progress.WorkflowID != workflow.ID || session.Progress.WorkflowType != workflow.Type || !session.Progress.Milestone.Valid() {
		return fmt.Errorf("session progress does not match workflow")
	}
	milestone := strings.ToLower(string(session.Progress.Milestone))
	notificationID := "card-progress-" + workflow.ID + "-" + strings.ReplaceAll(milestone, "_", "-")
	renderedAt := session.Progress.UpdatedAt
	if renderedAt.IsZero() {
		renderedAt = now
	}
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: notificationID, Kind: domain.NotificationSessionCard,
		SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID,
		Content: RenderPublic(Project(session, Options{Now: renderedAt, Workflow: &workflow})), CardRevision: session.Version,
		CorrelationID: workflow.CorrelationID, RequestedAt: now.UTC(),
	}
	return queue.Enqueue(ctx, request)
}
