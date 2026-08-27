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
const maximumPublishedFileBatch = 100

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
	items, err := client.Items(ctx, []uint64{publishedFileID})
	if err != nil {
		return domain.WorkshopItem{}, err
	}
	return items[0], nil
}

// Items batches Steam's published-file endpoint to bound collection latency,
// request count, and Lambda cost while preserving caller order exactly.
func (client *Client) Items(ctx context.Context, publishedFileIDs []uint64) ([]domain.WorkshopItem, error) {
	if len(publishedFileIDs) == 0 || len(publishedFileIDs) > domain.MaximumWorkshopChildren {
		return nil, fmt.Errorf("published file batch must contain 1 to %d IDs", domain.MaximumWorkshopChildren)
	}
	seen := make(map[uint64]struct{}, len(publishedFileIDs))
	items := make([]domain.WorkshopItem, 0, len(publishedFileIDs))
	for offset := 0; offset < len(publishedFileIDs); offset += maximumPublishedFileBatch {
		end := min(offset+maximumPublishedFileBatch, len(publishedFileIDs))
		values := url.Values{"itemcount": {strconv.Itoa(end - offset)}}
		for index, id := range publishedFileIDs[offset:end] {
			if id == 0 {
				return nil, fmt.Errorf("published file ID is required")
			}
			if _, duplicate := seen[id]; duplicate {
				return nil, fmt.Errorf("published file IDs must be unique")
			}
			seen[id] = struct{}{}
			values.Set(fmt.Sprintf("publishedfileids[%d]", index), strconv.FormatUint(id, 10))
		}
		batch, err := client.publishedFiles(ctx, values, publishedFileIDs[offset:end])
		if err != nil {
			return nil, err
		}
		items = append(items, batch...)
	}
	return items, nil
}

func (client *Client) publishedFiles(ctx context.Context, values url.Values, expected []uint64) ([]domain.WorkshopItem, error) {
	var response publishedFileResponse
	if err := client.post(ctx, "/GetPublishedFileDetails/v1/", values, &response); err != nil {
		return nil, err
	}
	if len(response.Response.Details) != len(expected) {
		return nil, domain.WorkshopMetadataError{Code: domain.WorkshopMetadataInvalidResponse, Retryable: true, Detail: "Steam returned incomplete item metadata"}
	}
	byID := make(map[uint64]domain.WorkshopItem, len(expected))
	for _, detail := range response.Response.Details {
		id, err := strconv.ParseUint(detail.PublishedFileID, 10, 64)
		if err != nil || id == 0 {
			return nil, domain.WorkshopMetadataError{Code: domain.WorkshopMetadataInvalidResponse, Retryable: true, Detail: "Steam returned mismatched item metadata"}
		}
		item := domain.WorkshopItem{PublishedFileID: id, ConsumerAppID: detail.ConsumerAppID, Title: strings.TrimSpace(detail.Title), FileSize: detail.FileSize, Available: detail.Result == 1, Collection: detail.FileType == 2}
		if detail.TimeUpdated > 0 {
			item.UpdatedAt = time.Unix(detail.TimeUpdated, 0).UTC()
		}
		for _, tag := range detail.Tags {
			item.Tags = append(item.Tags, tag.Tag)
		}
		if _, duplicate := byID[id]; duplicate {
			return nil, domain.WorkshopMetadataError{Code: domain.WorkshopMetadataInvalidResponse, Retryable: true, Detail: "Steam returned duplicate item metadata"}
		}
		byID[id] = item
	}
	items := make([]domain.WorkshopItem, 0, len(expected))
	for _, id := range expected {
		item, ok := byID[id]
		if !ok {
			return nil, domain.WorkshopMetadataError{Code: domain.WorkshopMetadataInvalidResponse, Retryable: true, Detail: "Steam omitted requested item metadata"}
		}
		items = append(items, item)
	}
	return items, nil
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
