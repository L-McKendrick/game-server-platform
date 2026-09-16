package steamexchange

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

const (
	maximumCacheBytes = 1024 * 1024
	minimumExpiry     = 15 * time.Minute
	maximumExpiry     = 12 * time.Hour
)

var safeIdentity = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type DynamoAPI interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

type S3API interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObjects(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

type SecretAPI interface {
	GetSecretValue(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
	PutSecretValue(context.Context, *secretsmanager.PutSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error)
}

type PresignAPI interface {
	PresignGetObject(context.Context, *s3.GetObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
	PresignPutObject(context.Context, *s3.PutObjectInput, ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

type Clock interface{ Now() time.Time }

type Generator interface{ NewID() (string, error) }

type Broker struct {
	dynamo                  DynamoAPI
	objects                 S3API
	presign                 PresignAPI
	secrets                 SecretAPI
	clock                   Clock
	ids                     Generator
	table, bucket, secretID string
}

type record struct {
	SchemaVersion      int    `dynamodbav:"schema_version"`
	PK                 string `dynamodbav:"pk"`
	SK                 string `dynamodbav:"sk"`
	ExchangeID         string `dynamodbav:"exchange_id"`
	SessionID          string `dynamodbav:"session_id"`
	WorkflowID         string `dynamodbav:"workflow_id"`
	WorkflowType       string `dynamodbav:"workflow_type"`
	InstanceID         string `dynamodbav:"instance_id"`
	Purpose            string `dynamodbav:"purpose"`
	SourceVersionID    string `dynamodbav:"source_version_id"`
	SourceSHA256       string `dynamodbav:"source_sha256"`
	SourceConfigSHA256 string `dynamodbav:"source_config_sha256"`
	SourceUsername     string `dynamodbav:"source_username"`
	SourceEnrolledAt   string `dynamodbav:"source_enrolled_at"`
	InputKey           string `dynamodbav:"input_key"`
	OutputKey          string `dynamodbav:"output_key"`
	State              string `dynamodbav:"state"`
	CreatedAt          int64  `dynamodbav:"created_at"`
	ExpiresAt          int64  `dynamodbav:"expires_at"`
	ExpiresAtEpoch     int64  `dynamodbav:"expires_at_epoch"`
}

type cachePayload struct {
	SchemaVersion   int    `json:"schema_version"`
	CacheFormat     string `json:"cache_format"`
	Status          string `json:"status"`
	Username        string `json:"username"`
	ConfigVDFBase64 string `json:"config_vdf_base64"`
	ConfigSHA256    string `json:"config_sha256"`
	EnrolledAt      string `json:"enrolled_at"`
	SourceVersionID string `json:"source_version_id,omitempty"`
}

func New(dynamoClient DynamoAPI, objectClient S3API, presignClient PresignAPI, secretClient SecretAPI, clock Clock, ids Generator, table, bucket, secretID string) (*Broker, error) {
	table, bucket, secretID = strings.TrimSpace(table), strings.TrimSpace(bucket), strings.TrimSpace(secretID)
	if dynamoClient == nil || objectClient == nil || presignClient == nil || secretClient == nil || clock == nil || ids == nil || table == "" || bucket == "" || secretID == "" {
		return nil, fmt.Errorf("Steam exchange broker configuration is incomplete")
	}
	return &Broker{dynamo: dynamoClient, objects: objectClient, presign: presignClient, secrets: secretClient, clock: clock, ids: ids, table: table, bucket: bucket, secretID: secretID}, nil
}

func NewAWS(config aws.Config, clock Clock, table, bucket, secretID string) (*Broker, error) {
	// Exchange capabilities are consumed by curl, not an AWS SDK. Do not let the
	// SDK's optional response-checksum validation add a required signed header.
	config.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	objects := s3.NewFromConfig(config)
	return New(dynamodb.NewFromConfig(config), objects, s3.NewPresignClient(objects), secretsmanager.NewFromConfig(config), clock, RandomGenerator{}, table, bucket, secretID)
}

func (b *Broker) Prepare(ctx context.Context, session domain.Session, purpose string, requested time.Duration) (string, string, string, time.Time, error) {
	purpose = strings.TrimSpace(purpose)
	if !safeIdentity.MatchString(session.ID) || !safeIdentity.MatchString(session.ActiveWorkflowID) || !safeIdentity.MatchString(purpose) || strings.TrimSpace(session.Infrastructure.InstanceID) == "" || session.ActiveWorkflowType == "" {
		return "", "", "", time.Time{}, fmt.Errorf("Steam exchange identity is invalid")
	}
	now := b.clock.Now().UTC()
	duration := requested
	if duration < minimumExpiry {
		duration = minimumExpiry
	}
	if duration > maximumExpiry {
		duration = maximumExpiry
	}
	expires := now.Add(duration)
	pk := "STEAM_EXCHANGE#" + session.ActiveWorkflowID + "#" + purpose
	if existing, found, err := b.load(ctx, pk); err != nil {
		return "", "", "", time.Time{}, err
	} else if found {
		switch {
		case existing.State == "PREPARED" && now.Unix() >= existing.ExpiresAt:
			if err := b.finish(ctx, existing, "EXPIRED"); err != nil {
				return "", "", "", time.Time{}, err
			}
		case existing.State == "PREPARED":
			if err := existing.matches(session, purpose, now); err != nil {
				return "", "", "", time.Time{}, err
			}
			if err := b.restoreInput(ctx, existing); err != nil {
				return "", "", "", time.Time{}, err
			}
			return b.urls(ctx, existing, time.Unix(existing.ExpiresAt, 0).Sub(now))
		case existing.State == "FAILED", existing.State == "EXPIRED", existing.State == "PROMOTED":
			if err := existing.matchesIdentity(session, purpose); err != nil {
				return "", "", "", time.Time{}, err
			}
			if err := b.finish(ctx, existing, existing.State); err != nil {
				return "", "", "", time.Time{}, err
			}
		case existing.State == "REAUTH_REQUIRED":
			return "", "", "", time.Time{}, fmt.Errorf("Steam authorization requires operator re-enrollment")
		default:
			return "", "", "", time.Time{}, fmt.Errorf("Steam exchange has invalid state %q", existing.State)
		}
	}

	exchangeID, err := b.ids.NewID()
	if err != nil || !safeIdentity.MatchString(exchangeID) {
		return "", "", "", time.Time{}, fmt.Errorf("generate Steam exchange ID")
	}
	owner := exchangeLeaseOwner(session.ActiveWorkflowID, purpose, exchangeID)
	_, err = b.dynamo.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(b.table), Key: key("STEAM_AUTH#CACHE", "STATE"),
		UpdateExpression:         aws.String("SET lease_owner = :owner, lease_expires_at = :expires"),
		ConditionExpression:      aws.String("(attribute_not_exists(#status) OR #status <> :reauth) AND (attribute_not_exists(lease_owner) OR lease_expires_at < :now OR lease_owner = :owner)"),
		ExpressionAttributeNames: map[string]string{"#status": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":owner": &types.AttributeValueMemberS{Value: owner}, ":expires": &types.AttributeValueMemberN{Value: fmt.Sprint(expires.Unix())}, ":now": &types.AttributeValueMemberN{Value: fmt.Sprint(now.Unix())}, ":reauth": &types.AttributeValueMemberS{Value: "REAUTH_REQUIRED"},
		},
	})
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("acquire Steam authorization lease: %w", err)
	}
	payload, sourcePayload, versionID, err := b.readSourcePayload(ctx, "")
	if err != nil {
		return "", "", "", time.Time{}, errors.Join(err, b.releaseLease(ctx, owner))
	}
	digest := sha256.Sum256(payload)
	base := "platform/steam-exchanges/" + exchangeID + "/"
	rec := record{SchemaVersion: 1, PK: pk, SK: "STATE", ExchangeID: exchangeID, SessionID: session.ID, WorkflowID: session.ActiveWorkflowID, WorkflowType: string(session.ActiveWorkflowType), InstanceID: session.Infrastructure.InstanceID, Purpose: purpose, SourceVersionID: versionID, SourceSHA256: hex.EncodeToString(digest[:]), SourceConfigSHA256: sourcePayload.ConfigSHA256, SourceUsername: sourcePayload.Username, SourceEnrolledAt: sourcePayload.EnrolledAt, InputKey: base + "input.json", OutputKey: base + "output.json", State: "PREPARED", CreatedAt: now.Unix(), ExpiresAt: expires.Unix(), ExpiresAtEpoch: expires.Add(24 * time.Hour).Unix()}
	item, _ := attributevalue.MarshalMap(rec)
	if _, err = b.dynamo.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(b.table), Item: item, ConditionExpression: aws.String("attribute_not_exists(pk) OR #state IN (:failed, :expired, :promoted, :reauth)"), ExpressionAttributeNames: map[string]string{"#state": "state"}, ExpressionAttributeValues: map[string]types.AttributeValue{":failed": &types.AttributeValueMemberS{Value: "FAILED"}, ":expired": &types.AttributeValueMemberS{Value: "EXPIRED"}, ":promoted": &types.AttributeValueMemberS{Value: "PROMOTED"}, ":reauth": &types.AttributeValueMemberS{Value: "REAUTH_REQUIRED"}}}); err != nil {
		if existing, found, loadErr := b.load(ctx, pk); loadErr == nil && found && existing.matches(session, purpose, now) == nil {
			if restoreErr := b.restoreInput(ctx, existing); restoreErr != nil {
				return "", "", "", time.Time{}, restoreErr
			}
			return b.urls(ctx, existing, time.Unix(existing.ExpiresAt, 0).Sub(now))
		}
		return "", "", "", time.Time{}, fmt.Errorf("record Steam exchange: %w", err)
	}
	if _, err = b.objects.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(rec.InputKey), Body: bytes.NewReader(payload), ContentType: aws.String("application/json"), ServerSideEncryption: "AES256"}); err != nil {
		cleanupErr := b.finish(ctx, rec, "FAILED")
		return "", "", "", time.Time{}, errors.Join(fmt.Errorf("write Steam exchange input: %w", err), cleanupErr)
	}
	return b.urls(ctx, rec, duration)
}

func (b *Broker) readSourcePayload(ctx context.Context, expectedVersion string) ([]byte, cachePayload, string, error) {
	secret, err := b.secrets.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(b.secretID), VersionStage: aws.String("AWSCURRENT")})
	if err != nil {
		return nil, cachePayload{}, "", fmt.Errorf("read Steam authorization cache: %w", err)
	}
	payload := []byte(strings.TrimSpace(aws.ToString(secret.SecretString)))
	if err := validatePayload(payload, ""); err != nil {
		return nil, cachePayload{}, "", fmt.Errorf("validate Steam authorization cache: %w", err)
	}
	var source cachePayload
	_ = json.Unmarshal(payload, &source)
	versionID := strings.TrimSpace(aws.ToString(secret.VersionId))
	if versionID == "" || (expectedVersion != "" && versionID != expectedVersion) {
		return nil, cachePayload{}, "", fmt.Errorf("Steam authorization cache changed during exchange")
	}
	var exchangePayload map[string]any
	if err := json.Unmarshal(payload, &exchangePayload); err != nil {
		return nil, cachePayload{}, "", fmt.Errorf("decode Steam authorization cache")
	}
	exchangePayload["source_version_id"] = versionID
	payload, err = json.Marshal(exchangePayload)
	if err != nil {
		return nil, cachePayload{}, "", fmt.Errorf("encode Steam authorization exchange")
	}
	return payload, source, versionID, nil
}

func (b *Broker) restoreInput(ctx context.Context, rec record) error {
	payload, source, _, err := b.readSourcePayload(ctx, rec.SourceVersionID)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	if hex.EncodeToString(digest[:]) != rec.SourceSHA256 || source.ConfigSHA256 != rec.SourceConfigSHA256 || source.Username != rec.SourceUsername || source.EnrolledAt != rec.SourceEnrolledAt {
		return fmt.Errorf("Steam authorization source does not match the recorded exchange")
	}
	if _, err := b.objects.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(rec.InputKey), Body: bytes.NewReader(payload), ContentType: aws.String("application/json"), ServerSideEncryption: "AES256"}); err != nil {
		return fmt.Errorf("restore Steam exchange input: %w", err)
	}
	return nil
}

func (b *Broker) Complete(ctx context.Context, reference, outcome string) error {
	parts := strings.Split(reference, ":")
	if len(parts) != 3 || !safeIdentity.MatchString(parts[0]) || !safeIdentity.MatchString(parts[1]) || !safeIdentity.MatchString(parts[2]) {
		return fmt.Errorf("Steam exchange reference is invalid")
	}
	rec, found, err := b.load(ctx, "STEAM_EXCHANGE#"+parts[0]+"#"+parts[1])
	if err != nil || !found {
		if err != nil {
			return err
		}
		return fmt.Errorf("Steam exchange was not found")
	}
	if rec.ExchangeID != parts[2] {
		return fmt.Errorf("Steam exchange reference mismatch")
	}
	if rec.State == "PROMOTED" {
		return b.finish(ctx, rec, "PROMOTED")
	}
	if rec.State == "REAUTH_REQUIRED" {
		if err := b.finish(ctx, rec, "REAUTH_REQUIRED"); err != nil {
			return err
		}
		if outcome == "reauth_required" {
			return nil
		}
		return fmt.Errorf("Steam exchange is terminal in state %s", rec.State)
	}
	if rec.State == "FAILED" {
		if err := b.finish(ctx, rec, "FAILED"); err != nil {
			return err
		}
		if outcome != "succeeded" {
			return nil
		}
		return fmt.Errorf("Steam exchange is terminal in state %s", rec.State)
	}
	if rec.State == "EXPIRED" {
		return b.finish(ctx, rec, "EXPIRED")
	}
	if rec.State != "PREPARED" {
		return fmt.Errorf("Steam exchange is terminal in state %s", rec.State)
	}
	if b.clock.Now().UTC().Unix() >= rec.ExpiresAt {
		return b.finish(ctx, rec, "EXPIRED")
	}
	if outcome == "reauth_required" {
		owner := rec.leaseOwner()
		_, stateErr := b.dynamo.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(b.table), Key: key("STEAM_AUTH#CACHE", "STATE"), UpdateExpression: aws.String("SET #status = :status, last_error_code = :code, updated_at = :now REMOVE lease_owner, lease_expires_at"), ConditionExpression: aws.String("lease_owner = :owner OR (#status = :status AND attribute_not_exists(lease_owner))"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":status": &types.AttributeValueMemberS{Value: "REAUTH_REQUIRED"}, ":code": &types.AttributeValueMemberS{Value: "ERR_STEAM_REAUTH_REQUIRED"}, ":now": &types.AttributeValueMemberS{Value: b.clock.Now().UTC().Format(time.RFC3339)}, ":owner": &types.AttributeValueMemberS{Value: owner}}})
		if stateErr != nil {
			return fmt.Errorf("record Steam reauthorization requirement: %w", stateErr)
		}
		return b.finishWithoutRelease(ctx, rec, "REAUTH_REQUIRED")
	}
	if outcome != "succeeded" {
		return b.finish(ctx, rec, "FAILED")
	}
	result, err := b.objects.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(rec.OutputKey)})
	if err != nil {
		return fmt.Errorf("read Steam authorization exchange output: %w", err)
	}
	defer result.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(result.Body, maximumCacheBytes+1))
	if err != nil {
		return fmt.Errorf("read Steam authorization exchange output: %w", err)
	}
	if len(payload) > maximumCacheBytes {
		return b.finish(ctx, rec, "FAILED")
	}
	if err := validatePayload(payload, rec.SourceVersionID); err != nil {
		return errors.Join(err, b.finish(ctx, rec, "FAILED"))
	}
	var validated cachePayload
	_ = json.Unmarshal(payload, &validated)
	if validated.Username != rec.SourceUsername || validated.EnrolledAt != rec.SourceEnrolledAt || rec.SourceUsername == "" || rec.SourceEnrolledAt == "" {
		return errors.Join(fmt.Errorf("Steam authorization identity changed during exchange"), b.finish(ctx, rec, "FAILED"))
	}
	current, err := b.secrets.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(b.secretID), VersionStage: aws.String("AWSCURRENT")})
	if err != nil {
		return fmt.Errorf("read current Steam authorization cache: %w", err)
	}
	currentVersion := strings.TrimSpace(aws.ToString(current.VersionId))
	promotedVersion := rec.SourceVersionID
	if currentVersion == rec.ExchangeID {
		if strings.TrimSpace(aws.ToString(current.SecretString)) != strings.TrimSpace(string(payload)) {
			return errors.Join(fmt.Errorf("Steam authorization promotion replay did not match the current cache"), b.finish(ctx, rec, "FAILED"))
		}
		promotedVersion = currentVersion
	} else if currentVersion != rec.SourceVersionID {
		return errors.Join(fmt.Errorf("Steam authorization cache changed during exchange"), b.finish(ctx, rec, "FAILED"))
	} else if validated.ConfigSHA256 != rec.SourceConfigSHA256 {
		promoted, putErr := b.secrets.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{SecretId: aws.String(b.secretID), ClientRequestToken: aws.String(rec.ExchangeID), SecretString: aws.String(string(payload))})
		if putErr != nil {
			return fmt.Errorf("promote Steam authorization cache: %w", putErr)
		}
		promotedVersion = aws.ToString(promoted.VersionId)
		if strings.TrimSpace(promotedVersion) == "" {
			promotedVersion = rec.ExchangeID
		}
	}
	owner := rec.leaseOwner()
	_, err = b.dynamo.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(b.table), Key: key("STEAM_AUTH#CACHE", "STATE"), UpdateExpression: aws.String("SET #status = :status, current_version_id = :version, config_sha256 = :sha, updated_at = :now REMOVE last_error_code"), ConditionExpression: aws.String("lease_owner = :owner"), ExpressionAttributeNames: map[string]string{"#status": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":status": &types.AttributeValueMemberS{Value: "ACTIVE"}, ":version": &types.AttributeValueMemberS{Value: promotedVersion}, ":sha": &types.AttributeValueMemberS{Value: validated.ConfigSHA256}, ":now": &types.AttributeValueMemberS{Value: b.clock.Now().UTC().Format(time.RFC3339)}, ":owner": &types.AttributeValueMemberS{Value: owner}}})
	if err != nil {
		return fmt.Errorf("record Steam authorization promotion: %w", err)
	}
	return b.finish(ctx, rec, "PROMOTED")
}

func (b *Broker) urls(ctx context.Context, rec record, duration time.Duration) (string, string, string, time.Time, error) {
	if duration <= 0 {
		return "", "", "", time.Time{}, fmt.Errorf("Steam exchange expired")
	}
	if duration > maximumExpiry {
		duration = maximumExpiry
	}
	get, err := b.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(rec.InputKey)}, func(o *s3.PresignOptions) { o.Expires = duration })
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("presign Steam exchange input: %w", err)
	}
	put, err := b.presign.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(b.bucket), Key: aws.String(rec.OutputKey), ContentType: aws.String("application/json")}, func(o *s3.PresignOptions) { o.Expires = duration })
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("presign Steam exchange output: %w", err)
	}
	return rec.WorkflowID + ":" + rec.Purpose + ":" + rec.ExchangeID, get.URL, put.URL, time.Unix(rec.ExpiresAt, 0).UTC(), nil
}

func (b *Broker) load(ctx context.Context, pk string) (record, bool, error) {
	output, err := b.dynamo.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(b.table), Key: key(pk, "STATE"), ConsistentRead: aws.Bool(true)})
	if err != nil {
		return record{}, false, err
	}
	if len(output.Item) == 0 {
		return record{}, false, nil
	}
	var rec record
	if err := attributevalue.UnmarshalMap(output.Item, &rec); err != nil {
		return record{}, false, err
	}
	if rec.SchemaVersion != 1 {
		return record{}, false, fmt.Errorf("unsupported Steam exchange schema version %d", rec.SchemaVersion)
	}
	return rec, true, nil
}

func (r record) matches(session domain.Session, purpose string, now time.Time) error {
	if r.State != "PREPARED" || now.Unix() >= r.ExpiresAt {
		return fmt.Errorf("Steam exchange does not match the active workflow")
	}
	return r.matchesIdentity(session, purpose)
}

func (r record) matchesIdentity(session domain.Session, purpose string) error {
	if r.SessionID != session.ID || r.WorkflowID != session.ActiveWorkflowID || r.WorkflowType != string(session.ActiveWorkflowType) || r.InstanceID != session.Infrastructure.InstanceID || r.Purpose != purpose {
		return fmt.Errorf("Steam exchange does not match the active workflow")
	}
	return nil
}

func (r record) leaseOwner() string {
	return exchangeLeaseOwner(r.WorkflowID, r.Purpose, r.ExchangeID)
}

func exchangeLeaseOwner(workflowID, purpose, exchangeID string) string {
	return workflowID + ":" + purpose + ":" + exchangeID
}

func (b *Broker) finish(ctx context.Context, rec record, state string) error {
	cleanupErr := b.finishWithoutRelease(ctx, rec, state)
	if cleanupErr != nil {
		return cleanupErr
	}
	owner := rec.leaseOwner()
	return errors.Join(cleanupErr, b.releaseLease(ctx, owner))
}

func (b *Broker) releaseLease(ctx context.Context, owner string) error {
	_, err := b.dynamo.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(b.table), Key: key("STEAM_AUTH#CACHE", "STATE"), UpdateExpression: aws.String("REMOVE lease_owner, lease_expires_at"), ConditionExpression: aws.String("attribute_not_exists(lease_owner) OR lease_owner = :owner"), ExpressionAttributeValues: map[string]types.AttributeValue{":owner": &types.AttributeValueMemberS{Value: owner}}})
	if conditionalFailure(err) {
		return nil
	}
	return err
}

func (b *Broker) finishWithoutRelease(ctx context.Context, rec record, state string) error {
	deleted, deleteErr := b.objects.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String(b.bucket), Delete: &s3types.Delete{Objects: []s3types.ObjectIdentifier{{Key: aws.String(rec.InputKey)}, {Key: aws.String(rec.OutputKey)}}}})
	if deleteErr == nil && deleted != nil && len(deleted.Errors) > 0 {
		deleteErr = fmt.Errorf("delete Steam authorization exchange objects: %d object errors (first code %q)", len(deleted.Errors), aws.ToString(deleted.Errors[0].Code))
	}
	_, updateErr := b.dynamo.UpdateItem(ctx, &dynamodb.UpdateItemInput{TableName: aws.String(b.table), Key: key(rec.PK, rec.SK), UpdateExpression: aws.String("SET #state = :state"), ConditionExpression: aws.String("exchange_id = :id AND (#state = :prepared OR #state = :state)"), ExpressionAttributeNames: map[string]string{"#state": "state"}, ExpressionAttributeValues: map[string]types.AttributeValue{":state": &types.AttributeValueMemberS{Value: state}, ":id": &types.AttributeValueMemberS{Value: rec.ExchangeID}, ":prepared": &types.AttributeValueMemberS{Value: "PREPARED"}}})
	if conditionalFailure(updateErr) {
		updateErr = nil
	}
	return errors.Join(deleteErr, updateErr)
}

func conditionalFailure(err error) bool {
	var conditional *types.ConditionalCheckFailedException
	return errors.As(err, &conditional)
}

func key(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}}
}

func validatePayload(raw []byte, sourceVersion string) error {
	if len(raw) == 0 || len(raw) > maximumCacheBytes {
		return fmt.Errorf("Steam authorization payload size is invalid")
	}
	var payload cachePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("Steam authorization payload is malformed")
	}
	_, enrolledErr := time.Parse(time.RFC3339, payload.EnrolledAt)
	if payload.SchemaVersion != 1 || payload.CacheFormat != "steamcmd-config-vdf" || payload.Status != "ACTIVE" || payload.Username == "" || len(payload.Username) > 64 || strings.ContainsAny(payload.Username, " \t\r\n\"\\") || enrolledErr != nil || !strings.HasSuffix(payload.EnrolledAt, "Z") || (sourceVersion != "" && payload.SourceVersionID != sourceVersion) {
		return fmt.Errorf("Steam authorization payload fields are invalid")
	}
	decoded, err := base64.StdEncoding.DecodeString(payload.ConfigVDFBase64)
	if err != nil || len(decoded) == 0 || len(decoded) > 524288 {
		return fmt.Errorf("Steam authorization config encoding is invalid")
	}
	digest := sha256.Sum256(decoded)
	if hex.EncodeToString(digest[:]) != payload.ConfigSHA256 {
		return fmt.Errorf("Steam authorization config checksum is invalid")
	}
	return nil
}

type RandomGenerator struct{}

func (RandomGenerator) NewID() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
