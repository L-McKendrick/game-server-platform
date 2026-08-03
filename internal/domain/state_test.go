package domain

import "testing"

func TestLifecycleTransitionAllowed(t *testing.T) {
	t.Parallel()

	if !StateDraft.CanTransition(StateNew) {
		t.Fatal("expected DRAFT -> NEW to be allowed")
	}
}

func TestLifecycleTransitionRejected(t *testing.T) {
	t.Parallel()

	if StateDraft.CanTransition(StateRunning) {
		t.Fatal("expected DRAFT -> RUNNING to be rejected")
	}
}

func TestLifecycleStatesAreValid(t *testing.T) {
	t.Parallel()

	states := []LifecycleState{
		StateDraft,
		StateNew,
		StateValidating,
		StateProvisioning,
		StateBootstrapping,
		StateInstalling,
		StateReady,
		StateRunning,
		StateIdle,
		StateStopping,
		StateSleeping,
		StateWaking,
		StateWarning1,
		StateWarning2,
		StateArchiving,
		StateDestroying,
		StateArchived,
		StateRestoring,
		StateDeleting,
		StateDeleted,
		StateFailed,
	}

	for _, state := range states {
		if !state.Valid() {
			t.Errorf("expected state %q to be valid", state)
		}
	}
}
