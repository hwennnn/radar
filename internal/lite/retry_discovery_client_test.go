package lite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hwennnn/radar/internal/tinyfish"
)

func TestRetryingTinyFishDiscoveryClientRecoversSearchAndFetch(t *testing.T) {
	searchCalls, fetchCalls := 0, 0
	client := NewRetryingTinyFishDiscoveryClient(discoveryClientFake{
		search: func(context.Context, tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
			searchCalls++
			if searchCalls == 1 {
				return tinyfish.SearchResponse{}, &tinyfish.HTTPError{Method: "GET", StatusCode: 503}
			}
			return tinyfish.SearchResponse{Results: []tinyfish.SearchResult{{Title: "careers"}}}, nil
		},
		fetch: func(context.Context, tinyfish.FetchRequest) (tinyfish.FetchResponse, error) {
			fetchCalls++
			if fetchCalls == 1 {
				return tinyfish.FetchResponse{}, temporaryExtractionError{}
			}
			return tinyfish.FetchResponse{}, nil
		},
	}, DiscoveryClientRetryOptions{MaxAttempts: 2})

	search, err := client.Search(context.Background(), tinyfish.SearchRequest{Query: "jobs"})
	if err != nil || searchCalls != 2 || len(search.Results) != 1 {
		t.Fatalf("search=%#v calls=%d err=%v", search, searchCalls, err)
	}
	if _, err := client.Fetch(context.Background(), tinyfish.FetchRequest{URLs: []string{"https://example.test"}}); err != nil || fetchCalls != 2 {
		t.Fatalf("fetch calls=%d err=%v", fetchCalls, err)
	}
}

func TestRetryingTinyFishDiscoveryClientStopsOnPermanentFailure(t *testing.T) {
	calls := 0
	client := NewRetryingTinyFishDiscoveryClient(discoveryClientFake{
		search: func(context.Context, tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
			calls++
			return tinyfish.SearchResponse{}, &tinyfish.HTTPError{Method: "GET", StatusCode: 401}
		},
	}, DiscoveryClientRetryOptions{MaxAttempts: 3})

	if _, err := client.Search(context.Background(), tinyfish.SearchRequest{Query: "jobs"}); err == nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestRetryingTinyFishDiscoveryClientBackoffIsCancellationAware(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := NewRetryingTinyFishDiscoveryClient(discoveryClientFake{
		search: func(context.Context, tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
			return tinyfish.SearchResponse{}, temporaryExtractionError{}
		},
	}, DiscoveryClientRetryOptions{
		MaxAttempts: 2, Delay: time.Minute,
		OnRetry: func(string, int, time.Duration, error) { cancel() },
	})

	started := time.Now()
	_, err := client.Search(ctx, tinyfish.SearchRequest{Query: "jobs"})
	if !errors.Is(err, context.Canceled) || time.Since(started) > time.Second {
		t.Fatalf("error=%v elapsed=%s", err, time.Since(started))
	}
}
