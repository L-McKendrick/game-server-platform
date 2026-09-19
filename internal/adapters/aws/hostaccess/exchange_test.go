package hostaccess_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/adapters/aws/hostaccess"
	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type exchangeClient struct {
	put        *s3.PutObjectInput
	get        *s3.GetObjectInput
	version    string
	payload    string
	encryption types.ServerSideEncryption
	err        error
}

func (client *exchangeClient) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	client.put = input
	return &s3.PutObjectOutput{VersionId: aws.String(client.version)}, client.err
}
func (client *exchangeClient) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	client.get = input
	return &s3.GetObjectOutput{VersionId: aws.String(client.version), ServerSideEncryption: client.encryption, Body: io.NopCloser(strings.NewReader(client.payload))}, client.err
}

func TestExchangeRequiresEncryptedVersionedBoundedObjects(t *testing.T) {
	scope := domain.HostAccessScope{SessionID: "session", GuildID: "guild", OperationID: "operation", AttemptID: "attempt", InstanceID: "i-123", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: time.Now().Add(time.Hour)}
	client := &exchangeClient{version: "version-1", payload: "manifest", encryption: types.ServerSideEncryptionAes256}
	exchange := hostaccess.ManifestExchange{Client: client, Bucket: "assets"}
	version, err := exchange.PutHostManifest(context.Background(), scope, []byte(client.payload))
	if err != nil || version != "version-1" {
		t.Fatalf("version=%s error=%v", version, err)
	}
	if aws.ToString(client.put.Key) != scope.StagingKey("manifest") || client.put.ServerSideEncryption != types.ServerSideEncryptionAes256 || aws.ToString(client.put.ChecksumSHA256) == "" || aws.ToInt64(client.put.ContentLength) != 8 {
		t.Fatal("missing exact encrypted upload constraints")
	}
	if aws.ToString(client.put.Tagging) != hostaccess.HostAccessRetentionTag {
		t.Fatal("manifest lacks staging retention tag")
	}
	payload, err := exchange.GetHostManifest(context.Background(), scope, version)
	if err != nil || string(payload) != "manifest" {
		t.Fatalf("payload=%s error=%v", payload, err)
	}
	if aws.ToString(client.get.VersionId) != version || aws.ToString(client.get.Key) != scope.StagingKey("manifest") {
		t.Fatal("exchange read is not version pinned")
	}
	for _, invalid := range []string{"", "null"} {
		client.version = invalid
		if _, err := exchange.PutHostManifest(context.Background(), scope, []byte("manifest")); err == nil {
			t.Fatal("accepted unversioned exchange")
		}
	}
	client.version = "wrong-version"
	if _, err := exchange.GetHostManifest(context.Background(), scope, version); err == nil {
		t.Fatal("accepted mismatched version")
	}
	client.version = version
	client.encryption = ""
	if _, err := exchange.GetHostManifest(context.Background(), scope, version); err == nil {
		t.Fatal("accepted unencrypted exchange")
	}
	client.encryption = types.ServerSideEncryptionAes256
	client.payload = strings.Repeat("x", domain.MaxHostAccessManifestBytes+1)
	if _, err := exchange.GetHostManifest(context.Background(), scope, version); err == nil {
		t.Fatal("accepted oversized exchange")
	}
	client.err = errors.New("https://secret.example.test/?signature=secret")
	if _, err := exchange.GetHostManifest(context.Background(), scope, version); err == nil || strings.Contains(err.Error(), "signature") {
		t.Fatal("exchange error leaked capability")
	}
}
