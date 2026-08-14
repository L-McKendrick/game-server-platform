package s3sessioncleanup

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type fakeAPI struct {
	listedPrefix string
	deleted      []types.ObjectIdentifier
}

func (fake *fakeAPI) ListObjectVersions(_ context.Context, input *s3.ListObjectVersionsInput, _ ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error) {
	fake.listedPrefix = aws.ToString(input.Prefix)
	return &s3.ListObjectVersionsOutput{
		Versions:      []types.ObjectVersion{{Key: aws.String(fake.listedPrefix + "input/mission.pbo"), VersionId: aws.String("v1")}},
		DeleteMarkers: []types.DeleteMarkerEntry{{Key: aws.String(fake.listedPrefix + "archives/a/manifest.json"), VersionId: aws.String("v2")}},
	}, nil
}

func (fake *fakeAPI) DeleteObjects(_ context.Context, input *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	fake.deleted = append(fake.deleted, input.Delete.Objects...)
	return &s3.DeleteObjectsOutput{}, nil
}

func TestCleanerDeletesEveryVersionUnderExactSessionPrefix(t *testing.T) {
	api := &fakeAPI{}
	cleaner, err := New(api, "assets")
	if err != nil {
		t.Fatal(err)
	}
	count, err := cleaner.DeleteSessionObjects(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if api.listedPrefix != "sessions/session-1/" || count != 2 || len(api.deleted) != 2 {
		t.Fatalf("prefix = %q, count = %d, deleted = %#v", api.listedPrefix, count, api.deleted)
	}
}

func TestCleanerRejectsPrefixEscapes(t *testing.T) {
	cleaner, _ := New(&fakeAPI{}, "assets")
	for _, sessionID := range []string{"", "../other", "session/other", `session\other`} {
		if _, err := cleaner.DeleteSessionObjects(context.Background(), sessionID); err == nil {
			t.Fatalf("session ID %q was accepted", sessionID)
		}
	}
}
