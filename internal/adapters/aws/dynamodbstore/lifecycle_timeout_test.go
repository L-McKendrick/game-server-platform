package dynamodbstore

import (
	"context"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestLifecycleTimeoutPolicyWriteIsAtomicWithAuditAndIdempotency(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	client := &fakeAPI{}
	repository := New(client, "metadata-table")
	policy := domain.GuildLifecycleTimeoutPolicy{GuildID: "guild-1", SleepAfterSeconds: 45 * 60, ArchiveAfterSeconds: 14 * 86400, Version: 1, UpdatedBy: "admin-1", UpdatedAt: now}
	audit := domain.LifecycleTimeoutPolicyAudit{ID: "audit-1", GuildID: "guild-1", ActorID: "admin-1", CorrelationID: "correlation-1", Reason: "planned event", PreviousSleepAfterSeconds: domain.DefaultSleepAfterSeconds, PreviousArchiveAfterSeconds: domain.DefaultArchiveAfterSeconds, SleepAfterSeconds: policy.SleepAfterSeconds, ArchiveAfterSeconds: policy.ArchiveAfterSeconds, OccurredAt: now}
	idempotency, err := domain.NewCompletedIdempotencyRecord("timeout-key", "request-hash", policy.GuildID, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.SaveLifecycleTimeoutPolicy(context.Background(), policy, 0, audit, idempotency); err != nil {
		t.Fatal(err)
	}
	transaction := client.transactWriteInput
	if transaction == nil || len(transaction.TransactItems) != 3 {
		t.Fatalf("transaction = %#v", transaction)
	}
	if got := stringAttribute(t, transaction.TransactItems[0].Put.Item["entity_type"]); got != "GuildLifecycleTimeoutPolicy" {
		t.Fatalf("policy entity = %q", got)
	}
	if got := stringAttribute(t, transaction.TransactItems[1].Put.Item["entity_type"]); got != "LifecycleTimeoutPolicyAudit" {
		t.Fatalf("audit entity = %q", got)
	}
	if got := stringAttribute(t, transaction.TransactItems[2].Put.Item["entity_type"]); got != "IdempotencyRecord" {
		t.Fatalf("idempotency entity = %q", got)
	}
}
