package domain

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// HostAccessAttempt contains no bearer capabilities. Manifest identifies an
// encrypted temporary exchange version; its reference is re-signed after recovery.
type HostAccessAttempt struct {
	Scope             HostAccessScope
	Revision          int64
	Generation        int64
	ManifestVersionID string
	ManifestSHA256    string
	ManifestSizeBytes int64
	ExpiresAt         time.Time
	DispatchState     string
	// WorkshopOutputs pins the entire validated staging set once. Retries read
	// these exact versions, so later host writes cannot replace accepted bytes.
	WorkshopOutputs map[string]HostOutputVersion `json:",omitempty"`
}

type HostOutputVersion struct {
	VersionID string
	SHA256    string
	SizeBytes int64
}

const (
	HostAccessPrepared   = "PREPARED"
	HostAccessDispatched = "DISPATCHED"
	HostAccessAmbiguous  = "AMBIGUOUS"
	HostAccessFinished   = "FINISHED"
)

func (attempt HostAccessAttempt) Validate() error {
	if attempt.Scope.Validate() != nil || attempt.Revision < 1 || attempt.Generation < 1 ||
		attempt.ManifestVersionID == "" || strings.ContainsAny(attempt.ManifestVersionID, "\r\n\x00") ||
		!validSHA256(attempt.ManifestSHA256) || attempt.ManifestSizeBytes < 1 || attempt.ManifestSizeBytes > MaxHostAccessManifestBytes ||
		attempt.ExpiresAt.IsZero() || attempt.ExpiresAt.After(attempt.Scope.DeadlineAt) {
		return fmt.Errorf("host access recovery metadata invalid")
	}
	if len(attempt.WorkshopOutputs) > MaximumWorkshopMissionItems+2 {
		return fmt.Errorf("host Workshop output inventory exceeds bound")
	}
	if len(attempt.WorkshopOutputs) > 0 {
		if _, ok := attempt.WorkshopOutputs["workshop-result"]; !ok {
			return fmt.Errorf("host Workshop result version missing")
		}
		_, resolution := attempt.WorkshopOutputs["workshop-resolution"]
		if (len(attempt.WorkshopOutputs) > 1) != resolution || (resolution && len(attempt.WorkshopOutputs) < 3) {
			return fmt.Errorf("host Workshop resolution inventory incomplete")
		}
	}
	for slot, output := range attempt.WorkshopOutputs {
		limit, minimum := int64(MaxHostResultBytes), int64(1)
		switch slot {
		case "workshop-result", "workshop-resolution":
		default:
			id, err := strconv.ParseUint(strings.TrimPrefix(slot, "mission-"), 10, 64)
			if err != nil || id == 0 || slot != "mission-"+strconv.FormatUint(id, 10) {
				return fmt.Errorf("host Workshop output slot invalid")
			}
			limit, minimum = MaximumWorkshopMissionBytes, 16
		}
		if output.VersionID == "" || output.VersionID == "null" || len(output.VersionID) > 1024 ||
			strings.ContainsAny(output.VersionID, "\r\n\x00") || !validSHA256(output.SHA256) || output.SizeBytes < minimum || output.SizeBytes > limit {
			return fmt.Errorf("host Workshop output version invalid")
		}
	}
	switch attempt.DispatchState {
	case HostAccessPrepared, HostAccessDispatched, HostAccessAmbiguous, HostAccessFinished:
		return nil
	default:
		return fmt.Errorf("host access dispatch state invalid")
	}
}

// ValidateHostAccessUpdate prevents replacing an attempt identity or extending
// its absolute deadline. Refresh advances generation; dispatch bookkeeping keeps
// the exact manifest version. Finished attempts cannot be resurrected.
func ValidateHostAccessUpdate(current, next HostAccessAttempt, expectedRevision int64) error {
	if next.Validate() != nil || next.Revision != expectedRevision+1 {
		return ErrConflict
	}
	if expectedRevision == 0 {
		if current.Revision != 0 || next.Generation != 1 || next.DispatchState != HostAccessPrepared || len(next.WorkshopOutputs) != 0 {
			return ErrConflict
		}
		return nil
	}
	if current.Revision != expectedRevision || !current.Scope.Equal(next.Scope) || current.DispatchState == HostAccessFinished {
		return ErrConflict
	}
	if len(current.WorkshopOutputs) > 0 && !reflect.DeepEqual(current.WorkshopOutputs, next.WorkshopOutputs) {
		return ErrConflict
	}
	if next.Generation == current.Generation {
		if next.ManifestVersionID != current.ManifestVersionID || next.ManifestSHA256 != current.ManifestSHA256 ||
			next.ManifestSizeBytes != current.ManifestSizeBytes || !next.ExpiresAt.Equal(current.ExpiresAt) {
			return ErrConflict
		}
		if next.DispatchState == HostAccessPrepared && !(current.DispatchState == HostAccessPrepared && len(current.WorkshopOutputs) == 0 && len(next.WorkshopOutputs) > 0) {
			return ErrConflict
		}
		return nil
	}
	if next.Generation != current.Generation+1 || next.DispatchState != HostAccessPrepared || !next.ExpiresAt.After(current.ExpiresAt) {
		return ErrConflict
	}
	return nil
}
