package archive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type testRunner struct{ status ports.ArchiveCommandStatus }

type testDestroyer struct{}

type testCompute struct {
	started int
	state   string
	managed bool
}

func (compute *testCompute) FindInstance(context.Context, domain.ComputeLaunchRequest) (domain.ComputeObservation, bool, error) {
	return domain.ComputeObservation{}, false, nil
}
func (compute *testCompute) EnsureInstance(context.Context, domain.ComputeLaunchRequest, string) (domain.ComputeObservation, error) {
	return domain.ComputeObservation{}, nil
}
func (compute *testCompute) ObserveInstance(context.Context, string) (domain.ComputeObservation, error) {
	return domain.ComputeObservation{State: compute.state}, nil
}
func (compute *testCompute) IsManaged(context.Context, string) (bool, error) {
	return compute.managed, nil
}
func (compute *testCompute) StopInstance(context.Context, string) error { return nil }
func (compute *testCompute) StartInstance(context.Context, string) error {
	compute.started++
	return nil
}

func (testDestroyer) TerminateInstance(context.Context, string, string) error { return nil }
func (testDestroyer) InstanceTerminated(context.Context, string, string) (bool, error) {
	return true, nil
}
func (testDestroyer) DeleteVolume(context.Context, string, string) error          { return nil }
func (testDestroyer) VolumeDeleted(context.Context, string, string) (bool, error) { return true, nil }

func (runner *testRunner) Start(context.Context, domain.Session, string) (string, error) {
	return "command-1", nil
}
func (runner *testRunner) Observe(context.Context, string, string) (ports.ArchiveCommandStatus, error) {
	return runner.status, nil
}

type testStore struct {
	put      ports.ArchiveObject
	body     []byte
	verified []ports.ArchiveObject
}

func (store *testStore) Put(_ context.Context, object ports.ArchiveObject, body []byte) error {
	store.put, store.body = object, append([]byte(nil), body...)
	return nil
}
func (store *testStore) Verify(_ context.Context, object ports.ArchiveObject) error {
	store.verified = append(store.verified, object)
	return nil
}
func (store *testStore) Get(context.Context, ports.ArchiveObject) ([]byte, error) {
	return append([]byte(nil), store.body...), nil
}

type testClock struct{ now time.Time }

func (clock testClock) Now() time.Time { return clock.now }

type testIDs struct{ value string }

func (ids testIDs) New(time.Time) (string, error) { return ids.value, nil }

func TestService_VerifiesManifestBeforeCompletingDestruction(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	session := archiveServiceSession(t, now)
	createEvent := domain.NewSessionCreatedEvent("event-create", "correlation-create", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now)
	idempotency, err := domain.NewCompletedIdempotencyRecord("create-1", "hash-1", session.ID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(ctx, session, createEvent, idempotency); err != nil {
		t.Fatal(err)
	}

	expectedVersion := session.Version
	if err := session.BeginArchive("archive-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	workflow := domain.Workflow{ID: "archive-1", SessionID: session.ID, Type: domain.ArchiveWorkflowType, Status: domain.WorkflowRunning, RequestedBy: "owner-1", CorrelationID: "correlation-archive", ExpectedVersion: expectedVersion, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}
	startEvent := domain.NewWorkflowEvent("event-start", domain.EventArchiveStarted, workflow.CorrelationID, domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, workflow, now)
	if err := repository.AcquireWorkflow(ctx, session, expectedVersion, workflow, startEvent); err != nil {
		t.Fatal(err)
	}

	objectKey := "sessions/session-1/archives/archive-1/session.tar.gz"
	runner := &testRunner{status: ports.ArchiveCommandStatus{Status: "Success", ObjectKey: objectKey, SHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), SizeBytes: 1234}}
	store := &testStore{}
	service, err := NewService(repository, repository, repository, runner, store, testDestroyer{}, nil, testIDs{value: "event-complete"}, testClock{now: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}

	dispatched, err := service.Handle(ctx, TaskRequest{Action: ActionDispatch, SessionID: session.ID, WorkflowID: workflow.ID})
	if err != nil || dispatched.CommandID != "command-1" {
		t.Fatalf("dispatch = %#v, %v", dispatched, err)
	}
	observed, err := service.Handle(ctx, TaskRequest{Action: ActionObserve, SessionID: session.ID, WorkflowID: workflow.ID, CommandID: dispatched.CommandID})
	if err != nil || !observed.Succeeded {
		t.Fatalf("observe = %#v, %v", observed, err)
	}
	verified, err := service.Handle(ctx, TaskRequest{Action: ActionVerify, SessionID: session.ID, WorkflowID: workflow.ID, ObjectKey: observed.ObjectKey, SHA256: observed.SHA256, SizeBytes: observed.SizeBytes})
	if err != nil || verified.ManifestObjectKey == "" {
		t.Fatalf("verify = %#v, %v", verified, err)
	}
	if len(store.verified) != 2 || store.put.Key != verified.ManifestObjectKey {
		t.Fatalf("store calls = %#v, put %#v", store.verified, store.put)
	}
	var manifest domain.ArchiveManifest
	if err := json.Unmarshal(store.body, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || manifest.SourceInstanceID != "i-1" || manifest.SourceDataVolumeID != "vol-1" || manifest.Vanilla || manifest.SessionName != session.DisplayName || manifest.SessionSlug != session.Slug || manifest.Description != session.Description ||
		manifest.PresetRevisionSequence != 2 || manifest.ActivePresetRevision == nil || manifest.ActivePresetRevision.Number != 1 || manifest.PendingPresetRevision == nil || manifest.PendingPresetRevision.Number != 2 || manifest.PendingPresetRevision.Status != domain.PresetRevisionPending ||
		!reflect.DeepEqual(manifest.MissionFiles, session.MissionFiles) || manifest.ConfiguredMission != session.ConfiguredMission || manifest.CurrentMission != session.CurrentMission {
		t.Fatalf("manifest = %#v", manifest)
	}

	_, err = service.Handle(ctx, TaskRequest{Action: ActionRecordVerified, SessionID: session.ID, WorkflowID: workflow.ID, ObjectKey: verified.ObjectKey, SHA256: verified.SHA256, SizeBytes: verified.SizeBytes, ManifestObjectKey: verified.ManifestObjectKey, ManifestSHA256: verified.ManifestSHA256, ManifestSizeBytes: verified.ManifestSizeBytes})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{ActionTerminate, ActionObserveTermination, ActionDeleteVolume, ActionObserveVolume} {
		if _, err := service.Handle(ctx, TaskRequest{Action: action, SessionID: session.ID, WorkflowID: workflow.ID}); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	completed, err := service.Handle(ctx, TaskRequest{Action: ActionComplete, SessionID: session.ID, WorkflowID: workflow.ID})
	if err != nil || !completed.Succeeded {
		t.Fatalf("complete = %#v, %v", completed, err)
	}
	stored, err := repository.Get(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LifecycleState != domain.StateArchived || stored.Archive.ID != workflow.ID || !stored.Infrastructure.Empty() {
		t.Fatalf("stored session = %#v", stored)
	}
}

func TestService_PreparesSleepingHostBeforeArchiveDispatch(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	session := archiveServiceSession(t, now.Add(-100*time.Hour))
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = domain.StateSleeping, domain.StateSleeping, domain.StateSleeping, domain.HealthStopped
	session.SleepingSince = now.Add(-72 * time.Hour)
	createEvent := domain.NewSessionCreatedEvent("sleeping-create", "sleeping-create", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now.Add(-100*time.Hour))
	idempotency, _ := domain.NewCompletedIdempotencyRecord("sleeping-create", "sleeping-hash", session.ID, now.Add(-100*time.Hour), time.Hour)
	if err := repository.Create(ctx, session, createEvent, idempotency); err != nil {
		t.Fatal(err)
	}
	expected := session.Version
	if err := session.BeginArchive("archive-sleeping", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	workflow := domain.Workflow{ID: "archive-sleeping", SessionID: session.ID, Type: domain.ArchiveWorkflowType, Status: domain.WorkflowRunning, RequestedBy: domain.InactivityMonitorActorID, CorrelationID: "archive-sleeping", ExpectedVersion: expected, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}
	startEvent := domain.NewWorkflowEvent("archive-sleeping-start", domain.EventArchiveStarted, workflow.CorrelationID, domain.Actor{Type: domain.ActorTypeSystem, ID: domain.InactivityMonitorActorID}, session, workflow, now)
	if err := repository.AcquireWorkflow(ctx, session, expected, workflow, startEvent); err != nil {
		t.Fatal(err)
	}
	compute := &testCompute{state: "running", managed: true}
	service, err := NewService(repository, repository, repository, &testRunner{}, &testStore{}, testDestroyer{}, nil, testIDs{value: "unused"}, testClock{now}, WithComputeProvisioner(compute))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := service.Handle(ctx, TaskRequest{Action: ActionPrepareHost, SessionID: session.ID, WorkflowID: workflow.ID})
	if err != nil || prepared.Ready || compute.started != 1 {
		t.Fatalf("prepare=%#v started=%d err=%v", prepared, compute.started, err)
	}
	observed, err := service.Handle(ctx, TaskRequest{Action: ActionObserveHost, SessionID: session.ID, WorkflowID: workflow.ID})
	if err != nil || !observed.Ready {
		t.Fatalf("observe=%#v err=%v", observed, err)
	}
}

func archiveServiceSession(t *testing.T, now time.Time) domain.Session {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "saturday-arma", DisplayName: "Saturday Arma", Description: "Weekly co-op night", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = domain.StateRunning, domain.StateRunning, domain.StateRunning, domain.HealthHealthy
	session.PresetObjectKey = "sessions/session-1/input/presets/v1.html"
	session.PresetRevisionSequence = 2
	session.ActivePresetRevision = domain.PresetRevision{
		Number: 1, PresetObjectKey: session.PresetObjectKey, Status: domain.PresetRevisionActive, StagedAt: now, ActivatedAt: now,
		Modlist: domain.PresetModlistMetadata{ObjectKey: "sessions/session-1/input/modlists/v1/session-1-modlist.html", Filename: "session-1-modlist.html", SHA256: strings.Repeat("a", 64), SizeBytes: 512, WorkshopCount: 2},
	}
	session.PendingPresetRevision = domain.PresetRevision{
		Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/presets/v2.html", Status: domain.PresetRevisionPending, StagedAt: now.Add(time.Minute),
		Modlist: domain.PresetModlistMetadata{ObjectKey: "sessions/session-1/input/modlists/v2/session-1-modlist.html", Filename: "session-1-modlist.html", SHA256: strings.Repeat("b", 64), SizeBytes: 640, WorkshopCount: 3},
	}
	session.MissionObjectKey = "sessions/session-1/input/missions/Coop.Altis.pbo"
	session.MissionFiles = []domain.MissionRecord{{ObjectKey: session.MissionObjectKey, Filename: "Coop.Altis.pbo", Status: domain.ArtifactAccepted, AddedAt: now}}
	session.ConfiguredMission = domain.UploadedMissionSelection(session.MissionObjectKey)
	session.CurrentMission = session.ConfiguredMission
	session.Infrastructure = domain.Infrastructure{CapacitySlotID: "slot-1", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile", AMIID: "ami-1", InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.1", LastObservedAt: now}
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
	return session
}
