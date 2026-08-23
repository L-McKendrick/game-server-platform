package dynamodbstore

import (
	"context"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

func TestConsumeResetConfirmationWritesConfirmationOperationAndEnvironmentLockAtomically(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	confirmation, _ := domain.NewResetConfirmation("confirmation-1", "dev", "guild-1", "admin-1", now)
	confirmationItem, err := attributevalue.MarshalMap(toResetConfirmationItem(confirmation))
	if err != nil {
		t.Fatal(err)
	}
	operation, _ := domain.NewResetOperation("operation-1", "dev", "guild-1", "admin-1", "correlation-1", now)
	client := &fakeAPI{getItemOutput: &dynamodb.GetItemOutput{Item: confirmationItem}}
	repository := New(client, "metadata")
	started, err := repository.ConsumeResetConfirmation(context.Background(), confirmation.ID, "admin-1", "guild-1", confirmation.Phrase(), operation, now)
	if err != nil || started.ID != operation.ID {
		t.Fatalf("started=%#v err=%v", started, err)
	}
	if client.transactWriteInput == nil || len(client.transactWriteInput.TransactItems) != 3 {
		t.Fatalf("transaction = %#v", client.transactWriteInput)
	}
	lock := client.transactWriteInput.TransactItems[2].Put.Item
	if got := stringAttribute(t, lock["pk"]); got != "RESET#dev" {
		t.Fatalf("lock pk = %q", got)
	}
	if got := stringAttribute(t, lock["operation_id"]); got != operation.ID {
		t.Fatalf("lock operation = %q", got)
	}
}

func TestSaveTerminalResetReleasesLockAndStoresLatestPointer(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	operation, _ := domain.NewResetOperation("operation-1", "dev", "guild-1", "admin-1", "correlation-1", now)
	operation.Status, operation.Stage, operation.Version = domain.ResetSucceeded, "Runtime reset complete", 2
	operation.UpdatedAt, operation.CompletedAt = now.Add(time.Minute), now.Add(time.Minute)
	client := &fakeAPI{}
	repository := New(client, "metadata")
	if err := repository.SaveResetOperation(context.Background(), operation, 1); err != nil {
		t.Fatal(err)
	}
	transaction := client.transactWriteInput
	if transaction == nil || len(transaction.TransactItems) != 3 {
		t.Fatalf("transaction = %#v", transaction)
	}
	audit := transaction.TransactItems[1].Put.Item
	if got := stringAttribute(t, audit["sk"]); got != "AUDIT#LATEST" {
		t.Fatalf("audit sk = %q", got)
	}
	if _, hasStatus := audit["status"]; hasStatus {
		t.Fatal("latest audit pointer duplicated terminal detail")
	}
	if transaction.TransactItems[2].Delete == nil {
		t.Fatal("terminal save did not release active lock")
	}

	operationItem, _ := attributevalue.MarshalMap(toResetOperationItem(operation))
	client.getItemOutputs = []*dynamodb.GetItemOutput{{Item: audit}, {Item: operationItem}}
	latest, err := repository.GetLatestReset(context.Background(), "dev")
	if err != nil || latest.ID != operation.ID || latest.Status != domain.ResetSucceeded {
		t.Fatalf("latest=%#v err=%v", latest, err)
	}
}
