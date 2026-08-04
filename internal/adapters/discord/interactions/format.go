package interactions

import (
	"fmt"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func formatCreatedSession(session domain.Session) string {
	return fmt.Sprintf(
		"**Draft session created**\nName: %s\nID: `%s`\nState: `%s`\nGame: `%s`",
		sanitizeInline(session.DisplayName),
		sanitizeInline(session.ID),
		session.LifecycleState,
		sanitizeInline(session.GameType),
	)
}

func formatSessionStatus(session domain.Session) string {
	return fmt.Sprintf(
		"**%s**\nID: `%s`\nSlug: `%s`\nLifecycle: `%s`\nHealth: `%s`\nVersion: `%d`",
		sanitizeInline(session.DisplayName),
		sanitizeInline(session.ID),
		sanitizeInline(session.Slug),
		session.LifecycleState,
		session.HealthStatus,
		session.Version,
	)
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
