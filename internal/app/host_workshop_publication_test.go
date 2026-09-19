package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type publicationInspector struct {
	calls    int
	checksum string
}

func (inspector *publicationInspector) InspectHostRead(context.Context, string, string, int64) (string, string, int64, error) {
	inspector.calls++
	return inspector.checksum, "output-v1", 16, nil
}

type publicationStore struct {
	bodies       map[string][]byte
	recovery     *manifestRecovery
	actions      []string
	failDocument bool
	lateWrite    bool
	onRead       func()
	onPromote    func()
}

func (store *publicationStore) ReadHostOutput(_ context.Context, key, version, _ string, _ int64) ([]byte, domain.HostOutputVersion, error) {
	if store.onRead != nil {
		store.onRead()
	}
	if version != "" && version != "output-v1" {
		return nil, domain.HostOutputVersion{}, domain.ErrConflict
	}
	body := store.bodies[key]
	if store.lateWrite && version == "" {
		body = []byte("late tampering")
	}
	return body, domain.HostOutputVersion{VersionID: "output-v1", SHA256: manifestDigest(body), SizeBytes: int64(len(body))}, nil
}
func (store *publicationStore) PromoteHostOutput(_ context.Context, _, destination, _ string, pin domain.HostOutputVersion) (string, error) {
	if len(store.recovery.current.WorkshopOutputs) != 3 || pin.VersionID != "output-v1" {
		return "", errors.New("publication before full-set pin")
	}
	store.actions = append(store.actions, destination)
	if store.onPromote != nil {
		store.onPromote()
	}
	return "durable-v1", nil
}
func (store *publicationStore) PublishHostDocument(_ context.Context, key, _ string, body []byte) (string, error) {
	if len(store.recovery.current.WorkshopOutputs) != 3 || len(body) == 0 {
		return "", errors.New("canonical publication before validation")
	}
	store.actions = append(store.actions, key)
	if store.failDocument {
		store.failDocument = false
		return "", errors.New("transient storage failure")
	}
	return "durable-v1", nil
}

func TestWorkshopPublicationPinsFullSetBeforeMutationAndRecoversPartialWrites(t *testing.T) {
	for _, scenario := range []string{"valid", "partial-replay", "bad-checksum", "cancelled", "pin-conflict", "cancelled-after-promotion", "wrong-revision", "rollback"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			source := domain.WorkshopMissionSource{Source: domain.WorkshopReference{PublishedFileID: 42, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=42"}, SourceKind: domain.WorkshopSourceItem, ResolutionSHA256: strings.Repeat("b", 64), AcceptedItemIDs: []uint64{42}, AcceptedItems: []domain.WorkshopMissionItem{{PublishedFileID: 42, Filename: "scenario.pbo", FileSize: 16}}, ResolvedAt: now}
			records := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", Version: 7, Vanilla: true, WorkshopMissionSources: []domain.WorkshopMissionSource{source}, ActiveWorkflowID: "operation", ActiveWorkflowType: domain.BootstrapWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour)}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.BootstrapWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
			records.session.Infrastructure.InstanceID = "i-123"
			if scenario == "rollback" {
				records.session.ActivePresetRevision = domain.PresetRevision{Number: 1, PresetObjectKey: "sessions/session/input/presets/" + strings.Repeat("d", 64) + "-active.html"}
				records.session.PendingPresetRevision = domain.PresetRevision{Number: 2, PresetObjectKey: "sessions/session/input/presets/" + strings.Repeat("e", 64) + "-pending.html", Status: domain.PresetRevisionApplying, ApplyWorkflowID: "operation"}
				records.session.Progress = domain.SessionProgress{WorkflowID: "operation", WorkflowType: domain.BootstrapWorkflowType, State: domain.ProgressRollingBack, StartedAt: now}
				records.workflow.CommandID = "prior-command"
				records.workflow.CommandDeadlineAt = now.Add(-time.Minute)
			}
			recovery := &manifestRecovery{}
			digest := strings.Repeat("a", 64)
			inspector := &publicationInspector{checksum: hostBase64Digest(digest)}
			store := &publicationStore{bodies: make(map[string][]byte), recovery: recovery, failDocument: scenario == "partial-replay"}
			delivery := HostCommandDelivery{Issuer: HostManifestIssuer{Inputs: HostObjectIssuer{Authority: HostAccessAuthority{Records: records, Instances: instanceAuthority(true)}, Signer: manifestSigner{expiry: now.Add(10 * time.Minute)}, Script: domain.HostObjectRequest{Purpose: domain.HostObjectBootstrapScript, Slot: "bootstrap-script", Key: "platform/bootstrap/script.sh", SHA256: hostBase64Digest(digest), MaxBytes: 1024}}, Attempts: recovery, Exchange: recovery}, Inspector: inspector, WorkshopStore: store}
			revision, err := records.session.WorkshopMissionRevision()
			if err != nil {
				t.Fatal(err)
			}
			stage := "bootstrap"
			if scenario == "rollback" {
				stage = "rollback"
			}
			ref, err := delivery.PrepareHostCommand(context.Background(), "session", stage, scenario == "rollback", 30*time.Minute, func(context.Context, map[string]string) (map[string]string, error) {
				return map[string]string{"VANILLA_MODE_B64": "true", "WORKSHOP_MISSION_REVISION_B64": revision}, nil
			})
			if err != nil {
				t.Fatal("prepare", err)
			}
			var prepared ports.HostAccessManifest
			if json.Unmarshal(recovery.versions[recovery.current.ManifestVersionID], &prepared) != nil {
				t.Fatal("prepared inventory unavailable")
			}
			for _, capability := range prepared.Objects {
				if capability.Object.Purpose == domain.HostObjectWorkshopMission && (capability.Object.MinBytes != 16 || capability.Object.MaxBytes != 16) {
					t.Fatal("resolved mission size not enforced in upload capability")
				}
			}
			missionKey := "sessions/session/input/missions/" + digest + "-scenario.pbo"
			row := digest + "\tscenario.pbo\t" + missionKey + "\t42\n"
			if scenario == "bad-checksum" {
				row = strings.ReplaceAll(row, digest, strings.Repeat("c", 64))
			}
			store.bodies[ref.Scope.StagingKey("workshop-resolution")] = []byte(row)
			result, _ := json.Marshal(map[string]any{"schema_version": 1, "session_id": "session", "workflow_id": "operation", "target": "all", "completed_at": now, "items": []hostWorkshopResultItem{{Target: "mission", PublishedFileID: "42", Revision: revision, Status: "succeeded"}}})
			store.bodies[ref.Scope.StagingKey("workshop-result")] = result
			if scenario == "wrong-revision" {
				store.bodies[ref.Scope.StagingKey("workshop-result")] = []byte(strings.ReplaceAll(string(result), revision, "unapproved"))
			}
			if scenario == "pin-conflict" {
				recovery.conflict = true
			}
			if scenario == "cancelled-after-promotion" {
				store.onPromote = func() { records.workflow.CancelRequestedAt = time.Now() }
			}
			if scenario == "cancelled" {
				store.onRead = func() { records.workflow.CancelRequestedAt = time.Now() }
			}
			err = delivery.PublishHostWorkshop(context.Background(), ref.Scope)
			if scenario == "bad-checksum" || scenario == "cancelled" || scenario == "pin-conflict" || scenario == "wrong-revision" {
				if err == nil || len(store.actions) != 0 || len(recovery.current.WorkshopOutputs) != 0 {
					t.Fatal("invalid output mutated durable state", err)
				}
				return
			}
			if scenario == "cancelled-after-promotion" {
				if err == nil || len(store.actions) != 1 || len(recovery.current.WorkshopOutputs) != 3 {
					t.Fatal("publication continued after authority changed", err)
				}
				return
			}
			if scenario == "partial-replay" {
				if err == nil || len(recovery.current.WorkshopOutputs) != 3 || len(store.actions) != 2 {
					t.Fatal("partial publication did not retain full pins", err)
				}
				store.lateWrite = true
				store.actions = nil
				err = delivery.PublishHostWorkshop(context.Background(), ref.Scope)
			}
			resultKey := "sessions/session/workshop-sync/operation.json"
			if scenario == "rollback" {
				resultKey = "sessions/session/workshop-sync/operation-rollback-" + ref.Scope.AttemptID + ".json"
			}
			if err != nil || len(store.actions) != 3 || store.actions[0] != missionKey || !strings.Contains(store.actions[1], "workshop-resolutions/"+revision+".tsv") || store.actions[2] != resultKey || inspector.calls != 1 {
				t.Fatal("publication order or pinned replay failed", store.actions, inspector.calls, err)
			}
		})
	}
}

var _ ports.HostWorkshopStore = (*publicationStore)(nil)
