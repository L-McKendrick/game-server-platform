package steamworkshop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
	"github.com/L-McKendrick/game-server-platform/internal/ports"
)

const apiBaseURL = "https://api.steampowered.com/ISteamRemoteStorage"

type Client struct {
	httpClient *http.Client
	baseURL    string
}

var _ ports.WorkshopCatalog = (*Client)(nil)

func New() *Client {
	return &Client{httpClient: &http.Client{Timeout: 10 * time.Second}, baseURL: apiBaseURL}
}

func NewWithClient(client *http.Client, baseURL string) (*Client, error) {
	if client == nil || strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("Steam Workshop HTTP client and base URL are required")
	}
	return &Client{httpClient: client, baseURL: strings.TrimRight(baseURL, "/")}, nil
}

func (client *Client) Item(ctx context.Context, publishedFileID uint64) (domain.WorkshopItem, error) {
	if publishedFileID == 0 {
		return domain.WorkshopItem{}, fmt.Errorf("published file ID is required")
	}
	values := url.Values{"itemcount": {"1"}, "publishedfileids[0]": {strconv.FormatUint(publishedFileID, 10)}}
	var response publishedFileResponse
	if err := client.post(ctx, "/GetPublishedFileDetails/v1/", values, &response); err != nil {
		return domain.WorkshopItem{}, err
	}
	if len(response.Response.Details) != 1 {
		return domain.WorkshopItem{}, domain.WorkshopMetadataError{Code: domain.WorkshopMetadataInvalidResponse, Retryable: true, Detail: "Steam returned no item metadata"}
	}
	detail := response.Response.Details[0]
	item := domain.WorkshopItem{PublishedFileID: publishedFileID, ConsumerAppID: detail.ConsumerAppID, Title: strings.TrimSpace(detail.Title), FileSize: detail.FileSize, Available: detail.Result == 1, Collection: detail.FileType == 2}
	if detail.PublishedFileID != "" {
		id, err := strconv.ParseUint(detail.PublishedFileID, 10, 64)
		if err != nil || id != publishedFileID {
			return domain.WorkshopItem{}, domain.WorkshopMetadataError{Code: domain.WorkshopMetadataInvalidResponse, Retryable: true, Detail: "Steam returned mismatched item metadata"}
		}
	}
	if detail.TimeUpdated > 0 {
		item.UpdatedAt = time.Unix(detail.TimeUpdated, 0).UTC()
	}
	for _, tag := range detail.Tags {
		item.Tags = append(item.Tags, tag.Tag)
	}
	return item, nil
}

func (client *Client) CollectionChildren(ctx context.Context, publishedFileID uint64) ([]uint64, error) {
	if publishedFileID == 0 {
		return nil, fmt.Errorf("collection ID is required")
	}
	values := url.Values{"collectioncount": {"1"}, "publishedfileids[0]": {strconv.FormatUint(publishedFileID, 10)}}
	var response collectionResponse
	if err := client.post(ctx, "/GetCollectionDetails/v1/", values, &response); err != nil {
		return nil, err
	}
	if len(response.Response.Details) != 1 || response.Response.Details[0].Result != 1 {
		return nil, domain.WorkshopMetadataError{Code: domain.WorkshopMetadataUnavailable, Detail: "Steam collection is unavailable or private"}
	}
	children := make([]uint64, 0, len(response.Response.Details[0].Children))
	for _, child := range response.Response.Details[0].Children {
		id, err := strconv.ParseUint(child.PublishedFileID, 10, 64)
		if err != nil || id == 0 {
			return nil, domain.WorkshopMetadataError{Code: domain.WorkshopMetadataInvalidResponse, Retryable: true, Detail: "Steam returned an invalid collection child"}
		}
		children = append(children, id)
	}
	return children, nil
}

func (client *Client) post(ctx context.Context, endpoint string, values url.Values, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("create Steam metadata request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "game-server-platform-workshop/1")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return domain.WorkshopMetadataError{Code: domain.WorkshopMetadataTransient, Retryable: true, Detail: "Steam metadata request failed"}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return domain.WorkshopMetadataError{Code: domain.WorkshopMetadataRateLimited, Retryable: true, Detail: "Steam rate limited the metadata request"}
	}
	if response.StatusCode >= 500 {
		return domain.WorkshopMetadataError{Code: domain.WorkshopMetadataTransient, Retryable: true, Detail: fmt.Sprintf("Steam metadata returned HTTP %d", response.StatusCode)}
	}
	if response.StatusCode != http.StatusOK {
		return domain.WorkshopMetadataError{Code: domain.WorkshopMetadataRejected, Detail: fmt.Sprintf("Steam metadata returned HTTP %d", response.StatusCode)}
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024))
	if err := decoder.Decode(output); err != nil {
		return domain.WorkshopMetadataError{Code: domain.WorkshopMetadataInvalidResponse, Retryable: true, Detail: "Steam returned malformed metadata"}
	}
	return nil
}

type publishedFileResponse struct {
	Response struct {
		Details []struct {
			PublishedFileID string `json:"publishedfileid"`
			Result          int    `json:"result"`
			FileType        int    `json:"file_type"`
			ConsumerAppID   uint32 `json:"consumer_app_id"`
			Title           string `json:"title"`
			FileSize        int64  `json:"file_size"`
			TimeUpdated     int64  `json:"time_updated"`
			Tags            []struct {
				Tag string `json:"tag"`
			} `json:"tags"`
		} `json:"publishedfiledetails"`
	} `json:"response"`
}

type collectionResponse struct {
	Response struct {
		Details []struct {
			Result   int `json:"result"`
			Children []struct {
				PublishedFileID string `json:"publishedfileid"`
			} `json:"children"`
		} `json:"collectiondetails"`
	} `json:"response"`
}
