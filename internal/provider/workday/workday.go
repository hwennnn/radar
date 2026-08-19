package workday

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hwennnn/radar/internal/provider"
)

var (
	htmlTagPattern     = regexp.MustCompile(`<[^>]+>`)
	internLevelPattern = regexp.MustCompile(`\bintern(?:ship)?s?\b|\bco-?op\b`)
)

type Options struct {
	Client        *http.Client
	PageSize      int
	MaxPages      int
	DetailMaxJobs int
	DetailTimeout time.Duration
}

type Engine struct {
	client        *http.Client
	pageSize      int
	maxPages      int
	detailMaxJobs int
	detailTimeout time.Duration
}

func New(opts Options) *Engine {
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Engine{
		client:        client,
		pageSize:      boundedInt(opts.PageSize, 20, 1, 100),
		maxPages:      boundedInt(opts.MaxPages, 5, 1, 20),
		detailMaxJobs: boundedInt(opts.DetailMaxJobs, 12, 0, 100),
		detailTimeout: boundedDuration(opts.DetailTimeout, 4*time.Second, 250*time.Millisecond, 30*time.Second),
	}
}

func (e *Engine) Name() string {
	return "workday-provider"
}

func (e *Engine) Extract(ctx context.Context, source provider.Source) (provider.Result, error) {
	if err := ctx.Err(); err != nil {
		return provider.Result{}, err
	}
	config, err := configFromSource(source)
	if err != nil {
		return provider.Result{}, err
	}
	searchEndpoint, err := joinURL(config.BaseURL, "wday", "cxs", config.Tenant, config.Site, "jobs")
	if err != nil {
		return provider.Result{}, err
	}

	jobs := make([]provider.Posting, 0, e.pageSize)
	searchText := sourceSearchText(source)
	pagesFetched, totalAvailable := 0, 0
	detailAttempts, detailSuccesses, detailFallbacks := 0, 0, 0
	hasMore := false
	for pageNo := 0; pageNo < e.maxPages; pageNo++ {
		offset := pageNo * e.pageSize
		var payload searchResponse
		req := searchRequest{
			AppliedFacets: map[string]any{},
			Limit:         e.pageSize,
			Offset:        offset,
			SearchText:    searchText,
		}
		if err := e.postJSON(ctx, searchEndpoint.String(), req, &payload); err != nil {
			return provider.Result{}, err
		}
		pagesFetched++
		totalAvailable = payload.Total
		if len(payload.JobPostings) == 0 {
			hasMore = false
			break
		}
		for _, posting := range payload.JobPostings {
			fetchDetail := detailAttempts < e.detailMaxJobs
			if fetchDetail {
				detailAttempts++
			}
			job, detailed, err := e.jobPosting(ctx, source, config, searchEndpoint.String(), posting, fetchDetail)
			if err != nil {
				return provider.Result{}, err
			}
			if fetchDetail {
				if detailed {
					detailSuccesses++
				} else {
					detailFallbacks++
				}
			}
			if job.SourceJobID != "" {
				jobs = append(jobs, job)
			}
		}
		hasMore = len(payload.JobPostings) >= e.pageSize && !(payload.Total > 0 && offset+len(payload.JobPostings) >= payload.Total)
		if !hasMore {
			break
		}
	}

	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.88,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "provider_endpoint", Text: "Workday CXS jobs API", URL: searchEndpoint.String()},
		},
		Diagnostics: map[string]string{
			"provider_engine":     e.Name(),
			"workday_tenant":      config.Tenant,
			"workday_site":        config.Site,
			"completeness_status": completenessStatus(hasMore),
			"completeness_reason": completenessReason(hasMore),
			"pages_fetched":       strconv.Itoa(pagesFetched),
			"page_size":           strconv.Itoa(e.pageSize),
			"total_available":     strconv.Itoa(totalAvailable),
			"result_limit":        strconv.Itoa(e.pageSize * e.maxPages),
			"has_more":            strconv.FormatBool(hasMore),
			"detail_limit":        strconv.Itoa(e.detailMaxJobs),
			"detail_attempts":     strconv.Itoa(detailAttempts),
			"detail_successes":    strconv.Itoa(detailSuccesses),
			"detail_fallbacks":    strconv.Itoa(detailFallbacks),
		},
	}, nil
}

func completenessStatus(hasMore bool) string {
	if hasMore {
		return "truncated"
	}
	return "complete"
}

func completenessReason(hasMore bool) string {
	if hasMore {
		return "page_limit_reached"
	}
	return "all_pages_exhausted"
}

func (e *Engine) jobPosting(ctx context.Context, source provider.Source, config Config, searchEndpoint string, posting postingSummary, fetchDetail bool) (provider.Posting, bool, error) {
	var detail postingDetail
	detailURL := ""
	detailed := false
	if fetchDetail {
		detailCtx, cancel := context.WithTimeout(ctx, e.detailTimeout)
		var err error
		detail, detailURL, err = e.postingDetail(detailCtx, config, posting.ExternalPath)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return provider.Posting{}, false, ctx.Err()
			}
			detail = postingDetail{}
		} else {
			detailed = true
		}
	}

	title := firstNonEmptyString(detail.Info.Title, posting.Title)
	if strings.TrimSpace(title) == "" {
		return provider.Posting{}, detailed, nil
	}
	location := firstNonEmptyString(detail.Info.LocationsText, rawText(detail.Info.JobRequisitionLocation), rawText(detail.Info.Location), posting.LocationsText)
	country := workdayCountry(firstNonEmptyString(rawText(detail.Info.Country), location))
	description := cleanHTMLText(detail.Info.JobDescription)
	jobReqID := firstNonEmptyString(detail.Info.JobReqID, posting.JobReqID(), stableJobToken(posting.ExternalPath, title))
	applyURL := firstNonEmptyString(detail.Info.ExternalURL, hostedURL(config, posting.ExternalPath), source.URL)
	employment := employmentFromText(title, firstNonEmptyString(detail.Info.TimeType, posting.TimeType))
	postedAt := parseTimePtr(firstNonEmptyString(rawText(detail.Info.Posted), detail.Info.StartDate, posting.PostedOn))

	evidence := []provider.Evidence{
		{Field: "provider", Text: "Workday CXS jobs API", URL: searchEndpoint},
	}
	if description != "" {
		evidence = append(evidence, provider.Evidence{Field: "description", Text: description, URL: firstNonEmptyString(detailURL, applyURL)})
	}
	if location != "" {
		evidence = append(evidence, provider.Evidence{Field: "location", Text: location, URL: applyURL})
	}

	if detail.Info.CanApply != nil && !*detail.Info.CanApply {
		return provider.Posting{}, detailed, nil
	}

	return provider.Posting{
		SourceJobID:    "workday:" + config.Tenant + ":" + config.Site + ":" + jobReqID,
		Company:        sourceCompany(source, config.Tenant),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		RoleFamily:     inferRoleFamily(title + " " + description),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       postedAt,
		Live:           true,
		Confidence:     0.88,
		Strategy:       provider.TierATS,
		Evidence:       evidence,
	}, detailed, nil
}

func boundedDuration(value, fallback, minValue, maxValue time.Duration) time.Duration {
	if value == 0 {
		value = fallback
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func (e *Engine) postingDetail(ctx context.Context, config Config, externalPath string) (postingDetail, string, error) {
	if strings.TrimSpace(externalPath) == "" {
		return postingDetail{}, "", errors.New("workday external path is required")
	}
	endpoint, err := joinURL(config.BaseURL, "wday", "cxs", config.Tenant, config.Site, strings.TrimLeft(externalPath, "/"))
	if err != nil {
		return postingDetail{}, "", err
	}
	var detail postingDetail
	if err := e.getJSON(ctx, endpoint.String(), &detail); err != nil {
		return postingDetail{}, endpoint.String(), err
	}
	return detail, endpoint.String(), nil
}

func (e *Engine) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s returned HTTP %d", endpoint, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (e *Engine) postJSON(ctx context.Context, endpoint string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s returned HTTP %d", endpoint, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type Config struct {
	BaseURL          string
	Tenant           string
	Site             string
	PublicPathPrefix string
}

type searchRequest struct {
	AppliedFacets map[string]any `json:"appliedFacets"`
	Limit         int            `json:"limit"`
	Offset        int            `json:"offset"`
	SearchText    string         `json:"searchText"`
}

type searchResponse struct {
	Total       int              `json:"total"`
	JobPostings []postingSummary `json:"jobPostings"`
}

type postingSummary struct {
	Title         string   `json:"title"`
	ExternalPath  string   `json:"externalPath"`
	LocationsText string   `json:"locationsText"`
	PostedOn      string   `json:"postedOn"`
	TimeType      string   `json:"timeType"`
	BulletFields  []string `json:"bulletFields"`
}

func (p postingSummary) JobReqID() string {
	for _, field := range p.BulletFields {
		field = strings.TrimSpace(field)
		if field != "" {
			return field
		}
	}
	if token := stableJobToken(p.ExternalPath, p.Title); token != "" {
		if idx := strings.LastIndex(token, "_"); idx >= 0 && idx+1 < len(token) {
			return token[idx+1:]
		}
		return token
	}
	return ""
}

type postingDetail struct {
	Info postingInfo `json:"jobPostingInfo"`
}

type postingInfo struct {
	ID                     string          `json:"id"`
	Title                  string          `json:"title"`
	JobDescription         string          `json:"jobDescription"`
	JobReqID               string          `json:"jobReqId"`
	ExternalURL            string          `json:"externalUrl"`
	LocationsText          string          `json:"locationsText"`
	JobRequisitionLocation json.RawMessage `json:"jobRequisitionLocation"`
	Location               json.RawMessage `json:"location"`
	Country                json.RawMessage `json:"country"`
	TimeType               string          `json:"timeType"`
	Posted                 json.RawMessage `json:"posted"`
	PostedOn               string          `json:"postedOn"`
	StartDate              string          `json:"startDate"`
	CanApply               *bool           `json:"canApply"`
}

func ConfigFromSource(source provider.Source) (Config, error) {
	return configFromSource(source)
}

func configFromSource(source provider.Source) (Config, error) {
	tenant := firstNonEmptyString(source.Metadata["workday_tenant"], source.Metadata["tenant"])
	site := firstNonEmptyString(source.Metadata["workday_site"], source.Metadata["site"])
	baseURL := strings.TrimRight(firstNonEmptyString(source.Metadata["workday_base_url"], source.Metadata["base_url"]), "/")
	publicPrefix := strings.Trim(strings.TrimSpace(source.Metadata["workday_public_path_prefix"]), "/")

	parsed, err := url.Parse(strings.TrimSpace(source.URL))
	if err != nil {
		return Config{}, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return Config{}, fmt.Errorf("invalid workday source url: %s", source.URL)
	}
	if baseURL == "" {
		baseURL = parsed.Scheme + "://" + parsed.Host
	}
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		switch strings.ToLower(part) {
		case "cxs":
			if i+2 < len(parts) {
				tenant = firstNonEmptyString(tenant, parts[i+1])
				site = firstNonEmptyString(site, parts[i+2])
				publicPrefix = firstNonEmptyString(publicPrefix, site)
			}
		case "recruiting":
			if i+2 < len(parts) {
				tenant = firstNonEmptyString(tenant, parts[i+1])
				site = firstNonEmptyString(site, parts[i+2])
				publicPrefix = firstNonEmptyString(publicPrefix, path.Join("recruiting", parts[i+1], parts[i+2]))
			}
		}
	}

	host := strings.ToLower(parsed.Hostname())
	if tenant == "" && (strings.Contains(host, "myworkdayjobs.com") || strings.Contains(host, "workdayjobs.com")) {
		tenant = strings.Split(host, ".")[0]
	}
	if site == "" {
		site = siteFromPath(parts)
	}
	if publicPrefix == "" && site != "" {
		publicPrefix = publicPathPrefix(parts, site)
	}
	if tenant == "" {
		return Config{}, errors.New("workday tenant is required")
	}
	if site == "" {
		return Config{}, errors.New("workday site is required")
	}
	return Config{
		BaseURL:          baseURL,
		Tenant:           tenant,
		Site:             site,
		PublicPathPrefix: publicPrefix,
	}, nil
}

func siteFromPath(parts []string) string {
	for _, part := range parts {
		lower := strings.ToLower(strings.TrimSpace(part))
		if lower == "" || lower == "job" || lower == "jobs" && len(parts) > 1 && strings.EqualFold(parts[0], "wday") {
			continue
		}
		if lower == "en" || isLocale(lower) || lower == "recruiting" || lower == "wday" || lower == "cxs" {
			continue
		}
		return part
	}
	return ""
}

func publicPathPrefix(parts []string, site string) string {
	for i, part := range parts {
		if strings.EqualFold(part, site) {
			return path.Join(parts[:i+1]...)
		}
	}
	return site
}

func hostedURL(config Config, externalPath string) string {
	if strings.TrimSpace(externalPath) == "" {
		return ""
	}
	hosted, err := joinURL(config.BaseURL, config.PublicPathPrefix, strings.TrimLeft(externalPath, "/"))
	if err != nil {
		return ""
	}
	return hosted.String()
}

func workdayCountry(value string) string {
	value = normalizeSpace(value)
	if value == "" {
		return ""
	}
	if country := canonicalCountry(value); country != value {
		return country
	}
	if strings.Contains(value, ",") {
		first := strings.TrimSpace(strings.Split(value, ",")[0])
		if country := canonicalCountry(first); country != first || len(first) == 2 {
			return country
		}
	}
	return canonicalCountry(value)
}

func rawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil {
		for _, key := range []string{"descriptor", "name", "location", "value"} {
			if value, ok := object[key]; ok {
				if text := rawText(value); text != "" {
					return text
				}
			}
		}
		if country, ok := object["country"]; ok {
			return rawText(country)
		}
	}
	return ""
}

func sourceSearchText(source provider.Source) string {
	for _, key := range []string{"search_text", "query", "keywords"} {
		if value := normalizeSpace(source.Metadata[key]); value != "" {
			return value
		}
	}
	if parsed, err := url.Parse(source.URL); err == nil {
		if value := normalizeSpace(parsed.Query().Get("q")); value != "" {
			return value
		}
	}
	return ""
}

func sourceCompany(source provider.Source, fallback string) string {
	if source.Name != "" {
		return source.Name
	}
	for _, key := range []string{"company", "company_name"} {
		if value := normalizeSpace(source.Metadata[key]); value != "" {
			return value
		}
	}
	return fallback
}

func employmentFromText(values ...string) string {
	joined := strings.ToLower(strings.Join(values, " "))
	switch {
	case internLevelPattern.MatchString(joined):
		return "internship"
	case strings.Contains(joined, "contract"):
		return "contract"
	case strings.Contains(joined, "part"):
		return "part_time"
	case strings.Contains(joined, "full"):
		return "full_time"
	default:
		return ""
	}
}

func inferRoleFamily(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "machine learning"), strings.Contains(lower, "artificial intelligence"), strings.Contains(lower, " ai "), strings.Contains(lower, "ml "):
		return "ml_ai"
	case strings.Contains(lower, "data"):
		return "data"
	case strings.Contains(lower, "frontend"), strings.Contains(lower, "front-end"), strings.Contains(lower, "web"):
		return "frontend"
	case strings.Contains(lower, "backend"), strings.Contains(lower, "back-end"), strings.Contains(lower, "platform"):
		return "backend"
	case strings.Contains(lower, "infrastructure"), strings.Contains(lower, "infra"), strings.Contains(lower, "systems"):
		return "infrastructure"
	case strings.Contains(lower, "full stack"), strings.Contains(lower, "full-stack"):
		return "full_stack"
	default:
		return "software_engineering"
	}
}

func parseTimePtr(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", "Jan 2, 2006", "January 2, 2006"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return &parsed
		}
	}
	return nil
}

func stableJobToken(values ...string) string {
	for _, value := range values {
		value = strings.Trim(strings.TrimSpace(value), "/")
		if value == "" {
			continue
		}
		parts := strings.FieldsFunc(value, func(r rune) bool {
			return r == '/' || r == '?' || r == '#'
		})
		for i := len(parts) - 1; i >= 0; i-- {
			token := strings.TrimSpace(parts[i])
			if token != "" {
				return token
			}
		}
	}
	return ""
}

func cleanHTMLText(value string) string {
	if value == "" {
		return ""
	}
	value = html.UnescapeString(htmlTagPattern.ReplaceAllString(value, " "))
	return normalizeSpace(value)
}

func canonicalCountry(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch lower {
	case "us", "usa", "united states", "united states of america":
		return "US"
	case "uk", "united kingdom", "great britain":
		return "UK"
	case "ca", "canada":
		return "Canada"
	case "sg", "singapore":
		return "Singapore"
	case "hk", "hong kong":
		return "Hong Kong"
	default:
		return strings.TrimSpace(value)
	}
}

func isLocale(value string) bool {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(value)), "-")
	if len(parts) < 1 || len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if len(part) != 2 {
			return false
		}
		for _, r := range part {
			if r < 'a' || r > 'z' {
				return false
			}
		}
	}
	return true
}

func nonEmptyPathParts(parsed *url.URL) []string {
	rawParts := strings.Split(parsed.EscapedPath(), "/")
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		part, _ = url.PathUnescape(part)
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func joinURL(base string, parts ...string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		parsed.Path = path.Join(parsed.Path, part)
	}
	return parsed, nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeSpace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func boundedInt(value, fallback, minValue, maxValue int) int {
	if value == 0 {
		value = fallback
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
