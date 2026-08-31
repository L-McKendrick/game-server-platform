package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// ProgressMilestone identifies a stable, sanitized workflow checkpoint. Values
// are presentation facts only: they never contain command text or output.
type ProgressMilestone string

type ProgressState string

const (
	ProgressActive         ProgressState = "ACTIVE"
	ProgressWaiting        ProgressState = "WAITING"
	ProgressRetrying       ProgressState = "RETRYING"
	ProgressRollingBack    ProgressState = "ROLLING_BACK"
	ProgressCompletedState ProgressState = "COMPLETED"
	ProgressActionRequired ProgressState = "ACTION_REQUIRED"
	ProgressCancelled      ProgressState = "CANCELLED"
)

func (state ProgressState) Valid() bool {
	switch state {
	case ProgressActive, ProgressWaiting, ProgressRetrying, ProgressRollingBack,
		ProgressCompletedState, ProgressActionRequired, ProgressCancelled:
		return true
	default:
		return false
	}
}

const (
	ProgressAccepted            ProgressMilestone = "ACCEPTED"
	ProgressCapacityReserved    ProgressMilestone = "CAPACITY_RESERVED"
	ProgressComputeReady        ProgressMilestone = "COMPUTE_READY"
	ProgressInfrastructureReady ProgressMilestone = "INFRASTRUCTURE_READY"
	ProgressHostPrepared        ProgressMilestone = "HOST_PREPARED"
	ProgressGameServerInstalled ProgressMilestone = "GAME_SERVER_INSTALLED"
	ProgressModsApplied         ProgressMilestone = "MODS_APPLIED"
	ProgressConfigurationReady  ProgressMilestone = "CONFIGURATION_READY"
	ProgressServiceStarted      ProgressMilestone = "SERVICE_STARTED"
	ProgressHealthVerification  ProgressMilestone = "HEALTH_VERIFICATION"
	ProgressInstanceStopped     ProgressMilestone = "INSTANCE_STOPPED"
	ProgressArchiveCreated      ProgressMilestone = "ARCHIVE_CREATED"
	ProgressArchiveVerified     ProgressMilestone = "ARCHIVE_VERIFIED"
	ProgressDataRestored        ProgressMilestone = "DATA_RESTORED"
	ProgressRuntimeRemoved      ProgressMilestone = "RUNTIME_REMOVED"
	ProgressArtifactsRemoved    ProgressMilestone = "ARTIFACTS_REMOVED"
	ProgressResourcesInspected  ProgressMilestone = "RESOURCES_INSPECTED"
	ProgressMetadataReconciled  ProgressMilestone = "METADATA_RECONCILED"
	ProgressCompleted           ProgressMilestone = "COMPLETED"

	// Legacy coarse checkpoints remain readable during additive migration.
	ProgressGameContentSetup ProgressMilestone = "GAME_CONTENT_SETUP"
	ProgressFailed           ProgressMilestone = "FAILED"
)

const ProvisionWorkflowType = "ProvisionSession"
const WorkshopContentSyncWorkflowType = "WorkshopContentSync"

var workflowMilestoneSets = map[string][]ProgressMilestone{
	ProvisionWorkflowType: {
		ProgressAccepted, ProgressCapacityReserved, ProgressComputeReady,
		ProgressInfrastructureReady, ProgressCompleted,
	},
	BootstrapWorkflowType: {
		ProgressAccepted, ProgressHostPrepared, ProgressGameServerInstalled,
		ProgressModsApplied, ProgressConfigurationReady, ProgressServiceStarted,
		ProgressHealthVerification, ProgressCompleted,
	},
	SleepWorkflowType: {
		ProgressAccepted, ProgressInstanceStopped, ProgressCompleted,
	},
	WakeWorkflowType: {
		ProgressAccepted, ProgressComputeReady, ProgressModsApplied,
		ProgressServiceStarted, ProgressHealthVerification, ProgressCompleted,
	},
	ArchiveWorkflowType: {
		ProgressAccepted, ProgressArchiveCreated,
		ProgressArchiveVerified, ProgressRuntimeRemoved, ProgressCompleted,
	},
	RestoreWorkflowType: {
		ProgressAccepted, ProgressArchiveVerified, ProgressInfrastructureReady,
		ProgressDataRestored, ProgressHostPrepared, ProgressGameServerInstalled, ProgressModsApplied,
		ProgressConfigurationReady, ProgressServiceStarted, ProgressHealthVerification,
		ProgressCompleted,
	},
	TerminationWorkflowType: {
		ProgressAccepted, ProgressRuntimeRemoved, ProgressArtifactsRemoved, ProgressCompleted,
	},
	WorkshopContentSyncWorkflowType: {ProgressAccepted, ProgressModsApplied, ProgressCompleted},
	"ReconcileSession": {
		ProgressAccepted, ProgressResourcesInspected, ProgressMetadataReconciled, ProgressCompleted,
	},
}

// MilestonesForWorkflow returns a copy so callers cannot mutate the stable
// process-wide definition.
func MilestonesForWorkflow(workflowType string) ([]ProgressMilestone, bool) {
	milestones, ok := workflowMilestoneSets[strings.TrimSpace(workflowType)]
	return slices.Clone(milestones), ok
}

func (milestone ProgressMilestone) Valid() bool {
	if milestone == ProgressGameContentSetup || milestone == ProgressFailed {
		return true
	}
	for _, milestones := range workflowMilestoneSets {
		if slices.Contains(milestones, milestone) {
			return true
		}
	}
	return false
}

func (milestone ProgressMilestone) Terminal() bool {
	return milestone == ProgressCompleted || milestone == ProgressFailed
}

// SessionProgress is durable presentation state, not workflow identity. The
// workflow fields bind updates so a delayed worker cannot regress a later
// operation. Milestone is the current checkpoint; CompletedMilestones contains
// only ordered checkpoint facts that have been reached. StartedAt and
// LastProgressAt are operation clocks, never duration estimates.
type SessionProgress struct {
	WorkflowID          string
	WorkflowType        string
	Milestone           ProgressMilestone
	CompletedMilestones []ProgressMilestone
	SkippedMilestones   []ProgressMilestone
	State               ProgressState
	Activity            string
	StartedAt           time.Time
	LastProgressAt      time.Time
}

func (progress SessionProgress) Empty() bool {
	return progress.WorkflowID == "" && progress.WorkflowType == "" && progress.Milestone == "" &&
		len(progress.CompletedMilestones) == 0 && len(progress.SkippedMilestones) == 0 && progress.State == "" &&
		progress.Activity == "" && progress.StartedAt.IsZero() && progress.LastProgressAt.IsZero()
}

func (progress SessionProgress) Validate() error {
	if progress.Empty() {
		return nil
	}
	switch {
	case strings.TrimSpace(progress.WorkflowID) == "":
		return fmt.Errorf("progress workflow ID is required")
	case strings.TrimSpace(progress.WorkflowType) == "":
		return fmt.Errorf("progress workflow type is required")
	case !progress.Milestone.Valid():
		return fmt.Errorf("invalid progress milestone %q", progress.Milestone)
	case progress.State != "" && !progress.State.Valid():
		return fmt.Errorf("invalid progress state %q", progress.State)
	case len(progress.Activity) > 100 || strings.ContainsAny(progress.Activity, "\r\n"):
		return fmt.Errorf("progress activity is invalid")
	case progress.StartedAt.IsZero():
		return fmt.Errorf("progress start timestamp is required")
	case progress.LastProgressAt.IsZero():
		return fmt.Errorf("progress last-progress timestamp is required")
	case progress.LastProgressAt.Before(progress.StartedAt):
		return fmt.Errorf("progress last-progress timestamp cannot precede its start")
	}

	milestones, known := MilestonesForWorkflow(progress.WorkflowType)
	if !known {
		if len(progress.CompletedMilestones) != 0 {
			return fmt.Errorf("unknown workflow progress cannot contain completed milestones")
		}
		return nil
	}
	currentIndex := slices.Index(milestones, progress.Milestone)
	if currentIndex < 0 {
		if progress.Milestone == ProgressGameContentSetup || progress.Milestone == ProgressFailed {
			return nil
		}
		return fmt.Errorf("progress milestone %q does not belong to workflow %q", progress.Milestone, progress.WorkflowType)
	}
	previous := -1
	completedSet := make(map[ProgressMilestone]bool, len(progress.CompletedMilestones))
	for _, completed := range progress.CompletedMilestones {
		index := slices.Index(milestones, completed)
		if index < 0 {
			return fmt.Errorf("completed milestone %q does not belong to workflow %q", completed, progress.WorkflowType)
		}
		if index <= previous {
			return fmt.Errorf("completed milestones must be unique and ordered")
		}
		if index >= currentIndex && progress.Milestone != ProgressCompleted {
			return fmt.Errorf("completed milestone %q cannot reach or follow current milestone %q", completed, progress.Milestone)
		}
		previous = index
		completedSet[completed] = true
	}
	previous = -1
	for _, skipped := range progress.SkippedMilestones {
		index := slices.Index(milestones, skipped)
		if index < 0 {
			return fmt.Errorf("skipped milestone %q does not belong to workflow %q", skipped, progress.WorkflowType)
		}
		if index <= previous {
			return fmt.Errorf("skipped milestones must be unique and ordered")
		}
		if completedSet[skipped] {
			return fmt.Errorf("milestone %q cannot be both completed and skipped", skipped)
		}
		if index >= currentIndex && progress.Milestone != ProgressCompleted {
			return fmt.Errorf("skipped milestone %q cannot reach or follow current milestone %q", skipped, progress.Milestone)
		}
		previous = index
	}
	return nil
}

// SetProgressActivity records only an adapter-sanitized, bounded activity
// label. Repeated observations are no-ops and cannot move workflow milestones.
func (session *Session) SetProgressActivity(workflowID, activity string, now time.Time) (bool, error) {
	if session.Progress.WorkflowID != strings.TrimSpace(workflowID) {
		return false, fmt.Errorf("%w: progress belongs to another workflow", ErrConflict)
	}
	activity = strings.TrimSpace(activity)
	if len(activity) > 100 || strings.ContainsAny(activity, "\r\n") {
		return false, fmt.Errorf("invalid progress activity")
	}
	if session.Progress.Activity == activity {
		return false, nil
	}
	now = monotonicProgressTime(now, session.Progress.LastProgressAt)
	session.Progress.Activity = activity
	session.Progress.LastProgressAt = now
	session.Version++
	session.UpdatedAt = now
	return true, session.Validate()
}

// AdvanceProgress records a monotonic milestone for the current workflow and
// advances the session version only when the public checkpoint changes.
func (session *Session) AdvanceProgress(workflowID string, milestone ProgressMilestone, now time.Time) (bool, error) {
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" || !milestone.Valid() {
		return false, fmt.Errorf("valid workflow ID and progress milestone are required")
	}
	if session.Progress.Empty() && session.ActiveWorkflowID == workflowID {
		if err := session.beginProgress(workflowID, session.ActiveWorkflowType, session.ActiveWorkflowStartedAt); err != nil {
			return false, err
		}
	}
	if session.Progress.WorkflowID != workflowID {
		return false, fmt.Errorf("%w: progress belongs to another workflow", ErrConflict)
	}
	if session.ActiveWorkflowID != "" && session.ActiveWorkflowID != workflowID {
		return false, fmt.Errorf("%w: workflow does not hold the session lock", ErrConflict)
	}
	if session.Progress.Milestone == milestone {
		return false, nil
	}
	currentRank, nextRank := progressRank(session.Progress.WorkflowType, session.Progress.Milestone), progressRank(session.Progress.WorkflowType, milestone)
	if currentRank > 0 && nextRank > 0 && nextRank < currentRank {
		return false, nil
	}
	if session.Progress.State == ProgressCompletedState || session.Progress.State == ProgressActionRequired || session.Progress.State == ProgressCancelled {
		return false, fmt.Errorf("%w: terminal progress state %s cannot advance", ErrInvalidTransition, session.Progress.State)
	}
	if session.Progress.Milestone.Terminal() {
		return false, fmt.Errorf("%w: progress cannot move from %s to %s", ErrInvalidTransition, session.Progress.Milestone, milestone)
	}
	now = now.UTC()
	if now.Before(session.Progress.LastProgressAt) {
		now = session.Progress.LastProgressAt
	}
	session.Progress.CompletedMilestones = slices.Clone(session.Progress.CompletedMilestones)
	session.Progress.SkippedMilestones = slices.Clone(session.Progress.SkippedMilestones)
	completeCurrentMilestone(&session.Progress)
	session.Progress.Milestone = milestone
	if milestone == ProgressCompleted {
		completeCurrentMilestone(&session.Progress)
		session.Progress.State = ProgressCompletedState
	} else {
		session.Progress.State = ProgressActive
	}
	session.Progress.LastProgressAt = now
	session.Version++
	session.UpdatedAt = now
	return true, session.Validate()
}

// AdvanceProgressSequence atomically folds one or more observed checkpoints
// into a single persisted session revision. This is used when polling returns
// several checkpoints that the managed command crossed between observations.
func (session *Session) AdvanceProgressSequence(workflowID string, milestones []ProgressMilestone, now time.Time) (bool, error) {
	return session.ApplyProgressSequence(workflowID, milestones, nil, now)
}

// ApplyProgressSequence atomically records ordered observations and explicit
// no-work checkpoints. A skipped checkpoint is entered, recorded as skipped,
// and advanced past without ever entering CompletedMilestones.
func (session *Session) ApplyProgressSequence(workflowID string, milestones, skipped []ProgressMilestone, now time.Time) (bool, error) {
	working := *session
	working.Progress.CompletedMilestones = slices.Clone(session.Progress.CompletedMilestones)
	working.Progress.SkippedMilestones = slices.Clone(session.Progress.SkippedMilestones)
	skippedSet := make(map[ProgressMilestone]bool, len(skipped))
	for _, milestone := range skipped {
		skippedSet[milestone] = true
	}
	changed := false
	for _, milestone := range milestones {
		advanced, err := working.AdvanceProgress(workflowID, milestone, now)
		if err != nil {
			return false, err
		}
		changed = changed || advanced
		if skippedSet[milestone] && working.Progress.Milestone == milestone && !slices.Contains(working.Progress.SkippedMilestones, milestone) {
			ordered, ok := MilestonesForWorkflow(working.Progress.WorkflowType)
			index := slices.Index(ordered, milestone)
			if !ok || index < 0 || index+1 >= len(ordered) {
				return false, fmt.Errorf("%w: skipped milestone has no next checkpoint", ErrInvalidTransition)
			}
			skippedChanged, err := working.SkipProgress(workflowID, milestone, ordered[index+1], now)
			if err != nil {
				return false, err
			}
			changed = changed || skippedChanged
		}
	}
	if !changed {
		return false, nil
	}
	working.Version = session.Version + 1
	if err := working.Validate(); err != nil {
		return false, err
	}
	*session = working
	return true, nil
}

// SkipProgress records an explicit no-work checkpoint without filling its bar
// segment, then advances to the supplied next checkpoint. Replays are no-ops.
func (session *Session) SkipProgress(workflowID string, milestone, next ProgressMilestone, now time.Time) (bool, error) {
	if session.Progress.WorkflowID != strings.TrimSpace(workflowID) {
		return false, fmt.Errorf("%w: progress belongs to another workflow", ErrConflict)
	}
	if slices.Contains(session.Progress.SkippedMilestones, milestone) {
		return false, nil
	}
	if session.Progress.State == ProgressCompletedState || session.Progress.State == ProgressActionRequired || session.Progress.State == ProgressCancelled {
		return false, fmt.Errorf("%w: terminal progress state %s cannot skip", ErrInvalidTransition, session.Progress.State)
	}
	if session.Progress.Milestone != milestone {
		return false, fmt.Errorf("%w: only the current milestone may be skipped", ErrInvalidTransition)
	}
	milestones, ok := MilestonesForWorkflow(session.Progress.WorkflowType)
	if !ok || slices.Index(milestones, next) != slices.Index(milestones, milestone)+1 {
		return false, fmt.Errorf("%w: skip must advance to the next workflow milestone", ErrInvalidTransition)
	}
	now = monotonicProgressTime(now, session.Progress.LastProgressAt)
	session.Progress.CompletedMilestones = slices.Clone(session.Progress.CompletedMilestones)
	session.Progress.SkippedMilestones = slices.Clone(session.Progress.SkippedMilestones)
	session.Progress.SkippedMilestones = append(session.Progress.SkippedMilestones, milestone)
	session.Progress.Milestone = next
	session.Progress.State = ProgressActive
	session.Progress.LastProgressAt = now
	session.Version++
	session.UpdatedAt = now
	return true, session.Validate()
}

// SetProgressState changes only the qualitative operation condition. It never
// completes, skips, or rewinds a checkpoint.
func (session *Session) SetProgressState(workflowID string, state ProgressState, now time.Time) (bool, error) {
	if session.Progress.WorkflowID != strings.TrimSpace(workflowID) {
		return false, fmt.Errorf("%w: progress belongs to another workflow", ErrConflict)
	}
	if !state.Valid() || state == ProgressCompletedState {
		return false, fmt.Errorf("invalid non-completion progress state %q", state)
	}
	if session.Progress.State == state {
		return false, nil
	}
	if session.Progress.State == ProgressCompletedState || session.Progress.State == ProgressActionRequired || session.Progress.State == ProgressCancelled {
		return false, fmt.Errorf("%w: terminal progress state %s cannot change", ErrInvalidTransition, session.Progress.State)
	}
	now = monotonicProgressTime(now, session.Progress.LastProgressAt)
	session.Progress.State = state
	session.Progress.LastProgressAt = now
	session.Version++
	session.UpdatedAt = now
	return true, session.Validate()
}

func (session *Session) beginProgress(workflowID string, workflowType string, now time.Time) error {
	now = now.UTC()
	progress := SessionProgress{
		WorkflowID: strings.TrimSpace(workflowID), WorkflowType: strings.TrimSpace(workflowType),
		Milestone: ProgressAccepted, State: ProgressActive, StartedAt: now, LastProgressAt: now,
	}
	if err := progress.Validate(); err != nil {
		return err
	}
	session.Progress = progress
	return nil
}

func (session *Session) setProgressWithoutVersion(workflowID string, milestone ProgressMilestone, now time.Time) error {
	currentVersion := session.Version
	var changed bool
	var err error
	if milestone == ProgressFailed {
		changed, err = session.SetProgressState(workflowID, ProgressActionRequired, now)
	} else {
		changed, err = session.AdvanceProgress(workflowID, milestone, now)
	}
	if err != nil {
		return err
	}
	if changed {
		session.Version = currentVersion
	}
	return nil
}

func (session *Session) setProgressSequenceWithoutVersion(workflowID string, milestones []ProgressMilestone, now time.Time) error {
	currentVersion := session.Version
	changed, err := session.AdvanceProgressSequence(workflowID, milestones, now)
	if err != nil {
		return err
	}
	if changed {
		session.Version = currentVersion
	}
	return nil
}

func (session *Session) completeProgressWithoutVersion(workflowID string, now time.Time) error {
	milestones, ok := MilestonesForWorkflow(session.Progress.WorkflowType)
	current := slices.Index(milestones, session.Progress.Milestone)
	if !ok || current < 0 {
		return session.setProgressWithoutVersion(workflowID, ProgressCompleted, now)
	}
	return session.setProgressSequenceWithoutVersion(workflowID, milestones[current+1:], now)
}

func completeCurrentMilestone(progress *SessionProgress) {
	if progress.Milestone == ProgressFailed || slices.Contains(progress.CompletedMilestones, progress.Milestone) || slices.Contains(progress.SkippedMilestones, progress.Milestone) {
		return
	}
	milestones, known := MilestonesForWorkflow(progress.WorkflowType)
	if known && !slices.Contains(milestones, progress.Milestone) {
		return
	}
	progress.CompletedMilestones = append(progress.CompletedMilestones, progress.Milestone)
}

func monotonicProgressTime(now, last time.Time) time.Time {
	now = now.UTC()
	if now.Before(last) {
		return last
	}
	return now
}

func progressRank(workflowType string, milestone ProgressMilestone) int {
	milestones, ok := MilestonesForWorkflow(workflowType)
	if ok {
		if index := slices.Index(milestones, milestone); index >= 0 {
			return index + 1
		}
	}
	switch milestone {
	case ProgressAccepted:
		return 1
	case ProgressInfrastructureReady:
		return 2
	case ProgressGameContentSetup:
		return 3
	case ProgressHealthVerification:
		return 4
	case ProgressCompleted, ProgressFailed:
		return 5
	default:
		return 0
	}
}
