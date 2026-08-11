package httpartifact

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

type Downloader struct{ client *http.Client }

var _ ports.ArtifactDownloader = (*Downloader)(nil)

func New() *Downloader {
	return &Downloader{client: &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many attachment redirects")
			}
			return validateURL(request.URL)
		},
	}}
}

func NewWithClient(client *http.Client) *Downloader { return &Downloader{client: client} }

func (downloader *Downloader) Download(ctx context.Context, request domain.ArtifactIngestRequest) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if downloader == nil || downloader.client == nil {
		return nil, fmt.Errorf("HTTP artifact client is required")
	}
	parsed, err := url.Parse(request.SourceURL)
	if err != nil || validateURL(parsed) != nil {
		return nil, fmt.Errorf("attachment URL is not approved")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create attachment request: %w", err)
	}
	httpRequest.Header.Set("User-Agent", "game-server-platform-artifact-worker/1")
	response, err := downloader.client.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("download attachment: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("attachment server returned %s", response.Status)
	}
	limited := io.LimitReader(response.Body, request.SizeBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	if int64(len(body)) > request.SizeBytes {
		return nil, fmt.Errorf("attachment exceeded declared size")
	}
	return body, nil
}

func validateURL(value *url.URL) error {
	host := strings.ToLower(value.Hostname())
	if value.Scheme != "https" || (host != "cdn.discordapp.com" && host != "media.discordapp.net") {
		return fmt.Errorf("URL must use an approved Discord CDN host")
	}
	return nil
}
