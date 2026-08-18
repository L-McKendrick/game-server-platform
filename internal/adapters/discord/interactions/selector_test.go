package interactions

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	appsession "github.com/L-McKendrick/game-server-platform/internal/app/sessions"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestSessionSelectionLabelNormalizesAndBoundsUnicode(t *testing.T) {
	t.Parallel()

	label := sessionSelectionLabel(appsession.Selection{
		DisplayName:    strings.Repeat("🎮", 120) + "\n@everyone\u200b",
		Slug:           "saturday-arma",
		LifecycleState: domain.StateRunning,
	})
	if utf8.RuneCountInString(label) != maximumAutocompleteLabelRunes {
		t.Fatalf("label rune count = %d; want %d: %q", utf8.RuneCountInString(label), maximumAutocompleteLabelRunes, label)
	}
	for _, character := range label {
		if unicode.IsControl(character) || unicode.Is(unicode.Cf, character) {
			t.Fatalf("label contains unsafe character %U: %q", character, label)
		}
	}
	if !strings.HasSuffix(label, " — saturday-arma — Running") {
		t.Fatalf("label = %q; want readable suffix", label)
	}
}
