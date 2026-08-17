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

// EnqueueActivatedModlist publishes the sanitized attachment only when this
// completed lifecycle transaction activated the revision. Pending, failed,
// and previously active revisions are never re-advertised by another workflow.
func EnqueueActivatedModlist(ctx context.Context, queue ports.NotificationQueue, session domain.Session, workflow domain.Workflow, now time.Time) error {
	if queue == nil || session.Vanilla {
		return nil
	}
	active := session.EffectiveActivePresetRevision()
	if active.Empty() || active.Status != domain.PresetRevisionActive || active.Modlist.Empty() || workflow.Status != domain.WorkflowSucceeded || workflow.CompletedAt.IsZero() || !active.ActivatedAt.Equal(workflow.CompletedAt) {
		return nil
	}
	if strings.TrimSpace(workflow.CorrelationID) == "" {
		return fmt.Errorf("active modlist correlation ID is required")
	}
	publishedAt := active.ActivatedAt.UTC()
	if publishedAt.IsZero() {
		publishedAt = now.UTC()
	}
	digestPrefix := active.Modlist.SHA256
	if len(digestPrefix) > 12 {
		digestPrefix = digestPrefix[:12]
	}
	request := domain.NotificationRequest{
		SchemaVersion:  1,
		NotificationID: fmt.Sprintf("modlist-active-%s-r%d-%s", session.ID, active.Number, digestPrefix),
		SessionID:      session.ID,
		GuildID:        session.GuildID,
		ChannelID:      session.ChannelID,
		Content:        RenderModlistMessage(session, active.Modlist.Filename, active.Modlist.WorkshopCount, publishedAt),
		Kind:           domain.NotificationSessionModlist,
		Attachment: &domain.NotificationAttachment{
			ObjectKey: active.Modlist.ObjectKey, Filename: active.Modlist.Filename,
			ContentType: "text/html; charset=utf-8", SHA256: active.Modlist.SHA256,
			SizeBytes: active.Modlist.SizeBytes, Revision: session.Version,
		},
		CorrelationID: strings.TrimSpace(workflow.CorrelationID), RequestedAt: now.UTC(),
	}
	return queue.Enqueue(ctx, request)
}
