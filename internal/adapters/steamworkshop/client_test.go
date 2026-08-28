package steamworkshop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/L-McKendrick/game-server-platform/internal/domain"
)

func TestClientReadsPublishedFileAndCollectionMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/GetPublishedFileDetails/v1/":
			fmt.Fprint(writer, `{"response":{"publishedfiledetails":[{"publishedfileid":"42","result":1,"file_type":0,"consumer_app_id":107410,"title":"Coop Night","filename":"Coop%20Night.Altis.pbo","file_size":"123","time_updated":1700000000,"tags":[{"tag":"Scenario"},{"tag":"Coop"}]}]}}`)
		case "/GetCollectionDetails/v1/":
			fmt.Fprint(writer, `{"response":{"collectiondetails":[{"result":1,"children":[{"publishedfileid":"42"}]}]}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := NewWithClient(server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	item, err := client.Item(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if !item.Available || item.ConsumerAppID != domain.Arma3WorkshopAppID || item.Filename != "Coop_Night.Altis.pbo" || item.FileSize != 123 || len(item.Tags) != 2 {
		t.Fatalf("item = %#v", item)
	}
	children, err := client.CollectionChildren(context.Background(), 9)
	if err != nil || len(children) != 1 || children[0] != 42 {
		t.Fatalf("children = %v, %v", children, err)
	}
}

func TestClientBatchesPublishedFilesAndRestoresRequestedOrder(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if err := request.ParseForm(); err != nil {
			t.Fatal(err)
		}
		count, _ := strconv.Atoi(request.FormValue("itemcount"))
		fmt.Fprint(writer, `{"response":{"publishedfiledetails":[`)
		for index := count - 1; index >= 0; index-- {
			if index != count-1 {
				fmt.Fprint(writer, ",")
			}
			fmt.Fprintf(writer, `{"publishedfileid":%q,"result":1,"consumer_app_id":107410,"title":"Mod","tags":[{"tag":"Mod"}]}`, request.FormValue(fmt.Sprintf("publishedfileids[%d]", index)))
		}
		fmt.Fprint(writer, `]}}`)
	}))
	defer server.Close()
	client, err := NewWithClient(server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]uint64, 101)
	for index := range ids {
		ids[index] = uint64(index + 1)
	}
	items, err := client.Items(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(items) != len(ids) {
		t.Fatalf("calls = %d, items = %d", calls.Load(), len(items))
	}
	for index, item := range items {
		if item.PublishedFileID != ids[index] {
			t.Fatalf("item %d ID = %d, want %d", index, item.PublishedFileID, ids[index])
		}
	}
}

func TestClientClassifiesRateLimitAsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, err := NewWithClient(server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Item(context.Background(), 42)
	var metadataErr domain.WorkshopMetadataError
	if !errors.As(err, &metadataErr) || metadataErr.Code != domain.WorkshopMetadataRateLimited || !metadataErr.Retryable {
		t.Fatalf("Item() error = %#v", err)
	}
}
