package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

// HostAccessRecords must read current records strongly consistently. Discovery
// indexes and host-provided metadata cannot authorize access.
type HostAccessRecords interface {
	Get(context.Context, string) (domain.Session, error)
	GetWorkflow(context.Context, string, string) (domain.Workflow, error)
}

// HostInstanceAuthority verifies the exact live instance and immutable project,
// environment and SessionId tags. It must fail closed on missing or duplicate tags.
type HostInstanceAuthority interface {
	VerifyHostInstance(context.Context, string, string) error
}

type HostAccessAuthority struct {
	Records   HostAccessRecords
	Instances HostInstanceAuthority
}

// ValidateWorkflow revalidates identity and immutable content before issuance,
// refresh or output acceptance. Purpose-specific accepted-object validation is
// additionally required by each trusted worker; this does not sign arbitrary keys.
func (authority HostAccessAuthority) ValidateWorkflow(ctx context.Context, scope domain.HostAccessScope, now time.Time) (domain.Session, domain.Workflow, error) {
	denied := func() (domain.Session, domain.Workflow, error) {
		return domain.Session{}, domain.Workflow{}, fmt.Errorf("host access authority no longer current")
	}
	if scope.Validate() != nil || now.IsZero() || !scope.DeadlineAt.After(now) || authority.Records == nil || authority.Instances == nil {
		return denied()
	}
	session, err := authority.Records.Get(ctx, scope.SessionID)
	if err != nil {
		return denied()
	}
	workflow, err := authority.Records.GetWorkflow(ctx, scope.SessionID, scope.OperationID)
	if err != nil {
		return denied()
	}
	rollback := scope.CommandMode == "rollback"
	if (rollback && !session.HostAccessRollbackAllowed(workflow.ID)) || (!rollback && session.Progress.State == domain.ProgressRollingBack) {
		return denied()
	}
	if session.ID != scope.SessionID || session.GuildID != scope.GuildID || session.Infrastructure.InstanceID != scope.InstanceID ||
		session.ActiveWorkflowID != scope.OperationID || session.ActiveWorkflowType != workflow.Type ||
		workflow.ID != scope.OperationID || workflow.SessionID != scope.SessionID || workflow.Status != domain.WorkflowRunning ||
		!session.ActiveWorkflowStartedAt.Equal(workflow.StartedAt) ||
		!workflow.CancelRequestedAt.IsZero() || !workflow.CompletedAt.IsZero() ||
		!workflow.LeaseExpiresAt.After(now) || !session.ActiveWorkflowLeaseExpiresAt.After(now) ||
		scope.DeadlineAt.After(workflow.LeaseExpiresAt) || scope.DeadlineAt.After(session.ActiveWorkflowLeaseExpiresAt) ||
		(workflow.InstanceID != "" && workflow.InstanceID != scope.InstanceID) ||
		(!rollback && !workflow.CommandDeadlineAt.IsZero() && scope.DeadlineAt.After(workflow.CommandDeadlineAt)) ||
		session.LifecycleState == domain.StateDeleted || session.LifecycleState == domain.StateDeleting {
		return denied()
	}
	if HostContentSnapshot(session) != scope.SnapshotSHA256 {
		return denied()
	}
	if authority.Instances.VerifyHostInstance(ctx, scope.SessionID, scope.InstanceID) != nil {
		return denied()
	}
	return session, workflow, nil
}

// HostContentSnapshot excludes progress, health, deadlines and row version, which
// change during observation. It includes accepted/pending content and application
// intent so a content change invalidates an outstanding attempt.
func HostContentSnapshot(session domain.Session) string {
	content := struct {
		ConfigurationRevision               int64
		Vanilla, TeamSpeakEnabled           bool
		CreatorDLCs                         []string
		ServerConfigRevision                int64
		ServerConfigKey, ServerConfigSHA256 string
		Missions                            []domain.MissionRecord
		ConfiguredMission, CurrentMission   domain.MissionSelection
		ClientPreset, ServerPreset          int64
		ClientKey, ServerKey                string
		WorkshopMissions                    []domain.WorkshopMissionSource
		WorkshopMods                        []domain.WorkshopModSource
		Archive                             domain.ArchiveMetadata
		ResolutionTarget                    domain.WorkshopTarget
		ResolutionRequestKey                string
		ActiveClient, ActiveServer          domain.PresetRevision
	}{session.ConfigurationRevision, session.Vanilla, session.TeamSpeakEnabled, session.CreatorDLCs,
		session.ServerConfigRevision, session.ServerConfigObjectKey, session.ServerConfigSHA256,
		session.AcceptedMissionFiles(), session.ConfiguredMission, session.CurrentMission,
		session.PresetRevisionForApplication(), session.ServerPresetRevisionForApplication(),
		session.PresetObjectKeyForApplication(), session.ServerPresetObjectKeyForApplication(),
		session.WorkshopMissionSources, session.WorkshopModSources, session.Archive,
		session.WorkshopResolutionTarget, session.WorkshopResolutionRequestKey,
		session.EffectiveActivePresetRevision(), session.EffectiveActiveServerPresetRevision()}
	encoded, err := json.Marshal(content)
	if err != nil {
		return ""
	} // Invalid timestamps must not collapse to a shared digest.
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
