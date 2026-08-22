package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hwennnn/radar/internal/pipeline"
)

type fakeFeedStore struct {
	postings    []pipeline.Posting
	statuses    []pipeline.SourceStatus
	operational pipeline.OperationalState
	err         error
}

func (s fakeFeedStore) ListPostings(context.Context) ([]pipeline.Posting, error) {
	return s.postings, s.err
}

func (s fakeFeedStore) ListSourceStatuses(context.Context) ([]pipeline.SourceStatus, error) {
	return s.statuses, s.err
}

func (s fakeFeedStore) ReadOperationalState(context.Context) (pipeline.OperationalState, error) {
	return s.operational, s.err
}

func TestBuildFeedFiltersAndGroupsPresentationDuplicates(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	postings := []pipeline.Posting{
		feedTestPosting("job-a", "ByteDance", "Software Engineer Intern", "San Jose, CA", "US", "Internship", "Internship", "https://example.com/a", now.Add(-time.Hour)),
		feedTestPosting("job-b", "ByteDance", "Software Engineer Intern", "San Jose, CA", "US", "Internship", "Internship", "https://example.com/b", now.Add(-2*time.Hour)),
		feedTestPosting("job-c", "Jane Street", "Graduate Software Engineer", "New York, NY", "US", "Full-time", "New Grad", "https://example.com/c", now.Add(-25*time.Hour)),
		feedTestPosting("job-d", "Rippling", "Customer Support Specialist Intern", "New York, NY", "US", "Internship", "Internship", "https://example.com/d", now),
		feedTestPosting("job-e", "Tencent", "Software Engineer Intern", "Singapore", "SG", "Internship", "Internship", "https://example.com/e", now),
	}
	jobs, summary := buildFeed(postings, []pipeline.SourceStatus{
		{SourceID: "healthy", State: "success"},
		{SourceID: "failed", State: "failure"},
	}, 48, now)

	if len(jobs) != 2 || summary.EligibleOpenings != 3 || summary.GroupedRoles != 2 || summary.Companies != 2 {
		t.Fatalf("unexpected feed: jobs=%+v summary=%+v", jobs, summary)
	}
	if jobs[0].Company != "ByteDance" || jobs[0].OpeningCount != 2 || len(jobs[0].Openings) != 2 {
		t.Fatalf("duplicate openings were not grouped and retained: %+v", jobs[0])
	}
	if jobs[0].Openings[0].ApplyURL != "https://example.com/a" || jobs[0].Openings[1].ApplyURL != "https://example.com/b" {
		t.Fatalf("unexpected grouped apply links: %+v", jobs[0].Openings)
	}
	if summary.AddedToday != 2 || summary.AddedThisWeek != 3 || summary.SourcesHealthy != 1 || summary.SourcesFailed != 1 || summary.SourcesTotal != 48 {
		t.Fatalf("unexpected summary counts: %+v", summary)
	}
}

func TestBuildFeedEvaluatesPostingAgeAtSnapshotTime(t *testing.T) {
	now := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	postedAt := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	posting := feedTestPosting(
		"stale", "Example AI", "Software Engineer Intern", "New York, NY", "US",
		"Internship", "Internship", "https://example.com/stale", now.Add(-time.Hour),
	)
	posting.PostedAt = &postedAt

	jobs, summary := buildFeed([]pipeline.Posting{posting}, nil, 1, now)
	if len(jobs) != 0 || summary.EligibleOpenings != 0 {
		t.Fatalf("posting stale at snapshot time remained visible: jobs=%+v summary=%+v", jobs, summary)
	}
}

func TestBuildFeedCollapsesLegacyCompanySpacingDuplicateByApplyURL(t *testing.T) {
	now := time.Date(2026, 8, 19, 1, 0, 0, 0, time.UTC)
	applyURL := "https://www.citadelsecurities.com/careers/details/software-engineer-intern-us"
	postings := []pipeline.Posting{
		feedTestPosting("canonical", "Citadel Securities", "Software Engineer - Intern (US)", "United States", "US", "Intern", "Internship", applyURL, now.Add(-time.Hour)),
		feedTestPosting("legacy-spacing", "Citadelsecurities", "Software Engineer – Intern (US)", "United States", "US", "Intern", "Internship", applyURL, now.Add(-time.Minute)),
	}

	jobs, summary := buildFeed(postings, nil, 2, now)
	if len(jobs) != 1 || jobs[0].OpeningCount != 1 || len(jobs[0].Openings) != 1 {
		t.Fatalf("legacy URL duplicate remained visible: jobs=%+v", jobs)
	}
	if summary.EligibleOpenings != 1 || summary.GroupedRoles != 1 || summary.Companies != 1 || summary.AddedToday != 1 {
		t.Fatalf("legacy URL duplicate inflated summary: %+v", summary)
	}
}

func TestBuildFeedCollapsesCanonicalApplyURLAcrossPresentationGroups(t *testing.T) {
	now := time.Date(2026, 8, 19, 1, 0, 0, 0, time.UTC)
	postings := []pipeline.Posting{
		feedTestPosting("new", "Example AI", "Software Engineer Intern", "New York, NY", "US", "Internship", "Internship", "https://jobs.example.com/roles/42/?utm_source=radar&lever-source=board#apply", now),
		feedTestPosting("old", "Example AI", "Software Engineering Intern", "New York", "US", "Internship", "Internship", "https://JOBS.EXAMPLE.COM:443/roles/42?lever-via=feed", now.Add(-time.Hour)),
	}

	jobs, summary := buildFeed(postings, nil, 1, now)
	if len(jobs) != 1 || jobs[0].OpeningCount != 1 || len(jobs[0].Openings) != 1 {
		t.Fatalf("canonical URL duplicate remained visible across role groups: jobs=%+v", jobs)
	}
	if jobs[0].ApplyURL != "https://jobs.example.com/roles/42" {
		t.Fatalf("feed did not expose the canonical apply URL: %q", jobs[0].ApplyURL)
	}
	if summary.EligibleOpenings != 1 || summary.GroupedRoles != 1 || summary.Companies != 1 {
		t.Fatalf("canonical URL duplicate inflated summary: %+v", summary)
	}
}

func TestBuildFeedKeepsDistinctRequisitionURLs(t *testing.T) {
	now := time.Date(2026, 8, 19, 1, 0, 0, 0, time.UTC)
	postings := []pipeline.Posting{
		feedTestPosting("req-a", "Jane Street", "Software Engineer", "New York, NY", "US", "Full-time", "New Grad", "https://www.janestreet.com/join-jane-street/position/8594541002", now),
		feedTestPosting("req-b", "Jane Street", "Software Engineer", "New York, NY", "US", "Full-time", "New Grad", "https://www.janestreet.com/join-jane-street/position/8599644002", now.Add(-time.Hour)),
	}

	jobs, summary := buildFeed(postings, nil, 1, now)
	if len(jobs) != 1 || jobs[0].OpeningCount != 2 || len(jobs[0].Openings) != 2 {
		t.Fatalf("distinct requisitions were over-deduplicated: jobs=%+v", jobs)
	}
	if summary.EligibleOpenings != 2 {
		t.Fatalf("distinct requisitions were missing from the summary: %+v", summary)
	}
}

func TestFeedHandlerFiltersSanitizesAndCapsResults(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	store := fakeFeedStore{postings: []pipeline.Posting{
		feedTestPosting("sg", "Example AI", "Machine Learning Engineer Intern", "Singapore", "SG", "Internship", "Internship", "javascript:alert(1)", now),
		feedTestPosting("us", "Example Systems", "New Grad Platform Engineer", "Seattle, WA", "US", "Full-time", "New Grad", "https://example.com/us", now.Add(-time.Hour)),
	}}
	request := httptest.NewRequest(http.MethodGet, "/api/jobs?location=singapore&track=internship&role=ai_ml&q=example&limit=9999", nil)
	response := httptest.NewRecorder()
	(feedServer{store: store, totalSources: 48, now: func() time.Time { return now }}).handler(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
	}
	var body feedResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || body.Showing != 1 || body.Limit != maxFeedLimit || len(body.Jobs) != 1 {
		t.Fatalf("unexpected filtered response: %+v", body)
	}
	if body.Jobs[0].ApplyURL != "" || body.Jobs[0].Openings[0].ApplyURL != "" {
		t.Fatalf("unsafe apply URL reached the feed: %+v", body.Jobs[0])
	}
}

func TestFeedHandlerReturnsIncrementalUpdatesAndActiveIDs(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	store := fakeFeedStore{postings: []pipeline.Posting{
		feedTestPosting("old", "Example Systems", "Graduate Software Engineer", "New York, NY", "US", "Full-time", "New Grad", "https://example.com/old", now.Add(-2*time.Hour)),
		feedTestPosting("new", "Example AI", "Machine Learning Engineer Intern", "Singapore", "SG", "Internship", "Internship", "https://example.com/new", now.Add(-30*time.Minute)),
	}}
	request := httptest.NewRequest(http.MethodGet, "/api/jobs?limit=500&since="+now.Add(-time.Hour).Format(time.RFC3339), nil)
	response := httptest.NewRecorder()
	(feedServer{store: store, totalSources: 2, now: func() time.Time { return now }}).handler(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
	}
	var body feedResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Incremental || body.Total != 2 || body.Showing != 2 || len(body.ActiveIDs) != 2 {
		t.Fatalf("incremental response is missing reconciliation state: %+v", body)
	}
	if len(body.Jobs) != 1 || body.Jobs[0].Company != "Example AI" {
		t.Fatalf("incremental response did not isolate new jobs: %+v", body.Jobs)
	}
}

func TestFeedHandlerRejectsInvalidIncrementalTimestamp(t *testing.T) {
	response := httptest.NewRecorder()
	(feedServer{store: fakeFeedStore{}}).handler(response, httptest.NewRequest(http.MethodGet, "/api/jobs?since=not-a-time", nil))

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "RFC3339") {
		t.Fatalf("unexpected invalid-since response: %d %s", response.Code, response.Body.String())
	}
}

func TestFeedHandlerIncludesRegistryLogoDomain(t *testing.T) {
	now := time.Date(2026, 8, 19, 3, 0, 0, 0, time.UTC)
	store := fakeFeedStore{postings: []pipeline.Posting{
		feedTestPosting("aqr", "AQR", "Software Engineer Intern", "New York, NY", "US", "Internship", "Internship", "https://example.com/aqr", now),
	}}
	response := httptest.NewRecorder()
	(feedServer{
		store: store, totalSources: 1, now: func() time.Time { return now },
		logoDomains: map[string]string{normalizeFeedCompany("AQR"): "aqr.com"},
	}).handler(response, httptest.NewRequest(http.MethodGet, "/api/jobs", nil))

	var body feedResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Jobs) != 1 || body.Jobs[0].LogoDomain != "aqr.com" {
		t.Fatalf("registry logo domain missing from feed: %+v", body.Jobs)
	}
}

func TestFeedHandlerDoesNotLeakStoreErrors(t *testing.T) {
	var logs strings.Builder
	response := httptest.NewRecorder()
	(feedServer{
		store:  fakeFeedStore{err: errors.New("postgres://user:secret@example")},
		logger: slog.New(slog.NewTextHandler(&logs, nil)),
	}).handler(response, httptest.NewRequest(http.MethodGet, "/api/jobs", nil))

	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("unsafe feed error response: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(logs.String(), "secret") {
		t.Fatal("expected the private diagnostic to remain in server logs")
	}
}

func TestEmbeddedUIAndFeedShareOneServer(t *testing.T) {
	handler := newServerHandler(&healthState{}, fakeFeedStore{}, dashboardConfig{TotalSources: 48}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for path, contentType := range map[string]string{
		"/":           "text/html",
		"/jobs":       "text/html",
		"/companies":  "text/html",
		"/system":     "text/html",
		"/docs":       "text/html",
		"/docs.html":  "text/html",
		"/styles.css": "text/css",
		"/app.js":     "text/javascript",
		"/docs.js":    "text/javascript",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), contentType) {
			t.Fatalf("unexpected static response for %s: %d %q", path, response.Code, response.Header().Get("Content-Type"))
		}
		policy := response.Header().Get("Content-Security-Policy")
		if policy == "" {
			t.Fatalf("missing content security policy for %s", path)
		}
		if !strings.Contains(policy, "img-src 'self' data: https://www.google.com") {
			t.Fatalf("company logo host missing from content security policy for %s: %q", path, policy)
		}
		if !strings.Contains(policy, "https://*.gstatic.com") {
			t.Fatalf("company logo redirect host missing from content security policy for %s: %q", path, policy)
		}
		if contentType == "text/html" {
			body := response.Body.String()
			if strings.Contains(strings.ToLower(body), "radar lite") {
				t.Fatalf("legacy product branding remains on %s: %s", path, body)
			}
			if !strings.Contains(body, `aria-label="Radar home"`) || !strings.Contains(body, `<span>Radar</span>`) {
				t.Fatalf("Radar product branding is missing on %s: %s", path, body)
			}
			for _, expected := range []string{
				`aria-label="Footer resources"`,
				`href="https://github.com/hwennnn/radar"`,
				`href="https://github.com/hwennnn/radar/issues"`,
			} {
				if !strings.Contains(body, expected) {
					t.Fatalf("footer is missing public repository resource %q on %s: %s", expected, path, body)
				}
			}
		}
		if path == "/docs" && !strings.Contains(response.Body.String(), "Job identity and deduplication") {
			t.Fatalf("docs route did not serve engineering note: %s", response.Body.String())
		}
		if path == "/docs" && strings.Contains(strings.ToLower(response.Body.String()), "tinyfish") {
			t.Fatalf("docs route exposed an internal discovery provider: %s", response.Body.String())
		}
		if path == "/docs" {
			body := response.Body.String()
			for _, expected := range []string{`href="/jobs"`, `href="/companies"`, `href="/system"`, `href="/docs" aria-current="page">How it works`, `id="source-state"`} {
				if !strings.Contains(body, expected) {
					t.Fatalf("docs header is missing shared navigation %q: %s", expected, body)
				}
			}
		}
		if path == "/" || path == "/jobs" || path == "/companies" || path == "/system" {
			body := response.Body.String()
			if !strings.Contains(body, `id="company-field-label"`) ||
				!strings.Contains(body, `aria-labelledby="company-field-label company-picker-label"`) {
				t.Fatalf("company picker is missing its external field label: %s", body)
			}
			if !strings.Contains(body, `id="source-roster-list"`) ||
				!strings.Contains(body, `role="status"`) ||
				!strings.Contains(body, `class="source-roster-skeleton"`) ||
				!strings.Contains(body, `aria-busy="true"`) ||
				!strings.Contains(body, `placeholder="Search by company or provider"`) ||
				strings.Contains(body, `id="source-roster-toggle"`) {
				t.Fatalf("source roster controls are missing: %s", body)
			}
			if !strings.Contains(body, `href="/companies" data-view-link="companies"`) ||
				!strings.Contains(body, `id="companies-panel" data-view="companies"`) {
				t.Fatalf("companies tab is missing: %s", body)
			}
			if !strings.Contains(body, `href="/jobs" data-view-link="jobs"`) {
				t.Fatalf("jobs route is missing: %s", body)
			}
			if !strings.Contains(body, `href="/system" data-view-link="system"`) {
				t.Fatalf("system route is missing: %s", body)
			}
			for _, expected := range []string{`id="job-pagination"`, `id="previous-page"`, `id="pagination-pages"`, `id="next-page"`} {
				if !strings.Contains(body, expected) {
					t.Fatalf("job pagination is missing %q: %s", expected, body)
				}
			}
			if strings.Contains(body, `id="load-more"`) {
				t.Fatalf("legacy load-more control is still present: %s", body)
			}
			if !strings.Contains(body, `class="select-shell"`) ||
				!strings.Contains(body, `id="track-filter-label" for="track-filter"`) {
				t.Fatalf("filter selects are missing accessible enhancement hooks: %s", body)
			}
			for _, expected := range []string{`<details class="attention-panel" id="source-attention" hidden>`, `<summary class="attention-summary">`, `id="attention-count"`} {
				if !strings.Contains(body, expected) {
					t.Fatalf("system attention disclosure is missing %q: %s", expected, body)
				}
			}
			for _, expected := range []string{`<details class="technical-panel" id="technical-panel">`, `<summary class="technical-summary">`, `Crawler, source health, dedupe, and delivery state`} {
				if !strings.Contains(body, expected) {
					t.Fatalf("system technical disclosure is missing %q: %s", expected, body)
				}
			}
			if strings.Contains(body, `<details class="technical-panel" id="technical-panel" open`) {
				t.Fatalf("system technical disclosure must be collapsed by default: %s", body)
			}
			if strings.Contains(body, `<details class="attention-panel" id="source-attention" open`) {
				t.Fatalf("system attention disclosure must be collapsed by default: %s", body)
			}
		}
		if path == "/app.js" {
			body := response.Body.String()
			if strings.Contains(body, "matches.slice(0, 12)") {
				t.Fatalf("companies tab still truncates the monitored-company list: %s", body)
			}
			if !strings.Contains(body, `document.querySelectorAll("main > [data-view]")`) {
				t.Fatalf("dashboard view switching is not scoped away from the body element: %s", body)
			}
			if !strings.Contains(body, `node("article", "source-roster-card")`) {
				t.Fatalf("companies tab is not rendered as a card grid: %s", body)
			}
			for _, expected := range []string{
				`window.history.pushState`,
				`window.addEventListener("popstate"`,
				`readRequestCache(state.feedCache`,
				`readPersistentFeedCache(requestKey)`,
				`writePersistentFeedCache(requestKey, data)`,
				`loadFeed({ force: true })`,
			} {
				if !strings.Contains(body, expected) {
					t.Fatalf("dashboard client cache/navigation is missing %q: %s", expected, body)
				}
			}
		}
	}

	api := httptest.NewRecorder()
	handler.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/api/jobs", nil))
	if api.Code != http.StatusOK || !strings.Contains(api.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("unexpected API response: %d %q %s", api.Code, api.Header().Get("Content-Type"), api.Body.String())
	}
	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("unexpected status API response: %d %q %s", status.Code, status.Header().Get("Content-Type"), status.Body.String())
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/not-a-page", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unexpected missing-page status: %d", missing.Code)
	}
}

func feedTestPosting(id, company, title, location, country, employment, level, applyURL string, firstSeen time.Time) pipeline.Posting {
	return pipeline.Posting{
		ID: id, Company: company, Title: title, Location: location, Country: country,
		EmploymentType: employment, Level: level, ApplyURL: applyURL,
		FirstSeenAt: firstSeen, LastSeenAt: firstSeen.Add(10 * time.Minute),
	}
}
