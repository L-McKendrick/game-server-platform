package domain

import (
	"errors"
	"testing"
	"time"
)

func TestRestartLifecycleAndPendingRevisionAuthority(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	for _, state := range []LifecycleState{StateRunning, StateIdle, StateSleeping, StateArchived, StateDraft, StateFailed, StateRestarting} {
		t.Run(string(state), func(t *testing.T) {
			session, err := NewSession(NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner", GuildID: "guild", ChannelID: "channel"}, now)
			if err != nil {
				t.Fatal(err)
			}
			session.LifecycleState, session.ObservedState, session.DesiredState = state, state, state
			session.Infrastructure = Infrastructure{CapacitySlotID: "slot-0", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile", AMIID: "ami-1", InstanceType: "c7i.large", InstanceID: "i-1", DataVolumeID: "vol-1", LastObservedAt: now}
			session.ActivePresetRevision = PresetRevision{Number: 1, PresetObjectKey: "sessions/session-1/input/presets/active.html", Status: PresetRevisionActive, StagedAt: now, ActivatedAt: now}
			session.PresetObjectKey = session.ActivePresetRevision.PresetObjectKey
			session.PresetRevisionSequence = 2
			session.PendingPresetRevision = PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/presets/pending.html", Status: PresetRevisionPending, StagedAt: now}
			session.PendingServerPresetRevision = PresetRevision{Number: 1, PresetObjectKey: "sessions/session-1/input/server-presets/pending.html", Status: PresetRevisionPending, StagedAt: now}
			session.ServerPresetRevisionSequence = 1
			before := session.Version
			err = session.BeginRestart("restart", time.Hour, now)
			if state != StateRunning && state != StateIdle {
				if !errors.Is(err, ErrInvalidTransition) || session.Version != before {
					t.Fatalf("invalid state mutation: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if session.Version != before+1 || session.LifecycleState != StateRestarting || session.ActivePresetRevision.Number != 1 || session.PendingPresetRevision.Status != PresetRevisionApplying || session.PendingServerPresetRevision.Status != PresetRevisionApplying {
				t.Fatalf("start=%#v", session)
			}
			if err := session.BeginRestart("conflicting", time.Hour, now); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("lock=%v", err)
			}
			failed := session
			if err := failed.FailActiveWorkflowForReconciliation("restart", now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if failed.PendingPresetRevision.Status != PresetRevisionFailed || failed.PendingServerPresetRevision.Status != PresetRevisionFailed || failed.ActiveWorkflowID != "" || failed.LifecycleState != StateFailed {
				t.Fatal("reconciliation left applying revisions or lock")
			}
			before = session.Version
			if err := session.CompleteRestart("wrong", nil, now); !errors.Is(err, ErrConflict) || session.Version != before {
				t.Fatal("wrong workflow completed")
			}
			if err := session.CompleteRestart("restart", nil, now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			if session.Version != before+1 || session.LifecycleState != StateRunning || session.ActiveWorkflowID != "" || session.ActivePresetRevision.Number != 2 || session.ActiveServerPresetRevision.Number != 1 {
				t.Fatalf("complete=%#v", session)
			}
		})
	}
}
