package app

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type workshopIntentStore struct {
	read func(string, string) ([]byte, domain.HostOutputVersion, error)
}

func (store workshopIntentStore) PublishHostDocument(context.Context, string, string, []byte) (string, error) {
	panic("unexpected intent publication")
}

func (store workshopIntentStore) ReadHostOutput(_ context.Context, key, version, _ string, _ int64) ([]byte, domain.HostOutputVersion, error) {
	return store.read(key, version)
}
func (store workshopIntentStore) PromoteHostOutput(context.Context, string, string, string, domain.HostOutputVersion) (string, error) {
	panic("unexpected intent publication")
}

func TestPresetIntentMatchesHostExtractionAndBounds(t *testing.T) {
	ids, err := hostPresetIDs([]byte(`<a href="?id=42">x</a><span DATA-PUBLISHEDFILEID='43'>y</span><a id=42>`))
	if err != nil || !reflect.DeepEqual(ids, []string{"42", "43"}) {
		t.Fatal("host preset extraction changed", ids, err)
	}
	for _, body := range []string{"id=0", "id=042", "id=18446744073709551616"} {
		if _, err := hostPresetIDs([]byte(body)); err == nil {
			t.Fatal("invalid item identity accepted")
		}
	}
	var oversized strings.Builder
	for id := 1; id <= domain.MaximumWorkshopModItems+1; id++ {
		oversized.WriteString("id=" + fmt.Sprint(id) + " ")
	}
	if _, err := hostPresetIDs([]byte(oversized.String())); err == nil {
		t.Fatal("unbounded mod inventory accepted")
	}
}

func TestExpectedWorkshopItemsUsePinnedPresetsAndCurrentAuthority(t *testing.T) {
	for _, scenario := range []string{"valid", "changed-digest", "cancelled-during-read", "mission-only"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC()
			records := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", ActiveWorkflowID: "operation", ActiveWorkflowType: domain.BootstrapWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour)}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.BootstrapWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
			records.session.Infrastructure.InstanceID = "i-123"
			scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: HostContentSnapshot(records.session), DeadlineAt: now.Add(30 * time.Minute)}
			client, server := []byte("id=42 id=43"), []byte("id=43 id=44")
			objects := []domain.HostObjectRequest{{Purpose: domain.HostObjectServerPreset, Key: "server", VersionID: "server-v1", SHA256: manifestDigest(server), MaxBytes: 100}, {Purpose: domain.HostObjectClientPreset, Key: "client", VersionID: "client-v1", SHA256: manifestDigest(client), MaxBytes: 100}, {Purpose: domain.HostObjectWorkshopMission, Slot: "mission-45"}}
			reads := 0
			delivery := HostCommandDelivery{Issuer: HostManifestIssuer{Inputs: HostObjectIssuer{Authority: HostAccessAuthority{Records: records, Instances: instanceAuthority(true)}}}, WorkshopStore: workshopIntentStore{read: func(key, version string) ([]byte, domain.HostOutputVersion, error) {
				reads++
				body := client
				if key == "server" {
					body = server
					if version != "server-v1" {
						t.Fatal("server version not pinned")
					}
				} else if key != "client" || version != "client-v1" {
					t.Fatal("unexpected preset read")
				}
				pin := domain.HostOutputVersion{VersionID: version, SHA256: manifestDigest(body), SizeBytes: int64(len(body))}
				if scenario == "changed-digest" {
					pin.SHA256 = manifestDigest([]byte("changed"))
				}
				if scenario == "cancelled-during-read" {
					records.workflow.CancelRequestedAt = time.Now()
				}
				return body, pin, nil
			}}}
			environment := map[string]string{"WORKSHOP_MISSION_REVISION_B64": "mission-revision", "PRESET_REVISION_B64": "2", "WORKSHOP_MOD_RESOLUTION_B64": "client-resolution", "SERVER_PRESET_REVISION_B64": "3"}
			if scenario == "mission-only" {
				environment["HOST_WORKSHOP_TARGET_B64"] = "mission"
			}
			target, items, err := delivery.workshopExpectedItems(context.Background(), scope, objects, environment)
			if scenario == "valid" {
				want := []hostWorkshopResultItem{{Target: "mission", PublishedFileID: "45", Revision: "mission-revision", Status: "succeeded"}, {Target: "mod", PublishedFileID: "42", Revision: "client-resolution", Status: "succeeded"}, {Target: "mod", PublishedFileID: "43", Revision: "client-resolution", Status: "succeeded"}, {Target: "server_mod", PublishedFileID: "44", Revision: "3", Status: "succeeded"}}
				if err != nil || target != "all" || reads != 2 || !reflect.DeepEqual(items, want) {
					t.Fatal("expected inventory changed", items, err)
				}
			} else if scenario == "mission-only" {
				if err != nil || target != "mission" || reads != 0 || len(items) != 1 {
					t.Fatal("mission-only read preset", err)
				}
			} else if err == nil || items != nil {
				t.Fatal("untrusted or cancelled intent accepted")
			}
		})
	}
}
