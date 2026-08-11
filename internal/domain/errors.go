package domain

import "errors"

var (
	ErrNotFound            = errors.New("not found")
	ErrAlreadyExists       = errors.New("already exists")
	ErrConflict            = errors.New("version conflict")
	ErrForbidden           = errors.New("forbidden")
	ErrInvalidTransition   = errors.New("invalid lifecycle transition")
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")
	ErrCommandInProgress   = errors.New("command is already in progress")
	ErrWorkflowLocked      = errors.New("session workflow lock is held")
)
