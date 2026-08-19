package tinyfish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultSearchBaseURL = "https://api.search.tinyfish.ai"
	DefaultFetchBaseURL  = "https://api.fetch.tinyfish.ai"
	DefaultAgentBaseURL  = "https://agent.tinyfish.ai"
	// MaxRetryAfterDelay caps provider retry hints before they reach workers or direct callers.
	MaxRetryAfterDelay        = 30 * time.Minute
	maxRequestBodyBytes       = 512 * 1024
	maxResponseBodyBytes      = 2 * 1024 * 1024
	maxSearchQueryBytes       = 4096
	maxSearchLocationBytes    = 512
	maxFetchURLBytes          = 4096
	maxAutomationGoalBytes    = 8192
	maxAutomationURLBytes     = 4096
	maxAutomationProfileBytes = 256
	maxAutomationRunIDBytes   = 256
	maxProviderErrorCode      = 80
	maxProviderErrorMessage   = 512
)

type Client struct {
	APIKey        string
	SearchBaseURL string
	FetchBaseURL  string
	AgentBaseURL  string
	HTTPClient    *http.Client
}

type SearchRequest struct {
	Query    string `json:"query"`
	Location string `json:"location,omitempty"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

type SearchResult struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Snippet  string `json:"snippet,omitempty"`
	SiteName string `json:"site_name,omitempty"`
	Position int    `json:"position,omitempty"`
}

type FetchRequest struct {
	URLs   []string `json:"urls"`
	Format string   `json:"format,omitempty"`
}

type FetchResponse struct {
	Results []FetchResult `json:"results"`
	Errors  []FetchError  `json:"errors,omitempty"`
}

type FetchResult struct {
	URL      string `json:"url"`
	Content  string `json:"content,omitempty"`
	Markdown string `json:"markdown,omitempty"`
	Text     string `json:"text,omitempty"`
	Title    string `json:"title,omitempty"`
}

type FetchError struct {
	URL     string `json:"url,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type AutomationRequest struct {
	URL            string         `json:"url,omitempty"`
	Goal           string         `json:"goal"`
	BrowserProfile string         `json:"browser_profile,omitempty"`
	WebhookURL     string         `json:"webhook_url,omitempty"`
	OutputSchema   map[string]any `json:"output_schema,omitempty"`
	AgentConfig    map[string]any `json:"agent_config,omitempty"`
	CaptureConfig  map[string]any `json:"capture_config,omitempty"`
}

type AutomationResponse struct {
	RunID string          `json:"run_id"`
	Error *TinyFishError  `json:"error,omitempty"`
	Raw   json.RawMessage `json:"-"`
}

type AutomationRunResponse struct {
	RunID        string          `json:"run_id"`
	Status       string          `json:"status"`
	StartedAt    string          `json:"started_at,omitempty"`
	FinishedAt   string          `json:"finished_at,omitempty"`
	NumOfSteps   int             `json:"num_of_steps,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
	ResultJSON   json.RawMessage `json:"resultJson,omitempty"`
	Error        *TinyFishError  `json:"error,omitempty"`
	Raw          json.RawMessage `json:"-"`
	Polls        int             `json:"-"`
	Mode         string          `json:"-"`
	CancelStatus string          `json:"-"`
}

type AutomationCancelResponse struct {
	RunID       string          `json:"run_id"`
	Status      string          `json:"status"`
	CancelledAt string          `json:"cancelled_at,omitempty"`
	Message     string          `json:"message,omitempty"`
	Error       *TinyFishError  `json:"error,omitempty"`
	Raw         json.RawMessage `json:"-"`
}

type TinyFishError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type APIError struct {
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" && e.Message != "" {
		return fmt.Sprintf("tinyfish API error %s: %s", e.Code, e.Message)
	}
	if e.Message != "" {
		return "tinyfish API error: " + e.Message
	}
	if e.Code != "" {
		return "tinyfish API error: " + e.Code
	}
	return "tinyfish API error"
}

type HTTPError struct {
	Method          string
	StatusCode      int
	Message         string
	RetryAfterDelay time.Duration
}

func (e *HTTPError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return fmt.Sprintf("tinyfish %s failed with %d: %s", e.Method, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("tinyfish %s failed with %d", e.Method, e.StatusCode)
}

func (e *HTTPError) RetryAfter() time.Duration {
	if e == nil {
		return 0
	}
	return e.RetryAfterDelay
}

func (c Client) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	request.Query = strings.TrimSpace(strings.ToValidUTF8(request.Query, ""))
	request.Location = strings.TrimSpace(strings.ToValidUTF8(request.Location, ""))
	if request.Query == "" {
		return SearchResponse{}, fmt.Errorf("search query is required")
	}
	if len(request.Query) > maxSearchQueryBytes {
		return SearchResponse{}, fmt.Errorf("tinyfish search query too large: limit %d bytes", maxSearchQueryBytes)
	}
	if len(request.Location) > maxSearchLocationBytes {
		return SearchResponse{}, fmt.Errorf("tinyfish search location too large: limit %d bytes", maxSearchLocationBytes)
	}

	endpoint, err := url.Parse(c.searchBaseURL())
	if err != nil {
		return SearchResponse{}, err
	}
	query := endpoint.Query()
	query.Set("query", request.Query)
	if request.Location != "" {
		query.Set("location", request.Location)
	}
	endpoint.RawQuery = query.Encode()

	var response SearchResponse
	err = c.do(ctx, http.MethodGet, endpoint.String(), nil, &response)
	return response, err
}

func (c Client) Fetch(ctx context.Context, request FetchRequest) (FetchResponse, error) {
	if len(request.URLs) == 0 {
		return FetchResponse{}, fmt.Errorf("at least one URL is required")
	}
	if len(request.URLs) > 10 {
		return FetchResponse{}, fmt.Errorf("fetch accepts at most 10 URLs per request")
	}
	for index, rawURL := range request.URLs {
		cleanURL := strings.TrimSpace(strings.ToValidUTF8(rawURL, ""))
		if cleanURL == "" {
			return FetchResponse{}, fmt.Errorf("tinyfish fetch url is required at index %d", index)
		}
		if len(cleanURL) > maxFetchURLBytes {
			return FetchResponse{}, fmt.Errorf("tinyfish fetch url too large at index %d: limit %d bytes", index, maxFetchURLBytes)
		}
		request.URLs[index] = cleanURL
	}

	var response FetchResponse
	err := c.do(ctx, http.MethodPost, c.fetchBaseURL(), request, &response)
	return response, err
}

func (c Client) StartAutomation(ctx context.Context, request AutomationRequest) (AutomationResponse, error) {
	validated, err := validateAutomationRequest(request)
	if err != nil {
		return AutomationResponse{}, err
	}

	var response AutomationResponse
	err = c.do(ctx, http.MethodPost, strings.TrimRight(c.agentBaseURL(), "/")+"/v1/automation/run-async", validated, &response)
	if response.Error != nil {
		return response, automationAPIError(response.Error)
	}
	return response, err
}

func (c Client) GetAutomationRun(ctx context.Context, runID string) (AutomationRunResponse, error) {
	validatedRunID, err := validateAutomationRunID(runID)
	if err != nil {
		return AutomationRunResponse{}, err
	}

	var response AutomationRunResponse
	err = c.do(ctx, http.MethodGet, strings.TrimRight(c.agentBaseURL(), "/")+"/v1/runs/"+url.PathEscape(validatedRunID), nil, &response)
	if response.Error != nil {
		return response, automationAPIError(response.Error)
	}
	return response, err
}

func (c Client) CancelAutomation(ctx context.Context, runID string) (AutomationCancelResponse, error) {
	validatedRunID, err := validateAutomationRunID(runID)
	if err != nil {
		return AutomationCancelResponse{}, err
	}

	var response AutomationCancelResponse
	err = c.do(ctx, http.MethodPost, strings.TrimRight(c.agentBaseURL(), "/")+"/v1/runs/"+url.PathEscape(validatedRunID)+"/cancel", nil, &response)
	if response.Error != nil {
		return response, automationAPIError(response.Error)
	}
	return response, err
}

func (c Client) RunAutomation(ctx context.Context, request AutomationRequest) (AutomationRunResponse, error) {
	validated, err := validateAutomationRequest(request)
	if err != nil {
		return AutomationRunResponse{}, err
	}

	var response AutomationRunResponse
	err = c.do(ctx, http.MethodPost, strings.TrimRight(c.agentBaseURL(), "/")+"/v1/automation/run", validated, &response)
	if response.Error != nil {
		return response, automationAPIError(response.Error)
	}
	if strings.EqualFold(response.Status, "FAILED") || strings.EqualFold(response.Status, "CANCELLED") {
		return response, fmt.Errorf("tinyfish automation finished with status %s", response.Status)
	}
	return response, err
}

func validateAutomationRequest(request AutomationRequest) (AutomationRequest, error) {
	request.Goal = strings.TrimSpace(strings.ToValidUTF8(request.Goal, ""))
	request.URL = strings.TrimSpace(strings.ToValidUTF8(request.URL, ""))
	request.WebhookURL = strings.TrimSpace(strings.ToValidUTF8(request.WebhookURL, ""))
	request.BrowserProfile = strings.TrimSpace(strings.ToValidUTF8(request.BrowserProfile, ""))

	if request.Goal == "" {
		return AutomationRequest{}, fmt.Errorf("automation goal is required")
	}
	if len(request.Goal) > maxAutomationGoalBytes {
		return AutomationRequest{}, fmt.Errorf("tinyfish automation goal too large: limit %d bytes", maxAutomationGoalBytes)
	}
	if len(request.URL) > maxAutomationURLBytes {
		return AutomationRequest{}, fmt.Errorf("tinyfish automation url too large: limit %d bytes", maxAutomationURLBytes)
	}
	if len(request.WebhookURL) > maxAutomationURLBytes {
		return AutomationRequest{}, fmt.Errorf("tinyfish automation webhook url too large: limit %d bytes", maxAutomationURLBytes)
	}
	if len(request.BrowserProfile) > maxAutomationProfileBytes {
		return AutomationRequest{}, fmt.Errorf("tinyfish automation browser profile too large: limit %d bytes", maxAutomationProfileBytes)
	}
	return request, nil
}

func validateAutomationRunID(runID string) (string, error) {
	cleanRunID := strings.TrimSpace(strings.ToValidUTF8(runID, ""))
	if cleanRunID == "" {
		return "", fmt.Errorf("automation run id is required")
	}
	if len(cleanRunID) > maxAutomationRunIDBytes {
		return "", fmt.Errorf("tinyfish automation run id too large: limit %d bytes", maxAutomationRunIDBytes)
	}
	return cleanRunID, nil
}

func (c Client) do(ctx context.Context, method, endpoint string, body any, target any) error {
	if strings.TrimSpace(c.APIKey) == "" {
		return fmt.Errorf("tinyfish API key is required")
	}

	var payload *bytes.Reader
	if body == nil {
		payload = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		if len(encoded) > maxRequestBodyBytes {
			return fmt.Errorf("tinyfish request body too large: limit %d bytes", maxRequestBodyBytes)
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Error TinyFishError `json:"error"`
		}
		if err := decodeTinyFishErrorEnvelope(resp.Body, &apiErr); err != nil {
			apiErr.Error.Message = err.Error()
		}
		return &HTTPError{
			Method:          method,
			StatusCode:      resp.StatusCode,
			Message:         boundedProviderErrorMessage(apiErr.Error.Message),
			RetryAfterDelay: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now),
		}
	}

	if target == nil {
		return nil
	}
	return decodeTinyFishJSON(resp.Body, target)
}

func decodeTinyFishErrorEnvelope(reader io.Reader, target any) error {
	data, err := readTinyFishResponseBody(reader)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func decodeTinyFishJSON(reader io.Reader, target any) error {
	data, err := readTinyFishResponseBody(reader)
	if err != nil {
		return err
	}
	if err := embeddedTinyFishError(data); err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func embeddedTinyFishError(data []byte) error {
	var envelope struct {
		Error *TinyFishError `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil
	}
	if envelope.Error == nil || (strings.TrimSpace(envelope.Error.Code) == "" && strings.TrimSpace(envelope.Error.Message) == "") {
		return nil
	}
	return &APIError{
		Code:    boundedProviderErrorCode(envelope.Error.Code),
		Message: boundedProviderErrorMessage(envelope.Error.Message),
	}
}

func automationAPIError(value *TinyFishError) error {
	if value == nil {
		return nil
	}
	return fmt.Errorf("tinyfish automation error %s: %s", boundedProviderErrorCode(value.Code), boundedProviderErrorMessage(value.Message))
}

func boundedProviderErrorCode(value string) string {
	return boundedProviderErrorString(value, maxProviderErrorCode)
}

func boundedProviderErrorMessage(value string) string {
	return boundedProviderErrorString(value, maxProviderErrorMessage)
}

func boundedProviderErrorString(value string, limit int) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	if limit <= 0 || len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8RuneStart(value[limit]) {
		limit--
	}
	return strings.TrimSpace(value[:limit])
}

func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

func readTinyFishResponseBody(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxResponseBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBodyBytes {
		return nil, fmt.Errorf("tinyfish response body too large: limit %d bytes", maxResponseBodyBytes)
	}
	return data, nil
}

func parseRetryAfter(value string, now func() time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return boundedRetryAfter(time.Duration(seconds) * time.Second)
	}
	if now == nil {
		now = time.Now
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		delay := retryAt.Sub(now())
		if delay > 0 {
			return boundedRetryAfter(delay)
		}
	}
	return 0
}

func boundedRetryAfter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	if delay > MaxRetryAfterDelay {
		return MaxRetryAfterDelay
	}
	return delay
}

func (c Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return newDefaultHTTPClient(30 * time.Second)
}

func (c Client) searchBaseURL() string {
	if c.SearchBaseURL != "" {
		return c.SearchBaseURL
	}
	return DefaultSearchBaseURL
}

func (c Client) fetchBaseURL() string {
	if c.FetchBaseURL != "" {
		return c.FetchBaseURL
	}
	return DefaultFetchBaseURL
}

func (c Client) agentBaseURL() string {
	if c.AgentBaseURL != "" {
		return c.AgentBaseURL
	}
	return DefaultAgentBaseURL
}
