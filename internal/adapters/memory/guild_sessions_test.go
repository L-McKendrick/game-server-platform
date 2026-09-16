package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

func TestListGuildSessionsPagesBeyondOneHundredAndIsolatesGuild(t *testing.T) {
	repository := NewSessionRepository()
	now := time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)
	for index := 0; index < 135; index++ {
		id := fmt.Sprintf("session-%03d", index)
		repository.sessions[id] = domain.Session{ID: id, GuildID: "guild-1", LifecycleState: domain.StateRunning, UpdatedAt: now.Add(time.Duration(index) * time.Second)}
	}
	repository.sessions["other-guild"] = domain.Session{ID: "other-guild", GuildID: "guild-2", LifecycleState: domain.StateRunning, UpdatedAt: now.Add(time.Hour)}

	seen := map[string]struct{}{}
	cursor := ""
	for {
		page, err := repository.ListGuildSessions(context.Background(), "guild-1", []domain.LifecycleState{domain.StateRunning}, ports.GuildSessionPage{Size: 37, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, session := range page.Sessions {
			if session.GuildID != "guild-1" {
				t.Fatalf("cross-guild session returned: %#v", session)
			}
			if _, duplicate := seen[session.ID]; duplicate {
				t.Fatalf("duplicate session %s", session.ID)
			}
			seen[session.ID] = struct{}{}
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 135 {
		t.Fatalf("listed %d sessions, want 135", len(seen))
	}
}
