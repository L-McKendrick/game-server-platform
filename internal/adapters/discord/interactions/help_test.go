package interactions

import (
	"strings"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestSessionNextActionIsStateAwareAndNeverImpliesRetry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state domain.LifecycleState
		want  string
	}{
		{domain.StateDraft, "/rb setup"},
		{domain.StateNew, "/rb start"},
		{domain.StateProvisioning, "No second operation"},
		{domain.StateRunning, "/rb sleep"},
		{domain.StateSleeping, "/rb wake"},
		{domain.StateArchived, "/rb restore"},
		{domain.StateFailed, "No automatic retry is scheduled"},
		{domain.StateDeleting, "no scheduled retry"},
		{domain.StateDeleted, "/rb create"},
	}
	for _, test := range tests {
		message := sessionNextAction(domain.Session{LifecycleState: test.state})
		if !strings.Contains(message, test.want) {
			t.Errorf("state %s action = %q; want %q", test.state, message, test.want)
		}
	}
}

func TestSessionNextActionDistinguishesPendingAndRejectedDrafts(t *testing.T) {
	t.Parallel()
	pending := sessionNextAction(domain.Session{LifecycleState: domain.StateDraft, MissionArtifactStatus: domain.ArtifactPending})
	rejected := sessionNextAction(domain.Session{LifecycleState: domain.StateDraft, MissionArtifactStatus: domain.ArtifactRejected})
	if !strings.Contains(pending, "validation finishes") || !strings.Contains(pending, "no infrastructure") {
		t.Fatalf("pending action = %q", pending)
	}
	if !strings.Contains(rejected, "/rb setup") || !strings.Contains(rejected, "no scheduled retry") {
		t.Fatalf("rejected action = %q", rejected)
	}
}
