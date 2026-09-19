package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestLiveMissionAccessRequiresCurrentEligibilityWithoutWorkflow(t *testing.T) {
	for _, scenario := range []string{"running", "idle", "locked", "sleeping", "unaccepted", "replacement-during-signing", "removed-during-signing", "wrong-tags"} {
		t.Run(scenario, func(t *testing.T) {
			key := "sessions/session/input/missions/" + strings.Repeat("a", 64) + "-mission.pbo"
			records := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", LifecycleState: domain.StateRunning, MissionFiles: []domain.MissionRecord{{ObjectKey: key, Filename: "mission.pbo", Status: domain.ArtifactAccepted}}}}
			records.session.Infrastructure.InstanceID = "i-123"
			signer := &inputSigner{}
			allowedTags := true
			switch scenario {
			case "idle":
				records.session.LifecycleState = domain.StateIdle
			case "locked":
				records.session.ActiveWorkflowID = "other"
			case "sleeping":
				records.session.LifecycleState = domain.StateSleeping
			case "unaccepted":
				records.session.MissionFiles[0].Status = domain.ArtifactRejected
			case "replacement-during-signing":
				signer.mutate = func() { records.session.Infrastructure.InstanceID = "i-other" }
			case "removed-during-signing":
				signer.mutate = func() { records.session.MissionFiles[0].RemovedAt = time.Now() }
			case "wrong-tags":
				allowedTags = false
			}
			issuer := HostObjectIssuer{Authority: HostAccessAuthority{Records: records, Instances: instanceAuthority(allowedTags)}, Signer: signer}
			manifest, err := issuer.SignLiveMission(context.Background(), "session", key)
			if scenario == "running" || scenario == "idle" {
				if err != nil {
					t.Fatal(err)
				}
				if len(manifest.Objects) != 1 || manifest.Objects[0].Object.Key != key || manifest.Scope.DeadlineAt.After(time.Now().Add(hostLiveCopyLifetime)) || records.session.ActiveWorkflowID != "" {
					t.Fatal("live copy scope or eligibility changed")
				}
				if issuer.Authority.ValidateLiveMission(context.Background(), manifest.Scope, key, time.Now()) != nil {
					t.Fatal("fresh live authority invalid")
				}
				changed := manifest.Scope
				changed.OperationID = "other"
				if issuer.Authority.ValidateLiveMission(context.Background(), changed, key, time.Now()) == nil {
					t.Fatal("accepted unrelated operation identity")
				}
			} else if err == nil || len(manifest.Objects) != 0 {
				t.Fatal("unauthorized live copy released capability")
			}
		})
	}
}
