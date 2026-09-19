package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type attemptCleaner struct{ calls int }

func (cleaner *attemptCleaner) DeleteHostAccessAttempt(context.Context, domain.HostAccessScope) (int, error) {
	cleaner.calls++
	return 2, nil
}

func TestExpiredAttemptCleanupChecksPersistedOwnershipAfterDeletion(t *testing.T) {
	now := time.Now().UTC()
	scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: now.Add(-time.Hour)}
	recovery := &manifestRecovery{current: domain.HostAccessAttempt{Scope: scope, Revision: 1, Generation: 1, ManifestVersionID: "version", ManifestSHA256: manifestDigest([]byte("manifest")), ManifestSizeBytes: 8, ExpiresAt: scope.DeadlineAt.Add(-time.Minute), DispatchState: domain.HostAccessFinished}}
	records := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", LifecycleState: domain.StateDeleted}}
	issuer := HostManifestIssuer{Inputs: HostObjectIssuer{Authority: HostAccessAuthority{Records: records}}, Attempts: recovery}
	cleaner := &attemptCleaner{}
	for pass := 0; pass < 2; pass++ {
		if count, err := issuer.CleanupExpiredAttempt(context.Background(), scope, cleaner); err != nil || count != 2 {
			t.Fatalf("count=%d error=%v", count, err)
		}
	}
	if recovery.current.Revision != 1 {
		t.Fatal("cleanup erased recovery ownership")
	}
	wrong := scope
	wrong.AttemptID = "other"
	if _, err := issuer.CleanupExpiredAttempt(context.Background(), wrong, cleaner); err == nil {
		t.Fatal("deleted unrelated attempt")
	}
	records.session.GuildID = "other"
	if _, err := issuer.CleanupExpiredAttempt(context.Background(), scope, cleaner); err == nil {
		t.Fatal("deleted mismatched guild ownership")
	}
	active := scope
	active.DeadlineAt = now.Add(time.Hour)
	if _, err := issuer.CleanupExpiredAttempt(context.Background(), active, cleaner); err == nil {
		t.Fatal("deleted unexpired attempt")
	}
	if cleaner.calls != 2 {
		t.Fatal("denied cleanup reached destructive adapter")
	}
}
