package sessioncard

import (
	"context"
	"crypto/sha256"
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
	if session.Progress.State == domain.ProgressWaiting || session.Progress.State == domain.ProgressRetrying {
		return nil
	}
	milestone := strings.ToLower(string(session.Progress.Milestone))
	notificationKey := strings.ReplaceAll(milestone, "_", "-")
	if session.Progress.Activity != "" {
		digest := sha256.Sum256([]byte(session.Progress.Activity))
		notificationKey += fmt.Sprintf("-activity-%x", digest[:6])
	}
	if session.Progress.Milestone != domain.ProgressCompleted {
		switch session.Progress.State {
		case domain.ProgressRollingBack, domain.ProgressActionRequired, domain.ProgressCancelled:
			notificationKey += "-" + strings.ReplaceAll(strings.ToLower(string(session.Progress.State)), "_", "-")
		}
	}
	notificationID := "card-progress-" + workflow.ID + "-" + notificationKey
	renderedAt := session.Progress.LastProgressAt
	if renderedAt.IsZero() {
		renderedAt = now
	}
	projection := Project(session, Options{Now: renderedAt, Workflow: &workflow})
	request := domain.NotificationRequest{
		SchemaVersion: 1, NotificationID: notificationID, Kind: domain.NotificationSessionCard,
		SessionID: session.ID, GuildID: session.GuildID, ChannelID: session.ChannelID,
		Content: RenderPublic(projection), Embed: RenderPublicEmbed(projection), CardRevision: session.Version,
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
