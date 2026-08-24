package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// PresetRevisionStatus describes the durable state of one validated preset
// revision. Only the active revision is authoritative for runtime bootstrap.
type PresetRevisionStatus string
type PresetRollbackDisposition string

const (
	PresetRevisionActive     PresetRevisionStatus      = "ACTIVE"
	PresetRevisionPending    PresetRevisionStatus      = "PENDING"
	PresetRevisionApplying   PresetRevisionStatus      = "APPLYING"
	PresetRevisionFailed     PresetRevisionStatus      = "FAILED"
	PresetRollbackSucceeded  PresetRollbackDisposition = "SUCCEEDED"
	PresetRollbackFailed     PresetRollbackDisposition = "FAILED"
	PresetRollbackUnverified PresetRollbackDisposition = "UNVERIFIED"
)

const MaximumPresetRevisionFailureRunes = 160

var presetRevisionSHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (status PresetRevisionStatus) Valid() bool {
	return status == PresetRevisionActive || status == PresetRevisionPending || status == PresetRevisionApplying || status == PresetRevisionFailed
}

func (disposition PresetRollbackDisposition) Valid() bool {
	return disposition == "" || disposition == PresetRollbackSucceeded || disposition == PresetRollbackFailed || disposition == PresetRollbackUnverified
}

// PresetModlistMetadata identifies the sanitized, downloadable projection of
// a validated launcher preset. Empty metadata remains valid for legacy rows.
type PresetModlistMetadata struct {
	ObjectKey     string
	Filename      string
	SHA256        string
	SizeBytes     int64
	WorkshopCount int
}

func (metadata PresetModlistMetadata) Empty() bool {
	return strings.TrimSpace(metadata.ObjectKey) == "" && strings.TrimSpace(metadata.Filename) == "" && strings.TrimSpace(metadata.SHA256) == "" && metadata.SizeBytes == 0 && metadata.WorkshopCount == 0
}

func (metadata PresetModlistMetadata) Validate() error {
	if metadata.Empty() {
		return nil
	}
	switch {
	case strings.TrimSpace(metadata.ObjectKey) == "":
		return fmt.Errorf("preset revision modlist object key is required")
	case strings.TrimSpace(metadata.Filename) == "" || strings.ContainsAny(metadata.Filename, `/\\`):
		return fmt.Errorf("preset revision modlist filename is invalid")
	case !presetRevisionSHA256Pattern.MatchString(strings.TrimSpace(metadata.SHA256)):
		return fmt.Errorf("preset revision modlist SHA-256 is invalid")
	case metadata.SizeBytes <= 0:
		return fmt.Errorf("preset revision modlist size must be positive")
	case metadata.WorkshopCount <= 0 || metadata.WorkshopCount > 250:
		return fmt.Errorf("preset revision Workshop count must be between 1 and 250")
	default:
		return nil
	}
}

// PresetRevision is one immutable preset input plus its application state.
// BaseRevision binds a pending change to the active configuration it replaces.
type PresetRevision struct {
	Number              int64
	BaseRevision        int64
	PresetObjectKey     string
	Modlist             PresetModlistMetadata
	Status              PresetRevisionStatus
	StagedAt            time.Time
	ApplyWorkflowID     string
	ApplyStartedAt      time.Time
	ActivatedAt         time.Time
	FailedAt            time.Time
	FailureDetail       string
	RollbackDisposition PresetRollbackDisposition
	RollbackAt          time.Time
	RollbackDetail      string
}

func (revision PresetRevision) Empty() bool {
	return revision.Number == 0 && revision.BaseRevision == 0 && strings.TrimSpace(revision.PresetObjectKey) == "" && revision.Modlist.Empty() && revision.Status == "" && revision.StagedAt.IsZero() && strings.TrimSpace(revision.ApplyWorkflowID) == "" && revision.ApplyStartedAt.IsZero() && revision.ActivatedAt.IsZero() && revision.FailedAt.IsZero() && strings.TrimSpace(revision.FailureDetail) == "" && revision.RollbackDisposition == "" && revision.RollbackAt.IsZero() && strings.TrimSpace(revision.RollbackDetail) == ""
}

func (revision PresetRevision) Validate() error {
	if revision.Empty() {
		return nil
	}
	if err := revision.Modlist.Validate(); err != nil {
		return err
	}
	switch {
	case revision.Number < 1:
		return fmt.Errorf("preset revision number must be positive")
	case revision.BaseRevision < 0 || revision.BaseRevision >= revision.Number:
		return fmt.Errorf("preset revision base must precede its number")
	case strings.TrimSpace(revision.PresetObjectKey) == "":
		return fmt.Errorf("preset revision object key is required")
	case !revision.Status.Valid():
		return fmt.Errorf("invalid preset revision status %q", revision.Status)
	case revision.StagedAt.IsZero():
		return fmt.Errorf("preset revision staged timestamp is required")
	case revision.Status == PresetRevisionActive && revision.ActivatedAt.IsZero():
		return fmt.Errorf("active preset revision requires an activation timestamp")
	case revision.Status != PresetRevisionActive && !revision.ActivatedAt.IsZero():
		return fmt.Errorf("only an active preset revision may have an activation timestamp")
	case revision.Status == PresetRevisionApplying && (strings.TrimSpace(revision.ApplyWorkflowID) == "" || revision.ApplyStartedAt.IsZero()):
		return fmt.Errorf("applying preset revision requires workflow metadata")
	case revision.Status != PresetRevisionApplying && (!revision.ApplyStartedAt.IsZero() || strings.TrimSpace(revision.ApplyWorkflowID) != ""):
		return fmt.Errorf("preset revision workflow metadata requires applying status")
	case revision.Status == PresetRevisionFailed && (revision.FailedAt.IsZero() || strings.TrimSpace(revision.FailureDetail) == ""):
		return fmt.Errorf("failed preset revision requires bounded failure metadata")
	case revision.Status != PresetRevisionFailed && (!revision.FailedAt.IsZero() || strings.TrimSpace(revision.FailureDetail) != ""):
		return fmt.Errorf("preset revision failure metadata requires failed status")
	case utf8.RuneCountInString(revision.FailureDetail) > MaximumPresetRevisionFailureRunes:
		return fmt.Errorf("preset revision failure detail exceeds %d characters", MaximumPresetRevisionFailureRunes)
	case revision.FailureDetail != "" && revision.FailureDetail != boundedPresetRevisionDetail(revision.FailureDetail, revision.FailureDetail):
		return fmt.Errorf("preset revision failure detail must be redacted and normalized")
	case !revision.RollbackDisposition.Valid():
		return fmt.Errorf("invalid preset revision rollback disposition %q", revision.RollbackDisposition)
	case revision.RollbackDisposition == "" && (!revision.RollbackAt.IsZero() || revision.RollbackDetail != ""):
		return fmt.Errorf("preset revision rollback metadata requires a disposition")
	case revision.RollbackDisposition != "" && (revision.RollbackAt.IsZero() || strings.TrimSpace(revision.RollbackDetail) == ""):
		return fmt.Errorf("preset revision rollback disposition requires bounded detail and timestamp")
	case utf8.RuneCountInString(revision.RollbackDetail) > MaximumPresetRevisionFailureRunes:
		return fmt.Errorf("preset revision rollback detail exceeds %d characters", MaximumPresetRevisionFailureRunes)
	case revision.RollbackDetail != "" && revision.RollbackDetail != boundedPresetRevisionDetail(revision.RollbackDetail, revision.RollbackDetail):
		return fmt.Errorf("preset revision rollback detail must be redacted and normalized")
	case !revision.StagedAt.Equal(revision.StagedAt.UTC()):
		return fmt.Errorf("preset revision staged timestamp must be UTC")
	case !revision.ApplyStartedAt.IsZero() && !revision.ApplyStartedAt.Equal(revision.ApplyStartedAt.UTC()):
		return fmt.Errorf("preset revision application timestamp must be UTC")
	case !revision.ActivatedAt.IsZero() && !revision.ActivatedAt.Equal(revision.ActivatedAt.UTC()):
		return fmt.Errorf("preset revision activation timestamp must be UTC")
	case !revision.FailedAt.IsZero() && !revision.FailedAt.Equal(revision.FailedAt.UTC()):
		return fmt.Errorf("preset revision failure timestamp must be UTC")
	case !revision.RollbackAt.IsZero() && !revision.RollbackAt.Equal(revision.RollbackAt.UTC()):
		return fmt.Errorf("preset revision rollback timestamp must be UTC")
	default:
		return nil
	}
}

// EffectiveActivePresetRevision supplies an additive migration projection for
// legacy sessions that only contain preset_object_key.
func (session Session) EffectiveActivePresetRevision() PresetRevision {
	if !session.ActivePresetRevision.Empty() {
		return session.ActivePresetRevision
	}
	if strings.TrimSpace(session.PresetObjectKey) == "" {
		return PresetRevision{}
	}
	when := session.CreatedAt.UTC()
	return PresetRevision{Number: 1, PresetObjectKey: session.PresetObjectKey, Status: PresetRevisionActive, StagedAt: when, ActivatedAt: when}
}

// EffectivePresetRevisionSequence includes the synthesized legacy revision.
func (session Session) EffectivePresetRevisionSequence() int64 {
	sequence := session.PresetRevisionSequence
	if active := session.EffectiveActivePresetRevision(); active.Number > sequence {
		sequence = active.Number
	}
	if session.PendingPresetRevision.Number > sequence {
		sequence = session.PendingPresetRevision.Number
	}
	return sequence
}

func (session Session) EffectiveActiveServerPresetRevision() PresetRevision {
	if !session.ActiveServerPresetRevision.Empty() {
		return session.ActiveServerPresetRevision
	}
	if strings.TrimSpace(session.ServerPresetObjectKey) == "" {
		return PresetRevision{}
	}
	when := session.CreatedAt.UTC()
	return PresetRevision{Number: 1, PresetObjectKey: session.ServerPresetObjectKey, Status: PresetRevisionActive, StagedAt: when, ActivatedAt: when}
}

func (session Session) EffectiveServerPresetRevisionSequence() int64 {
	sequence := session.ServerPresetRevisionSequence
	if active := session.EffectiveActiveServerPresetRevision(); active.Number > sequence {
		sequence = active.Number
	}
	if session.PendingServerPresetRevision.Number > sequence {
		sequence = session.PendingServerPresetRevision.Number
	}
	return sequence
}

func (session Session) ValidateServerPresetRevisionStaging(expectedActiveRevision int64) error {
	active := session.EffectiveActiveServerPresetRevision()
	switch {
	case session.Vanilla:
		return fmt.Errorf("%w: vanilla sessions do not have server mods", ErrInvalidTransition)
	case active.Number != expectedActiveRevision:
		return fmt.Errorf("%w: active server preset revision changed", ErrConflict)
	case session.ActiveWorkflowID != "":
		return fmt.Errorf("%w: wait for the active lifecycle operation before changing mods", ErrWorkflowLocked)
	case session.LifecycleState == StateDraft || session.LifecycleState == StateDeleting || session.LifecycleState == StateDeleted || session.LifecycleState == StateArchiving || session.LifecycleState == StateDestroying || session.LifecycleState == StateRestoring || session.LifecycleState == StateWaking || session.LifecycleState == StateStopping:
		return fmt.Errorf("%w: server mods cannot be staged in lifecycle state %s", ErrInvalidTransition, session.LifecycleState)
	case !session.PendingServerPresetRevision.Empty() && session.PendingServerPresetRevision.Status != PresetRevisionFailed:
		return fmt.Errorf("%w: server preset revision %d is already %s", ErrConflict, session.PendingServerPresetRevision.Number, session.PendingServerPresetRevision.Status)
	default:
		return nil
	}
}

func (session *Session) StageServerPresetRevision(expectedActiveRevision int64, objectKey string, now time.Time) (PresetRevision, error) {
	if err := session.ValidateServerPresetRevisionStaging(expectedActiveRevision); err != nil {
		return PresetRevision{}, err
	}
	objectKey = strings.TrimSpace(objectKey)
	if objectKey == "" {
		return PresetRevision{}, fmt.Errorf("server preset revision object key is required")
	}
	active := session.EffectiveActiveServerPresetRevision()
	number := session.EffectiveServerPresetRevisionSequence() + 1
	revision := PresetRevision{Number: number, BaseRevision: active.Number, PresetObjectKey: objectKey, Status: PresetRevisionPending, StagedAt: now.UTC()}
	if err := revision.Validate(); err != nil {
		return PresetRevision{}, err
	}
	if session.ActiveServerPresetRevision.Empty() {
		session.ActiveServerPresetRevision = active
	}
	session.PendingServerPresetRevision = revision
	session.ServerPresetRevisionSequence = number
	session.ServerPresetArtifactStatus, session.ServerPresetArtifactIssue = ArtifactAccepted, ""
	session.Version++
	session.UpdatedAt = now.UTC()
	return revision, session.Validate()
}

// ValidatePresetRevisionStaging verifies that a new upload can safely target
// the current active configuration without mutating lifecycle state.
func (session Session) ValidatePresetRevisionStaging(expectedActiveRevision int64) error {
	active := session.EffectiveActivePresetRevision()
	switch {
	case session.Vanilla:
		return fmt.Errorf("%w: vanilla sessions do not have a mod preset", ErrInvalidTransition)
	case active.Empty():
		return fmt.Errorf("%w: an active preset is required before staging a revision", ErrInvalidTransition)
	case active.Number != expectedActiveRevision:
		return fmt.Errorf("%w: active preset revision changed", ErrConflict)
	case session.ActiveWorkflowID != "":
		return fmt.Errorf("%w: wait for the active lifecycle operation before changing mods", ErrWorkflowLocked)
	case session.LifecycleState == StateDraft || session.LifecycleState == StateDeleting || session.LifecycleState == StateDeleted || session.LifecycleState == StateArchiving || session.LifecycleState == StateDestroying || session.LifecycleState == StateRestoring || session.LifecycleState == StateWaking || session.LifecycleState == StateStopping:
		return fmt.Errorf("%w: mods cannot be staged in lifecycle state %s", ErrInvalidTransition, session.LifecycleState)
	case !session.PendingPresetRevision.Empty() && session.PendingPresetRevision.Status != PresetRevisionFailed:
		return fmt.Errorf("%w: preset revision %d is already %s", ErrConflict, session.PendingPresetRevision.Number, session.PendingPresetRevision.Status)
	default:
		return nil
	}
}

// StagePresetRevision records a validated pending revision without changing
// the running service or the compatibility pointer to the active preset.
func (session *Session) StagePresetRevision(expectedActiveRevision int64, presetObjectKey string, modlist PresetModlistMetadata, now time.Time) (PresetRevision, error) {
	if err := session.ValidatePresetRevisionStaging(expectedActiveRevision); err != nil {
		return PresetRevision{}, err
	}
	if strings.TrimSpace(presetObjectKey) == "" {
		return PresetRevision{}, fmt.Errorf("preset revision object key is required")
	}
	if err := modlist.Validate(); err != nil {
		return PresetRevision{}, err
	}
	if modlist.Empty() {
		return PresetRevision{}, fmt.Errorf("validated preset revision requires sanitized modlist metadata")
	}
	active := session.EffectiveActivePresetRevision()
	number := session.EffectivePresetRevisionSequence() + 1
	revision := PresetRevision{
		Number: number, BaseRevision: active.Number, PresetObjectKey: strings.TrimSpace(presetObjectKey),
		Modlist: modlist, Status: PresetRevisionPending, StagedAt: now.UTC(),
	}
	if err := revision.Validate(); err != nil {
		return PresetRevision{}, err
	}
	if session.ActivePresetRevision.Empty() {
		session.ActivePresetRevision = active
	}
	session.PendingPresetRevision = revision
	session.PresetRevisionSequence = number
	session.Version++
	session.UpdatedAt = now.UTC()
	if err := session.Validate(); err != nil {
		return PresetRevision{}, err
	}
	return revision, nil
}

func (session *Session) beginPresetRevisionApplication(workflowID string, now time.Time) bool {
	changed := false
	if !session.PendingPresetRevision.Empty() && session.PendingPresetRevision.Status == PresetRevisionPending {
		pending := session.PendingPresetRevision
		pending.Status, pending.ApplyWorkflowID, pending.ApplyStartedAt = PresetRevisionApplying, strings.TrimSpace(workflowID), now.UTC()
		session.PendingPresetRevision = pending
		changed = true
	}
	if !session.PendingServerPresetRevision.Empty() && session.PendingServerPresetRevision.Status == PresetRevisionPending {
		pending := session.PendingServerPresetRevision
		pending.Status, pending.ApplyWorkflowID, pending.ApplyStartedAt = PresetRevisionApplying, strings.TrimSpace(workflowID), now.UTC()
		session.PendingServerPresetRevision = pending
		changed = true
	}
	return changed
}

// PresetObjectKeyForApplication selects pending content only for the workflow
// that durably owns its application. The compatibility pointer stays active.
func (session Session) PresetObjectKeyForApplication() string {
	if session.PendingPresetRevision.Status == PresetRevisionApplying && session.PendingPresetRevision.ApplyWorkflowID == session.ActiveWorkflowID {
		return session.PendingPresetRevision.PresetObjectKey
	}
	return session.EffectiveActivePresetRevision().PresetObjectKey
}

// PresetRevisionForApplication returns the revision installed by a lifecycle
// worker, or the active revision when no pending application is in progress.
func (session Session) PresetRevisionForApplication() int64 {
	if session.PendingPresetRevision.Status == PresetRevisionApplying && session.PendingPresetRevision.ApplyWorkflowID == session.ActiveWorkflowID {
		return session.PendingPresetRevision.Number
	}
	return session.EffectiveActivePresetRevision().Number
}

func (session Session) HasApplyingPresetRevision(workflowID string) bool {
	workflowID = strings.TrimSpace(workflowID)
	return session.PendingPresetRevision.Status == PresetRevisionApplying && session.PendingPresetRevision.ApplyWorkflowID == workflowID ||
		session.PendingServerPresetRevision.Status == PresetRevisionApplying && session.PendingServerPresetRevision.ApplyWorkflowID == workflowID
}

func (session Session) ServerPresetObjectKeyForApplication() string {
	if session.PendingServerPresetRevision.Status == PresetRevisionApplying && session.PendingServerPresetRevision.ApplyWorkflowID == session.ActiveWorkflowID {
		return session.PendingServerPresetRevision.PresetObjectKey
	}
	return session.EffectiveActiveServerPresetRevision().PresetObjectKey
}

func (session Session) ServerPresetRevisionForApplication() int64 {
	if session.PendingServerPresetRevision.Status == PresetRevisionApplying && session.PendingServerPresetRevision.ApplyWorkflowID == session.ActiveWorkflowID {
		return session.PendingServerPresetRevision.Number
	}
	return session.EffectiveActiveServerPresetRevision().Number
}

// RecordPresetRevisionRollback stores the outcome of the managed rollback
// command while the lifecycle workflow still owns the session lock.
func (session *Session) RecordPresetRevisionRollback(workflowID string, succeeded bool, detail string, now time.Time) (bool, error) {
	if !session.HasApplyingPresetRevision(workflowID) {
		return false, nil
	}
	changed := false
	for _, pending := range []*PresetRevision{&session.PendingPresetRevision, &session.PendingServerPresetRevision} {
		if pending.Status != PresetRevisionApplying || pending.ApplyWorkflowID != strings.TrimSpace(workflowID) || pending.RollbackDisposition != "" {
			continue
		}
		if succeeded {
			pending.RollbackDisposition = PresetRollbackSucceeded
		} else {
			pending.RollbackDisposition = PresetRollbackFailed
		}
		pending.RollbackAt = now.UTC()
		rollbackDetail := detail
		if succeeded {
			rollbackDetail = "Previous active mod configuration restored and health-checked."
		}
		pending.RollbackDetail = boundedPresetRevisionDetail(rollbackDetail, "Rollback did not restore a healthy service.")
		changed = true
	}
	if !changed {
		return false, nil
	}
	session.Version++
	session.UpdatedAt = now.UTC()
	return true, session.Validate()
}

// FailPresetRevisionApplication retains the candidate and rollback outcome as
// part of the surrounding lifecycle failure mutation.
func (session *Session) FailPresetRevisionApplication(workflowID, detail string, now time.Time) error {
	if !session.HasApplyingPresetRevision(workflowID) {
		return nil
	}
	for _, pending := range []*PresetRevision{&session.PendingPresetRevision, &session.PendingServerPresetRevision} {
		if pending.Status != PresetRevisionApplying || pending.ApplyWorkflowID != strings.TrimSpace(workflowID) {
			continue
		}
		pending.Status, pending.ApplyWorkflowID, pending.ApplyStartedAt = PresetRevisionFailed, "", time.Time{}
		pending.FailedAt = now.UTC()
		pending.FailureDetail = boundedPresetRevisionDetail(detail, "Pending mod revision did not pass installation and health verification.")
		if pending.RollbackDisposition == "" {
			pending.RollbackDisposition, pending.RollbackAt = PresetRollbackUnverified, now.UTC()
			pending.RollbackDetail = "Rollback could not be verified before the workflow stopped."
		}
	}
	return session.Validate()
}

func boundedPresetRevisionDetail(value, fallback string) string {
	value = sanitizeFailureDetail(value)
	if value == "" {
		value = sanitizeFailureDetail(fallback)
	}
	runes := []rune(value)
	if len(runes) > MaximumPresetRevisionFailureRunes {
		value = string(runes[:MaximumPresetRevisionFailureRunes])
	}
	return strings.TrimSpace(value)
}

// promotePresetRevision completes the already health-verified application as
// part of the surrounding lifecycle mutation, without a second version bump.
func (session *Session) promotePresetRevision(workflowID string, now time.Time) (PresetRevision, bool, error) {
	var promoted PresetRevision
	changed := false
	if session.PendingPresetRevision.Status == PresetRevisionApplying && session.PendingPresetRevision.ApplyWorkflowID == strings.TrimSpace(workflowID) {
		promoted = activatePresetRevision(session.PendingPresetRevision, now)
		if err := promoted.Validate(); err != nil {
			return PresetRevision{}, false, err
		}
		session.ActivePresetRevision, session.PendingPresetRevision, session.PresetObjectKey = promoted, PresetRevision{}, promoted.PresetObjectKey
		changed = true
	}
	if session.PendingServerPresetRevision.Status == PresetRevisionApplying && session.PendingServerPresetRevision.ApplyWorkflowID == strings.TrimSpace(workflowID) {
		serverPromoted := activatePresetRevision(session.PendingServerPresetRevision, now)
		if err := serverPromoted.Validate(); err != nil {
			return PresetRevision{}, false, err
		}
		session.ActiveServerPresetRevision, session.PendingServerPresetRevision, session.ServerPresetObjectKey = serverPromoted, PresetRevision{}, serverPromoted.PresetObjectKey
		if !changed {
			promoted = serverPromoted
		}
		changed = true
	}
	return promoted, changed, nil
}

func activatePresetRevision(revision PresetRevision, now time.Time) PresetRevision {
	revision.Status, revision.ApplyWorkflowID, revision.ApplyStartedAt = PresetRevisionActive, "", time.Time{}
	revision.ActivatedAt = now.UTC()
	return revision
}
