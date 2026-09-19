package hostaccess

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type outputHTTPClient struct {
	handle func(*http.Request) (*http.Response, error)
}

func (client outputHTTPClient) Do(request *http.Request) (*http.Response, error) {
	return client.handle(request)
}

func TestPinnedSDKPromotesNonSeekableSourceWithoutBuffering(t *testing.T) {
	payload := strings.Repeat("p", 16)
	checksum := outputDigest(payload)
	puts := 0
	config := aws.Config{Region: "us-west-2", Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "test-key", SecretAccessKey: "test-secret"}, nil
	}), RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired, HTTPClient: outputHTTPClient{handle: func(request *http.Request) (*http.Response, error) {
		response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request}
		if request.Method == "GET" {
			if request.URL.Query().Get("versionId") != "source-v1" {
				t.Fatal("SDK did not request pinned source")
			}
			response.Header.Set("X-Amz-Version-Id", "source-v1")
			response.Header.Set("Content-Type", "application/octet-stream")
			response.Header.Set("Content-Length", "16")
			response.ContentLength = 16
			response.Body = io.NopCloser(strings.NewReader(payload))
		} else if request.Method == "PUT" {
			puts++
			body, err := io.ReadAll(request.Body)
			if err != nil || string(body) != payload || request.ContentLength != 16 || request.Header.Get("If-None-Match") != "*" || request.Header.Get("X-Amz-Checksum-Sha256") != checksum {
				t.Fatal("SDK streaming constraints changed", err)
			}
			response.Header.Set("X-Amz-Version-Id", "durable-v1")
		} else {
			t.Fatal("unexpected SDK request", request.Method)
		}
		return response, nil
	}}}
	store := WorkshopStore{Client: s3.NewFromConfig(config), Bucket: "assets"}
	version, err := store.PromoteHostOutput(context.Background(), "stage", "durable", "application/octet-stream", domain.HostOutputVersion{VersionID: "source-v1", SHA256: checksum, SizeBytes: 16})
	if err != nil || version != "durable-v1" || puts != 1 {
		t.Fatal("nonseekable SDK publication failed", err)
	}
}

type outputClient struct {
	get func(*s3.GetObjectInput) (*s3.GetObjectOutput, error)
	put func(*s3.PutObjectInput) (*s3.PutObjectOutput, error)
}

func (client outputClient) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return client.get(input)
}
func (client outputClient) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return client.put(input)
}
func outputResponse(body, version, contentType string) *s3.GetObjectOutput {
	return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(body)), ContentLength: aws.Int64(int64(len(body))), ContentType: aws.String(contentType), VersionId: aws.String(version)}
}
func outputDigest(body string) string {
	digest := sha256.Sum256([]byte(body))
	return base64.StdEncoding.EncodeToString(digest[:])
}

func TestCanonicalDocumentPublicationIsBoundedAndImmutable(t *testing.T) {
	for _, scenario := range []string{"new", "existing", "conflict", "empty", "oversized", "wrong-type"} {
		t.Run(scenario, func(t *testing.T) {
			body, contentType := "canonical rows\n", "text/tab-separated-values"
			calls := 0
			store := WorkshopStore{Bucket: "assets", Client: outputClient{
				get: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					if aws.ToString(input.Key) != "resolution" {
						t.Fatal("wrong document lookup")
					}
					payload := body
					if scenario == "conflict" {
						payload = strings.Repeat("x", len(body))
					}
					return outputResponse(payload, "existing-v1", contentType), nil
				},
				put: func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
					calls++
					payload, err := io.ReadAll(input.Body)
					if err != nil || string(payload) != body || aws.ToString(input.Key) != "resolution" || aws.ToString(input.IfNoneMatch) != "*" || aws.ToString(input.ChecksumSHA256) != outputDigest(body) || aws.ToInt64(input.ContentLength) != int64(len(body)) || aws.ToString(input.ContentType) != contentType {
						t.Fatal("canonical publication constraints changed")
					}
					if scenario != "new" {
						return nil, &smithy.GenericAPIError{Code: "PreconditionFailed"}
					}
					return &s3.PutObjectOutput{VersionId: aws.String("new-v1")}, nil
				},
			}}
			switch scenario {
			case "empty":
				body = ""
			case "oversized":
				body = strings.Repeat("x", domain.MaxHostResultBytes+1)
			case "wrong-type":
				contentType = "application/octet-stream"
			}
			version, err := store.PublishHostDocument(context.Background(), "resolution", contentType, []byte(body))
			if scenario == "new" || scenario == "existing" {
				if err != nil || version == "" || calls != 1 {
					t.Fatal("valid document publication failed", err)
				}
			} else {
				if err == nil {
					t.Fatal("invalid document publication accepted")
				}
				if scenario != "conflict" && calls != 0 {
					t.Fatal("invalid document reached storage")
				}
			}
		})
	}
}

func TestOutputReadPinsBoundedControlBytes(t *testing.T) {
	for _, scenario := range []string{"valid", "oversized", "truncated", "null-version", "wrong-version", "wrong-type"} {
		t.Run(scenario, func(t *testing.T) {
			store := WorkshopStore{Bucket: "assets", Client: outputClient{get: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
				if aws.ToString(input.VersionId) != "pinned" {
					t.Fatal("read did not request exact version")
				}
				response := outputResponse("{}", "pinned", "application/json")
				switch scenario {
				case "oversized":
					response.Body = io.NopCloser(strings.NewReader("oversized"))
				case "truncated":
					response.ContentLength = aws.Int64(3)
				case "null-version":
					response.VersionId = aws.String("null")
				case "wrong-version":
					response.VersionId = aws.String("other")
				case "wrong-type":
					response.ContentType = aws.String("text/plain")
				}
				return response, nil
			}}}
			body, pin, err := store.ReadHostOutput(context.Background(), "stage", "pinned", "application/json", 4)
			if scenario == "valid" {
				if err != nil || string(body) != "{}" || pin.VersionID != "pinned" || pin.SHA256 != outputDigest("{}") || pin.SizeBytes != 2 {
					t.Fatal("control bytes not pinned", err)
				}
			} else if err == nil {
				t.Fatal("invalid control output accepted")
			}
		})
	}
}

func TestOutputPromotionUsesPinnedStreamAndCreateOnlyChecksum(t *testing.T) {
	for _, scenario := range []string{"new", "existing", "conflicting", "wrong-type", "changed-source"} {
		t.Run(scenario, func(t *testing.T) {
			body := strings.Repeat("p", 16)
			pin := domain.HostOutputVersion{VersionID: "source-v1", SHA256: outputDigest(body), SizeBytes: int64(len(body))}
			puts := 0
			store := WorkshopStore{Bucket: "assets", Client: outputClient{
				get: func(input *s3.GetObjectInput) (*s3.GetObjectOutput, error) {
					if aws.ToString(input.Key) == "stage" {
						if aws.ToString(input.VersionId) != pin.VersionID {
							t.Fatal("promotion read latest staging instead of pin")
						}
						response := outputResponse(body, pin.VersionID, "application/octet-stream")
						if scenario == "changed-source" {
							response.VersionId = aws.String("later")
						}
						return response, nil
					}
					if aws.ToString(input.Key) != "durable" {
						t.Fatal("unexpected destination lookup")
					}
					response := outputResponse(body, "destination-v1", "application/octet-stream")
					if scenario == "conflicting" {
						response.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", 16)))
					}
					if scenario == "wrong-type" {
						response.ContentType = aws.String("text/plain")
					}
					return response, nil
				},
				put: func(input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
					puts++
					if aws.ToString(input.Key) != "durable" || aws.ToString(input.IfNoneMatch) != "*" || aws.ToString(input.ChecksumSHA256) != pin.SHA256 || aws.ToString(input.ContentType) != "application/octet-stream" || aws.ToInt64(input.ContentLength) != pin.SizeBytes {
						t.Fatal("missing immutable publication constraints")
					}
					if _, buffered := input.Body.(*strings.Reader); buffered {
						t.Fatal("publication replaced stream with buffered payload")
					}
					payload, err := io.ReadAll(input.Body)
					if err != nil || string(payload) != body {
						t.Fatal("wrong pinned bytes streamed")
					}
					if scenario != "new" {
						return nil, &smithy.GenericAPIError{Code: "PreconditionFailed"}
					}
					return &s3.PutObjectOutput{VersionId: aws.String("created-v1")}, nil
				},
			}}
			version, err := store.PromoteHostOutput(context.Background(), "stage", "durable", "application/octet-stream", pin)
			if scenario == "new" || scenario == "existing" {
				if err != nil || version == "" || puts != 1 {
					t.Fatal("valid publication failed", err)
				}
			} else if err == nil {
				t.Fatal("conflicting publication accepted")
			}
			if scenario == "changed-source" && puts != 0 {
				t.Fatal("changed source reached publication")
			}
		})
	}
}
