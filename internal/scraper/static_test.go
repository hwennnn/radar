package scraper

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStaticExtractorDefaultClientBlocksPrivateTargets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>private careers</body></html>`))
	}))
	defer server.Close()

	extractor := NewStaticExtractor()
	_, err := extractor.Extract(context.Background(), Source{
		ID:   "private_static_source",
		Name: "Private Careers",
		URL:  server.URL,
		Tier: TierStaticFetch,
	})
	if err == nil {
		t.Fatal("Extract() error = nil, want private target rejection")
	}
	if !strings.Contains(err.Error(), "resolved private address blocked") {
		t.Fatalf("Extract() error = %q, want private target rejection", err)
	}
}

func TestStaticExtractorExtractsJSONLDJobPostings(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html>
<html>
<head>
<script type="application/ld+json">
{
  "@context": "https://schema.org",
  "@type": "JobPosting",
  "title": "Software Engineering Intern, Backend Platform - Summer 2026",
  "description": "<p>Build backend platform services for 2026 internship candidates.</p>",
  "datePosted": "2026-04-02",
  "validThrough": "2099-12-31T23:59:59Z",
  "employmentType": "INTERN",
  "url": "%s/jobs/backend-intern",
  "identifier": {"value": "backend-intern-2026"},
  "hiringOrganization": {"name": "Acme Labs"},
  "jobLocation": {
    "@type": "Place",
    "address": {
      "addressLocality": "New York",
      "addressRegion": "NY",
      "addressCountry": "US"
    }
  }
}
</script>
</head>
<body>Careers</body>
</html>`, server.URL)
	}))
	defer server.Close()

	extractor := NewStaticExtractor(StaticOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Acme Labs",
		URL:  server.URL + "/careers",
		Tier: TierStaticFetch,
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "backend-intern-2026" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized JSON-LD backend internship", job)
	}
	if job.ApplyURL != server.URL+"/jobs/backend-intern" || job.Location != "New York, NY, US" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
}

func TestStaticExtractorWorkerRiskNotificationSample(t *testing.T) {
	extractor := NewStaticExtractor()
	fixedNow := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	extractor.now = func() time.Time {
		return fixedNow
	}

	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_worker_risk",
		Name: "Harness Risk Source",
		URL:  "sample://radar-harness/worker-risk-notifications/test-run",
		Tier: TierStaticFetch,
		Metadata: map[string]string{
			"harness_company_name": "Radar Harness Worker Risk",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}

	job := result.Jobs[0]
	if job.Company != "Radar Harness Worker Risk" || job.RoleFamily != "backend" || job.EmploymentType != "internship" || job.Level != "intern" {
		t.Fatalf("job = %#v, want backend internship fixture for rated harness company", job)
	}
	if job.PostedAt == nil || fixedNow.Sub(*job.PostedAt) < 90*24*time.Hour {
		t.Fatalf("posted_at = %v, want stale date older than 90 days", job.PostedAt)
	}
	evidenceText := make([]string, 0, len(job.Evidence))
	for _, item := range job.Evidence {
		evidenceText = append(evidenceText, item.Text)
	}
	if !strings.Contains(strings.ToLower(strings.Join(evidenceText, " ")), "december 2026") {
		t.Fatalf("evidence = %#v, want December 2026 graduation signal", job.Evidence)
	}
}

func TestStaticExtractorMCPEndToEndSample(t *testing.T) {
	extractor := NewStaticExtractor()
	fixedNow := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	extractor.now = func() time.Time {
		return fixedNow
	}

	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_mcp_e2e",
		Name: "Radar MCP Proof",
		URL:  "sample://radar-harness/mcp-e2e/test-run",
		Tier: TierStaticFetch,
		Metadata: map[string]string{
			"harness_company_name": "Radar MCP Local Proof Test",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}

	job := result.Jobs[0]
	if job.Company != "Radar MCP Local Proof Test" || job.Country != "Singapore" || job.RoleFamily != "backend" || job.Level != "new_grad" {
		t.Fatalf("job = %#v, want normalized Singapore backend entry-level proof job", job)
	}
	if !strings.Contains(job.SourceJobID, "mcp-e2e-backend-platform") || !strings.Contains(job.ApplyURL, "mcp-e2e") {
		t.Fatalf("job identifiers = %q %q, want stable mcp-e2e identifiers", job.SourceJobID, job.ApplyURL)
	}
}

func TestStaticExtractorDoesNotReturnSamplesForUnstructuredPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><h1>Careers</h1><p>No jobs today.</p></body></html>`))
	}))
	defer server.Close()

	extractor := NewStaticExtractor(StaticOptions{Client: server.Client()})
	_, err := extractor.Extract(context.Background(), Source{
		ID:   "source_static",
		Name: "Acme Labs",
		URL:  server.URL + "/careers",
		Tier: TierStaticFetch,
	})
	if !errors.Is(err, ErrNoJobs) {
		t.Fatalf("Extract() error = %v, want ErrNoJobs", err)
	}
}

func TestStaticExtractorDiscoversJSONLDJobsFromSitemap(t *testing.T) {
	requests := map[string]int{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		switch r.URL.Path {
		case "/careers":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<html><head><link rel="sitemap" href="/jobs-sitemap.xml"></head><body>Open roles</body></html>`)
		case "/jobs-sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>%s/jobs/backend-intern</loc><lastmod>2026-06-21</lastmod></url>
  <url><loc>%s/blog/company-update</loc><lastmod>2026-06-20</lastmod></url>
  <url><loc>%s/jobs/frontend-new-grad</loc><lastmod>2026-06-19</lastmod></url>
</urlset>`, server.URL, server.URL, server.URL)
		case "/jobs/backend-intern":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><script type="application/ld+json">{
				"@context": "https://schema.org",
				"@type": "JobPosting",
				"title": "Backend Software Engineering Intern",
				"description": "Build ingestion workers and queue leases.",
				"datePosted": "2026-06-21",
				"employmentType": "INTERN",
				"url": "%s/jobs/backend-intern",
				"identifier": {"value": "backend-intern"},
				"hiringOrganization": {"name": "Acme Labs"},
				"jobLocation": {"@type": "Place", "address": {"addressLocality": "Singapore", "addressCountry": "SG"}}
			}</script>`, server.URL)
		case "/jobs/frontend-new-grad":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><script type="application/ld+json">{
				"@context": "https://schema.org",
				"@type": "JobPosting",
				"title": "Frontend New Grad Software Engineer",
				"description": "Own polished React command surfaces.",
				"datePosted": "2026-06-19",
				"employmentType": "FULL_TIME",
				"url": "%s/jobs/frontend-new-grad",
				"identifier": {"value": "frontend-new-grad"},
				"hiringOrganization": {"name": "Acme Labs"},
				"jobLocation": {"@type": "Place", "address": {"addressLocality": "New York", "addressRegion": "NY", "addressCountry": "US"}}
			}</script>`, server.URL)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewStaticExtractor(StaticOptions{Client: server.Client(), MaxSitemapURLs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_static_sitemap",
		Name: "Acme Labs",
		URL:  server.URL + "/careers",
		Tier: TierStaticFetch,
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2: %#v", len(result.Jobs), result.Jobs)
	}
	if requests["/blog/company-update"] != 0 {
		t.Fatalf("blog page fetched %d times, want filtered out by job URL heuristic", requests["/blog/company-update"])
	}
	if result.Jobs[0].SourceJobID != "backend-intern" || result.Jobs[0].RoleFamily != "backend" || result.Jobs[0].Country != "Singapore" {
		t.Fatalf("first job = %#v, want normalized sitemap detail job", result.Jobs[0])
	}
	if result.Jobs[1].SourceJobID != "frontend-new-grad" || result.Jobs[1].RoleFamily != "frontend" || result.Jobs[1].Country != "US" {
		t.Fatalf("second job = %#v, want normalized sitemap detail job", result.Jobs[1])
	}
	if result.RawEvidence[0].Field != "static_sitemap" {
		t.Fatalf("raw evidence = %#v, want sitemap extraction evidence", result.RawEvidence)
	}
}

func TestStaticExtractorDiscoversJSONLDJobsFromSitemapIndex(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/careers":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<html><body>Careers</body></html>`)
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <sitemap><loc>%s/careers-sitemap.xml</loc></sitemap>
</sitemapindex>`, server.URL)
		case "/careers-sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>%s/careers/jobs/platform-engineer</loc></url>
</urlset>`, server.URL)
		case "/careers/jobs/platform-engineer":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><script type="application/ld+json">{
				"@context": "https://schema.org",
				"@type": "JobPosting",
				"title": "Platform Software Engineer",
				"description": "Build source discovery pipelines.",
				"datePosted": "2026-06-18",
				"url": "%s/careers/jobs/platform-engineer",
				"identifier": {"value": "platform-engineer"},
				"hiringOrganization": {"name": "Acme Labs"},
				"jobLocation": {"@type": "Place", "address": {"addressLocality": "London", "addressCountry": "GB"}}
			}</script>`, server.URL)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewStaticExtractor(StaticOptions{Client: server.Client(), MaxSitemapURLs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_static_sitemap_index",
		Name: "Acme Labs",
		URL:  server.URL + "/careers",
		Tier: TierStaticFetch,
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	if result.Jobs[0].SourceJobID != "platform-engineer" || result.Jobs[0].Country != "UK" {
		t.Fatalf("job = %#v, want sitemap-index-discovered UK job", result.Jobs[0])
	}
}

func TestStaticExtractorBoundsSitemapDetailFetchAttempts(t *testing.T) {
	detailRequests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/careers":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<html><head><link rel="sitemap" href="/jobs-sitemap.xml"></head><body>Open roles</body></html>`)
		case "/jobs-sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>%s/jobs/one</loc></url>
  <url><loc>%s/jobs/two</loc></url>
  <url><loc>%s/jobs/three</loc></url>
</urlset>`, server.URL, server.URL, server.URL)
		case "/jobs/one", "/jobs/two", "/jobs/three":
			detailRequests++
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<html><body>No structured data here.</body></html>`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewStaticExtractor(StaticOptions{Client: server.Client(), MaxSitemapURLs: 2})
	_, err := extractor.Extract(context.Background(), Source{
		ID:   "source_static_bounded_sitemap",
		Name: "Acme Labs",
		URL:  server.URL + "/careers",
		Tier: TierStaticFetch,
	})
	if !errors.Is(err, ErrNoJobs) {
		t.Fatalf("Extract() error = %v, want ErrNoJobs", err)
	}
	if detailRequests != 2 {
		t.Fatalf("detail requests = %d, want bounded 2", detailRequests)
	}
}
