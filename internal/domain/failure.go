package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaximumFailureStageRunes  = 64
	MaximumFailureDetailRunes = 240
)

var (
	failureCodePattern         = regexp.MustCompile(`^ERR_[A-Z0-9_]{1,60}$`)
	supportReferencePattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{6,64}$`)
	cloudIdentifierPattern     = regexp.MustCompile(`(?i)\b(?:i|vol|sg|subnet|vpc|ami|snap)-[a-z0-9-]+\b|\barn:(?:aws|aws-us-gov|aws-cn):\S+|\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
	sensitiveAssignmentPattern = regexp.MustCompile(`(?i)\b(?:authorization|credential|password|secret|token)\s*[:=]\s*\S+`)
	awsAccessKeyPattern        = regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)
	addressOrURLPattern        = regexp.MustCompile(`(?i)https?://\S+|\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
)

type RetryDisposition string

const (
	RetryNotScheduled RetryDisposition = "NOT_SCHEDULED"
	RetryScheduled    RetryDisposition = "SCHEDULED"
)

func (disposition RetryDisposition) Valid() bool {
	return disposition == RetryNotScheduled || disposition == RetryScheduled
}

type ResourceCostImpact string

const (
	ResourceCostNone     ResourceCostImpact = "NONE"
	ResourceCostRetained ResourceCostImpact = "RETAINED_MAY_INCUR_COST"
	ResourceCostUnknown  ResourceCostImpact = "UNKNOWN_MAY_INCUR_COST"
)

func (impact ResourceCostImpact) Valid() bool {
	return impact == ResourceCostNone || impact == ResourceCostRetained || impact == ResourceCostUnknown
}

// FailureRecord is the sanitized, user-presentable failure state persisted on
// a session. Raw provider and command diagnostics remain on protected workflow
// records and must never be copied into this projection.
type FailureRecord struct {
	Code             string
	Stage            string
	RetryDisposition RetryDisposition
	ResourceImpact   ResourceCostImpact
	Detail           string
	FailedAt         time.Time
	SupportReference string
}

type FailureRecordInput struct {
	Code             string
	Stage            string
	RetryDisposition RetryDisposition
	ResourceImpact   ResourceCostImpact
	Detail           string
	FailedAt         time.Time
	SupportReference string
}

func NewFailureRecord(input FailureRecordInput) (FailureRecord, error) {
	record := FailureRecord{
		Code:             strings.ToUpper(strings.TrimSpace(input.Code)),
		Stage:            normalizeFailureText(input.Stage, MaximumFailureStageRunes),
		RetryDisposition: input.RetryDisposition,
		ResourceImpact:   input.ResourceImpact,
		Detail:           sanitizeFailureDetail(input.Detail),
		FailedAt:         input.FailedAt.UTC(),
		SupportReference: strings.TrimSpace(input.SupportReference),
	}
	if err := record.Validate(); err != nil {
		return FailureRecord{}, err
	}
	return record, nil
}

// FailureSupportReference turns an internal correlation value into a short,
// opaque reference suitable for public support handoff.
func FailureSupportReference(value string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return "ref_" + hex.EncodeToString(digest[:6])
}

func (record FailureRecord) Empty() bool {
	return record == (FailureRecord{})
}

func (record FailureRecord) Validate() error {
	if record.Empty() {
		return nil
	}
	switch {
	case !failureCodePattern.MatchString(record.Code):
		return fmt.Errorf("failure code is invalid")
	case record.Stage == "":
		return fmt.Errorf("failure stage is required")
	case record.Stage != normalizeFailureText(record.Stage, MaximumFailureStageRunes):
		return fmt.Errorf("failure stage must be normalized and bounded")
	case !record.RetryDisposition.Valid():
		return fmt.Errorf("failure retry disposition is invalid")
	case !record.ResourceImpact.Valid():
		return fmt.Errorf("failure resource impact is invalid")
	case record.Detail == "":
		return fmt.Errorf("failure detail is required")
	case record.Detail != sanitizeFailureDetail(record.Detail):
		return fmt.Errorf("failure detail must be sanitized and bounded")
	case record.FailedAt.IsZero():
		return fmt.Errorf("failure timestamp is required")
	case !supportReferencePattern.MatchString(record.SupportReference):
		return fmt.Errorf("failure support reference is invalid")
	default:
		return nil
	}
}

func sanitizeFailureDetail(value string) string {
	value = cloudIdentifierPattern.ReplaceAllString(value, "[redacted]")
	value = sensitiveAssignmentPattern.ReplaceAllString(value, "[redacted]")
	value = awsAccessKeyPattern.ReplaceAllString(value, "[redacted]")
	value = addressOrURLPattern.ReplaceAllString(value, "[redacted]")
	return normalizeFailureText(value, MaximumFailureDetailRunes)
}

// SanitizeDiagnostic removes identifiers, credentials, addresses, control
// characters, and excess length before a diagnostic enters audit metadata.
func SanitizeDiagnostic(value string) string {
	return sanitizeFailureDetail(value)
}

func normalizeFailureText(value string, limit int) string {
	var builder strings.Builder
	spacePending := false
	for _, character := range value {
		switch {
		case unicode.IsSpace(character):
			if builder.Len() > 0 {
				spacePending = true
			}
		case unicode.IsControl(character), unicode.Is(unicode.Cf, character):
			continue
		default:
			if spacePending {
				builder.WriteByte(' ')
				spacePending = false
			}
			builder.WriteRune(character)
		}
	}
	normalized := builder.String()
	if utf8.RuneCountInString(normalized) > limit {
		normalized = string([]rune(normalized)[:limit])
	}
	return normalized
}
