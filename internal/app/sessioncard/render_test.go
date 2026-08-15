package sessioncard

import (
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestRenderSetupShowsDurableArtifactOutcomesWithoutMentions(t *testing.T) {
	t.Parallel()
	session := domain.Session{
		DisplayName: "@everyone *Friday*", Slug: "friday", Description: "Co-op", Vanilla: false,
		MissionArtifactStatus: domain.ArtifactAccepted,
		PresetArtifactStatus:  domain.ArtifactRejected, PresetArtifactIssue: "local mod path",
	}
	content := RenderSetup(session, time.Unix(1_800_000_000, 0))
	for _, expected := range []string{"Setting up", "Mission:** Accepted", "Preset:** Rejected", "Replacement is required", "<t:1800000000:R>"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("content = %q; missing %q", content, expected)
		}
	}
	if strings.Contains(content, "@everyone") {
		t.Fatalf("content contains an active mention: %q", content)
	}
}

func TestRenderSetupTreatsLegacyArtifactObjectKeysAsAccepted(t *testing.T) {
	t.Parallel()
	session := domain.Session{
		DisplayName: "Legacy", Slug: "legacy",
		MissionObjectKey: "sessions/legacy/input/mission.pbo",
		PresetObjectKey:  "sessions/legacy/input/preset.html",
	}
	content := RenderSetup(session, time.Unix(1_800_000_000, 0))
	if strings.Count(content, "Accepted") != 2 || strings.Contains(content, "Awaiting validation") {
		t.Fatalf("legacy setup card = %q; want both artifacts accepted", content)
	}
}
