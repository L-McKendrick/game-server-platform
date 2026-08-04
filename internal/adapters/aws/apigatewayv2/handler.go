// Package apigatewayv2 adapts API Gateway HTTP API payload version 2.0 events
// to standard-library HTTP handlers.
package apigatewayv2

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

const payloadVersion = "2.0"

// Handler invokes a net/http handler from API Gateway HTTP API events.
type Handler struct {
	next http.Handler
}

// New creates an API Gateway HTTP API payload v2 adapter.
func New(next http.Handler) (*Handler, error) {
	if next == nil {
		return nil, fmt.Errorf("HTTP handler is required")
	}

	return &Handler{next: next}, nil
}

// Handle converts an API Gateway HTTP API payload v2 request into an
// http.Request and converts the recorder output back into a v2 response.
func (handler *Handler) Handle(
	ctx context.Context,
	event events.APIGatewayV2HTTPRequest,
) (events.APIGatewayV2HTTPResponse, error) {
	if strings.TrimSpace(event.Version) != payloadVersion {
		return errorResponse(
			http.StatusInternalServerError,
			"unsupported API Gateway payload version",
		), nil
	}

	body, err := decodeBody(event.Body, event.IsBase64Encoded)
	if err != nil {
		return errorResponse(http.StatusBadRequest, "invalid request body"), nil
	}

	request, err := newHTTPRequest(ctx, event, body)
	if err != nil {
		return errorResponse(
			http.StatusInternalServerError,
			"invalid API Gateway request",
		), nil
	}

	recorder := httptest.NewRecorder()
	handler.next.ServeHTTP(recorder, request)

	return toLambdaResponse(recorder), nil
}

func decodeBody(body string, encoded bool) ([]byte, error) {
	if !encoded {
		return []byte(body), nil
	}

	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return nil, fmt.Errorf("decode base64 request body: %w", err)
	}

	return decoded, nil
}

func newHTTPRequest(
	ctx context.Context,
	event events.APIGatewayV2HTTPRequest,
	body []byte,
) (*http.Request, error) {
	method := strings.TrimSpace(event.RequestContext.HTTP.Method)
	if method == "" {
		return nil, fmt.Errorf("request method is required")
	}

	path := event.RawPath
	if strings.TrimSpace(path) == "" {
		path = event.RequestContext.HTTP.Path
	}
	if strings.TrimSpace(path) == "" {
		path = "/"
	}

	host := strings.TrimSpace(event.RequestContext.DomainName)
	if host == "" {
		host = headerValue(event.Headers, "host")
	}
	if host == "" {
		host = "lambda.invalid"
	}

	scheme := headerValue(event.Headers, "x-forwarded-proto")
	if scheme == "" {
		scheme = "https"
	}

	requestURL := url.URL{
		Scheme:   scheme,
		Host:     host,
		Path:     path,
		RawQuery: event.RawQueryString,
	}

	request, err := http.NewRequestWithContext(
		ctx,
		method,
		requestURL.String(),
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}

	for name, value := range event.Headers {
		request.Header.Set(name, value)
	}

	if len(event.Cookies) > 0 && request.Header.Get("Cookie") == "" {
		request.Header.Set("Cookie", strings.Join(event.Cookies, "; "))
	}

	request.Host = host
	request.ContentLength = int64(len(body))
	return request, nil
}

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

func toLambdaResponse(
	recorder *httptest.ResponseRecorder,
) events.APIGatewayV2HTTPResponse {
	result := recorder.Result()
	defer result.Body.Close()

	body, err := io.ReadAll(result.Body)
	if err != nil {
		return errorResponse(
			http.StatusInternalServerError,
			"failed to read HTTP response",
		)
	}

	headers := make(map[string]string, len(result.Header))
	var cookies []string
	for name, values := range result.Header {
		if strings.EqualFold(name, "Set-Cookie") {
			cookies = append(cookies, values...)
			continue
		}

		headers[name] = strings.Join(values, ",")
	}

	return events.APIGatewayV2HTTPResponse{
		StatusCode:      result.StatusCode,
		Headers:         headers,
		Body:            string(body),
		IsBase64Encoded: false,
		Cookies:         cookies,
	}
}

func errorResponse(status int, message string) events.APIGatewayV2HTTPResponse {
	return events.APIGatewayV2HTTPResponse{
		StatusCode: status,
		Headers: map[string]string{
			"Content-Type": "text/plain; charset=utf-8",
		},
		Body: message + "\n",
	}
}
