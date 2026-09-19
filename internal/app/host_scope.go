package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

var hostCommandStage = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// ResolveWorkflowScope gives a trusted command stage a stable attempt identity.
// Recovery preserves its first persisted deadline; it never derives a new one
// from retry time or silently replaces a changed content snapshot. Concurrent
// first dispatches still arbitrate publication through the existing attempt CAS.
func (issuer HostManifestIssuer) ResolveWorkflowScope(ctx context.Context, sessionID, stage string, timeout time.Duration) (domain.HostAccessScope, error) {
	if issuer.Attempts == nil || issuer.Inputs.Authority.Records == nil || !hostCommandStage.MatchString(stage) || timeout < domain.HostAccessCredentialMargin || timeout > 48*time.Hour {
		return domain.HostAccessScope{}, fmt.Errorf("host command scope configuration invalid")
	}
	if issuer.Inputs.Rollback && stage != "rollback" {
		return domain.HostAccessScope{}, fmt.Errorf("rollback command stage mismatch")
	}
	session, err := issuer.Inputs.Authority.Records.Get(ctx, sessionID)
	if err != nil {
		return domain.HostAccessScope{}, err
	}
	if session.ID != sessionID || session.ActiveWorkflowID == "" {
		return domain.HostAccessScope{}, domain.ErrWorkflowLocked
	}
	workflow, err := issuer.Inputs.Authority.Records.GetWorkflow(ctx, sessionID, session.ActiveWorkflowID)
	if err != nil {
		return domain.HostAccessScope{}, err
	}
	mode := ""
	if issuer.Inputs.Rollback {
		mode = "rollback"
	}
	identity := sha256.Sum256([]byte(sessionID + "\x00" + workflow.ID + "\x00" + session.Infrastructure.InstanceID + "\x00" + stage + "\x00" + mode + "\x00" + workflow.StartedAt.UTC().Format(time.RFC3339Nano)))
	attemptID := hex.EncodeToString(identity[:])[:32]
	current, err := issuer.Attempts.GetHostAccessAttempt(ctx, sessionID, workflow.ID, attemptID)
	if err == nil {
		if current.Validate() != nil || current.Scope.AttemptID != attemptID || current.Scope.CommandMode != mode || current.DispatchState == domain.HostAccessFinished {
			return domain.HostAccessScope{}, domain.ErrConflict
		}
		if _, _, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, current.Scope, time.Now().UTC()); err != nil {
			return domain.HostAccessScope{}, err
		}
		return current.Scope, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return domain.HostAccessScope{}, err
	}
	now := time.Now().UTC()
	deadline := now.Add(timeout)
	if stage == "archive" {
		deadline = workflow.StartedAt.Add(timeout)
	}
	for _, bound := range []time.Time{workflow.LeaseExpiresAt, session.ActiveWorkflowLeaseExpiresAt} {
		if bound.Before(deadline) {
			deadline = bound
		}
	}
	if mode == "" && !workflow.CommandDeadlineAt.IsZero() && workflow.CommandDeadlineAt.Before(deadline) {
		deadline = workflow.CommandDeadlineAt
	}
	scope := domain.HostAccessScope{SessionID: sessionID, GuildID: session.GuildID, OperationID: workflow.ID, AttemptID: attemptID, InstanceID: session.Infrastructure.InstanceID, SnapshotSHA256: HostContentSnapshot(session), DeadlineAt: deadline.UTC().Truncate(time.Second), CommandMode: mode}
	if _, err := domain.HostAccessExpiry(now, scope.DeadlineAt, time.Time{}); err != nil {
		return domain.HostAccessScope{}, err
	}
	if _, _, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, now); err != nil {
		return domain.HostAccessScope{}, err
	}
	return scope, nil
}
