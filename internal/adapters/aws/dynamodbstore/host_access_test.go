package dynamodbstore

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestHostAccessCreationUsesAtomicAuthorityAndMapsConflict(t *testing.T) {
	now := time.Now().UTC()
	attempt := domain.HostAccessAttempt{Scope: domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: now.Add(time.Hour)}, Revision: 1, Generation: 1, ManifestVersionID: "version", ManifestSHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), ManifestSizeBytes: 100, ExpiresAt: now.Add(10 * time.Minute), DispatchState: domain.HostAccessPrepared}
	client := &fakeAPI{getItemOutput: &dynamodb.GetItemOutput{}}
	repository := New(client, "metadata")
	if err := repository.SaveHostAccessAttempt(context.Background(), attempt, 0, 7, now); err != nil {
		t.Fatal(err)
	}
	transaction := client.transactWriteInput
	if transaction == nil || len(transaction.TransactItems) != 3 {
		t.Fatal("missing atomic authority transaction")
	}
	session := transaction.TransactItems[0].ConditionCheck
	workflow := transaction.TransactItems[1].ConditionCheck
	put := transaction.TransactItems[2].Put
	if put.Item["sk"].(*types.AttributeValueMemberS).Value != "HOSTACCESS#operation#attempt" {
		t.Fatal("recovery record does not qualify attempt")
	}
	other := hostAccessKey("session", "operation", "rollback")
	if other["pk"].(*types.AttributeValueMemberS).Value != put.Item["pk"].(*types.AttributeValueMemberS).Value || other["sk"].(*types.AttributeValueMemberS).Value == put.Item["sk"].(*types.AttributeValueMemberS).Value {
		t.Fatal("command attempts collide in shared workflow")
	}
	for _, constraint := range []string{"#version = :version", "guild_id = :guild", "instance_id = :instance", "active_workflow_id = :operation", "active_workflow_lease_expires_at >= :deadline"} {
		if !strings.Contains(aws.ToString(session.ConditionExpression), constraint) {
			t.Fatalf("missing session constraint %s", constraint)
		}
	}
	for _, constraint := range []string{"#status = :running", "attribute_not_exists(cancel_requested_at)", "attribute_not_exists(completed_at)", "lease_expires_at >= :deadline", "command_deadline_at >= :deadline"} {
		if !strings.Contains(aws.ToString(workflow.ConditionExpression), constraint) {
			t.Fatalf("missing workflow constraint %s", constraint)
		}
	}
	if aws.ToString(put.ConditionExpression) != "attribute_not_exists(pk)" {
		t.Fatal("creation may overwrite existing attempt")
	}
	client.transactWriteErr = &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}
	if err := repository.SaveHostAccessAttempt(context.Background(), attempt, 0, 7, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflict error=%v", err)
	}
}

func TestHostAccessDeadlineComparisonPreservesLegacyLeaseOrdering(t *testing.T) {
	now := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	for _, fraction := range []time.Duration{0, 400 * time.Millisecond} {
		deadline := now.Add(time.Hour + fraction)
		attempt := domain.HostAccessAttempt{Scope: domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: deadline}, Revision: 1, Generation: 1, ManifestVersionID: "version", ManifestSHA256: base64.StdEncoding.EncodeToString(make([]byte, 32)), ManifestSizeBytes: 100, ExpiresAt: now.Add(10 * time.Minute), DispatchState: domain.HostAccessPrepared}
		client := &fakeAPI{getItemOutput: &dynamodb.GetItemOutput{}}
		if err := New(client, "metadata").SaveHostAccessAttempt(context.Background(), attempt, 0, 7, now); err != nil {
			t.Fatal(err)
		}
		ceiling := deadline.Truncate(time.Second)
		if ceiling.Before(deadline) {
			ceiling = ceiling.Add(time.Second)
		}
		for _, item := range client.transactWriteInput.TransactItems[:2] {
			bound := item.ConditionCheck.ExpressionAttributeValues[":deadline"].(*types.AttributeValueMemberS).Value
			for _, offset := range []time.Duration{-time.Second, -time.Nanosecond, 0, time.Nanosecond, 400 * time.Millisecond, 404511800 * time.Nanosecond, time.Second} {
				lease := ceiling.Add(offset)
				for _, format := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000000000Z"} {
					accepted := lease.Format(format) >= bound
					if accepted != !lease.Before(ceiling) {
						t.Errorf("deadline=%s lease=%s bound=%s accepted=%t", deadline, lease.Format(format), bound, accepted)
					}
					if accepted && lease.Before(deadline) {
						t.Fatal("authorized lease ending before actual deadline")
					}
				}
			}
		}
	}
}
