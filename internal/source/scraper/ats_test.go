package scraper

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNewATSExtractorUsesBoundedDefaultHTTPClient(t *testing.T) {
	extractor := NewATSExtractor(ATSOptions{})
	if extractor.client == nil {
		t.Fatal("client is nil, want bounded default HTTP client")
	}
	if extractor.client == http.DefaultClient {
		t.Fatal("client uses http.DefaultClient, want bounded private client")
	}
	if extractor.client.Timeout != defaultATSHTTPTimeout {
		t.Fatalf("client timeout = %s, want %s", extractor.client.Timeout, defaultATSHTTPTimeout)
	}
	if extractor.workdayPageSize != 20 {
		t.Fatalf("workday page size = %d, want provider-safe default 20", extractor.workdayPageSize)
	}
	if extractor.byteDancePageSize != 50 || extractor.byteDanceMaxPages != 10 || extractor.byteDanceMaxJobs != 500 {
		t.Fatalf("ByteDance pagination defaults = %d/%d/%d", extractor.byteDancePageSize, extractor.byteDanceMaxPages, extractor.byteDanceMaxJobs)
	}
}

func TestEmploymentFromTextUsesTimingWordBoundaries(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{title: "Internal Tools Engineer", want: "full_time"},
		{title: "Internals Software Engineer", want: "full_time"},
		{title: "International Software Engineer", want: "full_time"},
		{title: "Cooperative Systems Engineer", want: "full_time"},
		{title: "Software Engineer Intern", want: "internship"},
		{title: "Software Engineering Internship", want: "internship"},
		{title: "Software Engineer Co-op", want: "internship"},
		{title: "Software Engineer Coop", want: "internship"},
	}

	for _, test := range tests {
		t.Run(test.title, func(t *testing.T) {
			if got := employmentFromText(test.title, ""); got != test.want {
				t.Fatalf("employmentFromText(%q, %q) = %q, want %q", test.title, "", got, test.want)
			}
		})
	}
}

func TestATSExtractorDefaultClientBlocksPrivateTargets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jobs":[]}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{GreenhouseBaseURL: server.URL + "/v1/boards"})
	_, err := extractor.Extract(context.Background(), Source{
		ID:   "private_greenhouse_source",
		Name: "Private Greenhouse",
		URL:  "https://boards.greenhouse.io/acme",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "greenhouse",
		},
	})
	if err == nil {
		t.Fatal("Extract() error = nil, want private target rejection")
	}
	if !strings.Contains(err.Error(), "resolved private address blocked") {
		t.Fatalf("Extract() error = %q, want private target rejection", err)
	}
}

func TestNewATSExtractorPreservesProvidedHTTPClient(t *testing.T) {
	client := &http.Client{Timeout: 123 * time.Millisecond}
	extractor := NewATSExtractor(ATSOptions{Client: client})
	if extractor.client != client {
		t.Fatal("provided HTTP client was not preserved")
	}
}

func TestATSExtractorExtractsGreenhouseJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/boards/acme/jobs" {
			t.Fatalf("path = %q, want greenhouse jobs path", r.URL.Path)
		}
		if r.URL.Query().Get("content") != "true" {
			t.Fatalf("content query = %q, want true", r.URL.Query().Get("content"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"jobs": [
				{
					"id": 123,
					"title": "Software Engineering Intern, Backend Platform - Summer 2026",
					"updated_at": "2026-02-12T09:30:00Z",
					"location": {"name": "New York, NY, United States"},
					"absolute_url": "https://boards.greenhouse.io/acme/jobs/123",
					"content": "<p>Build distributed services. Candidates graduating in 2026 encouraged.</p>",
					"departments": [{"name": "Engineering"}],
					"offices": [{"name": "New York", "location": "New York, NY, United States"}]
				}
			]
		}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:            server.Client(),
		GreenhouseBaseURL: server.URL + "/v1/boards",
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_greenhouse",
		Name: "Acme",
		URL:  "https://boards.greenhouse.io/acme",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "greenhouse",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.9 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "greenhouse:123" || job.Company != "Acme" || job.Level != "internship" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized greenhouse internship", job)
	}
	if job.ApplyURL != "https://boards.greenhouse.io/acme/jobs/123" || job.SourceURL != "https://boards.greenhouse.io/acme" {
		t.Fatalf("urls = source %q apply %q", job.SourceURL, job.ApplyURL)
	}
	if len(job.Evidence) == 0 {
		t.Fatal("job evidence should include source content")
	}
}

func TestATSExtractorExtractsByteDanceCareersJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/public/supplier/search/job/posts" {
			t.Fatalf("path = %q, want ByteDance search endpoint", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if r.Header.Get("website-path") != "en" || r.Header.Get("Origin") != "https://joinbytedance.com" {
			t.Fatalf("headers missing ByteDance public API contract: website-path=%q origin=%q", r.Header.Get("website-path"), r.Header.Get("Origin"))
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["keyword"] != "software engineer intern" {
			t.Fatalf("keyword = %v, want software engineer intern", req["keyword"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 0,
			"data": {
				"job_post_list": [
					{
						"id": "7595707875767699765",
						"code": "A37322A",
						"title": "Software Engineer Intern (Developer Infrastructure) - 2026 Summer (BS/MS)",
						"description": "Build Cloud IDE, CI/CD systems, Kubernetes and backend infrastructure.",
						"requirement": "Currently pursuing an Undergraduate/Master in Software Development or Computer Science.",
						"recruit_type": {"id": "202", "en_name": "Intern"},
						"job_category": {"id": "6704215862603155720", "en_name": "R&D"},
						"city_info": {
							"code": "CT_1103355",
							"en_name": "San Jose",
							"parent": {
								"code": "ST_31",
								"en_name": "California",
								"parent": {
									"code": "CN_6",
									"en_name": "United States of America"
								}
							}
						},
						"job_subject": {"id": "7459987887569733896", "en_name": "Undergraduate/Master Intern - 2026 Start"}
					}
				],
				"count": 1
			},
			"message": "ok"
		}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:           server.Client(),
		ByteDanceBaseURL: server.URL + "/api/v1/public/supplier",
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_bytedance",
		Name: "ByteDance",
		URL:  "https://jobs.bytedance.com/en/position?keywords=software%20engineer%20intern",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "bytedance_careers",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.8 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "bytedance_careers:7595707875767699765" || job.Company != "ByteDance" || job.Level != "internship" {
		t.Fatalf("job = %#v, want normalized ByteDance internship", job)
	}
	if job.Country != "US" || !strings.Contains(job.Location, "San Jose") {
		t.Fatalf("location/country = %q/%q, want San Jose US", job.Location, job.Country)
	}
	if job.ApplyURL != "https://jobs.bytedance.com/en/position/7595707875767699765" {
		t.Fatalf("apply URL = %q", job.ApplyURL)
	}
}

func TestATSExtractorPaginatesByteDanceCareersJobs(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var req byteDanceSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Offset {
		case 0:
			_, _ = w.Write([]byte(`{"code":0,"data":{"job_post_list":[{"id":"1","title":"Software Engineer Intern"},{"id":"2","title":"Backend Engineer Intern"}],"count":3}}`))
		case 2:
			_, _ = w.Write([]byte(`{"code":0,"data":{"job_post_list":[{"id":"3","title":"Machine Learning Engineer Intern"}],"count":3}}`))
		default:
			t.Fatalf("unexpected offset %d", req.Offset)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client: server.Client(), ByteDanceBaseURL: server.URL,
		ByteDancePageSize: 2, ByteDanceMaxPages: 3, ByteDanceMaxJobs: 10,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID: "source_bytedance", Name: "ByteDance", URL: "https://jobs.bytedance.com/en/position?keywords=software%20engineer%20intern",
		Tier: TierATS, Metadata: map[string]string{"source_kind": "bytedance_careers"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || len(result.Jobs) != 3 {
		t.Fatalf("requests=%d jobs=%d", requests, len(result.Jobs))
	}
	if result.Diagnostics["completeness_status"] != "complete" || result.Diagnostics["pages_fetched"] != "2" || result.Diagnostics["total_available"] != "3" {
		t.Fatalf("diagnostics=%#v", result.Diagnostics)
	}
}

func TestByteDanceTimingIgnoresDescriptionInternMentions(t *testing.T) {
	graduate, ok := byteDancePosting(Source{Name: "ByteDance"}, "https://example.test/api", byteDanceJobPost{
		ID:          "graduate-1",
		Title:       "Applied Machine Learning Production Engineer Graduate",
		Description: "You will mentor interns and improve internal tooling.",
		RecruitType: byteDanceNamedItem{ENName: "Regular"},
	})
	if !ok || graduate.Level != "new_grad" {
		t.Fatalf("graduate posting = %#v ok=%v, want new_grad", graduate, ok)
	}

	experienced, ok := byteDancePosting(Source{Name: "ByteDance"}, "https://example.test/api", byteDanceJobPost{
		ID:          "experienced-1",
		Title:       "Production Engineer",
		Description: "You will mentor interns and improve internal tooling.",
		RecruitType: byteDanceNamedItem{ENName: "Regular"},
	})
	if !ok || experienced.Level != "unknown" {
		t.Fatalf("experienced posting = %#v ok=%v, want unknown", experienced, ok)
	}
}

func TestATSExtractorExtractsJobsynJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/solr/search" {
			t.Fatalf("path = %q, want jobsyn search endpoint", r.URL.Path)
		}
		if r.Header.Get("X-Origin") != "metacareers.dejobs.org" {
			t.Fatalf("X-Origin = %q, want metacareers.dejobs.org", r.Header.Get("X-Origin"))
		}
		if r.URL.Query().Get("q") != "software engineer intern" || r.URL.Query().Get("page") != "1" || r.URL.Query().Get("num_items") != "2" {
			t.Fatalf("query = %q, want q/page/num_items contract", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"jobs": [
				{
					"guid": "C31EE8AD36ED44C49BBA84A0B725CDEA",
					"reqid": "a1KDp00000CZKSGMA5",
					"company_exact": "Meta",
					"title_exact": "Software Engineer Intern, Product Infrastructure",
					"title_slug": "software-engineer-intern-product-infrastructure",
					"location_exact": "Menlo Park, CA",
					"country_exact": "United States",
					"description": "<p>Build backend systems for internship candidates graduating in 2027.</p>",
					"date_new": "2026-04-22T03:22:20Z",
					"is_posted": true
				}
			],
			"pagination": {"has_more_pages": false, "page": 1.0, "page_size": 2.0, "total": 1.0, "total_pages": 1.0}
		}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:         server.Client(),
		JobsynBaseURL:  server.URL + "/api",
		JobsynPageSize: 2,
		JobsynMaxPages: 1,
		JobsynMaxJobs:  2,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_meta",
		Name: "Meta",
		URL:  "https://metacareers.dejobs.org/jobs/?q=software%20engineer%20intern",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "jobsyn",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.8 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "jobsyn:a1KDp00000CZKSGMA5" || job.Company != "Meta" || job.Level != "internship" || job.RoleFamily != "backend" {
		t.Fatalf("job = %#v, want normalized Meta internship", job)
	}
	if job.Country != "US" || job.Location != "Menlo Park, CA" {
		t.Fatalf("location/country = %q/%q, want Menlo Park US", job.Location, job.Country)
	}
	if job.ApplyURL != "https://metacareers.dejobs.org/menlo-park-ca/software-engineer-intern-product-infrastructure/C31EE8AD36ED44C49BBA84A0B725CDEA/job/" {
		t.Fatalf("apply URL = %q", job.ApplyURL)
	}
}

func TestATSExtractorExtractsCitadelSecuritiesCareerSitemapJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/career-sitemap.xml" {
			t.Fatalf("path = %q, want career sitemap", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>` + serverURL(r) + `/careers/details/software-engineer-intern-us/</loc>
    <lastmod>2026-07-07T12:22:30+00:00</lastmod>
  </url>
  <url>
    <loc>` + serverURL(r) + `/careers/details/cloud-platform-engineer/</loc>
    <lastmod>2026-07-07T12:22:31+00:00</lastmod>
  </url>
  <url>
    <loc>` + serverURL(r) + `/careers/details/quantitative-developer-research-engineer/</loc>
    <lastmod>2026-07-07T12:22:32+00:00</lastmod>
  </url>
  <url>
    <loc>` + serverURL(r) + `/careers/details/fpga-engineer-intern-us/</loc>
    <lastmod>2026-07-07T12:22:33+00:00</lastmod>
  </url>
  <url>
    <loc>` + serverURL(r) + `/careers/details/quantitative-trader-intern-us/</loc>
    <lastmod>2026-07-07T12:22:34+00:00</lastmod>
  </url>
  <url>
    <loc>` + serverURL(r) + `/careers/details/campus-referrals-software-engineering-us/</loc>
    <lastmod>2026-07-07T12:22:35+00:00</lastmod>
  </url>
</urlset>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:                   server.Client(),
		CitadelSecuritiesMaxJobs: 10,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_citadel_securities",
		Name: "Citadel Securities",
		URL:  server.URL + "/careers/open-opportunities/",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "citadel_securities_careers",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.6 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 3 {
		t.Fatalf("jobs = %d, want 3 filtered engineering jobs", len(result.Jobs))
	}
	if result.Jobs[0].Title != "Software Engineer - Intern (US)" || result.Jobs[0].Level != "internship" || result.Jobs[0].Country != "US" {
		t.Fatalf("first job = %#v, want US software engineer internship", result.Jobs[0])
	}
	if result.Jobs[1].Title != "Cloud Platform Engineer" || result.Jobs[2].Title != "Quantitative Developer / Research Engineer" {
		t.Fatalf("jobs = %#v, want platform and quant developer roles after internship", result.Jobs)
	}
	for _, job := range result.Jobs {
		if strings.Contains(strings.ToLower(job.Title), "fpga") || strings.Contains(strings.ToLower(job.Title), "trader") || strings.Contains(strings.ToLower(job.ApplyURL), "campus-referrals") {
			t.Fatalf("unexpected noisy Citadel job survived filtering: %#v", job)
		}
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func TestATSExtractorExtractsIBMCareersJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/api/v2" {
			t.Fatalf("path = %q, want IBM search endpoint", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["appId"] != "careers" {
			t.Fatalf("appId = %v, want careers", req["appId"])
		}
		query, ok := req["query"].(map[string]any)
		if !ok {
			t.Fatalf("query = %#v, want object", req["query"])
		}
		boolQuery, ok := query["bool"].(map[string]any)
		if !ok {
			t.Fatalf("query.bool = %#v, want object", query["bool"])
		}
		must, ok := boolQuery["must"].([]any)
		if !ok || len(must) != 1 {
			t.Fatalf("query.bool.must = %#v, want one clause", boolQuery["must"])
		}
		clause, ok := must[0].(map[string]any)
		if !ok {
			t.Fatalf("query clause = %#v, want object", must[0])
		}
		simpleQuery, ok := clause["simple_query_string"].(map[string]any)
		if !ok || simpleQuery["query"] != "software engineer" {
			t.Fatalf("simple query = %#v, want current IBM search contract", clause)
		}
		fields, ok := simpleQuery["fields"].([]any)
		if !ok || len(fields) == 0 {
			t.Fatalf("simple query fields = %#v, want explicit allowlist", simpleQuery["fields"])
		}
		if _, legacy := clause["query_string"]; legacy {
			t.Fatalf("query clause = %#v, legacy query_string is rejected by IBM", clause)
		}
		filter, ok := req["post_filter"].(map[string]any)
		if !ok || filter["terms"] == nil {
			t.Fatalf("post_filter = %#v, want terms career-stage filter", req["post_filter"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"hits": {
				"total": {"value": 1, "relation": "eq"},
				"hits": [{
					"_id": "hit_1",
					"_score": 11.15,
					"_source": {
						"url": "https://careers.ibm.com/careers/JobDetail?jobId=109738",
						"title": "Application Developer Intern",
						"description": "Software Developers at IBM design, code, test, and provide industry-leading solutions.",
						"field_keyword_08": "Software Engineering",
						"field_keyword_17": "Hybrid",
						"field_keyword_18": "Internship",
						"field_keyword_19": "Vilnius, LT"
					}
				}, {
					"_id": "hit_sales",
					"_score": 10.0,
					"_source": {
						"url": "https://careers.ibm.com/careers/JobDetail?jobId=121780",
						"title": "Entry level Technical Sales Specialist Data - IBM Norway",
						"description": "Work with clients on technical sales motions.",
						"field_keyword_08": "Data & Analytics",
						"field_keyword_17": "Hybrid",
						"field_keyword_18": "Entry Level",
						"field_keyword_19": "Oslo, NO"
					}
				}]
			}
		}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:              server.Client(),
		IBMSearchAPIBaseURL: server.URL + "/search/api/v2",
		IBMSearchMaxJobs:    5,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_ibm",
		Name: "IBM",
		URL:  "https://www.ibm.com/careers/search?q=software%20engineer",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "ibm_careers",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "ibm:109738" || job.Company != "IBM" || job.Level != "internship" || job.RoleFamily != "software_engineering" {
		t.Fatalf("job = %#v, want normalized IBM internship", job)
	}
	if job.Country != "LT" || job.Location != "Vilnius, LT" {
		t.Fatalf("location/country = %q/%q, want Vilnius LT", job.Location, job.Country)
	}
	if job.ApplyURL != "https://careers.ibm.com/careers/JobDetail?jobId=109738" {
		t.Fatalf("apply URL = %q", job.ApplyURL)
	}
}

func TestATSExtractorExtractsWhatnotCareersJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/api/jobs" {
			t.Fatalf("path = %q, want Whatnot jobs API", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results": [
				{
					"id": "posting-1001",
					"jobId": "job-1001",
					"title": "Software Engineer Intern, Backend Platform",
					"departmentName": "Engineering",
					"teamName": "Infrastructure",
					"locationName": "San Francisco, CA",
					"secondaryLocationNames": ["New York, NY", "San Francisco, CA"],
					"workplaceType": "Hybrid",
					"employmentType": "Internship",
					"isListed": true,
					"publishedDate": "2026-06-18",
					"applyLink": "https://jobs.ashbyhq.com/whatnot/posting-1001",
					"compensationTierSummary": "$45/hour"
				},
				{
					"id": "posting-hidden",
					"title": "Hidden Role",
					"isListed": false,
					"applyLink": "https://jobs.ashbyhq.com/whatnot/posting-hidden"
				},
				{
					"id": "posting-1002",
					"title": "New Grad Software Engineer, AI Platform",
					"departmentName": "Engineering",
					"locationName": "New York, NY",
					"employmentType": "FullTime",
					"isListed": true,
					"publishedDate": "2026-06-20",
					"externalLink": "https://jobs.ashbyhq.com/whatnot/posting-1002"
				},
				{
					"id": "posting-over-cap",
					"title": "Software Engineer",
					"locationName": "Seattle, WA",
					"isListed": true,
					"applyLink": "https://jobs.ashbyhq.com/whatnot/posting-over-cap"
				}
			]
		}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), WhatnotMaxJobs: 2})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_whatnot",
		Name: "Whatnot",
		URL:  server.URL + "/careers",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "whatnot_careers",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.9 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two bounded Whatnot jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "whatnot:posting-1001" || first.Company != "Whatnot" || first.Level != "internship" || first.RoleFamily != "backend" || first.Country != "US" {
		t.Fatalf("first job = %#v, want normalized Whatnot backend internship", first)
	}
	if first.Location != "San Francisco, CA; New York, NY" || first.ApplyURL != "https://jobs.ashbyhq.com/whatnot/posting-1001" {
		t.Fatalf("first location/apply = %q/%q", first.Location, first.ApplyURL)
	}
	if first.PostedAt == nil || first.PostedAt.Format(time.DateOnly) != "2026-06-18" {
		t.Fatalf("first posted_at = %v, want 2026-06-18", first.PostedAt)
	}
	second := result.Jobs[1]
	if second.SourceJobID != "whatnot:posting-1002" || second.Level != "new_grad" || second.RoleFamily != "ml_ai" {
		t.Fatalf("second job = %#v, want normalized Whatnot new-grad AI role", second)
	}
}

func TestATSExtractorExtractsPaginatedWalmartCareersJobs(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/api/ai/search-ai/api/v1/combined/hybrid-search" {
			t.Fatalf("path = %q, want Walmart hybrid search endpoint", r.URL.Path)
		}
		if r.URL.Query().Get("size") != "2" || r.URL.Query().Get("locale") != "en_US" {
			t.Fatalf("query = %q, want size and locale", r.URL.RawQuery)
		}
		var req walmartSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Query != "software engineer intern" || req.BasicSearch || req.Filter != "brand IN [Walmart]" || req.Locale != "en_US" {
			t.Fatalf("request = %#v, want Walmart public search contract", req)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "0":
			_, _ = w.Write([]byte(`{
				"totalJobs": 3,
				"jobs": [
					{
						"id": "R-1001-External",
						"text": "Build backend Go services and distributed job systems for a software engineering internship.",
						"metadata": {
							"title": "Software Engineer Intern, Backend Platform",
							"jobPostingTitle": "Software Engineer Intern, Backend Platform",
							"jobId": "R-1001",
							"jobPostingStartDate": 1782777600000,
							"primaryLocationCity": "BENTONVILLE",
							"primaryLocationState": "AR",
							"primaryLocationCountry": "US",
							"requisitionStatus": "Open",
							"employmentTypes": ["Internship"],
							"brand": "Walmart",
							"categories": ["Software Engineering and Architecture"],
							"areas": ["Technology"]
						}
					},
					{
						"id": "R-CLOSED-External",
						"text": "Closed posting",
						"metadata": {
							"jobPostingTitle": "Software Engineer",
							"jobId": "R-CLOSED",
							"requisitionStatus": "Closed"
						}
					}
				]
			}`))
		case "1":
			_, _ = w.Write([]byte(`{
				"totalJobs": 3,
				"jobs": [{
					"id": "R-1002-External",
					"text": "Train and deploy machine learning models on the AI platform for new graduates.",
					"metadata": {
						"jobPostingTitle": "New Grad Machine Learning Engineer",
						"jobId": "R-1002",
						"jobPostingStartDate": 1782950400000,
						"primaryLocationCity": "SUNNYVALE",
						"primaryLocationState": "CA",
						"primaryLocationCountry": "US",
						"requisitionStatus": "Open",
						"employmentTypes": ["Full time"],
						"brand": "Walmart",
						"categories": ["Data Science and Analytics"],
						"areas": ["Technology"]
					}
				}]
			}`))
		default:
			t.Fatalf("unexpected page %q", r.URL.Query().Get("page"))
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:          server.Client(),
		WalmartPageSize: 2,
		WalmartMaxPages: 3,
		WalmartMaxJobs:  3,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_walmart",
		Name: "Walmart",
		URL:  server.URL + "/us/en/results?searchQuery=software%20engineer%20intern",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind":    "walmart_careers",
			"walmart_filter": "brand IN [Walmart]",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want two bounded pages", requests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.89 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two open Walmart jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "walmart:R-1001" || first.Company != "Walmart" || first.Level != "internship" || first.RoleFamily != "backend" || first.Country != "US" {
		t.Fatalf("first job = %#v, want normalized Walmart backend internship", first)
	}
	if first.ApplyURL != server.URL+"/us/en/jobs/R-1001" || first.Location != "BENTONVILLE, AR, US" {
		t.Fatalf("first location/apply = %q/%q", first.Location, first.ApplyURL)
	}
	if first.PostedAt == nil || first.PostedAt.Format(time.DateOnly) != "2026-06-30" {
		t.Fatalf("first posted_at = %v, want 2026-06-30", first.PostedAt)
	}
	second := result.Jobs[1]
	if second.SourceJobID != "walmart:R-1002" || second.Level != "new_grad" || second.RoleFamily != "ml_ai" {
		t.Fatalf("second job = %#v, want normalized Walmart new-grad AI role", second)
	}
}

func TestATSExtractorExtractsWorldQuantCareerListingJobs(t *testing.T) {
	page := `<html><body>
		<a class="fo-link" href=".?id=outside"><h4>Outside Listing</h4></a>
		<ul class="cg-list" id="careers_list">
			<li><a class="fo-link" href=".?id=4069460006"><h4 class="h4">Data Science Intern</h4><div><span class="fo-location">New York, United States</span></div></a></li>
			<li><a class="card fo-link active" href=".?id=4611235006"><h4 class="h4">Senior Python Engineer - AI/ML Systems</h4><div><span class="fo-location">Yerevan, Armenia</span></div></a></li>
			<li><a class="fo-link" href=".?id=over-cap"><h4 class="h4">Quantitative Researcher</h4><div><span class="fo-location">London, United Kingdom</span></div></a></li>
		</ul>
	</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/career-listing/" {
			t.Fatalf("request = %s %s, want WorldQuant career listing", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), WorldQuantMaxJobs: 2})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_worldquant",
		Name: "WorldQuant",
		URL:  server.URL + "/career-listing/",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "worldquant_careers",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.85 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two bounded WorldQuant jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "worldquant:4069460006" || first.Level != "internship" || first.RoleFamily != "data" || first.Country != "US" {
		t.Fatalf("first job = %#v, want normalized WorldQuant data internship", first)
	}
	if first.ApplyURL != server.URL+"/career-listing/?id=4069460006" {
		t.Fatalf("first apply URL = %q", first.ApplyURL)
	}
	second := result.Jobs[1]
	if second.SourceJobID != "worldquant:4611235006" || second.RoleFamily != "ml_ai" || second.Country != "Armenia" {
		t.Fatalf("second job = %#v, want normalized WorldQuant AI/ML role", second)
	}
}

func TestATSExtractorExtractsIMCCareersSSRJobs(t *testing.T) {
	page := `
<div>
  <a class="job-card" href="/us/careers/jobs/4823923101">
    <h2>Quantitative Trader Intern - Summer 2027</h2>
    <span>Chicago</span>
  </a>
  <a class="apply" href="/us/careers/jobs/4823923101/apply"><span>Apply Now</span></a>
  <a class="job-card" href="/us/careers/jobs/4907368101">
    <h2>Graduate Quantitative Researcher (BS/MS)</h2>
    <span>Chicago</span>
  </a>
  <a class="job-card duplicate" href="/us/careers/jobs/4907368101">
    <h2>Graduate Quantitative Researcher (BS/MS)</h2>
    <span>Chicago</span>
  </a>
</div>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/us/search-careers" {
			t.Fatalf("path = %q, want IMC search careers", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_imc",
		Name: "IMC",
		URL:  server.URL + "/us/search-careers",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "imc_careers",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2 deduped jobs", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "imc_careers:4823923101" || job.Level != "internship" || job.Location != "Chicago" {
		t.Fatalf("job = %#v, want normalized IMC intern in Chicago", job)
	}
	if job.ApplyURL != server.URL+"/us/careers/jobs/4823923101/apply" {
		t.Fatalf("apply URL = %q", job.ApplyURL)
	}
	if result.Jobs[1].Level != "new_grad" {
		t.Fatalf("graduate level = %q, want new_grad", result.Jobs[1].Level)
	}
}

func TestGreenhouseBoardTokenSupportsJobBoardsHost(t *testing.T) {
	token, err := greenhouseBoardToken("https://job-boards.greenhouse.io/reddit/jobs/8107752")
	if err != nil {
		t.Fatal(err)
	}
	if token != "reddit" {
		t.Fatalf("token = %q, want reddit", token)
	}
}

func TestGreenhouseBoardTokenSupportsBoardsAPIURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://boards-api.greenhouse.io/v1/boards/later/jobs", "later"},
		{"https://boards-api.eu.greenhouse.io/v1/boards/axiomaticai/jobs", "axiomaticai"},
		{"https://boards-api.greenhouse.io/v1/boards/offerup", "offerup"},
	}
	for _, tt := range tests {
		token, err := greenhouseBoardToken(tt.url)
		if err != nil {
			t.Fatalf("greenhouseBoardToken(%q) error = %v", tt.url, err)
		}
		if token != tt.want {
			t.Fatalf("greenhouseBoardToken(%q) = %q, want %q", tt.url, token, tt.want)
		}
	}
}

func TestATSKindIgnoresGenericMetadataKind(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]string
		url      string
		want     string
	}{
		{"generic ats falls back to host", map[string]string{"source_kind": "ats"}, "https://api.lever.co/v0/postings/wyetechllc", "lever"},
		{"generic ats_adapter falls back to host", map[string]string{"kind": "ats_adapter"}, "https://boards-api.greenhouse.io/v1/boards/later/jobs", "greenhouse"},
		{"Whatnot careers host", nil, "https://jobs.whatnot.com/", "whatnot_careers"},
		{"Walmart careers host", nil, "https://careers.walmart.com/us/en/results", "walmart_careers"},
		{"WorldQuant careers host", nil, "https://www.worldquant.com/career-listing/", "worldquant_careers"},
		{"specific kind still wins", map[string]string{"source_kind": "greenhouse"}, "https://example.com/careers", "greenhouse"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := atsKind(Source{URL: tt.url, Metadata: tt.metadata, Tier: TierATS})
			if got != tt.want {
				t.Fatalf("atsKind() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestATSExtractorExtractsGitHubCommunityJobList(t *testing.T) {
	markdown := `
<table>
<tr><th>Company</th><th>Role</th><th>Location</th><th>Application</th><th>Age</th></tr>
<tr>
  <td>🔥 <a href="https://later.example">Later</a></td>
  <td>Backend Software Engineer Intern 🛂</td>
  <td>Remote in US</td>
  <td>🇺🇸 <a href="https://jobs.later.example/apply/backend-intern">Apply</a> <a href="https://simplify.jobs/p/later-backend">Simplify</a></td>
  <td>0d</td>
</tr>
<tr>
  <td>↳</td>
  <td>Frontend New Grad Engineer 🎓</td>
  <td>Toronto, Canada</td>
  <td><a href="https://simplify.jobs/p/later-frontend">Simplify</a> <a href="https://jobs.later.example/apply/frontend-new-grad">Apply</a></td>
  <td>2d</td>
</tr>
<tr>
  <td>ClosedCo</td>
  <td>Software Engineer Intern</td>
  <td>New York, NY</td>
  <td>Closed</td>
  <td>1d</td>
</tr>
</table>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/README.md" {
			t.Fatalf("path = %q, want /README.md", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte(markdown))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:               server.Client(),
		GitHubJobListMaxJobs: 10,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_github_list",
		Name: "SimplifyJobs",
		URL:  "https://github.com/SimplifyJobs/Summer2026-Internships",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind":    "github_job_list",
			"github_raw_url": server.URL + "/README.md",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.7 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2: %#v", len(result.Jobs), result.Jobs)
	}

	first := result.Jobs[0]
	if first.Company != "Later" || first.Title != "Backend Software Engineer Intern" || first.Country != "US" || first.Level != "internship" || first.EmploymentType != "internship" {
		t.Fatalf("first job = %#v, want normalized US internship", first)
	}
	if first.ApplyURL != "https://jobs.later.example/apply/backend-intern" || !strings.HasPrefix(first.SourceJobID, "github_job_list:later:") {
		t.Fatalf("first job ids/urls = %#v", first)
	}
	if first.PostedAt == nil {
		t.Fatalf("first posted_at = nil, want age-derived date")
	}
	if evidenceText(first.Evidence, "visa") == "" || evidenceText(first.Evidence, "authorization") == "" || evidenceText(first.Evidence, "priority") == "" {
		t.Fatalf("first evidence = %#v, want visa, authorization, and priority markers preserved", first.Evidence)
	}

	second := result.Jobs[1]
	if second.Company != "Later" || second.Title != "Frontend New Grad Engineer" || second.Country != "Canada" || second.Level != "new_grad" {
		t.Fatalf("second job = %#v, want inherited company Canada new-grad", second)
	}
	if second.ApplyURL != "https://jobs.later.example/apply/frontend-new-grad" {
		t.Fatalf("second apply_url = %q, want real apply link after Simplify helper", second.ApplyURL)
	}
	if second.PostedAt == nil || !second.PostedAt.Before(time.Now().UTC().Add(-24*time.Hour)) {
		t.Fatalf("second posted_at = %v, want age-derived stale-ish date", second.PostedAt)
	}
	if evidenceText(second.Evidence, "new_grad") == "" {
		t.Fatalf("second evidence = %#v, want new-grad marker preserved", second.Evidence)
	}
}

func TestATSExtractorExtractsGitHubMarkdownCommunityJobList(t *testing.T) {
	markdown := `
| Company | Position | Location | Salary | Posting | Age |
| --- | --- | --- | --- | --- | --- |
| 🚀 [SpeedyAI](https://speedyai.example) | Software Engineer Intern, AI Platform 🛂 | San Francisco, CA | $60/hr | [Simplify](https://simplify.jobs/p/speedyai) [Apply](https://jobs.speedyai.example/intern-ai) | 1d |
| ↳ | Software Engineer New Grad 🎓 | London, UK | | [Apply](https://jobs.speedyai.example/new-grad) | 7d |
| ClosedCo | Backend Intern | Remote | $45/hr | Closed | 0d |
`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/NEW_GRAD_INTL.md" {
			t.Fatalf("path = %q, want /NEW_GRAD_INTL.md", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte(markdown))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:               server.Client(),
		GitHubJobListMaxJobs: 10,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_speedyapply",
		Name: "SpeedyApply",
		URL:  "https://github.com/speedyapply/2026-SWE-College-Jobs/blob/main/NEW_GRAD_INTL.md",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind":    "github_job_list",
			"github_raw_url": server.URL + "/NEW_GRAD_INTL.md",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2: %#v", len(result.Jobs), result.Jobs)
	}

	first := result.Jobs[0]
	if first.Company != "SpeedyAI" || first.Title != "Software Engineer Intern, AI Platform" || first.Country != "US" || first.RoleFamily != "ml_ai" || first.Level != "internship" {
		t.Fatalf("first job = %#v, want normalized markdown internship", first)
	}
	if first.ApplyURL != "https://jobs.speedyai.example/intern-ai" {
		t.Fatalf("first apply_url = %q, want real apply link after helper link", first.ApplyURL)
	}
	if first.PostedAt == nil || evidenceText(first.Evidence, "visa") == "" {
		t.Fatalf("first posted/evidence = %v %#v, want age and visa evidence", first.PostedAt, first.Evidence)
	}
	if evidenceText(first.Evidence, "compensation") != "$60/hr" {
		t.Fatalf("first evidence = %#v, want salary evidence", first.Evidence)
	}

	second := result.Jobs[1]
	if second.Company != "SpeedyAI" || second.Title != "Software Engineer New Grad" || second.Country != "UK" || second.Level != "new_grad" {
		t.Fatalf("second job = %#v, want inherited company UK new-grad", second)
	}
	if second.ApplyURL != "https://jobs.speedyai.example/new-grad" || evidenceText(second.Evidence, "new_grad") == "" {
		t.Fatalf("second job/evidence = %#v %#v, want apply URL and new-grad evidence", second, second.Evidence)
	}
	if evidenceText(second.Evidence, "compensation") != "" {
		t.Fatalf("second evidence = %#v, want blank salary omitted", second.Evidence)
	}
}

func TestGitHubJobListRawCandidatesPreserveOffSeasonBlobPath(t *testing.T) {
	candidates, err := githubJobListRawCandidates("https://github.com/SimplifyJobs/Summer2026-Internships/blob/dev/README-Off-Season.md")
	if err != nil {
		t.Fatalf("githubJobListRawCandidates() error = %v", err)
	}
	if len(candidates) != 1 || candidates[0] != "https://raw.githubusercontent.com/SimplifyJobs/Summer2026-Internships/dev/README-Off-Season.md" {
		t.Fatalf("candidates = %#v, want off-season raw README path", candidates)
	}
}

func readRequestBody(t *testing.T, r *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestGreenhouseLocationTextUsesStructuredOfficeThenAvailableLocations(t *testing.T) {
	tests := []struct {
		name        string
		primary     string
		offices     []greenhouseOffice
		description string
		want        string
	}{
		{"specific primary", "New York, NY", []greenhouseOffice{{Location: "Austin, TX"}}, "Available Locations: Lisbon, Portugal About the role", "New York, NY"},
		{"structured office", "In-Office", []greenhouseOffice{{Location: "San Francisco, CA"}}, "Available Locations: Austin, TX About the role", "San Francisco, CA"},
		{"austin description", "In-Office", nil, "Available Locations: Austin, TX About the department", "Austin, TX"},
		{"lisbon description", "In-Office", nil, "Available Locations: Lisbon, Portugal About the department", "Lisbon, Portugal"},
		{"london description", "In-Office", nil, "Available Locations: London, United Kingdom About the team", "London, United Kingdom"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := greenhouseLocationText(test.primary, test.offices, test.description); got != test.want {
				t.Fatalf("greenhouseLocationText() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestATSExtractorExtractsLeverJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/postings/stripe" {
			t.Fatalf("path = %q, want lever postings path", r.URL.Path)
		}
		if r.URL.Query().Get("mode") != "json" {
			t.Fatalf("mode query = %q, want json", r.URL.Query().Get("mode"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"id": "lever-1",
				"text": "New Grad Software Engineer, Payments Infrastructure",
				"categories": {
					"location": "San Francisco, CA or Remote US",
					"commitment": "Full-time",
					"team": "Engineering",
					"department": "Infrastructure",
					"allLocations": ["San Francisco, CA", "Remote US"]
				},
				"country": "US",
				"descriptionPlain": "Build payment infrastructure for 2026 graduates.",
				"hostedUrl": "https://jobs.lever.co/stripe/lever-1",
				"applyUrl": "https://jobs.lever.co/stripe/lever-1/apply"
			}
		]`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:               server.Client(),
		LeverGlobalBaseURL:   server.URL + "/v0/postings",
		LeverEuropeBaseURL:   server.URL + "/v0/postings",
		AshbyJobBoardBaseURL: "https://example.invalid",
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_lever",
		Name: "Stripe",
		URL:  "https://jobs.lever.co/stripe",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "lever",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "lever:lever-1" || job.Company != "Stripe" || job.Country != "US" || job.Level != "new_grad" || job.RoleFamily != "infrastructure" {
		t.Fatalf("job = %#v, want normalized lever new-grad job", job)
	}
	if job.Location != "San Francisco, CA or Remote US" || job.ApplyURL != "https://jobs.lever.co/stripe/lever-1/apply" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
}

func TestATSExtractorExtractsAshbyJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/posting-api/job-board/Anthropic" {
			t.Fatalf("path = %q, want ashby job board path", r.URL.Path)
		}
		if r.URL.Query().Get("includeCompensation") != "true" {
			t.Fatalf("includeCompensation = %q, want true", r.URL.Query().Get("includeCompensation"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"apiVersion": "1",
			"jobs": [
				{
					"title": "Software Engineer Intern, AI Agents",
					"location": "Singapore",
					"secondaryLocations": [{"location": "Hong Kong"}],
					"department": "Engineering",
					"team": "Agent Runtime",
					"isListed": true,
					"isRemote": false,
					"workplaceType": "Hybrid",
					"descriptionPlain": "Work on agent runtime systems. Internship for 2026 graduates.",
					"publishedAt": "2026-03-01T12:00:00Z",
					"employmentType": "Intern",
					"jobUrl": "https://jobs.ashbyhq.com/Anthropic/agent-intern",
					"applyUrl": "https://jobs.ashbyhq.com/Anthropic/agent-intern/application"
				},
				{
					"title": "Unlisted Staff Engineer",
					"location": "Remote",
					"isListed": false,
					"employmentType": "FullTime",
					"jobUrl": "https://jobs.ashbyhq.com/Anthropic/unlisted",
					"applyUrl": "https://jobs.ashbyhq.com/Anthropic/unlisted/application"
				}
			]
		}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:               server.Client(),
		AshbyJobBoardBaseURL: server.URL + "/posting-api/job-board",
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_ashby",
		Name: "Anthropic",
		URL:  "https://jobs.ashbyhq.com/Anthropic",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "ashby",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want only listed jobs", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "ashby:Anthropic:agent-intern" || job.Company != "Anthropic" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "Singapore" {
		t.Fatalf("job = %#v, want normalized ashby intern", job)
	}
	if job.Location != "Singapore; Hong Kong" || job.ApplyURL != "https://jobs.ashbyhq.com/Anthropic/agent-intern/application" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
}

func TestATSExtractorExtractsJobylonFeedJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/feeds/token-123/" {
			t.Fatalf("path = %q, want jobylon feed path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<jobs>
	<job>
		<id>4069</id>
		<company>
			<slug>acme</slug>
			<name>Acme Robotics</name>
			<website>https://acme.example</website>
		</company>
		<departments>
			<descr>Engineering</descr>
			<id>1473</id>
		</departments>
		<title><![CDATA[Software Engineer Intern, Robot Learning - Summer 2026]]></title>
		<slug>software-engineer-intern-robot-learning</slug>
		<descr><![CDATA[<p>Build robot learning systems for candidates graduating in 2026.</p>]]></descr>
		<skills><![CDATA[<p>Go, Python, distributed systems, ML.</p>]]></skills>
		<function>software development</function>
		<experience>student</experience>
		<employment_type>internship</employment_type>
		<from_date>2026-02-01</from_date>
		<locations>
			<location>
				<city>Singapore</city>
				<country>Singapore</country>
				<country_short>SG</country_short>
				<text>Singapore</text>
			</location>
		</locations>
		<urls>
			<ad>https://jobs.jobylon.com/acme/4069-software-engineer-intern-robot-learning/</ad>
			<apply>https://jobs.jobylon.com/acme/applications/jobs/4069/create/</apply>
		</urls>
	</job>
	<job>
		<id>4070</id>
		<company><name>Acme Robotics</name></company>
		<title>Backend Software Engineer, Infrastructure</title>
		<descr>Build infrastructure for new grads.</descr>
		<employment-type>Full time</employment-type>
		<from-date>2026-03-02</from-date>
		<locations><location><text>New York, NY, United States</text><country-short>US</country-short></location></locations>
		<urls><ad>https://jobs.jobylon.com/acme/4070-backend-software-engineer/</ad></urls>
	</job>
</jobs>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:         server.Client(),
		JobylonMaxJobs: 10,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_jobylon",
		Name: "Acme",
		URL:  server.URL + "/feeds/token-123/",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "jobylon",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.8 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(result.Jobs))
	}
	first := result.Jobs[0]
	if first.SourceJobID != "jobylon:4069" || first.Company != "Acme" || first.Level != "internship" || first.RoleFamily != "ml_ai" || first.Country != "SG" {
		t.Fatalf("first job = %#v, want normalized Jobylon internship", first)
	}
	if first.Location != "Singapore" || first.ApplyURL != "https://jobs.jobylon.com/acme/applications/jobs/4069/create/" {
		t.Fatalf("first location/apply = %q %q", first.Location, first.ApplyURL)
	}
	second := result.Jobs[1]
	if second.ApplyURL != "https://jobs.jobylon.com/acme/4070-backend-software-engineer/" || second.Level != "new_grad" || second.Country != "US" {
		t.Fatalf("second job = %#v, want ad URL fallback and normalized new-grad US role", second)
	}
}

func TestATSExtractorExtractsJobylonJSONFeedJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"id": "json-1",
				"title": "New Grad Software Engineer, Product Platform",
				"descr": "Build product platforms for 2026 graduates.",
				"company": {"name": "Acme"},
				"locations": [{"text": "Toronto, Canada", "country_short": "CA"}],
				"urls": {
					"ad": "https://jobs.jobylon.com/acme/json-1/",
					"apply": "https://jobs.jobylon.com/acme/applications/jobs/json-1/create/"
				}
			}
		]`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_jobylon_json",
		Name: "Acme",
		URL:  server.URL + "/feeds/token-123/",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "jobylon",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "jobylon:json-1" || job.Level != "new_grad" || job.Country != "CA" || job.RoleFamily != "backend" {
		t.Fatalf("job = %#v, want normalized Jobylon JSON feed role", job)
	}
}

func TestATSExtractorExtractsJobylonHostedBoardDetailJobs(t *testing.T) {
	requestedDetails := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`
				<html><body>
					<a href="/acme/4069-software-engineer-intern-robot-learning/">Software Engineer Intern</a>
					<a href="/acme/applications/jobs/4069/create/">Apply</a>
					<a href="/acme/4070-backend-platform-new-grad/">Backend Platform New Grad</a>
				</body></html>`))
		case "/acme/4069-software-engineer-intern-robot-learning/":
			requestedDetails++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<script type="application/ld+json">{
				"@context": "https://schema.org",
				"@type": "JobPosting",
				"title": "Software Engineer Intern, Robot Learning - Summer 2026",
				"description": "Build robot learning systems with Go, Python, distributed systems, and ML.",
				"employmentType": "Internship",
				"hiringOrganization": {"name": "Acme Robotics"},
				"jobLocation": {"@type": "Place", "address": {"addressLocality": "Singapore", "addressCountry": "SG"}}
			}</script>`))
		case "/acme/4070-backend-platform-new-grad/":
			requestedDetails++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<script type="application/ld+json">{
				"@context": "https://schema.org",
				"@type": "JobPosting",
				"title": "Backend Platform New Grad",
				"description": "Build backend systems for 2026 graduates.",
				"employmentType": "Full-time",
				"hiringOrganization": {"name": "Acme Robotics"},
				"jobLocation": {"@type": "Place", "address": {"addressLocality": "New York", "addressRegion": "NY", "addressCountry": "US"}}
			}</script>`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), JobylonMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_jobylon_board",
		Name: "Acme",
		URL:  server.URL + "/acme",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "jobylon",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 || requestedDetails != 1 {
		t.Fatalf("jobs = %d detail requests = %d, want one bounded detail job", len(result.Jobs), requestedDetails)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "jobylon:4069-software-engineer-intern-robot-learning" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "Singapore" {
		t.Fatalf("job = %#v, want normalized Jobylon board detail internship", job)
	}
	if job.ApplyURL != server.URL+"/acme/4069-software-engineer-intern-robot-learning/" {
		t.Fatalf("apply url = %q, want detail URL", job.ApplyURL)
	}
	if evidenceText(job.Evidence, "ats") != "Jobylon hosted JobPosting detail page" {
		t.Fatalf("ats evidence = %q, want Jobylon hosted detail evidence", evidenceText(job.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsPaycomBoardJobs(t *testing.T) {
	var server *httptest.Server
	var searchRequests int
	var detailRequests int
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v4/ats/web.php/"):
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(paycomPortalHTML(server.URL, "paycom-session-token")))
		case r.URL.Path == "/api/ats/job-posting-previews/search":
			searchRequests++
			if got := r.Header.Get("Authorization"); got != "paycom-session-token" {
				t.Fatalf("authorization = %q, want session token", got)
			}
			var payload paycomPreviewSearchPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			if payload.Skip != 0 || payload.Take != 5 {
				t.Fatalf("payload = %#v, want bounded first page", payload)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"jobPostingPreviews": [
					{"jobId": 9001, "jobTitle": "Software Engineering Intern, Platform", "positionType": "Intern", "remoteType": "", "locations": "New York, NY 10001", "description": "Build backend services.", "postedOn": "Jun 20, 2026"},
					{"jobId": 9002, "jobTitle": "New Grad Backend Engineer", "positionType": "Full Time", "remoteType": "Hybrid", "locations": "Singapore", "description": "Build Go APIs for backend platform services.", "postedOn": "2026-06-21"}
				],
				"jobPostingPreviewsCount": 2
			}`))
		case r.URL.Path == "/api/ats/job-postings/9001":
			detailRequests++
			if got := r.Header.Get("Authorization"); got != "paycom-session-token" {
				t.Fatalf("detail authorization = %q, want session token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"jobPosting": {
					"jobId": 9001,
					"clientCode": "ACME",
					"jobTitle": "Software Engineering Intern, Platform",
					"location": "San Francisco, CA 94105",
					"secondaryLocations": ["Remote US"],
					"salaryRange": "$45.00 - $55.00",
					"positionType": "Internship",
					"jobCategory": "Engineering",
					"description": "<p>Build distributed Go services for students graduating in 2026.</p>",
					"qualifications": "<p>Go, PostgreSQL, Redis.</p>"
				}
			}`))
		default:
			t.Fatalf("unexpected request %s", r.URL.String())
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), PaycomMaxJobs: 5, PaycomDetailMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Acme Health",
		URL:  server.URL + "/v4/ats/web.php/jobs?clientkey=ABCDEF1234567890",
		Metadata: map[string]string{
			"source_kind": "paycom",
		},
	})
	if err != nil {
		t.Fatalf("extract paycom: %v", err)
	}
	if searchRequests != 1 || detailRequests != 1 {
		t.Fatalf("requests search=%d detail=%d, want one search and one bounded detail", searchRequests, detailRequests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.8 || len(result.Jobs) != 2 {
		t.Fatalf("result = %#v, want two Paycom ATS jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "paycom:9001" || first.Company != "Acme Health" || first.Level != "internship" || first.Country != "US" || first.RoleFamily != "backend" {
		t.Fatalf("first job = %#v, want normalized Paycom internship", first)
	}
	if first.ApplyURL != server.URL+"/v4/ats/web.php/portal/ABCDEF1234567890/jobs/9001" {
		t.Fatalf("apply url = %q, want Paycom hosted job route", first.ApplyURL)
	}
	if evidenceText(first.Evidence, "compensation") != "$45.00 - $55.00" {
		t.Fatalf("compensation evidence = %q", evidenceText(first.Evidence, "compensation"))
	}
	second := result.Jobs[1]
	if second.SourceJobID != "paycom:9002" || second.Country != "Singapore" || second.Level != "new_grad" || second.RoleFamily != "backend" {
		t.Fatalf("second job = %#v, want preview fallback Singapore new-grad backend", second)
	}
}

func TestATSExtractorExtractsPaycomDetailJob(t *testing.T) {
	var server *httptest.Server
	var detailRequests int
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v4/ats/web.php/portal/ABCDEF1234567890/jobs/9002"):
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(paycomPortalHTML(server.URL, "paycom-detail-token")))
		case r.URL.Path == "/api/ats/job-postings/9002":
			detailRequests++
			if got := r.Header.Get("Authorization"); got != "paycom-detail-token" {
				t.Fatalf("authorization = %q, want detail session token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"jobPosting": {
					"jobId": 9002,
					"jobTitle": "New Grad Backend Engineer",
					"location": "Singapore",
					"positionType": "Full Time",
					"jobCategory": "Engineering",
					"description": "<p>Build Go services for 2026 graduates.</p>"
				}
			}`))
		default:
			t.Fatalf("unexpected request %s", r.URL.String())
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Acme Health",
		URL:  server.URL + "/v4/ats/web.php/portal/ABCDEF1234567890/jobs/9002",
		Metadata: map[string]string{
			"source_kind": "paycom",
		},
	})
	if err != nil {
		t.Fatalf("extract paycom detail: %v", err)
	}
	if detailRequests != 1 || len(result.Jobs) != 1 {
		t.Fatalf("detail requests=%d jobs=%d, want one detail job", detailRequests, len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "paycom:9002" || job.Level != "new_grad" || job.Country != "Singapore" {
		t.Fatalf("job = %#v, want normalized Paycom detail job", job)
	}
	if evidenceText(job.Evidence, "ats") != "Paycom public job posting API" {
		t.Fatalf("ats evidence = %q", evidenceText(job.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsAvatureRSSJobs(t *testing.T) {
	requests := map[string]int{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		switch r.URL.Path {
		case "/careers/SearchJobs/feed/":
			if r.URL.Query().Get("jobRecordsPerPage") != "5" {
				t.Fatalf("jobRecordsPerPage = %q, want 5", r.URL.Query().Get("jobRecordsPerPage"))
			}
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
				<rss version="2.0"><channel><title>careers</title>
					<item>
						<title><![CDATA[Software Engineering Intern - Market Data]]></title>
						<description><![CDATA[ - 10050001]]></description>
						<guid isPermaLink="true">` + server.URL + `/careers/JobDetail/Software-Engineering-Intern-Market-Data/19001</guid>
						<link>` + server.URL + `/careers/JobDetail/Software-Engineering-Intern-Market-Data/19001</link>
						<pubDate>Mon, 22 Jun 2026 00:00:00 +0000</pubDate>
					</item>
					<item>
						<title><![CDATA[Backend Platform Engineer]]></title>
						<description><![CDATA[ - 10050002]]></description>
						<guid isPermaLink="true">` + server.URL + `/careers/JobDetail/Backend-Platform-Engineer/19002</guid>
						<link>` + server.URL + `/careers/JobDetail/Backend-Platform-Engineer/19002</link>
						<pubDate>Tue, 23 Jun 2026 00:00:00 +0000</pubDate>
					</item>
				</channel></rss>`))
		case "/careers/JobDetail/Software-Engineering-Intern-Market-Data/19001":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(avatureDetailHTML("Software Engineering Intern - Market Data", "New York", "Engineering", "Build Go services and market data APIs for interns graduating in 2026.", "/careers/Login?jobId=19001")))
		case "/careers/JobDetail/Backend-Platform-Engineer/19002":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(avatureDetailHTML("Backend Platform Engineer", "Singapore", "Infrastructure", "Own distributed backend systems, PostgreSQL, and observability.", "/careers/Login?jobId=19002")))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), AvatureMaxJobs: 5, AvatureDetailMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Bloomberg",
		URL:  server.URL + "/careers",
		Metadata: map[string]string{
			"source_kind": "avature",
		},
	})
	if err != nil {
		t.Fatalf("extract avature: %v", err)
	}
	if requests["/careers/SearchJobs/feed/"] != 1 || requests["/careers/JobDetail/Software-Engineering-Intern-Market-Data/19001"] != 1 || requests["/careers/JobDetail/Backend-Platform-Engineer/19002"] != 1 {
		t.Fatalf("requests = %#v, want feed and two detail enrichments", requests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.83 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two Avature jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "avature:19001" || first.Level != "internship" || first.RoleFamily != "data" || first.Country != "US" {
		t.Fatalf("first job = %#v, want normalized Avature internship", first)
	}
	if first.ApplyURL != server.URL+"/careers/Login?jobId=19001" || evidenceText(first.Evidence, "ats") != "Avature public SearchJobs RSS feed" {
		t.Fatalf("first apply/evidence = %q/%q, want Avature detail apply URL", first.ApplyURL, evidenceText(first.Evidence, "ats"))
	}
	second := result.Jobs[1]
	if second.SourceJobID != "avature:19002" || second.RoleFamily != "backend" || second.Country != "Singapore" {
		t.Fatalf("second job = %#v, want backend Singapore Avature job", second)
	}
}

func TestATSExtractorExtractsAvatureDetailJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/careers/JobDetail/Software-Engineering-Intern-Market-Data/19001" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(avatureDetailHTML("Software Engineering Intern - Market Data", "New York", "Engineering", "Build Go services and market data APIs for interns graduating in 2026.", "/careers/Login?jobId=19001")))
	}))
	defer server.Close()

	sourceURL := server.URL + "/careers/JobDetail/Software-Engineering-Intern-Market-Data/19001"
	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Bloomberg",
		URL:  sourceURL,
		Metadata: map[string]string{
			"source_kind": "avature",
		},
	})
	if err != nil {
		t.Fatalf("extract avature detail: %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want one Avature detail job", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "avature:19001" || job.ApplyURL != server.URL+"/careers/Login?jobId=19001" || job.Level != "internship" {
		t.Fatalf("job = %#v, want normalized Avature detail", job)
	}
}

func TestATSExtractorExtractsFreshteamJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs" {
			t.Fatalf("path = %q, want Freshteam jobs page", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
	<body>
		<h4>Engineering - Open Role</h4>
		<a class="job-role-list" href="/jobs/42-software-engineer-intern-backend-platform">
			<span class="job-title">Software Engineer Intern, Backend Platform - Summer 2026</span>
			<span class="job-desc">Build Go services for students graduating in 2026.</span>
			<span class="job-location">Singapore</span>
			<span class="job-type">Internship</span>
		</a>
		<a href="https://acme.freshteam.com/jobs/43-new-grad-software-engineer-infra">
			<h5>New Grad Software Engineer, Infrastructure</h5>
			<div>San Francisco, California, United States</div>
			<div>Full Time</div>
			<p>Build distributed systems for 2026 graduates.</p>
		</a>
		<a href="/about">About Acme</a>
	</body>
</html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client: server.Client(),
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_freshteam",
		Name: "Acme",
		URL:  server.URL + "/jobs",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "freshteam",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.75 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(result.Jobs))
	}
	first := result.Jobs[0]
	if first.SourceJobID != "freshteam:42-software-engineer-intern-backend-platform" || first.Company != "Acme" || first.Level != "internship" || first.RoleFamily != "backend" || first.Country != "Singapore" {
		t.Fatalf("first job = %#v, want normalized Freshteam internship", first)
	}
	if first.ApplyURL != server.URL+"/jobs/42-software-engineer-intern-backend-platform" || first.Location != "Singapore" {
		t.Fatalf("first urls/location = %q %q", first.ApplyURL, first.Location)
	}
	second := result.Jobs[1]
	if second.SourceJobID != "freshteam:43-new-grad-software-engineer-infra" || second.Level != "new_grad" || second.Country != "US" || second.RoleFamily != "infrastructure" {
		t.Fatalf("second job = %#v, want normalized Freshteam new-grad role", second)
	}
	if len(first.Evidence) == 0 || len(second.Evidence) == 0 {
		t.Fatal("Freshteam jobs should include card evidence")
	}
}

func TestATSExtractorExtractsHomerunJSONLDJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Fatalf("path = %q, want Homerun careers root", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
	<head>
		<script type="application/ld+json">
		{
			"@context": "https://schema.org",
			"@type": "JobPosting",
			"identifier": {"value": "hr-100"},
			"title": "Software Engineer Intern, Product Platform - Summer 2026",
			"description": "<p>Build TypeScript and Go product systems for students graduating in 2026.</p>",
			"datePosted": "2026-03-15",
			"employmentType": "Internship",
			"hiringOrganization": {"name": "Acme"},
			"jobLocation": {
				"@type": "Place",
				"address": {
					"@type": "PostalAddress",
					"addressLocality": "London",
					"addressCountry": "GB"
				}
			},
			"url": "/software-engineer-intern-product-platform"
		}
		</script>
	</head>
	<body>Open jobs</body>
</html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_homerun",
		Name: "Acme",
		URL:  server.URL + "/",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "homerun",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.78 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "homerun:hr-100" || job.Company != "Acme" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "UK" {
		t.Fatalf("job = %#v, want normalized Homerun JSON-LD internship", job)
	}
	if job.ApplyURL != server.URL+"/software-engineer-intern-product-platform" || job.Strategy != TierATS {
		t.Fatalf("job apply/strategy = %q %q", job.ApplyURL, job.Strategy)
	}
}

func TestATSExtractorExtractsCATSOneJSONLDJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/careers" {
			t.Fatalf("path = %q, want CATS careers page", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
	<head>
		<script type="application/ld+json">
		{
			"@context": "https://schema.org",
			"@type": "JobPosting",
			"identifier": {"value": "cats-2026"},
			"title": "Backend Software Engineer Intern - Winter 2026",
			"description": "<p>Build Go services and distributed systems for a December 2026 graduate.</p>",
			"datePosted": "2026-04-02",
			"employmentType": "Internship",
			"hiringOrganization": {"name": "Acme"},
			"jobLocation": {
				"@type": "Place",
				"address": {
					"@type": "PostalAddress",
					"addressLocality": "Singapore",
					"addressCountry": "SG"
				}
			},
			"url": "/careers/backend-software-engineer-intern"
		}
		</script>
	</head>
	<body>Open jobs</body>
</html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_catsone",
		Name: "Acme",
		URL:  server.URL + "/careers",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "catsone",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.78 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "catsone:cats-2026" || job.Company != "Acme" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "Singapore" {
		t.Fatalf("job = %#v, want normalized CATS internship", job)
	}
	if job.ApplyURL != server.URL+"/careers/backend-software-engineer-intern" || job.Strategy != TierATS {
		t.Fatalf("job apply/strategy = %q %q", job.ApplyURL, job.Strategy)
	}
}

func TestATSExtractorExtractsHiBobHiringJSONLDJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Fatalf("path = %q, want HiBob careers root", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
	<head>
		<script type="application/ld+json">
		{
			"@context": "https://schema.org",
			"@type": "JobPosting",
			"identifier": {"value": "hb-100"},
			"title": "Software Engineer Intern, Data Platform - Summer 2026",
			"description": "<p>Build Go and TypeScript data systems for students graduating in 2026.</p>",
			"datePosted": "2026-04-01",
			"employmentType": "Internship",
			"hiringOrganization": {"name": "Acme Robotics"},
			"jobLocation": {"@type": "Place", "address": {"addressLocality": "Singapore", "addressCountry": "SG"}},
			"url": "/jobs/hb-100-software-engineer-intern-data-platform"
		}
		</script>
	</head>
	<body>Open jobs</body>
</html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), HiBobHiringMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_hibob",
		Name: "Acme",
		URL:  server.URL + "/",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "hibob_hiring",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.8 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "hibob:hb-100" || job.Level != "internship" || job.RoleFamily != "data" || job.Country != "Singapore" {
		t.Fatalf("job = %#v, want normalized HiBob internship", job)
	}
	if evidenceText(job.Evidence, "ats") != "HiBob Hiring hosted careers page JSON-LD or sitemap" {
		t.Fatalf("ats evidence = %q, want HiBob evidence", evidenceText(job.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsFountainJSONLDJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acme" {
			t.Fatalf("path = %q, want Fountain board", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
	<head>
		<script type="application/ld+json">
		{
			"@context": "https://schema.org",
			"@type": "JobPosting",
			"identifier": "fn-2026",
			"title": "Software Engineering Intern, Platform - Summer 2026",
			"description": "<p>Build TypeScript and Go workflow services for early-career engineers.</p>",
			"datePosted": "2026-04-12",
			"employmentType": "Internship",
			"hiringOrganization": {"name": "Fountain Labs"},
			"jobLocation": {"@type": "Place", "address": {"addressLocality": "New York", "addressRegion": "NY", "addressCountry": "US"}},
			"url": "/acme/jobs/fn-2026"
		}
		</script>
	</head>
	<body>Open jobs</body>
</html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), FountainMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_fountain",
		Name: "Fountain Labs",
		URL:  server.URL + "/acme",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "fountain",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.78 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "fountain:fn-2026" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Fountain internship", job)
	}
	if evidenceText(job.Evidence, "ats") != "Fountain hosted careers page JSON-LD or sitemap" {
		t.Fatalf("ats evidence = %q, want Fountain evidence", evidenceText(job.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsZohoRecruitJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/Careers" {
			t.Fatalf("path = %q, want Zoho Recruit careers page", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
	<body>
		<input type="hidden" id="jobs" value="[
			{&#34;id&#34;:&#34;zr-100&#34;,&#34;Job_Opening_Name&#34;:&#34;Software Engineer Intern, AI Platform&#34;,&#34;Posting_Title&#34;:&#34;Software Engineer Intern, AI Platform&#34;,&#34;City&#34;:&#34;Singapore&#34;,&#34;Country&#34;:&#34;Singapore&#34;,&#34;Job_Type&#34;:&#34;Temporary&#34;,&#34;Date_Opened&#34;:&#34;2026-03-21&#34;,&#34;Publish&#34;:true},
			{&#34;id&#34;:&#34;zr-200&#34;,&#34;Job_Opening_Name&#34;:&#34;Senior Account Manager&#34;,&#34;City&#34;:&#34;New York&#34;,&#34;Country&#34;:&#34;United States&#34;,&#34;Publish&#34;:false}
		]">
	</body>
</html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_zoho",
		Name: "Acme",
		URL:  server.URL + "/jobs/Careers",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "zoho_recruit",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.74 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "zoho_recruit:zr-100" || job.Company != "Acme" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "Singapore" {
		t.Fatalf("job = %#v, want normalized Zoho Recruit internship", job)
	}
	if job.ApplyURL != server.URL+"/jobs/Careers/zr-100" || job.Strategy != TierATS {
		t.Fatalf("job apply/strategy = %q %q", job.ApplyURL, job.Strategy)
	}
	if job.PostedAt == nil || job.PostedAt.Format("2006-01-02") != "2026-03-21" {
		t.Fatalf("posted_at = %v, want parsed Date_Opened", job.PostedAt)
	}
}

func TestATSExtractorExtractsManatalJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
<html>
	<body>
		<article class="job-card">
			<a href="/jobs/fc168286-7def-4445-8b28-b4bf6c96b625"
				class="job-title-link"
				data-job-id="fc168286-7def-4445-8b28-b4bf6c96b625"
				data-job-title="AI Content Marketing Internship"
				data-job-city="Bangkok"
				data-job-country="Thailand">
				<h6 class="job-title">AI Content Marketing Internship</h6>
			</a>
			<a href="jobs/fc168286-7def-4445-8b28-b4bf6c96b625/apply">Apply now</a>
		</article>
</body>
</html>`))
		case "/jobs/fc168286-7def-4445-8b28-b4bf6c96b625":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
<html>
	<head>
		<script type="application/ld+json">
		{
			"@context": "https://schema.org",
			"@type": "JobPosting",
			"identifier": {"value": "fc168286-7def-4445-8b28-b4bf6c96b625"},
			"title": "AI Content Marketing Internship",
			"description": "<p>Work with LLM optimization, AI search, and marketing analytics.</p>",
			"datePosted": "2026-03-06T09:53:13Z",
			"employmentType": "FULL_TIME",
			"hiringOrganization": {"name": "Manatal Co LTD"},
			"jobLocation": {
				"@type": "Place",
				"address": {
					"@type": "PostalAddress",
					"addressLocality": "Bangkok",
					"addressRegion": "Bangkok",
					"addressCountry": "Thailand"
				}
			},
			"url": "/jobs/fc168286-7def-4445-8b28-b4bf6c96b625"
		}
		</script>
	</head>
</html>`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_manatal",
		Name: "Acme",
		URL:  server.URL + "/",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "manatal",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.76 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "manatal:fc168286-7def-4445-8b28-b4bf6c96b625" || job.Company != "Manatal Co LTD" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "Thailand" {
		t.Fatalf("job = %#v, want normalized Manatal internship", job)
	}
	if job.ApplyURL != server.URL+"/jobs/fc168286-7def-4445-8b28-b4bf6c96b625" || job.Strategy != TierATS {
		t.Fatalf("job apply/strategy = %q %q", job.ApplyURL, job.Strategy)
	}
}

func TestATSExtractorExtractsApplicantProJobs(t *testing.T) {
	detailRequests := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><script>window.courierCurrentRouteData = {"domain_id":"11099"};</script></html>`))
		case "/core/jobs/11099":
			if r.URL.Query().Get("getParams") == "" {
				t.Fatalf("getParams query is required")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"success": true,
				"data": {
					"jobCount": 2,
					"jobs": [
						{
							"id": 9001,
							"title": "Software Engineer Intern, Backend Platform - Summer 2026",
							"jobLocation": "Toronto, Ontario, Canada",
							"city": "Toronto",
							"stateName": "Ontario",
							"iso3": "CAN",
							"orgTitle": "Engineering",
							"employmentType": "Internship",
							"classification": "Student",
							"jobUrl": "` + serverURL + `/jobs/9001",
							"startDateRef": "Mar 20, 2026"
						},
						{
							"id": 9002,
							"title": "Customer Success Manager",
							"jobLocation": "Remote US",
							"employmentType": "Full Time",
							"jobUrl": "` + serverURL + `/jobs/9002"
						}
					]
				}
			}`))
		case "/core/jobs/11099/9001/job-details":
			detailRequests++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"success": true,
				"data": {
					"id": 9001,
					"title": "Software Engineer Intern, Backend Platform - Summer 2026",
					"advertisingDescriptionHtml": "<p>Build Go APIs and distributed systems for students graduating in 2026.</p>",
					"benefits": "Mentorship and shipping production services."
				}
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), ApplicantProMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_applicantpro",
		Name: "Acme",
		URL:  server.URL + "/jobs/",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "applicantpro",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if detailRequests != 1 {
		t.Fatalf("detailRequests = %d, want 1", detailRequests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want max-capped 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "applicantpro:9001" || job.Company != "Acme" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "Canada" {
		t.Fatalf("job = %#v, want normalized ApplicantPro internship", job)
	}
	if job.Location != "Toronto, Ontario, Canada" || !strings.Contains(job.ApplyURL, "/jobs/9001") {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if len(job.Evidence) == 0 {
		t.Fatal("ApplicantPro jobs should include API/detail evidence")
	}
}

func TestATSExtractorExtractsJobsoidJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/jobs" {
			t.Fatalf("path = %q, want jobsoid jobs API path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"id": 9001,
				"jobId": "backend-intern-2026",
				"jobTitle": "Backend Software Engineer Intern, Data Platform - Summer 2026",
				"jobDescription": "<p>Build Go data services for candidates graduating in 2026.</p>",
				"department": "Engineering",
				"location": "Toronto, Canada",
				"country": "Canada",
				"employmentType": "Internship",
				"datePosted": "2026-02-18T10:00:00Z",
				"jobUrl": "https://acme.jobsoid.com/j/9001/backend-software-engineer-intern"
			},
			{
				"id": 9002,
				"title": "Customer Success Manager",
				"description": "Help customers onboard.",
				"apply_url": "https://acme.jobsoid.com/j/9002/customer-success-manager"
			}
		]`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:         server.Client(),
		JobsoidMaxJobs: 10,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_jobsoid",
		Name: "Acme",
		URL:  server.URL + "/jobs",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "jobsoid",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.8 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "jobsoid:backend-intern-2026" || job.Company != "Acme" || job.Level != "internship" || job.RoleFamily != "data" || job.Country != "Canada" {
		t.Fatalf("job = %#v, want normalized Jobsoid internship", job)
	}
	if job.Location != "Toronto, Canada" || job.ApplyURL != "https://acme.jobsoid.com/j/9001/backend-software-engineer-intern" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
}

func TestATSExtractorExtractsTalentLyftJobs(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v2/public/acme/jobs" {
			t.Fatalf("path = %q, want TalentLyft public jobs path", r.URL.Path)
		}
		if r.URL.Query().Get("details") != "true" {
			t.Fatalf("details = %q, want true", r.URL.Query().Get("details"))
		}
		if r.URL.Query().Get("page") != strconv.Itoa(requests) {
			t.Fatalf("page = %q, want %d", r.URL.Query().Get("page"), requests)
		}
		w.Header().Set("Content-Type", "application/json")
		switch requests {
		case 1:
			_, _ = w.Write([]byte(`{
				"Results": [
					{
						"id": 101,
						"title": "Software Engineer Intern, AI Platform - Summer 2026",
						"description": "<p>Build agent infrastructure for 2026 graduates.</p>",
						"department": {"name": "Engineering"},
						"location": {"name": "Singapore", "country": "Singapore"},
						"employmentType": "Internship",
						"publishedAt": "2026-04-02T09:00:00Z",
						"url": "https://acme.talentlyft.com/jobs/software-engineer-intern-ai-platform-101"
					}
				],
				"Page": 1,
				"PerPage": 1,
				"Count": 2,
				"Pages": {"Next": "2"}
			}`))
		default:
			_, _ = w.Write([]byte(`{
				"Results": [
					{
						"id": "backend-new-grad",
						"name": "Backend Software Engineer, New Grad",
						"jobDescription": "Build backend services.",
						"departmentName": "Platform",
						"locationName": "New York, NY, United States",
						"type": "Full time",
						"applyUrl": "https://acme.talentlyft.com/jobs/backend-new-grad"
					}
				],
				"Page": 2,
				"PerPage": 1,
				"Count": 2,
				"Pages": {}
			}`))
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:                server.Client(),
		TalentLyftBaseURL:     server.URL,
		TalentLyftMaxPages:    3,
		TalentLyftMaxJobs:     10,
		TalentLyftPageSize:    1,
		TalentLyftDetailPages: true,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_talentlyft",
		Name: "Acme",
		URL:  "https://acme.talentlyft.com",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "talentlyft",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2 paginated calls", requests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.8 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(result.Jobs))
	}
	first := result.Jobs[0]
	if first.SourceJobID != "talentlyft:acme:101" || first.Level != "internship" || first.RoleFamily != "ml_ai" || first.Country != "Singapore" {
		t.Fatalf("first job = %#v, want normalized TalentLyft internship", first)
	}
	if first.ApplyURL != "https://acme.talentlyft.com/jobs/software-engineer-intern-ai-platform-101" || first.Location != "Singapore" {
		t.Fatalf("first job location/apply = %q %q", first.Location, first.ApplyURL)
	}
	second := result.Jobs[1]
	if second.SourceJobID != "talentlyft:acme:backend-new-grad" || second.Level != "new_grad" || second.Country != "US" {
		t.Fatalf("second job = %#v, want normalized TalentLyft new-grad role", second)
	}
}

func TestATSExtractorExtractsWorkableJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/acme" {
			t.Fatalf("path = %q, want workable account path", r.URL.Path)
		}
		if r.URL.Query().Get("details") != "true" {
			t.Fatalf("details query = %q, want true", r.URL.Query().Get("details"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name": "Acme",
			"jobs": [
				{
					"id": "job-1",
					"title": "Software Engineering Intern, AI Platform - Summer 2026",
					"full_title": "Software Engineering Intern, AI Platform - Summer 2026",
					"shortcode": "AI26",
					"state": "published",
					"department": "Engineering",
					"url": "https://acme.workable.com/jobs/job-1",
					"application_url": "https://acme.workable.com/jobs/job-1/candidates/new",
					"shortlink": "https://apply.workable.com/j/AI26",
					"location": {
						"location_str": "Singapore",
						"country": "Singapore",
						"country_code": "SG",
						"city": "Singapore",
						"workplace_type": "hybrid"
					},
					"description": "<p>Build AI platform systems for 2026 internship candidates.</p>",
					"created_at": "2026-04-02T08:00:00Z",
					"employment_type": "Intern"
				},
				{
					"id": "draft-1",
					"title": "Draft Staff Engineer",
					"shortcode": "DRAFT",
					"state": "draft",
					"url": "https://acme.workable.com/jobs/draft-1"
				}
			]
		}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:                server.Client(),
		WorkablePublicBaseURL: server.URL + "/api/accounts",
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_workable",
		Name: "Acme",
		URL:  "https://apply.workable.com/acme/",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "workable",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want only published jobs", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "workable:acme:AI26" || job.Company != "Acme" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "Singapore" {
		t.Fatalf("job = %#v, want normalized workable internship", job)
	}
	if job.Location != "Singapore" || job.ApplyURL != "https://acme.workable.com/jobs/job-1/candidates/new" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if len(job.Evidence) == 0 {
		t.Fatal("job evidence should include workable content")
	}
}

func TestATSExtractorExtractsWorkableJobsNetworkSearch(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RawQuery)
		if r.URL.Path != "/api/v1/jobs" {
			t.Fatalf("path = %q, want workable jobs search path", r.URL.Path)
		}
		if r.URL.Query().Get("query") != "software engineer intern" {
			t.Fatalf("query = %q, want source search query", r.URL.Query().Get("query"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("pageToken") {
		case "":
			_, _ = w.Write([]byte(`{
				"title":"Workable",
				"totalSize":2,
				"nextPageToken":"next-page",
				"jobs":[
					{
						"id":"job-1",
						"title":"Software Engineer Intern",
						"state":"published",
						"department":"Engineering",
						"description":"<p>Build Go and React systems for 2026 interns.</p>",
						"url":"https://jobs.workable.com/view/job-1/software-engineer-intern-at-acme",
						"employmentType":"Internship",
						"locations":["TELECOMMUTE","Porto, Portugal"],
						"location":{"city":"Porto","countryName":"Portugal"},
						"created":"2026-06-20T12:00:00Z",
						"company":{"id":"company-1","title":"Acme Robotics","website":"https://acme.test","url":"https://jobs.workable.com/company/acme"}
					},
					{"id":"draft","title":"Draft Engineer","state":"draft","company":{"title":"Acme Robotics"}}
				]
			}`))
		case "next-page":
			_, _ = w.Write([]byte(`{
				"title":"Workable",
				"totalSize":2,
				"jobs":[
					{
						"id":"job-2",
						"title":"New Grad Backend Software Engineer",
						"state":"published",
						"department":"Platform",
						"description":"<p>Build distributed systems and APIs.</p>",
						"url":"https://jobs.workable.com/view/job-2/new-grad-backend-software-engineer-at-deepinfra",
						"employmentType":"Full-time",
						"locations":["New York, New York, United States"],
						"location":{"city":"New York","subregion":"New York","countryName":"United States"},
						"created":"2026-06-21T09:30:00Z",
						"company":{"id":"company-2","title":"Deep Infra","website":"https://deepinfra.test","url":"https://jobs.workable.com/company/deepinfra"}
					}
				]
			}`))
		default:
			t.Fatalf("unexpected page token %q", r.URL.Query().Get("pageToken"))
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:               server.Client(),
		WorkableJobsBaseURL:  server.URL + "/api/v1/jobs",
		WorkableJobsMaxPages: 2,
		WorkableJobsMaxJobs:  5,
	})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Workable network",
		URL:  "https://jobs.workable.com/search?query=software%20engineer%20intern",
		Metadata: map[string]string{
			"source_kind": "workable_jobs",
		},
	})
	if err != nil {
		t.Fatalf("extract workable jobs: %v", err)
	}
	if len(requests) != 2 || !strings.Contains(requests[1], "pageToken=next-page") {
		t.Fatalf("requests = %#v, want first page plus page token", requests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.85 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two Workable Jobs postings", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "workable_jobs:company-1:job-1" || first.Company != "Acme Robotics" || first.Level != "internship" || first.RoleFamily != "software_engineering" || first.Country != "Portugal" {
		t.Fatalf("first job = %#v, want normalized remote internship", first)
	}
	if first.Location != "Porto, Portugal; Remote" || first.ApplyURL != "https://jobs.workable.com/view/job-1/software-engineer-intern-at-acme" || evidenceText(first.Evidence, "ats") != "Workable Jobs public search API" {
		t.Fatalf("first location/apply/evidence = %q/%q/%q", first.Location, first.ApplyURL, evidenceText(first.Evidence, "ats"))
	}
	second := result.Jobs[1]
	if second.SourceJobID != "workable_jobs:company-2:job-2" || second.Level != "new_grad" || second.RoleFamily != "backend" || second.Country != "US" {
		t.Fatalf("second job = %#v, want normalized new-grad backend job", second)
	}
}

func TestATSExtractorExtractsRecruiteeJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/offers/" {
			t.Fatalf("path = %q, want recruitee offers path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"offers": [
				{
					"id": 456,
					"slug": "backend-intern",
					"title": "Software Engineering Intern, Backend Platform - Summer 2026",
					"kind": "job",
					"status": "published",
					"department": "Engineering",
					"careers_url": "https://acme.recruitee.com/o/backend-intern",
					"description": "<p>Build backend platform services.</p>",
					"requirements": "<p>Internship candidates graduating in 2026.</p>",
					"locations": [
						{"name": "New York", "city": "New York", "state": "NY", "country_code": "US"}
					],
					"published_at": "2026-04-03T12:00:00Z",
					"employment_type": "internship"
				},
				{
					"id": 999,
					"slug": "talent-community",
					"title": "Talent Community",
					"kind": "talent_pool",
					"status": "published",
					"careers_url": "https://acme.recruitee.com/o/talent-community"
				}
			]
		}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:           server.Client(),
		RecruiteeBaseURL: server.URL + "/api/offers/",
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_recruitee",
		Name: "Acme",
		URL:  "https://acme.recruitee.com",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "recruitee",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want only job offers", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "recruitee:acme:backend-intern" || job.Company != "Acme" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized recruitee internship", job)
	}
	if job.Location != "New York, NY, US" || job.ApplyURL != "https://acme.recruitee.com/o/backend-intern" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
}

func TestATSExtractorExtractsSmartRecruitersJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/companies/acme/postings":
			if r.URL.Query().Get("limit") != "100" {
				t.Fatalf("limit = %q, want 100", r.URL.Query().Get("limit"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"limit": 100,
				"offset": 0,
				"totalFound": 1,
				"content": [
					{
						"id": "sr-1",
						"uuid": "uuid-1",
						"name": "Software Engineer Intern, Platform",
						"releasedDate": "2026-06-20T10:00:00.000Z",
						"company": {"identifier": "acme", "name": "Acme AI"},
						"department": {"label": "Engineering"},
						"function": {"label": "Engineering"},
						"typeOfEmployment": {"label": "Internship"},
						"experienceLevel": {"label": "Internship"},
						"location": {"city": "Singapore", "country": "sg", "remote": false}
					}
				]
			}`))
		case "/v1/companies/acme/postings/sr-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "sr-1",
				"uuid": "uuid-1",
				"name": "Software Engineer Intern, Platform",
				"releasedDate": "2026-06-20T10:00:00.000Z",
				"applyUrl": "https://jobs.smartrecruiters.com/acme/sr-1-platform-intern",
				"company": {"identifier": "acme", "name": "Acme AI"},
				"department": {"label": "Engineering"},
				"function": {"label": "Engineering"},
				"typeOfEmployment": {"label": "Internship"},
				"experienceLevel": {"label": "Internship"},
				"location": {"city": "Singapore", "country": "sg", "remote": false},
				"jobAd": {
					"sections": {
						"jobDescription": {"title": "Job Description", "text": "<p>Build backend services for AI infrastructure.</p>"},
						"qualifications": {"title": "Qualifications", "text": "<p>Graduating in 2026 and comfortable with Go.</p>"}
					}
				}
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:                 server.Client(),
		SmartRecruitersBaseURL: server.URL + "/v1/companies",
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_smartrecruiters",
		Name: "Acme AI",
		URL:  "https://jobs.smartrecruiters.com/acme",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "smartrecruiters",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.9 {
		t.Fatalf("result = %+v, want high-confidence ATS result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "smartrecruiters:acme:sr-1" || job.Company != "Acme AI" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "Singapore" {
		t.Fatalf("job = %#v, want normalized SmartRecruiters internship", job)
	}
	if job.Location != "Singapore" || job.ApplyURL != "https://jobs.smartrecruiters.com/acme/sr-1-platform-intern" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "Build backend services") {
		t.Fatalf("evidence = %#v, want SmartRecruiters description evidence", job.Evidence)
	}
}

func TestATSExtractorCapsSmartRecruitersJobsAndDetailFetches(t *testing.T) {
	var detailRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/companies/acme/postings":
			if r.URL.Query().Get("limit") != "2" {
				t.Fatalf("limit = %q, want 2", r.URL.Query().Get("limit"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"limit": 2,
				"offset": 0,
				"totalFound": 3,
				"content": [
					{"id": "sr-1", "name": "Software Engineer Intern", "applyUrl": "https://jobs.smartrecruiters.com/acme/sr-1", "company": {"identifier": "acme", "name": "Acme AI"}, "location": {"city": "Singapore", "country": "sg"}},
					{"id": "sr-2", "name": "Backend Software Engineer", "applyUrl": "https://jobs.smartrecruiters.com/acme/sr-2", "company": {"identifier": "acme", "name": "Acme AI"}, "location": {"city": "New York", "country": "us"}},
					{"id": "sr-3", "name": "Data Engineer", "applyUrl": "https://jobs.smartrecruiters.com/acme/sr-3", "company": {"identifier": "acme", "name": "Acme AI"}, "location": {"city": "London", "country": "gb"}}
				]
			}`))
		case "/v1/companies/acme/postings/sr-1":
			detailRequests++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "sr-1",
				"name": "Software Engineer Intern",
				"applyUrl": "https://jobs.smartrecruiters.com/acme/sr-1-detail",
				"company": {"identifier": "acme", "name": "Acme AI"},
				"location": {"city": "Singapore", "country": "sg"},
				"jobAd": {"sections": {"jobDescription": {"text": "<p>Build platform services.</p>"}}}
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:                       server.Client(),
		SmartRecruitersBaseURL:       server.URL + "/v1/companies",
		SmartRecruitersMaxJobs:       2,
		SmartRecruitersDetailMaxJobs: 1,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:       "source_smartrecruiters",
		Name:     "Acme AI",
		URL:      "https://jobs.smartrecruiters.com/acme",
		Tier:     TierATS,
		Metadata: map[string]string{"source_kind": "smartrecruiters"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want capped 2", len(result.Jobs))
	}
	if detailRequests != 1 {
		t.Fatalf("detailRequests = %d, want 1", detailRequests)
	}
	if result.Jobs[0].ApplyURL != "https://jobs.smartrecruiters.com/acme/sr-1-detail" {
		t.Fatalf("first apply URL = %q, want detail-enriched URL", result.Jobs[0].ApplyURL)
	}
	if result.Jobs[1].ApplyURL != "https://jobs.smartrecruiters.com/acme/sr-2" {
		t.Fatalf("second apply URL = %q, want summary URL without detail fetch", result.Jobs[1].ApplyURL)
	}
}

func TestATSExtractorExtractsJaneStreetJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/jobs/main.json":
			_, _ = w.Write([]byte(`[
				{
					"id": 9101,
					"position": "Software Engineer Internship",
					"category": "Technology",
					"availability": "Internship",
					"city": "SGP",
					"overview": "<p>Build OCaml services, developer tools, and trading infrastructure.</p>",
					"team": "Software Engineering",
					"duration": "May-August"
				},
				{
					"id": 9102,
					"position": "AML Onboarding Analyst",
					"category": "Legal and Compliance",
					"availability": "Full-Time: Experienced",
					"city": "NYC",
					"overview": "<p>Support compliance workflows.</p>",
					"team": "Legal and Compliance",
					"duration": "Permanent"
				},
				{
					"id": 9999,
					"position": "Hidden Draft Role",
					"category": "Technology",
					"availability": "Internship",
					"city": "NYC"
				}
			]`))
		case "/static/position-directories.json":
			_, _ = w.Write([]byte(`["9101", "9102"]`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_janestreet",
		Name: "Jane Street",
		URL:  server.URL + "/join-jane-street/open-roles/?search=software%20engineer%20intern",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "janestreet_careers",
		},
	})
	if err != nil {
		t.Fatalf("extract Jane Street careers: %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.87 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two Jane Street jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "janestreet_careers:9101" || first.Company != "Jane Street" || first.Location != "Singapore" || first.Country != "Singapore" {
		t.Fatalf("first job = %#v, want normalized Singapore internship", first)
	}
	if first.ApplyURL != server.URL+"/join-jane-street/position/9101" || first.Level != "internship" || first.RoleFamily != "infrastructure" {
		t.Fatalf("first job apply/fit = %#v, want Jane Street apply URL and software fit", first)
	}
	if !strings.Contains(evidenceText(first.Evidence, "description"), "trading infrastructure") {
		t.Fatalf("description evidence = %q, want overview text", evidenceText(first.Evidence, "description"))
	}
}

func TestJaneStreetAndAkunaTimingIgnoresDescriptiveInternMentions(t *testing.T) {
	source := Source{
		Name: "Test Company",
		URL:  "https://example.com/careers",
		Tier: TierATS,
	}

	janeStreet, ok := janeStreetPosting(source, "https://example.com", janeStreetJob{
		ID:           1,
		Position:     "Software Engineer, Internal Systems",
		Category:     "Internship Programs",
		Availability: "Full-Time: Experienced",
		Overview:     "Experienced engineers mentor interns and build internal systems.",
		Team:         "Cooperative Infrastructure",
		Duration:     "Permanent",
	})
	if !ok {
		t.Fatal("janeStreetPosting rejected valid fixture")
	}
	if janeStreet.Level != "unknown" || janeStreet.EmploymentType == "internship" {
		t.Fatalf("Jane Street timing = level %q employment %q, want non-internship", janeStreet.Level, janeStreet.EmploymentType)
	}

	akuna, ok := akunaPosting(source, "https://example.com/jobs.json", akunaJob{
		ID:          2,
		Title:       "Software Engineer, Internal Systems",
		Departments: []string{"Internship Programs"},
		Experience:  "Experienced",
		Specialties: []string{"Cooperative Infrastructure"},
		Content:     "Experienced engineers mentor interns and build internal systems.",
	})
	if !ok {
		t.Fatal("akunaPosting rejected valid fixture")
	}
	if akuna.Level != "unknown" || akuna.EmploymentType == "internship" {
		t.Fatalf("Akuna timing = level %q employment %q, want non-internship", akuna.Level, akuna.EmploymentType)
	}
}

func TestATSExtractorExtractsAkunaCareersJSONFeed(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wp-content/uploads/akuna_jobs.json" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"id": 8001,
				"title": "Software Engineer Intern - Trading Systems",
				"location": "Singapore",
				"locationRaw": "Singapore",
				"departments": ["Development"],
				"experience": "Intern",
				"specialties": ["Low Latency", "Trading"],
				"absolute_url": "` + server.URL + `/careers/job/8001/?gh_jid=8001",
				"updated_at": "2026-06-22T12:57:24-04:00",
				"content": "&lt;p&gt;Build low latency Go and C++ trading systems for options markets.&lt;/p&gt;"
			},
			{
				"id": 8002,
				"title": "Akuna Capital's Talent Community",
				"location": "Virtual",
				"absolute_url": "` + server.URL + `/careers/job/8002/?gh_jid=8002",
				"content": "&lt;p&gt;Join our talent community.&lt;/p&gt;"
			}
		]`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_akuna",
		Name: "Akuna Capital",
		URL:  server.URL + "/careers/?search=software%20engineer%20intern",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "akuna_careers",
		},
	})
	if err != nil {
		t.Fatalf("extract Akuna careers: %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.86 || len(result.Jobs) != 1 {
		t.Fatalf("result = %+v, want one non-community Akuna job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "akuna_careers:8001" || job.Company != "Akuna Capital" || job.Country != "Singapore" || job.Level != "internship" {
		t.Fatalf("job = %#v, want normalized Akuna internship", job)
	}
	if job.ApplyURL != server.URL+"/careers/job/8001/?gh_jid=8001" || job.RoleFamily != "infrastructure" {
		t.Fatalf("job apply/role = %#v, want Akuna apply URL and engineering role", job)
	}
	if !strings.Contains(evidenceText(job.Evidence, "description"), "trading systems") {
		t.Fatalf("description evidence = %q, want Akuna feed content", evidenceText(job.Evidence, "description"))
	}
}

func TestATSExtractorExtractsComeetJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/careers-api/2.0/company/61.005/positions" {
			t.Fatalf("path = %q, want Comeet positions path", r.URL.Path)
		}
		if r.URL.Query().Get("token") != "tok_123" {
			t.Fatalf("token = %q, want tok_123", r.URL.Query().Get("token"))
		}
		if r.URL.Query().Get("details") != "true" {
			t.Fatalf("details = %q, want true", r.URL.Query().Get("details"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"name": "Backend Software Engineer Intern, AI Infrastructure",
				"department": "Engineering",
				"uid": "8A.26E-E5.60A",
				"company_name": "MWDN",
				"employment_type": "Internship",
				"experience_level": "Student",
				"url_comeet_hosted_page": "https://www.comeet.com/jobs/mwdn/61.005/backend-software-engineer-intern/8A.26E-E5.60A",
				"url_active_page": "https://jobs.mwdn.com/careers/co/remote/8A.26E/backend-software-engineer-intern/all/",
				"position_url": "https://www.comeet.co/careers-api/2.0/company/61.005/positions/8A.26E-E5.60A?token=tok_123",
				"time_updated": "2026-06-21T15:43:11Z",
				"workplace_type": "Remote",
				"location": {
					"name": "Remote US",
					"country": "US",
					"city": "New York",
					"state": "NY",
					"is_remote": true
				},
				"details": [
					{"name": "Description", "value": "<p>Build backend services and LLM evaluation tooling.</p>", "order": 1},
					{"name": "Requirements", "value": "<p>Internship candidates graduating in 2026.</p>", "order": 2}
				]
			}
		]`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:        server.Client(),
		ComeetBaseURL: server.URL + "/careers-api/2.0",
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_comeet",
		Name: "MWDN",
		URL:  server.URL + "/careers-api/2.0/company/61.005/positions?token=tok_123",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "comeet",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.9 {
		t.Fatalf("result = %+v, want high-confidence Comeet ATS result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "comeet:61.005:8A.26E-E5.60A" || job.Company != "MWDN" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Comeet internship", job)
	}
	if job.Location != "Remote US" || job.ApplyURL != "https://jobs.mwdn.com/careers/co/remote/8A.26E/backend-software-engineer-intern/all/" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "Build backend services") {
		t.Fatalf("evidence = %#v, want Comeet description evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsComeetHostedJSONLDJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/mwdn/61.005/backend-software-engineer-intern/8A.26E-E5.60A" {
			t.Fatalf("path = %q, want Comeet hosted page", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
	<head>
		<script type="application/ld+json">
		{
			"@context": "https://schema.org",
			"@type": "JobPosting",
			"identifier": {"value": "8A.26E-E5.60A"},
			"title": "Backend Software Engineer Intern, AI Infrastructure",
			"description": "<p>Build backend services and LLM evaluation tooling for internship candidates graduating in 2026.</p>",
			"datePosted": "2026-06-21",
			"employmentType": "Internship",
			"hiringOrganization": {"name": "MWDN"},
			"jobLocation": {"@type": "Place", "address": {"addressLocality": "New York", "addressRegion": "NY", "addressCountry": "US"}},
			"url": "/jobs/mwdn/61.005/backend-software-engineer-intern/8A.26E-E5.60A"
		}
		</script>
	</head>
	<body>Open jobs</body>
</html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), ComeetHostedMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_comeet_hosted",
		Name: "MWDN",
		URL:  server.URL + "/jobs/mwdn/61.005/backend-software-engineer-intern/8A.26E-E5.60A",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "comeet",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.8 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "comeet:8A.26E-E5.60A" || job.Company != "MWDN" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized hosted Comeet internship", job)
	}
	if evidenceText(job.Evidence, "ats") != "Comeet hosted page JSON-LD or sitemap" {
		t.Fatalf("ats evidence = %q, want hosted Comeet evidence", evidenceText(job.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsWorkdayJobs(t *testing.T) {
	requestedDetails := map[string]bool{}
	requestedSearchTexts := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wday/cxs/nvidia/NVIDIAExternalCareerSite/jobs":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			var searchReq workdaySearchRequest
			if err := json.Unmarshal([]byte(readRequestBody(t, r)), &searchReq); err != nil {
				t.Fatalf("decode Workday search request: %v", err)
			}
			requestedSearchTexts = append(requestedSearchTexts, searchReq.SearchText)
			if searchReq.SearchText != "software engineer intern" {
				t.Fatalf("searchText = %q, want source query", searchReq.SearchText)
			}
			if searchReq.Offset == 0 {
				_, _ = w.Write([]byte(`{
					"total": 2,
					"jobPostings": [
						{
							"title": "Software Engineering Intern, JAX - Fall 2026",
							"externalPath": "/job/US-CA-Santa-Clara/Software-Engineering-Intern--JAX---Fall-2026_JR2009745",
							"locationsText": "US, CA, Santa Clara",
							"postedOn": "Posted 30+ Days Ago",
							"bulletFields": ["JR2009745"],
							"timeType": "Full time"
						}
					]
				}`))
				return
			}
			_, _ = w.Write([]byte(`{
				"total": 2,
				"jobPostings": [
					{
						"title": "New College Graduate Software Engineer, CUDA",
						"externalPath": "/job/US-TX-Austin/New-College-Graduate-Software-Engineer--CUDA_JR2000002",
						"locationsText": "US, TX, Austin",
						"postedOn": "Posted Yesterday",
						"bulletFields": ["JR2000002"],
						"timeType": "Full time"
					}
				]
			}`))
		case "/wday/cxs/nvidia/NVIDIAExternalCareerSite/job/US-CA-Santa-Clara/Software-Engineering-Intern--JAX---Fall-2026_JR2009745":
			requestedDetails[r.URL.Path] = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"jobPostingInfo": {
					"title": "Software Engineering Intern, JAX - Fall 2026",
					"jobDescription": "<p>Build JAX compiler tooling for AI researchers.</p>",
					"jobReqId": "JR2009745",
					"externalUrl": "https://nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite/job/US-CA-Santa-Clara/Software-Engineering-Intern--JAX---Fall-2026_JR2009745",
					"jobRequisitionLocation": {
						"descriptor": "US, CA, Santa Clara",
						"country": {"descriptor": "United States of America", "alpha2Code": "US"}
					},
					"country": {"descriptor": "United States of America"},
					"timeType": "Full time",
					"posted": true,
					"startDate": "2026-06-21",
					"canApply": true
				}
			}`))
		case "/wday/cxs/nvidia/NVIDIAExternalCareerSite/job/US-TX-Austin/New-College-Graduate-Software-Engineer--CUDA_JR2000002":
			requestedDetails[r.URL.Path] = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"jobPostingInfo": {
					"title": "New College Graduate Software Engineer, CUDA",
					"jobDescription": "<p>Build CUDA developer tooling for new grads.</p>",
					"jobReqId": "JR2000002",
					"locationsText": "US, TX, Austin",
					"country": "US",
					"timeType": "Full time",
					"canApply": true
				}
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:          server.Client(),
		WorkdayPageSize: 1,
		WorkdayMaxPages: 2,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_workday",
		Name: "NVIDIA",
		URL:  server.URL + "/recruiting/nvidia/NVIDIAExternalCareerSite?q=software%20engineer%20intern",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "workday",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.85 {
		t.Fatalf("result = %+v, want high-confidence Workday result", result)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "workday:nvidia:NVIDIAExternalCareerSite:JR2009745" || job.Company != "NVIDIA" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Workday internship", job)
	}
	if job.Location != "US, CA, Santa Clara" || job.ApplyURL != "https://nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite/job/US-CA-Santa-Clara/Software-Engineering-Intern--JAX---Fall-2026_JR2009745" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "Build JAX compiler") {
		t.Fatalf("evidence = %#v, want Workday detail evidence", job.Evidence)
	}
	if !requestedDetails["/wday/cxs/nvidia/NVIDIAExternalCareerSite/job/US-CA-Santa-Clara/Software-Engineering-Intern--JAX---Fall-2026_JR2009745"] {
		t.Fatal("expected Workday detail endpoint to be fetched")
	}
	if len(requestedSearchTexts) != 2 {
		t.Fatalf("search requests = %d, want 2", len(requestedSearchTexts))
	}
}

func TestATSExtractorExtractsBreezyJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json" {
			t.Fatalf("path = %q, want Breezy JSON board path", r.URL.Path)
		}
		if r.URL.Query().Get("verbose") != "true" {
			t.Fatalf("verbose = %q, want true", r.URL.Query().Get("verbose"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{
				"id": "b8e6b722f7ed",
				"friendly_id": "b8e6b722f7ed-software-engineering-intern-ai-platform",
				"name": "Software Engineering Intern, AI Platform - Summer 2026",
				"url": "https://acme.breezy.hr/p/b8e6b722f7ed-software-engineering-intern-ai-platform",
				"published_date": "2026-06-18T14:45:23.799Z",
				"type": {"id": "fullTime", "name": "Full-Time"},
				"location": {
					"country": {"name": "United States", "id": "US"},
					"city": "San Francisco",
					"primary": true,
					"is_remote": false,
					"name": "San Francisco, CA, US"
				},
				"department": "Engineering",
				"company": {"name": "Acme AI", "friendly_id": "acme"},
				"locations": [
					{"country": {"name": "United States", "id": "US"}, "city": "San Francisco", "name": "San Francisco, CA, US", "primary": true, "is_remote": false},
					{"country": {"name": "United States", "id": "US"}, "city": "Remote", "name": "Remote US", "is_remote": true}
				],
				"description": "<p>Build AI infrastructure and evaluation systems for 2026 internship candidates.</p>"
			}
		]`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_breezy",
		Name: "Acme AI",
		URL:  server.URL + "/",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "breezy",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.85 {
		t.Fatalf("result = %+v, want high-confidence Breezy result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "breezy:acme:b8e6b722f7ed" || job.Company != "Acme AI" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Breezy internship", job)
	}
	if job.Location != "San Francisco, CA, US; Remote US" || job.ApplyURL != "https://acme.breezy.hr/p/b8e6b722f7ed-software-engineering-intern-ai-platform" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "Build AI infrastructure") {
		t.Fatalf("evidence = %#v, want Breezy description evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsPersonioJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/xml" {
			t.Fatalf("path = %q, want Personio XML feed path", r.URL.Path)
		}
		if r.URL.Query().Get("language") != "en" {
			t.Fatalf("language = %q, want en", r.URL.Query().Get("language"))
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<workzag-jobs>
	<position>
		<id>1834171</id>
		<subcompany>Acme AI Ltd.</subcompany>
		<office>London</office>
		<additionalOffices>
			<office>Berlin</office>
		</additionalOffices>
		<department>Product and Tech</department>
		<recruitingCategory>Engineering</recruitingCategory>
		<name>Software Engineering Intern, AI Platform - Summer 2026</name>
		<jobDescriptions>
			<jobDescription>
				<name>The Role</name>
				<value><![CDATA[<p>Build AI platform services for 2026 internship candidates.</p>]]></value>
			</jobDescription>
			<jobDescription>
				<name>What you need</name>
				<value><![CDATA[<ul><li>Go, React, and distributed systems interest.</li></ul>]]></value>
			</jobDescription>
		</jobDescriptions>
		<employmentType>intern</employmentType>
		<seniority>student</seniority>
		<schedule>full-time</schedule>
		<occupation>software_and_web_development</occupation>
		<occupationCategory>it_software</occupationCategory>
		<createdAt>2026-06-19T10:12:30+00:00</createdAt>
	</position>
</workzag-jobs>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_personio",
		Name: "Acme AI",
		URL:  server.URL + "/careers",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "personio",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.85 {
		t.Fatalf("result = %+v, want high-confidence Personio result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "personio:1834171" || job.Company != "Acme AI" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "UK" {
		t.Fatalf("job = %#v, want normalized Personio internship", job)
	}
	if job.Location != "London; Berlin" || job.ApplyURL != server.URL+"/job/1834171?language=en" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "Build AI platform services") {
		t.Fatalf("evidence = %#v, want Personio description evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsPinpointJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/postings.json" {
			t.Fatalf("path = %q, want Pinpoint postings path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{
					"id": "110550",
					"title": "Software Engineering Intern, AI Platform - Summer 2026",
					"url": "https://acme.pinpointhq.com/en/postings/4e4fb030-ai-platform-intern",
					"description": "<div>Build AI infrastructure and evaluation systems.</div>",
					"key_responsibilities": "<ul><li>Ship Go services.</li></ul>",
					"skills_knowledge_expertise": "<p>Internship candidates graduating in 2026.</p>",
					"employment_type": "internship",
					"employment_type_text": "Internship",
					"workplace_type": "hybrid",
					"workplace_type_text": "Hybrid",
					"location": {
						"id": "17640",
						"city": "London",
						"name": "London",
						"province": "England"
					}
				}
			]
		}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_pinpoint",
		Name: "Acme AI",
		URL:  server.URL + "/jobs",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "pinpoint",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.85 {
		t.Fatalf("result = %+v, want high-confidence Pinpoint result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "pinpoint:110550" || job.Company != "Acme AI" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "UK" {
		t.Fatalf("job = %#v, want normalized Pinpoint internship", job)
	}
	if job.Location != "London, England" || job.ApplyURL != "https://acme.pinpointhq.com/en/postings/4e4fb030-ai-platform-intern" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "Build AI infrastructure") {
		t.Fatalf("evidence = %#v, want Pinpoint description evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsPolymerJobs(t *testing.T) {
	requestedDetails := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/aperturelabs/jobs":
			if r.URL.Query().Get("page") != "1" {
				t.Fatalf("page = %q, want 1", r.URL.Query().Get("page"))
			}
			_, _ = w.Write([]byte(`{
				"items": [
					{
						"id": 30084,
						"job_id": 30084,
						"title": "Back End Developer Intern - Summer 2026",
						"country": "US",
						"display_location": "San Francisco, CA",
						"organization_name": "Aperture Labs",
						"kind": "internship",
						"kind_pretty": "Internship",
						"published_at": "2026-01-15T14:33:11.225Z",
						"job_post_url": "https://jobs.polymer.co/aperturelabs/30084",
						"remoteness_pretty": "Remote friendly",
						"job_category_name": "Software Development"
					}
				],
				"meta": {"is_last": true, "page": 1, "organization_name": "Aperture Labs"}
			}`))
		case "/aperturelabs/jobs/30084":
			requestedDetails++
			_, _ = w.Write([]byte(`{
				"id": 30084,
				"job_id": 30084,
				"title": "Back End Developer Intern - Summer 2026",
				"description": "<p>Build distributed Go services for the developer platform.</p>",
				"country": "US",
				"display_location": "San Francisco, CA",
				"organization_name": "Aperture Labs",
				"kind": "internship",
				"kind_pretty": "Internship",
				"published_at": "2026-01-15T14:33:11.225Z",
				"job_post_url": "https://jobs.polymer.co/aperturelabs/30084",
				"remoteness_pretty": "Remote friendly",
				"job_category_name": "Software Development"
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:               server.Client(),
		PolymerPublicBaseURL: server.URL,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_polymer",
		URL:  "https://jobs.polymer.co/aperturelabs",
		Tier: TierATS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.85 {
		t.Fatalf("result = %+v, want high-confidence Polymer result", result)
	}
	if requestedDetails != 1 {
		t.Fatalf("requestedDetails = %d, want 1", requestedDetails)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "polymer:30084" || job.Company != "Aperture Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Polymer internship", job)
	}
	if job.Location != "San Francisco, CA" || job.ApplyURL != "https://jobs.polymer.co/aperturelabs/30084" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-01-15" {
		t.Fatalf("posted_at = %v, want 2026-01-15", job.PostedAt)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "distributed Go services") {
		t.Fatalf("evidence = %#v, want Polymer detail description evidence", job.Evidence)
	}
}

func TestPolymerOrganizationSlugFromSupportedURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "board", url: "https://jobs.polymer.co/aperturelabs", want: "aperturelabs"},
		{name: "job post", url: "https://jobs.polymer.co/aperturelabs/30084", want: "aperturelabs"},
		{name: "api list", url: "https://api.polymer.co/v1/hire/organizations/aperturelabs/jobs", want: "aperturelabs"},
		{name: "api detail", url: "https://api.polymer.co/v1/hire/organizations/aperturelabs/jobs/30084", want: "aperturelabs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := polymerOrganizationSlug(tt.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("polymerOrganizationSlug() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestATSExtractorExtractsICIMSJobsFromSitemap(t *testing.T) {
	requestedDetails := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
				<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
					<url><loc>` + serverURL + `/jobs/intro</loc></url>
					<url>
						<loc>` + serverURL + `/jobs/3112/software-engineer-intern-backend-platform/job</loc>
						<lastmod>2026-06-22T15:13:30-04:00</lastmod>
					</url>
				</urlset>`))
		case "/jobs/3112/software-engineer-intern-backend-platform/job":
			requestedDetails++
			if r.URL.Query().Get("in_iframe") != "1" {
				t.Fatalf("in_iframe = %q, want 1", r.URL.Query().Get("in_iframe"))
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><head>
					<script type="application/ld+json">{
						"@context": "http://schema.org",
						"@type": "JobPosting",
						"title": "Software Engineer Intern, Backend Platform - Summer 2026",
						"description": "<p>Build distributed Go services for mission software.</p>",
						"datePosted": "2026-06-22T04:00:00.000Z",
						"validThrough": "2027-06-22T04:00:00.000Z",
						"employmentType": "INTERN",
						"url": "` + serverURL + `/jobs/3112/software-engineer-intern-backend-platform/job",
						"hiringOrganization": {"@type": "Organization", "name": "Bridge Core"},
						"jobLocation": [{
							"@type": "Place",
							"address": {
								"@type": "PostalAddress",
								"addressLocality": "Chantilly",
								"addressRegion": "VA",
								"addressCountry": "US"
							}
						}]
					}</script>
				</head><body>
					<h1 class="iCIMS_Header">Software Engineer Intern, Backend Platform - Summer 2026</h1>
					<a href="` + serverURL + `/jobs/3112/software-engineer-intern-backend-platform/job?mode=apply&amp;apply=yes&amp;in_iframe=1">Apply for this job online</a>
				</body></html>`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), ICIMSMaxJobs: 5})
	sourceURL := server.URL + "/jobs/search?in_iframe=1"
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_icims",
		URL:  sourceURL,
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "icims",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requestedDetails = %d, want 1", requestedDetails)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 {
		t.Fatalf("result = %+v, want high-confidence iCIMS result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "icims:3112" || job.Company != "Bridge Core" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized iCIMS internship", job)
	}
	if job.Location != "Chantilly, VA, US" {
		t.Fatalf("location = %q, want Chantilly, VA, US", job.Location)
	}
	if !strings.Contains(job.ApplyURL, "mode=apply") {
		t.Fatalf("apply url = %q, want iCIMS apply URL", job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-22" {
		t.Fatalf("posted_at = %v, want 2026-06-22", job.PostedAt)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "distributed Go services") {
		t.Fatalf("evidence = %#v, want iCIMS description evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsJazzHRJobsFromHostedBoard(t *testing.T) {
	requestedDetails := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply/jobs/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><head><title>JazzHR &raquo; Job Listings</title></head>
				<body>
					<table id="jobs_table">
						<tbody>
							<tr><th>Position</th><th>Location</th></tr>
							<tr>
								<td>
									<a class="job_title_link" href="/apply/jobs/details/k3vLfIXwh7?&amp;">Software Engineering Intern, Backend Platform - Summer 2026</a>
									<br><span class="resumator_department">Engineering</span>
								</td>
								<td>Remote</td>
							</tr>
						</tbody>
					</table>
				</body></html>`))
		case "/apply/jobs/details/k3vLfIXwh7":
			requestedDetails++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><head>
					<script type="application/ld+json">{
						"@context": "https://schema.org/",
						"@type": "JobPosting",
						"title": "Software Engineering Intern, Backend Platform - Summer 2026",
						"description": "<p>Build distributed Go services for applicant tracking workflows.</p>",
						"datePosted": "2026-06-12",
						"validThrough": "2027-09-10",
						"employmentType": "INTERN",
						"url": "` + serverURL + `/apply/k3vLfIXwh7/software-engineering-intern-backend-platform-summer-2026",
						"hiringOrganization": {"@type": "Organization", "name": "Acme Labs"},
						"jobLocation": {
							"@type": "Place",
							"address": {
								"@type": "PostalAddress",
								"addressLocality": "New York",
								"addressRegion": "NY",
								"addressCountry": "US"
							}
						}
					}</script>
				</head><body>
					<h1 class="job_title">Software Engineering Intern, Backend Platform - Summer 2026</h1>
					<h3 class="job_meta">Remote - Internship</h3>
				</body></html>`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), JazzHRMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_jazzhr",
		URL:  server.URL + "/apply/jobs/",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "jazzhr",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requestedDetails = %d, want 1", requestedDetails)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 {
		t.Fatalf("result = %+v, want high-confidence JazzHR result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "jazzhr:k3vLfIXwh7" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized JazzHR internship", job)
	}
	if job.Location != "New York, NY, US" {
		t.Fatalf("location = %q, want New York, NY, US", job.Location)
	}
	if !strings.Contains(job.ApplyURL, "/apply/k3vLfIXwh7/") {
		t.Fatalf("apply url = %q, want JazzHR apply URL", job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-12" {
		t.Fatalf("posted_at = %v, want 2026-06-12", job.PostedAt)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "distributed Go services") {
		t.Fatalf("evidence = %#v, want JazzHR description evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsJazzHRDirectDetailWhenBoardUnavailable(t *testing.T) {
	requestedDetails := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apply/jobs/":
			http.NotFound(w, r)
		case "/apply/jobs/details/direct123":
			requestedDetails++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><head>
					<script type="application/ld+json">{
						"@context": "https://schema.org/",
						"@type": "JobPosting",
						"title": "New Grad Software Engineer, Product Platform",
						"description": "<p>Own product platform services and collaborate with frontend teams.</p>",
						"datePosted": "2026-06-13",
						"validThrough": "2027-09-10",
						"employmentType": "FULL_TIME",
						"url": "` + serverURL + `/apply/direct123/new-grad-software-engineer-product-platform",
						"hiringOrganization": {"@type": "Organization", "name": "Acme Labs"},
						"jobLocation": {
							"@type": "Place",
							"address": {
								"@type": "PostalAddress",
								"addressLocality": "Boston",
								"addressRegion": "MA",
								"addressCountry": "US"
							}
						}
					}</script>
				</head><body></body></html>`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), JazzHRMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_jazzhr_detail",
		URL:  server.URL + "/apply/jobs/details/direct123",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "jazzhr",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requestedDetails = %d, want 1", requestedDetails)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "jazzhr:direct123" || job.Company != "Acme Labs" || job.Level != "new_grad" {
		t.Fatalf("job = %#v, want direct JazzHR new-grad job", job)
	}
}

func TestJazzHRBoardURLFromSupportedURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "board", url: "https://cecp.applytojob.com/apply/jobs/", want: "https://cecp.applytojob.com/apply/jobs/"},
		{name: "detail", url: "https://cecp.applytojob.com/apply/jobs/details/k3vLfIXwh7?&", want: "https://cecp.applytojob.com/apply/jobs/"},
		{name: "apply", url: "https://cecp.applytojob.com/apply/k3vLfIXwh7/corporate-insights-research-2026-intern", want: "https://cecp.applytojob.com/apply/jobs/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := jazzHRBoardURL(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("jazzHRBoardURL() = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestATSExtractorExtractsJobviteJobsFromHostedBoard(t *testing.T) {
	requestedDetails := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/jobs":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><head><title>Acme Labs Careers</title></head>
				<body>
					<table class="jv-job-list">
						<tbody>
							<tr>
								<td class="jv-job-list-name">
									<a href="/acme/job/oMl123">Machine Learning Engineer Intern - Fall 2026</a>
								</td>
								<td class="jv-job-list-location">Sunnyvale, California</td>
							</tr>
						</tbody>
					</table>
				</body></html>`))
		case "/acme/job/oMl123":
			requestedDetails++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><head>
					<script type="text/javascript">function getCompanyName() { return 'Acme Labs'; }</script>
					<script type="text/javascript">function getJobId() { return 'oMl123'; }</script>
				</head><body>
					<script type="application/ld+json">{
						"@context": "http://schema.org",
						"@type": "JobPosting",
						"datePosted": "2026-06-17",
						"description": "<p>Build machine learning ranking systems and Go services for job intelligence.</p>",
						"employmentType": "Intern",
						"hiringOrganization": "Acme Labs",
						"identifier": "oMl123",
						"industry": "Engineering",
						"jobLocation": [{
							"@type": "Place",
							"address": {
								"@type": "PostalAddress",
								"addressLocality": "Sunnyvale",
								"addressRegion": "CA",
								"addressCountry": "US"
							}
						}],
						"title": "Machine Learning Engineer Intern - Fall 2026",
						"url": "` + serverURL + `/acme/job/oMl123"
					}</script>
					<a class="jv-button jv-button-primary jv-button-apply" href="/acme/job/oMl123/apply">Apply</a>
				</body></html>`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), JobviteMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_jobvite",
		Name: "Acme Labs",
		URL:  server.URL + "/acme/jobs",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "jobvite",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requestedDetails = %d, want 1", requestedDetails)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 {
		t.Fatalf("result = %+v, want high-confidence Jobvite result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "jobvite:acme:oMl123" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Jobvite internship", job)
	}
	if job.Location != "Sunnyvale, CA, US" {
		t.Fatalf("location = %q, want Sunnyvale, CA, US", job.Location)
	}
	if !strings.Contains(job.ApplyURL, "/acme/job/oMl123/apply") {
		t.Fatalf("apply url = %q, want Jobvite apply URL", job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-17" {
		t.Fatalf("posted_at = %v, want 2026-06-17", job.PostedAt)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "machine learning ranking systems") {
		t.Fatalf("evidence = %#v, want Jobvite description evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsJobviteDirectDetailWithoutJSONLD(t *testing.T) {
	requestedDetails := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/jobs":
			http.NotFound(w, r)
		case "/acme/job/direct123":
			requestedDetails++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><head>
					<title>Acme Careers - Backend Software Engineer New Grad</title>
					<script type="text/javascript">function getCompanyName() { return 'Acme Labs'; }</script>
				</head><body>
					<h2 class="jv-header">Backend Software Engineer New Grad</h2>
					<p class="jv-job-detail-meta">Engineering<span class="jv-inline-separator"></span>New York, New York</p>
					<div class="jv-job-detail-description" ng-non-bindable>
						<p>Build distributed backend systems for candidate matching.</p>
					</div>
					<div class="job-description-meta">
						<ul>
							<li><strong>Location</strong>New York, New York</li>
							<li><strong>Employment Type</strong>Full-Time</li>
						</ul>
					</div>
					<div class="jv-job-detail-bottom-actions">
						<a class="jv-button jv-button-primary jv-button-apply" href="/acme/job/direct123/apply">Apply</a>
					</div>
				</body></html>`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), JobviteMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_jobvite_detail",
		URL:  server.URL + "/acme/job/direct123",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "jobvite",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requestedDetails = %d, want 1", requestedDetails)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "jobvite:acme:direct123" || job.Company != "Acme Labs" || job.Level != "new_grad" || job.RoleFamily != "backend" {
		t.Fatalf("job = %#v, want direct Jobvite new-grad backend job", job)
	}
	if job.Location != "New York, New York" {
		t.Fatalf("location = %q, want New York, New York", job.Location)
	}
	if !strings.Contains(job.ApplyURL, "/acme/job/direct123/apply") {
		t.Fatalf("apply url = %q, want Jobvite apply URL", job.ApplyURL)
	}
}

func TestJobviteBoardURLFromSupportedURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "root", url: "https://jobs.jobvite.com/progress", want: "https://jobs.jobvite.com/progress/jobs"},
		{name: "jobs", url: "https://jobs.jobvite.com/progress/jobs", want: "https://jobs.jobvite.com/progress/jobs"},
		{name: "search", url: "https://jobs.jobvite.com/progress/search?c=Engineering", want: "https://jobs.jobvite.com/progress/jobs"},
		{name: "detail", url: "https://jobs.jobvite.com/progress/job/oAVfAfw4", want: "https://jobs.jobvite.com/progress/jobs"},
		{name: "apply", url: "https://jobs.jobvite.com/progress/job/oAVfAfw4/apply", want: "https://jobs.jobvite.com/progress/jobs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := jobviteBoardURL(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("jobviteBoardURL() = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestATSExtractorExtractsTeamtailorJobsFromHostedBoard(t *testing.T) {
	requestedDetails := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><body>
					<a class="no-company-style" data-turbo="false" href="/jobs/7847431-software-engineering-intern-backend-platform">
						<span class="absolute inset-0"></span>
						Software Engineering Intern, Backend Platform
					</a>
				</body></html>`))
		case "/jobs/7847431-software-engineering-intern-backend-platform":
			requestedDetails++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><body>
					<main data-careersite--jobs--form-overlay-job-id-value="7847431"
						data-careersite--jobs--form-overlay-job-application-url-value="` + serverURL + `/jobs/7847431-software-engineering-intern-backend-platform/applications/new">
						<script type="application/ld+json">{
							"@context": "http://schema.org/",
							"@type": "JobPosting",
							"title": "Software Engineering Intern, Backend Platform",
							"description": "<p>Build backend platform systems for scraping and data acquisition.</p>",
							"identifier": {
								"@type": "PropertyValue",
								"name": "Flanks",
								"value": "7847431"
							},
							"datePosted": "2026-06-18T10:39:45+02:00",
							"employmentType": "INTERN",
							"hiringOrganization": {
								"@type": "Organization",
								"name": "Flanks",
								"sameAs": "` + serverURL + `"
							},
							"jobLocation": [{
								"@type": "Place",
								"address": {
									"@type": "PostalAddress",
									"addressLocality": "Barcelona",
									"addressRegion": "Catalunya",
									"addressCountry": "ES"
								}
							}]
						}</script>
					</main>
				</body></html>`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), TeamtailorMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_teamtailor",
		URL:  server.URL + "/jobs",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "teamtailor",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requestedDetails = %d, want 1", requestedDetails)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 {
		t.Fatalf("result = %+v, want high-confidence Teamtailor result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "teamtailor:127-0-0-1:7847431" || job.Company != "Flanks" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "Spain" {
		t.Fatalf("job = %#v, want normalized Teamtailor internship", job)
	}
	if job.Location != "Barcelona, Catalunya, Spain" {
		t.Fatalf("location = %q, want Barcelona, Catalunya, Spain", job.Location)
	}
	if !strings.Contains(job.ApplyURL, "/jobs/7847431-software-engineering-intern-backend-platform/applications/new") {
		t.Fatalf("apply url = %q, want Teamtailor application URL", job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-18" {
		t.Fatalf("posted_at = %v, want 2026-06-18", job.PostedAt)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "backend platform systems") {
		t.Fatalf("evidence = %#v, want Teamtailor description evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsTeamtailorDirectDetailWhenBoardUnavailable(t *testing.T) {
	requestedDetails := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs":
			http.NotFound(w, r)
		case "/jobs/7765804-software-engineer":
			requestedDetails++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><body>
					<main data-careersite--jobs--form-overlay-job-application-url-value="` + serverURL + `/jobs/7765804-software-engineer/applications/new">
						<script type="application/ld+json">{
							"@context": "http://schema.org/",
							"@type": "JobPosting",
							"title": "Software Engineer",
							"description": "<p>Build production web services for panel data collection.</p>",
							"identifier": {"@type": "PropertyValue", "name": "RealityMine", "value": "7765804"},
							"datePosted": "2026-06-19",
							"employmentType": "FULL_TIME",
							"hiringOrganization": {"@type": "Organization", "name": "RealityMine"},
							"jobLocation": [{
								"@type": "Place",
								"address": {
									"@type": "PostalAddress",
									"addressLocality": "Manchester",
									"addressCountry": "GB"
								}
							}]
						}</script>
					</main>
				</body></html>`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), TeamtailorMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_teamtailor_detail",
		URL:  server.URL + "/jobs/7765804-software-engineer",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "teamtailor",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requestedDetails = %d, want 1", requestedDetails)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "teamtailor:127-0-0-1:7765804" || job.Company != "RealityMine" || job.RoleFamily != "software_engineering" || job.Country != "UK" {
		t.Fatalf("job = %#v, want direct Teamtailor software job", job)
	}
}

func TestTeamtailorBoardURLFromSupportedURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "root", url: "https://flanks.teamtailor.com", want: "https://flanks.teamtailor.com/jobs"},
		{name: "jobs", url: "https://flanks.teamtailor.com/jobs", want: "https://flanks.teamtailor.com/jobs"},
		{name: "detail", url: "https://flanks.teamtailor.com/jobs/7847431-software-engineering-intern-web-scraping-data-acquisition", want: "https://flanks.teamtailor.com/jobs"},
		{name: "regional detail", url: "https://arborealmanagement.na.teamtailor.com/jobs/582953-software-engineering-intern", want: "https://arborealmanagement.na.teamtailor.com/jobs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := teamtailorBoardURL(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("teamtailorBoardURL() = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestATSExtractorExtractsBambooHRJobsFromPublicCareersAPI(t *testing.T) {
	requestedDetails := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/careers/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"meta": {"totalCount": 2},
				"result": [
					{
						"id": "99",
						"jobOpeningName": "Software Engineer Intern, Backend Platform - Summer 2026",
						"departmentLabel": "Engineering",
						"employmentStatusLabel": "Internship",
						"location": {"city": "New York", "state": "NY"},
						"atsLocation": {"country": "United States", "state": "NY", "city": "New York"},
						"isRemote": false,
						"locationType": "1"
					},
					{
						"id": "100",
						"jobOpeningName": "Sales Associate",
						"departmentLabel": "Sales",
						"employmentStatusLabel": "Full-Time",
						"location": {"city": "Austin", "state": "TX"},
						"atsLocation": {"country": "United States", "state": "TX", "city": "Austin"},
						"isRemote": false,
						"locationType": "1"
					}
				]
			}`))
		case "/careers/99/detail":
			requestedDetails++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"result": {
					"jobOpening": {
						"jobOpeningShareUrl": "` + serverURL + `/careers/99",
						"jobOpeningName": "Software Engineer Intern, Backend Platform - Summer 2026",
						"jobOpeningStatus": "Open",
						"departmentLabel": "Engineering",
						"employmentStatusLabel": "Internship",
						"location": {"city": "New York", "state": "NY", "addressCountry": "United States"},
						"atsLocation": {"country": "United States", "state": "NY", "city": "New York"},
						"description": "<p>Build distributed Go services for public job intelligence workflows.</p>",
						"compensation": "$30/hr",
						"datePosted": "2026-06-10",
						"minimumExperience": "Entry-level",
						"locationType": "1"
					}
				}
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), BambooHRMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_bamboohr",
		Name: "Acme Labs",
		URL:  server.URL + "/careers",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "bamboohr",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requestedDetails = %d, want 1", requestedDetails)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 {
		t.Fatalf("result = %+v, want high-confidence BambooHR result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "bamboohr:127-0-0-1:99" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized BambooHR internship", job)
	}
	if job.Location != "New York, NY, US" {
		t.Fatalf("location = %q, want New York, NY, US", job.Location)
	}
	if !strings.Contains(job.ApplyURL, "/careers/99") {
		t.Fatalf("apply url = %q, want BambooHR career URL", job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-10" {
		t.Fatalf("posted_at = %v, want 2026-06-10", job.PostedAt)
	}
	foundDescription := false
	for _, evidence := range job.Evidence {
		if evidence.Field == "description" && strings.Contains(evidence.Text, "distributed Go services") {
			foundDescription = true
		}
	}
	if !foundDescription {
		t.Fatalf("evidence = %#v, want BambooHR description evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsBambooHRDirectDetailWhenListUnavailable(t *testing.T) {
	requestedDetails := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/careers/list":
			http.NotFound(w, r)
		case "/careers/371/detail":
			requestedDetails++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"result": {
					"jobOpening": {
						"jobOpeningShareUrl": "` + serverURL + `/careers/371",
						"jobOpeningName": "UAS Software Engineer",
						"jobOpeningStatus": "Open",
						"departmentLabel": "Engineering",
						"employmentStatusLabel": "Full-Time",
						"location": {"city": "Huntsville", "state": "AL", "addressCountry": "United States"},
						"atsLocation": {"country": "United States", "state": "AL", "city": "Huntsville"},
						"description": "<p>Build flight software and simulation tools.</p>",
						"datePosted": "2026-06-11",
						"minimumExperience": "Entry-level",
						"locationType": "1"
					}
				}
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), BambooHRMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_bamboohr_detail",
		Name: "Aerodyne",
		URL:  server.URL + "/careers/371",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "bamboohr",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requestedDetails = %d, want 1", requestedDetails)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "bamboohr:127-0-0-1:371" || job.Company != "Aerodyne" || job.RoleFamily != "software_engineering" || job.Country != "US" {
		t.Fatalf("job = %#v, want direct BambooHR software job", job)
	}
	if job.Location != "Huntsville, AL, US" {
		t.Fatalf("location = %q, want Huntsville, AL, US", job.Location)
	}
}

func TestATSExtractorPrioritizesDirectBambooHRDetailInsideMaxJobs(t *testing.T) {
	requestedDetails := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/careers/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"result": [
					{"id": "1", "jobOpeningName": "Sales Associate", "employmentStatusLabel": "Full-Time"},
					{"id": "371", "jobOpeningName": "UAS Software Engineer", "employmentStatusLabel": "Full-Time"}
				]
			}`))
		case "/careers/371/detail":
			requestedDetails++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"result": {
					"jobOpening": {
						"jobOpeningShareUrl": "` + serverURL + `/careers/371",
						"jobOpeningName": "UAS Software Engineer",
						"jobOpeningStatus": "Open",
						"employmentStatusLabel": "Full-Time",
						"location": {"city": "Huntsville", "state": "AL", "addressCountry": "United States"},
						"description": "<p>Build production software for autonomy systems.</p>"
					}
				}
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), BambooHRMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_bamboohr_detail_priority",
		Name: "Aerodyne",
		URL:  server.URL + "/careers/371",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "bamboohr",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requestedDetails = %d, want 1 direct detail request", requestedDetails)
	}
	if len(result.Jobs) != 1 || result.Jobs[0].SourceJobID != "bamboohr:127-0-0-1:371" {
		t.Fatalf("jobs = %#v, want direct BambooHR job inside bounded fetch window", result.Jobs)
	}
}

func TestBambooHRURLHelpers(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		listURL   string
		detailID  string
		detailURL string
		directJob string
	}{
		{
			name:      "careers root",
			url:       "https://fortunatemedia.bamboohr.com/careers",
			listURL:   "https://fortunatemedia.bamboohr.com/careers/list",
			detailID:  "99",
			detailURL: "https://fortunatemedia.bamboohr.com/careers/99/detail",
		},
		{
			name:      "career detail",
			url:       "https://fortunatemedia.bamboohr.com/careers/99",
			listURL:   "https://fortunatemedia.bamboohr.com/careers/list",
			detailID:  "99",
			detailURL: "https://fortunatemedia.bamboohr.com/careers/99/detail",
			directJob: "99",
		},
		{
			name:      "legacy view",
			url:       "https://fortunatemedia.bamboohr.com/jobs/view.php?id=99&source=abc",
			listURL:   "https://fortunatemedia.bamboohr.com/careers/list",
			detailID:  "99",
			detailURL: "https://fortunatemedia.bamboohr.com/careers/99/detail",
			directJob: "99",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listURL, err := bambooHRListURL(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if listURL.String() != tt.listURL {
				t.Fatalf("bambooHRListURL() = %q, want %q", listURL.String(), tt.listURL)
			}
			detailURL, err := bambooHRDetailURL(tt.url, tt.detailID)
			if err != nil {
				t.Fatal(err)
			}
			if detailURL.String() != tt.detailURL {
				t.Fatalf("bambooHRDetailURL() = %q, want %q", detailURL.String(), tt.detailURL)
			}
			if got := bambooHRJobIDFromURL(tt.url); got != tt.directJob {
				t.Fatalf("bambooHRJobIDFromURL() = %q, want %q", got, tt.directJob)
			}
		})
	}
}

func TestATSExtractorExtractsRipplingJobsFromBoardAPI(t *testing.T) {
	requestedDetails := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/board/acme/jobs":
			if r.URL.Query().Get("page") != "0" || r.URL.Query().Get("pageSize") != "2" {
				t.Fatalf("query = %q, want page=0&pageSize=2", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"items": [
					{
						"id": "job-1",
						"name": "Software Engineer Intern, Backend Platform - Summer 2026",
						"url": "` + serverURL + `/acme/jobs/job-1",
						"department": {"name": "Engineering"},
						"locations": [
							{"name": "New York, NY", "country": "United States", "countryCode": "US", "stateCode": "NY", "city": "New York", "workplaceType": "HYBRID"}
						],
						"language": "en-US"
					},
					{
						"id": "job-2",
						"name": "Sales Associate",
						"url": "` + serverURL + `/acme/jobs/job-2",
						"department": {"name": "Sales"},
						"locations": [
							{"name": "Austin, TX", "country": "United States", "countryCode": "US", "stateCode": "TX", "city": "Austin", "workplaceType": "ON_SITE"}
						],
						"language": "en-US"
					}
				],
				"page": 0,
				"pageSize": 2,
				"totalItems": 2,
				"totalPages": 1
			}`))
		case "/api/v2/board/acme/jobs/job-1":
			requestedDetails++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"uuid": "job-1",
				"name": "Software Engineer Intern, Backend Platform - Summer 2026",
				"url": "` + serverURL + `/acme/jobs/job-1",
				"companyName": "Acme Labs",
				"description": {
					"company": "<p>Acme Labs builds job intelligence systems.</p>",
					"role": "<p>Build distributed Go services for crawler workers and matching fanout.</p>"
				},
				"workLocations": ["New York, NY"],
				"department": {"name": "Engineering", "base_department": "Engineering", "department_tree": ["Engineering", "Interns"]},
				"employmentType": {"label": "TEMP", "id": "Temporary / Intern"},
				"createdOn": "2026-05-13T02:37:35.881000-07:00"
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), RipplingPageSize: 2, RipplingMaxPages: 1, RipplingMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_rippling",
		URL:  server.URL + "/acme",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "rippling",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requestedDetails = %d, want 1", requestedDetails)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 {
		t.Fatalf("result = %+v, want high-confidence Rippling result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "rippling:acme:job-1" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Rippling internship", job)
	}
	if job.Location != "New York, NY, US" {
		t.Fatalf("location = %q, want New York, NY, US", job.Location)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-05-13" {
		t.Fatalf("posted_at = %v, want 2026-05-13", job.PostedAt)
	}
	foundDescription := false
	for _, evidence := range job.Evidence {
		if evidence.Field == "description" && strings.Contains(evidence.Text, "crawler workers") {
			foundDescription = true
		}
	}
	if !foundDescription {
		t.Fatalf("evidence = %#v, want Rippling role description evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsRipplingDirectDetailWhenBoardUnavailable(t *testing.T) {
	requestedDetails := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/board/acme/jobs":
			http.NotFound(w, r)
		case "/api/v2/board/acme/jobs/job-99":
			requestedDetails++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"uuid": "job-99",
				"name": "Software Engineer II, Backend - Platform Team",
				"url": "` + serverURL + `/acme/jobs/job-99",
				"companyName": "Acme Labs",
				"description": {
					"role": "<p>Build backend systems for platform automation.</p>"
				},
				"workLocations": ["San Francisco, CA"],
				"department": {"name": "Engineering"},
				"employmentType": {"label": "FULL_TIME", "id": "Full time"},
				"createdOn": "2026-05-14T11:00:00Z"
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), RipplingPageSize: 2, RipplingMaxPages: 1, RipplingMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_rippling_detail",
		URL:  server.URL + "/acme/jobs/job-99",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "rippling",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requestedDetails = %d, want 1", requestedDetails)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "rippling:acme:job-99" || job.Company != "Acme Labs" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want direct Rippling backend job", job)
	}
}

func TestATSExtractorExtractsRipplingHostedJSONLDJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acme" {
			t.Fatalf("path = %q, want Rippling hosted board", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
	<head>
		<script type="application/ld+json">
		{
			"@context": "https://schema.org",
			"@type": "JobPosting",
			"identifier": {"value": "rp-2026"},
			"title": "Backend Software Engineer Intern - Summer 2026",
			"description": "<p>Build Go and TypeScript platform APIs for new-grad and internship users.</p>",
			"datePosted": "2026-05-01",
			"employmentType": "Internship",
			"hiringOrganization": {"name": "Rippling Labs"},
			"jobLocation": {"@type": "Place", "address": {"addressLocality": "San Francisco", "addressRegion": "CA", "addressCountry": "US"}},
			"url": "/acme/jobs/rp-2026"
		}
		</script>
	</head>
	<body>Open jobs</body>
</html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), RipplingMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_rippling_jobs",
		Name: "Rippling Labs",
		URL:  server.URL + "/acme",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "rippling_jobs",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.8 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "rippling_jobs:rp-2026" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Rippling hosted internship", job)
	}
	if evidenceText(job.Evidence, "ats") != "Rippling Jobs hosted page JSON-LD or sitemap" {
		t.Fatalf("ats evidence = %q, want Rippling Jobs evidence", evidenceText(job.Evidence, "ats"))
	}
}

func TestRipplingURLHelpers(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		board     string
		jobID     string
		jobsURL   string
		detailURL string
	}{
		{
			name:    "board root",
			url:     "https://ats.rippling.com/rippling",
			board:   "rippling",
			jobsURL: "https://ats.rippling.com/api/v2/board/rippling/jobs?page=0&pageSize=50",
		},
		{
			name:      "job detail",
			url:       "https://ats.rippling.com/rippling/jobs/00cbc991-d2fb-452c-a8b6-2978f109a484",
			board:     "rippling",
			jobID:     "00cbc991-d2fb-452c-a8b6-2978f109a484",
			jobsURL:   "https://ats.rippling.com/api/v2/board/rippling/jobs?page=0&pageSize=50",
			detailURL: "https://ats.rippling.com/api/v2/board/rippling/jobs/00cbc991-d2fb-452c-a8b6-2978f109a484",
		},
		{
			name:      "localized detail",
			url:       "https://ats.rippling.com/en-GB/rippling/jobs/00cbc991-d2fb-452c-a8b6-2978f109a484",
			board:     "rippling",
			jobID:     "00cbc991-d2fb-452c-a8b6-2978f109a484",
			jobsURL:   "https://ats.rippling.com/api/v2/board/rippling/jobs?page=0&pageSize=50",
			detailURL: "https://ats.rippling.com/api/v2/board/rippling/jobs/00cbc991-d2fb-452c-a8b6-2978f109a484",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			board, err := ripplingBoardSlug(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if board != tt.board {
				t.Fatalf("ripplingBoardSlug() = %q, want %q", board, tt.board)
			}
			jobsURL, err := ripplingJobsAPIURL(tt.url, board, 0, 50)
			if err != nil {
				t.Fatal(err)
			}
			if jobsURL.String() != tt.jobsURL {
				t.Fatalf("ripplingJobsAPIURL() = %q, want %q", jobsURL.String(), tt.jobsURL)
			}
			if got := ripplingJobIDFromURL(tt.url); got != tt.jobID {
				t.Fatalf("ripplingJobIDFromURL() = %q, want %q", got, tt.jobID)
			}
			if tt.detailURL != "" {
				detailURL, err := ripplingDetailAPIURL(tt.url, board, tt.jobID)
				if err != nil {
					t.Fatal(err)
				}
				if detailURL.String() != tt.detailURL {
					t.Fatalf("ripplingDetailAPIURL() = %q, want %q", detailURL.String(), tt.detailURL)
				}
			}
		})
	}
}

func TestATSExtractorExtractsSuccessFactorsJobsFromRSSFeed(t *testing.T) {
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sitemal.xml" {
			t.Fatalf("path = %q, want /sitemal.xml", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
			<rss version="2.0" xmlns:g="http://base.google.com/ns/1.0">
				<channel>
					<title>Jobs at Acme Labs</title>
					<description>Search jobs and apply.</description>
					<item>
						<title>Software Engineer Intern - Backend Platform (New York, NY, US, 10001)</title>
						<description><![CDATA[
							<p><strong>Req Number 331432</strong></p>
							<p>Build distributed Go services for crawler workers and matching fanout.</p>
							<p>Work Location Type: Hybrid</p>
						]]></description>
						<link>` + serverURL + `/job/New-York-Software-Engineer-Intern-Backend-Platform-NY-10001/1395703100/</link>
						<guid isPermaLink="false">1395703100</guid>
						<g:id>1395703100</g:id>
						<g:expiration_date>2026-07-23</g:expiration_date>
						<g:employer>Acme Labs</g:employer>
						<g:job_function>Engineering</g:job_function>
						<g:location>New York, NY, US, 10001</g:location>
					</item>
					<item>
						<title>Sales Associate (Austin, TX, US, 78701)</title>
						<description><![CDATA[<p>Sell enterprise software.</p>]]></description>
						<link>` + serverURL + `/job/Austin-Sales-Associate-TX-78701/1395703200/</link>
						<guid isPermaLink="false">1395703200</guid>
						<g:id>1395703200</g:id>
						<g:employer>Acme Labs</g:employer>
						<g:job_function>Sales</g:job_function>
						<g:location>Austin, TX, US, 78701</g:location>
					</item>
				</channel>
			</rss>`))
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), SuccessFactorsMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_successfactors",
		URL:  server.URL + "/search",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "successfactors",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 {
		t.Fatalf("result = %+v, want high-confidence SuccessFactors result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "successfactors:127-0-0-1:1395703100" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized SuccessFactors internship", job)
	}
	if job.Title != "Software Engineer Intern - Backend Platform" {
		t.Fatalf("title = %q, want title without location suffix", job.Title)
	}
	if job.Location != "New York, NY, US, 10001" {
		t.Fatalf("location = %q, want feed location", job.Location)
	}
	if job.PostedAt != nil {
		t.Fatalf("posted_at = %v, want nil because feed only exposes expiration", job.PostedAt)
	}
	foundDescription := false
	foundExpiration := false
	for _, evidence := range job.Evidence {
		if evidence.Field == "description" && strings.Contains(evidence.Text, "crawler workers") {
			foundDescription = true
		}
		if evidence.Field == "expiration_date" && evidence.Text == "2026-07-23" {
			foundExpiration = true
		}
	}
	if !foundDescription || !foundExpiration {
		t.Fatalf("evidence = %#v, want description and expiration date evidence", job.Evidence)
	}
}

func TestSuccessFactorsFeedURLFromSupportedURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "root", url: "https://jobs.sap.com", want: "https://jobs.sap.com/sitemal.xml"},
		{name: "search", url: "https://jobs.sap.com/search/?q=software", want: "https://jobs.sap.com/sitemal.xml"},
		{name: "sitemal", url: "https://jobs.sap.com/sitemal.xml", want: "https://jobs.sap.com/sitemal.xml"},
		{name: "sitemap rss", url: "https://jobs.sap.com/sitemap.xml", want: "https://jobs.sap.com/sitemap.xml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := successFactorsFeedURL(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("successFactorsFeedURL() = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestATSExtractorExtractsADPWorkforceNowJobsFromPublicAPI(t *testing.T) {
	requestedDetails := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mascsr/default/careercenter/public/events/staffing/v1/job-requisitions":
			if got := r.URL.Query().Get("cid"); got != "cid-123" {
				t.Fatalf("cid = %q, want cid-123", got)
			}
			if got := r.URL.Query().Get("ccId"); got != "19000101_000001" {
				t.Fatalf("ccId = %q, want 19000101_000001", got)
			}
			if got := r.URL.Query().Get("lang"); got != "en_US" {
				t.Fatalf("lang = %q, want en_US", got)
			}
			if got := r.URL.Query().Get("locale"); got != "en_US" {
				t.Fatalf("locale = %q, want en_US", got)
			}
			if got := r.URL.Query().Get("$top"); got != "2" {
				t.Fatalf("$top = %q, want 2", got)
			}
			if got := r.URL.Query().Get("$skip"); got != "0" {
				t.Fatalf("$skip = %q, want 0", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"jobRequisitions": [{
					"itemID": "jw-1",
					"requisitionTitle": "Software Engineer Intern, Backend Platform - Summer 2026",
					"postDate": "2026-06-18T11:03:00.000-04:00",
					"clientRequisitionID": "REQ-1",
					"requisitionLocations": [{
						"address": {
							"cityName": "New York",
							"countrySubdivisionLevel1": {"codeValue": "NY"},
							"postalCode": "10001"
						},
						"nameCode": {"shortName": "New York, NY, US"}
					}],
					"workLevelCode": {"shortName": "Intern-paid wages Part-Time"},
					"customFieldGroup": {
						"stringFields": [
							{"stringValue": "936700", "nameCode": {"codeValue": "ExternalJobID"}},
							{"stringValue": "Engineering", "nameCode": {"codeValue": "JobClass"}},
							{"stringValue": "$30 To $40 (USD) Hourly", "nameCode": {"codeValue": "SalaryRange"}}
						],
						"dateFields": [
							{"dateValue": "2026-06-18T11:03Z", "nameCode": {"codeValue": "PostingDate"}}
						]
					}
				}, {
					"itemID": "jw-2",
					"requisitionTitle": "Sales Associate",
					"customFieldGroup": {
						"stringFields": [
							{"stringValue": "936701", "nameCode": {"codeValue": "ExternalJobID"}}
						]
					}
				}],
				"meta": {"totalNumber": 2}
			}`))
		case "/mascsr/default/careercenter/public/events/staffing/v1/job-requisitions/936700":
			requestedDetails++
			if got := r.URL.Query().Get("cid"); got != "cid-123" {
				t.Fatalf("detail cid = %q, want cid-123", got)
			}
			if got := r.URL.Query().Get("ccId"); got != "19000101_000001" {
				t.Fatalf("detail ccId = %q, want 19000101_000001", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"itemID": "jw-1",
				"requisitionTitle": "Software Engineer Intern, Backend Platform - Summer 2026",
				"clientRequisitionID": "REQ-1",
				"postDate": "2026-06-18T11:03:00.000-04:00",
				"requisitionDescription": "<p>Build distributed Go services for crawler workers and matching fanout.</p>",
				"requisitionLocations": [{
					"address": {
						"cityName": "New York",
						"countrySubdivisionLevel1": {"codeValue": "NY"},
						"postalCode": "10001"
					},
					"nameCode": {"shortName": "New York, NY, US"}
				}],
				"workLevelCode": {"shortName": "Intern-paid wages Part-Time"},
				"customFieldGroup": {
					"stringFields": [
						{"stringValue": "936700", "nameCode": {"codeValue": "ExternalJobID"}},
						{"stringValue": "Engineering", "nameCode": {"codeValue": "JobClass"}},
						{"stringValue": "$30 To $40 (USD) Hourly", "nameCode": {"codeValue": "SalaryRange"}}
					],
					"dateFields": [
						{"dateValue": "2026-06-18T11:03Z", "nameCode": {"codeValue": "PostingDate"}}
					]
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:                  server.Client(),
		ADPWorkforceNowPageSize: 2,
		ADPWorkforceNowMaxPages: 1,
		ADPWorkforceNowMaxJobs:  1,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_adp_wfn",
		Name: "Acme Labs",
		URL:  server.URL + "/mascsr/default/mdf/recruitment/recruitment.html?cid=cid-123&ccId=19000101_000001&lang=en_US&type=JS",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "adp_workforcenow",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requested details = %d, want 1", requestedDetails)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 {
		t.Fatalf("result = %+v, want high-confidence ADP Workforce Now result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "adp_workforcenow:cid-123:936700" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized ADP Workforce Now internship", job)
	}
	if job.Location != "New York, NY, US" {
		t.Fatalf("location = %q, want New York, NY, US", job.Location)
	}
	if job.PostedAt == nil || job.PostedAt.Format("2006-01-02") != "2026-06-18" {
		t.Fatalf("posted_at = %v, want 2026-06-18", job.PostedAt)
	}
	if !strings.Contains(job.ApplyURL, "/mascsr/default/mdf/recruitment/recruitment.html") || !strings.Contains(job.ApplyURL, "jobId=936700") || !strings.Contains(job.ApplyURL, "jwId=jw-1") {
		t.Fatalf("apply_url = %q, want hosted ADP apply URL with jobId and jwId", job.ApplyURL)
	}
	foundDescription := false
	foundSalary := false
	for _, evidence := range job.Evidence {
		if evidence.Field == "description" && strings.Contains(evidence.Text, "crawler workers") {
			foundDescription = true
		}
		if evidence.Field == "salary_range" && strings.Contains(evidence.Text, "$30") {
			foundSalary = true
		}
	}
	if !foundDescription || !foundSalary {
		t.Fatalf("evidence = %#v, want description and salary range evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsADPWorkforceNowDirectDetailWhenListUnavailable(t *testing.T) {
	requestedDetails := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mascsr/default/careercenter/public/events/staffing/v1/job-requisitions":
			http.NotFound(w, r)
		case "/mascsr/default/careercenter/public/events/staffing/v1/job-requisitions/936700":
			requestedDetails++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"itemID": "jw-1",
				"requisitionTitle": "Software Engineer II, Backend - Platform Team",
				"clientRequisitionID": "REQ-2",
				"requisitionDescription": "<p>Own distributed backend systems and APIs.</p>",
				"requisitionLocations": [{
					"address": {
						"cityName": "San Francisco",
						"countrySubdivisionLevel1": {"codeValue": "CA"}
					},
					"nameCode": {"shortName": "San Francisco, CA, US"}
				}],
				"workLevelCode": {"shortName": "Regular Full-Time"},
				"customFieldGroup": {
					"stringFields": [
						{"stringValue": "936700", "nameCode": {"codeValue": "ExternalJobID"}},
						{"stringValue": "Professional", "nameCode": {"codeValue": "JobClass"}}
					],
					"dateFields": [
						{"dateValue": "2026-04-10T12:00Z", "nameCode": {"codeValue": "PostingDate"}}
					]
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:                  server.Client(),
		ADPWorkforceNowPageSize: 2,
		ADPWorkforceNowMaxPages: 1,
		ADPWorkforceNowMaxJobs:  5,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_adp_wfn",
		Name: "Acme Labs",
		URL:  server.URL + "/mascsr/default/mdf/recruitment/recruitment.html?cid=cid-123&ccId=cc-1&lang=en_US&type=JS&jobId=936700&jwId=jw-1",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "adp_workforcenow",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedDetails != 1 {
		t.Fatalf("requested details = %d, want 1", requestedDetails)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "adp_workforcenow:cid-123:936700" || job.Title != "Software Engineer II, Backend - Platform Team" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized direct ADP Workforce Now backend role", job)
	}
	if job.PostedAt == nil || job.PostedAt.Format("2006-01-02") != "2026-04-10" {
		t.Fatalf("posted_at = %v, want 2026-04-10 from minute-precision PostingDate", job.PostedAt)
	}
}

func TestADPWorkforceNowURLHelpers(t *testing.T) {
	boardURL := "https://workforcenow.adp.com/mascsr/default/mdf/recruitment/recruitment.html?cid=cid-123&ccId=19000101_000001&lang=en_US&type=JS"
	config, err := adpWorkforceNowConfigFromURL(boardURL)
	if err != nil {
		t.Fatal(err)
	}
	if config.CID != "cid-123" || config.CCID != "19000101_000001" || config.Lang != "en_US" || config.Locale != "en_US" {
		t.Fatalf("config = %#v, want cid/ccId/lang/locale from board URL", config)
	}

	directConfig, err := adpWorkforceNowConfigFromURL(boardURL + "&jobId=936700&jwId=jw-1")
	if err != nil {
		t.Fatal(err)
	}
	if directConfig.JobID != "936700" || directConfig.JWID != "jw-1" {
		t.Fatalf("direct config = %#v, want jobId and jwId", directConfig)
	}

	listURL, err := adpWorkforceNowListURL(boardURL, config, 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	if listURL.Path != "/mascsr/default/careercenter/public/events/staffing/v1/job-requisitions" {
		t.Fatalf("list path = %q, want ADP public job requisitions path", listURL.Path)
	}
	if listURL.Query().Get("cid") != "cid-123" || listURL.Query().Get("ccId") != "19000101_000001" || listURL.Query().Get("$top") != "10" || listURL.Query().Get("$skip") != "20" {
		t.Fatalf("list query = %q, want cid/ccId/$top/$skip", listURL.RawQuery)
	}

	detailURL, err := adpWorkforceNowDetailURL(boardURL, directConfig, "936700")
	if err != nil {
		t.Fatal(err)
	}
	if detailURL.Path != "/mascsr/default/careercenter/public/events/staffing/v1/job-requisitions/936700" || detailURL.Query().Get("cid") != "cid-123" {
		t.Fatalf("detail url = %q, want ADP detail endpoint", detailURL.String())
	}

	applyURL := adpWorkforceNowApplyURL(boardURL, directConfig, "936700", "jw-1")
	if !strings.Contains(applyURL, "/mascsr/default/mdf/recruitment/recruitment.html") || !strings.Contains(applyURL, "jobId=936700") || !strings.Contains(applyURL, "jwId=jw-1") {
		t.Fatalf("apply url = %q, want ADP hosted apply URL", applyURL)
	}
}

func TestATSExtractorExtractsADPMyJobsBoard(t *testing.T) {
	requestedConfig := 0
	requestedList := 0
	requestedDetails := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/public/staffing/v1/career-site/acmeexternal":
			requestedConfig++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"domain": "acmeexternal",
				"clientName": "Acme Labs",
				"myJobsToken": "token-123"
			}`))
		case "/myadp_prefix/mycareer/public/staffing/v1/job-requisitions":
			requestedList++
			if got := r.Header.Get("myJobsToken"); got != "token-123" {
				t.Fatalf("myJobsToken header = %q, want token-123", got)
			}
			if r.URL.Query().Get("$top") != "2" || r.URL.Query().Get("$skip") != "0" {
				t.Fatalf("list query = %q, want bounded first page", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"count": 1,
				"jobRequisitions": [{
					"jobTitle": "Software Engineer Intern, Backend Platform - Summer 2026",
					"reqId": "5001",
					"clientRequisitionID": "ENG-5001",
					"requisitionLocations": [{
						"address": {
							"cityName": "Singapore",
							"country": {"codeValue": "SG", "longName": "Singapore"}
						},
						"nameCode": {"longName": "Singapore"}
					}],
					"postingInstructions": [{
						"timestampLastPosted": "2026-06-20T10:00:00Z",
						"postingChannel": {"internetAddress": "https://recruiting.adp.com/srccsh/public/RTI.home?r=5001&c=115&d=ExternalCareerSite"}
					}]
				}]
			}`))
		case "/myadp_prefix/mycareer/public/staffing/v1/job-requisitions/search-meta/5001":
			requestedDetails++
			if got := r.Header.Get("myJobsToken"); got != "token-123" {
				t.Fatalf("detail myJobsToken header = %q, want token-123", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"jobRequisitions": [{
					"itemID": 5001,
					"requisitionTitle": "Software Engineer Intern, Backend Platform - Summer 2026",
					"reqId": "5001",
					"clientRequisitionID": "ENG-5001",
					"requisitionDescription": "<p>Build Go API workers, Redis queues, and matching fanout.</p>",
					"screeningRequirements": [{
						"requirementDescription": "<p>Internship for students graduating in 2026.</p>"
					}],
					"requisitionLocations": [{
						"address": {
							"cityName": "Singapore",
							"country": {"codeValue": "SG", "longName": "Singapore"}
						},
						"nameCode": {"longName": "Singapore"}
					}],
					"postingInstructions": [{
						"timestampLastPosted": "2026-06-20T10:00:00Z"
					}],
					"customFieldGroup": {
						"codeFields": [{
							"categoryCode": {"codeValue": "RTiReq_typeOfFulltime"},
							"longName": "Internship"
						}]
					}
				}]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:                 server.Client(),
		ADPMyJobsConfigBaseURL: server.URL + "/public/staffing/v1/career-site",
		ADPMyJobsAPIBaseURL:    server.URL + "/myadp_prefix/mycareer/public/staffing/v1/job-requisitions",
		ADPMyJobsPageSize:      2,
		ADPMyJobsMaxPages:      1,
		ADPMyJobsMaxJobs:       2,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_adp_myjobs",
		Name: "Acme Labs",
		URL:  server.URL + "/acmeexternal/cx/job-listing",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "adp_myjobs",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedConfig != 1 || requestedList != 1 || requestedDetails != 1 {
		t.Fatalf("requests config/list/detail = %d/%d/%d, want 1/1/1", requestedConfig, requestedList, requestedDetails)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 || len(result.Jobs) != 1 {
		t.Fatalf("result = %+v, want one high-confidence ADP MyJobs result", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "adp_myjobs:acmeexternal:5001" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "Singapore" {
		t.Fatalf("job = %#v, want normalized ADP MyJobs internship", job)
	}
	if job.Location != "Singapore" {
		t.Fatalf("location = %q, want Singapore", job.Location)
	}
	if job.ApplyURL != server.URL+"/acmeexternal/cx/job-details?lang=en-US&reqId=5001" {
		t.Fatalf("apply_url = %q, want MyJobs detail URL", job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-20" {
		t.Fatalf("posted_at = %v, want 2026-06-20", job.PostedAt)
	}
	if !strings.Contains(evidenceText(job.Evidence, "description"), "matching fanout") || !strings.Contains(evidenceText(job.Evidence, "requirements"), "graduating in 2026") {
		t.Fatalf("evidence = %#v, want description and requirements evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsADPMyJobsDirectDetailWhenListUnavailable(t *testing.T) {
	requestedList := 0
	requestedDetails := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/public/staffing/v1/career-site/acmeexternal":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"domain": "acmeexternal",
				"clientName": "Acme Labs",
				"myJobsToken": "token-456"
			}`))
		case "/myadp_prefix/mycareer/public/staffing/v1/job-requisitions":
			requestedList++
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		case "/myadp_prefix/mycareer/public/staffing/v1/job-requisitions/search-meta/5002":
			requestedDetails++
			if got := r.Header.Get("myJobsToken"); got != "token-456" {
				t.Fatalf("detail myJobsToken header = %q, want token-456", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"jobRequisitions": [{
					"itemID": 5002,
					"requisitionTitle": "Software Engineer II, Platform Services",
					"clientRequisitionID": "ENG-5002",
					"requisitionDescription": "<p>Own distributed backend APIs and observability pipelines.</p>",
					"requisitionLocations": [{
						"address": {
							"cityName": "Toronto",
							"countrySubdivisionLevel1": {"codeValue": "ON"},
							"country": {"codeValue": "CA", "longName": "Canada"}
						},
						"nameCode": {"longName": "Toronto"}
					}],
					"postingInstructions": [{
						"timestampLastPosted": "2026-06-19T09:00:00Z"
					}],
					"customFieldGroup": {
						"codeFields": [{
							"categoryCode": {"codeValue": "RTiReq_typeOfFulltime"},
							"longName": "Full-time"
						}]
					}
				}]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:                 server.Client(),
		ADPMyJobsConfigBaseURL: server.URL + "/public/staffing/v1/career-site",
		ADPMyJobsAPIBaseURL:    server.URL + "/myadp_prefix/mycareer/public/staffing/v1/job-requisitions",
		ADPMyJobsPageSize:      2,
		ADPMyJobsMaxPages:      1,
		ADPMyJobsMaxJobs:       2,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_adp_myjobs_detail",
		Name: "Acme Labs",
		URL:  server.URL + "/acmeexternal/cx/job-details?reqId=5002&lang=en-US",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "adp_myjobs",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedList != 1 || requestedDetails != 1 {
		t.Fatalf("requests list/detail = %d/%d, want 1/1", requestedList, requestedDetails)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want direct detail job", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "adp_myjobs:acmeexternal:5002" || job.Title != "Software Engineer II, Platform Services" || job.RoleFamily != "backend" || job.Country != "Canada" {
		t.Fatalf("job = %#v, want normalized direct ADP MyJobs backend role", job)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-19" {
		t.Fatalf("posted_at = %v, want 2026-06-19", job.PostedAt)
	}
}

func TestADPMyJobsURLHelpers(t *testing.T) {
	boardURL := "https://myjobs.adp.com/acmeexternal/cx/job-listing?lang=en-US"
	config, err := adpMyJobsConfigFromURL(boardURL)
	if err != nil {
		t.Fatal(err)
	}
	if config.Domain != "acmeexternal" || config.Lang != "en-US" || config.ReqID != "" {
		t.Fatalf("config = %#v, want domain/lang from board URL", config)
	}

	detailConfig, err := adpMyJobsConfigFromURL("https://myjobs.adp.com/acmeexternal/cx/job-details?reqId=5001&lang=en-US")
	if err != nil {
		t.Fatal(err)
	}
	if detailConfig.Domain != "acmeexternal" || detailConfig.ReqID != "5001" {
		t.Fatalf("detail config = %#v, want reqId", detailConfig)
	}

	listURL, err := adpMyJobsListURL("https://my.adp.com/myadp_prefix/mycareer/public/staffing/v1/job-requisitions", 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	if listURL.Path != "/myadp_prefix/mycareer/public/staffing/v1/job-requisitions" || listURL.Query().Get("$top") != "10" || listURL.Query().Get("$skip") != "20" {
		t.Fatalf("list url = %q, want bounded MyJobs list URL", listURL.String())
	}

	detailURL, err := adpMyJobsDetailAPIURL("https://my.adp.com/myadp_prefix/mycareer/public/staffing/v1/job-requisitions", "5001")
	if err != nil {
		t.Fatal(err)
	}
	if detailURL.Path != "/myadp_prefix/mycareer/public/staffing/v1/job-requisitions/search-meta/5001" {
		t.Fatalf("detail url = %q, want MyJobs search-meta detail URL", detailURL.String())
	}

	applyURL := adpMyJobsApplyURL(boardURL, config, "5001")
	if applyURL != "https://myjobs.adp.com/acmeexternal/cx/job-details?lang=en-US&reqId=5001" {
		t.Fatalf("apply url = %q, want MyJobs detail URL", applyURL)
	}
}

func TestATSExtractorExtractsUKGProJobsFromHydratedJobBoard(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ACM1000/JobBoard/board-id" {
			t.Fatalf("path = %q, want UKG job board path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
			<html>
				<body>
					<a class="navbar-brand"><img alt="Acme Labs" /></a>
					<script>
						var opportunityModel = new US.Opportunity.OpportunitiesViewModel({
							opportunities: [{
								"Id": "opp-1",
								"Featured": true,
								"Title": "Software Engineer Intern, Backend Platform - Summer 2026",
								"RequisitionNumber": "SOFT001",
								"FullTime": false,
								"JobCategoryName": "Internships",
								"Locations": [{
									"LocalizedName": "New York HQ",
									"LocalizedDescription": "Manhattan",
									"Address": {
										"City": "New York",
										"PostalCode": "10001",
										"State": {"Code": "NY", "Name": "New York"},
										"Country": {"Code": "USA", "Name": "United States"}
									}
								}],
								"PostedDate": "2026-06-05T17:44:55.768Z",
								"BriefDescription": "Build Go services for crawler workers and matching fanout."
							}, {
								"Id": "opp-2",
								"Title": "Sales Associate",
								"RequisitionNumber": "SALES001",
								"FullTime": true,
								"JobCategoryName": "Sales",
								"Locations": [],
								"PostedDate": "2026-06-01T12:00:00.000Z",
								"BriefDescription": "Sell enterprise software."
							}],
							pageSize: 50,
							opportunityLinkUrl: "/ACM1000/JobBoard/board-id/OpportunityDetail?opportunityId=00000000-0000-0000-0000-000000000000",
							jobBoard: {"Id": "board-id", "Name": "Engineering roles"}
						});
					</script>
				</body>
			</html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), UKGMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_ukg",
		Name: "Acme Labs",
		URL:  server.URL + "/ACM1000/JobBoard/board-id",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "ukg",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.82 {
		t.Fatalf("result = %+v, want high-confidence UKG result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "ukg:acm1000:opp-1" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized UKG internship", job)
	}
	if job.Location != "New York, NY, US" {
		t.Fatalf("location = %q, want New York, NY, US", job.Location)
	}
	if job.PostedAt == nil || job.PostedAt.Format("2006-01-02") != "2026-06-05" {
		t.Fatalf("posted_at = %v, want 2026-06-05", job.PostedAt)
	}
	if !strings.Contains(job.ApplyURL, "/ACM1000/JobBoard/board-id/OpportunityDetail") || !strings.Contains(job.ApplyURL, "opportunityId=opp-1") {
		t.Fatalf("apply_url = %q, want UKG opportunity detail URL", job.ApplyURL)
	}
	foundDescription := false
	foundRequisition := false
	for _, evidence := range job.Evidence {
		if evidence.Field == "description" && strings.Contains(evidence.Text, "crawler workers") {
			foundDescription = true
		}
		if evidence.Field == "requisition_number" && evidence.Text == "SOFT001" {
			foundRequisition = true
		}
	}
	if !foundDescription || !foundRequisition {
		t.Fatalf("evidence = %#v, want description and requisition evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsUKGProJobsFromLoadSearchResults(t *testing.T) {
	loadCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ACM1000/JobBoard/board-id":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html>
			<html>
				<body>
					<script>
						var opportunityModel = new US.Opportunity.OpportunitiesViewModel({
							opportunities: [],
							pageSize: 2,
							loadUrl: "/ACM1000/JobBoard/board-id/JobBoardView/LoadSearchResults",
							opportunityLinkUrl: "/ACM1000/JobBoard/board-id/OpportunityDetail?opportunityId=00000000-0000-0000-0000-000000000000",
							jobBoard: {"Id": "board-id", "Name": "Engineering roles"}
						});
					</script>
				</body>
			</html>`))
		case "/ACM1000/JobBoard/board-id/JobBoardView/LoadSearchResults":
			loadCalls++
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			var req struct {
				OpportunitySearch struct {
					QueryString    string `json:"QueryString"`
					Top            int    `json:"Top"`
					Skip           int    `json:"Skip"`
					LocationIDs    []any  `json:"LocationIds"`
					JobCategoryIDs []any  `json:"JobCategoryIds"`
					OrderBy        []struct {
						Value        string `json:"Value"`
						PropertyName string `json:"PropertyName"`
						Ascending    bool   `json:"Ascending"`
					} `json:"OrderBy"`
				} `json:"opportunitySearch"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if req.OpportunitySearch.Top != 2 || req.OpportunitySearch.Skip != 0 || req.OpportunitySearch.QueryString != "" {
				t.Fatalf("opportunitySearch = %#v, want bounded first page", req.OpportunitySearch)
			}
			if len(req.OpportunitySearch.LocationIDs) != 0 || len(req.OpportunitySearch.JobCategoryIDs) != 0 {
				t.Fatalf("opportunitySearch filters = %#v/%#v, want unfiltered board crawl", req.OpportunitySearch.LocationIDs, req.OpportunitySearch.JobCategoryIDs)
			}
			if len(req.OpportunitySearch.OrderBy) != 1 || req.OpportunitySearch.OrderBy[0].PropertyName != "PostedDate" || req.OpportunitySearch.OrderBy[0].Ascending {
				t.Fatalf("orderBy = %#v, want posted date descending", req.OpportunitySearch.OrderBy)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"opportunities": [{
					"Id": "opp-load-1",
					"Featured": false,
					"Title": "Software Engineer New Grad, Backend Platform",
					"RequisitionNumber": "SWE-NG-2026",
					"FullTime": true,
					"JobCategoryName": "Engineering",
					"Locations": [{
						"LocalizedName": "Vancouver Engineering",
						"LocalizedDescription": "Downtown Vancouver",
						"Address": {
							"City": "Vancouver",
							"State": {"Code": "BC", "Name": "British Columbia"},
							"Country": {"Code": "CA", "Name": "Canada"}
						}
					}],
					"PostedDate": "2026-06-12T15:00:00.000Z",
					"BriefDescription": "Build Go services for crawler workers and backend platform workflows."
				}],
				"totalCount": 1,
				"locations": []
			}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), UKGMaxJobs: 2})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_ukg_load",
		Name: "Acme Labs",
		URL:  server.URL + "/ACM1000/JobBoard/board-id",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "ukg",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if loadCalls != 1 {
		t.Fatalf("load calls = %d, want one load-search request", loadCalls)
	}
	if result.Strategy != TierATS || result.Confidence < 0.82 || len(result.Jobs) != 1 {
		t.Fatalf("result = %+v, want high-confidence UKG load-search result", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "ukg:acm1000:opp-load-1" || job.Company != "Acme Labs" || job.Level != "new_grad" || job.RoleFamily != "backend" || job.Country != "Canada" {
		t.Fatalf("job = %#v, want normalized UKG load-search new grad", job)
	}
	if job.Location != "Vancouver, BC, Canada" {
		t.Fatalf("location = %q, want Vancouver, BC, Canada", job.Location)
	}
	if !strings.Contains(job.ApplyURL, "/ACM1000/JobBoard/board-id/OpportunityDetail") || !strings.Contains(job.ApplyURL, "opportunityId=opp-load-1") {
		t.Fatalf("apply_url = %q, want UKG opportunity detail URL", job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-12" {
		t.Fatalf("posted_at = %v, want parsed UKG load-search date", job.PostedAt)
	}
	if !strings.Contains(evidenceText(job.Evidence, "ats"), "load search") {
		t.Fatalf("ats evidence = %q, want load-search evidence", evidenceText(job.Evidence, "ats"))
	}
	if !strings.Contains(evidenceText(job.Evidence, "description"), "crawler workers") {
		t.Fatalf("description evidence = %q, want load-search description", evidenceText(job.Evidence, "description"))
	}
}

func TestUKGProURLHelpers(t *testing.T) {
	boardURL := "https://recruiting.ultipro.com/ACM1000/JobBoard/86df2700-c124-49b9-b096-7cacea55e9dd"
	config, err := ukgProConfigFromURL(boardURL)
	if err != nil {
		t.Fatal(err)
	}
	if config.Account != "ACM1000" || config.BoardID != "86df2700-c124-49b9-b096-7cacea55e9dd" {
		t.Fatalf("config = %#v, want account and board id", config)
	}
	detailURL := ukgProOpportunityDetailURL(boardURL, "/ACM1000/JobBoard/86df2700-c124-49b9-b096-7cacea55e9dd/OpportunityDetail?opportunityId=00000000-0000-0000-0000-000000000000", "opp-1")
	if detailURL != "https://recruiting.ultipro.com/ACM1000/JobBoard/86df2700-c124-49b9-b096-7cacea55e9dd/OpportunityDetail?opportunityId=opp-1" {
		t.Fatalf("detail url = %q, want hosted UKG detail URL", detailURL)
	}
}

func TestATSExtractorExtractsDayforceJobDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/en-US/acme/CANDIDATEPORTAL/jobs/2429" {
			t.Fatalf("path = %q, want Dayforce detail path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
			<html>
				<body>
					<script id="__NEXT_DATA__" type="application/json">{
						"props": {
							"pageProps": {
								"jobData": {
									"jobPostingId": 2429,
									"jobReqId": 959,
									"jobTitle": "Software Engineer Intern, Backend Platform - Summer 2026",
									"postingStartTimestampUTC": "2026-06-08T08:00:00+00:00",
									"createdTimestampUTC": "2026-06-07T18:30:00+00:00",
									"isoCurrencyRegion": "USD",
									"jobPostingContent": {
										"jobDescriptionHeader": "<p>Join Acme Labs.</p>",
										"jobDescription": "<p>Build distributed Go services for crawler workers and matching fanout.</p>",
										"jobDescriptionFooter": "<p>Equal opportunity employer.</p>"
									},
									"postingLocations": [{
										"formattedAddress": "New York, New York, United States of America",
										"isoCountryCode": "US",
										"stateCode": "NY",
										"cityName": "New York"
									}],
									"jobPostingAttributes": [
										{"name": "JobFunction", "value": "Engineering", "type": "string"},
										{"name": "PayType", "value": "Hourly", "type": "string"},
										{"name": "HiringMinRate", "value": 30, "type": "currency"},
										{"name": "HiringMaxRate", "value": 40, "type": "currency"}
									],
									"postingStatus": 1
								}
							}
						},
						"query": {
							"clientNamespace": "acme",
							"careerSiteXRefCode": "CANDIDATEPORTAL",
							"id": "2429"
						}
					}</script>
				</body>
			</html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_dayforce",
		Name: "Acme Labs",
		URL:  server.URL + "/en-US/acme/CANDIDATEPORTAL/jobs/2429",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "dayforce",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 {
		t.Fatalf("result = %+v, want high-confidence Dayforce result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "dayforce:acme:candidateportal:2429" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Dayforce internship", job)
	}
	if job.Location != "New York, NY, US" {
		t.Fatalf("location = %q, want New York, NY, US", job.Location)
	}
	if job.PostedAt == nil || job.PostedAt.Format("2006-01-02") != "2026-06-08" {
		t.Fatalf("posted_at = %v, want 2026-06-08", job.PostedAt)
	}
	if job.ApplyURL != server.URL+"/en-US/acme/CANDIDATEPORTAL/jobs/2429" {
		t.Fatalf("apply_url = %q, want source detail URL", job.ApplyURL)
	}
	foundDescription := false
	foundComp := false
	for _, evidence := range job.Evidence {
		if evidence.Field == "description" && strings.Contains(evidence.Text, "crawler workers") {
			foundDescription = true
		}
		if evidence.Field == "compensation" && strings.Contains(evidence.Text, "30") && strings.Contains(evidence.Text, "40") {
			foundComp = true
		}
	}
	if !foundDescription || !foundComp {
		t.Fatalf("evidence = %#v, want description and compensation evidence", job.Evidence)
	}
}

func TestATSExtractorExtractsDayforceBoardSearchJobs(t *testing.T) {
	var gotRequest dayforceSearchRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/geo/acme/jobposting/search" {
			t.Fatalf("path = %q, want Dayforce search path", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(dayforceSearchResponse{
			Count:    2,
			MaxCount: 2,
			JobPostings: []dayforceJobData{
				{
					JobPostingID:             3001,
					JobReqID:                 4401,
					JobTitle:                 "New Grad Software Engineer, Data Platform",
					JobDescription:           "<p>Build Go services, APIs, and data workflows for early-career engineers.</p>",
					PostingStartTimestampUTC: "2026-06-10T12:00:00+00:00",
					ISOCurrencyRegion:        "USD",
					PostingLocations: []dayforcePostingLocation{{
						CityName:       "San Francisco",
						StateCode:      "CA",
						ISOCountryCode: "US",
					}},
					JobPostingAttributes: []dayforcePostingAttribute{
						{Name: "JobFunction", Value: json.RawMessage(`"Engineering"`), Type: "string"},
						{Name: "PayType", Value: json.RawMessage(`"Salary"`), Type: "string"},
					},
					PostingStatus: 1,
				},
				{
					JobPostingID:             3002,
					JobTitle:                 "Software Engineer Intern, AI Systems",
					ShortDescription:         "Work on agent evaluation systems.",
					PostingStartTimestampUTC: "2026-06-11T12:00:00+00:00",
					PostingLocations: []dayforcePostingLocation{{
						FormattedAddress: "Singapore",
						ISOCountryCode:   "SG",
					}},
					PostingStatus: 1,
				},
			},
		})
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:           server.Client(),
		DayforcePageSize: 2,
		DayforceMaxPages: 1,
		DayforceMaxJobs:  1,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_dayforce_board",
		Name: "Acme Labs",
		URL:  server.URL + "/en-US/acme/CANDIDATEPORTAL",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "dayforce",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotRequest.ClientNamespace != "acme" || gotRequest.JobBoardCode != "CANDIDATEPORTAL" || gotRequest.CultureCode != "en-US" || gotRequest.PaginationStart != 0 || gotRequest.PageSize != 2 {
		t.Fatalf("request = %#v, want Dayforce board search payload", gotRequest)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 {
		t.Fatalf("result = %+v, want high-confidence Dayforce board result", result)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want max-capped 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "dayforce:acme:candidateportal:3001" || job.Company != "Acme Labs" || job.Level != "new_grad" || job.RoleFamily != "data" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Dayforce board new-grad job", job)
	}
	if job.Location != "San Francisco, CA, US" {
		t.Fatalf("location = %q, want San Francisco, CA, US", job.Location)
	}
	if job.ApplyURL != server.URL+"/en-US/acme/CANDIDATEPORTAL/jobs/3001" {
		t.Fatalf("apply_url = %q, want Dayforce hosted detail URL", job.ApplyURL)
	}
	foundDescription := false
	for _, evidence := range job.Evidence {
		if evidence.Field == "description" && strings.Contains(evidence.Text, "data workflows") {
			foundDescription = true
		}
	}
	if !foundDescription {
		t.Fatalf("evidence = %#v, want root jobDescription evidence", job.Evidence)
	}
}

func TestDayforceURLHelpers(t *testing.T) {
	detailURL := "https://jobs.dayforcehcm.com/en-US/acme/CANDIDATEPORTAL/jobs/2429"
	config, err := dayforceConfigFromURL(detailURL)
	if err != nil {
		t.Fatal(err)
	}
	if config.Culture != "en-US" || config.ClientNamespace != "acme" || config.JobBoardCode != "CANDIDATEPORTAL" || config.JobID != "2429" {
		t.Fatalf("config = %#v, want culture/client/board/job id", config)
	}
	boardURL := "https://jobs.dayforcehcm.com/hok/candidateportal"
	boardConfig, err := dayforceConfigFromURL(boardURL)
	if err != nil {
		t.Fatal(err)
	}
	if boardConfig.Culture != "en-US" || boardConfig.ClientNamespace != "hok" || boardConfig.JobBoardCode != "candidateportal" || boardConfig.JobID != "" {
		t.Fatalf("board config = %#v, want default culture and no job id", boardConfig)
	}
	if got := dayforceHostedJobURL(detailURL, config, "2429"); got != detailURL {
		t.Fatalf("hosted url = %q, want original detail URL", got)
	}
	endpoint, err := dayforceSearchEndpoint(boardURL, boardConfig)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.String() != "https://jobs.dayforcehcm.com/api/geo/hok/jobposting/search" {
		t.Fatalf("search endpoint = %q, want Dayforce geo search endpoint", endpoint.String())
	}
}

func TestATSExtractorExtractsOracleRecruitingBoard(t *testing.T) {
	var searchCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hcmRestApi/resources/latest/recruitingCEJobRequisitions":
			searchCalls++
			finder := r.URL.Query().Get("finder")
			if !strings.Contains(finder, "siteNumber=CX_1") || !strings.Contains(finder, "limit=2") || !strings.Contains(finder, "offset=0") {
				t.Fatalf("finder = %q, want CX_1 pagination", finder)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"items": [{
					"TotalJobsCount": 2,
					"SiteNumber": "CX_1",
					"requisitionList": [{
						"Id": "47611",
						"Title": "Principal Software Engineer",
						"PostedDate": "2026-06-22",
						"PostingEndDate": null,
						"PrimaryLocationCountry": "US",
						"PrimaryLocation": "United States",
						"ShortDescriptionStr": "Build core platform services.",
						"JobFunction": "Engineering",
						"WorkerType": "Employee",
						"JobSchedule": "Full time",
						"WorkplaceType": "Remote"
					}, {
						"Id": "47612",
						"Title": "Backend Software Engineer Intern",
						"PostedDate": "2026-06-20",
						"PrimaryLocationCountry": "SG",
						"PrimaryLocation": "Singapore",
						"ShortDescriptionStr": "Internship on crawler queues.",
						"JobFunction": "Engineering",
						"WorkerType": "Intern",
						"JobSchedule": "Full time"
					}]
				}],
				"count": 1,
				"hasMore": false,
				"limit": 200,
				"offset": 0
			}`))
		case "/hcmUI/CandidateExperience/en/sites/CX_1/job/47611":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head>
				<meta property="og:title" content="Principal Software Engineer"/>
				<meta property="og:description" content="Own distributed systems for resilient job discovery."/>
				<meta property="og:site_name" content="Acme Careers"/>
			</head></html>`))
		case "/hcmUI/CandidateExperience/en/sites/CX_1/job/47612":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head>
				<meta property="og:title" content="Backend Software Engineer Intern"/>
				<meta property="og:description" content="Internship building Redis-backed crawler workers."/>
			</head></html>`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:                   server.Client(),
		OracleRecruitingPageSize: 2,
		OracleRecruitingMaxPages: 1,
		OracleRecruitingMaxJobs:  5,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_oracle",
		Name: "Acme Labs",
		URL:  server.URL + "/hcmUI/CandidateExperience/en/sites/CX_1",
		Metadata: map[string]string{
			"source_kind": "oracle_recruiting",
		},
	})
	if err != nil {
		t.Fatalf("extract oracle recruiting: %v", err)
	}
	if searchCalls != 1 {
		t.Fatalf("search calls = %d, want 1", searchCalls)
	}
	if result.Strategy != TierATS || result.Confidence < 0.85 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two high-confidence Oracle Recruiting jobs", result)
	}

	job := result.Jobs[0]
	if job.SourceJobID != "oracle_recruiting:cx-1:47611" || job.Company != "Acme Labs" || job.RoleFamily != "software_engineering" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Oracle Recruiting posting", job)
	}
	if job.ApplyURL != server.URL+"/hcmUI/CandidateExperience/en/sites/CX_1/job/47611" {
		t.Fatalf("apply url = %q, want Oracle job detail URL", job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format("2006-01-02") != "2026-06-22" {
		t.Fatalf("posted_at = %v, want 2026-06-22", job.PostedAt)
	}
	if evidenceText(job.Evidence, "description") != "Own distributed systems for resilient job discovery." {
		t.Fatalf("description evidence = %q, want detail-page description", evidenceText(job.Evidence, "description"))
	}

	intern := result.Jobs[1]
	if intern.Level != "internship" || intern.EmploymentType != "internship" || intern.Country != "Singapore" {
		t.Fatalf("intern = %#v, want internship normalized from worker type and title", intern)
	}
}

func TestOracleRecruitingURLHelpers(t *testing.T) {
	boardURL := "https://hcgn.fa.us2.oraclecloud.com/hcmUI/CandidateExperience/en/sites/CX_1"
	config, err := oracleRecruitingConfigFromURL(boardURL)
	if err != nil {
		t.Fatalf("config from board: %v", err)
	}
	if config.Culture != "en" || config.SiteNumber != "CX_1" || config.JobID != "" {
		t.Fatalf("config = %#v, want CX_1 board config", config)
	}

	detailURL := "https://hcgn.fa.us2.oraclecloud.com/hcmUI/CandidateExperience/en/sites/CX_1/job/47611"
	detailConfig, err := oracleRecruitingConfigFromURL(detailURL)
	if err != nil {
		t.Fatalf("config from detail: %v", err)
	}
	if detailConfig.Culture != "en" || detailConfig.SiteNumber != "CX_1" || detailConfig.JobID != "47611" {
		t.Fatalf("detail config = %#v, want job detail config", detailConfig)
	}
	if got := oracleRecruitingJobURL(boardURL, config, "47611"); got != detailURL {
		t.Fatalf("job url = %q, want %q", got, detailURL)
	}
}

func TestATSExtractorExtractsPaylocityHostedJobs(t *testing.T) {
	var detailCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/recruiting/jobs/All/feed-guid/Acme-Labs":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><body>
				<script>
					window.pageData = {
						"ModuleTitle": "Acme Labs",
						"Jobs": [{
							"JobId": 1001,
							"JobTitle": "Backend Software Engineer Intern",
							"PublishedDate": "2026-06-21T09:30:00-05:00",
							"Description": "",
							"IsInternal": false,
							"HiringDepartment": "Engineering",
							"JobLocation": {
								"Name": "New York",
								"City": "New York",
								"State": "NY",
								"Country": "USA"
							},
							"IsRemote": false
						}, {
							"JobId": 1002,
							"JobTitle": "Internal Payroll Specialist",
							"PublishedDate": "2026-06-20T09:30:00-05:00",
							"IsInternal": true
						}]
					};
				</script>
			</body></html>`))
		case "/recruiting/jobs/Details/1001/Acme-Labs/Backend-Software-Engineer-Intern":
			detailCalls++
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><script type="application/ld+json">{
				"@context": "https://schema.org",
				"@type": "JobPosting",
				"title": "Backend Software Engineer Intern",
				"description": "<p>Build Go services for crawler queues and matching fanout.</p>",
				"datePosted": "2026-06-21T09:30:00-05:00",
				"employmentType": "INTERN",
				"hiringOrganization": {"name": "Acme Labs"},
				"jobLocation": {"@type": "Place", "address": {"addressLocality": "New York", "addressRegion": "NY", "addressCountry": "US"}},
				"baseSalary": {"@type": "MonetaryAmount", "currency": "USD", "value": {"@type": "QuantitativeValue", "minValue": 30, "maxValue": 45, "unitText": "HOUR"}}
			}</script>`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), PaylocityMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_paylocity",
		Name: "Acme Labs",
		URL:  server.URL + "/recruiting/jobs/All/feed-guid/Acme-Labs",
		Metadata: map[string]string{
			"source_kind": "paylocity",
		},
	})
	if err != nil {
		t.Fatalf("extract paylocity: %v", err)
	}
	if detailCalls != 1 {
		t.Fatalf("detail calls = %d, want one external detail fetch", detailCalls)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 || len(result.Jobs) != 1 {
		t.Fatalf("result = %+v, want one high-confidence Paylocity job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "paylocity:feed-guid:1001" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Paylocity internship", job)
	}
	if job.ApplyURL != server.URL+"/recruiting/jobs/Apply/1001/Acme-Labs/Backend-Software-Engineer-Intern" {
		t.Fatalf("apply url = %q, want hosted Paylocity apply URL", job.ApplyURL)
	}
	if !strings.Contains(evidenceText(job.Evidence, "description"), "crawler queues") {
		t.Fatalf("description evidence = %q, want detail JSON-LD description", evidenceText(job.Evidence, "description"))
	}
	if !strings.Contains(evidenceText(job.Evidence, "compensation"), "30") || !strings.Contains(evidenceText(job.Evidence, "compensation"), "45") {
		t.Fatalf("compensation evidence = %q, want JSON-LD salary range", evidenceText(job.Evidence, "compensation"))
	}
}

func TestATSExtractorExtractsJibeJobPostingJSONLD(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acme/jobs" {
			t.Fatalf("path = %q, want Jibe jobs page", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
			<script type="application/ld+json">[
				{
					"@context": "https://schema.org",
					"@type": "JobPosting",
					"identifier": {"value": "JR-1001"},
					"title": "Backend Software Engineer Intern",
					"description": "<p>Build Go worker systems for source discovery.</p>",
					"datePosted": "2026-06-22",
					"employmentType": "INTERN",
					"hiringOrganization": {"name": "Acme Labs"},
					"url": "/acme/jobs/backend-software-engineer-intern-JR-1001",
					"jobLocation": {"@type": "Place", "address": {"addressLocality": "San Francisco", "addressRegion": "CA", "addressCountry": "US"}}
				},
				{
					"@context": "https://schema.org",
					"@type": "JobPosting",
					"identifier": {"value": "JR-1002"},
					"title": "Product Manager",
					"description": "<p>Second posting should be capped by JibeMaxJobs.</p>",
					"hiringOrganization": {"name": "Acme Labs"},
					"url": "/acme/jobs/product-manager-JR-1002"
				}
			]</script>
		</body></html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), JibeMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_jibe",
		Name: "Acme Labs",
		URL:  server.URL + "/acme/jobs",
		Metadata: map[string]string{
			"source_kind": "jibe",
		},
	})
	if err != nil {
		t.Fatalf("extract jibe: %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.82 || len(result.Jobs) != 1 {
		t.Fatalf("result = %+v, want one bounded Jibe ATS job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "jibe:JR-1001" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Jibe backend internship", job)
	}
	if job.ApplyURL != server.URL+"/acme/jobs/backend-software-engineer-intern-JR-1001" {
		t.Fatalf("apply url = %q, want resolved Jibe apply URL", job.ApplyURL)
	}
	if evidenceText(job.Evidence, "ats") != "Jibe/Radancy hosted JobPosting JSON-LD" {
		t.Fatalf("ats evidence = %q, want Jibe JSON-LD evidence", evidenceText(job.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsJobScoreJobPostingJSONLD(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/careers/acme-labs" {
			t.Fatalf("path = %q, want JobScore careers page", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
			<script type="application/ld+json">[
				{
					"@context": "https://schema.org",
					"@type": "JobPosting",
					"identifier": {"value": "JS-1001"},
					"title": "Backend Software Engineer Intern",
					"description": "<p>Build Go services for extraction queues and matching.</p>",
					"datePosted": "2026-06-22",
					"employmentType": "INTERN",
					"hiringOrganization": {"name": "Acme Labs"},
					"url": "/careers/acme-labs/jobs/backend-software-engineer-intern-JS-1001",
					"jobLocation": {"@type": "Place", "address": {"addressLocality": "New York", "addressRegion": "NY", "addressCountry": "US"}}
				},
				{
					"@context": "https://schema.org",
					"@type": "JobPosting",
					"identifier": {"value": "JS-1002"},
					"title": "Product Manager",
					"description": "<p>Second posting should be capped by JobScoreMaxJobs.</p>",
					"hiringOrganization": {"name": "Acme Labs"},
					"url": "/careers/acme-labs/jobs/product-manager-JS-1002"
				}
			]</script>
		</body></html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), JobScoreMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_jobscore",
		Name: "Acme Labs",
		URL:  server.URL + "/careers/acme-labs",
		Metadata: map[string]string{
			"source_kind": "jobscore",
		},
	})
	if err != nil {
		t.Fatalf("extract jobscore: %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.82 || len(result.Jobs) != 1 {
		t.Fatalf("result = %+v, want one bounded JobScore ATS job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "jobscore:JS-1001" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized JobScore backend internship", job)
	}
	if job.ApplyURL != server.URL+"/careers/acme-labs/jobs/backend-software-engineer-intern-JS-1001" {
		t.Fatalf("apply url = %q, want resolved JobScore apply URL", job.ApplyURL)
	}
	if evidenceText(job.Evidence, "ats") != "JobScore hosted JobPosting JSON-LD" {
		t.Fatalf("ats evidence = %q, want JobScore JSON-LD evidence", evidenceText(job.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsTalentBrewJobPostingJSONLD(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Fatalf("path = %q, want TalentBrew search page", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
			<script type="application/ld+json">[
				{
					"@context": "https://schema.org",
					"@type": "JobPosting",
					"identifier": {"value": "TB-1001"},
					"title": "Backend Software Engineer Intern",
					"description": "<p>Build Go services for source scheduling and matching fanout.</p>",
					"datePosted": "2026-06-23",
					"employmentType": "INTERN",
					"hiringOrganization": {"name": "Acme Labs"},
					"url": "/job/new-york/backend-software-engineer-intern/TB-1001",
					"jobLocation": {"@type": "Place", "address": {"addressLocality": "New York", "addressRegion": "NY", "addressCountry": "US"}}
				},
				{
					"@context": "https://schema.org",
					"@type": "JobPosting",
					"identifier": {"value": "TB-1002"},
					"title": "Product Manager",
					"description": "<p>Second posting should be capped by TalentBrewMaxJobs.</p>",
					"hiringOrganization": {"name": "Acme Labs"},
					"url": "/job/new-york/product-manager/TB-1002"
				}
			]</script>
		</body></html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), TalentBrewMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_talentbrew",
		Name: "Acme Labs",
		URL:  server.URL + "/search",
		Metadata: map[string]string{
			"source_kind": "talentbrew",
		},
	})
	if err != nil {
		t.Fatalf("extract talentbrew: %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.82 || len(result.Jobs) != 1 {
		t.Fatalf("result = %+v, want one bounded TalentBrew ATS job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "talentbrew:TB-1001" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized TalentBrew backend internship", job)
	}
	if job.ApplyURL != server.URL+"/job/new-york/backend-software-engineer-intern/TB-1001" {
		t.Fatalf("apply url = %q, want resolved TalentBrew apply URL", job.ApplyURL)
	}
	if evidenceText(job.Evidence, "ats") != "TalentBrew hosted JobPosting JSON-LD" {
		t.Fatalf("ats evidence = %q, want TalentBrew JSON-LD evidence", evidenceText(job.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsPaylocityV2FeedJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/recruiting/v2/api/feed/jobs/feed-guid" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"displayName": "Acme Labs",
			"jobs": [{
				"jobId": 2001,
				"title": "Software Engineer New Grad, AI Platform",
				"companyName": "Acme Labs",
				"applyUrl": "https://recruiting.paylocity.com/recruiting/Jobs/Apply/2001/Acme-Labs/Software-Engineer-New-Grad-AI-Platform",
				"publishedDate": "2026-06-22T14:10:00.000",
				"description": "<p>Build AI ranking systems for internship discovery.</p>",
				"displayUrl": "https://recruiting.paylocity.com/recruiting/Jobs/Details/2001/Acme-Labs/Software-Engineer-New-Grad-AI-Platform",
				"jobLocation": {
					"name": "San Francisco HQ",
					"city": "San Francisco",
					"state": "CA",
					"locationDisplayName": "San Francisco HQ"
				},
				"salaryDescription": "$140,000",
				"hiringDepartment": "Engineering",
				"jobTypesArray": ["Full-time"]
			}]
		}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), PaylocityMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_paylocity_feed",
		Name: "Acme Labs",
		URL:  server.URL + "/recruiting/v2/api/feed/jobs/feed-guid",
		Metadata: map[string]string{
			"source_kind": "paylocity",
		},
	})
	if err != nil {
		t.Fatalf("extract paylocity v2 feed: %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 || len(result.Jobs) != 1 {
		t.Fatalf("result = %+v, want one high-confidence Paylocity feed job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "paylocity:feed-guid:2001" || job.Company != "Acme Labs" || job.Level != "new_grad" || job.RoleFamily != "ml_ai" {
		t.Fatalf("job = %#v, want normalized Paylocity feed new-grad AI role", job)
	}
	if job.ApplyURL != "https://recruiting.paylocity.com/recruiting/Jobs/Apply/2001/Acme-Labs/Software-Engineer-New-Grad-AI-Platform" {
		t.Fatalf("apply url = %q, want feed applyUrl", job.ApplyURL)
	}
	if !strings.Contains(evidenceText(job.Evidence, "description"), "AI ranking systems") {
		t.Fatalf("description evidence = %q, want feed description", evidenceText(job.Evidence, "description"))
	}
	if !strings.Contains(evidenceText(job.Evidence, "compensation"), "$140,000") {
		t.Fatalf("compensation evidence = %q, want feed salaryDescription", evidenceText(job.Evidence, "compensation"))
	}
}

func TestATSExtractorExtractsPaylocityV1FeedJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/recruiting/api/feed/jobs/feed-guid" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{
			"JobId": 3001,
			"Title": "Backend Software Engineering Intern",
			"CompanyName": "Acme Labs",
			"ApplyUrl": "https://recruiting.paylocity.com/recruiting/Jobs/Apply/3001/Acme-Labs/Backend-Software-Engineering-Intern",
			"PublishedDate": "2026-06-20T12:00:00.000",
			"Description": "<p>Own Go services for source scheduling.</p>",
			"DisplayUrl": "https://recruiting.paylocity.com/recruiting/Jobs/Details/3001/Acme-Labs/Backend-Software-Engineering-Intern",
			"JobLocation": {
				"Name": "New York HQ",
				"City": "New York",
				"State": "NY",
				"LocationDisplayName": "New York HQ"
			},
			"SalaryDescription": "$35/hr",
			"HiringDepartment": "Platform"
		}]`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), PaylocityMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_paylocity_v1_feed",
		Name: "Acme Labs",
		URL:  server.URL + "/recruiting/api/feed/jobs/feed-guid",
		Metadata: map[string]string{
			"source_kind": "paylocity",
		},
	})
	if err != nil {
		t.Fatalf("extract paylocity v1 feed: %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want one v1 feed job", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "paylocity:feed-guid:3001" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Paylocity v1 internship", job)
	}
	if !strings.Contains(evidenceText(job.Evidence, "description"), "source scheduling") {
		t.Fatalf("description evidence = %q, want v1 feed description", evidenceText(job.Evidence, "description"))
	}
}

func TestATSExtractorExtractsPaylocityXMLFeedJobs(t *testing.T) {
	var acceptHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/recruiting/api/feed/jobs/feed-guid" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		acceptHeader = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<Jobs>
	<DisplayName>Acme Labs</DisplayName>
	<Job>
		<JobId>4001</JobId>
		<Title>Backend Infrastructure Software Engineer Intern</Title>
		<CompanyName>Acme Labs</CompanyName>
		<ApplyUrl>https://recruiting.paylocity.com/recruiting/Jobs/Apply/4001/Acme-Labs/Backend-Infrastructure-Software-Engineer-Intern</ApplyUrl>
		<DisplayUrl>https://recruiting.paylocity.com/recruiting/Jobs/Details/4001/Acme-Labs/Backend-Infrastructure-Software-Engineer-Intern</DisplayUrl>
		<PublishedDate>2026-06-23T09:00:00.000</PublishedDate>
		<Description><![CDATA[<p>Build Redis queues and crawler workers.</p>]]></Description>
		<Requirements>Go and distributed systems.</Requirements>
		<SalaryDescription>$40/hr</SalaryDescription>
		<HiringDepartment>Platform</HiringDepartment>
		<JobTypes>Internship</JobTypes>
		<JobLocation>
			<Name>Toronto HQ</Name>
			<City>Toronto</City>
			<State>ON</State>
			<Country>Canada</Country>
		</JobLocation>
	</Job>
</Jobs>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), PaylocityMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_paylocity_xml_feed",
		Name: "Acme Labs",
		URL:  server.URL + "/recruiting/api/feed/jobs/feed-guid",
		Metadata: map[string]string{
			"source_kind": "paylocity",
		},
	})
	if err != nil {
		t.Fatalf("extract paylocity xml feed: %v", err)
	}
	if !strings.Contains(acceptHeader, "application/xml") {
		t.Fatalf("accept header = %q, want xml feed support", acceptHeader)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 || len(result.Jobs) != 1 {
		t.Fatalf("result = %+v, want one high-confidence Paylocity XML feed job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "paylocity:feed-guid:4001" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "Canada" {
		t.Fatalf("job = %#v, want normalized Paylocity XML internship", job)
	}
	if job.Location != "Toronto, ON, Canada" {
		t.Fatalf("location = %q, want Toronto, ON, Canada", job.Location)
	}
	if job.ApplyURL != "https://recruiting.paylocity.com/recruiting/Jobs/Apply/4001/Acme-Labs/Backend-Infrastructure-Software-Engineer-Intern" {
		t.Fatalf("apply url = %q, want XML feed apply URL", job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-23" {
		t.Fatalf("posted_at = %v, want parsed XML feed date", job.PostedAt)
	}
	if !strings.Contains(evidenceText(job.Evidence, "description"), "crawler workers") {
		t.Fatalf("description evidence = %q, want XML feed description", evidenceText(job.Evidence, "description"))
	}
	if !strings.Contains(evidenceText(job.Evidence, "compensation"), "$40/hr") {
		t.Fatalf("compensation evidence = %q, want XML feed salaryDescription", evidenceText(job.Evidence, "compensation"))
	}
	if !strings.Contains(evidenceText(job.Evidence, "ats"), "XML") {
		t.Fatalf("ats evidence = %q, want XML feed evidence", evidenceText(job.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsPhenomPeopleSearchResults(t *testing.T) {
	var firstPageRequests int
	var secondPageRequests int
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/us/en/search-results" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Query().Get("from") {
		case "":
			firstPageRequests++
			_, _ = w.Write([]byte(phenomSearchPageHTML(serverURL+"/us/en", "ACME123", 3, []string{
				`{
					"title":"Software Engineer Intern, Backend Platform - Summer 2026",
					"jobSeqNo":"ACME123P1001EXTERNALENUS",
					"jobId":"P-1001",
					"reqId":"REQ-1001",
					"type":"Internship",
					"category":"Engineering",
					"department":"Platform",
					"descriptionTeaser":"Build Go services, Redis-backed queues, and crawler workers.",
					"postedDate":"2026-06-18T11:03:00.000+0000",
					"location":"Singapore",
					"country":"Singapore",
					"multi_location":["Singapore"],
					"ml_job_parser":{"descriptionTeaser_ats":"Internship for students graduating in 2026."}
				}`,
				`{
					"title":"Product Marketing Manager",
					"jobSeqNo":"ACME123P1002EXTERNALENUS",
					"jobId":"P-1002",
					"reqId":"REQ-1002",
					"type":"Full-Time",
					"category":"Marketing",
					"descriptionTeaser":"Run launches.",
					"postedDate":"2026-06-17T09:00:00.000+0000",
					"location":"New York, New York, United States",
					"country":"United States"
				}`,
			})))
		case "2":
			secondPageRequests++
			if r.URL.Query().Get("s") != "1" {
				t.Fatalf("s query = %q, want 1", r.URL.Query().Get("s"))
			}
			_, _ = w.Write([]byte(phenomSearchPageHTML(serverURL+"/us/en", "ACME123", 3, []string{
				`{
					"title":"New Grad Software Engineer, AI Platform",
					"jobSeqNo":"ACME123P1003EXTERNALENUS",
					"jobId":"P-1003",
					"reqId":"REQ-1003",
					"type":"Full-Time",
					"category":"Engineering",
					"department":"AI",
					"descriptionTeaser":"Build AI platform APIs and observability systems.",
					"dateCreated":"2026-06-20T12:00:00.000+0000",
					"location":"Toronto, Ontario, Canada",
					"country":"Canada"
				}`,
			})))
		default:
			t.Fatalf("unexpected from query %q", r.URL.Query().Get("from"))
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), PhenomPeopleMaxPages: 2, PhenomPeopleMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_phenom",
		Name: "Acme Labs",
		URL:  server.URL + "/us/en/search-results",
		Metadata: map[string]string{
			"source_kind": "phenom_people",
		},
	})
	if err != nil {
		t.Fatalf("extract phenom people: %v", err)
	}
	if firstPageRequests != 1 || secondPageRequests != 1 {
		t.Fatalf("page requests = first:%d second:%d, want one each", firstPageRequests, secondPageRequests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.84 || len(result.Jobs) != 3 {
		t.Fatalf("result = %+v, want three high-confidence Phenom jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "phenom_people:acme123:ACME123P1001EXTERNALENUS" || first.Company != "Acme Labs" || first.Level != "internship" || first.RoleFamily != "backend" || first.Country != "Singapore" {
		t.Fatalf("first job = %#v, want normalized Phenom internship", first)
	}
	if first.ApplyURL != server.URL+"/us/en/job/ACME123P1001EXTERNALENUS" {
		t.Fatalf("apply url = %q, want hosted Phenom detail URL", first.ApplyURL)
	}
	if first.PostedAt == nil || first.PostedAt.Format(time.DateOnly) != "2026-06-18" {
		t.Fatalf("posted_at = %v, want parsed Phenom date", first.PostedAt)
	}
	if !strings.Contains(evidenceText(first.Evidence, "description"), "crawler workers") {
		t.Fatalf("description evidence = %q, want crawler workers", evidenceText(first.Evidence, "description"))
	}
	if !strings.Contains(evidenceText(first.Evidence, "requirements"), "graduating in 2026") {
		t.Fatalf("requirements evidence = %q, want graduation evidence", evidenceText(first.Evidence, "requirements"))
	}
	third := result.Jobs[2]
	if third.SourceJobID != "phenom_people:acme123:ACME123P1003EXTERNALENUS" || third.Country != "Canada" || third.Level != "new_grad" {
		t.Fatalf("third job = %#v, want normalized new-grad Canada job", third)
	}
}

func TestATSExtractorRoutesSnowflakeCareersThroughPhenomPeople(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/us/en/search-results" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(phenomSearchPageHTML(serverURL+"/us/en", "SNCOUS", 1, []string{
			`{
				"title":"Software Engineer Intern, AI Platform",
				"jobSeqNo":"SNCOUSP1001EXTERNALENUS",
				"jobId":"P-1001",
				"reqId":"REQ-1001",
				"type":"Internship",
				"category":"Engineering",
				"department":"AI",
				"descriptionTeaser":"Build Snowflake data platform services with Go and Python.",
				"postedDate":"2026-06-18T11:03:00.000+0000",
				"location":"San Mateo, California, United States",
				"country":"United States"
			}`,
		})))
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), PhenomPeopleMaxPages: 1, PhenomPeopleMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_snowflake",
		Name: "Snowflake",
		URL:  server.URL + "/us/en/search-results?keywords=software%20engineer%20intern",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "snowflake_careers",
		},
	})
	if err != nil {
		t.Fatalf("extract snowflake careers: %v", err)
	}
	if result.Strategy != TierATS || len(result.Jobs) != 1 {
		t.Fatalf("result = %+v, want Snowflake Phenom job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "phenom_people:sncous:SNCOUSP1001EXTERNALENUS" || job.Company != "Snowflake" || job.Level != "internship" || job.Country != "US" {
		t.Fatalf("job = %#v, want Snowflake Phenom normalization", job)
	}
}

func TestATSExtractorExtractsAppleJobsSearchResults(t *testing.T) {
	var searchRequests int
	var detailRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/en-us/search" && !strings.HasPrefix(r.URL.Path, "/en-us/details/") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Path == "/en-us/search" && r.URL.Query().Get("search") != "software engineer intern" {
			t.Fatalf("search query = %q, want software engineer intern", r.URL.Query().Get("search"))
		}
		w.Header().Set("Content-Type", "text/html")
		if strings.HasPrefix(r.URL.Path, "/en-us/details/") {
			detailRequests++
			_, _ = w.Write([]byte(appleJobsSearchPageHTML("en-us", nil)))
			return
		}
		searchRequests++
		_, _ = w.Write([]byte(appleJobsSearchPageHTML("en-us", []string{
			`{
				"id":"200700001-0836",
				"positionId":"200700001",
				"reqId":"200700001-0836",
				"jobPositionId":"REQ-200700001",
				"postingTitle":"Backend Platform Software Engineer Intern - Summer 2026",
				"transformedPostingTitle":"backend-platform-software-engineer-intern-summer-2026",
				"postingDate":"Jun 23, 2026",
				"postDateInGMT":"2026-06-23T05:00:39.873Z",
				"jobSummary":"Build Go services, Redis queues, and observability for Apple cloud systems. Internship for students graduating in 2026.",
				"standardWeeklyHours":40,
				"type":"REQ",
				"team":{"teamName":"Software and Services","teamID":"teamsAndSubTeams-SFTWR","teamCode":"SFTWR"},
				"locations":[
					{"name":"Cupertino","countryName":"United States of America","countryID":"iso-country-USA","level":5},
					{"name":"Seattle","countryName":"United States of America","countryID":"iso-country-USA","level":5}
				],
				"postExternal":true
			}`,
			`{
				"id":"200700002-1536",
				"positionId":"200700002",
				"reqId":"200700002-1536",
				"jobPositionId":"REQ-200700002",
				"postingTitle":"AIML Software Engineer, Evaluation",
				"transformedPostingTitle":"aiml-software-engineer-evaluation",
				"postDateInGMT":"2026-06-22T15:05:55.498351783Z",
				"jobSummary":"Build evaluation systems for ML products and large language model quality.",
				"standardWeeklyHours":40,
				"type":"REQ",
				"team":{"teamName":"Machine Learning and AI","teamID":"teamsAndSubTeams-MLAI","teamCode":"MLAI"},
				"locations":[{"name":"Singapore","countryName":"Singapore","countryID":"iso-country-SGP","level":5}],
				"postExternal":true
			}`,
		})))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), AppleJobsMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_apple",
		Name: "Apple",
		URL:  server.URL + "/en-us/search?search=software+engineer+intern",
		Metadata: map[string]string{
			"source_kind": "apple_jobs",
		},
	})
	if err != nil {
		t.Fatalf("extract apple jobs: %v", err)
	}
	if searchRequests != 1 || detailRequests != 2 {
		t.Fatalf("requests = search %d detail %d, want 1/2", searchRequests, detailRequests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.86 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two high-confidence Apple jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "apple_jobs:apple:200700001-0836" || first.Company != "Apple" || first.Level != "internship" || first.RoleFamily != "backend" || first.Country != "US" {
		t.Fatalf("first job = %#v, want normalized Apple backend internship", first)
	}
	if first.Location != "Cupertino, US; Seattle, US" {
		t.Fatalf("location = %q, want Cupertino and Seattle", first.Location)
	}
	if first.ApplyURL != server.URL+"/en-us/details/200700001-0836/backend-platform-software-engineer-intern-summer-2026?team=SFTWR" {
		t.Fatalf("apply url = %q, want Apple detail URL", first.ApplyURL)
	}
	if first.PostedAt == nil || first.PostedAt.Format(time.DateOnly) != "2026-06-23" {
		t.Fatalf("posted_at = %v, want parsed Apple date", first.PostedAt)
	}
	if !strings.Contains(evidenceText(first.Evidence, "description"), "graduating in 2026") {
		t.Fatalf("description evidence = %q, want graduation evidence", evidenceText(first.Evidence, "description"))
	}
	if !strings.Contains(evidenceText(first.Evidence, "team"), "Software and Services") {
		t.Fatalf("team evidence = %q, want Apple team", evidenceText(first.Evidence, "team"))
	}
	second := result.Jobs[1]
	if second.SourceJobID != "apple_jobs:apple:200700002-1536" || second.Country != "Singapore" || second.RoleFamily != "ml_ai" {
		t.Fatalf("second job = %#v, want normalized Apple Singapore ML job", second)
	}
}

func TestATSExtractorEnrichesAppleJobsFromBoundedDetailPages(t *testing.T) {
	var searchRequests int
	var detailRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/en-us/search":
			searchRequests++
			_, _ = w.Write([]byte(appleJobsSearchPageHTML("en-us", []string{
				`{
					"id":"200700001-0836",
					"positionId":"200700001",
					"reqId":"200700001-0836",
					"postingTitle":"Backend Platform Software Engineer Intern - Summer 2026",
					"transformedPostingTitle":"backend-platform-software-engineer-intern-summer-2026",
					"jobSummary":"Short summary.",
					"team":{"teamName":"Software and Services","teamCode":"SFTWR"},
					"locations":[{"name":"Cupertino","countryName":"United States of America","countryID":"iso-country-USA","level":5}],
					"postExternal":true
				}`,
				`{
					"id":"200700002-0836",
					"positionId":"200700002",
					"reqId":"200700002-0836",
					"postingTitle":"Frontend Software Engineer Intern - Summer 2026",
					"transformedPostingTitle":"frontend-software-engineer-intern-summer-2026",
					"jobSummary":"Search-only summary.",
					"team":{"teamName":"Software and Services","teamCode":"SFTWR"},
					"locations":[{"name":"Seattle","countryName":"United States of America","countryID":"iso-country-USA","level":5}],
					"postExternal":true
				}`,
			})))
		case "/en-us/details/200700001-0836/backend-platform-software-engineer-intern-summer-2026":
			detailRequests++
			if r.URL.Query().Get("team") != "SFTWR" {
				t.Fatalf("team query = %q, want SFTWR", r.URL.Query().Get("team"))
			}
			_, _ = w.Write([]byte(appleJobsSearchPageHTML("en-us", []string{
				`{
					"id":"200700001-0836",
					"positionId":"200700001",
					"reqId":"200700001-0836",
					"postingTitle":"Backend Platform Software Engineer Intern - Summer 2026",
					"transformedPostingTitle":"backend-platform-software-engineer-intern-summer-2026",
					"postDateInGMT":"2026-06-23T05:00:39.873Z",
					"jobSummary":"Own Go services, Redis queues, PostgreSQL storage, and observability for Apple cloud systems. Internship for students graduating in December 2026.",
					"standardWeeklyHours":40,
					"team":{"teamName":"Cloud Infrastructure","teamCode":"SFTWR"},
					"locations":[{"name":"Cupertino","countryName":"United States of America","countryID":"iso-country-USA","level":5}],
					"postExternal":true
				}`,
			})))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), AppleJobsMaxJobs: 2, AppleJobsDetailMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_apple",
		Name: "Apple",
		URL:  server.URL + "/en-us/search?search=software+engineer+intern",
		Metadata: map[string]string{
			"source_kind": "apple_jobs",
		},
	})
	if err != nil {
		t.Fatalf("extract apple jobs: %v", err)
	}
	if searchRequests != 1 || detailRequests != 1 {
		t.Fatalf("requests = search %d detail %d, want 1/1", searchRequests, detailRequests)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(result.Jobs))
	}
	first := result.Jobs[0]
	if !strings.Contains(evidenceText(first.Evidence, "description"), "graduating in December 2026") {
		t.Fatalf("description evidence = %q, want detail-page graduation evidence", evidenceText(first.Evidence, "description"))
	}
	if evidenceText(first.Evidence, "team") != "Cloud Infrastructure" {
		t.Fatalf("team evidence = %q, want detail team", evidenceText(first.Evidence, "team"))
	}
	second := result.Jobs[1]
	if evidenceText(second.Evidence, "description") != "Search-only summary." {
		t.Fatalf("second description = %q, want non-enriched search summary", evidenceText(second.Evidence, "description"))
	}
}

func TestAppleJobsHydrationHelpers(t *testing.T) {
	page := appleJobsSearchPageHTML("en-sg", []string{
		`{
			"id":"200700010-1536",
			"positionId":"200700010",
			"reqId":"200700010-1536",
			"postingTitle":"Software Engineer, Cloud Infrastructure",
			"transformedPostingTitle":"software-engineer-cloud-infrastructure",
			"postingDate":"Jun 21, 2026",
			"team":{"teamName":"Software and Services","teamCode":"SFTWR"},
			"locations":[{"name":"Singapore","countryName":"Singapore","countryID":"iso-country-SGP"}]
		}`,
	})
	data, err := appleJobsHydrationFromHTML(page)
	if err != nil {
		t.Fatalf("hydration from html: %v", err)
	}
	if data.LoaderData.Root.Locale != "en-sg" || len(data.LoaderData.Search.SearchResults) != 1 {
		t.Fatalf("hydration = %#v, want one en-sg search result", data)
	}
	job := data.LoaderData.Search.SearchResults[0]
	if got := appleJobsDetailURL(Source{URL: "https://jobs.apple.com/en-sg/search?search=software"}, "en-sg", job); got != "https://jobs.apple.com/en-sg/details/200700010-1536/software-engineer-cloud-infrastructure?team=SFTWR" {
		t.Fatalf("detail url = %q, want Apple details URL", got)
	}
	if postedAt := appleJobsPostedAt(job); postedAt == nil || postedAt.Format(time.DateOnly) != "2026-06-21" {
		t.Fatalf("posted_at = %v, want medium-date fallback", postedAt)
	}
}

func TestATSExtractorReturnsNoJobsForEmptyAppleSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(appleJobsSearchPageHTML("en-us", nil)))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	_, err := extractor.Extract(context.Background(), Source{
		Name: "Apple",
		URL:  server.URL + "/en-us/search?search=software",
		Metadata: map[string]string{
			"source_kind": "apple_jobs",
		},
	})
	if !errors.Is(err, ErrNoJobs) {
		t.Fatalf("extract empty apple search error = %v, want ErrNoJobs", err)
	}
}

func TestATSExtractorExtractsStripeJobsSearchResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/search" || r.URL.Query().Get("query") != "software engineer intern" {
			t.Fatalf("request = %s?%s, want Stripe jobs search query", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`
<table>
  <tbody>
    <tr class="TableRow">
      <td class="TableCell JobsListings__tableCell JobsListings__tableCell--title JobsListings__tableCell--first">
        <a class="Link JobsListings__link" href="/jobs/listing/software-engineer-intern/7532256">Software Engineer, Intern</a>
      </td>
      <td class="TableCell JobsListings__tableCell JobsListings__tableCell--departments">
        <ul><li>University</li></ul>
      </td>
      <td class="TableCell JobsListings__tableCell JobsListings__tableCell--country">
        <img class="Flag Flag--countryAU" alt="AU">
        <span class="JobsListings__locationDisplayName">Sydney</span>
      </td>
    </tr>
    <tr class="TableRow">
      <td class="TableCell JobsListings__tableCell JobsListings__tableCell--title">
        <a class="Link JobsListings__link" href="/jobs/listing/backend-software-engineer-new-grad/7539999">Backend Software Engineer, New Grad</a>
      </td>
      <td class="TableCell JobsListings__tableCell JobsListings__tableCell--departments">
        <ul><li>Engineering</li></ul>
      </td>
      <td class="TableCell JobsListings__tableCell JobsListings__tableCell--country">
        <img class="Flag Flag--countryUS" alt="US">
        <span class="JobsListings__locationDisplayName">New York</span>
      </td>
    </tr>
  </tbody>
</table>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), StripeJobsMaxJobs: 2})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_stripe",
		Name: "Stripe",
		URL:  server.URL + "/jobs/search?query=software%20engineer%20intern",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "stripe_jobs",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(result.Jobs))
	}
	first := result.Jobs[0]
	if first.SourceJobID != "stripe_jobs:7532256" || first.Company != "Stripe" || first.Level != "internship" || first.RoleFamily != "software_engineering" || first.Country != "Australia" {
		t.Fatalf("first job = %#v, want normalized Stripe intern", first)
	}
	if first.Location != "Sydney, Australia" || first.ApplyURL != server.URL+"/jobs/listing/software-engineer-intern/7532256" {
		t.Fatalf("first location/apply = %q %q", first.Location, first.ApplyURL)
	}
	if evidenceText(first.Evidence, "department") != "University" {
		t.Fatalf("department evidence = %q, want University", evidenceText(first.Evidence, "department"))
	}
	second := result.Jobs[1]
	if second.SourceJobID != "stripe_jobs:7539999" || second.Level != "new_grad" || second.Country != "US" {
		t.Fatalf("second job = %#v, want new-grad US Stripe job", second)
	}
}

func TestATSExtractorExtractsAmazonJobsSearchResults(t *testing.T) {
	var requests []amazonJobsSearchRequest
	var detailRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/en/jobs/") {
			detailRequests++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body>No structured detail.</body></html>`))
			return
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/jobs/search" {
			t.Fatalf("path = %q, want Amazon search API", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
			t.Fatalf("content-type = %q, want json", got)
		}
		var req amazonJobsSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, req)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"found": 3,
			"start": 0,
			"searchHits": [
				{
					"fields": {
						"icimsJobId": ["3024357"],
						"title": ["Backend Software Development Engineer Intern, Core Services - 2026"],
						"location": ["US, WA, Seattle"],
						"normalizedLocation": ["Seattle, Washington, USA"],
						"country": ["US"],
						"description": ["Build highly available Go services for Amazon commerce systems."],
						"basicQualifications": ["Currently enrolled in a Bachelor's degree program in Computer Science or related field with graduation between December 2026 and August 2027."],
						"preferredQualifications": ["Experience with distributed systems, Redis, and observability."],
						"category": ["Software Development"],
						"businessCategory": ["amazon-development-centre"],
						"teamCategory": ["aws"],
						"createdDate": ["1782086400"],
						"updatedDate": ["1782259200"],
						"urlNextStep": ["https://account.amazon.jobs/jobs/3024357/apply"]
					}
				},
				{
					"fields": {
						"icimsJobId": ["3024358"],
						"title": ["Machine Learning Engineer, Recommendations"],
						"location": ["SG, Singapore"],
						"normalizedLocation": ["Singapore"],
						"country": ["SG"],
						"description": ["Improve ranking models, evaluation harnesses, and large-scale ML systems."],
						"basicQualifications": ["Experience building machine learning systems."],
						"category": ["Machine Learning Science"],
						"businessCategory": ["amazon-ai"],
						"updatedDate": ["2026-06-23T11:30:00Z"],
						"urlNextStep": ["https://account.amazon.jobs/jobs/3024358/apply"]
					}
				}
			]
		}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), AmazonJobsPageSize: 2, AmazonJobsMaxPages: 1, AmazonJobsMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_amazon",
		Name: "Amazon",
		URL:  server.URL + "/en/search?base_query=software+engineer+intern&loc_query=United+States",
		Metadata: map[string]string{
			"source_kind": "amazon_jobs",
		},
	})
	if err != nil {
		t.Fatalf("extract amazon jobs: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want one", len(requests))
	}
	if detailRequests != 2 {
		t.Fatalf("detail requests = %d, want 2 opportunistic detail fetches", detailRequests)
	}
	if got := requests[0].JobPostingSearchRequest.Query; got != "software engineer intern" {
		t.Fatalf("query = %q, want source search query", got)
	}
	if requests[0].JobPostingSearchRequest.Start != 0 || requests[0].JobPostingSearchRequest.Size != 2 {
		t.Fatalf("pagination = start %d size %d, want start 0 size 2", requests[0].JobPostingSearchRequest.Start, requests[0].JobPostingSearchRequest.Size)
	}
	if result.Strategy != TierATS || result.Confidence < 0.86 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two high-confidence Amazon jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "amazon_jobs:amazon:3024357" || first.Company != "Amazon" || first.Level != "internship" || first.RoleFamily != "backend" || first.Country != "US" {
		t.Fatalf("first job = %#v, want normalized Amazon backend internship", first)
	}
	if first.Location != "Seattle, Washington, USA" {
		t.Fatalf("location = %q, want normalized Amazon location", first.Location)
	}
	if first.SourceURL != server.URL+"/en/jobs/3024357/backend-software-development-engineer-intern-core-services-2026" {
		t.Fatalf("source url = %q, want Amazon detail URL", first.SourceURL)
	}
	if first.ApplyURL != "https://account.amazon.jobs/jobs/3024357/apply" {
		t.Fatalf("apply url = %q, want Amazon apply URL", first.ApplyURL)
	}
	if first.PostedAt == nil || first.PostedAt.Format(time.DateOnly) != "2026-06-22" {
		t.Fatalf("posted_at = %v, want parsed Amazon created date", first.PostedAt)
	}
	if !strings.Contains(evidenceText(first.Evidence, "requirements"), "December 2026") {
		t.Fatalf("requirements evidence = %q, want graduation evidence", evidenceText(first.Evidence, "requirements"))
	}
	if !strings.Contains(evidenceText(first.Evidence, "preferred_qualifications"), "Redis") {
		t.Fatalf("preferred evidence = %q, want preferred quals", evidenceText(first.Evidence, "preferred_qualifications"))
	}
	second := result.Jobs[1]
	if second.SourceJobID != "amazon_jobs:amazon:3024358" || second.Country != "Singapore" || second.RoleFamily != "ml_ai" {
		t.Fatalf("second job = %#v, want normalized Amazon Singapore ML job", second)
	}
}

func TestATSExtractorEnrichesAmazonJobsFromBoundedDetailPages(t *testing.T) {
	var searchRequests int
	var detailRequests int
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/en/jobs/3024357/backend-software-development-engineer-intern-core-services-2026":
			detailRequests++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><script type="application/ld+json">{
				"@context": "https://schema.org",
				"@type": "JobPosting",
				"identifier": {"value": "3024357"},
				"title": "Backend Software Development Engineer Intern, Core Services - 2026",
				"description": "Own Go services, PostgreSQL storage, Redis queues, and observability for Amazon systems. Internship for students graduating in December 2026.",
				"datePosted": "2026-06-23T11:30:00Z",
				"employmentType": "INTERN",
				"hiringOrganization": {"name": "Amazon"},
				"jobLocation": {"@type": "Place", "address": {"addressLocality": "Seattle", "addressRegion": "WA", "addressCountry": "US"}},
				"url": "` + serverURL + `/en/jobs/3024357/backend-software-development-engineer-intern-core-services-2026"
			}</script></head><body></body></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/jobs/search":
			searchRequests++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"found": 2,
				"start": 0,
				"searchHits": [
					{
						"fields": {
							"icimsJobId": ["3024357"],
							"title": ["Backend Software Development Engineer Intern, Core Services - 2026"],
							"location": ["US, WA, Seattle"],
							"normalizedLocation": ["Seattle, Washington, USA"],
							"country": ["US"],
							"description": ["Short search summary."],
							"basicQualifications": ["Currently enrolled in Computer Science."],
							"category": ["Software Development"],
							"createdDate": ["1782086400"]
						}
					},
					{
						"fields": {
							"icimsJobId": ["3024358"],
							"title": ["Frontend Software Development Engineer Intern - 2026"],
							"location": ["US, CA, San Francisco"],
							"normalizedLocation": ["San Francisco, California, USA"],
							"country": ["US"],
							"description": ["Search-only frontend summary."],
							"category": ["Software Development"]
						}
					}
				]
			}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), AmazonJobsPageSize: 2, AmazonJobsMaxPages: 1, AmazonJobsMaxJobs: 2, AmazonJobsDetailMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_amazon",
		Name: "Amazon",
		URL:  server.URL + "/en/search?base_query=software+engineer+intern",
		Metadata: map[string]string{
			"source_kind": "amazon_jobs",
		},
	})
	if err != nil {
		t.Fatalf("extract amazon jobs: %v", err)
	}
	if searchRequests != 1 || detailRequests != 1 {
		t.Fatalf("requests = search %d detail %d, want 1/1", searchRequests, detailRequests)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(result.Jobs))
	}
	first := result.Jobs[0]
	if !strings.Contains(evidenceText(first.Evidence, "description"), "graduating in December 2026") {
		t.Fatalf("description evidence = %q, want detail-page graduation evidence", evidenceText(first.Evidence, "description"))
	}
	if evidenceText(first.Evidence, "detail") != "Amazon hosted JobPosting detail page" {
		t.Fatalf("detail evidence = %q, want hosted detail evidence", evidenceText(first.Evidence, "detail"))
	}
	if first.PostedAt == nil || first.PostedAt.Format(time.DateOnly) != "2026-06-23" {
		t.Fatalf("posted_at = %v, want detail date", first.PostedAt)
	}
	if evidenceText(result.Jobs[1].Evidence, "description") != "Search-only frontend summary." {
		t.Fatalf("second description = %q, want search-only summary", evidenceText(result.Jobs[1].Evidence, "description"))
	}
}

func TestAmazonJobsHelpers(t *testing.T) {
	if got := amazonJobsSearchEndpoint("https://www.amazon.jobs/en/search?base_query=software"); got != "https://www.amazon.jobs/api/jobs/search" {
		t.Fatalf("endpoint = %q, want Amazon search API", got)
	}
	if got := amazonJobsQuery("https://www.amazon.jobs/en/search?base_query=software+engineer+intern"); got != "software engineer intern" {
		t.Fatalf("query = %q, want decoded base_query", got)
	}
	hit := amazonJobsHit{Fields: map[string][]string{
		"icimsJobId":          {"10453131"},
		"title":               {"Senior Product Manager -Tech, Amazon Warehousing and Distribution"},
		"normalizedLocation":  {"Bellevue, Washington, USA"},
		"country":             {"US"},
		"description":         {"Own technical products."},
		"basicQualifications": {"Bachelor's degree."},
	}}
	if got := amazonJobsDetailURL(Source{URL: "https://www.amazon.jobs/en/search"}, hit); got != "https://www.amazon.jobs/en/jobs/10453131/senior-product-manager-tech-amazon-warehousing-and-distribution" {
		t.Fatalf("detail url = %q, want Amazon job detail URL", got)
	}
}

func TestInferRoleFamilyDoesNotOvermatchAIToken(t *testing.T) {
	if got := inferRoleFamily("Backend service for highly available commerce systems"); got != "backend" {
		t.Fatalf("role family = %q, want backend without treating available as AI", got)
	}
	if got := inferRoleFamily("Software Engineer Intern, AI Platform"); got != "ml_ai" {
		t.Fatalf("role family = %q, want AI token to remain ML/AI", got)
	}
}

func TestATSExtractorReturnsNoJobsForEmptyAmazonSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"found":0,"start":0,"searchHits":[]}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), AmazonJobsMaxPages: 1})
	_, err := extractor.Extract(context.Background(), Source{
		Name: "Amazon",
		URL:  server.URL + "/en/search?base_query=software",
		Metadata: map[string]string{
			"source_kind": "amazon_jobs",
		},
	})
	if !errors.Is(err, ErrNoJobs) {
		t.Fatalf("extract empty amazon search error = %v, want ErrNoJobs", err)
	}
}

func TestATSExtractorExtractsEightfoldPCSXSearchResults(t *testing.T) {
	var searchRequests int
	var detailRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pcsx/search":
			searchRequests++
			if r.URL.Query().Get("domain") != "microsoft.com" {
				t.Fatalf("domain = %q, want microsoft.com", r.URL.Query().Get("domain"))
			}
			if r.URL.Query().Get("query") != "software engineer intern" {
				t.Fatalf("query = %q, want source query", r.URL.Query().Get("query"))
			}
			if r.URL.Query().Get("location") != "United States" {
				t.Fatalf("location = %q, want source location", r.URL.Query().Get("location"))
			}
			if r.URL.Query().Get("start") != "0" {
				t.Fatalf("start = %q, want first page", r.URL.Query().Get("start"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"status": 200,
				"error": {"message": "", "body": ""},
				"data": {
					"count": 2,
					"positions": [
						{
							"id": 1970393556874926,
							"displayJobId": "200040312",
							"name": "Backend Software Engineer Intern",
							"locations": ["United States, Washington, Redmond"],
							"standardizedLocations": ["Redmond, WA, US"],
							"postedTs": 1781622057,
							"creationTs": 1781204923,
							"department": "Software Engineering",
							"workLocationOption": "onsite",
							"atsJobId": "200040312",
							"positionUrl": "/careers/job/1970393556874926"
						},
						{
							"id": 1970393556849595,
							"displayJobId": "200032137",
							"name": "Software Engineer, AI Frameworks",
							"locations": ["Singapore"],
							"standardizedLocations": ["Singapore"],
							"postedTs": 1780075879,
							"creationTs": 1773688771,
							"department": "AI Platform",
							"workLocationOption": "hybrid",
							"atsJobId": "200032137",
							"positionUrl": "/careers/job/1970393556849595"
						}
					]
				}
			}`))
		case "/api/pcsx/position_details":
			detailRequests++
			id := r.URL.Query().Get("position_id")
			if r.URL.Query().Get("domain") != "microsoft.com" || r.URL.Query().Get("hl") != "en" {
				t.Fatalf("detail query = %q, want domain and hl", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			switch id {
			case "1970393556874926":
				_, _ = w.Write([]byte(`{
					"status": 200,
					"error": {"message": "", "body": ""},
					"data": {
						"id": 1970393556874926,
						"displayJobId": "200040312",
						"name": "Backend Software Engineer Intern",
						"jobDescription": "<b>Overview</b><p>Build Go services, Redis queues, and observability systems for students graduating in December 2026.</p>",
						"employmentType": "Internship",
						"roleType": "Individual Contributor"
					}
				}`))
			case "1970393556849595":
				_, _ = w.Write([]byte(`{
					"status": 200,
					"error": {"message": "", "body": ""},
					"data": {
						"id": 1970393556849595,
						"displayJobId": "200032137",
						"name": "Software Engineer, AI Frameworks",
						"jobDescription": "<p>Build model serving, ranking, and ML evaluation infrastructure.</p>",
						"employmentType": "Full-Time"
					}
				}`))
			default:
				t.Fatalf("unexpected detail position_id %q", id)
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), EightfoldPCSXMaxPages: 1, EightfoldPCSXMaxJobs: 5, EightfoldPCSXDetailMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_msft",
		Name: "Microsoft",
		URL:  server.URL + "/global/en/search?q=software+engineer+intern&lc=United+States",
		Metadata: map[string]string{
			"source_kind":  "eightfold_pcsx",
			"domain":       "microsoft.com",
			"api_base_url": server.URL,
		},
	})
	if err != nil {
		t.Fatalf("extract eightfold pcsx: %v", err)
	}
	if searchRequests != 1 || detailRequests != 2 {
		t.Fatalf("requests = search %d detail %d, want one search and two detail", searchRequests, detailRequests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.83 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two high-confidence Eightfold PCSX jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "eightfold_pcsx:microsoft:1970393556874926" || first.Company != "Microsoft" || first.Level != "internship" || first.RoleFamily != "backend" || first.Country != "US" {
		t.Fatalf("first job = %#v, want normalized Microsoft backend internship", first)
	}
	if first.Location != "Redmond, WA, US" {
		t.Fatalf("location = %q, want standardized location", first.Location)
	}
	if first.SourceURL != server.URL+"/careers/job/1970393556874926" || first.ApplyURL != first.SourceURL {
		t.Fatalf("urls = source %q apply %q, want resolved PCSX detail URL", first.SourceURL, first.ApplyURL)
	}
	if first.PostedAt == nil || first.PostedAt.Format(time.DateOnly) != "2026-06-16" {
		t.Fatalf("posted_at = %v, want parsed postedTs", first.PostedAt)
	}
	if !strings.Contains(evidenceText(first.Evidence, "description"), "December 2026") {
		t.Fatalf("description evidence = %q, want detail description", evidenceText(first.Evidence, "description"))
	}
	second := result.Jobs[1]
	if second.SourceJobID != "eightfold_pcsx:microsoft:1970393556849595" || second.Country != "Singapore" || second.RoleFamily != "ml_ai" {
		t.Fatalf("second job = %#v, want normalized Singapore AI job", second)
	}
}

func TestATSExtractorExtractsEightfoldApplyJobs(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/apply/v2/jobs" {
			t.Fatalf("path = %q, want Eightfold apply jobs endpoint", r.URL.Path)
		}
		if r.URL.Query().Get("domain") != "netflix.com" {
			t.Fatalf("domain = %q, want netflix.com", r.URL.Query().Get("domain"))
		}
		if r.URL.Query().Get("query") != "software engineer intern" {
			t.Fatalf("query = %q, want source query", r.URL.Query().Get("query"))
		}
		if r.URL.Query().Get("num") != "10" || r.URL.Query().Get("start") != "0" {
			t.Fatalf("pagination = %q, want num=10 start=0", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"count": 1,
			"positions": [{
				"id": 790313241540,
				"name": "Software Engineer PhD Intern, Streaming Algorithms (Summer 2026)",
				"posting_name": "Software Engineer PhD Intern, Streaming Algorithms (Summer 2026)",
				"location": "Los Gatos,California,United States of America",
				"locations": ["Los Gatos,California,United States of America"],
				"department": "Engineering",
				"business_unit": "Streaming",
				"t_update": 1765324800,
				"t_create": 1765324800,
				"ats_job_id": "JR37687",
				"display_job_id": "JR37687",
				"type": "ATS",
				"job_description": "<p>Build streaming algorithms and experimentation systems.</p>",
				"canonicalPositionUrl": "https://explore.jobs.netflix.net/careers/job/790313241540",
				"work_location_option": "onsite"
			}]
		}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), EightfoldApplyPageSize: 10, EightfoldApplyMaxPages: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_netflix",
		Name: "Netflix",
		URL:  server.URL + "/careers?query=software+engineer+intern&domain=netflix.com",
		Metadata: map[string]string{
			"source_kind":  "eightfold_apply",
			"domain":       "netflix.com",
			"api_base_url": server.URL,
		},
	})
	if err != nil {
		t.Fatalf("extract eightfold apply: %v", err)
	}
	if requests != 1 || result.Strategy != TierATS || result.Confidence < 0.82 || len(result.Jobs) != 1 {
		t.Fatalf("requests = %d result = %+v, want one high-confidence Eightfold apply job", requests, result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "eightfold_apply:netflix:790313241540" || job.Company != "Netflix" || job.Level != "internship" || job.RoleFamily != "infrastructure" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Netflix infrastructure internship", job)
	}
	if job.SourceURL != "https://explore.jobs.netflix.net/careers/job/790313241540" || job.ApplyURL != job.SourceURL {
		t.Fatalf("urls = source %q apply %q, want canonical apply URL", job.SourceURL, job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2025-12-10" {
		t.Fatalf("posted_at = %v, want parsed t_update", job.PostedAt)
	}
	if !strings.Contains(evidenceText(job.Evidence, "description"), "streaming algorithms") {
		t.Fatalf("description evidence = %q, want cleaned description", evidenceText(job.Evidence, "description"))
	}
}

func TestEightfoldPCSXHelpers(t *testing.T) {
	config, err := eightfoldPCSXConfigFromSource(Source{
		URL: "https://jobs.careers.microsoft.com/global/en/search?q=software+engineer+intern&lc=United+States",
	})
	if err != nil {
		t.Fatalf("config from microsoft url: %v", err)
	}
	if config.APIBaseURL != "https://apply.careers.microsoft.com" || config.Domain != "microsoft.com" || config.Query != "software engineer intern" || config.Location != "United States" {
		t.Fatalf("config = %#v, want Microsoft PCSX defaults", config)
	}
	if got := eightfoldPCSXSearchURL(config, 20); got != "https://apply.careers.microsoft.com/api/pcsx/search?domain=microsoft.com&location=United+States&query=software+engineer+intern&start=20" {
		t.Fatalf("search url = %q, want PCSX search endpoint", got)
	}
}

func TestATSExtractorKeepsEightfoldPCSXSearchResultWhenDetailFails(t *testing.T) {
	var detailRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pcsx/search":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"status": 200,
				"data": {"count": 1, "positions": [{
					"id": 1970393556874926,
					"displayJobId": "200040312",
					"name": "Backend Software Engineer Intern",
					"standardizedLocations": ["Redmond, WA, US"],
					"postedTs": 1781622057,
					"department": "Software Engineering",
					"positionUrl": "/careers/job/1970393556874926"
				}]}
			}`))
		case "/api/pcsx/position_details":
			detailRequests++
			http.Error(w, "rate limited", http.StatusTooManyRequests)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), EightfoldPCSXMaxPages: 1, EightfoldPCSXDetailMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Microsoft",
		URL:  server.URL + "/search?q=software",
		Metadata: map[string]string{
			"source_kind":  "eightfold_pcsx",
			"domain":       "microsoft.com",
			"api_base_url": server.URL,
		},
	})
	if err != nil {
		t.Fatalf("extract with detail failure: %v", err)
	}
	if detailRequests != 1 || len(result.Jobs) != 1 {
		t.Fatalf("detail requests = %d jobs = %d, want one detail attempt and one fallback job", detailRequests, len(result.Jobs))
	}
	if result.Jobs[0].SourceJobID != "eightfold_pcsx:microsoft:1970393556874926" || result.Jobs[0].ApplyURL == "" {
		t.Fatalf("fallback job = %#v, want normalized search result", result.Jobs[0])
	}
}

func TestATSExtractorReturnsNoJobsForEmptyEightfoldPCSXSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":200,"data":{"count":0,"positions":[]}}`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), EightfoldPCSXMaxPages: 1})
	_, err := extractor.Extract(context.Background(), Source{
		Name: "Microsoft",
		URL:  server.URL + "/search?q=software",
		Metadata: map[string]string{
			"source_kind":  "eightfold_pcsx",
			"domain":       "microsoft.com",
			"api_base_url": server.URL,
		},
	})
	if !errors.Is(err, ErrNoJobs) {
		t.Fatalf("extract empty eightfold pcsx error = %v, want ErrNoJobs", err)
	}
}

func TestATSExtractorExtractsGoogleCareersSearchResults(t *testing.T) {
	var searchRequests int
	var detailRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/about/careers/applications/jobs/results/") && r.URL.Path != "/about/careers/applications/jobs/results/" {
			detailRequests++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><p>No structured detail.</p></body></html>`))
			return
		}
		if r.URL.Path != "/about/careers/applications/jobs/results/" {
			t.Fatalf("path = %q, want Google Careers results path", r.URL.Path)
		}
		if r.URL.Query().Get("q") != "software engineer intern" {
			t.Fatalf("q = %q, want source query", r.URL.Query().Get("q"))
		}
		searchRequests++
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`
			<html><body>
				<ul>
					<li class="lLd3Je" jsdata="Aiqs8c;114658691801588422;15">
						<h3 class="QJPWVe">Software Engineering Intern, Backend Systems</h3>
						<span class="RP7SMd"><span>Google</span></span>
						<span class="r0wTof ">Mountain View, CA, USA</span>
						<div class="Xsxa1e">
							<h4>Minimum qualifications</h4>
							<ul>
								<li>Currently pursuing a Bachelor's degree in Computer Science or related technical field.</li>
								<li>Experience with Go, distributed systems, and backend services.</li>
							</ul>
						</div>
						<a class="WpHeLc" href="/about/careers/applications/jobs/results/114658691801588422-software-engineering-intern-backend-systems?q=software+engineer+intern&amp;location=United+States">Learn more</a>
					</li>
					<li class="lLd3Je" jsdata="Aiqs8c;108226823657005766;16">
						<h3 class="QJPWVe">Machine Learning Software Engineer, Search</h3>
						<span class="RP7SMd"><span>Google</span></span>
						<span class="r0wTof ">Singapore</span>
						<div class="Xsxa1e">
							<h4>Minimum qualifications</h4>
							<ul><li>Build ranking models, evaluation systems, and ML infrastructure.</li></ul>
						</div>
						<a class="WpHeLc" href="/about/careers/applications/jobs/results/108226823657005766-machine-learning-software-engineer-search">Learn more</a>
					</li>
				</ul>
			</body></html>
		`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), GoogleCareersMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_google",
		Name: "Google",
		URL:  server.URL + "/about/careers/applications/jobs/results/?q=software+engineer+intern&location=United+States",
		Metadata: map[string]string{
			"source_kind": "google_careers",
		},
	})
	if err != nil {
		t.Fatalf("extract google careers: %v", err)
	}
	if searchRequests != 1 || detailRequests != 2 {
		t.Fatalf("requests = search %d detail %d, want 1/2", searchRequests, detailRequests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.80 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two Google Careers jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "google_careers:google:114658691801588422" || first.Company != "Google" || first.Level != "internship" || first.RoleFamily != "backend" || first.Country != "US" {
		t.Fatalf("first job = %#v, want normalized Google backend internship", first)
	}
	if first.Location != "Mountain View, CA, USA" {
		t.Fatalf("location = %q, want Google location", first.Location)
	}
	wantDetail := server.URL + "/about/careers/applications/jobs/results/114658691801588422-software-engineering-intern-backend-systems?q=software+engineer+intern&location=United+States"
	if first.SourceURL != wantDetail || first.ApplyURL != wantDetail {
		t.Fatalf("urls = source %q apply %q, want Google detail URL", first.SourceURL, first.ApplyURL)
	}
	if !strings.Contains(evidenceText(first.Evidence, "requirements"), "Bachelor's degree") {
		t.Fatalf("requirements evidence = %q, want nested qualification text", evidenceText(first.Evidence, "requirements"))
	}
	second := result.Jobs[1]
	if second.SourceJobID != "google_careers:google:108226823657005766" || second.Country != "Singapore" || second.RoleFamily != "ml_ai" {
		t.Fatalf("second job = %#v, want normalized Google Singapore ML job", second)
	}
}

func TestATSExtractorEnrichesGoogleCareersFromBoundedDetailPages(t *testing.T) {
	var searchRequests int
	var detailRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/about/careers/applications/jobs/results/":
			searchRequests++
			_, _ = w.Write([]byte(`
				<html><body>
					<ul>
						<li class="lLd3Je" jsdata="Aiqs8c;114658691801588422;15">
							<h3 class="QJPWVe">Software Engineering Intern, Backend Systems</h3>
							<span class="r0wTof ">Mountain View, CA, USA</span>
							<div class="Xsxa1e"><h4>Minimum qualifications</h4><ul><li>Search-card qualification.</li></ul></div>
							<a class="WpHeLc" href="/about/careers/applications/jobs/results/114658691801588422-software-engineering-intern-backend-systems">Learn more</a>
						</li>
						<li class="lLd3Je" jsdata="Aiqs8c;108226823657005766;16">
							<h3 class="QJPWVe">Frontend Software Engineering Intern</h3>
							<span class="r0wTof ">New York, NY, USA</span>
							<div class="Xsxa1e"><h4>Minimum qualifications</h4><ul><li>Search-only frontend qualification.</li></ul></div>
							<a class="WpHeLc" href="/about/careers/applications/jobs/results/108226823657005766-frontend-software-engineering-intern">Learn more</a>
						</li>
					</ul>
				</body></html>
			`))
		case "/about/careers/applications/jobs/results/114658691801588422-software-engineering-intern-backend-systems":
			detailRequests++
			_, _ = w.Write([]byte(`
				<html><body>
					<h1>Software Engineering Intern, Backend Systems</h1>
					<section class="job-detail-description">
						<h2>About the job</h2>
						<p>Build Go services, Redis queues, and observability systems for Google infrastructure. Internship for students graduating in December 2026.</p>
					</section>
					<section class="job-detail-qualifications">
						<h2>Minimum qualifications</h2>
						<ul>
							<li>Currently pursuing a Bachelor's degree in Computer Science.</li>
							<li>Experience with distributed systems and backend services.</li>
						</ul>
					</section>
				</body></html>
			`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), GoogleCareersMaxJobs: 2, GoogleCareersDetailMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_google",
		Name: "Google",
		URL:  server.URL + "/about/careers/applications/jobs/results/?q=software+engineer+intern",
		Metadata: map[string]string{
			"source_kind": "google_careers",
		},
	})
	if err != nil {
		t.Fatalf("extract google careers: %v", err)
	}
	if searchRequests != 1 || detailRequests != 1 {
		t.Fatalf("requests = search %d detail %d, want 1/1", searchRequests, detailRequests)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(result.Jobs))
	}
	first := result.Jobs[0]
	if !strings.Contains(evidenceText(first.Evidence, "description"), "graduating in December 2026") {
		t.Fatalf("description evidence = %q, want detail-page graduation evidence", evidenceText(first.Evidence, "description"))
	}
	if !strings.Contains(evidenceText(first.Evidence, "requirements"), "distributed systems") {
		t.Fatalf("requirements evidence = %q, want detail qualifications", evidenceText(first.Evidence, "requirements"))
	}
	if evidenceText(first.Evidence, "detail") != "Google Careers hosted detail page" {
		t.Fatalf("detail evidence = %q, want hosted detail evidence", evidenceText(first.Evidence, "detail"))
	}
	if evidenceText(result.Jobs[1].Evidence, "requirements") != "Minimum qualifications Search-only frontend qualification." {
		t.Fatalf("second requirements = %q, want search-card qualifications", evidenceText(result.Jobs[1].Evidence, "requirements"))
	}
}

func TestGoogleCareersHTMLHelpers(t *testing.T) {
	page := `<li class="lLd3Je" jsdata="Aiqs8c;12345;1"><h3 class="QJPWVe">Backend Engineer</h3><ul><li>Nested bullet</li></ul></li><li class="lLd3Je" jsdata="Aiqs8c;67890;2"><h3 class="QJPWVe">Frontend Engineer</h3></li>`
	cards := googleCareersJobCards(page)
	if len(cards) != 2 {
		t.Fatalf("cards = %d, want two top-level cards", len(cards))
	}
	if id := googleCareersID(cards[0]); id != "12345" {
		t.Fatalf("id = %q, want first jsdata id", id)
	}
	if title := googleCareersTitle(cards[1]); title != "Frontend Engineer" {
		t.Fatalf("title = %q, want second card title", title)
	}
	fallback := `<li class="lLd3Je"><h3 class="QJPWVe">Fallback ID Engineer</h3><a class="WpHeLc" href="jobs/results/99999-fallback-id-engineer">Learn more</a></li>`
	if id := googleCareersID(fallback); id != "99999" {
		t.Fatalf("fallback id = %q, want id from relative detail href", id)
	}
}

func TestGoogleCareersPostingIgnoresGenericDetailPageTitle(t *testing.T) {
	source := Source{
		Name: "Google",
		URL:  "https://www.google.com/about/careers/applications/jobs/results/?q=software+engineer",
	}
	card := `<li class="lLd3Je" jsdata="Aiqs8c;103498301961052870;1">
		<h3 class="QJPWVe">Senior Software Engineer, Google Ads</h3>
		<span class="r0wTof">Mountain View, CA, USA</span>
		<a class="WpHeLc" href="/about/careers/applications/jobs/results/103498301961052870-senior-software-engineer-google-ads">Learn more</a>
	</li>`
	detail := googleCareersDetailFromHTML(`<html><body><h1>Job details</h1></body></html>`)

	job, ok := googleCareersPosting(source, card, detail)
	if !ok {
		t.Fatal("googleCareersPosting() rejected a valid search card")
	}
	if job.Title != "Senior Software Engineer, Google Ads" {
		t.Fatalf("title = %q, want search-card title", job.Title)
	}
}

func TestATSExtractorReturnsNoJobsForEmptyGoogleCareersSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><p>No jobs matched.</p></body></html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	_, err := extractor.Extract(context.Background(), Source{
		Name: "Google",
		URL:  server.URL + "/about/careers/applications/jobs/results/?q=software",
		Metadata: map[string]string{
			"source_kind": "google_careers",
		},
	})
	if !errors.Is(err, ErrNoJobs) {
		t.Fatalf("extract empty google careers error = %v, want ErrNoJobs", err)
	}
}

func TestATSExtractorExtractsOpenAICareersJobs(t *testing.T) {
	page := `<html><body>
		<a href="/careers/backend-software-engineer-applied-foundations">Backend Software Engineer, Applied Foundations Applied AI Engineering 2 locations</a>
		<a href="https://jobs.ashbyhq.com/openai/4df1c6c9-60e7-4241-a122-9c20ff0f95de">Apply now</a>
		<a href="/careers/frontend-software-engineer-codex-app">Frontend Software Engineer, Codex App Codex - Engineering San Francisco</a>
		<a href="https://jobs.ashbyhq.com/openai/a986e813-c2cb-4eb5-8fc4-bc2d32fbf334">Apply now</a>
		<a href="/careers">Careers</a>
	</body></html>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/careers/search/" {
			t.Fatalf("path = %q, want OpenAI careers search path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{
		Client:               server.Client(),
		OpenAICareersMaxJobs: 10,
	})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "OpenAI",
		URL:  server.URL + "/careers/search/?q=software%20engineer",
		Metadata: map[string]string{
			"source_kind": "openai_careers",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.79 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2: %#v", len(result.Jobs), result.Jobs)
	}
	first := result.Jobs[0]
	if first.Company != "OpenAI" || first.Title != "Backend Software Engineer, Applied Foundations" || first.Location != "Multiple locations" || first.ApplyURL != "https://jobs.ashbyhq.com/openai/4df1c6c9-60e7-4241-a122-9c20ff0f95de" {
		t.Fatalf("first job = %#v, want normalized OpenAI backend role", first)
	}
	if first.SourceJobID != "openai:4df1c6c9-60e7-4241-a122-9c20ff0f95de" || first.RoleFamily != "ml_ai" {
		t.Fatalf("first id/role = %q/%q, want stable Ashby id and AI family", first.SourceJobID, first.RoleFamily)
	}
	second := result.Jobs[1]
	if second.Title != "Frontend Software Engineer, Codex App" || second.Location != "San Francisco" || second.Country != "US" || second.RoleFamily != "frontend" {
		t.Fatalf("second job = %#v, want normalized San Francisco frontend role", second)
	}
	if evidenceText(second.Evidence, "listing") == "" {
		t.Fatalf("second evidence = %#v, want listing evidence", second.Evidence)
	}
}

func TestATSExtractorReturnsNoJobsForOpenAICareersChallengePage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><noscript>Enable JavaScript and cookies to continue</noscript></body></html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	_, err := extractor.Extract(context.Background(), Source{
		Name: "OpenAI",
		URL:  server.URL + "/careers/search/?q=software",
		Metadata: map[string]string{
			"source_kind": "openai_careers",
		},
	})
	if !errors.Is(err, ErrNoJobs) {
		t.Fatalf("extract challenge OpenAI careers error = %v, want ErrNoJobs", err)
	}
}

func TestATSExtractorExtractsJOINBoardJobs(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/companies/routinelabs" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><script id="__NEXT_DATA__" type="application/json">{
			"props":{
				"pageProps":{
					"initialState":{
						"company":{"name":"Routine Labs","domain":"routinelabs"},
						"jobs":{
							"items":[
								{
									"id":15846120,
									"idParam":"16297684-founders-associate-intern-working-student",
									"title":"Founders Associate Intern / Working Student",
									"createdAt":"2026-03-13T10:02:15.614Z",
									"workplaceType":"REMOTE",
									"city":{"cityName":"Berlin","countryName":"Germany"},
									"country":{"iso3166":"DE"},
									"employmentType":{"name":"Internship"},
									"category":{"name":"Other"}
								},
								{
									"id":15393396,
									"idParam":"16268875-senior-software-engineer-backend-llm-infrastructure",
									"title":"Senior Software Engineer (Backend/LLM Infrastructure)",
									"createdAt":"2025-12-18T15:37:26.494Z",
									"workplaceType":"ONSITE",
									"city":{"cityName":"Berlin","countryName":"Germany"},
									"country":{"iso3166":"DE"},
									"employmentType":{"name":"Employee"},
									"category":{"name":"Software Development"}
								}
							]
						}
					}
				}
			}
		}</script></body></html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), JOINMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Routine Labs",
		URL:  server.URL + "/companies/routinelabs",
		Metadata: map[string]string{
			"source_kind": "join_com",
		},
	})
	if err != nil {
		t.Fatalf("extract join: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one board fetch", requests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.80 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two JOIN jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "join:16297684-founders-associate-intern-working-student" || first.Company != "Routine Labs" || first.Level != "internship" {
		t.Fatalf("first job = %#v, want normalized JOIN internship", first)
	}
	if first.Location != "Remote - Berlin, Germany" || first.Country != "DE" {
		t.Fatalf("location = %q country = %q, want remote Berlin Germany", first.Location, first.Country)
	}
	wantApply := server.URL + "/companies/routinelabs/16297684-founders-associate-intern-working-student"
	if first.ApplyURL != wantApply || first.SourceURL != server.URL+"/companies/routinelabs" {
		t.Fatalf("urls = source %q apply %q, want JOIN board/detail", first.SourceURL, first.ApplyURL)
	}
	if evidenceText(first.Evidence, "ats") != "JOIN hosted company page Next.js jobs state" {
		t.Fatalf("ats evidence = %q, want JOIN state evidence", evidenceText(first.Evidence, "ats"))
	}
	second := result.Jobs[1]
	if second.RoleFamily != "ml_ai" || second.EmploymentType != "Employee" {
		t.Fatalf("second job = %#v, want ML/AI employee role", second)
	}
}

func TestATSExtractorExtractsJOINDetailJobPosting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/companies/zenysiscom/16321120-software-engineer-general-2-positions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><script type="application/ld+json">{
			"@context":"https://schema.org",
			"@type":"JobPosting",
			"datePosted":"2026-05-21T11:48:28.037Z",
			"title":"Software Engineer - General (2 Positions)",
			"description":"<p>Build public-sector health data services with Go and PostgreSQL. Internship experience is acceptable.</p>",
			"employmentType":"FULL_TIME",
			"hiringOrganization":{"@type":"Organization","name":"Zenysis Technologies"},
			"jobLocation":{"@type":"Place","address":{"@type":"PostalAddress","addressCountry":"Rwanda","addressLocality":"Kigali"}}
		}</script></body></html>`))
	}))
	defer server.Close()

	sourceURL := server.URL + "/companies/zenysiscom/16321120-software-engineer-general-2-positions"
	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Zenysis",
		URL:  sourceURL,
		Metadata: map[string]string{
			"source_kind": "join_com",
		},
	})
	if err != nil {
		t.Fatalf("extract join detail: %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want one JOIN detail job", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "join:16321120-software-engineer-general-2-positions" || job.Company != "Zenysis Technologies" || job.Country != "Rwanda" {
		t.Fatalf("job = %#v, want normalized JOIN detail", job)
	}
	if job.Strategy != TierATS || evidenceText(job.Evidence, "ats") != "JOIN hosted JobPosting detail JSON-LD" {
		t.Fatalf("strategy/evidence = %s/%q, want JOIN detail ATS", job.Strategy, evidenceText(job.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsOccupopFrameJobs(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/jobs-frame/token-123" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`<html><body>
			<table class="table table-responsive">
				<tbody class="searchable">
					<tr class=" ">
						<td>
							<h4 class="title">
								<a href="https://api.occupop.com/shared/job/software-engineer-backend-32afad" target="_blank">Software Engineer - Backend</a><br>
								<small class="location"><i class="fa fa-map-marker"></i> Hybrid / Remote (Somerset, South Africa)</small>
								<small class="category"><i class="fa fa-tag"></i> IT</small>
								<small class="type hidden-xs"><i class="fa fa-clock-o"></i> Permanent</small>
							</h4>
						</td>
						<td class="text-right">
							<a class="btn btn-primary" href="https://api.occupop.com/shared/job/software-engineer-backend-32afad" target="_blank">Apply</a>
						</td>
					</tr>
					<tr class=" ">
						<td>
							<h4 class="title">
								<a href="/shared/job/marketing-coordinator-12345" target="_blank">Marketing Coordinator</a><br>
								<small class="location"><i class="fa fa-map-marker"></i> Canada</small>
								<small class="category"><i class="fa fa-tag"></i> Marketing</small>
								<small class="type hidden-xs"><i class="fa fa-clock-o"></i> Permanent</small>
							</h4>
						</td>
					</tr>
				</tbody>
			</table>
		</body></html>`))
	}))
	defer server.Close()

	sourceURL := server.URL + "/api/jobs-frame/token-123?fields=title%2Ctype%2Clocation%2Csector&visibility=external"
	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), OccupopMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "CyberSentriq",
		URL:  sourceURL,
		Metadata: map[string]string{
			"source_kind": "occupop",
		},
	})
	if err != nil {
		t.Fatalf("extract occupop: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one frame fetch", requests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.75 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two Occupop jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "occupop:software-engineer-backend-32afad" || first.Company != "CyberSentriq" || first.RoleFamily != "backend" {
		t.Fatalf("first job = %#v, want normalized backend Occupop job", first)
	}
	if first.Location != "Hybrid / Remote (Somerset, South Africa)" || first.Country != "South Africa" {
		t.Fatalf("location = %q country = %q, want South Africa", first.Location, first.Country)
	}
	if first.EmploymentType != "Permanent" || evidenceText(first.Evidence, "ats") != "Occupop jobs-frame hosted board" {
		t.Fatalf("employment/evidence = %q/%q, want Occupop frame evidence", first.EmploymentType, evidenceText(first.Evidence, "ats"))
	}
	second := result.Jobs[1]
	if second.ApplyURL != server.URL+"/shared/job/marketing-coordinator-12345" || second.Country != "Canada" {
		t.Fatalf("second job = %#v, want resolved relative detail URL", second)
	}
}

func TestATSExtractorExtractsWorkstreamBoardJobs(t *testing.T) {
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/j/caf15f37/sleep-inn":
			_, _ = w.Write([]byte(`<html><body>
				<a href="/j/caf15f37/sleep-inn/positions?locale=en">OPEN POSITIONS (2)</a>
				<div class="position-card pointer" onclick="location.href='/j/caf15f37/sleep-inn/eastman-202797/software-engineering-intern-abc123?locale=en'">
					<a href="/j/caf15f37/sleep-inn/eastman-202797/software-engineering-intern-abc123?locale=en">Software Engineering Intern</a>
					<div class="position-address mute fz13px">995 Indian Dr, Eastman, GA 31023, USA</div>
					<div class="position-short-desc fz13px">Build Go services and scheduling tools for students graduating in December 2026.</div>
					<span class="tag tag-small">Internship</span>
					<img data-icon="rate-of-pay"></img><span class="mute fz13px">$20.00 per hour</span>
				</div>
			</body></html>`))
		case "/j/caf15f37/sleep-inn/positions":
			_, _ = w.Write([]byte(`<html><body>
				<div class="position-card pointer">
					<a href="/j/caf15f37/sleep-inn/eastman-202797/software-engineering-intern-abc123?locale=en">Software Engineering Intern</a>
					<div class="position-address mute fz13px">995 Indian Dr, Eastman, GA 31023, USA</div>
				</div>
				<div class="position-card pointer">
					<a href="/j/caf15f37/sleep-inn/remote-391/backend-platform-engineer-def456?locale=en">Backend Platform Engineer</a>
					<div class="position-address mute fz13px">Remote, United States</div>
					<div class="position-short-desc fz13px">Operate Postgres, Redis, and high-throughput worker queues.</div>
					<span class="tag tag-small">Full-time</span>
				</div>
			</body></html>`))
		case "/j/caf15f37/sleep-inn/eastman-202797/software-engineering-intern-abc123":
			_, _ = w.Write([]byte(`<html><body><script type="application/ld+json">{
				"@context":"https://schema.org",
				"@type":"JobPosting",
				"title":"Software Engineering Intern",
				"description":"<p>Build Go services and scheduling tools for students graduating in December 2026.</p>",
				"datePosted":"2026-06-20",
				"employmentType":["INTERN"],
				"url":"/j/caf15f37/sleep-inn/eastman-202797/software-engineering-intern-abc123/apply?locale=en",
				"hiringOrganization":{"@type":"Organization","name":"Sleep Inn"},
				"jobLocation":{"@type":"Place","address":{"@type":"PostalAddress","addressLocality":"Eastman","addressRegion":"GA","addressCountry":"US"}}
			}</script></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), WorkstreamMaxJobs: 5, WorkstreamDetailMaxJobs: 1})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Sleep Inn",
		URL:  server.URL + "/j/caf15f37/sleep-inn?locale=en",
		Metadata: map[string]string{
			"source_kind": "workstream",
		},
	})
	if err != nil {
		t.Fatalf("extract workstream: %v", err)
	}
	if requests["/j/caf15f37/sleep-inn"] != 1 || requests["/j/caf15f37/sleep-inn/positions"] != 1 || requests["/j/caf15f37/sleep-inn/eastman-202797/software-engineering-intern-abc123"] != 1 {
		t.Fatalf("requests = %#v, want board, positions, and one bounded detail request", requests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.80 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two Workstream jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "workstream:software-engineering-intern-abc123" || first.Company != "Sleep Inn" || first.Level != "internship" {
		t.Fatalf("first job = %#v, want normalized Workstream internship", first)
	}
	if first.Location != "Eastman, GA, US" || first.Country != "US" || first.EmploymentType != "intern" {
		t.Fatalf("first location/country/employment = %q/%q/%q, want JSON-LD normalized detail", first.Location, first.Country, first.EmploymentType)
	}
	if first.ApplyURL != server.URL+"/j/caf15f37/sleep-inn/eastman-202797/software-engineering-intern-abc123/apply?locale=en" || evidenceText(first.Evidence, "ats") != "Workstream hosted JobPosting detail JSON-LD" {
		t.Fatalf("first apply/evidence = %q/%q, want detail enrichment", first.ApplyURL, evidenceText(first.Evidence, "ats"))
	}
	second := result.Jobs[1]
	if second.SourceJobID != "workstream:backend-platform-engineer-def456" || second.RoleFamily != "backend" || second.Country != "US" {
		t.Fatalf("second job = %#v, want card fallback backend US job", second)
	}
	if evidenceText(second.Evidence, "ats") != "Workstream hosted board card" {
		t.Fatalf("second ats evidence = %q, want card evidence", evidenceText(second.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsWorkstreamDetailJobPosting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/j/4e274f56/ruths-chris-steak-house/pikesville-7242/it-and-pos-deployment-engineer-6da47fc7" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><script type="application/ld+json">{
			"@context":"https://schema.org",
			"@type":"JobPosting",
			"title":"It and POS Deployment Engineer",
			"description":"<p>Deploy restaurant systems, troubleshoot networks, and write automation scripts.</p>",
			"datePosted":"2026-06-20",
			"validThrough":"2026-08-21",
			"employmentType":["FULL_TIME"],
			"url":"/j/4e274f56/ruths-chris-steak-house/pikesville-7242/it-and-pos-deployment-engineer-6da47fc7/apply?locale=en",
			"hiringOrganization":{"@type":"Organization","name":"Ruth's Chris Steak House"},
			"jobLocation":{"@type":"Place","address":{"@type":"PostalAddress","addressLocality":"Pikesville","addressRegion":"MD","addressCountry":"US"}}
		}</script></body></html>`))
	}))
	defer server.Close()

	sourceURL := server.URL + "/j/4e274f56/ruths-chris-steak-house/pikesville-7242/it-and-pos-deployment-engineer-6da47fc7?locale=en"
	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Ruth's Chris",
		URL:  sourceURL,
		Metadata: map[string]string{
			"source_kind": "workstream",
		},
	})
	if err != nil {
		t.Fatalf("extract workstream detail: %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want one Workstream detail job", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "workstream:it-and-pos-deployment-engineer-6da47fc7" || job.Company != "Ruth's Chris Steak House" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Workstream detail", job)
	}
	if job.Strategy != TierATS || evidenceText(job.Evidence, "ats") != "Workstream hosted JobPosting detail JSON-LD" {
		t.Fatalf("strategy/evidence = %s/%q, want Workstream detail ATS", job.Strategy, evidenceText(job.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsCareerPlugBoardJobs(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/jobs" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><script type="application/ld+json">{
			"@context":"https://schema.org",
			"@type":"ItemList",
			"numberOfItems":2,
			"itemListElement":[
				{"@type":"ListItem","position":1,"item":{
					"@type":"JobPosting",
					"title":"Junior Physics Engineer Intern",
					"description":"<p>Develop simulations using MATLAB, Python, or C++ for radar systems. Students graduating in December 2026 are encouraged.</p>",
					"datePosted":"2026-06-20T12:00:00Z",
					"employmentType":"PART_TIME",
					"identifier":{"@type":"PropertyValue","name":"CareerPlug Job ID","value":"2847861"},
					"hiringOrganization":{"@type":"Organization","name":"TLC Millimeter Wave Products, Inc."},
					"jobLocation":{"@type":"Place","address":{"@type":"PostalAddress","addressLocality":"Minneapolis","addressRegion":"MN","addressCountry":"US"}}
				}},
				{"@type":"ListItem","position":2,"item":{
					"@type":"JobPosting",
					"title":"Backend Software Engineer",
					"description":"<p>Build Go APIs and PostgreSQL services.</p>",
					"datePosted":"2026-06-21T12:00:00Z",
					"employmentType":"FULL_TIME",
					"url":"/jobs/3000002",
					"identifier":{"@type":"PropertyValue","name":"CareerPlug Job ID","value":"3000002"},
					"hiringOrganization":{"@type":"Organization","name":"TLC Millimeter Wave Products, Inc."},
					"jobLocation":{"@type":"Place","address":{"@type":"PostalAddress","addressLocality":"Remote","addressCountry":"US"}}
				}}
			]
		}</script></body></html>`))
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), CareerPlugMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "TLC",
		URL:  server.URL + "/jobs",
		Metadata: map[string]string{
			"source_kind": "careerplug",
		},
	})
	if err != nil {
		t.Fatalf("extract careerplug: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one board fetch", requests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.80 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two CareerPlug jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "careerplug:2847861" || first.Company != "TLC Millimeter Wave Products, Inc." || first.Level != "internship" {
		t.Fatalf("first job = %#v, want normalized CareerPlug internship", first)
	}
	if first.ApplyURL != server.URL+"/jobs/2847861" || first.Country != "US" || first.EmploymentType != "part_time" {
		t.Fatalf("first apply/country/employment = %q/%q/%q, want reconstructed CareerPlug detail", first.ApplyURL, first.Country, first.EmploymentType)
	}
	second := result.Jobs[1]
	if second.SourceJobID != "careerplug:3000002" || second.RoleFamily != "backend" || second.ApplyURL != server.URL+"/jobs/3000002" {
		t.Fatalf("second job = %#v, want backend CareerPlug URL from JSON-LD", second)
	}
}

func TestATSExtractorExtractsCareerPlugDetailJobPosting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/2847861" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><script type="application/ld+json">{
			"@context":"https://schema.org",
			"@type":"JobPosting",
			"title":"Junior Physics Engineer Intern",
			"description":"<p>Develop simulations using MATLAB, Python, or C++ for radar systems.</p>",
			"datePosted":"2026-06-20T12:00:00Z",
			"employmentType":"PART_TIME",
			"identifier":{"@type":"PropertyValue","name":"CareerPlug Job ID","value":"2847861"},
			"hiringOrganization":{"@type":"Organization","name":"TLC Millimeter Wave Products, Inc."},
			"jobLocation":{"@type":"Place","address":{"@type":"PostalAddress","addressLocality":"Minneapolis","addressRegion":"MN","addressCountry":"US"}}
		}</script></body></html>`))
	}))
	defer server.Close()

	sourceURL := server.URL + "/jobs/2847861?embed=1"
	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "TLC",
		URL:  sourceURL,
		Metadata: map[string]string{
			"source_kind": "careerplug",
		},
	})
	if err != nil {
		t.Fatalf("extract careerplug detail: %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want one CareerPlug detail job", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "careerplug:2847861" || job.ApplyURL != server.URL+"/jobs/2847861" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized CareerPlug detail", job)
	}
	if job.Strategy != TierATS || evidenceText(job.Evidence, "ats") != "CareerPlug hosted JobPosting JSON-LD" {
		t.Fatalf("strategy/evidence = %s/%q, want CareerPlug detail ATS", job.Strategy, evidenceText(job.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsHireologyBoardJobs(t *testing.T) {
	requests := map[string]int{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		switch r.URL.Path {
		case "/acme":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><script>
				var startingData = {"apiUrl":"` + server.URL + `","appUrl":"` + server.URL + `","apiToken":"test-token","careersPath":"acme"};
			</script><div id="careers-index-container"></div></body></html>`))
		case "/public/careers/acme":
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Fatalf("authorization header = %q, want bearer token", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"count":2,
				"page":1,
				"page_size":20,
				"data":[
					{
						"id":2730757,
						"name":"Software Engineering Intern",
						"created_at":"2026-06-20T13:10:00Z",
						"status":"Open",
						"employment_status":"Part Time",
						"job_description":"<p>Build internal TypeScript tools and Go services. December 2026 grads encouraged.</p>",
						"locations":[{"city":"Salt Lake City","state":"UT","zip_code":"84119","address":"2064 W. Alexander Street"}],
						"remote":false,
						"job_family":{"name":"Engineering"},
						"career_site_url":"` + server.URL + `/acme/2730757/description",
						"application_path":"/careers/2730757/application",
						"career_site_path":"/acme/2730757/description",
						"organization":{"name":"Acme Robotics"},
						"compensation":{"is_comp_range":true,"comp_range_min":"30.0","comp_range_max":"45.0","comp_period":"hour","comp_frequency":"hourly"}
					},
					{
						"id":2730758,
						"name":"Backend Platform Engineer",
						"created_at":"2026-06-21T13:10:00Z",
						"status":"Open",
						"employment_status":"Full Time",
						"job_description":"<p>Own Go APIs, PostgreSQL storage, and queue workers.</p>",
						"locations":[{"city":"New York","state":"NY"}],
						"remote":true,
						"job_family":{"name":"Engineering"},
						"career_site_url":"` + server.URL + `/acme/2730758/description",
						"application_path":"/careers/2730758/application",
						"career_site_path":"/acme/2730758/description",
						"organization":{"name":"Acme Robotics"}
					}
				]
			}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), HireologyMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Acme",
		URL:  server.URL + "/acme",
		Metadata: map[string]string{
			"source_kind": "hireology",
		},
	})
	if err != nil {
		t.Fatalf("extract hireology: %v", err)
	}
	if requests["/acme"] != 1 || requests["/public/careers/acme"] != 1 {
		t.Fatalf("requests = %#v, want board and API fetch", requests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.85 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two Hireology jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "hireology:2730757" || first.Company != "Acme" || first.Level != "internship" {
		t.Fatalf("first job = %#v, want normalized Hireology intern", first)
	}
	if first.Location != "Salt Lake City, UT" || first.Country != "US" || first.EmploymentType != "Part Time" {
		t.Fatalf("first location/country/employment = %q/%q/%q, want Hireology location", first.Location, first.Country, first.EmploymentType)
	}
	if first.ApplyURL != server.URL+"/2730757/application" || evidenceText(first.Evidence, "ats") != "Hireology public careers API" {
		t.Fatalf("first apply/evidence = %q/%q, want API-normalized application URL", first.ApplyURL, evidenceText(first.Evidence, "ats"))
	}
	second := result.Jobs[1]
	if second.SourceJobID != "hireology:2730758" || second.RoleFamily != "backend" || second.Country != "US" {
		t.Fatalf("second job = %#v, want backend Hireology job", second)
	}
}

func TestATSExtractorExtractsHireologyDetailJobPosting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acme/2730757/description" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><script type="application/ld+json">{
			"@context":"https://schema.org",
			"@type":"JobPosting",
			"title":"Lean Manufacturing / Engineering Intern",
			"description":"<p>Improve production systems and write scripts for continuous improvement.</p>",
			"datePosted":"2026-06-20",
			"validThrough":"2026-08-20T14:15",
			"employmentType":"PART_TIME",
			"identifier":{"@type":"PropertyValue","name":"company","value":"2730756"},
			"directApply":true,
			"hiringOrganization":{"@type":"Organization","name":"Acme Robotics"},
			"jobLocation":{"@type":"Place","address":{"@type":"PostalAddress","addressLocality":"Salt Lake City","addressRegion":"UT","addressCountry":"US"}}
		}</script></body></html>`))
	}))
	defer server.Close()

	sourceURL := server.URL + "/acme/2730757/description?ref=indeed.com"
	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Acme",
		URL:  sourceURL,
		Metadata: map[string]string{
			"source_kind": "hireology",
		},
	})
	if err != nil {
		t.Fatalf("extract hireology detail: %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want one Hireology detail job", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "hireology:2730757" || job.Company != "Acme Robotics" || job.Country != "US" {
		t.Fatalf("job = %#v, want URL ID preferred over JSON-LD identifier", job)
	}
	if job.Strategy != TierATS || evidenceText(job.Evidence, "ats") != "Hireology hosted JobPosting JSON-LD" {
		t.Fatalf("strategy/evidence = %s/%q, want Hireology detail ATS", job.Strategy, evidenceText(job.Evidence, "ats"))
	}
}

func TestATSExtractorExtractsGemBoardJobs(t *testing.T) {
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		if r.URL.Path != "/api/public/graphql" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var req struct {
			OperationName string            `json:"operationName"`
			Variables     map[string]string `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Variables["boardId"] != "deep-infra" {
			t.Fatalf("boardId = %q, want deep-infra", req.Variables["boardId"])
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.OperationName {
		case "JobBoardList":
			_, _ = w.Write([]byte(`{
				"data":{
					"jobBoardExternal":{"id":"board1","teamDisplayName":"Deep Infra Inc.","pageTitle":"Deep Infra Careers"},
					"oatsExternalJobPostings":{"jobPostings":[
						{
							"id":"T2F0c0pvYlBvc3Q6MTgzNDY1NQ==",
							"extId":"am9icG9zdDpFMzr4l_l2oOQh6tBtM3i_",
							"title":"Software Engineer Intern (EU-Remote)",
							"locations":[{"id":"9940","name":"EU Remote","city":"Sofia","isoCountry":"BGR","isRemote":true,"extId":"bG9jOr1sDNnRd4ZpqiRxSY7zOVY"}],
							"job":{"id":"job1","department":{"id":"45759","name":"Engineering","extId":"dept1"},"locationType":"REMOTE","employmentType":"INTERN"}
						},
						{
							"id":"T2F0c0pvYlBvc3Q6MTgzNDY1Ng==",
							"extId":"early-career-123",
							"title":"Software Engineer, Early Career",
							"locations":[{"id":"9941","name":"Palo Alto, CA, USA","city":"Palo Alto","isoCountry":"US","isRemote":false,"extId":"loc2"}],
							"job":{"id":"job2","department":{"id":"45759","name":"Backend Platform","extId":"dept2"},"locationType":"IN_OFFICE","employmentType":"FULL_TIME"}
						}
					]}
				}
			}`))
		case "ExternalJobPostingQuery":
			switch req.Variables["extId"] {
			case "am9icG9zdDpFMzr4l_l2oOQh6tBtM3i_":
				_, _ = w.Write([]byte(`{"data":{"oatsExternalJobPosting":{
					"id":"T2F0c0pvYlBvc3Q6MTgzNDY1NQ==",
					"extId":"am9icG9zdDpFMzr4l_l2oOQh6tBtM3i_",
					"title":"Software Engineer Intern (EU-Remote)",
					"descriptionHtml":"<div>Build TypeScript and Python services for ML inference. Internship for 2026 graduates.</div>",
					"firstPublishedTsSec":1759943877,
					"locations":[{"id":"9940","name":"EU Remote","city":"Sofia","isoCountry":"BGR","isRemote":true,"extId":"bG9jOr1sDNnRd4ZpqiRxSY7zOVY"}],
					"job":{"id":"job1","department":{"id":"45759","name":"Engineering","extId":"dept1"},"locationType":"REMOTE","employmentType":"INTERN"},
					"jobPostSectionHtml":{"introHtml":"","outroHtml":""},
					"compensationHtml":"<p>Competitive hourly pay</p>"
				}}}`))
			case "early-career-123":
				_, _ = w.Write([]byte(`{"data":{"oatsExternalJobPosting":{
					"id":"T2F0c0pvYlBvc3Q6MTgzNDY1Ng==",
					"extId":"early-career-123",
					"title":"Software Engineer, Early Career",
					"descriptionHtml":"<p>Own backend APIs, distributed systems, and PostgreSQL services.</p>",
					"firstPublishedTsSec":1759950000,
					"locations":[{"id":"9941","name":"Palo Alto, CA, USA","city":"Palo Alto","isoCountry":"US","isRemote":false,"extId":"loc2"}],
					"job":{"id":"job2","department":{"id":"45759","name":"Backend Platform","extId":"dept2"},"locationType":"IN_OFFICE","employmentType":"FULL_TIME"},
					"jobPostSectionHtml":{"introHtml":"","outroHtml":""}
				}}}`))
			default:
				t.Fatalf("unexpected extId %s", req.Variables["extId"])
			}
		default:
			t.Fatalf("unexpected operation %s", req.OperationName)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), GemMaxJobs: 5, GemDetailMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		Name: "Deep Infra",
		URL:  server.URL + "/deep-infra",
		Metadata: map[string]string{
			"source_kind": "gem",
		},
	})
	if err != nil {
		t.Fatalf("extract gem: %v", err)
	}
	if requests["/api/public/graphql"] != 3 {
		t.Fatalf("graphql requests = %#v, want board plus two details", requests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.85 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two Gem jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "gem:am9icG9zdDpFMzr4l_l2oOQh6tBtM3i_" || first.Level != "internship" || first.Country != "BGR" {
		t.Fatalf("first job = %#v, want normalized Gem internship", first)
	}
	if first.SourceURL != server.URL+"/deep-infra/am9icG9zdDpFMzr4l_l2oOQh6tBtM3i_" || evidenceText(first.Evidence, "ats") != "Gem public job board GraphQL" {
		t.Fatalf("first source/evidence = %q/%q, want Gem detail URL and evidence", first.SourceURL, evidenceText(first.Evidence, "ats"))
	}
	second := result.Jobs[1]
	if second.SourceJobID != "gem:early-career-123" || second.RoleFamily != "backend" || second.Country != "US" {
		t.Fatalf("second job = %#v, want early-career backend US job", second)
	}
}

func TestATSExtractorExtractsDoverJobs(t *testing.T) {
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/careers-page-slug/acme":
			_, _ = w.Write([]byte(`{
				"id":"client-123",
				"slug":"acme",
				"name":"Acme Robotics",
				"primary_domain":"acme.test"
			}`))
		case "/api/v1/careers-page/client-123/jobs":
			_, _ = w.Write([]byte(`{
				"count":4,
				"next":null,
				"results":[
					{
						"id":"job-1",
						"title":"Software Engineer Intern, Backend Platform",
						"locations":[{"location_type":"REMOTE","name":"United States","location_option":{"display_name":"United States","location_type":"COUNTRY","country":"US"}}],
						"is_published":true,
						"is_sample":false
					},
					{
						"id":"job-2",
						"title":"Early Career ML Engineer",
						"locations":[
							{"location_type":"IN_PERSON","name":"Singapore","location_option":{"display_name":"Singapore","location_type":"COUNTRY","country":"SG"}},
							{"location_type":"REMOTE","name":"Canada","location_option":{"display_name":"Canada","location_type":"COUNTRY","country":"CA"}}
						],
						"is_published":true,
						"is_sample":false
					},
					{"id":"sample","title":"Sample Engineer","locations":[],"is_published":true,"is_sample":true},
					{"id":"draft","title":"Draft Engineer","locations":[],"is_published":false,"is_sample":false}
				]
			}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), DoverMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		URL: server.URL + "/jobs/acme",
		Metadata: map[string]string{
			"source_kind": "dover",
		},
	})
	if err != nil {
		t.Fatalf("extract dover: %v", err)
	}
	if requests["/api/v1/careers-page-slug/acme"] != 1 || requests["/api/v1/careers-page/client-123/jobs"] != 1 {
		t.Fatalf("requests = %#v, want slug lookup and jobs lookup", requests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.8 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two Dover jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "dover:job-1" || first.Company != "Acme Robotics" || first.Level != "internship" || first.RoleFamily != "backend" || first.Country != "US" {
		t.Fatalf("first job = %#v, want normalized Dover internship", first)
	}
	if first.ApplyURL != server.URL+"/apply/acme/job-1" || evidenceText(first.Evidence, "ats") != "Dover public careers-page jobs API" {
		t.Fatalf("first apply/evidence = %q/%q, want Dover apply URL and evidence", first.ApplyURL, evidenceText(first.Evidence, "ats"))
	}
	second := result.Jobs[1]
	if second.SourceJobID != "dover:job-2" || second.Level != "early_career" || second.RoleFamily != "ml_ai" || second.Country != "Singapore" {
		t.Fatalf("second job = %#v, want early-career Singapore ML job", second)
	}
}

func TestATSExtractorExtractsMetaCareersGraphQLJobs(t *testing.T) {
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		switch r.URL.Path {
		case "/jobsearch/":
			_, _ = w.Write([]byte(`<html><script>["LSD",[],{"token":"test-lsd"},323]</script></html>`))
		case "/graphql":
			if r.Method != http.MethodPost || r.Header.Get("x-fb-lsd") != "test-lsd" {
				t.Fatalf("graphql method/token = %s/%q", r.Method, r.Header.Get("x-fb-lsd"))
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(r.Form.Get("variables"), "software engineer intern") {
				t.Fatalf("unexpected form: %#v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			switch r.Form.Get("doc_id") {
			case metaCareersSearchDocID:
				_, _ = w.Write([]byte(`{"data":{"job_search_with_featured_jobs_v2":{"all_jobs":[{"id":"123","title":"Software Engineer Intern","locations":["Menlo Park, CA"],"teams":["Software Engineering"],"sub_teams":["Engineering"]},{"id":"456","title":"Research Scientist Intern, AI","locations":["New York, NY"],"teams":["Artificial Intelligence","Internship - Engineering, Tech & Design"],"sub_teams":["Research"]}]}}}`))
			case metaCareersCountDocID:
				_, _ = w.Write([]byte(`{"data":{"job_search_with_featured_jobs_v2":{"job_count":2}}}`))
			default:
				t.Fatalf("unexpected doc id %q", r.Form.Get("doc_id"))
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		URL:  server.URL + "/jobs/?q=software%20engineer%20intern",
		Name: "Meta",
		Metadata: map[string]string{
			"source_kind": "meta_careers",
		},
	})
	if err != nil {
		t.Fatalf("extract Meta: %v", err)
	}
	if requests["/jobsearch/"] != 1 || requests["/graphql"] != 2 || len(result.Jobs) != 2 {
		t.Fatalf("requests/jobs = %#v/%d", requests, len(result.Jobs))
	}
	if result.Jobs[0].SourceJobID != "meta:123" || result.Jobs[0].ApplyURL != server.URL+"/profile/job_details/123" {
		t.Fatalf("first job = %#v", result.Jobs[0])
	}
	if result.Diagnostics["completeness_status"] != "complete" || result.Diagnostics["total_available"] != "2" {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestATSExtractorExtractsAllOptiverPages(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/en/api/v1/jobs" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		requests++
		offset, _ := strconv.Atoi(r.URL.Query().Get("from"))
		count := optiverPageSize
		if offset+count > 18 {
			count = 18 - offset
		}
		items := make([]optiverJob, 0, count)
		for i := 0; i < count; i++ {
			id := offset + i + 1
			items = append(items, optiverJob{
				Title:       "Software Engineer Intern " + strconv.Itoa(id),
				Location:    "Chicago",
				Experience:  "Internship",
				Domain:      "Technology",
				Href:        "/join-us/jobs/technology/chicago/role-" + strconv.Itoa(id) + "/",
				ComponentID: id,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(optiverJobsResponse{Items: items, TotalCount: 18})
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client()})
	result, err := extractor.Extract(context.Background(), Source{
		URL:  server.URL + "/join-us/jobs/",
		Name: "Optiver",
		Metadata: map[string]string{
			"source_kind": "optiver_careers",
		},
	})
	if err != nil {
		t.Fatalf("extract Optiver: %v", err)
	}
	if requests != 2 || len(result.Jobs) != 18 {
		t.Fatalf("requests/jobs = %d/%d, want 2/18", requests, len(result.Jobs))
	}
	if result.Jobs[0].SourceJobID != "optiver:1" || result.Jobs[0].ApplyURL != server.URL+"/join-us/jobs/technology/chicago/role-1/" {
		t.Fatalf("first job = %#v", result.Jobs[0])
	}
	if result.Diagnostics["completeness_status"] != "complete" || result.Diagnostics["total_available"] != "18" {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestATSExtractorExtractsTrakstarRSSJobs(t *testing.T) {
	requests := map[string]int{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		w.Header().Set("Content-Type", "application/rss+xml")
		switch r.URL.Path {
		case "/jobfeeds/acme":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:job="https://recruiterbox.com/rss/job/">
  <channel>
    <title>Jobs at Acme Robotics</title>
    <link>https://recruiterbox.com/jobfeeds/acme</link>
    <description>Current open positions at Acme Robotics</description>
    <item>
      <title>Software Engineer Intern, Backend Platform</title>
      <link>` + server.URL + `/jobs/fk0abc</link>
      <guid>` + server.URL + `/jobs/fk0abc</guid>
      <description>&lt;div id="job_description"&gt;&lt;p&gt;Build distributed Go services. Applicants must be authorized to work in the U.S.&lt;/p&gt;&lt;/div&gt;&lt;div id="how_to_apply"&gt;&lt;a href="https://acme.recruiterbox.com/jobs/fk0abc/?apply=true"&gt;Apply to this job&lt;/a&gt;&lt;/div&gt;</description>
      <pubDate>Tue, 09 Jun 2026 00:00:00 +0530</pubDate>
      <job:locationCity>New York</job:locationCity>
      <job:locationState>NY</job:locationState>
      <job:locationCountry>US</job:locationCountry>
      <job:positionType>internship</job:positionType>
      <job:team>Engineering</job:team>
    </item>
    <item>
      <title>Software Engineer Intern, Backend Platform</title>
      <link>` + server.URL + `/jobs/fk0abc</link>
      <guid>` + server.URL + `/jobs/fk0abc</guid>
      <description>duplicate should be ignored</description>
    </item>
    <item>
      <title>Early Career ML Engineer</title>
      <link>` + server.URL + `/jobs/fk0ml</link>
      <guid>` + server.URL + `/jobs/fk0ml</guid>
      <description>&lt;h2 id="job_meta"&gt;&lt;p&gt;Location: Singapore,, &lt;/p&gt;&lt;/h2&gt;&lt;div id="job_description"&gt;&lt;p&gt;Work on model evaluation systems.&lt;/p&gt;&lt;/div&gt;</description>
      <pubDate>Mon, 08 Jun 2026 00:00:00 +0530</pubDate>
      <job:positionType>full_time</job:positionType>
      <job:team>AI Platform</job:team>
    </item>
  </channel>
</rss>`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	extractor := NewATSExtractor(ATSOptions{Client: server.Client(), TrakstarMaxJobs: 5})
	result, err := extractor.Extract(context.Background(), Source{
		URL:  server.URL + "/acme",
		Name: "Acme Robotics",
		Metadata: map[string]string{
			"source_kind": "recruiterbox",
		},
	})
	if err != nil {
		t.Fatalf("extract trakstar: %v", err)
	}
	if requests["/jobfeeds/acme"] != 1 {
		t.Fatalf("requests = %#v, want one feed request", requests)
	}
	if result.Strategy != TierATS || result.Confidence < 0.8 || len(result.Jobs) != 2 {
		t.Fatalf("result = %+v, want two Trakstar jobs", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "recruiterbox:acme:fk0abc" || first.Company != "Acme Robotics" || first.Level != "internship" || first.RoleFamily != "backend" || first.Country != "US" {
		t.Fatalf("first job = %#v, want normalized Trakstar internship", first)
	}
	if first.ApplyURL != "https://acme.recruiterbox.com/jobs/fk0abc/?apply=true" || evidenceText(first.Evidence, "ats") != "Trakstar Hire Recruiterbox RSS job feed" {
		t.Fatalf("first apply/evidence = %q/%q, want Trakstar apply URL and evidence", first.ApplyURL, evidenceText(first.Evidence, "ats"))
	}
	if first.PostedAt == nil || first.PostedAt.UTC().Format("2006-01-02") != "2026-06-08" {
		t.Fatalf("first posted_at = %v, want timezone-normalized RSS date", first.PostedAt)
	}
	second := result.Jobs[1]
	if second.SourceJobID != "recruiterbox:acme:fk0ml" || second.Level != "early_career" || second.RoleFamily != "ml_ai" || second.Country != "Singapore" {
		t.Fatalf("second job = %#v, want early-career Singapore ML job", second)
	}
}

func TestPhenomPeopleHTMLHelpers(t *testing.T) {
	page := phenomSearchPageHTML("https://jobs.example/us/en", "ACME123", 1, []string{
		`{"title":"Backend Software Engineer Intern","jobSeqNo":"ACME123P1EXTERNALENUS","country":"United States"}`,
	})
	config, err := phenomPeopleConfigFromHTML(page)
	if err != nil {
		t.Fatalf("config from html: %v", err)
	}
	if config.RefNum != "ACME123" || config.BaseURL != "https://jobs.example/us/en/" {
		t.Fatalf("config = %#v, want ref num and base url", config)
	}
	ddo, err := phenomPeopleDDOFromHTML(page)
	if err != nil {
		t.Fatalf("ddo from html: %v", err)
	}
	data := ddo.refineData()
	if data.TotalHits != 1 || len(data.Hits) != 1 || data.Hits[0].JobSeqNo != "ACME123P1EXTERNALENUS" {
		t.Fatalf("refine data = %#v, want one hit", data)
	}
	nextURL, err := phenomPeopleSearchURL("https://jobs.example/us/en/search-results?keywords=software", 20)
	if err != nil {
		t.Fatalf("search url: %v", err)
	}
	if nextURL != "https://jobs.example/us/en/search-results?from=20&keywords=software&s=1" {
		t.Fatalf("next url = %q, want preserved keywords and Phenom pagination", nextURL)
	}
	if got := phenomPeopleJobURL(config, data.Hits[0]); got != "https://jobs.example/us/en/job/ACME123P1EXTERNALENUS" {
		t.Fatalf("job url = %q, want hosted Phenom job URL", got)
	}

	numericStatusPage := strings.Replace(page, `"status":"success"`, `"status":200`, 1)
	numericStatusDDO, err := phenomPeopleDDOFromHTML(numericStatusPage)
	if err != nil {
		t.Fatalf("ddo with numeric status from html: %v", err)
	}
	if numericStatusDDO.refineData().TotalHits != 1 {
		t.Fatalf("numeric status refine data = %#v, want parsed hits", numericStatusDDO.refineData())
	}

	liveShapePage := `<html><body><script>var phApp = phApp || {"refNum":"SNCOUS","baseUrl":"https://careers.snowflake.com/us/en/","baseDomain":"https://careers.snowflake.com"}; phApp.ddo = {"eagerLoadRefineSearch":{"status":200,"hits":1,"totalHits":1,"data":{"jobs":[{"title":"Software Engineer Intern","jobSeqNo":"SNCOUSP1EXTERNALENUS","location":"Warsaw, Poland"}]}}};</script></body></html>`
	liveShapeDDO, err := phenomPeopleDDOFromHTML(liveShapePage)
	if err != nil {
		t.Fatalf("live-shape ddo from html: %v", err)
	}
	liveShapeData := liveShapeDDO.refineData()
	if liveShapeData.TotalHits != 1 || len(liveShapeData.Hits) != 1 || liveShapeData.Hits[0].JobSeqNo != "SNCOUSP1EXTERNALENUS" {
		t.Fatalf("live-shape refine data = %#v, want jobs promoted to hits", liveShapeData)
	}
}

func phenomSearchPageHTML(baseURL string, refNum string, total int, jobs []string) string {
	baseURL = strings.TrimRight(baseURL, "/") + "/"
	return `<html><body><script>var phApp = phApp || {"refNum":"` + refNum + `","locale":"en_us","siteType":"external","baseUrl":"` + baseURL + `","baseDomain":"` + strings.TrimRight(baseURL, "/") + `"}; phApp.ddo = {"eagerLoadRefineSearch":{"status":"success","data":{"totalHits":` + strconv.Itoa(total) + `,"hits":[` + strings.Join(jobs, ",") + `]}}};</script></body></html>`
}

func avatureDetailHTML(title string, location string, businessArea string, description string, applyPath string) string {
	return `<html><head>
		<meta property="og:title" content="` + html.EscapeString(title) + `" />
		<title>` + html.EscapeString(title) + ` - 19001</title>
	</head><body>
		<div class="article__content__view__field">
			<div class="article__content__view__field__label">Location</div>
			<div class="article__content__view__field__value">` + html.EscapeString(location) + `</div>
		</div>
		<div class="article__content__view__field">
			<div class="article__content__view__field__label">Business Area</div>
			<div class="article__content__view__field__value">` + html.EscapeString(businessArea) + `</div>
		</div>
		<div class="article__content__view__field field--rich-text">
			<div class="article__content__view__field__value"><p>` + html.EscapeString(description) + `</p></div>
		</div>
		<a class="button button--primary" href="` + html.EscapeString(applyPath) + `">Apply Now</a>
	</body></html>`
}

func appleJobsSearchPageHTML(locale string, jobs []string) string {
	payload := `{"loaderData":{"root":{"locale":"` + locale + `","baseUrl":"https://jobs.apple.com"},"search":{"searchResults":[` + strings.Join(jobs, ",") + `],"totalRecords":` + strconv.Itoa(len(jobs)) + `,"requestUrl":"https://jobs.apple.com/` + locale + `/search","queryParams":{"search":"software engineer intern"},"page":1}},"actionData":null,"errors":null}`
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return `<html><body><script nonce="test">window.__staticRouterHydrationData = JSON.parse(` + string(encoded) + `);</script></body></html>`
}

func TestPaylocityURLHelpers(t *testing.T) {
	boardURL := "https://recruiting.paylocity.com/recruiting/jobs/All/feed-guid/Acme-Labs"
	config, err := paylocityConfigFromURL(boardURL)
	if err != nil {
		t.Fatalf("config from board: %v", err)
	}
	if config.FeedID != "feed-guid" || config.CompanySlug != "Acme-Labs" || config.JobID != "" {
		t.Fatalf("config = %#v, want feed board config", config)
	}
	detailURL := "https://recruiting.paylocity.com/recruiting/jobs/Details/1001/Acme-Labs/Backend-Software-Engineer-Intern"
	detailConfig, err := paylocityConfigFromURL(detailURL)
	if err != nil {
		t.Fatalf("config from detail: %v", err)
	}
	if detailConfig.JobID != "1001" || detailConfig.CompanySlug != "Acme-Labs" {
		t.Fatalf("detail config = %#v, want job id and company slug", detailConfig)
	}
	if got := paylocityHostedURL(boardURL, "Details", "1001", "Acme Labs", "Backend Software Engineer Intern"); got != detailURL {
		t.Fatalf("detail url = %q, want %q", got, detailURL)
	}
}

func evidenceText(evidence []Evidence, field string) string {
	for _, item := range evidence {
		if item.Field == field {
			return item.Text
		}
	}
	return ""
}

func TestICIMSURLHelpers(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "search", url: "https://careers-bcore.icims.com/jobs/search?in_iframe=1", want: "https://careers-bcore.icims.com/sitemap.xml"},
		{name: "detail", url: "https://careers-bcore.icims.com/jobs/3112/software-engineer/job", want: "https://careers-bcore.icims.com/sitemap.xml"},
		{name: "sitemap", url: "https://careers-bcore.icims.com/sitemap.xml", want: "https://careers-bcore.icims.com/sitemap.xml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := icimsSitemapURL(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("icimsSitemapURL() = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestPinpointPostingsURLFromSupportedURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "root", url: "https://acme.pinpointhq.com", want: "https://acme.pinpointhq.com/postings.json"},
		{name: "posting", url: "https://acme.pinpointhq.com/en/postings/4e4fb030-ai-platform-intern", want: "https://acme.pinpointhq.com/postings.json"},
		{name: "postings json", url: "https://acme.pinpointhq.com/postings.json", want: "https://acme.pinpointhq.com/postings.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pinpointPostingsURL(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("pinpointPostingsURL() = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestPersonioFeedURLFromSupportedURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "root de", url: "https://acme.jobs.personio.de", want: "https://acme.jobs.personio.de/xml?language=en"},
		{name: "root com", url: "https://acme.jobs.personio.com/", want: "https://acme.jobs.personio.com/xml?language=en"},
		{name: "xml language", url: "https://acme.jobs.personio.de/xml?language=de", want: "https://acme.jobs.personio.de/xml?language=de"},
		{name: "job page", url: "https://acme.jobs.personio.de/job/1834171?language=fr", want: "https://acme.jobs.personio.de/xml?language=fr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := personioFeedURL(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("personioFeedURL() = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestBreezyBoardURLFromSupportedURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "root", url: "https://acme.breezy.hr/", want: "https://acme.breezy.hr/json?verbose=true"},
		{name: "posting", url: "https://acme.breezy.hr/p/b8e6b722f7ed-software-engineering-intern", want: "https://acme.breezy.hr/json?verbose=true"},
		{name: "json", url: "https://acme.breezy.hr/json", want: "https://acme.breezy.hr/json?verbose=true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := breezyBoardURL(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != tt.want {
				t.Fatalf("breezyBoardURL() = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestWorkdayConfigFromSupportedURLs(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		wantTenant string
		wantSite   string
		wantPrefix string
	}{
		{name: "myworkdayjobs root", url: "https://nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite", wantTenant: "nvidia", wantSite: "NVIDIAExternalCareerSite", wantPrefix: "NVIDIAExternalCareerSite"},
		{name: "localized root", url: "https://company.wd5.myworkdayjobs.com/en-US/jobs", wantTenant: "company", wantSite: "jobs", wantPrefix: "en-US/jobs"},
		{name: "myworkdaysite recruiting", url: "https://wd5.myworkdaysite.com/recruiting/datadog/Datadog_Careers", wantTenant: "datadog", wantSite: "Datadog_Careers", wantPrefix: "recruiting/datadog/Datadog_Careers"},
		{name: "cxs endpoint", url: "https://nvidia.wd5.myworkdayjobs.com/wday/cxs/nvidia/NVIDIAExternalCareerSite/jobs", wantTenant: "nvidia", wantSite: "NVIDIAExternalCareerSite", wantPrefix: "NVIDIAExternalCareerSite"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := workdayConfigFromSource(Source{URL: tt.url})
			if err != nil {
				t.Fatal(err)
			}
			if got.Tenant != tt.wantTenant || got.Site != tt.wantSite || got.PublicPathPrefix != tt.wantPrefix {
				t.Fatalf("workdayConfigFromSource() = %#v, want tenant %q site %q prefix %q", got, tt.wantTenant, tt.wantSite, tt.wantPrefix)
			}
		})
	}
}

func TestComeetLocationTextUsesLocationsArray(t *testing.T) {
	location, country := comeetLocationText(comeetLocation{}, []comeetLocation{
		{Name: "London", Country: "GB"},
		{City: "New York", State: "NY", Country: "US", IsRemote: true},
	})

	if location != "London; New York, NY, US or Remote" || country != "UK" {
		t.Fatalf("location = %q, country = %q", location, country)
	}
}

func TestRecruiteeCompanySlugFromSupportedURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "company root", url: "https://acme.recruitee.com", want: "acme"},
		{name: "offer page", url: "https://acme.recruitee.com/o/backend-intern", want: "acme"},
		{name: "offers api", url: "https://acme.recruitee.com/api/offers/", want: "acme"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := recruiteeCompanySlug(tt.url)
			if err != nil {
				t.Fatalf("recruiteeCompanySlug() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("recruiteeCompanySlug(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestWorkableAccountSlugFromSupportedURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "apply host", url: "https://apply.workable.com/acme/", want: "acme"},
		{name: "company subdomain", url: "https://acme.workable.com/jobs/123", want: "acme"},
		{name: "public api", url: "https://www.workable.com/api/accounts/acme?details=true", want: "acme"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := workableAccountSlug(tt.url)
			if err != nil {
				t.Fatalf("workableAccountSlug() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("workableAccountSlug(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestATSExtractorRejectsUnsupportedSource(t *testing.T) {
	extractor := NewATSExtractor(ATSOptions{})
	_, err := extractor.Extract(context.Background(), Source{URL: "https://example.com/careers", Tier: TierATS})
	if err == nil {
		t.Fatal("Extract() error = nil, want unsupported source error")
	}
}

func TestATSExtractorExtractsTaleoJobs(t *testing.T) {
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/careersection/rest/jobboard/searchjobs" {
			t.Fatalf("path = %q, want taleo search path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"requisitionList": [
				{
					"contestNo": "REQ-12345",
					"title": "Software Engineer Intern",
					"cityTown": "Austin",
					"state": "TX",
					"country": "United States",
					"jobLevelLabel": "Individual Contributor",
					"referencePartnerURL": "` + serverURL + `/careersection/2/jobdetail.ftl?job=REQ-12345"
				},
				{
					"contestNo": "REQ-67890",
					"title": "Data Analyst",
					"cityTown": "San Francisco",
					"state": "CA",
					"country": "United States",
					"jobLevelLabel": "Individual Contributor",
					"referencePartnerURL": ""
				}
			]
		}`))
	}))
	defer server.Close()
	serverURL = server.URL

	// Use server.URL as the taleo base (tenant placeholder doesn't matter since we override)
	extractor := NewATSExtractor(ATSOptions{
		Client:       server.Client(),
		TaleoBaseURL: server.URL,
		TaleoMaxJobs: 100,
	})
	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_taleo",
		Name: "Acme Corp",
		URL:  "https://acme.taleo.net/careersection/2/jobsearch.ftl",
		Tier: TierATS,
		Metadata: map[string]string{
			"source_kind": "taleo",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != TierATS || result.Confidence < 0.8 {
		t.Fatalf("result strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.Title != "Software Engineer Intern" {
		t.Fatalf("job.Title = %q, want Software Engineer Intern", job.Title)
	}
	if job.Company != "Acme Corp" {
		t.Fatalf("job.Company = %q, want Acme Corp", job.Company)
	}
	if !strings.Contains(job.Location, "Austin") {
		t.Fatalf("job.Location = %q, want Austin", job.Location)
	}
	if job.SourceJobID != "taleo:acme:REQ-12345" {
		t.Fatalf("job.SourceJobID = %q, want taleo:acme:REQ-12345", job.SourceJobID)
	}
	if len(job.Evidence) == 0 {
		t.Fatal("job evidence should not be empty")
	}
}

func TestTaleoTenantFromURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		{name: "standard taleo subdomain", url: "https://acme.taleo.net/careersection/2/jobsearch.ftl", want: "acme"},
		{name: "enterprise taleo subdomain", url: "https://bigcorp.taleo.net/careersection/4/jobsearch.ftl", want: "bigcorp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := taleoTenantFromURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("taleoTenantFromURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("taleoTenantFromURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestTaleoKindDetectedFromURL(t *testing.T) {
	source := Source{URL: "https://acme.taleo.net/careersection/2/jobsearch.ftl", Tier: TierATS}
	got := atsKind(source)
	if got != "taleo" {
		t.Fatalf("atsKind() = %q, want taleo", got)
	}
}

func paycomPortalHTML(serviceURL string, token string) string {
	libConfig := strconv.Quote(`{"atsPortalMantleServiceUrl":` + strconv.Quote(serviceURL+"/") + `}`)
	return `<html><body><script>
		var configsFromHost = {"sessionJWT":` + strconv.Quote(token) + `,"libConfig":` + libConfig + `};
	</script></body></html>`
}
