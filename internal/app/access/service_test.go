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
