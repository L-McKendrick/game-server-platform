// Package sessioncard builds the bounded public setup projection shared by
// interaction and worker adapters. The richer lifecycle projection replaces
// this setup-only view later in Phase 12.
package sessioncard

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// RenderSetup returns a public, mention-safe setup card for one session.
func RenderSetup(session domain.Session, now time.Time) string {
	mode := "Modded"
	if session.Vanilla {
		mode = "Vanilla"
	}
	voice := "Off"
	if session.TeamSpeakEnabled {
		voice = "On"
	}
	mission := artifactLine("Mission", session.MissionArtifactStatus, session.MissionObjectKey, session.MissionArtifactIssue, false)
	preset := artifactLine("Preset", session.PresetArtifactStatus, session.PresetObjectKey, session.PresetArtifactIssue, session.Vanilla)
	content := fmt.Sprintf(
		"## Setting up: %s\n`%s`%s\n\n**Game:** Arma 3\n**Mode:** %s\n**TeamSpeak:** %s\n**Mission:** %s\n**Preset:** %s\n\nUploads are validated asynchronously. Last updated <t:%d:R>.",
		safe(session.DisplayName), safeCode(session.Slug), description(session.Description), mode, voice,
		mission, preset, now.UTC().Unix(),
	)
	if len(content) > 1900 {
		return content[:1900]
	}
	return content
}

func artifactLine(label string, status domain.ArtifactStatus, objectKey, issue string, notRequired bool) string {
	switch status {
	case domain.ArtifactPending:
		return "Queued for validation"
	case domain.ArtifactAccepted:
		return "Accepted"
	case domain.ArtifactRejected:
		return "Rejected — " + safe(issue) + " Replacement is required before this draft can be ready."
	default:
		if strings.TrimSpace(objectKey) != "" {
			return "Accepted"
		}
		if notRequired {
			return "Not required for vanilla"
		}
		if label == "Preset" {
			return "Awaiting validation or not provided"
		}
		return "Awaiting validation"
	}
}

func description(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "\n" + safe(value)
}

func safe(value string) string {
	var builder strings.Builder
	for _, character := range value {
		switch {
		case unicode.IsControl(character), unicode.Is(unicode.Cf, character):
			continue
		case strings.ContainsRune("\\`*_{}[]()#+-.!|>~", character):
			builder.WriteByte('\\')
		}
		builder.WriteRune(character)
	}
	return strings.ReplaceAll(builder.String(), "@", "@\u200b")
}

func safeCode(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "`", ""), "@", "@\u200b")
}
