package lambdahttp

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestAdapterPreservesDecodedRawBodyAndHeaders(t *testing.T) {
	t.Parallel()

	rawBody := []byte(`{"type":1}`)
	adapter, err := New(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatalf("ReadAll() error = %v", readErr)
		}
		if string(body) != string(rawBody) {
			t.Errorf("body = %q; want %q", body, rawBody)
		}
		if request.Header.Get("X-Signature-Ed25519") != "signature" {
			t.Errorf("signature header was not preserved")
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"accepted":true}`))
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	response, err := adapter.Handle(context.Background(), events.APIGatewayV2HTTPRequest{
		RawPath:         "/discord/interactions",
		Body:            base64.StdEncoding.EncodeToString(rawBody),
		IsBase64Encoded: true,
		Headers: map[string]string{
			"x-signature-ed25519": "signature",
		},
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: http.MethodPost},
		},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Errorf("StatusCode = %d; want %d", response.StatusCode, http.StatusAccepted)
	}
	if response.Body != `{"accepted":true}` {
		t.Errorf("Body = %q", response.Body)
	}
}
