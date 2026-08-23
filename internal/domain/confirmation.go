package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const ConfirmationLifetime = 10 * time.Minute

var confirmationCodePattern = regexp.MustCompile(`^[A-F0-9]{8,12}$`)

type ConfirmationAction string

const (
	ConfirmationArchive   ConfirmationAction = "ARCHIVE"
	ConfirmationTerminate ConfirmationAction = "TERMINATE"
)

func (action ConfirmationAction) Valid() bool {
	return action == ConfirmationArchive || action == ConfirmationTerminate
}

type ConfirmationStatus string

const (
	ConfirmationPending   ConfirmationStatus = "PENDING"
	ConfirmationConsumed  ConfirmationStatus = "CONSUMED"
	ConfirmationCancelled ConfirmationStatus = "CANCELLED"
)

func (status ConfirmationStatus) Valid() bool {
	return status == ConfirmationPending || status == ConfirmationConsumed || status == ConfirmationCancelled
}

type Confirmation struct {
	ID                 string
	Code               string
	SessionID          string
	OwnerDiscordUserID string
	GuildID            string
	Action             ConfirmationAction
	BoundState         LifecycleState
	BoundVersion       int64
	Status             ConfirmationStatus
	CreatedAt          time.Time
	ExpiresAt          time.Time
	ConsumedAt         time.Time
	CancelledAt        time.Time
}

func NewConfirmation(id, code string, session Session, action ConfirmationAction, now time.Time) (Confirmation, error) {
	now = now.UTC()
	confirmation := Confirmation{
		ID: strings.TrimSpace(id), Code: strings.ToUpper(strings.TrimSpace(code)), SessionID: session.ID,
		OwnerDiscordUserID: session.OwnerDiscordUserID, GuildID: session.GuildID,
		Action: action, BoundState: session.LifecycleState, BoundVersion: session.Version,
		Status: ConfirmationPending, CreatedAt: now, ExpiresAt: now.Add(ConfirmationLifetime),
	}
	if err := confirmation.Validate(); err != nil {
		return Confirmation{}, err
	}
	return confirmation, nil
}

func ConfirmationCode(value string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return strings.ToUpper(hex.EncodeToString(digest[:6]))
}

// PendingConfirmationCode identifies the single server-side confirmation slot
// for a Discord user in a guild. It is an internal persistence key, not a
// secret or a value users need to enter.
func PendingConfirmationCode(guildID, ownerDiscordUserID string) string {
	return ConfirmationCode(strings.TrimSpace(guildID) + "\x00" + strings.TrimSpace(ownerDiscordUserID))
}

func (confirmation Confirmation) Validate() error {
	switch {
	case strings.TrimSpace(confirmation.ID) == "":
		return fmt.Errorf("confirmation ID is required")
	case !confirmationCodePattern.MatchString(confirmation.Code):
		return fmt.Errorf("confirmation code is invalid")
	case strings.TrimSpace(confirmation.SessionID) == "":
		return fmt.Errorf("confirmation session ID is required")
	case strings.TrimSpace(confirmation.OwnerDiscordUserID) == "":
		return fmt.Errorf("confirmation owner is required")
	case strings.TrimSpace(confirmation.GuildID) == "":
		return fmt.Errorf("confirmation guild is required")
	case !confirmation.Action.Valid():
		return fmt.Errorf("confirmation action is invalid")
	case !confirmation.BoundState.Valid():
		return fmt.Errorf("confirmation bound state is invalid")
	case confirmation.BoundVersion < 1:
		return fmt.Errorf("confirmation bound version must be positive")
	case !confirmation.Status.Valid():
		return fmt.Errorf("confirmation status is invalid")
	case confirmation.CreatedAt.IsZero() || confirmation.ExpiresAt.IsZero():
		return fmt.Errorf("confirmation timestamps are required")
	case !confirmation.ExpiresAt.After(confirmation.CreatedAt) || confirmation.ExpiresAt.Sub(confirmation.CreatedAt) > ConfirmationLifetime:
		return fmt.Errorf("confirmation expiry must be within ten minutes")
	case confirmation.Status == ConfirmationPending && (!confirmation.ConsumedAt.IsZero() || !confirmation.CancelledAt.IsZero()):
		return fmt.Errorf("pending confirmation cannot have a terminal timestamp")
	case confirmation.Status == ConfirmationConsumed && (confirmation.ConsumedAt.IsZero() || !confirmation.CancelledAt.IsZero()):
		return fmt.Errorf("consumed confirmation requires only a consumed timestamp")
	case confirmation.Status == ConfirmationCancelled && (confirmation.CancelledAt.IsZero() || !confirmation.ConsumedAt.IsZero()):
		return fmt.Errorf("cancelled confirmation requires only a cancelled timestamp")
	default:
		return nil
	}
}

func (confirmation Confirmation) CheckActor(ownerDiscordUserID, guildID string) error {
	if confirmation.OwnerDiscordUserID != strings.TrimSpace(ownerDiscordUserID) || confirmation.GuildID != strings.TrimSpace(guildID) {
		return ErrConfirmationMismatch
	}
	return nil
}

func (confirmation Confirmation) CheckPending(now time.Time) error {
	switch confirmation.Status {
	case ConfirmationConsumed:
		return ErrConfirmationConsumed
	case ConfirmationCancelled:
		return ErrConfirmationCancelled
	case ConfirmationPending:
		if !now.UTC().Before(confirmation.ExpiresAt) {
			return ErrConfirmationExpired
		}
		return nil
	default:
		return ErrConfirmationMismatch
	}
}
