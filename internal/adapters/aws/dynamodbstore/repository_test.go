package dynamodbstore

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type fakeAPI struct {
	getItemOutput      *dynamodb.GetItemOutput
	getItemErr         error
	queryOutput        *dynamodb.QueryOutput
	queryErr           error
	transactWriteInput *dynamodb.TransactWriteItemsInput
	transactWriteErr   error
}

func (fake *fakeAPI) GetItem(
	_ context.Context,
	_ *dynamodb.GetItemInput,
	_ ...func(*dynamodb.Options),
) (*dynamodb.GetItemOutput, error) {
	return fake.getItemOutput, fake.getItemErr
}

func (fake *fakeAPI) Query(
	_ context.Context,
	_ *dynamodb.QueryInput,
	_ ...func(*dynamodb.Options),
) (*dynamodb.QueryOutput, error) {
	return fake.queryOutput, fake.queryErr
}

func (fake *fakeAPI) TransactWriteItems(
	_ context.Context,
	input *dynamodb.TransactWriteItemsInput,
	_ ...func(*dynamodb.Options),
) (*dynamodb.TransactWriteItemsOutput, error) {
	fake.transactWriteInput = input

	return &dynamodb.TransactWriteItemsOutput{}, fake.transactWriteErr
}

func TestCreateWritesSessionEventAndIdempotencyAtomically(t *testing.T) {
	t.Parallel()

	client := &fakeAPI{}
	repository := New(client, "metadata-table")
	now := time.Date(2026, 8, 3, 20, 0, 0, 0, time.UTC)

	session := testSession(t, now)
	event := domain.NewSessionCreatedEvent(
		"event-1",
		"correlation-1",
		domain.Actor{
			Type: domain.ActorTypeDiscordUser,
			ID:   session.OwnerDiscordUserID,
		},
		session,
		now,
	)
	idempotency := testIdempotency(t, now, session.ID)

	if err := repository.Create(
		context.Background(),
		session,
		event,
		idempotency,
	); err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}

	if client.transactWriteInput == nil {
		t.Fatal("Create() did not call TransactWriteItems")
	}

	items := client.transactWriteInput.TransactItems
	if len(items) != 3 {
		t.Fatalf("transaction item count = %d; want 3", len(items))
	}

	idempotencyPut := items[2].Put
	if idempotencyPut == nil {
		t.Fatal("third transaction item is not a Put")
	}

	if got := stringAttribute(t, idempotencyPut.Item["pk"]); got != "IDEMPOTENCY#discord:interaction-1" {
		t.Errorf(
			"idempotency pk = %q; want %q",
			got,
			"IDEMPOTENCY#discord:interaction-1",
		)
	}

	if got := stringAttribute(t, idempotencyPut.Item["sk"]); got != idempotencySortKey {
		t.Errorf("idempotency sk = %q; want %q", got, idempotencySortKey)
	}
}

func TestGetIdempotencyDecodesStoredRecord(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 20, 0, 0, 0, time.UTC)
	record := testIdempotency(t, now, "session-1")

	attributes, err := attributevalue.MarshalMap(toIdempotencyItem(record))
	if err != nil {
		t.Fatalf("MarshalMap() returned error: %v", err)
	}

	client := &fakeAPI{
		getItemOutput: &dynamodb.GetItemOutput{Item: attributes},
	}
	repository := New(client, "metadata-table")

	stored, err := repository.GetIdempotency(
		context.Background(),
		record.Key,
	)
	if err != nil {
		t.Fatalf("GetIdempotency() returned error: %v", err)
	}

	if stored.RequestHash != record.RequestHash {
		t.Errorf(
			"RequestHash = %q; want %q",
			stored.RequestHash,
			record.RequestHash,
		)
	}

	if stored.ResultReference != record.ResultReference {
		t.Errorf(
			"ResultReference = %q; want %q",
			stored.ResultReference,
			record.ResultReference,
		)
	}
}

func testSession(t *testing.T, now time.Time) domain.Session {
	t.Helper()

	session, err := domain.NewSession(
		domain.NewSessionInput{
			ID:                 "session-1",
			Slug:               "saturday-arma",
			DisplayName:        "Saturday Arma",
			GameType:           "arma3",
			OwnerDiscordUserID: "owner-1",
			GuildID:            "guild-1",
			ChannelID:          "channel-1",
		},
		now,
	)
	if err != nil {
		t.Fatalf("NewSession() returned error: %v", err)
	}

	return session
}

func testIdempotency(
	t *testing.T,
	now time.Time,
	resultReference string,
) domain.IdempotencyRecord {
	t.Helper()

	record, err := domain.NewCompletedIdempotencyRecord(
		"discord:interaction-1",
		"request-hash",
		resultReference,
		now,
		7*24*time.Hour,
	)
	if err != nil {
		t.Fatalf("NewCompletedIdempotencyRecord() returned error: %v", err)
	}

	return record
}

func stringAttribute(
	t *testing.T,
	attribute types.AttributeValue,
) string {
	t.Helper()

	value, ok := attribute.(*types.AttributeValueMemberS)
	if !ok {
		t.Fatalf("attribute type = %T; want string", attribute)
	}

	return value.Value
}
