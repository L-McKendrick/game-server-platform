package domain

import (
	"fmt"
	"strings"
	"time"
)

// ProgressMilestone is the coarse, cross-workflow card-update taxonomy. Phase
// 12.7 may add measured sub-stages, but must retain these public milestones.
type ProgressMilestone string

const (
	ProgressAccepted            ProgressMilestone = "ACCEPTED"
	ProgressInfrastructureReady ProgressMilestone = "INFRASTRUCTURE_READY"
	ProgressGameContentSetup    ProgressMilestone = "GAME_CONTENT_SETUP"
	ProgressHealthVerification  ProgressMilestone = "HEALTH_VERIFICATION"
	ProgressCompleted           ProgressMilestone = "COMPLETED"
	ProgressFailed              ProgressMilestone = "FAILED"
)

func (milestone ProgressMilestone) Valid() bool {
	switch milestone {
	case ProgressAccepted, ProgressInfrastructureReady, ProgressGameContentSetup,
		ProgressHealthVerification, ProgressCompleted, ProgressFailed:
		return true
	default:
		return false
	}
}

func (milestone ProgressMilestone) Terminal() bool {
	return milestone == ProgressCompleted || milestone == ProgressFailed
}

// SessionProgress is durable presentation state, not workflow identity. The
// workflow fields bind updates so a delayed worker cannot regress a later
// operation's public card.
type SessionProgress struct {
	WorkflowID   string
	WorkflowType string
	Milestone    ProgressMilestone
	UpdatedAt    time.Time
}

func (progress SessionProgress) Empty() bool {
	return progress.WorkflowID == "" && progress.WorkflowType == "" && progress.Milestone == "" && progress.UpdatedAt.IsZero()
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
	case progress.UpdatedAt.IsZero():
		return fmt.Errorf("progress update timestamp is required")
	default:
		return nil
	}
}

// AdvanceProgress records a monotonic milestone for the current workflow and
// advances the session version only when the public milestone changes.
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
	if progressRank(milestone) < progressRank(session.Progress.Milestone) {
		return false, nil
	}
	if session.Progress.Milestone.Terminal() {
		return false, fmt.Errorf("%w: progress cannot move from %s to %s", ErrInvalidTransition, session.Progress.Milestone, milestone)
	}
	now = now.UTC()
	if now.Before(session.Progress.UpdatedAt) {
		return false, fmt.Errorf("progress timestamp cannot move backward")
	}
	session.Progress.Milestone = milestone
	session.Progress.UpdatedAt = now
	session.Version++
	session.UpdatedAt = now
	return true, session.Validate()
}

func (session *Session) beginProgress(workflowID string, workflowType string, now time.Time) error {
	progress := SessionProgress{
		WorkflowID: strings.TrimSpace(workflowID), WorkflowType: strings.TrimSpace(workflowType),
		Milestone: ProgressAccepted, UpdatedAt: now.UTC(),
	}
	if err := progress.Validate(); err != nil {
		return err
	}
	session.Progress = progress
	return nil
}

func (session *Session) setProgressWithoutVersion(workflowID string, milestone ProgressMilestone, now time.Time) error {
	workflowID = strings.TrimSpace(workflowID)
	if session.Progress.Empty() && session.ActiveWorkflowID == workflowID {
		if err := session.beginProgress(workflowID, session.ActiveWorkflowType, session.ActiveWorkflowStartedAt); err != nil {
			return err
		}
	}
	if session.Progress.WorkflowID != workflowID {
		return fmt.Errorf("%w: progress belongs to another workflow", ErrConflict)
	}
	if !milestone.Valid() {
		return fmt.Errorf("invalid progress milestone %q", milestone)
	}
	if session.Progress.Milestone != milestone &&
		(session.Progress.Milestone.Terminal() || progressRank(milestone) < progressRank(session.Progress.Milestone)) {
		return fmt.Errorf("%w: progress cannot move from %s to %s", ErrInvalidTransition, session.Progress.Milestone, milestone)
	}
	now = now.UTC()
	if now.Before(session.Progress.UpdatedAt) {
		return fmt.Errorf("progress timestamp cannot move backward")
	}
	session.Progress.Milestone = milestone
	session.Progress.UpdatedAt = now
	return nil
}

func progressRank(milestone ProgressMilestone) int {
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
