// Package sessioncard owns the single bounded projection and rendering source
// shared by public Discord cards and private detailed status responses.
package sessioncard

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const maximumContentRunes = 1900

// RenderPublic renders the concise public form of an authoritative projection.
func RenderPublic(card Projection) string {
	return render(card, false)
}

// RenderDetailed renders the private progressive-disclosure form of the same
// projection. It may include bounded player names but never internal IDs.
func RenderDetailed(card Projection) string {
	return render(card, true)
}

// RenderSetup remains as a compatibility wrapper for in-flight Step 12.3
// callers. New callers should explicitly Project and RenderPublic.
func RenderSetup(session domain.Session, now time.Time) string {
	return RenderPublic(Project(session, Options{Now: now}))
}

func render(card Projection, detailed bool) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "## %s: %s", safe(card.Lifecycle), safe(card.Name))
	if detailed {
		fmt.Fprintf(&builder, "\nSlug: `%s`", safeCode(card.Slug))
	} else {
		fmt.Fprintf(&builder, "\n`%s`", safeCode(card.Slug))
	}
	if strings.TrimSpace(card.Description) != "" {
		if detailed {
			fmt.Fprintf(&builder, "\nDescription: %s", safe(card.Description))
		} else {
			fmt.Fprintf(&builder, "\n%s", safe(card.Description))
		}
	}
	fmt.Fprintf(
		&builder,
		"\n\n**Game:** %s\n**Mode:** %s\n**TeamSpeak:** %s\nStatus: %s\nHealth: %s\nStage: %s",
		safe(card.Game), safe(card.Mode), enabled(card.TeamSpeak), safe(card.Lifecycle), safe(card.Health), safe(card.Stage),
	)
	if card.CurrentOperation != "" {
		fmt.Fprintf(&builder, "\n**Current operation:** %s", safe(card.CurrentOperation))
	}
	if card.Elapsed > 0 {
		fmt.Fprintf(&builder, "\n**Elapsed:** %s", formatDuration(card.Elapsed))
	}
	if card.Endpoints.Game.Available {
		fmt.Fprintf(&builder, "\n\n%s", connectionLine("Arma", card.Endpoints.Game))
	}
	if card.Endpoints.TeamSpeak.Available {
		fmt.Fprintf(&builder, "\n%s", connectionLine("TeamSpeak", card.Endpoints.TeamSpeak))
	}

	fmt.Fprintf(
		&builder,
		"\n\n**Mission:** %s\n**Preset:** %s\n**Mods:** %s",
		artifactLine(card.Artifacts.Mission), artifactLine(card.Artifacts.Preset), safe(card.Mods.Status),
	)
	if card.Mods.ActiveRevision != "" {
		fmt.Fprintf(&builder, "\n**Active mod revision:** `%s`", safeCode(card.Mods.ActiveRevision))
	}
	if card.Mods.PendingRevision != "" {
		fmt.Fprintf(&builder, "\n**Pending mod revision:** `%s`", safeCode(card.Mods.PendingRevision))
	}
	if card.Mods.DownloadURL != "" {
		fmt.Fprintf(&builder, "\n%s", modlistLinkLine(card.Mods.DownloadURL))
	}

	if card.Players.Available {
		if detailed {
			fmt.Fprintf(&builder, "\n\nLive players (A2S): `%d/%d`\nPlayer names: %s", card.Players.Count, card.Players.Capacity, boundedNames(card.Players.Names))
		} else {
			fmt.Fprintf(&builder, "\n\n**Players:** `%d/%d`", card.Players.Count, card.Players.Capacity)
		}
	} else if detailed {
		builder.WriteString("\n\nLive players (A2S): unavailable")
	}

	if card.Failure.Present {
		fmt.Fprintf(&builder, "\n\n**Action required:** %s\n%s", safe(card.Failure.Summary), safePreservingCode(card.Failure.Action))
		if card.Failure.ResourcesMayExist {
			builder.WriteString("\nResources may still exist and incur cost.")
		}
	}

	if !card.Freshness.SessionUpdatedAt.IsZero() {
		if detailed {
			fmt.Fprintf(&builder, "\n\nUpdated: %s", detailedTimestamp(card.Freshness.SessionUpdatedAt))
		} else {
			fmt.Fprintf(&builder, "\n\nLast updated %s.", timestamp(card.Freshness.SessionUpdatedAt))
		}
	}
	if detailed && !card.Freshness.InfrastructureObservedAt.IsZero() {
		fmt.Fprintf(&builder, "\nInfrastructure observed %s.", timestamp(card.Freshness.InfrastructureObservedAt))
	}
	if detailed && !card.Freshness.PlayersObservedAt.IsZero() {
		fmt.Fprintf(&builder, "\nPlayers observed %s.", timestamp(card.Freshness.PlayersObservedAt))
	}
	return bound(builder.String())
}

// RenderModlistMessage renders the stable companion message that owns the
// downloadable attachment rather than repeatedly attaching it to the card.
func RenderModlistMessage(session domain.Session, filename string, workshopCount int, now time.Time) string {
	content := fmt.Sprintf(
		"## Active Arma 3 modlist: %s\n`%s`\n%d Steam Workshop mod%s. Download this file and import it from Mods / Preset / Import in Arma 3 Launcher.\n\nPublished %s.",
		safe(session.DisplayName), safeCode(filename), workshopCount, plural(workshopCount), timestamp(now),
	)
	return bound(content)
}

// WithModlistLink enriches an already-rendered canonical card at the delivery
// boundary, where the stable Discord message ID is finally known.
func WithModlistLink(content, messageURL string) string {
	messageURL = normalizeModlistURL(messageURL)
	if messageURL == "" {
		return bound(content)
	}
	lines := strings.Split(strings.TrimSpace(content), "\n")
	filtered := make([]string, 0, len(lines)+1)
	insertAt := -1
	for _, line := range lines {
		if strings.HasPrefix(line, "**Active modlist:**") {
			continue
		}
		filtered = append(filtered, line)
		if strings.HasPrefix(line, "**Mods:**") {
			insertAt = len(filtered)
		}
	}
	link := modlistLinkLine(messageURL)
	if insertAt < 0 {
		filtered = append(filtered, "", link)
	} else {
		filtered = append(filtered, "")
		copy(filtered[insertAt+1:], filtered[insertAt:])
		filtered[insertAt] = link
	}
	return bound(strings.Join(filtered, "\n"))
}

func modlistLinkLine(messageURL string) string {
	return "**Active modlist:** [Download and import](" + messageURL + ")"
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func connectionLine(label string, connection ConnectionProjection) string {
	addressType := strings.TrimSpace(connection.AddressType)
	if addressType != "DNS" && addressType != "IP" {
		addressType = "address"
	}
	line := fmt.Sprintf("**%s %s:** `%s:%d`", label, addressType, safeCode(connection.Host), connection.Port)
	if connection.Offline {
		line += " — Offline (retained address)"
	}
	return line
}

func artifactLine(artifact ArtifactView) string {
	if artifact.Status != "Rejected" || strings.TrimSpace(artifact.Issue) == "" {
		return safe(artifact.Status)
	}
	return "Rejected — " + safe(artifact.Issue) + " Replacement is required before this draft can be ready."
}

func enabled(value bool) string {
	if value {
		return "On"
	}
	return "Off"
}

func formatDuration(value time.Duration) string {
	if value < 0 {
		value = 0
	}
	value = value.Round(time.Second)
	hours := int(value / time.Hour)
	minutes := int(value%time.Hour) / int(time.Minute)
	seconds := int(value%time.Minute) / int(time.Second)
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func timestamp(value time.Time) string {
	return fmt.Sprintf("<t:%d:R>", value.UTC().Unix())
}

func detailedTimestamp(value time.Time) string {
	unix := value.UTC().Unix()
	return fmt.Sprintf("<t:%d:F> (<t:%d:R>)", unix, unix)
}

func safe(value string) string {
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
			if strings.ContainsRune("\\`*_{}[]()#+|>~<", character) {
				builder.WriteByte('\\')
			}
			builder.WriteRune(character)
			lastWasSpace = false
		}
	}
	return strings.ReplaceAll(strings.TrimSpace(builder.String()), "@", "@\u200b")
}

func safeCode(value string) string {
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) || character == '`' {
			return -1
		}
		return character
	}, strings.TrimSpace(value))
	return strings.ReplaceAll(value, "@", "@\u200b")
}

func safePreservingCode(value string) string {
	return strings.ReplaceAll(safe(value), "\\`", "`")
}

func boundedNames(names []string) string {
	const maximumNameRunes = 64
	const maximumOutputRunes = 600
	values := make([]string, 0, len(names))
	used := 0
	for _, name := range names {
		name = safe(name)
		runes := []rune(name)
		if len(runes) > maximumNameRunes {
			name = string(runes[:maximumNameRunes-1]) + "…"
		}
		if name == "" {
			name = "(unnamed)"
		}
		additional := len([]rune(name))
		if len(values) > 0 {
			additional += 2
		}
		if used+additional > maximumOutputRunes {
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

func bound(content string) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= maximumContentRunes {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:maximumContentRunes-1])) + "…"
}
