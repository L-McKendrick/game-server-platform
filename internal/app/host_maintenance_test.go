package app

import (
	"context"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"strings"
	"testing"
	"time"
)

type attemptList struct {
	attempts []domain.HostAccessAttempt
	calls    int
}

func (list *attemptList) ListHostAccessAttempts(context.Context, string) ([]domain.HostAccessAttempt, error) {
	list.calls++
	return list.attempts, nil
}

func TestMaintenanceSweepsOnlyExpiredPersistedOwnerAndKeepsReplayMetadata(t *testing.T) {
	now := time.Now().UTC()
	scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: now.Add(-time.Hour)}
	expired := domain.HostAccessAttempt{Scope: scope, Revision: 1, Generation: 1, ManifestVersionID: "version", ManifestSHA256: manifestDigest([]byte("manifest")), ManifestSizeBytes: 8, ExpiresAt: scope.DeadlineAt.Add(-time.Minute), DispatchState: domain.HostAccessFinished}
	alive := expired
	alive.Scope.AttemptID = "live"
	alive.Scope.DeadlineAt = now.Add(time.Hour)
	alive.ExpiresAt = now.Add(10 * time.Minute)
	alive.DispatchState = domain.HostAccessPrepared
	old := expired
	old.Scope.AttemptID = "old"
	old.Scope.DeadlineAt = now.Add(-10 * 24 * time.Hour)
	old.ExpiresAt = old.Scope.DeadlineAt.Add(-time.Minute)
	records := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", LifecycleState: domain.StateDeleted}}
	recovery := &manifestRecovery{current: expired}
	list := &attemptList{attempts: []domain.HostAccessAttempt{alive, expired, old}}
	cleaner := &attemptCleaner{}
	maintenance := HostAttemptMaintenance{Issuer: HostManifestIssuer{Inputs: HostObjectIssuer{Authority: HostAccessAuthority{Records: records}}, Attempts: recovery}, Lister: list, Cleaner: cleaner}
	for pass := 0; pass < 2; pass++ {
		if count, err := maintenance.CleanupSessionHostAttempts(context.Background(), "session"); err != nil || count != 2 {
			t.Fatal("owned cleanup failed", err)
		}
	}
	if cleaner.calls != 2 || recovery.current.Revision != 1 {
		t.Fatal("live attempt deleted or recovery metadata erased")
	}
	list.attempts[0].Scope.GuildID = "foreign"
	if _, err := maintenance.CleanupSessionHostAttempts(context.Background(), "session"); err == nil || cleaner.calls != 2 {
		t.Fatal("foreign owner cleanup allowed")
	}
}
