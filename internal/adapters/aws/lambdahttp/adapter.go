package lambdahttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

// Adapter exposes a standard net/http handler through API Gateway HTTP API v2.
type Adapter struct {
	handler http.Handler
}

func New(handler http.Handler) (*Adapter, error) {
	if handler == nil {
		return nil, fmt.Errorf("HTTP handler is required")
	}
	return &Adapter{handler: handler}, nil
}

func (adapter *Adapter) Handle(
	ctx context.Context,
	event events.APIGatewayV2HTTPRequest,
) (events.APIGatewayV2HTTPResponse, error) {
	body := []byte(event.Body)
	if event.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(event.Body)
		if err != nil {
			return events.APIGatewayV2HTTPResponse{}, fmt.Errorf("decode API Gateway request body: %w", err)
		}
		body = decoded
	}

	path := event.RawPath
	if path == "" {
		path = "/discord/interactions"
	}
	requestURL := &url.URL{Path: path, RawQuery: event.RawQueryString}
	method := strings.TrimSpace(event.RequestContext.HTTP.Method)
	if method == "" {
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), bytes.NewReader(body))
	if err != nil {
		return events.APIGatewayV2HTTPResponse{}, fmt.Errorf("construct HTTP request: %w", err)
	}
	for name, value := range event.Headers {
		request.Header.Set(name, value)
	}
	if len(event.Cookies) > 0 {
		request.Header.Set("Cookie", strings.Join(event.Cookies, "; "))
	}

	recorder := httptest.NewRecorder()
	adapter.handler.ServeHTTP(recorder, request)
	result := recorder.Result()
	defer result.Body.Close()

	headers := make(map[string]string, len(result.Header))
	for name, values := range result.Header {
		headers[name] = strings.Join(values, ",")
	}
	return events.APIGatewayV2HTTPResponse{
		StatusCode:      result.StatusCode,
		Headers:         headers,
		Body:            recorder.Body.String(),
		IsBase64Encoded: false,
	}, nil
}
