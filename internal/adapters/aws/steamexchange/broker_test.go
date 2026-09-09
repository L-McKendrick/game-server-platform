package steamexchange

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time { return f.now }

type fakeIDs struct{ id string }

func (f fakeIDs) NewID() (string, error) { return f.id, nil }

type fakeDynamo struct {
	items      map[string]map[string]ddbtypes.AttributeValue
	leaseOwner string
}

func (f *fakeDynamo) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: f.items[attributeString(in.Key["pk"])]}, nil
}
func (f *fakeDynamo) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	pk := attributeString(in.Item["pk"])
	if _, exists := f.items[pk]; exists {
		return nil, &ddbtypes.ConditionalCheckFailedException{}
	}
	f.items[pk] = in.Item
	return &dynamodb.PutItemOutput{}, nil
}
func (f *fakeDynamo) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	pk := attributeString(in.Key["pk"])
	owner := attributeString(in.ExpressionAttributeValues[":owner"])
	if pk == "STEAM_AUTH#CACHE" {
		if strings.HasPrefix(aws.ToString(in.UpdateExpression), "SET lease_owner") {
			if f.leaseOwner != "" && f.leaseOwner != owner {
				return nil, &ddbtypes.ConditionalCheckFailedException{}
			}
			f.leaseOwner = owner
		} else if owner != "" && f.leaseOwner != owner {
			return nil, &ddbtypes.ConditionalCheckFailedException{}
		} else if strings.Contains(aws.ToString(in.UpdateExpression), "REMOVE lease_owner") {
			f.leaseOwner = ""
		}
		return &dynamodb.UpdateItemOutput{}, nil
	}
	item := f.items[pk]
	if item == nil {
		return nil, &ddbtypes.ConditionalCheckFailedException{}
	}
	item["state"] = in.ExpressionAttributeValues[":state"]
	return &dynamodb.UpdateItemOutput{}, nil
}

type fakeObjects struct {
	objects map[string][]byte
	deleted []string
}

func (f *fakeObjects) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	body, _ := io.ReadAll(in.Body)
	f.objects[aws.ToString(in.Key)] = body
	return &s3.PutObjectOutput{}, nil
}
func (f *fakeObjects) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	body, ok := f.objects[aws.ToString(in.Key)]
	if !ok {
		return nil, errors.New("missing")
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(body))}, nil
}
func (f *fakeObjects) DeleteObjects(_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	for _, object := range in.Delete.Objects {
		key := aws.ToString(object.Key)
		f.deleted = append(f.deleted, key)
		delete(f.objects, key)
	}
	return &s3.DeleteObjectsOutput{}, nil
}

type fakePresign struct{}

func (fakePresign) PresignGetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	return &v4.PresignedHTTPRequest{URL: "https://example.com/get/" + aws.ToString(in.Key)}, nil
}
func (fakePresign) PresignPutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	return &v4.PresignedHTTPRequest{URL: "https://example.com/put/" + aws.ToString(in.Key)}, nil
}

type fakeSecrets struct{ payload, version, promoted string }

func (f *fakeSecrets) GetSecretValue(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(f.payload), VersionId: aws.String(f.version)}, nil
}
func (f *fakeSecrets) PutSecretValue(_ context.Context, in *secretsmanager.PutSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error) {
	f.promoted = aws.ToString(in.SecretString)
	f.version = aws.ToString(in.ClientRequestToken)
	return &secretsmanager.PutSecretValueOutput{VersionId: in.ClientRequestToken}, nil
}

func TestBrokerPrepareReplayAndPromote(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	dynamoClient := &fakeDynamo{items: map[string]map[string]ddbtypes.AttributeValue{}}
	objects := &fakeObjects{objects: map[string][]byte{}}
	secrets := &fakeSecrets{payload: validPayload(""), version: "version-1"}
	broker, err := New(dynamoClient, objects, fakePresign{}, secrets, clock, fakeIDs{id: strings.Repeat("a", 64)}, "metadata", "assets", "steam-secret")
	if err != nil {
		t.Fatal(err)
	}
	session := exchangeSession("workflow-1")
	reference, getURL, putURL, expires, err := broker.Prepare(context.Background(), session, "bootstrap", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if reference != "workflow-1:bootstrap:"+strings.Repeat("a", 64) || !strings.HasPrefix(getURL, "https://") || !strings.HasPrefix(putURL, "https://") || !expires.Equal(now.Add(time.Hour)) {
		t.Fatalf("exchange = %q %q %q %s", reference, getURL, putURL, expires)
	}
	inputKey := "platform/steam-exchanges/" + strings.Repeat("a", 64) + "/input.json"
	if !strings.Contains(string(objects.objects[inputKey]), `"source_version_id":"version-1"`) {
		t.Fatal("exchange input omitted its source version binding")
	}
	replayed, _, _, _, err := broker.Prepare(context.Background(), session, "bootstrap", time.Hour)
	if err != nil || replayed != reference {
		t.Fatalf("replay = %q, %v", replayed, err)
	}
	outputKey := "platform/steam-exchanges/" + strings.Repeat("a", 64) + "/output.json"
	objects.objects[outputKey] = []byte(validPayloadFor("version-1", "updated-config"))
	if err := broker.Complete(context.Background(), reference, "succeeded"); err != nil {
		t.Fatal(err)
	}
	if secrets.promoted == "" || dynamoClient.leaseOwner != "" || len(objects.deleted) != 2 {
		t.Fatalf("promotion cleanup incomplete: promoted=%t lease=%q deleted=%v", secrets.promoted != "", dynamoClient.leaseOwner, objects.deleted)
	}
	if err := broker.Complete(context.Background(), reference, "succeeded"); err != nil {
		t.Fatalf("terminal replay failed: %v", err)
	}
}

func TestBrokerRejectsCrossWorkflowAndMissingOutput(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	dynamoClient := &fakeDynamo{items: map[string]map[string]ddbtypes.AttributeValue{}}
	objects := &fakeObjects{objects: map[string][]byte{}}
	broker, _ := New(dynamoClient, objects, fakePresign{}, &fakeSecrets{payload: validPayload(""), version: "version-1"}, clock, fakeIDs{id: strings.Repeat("b", 64)}, "metadata", "assets", "steam-secret")
	reference, _, _, _, err := broker.Prepare(context.Background(), exchangeSession("workflow-1"), "restore", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := broker.Prepare(context.Background(), exchangeSession("workflow-2"), "restore", time.Hour); err == nil {
		t.Fatal("concurrent workflow acquired the shared cache")
	}
	if err := broker.Complete(context.Background(), "workflow-1:restore:"+strings.Repeat("c", 64), "succeeded"); err == nil {
		t.Fatal("mismatched exchange reference was accepted")
	}
	if err := broker.Complete(context.Background(), reference, "succeeded"); err == nil {
		t.Fatal("missing output was accepted")
	}
}

func TestValidatePayloadRejectsMalformedAndStaleData(t *testing.T) {
	for _, raw := range []string{"", `{}`, validPayload("wrong-version")} {
		if err := validatePayload([]byte(raw), "version-1"); err == nil {
			t.Fatalf("payload %q was accepted", raw)
		}
	}
}

func exchangeSession(workflowID string) domain.Session {
	return domain.Session{ID: "session-1", ActiveWorkflowID: workflowID, ActiveWorkflowType: domain.BootstrapWorkflowType, Infrastructure: domain.Infrastructure{InstanceID: "i-1"}}
}
func validPayload(source string) string {
	return validPayloadFor(source, "config")
}
func validPayloadFor(source, configValue string) string {
	config := []byte(configValue)
	digest := sha256.Sum256(config)
	payload := cachePayload{SchemaVersion: 1, CacheFormat: "steamcmd-config-vdf", Status: "ACTIVE", Username: "user", ConfigVDFBase64: base64.StdEncoding.EncodeToString(config), ConfigSHA256: hex.EncodeToString(digest[:]), EnrolledAt: "2026-09-08T00:00:00Z", SourceVersionID: source}
	raw, _ := json.Marshal(payload)
	return string(raw)
}
func attributeString(value ddbtypes.AttributeValue) string {
	if item, ok := value.(*ddbtypes.AttributeValueMemberS); ok {
		return item.Value
	}
	return ""
}
