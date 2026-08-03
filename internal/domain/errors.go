package domain

import "errors"

var (
	ErrNotFound          = errors.New("not found")
	ErrAlreadyExists     = errors.New("already exists")
	ErrConflict          = errors.New("version conflict")
	ErrForbidden         = errors.New("forbidden")
	ErrInvalidTransition = errors.New("invalid lifecycle transition")
)
