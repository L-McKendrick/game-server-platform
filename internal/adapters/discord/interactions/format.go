package interactions

import (
	"fmt"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func formatConfiguredSession(session domain.Session) string {
	return fmt.Sprintf(
		"**Session configured**\nID: `%s`\nRevision: `%d`\nProfile: `%s`\nVanilla: `%t`\nSleep after: `%d minutes`\nArchive after: `%d days`\nTeamSpeak: `%t`",
		sanitizeInline(session.ID),
		session.ConfigurationRevision,
		sanitizeInline(session.GameProfileID),
		session.Vanilla,
		session.SleepAfterSeconds/60,
		session.ArchiveAfterSeconds/86400,
		session.TeamSpeakEnabled,
	)
}

func formatArtifactAccepted(kind domain.ArtifactKind, filename string, sessionID string) string {
	return fmt.Sprintf(
		"**%s accepted for validation**\nFile: `%s`\nSession: `%s`\nThe platform will report the validation result asynchronously.",
		strings.ToLower(string(kind)),
		sanitizeInline(filename),
		sanitizeInline(sessionID),
	)
}

func formatCreatedSession(session domain.Session) string {
	return fmt.Sprintf(
		"**Draft session created**\nName: %s\nID: `%s`\nState: `%s`\nGame: `%s`",
		sanitizeInline(session.DisplayName),
		sanitizeInline(session.ID),
		session.LifecycleState,
		sanitizeInline(session.GameType),
	)
}

func formatSessionStatus(session domain.Session, players *domain.PlayerStatus) string {
	status := fmt.Sprintf(
		"**%s**\nID: `%s`\nSlug: `%s`\nLifecycle: `%s`\nHealth: `%s`\nConfiguration: `%d` (`%s`)\nVanilla: `%t`\nSleep after: `%d minutes`\nArchive after: `%d days`\nTeamSpeak: `%t`\nVersion: `%d`",
		sanitizeInline(session.DisplayName),
		sanitizeInline(session.ID),
		sanitizeInline(session.Slug),
		session.LifecycleState,
		session.HealthStatus,
		session.ConfigurationRevision,
		sanitizeInline(session.GameProfileID),
		session.Vanilla,
		session.SleepAfterSeconds/60,
		session.ArchiveAfterSeconds/86400,
		session.TeamSpeakEnabled,
		session.Version,
	)
	if players == nil {
		return status + "\nLive players (A2S): unavailable"
	}
	status += fmt.Sprintf("\nLive players (A2S): `%d/%d`", players.PlayerCount, players.MaxPlayers)
	if len(players.PlayerNames) == 0 {
		return status + "\nPlayer names: unavailable"
	}
	return status + "\nPlayer names: " + boundedNames(players.PlayerNames)
}

func boundedNames(names []string) string {
	const maxNameRunes = 64
	const maxOutputBytes = 600
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
		additional := len(name)
		if len(values) > 0 {
			additional += 2
		}
		if used+additional > maxOutputBytes {
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

func formatSessionList(sessions []domain.Session) string {
	if len(sessions) == 0 {
		return "You do not have any sessions yet. Use `/session create` to create one."
	}

	var builder strings.Builder
	builder.WriteString("**Your sessions**\n")

	for _, session := range sessions {
		line := fmt.Sprintf(
			"• %s — `%s` — `%s`\n",
			sanitizeInline(session.DisplayName),
			session.LifecycleState,
			sanitizeInline(session.ID),
		)

		if builder.Len()+len(line) > maximumResponseLength {
			builder.WriteString("…additional sessions omitted")
			break
		}

		builder.WriteString(line)
	}

	return strings.TrimSpace(builder.String())
}

func sanitizeInline(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "`", "ˋ")
	return value
}
