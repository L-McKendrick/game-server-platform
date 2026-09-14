package domain

import (
	"errors"
	"testing"
	"time"
)

func TestMaximumDurationLegacyAndBounds(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		seconds int64
		valid   bool
	}{
		{"legacy default", 0, true},
		{"minimum", MinimumMaximumDurationSeconds, true},
		{"maximum", MaximumMaximumDurationSeconds, true},
		{"too short", MinimumMaximumDurationSeconds - 1, false},
		{"too long", MaximumMaximumDurationSeconds + 1, false},
		{"negative", -1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := MaximumDuration{Seconds: test.seconds}
			if (policy.Validate() == nil) != test.valid {
				t.Fatalf("Validate() validity for %d differs from %v", test.seconds, test.valid)
			}
			if test.seconds == 0 && policy.EffectiveSeconds() != DefaultMaximumDurationSeconds {
				t.Fatal("legacy policy did not resolve to safe default")
			}
		})
	}
}

func TestMaximumDeadlineAutomationRevalidatesStateAndExtension(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	session := Session{ID: "session", GuildID: "guild", LifecycleState: StateRunning, Infrastructure: Infrastructure{InstanceID: "i-test", DataVolumeID: "vol-test"}, MaximumDuration: MaximumDuration{Seconds: 3600, StartedAt: now.Add(-time.Hour), DeadlineAt: now}}
	if action := session.MaximumDeadlineAction(now); action != CommandSleepSession {
		t.Fatalf("action = %q", action)
	}
	id := MaximumDeadlineCommandID(session.ID, now, CommandSleepSession)
	command := CommandEnvelope{SchemaVersion: 1, CommandID: id, CommandType: CommandSleepSession, Actor: CommandActor{System: true, DiscordUserID: InactivityMonitorActorID}, SessionID: session.ID, IdempotencyKey: "maximum-duration:" + id, CorrelationID: id, Parameters: map[string]string{MaximumDeadlineParameter: now.Format(time.RFC3339Nano)}}
	if err := ValidateAutomaticSleepCommand(command, session, now); err != nil {
		t.Fatal(err)
	}
	session.ActiveWorkflowID = "busy"
	if err := ValidateAutomaticSleepCommand(command, session, now); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("active lock accepted: %v", err)
	}
	session.ActiveWorkflowID = ""
	session.MaximumDuration.DeadlineAt = now.Add(time.Hour)
	if err := ValidateAutomaticSleepCommand(command, session, now); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("stale deadline accepted: %v", err)
	}
	session.MaximumDuration.DeadlineAt = now
	session.LifecycleState = StateSleeping
	if action := session.MaximumDeadlineAction(now); action != CommandArchiveSession {
		t.Fatalf("sleeping action = %q", action)
	}
	session.LifecycleState = StateFailed
	if action := session.MaximumDeadlineAction(now); action != "" {
		t.Fatalf("failed action = %q", action)
	}
}

func TestFailedInitialCreationIsOnlyAutomaticTerminationCandidate(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	failure, err := NewFailureRecord(FailureRecordInput{Code: "ERR_BOOTSTRAP_FAILED", Stage: "Game setup", RetryDisposition: RetryNotScheduled, ResourceImpact: ResourceCostRetained, Detail: "Setup failed", FailedAt: now, SupportReference: "ref_123456"})
	if err != nil {
		t.Fatal(err)
	}
	session := Session{ID: "session", LifecycleState: StateFailed, Failure: failure, MaximumDuration: MaximumDuration{Seconds: DefaultMaximumDurationSeconds, StartedAt: now.Add(-24 * time.Hour), DeadlineAt: now}, Progress: SessionProgress{WorkflowID: "bootstrap", WorkflowType: BootstrapWorkflowType}}
	if !session.FailedInitialCreation() || session.MaximumDeadlineAction(now) != CommandDestroySession {
		t.Fatal("initial bootstrap failure was not selected for termination")
	}
	commandID := MaximumDeadlineCommandID(session.ID, now, CommandDestroySession)
	command := CommandEnvelope{SchemaVersion: 1, CommandID: commandID, CommandType: CommandDestroySession, Actor: CommandActor{System: true, DiscordUserID: InactivityMonitorActorID}, SessionID: session.ID, IdempotencyKey: "maximum-duration:" + commandID, CorrelationID: commandID, Parameters: map[string]string{MaximumDeadlineParameter: now.Format(time.RFC3339Nano)}}
	if err := ValidateAutomaticTerminationCommand(command, session, now); err != nil {
		t.Fatal(err)
	}
	command.Actor.System = false
	if err := ValidateAutomaticTerminationCommand(command, session, now); !errors.Is(err, ErrForbidden) {
		t.Fatalf("forged actor accepted: %v", err)
	}
	command.Actor.System = true
	for _, workflowType := range []string{WakeWorkflowType, RestoreWorkflowType, RestartWorkflowType, TerminationWorkflowType} {
		session.Progress.WorkflowType = workflowType
		if session.FailedInitialCreation() || session.MaximumDeadlineAction(now) != "" {
			t.Fatalf("failed %s selected for deletion", workflowType)
		}
		if err := ValidateAutomaticTerminationCommand(command, session, now); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("failed %s command accepted: %v", workflowType, err)
		}
	}
	session.Progress.WorkflowType = BootstrapWorkflowType
	session.ActiveWorkflowID = "active"
	if session.MaximumDeadlineAction(now) != "" {
		t.Fatal("active workflow bypassed")
	}
	session.ActiveWorkflowID = ""
	session.MaximumDuration.DeadlineAt = now.Add(time.Hour)
	if session.MaximumDeadlineAction(now) != "" {
		t.Fatal("extension did not prevent termination")
	}
}

func TestMaximumDurationRequiresPairedOrderedTimestamps(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	for _, policy := range []MaximumDuration{
		{StartedAt: start},
		{DeadlineAt: start.Add(time.Hour)},
		{StartedAt: start, DeadlineAt: start},
		{StartedAt: start, DeadlineAt: start.Add(-time.Second)},
		{Seconds: 2 * 3600, StartedAt: start, DeadlineAt: start.Add(time.Hour)},
		{StartedAt: start, DeadlineAt: start.Add(8 * 24 * time.Hour)},
	} {
		if err := policy.Validate(); err == nil {
			t.Fatalf("accepted invalid policy %#v", policy)
		}
	}
	if err := (MaximumDuration{StartedAt: start, DeadlineAt: start.Add(24 * time.Hour)}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMaximumDurationClockAndExtension(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	policy := MaximumDuration{Seconds: 2 * 3600}
	if err := policy.Start(now); err != nil {
		t.Fatal(err)
	}
	if err := policy.Start(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !policy.StartedAt.Equal(now) || !policy.DeadlineAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("clock reset: %#v", policy)
	}
	if policy.Expired(now.Add(time.Hour)) || !policy.Expired(now.Add(2*time.Hour)) {
		t.Fatal("expiry boundary is wrong")
	}
	if err := policy.Extend(now.Add(3*time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if policy.Seconds != 3*3600 {
		t.Fatalf("extended limit = %d seconds", policy.Seconds)
	}
	if err := policy.Extend(now.Add(8*24*time.Hour), now); err == nil {
		t.Fatal("accepted extension past cap")
	}
	if !policy.DeadlineAt.Equal(now.Add(3 * time.Hour)) {
		t.Fatal("invalid extension mutated deadline")
	}
}

func TestMaximumDeadlineActionKeepsLaterFailuresAndLockedSessionsSafe(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	base := Session{ID: "session", Infrastructure: Infrastructure{InstanceID: "i-test", DataVolumeID: "vol-test", CapacitySlotID: "slot-1"}, MaximumDuration: MaximumDuration{Seconds: DefaultMaximumDurationSeconds, StartedAt: now.Add(-24 * time.Hour), DeadlineAt: now}}
	for _, test := range []struct {
		name       string
		state      LifecycleState
		lock, want string
	}{
		{"running", StateRunning, "", CommandSleepSession},
		{"idle", StateIdle, "", CommandSleepSession},
		{"sleeping", StateSleeping, "", CommandArchiveSession},
		{"restoring", StateRestoring, "", ""},
		{"archiving", StateArchiving, "", ""},
		{"destroying", StateDestroying, "", ""},
		{"failed later operation", StateFailed, "", ""},
		{"running with workflow lock", StateRunning, "workflow-1", ""},
		{"sleeping with workflow lock", StateSleeping, "workflow-1", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := base
			session.LifecycleState, session.ActiveWorkflowID = test.state, test.lock
			if got := session.MaximumDeadlineAction(now); got != test.want {
				t.Fatalf("action = %q; want %q", got, test.want)
			}
			if session.Infrastructure.CapacitySlotID != base.Infrastructure.CapacitySlotID || session.Infrastructure.DataVolumeID != base.Infrastructure.DataVolumeID {
				t.Fatal("deadline selection mutated retained resources or capacity")
			}
		})
	}
}
