package dynamodbstore

import (
	"context"
	"errors"
	"strings"
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
	scanOutput         *dynamodb.ScanOutput
	scanErr            error
	scanInput          *dynamodb.ScanInput
	transactWriteInput *dynamodb.TransactWriteItemsInput
	transactWriteErr   error
	putItemInput       *dynamodb.PutItemInput
}

func TestSaveCardReferenceUsesIndependentChannelBoundItem(t *testing.T) {
	t.Parallel()
	client := &fakeAPI{}
	repository := New(client, "metadata-table")
	if err := repository.SaveCardReference(context.Background(), domain.SessionCardReference{
		SessionID: "session-1", ChannelID: "channel-1", MessageID: "message-1",
		DeliveredRevision: 4, DeliveredNotificationID: "card-4", ContentSHA256: "digest-4",
	}); err != nil {
		t.Fatalf("SaveCardReference() returned error: %v", err)
	}
	if client.putItemInput == nil || client.putItemInput.ConditionExpression == nil ||
		*client.putItemInput.ConditionExpression != "attribute_not_exists(pk) OR channel_id = :channel" {
		t.Fatalf("put input = %#v", client.putItemInput)
	}
	client.getItemOutput = &dynamodb.GetItemOutput{Item: client.putItemInput.Item}
	reference, err := repository.GetCardReference(context.Background(), "session-1")
	if err != nil || reference.MessageID != "message-1" || reference.ChannelID != "channel-1" ||
		reference.DeliveredRevision != 4 || reference.DeliveredNotificationID != "card-4" || reference.ContentSHA256 != "digest-4" {
		t.Fatalf("GetCardReference() = %#v, %v", reference, err)
	}
}

func TestGetCardReferenceReadsLegacyDeliveryMetadata(t *testing.T) {
	t.Parallel()
	attributes, err := attributevalue.MarshalMap(sessionCardItem{
		PK: sessionPartitionKey("session-legacy"), SK: sessionCardSortKey,
		EntityType: "SessionCard", SchemaVersion: schemaVersion,
		SessionID: "session-legacy", ChannelID: "channel-1", MessageID: "message-legacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := New(&fakeAPI{getItemOutput: &dynamodb.GetItemOutput{Item: attributes}}, "metadata-table")
	reference, err := repository.GetCardReference(context.Background(), "session-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if reference.MessageID != "message-legacy" || reference.DeliveredRevision != 0 || reference.DeliveredNotificationID != "" {
		t.Fatalf("legacy reference = %#v", reference)
	}
}

func TestSaveModlistReferenceUsesIndependentChannelBoundItem(t *testing.T) {
	t.Parallel()
	client := &fakeAPI{}
	repository := New(client, "metadata-table")
	reference := domain.SessionModlistReference{
		SessionID: "session-1", ChannelID: "channel-1", MessageID: "modlist-message-1",
		ObjectKey: "sessions/session-1/input/modlists/digest/saturday-arma-modlist.html",
		Filename:  "saturday-arma-modlist.html", DeliveredRevision: 4,
		DeliveredNotificationID: "modlist-4", ContentSHA256: strings.Repeat("a", 64),
	}
	if err := repository.SaveModlistReference(context.Background(), reference); err != nil {
		t.Fatalf("SaveModlistReference() returned error: %v", err)
	}
	if client.putItemInput == nil || client.putItemInput.ConditionExpression == nil ||
		*client.putItemInput.ConditionExpression != "attribute_not_exists(pk) OR channel_id = :channel" {
		t.Fatalf("put input = %#v", client.putItemInput)
	}
	if got := stringAttribute(t, client.putItemInput.Item["sk"]); got != sessionModlistSortKey {
		t.Fatalf("sort key = %q; want %q", got, sessionModlistSortKey)
	}
	client.getItemOutput = &dynamodb.GetItemOutput{Item: client.putItemInput.Item}
	stored, err := repository.GetModlistReference(context.Background(), reference.SessionID)
	if err != nil || stored != reference {
		t.Fatalf("GetModlistReference() = %#v, %v; want %#v", stored, err, reference)
	}
}

func (fake *fakeAPI) Scan(_ context.Context, input *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	fake.scanInput = input
	if fake.scanOutput == nil {
		return &dynamodb.ScanOutput{}, fake.scanErr
	}
	return fake.scanOutput, fake.scanErr
}

func TestListByGuildReadsLegacySessionMetadataWithBoundedScan(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	session := testSession(t, now)
	attributes, err := attributevalue.MarshalMap(toSessionItem(session))
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeAPI{scanOutput: &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{attributes}, ScannedCount: 7}}
	repository := New(client, "metadata-table")

	sessions, err := repository.ListByGuild(context.Background(), "guild-1", 25)
	if err != nil {
		t.Fatalf("ListByGuild() returned error: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != session.ID {
		t.Fatalf("ListByGuild() = %#v", sessions)
	}
	if client.scanInput == nil || client.scanInput.FilterExpression == nil || *client.scanInput.FilterExpression != "entity_type = :type AND guild_id = :guild" {
		t.Fatalf("scan input = %#v", client.scanInput)
	}
	if client.scanInput.Limit == nil || *client.scanInput.Limit > 100 {
		t.Fatalf("scan limit = %#v; want bounded page", client.scanInput.Limit)
	}
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

func (fake *fakeAPI) PutItem(
	_ context.Context,
	input *dynamodb.PutItemInput,
	_ ...func(*dynamodb.Options),
) (*dynamodb.PutItemOutput, error) {
	fake.putItemInput = input
	return &dynamodb.PutItemOutput{}, nil
}

func (fake *fakeAPI) DeleteItem(
	_ context.Context,
	_ *dynamodb.DeleteItemInput,
	_ ...func(*dynamodb.Options),
) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
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
	if len(items) != 4 {
		t.Fatalf("transaction item count = %d; want 4", len(items))
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

	slugClaimPut := items[3].Put
	if slugClaimPut == nil {
		t.Fatal("fourth transaction item is not a Put")
	}
	if got := stringAttribute(t, slugClaimPut.Item["pk"]); got != "GUILD#guild-1" {
		t.Errorf("slug claim pk = %q; want %q", got, "GUILD#guild-1")
	}
	if got := stringAttribute(t, slugClaimPut.Item["sk"]); got != "SLUG#saturday-arma" {
		t.Errorf("slug claim sk = %q; want %q", got, "SLUG#saturday-arma")
	}
}

func TestCreateClassifiesAtomicSlugClaimConflict(t *testing.T) {
	t.Parallel()

	code := "ConditionalCheckFailed"
	client := &fakeAPI{transactWriteErr: &types.TransactionCanceledException{
		CancellationReasons: []types.CancellationReason{{}, {}, {}, {Code: &code}},
	}}
	repository := New(client, "metadata-table")
	now := time.Date(2026, 8, 14, 21, 0, 0, 0, time.UTC)
	session := testSession(t, now)
	event := domain.NewSessionCreatedEvent("event-1", "correlation-1", domain.Actor{
		Type: domain.ActorTypeDiscordUser, ID: session.OwnerDiscordUserID,
	}, session, now)

	err := repository.Create(context.Background(), session, event, testIdempotency(t, now, session.ID))
	if !errors.Is(err, domain.ErrSlugConflict) {
		t.Fatalf("Create() error = %v; want ErrSlugConflict", err)
	}
}

func TestCreateRejectsSlugUsedByLegacySessionWithoutClaim(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 21, 30, 0, 0, time.UTC)
	legacy := testSession(t, now.Add(-time.Hour))
	attributes, err := attributevalue.MarshalMap(toSessionItem(legacy))
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeAPI{scanOutput: &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{attributes}, ScannedCount: 1}}
	repository := New(client, "metadata-table")
	session := testSession(t, now)
	session.ID = "session-2"
	event := domain.NewSessionCreatedEvent("event-2", "correlation-2", domain.Actor{Type: domain.ActorTypeDiscordUser, ID: session.OwnerDiscordUserID}, session, now)

	err = repository.Create(context.Background(), session, event, testIdempotency(t, now, session.ID))
	if !errors.Is(err, domain.ErrSlugConflict) {
		t.Fatalf("Create() error = %v; want ErrSlugConflict", err)
	}
	if client.transactWriteInput != nil {
		t.Fatal("Create() attempted a transaction after finding a legacy slug collision")
	}
}

func TestCreateTransactionTokenChangesWithCollisionCandidate(t *testing.T) {
	t.Parallel()

	first := createTransactionToken("01KTESTEVENTIDENTIFIER000001", "friday-operations")
	second := createTransactionToken("01KTESTEVENTIDENTIFIER000001", "friday-operations-2")
	if first == second {
		t.Fatal("transaction token did not change with the collision candidate")
	}
	if len(first) > 36 || len(second) > 36 {
		t.Fatalf("transaction token lengths = %d/%d; want at most 36", len(first), len(second))
	}
}

func TestAcquireWorkflowConditionallyWritesLockWorkflowAndEvent(t *testing.T) {
	t.Parallel()

	client := &fakeAPI{}
	repository := New(client, "metadata-table")
	now := time.Date(2026, 8, 8, 22, 0, 0, 0, time.UTC)
	session := testSession(t, now)
	expectedVersion := session.Version
	if err := session.AcquireWorkflowLock("command-1", "ProvisionSession", 2*time.Hour, now.Add(time.Second)); err != nil {
		t.Fatalf("AcquireWorkflowLock() returned error: %v", err)
	}
	workflow := domain.Workflow{
		ID: "command-1", SessionID: session.ID, Type: "ProvisionSession", Status: domain.WorkflowPending,
		RequestedBy: session.OwnerDiscordUserID, CorrelationID: "correlation-workflow",
		ExpectedVersion: expectedVersion, StartedAt: now.Add(time.Second), LeaseExpiresAt: now.Add(2*time.Hour + time.Second),
	}
	event := domain.NewWorkflowEvent(
		"workflow-event", domain.EventWorkflowStarted, workflow.CorrelationID,
		domain.Actor{Type: domain.ActorTypeDiscordUser, ID: workflow.RequestedBy},
		session, workflow, workflow.StartedAt,
	)

	if err := repository.AcquireWorkflow(context.Background(), session, expectedVersion, workflow, event); err != nil {
		t.Fatalf("AcquireWorkflow() returned error: %v", err)
	}
	if client.transactWriteInput == nil || len(client.transactWriteInput.TransactItems) != 3 {
		t.Fatalf("workflow transaction = %#v; want three writes", client.transactWriteInput)
	}
	condition := *client.transactWriteInput.TransactItems[0].Put.ConditionExpression
	if condition != "#version = :expected_version AND (attribute_not_exists(active_workflow_id) OR active_workflow_lease_expires_at < :now)" {
		t.Fatalf("session lock condition = %q", condition)
	}
	workflowPut := client.transactWriteInput.TransactItems[1].Put
	if got := stringAttribute(t, workflowPut.Item["sk"]); got != "WORKFLOW#command-1" {
		t.Fatalf("workflow sort key = %q", got)
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

func TestSessionItemRoundTripPreservesVanillaMode(t *testing.T) {
	t.Parallel()
	session := testSession(t, time.Date(2026, 8, 14, 6, 0, 0, 0, time.UTC))
	session.Vanilla = true
	stored, err := fromSessionItem(toSessionItem(session))
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Vanilla {
		t.Fatal("vanilla mode was not preserved by DynamoDB mapping")
	}
}

func TestSessionItemReadInfersAcceptedLegacyArtifactObjectKeys(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 23, 0, 0, 0, time.UTC)
	item := toSessionItem(testSession(t, now))
	item.MissionObjectKey = "sessions/session-1/input/mission.pbo"
	item.PresetObjectKey = "sessions/session-1/input/preset.html"
	item.MissionArtifactStatus = ""
	item.PresetArtifactStatus = ""

	stored, err := fromSessionItem(item)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MissionArtifactStatus != domain.ArtifactAccepted || stored.PresetArtifactStatus != domain.ArtifactAccepted {
		t.Fatalf("legacy artifact statuses = mission %q preset %q; want accepted", stored.MissionArtifactStatus, stored.PresetArtifactStatus)
	}
}

func TestSessionItemRoundTripPreservesOptionalDescription(t *testing.T) {
	t.Parallel()
	session := testSession(t, time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC))
	session.Description = "Weekly co-op night"

	stored, err := fromSessionItem(toSessionItem(session))
	if err != nil {
		t.Fatal(err)
	}
	if stored.DisplayName != session.DisplayName || stored.Slug != session.Slug || stored.Description != session.Description {
		t.Fatalf("readable identity = (%q, %q, %q); want (%q, %q, %q)", stored.DisplayName, stored.Slug, stored.Description, session.DisplayName, session.Slug, session.Description)
	}
}

func TestSessionItemRoundTripPreservesProgressMilestone(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 15, 10, 30, 0, 0, time.UTC)
	session := testSession(t, now)
	if err := session.AcquireWorkflowLock("workflow-1", "ProvisionSession", time.Hour, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AdvanceProgress("workflow-1", domain.ProgressInfrastructureReady, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	stored, err := fromSessionItem(toSessionItem(session))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Progress != session.Progress {
		t.Fatalf("progress = %#v; want %#v", stored.Progress, session.Progress)
	}
}

func TestSessionItemWithoutProgressRemainsReadable(t *testing.T) {
	t.Parallel()
	session := testSession(t, time.Date(2026, 8, 15, 11, 0, 0, 0, time.UTC))
	item := toSessionItem(session)
	item.ProgressWorkflowID, item.ProgressWorkflowType, item.ProgressMilestone, item.ProgressUpdatedAt = "", "", "", ""

	stored, err := fromSessionItem(item)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Progress.Empty() {
		t.Fatalf("legacy progress = %#v; want empty", stored.Progress)
	}
}

func TestSessionItemWithoutDescriptionRemainsReadable(t *testing.T) {
	t.Parallel()
	session := testSession(t, time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC))
	item := toSessionItem(session)
	item.Description = ""

	stored, err := fromSessionItem(item)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Description != "" {
		t.Fatalf("Description = %q; want empty legacy default", stored.Description)
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
