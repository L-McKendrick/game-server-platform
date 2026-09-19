package hostaccess

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

// Signer implements transport only. Trusted workers must authorize requests
// against current records and instance tags before calling Sign.
type Signer struct {
	config aws.Config
	bucket string
}

const HostAccessRetentionTag = "gsp-retention=host-access"
const hostAccessRetentionXML = "<Tagging><TagSet><Tag><Key>gsp-retention</Key><Value>host-access</Value></Tag></TagSet></Tagging>"

func NewSigner(config aws.Config, bucket string) *Signer {
	return &Signer{config: config, bucket: bucket}
}

type frozenCredentials struct{ value aws.Credentials }

func (provider frozenCredentials) Retrieve(context.Context) (aws.Credentials, error) {
	return provider.value, nil
}

func (signer *Signer) Sign(ctx context.Context, scope domain.HostAccessScope, object domain.HostObjectRequest) (ports.HostObjectCapability, error) {
	if err := object.Validate(scope); err != nil {
		return ports.HostObjectCapability{}, err
	}
	if signer.bucket == "" || signer.config.Credentials == nil {
		return ports.HostObjectCapability{}, fmt.Errorf("host object signer configuration is incomplete")
	}
	credentials, err := signer.config.Credentials.Retrieve(ctx)
	if err != nil || credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" {
		return ports.HostObjectCapability{}, fmt.Errorf("host object signing credentials unavailable")
	}
	var credentialExpiry time.Time
	if credentials.CanExpire {
		if credentials.Expires.IsZero() {
			return ports.HostObjectCapability{}, fmt.Errorf("host object signing credential expiry unavailable")
		}
		credentialExpiry = credentials.Expires
	}
	now := time.Now().UTC()
	expiry, err := domain.HostAccessExpiry(now, scope.DeadlineAt, credentialExpiry)
	if err != nil {
		return ports.HostObjectCapability{}, err
	}
	// Freeze the retrieved credentials: a provider refresh during signing must
	// not substitute a different credential lifetime. Disable signing logs even
	// when the parent AWS configuration enables verbose diagnostics.
	config := signer.config
	config.Credentials = frozenCredentials{credentials}
	config.ClientLogMode = 0
	config.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	config.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	client := s3.NewPresignClient(s3.NewFromConfig(config))
	// SDK signing uses its own clock. Reserve a second against its whole-second
	// timestamp advancing between this calculation and signature generation.
	duration := expiry.Sub(now).Truncate(time.Second) - time.Second
	capability := ports.HostObjectCapability{Object: object, ExpiresAt: expiry}
	switch object.Purpose.Action() {
	case domain.HostObjectRead:
		input := &s3.GetObjectInput{Bucket: aws.String(signer.bucket), Key: aws.String(object.Key)}
		if object.VersionID != "" {
			input.VersionId = aws.String(object.VersionID)
		}
		request, signErr := client.PresignGetObject(ctx, input, func(options *s3.PresignOptions) { options.Expires = duration })
		if signErr != nil {
			return ports.HostObjectCapability{}, fmt.Errorf("host object read signing failed")
		}
		capability.Method, capability.URL = request.Method, request.URL
	case domain.HostObjectCreate:
		request, signErr := client.PresignPutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(signer.bucket), Key: aws.String(object.Key),
			ContentLength: aws.Int64(object.MaxBytes), ContentType: aws.String(object.ContentType),
			ChecksumSHA256: aws.String(object.SHA256), IfNoneMatch: aws.String("*"),
		}, func(options *s3.PresignOptions) { options.Expires = duration })
		if signErr != nil {
			return ports.HostObjectCapability{}, fmt.Errorf("host object create signing failed")
		}
		capability.Method, capability.URL = request.Method, request.URL
		capability.Headers = make(map[string]string)
		for name := range request.SignedHeader {
			if name != "Host" {
				capability.Headers[name] = request.SignedHeader.Get(name)
			}
		}
	case domain.HostObjectStage:
		request, signErr := client.PresignPostObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(signer.bucket), Key: aws.String(object.Key),
		}, func(options *s3.PresignPostOptions) {
			options.Expires = duration
			options.Conditions = []any{
				[]any{"content-length-range", object.MinBytes, object.MaxBytes},
				map[string]string{"Content-Type": object.ContentType},
				map[string]string{"tagging": hostAccessRetentionXML},
			}
		})
		if signErr != nil {
			return ports.HostObjectCapability{}, fmt.Errorf("host object staging signing failed")
		}
		capability.Method, capability.URL, capability.Form = "POST", request.URL, request.Values
		capability.Form["Content-Type"] = object.ContentType
		capability.Form["tagging"] = hostAccessRetentionXML
	}
	// Read the actual SDK expiry rather than reporting an approximate lifetime.
	// Reject clock drift or delayed signing instead of extending authority.
	var actualExpiry time.Time
	if capability.Method == "POST" {
		var policy struct {
			Expiration time.Time `json:"expiration"`
		}
		encoded, decodeErr := base64.StdEncoding.DecodeString(capability.Form["policy"])
		if decodeErr != nil || json.Unmarshal(encoded, &policy) != nil {
			return ports.HostObjectCapability{}, fmt.Errorf("host object signing expiry invalid")
		}
		actualExpiry = policy.Expiration
	} else {
		parsed, parseErr := url.Parse(capability.URL)
		if parseErr != nil {
			return ports.HostObjectCapability{}, fmt.Errorf("host object signing expiry invalid")
		}
		signedAt, dateErr := time.Parse("20060102T150405Z", parsed.Query().Get("X-Amz-Date"))
		seconds, secondsErr := strconv.ParseInt(parsed.Query().Get("X-Amz-Expires"), 10, 64)
		if dateErr != nil || secondsErr != nil || seconds < 1 || seconds > int64(domain.HostAccessLifetime/time.Second) {
			return ports.HostObjectCapability{}, fmt.Errorf("host object signing expiry invalid")
		}
		actualExpiry = signedAt.Add(time.Duration(seconds) * time.Second)
	}
	if actualExpiry.After(expiry) || !actualExpiry.After(time.Now().UTC()) {
		return ports.HostObjectCapability{}, fmt.Errorf("host object signing exceeded validity window")
	}
	capability.ExpiresAt = actualExpiry
	return capability, nil
}

var _ ports.HostObjectSigner = (*Signer)(nil)
