package archive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type testRunner struct{ status ports.ArchiveCommandStatus }

type testDestroyer struct{}

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
	if manifest.SchemaVersion != 1 || manifest.SourceInstanceID != "i-1" || manifest.SourceDataVolumeID != "vol-1" || !manifest.Vanilla {
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

func archiveServiceSession(t *testing.T, now time.Time) domain.Session {
	t.Helper()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = domain.StateRunning, domain.StateRunning, domain.StateRunning, domain.HealthHealthy
	session.Vanilla = true
	session.Infrastructure = domain.Infrastructure{CapacitySlotID: "slot-1", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile", AMIID: "ami-1", InstanceType: "c7i-flex.large", InstanceID: "i-1", DataVolumeID: "vol-1", PublicIPv4: "203.0.113.1", LastObservedAt: now}
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
	return session
}
