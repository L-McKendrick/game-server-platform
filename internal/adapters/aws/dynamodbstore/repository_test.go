package dynamodbstore

import (
	"context"
	"errors"
	"reflect"
	"slices"
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
	getItemOutputs     []*dynamodb.GetItemOutput
	getItemIndex       int
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
	if fake.getItemIndex < len(fake.getItemOutputs) {
		output := fake.getItemOutputs[fake.getItemIndex]
		fake.getItemIndex++
		return output, fake.getItemErr
	}
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

func TestWorkflowCommandDeadlineRoundTripsBackwardCompatibly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	workflow := domain.Workflow{
		ID: "bootstrap-1", SessionID: "session-1", Type: domain.BootstrapWorkflowType,
		Status: domain.WorkflowRunning, RequestedBy: "owner-1", CorrelationID: "correlation-1",
		ExpectedVersion: 1, StartedAt: now, LeaseExpiresAt: now.Add(7 * time.Hour),
		CommandID: "command-1", CommandDeadlineAt: now.Add(6 * time.Hour),
	}
	stored, err := fromWorkflowItem(toWorkflowItem(workflow))
	if err != nil {
		t.Fatal(err)
	}
	if stored.CommandID != workflow.CommandID || !stored.CommandDeadlineAt.Equal(workflow.CommandDeadlineAt) {
		t.Fatalf("stored workflow = %#v", stored)
	}

	workflow.CommandID = ""
	workflow.CommandDeadlineAt = time.Time{}
	legacy, err := fromWorkflowItem(toWorkflowItem(workflow))
	if err != nil || legacy.CommandID != "" || !legacy.CommandDeadlineAt.IsZero() {
		t.Fatalf("legacy workflow = %#v, err = %v", legacy, err)
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

func TestSessionItemRoundTripPreservesCreatorDLCSelection(t *testing.T) {
	t.Parallel()
	session := testSession(t, time.Date(2026, 8, 23, 6, 0, 0, 0, time.UTC))
	session.CreatorDLCs = []string{domain.CreatorDLCGlobalMobilization, domain.CreatorDLCReactionForces}
	session.StartWhenReady = true
	stored, err := fromSessionItem(toSessionItem(session))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stored.CreatorDLCs, session.CreatorDLCs) {
		t.Fatalf("CreatorDLCs = %#v; want %#v", stored.CreatorDLCs, session.CreatorDLCs)
	}
	if !stored.StartWhenReady {
		t.Fatal("start-when-ready intent was not preserved")
	}
}

func TestSessionItemRoundTripPreservesServerConfigSnapshot(t *testing.T) {
	t.Parallel()
	session := testSession(t, time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC))
	session.ServerConfigRevision = 3
	session.ServerConfigObjectKey = "guilds/guild-1/server-config/revisions/000003-a/server.cfg"
	session.ServerConfigSHA256 = strings.Repeat("a", 64)
	stored, err := fromSessionItem(toSessionItem(session))
	if err != nil || stored.ServerConfigRevision != 3 || stored.ServerConfigObjectKey != session.ServerConfigObjectKey || stored.ServerConfigSHA256 != session.ServerConfigSHA256 {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
}

func TestSessionItemRoundTripPreservesPresetRevisions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 20, 0, 0, 0, time.UTC)
	session := testSession(t, now)
	session.PresetObjectKey = "sessions/session-1/input/presets/v1.html"
	session.PresetRevisionSequence = 2
	session.ActivePresetRevision = domain.PresetRevision{
		Number: 1, PresetObjectKey: session.PresetObjectKey, Status: domain.PresetRevisionActive,
		StagedAt: now, ActivatedAt: now,
		Modlist: domain.PresetModlistMetadata{ObjectKey: "sessions/session-1/input/modlists/v1/modlist.html", Filename: "session-1-modlist.html", SHA256: strings.Repeat("a", 64), SizeBytes: 1200, WorkshopCount: 3},
	}
	session.PendingPresetRevision = domain.PresetRevision{
		Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/presets/v2.html", Status: domain.PresetRevisionFailed,
		StagedAt: now.Add(time.Minute), FailedAt: now.Add(2 * time.Minute), FailureDetail: "health verification failed",
		RollbackDisposition: domain.PresetRollbackSucceeded, RollbackAt: now.Add(2 * time.Minute), RollbackDetail: "Previous active mod configuration restored and health-checked.",
		Modlist: domain.PresetModlistMetadata{ObjectKey: "sessions/session-1/input/modlists/v2/modlist.html", Filename: "session-1-modlist.html", SHA256: strings.Repeat("b", 64), SizeBytes: 1300, WorkshopCount: 4},
	}
	session.ServerPresetObjectKey = "sessions/session-1/input/server-presets/v1.html"
	session.ServerPresetRevisionSequence = 2
	session.ActiveServerPresetRevision = domain.PresetRevision{Number: 1, PresetObjectKey: session.ServerPresetObjectKey, Status: domain.PresetRevisionActive, StagedAt: now, ActivatedAt: now}
	session.PendingServerPresetRevision = domain.PresetRevision{Number: 2, BaseRevision: 1, PresetObjectKey: "sessions/session-1/input/server-presets/v2.html", Status: domain.PresetRevisionPending, StagedAt: now.Add(time.Minute)}
	session.UpdatedAt = now.Add(2 * time.Minute)

	stored, err := fromSessionItem(toSessionItem(session))
	if err != nil {
		t.Fatal(err)
	}
	if stored.PresetRevisionSequence != session.PresetRevisionSequence || stored.ActivePresetRevision != session.ActivePresetRevision || stored.PendingPresetRevision != session.PendingPresetRevision || stored.PresetObjectKey != session.PresetObjectKey || stored.ServerPresetRevisionSequence != session.ServerPresetRevisionSequence || stored.ActiveServerPresetRevision != session.ActiveServerPresetRevision || stored.PendingServerPresetRevision != session.PendingServerPresetRevision || stored.ServerPresetObjectKey != session.ServerPresetObjectKey {
		t.Fatalf("stored preset revisions = %#v / %#v", stored.ActivePresetRevision, stored.PendingPresetRevision)
	}
}

func TestSessionItemReadMigratesLegacyPresetPointer(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 20, 0, 0, 0, time.UTC)
	item := toSessionItem(testSession(t, now))
	item.PresetObjectKey = "sessions/session-1/input/presets/legacy.html"
	item.PresetRevisionSequence = 0
	item.ActivePresetRevision = 0
	item.ActivePresetObjectKey = ""
	item.ActivePresetStagedAt = ""
	item.ActivePresetActivatedAt = ""

	stored, err := fromSessionItem(item)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PresetRevisionSequence != 1 || stored.ActivePresetRevision.Number != 1 || stored.ActivePresetRevision.PresetObjectKey != item.PresetObjectKey {
		t.Fatalf("migrated legacy revision = %#v sequence=%d", stored.ActivePresetRevision, stored.PresetRevisionSequence)
	}
	written := toSessionItem(stored)
	if written.ActivePresetRevision != 1 || written.ActivePresetObjectKey != item.PresetObjectKey {
		t.Fatalf("migration-on-write item = %#v", written)
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

func TestSessionItemRoundTripPreservesMissionHistoryAndSelections(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 23, 16, 0, 0, 0, time.UTC)
	session := testSession(t, now)
	key := "sessions/session-1/input/missions/" + strings.Repeat("a", 64) + "-Coop.Altis.pbo"
	session.MissionObjectKey = key
	session.MissionFiles = []domain.MissionRecord{{ObjectKey: key, Filename: "Coop.Altis.pbo", Status: domain.ArtifactAccepted, AddedAt: now}}
	session.ConfiguredMission = domain.UploadedMissionSelection(key)
	session.CurrentMission = domain.DefaultMissionSelection()
	stored, err := fromSessionItem(toSessionItem(session))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stored.MissionFiles, session.MissionFiles) || stored.ConfiguredMission != session.ConfiguredMission || stored.CurrentMission != session.CurrentMission {
		t.Fatalf("mission round trip = %#v / %#v / %#v", stored.MissionFiles, stored.ConfiguredMission, stored.CurrentMission)
	}
}

func TestSessionItemReadMigratesLegacyMissionPointer(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 23, 16, 0, 0, 0, time.UTC)
	item := toSessionItem(testSession(t, now))
	item.MissionObjectKey = "sessions/session-1/input/missions/Legacy.Altis.pbo"
	item.MissionFilesJSON, item.ConfiguredMissionJSON, item.CurrentMissionJSON = "", "", ""
	item.MissionArtifactStatus = ""
	stored, err := fromSessionItem(item)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.MissionFiles) != 1 || stored.MissionFiles[0].Status != domain.ArtifactAccepted || stored.ConfiguredMission.Template != "Legacy.Altis" || stored.ConfiguredMission.ObjectKey != item.MissionObjectKey {
		t.Fatalf("legacy mission migration = %#v", stored)
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

func TestSessionItemRoundTripPreservesSanitizedFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	session := testSession(t, now)
	failure, err := domain.NewFailureRecord(domain.FailureRecordInput{
		Code: "ERR_BOOTSTRAP_FAILED", Stage: "Game and content setup",
		RetryDisposition: domain.RetryNotScheduled, ResourceImpact: domain.ResourceCostRetained,
		Detail: "Setup stopped before health verification.", FailedAt: now,
		SupportReference: "support_ABC123",
	})
	if err != nil {
		t.Fatal(err)
	}
	session.Failure = failure

	stored, err := fromSessionItem(toSessionItem(session))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Failure != failure {
		t.Fatalf("failure = %#v; want %#v", stored.Failure, failure)
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
	if _, err := session.SetProgressActivity("workflow-1", "Arma 3 server files", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}

	stored, err := fromSessionItem(toSessionItem(session))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.Progress, session.Progress) {
		t.Fatalf("progress = %#v; want %#v", stored.Progress, session.Progress)
	}
}

func TestSessionItemMigratesLegacyProgressClocksOnRead(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 15, 10, 45, 0, 0, time.UTC)
	session := testSession(t, now)
	if err := session.AcquireWorkflowLock("workflow-legacy", domain.ProvisionWorkflowType, time.Hour, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	item := toSessionItem(session)
	item.ProgressStartedAt = ""
	item.ProgressLastProgressAt = ""
	item.ProgressCompletedMilestones = nil
	item.ProgressSkippedMilestones, item.ProgressState = nil, ""

	stored, err := fromSessionItem(item)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Progress.StartedAt != session.ActiveWorkflowStartedAt || stored.Progress.LastProgressAt != session.Progress.LastProgressAt || len(stored.Progress.CompletedMilestones) != 0 {
		t.Fatalf("migrated progress = %#v", stored.Progress)
	}
	migrated := toSessionItem(stored)
	if migrated.ProgressStartedAt == "" || migrated.ProgressLastProgressAt == "" || migrated.ProgressUpdatedAt != migrated.ProgressLastProgressAt {
		t.Fatalf("migration-on-write fields = %#v", migrated)
	}
}

func TestSessionItemMigratesLegacyCompletedProgressFactsOnRead(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 15, 10, 50, 0, 0, time.UTC)
	session := testSession(t, now)
	if err := session.AcquireWorkflowLock("workflow-legacy", domain.ProvisionWorkflowType, time.Hour, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	item := toSessionItem(session)
	item.ActiveWorkflowID, item.ActiveWorkflowType = "", ""
	item.ActiveWorkflowStartedAt, item.ActiveWorkflowLeaseExpiresAt = "", ""
	item.ProgressMilestone = string(domain.ProgressCompleted)
	item.ProgressCompletedMilestones = nil
	item.ProgressSkippedMilestones, item.ProgressState = nil, ""

	stored, err := fromSessionItem(item)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := domain.MilestonesForWorkflow(domain.ProvisionWorkflowType)
	if stored.Progress.State != domain.ProgressCompletedState || !reflect.DeepEqual(stored.Progress.CompletedMilestones, want) {
		t.Fatalf("migrated completed progress = %#v; want state %s and completed %#v", stored.Progress, domain.ProgressCompletedState, want)
	}
}

func TestSessionItemMigratesLegacyBootstrapFailureToVisibleActionRequiredProgress(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 15, 10, 55, 0, 0, time.UTC)
	session := testSession(t, now)
	if err := session.AcquireWorkflowLock("workflow-legacy", domain.BootstrapWorkflowType, time.Hour, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	item := toSessionItem(session)
	item.ActiveWorkflowID, item.ActiveWorkflowType = "", ""
	item.ActiveWorkflowStartedAt, item.ActiveWorkflowLeaseExpiresAt = "", ""
	item.ProgressMilestone = string(domain.ProgressFailed)
	item.ProgressCompletedMilestones = nil
	item.ProgressSkippedMilestones, item.ProgressState = nil, ""

	stored, err := fromSessionItem(item)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Progress.Milestone != domain.ProgressAccepted || stored.Progress.State != domain.ProgressActionRequired || len(stored.Progress.CompletedMilestones) != 0 {
		t.Fatalf("migrated failed progress = %#v", stored.Progress)
	}
}

func TestSessionItemWithoutProgressRemainsReadable(t *testing.T) {
	t.Parallel()
	session := testSession(t, time.Date(2026, 8, 15, 11, 0, 0, 0, time.UTC))
	item := toSessionItem(session)
	item.ProgressWorkflowID, item.ProgressWorkflowType, item.ProgressMilestone = "", "", ""
	item.ProgressCompletedMilestones = nil
	item.ProgressStartedAt, item.ProgressLastProgressAt, item.ProgressUpdatedAt = "", "", ""

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
