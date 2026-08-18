package sessioncard

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strings"
)

const controlTokenDigestBytes = 18

var controlTokenPattern = regexp.MustCompile(`^S_[A-Za-z0-9_-]{24}$`)

// ControlToken returns a stable one-way reference suitable for a Discord
// custom ID. The immutable session ID cannot be recovered from the token.
func ControlToken(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	digest := sha256.Sum256([]byte("session-card:" + sessionID))
	return "S_" + base64.RawURLEncoding.EncodeToString(digest[:controlTokenDigestBytes])
}

func ValidControlToken(token string) bool {
	return controlTokenPattern.MatchString(strings.TrimSpace(token))
}
