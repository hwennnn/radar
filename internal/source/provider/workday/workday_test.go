package workday

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hwennnn/radar/internal/source/provider"
)

func TestEmploymentFromTextUsesTimingWordBoundaries(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{title: "Internal Tools Engineer", want: ""},
		{title: "Internals Software Engineer", want: ""},
		{title: "International Software Engineer", want: ""},
		{title: "Cooperative Systems Engineer", want: ""},
		{title: "Software Engineer Intern", want: "internship"},
		{title: "Software Engineering Internship", want: "internship"},
		{title: "Software Engineer Co-op", want: "internship"},
		{title: "Software Engineer Coop", want: "internship"},
	}

	for _, test := range tests {
		t.Run(test.title, func(t *testing.T) {
			if got := employmentFromText(test.title); got != test.want {
				t.Fatalf("employmentFromText(%q) = %q, want %q", test.title, got, test.want)
			}
		})
	}
}

func TestEngineBoundsSlowDetailsAndKeepsSummaryJobs(t *testing.T) {
	var detailRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/jobs"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"total":3,"jobPostings":[
				{"title":"Software Engineer Intern","externalPath":"/job/intern_REQ-1","locationsText":"New York, NY","postedOn":"2026-08-01","bulletFields":["REQ-1"]},
				{"title":"New Grad Backend Engineer","externalPath":"/job/grad_REQ-2","locationsText":"New York, NY","postedOn":"2026-08-02","bulletFields":["REQ-2"]},
				{"title":"Platform Engineer","externalPath":"/job/platform_REQ-3","locationsText":"New York, NY","postedOn":"2026-08-03","bulletFields":["REQ-3"]}
			]}`))
		case r.Method == http.MethodGet:
			detailRequests.Add(1)
			select {
			case <-r.Context().Done():
				return
			case <-time.After(500 * time.Millisecond):
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jobPostingInfo":{"jobDescription":"late detail"}}`))
			}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	engine := New(Options{Client: server.Client(), DetailMaxJobs: 1, DetailTimeout: 20 * time.Millisecond})
	result, err := engine.Extract(context.Background(), provider.Source{
		ID: "bounded", Name: "Bounded", URL: "https://bounded.wd1.myworkdayjobs.com/Jobs",
		Metadata: map[string]string{"workday_base_url": server.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Jobs) != 3 {
		t.Fatalf("jobs = %d, want all 3 summary jobs", len(result.Jobs))
	}
	if detailRequests.Load() != 1 || result.Diagnostics["detail_attempts"] != "1" || result.Diagnostics["detail_fallbacks"] != "1" {
		t.Fatalf("requests=%d diagnostics=%#v", detailRequests.Load(), result.Diagnostics)
	}
	if result.Jobs[0].ApplyURL == "" || result.Jobs[1].SourceJobID == "" {
		t.Fatalf("summary fallback lost identity/apply URL: %#v", result.Jobs)
	}
}

func TestEngineExtractsWorkdayJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/wday/cxs/nvidia/NVIDIAExternalCareerSite/jobs":
			var searchReq searchRequest
			if err := json.NewDecoder(r.Body).Decode(&searchReq); err != nil {
				t.Fatal(err)
			}
			if searchReq.Limit != 20 || searchReq.Offset != 0 || searchReq.SearchText != "software engineer intern" {
				t.Fatalf("search request = %#v, want bounded openings-style query", searchReq)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"total": 2,
				"jobPostings": [
					{
						"title": "Software Engineering Intern, JAX - Fall 2026",
						"externalPath": "/job/US-CA-Santa-Clara/Software-Engineering-Intern--JAX---Fall-2026_JR2009745",
						"locationsText": "US, CA, Santa Clara",
						"postedOn": "2026-06-01",
						"timeType": "Full time",
						"bulletFields": ["JR2009745"]
					},
					{
						"externalPath": "/job/invalid",
						"bulletFields": ["JR-BROKEN"]
					}
				]
			}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "Software-Engineering-Intern--JAX---Fall-2026_JR2009745"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"jobPostingInfo": {
					"title": "Software Engineering Intern, JAX - Fall 2026",
					"jobReqId": "JR2009745",
					"externalUrl": "https://nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite/job/US-CA-Santa-Clara/Software-Engineering-Intern--JAX---Fall-2026_JR2009745",
					"locationsText": "US, CA, Santa Clara",
					"country": {"descriptor": "United States"},
					"timeType": "Full time",
					"posted": "2026-06-01",
					"canApply": true,
					"jobDescription": "<p>Build distributed systems and JAX tooling for AI workloads.</p>"
				}
			}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/job/invalid"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jobPostingInfo":{"canApply":true}}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	engine := New(Options{Client: server.Client()})
	result, err := engine.Extract(context.Background(), provider.Source{
		ID:   "source_workday",
		Name: "NVIDIA",
		URL:  "https://nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite?q=software%20engineer%20intern",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind":      "workday",
			"workday_base_url": server.URL,
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != provider.TierATS || result.Confidence < 0.8 || result.Diagnostics["provider_engine"] != "workday-provider" {
		t.Fatalf("result metadata = strategy %q confidence %.2f diagnostics %#v", result.Strategy, result.Confidence, result.Diagnostics)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "workday:nvidia:NVIDIAExternalCareerSite:JR2009745" || job.Company != "NVIDIA" || job.Country != "US" {
		t.Fatalf("job identity = %#v, want normalized Workday posting", job)
	}
	if job.RoleFamily != "ml_ai" || job.EmploymentType != "internship" {
		t.Fatalf("job family/employment = %q/%q, want ml_ai/internship", job.RoleFamily, job.EmploymentType)
	}
	if job.ApplyURL == "" || len(job.Evidence) == 0 {
		t.Fatalf("job evidence/apply = %#v", job)
	}
}

func TestConfigFromSourceSupportsKnownWorkdayURLShapes(t *testing.T) {
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
			got, err := ConfigFromSource(provider.Source{URL: tt.url})
			if err != nil {
				t.Fatal(err)
			}
			if got.Tenant != tt.wantTenant || got.Site != tt.wantSite || got.PublicPathPrefix != tt.wantPrefix {
				t.Fatalf("ConfigFromSource() = %#v, want tenant %q site %q prefix %q", got, tt.wantTenant, tt.wantSite, tt.wantPrefix)
			}
		})
	}
}
