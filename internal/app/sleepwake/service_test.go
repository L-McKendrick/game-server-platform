package sleepwake

import (
	"context"
	"errors"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type managedCompute struct {
	managed bool
	err     error
}

func (compute managedCompute) FindInstance(context.Context, domain.ComputeLaunchRequest) (domain.ComputeObservation, bool, error) {
	return domain.ComputeObservation{}, false, nil
}

func (compute managedCompute) EnsureInstance(context.Context, domain.ComputeLaunchRequest, string) (domain.ComputeObservation, error) {
	return domain.ComputeObservation{}, nil
}

func (compute managedCompute) ObserveInstance(context.Context, string) (domain.ComputeObservation, error) {
	return domain.ComputeObservation{}, nil
}

func (compute managedCompute) IsManaged(context.Context, string) (bool, error) {
	return compute.managed, compute.err
}

func (managedCompute) StopInstance(context.Context, string) error  { return nil }
func (managedCompute) StartInstance(context.Context, string) error { return nil }

func TestServiceCheckManaged(t *testing.T) {
	tests := []struct {
		name    string
		compute managedCompute
		want    bool
		wantErr bool
	}{
		{name: "offline node remains pending", compute: managedCompute{}, want: false},
		{name: "online node advances", compute: managedCompute{managed: true}, want: true},
		{name: "adapter failure is returned", compute: managedCompute{err: errors.New("describe failed")}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &Service{compute: test.compute}
			result, err := service.checkManaged(context.Background(), domain.Session{ID: "session-1", Infrastructure: domain.Infrastructure{InstanceID: "i-test"}}, domain.Workflow{ID: "wake-1"})
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("check managed: %v", err)
			}
			if result.Managed != test.want {
				t.Fatalf("managed = %v, want %v", result.Managed, test.want)
			}
			if result.SessionID != "session-1" || result.WorkflowID != "wake-1" {
				t.Fatalf("unexpected task identity: %+v", result)
			}
		})
	}
}
