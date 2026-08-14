package s3archive

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type API interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

func (store *Store) Get(ctx context.Context, object ports.ArchiveObject) ([]byte, error) {
	if err := validateObject(object); err != nil {
		return nil, err
	}
	if object.SizeBytes > 1024*1024 {
		return nil, fmt.Errorf("archive metadata object exceeds read limit")
	}
	output, err := store.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(store.bucket), Key: aws.String(object.Key), ChecksumMode: types.ChecksumModeEnabled})
	if err != nil {
		return nil, fmt.Errorf("get archive object: %w", err)
	}
	defer output.Body.Close()
	body, err := io.ReadAll(io.LimitReader(output.Body, object.SizeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read archive object: %w", err)
	}
	if int64(len(body)) != object.SizeBytes || strings.TrimSpace(aws.ToString(output.ChecksumSHA256)) != strings.TrimSpace(object.SHA256) {
		return nil, fmt.Errorf("archive metadata object verification failed")
	}
	return body, nil
}

type Store struct {
	client API
	bucket string
}

var _ ports.ArchiveStore = (*Store)(nil)

func New(client API, bucket string) (*Store, error) {
	bucket = strings.TrimSpace(bucket)
	if client == nil || bucket == "" {
		return nil, fmt.Errorf("S3 client and archive bucket are required")
	}
	return &Store{client: client, bucket: bucket}, nil
}

func (store *Store) Put(ctx context.Context, object ports.ArchiveObject, body []byte) error {
	if err := validateObject(object); err != nil {
		return err
	}
	if int64(len(body)) != object.SizeBytes {
		return fmt.Errorf("archive object body size does not match metadata")
	}
	_, err := store.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(store.bucket), Key: aws.String(object.Key), Body: bytes.NewReader(body),
		ContentLength: aws.Int64(object.SizeBytes), ContentType: aws.String(object.ContentType),
		ChecksumSHA256: aws.String(object.SHA256),
		Metadata:       map[string]string{"managed-by": "game-server-platform"},
	})
	if err != nil {
		return fmt.Errorf("put archive object: %w", err)
	}
	return nil
}

func (store *Store) Verify(ctx context.Context, object ports.ArchiveObject) error {
	if err := validateObject(object); err != nil {
		return err
	}
	output, err := store.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(store.bucket), Key: aws.String(object.Key), ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		return fmt.Errorf("head archive object: %w", err)
	}
	if aws.ToInt64(output.ContentLength) != object.SizeBytes {
		return fmt.Errorf("archive object size verification failed")
	}
	if strings.TrimSpace(aws.ToString(output.ChecksumSHA256)) != strings.TrimSpace(object.SHA256) {
		return fmt.Errorf("archive object checksum verification failed")
	}
	return nil
}

func validateObject(object ports.ArchiveObject) error {
	key := strings.TrimSpace(object.Key)
	switch {
	case key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, ".."):
		return fmt.Errorf("invalid archive object key")
	case strings.TrimSpace(object.SHA256) == "":
		return fmt.Errorf("archive object SHA-256 is required")
	case object.SizeBytes <= 0:
		return fmt.Errorf("archive object size must be positive")
	case strings.TrimSpace(object.ContentType) == "":
		return fmt.Errorf("archive object content type is required")
	default:
		return nil
	}
}
