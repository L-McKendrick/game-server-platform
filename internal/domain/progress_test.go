package domain

import (
	"errors"
	"testing"
	"time"
)

func TestSessionProgressIsMonotonicAndVersioned(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 15, 3, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-progress", Slug: "session-progress", DisplayName: "Progress",
		GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AcquireWorkflowLock("workflow-1", "TestWorkflow", time.Hour, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.Progress.Milestone != ProgressAccepted || session.Progress.WorkflowID != "workflow-1" {
		t.Fatalf("accepted progress = %#v", session.Progress)
	}

	version := session.Version
	changed, err := session.AdvanceProgress("workflow-1", ProgressGameContentSetup, now.Add(2*time.Minute))
	if err != nil || !changed || session.Version != version+1 {
		t.Fatalf("game/content progress = %#v, changed=%t, err=%v", session.Progress, changed, err)
	}
	version = session.Version
	changed, err = session.AdvanceProgress("workflow-1", ProgressGameContentSetup, now.Add(3*time.Minute))
	if err != nil || changed || session.Version != version {
		t.Fatalf("repeated progress changed=%t version=%d err=%v", changed, session.Version, err)
	}
	changed, err = session.AdvanceProgress("workflow-1", ProgressInfrastructureReady, now.Add(4*time.Minute))
	if err != nil || changed || session.Progress.Milestone != ProgressGameContentSetup {
		t.Fatalf("stale progress = %#v, changed=%t, err=%v", session.Progress, changed, err)
	}
	if _, err := session.AdvanceProgress("other-workflow", ProgressCompleted, now.Add(5*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("other workflow error = %v; want ErrConflict", err)
	}
	if _, err := session.AdvanceProgress("workflow-1", ProgressCompleted, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AdvanceProgress("workflow-1", ProgressFailed, now.Add(6*time.Minute)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal rewrite error = %v; want ErrInvalidTransition", err)
	}
}

func TestSessionProgressMigratesLegacyActiveWorkflowOnWrite(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 15, 4, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-legacy", Slug: "session-legacy", DisplayName: "Legacy",
		GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.ActiveWorkflowID = "workflow-legacy"
	session.ActiveWorkflowType = "TestWorkflow"
	session.ActiveWorkflowStartedAt = now.Add(time.Minute)
	session.ActiveWorkflowLeaseExpiresAt = now.Add(time.Hour)
	session.UpdatedAt = now.Add(time.Minute)
	if err := session.Validate(); err != nil {
		t.Fatalf("legacy active session error = %v", err)
	}
	changed, err := session.AdvanceProgress("workflow-legacy", ProgressHealthVerification, now.Add(2*time.Minute))
	if err != nil || !changed || session.Progress.Milestone != ProgressHealthVerification {
		t.Fatalf("migrated progress = %#v, changed=%t, err=%v", session.Progress, changed, err)
	}
}
