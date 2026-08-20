package tinyfish

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSearchUsesAPIKeyAndQueryParams(t *testing.T) {
	var gotKey, gotQuery, gotLocation string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		gotQuery = r.URL.Query().Get("query")
		gotLocation = r.URL.Query().Get("location")
		_ = json.NewEncoder(w).Encode(SearchResponse{
			Results: []SearchResult{{Title: "Backend Intern", URL: "https://example.com/job", Snippet: "Go infra internship"}},
		})
	}))
	defer server.Close()

	client := Client{
		APIKey:        "test-key",
		SearchBaseURL: server.URL,
		HTTPClient:    server.Client(),
	}
	response, err := client.Search(context.Background(), SearchRequest{
		Query:    `"software engineer intern" site:example.com`,
		Location: "US",
	})
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if gotKey != "test-key" {
		t.Fatalf("X-API-Key = %q, want test-key", gotKey)
	}
	if gotQuery != `"software engineer intern" site:example.com` {
		t.Fatalf("query = %q", gotQuery)
	}
	if gotLocation != "US" {
		t.Fatalf("location = %q", gotLocation)
	}
	if len(response.Results) != 1 || response.Results[0].URL == "" {
		t.Fatalf("response = %#v, want one search result", response)
	}
}

func TestSearchRejectsOversizedQueryBeforeHTTP(t *testing.T) {
	calls := 0
	client := Client{
		APIKey:        "test-key",
		SearchBaseURL: "https://example.invalid/search",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("unexpected HTTP call")
		})},
	}

	_, err := client.Search(context.Background(), SearchRequest{Query: strings.Repeat("software engineer intern ", maxSearchQueryBytes)})

	if err == nil || !strings.Contains(err.Error(), "tinyfish search query too large") {
		t.Fatalf("Search() error = %v, want oversized query rejection", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want validation before request", calls)
	}
}

func TestSearchRejectsOversizedLocationBeforeHTTP(t *testing.T) {
	calls := 0
	client := Client{
		APIKey:        "test-key",
		SearchBaseURL: "https://example.invalid/search",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("unexpected HTTP call")
		})},
	}

	_, err := client.Search(context.Background(), SearchRequest{
		Query:    "software engineer intern",
		Location: strings.Repeat("Singapore ", maxSearchLocationBytes),
	})

	if err == nil || !strings.Contains(err.Error(), "tinyfish search location too large") {
		t.Fatalf("Search() error = %v, want oversized location rejection", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want validation before request", calls)
	}
}

func TestDefaultHTTPClientBlocksPrivateTargets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(SearchResponse{})
	}))
	defer server.Close()

	client := Client{
		APIKey:        "test-key",
		SearchBaseURL: server.URL,
	}
	_, err := client.Search(context.Background(), SearchRequest{Query: "software intern"})
	if err == nil {
		t.Fatal("Search() error = nil, want private target rejection")
	}
	if !strings.Contains(err.Error(), "resolved private address blocked") {
		t.Fatalf("Search() error = %q, want private target rejection", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestHTTPClientPreservesInjectedClient(t *testing.T) {
	provided := &http.Client{Timeout: 123 * time.Millisecond}
	client := Client{HTTPClient: provided}
	if got := client.httpClient(); got != provided {
		t.Fatalf("httpClient() = %#v, want provided client", got)
	}
}

func TestFetchPostsURLBatch(t *testing.T) {
	var gotKey string
	var gotRequest FetchRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(FetchResponse{
			Results: []FetchResult{{URL: "https://example.com/job", Content: "Clean job text"}},
		})
	}))
	defer server.Close()

	client := Client{
		APIKey:       "test-key",
		FetchBaseURL: server.URL,
		HTTPClient:   server.Client(),
	}
	response, err := client.Fetch(context.Background(), FetchRequest{
		URLs:   []string{"https://example.com/job"},
		Format: "markdown",
	})
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if gotKey != "test-key" {
		t.Fatalf("X-API-Key = %q, want test-key", gotKey)
	}
	if len(gotRequest.URLs) != 1 || gotRequest.Format != "markdown" {
		t.Fatalf("request = %#v", gotRequest)
	}
	if len(response.Results) != 1 || response.Results[0].Content == "" {
		t.Fatalf("response = %#v, want extracted content", response)
	}
}

func TestFetchRejectsEmptyURLBeforeHTTP(t *testing.T) {
	calls := 0
	client := Client{
		APIKey:       "test-key",
		FetchBaseURL: "https://example.invalid/fetch",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("unexpected HTTP call")
		})},
	}

	_, err := client.Fetch(context.Background(), FetchRequest{URLs: []string{"   "}})

	if err == nil || !strings.Contains(err.Error(), "tinyfish fetch url is required") {
		t.Fatalf("Fetch() error = %v, want empty URL rejection", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want validation before request", calls)
	}
}

func TestFetchRejectsOversizedURLBeforeHTTP(t *testing.T) {
	calls := 0
	client := Client{
		APIKey:       "test-key",
		FetchBaseURL: "https://example.invalid/fetch",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("unexpected HTTP call")
		})},
	}

	_, err := client.Fetch(context.Background(), FetchRequest{URLs: []string{"https://example.com/" + strings.Repeat("jobs/", maxFetchURLBytes)}})

	if err == nil || !strings.Contains(err.Error(), "tinyfish fetch url too large") {
		t.Fatalf("Fetch() error = %v, want oversized URL rejection", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want validation before request", calls)
	}
}

func TestFetchRejectsOversizedRequestBody(t *testing.T) {
	client := Client{
		APIKey:       "test-key",
		FetchBaseURL: "https://example.invalid",
		HTTPClient:   http.DefaultClient,
	}

	_, err := client.Fetch(context.Background(), FetchRequest{
		URLs:   []string{"https://example.com/job"},
		Format: strings.Repeat("markdown", maxRequestBodyBytes/8),
	})
	if err == nil || !strings.Contains(err.Error(), "tinyfish request body too large") {
		t.Fatalf("Fetch() error = %v, want oversized request body error", err)
	}
}

func TestFetchReturnsHTTPErrorWithRetryAfterHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "rate limited"},
		})
	}))
	defer server.Close()

	client := Client{
		APIKey:       "test-key",
		FetchBaseURL: server.URL,
		HTTPClient:   server.Client(),
	}
	_, err := client.Fetch(context.Background(), FetchRequest{URLs: []string{"https://example.com/job"}})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %[1]v, want HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests || httpErr.Message != "rate limited" {
		t.Fatalf("http error = %#v, want 429 rate limited", httpErr)
	}
	if httpErr.RetryAfter() != 2*time.Minute {
		t.Fatalf("retry after = %s, want 2m", httpErr.RetryAfter())
	}
}

func TestSearchReturnsEmbeddedTinyFishError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "rate_limited", "message": "search quota exhausted"},
		})
	}))
	defer server.Close()

	client := Client{
		APIKey:        "test-key",
		SearchBaseURL: server.URL,
		HTTPClient:    server.Client(),
	}
	_, err := client.Search(context.Background(), SearchRequest{Query: "software intern"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T %[1]v, want APIError", err)
	}
	if apiErr.Code != "rate_limited" || apiErr.Message != "search quota exhausted" {
		t.Fatalf("api error = %#v, want embedded error details", apiErr)
	}
}

func TestSearchBoundsEmbeddedTinyFishErrorEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"code":    strings.Repeat("rate統", 80),
				"message": "   " + strings.Repeat("quota統exhausted ", 120) + "   ",
			},
		})
	}))
	defer server.Close()

	client := Client{
		APIKey:        "test-key",
		SearchBaseURL: server.URL,
		HTTPClient:    server.Client(),
	}
	_, err := client.Search(context.Background(), SearchRequest{Query: "software intern"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T %[1]v, want APIError", err)
	}
	if len(apiErr.Code) > maxProviderErrorCode || !utf8.ValidString(apiErr.Code) {
		t.Fatalf("api error code len/valid = %d/%v", len(apiErr.Code), utf8.ValidString(apiErr.Code))
	}
	if len(apiErr.Message) > maxProviderErrorMessage || !utf8.ValidString(apiErr.Message) {
		t.Fatalf("api error message len/valid = %d/%v", len(apiErr.Message), utf8.ValidString(apiErr.Message))
	}
	if strings.HasPrefix(apiErr.Message, " ") || strings.HasSuffix(apiErr.Message, " ") {
		t.Fatalf("api error message = %q, want trimmed evidence", apiErr.Message)
	}
}

func TestFetchReturnsEmbeddedTinyFishError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"code": "provider_unavailable", "message": "fetch backend unavailable"},
		})
	}))
	defer server.Close()

	client := Client{
		APIKey:       "test-key",
		FetchBaseURL: server.URL,
		HTTPClient:   server.Client(),
	}
	_, err := client.Fetch(context.Background(), FetchRequest{URLs: []string{"https://example.com/job"}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %T %[1]v, want APIError", err)
	}
	if apiErr.Code != "provider_unavailable" || apiErr.Message != "fetch backend unavailable" {
		t.Fatalf("api error = %#v, want embedded error details", apiErr)
	}
}

func TestFetchBoundsHTTPErrorEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": "   " + strings.Repeat("provider統overloaded ", 120) + "   "},
		})
	}))
	defer server.Close()

	client := Client{
		APIKey:       "test-key",
		FetchBaseURL: server.URL,
		HTTPClient:   server.Client(),
	}
	_, err := client.Fetch(context.Background(), FetchRequest{URLs: []string{"https://example.com/job"}})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %T %[1]v, want HTTPError", err)
	}
	if len(httpErr.Message) > maxProviderErrorMessage || !utf8.ValidString(httpErr.Message) {
		t.Fatalf("http error message len/valid = %d/%v", len(httpErr.Message), utf8.ValidString(httpErr.Message))
	}
	if strings.HasPrefix(httpErr.Message, " ") || strings.HasSuffix(httpErr.Message, " ") {
		t.Fatalf("http error message = %q, want trimmed evidence", httpErr.Message)
	}
}

func TestSearchRejectsOversizedResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(SearchResponse{
			Results: []SearchResult{{
				Title:   "Backend Intern",
				URL:     "https://example.com/job",
				Snippet: strings.Repeat("large ", (maxResponseBodyBytes/6)+1),
			}},
		})
	}))
	defer server.Close()

	client := Client{
		APIKey:        "test-key",
		SearchBaseURL: server.URL,
		HTTPClient:    server.Client(),
	}
	_, err := client.Search(context.Background(), SearchRequest{Query: "software intern"})
	if err == nil || !strings.Contains(err.Error(), "tinyfish response body too large") {
		t.Fatalf("Search error = %v, want oversized response error", err)
	}
}

func TestParseRetryAfterSupportsHTTPDate(t *testing.T) {
	now := time.Date(2026, 6, 23, 17, 0, 0, 0, time.UTC)
	retryAt := now.Add(90 * time.Second).Format(http.TimeFormat)
	if got := parseRetryAfter(retryAt, func() time.Time { return now }); got != 90*time.Second {
		t.Fatalf("retry after = %s, want 90s", got)
	}
}

func TestParseRetryAfterCapsRunawayDelay(t *testing.T) {
	now := time.Date(2026, 6, 23, 17, 0, 0, 0, time.UTC)
	if got := parseRetryAfter("86400", func() time.Time { return now }); got != MaxRetryAfterDelay {
		t.Fatalf("numeric retry after = %s, want cap %s", got, MaxRetryAfterDelay)
	}
	retryAt := now.Add(24 * time.Hour).Format(http.TimeFormat)
	if got := parseRetryAfter(retryAt, func() time.Time { return now }); got != MaxRetryAfterDelay {
		t.Fatalf("date retry after = %s, want cap %s", got, MaxRetryAfterDelay)
	}
}

func TestStartAutomationUsesAgentEndpoint(t *testing.T) {
	var gotKey string
	var gotRequest AutomationRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		if r.URL.Path != "/v1/automation/run-async" {
			t.Fatalf("path = %q, want /v1/automation/run-async", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(AutomationResponse{RunID: "run_123"})
	}))
	defer server.Close()

	client := Client{
		APIKey:       "test-key",
		AgentBaseURL: server.URL,
		HTTPClient:   server.Client(),
	}
	response, err := client.StartAutomation(context.Background(), AutomationRequest{
		URL:  "https://example.com/careers",
		Goal: "Find software engineering intern jobs and return structured JSON.",
	})
	if err != nil {
		t.Fatalf("StartAutomation returned error: %v", err)
	}
	if gotKey != "test-key" {
		t.Fatalf("X-API-Key = %q, want test-key", gotKey)
	}
	if gotRequest.Goal == "" || gotRequest.URL == "" {
		t.Fatalf("request = %#v", gotRequest)
	}
	if response.RunID != "run_123" {
		t.Fatalf("RunID = %q, want run_123", response.RunID)
	}
}

func TestStartAutomationBoundsProviderErrorEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(AutomationResponse{Error: &TinyFishError{
			Code:    strings.Repeat("blocked統", 40),
			Message: "   " + strings.Repeat("browser統blocked ", 120) + "   ",
		}})
	}))
	defer server.Close()

	client := Client{
		APIKey:       "test-key",
		AgentBaseURL: server.URL,
		HTTPClient:   server.Client(),
	}
	_, err := client.StartAutomation(context.Background(), AutomationRequest{
		URL:  "https://example.com/careers",
		Goal: "Find jobs",
	})
	if err == nil {
		t.Fatal("StartAutomation() error = nil, want bounded provider error")
	}
	text := err.Error()
	if len(text) > len("tinyfish automation error : ")+maxProviderErrorCode+maxProviderErrorMessage {
		t.Fatalf("automation error len = %d, want bounded text", len(text))
	}
	if !utf8.ValidString(text) {
		t.Fatalf("automation error is not valid UTF-8: %q", text)
	}
	if strings.Contains(text, strings.Repeat("browser", 20)) {
		t.Fatalf("automation error appears unbounded: %q", text)
	}
}

func TestStartAutomationRejectsOversizedRequestBody(t *testing.T) {
	client := Client{
		APIKey:       "test-key",
		AgentBaseURL: "https://example.invalid",
		HTTPClient:   http.DefaultClient,
	}

	_, err := client.StartAutomation(context.Background(), AutomationRequest{
		URL:  "https://example.com/careers",
		Goal: "Find software engineering intern jobs.",
		OutputSchema: map[string]any{
			"description": strings.Repeat("job field ", maxRequestBodyBytes/8),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "tinyfish request body too large") {
		t.Fatalf("StartAutomation() error = %v, want oversized request body error", err)
	}
}

func TestStartAutomationRejectsOversizedGoalBeforeHTTP(t *testing.T) {
	calls := 0
	client := Client{
		APIKey:       "test-key",
		AgentBaseURL: "https://example.invalid",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("unexpected HTTP call")
		})},
	}

	_, err := client.StartAutomation(context.Background(), AutomationRequest{
		URL:  "https://example.com/careers",
		Goal: strings.Repeat("Find software engineering intern jobs. ", maxAutomationGoalBytes),
	})

	if err == nil || !strings.Contains(err.Error(), "tinyfish automation goal too large") {
		t.Fatalf("StartAutomation() error = %v, want oversized goal rejection", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want validation before request", calls)
	}
}

func TestRunAutomationRejectsOversizedURLBeforeHTTP(t *testing.T) {
	calls := 0
	client := Client{
		APIKey:       "test-key",
		AgentBaseURL: "https://example.invalid",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("unexpected HTTP call")
		})},
	}

	_, err := client.RunAutomation(context.Background(), AutomationRequest{
		URL:  "https://example.com/" + strings.Repeat("careers/", maxAutomationURLBytes),
		Goal: "Extract jobs.",
	})

	if err == nil || !strings.Contains(err.Error(), "tinyfish automation url too large") {
		t.Fatalf("RunAutomation() error = %v, want oversized URL rejection", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want validation before request", calls)
	}
}

func TestStartAutomationRejectsOversizedWebhookBeforeHTTP(t *testing.T) {
	calls := 0
	client := Client{
		APIKey:       "test-key",
		AgentBaseURL: "https://example.invalid",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("unexpected HTTP call")
		})},
	}

	_, err := client.StartAutomation(context.Background(), AutomationRequest{
		URL:        "https://example.com/careers",
		Goal:       "Extract jobs.",
		WebhookURL: "https://example.com/" + strings.Repeat("webhook/", maxAutomationURLBytes),
	})

	if err == nil || !strings.Contains(err.Error(), "tinyfish automation webhook url too large") {
		t.Fatalf("StartAutomation() error = %v, want oversized webhook rejection", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want validation before request", calls)
	}
}

func TestRunAutomationRejectsOversizedBrowserProfileBeforeHTTP(t *testing.T) {
	calls := 0
	client := Client{
		APIKey:       "test-key",
		AgentBaseURL: "https://example.invalid",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("unexpected HTTP call")
		})},
	}

	_, err := client.RunAutomation(context.Background(), AutomationRequest{
		URL:            "https://example.com/careers",
		Goal:           "Extract jobs.",
		BrowserProfile: strings.Repeat("profile", maxAutomationProfileBytes),
	})

	if err == nil || !strings.Contains(err.Error(), "tinyfish automation browser profile too large") {
		t.Fatalf("RunAutomation() error = %v, want oversized browser profile rejection", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want validation before request", calls)
	}
}

func TestGetAutomationRunUsesRunsEndpoint(t *testing.T) {
	var gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/runs/run_123" {
			t.Fatalf("path = %q, want /v1/runs/run_123", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(AutomationRunResponse{
			RunID:      "run_123",
			Status:     "COMPLETED",
			NumOfSteps: 4,
			Result:     json.RawMessage(`{"jobs":[]}`),
		})
	}))
	defer server.Close()

	client := Client{
		APIKey:       "test-key",
		AgentBaseURL: server.URL,
		HTTPClient:   server.Client(),
	}
	response, err := client.GetAutomationRun(context.Background(), "run_123")
	if err != nil {
		t.Fatalf("GetAutomationRun returned error: %v", err)
	}
	if gotKey != "test-key" {
		t.Fatalf("X-API-Key = %q, want test-key", gotKey)
	}
	if response.RunID != "run_123" || response.Status != "COMPLETED" || response.NumOfSteps != 4 {
		t.Fatalf("response = %#v", response)
	}
}

func TestGetAutomationRunRejectsOversizedRunIDBeforeHTTP(t *testing.T) {
	calls := 0
	client := Client{
		APIKey:       "test-key",
		AgentBaseURL: "https://example.invalid",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("unexpected HTTP call")
		})},
	}

	_, err := client.GetAutomationRun(context.Background(), strings.Repeat("run_", maxAutomationRunIDBytes))

	if err == nil || !strings.Contains(err.Error(), "tinyfish automation run id too large") {
		t.Fatalf("GetAutomationRun() error = %v, want oversized run id rejection", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want validation before request", calls)
	}
}

func TestCancelAutomationUsesCancelEndpoint(t *testing.T) {
	var gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/runs/run_123/cancel" {
			t.Fatalf("path = %q, want /v1/runs/run_123/cancel", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(AutomationCancelResponse{
			RunID:       "run_123",
			Status:      "CANCELLED",
			CancelledAt: "2026-06-23T07:00:00Z",
		})
	}))
	defer server.Close()

	client := Client{
		APIKey:       "test-key",
		AgentBaseURL: server.URL,
		HTTPClient:   server.Client(),
	}
	response, err := client.CancelAutomation(context.Background(), "run_123")
	if err != nil {
		t.Fatalf("CancelAutomation returned error: %v", err)
	}
	if gotKey != "test-key" {
		t.Fatalf("X-API-Key = %q, want test-key", gotKey)
	}
	if response.RunID != "run_123" || response.Status != "CANCELLED" || response.CancelledAt == "" {
		t.Fatalf("response = %#v", response)
	}
}

func TestCancelAutomationRejectsOversizedRunIDBeforeHTTP(t *testing.T) {
	calls := 0
	client := Client{
		APIKey:       "test-key",
		AgentBaseURL: "https://example.invalid",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("unexpected HTTP call")
		})},
	}

	_, err := client.CancelAutomation(context.Background(), strings.Repeat("run_", maxAutomationRunIDBytes))

	if err == nil || !strings.Contains(err.Error(), "tinyfish automation run id too large") {
		t.Fatalf("CancelAutomation() error = %v, want oversized run id rejection", err)
	}
	if calls != 0 {
		t.Fatalf("HTTP calls = %d, want validation before request", calls)
	}
}

func TestRunAutomationUsesBlockingAgentEndpoint(t *testing.T) {
	var gotKey string
	var gotRequest AutomationRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		if r.URL.Path != "/v1/automation/run" {
			t.Fatalf("path = %q, want /v1/automation/run", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(AutomationRunResponse{
			RunID:      "run_456",
			Status:     "COMPLETED",
			NumOfSteps: 5,
			Result:     json.RawMessage(`{"jobs":[{"title":"Backend Intern"}]}`),
		})
	}))
	defer server.Close()

	client := Client{
		APIKey:       "test-key",
		AgentBaseURL: server.URL,
		HTTPClient:   server.Client(),
	}
	response, err := client.RunAutomation(context.Background(), AutomationRequest{
		URL:            "https://example.com/careers",
		Goal:           "Extract software engineering intern jobs as JSON.",
		BrowserProfile: "lite",
		OutputSchema:   map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("RunAutomation returned error: %v", err)
	}
	if gotKey != "test-key" {
		t.Fatalf("X-API-Key = %q, want test-key", gotKey)
	}
	if gotRequest.BrowserProfile != "lite" || gotRequest.OutputSchema["type"] != "object" {
		t.Fatalf("request = %#v", gotRequest)
	}
	if response.RunID != "run_456" || response.Status != "COMPLETED" || response.NumOfSteps != 5 {
		t.Fatalf("response = %#v", response)
	}
	if string(response.Result) == "" {
		t.Fatal("result raw JSON should be preserved")
	}
}
