package hostaccess

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type ExchangeClient interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type ManifestExchange struct {
	Client ExchangeClient
	Bucket string
}

// NewManifestExchange disables SDK HTTP/body diagnostics because exchange bytes
// contain bearer URLs. Injected clients must obey the same logging requirement.
func NewManifestExchange(config aws.Config, bucket string) ManifestExchange {
	config.ClientLogMode = 0
	return ManifestExchange{Client: s3.NewFromConfig(config), Bucket: bucket}
}

func (exchange ManifestExchange) PutHostManifest(ctx context.Context, scope domain.HostAccessScope, payload []byte) (string, error) {
	if exchange.Client == nil || exchange.Bucket == "" || scope.Validate() != nil || len(payload) < 1 || len(payload) > domain.MaxHostAccessManifestBytes {
		return "", fmt.Errorf("host manifest exchange input invalid")
	}
	digest := sha256.Sum256(payload)
	output, err := exchange.Client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(exchange.Bucket), Key: aws.String(scope.StagingKey("manifest")), Body: bytes.NewReader(payload), ContentLength: aws.Int64(int64(len(payload))), ContentType: aws.String("application/json"), ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(digest[:])), ServerSideEncryption: types.ServerSideEncryptionAes256, Tagging: aws.String(HostAccessRetentionTag)})
	if err != nil || output == nil || aws.ToString(output.VersionId) == "" || aws.ToString(output.VersionId) == "null" {
		return "", fmt.Errorf("versioned host manifest exchange write failed")
	}
	return aws.ToString(output.VersionId), nil
}

func (exchange ManifestExchange) GetHostManifest(ctx context.Context, scope domain.HostAccessScope, version string) ([]byte, error) {
	if exchange.Client == nil || exchange.Bucket == "" || scope.Validate() != nil || version == "" || version == "null" {
		return nil, fmt.Errorf("host manifest exchange reference invalid")
	}
	output, err := exchange.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(exchange.Bucket), Key: aws.String(scope.StagingKey("manifest")), VersionId: aws.String(version)})
	if output != nil && output.Body != nil {
		defer output.Body.Close()
	}
	if err != nil || output == nil || output.Body == nil {
		return nil, fmt.Errorf("host manifest exchange read failed")
	}
	if aws.ToString(output.VersionId) != version || (output.ServerSideEncryption != types.ServerSideEncryptionAes256 && output.ServerSideEncryption != types.ServerSideEncryptionAwsKms) {
		return nil, fmt.Errorf("host manifest exchange identity invalid")
	}
	payload, err := io.ReadAll(io.LimitReader(output.Body, domain.MaxHostAccessManifestBytes+1))
	if err != nil || len(payload) < 1 || len(payload) > domain.MaxHostAccessManifestBytes {
		return nil, fmt.Errorf("host manifest exchange size invalid")
	}
	return payload, nil
}

var _ ports.HostManifestExchange = ManifestExchange{}
