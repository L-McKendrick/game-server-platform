package sessioncard

import (
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// ControlToken returns a stable one-way reference suitable for a Discord
// custom ID. The immutable session ID cannot be recovered from the token.
func ControlToken(sessionID string) string {
	return domain.SessionCardControlToken(sessionID)
}

func ValidControlToken(token string) bool {
	return domain.ValidSessionCardControlToken(token)
}
