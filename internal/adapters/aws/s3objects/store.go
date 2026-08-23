package s3objects

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type API interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

type Store struct {
	client API
	bucket string
}

var _ ports.ObjectStore = (*Store)(nil)
var _ ports.ObjectReader = (*Store)(nil)
var _ ports.PrivateObjectStore = (*Store)(nil)

func New(client API, bucket string) *Store {
	return &Store{client: client, bucket: strings.TrimSpace(bucket)}
}

func (store *Store) Get(ctx context.Context, key string) ([]byte, error) {
	if store == nil || store.client == nil || store.bucket == "" {
		return nil, fmt.Errorf("S3 object store is not configured")
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		return nil, fmt.Errorf("invalid S3 object key")
	}
	output, err := store.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(store.bucket), Key: aws.String(key), ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("get S3 object: %w", err)
	}
	if output.Body == nil {
		return nil, fmt.Errorf("S3 object body is empty")
	}
	defer output.Body.Close()
	body, err := io.ReadAll(io.LimitReader(output.Body, domain.MaximumNotificationAttachmentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read S3 object: %w", err)
	}
	if int64(len(body)) > domain.MaximumNotificationAttachmentBytes {
		return nil, fmt.Errorf("S3 object exceeds notification attachment limit")
	}
	return body, nil
}

func (store *Store) Put(ctx context.Context, key string, contentType string, body []byte, sha256Base64 string) error {
	if store == nil || store.client == nil || store.bucket == "" {
		return fmt.Errorf("S3 object store is not configured")
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		return fmt.Errorf("invalid S3 object key")
	}
	_, err := store.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:         aws.String(store.bucket),
		Key:            aws.String(key),
		Body:           bytes.NewReader(body),
		ContentLength:  aws.Int64(int64(len(body))),
		ContentType:    aws.String(strings.TrimSpace(contentType)),
		ChecksumSHA256: aws.String(sha256Base64),
		Metadata: map[string]string{
			"managed-by": "game-server-platform",
		},
	})
	if err != nil {
		return fmt.Errorf("put S3 object: %w", err)
	}
	return nil
}

func (store *Store) Delete(ctx context.Context, key string) error {
	if store == nil || store.client == nil || store.bucket == "" {
		return fmt.Errorf("S3 object store is not configured")
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		return fmt.Errorf("invalid S3 object key")
	}
	if _, err := store.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(key)}); err != nil {
		return fmt.Errorf("delete S3 object: %w", err)
	}
	return nil
}
