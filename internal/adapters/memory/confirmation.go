package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

var _ ports.ConfirmationRepository = (*SessionRepository)(nil)

func (repository *SessionRepository) CreateConfirmation(ctx context.Context, confirmation domain.Confirmation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := confirmation.Validate(); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if existing, found := repository.confirmations[confirmation.Code]; found {
		if existing.ID == confirmation.ID ||
			(existing.Status == domain.ConfirmationPending && confirmation.CreatedAt.Before(existing.ExpiresAt)) ||
			(existing.Status == domain.ConfirmationConsumed && existing.SessionID == confirmation.SessionID && existing.BoundVersion == confirmation.BoundVersion) {
			return domain.ErrAlreadyExists
		}
	}
	session, found := repository.sessions[confirmation.SessionID]
	if !found {
		return domain.ErrNotFound
	}
	if !confirmationMatchesSession(confirmation, session) {
		return domain.ErrConfirmationStateDrift
	}
	repository.confirmations[confirmation.Code] = confirmation
	return nil
}

func (repository *SessionRepository) GetConfirmation(ctx context.Context, code string) (domain.Confirmation, error) {
	if err := ctx.Err(); err != nil {
		return domain.Confirmation{}, err
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	confirmation, found := repository.confirmations[strings.ToUpper(strings.TrimSpace(code))]
	if !found {
		return domain.Confirmation{}, fmt.Errorf("%w: confirmation", domain.ErrNotFound)
	}
	return confirmation, nil
}

func (repository *SessionRepository) ConsumeConfirmation(ctx context.Context, code, ownerDiscordUserID, guildID string, now time.Time) (domain.Confirmation, domain.Session, error) {
	if err := ctx.Err(); err != nil {
		return domain.Confirmation{}, domain.Session{}, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	code = strings.ToUpper(strings.TrimSpace(code))
	confirmation, found := repository.confirmations[code]
	if !found {
		return domain.Confirmation{}, domain.Session{}, domain.ErrNotFound
	}
	if err := confirmation.CheckActor(ownerDiscordUserID, guildID); err != nil {
		return domain.Confirmation{}, domain.Session{}, err
	}
	if err := confirmation.CheckPending(now); err != nil {
		return domain.Confirmation{}, domain.Session{}, err
	}
	session, found := repository.sessions[confirmation.SessionID]
	if !found || !confirmationMatchesSession(confirmation, session) {
		return domain.Confirmation{}, domain.Session{}, domain.ErrConfirmationStateDrift
	}
	confirmation.Status, confirmation.ConsumedAt = domain.ConfirmationConsumed, now.UTC()
	repository.confirmations[code] = confirmation
	return confirmation, session, nil
}

func (repository *SessionRepository) CancelConfirmation(ctx context.Context, code, ownerDiscordUserID, guildID string, now time.Time) (domain.Confirmation, error) {
	if err := ctx.Err(); err != nil {
		return domain.Confirmation{}, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	code = strings.ToUpper(strings.TrimSpace(code))
	confirmation, found := repository.confirmations[code]
	if !found {
		return domain.Confirmation{}, domain.ErrNotFound
	}
	if err := confirmation.CheckActor(ownerDiscordUserID, guildID); err != nil {
		return domain.Confirmation{}, err
	}
	if err := confirmation.CheckPending(now); err != nil {
		return domain.Confirmation{}, err
	}
	confirmation.Status, confirmation.CancelledAt = domain.ConfirmationCancelled, now.UTC()
	repository.confirmations[code] = confirmation
	return confirmation, nil
}

func confirmationMatchesSession(confirmation domain.Confirmation, session domain.Session) bool {
	return confirmation.SessionID == session.ID && confirmation.OwnerDiscordUserID == session.OwnerDiscordUserID &&
		confirmation.GuildID == session.GuildID && confirmation.BoundState == session.LifecycleState &&
		confirmation.BoundVersion == session.Version
}
