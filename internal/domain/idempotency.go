package domain

import (
	"fmt"
	"strings"
	"time"
)

// IdempotencyStatus describes the processing state of an external command.
type IdempotencyStatus string

const (
	IdempotencyPending   IdempotencyStatus = "PENDING"
	IdempotencyCompleted IdempotencyStatus = "COMPLETED"
)

// IdempotencyRecord prevents repeated external commands from producing
// duplicate effects.
type IdempotencyRecord struct {
	Key             string
	RequestHash     string
	Status          IdempotencyStatus
	CreatedAt       time.Time
	CompletedAt     time.Time
	ResultReference string
	ExpiresAtEpoch  int64
}

// NewCompletedIdempotencyRecord creates a completed command record for a
// synchronous metadata mutation.
func NewCompletedIdempotencyRecord(
	key string,
	requestHash string,
	resultReference string,
	now time.Time,
	retention time.Duration,
) (IdempotencyRecord, error) {
	now = now.UTC()

	record := IdempotencyRecord{
		Key:             strings.TrimSpace(key),
		RequestHash:     strings.TrimSpace(requestHash),
		Status:          IdempotencyCompleted,
		CreatedAt:       now,
		CompletedAt:     now,
		ResultReference: strings.TrimSpace(resultReference),
		ExpiresAtEpoch:  now.Add(retention).Unix(),
	}

	if err := record.Validate(); err != nil {
		return IdempotencyRecord{}, err
	}

	return record, nil
}

// Validate verifies the durable command-record invariants.
func (record IdempotencyRecord) Validate() error {
	switch {
	case strings.TrimSpace(record.Key) == "":
		return fmt.Errorf("idempotency key is required")
	case strings.TrimSpace(record.RequestHash) == "":
		return fmt.Errorf("idempotency request hash is required")
	case record.Status != IdempotencyPending && record.Status != IdempotencyCompleted:
		return fmt.Errorf("invalid idempotency status %q", record.Status)
	case record.CreatedAt.IsZero():
		return fmt.Errorf("idempotency created timestamp is required")
	case record.ExpiresAtEpoch <= record.CreatedAt.Unix():
		return fmt.Errorf("idempotency expiration must be after creation")
	case record.Status == IdempotencyPending && !record.CompletedAt.IsZero():
		return fmt.Errorf("pending idempotency record cannot have a completion timestamp")
	case record.Status == IdempotencyCompleted && record.CompletedAt.IsZero():
		return fmt.Errorf("completed idempotency record requires a completion timestamp")
	case record.Status == IdempotencyCompleted && record.CompletedAt.Before(record.CreatedAt):
		return fmt.Errorf("idempotency completion cannot precede creation")
	case record.Status == IdempotencyCompleted && strings.TrimSpace(record.ResultReference) == "":
		return fmt.Errorf("completed idempotency record requires a result reference")
	default:
		return nil
	}
}
