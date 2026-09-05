package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

func TestObserveSelectsInstallationIntervalAndPreservesDeadline(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	repository, workflow := seedBootstrap(t, now)
	runner := &testRunner{commandID: "command-1", status: ports.BootstrapCommandStatus{Status: "InProgress"}}
	clock := &testClock{now: now}
	service, err := NewService(repository, repository, repository, runner, nil, &testIDs{values: []string{"prepare-event"}}, clock, 6*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := TaskRequest{Action: ActionPrepare, SessionID: workflow.SessionID, WorkflowID: workflow.ID}
	if _, err := service.Handle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.Action = ActionDispatch
	dispatched, err := service.Handle(context.Background(), request)
	if err != nil || dispatched.NextPollSeconds != 30 {
		t.Fatalf("dispatch %#v %v", dispatched, err)
	}
	request.Action = ActionObserve
	request.CommandID = dispatched.CommandID
	for _, test := range []struct {
		active  bool
		elapsed time.Duration
		wait    int
		done    bool
	}{
		{false, 0, 30, false}, {true, time.Minute, 120, false}, {false, 2 * time.Minute, 30, false},
		{true, 6*time.Hour - 17*time.Second, 17, false}, {true, 6 * time.Hour, 120, true},
	} {
		runner.status.InstallationActive = test.active
		clock.now = now.Add(test.elapsed)
		got, err := service.Handle(context.Background(), request)
		if err != nil || got.NextPollSeconds != test.wait || got.Done != test.done {
			t.Fatalf("%#v: %#v %v", test, got, err)
		}
	}
}

func TestBootstrapWorkflowUsesObserverWaitAndRetainsRollbackCadence(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "infra", "terraform", "environments", "dev", "phase6.tf"))
	if err != nil {
		t.Fatal(err)
	}
	definition := strings.ReplaceAll(string(body), "\r\n", "\n")
	for _, required := range []string{`SecondsPath = "$.observation.result.next_poll_seconds"`, `"command_id.$"     = "$.observation.result.command_id"`, "WaitForRollback = {\n        Type    = \"Wait\"\n        Seconds = 30"} {
		if !strings.Contains(definition, required) {
			t.Fatalf("missing workflow contract %s", required)
		}
	}
	if strings.Count(definition, `ResultPath     = "$.observation"`) != 2 {
		t.Fatal("dispatch and observe must both populate the next wait input")
	}
}
