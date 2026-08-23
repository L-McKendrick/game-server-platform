package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const ResetConfirmationLifetime = 10 * time.Minute

type ResetStatus string

const (
	ResetPending   ResetStatus = "PENDING"
	ResetRunning   ResetStatus = "RUNNING"
	ResetSucceeded ResetStatus = "SUCCEEDED"
	ResetFailed    ResetStatus = "FAILED"
)

func (status ResetStatus) Valid() bool {
	return status == ResetPending || status == ResetRunning || status == ResetSucceeded || status == ResetFailed
}

type ResetConfirmation struct {
	ID          string
	Code        string
	Environment string
	GuildID     string
	RequestedBy string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ConsumedAt  time.Time
}

func NewResetConfirmation(id, environment, guildID, requestedBy string, now time.Time) (ResetConfirmation, error) {
	now = now.UTC()
	confirmation := ResetConfirmation{
		ID: strings.TrimSpace(id), Code: ResetCode(id), Environment: strings.TrimSpace(environment),
		GuildID: strings.TrimSpace(guildID), RequestedBy: strings.TrimSpace(requestedBy),
		CreatedAt: now, ExpiresAt: now.Add(ResetConfirmationLifetime),
	}
	return confirmation, confirmation.Validate()
}

func ResetCode(value string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return strings.ToUpper(hex.EncodeToString(digest[:4]))
}

func (confirmation ResetConfirmation) Phrase() string {
	return "RESET " + confirmation.Environment + " " + confirmation.Code
}

func (confirmation ResetConfirmation) Validate() error {
	switch {
	case strings.TrimSpace(confirmation.ID) == "", len(confirmation.Code) != 8, confirmation.Code != ResetCode(confirmation.ID):
		return fmt.Errorf("reset confirmation identity is invalid")
	case strings.TrimSpace(confirmation.Environment) == "", strings.TrimSpace(confirmation.GuildID) == "", strings.TrimSpace(confirmation.RequestedBy) == "":
		return fmt.Errorf("reset confirmation scope is required")
	case confirmation.CreatedAt.IsZero() || confirmation.ExpiresAt.IsZero() || !confirmation.ExpiresAt.After(confirmation.CreatedAt) || confirmation.ExpiresAt.Sub(confirmation.CreatedAt) > ResetConfirmationLifetime:
		return fmt.Errorf("reset confirmation expiry is invalid")
	case !confirmation.ConsumedAt.IsZero() && confirmation.ConsumedAt.Before(confirmation.CreatedAt):
		return fmt.Errorf("reset confirmation consumed timestamp is invalid")
	default:
		return nil
	}
}

func (confirmation ResetConfirmation) Check(actorID, guildID, phrase string, now time.Time) error {
	if confirmation.RequestedBy != strings.TrimSpace(actorID) || confirmation.GuildID != strings.TrimSpace(guildID) {
		return ErrConfirmationMismatch
	}
	if !confirmation.ConsumedAt.IsZero() {
		return ErrConfirmationConsumed
	}
	if !now.UTC().Before(confirmation.ExpiresAt) {
		return ErrConfirmationExpired
	}
	if strings.TrimSpace(phrase) != confirmation.Phrase() {
		return ErrConfirmationMismatch
	}
	return nil
}

type ResetOperation struct {
	ID              string
	Environment     string
	GuildID         string
	RequestedBy     string
	CorrelationID   string
	Status          ResetStatus
	Stage           string
	Version         int64
	StartedAt       time.Time
	UpdatedAt       time.Time
	CompletedAt     time.Time
	DeletedSessions int
	DeletedObjects  int
	ErrorCode       string
	ErrorDetail     string
}

func NewResetOperation(id, environment, guildID, requestedBy, correlationID string, now time.Time) (ResetOperation, error) {
	now = now.UTC()
	operation := ResetOperation{
		ID: strings.TrimSpace(id), Environment: strings.TrimSpace(environment), GuildID: strings.TrimSpace(guildID),
		RequestedBy: strings.TrimSpace(requestedBy), CorrelationID: strings.TrimSpace(correlationID),
		Status: ResetPending, Stage: "Queued", Version: 1, StartedAt: now, UpdatedAt: now,
	}
	return operation, operation.Validate()
}

func (operation ResetOperation) Validate() error {
	switch {
	case strings.TrimSpace(operation.ID) == "", strings.TrimSpace(operation.Environment) == "", strings.TrimSpace(operation.GuildID) == "":
		return fmt.Errorf("reset operation identity and scope are required")
	case strings.TrimSpace(operation.RequestedBy) == "", strings.TrimSpace(operation.CorrelationID) == "":
		return fmt.Errorf("reset operation actor and correlation are required")
	case !operation.Status.Valid() || strings.TrimSpace(operation.Stage) == "" || operation.Version < 1:
		return fmt.Errorf("reset operation state is invalid")
	case operation.StartedAt.IsZero() || operation.UpdatedAt.IsZero() || operation.UpdatedAt.Before(operation.StartedAt):
		return fmt.Errorf("reset operation timestamps are invalid")
	case (operation.Status == ResetSucceeded || operation.Status == ResetFailed) && operation.CompletedAt.IsZero():
		return fmt.Errorf("terminal reset operation requires completion time")
	case (operation.Status == ResetPending || operation.Status == ResetRunning) && !operation.CompletedAt.IsZero():
		return fmt.Errorf("active reset operation cannot have completion time")
	default:
		return nil
	}
}

func (operation ResetOperation) Active() bool {
	return operation.Status == ResetPending || operation.Status == ResetRunning
}

type ResetRequest struct {
	SchemaVersion int       `json:"schema_version"`
	OperationID   string    `json:"operation_id"`
	Environment   string    `json:"environment"`
	GuildID       string    `json:"guild_id"`
	RequestedAt   time.Time `json:"requested_at"`
}

func (request ResetRequest) Validate() error {
	if request.SchemaVersion != 1 || strings.TrimSpace(request.OperationID) == "" || strings.TrimSpace(request.Environment) == "" || strings.TrimSpace(request.GuildID) == "" || request.RequestedAt.IsZero() {
		return fmt.Errorf("reset request is invalid")
	}
	return nil
}
