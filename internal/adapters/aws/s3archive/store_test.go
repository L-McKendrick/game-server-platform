package s3archive

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type fakeAPI struct {
	put  *s3.PutObjectInput
	head *s3.HeadObjectOutput
}

func (api *fakeAPI) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	api.put = input
	return &s3.PutObjectOutput{}, nil
}
func (api *fakeAPI) HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return api.head, nil
}
func (api *fakeAPI) GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader("body")), ChecksumSHA256: aws.String("checksum")}, nil
}

func TestStore_PutsAndVerifiesSHA256AndSize(t *testing.T) {
	api := &fakeAPI{head: &s3.HeadObjectOutput{ContentLength: aws.Int64(4), ChecksumSHA256: aws.String("checksum")}}
	store, err := New(api, "bucket-1")
	if err != nil {
		t.Fatal(err)
	}
	object := ports.ArchiveObject{Key: "sessions/s/archives/a/manifest.v1.json", SHA256: "checksum", SizeBytes: 4, ContentType: "application/json"}
	if err := store.Put(context.Background(), object, []byte("body")); err != nil {
		t.Fatal(err)
	}
	if err := store.Verify(context.Background(), object); err != nil {
		t.Fatal(err)
	}
	if aws.ToString(api.put.ChecksumSHA256) != object.SHA256 || aws.ToInt64(api.put.ContentLength) != object.SizeBytes {
		t.Fatalf("put input = %#v", api.put)
	}
}

func TestStore_RejectsChecksumMismatch(t *testing.T) {
	api := &fakeAPI{head: &s3.HeadObjectOutput{ContentLength: aws.Int64(4), ChecksumSHA256: aws.String("different")}}
	store, _ := New(api, "bucket-1")
	err := store.Verify(context.Background(), ports.ArchiveObject{Key: "sessions/s/archives/a/session.tar.gz", SHA256: "checksum", SizeBytes: 4, ContentType: "application/gzip"})
	if err == nil {
		t.Fatal("Verify() returned nil error")
	}
}
