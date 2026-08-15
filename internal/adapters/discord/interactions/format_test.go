package interactions

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestFormatSessionStatus_IncludesLivePlayersAndNames(t *testing.T) {
	t.Parallel()
	content := formatSessionStatus(domain.Session{DisplayName: "Saturday Arma", ID: "session-1"}, &domain.PlayerStatus{
		PlayerCount: 2,
		MaxPlayers:  32,
		PlayerNames: []string{"Alice", "Bob"},
	})
	if !strings.Contains(content, "Live players (A2S): `2/32`") || !strings.Contains(content, "Player names: Alice, Bob") {
		t.Fatalf("formatSessionStatus() = %q", content)
	}
}

func TestDiscordRendererUsesSafeReadableSessionPresentation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 12, 30, 0, 0, time.UTC)
	session := domain.Session{
		ID: "immutable-session-id", DisplayName: "*Saturday*\n@everyone", Slug: "saturday-arma",
		Description: "Weekly *co-op*",
		GameType:    "arma3", GameProfileID: "arma3-default", LifecycleState: domain.StateRunning,
		HealthStatus: domain.HealthHealthy, SleepAfterSeconds: 1800, ArchiveAfterSeconds: 604800,
		UpdatedAt: now,
	}

	content := renderer.sessionStatus(session, nil)
	for _, expected := range []string{
		`\*Saturday\* @everyone`, "Slug: `saturday-arma`", `Description: Weekly \*co-op\*`, "Status: Running", "Health: Healthy",
		"<t:1786710600:F> (<t:1786710600:R>)",
	} {
		if !strings.Contains(content, expected) {
			t.Errorf("content missing %q: %q", expected, content)
		}
	}
	if strings.Contains(content, session.ID) || strings.Contains(content, "\n@everyone") {
		t.Fatalf("content exposes internal identity or multiline input: %q", content)
	}
}

func TestLifecyclePresentationUsesSharedAccessibleVocabulary(t *testing.T) {
	t.Parallel()
	tests := map[domain.LifecycleState]string{
		domain.StateDraft: "Setting up", domain.StateReady: "Ready", domain.StateWaking: "Starting",
		domain.StateRunning: "Running", domain.StateSleeping: "Sleeping", domain.StateArchived: "Archived",
		domain.StateFailed: "Action required", domain.StateDeleted: "Terminated",
	}
	for state, expected := range tests {
		if actual := lifecyclePresentation(state); actual != expected {
			t.Errorf("lifecyclePresentation(%q) = %q; want %q", state, actual, expected)
		}
	}
}

func TestDiscordRendererBoundsUnicodeContentAndSuppressesMentions(t *testing.T) {
	t.Parallel()
	data := renderer.messageData(strings.Repeat("🎮", maximumDiscordContentRunes+20), messageFlagEphemeral, nil)
	if count := utf8.RuneCountInString(data.Content); count != maximumDiscordContentRunes {
		t.Fatalf("content rune count = %d; want %d", count, maximumDiscordContentRunes)
	}
	if !utf8.ValidString(data.Content) || !strings.HasSuffix(data.Content, "…") {
		t.Fatalf("bounded content is invalid or lacks omission marker")
	}
	if data.AllowedMentions == nil || data.AllowedMentions.Parse == nil || len(data.AllowedMentions.Parse) != 0 {
		t.Fatalf("allowed mentions = %#v; want explicit empty parse list", data.AllowedMentions)
	}
}

func TestDiscordRendererBoundsLongSessionListWithoutExposingIDs(t *testing.T) {
	t.Parallel()

	sessions := make([]domain.Session, 0, 100)
	for index := 0; index < 100; index++ {
		sessions = append(sessions, domain.Session{
			ID:             "immutable-secret-" + strings.Repeat("x", index+1),
			DisplayName:    strings.Repeat("🎮", 100),
			Slug:           "session-slug",
			LifecycleState: domain.StateRunning,
		})
	}
	content := renderer.sessionList(sessions, 1, 20, "Active sessions")
	if utf8.RuneCountInString(content) > maximumDiscordContentRunes || !utf8.ValidString(content) {
		t.Fatalf("session list is not a valid bounded Discord response")
	}
	if !strings.Contains(content, "additional sessions omitted") || strings.Contains(content, "immutable-secret") {
		t.Fatalf("session list omission or ID safety failed: %q", content)
	}
}

func TestFormatSessionStatus_QueryUnavailableIsNotShownAsZero(t *testing.T) {
	t.Parallel()
	content := formatSessionStatus(domain.Session{DisplayName: "Saturday Arma", ID: "session-1"}, nil)
	if !strings.Contains(content, "Live players (A2S): unavailable") || strings.Contains(content, "Live players (A2S): `0/") {
		t.Fatalf("formatSessionStatus() = %q", content)
	}
}
