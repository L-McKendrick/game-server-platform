package sessioncard

import (
	"strings"
	"testing"
)

func TestControlTokenIsStableBoundedAndDoesNotExposeSessionID(t *testing.T) {
	t.Parallel()
	sessionID := "01KTESTSESSION000000000001"
	first := ControlToken(sessionID)
	second := ControlToken("  " + sessionID + "  ")
	if first == "" || first != second || len(first) > 64 || strings.Contains(first, sessionID) {
		t.Fatalf("ControlToken() = %q / %q", first, second)
	}
	if !ValidControlToken(first) || ValidControlToken("StaleTok") {
		t.Fatalf("control token validation accepted unexpected values: %q", first)
	}
	if first == ControlToken(sessionID+"2") {
		t.Fatal("different session IDs produced the same test token")
	}
	if ControlToken(" ") != "" {
		t.Fatal("empty session ID produced a token")
	}
}
