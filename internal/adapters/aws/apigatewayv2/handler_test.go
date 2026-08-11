package apigatewayv2

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func TestHandlerPreservesRawBodyAndDiscordHeaders(t *testing.T) {
	t.Parallel()

	body := "{\n  \"type\": 1, \"application_id\": \"app-1\"\n}"
	var observedBody string
	var observedSignature string
	var observedTimestamp string

	adapter, err := New(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		data, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatalf("ReadAll() returned error: %v", readErr)
		}

		observedBody = string(data)
		observedSignature = request.Header.Get("X-Signature-Ed25519")
		observedTimestamp = request.Header.Get("X-Signature-Timestamp")
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"type":1}`))
	}))
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	response, err := adapter.Handle(context.Background(), events.APIGatewayV2HTTPRequest{
		Version: payloadVersion,
		RawPath: "/discord/interactions",
		Headers: map[string]string{
			"content-type":          "application/json",
			"x-signature-ed25519":   "signature-value",
			"x-signature-timestamp": "1785796800",
			"x-forwarded-proto":     "https",
		},
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			DomainName: "example.execute-api.us-west-2.amazonaws.com",
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
				Method: http.MethodPost,
				Path:   "/discord/interactions",
			},
		},
		Body: body,
	})
	if err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if observedBody != body {
		t.Errorf("body = %q; want exact body %q", observedBody, body)
	}
	if observedSignature != "signature-value" {
		t.Errorf("signature = %q; want signature-value", observedSignature)
	}
	if observedTimestamp != "1785796800" {
		t.Errorf("timestamp = %q; want 1785796800", observedTimestamp)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d; want %d", response.StatusCode, http.StatusAccepted)
	}
	if response.Body != `{"type":1}` {
		t.Errorf("body = %q; want JSON response", response.Body)
	}
	if response.Headers["Content-Type"] != "application/json" {
		t.Errorf("content type = %q; want application/json", response.Headers["Content-Type"])
	}
}

func TestHandlerDecodesBase64RequestBody(t *testing.T) {
	t.Parallel()

	const decodedBody = `{"type":1}`
	var observedBody string

	adapter, err := New(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		data, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatalf("ReadAll() returned error: %v", readErr)
		}
		observedBody = string(data)
		writer.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	response, err := adapter.Handle(context.Background(), baseEvent(
		base64.StdEncoding.EncodeToString([]byte(decodedBody)),
		true,
	))
	if err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if observedBody != decodedBody {
		t.Errorf("body = %q; want %q", observedBody, decodedBody)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d; want %d", response.StatusCode, http.StatusNoContent)
	}
}

func TestHandlerReturnsCookiesSeparately(t *testing.T) {
	t.Parallel()

	adapter, err := New(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Add("Set-Cookie", "first=1; Secure")
		writer.Header().Add("Set-Cookie", "second=2; Secure")
		writer.WriteHeader(http.StatusOK)
	}))
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	response, err := adapter.Handle(context.Background(), baseEvent("", false))
	if err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if len(response.Cookies) != 2 {
		t.Fatalf("cookie count = %d; want 2", len(response.Cookies))
	}
}

func TestHandlerRejectsUnsupportedPayloadVersion(t *testing.T) {
	t.Parallel()

	called := false
	adapter, err := New(http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		called = true
	}))
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}

	event := baseEvent("", false)
	event.Version = "1.0"
	response, err := adapter.Handle(context.Background(), event)
	if err != nil {
		t.Fatalf("Handle() returned error: %v", err)
	}

	if called {
		t.Fatal("HTTP handler was called for unsupported payload version")
	}
	if response.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d; want %d", response.StatusCode, http.StatusInternalServerError)
	}
	if !strings.Contains(response.Body, "unsupported") {
		t.Errorf("body = %q; want unsupported-version message", response.Body)
	}
}

func baseEvent(body string, encoded bool) events.APIGatewayV2HTTPRequest {
	return events.APIGatewayV2HTTPRequest{
		Version:         payloadVersion,
		RawPath:         "/discord/interactions",
		Body:            body,
		IsBase64Encoded: encoded,
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			DomainName: "example.execute-api.us-west-2.amazonaws.com",
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
				Method: http.MethodPost,
				Path:   "/discord/interactions",
			},
		},
	}
}
