package ats

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hwennnn/radar/internal/source/provider"
)

func TestInferLevelUsesTimingWordBoundaries(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{title: "International Software Engineer", want: "unknown"},
		{title: "Internal Tools Engineer", want: "unknown"},
		{title: "Internals Software Engineer", want: "unknown"},
		{title: "Cooperative Systems Engineer", want: "unknown"},
		{title: "Software Engineer Intern", want: "internship"},
		{title: "Software Engineering Internship", want: "internship"},
		{title: "Software Engineer Co-op", want: "internship"},
		{title: "Software Engineer Coop", want: "internship"},
		{title: "New-Grad Software Engineer", want: "new_grad"},
		{title: "Graduate Software Engineer", want: "new_grad"},
		{title: "Entry-Level Software Engineer", want: "early_career"},
		{title: "Early Career Software Engineer", want: "early_career"},
	}

	for _, test := range tests {
		t.Run(test.title, func(t *testing.T) {
			if got := inferLevel(test.title); got != test.want {
				t.Fatalf("inferLevel(%q) = %q, want %q", test.title, got, test.want)
			}
		})
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

func TestEngineExtractsGreenhouseJobs(t *testing.T) {
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

	result, err := New("greenhouse", Options{Client: server.Client(), GreenhouseBaseURL: server.URL + "/v1/boards"}).Extract(context.Background(), provider.Source{
		ID:   "source_greenhouse",
		Name: "Acme",
		URL:  "https://boards.greenhouse.io/acme",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind": "greenhouse",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != provider.TierATS || result.Confidence != 0.94 {
		t.Fatalf("strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "greenhouse:123" || job.Company != "Acme" || job.Level != "internship" || job.Country != "US" || job.RoleFamily != "backend" {
		t.Fatalf("job = %#v, want normalized greenhouse internship", job)
	}
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

func TestEngineExtractsLeverJobs(t *testing.T) {
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

	result, err := New("lever", Options{
		Client:             server.Client(),
		LeverGlobalBaseURL: server.URL + "/v0/postings",
		LeverEuropeBaseURL: server.URL + "/v0/postings",
	}).Extract(context.Background(), provider.Source{
		ID:   "source_lever",
		Name: "Stripe",
		URL:  "https://jobs.lever.co/stripe",
		Tier: provider.TierATS,
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
}

func TestEngineExtractsAshbyJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/posting-api/job-board/Anthropic" {
			t.Fatalf("path = %q, want ashby board path", r.URL.Path)
		}
		if r.URL.Query().Get("includeCompensation") != "true" {
			t.Fatalf("includeCompensation = %q, want true", r.URL.Query().Get("includeCompensation"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
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

	result, err := New("ashby", Options{Client: server.Client(), AshbyBaseURL: server.URL + "/posting-api/job-board"}).Extract(context.Background(), provider.Source{
		ID:   "source_ashby",
		Name: "Anthropic",
		URL:  "https://jobs.ashbyhq.com/Anthropic",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind": "ashby",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want listed jobs only", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "ashby:Anthropic:agent-intern" || job.Company != "Anthropic" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "Singapore" {
		t.Fatalf("job = %#v, want normalized ashby intern", job)
	}
}

func TestEngineExtractsSmartRecruitersJobs(t *testing.T) {
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
						"name": "Software Engineer Intern, Platform",
						"releasedDate": "2026-01-10T10:00:00Z",
						"applyUrl": "https://jobs.smartrecruiters.com/acme/sr-1-platform-intern",
						"company": {"identifier": "acme", "name": "Acme AI"},
						"location": {"city": "Singapore", "countryCode": "sg"},
						"department": {"label": "Engineering"},
						"function": {"label": "Backend"},
						"typeOfEmployment": {"label": "Intern"},
						"jobAd": {"jobDescription": "<p>Build platform services in Go.</p>"}
					}
				]
			}`))
		case "/v1/companies/acme/postings/sr-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id": "sr-1",
				"name": "Software Engineer Intern, Platform",
				"releasedDate": "2026-01-10T10:00:00Z",
				"applyUrl": "https://jobs.smartrecruiters.com/acme/sr-1-detail",
				"company": {"identifier": "acme", "name": "Acme AI"},
				"location": {"city": "Singapore", "countryCode": "sg"},
				"department": {"label": "Engineering"},
				"function": {"label": "Backend"},
				"typeOfEmployment": {"label": "Intern"},
				"jobAd": {"jobDescription": {"text": "<p>Build backend platform services in Go.</p>"}}
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	result, err := New("smartrecruiters", Options{
		Client:                       server.Client(),
		SmartRecruitersBaseURL:       server.URL + "/v1/companies",
		SmartRecruitersDetailMaxJobs: 1,
	}).Extract(context.Background(), provider.Source{
		ID:   "source_smartrecruiters",
		Name: "Acme AI",
		URL:  "https://jobs.smartrecruiters.com/acme",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind": "smartrecruiters",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if result.Strategy != provider.TierATS || result.Confidence != 0.91 {
		t.Fatalf("strategy/confidence = %q %.2f", result.Strategy, result.Confidence)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "smartrecruiters:acme:sr-1" || job.Company != "Acme AI" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "Singapore" {
		t.Fatalf("job = %#v, want normalized SmartRecruiters internship", job)
	}
	if job.ApplyURL != "https://jobs.smartrecruiters.com/acme/sr-1-detail" {
		t.Fatalf("ApplyURL = %q, want detail apply URL", job.ApplyURL)
	}
}

func TestEngineExtractsWorkableJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/acme" {
			t.Fatalf("path = %q, want workable account path", r.URL.Path)
		}
		if r.URL.Query().Get("details") != "true" {
			t.Fatalf("details = %q, want true", r.URL.Query().Get("details"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name": "Acme",
			"jobs": [
				{
					"id": "job-1",
					"shortcode": "AI26",
					"title": "Software Engineer Intern, AI Platform",
					"state": "published",
					"department": "Engineering",
					"description": "<p>Build AI platform services in Go.</p>",
					"requirements": "<p>Internship candidates graduating in 2026.</p>",
					"url": "https://acme.workable.com/jobs/job-1",
					"application_url": "https://acme.workable.com/jobs/job-1/candidates/new",
					"employment_type": "Internship",
					"location": {"city": "Singapore", "countryName": "Singapore"},
					"published_at": "2026-06-20T12:00:00Z"
				},
				{"id": "draft-1", "title": "Draft Engineer", "state": "draft"}
			]
		}`))
	}))
	defer server.Close()

	result, err := New("workable", Options{Client: server.Client(), WorkablePublicBaseURL: server.URL + "/api/accounts"}).Extract(context.Background(), provider.Source{
		ID:   "source_workable",
		Name: "Acme",
		URL:  "https://apply.workable.com/acme/",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind": "workable",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want published jobs only", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.SourceJobID != "workable:acme:AI26" || job.Company != "Acme" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "Singapore" {
		t.Fatalf("job = %#v, want normalized Workable internship", job)
	}
	if job.Location != "Singapore" || job.ApplyURL != "https://acme.workable.com/jobs/job-1/candidates/new" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
}

func TestEngineExtractsWorkableLegacySchemaAliases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/acme" {
			t.Fatalf("path = %q, want workable account path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"name": "Acme",
			"jobs": [
				{
					"id": "legacy-job-1",
					"title": "Platform Engineer",
					"state": "published",
					"url": "https://acme.workable.com/jobs/legacy-job-1",
					"application_url": "https://acme.workable.com/jobs/legacy-job-1/candidates/new",
					"full_description": "<p>Build distributed platform systems.</p>",
					"created_at": "2026-06-14T12:00:00Z",
					"worktype": "Full-time",
					"location": {"city": "Singapore", "country_name": "Singapore"}
				},
				{
					"id": "legacy-job-2",
					"title": "Site Reliability Engineer",
					"state": "published",
					"url": "https://acme.workable.com/jobs/legacy-job-2",
					"application_url": "https://acme.workable.com/jobs/legacy-job-2/candidates/new",
					"location": {"country_code": "SG", "telecommuting": true}
				},
				{
					"id": "legacy-job-3",
					"title": "Data Platform Engineer",
					"state": "published",
					"url": "https://acme.workable.com/jobs/legacy-job-3",
					"application_url": "https://acme.workable.com/jobs/legacy-job-3/candidates/new",
					"updated": "2026-07-01T09:30:00Z",
					"work_type": "Contract",
					"location": {"country_code": "US", "workplace_type": "remote"}
				}
			]
		}`))
	}))
	defer server.Close()

	result, err := New("workable", Options{Client: server.Client(), WorkablePublicBaseURL: server.URL + "/api/accounts"}).Extract(context.Background(), provider.Source{
		ID:   "source_workable_aliases",
		Name: "Acme",
		URL:  "https://apply.workable.com/acme/",
		Tier: provider.TierATS,
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 3 {
		t.Fatalf("jobs = %d, want all schema variants", len(result.Jobs))
	}

	legacy := result.Jobs[0]
	if legacy.Location != "Singapore" || legacy.Country != "Singapore" || legacy.EmploymentType != "Full-time" {
		t.Fatalf("legacy location/country/employment = %q/%q/%q", legacy.Location, legacy.Country, legacy.EmploymentType)
	}
	if legacy.PostedAt == nil || legacy.PostedAt.Format(time.RFC3339) != "2026-06-14T12:00:00Z" {
		t.Fatalf("legacy posted_at = %v, want created_at alias", legacy.PostedAt)
	}
	descriptionFound := false
	for _, evidence := range legacy.Evidence {
		if evidence.Field == "description" && strings.Contains(evidence.Text, "distributed platform systems") {
			descriptionFound = true
		}
	}
	if !descriptionFound {
		t.Fatal("full_description alias should be preserved as evidence")
	}

	telecommute := result.Jobs[1]
	if telecommute.Location != "Remote" || telecommute.Country != "Singapore" {
		t.Fatalf("telecommute location/country = %q/%q, want Remote/Singapore", telecommute.Location, telecommute.Country)
	}

	workplace := result.Jobs[2]
	if workplace.Location != "Remote" || workplace.Country != "US" || workplace.EmploymentType != "contract" {
		t.Fatalf("workplace location/country/employment = %q/%q/%q", workplace.Location, workplace.Country, workplace.EmploymentType)
	}
	if workplace.PostedAt == nil || workplace.PostedAt.Format(time.RFC3339) != "2026-07-01T09:30:00Z" {
		t.Fatalf("workplace posted_at = %v, want updated alias", workplace.PostedAt)
	}
}

func TestEngineExtractsWorkableJobsNetworkSearch(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RawQuery)
		if r.URL.Path != "/api/v1/jobs" {
			t.Fatalf("path = %q, want workable jobs search path", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("pageToken") {
		case "":
			_, _ = w.Write([]byte(`{
				"nextPageToken": "next-page",
				"jobs": [
					{
						"id": "job-1",
						"title": "Software Engineer Intern",
						"state": "published",
						"department": "Engineering",
						"description": "<p>Build Go systems for 2026 interns.</p>",
						"url": "https://jobs.workable.com/view/job-1/software-engineer-intern-at-acme",
						"employmentType": "Internship",
						"locations": ["TELECOMMUTE", "Porto, Portugal"],
						"location": {"city": "Porto", "countryName": "Portugal"},
						"created": "2026-06-20T12:00:00Z",
						"company": {"id": "company-1", "title": "Acme Robotics", "website": "https://acme.test", "url": "https://jobs.workable.com/company/acme"}
					}
				]
			}`))
		case "next-page":
			_, _ = w.Write([]byte(`{
				"jobs": [
					{
						"id": "job-2",
						"title": "New Grad Backend Software Engineer",
						"state": "published",
						"department": "Platform",
						"description": "<p>Build distributed systems and APIs.</p>",
						"url": "https://jobs.workable.com/view/job-2/new-grad-backend-software-engineer-at-deepinfra",
						"employmentType": "Full-time",
						"locations": ["New York, New York, United States"],
						"location": {"city": "New York", "subregion": "New York", "countryName": "United States"},
						"created": "2026-06-21T09:30:00Z",
						"company": {"id": "company-2", "title": "Deep Infra", "website": "https://deepinfra.test", "url": "https://jobs.workable.com/company/deepinfra"}
					}
				]
			}`))
		default:
			t.Fatalf("unexpected page token %q", r.URL.Query().Get("pageToken"))
		}
	}))
	defer server.Close()

	result, err := New("workable_jobs", Options{
		Client:               server.Client(),
		WorkableJobsBaseURL:  server.URL + "/api/v1/jobs",
		WorkableJobsMaxPages: 2,
		WorkableJobsMaxJobs:  5,
	}).Extract(context.Background(), provider.Source{
		ID:   "source_workable_jobs",
		Name: "Workable network",
		URL:  "https://jobs.workable.com/search?query=software%20engineer%20intern",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind": "workable_jobs",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(requests) != 2 || !strings.Contains(requests[1], "pageToken=next-page") {
		t.Fatalf("requests = %#v, want first page plus page token", requests)
	}
	if len(result.Jobs) != 2 || result.Confidence != 0.86 {
		t.Fatalf("result = %+v, want two Workable Jobs postings", result)
	}
	first := result.Jobs[0]
	if first.SourceJobID != "workable_jobs:company-1:job-1" || first.Company != "Acme Robotics" || first.Level != "internship" || first.Country != "Portugal" {
		t.Fatalf("first job = %#v, want normalized remote internship", first)
	}
	second := result.Jobs[1]
	if second.SourceJobID != "workable_jobs:company-2:job-2" || second.Level != "new_grad" || second.RoleFamily != "backend" || second.Country != "US" {
		t.Fatalf("second job = %#v, want normalized new-grad backend job", second)
	}
}

func TestWorkableJobsQuerySupportsLegacyURLShapes(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "query", url: "https://jobs.workable.com/search?query=platform+engineer", want: "platform engineer"},
		{name: "q", url: "https://jobs.workable.com/search?q=backend+engineer", want: "backend engineer"},
		{name: "search", url: "https://jobs.workable.com/search?search=data+engineer", want: "data engineer"},
		{name: "path", url: "https://jobs.workable.com/software-engineer", want: "software-engineer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workableJobsQuery(tt.url); got != tt.want {
				t.Fatalf("workableJobsQuery(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestEngineExtractsRecruiteeJobs(t *testing.T) {
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

	result, err := New("recruitee", Options{
		Client:           server.Client(),
		RecruiteeBaseURL: server.URL + "/api/offers/",
	}).Extract(context.Background(), provider.Source{
		ID:   "source_recruitee",
		Name: "Acme",
		URL:  "https://acme.recruitee.com",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind": "recruitee",
		},
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(result.Jobs) != 1 || result.Confidence != 0.91 {
		t.Fatalf("result = %+v, want only published Recruitee job offers", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "recruitee:acme:backend-intern" || job.Company != "Acme" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Recruitee internship", job)
	}
	if job.Location != "New York, NY, US" || job.ApplyURL != "https://acme.recruitee.com/o/backend-intern" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
}

func TestEngineExtractsComeetJobs(t *testing.T) {
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
			},
			{
				"name": "Internal Ops Role",
				"uid": "internal-1",
				"is_internal": true
			}
		]`))
	}))
	defer server.Close()

	result, err := New("comeet", Options{
		Client:        server.Client(),
		ComeetBaseURL: server.URL + "/careers-api/2.0",
	}).Extract(context.Background(), provider.Source{
		ID:   "source_comeet",
		Name: "MWDN",
		URL:  server.URL + "/careers-api/2.0/company/61.005/positions?token=tok_123",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind": "comeet",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Jobs) != 1 || result.Confidence != 0.91 {
		t.Fatalf("result = %+v, want one public Comeet job", result)
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

func TestEngineExtractsBambooHRJobs(t *testing.T) {
	serverURL := ""
	detailRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/careers/list":
			_, _ = w.Write([]byte(`{
				"result": [
					{
						"id": "99",
						"jobOpeningName": "Software Engineer Intern, Backend Platform - Summer 2026",
						"departmentLabel": "Engineering",
						"employmentStatusLabel": "Internship",
						"location": {"city": "New York", "state": "NY", "addressCountry": "United States"},
						"atsLocation": {"country": "United States", "state": "NY", "city": "New York"},
						"isRemote": false
					},
					{
						"id": "100",
						"jobOpeningName": "Office Coordinator",
						"departmentLabel": "Operations"
					}
				]
			}`))
		case "/careers/99/detail":
			detailRequests++
			_, _ = w.Write([]byte(`{
				"result": {
					"jobOpening": {
						"id": "99",
						"jobOpeningShareUrl": "` + serverURL + `/careers/99",
						"jobOpeningName": "Software Engineer Intern, Backend Platform - Summer 2026",
						"jobOpeningStatus": "Open",
						"departmentLabel": "Engineering",
						"employmentStatusLabel": "Internship",
						"location": {"city": "New York", "state": "NY", "addressCountry": "United States"},
						"atsLocation": {"country": "United States", "state": "NY", "city": "New York"},
						"description": "<p>Build distributed Go services for job intelligence workflows.</p>",
						"compensation": "$30/hr",
						"datePosted": "2026-06-10",
						"minimumExperience": "Entry-level"
					}
				}
			}`))
		case "/careers/100/detail":
			detailRequests++
			_, _ = w.Write([]byte(`{
				"result": {
					"jobOpening": {
						"id": "100",
						"jobOpeningName": "Office Coordinator",
						"jobOpeningStatus": "Closed"
					}
				}
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	result, err := New("bamboohr", Options{Client: server.Client(), BambooHRMaxJobs: 5}).Extract(context.Background(), provider.Source{
		ID:   "source_bamboohr",
		Name: "Acme Labs",
		URL:  server.URL + "/careers",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind": "bamboohr",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if detailRequests != 2 {
		t.Fatalf("detailRequests = %d, want 2", detailRequests)
	}
	if len(result.Jobs) != 1 || result.Confidence != 0.86 {
		t.Fatalf("result = %+v, want one open BambooHR job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "bamboohr:127-0-0-1:99" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized BambooHR internship", job)
	}
	if job.Location != "New York, NY, US" || job.ApplyURL != server.URL+"/careers/99" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-10" {
		t.Fatalf("posted_at = %v, want 2026-06-10", job.PostedAt)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "distributed Go services") {
		t.Fatalf("evidence = %#v, want BambooHR description evidence", job.Evidence)
	}
}

func TestEngineExtractsICIMSJobs(t *testing.T) {
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
					<a href="` + serverURL + `/jobs/3112/software-engineer-intern-backend-platform/job?mode=apply&amp;apply=yes&amp;in_iframe=1">Apply for this job online</a>
				</body></html>`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	result, err := New("icims", Options{Client: server.Client(), ICIMSMaxJobs: 5}).Extract(context.Background(), provider.Source{
		ID:   "source_icims",
		URL:  server.URL + "/jobs/search?in_iframe=1",
		Tier: provider.TierATS,
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
	if len(result.Jobs) != 1 || result.Confidence != 0.86 {
		t.Fatalf("result = %+v, want one iCIMS job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "icims:3112" || job.Company != "Bridge Core" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized iCIMS internship", job)
	}
	if job.Location != "Chantilly, VA, US" || !strings.Contains(job.ApplyURL, "mode=apply") {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-22" {
		t.Fatalf("posted_at = %v, want 2026-06-22", job.PostedAt)
	}
	foundDescription := false
	for _, evidence := range job.Evidence {
		if evidence.Field == "description" && strings.Contains(evidence.Text, "distributed Go services") {
			foundDescription = true
		}
	}
	if !foundDescription {
		t.Fatalf("evidence = %#v, want iCIMS description evidence", job.Evidence)
	}
}

func TestEngineExtractsPersonioJobs(t *testing.T) {
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
		<additionalOffices><office>Berlin</office></additionalOffices>
		<department>Product and Tech</department>
		<recruitingCategory>Engineering</recruitingCategory>
		<name>Software Engineering Intern, AI Platform - Summer 2026</name>
		<jobDescriptions>
			<jobDescription><name>The Role</name><value><![CDATA[<p>Build AI platform services for 2026 internship candidates.</p>]]></value></jobDescription>
			<jobDescription><name>What you need</name><value><![CDATA[<ul><li>Go, React, and distributed systems interest.</li></ul>]]></value></jobDescription>
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

	result, err := New("personio", Options{Client: server.Client()}).Extract(context.Background(), provider.Source{
		ID:   "source_personio",
		Name: "Acme AI",
		URL:  server.URL + "/careers",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind": "personio",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Jobs) != 1 || result.Confidence != 0.9 {
		t.Fatalf("result = %+v, want one Personio job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "personio:1834171" || job.Company != "Acme AI" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "UK" {
		t.Fatalf("job = %#v, want normalized Personio internship", job)
	}
	if job.Location != "London; Berlin" || job.ApplyURL != server.URL+"/job/1834171?language=en" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-19" {
		t.Fatalf("posted_at = %v, want 2026-06-19", job.PostedAt)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "Build AI platform services") {
		t.Fatalf("evidence = %#v, want Personio description evidence", job.Evidence)
	}
}

func TestEngineExtractsBreezyJobs(t *testing.T) {
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
				"location": {"country": {"name": "United States", "id": "US"}, "city": "San Francisco", "primary": true, "is_remote": false, "name": "San Francisco, CA, US"},
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

	result, err := New("breezy", Options{Client: server.Client()}).Extract(context.Background(), provider.Source{
		ID:   "source_breezy",
		Name: "Acme AI",
		URL:  server.URL + "/",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind": "breezy",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Jobs) != 1 || result.Confidence != 0.89 {
		t.Fatalf("result = %+v, want one Breezy job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "breezy:acme:b8e6b722f7ed" || job.Company != "Acme AI" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Breezy internship", job)
	}
	if job.Location != "San Francisco, CA, US; Remote US" || job.ApplyURL != "https://acme.breezy.hr/p/b8e6b722f7ed-software-engineering-intern-ai-platform" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-18" {
		t.Fatalf("posted_at = %v, want 2026-06-18", job.PostedAt)
	}
	if len(job.Evidence) < 2 || !strings.Contains(job.Evidence[1].Text, "Build AI infrastructure") {
		t.Fatalf("evidence = %#v, want Breezy description evidence", job.Evidence)
	}
}

func TestEngineExtractsPinpointJobs(t *testing.T) {
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
					"location": {"id": "17640", "city": "London", "name": "London", "province": "England"}
				}
			]
		}`))
	}))
	defer server.Close()

	result, err := New("pinpoint", Options{Client: server.Client()}).Extract(context.Background(), provider.Source{
		ID:   "source_pinpoint",
		Name: "Acme AI",
		URL:  server.URL + "/jobs",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind": "pinpoint",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Jobs) != 1 || result.Confidence != 0.89 {
		t.Fatalf("result = %+v, want one Pinpoint job", result)
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

func TestEngineExtractsTeamtailorJobs(t *testing.T) {
	requestedDetails := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><body>
					<a href="/jobs/7847431-software-engineering-intern-backend-platform?utm=ignored">Software Engineering Intern</a>
					<a href="/jobs/7847431-software-engineering-intern-backend-platform">Duplicate</a>
				</body></html>`))
		case "/jobs/7847431-software-engineering-intern-backend-platform":
			requestedDetails++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><body>
					<main data-careersite--jobs--form-overlay-job-application-url-value="` + serverURL + `/jobs/7847431-software-engineering-intern-backend-platform/applications/new">
						<script type="application/ld+json">{
							"@context": "http://schema.org/",
							"@type": "JobPosting",
							"title": "Software Engineering Intern, Backend Platform",
							"description": "<p>Build backend platform systems for financial data workflows.</p>",
							"identifier": {"@type": "PropertyValue", "name": "Flanks", "value": "7847431"},
							"datePosted": "2026-06-18",
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

	result, err := New("teamtailor", Options{Client: server.Client(), TeamtailorMaxJobs: 5}).Extract(context.Background(), provider.Source{
		ID:   "source_teamtailor",
		Name: "Flanks",
		URL:  server.URL + "/jobs",
		Tier: provider.TierATS,
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
	if len(result.Jobs) != 1 || result.Confidence != 0.86 {
		t.Fatalf("result = %+v, want one Teamtailor job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "teamtailor:127-0-0-1:7847431" || job.Company != "Flanks" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "Spain" {
		t.Fatalf("job = %#v, want normalized Teamtailor internship", job)
	}
	if job.Location != "Barcelona, Catalunya, Spain" || job.ApplyURL != server.URL+"/jobs/7847431-software-engineering-intern-backend-platform/applications/new" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-18" {
		t.Fatalf("posted_at = %v, want 2026-06-18", job.PostedAt)
	}
	foundDescription := false
	for _, evidence := range job.Evidence {
		if evidence.Field == "description" && strings.Contains(evidence.Text, "backend platform systems") {
			foundDescription = true
		}
	}
	if !foundDescription {
		t.Fatalf("evidence = %#v, want Teamtailor description evidence", job.Evidence)
	}
}

func TestEngineExtractsJobviteJobs(t *testing.T) {
	requestedDetails := 0
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/jobs":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><body>
					<a href="/acme/job/oMl123?nl=1">Machine Learning Engineer Intern - Fall 2026</a>
					<a href="/acme/job/oMl123">Duplicate</a>
				</body></html>`))
		case "/acme/job/oMl123":
			requestedDetails++
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
				<html><head>
					<script type="text/javascript">function getCompanyName() { return 'Acme Labs'; }</script>
				</head><body>
					<script type="application/ld+json">{
						"@context": "http://schema.org",
						"@type": "JobPosting",
						"datePosted": "2026-06-17",
						"description": "<p>Build machine learning ranking systems and Go services for job intelligence.</p>",
						"employmentType": "Intern",
						"hiringOrganization": "Acme Labs",
						"identifier": "oMl123",
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

	result, err := New("jobvite", Options{Client: server.Client(), JobviteMaxJobs: 5}).Extract(context.Background(), provider.Source{
		ID:   "source_jobvite",
		Name: "Acme Labs",
		URL:  server.URL + "/acme/jobs",
		Tier: provider.TierATS,
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
	if len(result.Jobs) != 1 || result.Confidence != 0.85 {
		t.Fatalf("result = %+v, want one Jobvite job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "jobvite:acme:oMl123" || job.Company != "Acme Labs" || job.Level != "internship" || job.RoleFamily != "ml_ai" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Jobvite internship", job)
	}
	if job.Location != "Sunnyvale, CA, US" || job.ApplyURL != server.URL+"/acme/job/oMl123/apply" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-17" {
		t.Fatalf("posted_at = %v, want 2026-06-17", job.PostedAt)
	}
	foundDescription := false
	for _, evidence := range job.Evidence {
		if evidence.Field == "description" && strings.Contains(evidence.Text, "machine learning ranking systems") {
			foundDescription = true
		}
	}
	if !foundDescription {
		t.Fatalf("evidence = %#v, want Jobvite description evidence", job.Evidence)
	}
}

func TestEngineExtractsOracleRecruitingJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hcmRestApi/resources/latest/recruitingCEJobRequisitions":
			if !strings.Contains(r.URL.Query().Get("finder"), "siteNumber=CX_1") {
				t.Fatalf("finder = %q, want siteNumber", r.URL.Query().Get("finder"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"items": [{
					"TotalJobsCount": 1,
					"SiteNumber": "CX_1",
					"requisitionList": [{
						"Id": "REQ-2026-001",
						"Title": "Software Engineer Intern, Cloud Platform - Summer 2026",
						"PostedDate": "2026-06-19T12:00:00+00:00",
						"PostingEndDate": "2026-07-31",
						"PrimaryLocationCountry": "United States",
						"PrimaryLocation": "Austin, TX, United States",
						"ShortDescriptionStr": "Build cloud platform services in Go for early-career engineering candidates.",
						"ExternalResponsibilitiesStr": "Design distributed systems and internal developer tooling.",
						"ExternalQualificationsStr": "Experience with Go, Linux, and cloud infrastructure.",
						"JobFamily": "Engineering",
						"JobFunction": "Software Engineering",
						"WorkerType": "Intern",
						"JobSchedule": "Full time",
						"WorkplaceType": "Hybrid",
						"Organization": "Oracle Cloud Infrastructure",
						"LegalEmployer": "Oracle"
					}]
				}],
				"count": 1,
				"hasMore": false,
				"limit": 5,
				"offset": 0
			}`))
		case "/hcmUI/CandidateExperience/en/sites/CX_1/job/REQ-2026-001":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head>
				<meta property="og:title" content="Software Engineer Intern, Cloud Platform - Summer 2026">
				<meta property="og:description" content="Build OCI platform systems with Go, Kubernetes, and observability tooling.">
				<meta property="og:site_name" content="Oracle Cloud">
			</head><body>Oracle recruiting detail</body></html>`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	result, err := New("oracle_recruiting", Options{
		Client:                   server.Client(),
		OracleRecruitingPageSize: 5,
		OracleRecruitingMaxPages: 1,
		OracleRecruitingMaxJobs:  5,
	}).Extract(context.Background(), provider.Source{
		ID:   "source_oracle",
		Name: "Oracle Cloud",
		URL:  server.URL + "/hcmUI/CandidateExperience/en/sites/CX_1?keyword=software%20engineer%20intern",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind": "oracle_recruiting",
		},
	})
	if err != nil {
		t.Fatalf("Extract oracle recruiting: %v", err)
	}
	if len(result.Jobs) != 1 || result.Confidence != 0.85 || result.Strategy != provider.TierATS {
		t.Fatalf("result = %+v, want one Oracle Recruiting job", result)
	}
	job := result.Jobs[0]
	if job.SourceJobID != "oracle_recruiting:cx-1:REQ-2026-001" || job.Company != "Oracle Cloud" || job.Level != "internship" || job.RoleFamily != "backend" || job.Country != "US" {
		t.Fatalf("job = %#v, want normalized Oracle Recruiting internship", job)
	}
	if job.Location != "Austin, TX, United States" || job.ApplyURL != server.URL+"/hcmUI/CandidateExperience/en/sites/CX_1/job/REQ-2026-001" {
		t.Fatalf("job location/apply = %q %q", job.Location, job.ApplyURL)
	}
	if job.PostedAt == nil || job.PostedAt.Format(time.DateOnly) != "2026-06-19" {
		t.Fatalf("posted_at = %v, want 2026-06-19", job.PostedAt)
	}
	foundDescription := false
	for _, evidence := range job.Evidence {
		if evidence.Field == "description" && strings.Contains(evidence.Text, "OCI platform systems") {
			foundDescription = true
		}
	}
	if !foundDescription {
		t.Fatalf("evidence = %#v, want Oracle detail description evidence", job.Evidence)
	}
}
