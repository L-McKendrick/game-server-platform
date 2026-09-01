package workshopcontent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type recoveryRunner struct{}

func (recoveryRunner) StartContent(context.Context, domain.Session, domain.WorkshopTarget, bool) (string, error) {
	return "command-1", nil
}
func (recoveryRunner) FindContentCommand(context.Context, string, string, string) (string, error) {
	return "command-1", nil
}
func (recoveryRunner) ResolveContentCommand(context.Context, string) (string, string, string, error) {
	return "session-1", "wsync-recovery", "i-1", nil
}
func (recoveryRunner) CancelContentCommand(context.Context, string, string) error { return nil }
func (recoveryRunner) Observe(context.Context, string, string) (ports.BootstrapCommandStatus, error) {
	return ports.BootstrapCommandStatus{Status: "Success"}, nil
}

type recoveryClock struct{ now time.Time }

func (clock recoveryClock) Now() time.Time { return clock.now }

type recoveryIDs struct{}

func (recoveryIDs) New(time.Time) (string, error) { return "event-recovered", nil }

type recoveryManifest struct{ body []byte }

func (reader recoveryManifest) Get(context.Context, string) ([]byte, error) { return reader.body, nil }

func TestContentWorkflowIDBindsDigestAndRequest(t *testing.T) {
	digestA, digestB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	first := contentWorkflowID("session-1", domain.WorkshopTargetMods, digestA, "request-1")
	if first != contentWorkflowID("session-1", domain.WorkshopTargetMods, digestA, "request-1") {
		t.Fatal("workflow identity is not deterministic")
	}
	if first == contentWorkflowID("session-1", domain.WorkshopTargetMods, digestB, "request-1") || first == contentWorkflowID("session-1", domain.WorkshopTargetMods, digestA, "request-2") {
		t.Fatal("workflow identity is not bound to digest and request")
	}
	if len(first) != 30 || !strings.HasPrefix(first, "wsync-") {
		t.Fatalf("workflow ID = %q", first)
	}
}

func TestContentDigestMatchesExactModRevision(t *testing.T) {
	digest := strings.Repeat("a", 64)
	session := domain.Session{PendingPresetRevision: domain.PresetRevision{WorkshopResolutionSHA256: digest}}
	if !contentDigestMatches(session, domain.WorkshopTargetMods, digest) {
		t.Fatal("exact pending digest did not match")
	}
	if contentDigestMatches(session, domain.WorkshopTargetMods, strings.Repeat("b", 64)) || contentDigestMatches(session, domain.WorkshopTargetMods, "invalid") {
		t.Fatal("stale or malformed digest matched")
	}
}

func TestReconcileActiveRecoversCommandWhenEventBridgeDeliveryWasLost(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	repository := memory.NewSessionRepository()
	session, err := domain.NewSession(domain.NewSessionInput{ID: "session-1", Slug: "session-1", DisplayName: "Session", GameType: "arma3", OwnerDiscordUserID: "owner-1", GuildID: "guild-1", ChannelID: "channel-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	session.LifecycleState = domain.StateRunning
	session.Infrastructure = domain.Infrastructure{CapacitySlotID: "slot-1", AvailabilityZone: "us-west-2a", SubnetID: "subnet-1", SecurityGroupIDs: []string{"sg-1"}, InstanceProfile: "profile-1", AMIID: "ami-1", InstanceType: "c7i.large", InstanceID: "i-1", DataVolumeID: "vol-1", LastObservedAt: now}
	session.WorkshopMissionSources = []domain.WorkshopMissionSource{{Source: domain.WorkshopReference{PublishedFileID: 42, CanonicalURL: "https://steamcommunity.com/sharedfiles/filedetails/?id=42"}, SourceKind: domain.WorkshopSourceItem, ResolutionSHA256: strings.Repeat("a", 64), AcceptedItemIDs: []uint64{42}, ResolvedAt: now}}
	created := domain.NewSessionCreatedEvent("event-created", "correlation-1", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: "owner-1"}, session, now)
	record, err := domain.NewCompletedIdempotencyRecord("seed:session-1", "hash", session.ID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(context.Background(), session, created, record); err != nil {
		t.Fatal(err)
	}
	digest, err := session.WorkshopMissionRevision()
	if err != nil {
		t.Fatal(err)
	}
	expected := session.Version
	if err := session.AcquireWorkflowLock("wsync-recovery", domain.WorkshopContentSyncWorkflowType, commandLease, now); err != nil {
		t.Fatal(err)
	}
	workflow := domain.Workflow{ID: "wsync-recovery", SessionID: session.ID, Type: domain.WorkshopContentSyncWorkflowType, Status: domain.WorkflowRunning, RequestedBy: "owner-1", CorrelationID: "correlation-1", ExpectedVersion: expected, CurrentStage: "Dispatching", ContentTarget: string(domain.WorkshopTargetMission), ContentDigest: digest, InstanceID: "i-1", StartedAt: now, LeaseExpiresAt: now.Add(commandLease)}
	started := domain.SessionEvent{ID: "event-started", SessionID: session.ID, Type: domain.EventWorkflowStarted, OccurredAt: now, ActorType: string(domain.ActorTypeSystem), ActorID: domain.WorkshopContentSyncWorkflowType, CorrelationID: "correlation-1", Data: map[string]string{"workflow_id": workflow.ID}}
	if err := repository.AcquireWorkflow(context.Background(), session, expected, workflow, started); err != nil {
		t.Fatal(err)
	}
	manifestDigest := strings.Repeat("b", 64)
	manifest := []byte(manifestDigest + "\tRecovered.Altis.pbo\tsessions/session-1/input/missions/" + manifestDigest + "-Recovered.Altis.pbo\t42\n")
	service, err := New(repository, repository, recoveryRunner{}, recoveryIDs{}, recoveryClock{now: now.Add(time.Minute)}, WithWorkshopMissionManifest(recoveryManifest{body: manifest}))
	if err != nil {
		t.Fatal(err)
	}
	done, err := service.ReconcileActive(context.Background(), session, workflow)
	if err != nil || !done {
		t.Fatalf("reconcile done = %v, err = %v", done, err)
	}
	completed, err := repository.GetWorkflow(context.Background(), session.ID, workflow.ID)
	if err != nil || completed.Status != domain.WorkflowSucceeded || completed.CommandID != "command-1" {
		t.Fatalf("workflow = %#v, err = %v", completed, err)
	}
	persisted, err := repository.Get(context.Background(), session.ID)
	if err != nil || persisted.ActiveWorkflowID != "" || persisted.Version != session.Version+1 || len(persisted.MissionFiles) != 1 || persisted.MissionFiles[0].WorkshopItemID != 42 {
		t.Fatalf("session lock = %q, err = %v", persisted.ActiveWorkflowID, err)
	}
}
