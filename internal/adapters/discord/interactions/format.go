package interactions

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const maximumDiscordContentRunes = 1900

type discordRenderer struct{}

var renderer discordRenderer

func (discordRenderer) messageData(
	content string,
	flags int,
	components *[]interactionComponent,
) *interactionResponseData {
	return &interactionResponseData{
		Content:         boundDiscordContent(content),
		Flags:           flags,
		AllowedMentions: suppressedAllowedMentions(),
		Components:      components,
	}
}

func suppressedAllowedMentions() *interactionAllowedMentions {
	return &interactionAllowedMentions{Parse: []string{}}
}

func (discordRenderer) configuredSession(session domain.Session) string {
	return fmt.Sprintf(
		"**Session configured**\nName: %s\nSlug: `%s`\nConfiguration: `%d`\nProfile: `%s`\nMode: %s\nSleep after: `%d minutes`\nArchive after: `%d days`\nTeamSpeak: %s\nUpdated: %s",
		sanitizeInline(session.DisplayName),
		sanitizeCode(session.Slug),
		session.ConfigurationRevision,
		sanitizeCode(session.GameProfileID),
		sessionModeLabel(session.Vanilla),
		session.SleepAfterSeconds/60,
		session.ArchiveAfterSeconds/86400,
		enabledLabel(session.TeamSpeakEnabled),
		discordTimestamp(session.UpdatedAt),
	)
}

func (discordRenderer) artifactAccepted(kind domain.ArtifactKind, filename string) string {
	return fmt.Sprintf(
		"**%s accepted for validation**\nFile: `%s`\nValidation continues in the background. Use `/rb status` to check the session.",
		titleCase(strings.ToLower(string(kind))),
		sanitizeCode(filename),
	)
}

func (discordRenderer) createdSession(session domain.Session) string {
	return fmt.Sprintf(
		"**Draft session created**\nName: %s\nSlug: `%s`\nStatus: %s\nGame: `%s`\nCreated: %s\nNext: upload the mission%s, then configure the session.",
		sanitizeInline(session.DisplayName),
		sanitizeCode(session.Slug),
		lifecyclePresentation(session.LifecycleState),
		sanitizeCode(session.GameType),
		discordTimestamp(session.CreatedAt),
		presetInstruction(session.Vanilla),
	)
}

func (discordRenderer) sessionStatus(session domain.Session, players *domain.PlayerStatus) string {
	var builder strings.Builder
	fmt.Fprintf(
		&builder,
		"**%s**\nSlug: `%s`",
		sanitizeInline(session.DisplayName),
		sanitizeCode(session.Slug),
	)
	if session.Description != "" {
		fmt.Fprintf(&builder, "\nDescription: %s", sanitizeInline(session.Description))
	}
	fmt.Fprintf(
		&builder,
		"\nStatus: %s\nHealth: %s\nConfiguration: `%d` (`%s`)\nMode: %s\nSleep after: `%d minutes`\nArchive after: `%d days`\nTeamSpeak: %s\nUpdated: %s",
		lifecyclePresentation(session.LifecycleState),
		healthPresentation(session.HealthStatus),
		session.ConfigurationRevision,
		sanitizeCode(session.GameProfileID),
		sessionModeLabel(session.Vanilla),
		session.SleepAfterSeconds/60,
		session.ArchiveAfterSeconds/86400,
		enabledLabel(session.TeamSpeakEnabled),
		discordTimestamp(session.UpdatedAt),
	)
	if players == nil {
		builder.WriteString("\nLive players (A2S): unavailable")
		return boundDiscordContent(builder.String())
	}
	fmt.Fprintf(&builder, "\nLive players (A2S): `%d/%d`", players.PlayerCount, players.MaxPlayers)
	if len(players.PlayerNames) == 0 {
		builder.WriteString("\nPlayer names: unavailable")
	} else {
		builder.WriteString("\nPlayer names: ")
		builder.WriteString(boundedNames(players.PlayerNames))
	}
	return boundDiscordContent(builder.String())
}

func (discordRenderer) sessionList(sessions []domain.Session, page int, totalPages int, filterLabel string) string {
	if len(sessions) == 0 {
		if filterLabel == "Active sessions" {
			return "You do not have any active sessions. Use `/rb create` to create one, or choose the terminated filter to view deleted records."
		}
		return fmt.Sprintf("No sessions match **%s**. Choose another lifecycle filter.", sanitizeInline(filterLabel))
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "**Your sessions - Page %d of %d**\nFilter: %s\n\n", page, totalPages, sanitizeInline(filterLabel))
	for _, session := range sessions {
		line := fmt.Sprintf(
			"**%s**\nSlug: `%s`\nStatus: %s\n\n",
			sanitizeInline(session.DisplayName),
			sanitizeCode(session.Slug),
			lifecyclePresentation(session.LifecycleState),
		)
		if utf8.RuneCountInString(builder.String())+utf8.RuneCountInString(line) > maximumDiscordContentRunes-32 {
			builder.WriteString("…additional sessions omitted")
			break
		}
		builder.WriteString(line)
	}
	return boundDiscordContent(strings.TrimSpace(builder.String()))
}

func formatConfiguredSession(session domain.Session) string {
	return renderer.configuredSession(session)
}

func formatArtifactAccepted(kind domain.ArtifactKind, filename string) string {
	return renderer.artifactAccepted(kind, filename)
}

func formatCreatedSession(session domain.Session) string {
	return renderer.createdSession(session)
}

func formatSessionStatus(session domain.Session, players *domain.PlayerStatus) string {
	return renderer.sessionStatus(session, players)
}

func formatSessionList(sessions []domain.Session) string {
	return renderer.sessionList(sessions, 1, 1, "Active sessions")
}

func formatSessionListPage(sessions []domain.Session, page int, totalPages int, filterLabel string) string {
	return renderer.sessionList(sessions, page, totalPages, filterLabel)
}

func lifecyclePresentation(state domain.LifecycleState) string {
	switch state {
	case domain.StateDraft, domain.StateNew, domain.StateValidating, domain.StateProvisioning,
		domain.StateBootstrapping, domain.StateInstalling:
		return "Setting up"
	case domain.StateReady:
		return "Ready"
	case domain.StateWaking, domain.StateRestoring:
		return "Starting"
	case domain.StateRunning, domain.StateIdle:
		return "Running"
	case domain.StateStopping, domain.StateSleeping, domain.StateWarning1, domain.StateWarning2,
		domain.StateArchiving, domain.StateDestroying:
		return "Sleeping"
	case domain.StateArchived:
		return "Archived"
	case domain.StateFailed:
		return "Action required"
	case domain.StateDeleting, domain.StateDeleted:
		return "Terminated"
	default:
		return "Action required"
	}
}

func healthPresentation(status domain.HealthStatus) string {
	switch status {
	case domain.HealthStarting:
		return "Starting"
	case domain.HealthHealthy:
		return "Healthy"
	case domain.HealthDegraded:
		return "Degraded — action may be required"
	case domain.HealthUnhealthy:
		return "Unhealthy — action required"
	case domain.HealthStopped:
		return "Stopped"
	default:
		return "Not available"
	}
}

func discordTimestamp(value time.Time) string {
	if value.IsZero() {
		return "Not available"
	}
	unix := value.UTC().Unix()
	return fmt.Sprintf("<t:%d:F> (<t:%d:R>)", unix, unix)
}

func sanitizeInline(value string) string {
	value = normalizeSingleLine(value)
	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"<", "\\<",
		"`", "\\`",
		"*", "\\*",
		"_", "\\_",
		"~", "\\~",
		"|", "\\|",
		">", "\\>",
		"[", "\\[",
		"]", "\\]",
	)
	return replacer.Replace(value)
}

func sanitizeCode(value string) string {
	value = normalizeSingleLine(value)
	return strings.ReplaceAll(value, "`", "ˋ")
}

func normalizeSingleLine(value string) string {
	var builder strings.Builder
	lastWasSpace := true
	for _, character := range strings.TrimSpace(value) {
		switch {
		case unicode.IsSpace(character):
			if !lastWasSpace {
				builder.WriteByte(' ')
				lastWasSpace = true
			}
		case unicode.IsControl(character), unicode.Is(unicode.Cf, character):
			continue
		default:
			builder.WriteRune(character)
			lastWasSpace = false
		}
	}
	return strings.TrimSpace(builder.String())
}

func boundDiscordContent(content string) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) <= maximumDiscordContentRunes {
		return content
	}
	return strings.TrimSpace(string(runes[:maximumDiscordContentRunes-1])) + "…"
}

func boundedNames(names []string) string {
	const maxNameRunes = 64
	const maxOutputRunes = 600
	values := make([]string, 0, len(names))
	used := 0
	for _, name := range names {
		name = sanitizeInline(name)
		runes := []rune(name)
		if len(runes) > maxNameRunes {
			name = string(runes[:maxNameRunes-1]) + "…"
		}
		if name == "" {
			name = "(unnamed)"
		}
		additional := utf8.RuneCountInString(name)
		if len(values) > 0 {
			additional += 2
		}
		if used+additional > maxOutputRunes {
			values = append(values, "…")
			break
		}
		values = append(values, name)
		used += additional
	}
	if len(values) == 0 {
		return "unavailable"
	}
	return strings.Join(values, ", ")
}

func sessionModeLabel(vanilla bool) string {
	if vanilla {
		return "Vanilla"
	}
	return "Modded"
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "Enabled"
	}
	return "Disabled"
}

func presetInstruction(vanilla bool) string {
	if vanilla {
		return ""
	}
	return " and preset"
}

func titleCase(value string) string {
	if value == "" {
		return value
	}
	runes := []rune(value)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
