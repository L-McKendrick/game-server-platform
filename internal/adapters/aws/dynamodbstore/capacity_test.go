package dynamodbstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestCheckCapacityAllowsEmptyOrOwnedSlotAndRejectsAnotherSession(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	item := func(sessionID string) map[string]any {
		return map[string]any{
			"pk": "CAPACITY#PROVISIONED", "sk": "SLOT#slot-0", "entity_type": "CapacitySlot", "schema_version": schemaVersion,
			"slot_id": "slot-0", "session_id": sessionID, "workflow_id": "workflow-1", "acquired_at": now.Format(time.RFC3339Nano),
		}
	}
	tests := []struct {
		name      string
		item      map[string]any
		wantQuota bool
	}{
		{name: "empty"},
		{name: "owned", item: item("session-1")},
		{name: "occupied", item: item("other-session"), wantQuota: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			attributes, err := attributevalue.MarshalMap(test.item)
			if err != nil {
				t.Fatal(err)
			}
			repository := New(&fakeAPI{getItemOutput: &dynamodb.GetItemOutput{Item: attributes}}, "metadata-table")
			err = repository.CheckCapacity(context.Background(), "session-1", 1)
			if errors.Is(err, domain.ErrQuotaExceeded) != test.wantQuota || (!test.wantQuota && err != nil) {
				t.Fatalf("CheckCapacity() error = %v, wantQuota=%t", err, test.wantQuota)
			}
		})
	}
}
