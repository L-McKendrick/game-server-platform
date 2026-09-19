package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type HostManifestIssuer struct {
	Inputs   HostObjectIssuer
	Attempts ports.HostAccessAttemptRepository
	Exchange ports.HostManifestExchange
}

// RecoverEnvironment returns privileged command context from the pinned temporary
// manifest. Trusted dispatch retries use it before preparing external exchanges:
// re-signing their URLs would change the immutable replay context. No capability
// is issued here; callers must still use IssueWithEnvironment for publication.
func (issuer HostManifestIssuer) RecoverEnvironment(ctx context.Context, scope domain.HostAccessScope) (map[string]string, error) {
	manifest, err := issuer.recoverCommandManifest(ctx, scope)
	if err != nil {
		return nil, err
	}
	return manifest.Environment, nil
}
func (issuer HostManifestIssuer) recoverCommandManifest(ctx context.Context, scope domain.HostAccessScope) (ports.HostAccessManifest, error) {
	if issuer.Attempts == nil || issuer.Exchange == nil {
		return ports.HostAccessManifest{}, fmt.Errorf("host manifest recovery incomplete")
	}
	if _, _, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC()); err != nil {
		return ports.HostAccessManifest{}, err
	}
	current, err := issuer.Attempts.GetHostAccessAttempt(ctx, scope.SessionID, scope.OperationID, scope.AttemptID)
	if err != nil {
		return ports.HostAccessManifest{}, err
	}
	if current.Validate() != nil || !current.Scope.Equal(scope) || current.DispatchState == domain.HostAccessFinished {
		return ports.HostAccessManifest{}, domain.ErrConflict
	}
	payload, err := issuer.Exchange.GetHostManifest(ctx, scope, current.ManifestVersionID)
	if err != nil || int64(len(payload)) != current.ManifestSizeBytes || manifestDigest(payload) != current.ManifestSHA256 {
		return ports.HostAccessManifest{}, fmt.Errorf("host manifest recovery integrity failed")
	}
	var stored ports.HostAccessManifest
	if json.Unmarshal(payload, &stored) != nil || stored.SchemaVersion != domain.HostAccessSchemaVersion || !stored.Scope.Equal(scope) || ports.ValidateHostEnvironment(stored.Environment) != nil {
		return ports.HostAccessManifest{}, fmt.Errorf("host manifest recovery scope invalid")
	}
	if _, _, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC()); err != nil {
		return ports.HostAccessManifest{}, err
	}
	latest, err := issuer.Attempts.GetHostAccessAttempt(ctx, scope.SessionID, scope.OperationID, scope.AttemptID)
	if err != nil || latest.Revision != current.Revision || !latest.Scope.Equal(scope) || latest.DispatchState == domain.HostAccessFinished {
		return ports.HostAccessManifest{}, domain.ErrConflict
	}
	return stored, nil
}

// CleanupExpiredAttempt checks durable ownership rather than active workflow
// authority: cleanup must work after cancellation, completion or deletion. Keep
// recovery metadata so a late in-flight upload can be swept again safely.
func (issuer HostManifestIssuer) CleanupExpiredAttempt(ctx context.Context, scope domain.HostAccessScope, cleaner ports.HostAccessAttemptCleaner) (int, error) {
	if issuer.Attempts == nil || issuer.Inputs.Authority.Records == nil || cleaner == nil || scope.Validate() != nil || !time.Now().UTC().After(scope.DeadlineAt.Add(domain.HostAccessCredentialMargin)) {
		return 0, domain.ErrConflict
	}
	current, err := issuer.Attempts.GetHostAccessAttempt(ctx, scope.SessionID, scope.OperationID, scope.AttemptID)
	if err != nil {
		return 0, err
	}
	if current.Validate() != nil || !current.Scope.Equal(scope) {
		return 0, domain.ErrConflict
	}
	session, err := issuer.Inputs.Authority.Records.Get(ctx, scope.SessionID)
	if err != nil {
		return 0, err
	}
	if session.ID != scope.SessionID || session.GuildID != scope.GuildID {
		return 0, domain.ErrConflict
	}
	latest, err := issuer.Attempts.GetHostAccessAttempt(ctx, scope.SessionID, scope.OperationID, scope.AttemptID)
	if err != nil || latest.Revision != current.Revision || !latest.Scope.Equal(scope) {
		return 0, domain.ErrConflict
	}
	return cleaner.DeleteHostAccessAttempt(ctx, scope)
}

// RecordDispatch records the outcome of sending a particular generation. A
// timeout is ambiguous, never evidence that a command was not delivered. This
// bookkeeping neither allocates another attempt nor releases a new capability.
func (issuer HostManifestIssuer) RecordDispatch(ctx context.Context, scope domain.HostAccessScope, generation int64, state string) error {
	if issuer.Attempts == nil || generation < 1 {
		return domain.ErrConflict
	}
	switch state {
	case domain.HostAccessDispatched, domain.HostAccessAmbiguous, domain.HostAccessFinished:
	default:
		return domain.ErrConflict
	}
	current, err := issuer.Attempts.GetHostAccessAttempt(ctx, scope.SessionID, scope.OperationID, scope.AttemptID)
	if err != nil {
		return err
	}
	if !current.Scope.Equal(scope) || current.Generation != generation || current.DispatchState == domain.HostAccessFinished {
		return domain.ErrConflict
	}
	session, _, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC())
	if err != nil {
		return err
	}
	if current.DispatchState == state {
		return nil
	}
	next := current
	next.Revision++
	next.DispatchState = state
	return issuer.Attempts.SaveHostAccessAttempt(ctx, next, current.Revision, session.Version, time.Now().UTC())
}

// Issue creates or recovers one object manifest. Call only from trusted workers,
// using their exact command inventory. A replay must use the same scope and
// objects. Refresh preserves attempt identity and absolute deadline.
func (issuer HostManifestIssuer) Issue(ctx context.Context, scope domain.HostAccessScope, objects []domain.HostObjectRequest) (ports.HostAccessReference, error) {
	return issuer.IssueWithEnvironment(ctx, scope, objects, nil)
}

// IssueWithEnvironment moves trusted command context behind the bounded SSM
// reference. Replay/refresh must preserve it alongside the exact object inventory.
func (issuer HostManifestIssuer) IssueWithEnvironment(ctx context.Context, scope domain.HostAccessScope, objects []domain.HostObjectRequest, environment map[string]string) (ports.HostAccessReference, error) {
	if issuer.Inputs.Rollback != (scope.CommandMode == "rollback") {
		return ports.HostAccessReference{}, fmt.Errorf("host command mode mismatch")
	}
	if len(environment) == 0 {
		environment = nil
	}
	if err := ports.ValidateHostEnvironment(environment); err != nil {
		return ports.HostAccessReference{}, err
	}
	if issuer.Attempts == nil || issuer.Exchange == nil || issuer.Inputs.Signer == nil || len(objects) == 0 {
		return ports.HostAccessReference{}, fmt.Errorf("host manifest issuer incomplete")
	}
	session, workflow, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC())
	if err != nil {
		return ports.HostAccessReference{}, err
	}
	for _, object := range objects {
		if object.Validate(scope) != nil || !issuer.Inputs.acceptsObject(session, workflow, object) {
			return ports.HostAccessReference{}, fmt.Errorf("host manifest input unauthorized")
		}
	}
	current, err := issuer.Attempts.GetHostAccessAttempt(ctx, scope.SessionID, scope.OperationID, scope.AttemptID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return ports.HostAccessReference{}, fmt.Errorf("host manifest recovery unavailable")
	}
	if err == nil {
		if !current.Scope.Equal(scope) || current.DispatchState == domain.HostAccessFinished {
			return ports.HostAccessReference{}, domain.ErrConflict
		}
		payload, readErr := issuer.Exchange.GetHostManifest(ctx, scope, current.ManifestVersionID)
		if readErr != nil || int64(len(payload)) != current.ManifestSizeBytes || manifestDigest(payload) != current.ManifestSHA256 {
			return ports.HostAccessReference{}, fmt.Errorf("host manifest recovery integrity failed")
		}
		var stored ports.HostAccessManifest
		if json.Unmarshal(payload, &stored) != nil || !stored.Scope.Equal(scope) || stored.SchemaVersion != domain.HostAccessSchemaVersion {
			return ports.HostAccessReference{}, fmt.Errorf("host manifest recovery scope invalid")
		}
		var accepted []domain.HostObjectRequest
		for _, capability := range stored.Objects {
			accepted = append(accepted, capability.Object)
		}
		if !reflect.DeepEqual(accepted, objects) || !reflect.DeepEqual(stored.Environment, environment) {
			return ports.HostAccessReference{}, domain.ErrConflict
		}
		if current.ExpiresAt.Sub(time.Now().UTC()) > domain.HostAccessRefreshBefore {
			return issuer.reference(ctx, current)
		}
	}
	manifest := ports.HostAccessManifest{SchemaVersion: domain.HostAccessSchemaVersion, Scope: scope, Environment: environment}
	for _, object := range objects {
		// The complete inventory was authorized above; revalidate authority after
		// the batch and enforce session version in publication CAS. Repeating live
		// instance inspection for every object would throttle large inventories.
		capability, signErr := issuer.Inputs.Signer.Sign(ctx, scope, object)
		if signErr != nil {
			return ports.HostAccessReference{}, fmt.Errorf("host manifest object signing failed")
		}
		if capability.Object != object {
			return ports.HostAccessReference{}, domain.ErrConflict
		}
		manifest.Objects = append(manifest.Objects, capability)
	}
	payload, err := manifest.Encode(time.Now().UTC())
	if err != nil {
		return ports.HostAccessReference{}, err
	}
	expires := manifest.Objects[0].ExpiresAt
	for _, capability := range manifest.Objects {
		if capability.ExpiresAt.Before(expires) {
			expires = capability.ExpiresAt
		}
	}
	if current.Revision > 0 && !expires.After(current.ExpiresAt) {
		return issuer.reference(ctx, current)
	}
	version, err := issuer.Exchange.PutHostManifest(ctx, scope, payload)
	if err != nil {
		return ports.HostAccessReference{}, fmt.Errorf("host manifest exchange write failed")
	}
	next := domain.HostAccessAttempt{Scope: scope, Revision: current.Revision + 1, Generation: current.Generation + 1, ManifestVersionID: version, ManifestSHA256: manifestDigest(payload), ManifestSizeBytes: int64(len(payload)), ExpiresAt: expires, DispatchState: domain.HostAccessPrepared}
	next.WorkshopOutputs = current.WorkshopOutputs
	// Refresh authority after storage; the CAS catches a subsequent session change.
	session, _, err = issuer.Inputs.Authority.ValidateWorkflow(ctx, scope, time.Now().UTC())
	if err != nil {
		return ports.HostAccessReference{}, err
	}
	if err := issuer.Attempts.SaveHostAccessAttempt(ctx, next, current.Revision, session.Version, time.Now().UTC()); err != nil {
		return ports.HostAccessReference{}, err
	}
	return issuer.reference(ctx, next)
}

func manifestDigest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return base64.StdEncoding.EncodeToString(digest[:])
}

func (issuer HostManifestIssuer) reference(ctx context.Context, attempt domain.HostAccessAttempt) (ports.HostAccessReference, error) {
	if _, _, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, attempt.Scope, time.Now().UTC()); err != nil {
		return ports.HostAccessReference{}, err
	}
	if !attempt.ExpiresAt.After(time.Now().UTC()) {
		return ports.HostAccessReference{}, fmt.Errorf("host manifest expired")
	}
	object := domain.HostObjectRequest{Purpose: domain.HostObjectManifest, Slot: "manifest", Key: attempt.Scope.StagingKey("manifest"), SHA256: attempt.ManifestSHA256, MaxBytes: attempt.ManifestSizeBytes, VersionID: attempt.ManifestVersionID}
	capability, err := issuer.Inputs.Signer.Sign(ctx, attempt.Scope, object)
	if err != nil {
		return ports.HostAccessReference{}, fmt.Errorf("host manifest reference signing failed")
	}
	expires := capability.ExpiresAt
	if attempt.ExpiresAt.Before(expires) {
		expires = attempt.ExpiresAt
	}
	reference := ports.HostAccessReference{SchemaVersion: domain.HostAccessSchemaVersion, Scope: attempt.Scope, Generation: attempt.Generation, URL: capability.URL, SHA256: attempt.ManifestSHA256, SizeBytes: attempt.ManifestSizeBytes, ExpiresAt: expires}
	if _, err := reference.Encode(time.Now().UTC()); err != nil {
		return ports.HostAccessReference{}, err
	}
	if _, _, err := issuer.Inputs.Authority.ValidateWorkflow(ctx, attempt.Scope, time.Now().UTC()); err != nil {
		return ports.HostAccessReference{}, err
	}
	latest, err := issuer.Attempts.GetHostAccessAttempt(ctx, attempt.Scope.SessionID, attempt.Scope.OperationID, attempt.Scope.AttemptID)
	if err != nil || latest.Revision != attempt.Revision || latest.DispatchState == domain.HostAccessFinished {
		return ports.HostAccessReference{}, domain.ErrConflict
	}
	return reference, nil
}
