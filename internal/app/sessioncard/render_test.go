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
	for _, expected := range []string{"Setting up", "Mission:** Accepted", "Preset:** Rejected", "Replacement is required"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("content = %q; missing %q", content, expected)
		}
	}
	if strings.Contains(content, "@everyone") {
		t.Fatalf("content contains an active mention: %q", content)
	}
	if strings.Contains(content, "Last updated") || strings.Contains(content, "**Guidance:**") {
		t.Fatalf("public fallback contains removed card metadata: %q", content)
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
	if !strings.Contains(content, "Mission:** Accepted") || !strings.Contains(content, "Preset:** Accepted") ||
		strings.Contains(content, "Awaiting upload") {
		t.Fatalf("legacy setup card = %q; want both artifacts accepted", content)
	}
}

func TestRenderModlistMessageAndCardLinkAreSafeAndIdempotent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 13, 30, 0, 0, time.UTC)
	message := RenderModlistMessage(domain.Session{DisplayName: "@everyone Saturday Ops"}, "saturday-ops-modlist.html", 2, now)
	if strings.Contains(message, "@everyone") || !strings.Contains(message, "2 Steam Workshop mods") ||
		!strings.Contains(message, "Mods / Preset / Import") {
		t.Fatalf("modlist message = %q", message)
	}

	card := "## Saturday Ops\n\n**Mission:** Accepted\n**Preset:** Accepted\n**Mods:** Accepted\n\nLast updated now."
	url := "https://discord.com/channels/guild-1/channel-1/message-1"
	linked := WithModlistLink(card, url)
	replayed := WithModlistLink(linked, url)
	if linked != replayed || strings.Count(linked, "**Active modlist:**") != 1 || !strings.Contains(linked, url) {
		t.Fatalf("linked card=%q replayed=%q", linked, replayed)
	}
	if got := WithModlistLink(card, "https://example.test/steal"); got != card {
		t.Fatalf("untrusted link changed card: %q", got)
	}
}

func TestServerModsAppearOnlyInDetailedStatus(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 8, 0, 0, 0, time.UTC)
	session := domain.Session{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", ServerPresetArtifactStatus: domain.ArtifactRejected, ServerPresetArtifactIssue: "private server preset rejected", UpdatedAt: now}
	card := Project(session, Options{Now: now})
	if public := RenderPublic(card); strings.Contains(public, "Server-only mods") || strings.Contains(public, "private server preset") {
		t.Fatalf("public card leaked server mods: %s", public)
	}
	if detailed := RenderDetailed(card); !strings.Contains(detailed, "Server-only mods") || !strings.Contains(detailed, "private server preset rejected") {
		t.Fatalf("detailed status omitted server mods: %s", detailed)
	}
}
