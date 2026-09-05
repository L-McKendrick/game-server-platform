package domain

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrNotFound                      = errors.New("not found")
	ErrAlreadyExists                 = errors.New("already exists")
	ErrSlugConflict                  = errors.New("session slug already exists")
	ErrConflict                      = errors.New("version conflict")
	ErrForbidden                     = errors.New("forbidden")
	ErrInvalidTransition             = errors.New("invalid lifecycle transition")
	ErrWorkshopNotCollection         = errors.New("Workshop item is not a collection")
	ErrWorkshopNestedOnly            = errors.New("Workshop collection contains only nested collections")
	ErrIdempotencyConflict           = errors.New("idempotency key reused with a different request")
	ErrCommandInProgress             = errors.New("command is already in progress")
	ErrWorkflowLocked                = errors.New("session workflow lock is held")
	ErrQuotaExceeded                 = errors.New("provisioned session quota exceeded")
	ErrFeatureDisabled               = errors.New("feature is disabled")
	ErrConfirmationExpired           = errors.New("confirmation expired")
	ErrConfirmationConsumed          = errors.New("confirmation already consumed")
	ErrConfirmationCancelled         = errors.New("confirmation cancelled")
	ErrConfirmationMismatch          = errors.New("confirmation does not match actor or guild")
	ErrConfirmationStateDrift        = errors.New("session changed after confirmation")
	ErrConfirmationRequired          = errors.New("durable confirmation is required")
	ErrConfirmationDispatchUncertain = errors.New("confirmed action queue delivery is uncertain")
	ErrPermanentArtifactRejection    = errors.New("artifact permanently rejected")
	ErrPermanentWorkshopRejection    = errors.New("Workshop source permanently rejected")
	ErrWorkshopSnapshotLimit         = errors.New("Workshop source history limit reached")
	ErrPersistenceInvariant          = errors.New("persistence invariant violated")
)

// OperationInProgressError returns safe progress for a repeated lifecycle
// request without exposing immutable workflow identity.
type OperationInProgressError struct {
	WorkflowType string
	Milestone    ProgressMilestone
	StartedAt    time.Time
	UpdatedAt    time.Time
}

func (err OperationInProgressError) Error() string {
	return fmt.Sprintf("%s: %v", ErrCommandInProgress, err.WorkflowType)
}

func (err OperationInProgressError) Is(target error) bool {
	return target == ErrCommandInProgress
}
