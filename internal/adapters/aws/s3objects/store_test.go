package s3objects

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type fakeAPI struct {
	putInput *s3.PutObjectInput
	getInput *s3.GetObjectInput
	body     []byte
}

func (fake *fakeAPI) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	fake.putInput = input
	return &s3.PutObjectOutput{}, nil
}

func (fake *fakeAPI) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	fake.getInput = input
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(fake.body))}, nil
}

func TestStoreReadsManagedObjectWithChecksumsEnabled(t *testing.T) {
	t.Parallel()
	client := &fakeAPI{body: []byte("sanitized modlist")}
	store := New(client, "assets-bucket")
	key := "sessions/session-1/input/modlists/digest/session-modlist.html"

	body, err := store.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "sanitized modlist" || client.getInput == nil ||
		aws.ToString(client.getInput.Bucket) != "assets-bucket" || aws.ToString(client.getInput.Key) != key ||
		client.getInput.ChecksumMode != types.ChecksumModeEnabled {
		t.Fatalf("Get() body=%q input=%#v", body, client.getInput)
	}
}

func TestStoreRejectsOversizedNotificationObject(t *testing.T) {
	t.Parallel()
	client := &fakeAPI{body: make([]byte, domain.MaximumNotificationAttachmentBytes+1)}
	store := New(client, "assets-bucket")
	if _, err := store.Get(context.Background(), "sessions/session-1/input/modlists/digest/session-modlist.html"); err == nil {
		t.Fatal("oversized Get() returned nil error")
	}
}
