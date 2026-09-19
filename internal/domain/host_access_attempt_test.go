package domain

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHostAccessRecoveryRejectsReplayAndIdentityReplacement(t *testing.T) {
	now := time.Now().UTC()
	current := HostAccessAttempt{Scope: HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: now.Add(time.Hour)}, Revision: 1, Generation: 1, ManifestVersionID: "version-1", ManifestSHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), ManifestSizeBytes: 100, ExpiresAt: now.Add(10 * time.Minute), DispatchState: HostAccessPrepared}
	if err := ValidateHostAccessUpdate(HostAccessAttempt{}, current, 0); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(current)
	var recovered HostAccessAttempt
	if err := json.Unmarshal(encoded, &recovered); err != nil {
		t.Fatal(err)
	}
	recoveredNext := recovered
	recoveredNext.Revision++
	recoveredNext.DispatchState = HostAccessDispatched
	if !current.Scope.Equal(recovered.Scope) || ValidateHostAccessUpdate(current, recoveredNext, 1) != nil {
		t.Fatal("JSON recovery changed deadline authority")
	}
	for _, scenario := range []struct {
		name   string
		edit   func(*HostAccessAttempt)
		denied bool
	}{
		{"ambiguous", func(n *HostAccessAttempt) { n.DispatchState = HostAccessAmbiguous }, false},
		{"dispatch", func(n *HostAccessAttempt) { n.DispatchState = HostAccessDispatched }, false},
		{"finished", func(n *HostAccessAttempt) { n.DispatchState = HostAccessFinished }, false},
		{"replay", func(n *HostAccessAttempt) { n.Revision = 1 }, true},
		{"new-instance", func(n *HostAccessAttempt) { n.Scope.InstanceID = "i-other" }, true},
		{"deadline-extension", func(n *HostAccessAttempt) { n.Scope.DeadlineAt = n.Scope.DeadlineAt.Add(time.Hour) }, true},
		{"replace-version", func(n *HostAccessAttempt) { n.ManifestVersionID = "other"; n.DispatchState = HostAccessDispatched }, true},
		{"refresh", func(n *HostAccessAttempt) {
			n.Generation++
			n.ManifestVersionID = "version-2"
			n.ExpiresAt = n.ExpiresAt.Add(time.Minute)
		}, false},
		{"unhelpful-refresh", func(n *HostAccessAttempt) { n.Generation++ }, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			next := current
			next.Revision++
			scenario.edit(&next)
			err := ValidateHostAccessUpdate(current, next, 1)
			if (err != nil) != scenario.denied {
				t.Fatalf("denied=%v error=%v", scenario.denied, err)
			}
		})
	}
	finished := current
	finished.DispatchState = HostAccessFinished
	next := finished
	next.Revision++
	next.DispatchState = HostAccessDispatched
	if ValidateHostAccessUpdate(finished, next, 1) == nil {
		t.Fatal("resurrected finished attempt")
	}
}

func TestWorkshopOutputVersionsArePinnedOnceAndSurviveRefresh(t *testing.T) {
	now := time.Now().UTC()
	digest := base64.StdEncoding.EncodeToString(make([]byte, 32))
	current := HostAccessAttempt{Scope: HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: now.Add(time.Hour)}, Revision: 1, Generation: 1, ManifestVersionID: "manifest", ManifestSHA256: digest, ManifestSizeBytes: 100, ExpiresAt: now.Add(10 * time.Minute), DispatchState: HostAccessPrepared}
	pinned := current
	pinned.Revision++
	pinned.WorkshopOutputs = map[string]HostOutputVersion{"workshop-result": {VersionID: "result-v1", SHA256: digest, SizeBytes: 100}, "workshop-resolution": {VersionID: "resolution-v1", SHA256: digest, SizeBytes: 100}, "mission-42": {VersionID: "mission-v1", SHA256: digest, SizeBytes: 16}}
	if err := ValidateHostAccessUpdate(current, pinned, current.Revision); err != nil {
		t.Fatal("could not pin validated output set", err)
	}
	if ValidateHostAccessUpdate(HostAccessAttempt{}, func() HostAccessAttempt { n := pinned; n.Revision = 1; return n }(), 0) == nil {
		t.Fatal("outputs accepted before initial issuance")
	}
	for _, scenario := range []string{"removed", "version", "digest", "size", "additional", "invalid-slot", "oversized", "null-version"} {
		t.Run(scenario, func(t *testing.T) {
			next := pinned
			next.Revision++
			next.DispatchState = HostAccessDispatched
			next.WorkshopOutputs = make(map[string]HostOutputVersion)
			for slot, output := range pinned.WorkshopOutputs {
				next.WorkshopOutputs[slot] = output
			}
			output := next.WorkshopOutputs["mission-42"]
			switch scenario {
			case "removed":
				delete(next.WorkshopOutputs, "mission-42")
			case "version":
				output.VersionID = "later-host-write"
			case "digest":
				output.SHA256 = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
			case "size":
				output.SizeBytes++
			case "additional":
				next.WorkshopOutputs["mission-43"] = output
			case "invalid-slot":
				next.WorkshopOutputs["mission-042"] = output
			case "oversized":
				output.SizeBytes = MaximumWorkshopMissionBytes + 1
			case "null-version":
				output.VersionID = "null"
			}
			if scenario != "removed" {
				next.WorkshopOutputs["mission-42"] = output
			}
			if ValidateHostAccessUpdate(pinned, next, pinned.Revision) == nil {
				t.Fatal("replaced immutable output evidence")
			}
		})
	}
	refreshed := pinned
	refreshed.Revision++
	refreshed.Generation++
	refreshed.ManifestVersionID = "refreshed-manifest"
	refreshed.ExpiresAt = refreshed.ExpiresAt.Add(time.Minute)
	if ValidateHostAccessUpdate(pinned, refreshed, pinned.Revision) != nil {
		t.Fatal("refresh lost valid output pins")
	}
	encoded, _ := json.Marshal(refreshed)
	var recovered HostAccessAttempt
	if json.Unmarshal(encoded, &recovered) != nil || !reflect.DeepEqual(recovered.WorkshopOutputs, pinned.WorkshopOutputs) {
		t.Fatal("output evidence did not survive recovery")
	}
}
