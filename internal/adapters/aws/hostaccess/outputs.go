package hostaccess

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type OutputClient interface {
	ReadClient
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

type WorkshopStore struct {
	Client OutputClient
	Bucket string
}

// ReadHostOutput buffers only small control documents or bounded preset HTML.
// Mission verification uses ReadInspector's streaming path instead.
func (store WorkshopStore) ReadHostOutput(ctx context.Context, key, version, contentType string, limit int64) ([]byte, domain.HostOutputVersion, error) {
	if store.Client == nil || store.Bucket == "" || key == "" || version == "null" || limit < 1 || limit > 10*1024*1024 {
		return nil, domain.HostOutputVersion{}, fmt.Errorf("host output read input invalid")
	}
	input := &s3.GetObjectInput{Bucket: aws.String(store.Bucket), Key: aws.String(key)}
	if version != "" {
		input.VersionId = aws.String(version)
	}
	output, err := store.Client.GetObject(ctx, input)
	if output != nil && output.Body != nil {
		defer output.Body.Close()
	}
	if err != nil || output == nil || output.Body == nil {
		return nil, domain.HostOutputVersion{}, fmt.Errorf("host output unavailable")
	}
	pinned := aws.ToString(output.VersionId)
	if pinned == "" || pinned == "null" || (version != "" && pinned != version) || output.ContentLength == nil || *output.ContentLength < 1 || *output.ContentLength > limit || (contentType != "" && aws.ToString(output.ContentType) != contentType) {
		return nil, domain.HostOutputVersion{}, fmt.Errorf("host output metadata invalid")
	}
	body, err := io.ReadAll(io.LimitReader(output.Body, limit+1))
	if err != nil || int64(len(body)) != *output.ContentLength || int64(len(body)) > limit {
		return nil, domain.HostOutputVersion{}, fmt.Errorf("host output size invalid")
	}
	digest := sha256.Sum256(body)
	return body, domain.HostOutputVersion{VersionID: pinned, SHA256: base64.StdEncoding.EncodeToString(digest[:]), SizeBytes: int64(len(body))}, nil
}

// PromoteHostOutput streams an already verified exact source version. S3 must
// enforce its checksum, exact length and create-only destination. An existing
// destination is reusable only after independently hashing its bounded bytes.
func (store WorkshopStore) PromoteHostOutput(ctx context.Context, source, destination, contentType string, pin domain.HostOutputVersion) (string, error) {
	digest, decodeErr := base64.StdEncoding.DecodeString(pin.SHA256)
	if store.Client == nil || store.Bucket == "" || source == "" || destination == "" || source == destination || pin.VersionID == "" || pin.VersionID == "null" || decodeErr != nil || len(digest) != 32 || pin.SizeBytes < 1 || pin.SizeBytes > domain.MaximumWorkshopMissionBytes {
		return "", fmt.Errorf("host output promotion input invalid")
	}
	switch contentType {
	case "application/json", "text/tab-separated-values", "application/octet-stream":
	default:
		return "", fmt.Errorf("host output promotion type invalid")
	}
	input := &s3.GetObjectInput{Bucket: aws.String(store.Bucket), Key: aws.String(source), VersionId: aws.String(pin.VersionID)}
	output, err := store.Client.GetObject(ctx, input)
	if output != nil && output.Body != nil {
		defer output.Body.Close()
	}
	if err != nil || output == nil || output.Body == nil {
		return "", fmt.Errorf("host output promotion source unavailable")
	}
	if aws.ToString(output.VersionId) != pin.VersionID || output.ContentLength == nil || *output.ContentLength != pin.SizeBytes || aws.ToString(output.ContentType) != contentType {
		return "", fmt.Errorf("host output promotion source changed")
	}
	return store.putImmutableOutput(ctx, destination, contentType, pin, io.LimitReader(output.Body, pin.SizeBytes))
}

// PublishHostDocument publishes bounded worker-generated canonical bytes.
func (store WorkshopStore) PublishHostDocument(ctx context.Context, key, contentType string, body []byte) (string, error) {
	if store.Client == nil || store.Bucket == "" || key == "" || len(body) < 1 || len(body) > domain.MaxHostResultBytes || (contentType != "application/json" && contentType != "text/tab-separated-values") {
		return "", fmt.Errorf("host canonical document input invalid")
	}
	digest := sha256.Sum256(body)
	pin := domain.HostOutputVersion{SHA256: base64.StdEncoding.EncodeToString(digest[:]), SizeBytes: int64(len(body))}
	return store.putImmutableOutput(ctx, key, contentType, pin, bytes.NewReader(body))
}

func (store WorkshopStore) putImmutableOutput(ctx context.Context, destination, contentType string, pin domain.HostOutputVersion, body io.Reader) (string, error) {
	created, err := store.Client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(store.Bucket), Key: aws.String(destination), Body: body, ContentLength: aws.Int64(pin.SizeBytes), ContentType: aws.String(contentType), ChecksumSHA256: aws.String(pin.SHA256), IfNoneMatch: aws.String("*")})
	if err != nil {
		var apiError smithy.APIError
		if !errors.As(err, &apiError) || apiError.ErrorCode() != "PreconditionFailed" {
			return "", fmt.Errorf("host output immutable publication unavailable")
		}
		inspector := ReadInspector{Client: store.Client, Bucket: store.Bucket, ContentType: contentType}
		checksum, version, size, inspectErr := inspector.InspectHostRead(ctx, destination, "", pin.SizeBytes)
		if inspectErr != nil || checksum != pin.SHA256 || size != pin.SizeBytes {
			return "", domain.ErrConflict
		}
		return version, nil
	}
	if created == nil || aws.ToString(created.VersionId) == "" || aws.ToString(created.VersionId) == "null" {
		return "", fmt.Errorf("host output immutable version missing")
	}
	return aws.ToString(created.VersionId), nil
}

var _ ports.HostWorkshopStore = WorkshopStore{}
