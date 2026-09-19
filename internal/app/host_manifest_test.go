package app

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type manifestRecovery struct {
	current  domain.HostAccessAttempt
	versions map[string][]byte
	writes   int
	conflict bool
}

func (recovery *manifestRecovery) GetHostAccessAttempt(context.Context, string, string, string) (domain.HostAccessAttempt, error) {
	if recovery.current.Revision == 0 {
		return domain.HostAccessAttempt{}, domain.ErrNotFound
	}
	return recovery.current, nil
}
func (recovery *manifestRecovery) SaveHostAccessAttempt(_ context.Context, next domain.HostAccessAttempt, revision, version int64, _ time.Time) error {
	if recovery.conflict || version != 7 {
		return domain.ErrConflict
	}
	if err := domain.ValidateHostAccessUpdate(recovery.current, next, revision); err != nil {
		return err
	}
	recovery.current = next
	return nil
}
func (recovery *manifestRecovery) PutHostManifest(_ context.Context, _ domain.HostAccessScope, payload []byte) (string, error) {
	recovery.writes++
	version := fmt.Sprintf("version-%d", recovery.writes)
	if recovery.versions == nil {
		recovery.versions = make(map[string][]byte)
	}
	recovery.versions[version] = append([]byte(nil), payload...)
	return version, nil
}
func (recovery *manifestRecovery) GetHostManifest(_ context.Context, _ domain.HostAccessScope, version string) ([]byte, error) {
	return recovery.versions[version], nil
}

type manifestSigner struct{ expiry time.Time }

type countedInstanceAuthority struct{ calls int }

func (authority *countedInstanceAuthority) VerifyHostInstance(context.Context, string, string) error {
	authority.calls++
	return nil
}

type mutatingManifestSigner struct {
	manifestSigner
	mutate func()
}

func (signer mutatingManifestSigner) Sign(ctx context.Context, scope domain.HostAccessScope, object domain.HostObjectRequest) (ports.HostObjectCapability, error) {
	if signer.mutate != nil {
		signer.mutate()
	}
	return signer.manifestSigner.Sign(ctx, scope, object)
}

func TestManifestBatchBoundsInstanceChecksAndRejectsChangedAuthority(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			now := time.Now().UTC()
			records := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", Version: 7, ActiveWorkflowID: "operation", ActiveWorkflowType: domain.BootstrapWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour)}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.BootstrapWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
			records.session.Infrastructure.InstanceID = "i-123"
			var objects []domain.HostObjectRequest
			for index := 0; index < 1000; index++ {
				filename := fmt.Sprintf("mission-%04d.pbo", index)
				key := "sessions/session/input/missions/" + strings.Repeat("a", 64) + "-" + filename
				records.session.MissionFiles = append(records.session.MissionFiles, domain.MissionRecord{ObjectKey: key, Filename: filename, Status: domain.ArtifactAccepted})
				objects = append(objects, domain.HostObjectRequest{Purpose: domain.HostObjectMission, Slot: fmt.Sprintf("mission-%04d", index), Key: key, SHA256: hostBase64Digest(strings.Repeat("a", 64)), MaxBytes: 100})
			}
			scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: HostContentSnapshot(records.session), DeadlineAt: now.Add(30 * time.Minute)}
			authority := &countedInstanceAuthority{}
			signer := mutatingManifestSigner{manifestSigner: manifestSigner{expiry: now.Add(10 * time.Minute)}}
			if changed {
				signer.mutate = func() { records.workflow.CancelRequestedAt = time.Now() }
			}
			recovery := &manifestRecovery{}
			issuer := HostManifestIssuer{Inputs: HostObjectIssuer{Authority: HostAccessAuthority{Records: records, Instances: authority}, Signer: signer}, Attempts: recovery, Exchange: recovery}
			reference, err := issuer.Issue(context.Background(), scope, objects)
			if changed {
				if err == nil || reference.URL != "" || recovery.current.Revision != 0 {
					t.Fatal("changed authority published bearer reference")
				}
			} else if err != nil || reference.URL == "" {
				t.Fatal("large batch failed", err)
			}
			if authority.calls > 6 {
				t.Fatal("instance checks grew with inventory", authority.calls)
			}
		})
	}
}

func (signer manifestSigner) Sign(_ context.Context, _ domain.HostAccessScope, object domain.HostObjectRequest) (ports.HostObjectCapability, error) {
	if object.Purpose.Action() == domain.HostObjectStage {
		return ports.HostObjectCapability{Object: object, Method: "POST", URL: "https://assets.example.test/", ExpiresAt: signer.expiry, Form: map[string]string{"key": object.Key, "Content-Type": object.ContentType, "policy": "bounded-policy", "X-Amz-Signature": "signature"}}, nil
	}
	return ports.HostObjectCapability{Object: object, Method: "GET", URL: "https://assets.example.test/object?signature=bearer", ExpiresAt: signer.expiry}, nil
}

func TestManifestIssueRecoveryAndRefresh(t *testing.T) {
	now := time.Now().UTC()
	key := "sessions/session/input/missions/" + strings.Repeat("a", 64) + "-mission.pbo"
	records := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", Version: 7, ActiveWorkflowID: "operation", ActiveWorkflowType: domain.BootstrapWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour), MissionFiles: []domain.MissionRecord{{ObjectKey: key, Filename: "mission.pbo", Status: domain.ArtifactAccepted}}}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.BootstrapWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
	records.session.Infrastructure.InstanceID = "i-123"
	scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: HostContentSnapshot(records.session), DeadlineAt: now.Add(30 * time.Minute)}
	objects := []domain.HostObjectRequest{{Purpose: domain.HostObjectMission, Slot: "mission", Key: key, SHA256: hostBase64Digest(strings.Repeat("a", 64)), MaxBytes: 100}}
	recovery := &manifestRecovery{}
	issuer := HostManifestIssuer{Inputs: HostObjectIssuer{Authority: HostAccessAuthority{Records: records, Instances: instanceAuthority(true)}, Signer: manifestSigner{expiry: now.Add(10 * time.Minute)}}, Attempts: recovery, Exchange: recovery}
	environment := map[string]string{"MISSION_MANIFEST_B64": "accepted mission inventory"}
	first, err := issuer.IssueWithEnvironment(context.Background(), scope, objects, environment)
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation != 1 || recovery.writes != 1 {
		t.Fatal("first issuance not persisted")
	}
	recoveredEnvironment, err := issuer.RecoverEnvironment(context.Background(), scope)
	if err != nil || !reflect.DeepEqual(recoveredEnvironment, environment) || recovery.writes != 1 {
		t.Fatal("command context recovery changed publication", err)
	}
	pinned := recovery.versions[recovery.current.ManifestVersionID]
	recovery.versions[recovery.current.ManifestVersionID] = []byte("tampered")
	if values, err := issuer.RecoverEnvironment(context.Background(), scope); err == nil || values != nil {
		t.Fatal("corrupted context disclosed")
	}
	recovery.versions[recovery.current.ManifestVersionID] = pinned
	records.session.ConfigurationRevision++
	if values, err := issuer.RecoverEnvironment(context.Background(), scope); err == nil || values != nil {
		t.Fatal("stale content recovered context")
	}
	records.session.ConfigurationRevision--
	recovery.current.DispatchState = domain.HostAccessFinished
	if values, err := issuer.RecoverEnvironment(context.Background(), scope); err == nil || values != nil {
		t.Fatal("finished command recovered context")
	}
	recovery.current.DispatchState = domain.HostAccessPrepared
	if _, err := issuer.IssueWithEnvironment(context.Background(), scope, objects, map[string]string{"MISSION_MANIFEST_B64": "changed"}); err == nil {
		t.Fatal("replay changed command context")
	}
	if err := issuer.RecordDispatch(context.Background(), scope, first.Generation, domain.HostAccessAmbiguous); err != nil {
		t.Fatal(err)
	}
	if err := issuer.RecordDispatch(context.Background(), scope, first.Generation, domain.HostAccessAmbiguous); err != nil {
		t.Fatal("duplicate dispatch bookkeeping failed", err)
	}
	replayed, err := issuer.IssueWithEnvironment(context.Background(), scope, objects, environment)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, replayed) || recovery.writes != 1 {
		t.Fatal("ambiguous dispatch created a new generation")
	}
	changed := append([]domain.HostObjectRequest(nil), objects...)
	changed[0].Slot = "other"
	if _, err := issuer.IssueWithEnvironment(context.Background(), scope, changed, environment); err == nil {
		t.Fatal("replay changed command inventory")
	}
	recovery.current.ExpiresAt = now.Add(time.Minute)
	refreshed, err := issuer.IssueWithEnvironment(context.Background(), scope, objects, environment)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Generation != 2 || recovery.current.Revision != 3 || recovery.writes != 2 {
		t.Fatal("refresh did not conditionally advance generation")
	}
	if err := issuer.RecordDispatch(context.Background(), scope, first.Generation, domain.HostAccessDispatched); err == nil {
		t.Fatal("stale dispatch changed refreshed generation")
	}
	recovery.current.ExpiresAt = now.Add(time.Minute)
	recovery.conflict = true
	failed, err := issuer.IssueWithEnvironment(context.Background(), scope, objects, environment)
	if err == nil || failed.URL != "" {
		t.Fatal("CAS conflict released a capability reference")
	}
	recovery.conflict = false
	if err := issuer.RecordDispatch(context.Background(), scope, refreshed.Generation, domain.HostAccessFinished); err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.IssueWithEnvironment(context.Background(), scope, objects, environment); err == nil {
		t.Fatal("finished attempt released a capability")
	}
	recovery.current.DispatchState = domain.HostAccessPrepared
	recovery.versions[recovery.current.ManifestVersionID] = []byte("tampered")
	if _, err := issuer.IssueWithEnvironment(context.Background(), scope, objects, environment); err == nil {
		t.Fatal("accepted corrupt recovered manifest")
	}
	issuer.Inputs.Signer = manifestSigner{expiry: now.Add(-time.Minute)}
	recovery.current = domain.HostAccessAttempt{}
	if reference, err := issuer.IssueWithEnvironment(context.Background(), scope, objects, environment); err == nil || reference.URL != "" {
		t.Fatal("expired signer released reference")
	}
}
