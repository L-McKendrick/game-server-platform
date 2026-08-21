package interactions

import (
	"context"
	"fmt"
	"strings"

	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func (handler *Handler) help(ctx context.Context, options []applicationCommandOption, actor domain.Actor, guildID string) (string, error) {
	reference, err := stringOption(options, "session", false)
	if err != nil {
		return "", newUserError("Select a session or leave the session option empty.")
	}
	if strings.TrimSpace(reference) != "" {
		selection, err := handler.service.Resolve(ctx, appsession.ResolveQuery{
			Actor: actor, GuildID: guildID, Reference: reference, AllowGuildMember: true,
		})
		if err != nil {
			return "", err
		}
		session, err := handler.service.Get(ctx, appsession.GetQuery{
			Actor: actor, SessionID: selection.ID, GuildID: guildID, AllowGuildMember: true,
		})
		if err != nil {
			return "", fmt.Errorf("get session help: %w", err)
		}
		return contextualCommandHelp(session), nil
	}

	selections, err := handler.service.Select(ctx, appsession.SelectQuery{
		Actor: actor, GuildID: guildID, Limit: 1,
	})
	if err != nil {
		return "", fmt.Errorf("select sessions for help: %w", err)
	}
	if len(selections) == 0 {
		return "**Getting started**\n1. Use `/rb create` to open the non-billable setup form and upload the required mission (plus a preset for modded play).\n2. Use `/rb status` while Discord uploads are validated; validation does not create game-server infrastructure.\n3. After the required files are accepted, use `/rb start` to begin the server workflow, then follow authoritative progress in `/rb status`.\n\nSleep retains storage, archive verifies a portable backup before removing EC2/EBS, and terminate is irreversible. Use `/rb help session:...` later for one state-aware next action.", nil
	}
	return "**Platform help**\n`/rb create` prepares a non-billable draft; `/rb start` is the explicit billable boundary that begins the server workflow. `/rb status` is the authoritative detail view. Sleep retains storage, archive verifies a backup before removing EC2/EBS, restore creates replacement resources, and terminate is irreversible.\n\nNext: use `/rb list`, or choose `/rb help session:...` for state-aware guidance. Full operating and recovery procedures remain in the runbook.", nil
}

func contextualCommandHelp(session domain.Session) string {
	return fmt.Sprintf(
		"**Help for %s**\nSlug: `%s`\nState: %s\n\nNext: %s\n\nUse `/rb status` for authoritative details; no command shown here is queued automatically.",
		sanitizeInline(session.DisplayName), sanitizeCode(session.Slug), lifecyclePresentation(session.LifecycleState), sessionNextAction(session),
	)
}
