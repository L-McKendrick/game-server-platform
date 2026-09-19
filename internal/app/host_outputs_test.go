package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/hostaccess"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestOutputManifestsEnforcePurposeAndFixedSlots(t *testing.T) {
	for _, scenario := range []struct {
		name                        string
		purpose                     domain.HostObjectPurpose
		slot, contentType, workflow string
		noApprovedItems             bool
		denied                      bool
	}{
		{"progress", domain.HostObjectProgress, "progress", "text/plain", domain.BootstrapWorkflowType, false, false},
		{"arbitrary-progress-slot", domain.HostObjectProgress, "other", "text/plain", domain.BootstrapWorkflowType, false, true},
		{"diagnostic", domain.HostObjectDiagnostic, "diagnostic", "text/plain", domain.ArchiveWorkflowType, false, false},
		{"wrong-diagnostic-type", domain.HostObjectDiagnostic, "diagnostic", "application/json", domain.ArchiveWorkflowType, false, true},
		{"accepted-item", domain.HostObjectWorkshopMission, "mission-42", "application/octet-stream", domain.BootstrapWorkflowType, false, false},
		{"unaccepted-item", domain.HostObjectWorkshopMission, "mission-43", "application/octet-stream", domain.BootstrapWorkflowType, false, true},
		{"archive-cannot-download-workshop", domain.HostObjectWorkshopMission, "mission-42", "application/octet-stream", domain.ArchiveWorkflowType, false, true},
		{"sync-result", domain.HostObjectWorkshopResult, "workshop-result", "application/json", domain.WorkshopContentSyncWorkflowType, false, false},
		{"bootstrap-sync-result", domain.HostObjectWorkshopResult, "workshop-result", "application/json", domain.BootstrapWorkflowType, false, false},
		{"archive-cannot-publish-sync-result", domain.HostObjectWorkshopResult, "workshop-result", "application/json", domain.ArchiveWorkflowType, false, true},
		{"mission-resolution", domain.HostObjectWorkshopResolution, "workshop-resolution", "text/tab-separated-values", domain.BootstrapWorkflowType, false, false},
		{"wake-mission-resolution", domain.HostObjectWorkshopResolution, "workshop-resolution", "text/tab-separated-values", domain.WakeWorkflowType, false, false},
		{"wrong-resolution-type", domain.HostObjectWorkshopResolution, "workshop-resolution", "application/json", domain.BootstrapWorkflowType, false, true},
		{"no-approved-items-resolution", domain.HostObjectWorkshopResolution, "workshop-resolution", "text/tab-separated-values", domain.BootstrapWorkflowType, true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			now := time.Now().UTC()
			r := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", Version: 7, ActiveWorkflowID: "operation", ActiveWorkflowType: scenario.workflow, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour), WorkshopMissionSources: []domain.WorkshopMissionSource{{AcceptedItemIDs: []uint64{42}}}}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: scenario.workflow, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
			r.session.Infrastructure.InstanceID = "i-123"
			if scenario.noApprovedItems {
				r.session.WorkshopMissionSources = nil
			}
			scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: HostContentSnapshot(r.session), DeadlineAt: now.Add(30 * time.Minute)}
			object := domain.HostObjectRequest{Purpose: scenario.purpose, Slot: scenario.slot, Key: scope.StagingKey(scenario.slot), ContentType: scenario.contentType, MinBytes: 1, MaxBytes: 100}
			recovery := &manifestRecovery{}
			signer := hostaccess.NewSigner(aws.Config{Region: "us-west-2", Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
				return aws.Credentials{AccessKeyID: "key", SecretAccessKey: "secret"}, nil
			})}, "assets")
			issuer := HostManifestIssuer{Inputs: HostObjectIssuer{Authority: HostAccessAuthority{Records: r, Instances: instanceAuthority(true)}, Signer: signer}, Attempts: recovery, Exchange: recovery}
			reference, err := issuer.Issue(context.Background(), scope, []domain.HostObjectRequest{object})
			if (err != nil) != scenario.denied {
				t.Fatalf("denied=%v error=%v", scenario.denied, err)
			}
			if scenario.denied {
				if reference.URL != "" || recovery.writes != 0 {
					t.Fatal("unauthorized output was published")
				}
			} else if reference.Generation != 1 || !strings.Contains(reference.URL, "X-Amz-Signature") {
				t.Fatal("missing output manifest reference")
			}
		})
	}
}
