package s3sessioncleanup

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type API interface {
	ListObjectVersions(context.Context, *s3.ListObjectVersionsInput, ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error)
	DeleteObjects(context.Context, *s3.DeleteObjectsInput, ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

type Cleaner struct {
	client API
	bucket string
}

var _ ports.SessionObjectCleaner = (*Cleaner)(nil)

func New(client API, bucket string) (*Cleaner, error) {
	bucket = strings.TrimSpace(bucket)
	if client == nil || bucket == "" {
		return nil, fmt.Errorf("S3 client and bucket are required")
	}
	return &Cleaner{client: client, bucket: bucket}, nil
}

func (cleaner *Cleaner) DeleteSessionObjects(ctx context.Context, sessionID string) (int, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || strings.ContainsAny(sessionID, "/\\") || strings.Contains(sessionID, "..") {
		return 0, fmt.Errorf("invalid session ID")
	}
	prefix := "sessions/" + sessionID + "/"
	var keyMarker, versionMarker *string
	deleted := 0
	for {
		output, err := cleaner.client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket: aws.String(cleaner.bucket), Prefix: aws.String(prefix), KeyMarker: keyMarker, VersionIdMarker: versionMarker,
		})
		if err != nil {
			return deleted, fmt.Errorf("list session object versions: %w", err)
		}
		identifiers := make([]types.ObjectIdentifier, 0, len(output.Versions)+len(output.DeleteMarkers))
		for _, version := range output.Versions {
			if key := aws.ToString(version.Key); strings.HasPrefix(key, prefix) {
				identifiers = append(identifiers, types.ObjectIdentifier{Key: version.Key, VersionId: version.VersionId})
			}
		}
		for _, marker := range output.DeleteMarkers {
			if key := aws.ToString(marker.Key); strings.HasPrefix(key, prefix) {
				identifiers = append(identifiers, types.ObjectIdentifier{Key: marker.Key, VersionId: marker.VersionId})
			}
		}
		for start := 0; start < len(identifiers); start += 1000 {
			end := start + 1000
			if end > len(identifiers) {
				end = len(identifiers)
			}
			result, err := cleaner.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(cleaner.bucket), Delete: &types.Delete{Objects: identifiers[start:end], Quiet: aws.Bool(true)},
			})
			if err != nil {
				return deleted, fmt.Errorf("delete session objects: %w", err)
			}
			if len(result.Errors) != 0 {
				return deleted, fmt.Errorf("delete session objects: S3 rejected %d objects", len(result.Errors))
			}
			deleted += end - start
		}
		if !aws.ToBool(output.IsTruncated) {
			return deleted, nil
		}
		keyMarker, versionMarker = output.NextKeyMarker, output.NextVersionIdMarker
		if aws.ToString(keyMarker) == "" {
			return deleted, fmt.Errorf("list session object versions returned an invalid continuation marker")
		}
	}
}
