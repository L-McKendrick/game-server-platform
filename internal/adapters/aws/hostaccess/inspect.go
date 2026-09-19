package hostaccess

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type ReadClient interface {
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

type ReadInspector struct {
	Client      ReadClient
	Bucket      string
	ContentType string
}

// InspectHostRead streams rather than buffering legacy accepted input bytes.
// The returned version pins subsequent host delivery to precisely those bytes.
func (inspector ReadInspector) InspectHostRead(ctx context.Context, key, version string, limit int64) (string, string, int64, error) {
	if inspector.Client == nil || inspector.Bucket == "" || key == "" || version == "null" || limit < 1 || limit > 100*1024*1024 {
		return "", "", 0, fmt.Errorf("host read inspection input invalid")
	}
	input := &s3.GetObjectInput{Bucket: aws.String(inspector.Bucket), Key: aws.String(key)}
	if version != "" {
		input.VersionId = aws.String(version)
	}
	output, err := inspector.Client.GetObject(ctx, input)
	if output != nil && output.Body != nil {
		defer output.Body.Close()
	}
	if err != nil || output == nil || output.Body == nil {
		return "", "", 0, fmt.Errorf("host read inspection unavailable")
	}
	pinned := aws.ToString(output.VersionId)
	if pinned == "" || pinned == "null" || (version != "" && pinned != version) || output.ContentLength == nil || *output.ContentLength < 1 || *output.ContentLength > limit || (inspector.ContentType != "" && aws.ToString(output.ContentType) != inspector.ContentType) {
		return "", "", 0, fmt.Errorf("host read inspection metadata invalid")
	}
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(output.Body, limit+1))
	if err != nil || size < 1 || size > limit || size != *output.ContentLength {
		return "", "", 0, fmt.Errorf("host read inspection size invalid")
	}
	return base64.StdEncoding.EncodeToString(hash.Sum(nil)), pinned, size, nil
}
