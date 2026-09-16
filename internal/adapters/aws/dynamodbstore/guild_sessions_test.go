package dynamodbstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

func TestListGuildSessionsUsesExhaustiveCompatibilityPathBeforeCutover(t *testing.T) {
	session := indexedTestSession(t, "legacy-session", domain.StateRunning, time.Date(2026, 9, 16, 4, 0, 0, 0, time.UTC))
	attributes, _ := attributevalue.MarshalMap(toSessionItem(session))
	client := &fakeAPI{getItemOutput: &dynamodb.GetItemOutput{}, scanOutputs: []*dynamodb.ScanOutput{
		{ScannedCount: 1000, LastEvaluatedKey: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "MIXED"}, "sk": &types.AttributeValueMemberS{Value: "ENTITY"}}},
		{ScannedCount: 1, Items: []map[string]types.AttributeValue{attributes}},
	}}
	result, err := New(client, "metadata").ListGuildSessions(context.Background(), "guild-1", []domain.LifecycleState{domain.StateRunning}, ports.GuildSessionPage{})
	if err != nil || len(result.Sessions) != 1 || result.Sessions[0].ID != session.ID || client.scanIndex != 2 || client.queryIndex != 0 {
		t.Fatalf("result=%#v error=%v scans=%d queries=%d", result, err, client.scanIndex, client.queryIndex)
	}
	if client.scanInput == nil || !aws.ToBool(client.scanInput.ConsistentRead) {
		t.Fatal("pre-cutover compatibility discovery did not use strongly consistent scans")
	}
}

func TestListGuildSessionsMergesStatePagesDeterministically(t *testing.T) {
	now := time.Date(2026, 9, 16, 5, 0, 0, 0, time.UTC)
	idleNew := indexedTestSession(t, "idle-new", domain.StateIdle, now.Add(4*time.Minute))
	idleOld := indexedTestSession(t, "idle-old", domain.StateIdle, now.Add(time.Minute))
	runningNew := indexedTestSession(t, "running-new", domain.StateRunning, now.Add(3*time.Minute))
	runningOld := indexedTestSession(t, "running-old", domain.StateRunning, now.Add(2*time.Minute))
	client := &fakeAPI{
		getItemOutput: readyGuildIndexMarker(),
		queryOutputs: []*dynamodb.QueryOutput{
			{Items: marshalSessionItems(t, idleNew, idleOld), LastEvaluatedKey: keyForIndexedSession(idleOld)},
			{Items: marshalSessionItems(t, runningNew, runningOld), LastEvaluatedKey: keyForIndexedSession(runningOld)},
			{Items: marshalSessionItems(t, idleOld)},
			{Items: marshalSessionItems(t, runningOld)},
		},
	}
	repository := New(client, "metadata")
	first, err := repository.ListGuildSessions(context.Background(), "guild-1", []domain.LifecycleState{domain.StateRunning, domain.StateIdle}, ports.GuildSessionPage{Size: 2})
	if err != nil {
		t.Fatal(err)
	}
	assertSessionIDs(t, first.Sessions, "idle-new", "running-new")
	if first.NextCursor == "" {
		t.Fatal("first page omitted continuation cursor")
	}
	second, err := repository.ListGuildSessions(context.Background(), "guild-1", []domain.LifecycleState{domain.StateIdle, domain.StateRunning}, ports.GuildSessionPage{Size: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	assertSessionIDs(t, second.Sessions, "running-old", "idle-old")
	if second.NextCursor != "" {
		t.Fatalf("unexpected final cursor %q", second.NextCursor)
	}
}

func TestListGuildSessionsCursorIsBoundToGuildAndStates(t *testing.T) {
	cursor, err := encodeGuildSessionCursor(guildSessionCursor{Version: 1, GuildID: "guild-1", States: []string{"RUNNING"}, Positions: map[string]string{}, Done: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeAPI{getItemOutput: readyGuildIndexMarker()}
	_, err = New(client, "metadata").ListGuildSessions(context.Background(), "guild-2", []domain.LifecycleState{domain.StateRunning}, ports.GuildSessionPage{Cursor: cursor})
	if err == nil {
		t.Fatal("cross-guild cursor was accepted")
	}
}

func TestListGuildSessionsBreaksTimestampTiesByImmutableID(t *testing.T) {
	now := time.Date(2026, 9, 16, 5, 0, 0, 0, time.UTC)
	first := indexedTestSession(t, "session-a", domain.StateRunning, now)
	second := indexedTestSession(t, "session-b", domain.StateRunning, now)
	client := &fakeAPI{getItemOutput: readyGuildIndexMarker(), queryOutput: &dynamodb.QueryOutput{Items: marshalSessionItems(t, first, second)}}
	result, err := New(client, "metadata").ListGuildSessions(context.Background(), "guild-1", []domain.LifecycleState{domain.StateRunning}, ports.GuildSessionPage{Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertSessionIDs(t, result.Sessions, "session-b", "session-a")
}

func TestBackfillGuildSessionIndexIsConditionalAndReplaySafe(t *testing.T) {
	session := indexedTestSession(t, "session-1", domain.StateRunning, time.Date(2026, 9, 16, 6, 0, 0, 0, time.UTC))
	item := toSessionItem(session)
	item.GSI2PK, item.GSI2SK = "", ""
	attributes, err := attributevalue.MarshalMap(item)
	if err != nil {
		t.Fatal(err)
	}
	conditional := &types.ConditionalCheckFailedException{Message: stringPointer("changed")}
	client := &fakeAPI{scanOutput: &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{attributes}, ScannedCount: 1}, updateItemErr: conditional}
	result, err := New(client, "metadata").BackfillGuildSessionIndexPage(context.Background(), "", 100)
	if err != nil || result.Conflicted != 1 || result.Updated != 0 {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	client.updateItemErr = nil
	result, err = New(client, "metadata").BackfillGuildSessionIndexPage(context.Background(), "", 100)
	if err != nil || result.Updated != 1 || result.Conflicted != 0 {
		t.Fatalf("replay result = %#v, error = %v", result, err)
	}
	if client.updateItemInput.ConditionExpression == nil || *client.updateItemInput.ConditionExpression != "entity_type = :type AND #version = :version AND lifecycle_state = :state AND updated_at = :updated" {
		t.Fatalf("condition = %#v", client.updateItemInput.ConditionExpression)
	}
}

func TestBackfillGuildSessionIndexPagesBeyondOneThousandMixedItems(t *testing.T) {
	session := indexedTestSession(t, "test-62", domain.StateRunning, time.Date(2026, 9, 16, 6, 0, 0, 0, time.UTC))
	item := toSessionItem(session)
	item.GSI2PK, item.GSI2SK = "", ""
	attributes, _ := attributevalue.MarshalMap(item)
	outputs := make([]*dynamodb.ScanOutput, 0, 11)
	for page := 0; page < 10; page++ {
		outputs = append(outputs, &dynamodb.ScanOutput{ScannedCount: 100, LastEvaluatedKey: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: fmt.Sprintf("MIXED#%04d", page)},
			"sk": &types.AttributeValueMemberS{Value: "ENTITY"},
		}})
	}
	outputs = append(outputs, &dynamodb.ScanOutput{ScannedCount: 1, Items: []map[string]types.AttributeValue{attributes}})
	client := &fakeAPI{scanOutputs: outputs}
	repository := New(client, "metadata")
	cursor, scanned, updated := "", int32(0), 0
	for {
		result, err := repository.BackfillGuildSessionIndexPage(context.Background(), cursor, 100)
		if err != nil {
			t.Fatal(err)
		}
		scanned += result.Scanned
		updated += result.Updated
		cursor = result.NextCursor
		if cursor == "" {
			break
		}
	}
	if scanned != 1001 || updated != 1 || client.scanIndex != 11 {
		t.Fatalf("scanned=%d updated=%d pages=%d", scanned, updated, client.scanIndex)
	}
}

func TestEnableGuildSessionIndexRejectsPartialBackfill(t *testing.T) {
	session := indexedTestSession(t, "session-1", domain.StateRunning, time.Date(2026, 9, 16, 6, 0, 0, 0, time.UTC))
	item := toSessionItem(session)
	item.GSI2PK, item.GSI2SK = "", ""
	attributes, _ := attributevalue.MarshalMap(item)
	client := &fakeAPI{scanOutputs: []*dynamodb.ScanOutput{{Items: []map[string]types.AttributeValue{attributes}}, {}}}
	verification, err := New(client, "metadata").EnableGuildSessionIndex(context.Background(), time.Now())
	if err == nil || verification.MissingKeys != 1 || client.putItemInput != nil {
		t.Fatalf("verification = %#v, error = %v, marker = %#v", verification, err, client.putItemInput)
	}
}

func TestEnableGuildSessionIndexWritesReadyMarkerAfterExactVerification(t *testing.T) {
	session := indexedTestSession(t, "session-1", domain.StateRunning, time.Date(2026, 9, 16, 6, 0, 0, 0, time.UTC))
	attributes, _ := attributevalue.MarshalMap(toSessionItem(session))
	client := &fakeAPI{scanOutputs: []*dynamodb.ScanOutput{{Items: []map[string]types.AttributeValue{attributes}}, {Items: []map[string]types.AttributeValue{attributes}}}}
	verification, err := New(client, "metadata").EnableGuildSessionIndex(context.Background(), time.Date(2026, 9, 16, 7, 0, 0, 0, time.UTC))
	if err != nil || !verification.Valid() || client.putItemInput == nil {
		t.Fatalf("verification = %#v, error = %v, marker = %#v", verification, err, client.putItemInput)
	}
	if got := stringAttribute(t, client.putItemInput.Item["status"]); got != "READY" {
		t.Fatalf("marker status = %q", got)
	}
}

func indexedTestSession(t *testing.T, id string, state domain.LifecycleState, updated time.Time) domain.Session {
	t.Helper()
	session := testSession(t, updated)
	session.ID, session.Slug, session.LifecycleState, session.UpdatedAt = id, id, state, updated
	return session
}

func marshalSessionItems(t *testing.T, sessions ...domain.Session) []map[string]types.AttributeValue {
	t.Helper()
	items := make([]map[string]types.AttributeValue, 0, len(sessions))
	for _, session := range sessions {
		item, err := attributevalue.MarshalMap(toSessionItem(session))
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	return items
}

func keyForIndexedSession(session domain.Session) map[string]types.AttributeValue {
	item := toSessionItem(session)
	return guildSessionExclusiveStartKey(session.GuildID, item.GSI2SK)
}

func readyGuildIndexMarker() *dynamodb.GetItemOutput {
	return &dynamodb.GetItemOutput{Item: map[string]types.AttributeValue{"status": &types.AttributeValueMemberS{Value: "READY"}}}
}

func assertSessionIDs(t *testing.T, sessions []domain.Session, ids ...string) {
	t.Helper()
	if len(sessions) != len(ids) {
		t.Fatalf("sessions = %d, want %d", len(sessions), len(ids))
	}
	for index, id := range ids {
		if sessions[index].ID != id {
			t.Fatalf("session[%d] = %q, want %q (%s)", index, sessions[index].ID, id, fmt.Sprint(ids))
		}
	}
}

func stringPointer(value string) *string { return &value }
