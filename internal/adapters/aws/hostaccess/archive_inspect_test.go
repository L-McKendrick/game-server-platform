package hostaccess

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type archiveHeadClient struct {
	head func(*s3.HeadObjectInput) (*s3.HeadObjectOutput, error)
}

func (client archiveHeadClient) HeadObject(_ context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return client.head(input)
}

func TestArchiveInspectionRequiresWholeChecksumSizeTypeAndVersion(t *testing.T) {
	for _, scenario := range []string{"valid", "oversized", "zero", "wrong-type", "null-version", "changed-version", "composite-checksum", "invalid-digest", "not-found"} {
		t.Run(scenario, func(t *testing.T) {
			inspector := ArchiveInspector{Bucket: "assets", Client: archiveHeadClient{head: func(input *s3.HeadObjectInput) (*s3.HeadObjectOutput, error) {
				if aws.ToString(input.Key) != "approved-archive" || aws.ToString(input.VersionId) != "pinned" || input.ChecksumMode != types.ChecksumModeEnabled {
					t.Fatal("archive inspection not pinned/checksummed")
				}
				output := &s3.HeadObjectOutput{ContentLength: aws.Int64(domain.MaxArchiveSizeBytes), ContentType: aws.String("application/gzip"), VersionId: aws.String("pinned"), ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(make([]byte, 32))), ChecksumType: types.ChecksumTypeFullObject}
				switch scenario {
				case "oversized":
					output.ContentLength = aws.Int64(domain.MaxArchiveSizeBytes + 1)
				case "zero":
					output.ContentLength = aws.Int64(0)
				case "wrong-type":
					output.ContentType = aws.String("text/plain")
				case "null-version":
					output.VersionId = aws.String("null")
				case "changed-version":
					output.VersionId = aws.String("later")
				case "composite-checksum":
					output.ChecksumType = types.ChecksumTypeComposite
				case "invalid-digest":
					output.ChecksumSHA256 = aws.String("invalid")
				case "not-found":
					return nil, &smithy.GenericAPIError{Code: "NotFound"}
				}
				return output, nil
			}}}
			pin, err := inspector.InspectHostArchive(context.Background(), "approved-archive", "pinned")
			if scenario == "valid" {
				if err != nil || pin.VersionID != "pinned" || pin.SizeBytes != domain.MaxArchiveSizeBytes {
					t.Fatal("valid bounded archive not pinned", err)
				}
			} else {
				if err == nil {
					t.Fatal("invalid immutable metadata accepted")
				}
				if scenario == "not-found" && !errors.Is(err, domain.ErrNotFound) {
					t.Fatal("missing archive not classified for trusted replay", err)
				}
			}
		})
	}
}
