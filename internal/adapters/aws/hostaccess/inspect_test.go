package hostaccess

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type inspectionClient struct {
	output *s3.GetObjectOutput
	input  *s3.GetObjectInput
}

func (client *inspectionClient) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	client.input = input
	return client.output, nil
}

func TestReadInspectorPinsAndBoundsLegacyBytes(t *testing.T) {
	for _, test := range []struct {
		name, version, requested, body string
		length, limit                  int64
		valid                          bool
	}{
		{"valid", "version-1", "", "accepted", 8, 10, true},
		{"pinned", "version-1", "version-1", "accepted", 8, 10, true},
		{"wrong-version", "version-2", "version-1", "accepted", 8, 10, false},
		{"unversioned", "null", "", "accepted", 8, 10, false},
		{"oversized-header", "version-1", "", "accepted", 8, 7, false},
		{"lying-header", "version-1", "", "accepted", 5, 7, false},
		{"short-body", "version-1", "", "short", 8, 10, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &inspectionClient{output: &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(test.body)), VersionId: aws.String(test.version), ContentLength: aws.Int64(test.length)}}
			digest, version, size, err := (ReadInspector{Client: client, Bucket: "assets"}).InspectHostRead(context.Background(), "accepted-key", test.requested, test.limit)
			if !test.valid {
				if err == nil || digest != "" || version != "" || size != 0 {
					t.Fatal("invalid bytes produced verified metadata")
				}
				return
			}
			expected := sha256.Sum256([]byte(test.body))
			if err != nil || digest != base64.StdEncoding.EncodeToString(expected[:]) || version != test.version || size != test.length || aws.ToString(client.input.Key) != "accepted-key" || aws.ToString(client.input.VersionId) != test.requested {
				t.Fatal("inspection did not pin exact bytes", err)
			}
		})
	}
}
