package access

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/memory"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type accessClock struct{ now time.Time }

func (clock accessClock) Now() time.Time { return clock.now }

func TestGuildManagerConfiguresDurableGuildPolicy(t *testing.T) {
	t.Parallel()

	repository := memory.NewAccessPolicyRepository()
	service, err := NewService(repository, nil, nil, accessClock{time.Date(2026, 8, 8, 23, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("NewService() returned error: %v", err)
	}
	policy, err := service.Configure(context.Background(), "guild-1", "admin-1", true, []string{"role-2", "role-1", "role-2"}, nil)
	if err != nil {
		t.Fatalf("Configure() returned error: %v", err)
	}
	if policy.Version != 1 || len(policy.AllowedRoleIDs) != 2 || policy.AllowedRoleIDs[0] != "role-1" {
		t.Fatalf("policy = %#v", policy)
	}
	replayed, err := service.Configure(context.Background(), "guild-1", "admin-2", true, []string{"role-1", "role-2"}, nil)
	if err != nil {
		t.Fatalf("Configure(replay) returned error: %v", err)
	}
	if replayed.Version != policy.Version || replayed.UpdatedBy != policy.UpdatedBy {
		t.Fatalf("replayed policy = %#v; want unchanged %#v", replayed, policy)
	}
	if err := service.Authorize(context.Background(), "guild-1", "channel-1", "user-1", []string{"role-2"}); err != nil {
		t.Fatalf("Authorize() returned error: %v", err)
	}
	if err := service.Authorize(context.Background(), "guild-1", "channel-other", "user-1", []string{"role-2"}); err != nil {
		t.Fatalf("Authorize(other channel) returned error: %v", err)
	}
	if err := service.Authorize(context.Background(), "guild-1", "channel-1", "user-1", []string{"role-other"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Authorize(other role) error = %v; want ErrForbidden", err)
	}
}

func TestStaticFallbackAuthorizesUntilGuildPolicyExists(t *testing.T) {
	t.Parallel()

	service, err := NewService(memory.NewAccessPolicyRepository(), []string{"role-1"}, []string{"channel-1"}, accessClock{time.Now()})
	if err != nil {
		t.Fatalf("NewService() returned error: %v", err)
	}
	if err := service.Authorize(context.Background(), "guild-1", "channel-1", "user-1", []string{"role-1"}); err != nil {
		t.Fatalf("Authorize() returned error: %v", err)
	}
	if _, err := service.Configure(context.Background(), "guild-1", "user-1", false, []string{"role-2"}, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Configure(non-admin) error = %v; want ErrForbidden", err)
	}
}

func TestGuildManagerCanRemoveAllNormalAccessRoles(t *testing.T) {
	t.Parallel()

	repository := memory.NewAccessPolicyRepository()
	service, err := NewService(repository, []string{"fallback-role"}, nil, accessClock{time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	roles, version, err := service.AllowedRoles(context.Background(), "guild-1")
	if err != nil || len(roles) != 1 || roles[0] != "fallback-role" {
		t.Fatalf("fallback roles = %#v, version = %d, error = %v", roles, version, err)
	}
	policy, err := service.ClearRoles(context.Background(), "guild-1", "admin-1", true, version)
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.AllowedRoleIDs) != 0 {
		t.Fatalf("policy roles = %#v", policy.AllowedRoleIDs)
	}
	roles, version, err = service.AllowedRoles(context.Background(), "guild-1")
	if err != nil || len(roles) != 0 {
		t.Fatalf("persisted roles = %#v, version = %d, error = %v", roles, version, err)
	}
	if err := service.Authorize(context.Background(), "guild-1", "channel-1", "member-1", []string{"fallback-role"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Authorize() error = %v; want forbidden after explicit clear", err)
	}
}

func TestClearRolesRejectsStalePolicyRevision(t *testing.T) {
	t.Parallel()
	repository := memory.NewAccessPolicyRepository()
	service, err := NewService(repository, []string{"fallback-role"}, nil, accessClock{time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Configure(context.Background(), "guild-1", "manager-1", true, []string{"role-1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Configure(context.Background(), "guild-1", "manager-2", true, []string{"role-2"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ClearRoles(context.Background(), "guild-1", "manager-1", true, first.Version); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("ClearRoles() error = %v; want ErrConflict", err)
	}
	roles, _, err := service.AllowedRoles(context.Background(), "guild-1")
	if err != nil || len(roles) != 1 || roles[0] != "role-2" {
		t.Fatalf("roles after stale clear = %#v, error = %v", roles, err)
	}
}
