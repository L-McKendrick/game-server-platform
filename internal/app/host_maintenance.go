package app

import (
	"context"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"time"
)

type HostAttemptMaintenance struct {
	Issuer  HostManifestIssuer
	Lister  ports.HostAccessAttemptLister
	Cleaner ports.HostAccessAttemptCleaner
}

func (maintenance HostAttemptMaintenance) CleanupSessionHostAttempts(ctx context.Context, sessionID string) (int, error) {
	if maintenance.Lister == nil || maintenance.Cleaner == nil || maintenance.Issuer.Inputs.Authority.Records == nil {
		return 0, domain.ErrConflict
	}
	session, err := maintenance.Issuer.Inputs.Authority.Records.Get(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	if session.ID != sessionID {
		return 0, domain.ErrConflict
	}
	attempts, err := maintenance.Lister.ListHostAccessAttempts(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, attempt := range attempts {
		if attempt.Validate() != nil || attempt.Scope.SessionID != session.ID || attempt.Scope.GuildID != session.GuildID {
			return deleted, domain.ErrConflict
		}
		if !time.Now().UTC().After(attempt.Scope.DeadlineAt.Add(domain.HostAccessCredentialMargin)) {
			continue
		}
		// Tagged current/noncurrent expiry is the backstop for old or arbitrarily
		// late uploads. Avoid paying for empty historical sweeps indefinitely.
		if time.Now().UTC().After(attempt.Scope.DeadlineAt.Add(4 * 24 * time.Hour)) {
			continue
		}
		count, err := maintenance.Issuer.CleanupExpiredAttempt(ctx, attempt.Scope, maintenance.Cleaner)
		deleted += count
		if err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}
