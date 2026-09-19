package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type accessRecords struct {
	session  domain.Session
	workflow domain.Workflow
}

func (records *accessRecords) Get(context.Context, string) (domain.Session, error) {
	return records.session, nil
}
func (records *accessRecords) GetWorkflow(context.Context, string, string) (domain.Workflow, error) {
	return records.workflow, nil
}

type instanceAuthority bool

func (allowed instanceAuthority) VerifyHostInstance(context.Context, string, string) error {
	if !allowed {
		return fmt.Errorf("tag mismatch")
	}
	return nil
}

func TestHostAuthorityRejectsStaleIdentityAndContent(t *testing.T) {
	now := time.Now().UTC()
	for _, scenario := range []struct {
		name   string
		mutate func(*accessRecords, *domain.HostAccessScope)
		denied bool
	}{
		{"current", func(*accessRecords, *domain.HostAccessScope) {}, false},
		{"progress-version", func(r *accessRecords, _ *domain.HostAccessScope) { r.session.Version++ }, false},
		{"guild", func(_ *accessRecords, s *domain.HostAccessScope) { s.GuildID = "other" }, true},
		{"instance", func(r *accessRecords, _ *domain.HostAccessScope) { r.session.Infrastructure.InstanceID = "i-other" }, true},
		{"lock", func(r *accessRecords, _ *domain.HostAccessScope) { r.session.ActiveWorkflowID = "other" }, true},
		{"cancel", func(r *accessRecords, _ *domain.HostAccessScope) { r.workflow.CancelRequestedAt = now }, true},
		{"terminal", func(r *accessRecords, _ *domain.HostAccessScope) { r.workflow.Status = domain.WorkflowSucceeded }, true},
		{"lease", func(r *accessRecords, _ *domain.HostAccessScope) { r.workflow.LeaseExpiresAt = now }, true},
		{"deadline-extension", func(_ *accessRecords, s *domain.HostAccessScope) { s.DeadlineAt = now.Add(2 * time.Hour) }, true},
		{"configuration", func(r *accessRecords, _ *domain.HostAccessScope) { r.session.ConfigurationRevision++ }, true},
		{"expired", func(_ *accessRecords, s *domain.HostAccessScope) { s.DeadlineAt = now }, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			r := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", ActiveWorkflowID: "operation", ActiveWorkflowType: "bootstrap", ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour)}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: "bootstrap", Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
			r.session.Infrastructure.InstanceID = "i-123"
			s := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: HostContentSnapshot(r.session), DeadlineAt: now.Add(30 * time.Minute)}
			scenario.mutate(r, &s)
			_, _, err := (HostAccessAuthority{Records: r, Instances: instanceAuthority(true)}).ValidateWorkflow(context.Background(), s, now)
			if (err != nil) != scenario.denied {
				t.Fatalf("denied=%v, error=%v", scenario.denied, err)
			}
			if !scenario.denied {
				if _, _, err := (HostAccessAuthority{Records: r, Instances: instanceAuthority(false)}).ValidateWorkflow(context.Background(), s, now); err == nil {
					t.Fatal("accepted mismatched immutable tags")
				}
			}
		})
	}
}
