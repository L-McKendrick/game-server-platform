package interactions

import (
	"strings"
	"testing"

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

func TestFormatSessionStatus_QueryUnavailableIsNotShownAsZero(t *testing.T) {
	t.Parallel()
	content := formatSessionStatus(domain.Session{DisplayName: "Saturday Arma", ID: "session-1"}, nil)
	if !strings.Contains(content, "Live players (A2S): unavailable") || strings.Contains(content, "Live players (A2S): `0/") {
		t.Fatalf("formatSessionStatus() = %q", content)
	}
}
