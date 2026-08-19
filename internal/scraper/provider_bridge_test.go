package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hwennnn/radar/internal/provider"
	atsprovider "github.com/hwennnn/radar/internal/provider/ats"
	workdayprovider "github.com/hwennnn/radar/internal/provider/workday"
)

func TestATSProviderBridgeMatchesDirectProviderEngines(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		source  provider.Source
		options func(*httptest.Server) atsprovider.Options
		handler http.HandlerFunc
	}{
		{
			name: "greenhouse",
			kind: "greenhouse",
			source: provider.Source{
				ID:   "source_greenhouse",
				Name: "Acme",
				URL:  "https://boards.greenhouse.io/acme",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "greenhouse",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), GreenhouseBaseURL: server.URL + "/v1/boards"}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/boards/acme/jobs" {
					t.Fatalf("path = %q, want greenhouse jobs path", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jobs":[{"id":123,"title":"Software Engineering Intern, Backend Platform - Summer 2026","updated_at":"2026-02-12T09:30:00Z","location":{"name":"New York, NY, United States"},"absolute_url":"https://boards.greenhouse.io/acme/jobs/123","content":"<p>Build distributed services.</p>","departments":[{"name":"Engineering"}],"offices":[{"name":"New York","location":"New York, NY, United States"}]}]}`))
			},
		},
		{
			name: "lever",
			kind: "lever",
			source: provider.Source{
				ID:   "source_lever",
				Name: "Stripe",
				URL:  "https://jobs.lever.co/stripe",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "lever",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), LeverGlobalBaseURL: server.URL + "/v0/postings", LeverEuropeBaseURL: server.URL + "/v0/postings"}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v0/postings/stripe" {
					t.Fatalf("path = %q, want lever postings path", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[{"id":"lever-1","text":"New Grad Software Engineer, Payments Infrastructure","categories":{"location":"San Francisco, CA or Remote US","commitment":"Full-time","team":"Engineering","department":"Infrastructure","allLocations":["San Francisco, CA","Remote US"]},"country":"US","descriptionPlain":"Build payment infrastructure.","hostedUrl":"https://jobs.lever.co/stripe/lever-1","applyUrl":"https://jobs.lever.co/stripe/lever-1/apply"}]`))
			},
		},
		{
			name: "ashby",
			kind: "ashby",
			source: provider.Source{
				ID:   "source_ashby",
				Name: "Anthropic",
				URL:  "https://jobs.ashbyhq.com/Anthropic",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "ashby",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), AshbyBaseURL: server.URL + "/posting-api/job-board"}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/posting-api/job-board/Anthropic" {
					t.Fatalf("path = %q, want ashby board path", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jobs":[{"title":"Software Engineer Intern, AI Agents","location":"Singapore","secondaryLocations":[{"location":"Hong Kong"}],"department":"Engineering","team":"Agent Runtime","isListed":true,"isRemote":false,"workplaceType":"Hybrid","descriptionPlain":"Work on agent runtime systems.","publishedAt":"2026-03-01T12:00:00Z","employmentType":"Intern","jobUrl":"https://jobs.ashbyhq.com/Anthropic/agent-intern","applyUrl":"https://jobs.ashbyhq.com/Anthropic/agent-intern/application"}]}`))
			},
		},
		{
			name: "smartrecruiters",
			kind: "smartrecruiters",
			source: provider.Source{
				ID:   "source_smartrecruiters",
				Name: "Acme AI",
				URL:  "https://jobs.smartrecruiters.com/acme",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "smartrecruiters",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), SmartRecruitersBaseURL: server.URL + "/v1/companies", SmartRecruitersDetailMaxJobs: 1}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/companies/acme/postings":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"limit":100,"offset":0,"totalFound":1,"content":[{"id":"sr-1","name":"Software Engineer Intern, Platform","releasedDate":"2026-01-10T10:00:00Z","applyUrl":"https://jobs.smartrecruiters.com/acme/sr-1-platform-intern","company":{"identifier":"acme","name":"Acme AI"},"location":{"city":"Singapore","countryCode":"sg"},"department":{"label":"Engineering"},"function":{"label":"Backend"},"typeOfEmployment":{"label":"Intern"},"jobAd":{"jobDescription":"Build platform services."}}]}`))
				case "/v1/companies/acme/postings/sr-1":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"sr-1","name":"Software Engineer Intern, Platform","releasedDate":"2026-01-10T10:00:00Z","applyUrl":"https://jobs.smartrecruiters.com/acme/sr-1-detail","company":{"identifier":"acme","name":"Acme AI"},"location":{"city":"Singapore","countryCode":"sg"},"department":{"label":"Engineering"},"function":{"label":"Backend"},"typeOfEmployment":{"label":"Intern"},"jobAd":{"jobDescription":{"text":"Build backend platform services."}}}`))
				default:
					t.Fatalf("unexpected path %q", r.URL.Path)
				}
			},
		},
		{
			name: "workable",
			kind: "workable",
			source: provider.Source{
				ID:   "source_workable",
				Name: "Acme",
				URL:  "https://apply.workable.com/acme/",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "workable",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), WorkablePublicBaseURL: server.URL + "/api/accounts"}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/accounts/acme" {
					t.Fatalf("path = %q, want workable account path", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"name":"Acme","jobs":[{"id":"job-1","shortcode":"AI26","title":"Software Engineer Intern, AI Platform","state":"published","department":"Engineering","description":"<p>Build AI platform services in Go.</p>","requirements":"<p>Internship candidates graduating in 2026.</p>","url":"https://acme.workable.com/jobs/job-1","application_url":"https://acme.workable.com/jobs/job-1/candidates/new","employment_type":"Internship","location":{"city":"Singapore","countryName":"Singapore"},"published_at":"2026-06-20T12:00:00Z"}]}`))
			},
		},
		{
			name: "workable_jobs",
			kind: "workable_jobs",
			source: provider.Source{
				ID:   "source_workable_jobs",
				Name: "Workable network",
				URL:  "https://jobs.workable.com/search?query=software%20engineer%20intern",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "workable_jobs",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), WorkableJobsBaseURL: server.URL + "/api/v1/jobs", WorkableJobsMaxPages: 2, WorkableJobsMaxJobs: 5}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/jobs" {
					t.Fatalf("path = %q, want workable jobs search path", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Query().Get("pageToken") {
				case "":
					_, _ = w.Write([]byte(`{"nextPageToken":"next-page","jobs":[{"id":"job-1","title":"Software Engineer Intern","state":"published","department":"Engineering","description":"<p>Build Go systems for 2026 interns.</p>","url":"https://jobs.workable.com/view/job-1/software-engineer-intern-at-acme","employmentType":"Internship","locations":["TELECOMMUTE","Porto, Portugal"],"location":{"city":"Porto","countryName":"Portugal"},"created":"2026-06-20T12:00:00Z","company":{"id":"company-1","title":"Acme Robotics","website":"https://acme.test","url":"https://jobs.workable.com/company/acme"}}]}`))
				case "next-page":
					_, _ = w.Write([]byte(`{"jobs":[{"id":"job-2","title":"New Grad Backend Software Engineer","state":"published","department":"Platform","description":"<p>Build distributed systems and APIs.</p>","url":"https://jobs.workable.com/view/job-2/new-grad-backend-software-engineer-at-deepinfra","employmentType":"Full-time","locations":["New York, New York, United States"],"location":{"city":"New York","subregion":"New York","countryName":"United States"},"created":"2026-06-21T09:30:00Z","company":{"id":"company-2","title":"Deep Infra","website":"https://deepinfra.test","url":"https://jobs.workable.com/company/deepinfra"}}]}`))
				default:
					t.Fatalf("unexpected page token %q", r.URL.Query().Get("pageToken"))
				}
			},
		},
		{
			name: "recruitee",
			kind: "recruitee",
			source: provider.Source{
				ID:   "source_recruitee",
				Name: "Acme",
				URL:  "https://acme.recruitee.com",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "recruitee",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), RecruiteeBaseURL: server.URL + "/api/offers/"}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/offers/" {
					t.Fatalf("path = %q, want recruitee offers path", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"offers":[{"id":456,"slug":"backend-intern","title":"Software Engineering Intern, Backend Platform - Summer 2026","kind":"job","status":"published","department":"Engineering","careers_url":"https://acme.recruitee.com/o/backend-intern","description":"<p>Build backend platform services.</p>","requirements":"<p>Internship candidates graduating in 2026.</p>","locations":[{"name":"New York","city":"New York","state":"NY","country_code":"US"}],"published_at":"2026-04-03T12:00:00Z","employment_type":"internship"},{"id":999,"slug":"talent-community","title":"Talent Community","kind":"talent_pool","status":"published","careers_url":"https://acme.recruitee.com/o/talent-community"}]}`))
			},
		},
		{
			name: "comeet",
			kind: "comeet",
			source: provider.Source{
				ID:   "source_comeet",
				Name: "MWDN",
				URL:  "https://www.comeet.co/careers-api/2.0/company/61.005/positions?token=tok_123",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "comeet",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), ComeetBaseURL: server.URL + "/careers-api/2.0"}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/careers-api/2.0/company/61.005/positions" {
					t.Fatalf("path = %q, want Comeet positions path", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[{"name":"Backend Software Engineer Intern, AI Infrastructure","department":"Engineering","uid":"8A.26E-E5.60A","company_name":"MWDN","employment_type":"Internship","experience_level":"Student","url_comeet_hosted_page":"https://www.comeet.com/jobs/mwdn/61.005/backend-software-engineer-intern/8A.26E-E5.60A","url_active_page":"https://jobs.mwdn.com/careers/co/remote/8A.26E/backend-software-engineer-intern/all/","position_url":"https://www.comeet.co/careers-api/2.0/company/61.005/positions/8A.26E-E5.60A?token=tok_123","time_updated":"2026-06-21T15:43:11Z","workplace_type":"Remote","location":{"name":"Remote US","country":"US","city":"New York","state":"NY","is_remote":true},"details":[{"name":"Description","value":"<p>Build backend services and LLM evaluation tooling.</p>","order":1},{"name":"Requirements","value":"<p>Internship candidates graduating in 2026.</p>","order":2}]}]`))
			},
		},
		{
			name: "bamboohr",
			kind: "bamboohr",
			source: provider.Source{
				ID:   "source_bamboohr",
				Name: "Acme Labs",
				URL:  "https://acme.bamboohr.com/careers",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "bamboohr",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), BambooHRMaxJobs: 5}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/careers/list":
					_, _ = w.Write([]byte(`{"result":[{"id":"99","jobOpeningName":"Software Engineer Intern, Backend Platform - Summer 2026","departmentLabel":"Engineering","employmentStatusLabel":"Internship","location":{"city":"New York","state":"NY","addressCountry":"United States"},"atsLocation":{"country":"United States","state":"NY","city":"New York"}}]}`))
				case "/careers/99/detail":
					_, _ = w.Write([]byte(`{"result":{"jobOpening":{"id":"99","jobOpeningShareUrl":"` + serverURLFromRequest(r) + `/careers/99","jobOpeningName":"Software Engineer Intern, Backend Platform - Summer 2026","jobOpeningStatus":"Open","departmentLabel":"Engineering","employmentStatusLabel":"Internship","location":{"city":"New York","state":"NY","addressCountry":"United States"},"atsLocation":{"country":"United States","state":"NY","city":"New York"},"description":"<p>Build distributed Go services for job intelligence workflows.</p>","datePosted":"2026-06-10","minimumExperience":"Entry-level"}}}`))
				default:
					t.Fatalf("unexpected path %q", r.URL.Path)
				}
			},
		},
		{
			name: "breezy",
			kind: "breezy",
			source: provider.Source{
				ID:   "source_breezy",
				Name: "Acme AI",
				URL:  "https://acme.breezy.hr/",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "breezy",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), BreezyMaxJobs: 5}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/json" {
					t.Fatalf("path = %q, want Breezy JSON board path", r.URL.Path)
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
			},
		},
		{
			name: "jobvite",
			kind: "jobvite",
			source: provider.Source{
				ID:   "source_jobvite",
				Name: "Acme Labs",
				URL:  "https://jobs.jobvite.com/acme/jobs",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "jobvite",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), JobviteMaxJobs: 5}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/acme/jobs":
					w.Header().Set("Content-Type", "text/html")
					_, _ = w.Write([]byte(`<!doctype html><html><body><a href="/acme/job/oMl123">Machine Learning Engineer Intern - Fall 2026</a></body></html>`))
				case "/acme/job/oMl123":
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
								"jobLocation": [{"@type":"Place","address":{"@type":"PostalAddress","addressLocality":"Sunnyvale","addressRegion":"CA","addressCountry":"US"}}],
								"title": "Machine Learning Engineer Intern - Fall 2026",
								"url": "` + serverURLFromRequest(r) + `/acme/job/oMl123"
							}</script>
							<a class="jv-button jv-button-primary jv-button-apply" href="/acme/job/oMl123/apply">Apply</a>
						</body></html>`))
				default:
					t.Fatalf("unexpected path %q", r.URL.Path)
				}
			},
		},
		{
			name: "oracle_recruiting",
			kind: "oracle_recruiting",
			source: provider.Source{
				ID:   "source_oracle",
				Name: "Oracle Cloud",
				URL:  "https://eeho.fa.us2.oraclecloud.com/hcmUI/CandidateExperience/en/sites/CX_1?keyword=software%20engineer%20intern",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "oracle_recruiting",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), OracleRecruitingPageSize: 5, OracleRecruitingMaxPages: 1, OracleRecruitingMaxJobs: 5}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/hcmRestApi/resources/latest/recruitingCEJobRequisitions":
					if !strings.Contains(r.URL.Query().Get("finder"), "siteNumber=CX_1") {
						t.Fatalf("finder = %q, want siteNumber", r.URL.Query().Get("finder"))
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"items":[{"TotalJobsCount":1,"SiteNumber":"CX_1","requisitionList":[{"Id":"REQ-2026-001","Title":"Software Engineer Intern, Cloud Platform - Summer 2026","PostedDate":"2026-06-19T12:00:00+00:00","PostingEndDate":"2026-07-31","PrimaryLocationCountry":"United States","PrimaryLocation":"Austin, TX, United States","ShortDescriptionStr":"Build cloud platform services in Go for early-career engineering candidates.","ExternalResponsibilitiesStr":"Design distributed systems and internal developer tooling.","ExternalQualificationsStr":"Experience with Go, Linux, and cloud infrastructure.","JobFamily":"Engineering","JobFunction":"Software Engineering","WorkerType":"Intern","JobSchedule":"Full time","WorkplaceType":"Hybrid","Organization":"Oracle Cloud Infrastructure","LegalEmployer":"Oracle"}]}],"count":1,"hasMore":false,"limit":5,"offset":0}`))
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
			},
		},
		{
			name: "icims",
			kind: "icims",
			source: provider.Source{
				ID:   "source_icims",
				Name: "Bridge Core",
				URL:  "https://careers-bcore.icims.com/jobs/search?in_iframe=1",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "icims",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), ICIMSMaxJobs: 5}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/sitemap.xml":
					w.Header().Set("Content-Type", "application/xml")
					_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
						<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
							<url><loc>` + serverURLFromRequest(r) + `/jobs/3112/software-engineer-intern-backend-platform/job</loc><lastmod>2026-06-22T15:13:30-04:00</lastmod></url>
						</urlset>`))
				case "/jobs/3112/software-engineer-intern-backend-platform/job":
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
								"url": "` + serverURLFromRequest(r) + `/jobs/3112/software-engineer-intern-backend-platform/job",
								"hiringOrganization": {"@type": "Organization", "name": "Bridge Core"},
								"jobLocation": [{"@type":"Place","address":{"@type":"PostalAddress","addressLocality":"Chantilly","addressRegion":"VA","addressCountry":"US"}}]
							}</script>
						</head><body>
							<a href="` + serverURLFromRequest(r) + `/jobs/3112/software-engineer-intern-backend-platform/job?mode=apply&amp;apply=yes&amp;in_iframe=1">Apply for this job online</a>
						</body></html>`))
				default:
					t.Fatalf("unexpected path %q", r.URL.Path)
				}
			},
		},
		{
			name: "personio",
			kind: "personio",
			source: provider.Source{
				ID:   "source_personio",
				Name: "Acme AI",
				URL:  "https://acme.jobs.personio.com/careers",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "personio",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), PersonioMaxJobs: 5}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/xml" {
					t.Fatalf("path = %q, want Personio XML feed path", r.URL.Path)
				}
				w.Header().Set("Content-Type", "text/xml")
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
					<workzag-jobs><position>
						<id>1834171</id>
						<subcompany>Acme AI Ltd.</subcompany>
						<office>London</office>
						<additionalOffices><office>Berlin</office></additionalOffices>
						<department>Product and Tech</department>
						<recruitingCategory>Engineering</recruitingCategory>
						<name>Software Engineering Intern, AI Platform - Summer 2026</name>
						<jobDescriptions>
							<jobDescription><name>The Role</name><value><![CDATA[<p>Build AI platform services for 2026 internship candidates.</p>]]></value></jobDescription>
						</jobDescriptions>
						<employmentType>intern</employmentType>
						<schedule>full-time</schedule>
						<occupation>software_and_web_development</occupation>
						<occupationCategory>it_software</occupationCategory>
						<createdAt>2026-06-19T10:12:30+00:00</createdAt>
					</position></workzag-jobs>`))
			},
		},
		{
			name: "pinpoint",
			kind: "pinpoint",
			source: provider.Source{
				ID:   "source_pinpoint",
				Name: "Acme AI",
				URL:  "https://acme.pinpointhq.com/jobs",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "pinpoint",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), PinpointMaxJobs: 5}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/postings.json" {
					t.Fatalf("path = %q, want Pinpoint postings path", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[{"id":"110550","title":"Software Engineering Intern, AI Platform - Summer 2026","url":"https://acme.pinpointhq.com/en/postings/4e4fb030-ai-platform-intern","description":"<div>Build AI infrastructure and evaluation systems.</div>","key_responsibilities":"<ul><li>Ship Go services.</li></ul>","skills_knowledge_expertise":"<p>Internship candidates graduating in 2026.</p>","employment_type":"internship","employment_type_text":"Internship","workplace_type":"hybrid","workplace_type_text":"Hybrid","location":{"id":"17640","city":"London","name":"London","province":"England"}}]}`))
			},
		},
		{
			name: "teamtailor",
			kind: "teamtailor",
			source: provider.Source{
				ID:   "source_teamtailor",
				Name: "Flanks",
				URL:  "https://flanks.teamtailor.com/jobs",
				Tier: provider.TierATS,
				Metadata: map[string]string{
					"source_kind": "teamtailor",
				},
			},
			options: func(server *httptest.Server) atsprovider.Options {
				return atsprovider.Options{Client: server.Client(), TeamtailorMaxJobs: 5}
			},
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/jobs":
					w.Header().Set("Content-Type", "text/html")
					_, _ = w.Write([]byte(`<!doctype html><html><body><a href="/jobs/7847431-software-engineering-intern-backend-platform">Software Engineering Intern</a></body></html>`))
				case "/jobs/7847431-software-engineering-intern-backend-platform":
					w.Header().Set("Content-Type", "text/html")
					_, _ = w.Write([]byte(`<!doctype html>
						<html><body>
							<main data-careersite--jobs--form-overlay-job-application-url-value="` + serverURLFromRequest(r) + `/jobs/7847431-software-engineering-intern-backend-platform/applications/new">
								<script type="application/ld+json">{
									"@context": "http://schema.org/",
									"@type": "JobPosting",
									"title": "Software Engineering Intern, Backend Platform",
									"description": "<p>Build backend platform systems for financial data workflows.</p>",
									"identifier": {"@type": "PropertyValue", "name": "Flanks", "value": "7847431"},
									"datePosted": "2026-06-18",
									"employmentType": "INTERN",
									"hiringOrganization": {"@type": "Organization", "name": "Flanks"},
									"jobLocation": [{"@type": "Place","address": {"@type": "PostalAddress","addressLocality": "Barcelona","addressRegion": "Catalunya","addressCountry": "ES"}}]
								}</script>
							</main>
						</body></html>`))
				default:
					t.Fatalf("unexpected path %q", r.URL.Path)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			source := tt.source
			if tt.kind == "bamboohr" || tt.kind == "teamtailor" {
				source.URL = server.URL + "/careers"
			}
			if tt.kind == "jobvite" {
				source.URL = server.URL + "/acme/jobs"
			}
			if tt.kind == "breezy" {
				source.URL = server.URL + "/"
			}
			if tt.kind == "oracle_recruiting" {
				source.URL = server.URL + "/hcmUI/CandidateExperience/en/sites/CX_1?keyword=software%20engineer%20intern"
			}
			if tt.kind == "icims" {
				source.URL = server.URL + "/jobs/search?in_iframe=1"
			}
			if tt.kind == "personio" {
				source.URL = server.URL + "/careers"
			}
			if tt.kind == "pinpoint" {
				source.URL = server.URL + "/jobs"
			}
			if tt.kind == "teamtailor" {
				source.URL = server.URL + "/jobs"
			}
			direct, err := atsprovider.New(tt.kind, tt.options(server)).Extract(context.Background(), source)
			if err != nil {
				t.Fatalf("direct provider Extract() error = %v", err)
			}
			bridged, err := NewATSExtractor(ATSOptions{
				Client:                       server.Client(),
				GreenhouseBaseURL:            server.URL + "/v1/boards",
				LeverGlobalBaseURL:           server.URL + "/v0/postings",
				LeverEuropeBaseURL:           server.URL + "/v0/postings",
				AshbyJobBoardBaseURL:         server.URL + "/posting-api/job-board",
				SmartRecruitersBaseURL:       server.URL + "/v1/companies",
				SmartRecruitersDetailMaxJobs: 1,
				WorkablePublicBaseURL:        server.URL + "/api/accounts",
				WorkableJobsBaseURL:          server.URL + "/api/v1/jobs",
				WorkableJobsMaxPages:         2,
				WorkableJobsMaxJobs:          5,
				RecruiteeBaseURL:             server.URL + "/api/offers/",
				ComeetBaseURL:                server.URL + "/careers-api/2.0",
				ICIMSMaxJobs:                 5,
			}).Extract(context.Background(), Source{
				ID:       source.ID,
				Name:     source.Name,
				URL:      source.URL,
				Tier:     TierATS,
				Metadata: source.Metadata,
			})
			if err != nil {
				t.Fatalf("ATS bridge Extract() error = %v", err)
			}
			assertProviderBridgeMatch(t, direct, bridged)
		})
	}
}

func TestWorkdayProviderBridgeMatchesDirectProviderEngine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/wday/cxs/nvidia/NVIDIAExternalCareerSite/jobs":
			var searchReq map[string]any
			if err := json.NewDecoder(r.Body).Decode(&searchReq); err != nil {
				t.Fatal(err)
			}
			if searchReq["searchText"] != "software engineer intern" {
				t.Fatalf("searchText = %#v, want software engineer intern", searchReq["searchText"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"total": 1,
				"jobPostings": [
					{
						"title": "Software Engineering Intern, JAX - Fall 2026",
						"externalPath": "/job/US-CA-Santa-Clara/Software-Engineering-Intern--JAX---Fall-2026_JR2009745",
						"locationsText": "US, CA, Santa Clara",
						"postedOn": "2026-06-01",
						"timeType": "Full time",
						"bulletFields": ["JR2009745"]
					}
				]
			}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "JR2009745"):
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
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	providerSource := provider.Source{
		ID:   "source_workday",
		Name: "NVIDIA",
		URL:  "https://nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite?q=software%20engineer%20intern",
		Tier: provider.TierATS,
		Metadata: map[string]string{
			"source_kind":      "workday",
			"workday_base_url": server.URL,
		},
	}
	scraperSource := Source{
		ID:       providerSource.ID,
		Name:     providerSource.Name,
		URL:      providerSource.URL,
		Tier:     TierATS,
		Metadata: providerSource.Metadata,
	}

	direct, err := workdayprovider.New(workdayprovider.Options{Client: server.Client()}).Extract(context.Background(), providerSource)
	if err != nil {
		t.Fatalf("direct provider Extract() error = %v", err)
	}
	bridged, err := NewATSExtractor(ATSOptions{Client: server.Client()}).Extract(context.Background(), scraperSource)
	if err != nil {
		t.Fatalf("ATS bridge Extract() error = %v", err)
	}
	if len(direct.Jobs) != 1 || len(bridged.Jobs) != 1 {
		t.Fatalf("jobs = direct %d bridged %d, want 1/1", len(direct.Jobs), len(bridged.Jobs))
	}

	directJob := direct.Jobs[0]
	bridgedJob := bridged.Jobs[0]
	if bridgedJob.SourceJobID != directJob.SourceJobID ||
		bridgedJob.Company != directJob.Company ||
		bridgedJob.Title != directJob.Title ||
		bridgedJob.Location != directJob.Location ||
		bridgedJob.Country != directJob.Country ||
		bridgedJob.ApplyURL != directJob.ApplyURL {
		t.Fatalf("bridged job = %#v, direct job = %#v", bridgedJob, directJob)
	}
	if bridged.Strategy != TierATS || bridged.Confidence != direct.Confidence {
		t.Fatalf("bridged strategy/confidence = %q %.2f, direct %.2f", bridged.Strategy, bridged.Confidence, direct.Confidence)
	}
}

func assertProviderBridgeMatch(t *testing.T, direct provider.Result, bridged Result) {
	t.Helper()
	if len(direct.Jobs) != len(bridged.Jobs) {
		t.Fatalf("jobs = direct %d bridged %d", len(direct.Jobs), len(bridged.Jobs))
	}
	for i, directJob := range direct.Jobs {
		bridgedJob := bridged.Jobs[i]
		if bridgedJob.SourceJobID != directJob.SourceJobID ||
			bridgedJob.Company != directJob.Company ||
			bridgedJob.Title != directJob.Title ||
			bridgedJob.Location != directJob.Location ||
			bridgedJob.Country != directJob.Country ||
			bridgedJob.EmploymentType != directJob.EmploymentType ||
			bridgedJob.RoleFamily != directJob.RoleFamily ||
			bridgedJob.ApplyURL != directJob.ApplyURL {
			t.Fatalf("bridged job = %#v, direct job = %#v", bridgedJob, directJob)
		}
	}
	if bridged.Strategy != TierATS || bridged.Confidence != direct.Confidence {
		t.Fatalf("bridged strategy/confidence = %q %.2f, direct %.2f", bridged.Strategy, bridged.Confidence, direct.Confidence)
	}
}

func serverURLFromRequest(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
