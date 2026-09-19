package hostaccess

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type ArchiveHeadClient interface {
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

type ArchiveInspector struct {
	Client ArchiveHeadClient
	Bucket string
}

func (inspector ArchiveInspector) InspectHostArchive(ctx context.Context, key, version string) (domain.HostOutputVersion, error) {
	if inspector.Client == nil || inspector.Bucket == "" || key == "" || version == "null" {
		return domain.HostOutputVersion{}, fmt.Errorf("archive inspection input invalid")
	}
	input := &s3.HeadObjectInput{Bucket: aws.String(inspector.Bucket), Key: aws.String(key), ChecksumMode: types.ChecksumModeEnabled}
	if version != "" {
		input.VersionId = aws.String(version)
	}
	output, err := inspector.Client.HeadObject(ctx, input)
	if err != nil {
		var apiError smithy.APIError
		if errors.As(err, &apiError) && (apiError.ErrorCode() == "NotFound" || apiError.ErrorCode() == "NoSuchKey" || apiError.ErrorCode() == "NoSuchVersion") {
			return domain.HostOutputVersion{}, domain.ErrNotFound
		}
		return domain.HostOutputVersion{}, fmt.Errorf("archive inspection unavailable")
	}
	if output == nil {
		return domain.HostOutputVersion{}, fmt.Errorf("archive inspection response missing")
	}
	checksum, pinned := aws.ToString(output.ChecksumSHA256), aws.ToString(output.VersionId)
	digest, decodeErr := base64.StdEncoding.DecodeString(checksum)
	if decodeErr != nil || len(digest) != 32 || base64.StdEncoding.EncodeToString(digest) != checksum || output.ContentLength == nil || *output.ContentLength < 1 || *output.ContentLength > domain.MaxArchiveSizeBytes || aws.ToString(output.ContentType) != "application/gzip" || pinned == "" || pinned == "null" || (version != "" && pinned != version) || (output.ChecksumType != "" && output.ChecksumType != types.ChecksumTypeFullObject) {
		return domain.HostOutputVersion{}, fmt.Errorf("archive immutable metadata invalid")
	}
	return domain.HostOutputVersion{VersionID: pinned, SHA256: checksum, SizeBytes: *output.ContentLength}, nil
}

var _ ports.HostArchiveInspector = ArchiveInspector{}
