package sessioncard

import (
	"fmt"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const (
	embedColorOnline   = 0x23A55A
	embedColorSetup    = 0xF0B232
	embedColorError    = 0xDA373C
	embedColorInactive = 0x80848E
)

// RenderPublicEmbed renders the approved concise public card. RenderPublic is
// retained as the mandatory plain-text fallback and private detail views keep
// their progressive disclosure.
func RenderPublicEmbed(card Projection) *domain.NotificationEmbed {
	if card.Lifecycle == "Terminated" {
		description := safe(card.Description)
		if !card.StatusSince.IsZero() {
			if description != "" {
				description += "\n\n"
			}
			description += "Terminated: " + timestamp(card.StatusSince)
		}
		return &domain.NotificationEmbed{
			Title:       strings.ToUpper(safe(card.Game)) + " | " + safe(card.Name),
			Description: description, Color: embedColorInactive,
		}
	}
	status, color := publicEmbedStatus(card)
	description := "**" + strings.ToUpper(safe(card.Game)) + " | " + safe(card.Name) + "**"
	if strings.TrimSpace(card.Description) != "" {
		description += "\n" + safe(card.Description)
	}
	embed := &domain.NotificationEmbed{
		Title:       status,
		Description: description,
		Color:       color,
		Fields: []domain.NotificationEmbedField{{
			Name: "\u200b\nCURRENT MISSION", Value: publicMissionValue(card),
		}},
	}
	if value := publicProgressValue(card); value != "" && card.Lifecycle != "Running" {
		embed.Fields = append(embed.Fields, domain.NotificationEmbedField{Name: "\u200b\nPROGRESS", Value: value})
	}
	if value := publicFailureValue(card); value != "" {
		embed.Fields = append(embed.Fields, domain.NotificationEmbedField{Name: "ACTION REQUIRED", Value: value})
	}
	if card.Endpoints.Game.Available {
		embed.Fields = append(embed.Fields, domain.NotificationEmbedField{
			Name: "\u200b\nGame server", Value: publicGameConnectionValue(card), Inline: true,
		})
	}
	if card.Endpoints.TeamSpeak.Available {
		embed.Fields = append(embed.Fields, domain.NotificationEmbedField{
			Name: "TeamSpeak", Value: connectionAddress(card.Endpoints.TeamSpeak), Inline: true,
		})
	}
	return embed
}

func publicEmbedStatus(card Projection) (string, int) {
	if card.Failure.Present || strings.Contains(card.Health, "action required") || card.Lifecycle == "Action required" {
		return "🔴 ACTION REQUIRED · " + strings.ToUpper(safe(card.Stage)), embedColorError
	}
	switch card.Lifecycle {
	case "Running":
		if card.Health == "Healthy" {
			return "🟢 ONLINE · HEALTHY", embedColorOnline
		}
		return "🔴 ONLINE · " + strings.ToUpper(safe(card.Health)), embedColorError
	case "Archived":
		return "⚪ ARCHIVED · OFFLINE", embedColorInactive
	case "Terminated":
		return "⚪ TERMINATED", embedColorInactive
	case "Sleeping":
		return "⚪ OFFLINE · " + strings.ToUpper(safe(card.Lifecycle)), embedColorInactive
	default:
		return "🟠 " + strings.ToUpper(safe(card.Lifecycle)) + " · " + strings.ToUpper(safe(card.Stage)), embedColorSetup
	}
}

func publicMissionValue(card Projection) string {
	mission := safeCode(card.Players.Mission)
	mapName := safeCode(card.Players.Map)
	switch {
	case mission != "" && mapName != "" && !strings.EqualFold(mission, mapName):
		mission += " on " + mapName
	case mission == "" && mapName != "":
		mission = mapName
	case mission == "":
		mission = "Unavailable until the game server reports a live mission."
	}
	mission = "```\n" + mission + "\n```"
	if !card.Players.Available {
		return mission
	}
	players := fmt.Sprintf("%d of %d players", card.Players.Count, card.Players.Capacity)
	if card.Players.Capacity <= 0 {
		players = fmt.Sprintf("%d players", card.Players.Count)
	}
	if !card.StatusSince.IsZero() {
		players += " · session started " + timestamp(card.StatusSince)
	}
	return mission + "\n" + players
}

func publicProgressValue(card Projection) string {
	if !card.Progress.Visible {
		return ""
	}
	value := fmt.Sprintf("`%s` — Step %d/%d\n**Current stage:** %s", safeCode(card.Progress.Bar), card.Progress.Step, card.Progress.Total, safe(card.Stage))
	if card.Progress.Condition != "" {
		value += "\n**State:** " + safe(card.Progress.Condition)
	}
	if card.Progress.Activity != "" {
		value += "\n**Current download:** " + safe(card.Progress.Activity)
	}
	if !card.OperationStartedAt.IsZero() {
		value += "\n**Started:** " + timestamp(card.OperationStartedAt)
	}
	return value
}

func publicFailureValue(card Projection) string {
	if !card.Failure.Present {
		return ""
	}
	value := safe(card.Failure.Summary)
	if card.Failure.UserAction != "" {
		value += "\n**Your action:** " + safePreservingCode(card.Failure.UserAction)
	}
	if card.Failure.BillingImpact != "" {
		value += "\n**Billing:** " + safe(card.Failure.BillingImpact)
	}
	if card.Failure.SupportReference != "" {
		value += "\n**Support reference:** `" + safeCode(card.Failure.SupportReference) + "`"
	}
	return value
}

func publicGameConnectionValue(card Projection) string {
	value := connectionAddress(card.Endpoints.Game)
	value += "\n\n**Modlist:** "
	if len(card.Mods.CreatorDLCs) > 0 {
		value += safe(strings.Join(creatorDLCLabels(card.Mods.CreatorDLCs), ", ")) + "\n**Workshop preset:** "
	}
	if !card.Mods.Required {
		return value + "None"
	}
	if card.Mods.DownloadURL != "" {
		return value + "[" + safe(card.Name) + "](" + card.Mods.DownloadURL + ")"
	}
	if card.Mods.Status != "" {
		return value + safe(card.Mods.Status)
	}
	return value + "Unavailable"
}

func connectionAddress(connection ConnectionProjection) string {
	return fmt.Sprintf("`%s:%d`", safeCode(connection.Host), connection.Port)
}

// WithModlistLinkEmbed enriches the queued rich card at the delivery boundary,
// where the stable Discord attachment-message URL is known.
func WithModlistLinkEmbed(embed *domain.NotificationEmbed, sessionName, messageURL string) *domain.NotificationEmbed {
	if embed == nil || normalizeModlistURL(messageURL) == "" {
		return embed
	}
	copyEmbed := *embed
	copyEmbed.Fields = append([]domain.NotificationEmbedField(nil), embed.Fields...)
	for index := range copyEmbed.Fields {
		if strings.TrimSpace(strings.TrimPrefix(copyEmbed.Fields[index].Name, "\u200b")) != "Game server" {
			continue
		}
		base := strings.Split(copyEmbed.Fields[index].Value, "\n\n**Modlist:**")[0]
		copyEmbed.Fields[index].Value = base + "\n\n**Modlist:** [" + safe(sessionName) + "](" + normalizeModlistURL(messageURL) + ")"
		break
	}
	return &copyEmbed
}
