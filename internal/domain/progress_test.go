package domain

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestWorkflowMilestoneSetsAreStableOrderedCopies(t *testing.T) {
	t.Parallel()
	tests := map[string][]ProgressMilestone{
		ProvisionWorkflowType:   {ProgressAccepted, ProgressCapacityReserved, ProgressComputeReady, ProgressInfrastructureReady, ProgressCompleted},
		BootstrapWorkflowType:   {ProgressAccepted, ProgressHostPrepared, ProgressGameServerInstalled, ProgressModsApplied, ProgressConfigurationReady, ProgressServiceStarted, ProgressHealthVerification, ProgressCompleted},
		SleepWorkflowType:       {ProgressAccepted, ProgressInstanceStopped, ProgressCompleted},
		WakeWorkflowType:        {ProgressAccepted, ProgressComputeReady, ProgressModsApplied, ProgressServiceStarted, ProgressHealthVerification, ProgressCompleted},
		ArchiveWorkflowType:     {ProgressAccepted, ProgressArchiveCreated, ProgressArchiveVerified, ProgressRuntimeRemoved, ProgressCompleted},
		RestoreWorkflowType:     {ProgressAccepted, ProgressArchiveVerified, ProgressInfrastructureReady, ProgressDataRestored, ProgressHostPrepared, ProgressGameServerInstalled, ProgressModsApplied, ProgressConfigurationReady, ProgressServiceStarted, ProgressHealthVerification, ProgressCompleted},
		TerminationWorkflowType: {ProgressAccepted, ProgressRuntimeRemoved, ProgressArtifactsRemoved, ProgressCompleted},
	}
	for workflowType, want := range tests {
		got, ok := MilestonesForWorkflow(workflowType)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s milestones = %#v, %t; want %#v", workflowType, got, ok, want)
		}
		got[0] = ProgressFailed
		again, _ := MilestonesForWorkflow(workflowType)
		if again[0] != ProgressAccepted {
			t.Fatalf("%s milestone definition was mutable", workflowType)
		}
	}
}

func TestEveryWorkflowProgressOrderingCompletesExactlyOnce(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 2, 30, 0, 0, time.UTC)
	workflowTypes := []string{
		ProvisionWorkflowType, BootstrapWorkflowType, SleepWorkflowType, WakeWorkflowType,
		ArchiveWorkflowType, RestoreWorkflowType, TerminationWorkflowType, "ReconcileSession",
	}
	for _, workflowType := range workflowTypes {
		workflowType := workflowType
		t.Run(workflowType, func(t *testing.T) {
			t.Parallel()
			session, err := NewSession(NewSessionInput{
				ID: "session-" + strings.ToLower(workflowType), Slug: "progress", DisplayName: "Progress",
				GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
			}, now)
			if err != nil {
				t.Fatal(err)
			}
			if err := session.AcquireWorkflowLock("workflow-1", workflowType, time.Hour, now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			milestones, _ := MilestonesForWorkflow(workflowType)
			for index, milestone := range milestones[1:] {
				at := now.Add(time.Duration(index+2) * time.Minute)
				changed, err := session.AdvanceProgress("workflow-1", milestone, at)
				if err != nil || !changed {
					t.Fatalf("advance %s changed=%t err=%v progress=%#v", milestone, changed, err, session.Progress)
				}
				changed, err = session.AdvanceProgress("workflow-1", milestone, at.Add(time.Second))
				if err != nil || changed {
					t.Fatalf("replay %s changed=%t err=%v", milestone, changed, err)
				}
			}
			if session.Progress.Milestone != ProgressCompleted || session.Progress.State != ProgressCompletedState || !slices.Equal(session.Progress.CompletedMilestones, milestones) {
				t.Fatalf("completed progress = %#v; want %#v", session.Progress, milestones)
			}
			version := session.Version
			if changed, err := session.AdvanceProgress("workflow-1", milestones[1], now.Add(2*time.Hour)); err != nil || changed || session.Version != version {
				t.Fatalf("late replay changed=%t version=%d err=%v", changed, session.Version, err)
			}
		})
	}
}

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
	if err := session.AcquireWorkflowLock("workflow-1", ProvisionWorkflowType, time.Hour, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if session.Progress.Milestone != ProgressAccepted || session.Progress.WorkflowID != "workflow-1" {
		t.Fatalf("accepted progress = %#v", session.Progress)
	}

	version := session.Version
	changed, err := session.AdvanceProgress("workflow-1", ProgressComputeReady, now.Add(2*time.Minute))
	if err != nil || !changed || session.Version != version+1 {
		t.Fatalf("game/content progress = %#v, changed=%t, err=%v", session.Progress, changed, err)
	}
	version = session.Version
	changed, err = session.AdvanceProgress("workflow-1", ProgressComputeReady, now.Add(3*time.Minute))
	if err != nil || changed || session.Version != version {
		t.Fatalf("repeated progress changed=%t version=%d err=%v", changed, session.Version, err)
	}
	changed, err = session.AdvanceProgress("workflow-1", ProgressCapacityReserved, now.Add(4*time.Minute))
	if err != nil || changed || session.Progress.Milestone != ProgressComputeReady {
		t.Fatalf("stale progress = %#v, changed=%t, err=%v", session.Progress, changed, err)
	}
	if _, err := session.AdvanceProgress("other-workflow", ProgressCompleted, now.Add(5*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("other workflow error = %v; want ErrConflict", err)
	}
	if _, err := session.AdvanceProgress("workflow-1", ProgressCompleted, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	wantCompleted := []ProgressMilestone{ProgressAccepted, ProgressComputeReady, ProgressCompleted}
	if !reflect.DeepEqual(session.Progress.CompletedMilestones, wantCompleted) || session.Progress.StartedAt != now.Add(time.Minute) || session.Progress.LastProgressAt != now.Add(5*time.Minute) {
		t.Fatalf("completed progress = %#v; want milestones %#v and operation clocks", session.Progress, wantCompleted)
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
	session.ActiveWorkflowType = ProvisionWorkflowType
	session.ActiveWorkflowStartedAt = now.Add(time.Minute)
	session.ActiveWorkflowLeaseExpiresAt = now.Add(time.Hour)
	session.UpdatedAt = now.Add(time.Minute)
	if err := session.Validate(); err != nil {
		t.Fatalf("legacy active session error = %v", err)
	}
	changed, err := session.AdvanceProgress("workflow-legacy", ProgressInfrastructureReady, now.Add(2*time.Minute))
	if err != nil || !changed || session.Progress.Milestone != ProgressInfrastructureReady {
		t.Fatalf("migrated progress = %#v, changed=%t, err=%v", session.Progress, changed, err)
	}
}

func TestProgressOutcomesNeverCompleteOrRewindCheckpoints(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 3, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-outcomes", Slug: "session-outcomes", DisplayName: "Outcomes",
		GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AcquireWorkflowLock("wake-1", WakeWorkflowType, time.Hour, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AdvanceProgress("wake-1", ProgressComputeReady, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AdvanceProgress("wake-1", ProgressModsApplied, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	changed, err := session.SkipProgress("wake-1", ProgressModsApplied, ProgressServiceStarted, now.Add(4*time.Minute))
	if err != nil || !changed {
		t.Fatalf("skip changed=%t err=%v", changed, err)
	}
	if _, err := session.AdvanceProgress("wake-1", ProgressHealthVerification, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	before := slices.Clone(session.Progress.CompletedMilestones)
	changed, err = session.SkipProgress("wake-1", ProgressModsApplied, ProgressServiceStarted, now.Add(5*time.Minute))
	if err != nil || changed {
		t.Fatalf("skip replay changed=%t err=%v", changed, err)
	}
	for _, state := range []ProgressState{ProgressWaiting, ProgressRetrying, ProgressRollingBack, ProgressActionRequired} {
		changed, err = session.SetProgressState("wake-1", state, now.Add(-time.Hour))
		if err != nil || !changed {
			t.Fatalf("state %s changed=%t err=%v", state, changed, err)
		}
		if !slices.Equal(session.Progress.CompletedMilestones, before) || session.Progress.LastProgressAt.Before(now.Add(4*time.Minute)) {
			t.Fatalf("state %s changed checkpoint facts or regressed clock: %#v", state, session.Progress)
		}
	}
	if _, err := session.AdvanceProgress("wake-1", ProgressCompleted, now.Add(6*time.Minute)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("action-required advance error = %v", err)
	}
	if session.Progress.Milestone != ProgressHealthVerification || !slices.Equal(session.Progress.SkippedMilestones, []ProgressMilestone{ProgressModsApplied}) {
		t.Fatalf("terminal progress = %#v", session.Progress)
	}
}

func TestApplyProgressSequenceKeepsNonApplicableModStepSkipped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 3, 30, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-vanilla-progress", Slug: "vanilla-progress", DisplayName: "Vanilla",
		GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AcquireWorkflowLock("bootstrap-1", BootstrapWorkflowType, time.Hour, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	ordered, _ := MilestonesForWorkflow(BootstrapWorkflowType)
	changed, err := session.ApplyProgressSequence("bootstrap-1", ordered[1:len(ordered)-1], []ProgressMilestone{ProgressModsApplied}, now.Add(2*time.Minute))
	if err != nil || !changed {
		t.Fatalf("apply changed=%t err=%v", changed, err)
	}
	if session.Progress.Milestone != ProgressHealthVerification || !slices.Equal(session.Progress.SkippedMilestones, []ProgressMilestone{ProgressModsApplied}) || slices.Contains(session.Progress.CompletedMilestones, ProgressModsApplied) {
		t.Fatalf("vanilla progress = %#v", session.Progress)
	}
	if _, err := session.ApplyProgressSequence("bootstrap-1", ordered[1:len(ordered)-1], []ProgressMilestone{ProgressModsApplied}, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("replayed sequence error = %v", err)
	}
}

func TestCancelledProgressIsReplaySafeAndTerminal(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 4, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionInput{
		ID: "session-cancel", Slug: "session-cancel", DisplayName: "Cancel",
		GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AcquireWorkflowLock("archive-1", ArchiveWorkflowType, time.Hour, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if changed, err := session.SetProgressState("archive-1", ProgressCancelled, now.Add(2*time.Minute)); err != nil || !changed {
		t.Fatalf("cancel changed=%t err=%v", changed, err)
	}
	if changed, err := session.SetProgressState("archive-1", ProgressCancelled, now.Add(3*time.Minute)); err != nil || changed {
		t.Fatalf("cancel replay changed=%t err=%v", changed, err)
	}
	if _, err := session.AdvanceProgress("archive-1", ProgressArchiveVerified, now.Add(4*time.Minute)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("cancelled advance error = %v", err)
	}
}
