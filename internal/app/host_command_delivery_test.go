package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestHostCommandDeliveryRecoversBeforePreparingContext(t *testing.T) {
	now := time.Now().UTC()
	r := &accessRecords{session: domain.Session{ID: "session", GuildID: "guild", Version: 7, ActiveWorkflowID: "operation", ActiveWorkflowType: domain.BootstrapWorkflowType, ActiveWorkflowStartedAt: now, ActiveWorkflowLeaseExpiresAt: now.Add(time.Hour)}, workflow: domain.Workflow{ID: "operation", SessionID: "session", Type: domain.BootstrapWorkflowType, Status: domain.WorkflowRunning, StartedAt: now, LeaseExpiresAt: now.Add(time.Hour)}}
	r.session.Infrastructure.InstanceID = "i-123"
	recovery := &manifestRecovery{}
	delivery := HostCommandDelivery{Issuer: HostManifestIssuer{Inputs: HostObjectIssuer{Authority: HostAccessAuthority{Records: r, Instances: instanceAuthority(true)}, Signer: manifestSigner{expiry: now.Add(10 * time.Minute)}, Script: domain.HostObjectRequest{Purpose: domain.HostObjectBootstrapScript, Slot: "bootstrap-script", Key: "platform/bootstrap/script.sh", SHA256: hostBase64Digest(strings.Repeat("a", 64)), MaxBytes: 1024}}, Attempts: recovery, Exchange: recovery}}
	prepared := 0
	build := func(_ context.Context, prior map[string]string) (map[string]string, error) {
		if prior != nil {
			return prior, nil
		}
		prepared++
		return map[string]string{"STEAM_EXCHANGE_REFERENCE_B64": "exchange-1"}, nil
	}
	first, err := delivery.PrepareHostCommand(context.Background(), "session", "bootstrap", false, 30*time.Minute, build)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := delivery.PrepareHostCommand(context.Background(), "session", "bootstrap", false, 40*time.Minute, build)
	if err != nil || !reflect.DeepEqual(first, replayed) || prepared != 1 || recovery.writes != 1 {
		t.Fatal("retry changed preparation/publication", err)
	}
	if reference, err := delivery.RefreshHostCommand(context.Background(), first.Scope); err != nil || reference.URL != "" {
		t.Fatal("fresh capability unnecessarily renewed", err)
	}
	// The current payload remains version-pinned while issuance gets a longer
	// credential window. Only its transport generation may change.
	recovery.current.ExpiresAt = now.Add(4 * time.Minute)
	delivery.Issuer.Inputs.Signer = manifestSigner{expiry: now.Add(12 * time.Minute)}
	refreshed, err := delivery.RefreshHostCommand(context.Background(), first.Scope)
	if err != nil || refreshed.Generation != 2 || !refreshed.Scope.Equal(first.Scope) || prepared != 1 || recovery.writes != 2 {
		t.Fatal("renewal changed identity/context", err)
	}
	retry, err := delivery.RefreshHostCommand(context.Background(), first.Scope)
	if err != nil || !reflect.DeepEqual(refreshed, retry) || recovery.writes != 2 {
		t.Fatal("prepared renewal not recoverable", err)
	}
	if err := delivery.RecordHostRefresh(context.Background(), first.Scope, refreshed.Generation); err != nil {
		t.Fatal(err)
	}
	if reference, err := delivery.RefreshHostCommand(context.Background(), first.Scope); err != nil || reference.URL != "" {
		t.Fatal("confirmed fresh install repeated", err)
	}
	r.workflow.CancelRequestedAt = time.Now()
	if reference, err := delivery.PrepareHostCommand(context.Background(), "session", "bootstrap", false, 30*time.Minute, build); err == nil || reference.URL != "" || prepared != 1 {
		t.Fatal("cancelled command prepared context")
	}
}
