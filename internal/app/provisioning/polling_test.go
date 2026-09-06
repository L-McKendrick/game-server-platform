package provisioning

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type pollingCompute struct {
	testCompute
	ready   bool
	managed bool
}

func (compute *pollingCompute) ObserveInstance(context.Context, string) (domain.ComputeObservation, error) {
	if compute.ready {
		return compute.testCompute.ObserveInstance(context.Background(), "i-1")
	}
	return domain.ComputeObservation{InstanceID: "i-1", State: "pending"}, nil
}
func (compute *pollingCompute) IsManaged(context.Context, string) (bool, error) {
	return compute.managed, nil
}

func TestProvisioningPollsBoundEachStageAndReplay(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	repository, workflow := seededRepository(t, now)
	compute := &pollingCompute{}
	service := &Service{sessions: repository, stages: repository, workflows: repository, compute: compute, ids: &testIDs{}, clock: testClock{now}, config: Config{Project: "game-server-platform", Environment: "dev", AMIID: "ami-1", InstanceType: "c7i.large", SubnetID: "subnet-1", GameSecurityGroupID: "sg-game", VoiceSecurityGroupID: "sg-voice", InstanceProfile: "instance-profile", RootVolumeGiB: 30, DataVolumeGiB: 100, MaxProvisioned: 1}}
	request := TaskRequest{Action: ActionPrepare, SessionID: workflow.SessionID, WorkflowID: workflow.ID}
	if _, err := service.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Action = ActionEnsure
	initial, err := service.Handle(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Attempts = initial.Attempts
	for _, action := range []string{ActionObserve, ActionCheckManaged} {
		request.Action = action
		for i := 1; i <= 40; i++ {
			got, err := service.Handle(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := service.Handle(context.Background(), request)
			if err != nil || replay.Attempts != got.Attempts || replay.Exhausted != got.Exhausted {
				t.Fatalf("replay: %#v %#v %v", got, replay, err)
			}
			if got.Exhausted != (i == 40) {
				t.Fatalf("%s observation %d: %#v", action, i, got)
			}
			encoded, _ := json.Marshal(got)
			var wire map[string]any
			if err := json.Unmarshal(encoded, &wire); err != nil {
				t.Fatal(err)
			}
			if wire["ready"] != false || wire["managed"] != false {
				t.Fatalf("ASL requires explicit booleans: %s", encoded)
			}
			if i < 40 {
				request.Attempts = got.Attempts
			}
		}
		// A terminal readiness observation wins on the final allowed attempt.
		if action == ActionObserve {
			compute.ready = true
		} else {
			compute.managed = true
		}
		got, err := service.Handle(context.Background(), request)
		if err != nil || got.Exhausted || (action == ActionObserve && !got.Ready) || (action == ActionCheckManaged && !got.Managed) {
			t.Fatalf("final readiness: %#v %v", got, err)
		}
		request.Attempts = got.Attempts
	}
}

func TestProvisioningPollCountersRejectInvalidInput(t *testing.T) {
	for _, input := range []PollAttempts{{Instance: -1}, {SSM: -1}, {Instance: 41}, {SSM: 41}} {
		if _, _, err := nextPollAttempts(input, false, false); err == nil {
			t.Fatalf("accepted %#v", input)
		}
	}
}

// The ASL wiring must carry the returned counters into the next invocation.
func TestProvisioningWorkflowCarriesCountersWithoutCounterStates(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "infra", "terraform", "environments", "dev", "phase5.tf"))
	if err != nil {
		t.Fatal(err)
	}
	definition := string(body)
	for _, obsolete := range []string{"InitializeAttempts =", "IncrementInstanceAttempts =", "InstanceAttemptsAvailable =", "IncrementSSMAttempts =", "SSMAttemptsAvailable ="} {
		if strings.Contains(definition, obsolete) {
			t.Fatalf("counter-only state remains: %s", obsolete)
		}
	}
	if strings.Count(definition, `"attempts.$"       = "$.stage.result.attempts"`) != 2 || strings.Count(definition, `Variable = "$.stage.result.exhausted"`) != 2 {
		t.Fatal("both readiness loops must carry and check observer counters")
	}
	for _, loop := range []string{`Seconds = 15, Next = "ObserveInstance"`, `Seconds = 15, Next = "CheckManagedNode"`} {
		if !strings.Contains(definition, loop) {
			t.Fatalf("readiness cadence changed: %s", loop)
		}
	}
}
