package restore

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

type fakeCompute struct{ observation domain.ComputeObservation }

func (fake fakeCompute) FindInstance(context.Context, domain.ComputeLaunchRequest) (domain.ComputeObservation, bool, error) {
	return fake.observation, true, nil
}
func (fake fakeCompute) EnsureInstance(context.Context, domain.ComputeLaunchRequest, string) (domain.ComputeObservation, error) {
	return fake.observation, nil
}
func (fake fakeCompute) ObserveInstance(context.Context, string) (domain.ComputeObservation, error) {
	return fake.observation, nil
}
func (fake fakeCompute) IsManaged(context.Context, string) (bool, error) { return true, nil }
func (fake fakeCompute) StopInstance(context.Context, string) error      { return nil }
func (fake fakeCompute) StartInstance(context.Context, string) error     { return nil }

type fakeRunner struct{}

func (fakeRunner) Start(context.Context, domain.Session) (string, error) { return "command-1", nil }
func (fakeRunner) Observe(context.Context, string, string) (ports.BootstrapCommandStatus, error) {
	return ports.BootstrapCommandStatus{Status: "Success"}, nil
}

type fakeStore struct{ body []byte }

func (fake fakeStore) Put(context.Context, ports.ArchiveObject, []byte) error { return nil }
func (fake fakeStore) Verify(context.Context, ports.ArchiveObject) error      { return nil }
func (fake fakeStore) Get(context.Context, ports.ArchiveObject) ([]byte, error) {
	return append([]byte(nil), fake.body...), nil
}

type fakeClock struct{ now time.Time }

func (clock fakeClock) Now() time.Time { return clock.now }

type sequenceIDs struct{ next int }

func (ids *sequenceIDs) New(time.Time) (string, error) {
	ids.next++
	return "event-" + time.Duration(ids.next).String(), nil
}

func TestLaunchRequestIncludesReadableSessionIdentity(t *testing.T) {
	t.Parallel()
	service := Service{config: Config{Project: "game-server-platform", Environment: "dev", GameSecurityGroupID: "sg-game"}}
	request := service.launchRequest(
		domain.Session{ID: "session-1", DisplayName: "Saturday Arma", Slug: "saturday-arma", GameType: "arma3"},
		domain.Workflow{ID: "restore-1"},
	)
	if request.SessionID != "session-1" || request.SessionName != "Saturday Arma" || request.SessionSlug != "saturday-arma" {
		t.Fatalf("restore launch identity = %#v", request)
	}
}

func TestService_RecreatesBootstrapsAndRestoresVerifiedArchive(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	manifest := domain.ArchiveManifest{SchemaVersion: 1, ArchiveID: "archive-1", SessionID: "session-1", SessionName: "Saturday Arma", SessionSlug: "saturday-arma", Description: "Weekly co-op night", CreatedAt: now.Format(time.RFC3339Nano), Format: "tar+gzip", ObjectKey: "sessions/session-1/archives/archive-1/session.tar.gz", SHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), SizeBytes: 42, ContentRoots: []string{"/srv/game-server/config"}, GameProfileID: "arma3-default", MissionObjectKey: "sessions/session-1/input/mission.pbo", Vanilla: true, SourceInstanceID: "i-old", SourceDataVolumeID: "vol-old"}
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "saturday-arma", DisplayName: "Saturday Arma", Description: "Weekly co-op night", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.MissionObjectKey, session.PresetObjectKey = manifest.MissionObjectKey, manifest.PresetObjectKey
	session.Vanilla = true
	session.Archive = domain.ArchiveMetadata{ID: manifest.ArchiveID, ObjectKey: manifest.ObjectKey, ManifestObjectKey: "sessions/session-1/archives/archive-1/manifest.v1.json", ManifestSHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), ManifestSizeBytes: int64(len(body)), SHA256: manifest.SHA256, SizeBytes: manifest.SizeBytes, Format: manifest.Format, VerifiedAt: now}
	session.DesiredState, session.ObservedState, session.LifecycleState, session.HealthStatus = domain.StateArchived, domain.StateArchived, domain.StateArchived, domain.HealthStopped
	repository := memory.NewSessionRepository()
	idempotency, _ := domain.NewCompletedIdempotencyRecord("create", "hash", session.ID, now, time.Hour)
	if err := repository.Create(ctx, session, domain.NewSessionCreatedEvent("created", "correlation", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now), idempotency); err != nil {
		t.Fatal(err)
	}
	expected := session.Version
	if err := session.BeginRestore("restore-1", time.Hour, now); err != nil {
		t.Fatal(err)
	}
	workflow := domain.Workflow{ID: "restore-1", SessionID: session.ID, Type: domain.RestoreWorkflowType, Status: domain.WorkflowRunning, RequestedBy: "owner-1", CorrelationID: "restore-correlation", ExpectedVersion: expected, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}
	if err := repository.AcquireWorkflow(ctx, session, expected, workflow, domain.NewWorkflowEvent("started", domain.EventRestoreStarted, workflow.CorrelationID, domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, workflow, now)); err != nil {
		t.Fatal(err)
	}
	observation := domain.ComputeObservation{InstanceID: "i-new", DataVolumeID: "vol-new", AvailabilityZone: "us-west-2a", PublicIPv4: "203.0.113.2", State: "running"}
	ids := &sequenceIDs{}
	service, err := NewService(repository, repository, repository, fakeCompute{observation}, fakeRunner{}, fakeRunner{}, fakeStore{body}, nil, ids, fakeClock{now: now.Add(time.Minute)}, Config{Project: "game-server-platform", Environment: "dev", AMIID: "ami-1", InstanceType: "c7i.large", SubnetID: "subnet-1", GameSecurityGroupID: "sg-1", InstanceProfile: "profile-1", RootVolumeGiB: 30, DataVolumeGiB: 100, MaxProvisioned: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{ActionVerifyArchive, ActionPrepare, ActionEnsure, ActionObserveInstance, ActionCheckManaged, ActionDispatchBootstrap, ActionObserveBootstrap, ActionDispatchRestore, ActionObserveRestore, ActionComplete} {
		request := TaskRequest{Action: action, SessionID: session.ID, WorkflowID: workflow.ID, CommandID: "command-1"}
		if _, err := service.Handle(ctx, request); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	stored, err := repository.Get(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LifecycleState != domain.StateRunning || stored.HealthStatus != domain.HealthHealthy || stored.Infrastructure.InstanceID != "i-new" || stored.Infrastructure.DataVolumeID != "vol-new" || stored.Archive.ID != "archive-1" {
		t.Fatalf("restored session = %#v", stored)
	}
	if stored.DisplayName != manifest.SessionName || stored.Slug != manifest.SessionSlug || stored.Description != manifest.Description {
		t.Fatalf("restored readable identity = %#v", stored)
	}
}

func TestManifestReadableIdentityMatchingSupportsLegacyAndRejectsDrift(t *testing.T) {
	t.Parallel()
	session := domain.Session{DisplayName: "Saturday Arma", Slug: "saturday-arma", Description: "Weekly co-op night"}
	if !manifestReadableIdentityMatches(domain.ArchiveManifest{}, session) {
		t.Fatal("legacy manifest without readable identity was rejected")
	}
	manifest := domain.ArchiveManifest{SessionName: session.DisplayName, SessionSlug: session.Slug, Description: session.Description}
	if !manifestReadableIdentityMatches(manifest, session) {
		t.Fatal("matching readable identity was rejected")
	}
	manifest.Description = "Different description"
	if manifestReadableIdentityMatches(manifest, session) {
		t.Fatal("drifted readable identity was accepted")
	}
}
