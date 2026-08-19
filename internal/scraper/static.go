package scraper

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const defaultStaticMaxBodyBytes int64 = 2 << 20
const defaultStaticMaxSitemapURLs = 25

var (
	jsonLDScriptPattern     = regexp.MustCompile(`(?is)<script[^>]+type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	htmlTagWithHrefPattern  = regexp.MustCompile(`(?is)<(?:link|a)\b[^>]*href=["']([^"']+)["'][^>]*>`)
	staticXMLContentPattern = regexp.MustCompile(`(?is)<(?:urlset|sitemapindex)\b`)
)

type StaticOptions struct {
	Client         *http.Client
	MaxBodyBytes   int64
	MaxSitemapURLs int
}

type StaticExtractor struct {
	client         *http.Client
	now            func() time.Time
	maxBodyBytes   int64
	maxSitemapURLs int
}

func NewStaticExtractor(options ...StaticOptions) *StaticExtractor {
	var opts StaticOptions
	if len(options) > 0 {
		opts = options[0]
	}
	client := opts.Client
	if client == nil {
		client = NewSafeHTTPClient(10 * time.Second)
	}
	maxBodyBytes := opts.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultStaticMaxBodyBytes
	}
	maxSitemapURLs := opts.MaxSitemapURLs
	if maxSitemapURLs <= 0 {
		maxSitemapURLs = defaultStaticMaxSitemapURLs
	}
	return &StaticExtractor{
		client:         client,
		now:            func() time.Time { return time.Now().UTC() },
		maxBodyBytes:   maxBodyBytes,
		maxSitemapURLs: maxSitemapURLs,
	}
}

func (s *StaticExtractor) Name() string {
	return "static-fetch"
}

func (s *StaticExtractor) Tier() Tier {
	return TierStaticFetch
}

func (s *StaticExtractor) Sources() []Source {
	return SampleSources()
}

func (s *StaticExtractor) Extract(ctx context.Context, source Source) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(source.URL) == "" || strings.HasPrefix(source.URL, "sample://") {
		return s.extractSample(source)
	}
	return s.extractHTTP(ctx, source)
}

func (s *StaticExtractor) extractHTTP(ctx context.Context, source Source) (Result, error) {
	endpoint, err := parseStaticURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	body, err := s.get(ctx, endpoint, "text/html,application/xhtml+xml,application/xml,application/json;q=0.9,*/*;q=0.8")
	if err != nil {
		return Result{}, err
	}
	document := string(body)
	jobs := s.extractJSONLDJobs(source, endpoint, document)
	evidence := []Evidence{
		{Field: "static_page", Text: "Static page JSON-LD JobPosting extraction", URL: endpoint.String()},
	}
	if len(jobs) == 0 {
		sitemapJobs, sitemapEvidence := s.extractSitemapJobs(ctx, source, endpoint, document)
		if len(sitemapJobs) > 0 {
			jobs = sitemapJobs
			evidence = sitemapEvidence
		}
	}
	return NormalizeResult(Result{
		Source:      source,
		Jobs:        jobs,
		Confidence:  0.82,
		Strategy:    TierStaticFetch,
		Live:        true,
		FetchedAt:   s.now(),
		RawEvidence: evidence,
	})
}

func (s *StaticExtractor) get(ctx context.Context, endpoint *url.URL, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "RadarJobIntel/0.1 (+https://radar.local)")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("static fetch failed: %s", resp.Status)
	}
	return readLimited(resp.Body, s.maxBodyBytes)
}

func (s *StaticExtractor) extractJSONLDJobs(source Source, baseURL *url.URL, document string) []JobPosting {
	matches := jsonLDScriptPattern.FindAllStringSubmatch(document, -1)
	jobs := make([]JobPosting, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		var payload any
		decoder := json.NewDecoder(strings.NewReader(html.UnescapeString(strings.TrimSpace(match[1]))))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			continue
		}
		var nodes []map[string]any
		collectJSONLDJobPostings(payload, &nodes)
		for _, node := range nodes {
			job, ok := s.postingFromJSONLD(source, baseURL, node)
			if ok {
				jobs = append(jobs, job)
			}
		}
	}
	return jobs
}

func (s *StaticExtractor) extractSitemapJobs(ctx context.Context, source Source, pageURL *url.URL, document string) ([]JobPosting, []Evidence) {
	candidates := staticSitemapCandidates(pageURL, document)
	if len(candidates) == 0 {
		return nil, nil
	}
	visitedSitemaps := map[string]struct{}{}
	visitedDetails := map[string]struct{}{}
	jobs := make([]JobPosting, 0)
	evidenceURL := ""
	detailAttempts := 0
	for len(candidates) > 0 && detailAttempts < s.maxSitemapURLs {
		candidate := candidates[0]
		candidates = candidates[1:]
		if _, ok := visitedSitemaps[candidate.String()]; ok {
			continue
		}
		visitedSitemaps[candidate.String()] = struct{}{}
		body := []byte(document)
		if candidate.String() != pageURL.String() || !staticLooksLikeSitemapXML(pageURL, document) {
			var err error
			body, err = s.get(ctx, candidate, "application/xml,text/xml,text/plain;q=0.9,*/*;q=0.8")
			if err != nil {
				continue
			}
		}
		entries, nestedSitemaps, err := parseStaticSitemap(body)
		if err != nil {
			continue
		}
		if evidenceURL == "" {
			evidenceURL = candidate.String()
		}
		for _, nested := range nestedSitemaps {
			if len(candidates)+len(visitedSitemaps) >= s.maxSitemapURLs {
				break
			}
			if nestedURL := staticResolveURL(candidate, nested.Loc); nestedURL != nil {
				candidates = append(candidates, nestedURL)
			}
		}
		for _, entry := range entries {
			if detailAttempts >= s.maxSitemapURLs {
				break
			}
			detailURL := staticResolveURL(candidate, entry.Loc)
			if detailURL == nil || !staticLooksLikeJobURL(detailURL) {
				continue
			}
			if _, ok := visitedDetails[detailURL.String()]; ok {
				continue
			}
			visitedDetails[detailURL.String()] = struct{}{}
			detailAttempts++
			detailBody, err := s.get(ctx, detailURL, "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
			if err != nil {
				continue
			}
			for _, job := range s.extractJSONLDJobs(source, detailURL, string(detailBody)) {
				job.Evidence = append(job.Evidence, Evidence{Field: "sitemap", Text: "Discovered from XML sitemap", URL: candidate.String()})
				jobs = append(jobs, job)
				if len(jobs) >= s.maxSitemapURLs {
					break
				}
			}
		}
	}
	if len(jobs) == 0 {
		return nil, nil
	}
	return dedupeStaticJobs(jobs), []Evidence{
		{Field: "static_sitemap", Text: "Static XML sitemap job detail extraction", URL: evidenceURL},
	}
}

type staticSitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod"`
}

type staticSitemapDocument struct {
	URLs     []staticSitemapURL `xml:"url"`
	Sitemaps []staticSitemapURL `xml:"sitemap"`
}

func parseStaticSitemap(body []byte) ([]staticSitemapURL, []staticSitemapURL, error) {
	var document staticSitemapDocument
	if err := xml.Unmarshal(body, &document); err != nil {
		return nil, nil, err
	}
	return document.URLs, document.Sitemaps, nil
}

func staticSitemapCandidates(pageURL *url.URL, document string) []*url.URL {
	out := make([]*url.URL, 0, 3)
	seen := map[string]struct{}{}
	add := func(candidate *url.URL) {
		if candidate == nil {
			return
		}
		key := candidate.String()
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	if staticLooksLikeSitemapXML(pageURL, document) {
		copy := *pageURL
		add(&copy)
	}
	for _, match := range htmlTagWithHrefPattern.FindAllStringSubmatch(document, -1) {
		if len(match) < 2 {
			continue
		}
		tag := strings.ToLower(match[0])
		href := strings.TrimSpace(html.UnescapeString(match[1]))
		if !strings.Contains(tag, "sitemap") && !strings.Contains(strings.ToLower(href), "sitemap") {
			continue
		}
		if candidate := staticResolveURL(pageURL, href); candidate != nil {
			add(candidate)
		}
	}
	root := *pageURL
	root.Path = "/sitemap.xml"
	root.RawQuery = ""
	root.Fragment = ""
	if len(out) == 0 {
		add(&root)
	}
	return out
}

func staticLooksLikeSitemapXML(pageURL *url.URL, document string) bool {
	lowerPath := strings.ToLower(pageURL.Path)
	return strings.HasSuffix(lowerPath, ".xml") || staticXMLContentPattern.MatchString(document)
}

func staticResolveURL(baseURL *url.URL, rawURL string) *url.URL {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	return baseURL.ResolveReference(parsed)
}

func staticLooksLikeJobURL(value *url.URL) bool {
	lowerPath := strings.ToLower(value.EscapedPath())
	blocked := []string{"/blog/", "/news/", "/press/", "/privacy", "/terms", "/legal", "/events/"}
	for _, token := range blocked {
		if strings.Contains(lowerPath, token) {
			return false
		}
	}
	for _, token := range []string{"/job", "/jobs/", "/careers/", "/career/", "/position", "/positions/", "/opening", "/openings/", "/opportunit", "/requisition", "/role/"} {
		if strings.Contains(lowerPath, token) {
			return true
		}
	}
	return false
}

func dedupeStaticJobs(jobs []JobPosting) []JobPosting {
	out := make([]JobPosting, 0, len(jobs))
	seen := map[string]struct{}{}
	for _, job := range jobs {
		key := strings.ToLower(firstNonEmptyString(job.SourceJobID, job.ApplyURL, job.Title))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, job)
	}
	return out
}

func (s *StaticExtractor) postingFromJSONLD(source Source, baseURL *url.URL, node map[string]any) (JobPosting, bool) {
	title := jsonLDStringField(node, "title")
	if title == "" {
		return JobPosting{}, false
	}
	validThrough := parseTimePtr(jsonLDStringField(node, "validThrough"))
	if validThrough != nil && validThrough.Before(s.now()) {
		return JobPosting{}, false
	}

	rawURL := firstNonEmptyString(jsonLDStringField(node, "url"), jsonLDStringField(node, "sameAs"), source.URL)
	applyURL := resolveStaticURL(baseURL, rawURL)
	company := firstNonEmptyString(jsonLDNestedString(node["hiringOrganization"], "name"), source.Name, companyFromURL(source.URL))
	location, country := jsonLDJobLocation(node["jobLocation"])
	description := cleanHTMLText(jsonLDStringField(node, "description", "responsibilities", "skills"))
	sourceJobID := firstNonEmptyString(jsonLDIdentifier(node["identifier"]), stableStringID(firstNonEmptyString(applyURL, title)))

	return JobPosting{
		SourceJobID:    sourceJobID,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: strings.ToLower(strings.Join(jsonLDStringList(node["employmentType"]), ",")),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(jsonLDStringField(node, "datePosted")),
		Live:           true,
		Confidence:     0.82,
		Strategy:       TierStaticFetch,
		Evidence: []Evidence{
			{Field: "json_ld", Text: "schema.org JobPosting", URL: source.URL},
			{Field: "description", Text: description, URL: applyURL},
			{Field: "location", Text: location, URL: applyURL},
		},
	}, true
}

func (s *StaticExtractor) extractSample(source Source) (Result, error) {
	if source.ID == "" {
		source = SampleSources()[0]
	}
	if strings.Contains(source.URL, "worker-risk-notifications") {
		return s.extractWorkerRiskNotificationSample(source)
	}
	if strings.Contains(source.URL, "mcp-e2e") {
		return s.extractMCPEndToEndSample(source)
	}
	posted := s.now().AddDate(0, 0, -2)
	result := Result{
		Source:     source,
		Confidence: 0.88,
		Strategy:   s.Tier(),
		Live:       true,
		FetchedAt:  s.now(),
		RawEvidence: []Evidence{
			{Field: "feed", Text: "Static early-career software jobs seeded for Radar MVP development.", URL: source.URL},
		},
		Jobs: []JobPosting{
			{
				SourceJobID:    "sample-nimbus-intern-2026",
				Company:        "Nimbus Systems",
				Title:          "Software Engineering Intern, Backend Platform - Summer 2026",
				Location:       "New York, NY, United States",
				EmploymentType: "internship",
				SourceURL:      source.URL,
				ApplyURL:       "https://example.com/nimbus/jobs/software-engineering-intern-backend-platform-summer-2026",
				PostedAt:       &posted,
				Live:           true,
				Confidence:     0.91,
				Evidence: []Evidence{
					{Field: "graduation_window", Text: "Candidates graduating between December 2026 and June 2027 are encouraged to apply.", URL: source.URL},
					{Field: "role", Text: "Build distributed services for the backend platform team.", URL: source.URL},
				},
			},
			{
				SourceJobID:    "sample-quanta-newgrad-2026",
				Company:        "Quanta Ledger",
				Title:          "New Grad Software Engineer, Trading Infrastructure",
				Location:       "Singapore",
				EmploymentType: "full_time",
				SourceURL:      source.URL,
				ApplyURL:       "https://example.com/quanta-ledger/jobs/new-grad-software-engineer-trading-infrastructure",
				PostedAt:       &posted,
				Live:           true,
				Confidence:     0.86,
				Evidence: []Evidence{
					{Field: "new_grad_fit", Text: "Designed for 2026 graduates available for full-time work.", URL: source.URL},
					{Field: "domain", Text: "Low-latency systems, market data, and trading infrastructure from the Singapore engineering team.", URL: source.URL},
				},
			},
			{
				SourceJobID:    "sample-helio-ml-intern-2026",
				Company:        "Helio Labs",
				Title:          "Machine Learning Engineering Intern - AI Agents",
				Location:       "Toronto, Canada",
				EmploymentType: "internship",
				SourceURL:      source.URL,
				ApplyURL:       "https://example.com/helio-labs/jobs/machine-learning-engineering-intern-ai-agents",
				PostedAt:       &posted,
				Live:           true,
				Confidence:     0.84,
				Evidence: []Evidence{
					{Field: "role", Text: "Prototype agentic workflows and productionize ML services.", URL: source.URL},
					{Field: "location", Text: "Hybrid role based in Toronto with Canadian work authorization reviewed case by case.", URL: source.URL},
				},
			},
		},
	}
	return NormalizeResult(result)
}

func (s *StaticExtractor) extractMCPEndToEndSample(source Source) (Result, error) {
	company := strings.TrimSpace(source.Metadata["harness_company_name"])
	if company == "" {
		company = strings.TrimSpace(source.Name)
	}
	if company == "" {
		company = "Radar MCP Local Proof"
	}
	slug := strings.NewReplacer(
		"sample://", "",
		"/", "-",
		":", "-",
		"_", "-",
		".", "-",
	).Replace(strings.ToLower(strings.TrimSpace(source.URL)))
	posted := s.now().AddDate(0, 0, -1)
	result := Result{
		Source:     source,
		Confidence: 0.93,
		Strategy:   s.Tier(),
		Live:       true,
		FetchedAt:  s.now(),
		RawEvidence: []Evidence{
			{Field: "feed", Text: "Static MCP end-to-end local workflow proof seeded for Radar agents.", URL: source.URL},
		},
		Jobs: []JobPosting{{
			SourceJobID:    "mcp-e2e-backend-platform-" + slug,
			Company:        company,
			Title:          "New Grad Software Engineer, Backend Platform - Local MCP Proof",
			Location:       "Singapore",
			EmploymentType: "full_time",
			SourceURL:      source.URL,
			ApplyURL:       "https://example.com/radar-mcp-proof/jobs/backend-platform-" + slug,
			PostedAt:       &posted,
			Live:           true,
			Confidence:     0.93,
			Evidence: []Evidence{
				{Field: "role", Text: "Build Go services, Redis queues, Postgres-backed workflow state, and local observability for a backend platform team.", URL: source.URL},
				{Field: "location", Text: "Singapore local-first workflow proof role.", URL: source.URL},
				{Field: "target_bar", Text: "High-signal backend infrastructure role for the Radar local MCP proof.", URL: source.URL},
			},
		}},
	}
	return NormalizeResult(result)
}

func (s *StaticExtractor) extractWorkerRiskNotificationSample(source Source) (Result, error) {
	company := strings.TrimSpace(source.Metadata["harness_company_name"])
	if company == "" {
		company = "Radar Harness Worker Risk"
	}
	posted := s.now().AddDate(0, 0, -120)
	result := Result{
		Source:     source,
		Confidence: 0.91,
		Strategy:   s.Tier(),
		Live:       true,
		FetchedAt:  s.now(),
		RawEvidence: []Evidence{
			{Field: "feed", Text: "Static stale/repost notification fanout proof seeded for Radar backend harness.", URL: source.URL},
		},
		Jobs: []JobPosting{{
			SourceJobID:    "harness-risk-backend-intern-2026",
			Company:        company,
			Title:          "Backend Software Engineering Intern, Distributed Systems - Summer 2026",
			Location:       "San Francisco, CA, United States",
			Country:        "US",
			EmploymentType: "internship",
			RoleFamily:     "backend",
			Level:          "intern",
			SourceURL:      source.URL,
			ApplyURL:       "https://example.com/radar-harness/jobs/backend-software-engineering-intern-distributed-systems-summer-2026",
			PostedAt:       &posted,
			Live:           true,
			Confidence:     0.91,
			Evidence: []Evidence{
				{Field: "graduation_window", Text: "Candidates graduating in December 2026 are encouraged to apply.", URL: source.URL},
				{Field: "role", Text: "Build Go services, PostgreSQL persistence, Redis queues, and distributed worker systems.", URL: source.URL},
				{Field: "freshness", Text: "Harness fixture intentionally uses an old posted date to prove stale/repost alert suppression.", URL: source.URL},
			},
		}},
	}
	return NormalizeResult(result)
}

func parseStaticURL(rawURL string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("static source url must use http or https")
	}
	return parsed, nil
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = defaultStaticMaxBodyBytes
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("static response exceeded %d bytes", limit)
	}
	return body, nil
}

func collectJSONLDJobPostings(value any, out *[]map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		if jsonLDTypeContains(typed["@type"], "JobPosting") {
			*out = append(*out, typed)
		}
		for _, child := range typed {
			collectJSONLDJobPostings(child, out)
		}
	case []any:
		for _, child := range typed {
			collectJSONLDJobPostings(child, out)
		}
	}
}

func jsonLDTypeContains(value any, want string) bool {
	for _, item := range jsonLDStringList(value) {
		if strings.EqualFold(item, want) {
			return true
		}
	}
	return false
}

func jsonLDStringField(node map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := jsonLDString(node[key]); value != "" {
			return value
		}
	}
	return ""
}

func jsonLDNestedString(value any, keys ...string) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case map[string]any:
		return jsonLDStringField(typed, keys...)
	case []any:
		for _, item := range typed {
			if value := jsonLDNestedString(item, keys...); value != "" {
				return value
			}
		}
	}
	return ""
}

func jsonLDIdentifier(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		return firstNonEmptyString(jsonLDStringField(typed, "value"), jsonLDStringField(typed, "name"), jsonLDStringField(typed, "@id"))
	case []any:
		for _, item := range typed {
			if id := jsonLDIdentifier(item); id != "" {
				return id
			}
		}
	}
	return ""
}

func jsonLDJobLocation(value any) (string, string) {
	switch typed := value.(type) {
	case map[string]any:
		return jsonLDPlaceLocation(typed)
	case []any:
		locations := make([]string, 0, len(typed))
		country := ""
		for _, item := range typed {
			locationText, itemCountry := jsonLDJobLocation(item)
			if locationText != "" {
				locations = append(locations, locationText)
			}
			if country == "" {
				country = itemCountry
			}
		}
		return strings.Join(compactStringList(locations...), "; "), country
	default:
		return "", ""
	}
}

func jsonLDPlaceLocation(place map[string]any) (string, string) {
	address, _ := place["address"].(map[string]any)
	if address == nil {
		if name := jsonLDStringField(place, "name"); name != "" {
			return name, canonicalCountry("")
		}
		return "", ""
	}
	country := canonicalCountry(jsonLDString(address["addressCountry"]))
	parts := compactStringList(
		jsonLDStringField(address, "addressLocality"),
		jsonLDStringField(address, "addressRegion"),
		country,
	)
	if len(parts) == 0 {
		return jsonLDStringField(place, "name"), country
	}
	return strings.Join(parts, ", "), country
}

func jsonLDStringList(value any) []string {
	switch typed := value.(type) {
	case string:
		return compactStringList(typed)
	case json.Number:
		return compactStringList(typed.String())
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = append(values, jsonLDStringList(item)...)
		}
		return compactStringList(values...)
	default:
		return nil
	}
}

func jsonLDString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case map[string]any:
		return firstNonEmptyString(jsonLDStringField(typed, "name"), jsonLDStringField(typed, "value"), jsonLDStringField(typed, "@id"))
	default:
		return ""
	}
}

func resolveStaticURL(baseURL *url.URL, rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if parsed.IsAbs() {
		return parsed.String()
	}
	return baseURL.ResolveReference(parsed).String()
}

func canonicalCountry(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToUpper(value) {
	case "US", "USA":
		return "US"
	case "GB", "UK":
		return "UK"
	case "SG":
		return "Singapore"
	case "HK":
		return "Hong Kong"
	case "CA":
		return "Canada"
	case "AU":
		return "Australia"
	case "DE":
		return "Germany"
	case "FR":
		return "France"
	case "IN":
		return "India"
	case "JP":
		return "Japan"
	case "NL":
		return "Netherlands"
	case "IL":
		return "Israel"
	case "ES":
		return "Spain"
	case "SE":
		return "Sweden"
	case "PL":
		return "Poland"
	case "IE":
		return "Ireland"
	case "CH":
		return "Switzerland"
	case "BR":
		return "Brazil"
	case "MX":
		return "Mexico"
	case "NZ":
		return "New Zealand"
	case "PT":
		return "Portugal"
	case "AT":
		return "Austria"
	case "DK":
		return "Denmark"
	case "FI":
		return "Finland"
	case "NO":
		return "Norway"
	case "BE":
		return "Belgium"
	case "IT":
		return "Italy"
	case "CZ":
		return "Czech Republic"
	case "RO":
		return "Romania"
	case "PH":
		return "Philippines"
	case "MY":
		return "Malaysia"
	case "ID":
		return "Indonesia"
	case "TH":
		return "Thailand"
	case "VN":
		return "Vietnam"
	case "KR":
		return "South Korea"
	case "TW":
		return "Taiwan"
	case "AE":
		return "UAE"
	case "ZA":
		return "South Africa"
	case "NG":
		return "Nigeria"
	case "KE":
		return "Kenya"
	case "GH":
		return "Ghana"
	case "AR":
		return "Argentina"
	case "CO":
		return "Colombia"
	case "CL":
		return "Chile"
	case "UA":
		return "Ukraine"
	case "HU":
		return "Hungary"
	case "GR":
		return "Greece"
	case "TR":
		return "Turkey"
	}
	switch strings.ToLower(value) {
	case "united states", "united states of america":
		return "US"
	case "united kingdom", "great britain", "england", "scotland", "wales":
		return "UK"
	case "singapore":
		return "Singapore"
	case "hong kong":
		return "Hong Kong"
	case "canada":
		return "Canada"
	case "australia":
		return "Australia"
	case "germany", "deutschland":
		return "Germany"
	case "france":
		return "France"
	case "india":
		return "India"
	case "japan":
		return "Japan"
	case "netherlands", "the netherlands", "holland":
		return "Netherlands"
	case "israel":
		return "Israel"
	case "spain":
		return "Spain"
	case "sweden":
		return "Sweden"
	case "poland":
		return "Poland"
	case "ireland":
		return "Ireland"
	case "switzerland":
		return "Switzerland"
	case "brazil":
		return "Brazil"
	case "mexico":
		return "Mexico"
	case "new zealand":
		return "New Zealand"
	case "portugal":
		return "Portugal"
	case "austria":
		return "Austria"
	case "denmark":
		return "Denmark"
	case "finland":
		return "Finland"
	case "norway":
		return "Norway"
	case "belgium":
		return "Belgium"
	case "italy":
		return "Italy"
	case "czech republic", "czechia":
		return "Czech Republic"
	case "romania":
		return "Romania"
	case "philippines":
		return "Philippines"
	case "malaysia":
		return "Malaysia"
	case "indonesia":
		return "Indonesia"
	case "thailand":
		return "Thailand"
	case "vietnam", "viet nam":
		return "Vietnam"
	case "south korea", "korea":
		return "South Korea"
	case "taiwan":
		return "Taiwan"
	case "united arab emirates", "uae":
		return "UAE"
	case "south africa":
		return "South Africa"
	case "nigeria":
		return "Nigeria"
	case "kenya":
		return "Kenya"
	case "ghana":
		return "Ghana"
	case "argentina":
		return "Argentina"
	case "colombia":
		return "Colombia"
	case "chile":
		return "Chile"
	case "ukraine":
		return "Ukraine"
	case "hungary":
		return "Hungary"
	case "greece":
		return "Greece"
	case "turkey", "türkiye":
		return "Turkey"
	default:
		return value
	}
}

func SampleSources() []Source {
	return []Source{
		{
			ID:   "sample-static-early-career",
			Name: "Radar static early-career sample feed",
			URL:  "sample://radar/early-career-software",
			Tier: TierStaticFetch,
			Metadata: map[string]string{
				"cadence": "dev",
				"kind":    "sample_data",
			},
		},
	}
}
