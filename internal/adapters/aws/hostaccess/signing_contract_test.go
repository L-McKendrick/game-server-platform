package hostaccess_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

// These tests exercise the pinned SDK without network access. AWS enforcement
// remains a live acceptance gate; generating a signature is not a denial test.
type testCredentials struct{ token string }

func (provider testCredentials) Retrieve(context.Context) (aws.Credentials, error) {
	token := provider.token
	if token == "" {
		token = "test-token"
	}
	return aws.Credentials{AccessKeyID: "test-key", SecretAccessKey: "test-secret", SessionToken: token}, nil
}

func presigner() *s3.PresignClient {
	return s3.NewPresignClient(s3.NewFromConfig(aws.Config{
		Region: "us-west-2", Credentials: testCredentials{},
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}))
}

func TestPresignedArchiveBindsLengthChecksumAndCreateOnly(t *testing.T) {
	t.Parallel()
	digest := sha256.Sum256([]byte("archive"))
	checksum := base64.StdEncoding.EncodeToString(digest[:])
	request, err := presigner().PresignPutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String("assets"), Key: aws.String("sessions/session-1/archives/archive-1/session.tar.gz"),
		ContentLength: aws.Int64(7), ChecksumSHA256: aws.String(checksum), IfNoneMatch: aws.String("*"), ContentType: aws.String("application/gzip"),
	}, func(options *s3.PresignOptions) { options.Expires = 15 * time.Minute })
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(request.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if request.Method != "PUT" || parsed.Path != "/sessions/session-1/archives/archive-1/session.tar.gz" {
		t.Fatalf("unexpected method or object: %s %s", request.Method, parsed.Path)
	}
	if got := query.Get("X-Amz-SignedHeaders"); got != "content-length;content-type;host;if-none-match;x-amz-checksum-sha256" {
		t.Fatalf("signed headers = %q", got)
	}
	if query.Get("X-Amz-Expires") != "900" || query.Get("X-Amz-Security-Token") != "test-token" || query.Get("X-Amz-Signature") == "" {
		t.Fatal("missing bounded expiry, session token, or signature")
	}
	if request.SignedHeader.Get("X-Amz-Checksum-Sha256") != checksum {
		t.Fatal("checksum is not bound in the signed headers")
	}
	if request.SignedHeader.Get("If-None-Match") != "*" || request.SignedHeader.Get("Content-Length") != "7" {
		t.Fatalf("upload headers = %v", request.SignedHeader)
	}
	if request.SignedHeader.Get("Content-Type") != "application/gzip" {
		t.Fatal("explicit PUT content type is not bound to the signature")
	}
	// Independently rebuild SigV4 from the returned transport data. Every
	// mutation must invalidate the original signature, including changes that
	// a host could make while retaining the unmodified bearer URL.
	mutations := []struct {
		name string
		edit func(*http.Request)
	}{
		{"unchanged", func(*http.Request) {}},
		{"method", func(r *http.Request) { r.Method = "GET" }},
		{"object", func(r *http.Request) { r.URL.Path = strings.ReplaceAll(r.URL.Path, "session-1", "session-2") }},
		{"length", func(r *http.Request) { r.ContentLength++ }},
		{"content type", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }},
		{"checksum", func(r *http.Request) { r.Header.Set("X-Amz-Checksum-Sha256", strings.Repeat("a", len(checksum))) }},
		{"overwrite", func(r *http.Request) { r.Header.Del("If-None-Match") }},
		{"expiry", func(r *http.Request) { q := r.URL.Query(); q.Set("X-Amz-Expires", "901"); r.URL.RawQuery = q.Encode() }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			rebuilt, err := http.NewRequest("PUT", request.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			rebuilt.Header = request.SignedHeader.Clone()
			rebuilt.Header.Del("Host")
			rebuilt.Header.Del("Content-Length")
			rebuilt.ContentLength = 7
			q := rebuilt.URL.Query()
			q.Del("X-Amz-Signature")
			rebuilt.URL.RawQuery = q.Encode()
			mutation.edit(rebuilt)
			signingTime, err := time.Parse("20060102T150405Z", query.Get("X-Amz-Date"))
			if err != nil {
				t.Fatal(err)
			}
			credentials, _ := (testCredentials{}).Retrieve(context.Background())
			resigned, _, err := v4.NewSigner().PresignHTTP(context.Background(), credentials, rebuilt, "UNSIGNED-PAYLOAD", "s3", "us-west-2", signingTime,
				func(options *v4.SignerOptions) { options.DisableURIPathEscaping = true })
			if err != nil {
				t.Fatal(err)
			}
			resignedURL, err := url.Parse(resigned)
			if err != nil {
				t.Fatal(err)
			}
			matches := resignedURL.Query().Get("X-Amz-Signature") == query.Get("X-Amz-Signature")
			if matches != (mutation.name == "unchanged") {
				t.Fatalf("signature match = %t", matches)
			}
		})
	}
}

func TestPresignedReadNeedsNoChecksumRequestHeader(t *testing.T) {
	t.Parallel()
	request, err := presigner().PresignGetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String("assets"), Key: aws.String("sessions/session-1/input/mission.pbo"), VersionId: aws.String("accepted-version"),
	}, func(options *s3.PresignOptions) { options.Expires = 15 * time.Minute })
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(request.URL)
	if err != nil {
		t.Fatal(err)
	}
	if request.Method != "GET" || parsed.Query().Get("X-Amz-SignedHeaders") != "host" || parsed.Query().Get("versionId") != "accepted-version" {
		t.Fatal("GET requires unexpected headers or loses accepted version")
	}
}

func TestPresignedStagingPolicyBindsExactKeyAndSizeRange(t *testing.T) {
	t.Parallel()
	key := "sessions/session-1/runtime/host-access/workflow-1/attempt-1/progress.txt"
	request, err := presigner().PresignPostObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String("assets"), Key: aws.String(key),
	}, func(options *s3.PresignPostOptions) {
		options.Expires = 15 * time.Minute
		options.Conditions = []any{
			[]any{"content-length-range", 1, 16 * 1024},
			map[string]string{"Content-Type": "text/plain"},
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := base64.StdEncoding.DecodeString(request.Values["policy"])
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		Expiration time.Time         `json:"expiration"`
		Conditions []json.RawMessage `json:"conditions"`
	}
	if err := json.Unmarshal(encoded, &policy); err != nil {
		t.Fatal(err)
	}
	wantConditions := []string{
		`{"bucket":"assets"}`, `{"key":"` + key + `"}`,
		`["content-length-range",1,16384]`, `{"Content-Type":"text/plain"}`,
		`{"X-Amz-Security-Token":"test-token"}`,
	}
	for _, want := range wantConditions {
		found := false
		for _, condition := range policy.Conditions {
			found = found || string(condition) == want
		}
		if !found {
			t.Errorf("policy is missing %s", want)
		}
	}
	if strings.Contains(string(encoded), "starts-with") {
		t.Fatal("staging policy unexpectedly delegates a prefix")
	}
	if request.Values["key"] != key || request.Values["X-Amz-Signature"] == "" {
		t.Fatal("form loses its exact key or signature")
	}
	if request.Values["Content-Type"] != "" {
		t.Fatal("POST custom form-field contract has changed; review transport handling")
	}
	// The pinned SDK returns only signing fields and key. The host-access signer
	// must add Content-Type to the form as well as its exact policy condition.
	if !policy.Expiration.After(time.Now().Add(14*time.Minute)) || policy.Expiration.After(time.Now().Add(16*time.Minute)) {
		t.Fatal("POST policy is not short lived")
	}
}

func TestRealSDKManifestReferenceFitsSSMWithLargeEscapedToken(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	scope := domain.HostAccessScope{
		SessionID: "session-1", GuildID: "guild-1", OperationID: "workflow-1", AttemptID: "attempt-1",
		InstanceID: "i-1234567890abcdef0", SnapshotSHA256: strings.Repeat("a", 64), DeadlineAt: now.Add(48 * time.Hour),
	}
	// Every slash expands to three query bytes; this exercises a 2-KiB token
	// beyond the ordinary static fixture rather than assuming tiny bearer URLs.
	client := s3.NewFromConfig(aws.Config{Region: "us-west-2", Credentials: testCredentials{token: strings.Repeat("/", 2048)},
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired})
	request, err := s3.NewPresignClient(client).PresignGetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String("assets"), Key: aws.String(scope.StagingKey("manifest")), VersionId: aws.String("manifest-version"),
	}, func(options *s3.PresignOptions) { options.Expires = domain.HostAccessLifetime })
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("manifest"))
	reference := ports.HostAccessReference{
		SchemaVersion: domain.HostAccessSchemaVersion, Scope: scope, Generation: 1, URL: request.URL,
		SHA256: base64.StdEncoding.EncodeToString(digest[:]), SizeBytes: 8, ExpiresAt: now.Add(domain.HostAccessLifetime),
	}
	encoded, err := reference.Encode(now)
	if err != nil {
		t.Fatal(err)
	}
	if base64.StdEncoding.EncodedLen(len(encoded))+8*1024 > domain.MaxHostAccessCommandBytes {
		t.Fatal("real signed reference exceeds the SSM command budget")
	}
}
