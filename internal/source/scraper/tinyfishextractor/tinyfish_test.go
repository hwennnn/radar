package tinyfishextractor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hwennnn/radar/internal/source/tinyfish"
)

func TestTinyFishSearchExtractorSearchesFetchesAndNormalizesJobs(t *testing.T) {
	now := time.Date(2026, 6, 23, 14, 0, 0, 0, time.UTC)
	client := &fakeTinyFishClient{
		search: tinyfish.SearchResponse{
			Results: []tinyfish.SearchResult{
				{
					Title:    "Backend Software Engineer Intern - Careers",
					URL:      "https://jobs.example.com/backend-intern",
					Snippet:  "Summer 2026 software engineering internship in New York.",
					SiteName: "Example Cloud",
				},
				{
					Title:   "Press release",
					URL:     "https://example.com/blog",
					Snippet: "Company news.",
				},
			},
		},
		fetch: tinyfish.FetchResponse{
			Results: []tinyfish.FetchResult{
				{
					URL:      "https://jobs.example.com/backend-intern",
					Title:    "Backend Software Engineer Intern - Example Cloud",
					Markdown: "Backend Software Engineer Intern\n\nNew York, United States\n\nPosted on June 20, 2026. Candidates graduating in 2026 should apply. Build Go services and distributed systems.",
				},
			},
		},
	}
	extractor := NewTinyFishSearchExtractor(client)
	extractor.now = func() time.Time { return now }

	result, err := extractor.Extract(context.Background(), Source{
		ID:   "tinyfish-test",
		Name: "TinyFish test",
		URL:  "tinyfish://search/test",
		Tier: TierSearchDiscovery,
		Metadata: map[string]string{
			"query":    "software engineer intern",
			"location": "US",
		},
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}

	if client.gotSearch.Query != "software engineer intern" || client.gotSearch.Location != "US" {
		t.Fatalf("search request = %#v", client.gotSearch)
	}
	if len(client.gotFetch.URLs) != 1 || client.gotFetch.URLs[0] != "https://jobs.example.com/backend-intern" {
		t.Fatalf("fetch request = %#v", client.gotFetch)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.Company != "Example Cloud" {
		t.Fatalf("company = %q, want Example Cloud", job.Company)
	}
	if job.Level != "internship" {
		t.Fatalf("level = %q, want internship", job.Level)
	}
	if job.Country != "US" {
		t.Fatalf("country = %q, want US", job.Country)
	}
	if job.PostedAt == nil || !job.PostedAt.Equal(time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("posted at = %v, want 2026-06-20", job.PostedAt)
	}
	if job.Strategy != TierSearchDiscovery {
		t.Fatalf("strategy = %q, want %q", job.Strategy, TierSearchDiscovery)
	}
}

func TestTinyFishSearchExtractorReturnsNoJobsWhenResultsAreIrrelevant(t *testing.T) {
	client := &fakeTinyFishClient{
		search: tinyfish.SearchResponse{
			Results: []tinyfish.SearchResult{{Title: "Company blog", URL: "https://example.com/blog", Snippet: "News"}},
		},
	}
	extractor := NewTinyFishSearchExtractor(client)

	_, err := extractor.Extract(context.Background(), Source{
		ID:       "tinyfish-test",
		Name:     "TinyFish test",
		URL:      "tinyfish://search/test",
		Tier:     TierSearchDiscovery,
		Metadata: map[string]string{"query": "software engineer intern"},
	})
	if err != ErrNoJobs {
		t.Fatalf("err = %v, want ErrNoJobs", err)
	}
}

func TestTinyFishSearchExtractorSkipsNoisyDiscoveryResults(t *testing.T) {
	client := &fakeTinyFishClient{
		search: tinyfish.SearchResponse{
			Results: []tinyfish.SearchResult{
				{
					Title:   "128 Software Engineer Intern jobs in Canada",
					URL:     "https://ca.linkedin.com/jobs/software-engineer-intern-jobs",
					Snippet: "Browse software engineer intern jobs in Canada.",
				},
				{
					Title:   "Graduate Software Engineer Jobs (with Salaries)",
					URL:     "https://uk.indeed.com/q-graduate-software-engineer-jobs.html",
					Snippet: "Search result listing page.",
				},
				{
					Title:   "[0 YOE] Software Engineer Intern/International student",
					URL:     "https://www.reddit.com/r/EngineeringResumes/comments/1rdpc5k/0_yoe_software_engineer_interninternational/",
					Snippet: "Resume review discussion.",
				},
				{
					Title:   "GitHub - SimplifyJobs/New-Grad-Positions",
					URL:     "https://github.com/SimplifyJobs/New-Grad-Positions",
					Snippet: "Community list of new grad software engineer jobs.",
				},
				{
					Title:    "Backend Software Engineer Intern - Meridian Robotics",
					URL:      "https://meridian.example/careers/backend-software-engineer-intern",
					Snippet:  "Summer 2026 software engineering internship in Toronto.",
					SiteName: "Meridian Robotics",
				},
			},
		},
		fetch: tinyfish.FetchResponse{
			Results: []tinyfish.FetchResult{
				{
					URL:      "https://meridian.example/careers/backend-software-engineer-intern",
					Title:    "Backend Software Engineer Intern",
					Markdown: "Backend Software Engineer Intern\n\nToronto, Canada\n\nApply now for a 2026 internship building Go services.",
				},
			},
		},
	}
	extractor := NewTinyFishSearchExtractor(client)

	result, err := extractor.Extract(context.Background(), Source{
		ID:       "tinyfish-noisy-results",
		Name:     "TinyFish noisy result filter",
		URL:      "tinyfish://search/canada-early-career-software",
		Tier:     TierSearchDiscovery,
		Metadata: map[string]string{"query": `"software engineer intern" Canada careers jobs`},
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(client.gotFetch.URLs) != 1 || client.gotFetch.URLs[0] != "https://meridian.example/careers/backend-software-engineer-intern" {
		t.Fatalf("fetch urls = %#v, want only direct company posting", client.gotFetch.URLs)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want one direct posting", len(result.Jobs))
	}
	if result.Jobs[0].Company != "Meridian Robotics" || result.Jobs[0].Country != "Canada" {
		t.Fatalf("job = %#v, want normalized company/country", result.Jobs[0])
	}
}

func TestTinyFishSearchExtractorSamplesRejectedFetchedResults(t *testing.T) {
	client := &fakeTinyFishClient{
		search: tinyfish.SearchResponse{
			Results: []tinyfish.SearchResult{
				{
					Title:    "Backend Software Engineer Intern - Nimbus Systems",
					URL:      "https://jobs.example.com/nimbus/backend-software-engineer-intern",
					Snippet:  "Summer 2026 software engineering internship in New York.",
					SiteName: "Nimbus Systems",
				},
				{
					Title:   "Software Engineer Intern - Closed",
					URL:     "https://jobs.example.com/nimbus/software-engineer-intern-closed",
					Snippet: "Software engineering internship in New York.",
				},
			},
		},
		fetch: tinyfish.FetchResponse{
			Results: []tinyfish.FetchResult{
				{
					URL:      "https://jobs.example.com/nimbus/backend-software-engineer-intern",
					Title:    "Backend Software Engineer Intern",
					Markdown: "Backend Software Engineer Intern\n\nNew York, NY, United States\n\nApply now for a Summer 2026 internship building backend platform systems.",
				},
				{
					URL:      "https://jobs.example.com/nimbus/software-engineer-intern-closed",
					Title:    "Software Engineer Intern",
					Markdown: "Software Engineer Intern\n\nNew York, NY, United States\n\nThis job is closed and no longer accepting applications.",
				},
			},
		},
	}
	extractor := NewTinyFishSearchExtractor(client)

	result, err := extractor.Extract(context.Background(), Source{
		ID:       "tinyfish-rejection-samples",
		Name:     "TinyFish rejection samples",
		URL:      "tinyfish://search/rejection-samples",
		Tier:     TierSearchDiscovery,
		Metadata: map[string]string{"query": `"software engineer intern" careers jobs`},
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want one accepted posting", len(result.Jobs))
	}
	assertEvidence(t, result.RawEvidence, "tinyfish_rejection_count", "1")
	foundSample := false
	for _, item := range result.RawEvidence {
		if item.Field == "tinyfish_rejection_sample" &&
			item.URL == "https://jobs.example.com/nimbus/software-engineer-intern-closed" &&
			strings.Contains(item.Text, "closed_or_not_live") {
			foundSample = true
		}
	}
	if !foundSample {
		t.Fatalf("rejection sample with reason not found in %#v", result.RawEvidence)
	}
}

func TestTinyFishSearchExtractorQualityFixtureSet(t *testing.T) {
	raw, err := os.ReadFile("testdata/tinyfish_quality_gate.json")
	if err != nil {
		t.Fatalf("read quality fixture: %v", err)
	}
	var fixture tinyFishQualityFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parse quality fixture: %v", err)
	}
	if fixture.Targets.Precision == 0 {
		fixture.Targets.Precision = 0.95
	}
	if fixture.Targets.GoodRecall == 0 {
		fixture.Targets.GoodRecall = 1
	}
	if fixture.Targets.JunkRejection == 0 {
		fixture.Targets.JunkRejection = 1
	}

	client := &fakeTinyFishClient{
		search: tinyfish.SearchResponse{},
		fetch:  tinyfish.FetchResponse{},
	}
	for _, item := range fixture.Cases {
		client.search.Results = append(client.search.Results, item.Search)
		client.fetch.Results = append(client.fetch.Results, item.Fetch)
	}
	extractor := NewTinyFishSearchExtractor(client)
	extractor.maxResults = len(fixture.Cases)
	if fixture.Now != "" {
		parsed, err := time.Parse(time.RFC3339, fixture.Now)
		if err != nil {
			t.Fatalf("parse fixture now: %v", err)
		}
		extractor.now = func() time.Time { return parsed }
	}

	result, err := extractor.Extract(context.Background(), Source{
		ID:       fixture.Source.ID,
		Name:     fixture.Source.Name,
		URL:      fixture.Source.URL,
		Tier:     TierSearchDiscovery,
		Metadata: fixture.Source.Metadata,
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}

	acceptedByURL := make(map[string]JobPosting, len(result.Jobs))
	for _, job := range result.Jobs {
		acceptedByURL[job.SourceURL] = job
	}

	report := tinyFishQualityReport{
		Fixture: fixture.Name,
		Targets: fixture.Targets,
	}
	for _, item := range fixture.Cases {
		job, accepted := acceptedByURL[item.Fetch.URL]
		decision := tinyFishQualityDecision{
			ID:       item.ID,
			Label:    item.Label,
			URL:      item.Fetch.URL,
			Reason:   item.Reason,
			Accepted: accepted,
		}
		if accepted {
			decision.Title = job.Title
			decision.Company = job.Company
			decision.Location = job.Location
			report.Accepted++
			if item.Label == "good" {
				report.AcceptedGood++
			} else {
				report.AcceptedJunk++
			}
		} else {
			report.Rejected++
			if item.Label == "good" {
				report.RejectedGood++
			} else {
				report.RejectedJunk++
			}
		}
		report.Decisions = append(report.Decisions, decision)
	}

	totalGood := report.AcceptedGood + report.RejectedGood
	totalJunk := report.AcceptedJunk + report.RejectedJunk
	if report.Accepted > 0 {
		report.Precision = float64(report.AcceptedGood) / float64(report.Accepted)
	}
	if totalGood > 0 {
		report.GoodRecall = float64(report.AcceptedGood) / float64(totalGood)
	}
	if totalJunk > 0 {
		report.JunkRejection = float64(report.RejectedJunk) / float64(totalJunk)
	}
	if path := strings.TrimSpace(os.Getenv("RADAR_TINYFISH_QUALITY_REPORT")); path != "" {
		if err := writeTinyFishQualityReport(path, report); err != nil {
			t.Fatalf("write quality report: %v", err)
		}
	}

	if report.Precision < fixture.Targets.Precision {
		t.Fatalf("precision = %.3f, want >= %.3f; report = %#v", report.Precision, fixture.Targets.Precision, report.Decisions)
	}
	if report.GoodRecall < fixture.Targets.GoodRecall {
		t.Fatalf("good recall = %.3f, want >= %.3f; report = %#v", report.GoodRecall, fixture.Targets.GoodRecall, report.Decisions)
	}
	if report.JunkRejection < fixture.Targets.JunkRejection {
		t.Fatalf("junk rejection = %.3f, want >= %.3f; report = %#v", report.JunkRejection, fixture.Targets.JunkRejection, report.Decisions)
	}
}

func writeTinyFishQualityReport(path string, report tinyFishQualityReport) error {
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(payload, '\n'), 0o644)
}

func TestTinyFishSearchExtractorKeepsScopedSearchResultsOnSourceSite(t *testing.T) {
	client := &fakeTinyFishClient{
		search: tinyfish.SearchResponse{
			Results: []tinyfish.SearchResult{
				{
					Title:   "Software Engineer Intern - Salesforce",
					URL:     "https://careers.salesforce.com/en/jobs/jr-123/software-engineer-intern",
					Snippet: "Software engineering internship for 2026 graduates.",
				},
				{
					Title:   "Software Engineer Intern - Aggregated copy",
					URL:     "https://mirror.example/jobs/salesforce-software-engineer-intern",
					Snippet: "Software engineering internship copied from Salesforce careers.",
				},
			},
		},
		fetch: tinyfish.FetchResponse{
			Results: []tinyfish.FetchResult{
				{
					URL:      "https://careers.salesforce.com/en/jobs/jr-123/software-engineer-intern",
					Title:    "Software Engineer Intern",
					Markdown: "Software Engineer Intern\n\nSan Francisco, United States\n\nFutureforce 2026 internship for software engineers.",
				},
			},
		},
	}
	extractor := NewTinyFishSearchExtractor(client)

	result, err := extractor.Extract(context.Background(), Source{
		ID:   "salesforce-official",
		Name: "Salesforce",
		URL:  "https://careers.salesforce.com/en/jobs/?search=software%20engineer%20intern",
		Tier: TierSearchDiscovery,
		Metadata: map[string]string{
			"source_kind": "salesforce_careers",
		},
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(client.gotFetch.URLs) != 1 || client.gotFetch.URLs[0] != "https://careers.salesforce.com/en/jobs/jr-123/software-engineer-intern" {
		t.Fatalf("fetch urls = %#v, want only scoped official result", client.gotFetch.URLs)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want one scoped official posting", len(result.Jobs))
	}
}

func TestTinyFishMarketSearchRejectsAggregatorResults(t *testing.T) {
	source := Source{
		ID:   "market-software-intern-2027",
		Name: "Market discovery",
		URL:  "tinyfish://search/market-software-intern-2027?query=software+engineer+intern+2027",
		Tier: TierSearchDiscovery,
		Metadata: map[string]string{
			"source_kind": "market_search",
		},
	}
	results := []tinyfish.SearchResult{
		{Title: "Software Engineering Internship 2027", URL: "https://interninsider.me/internships/acme/123", Snippet: "Apply for this software engineering internship"},
		{Title: "Software Engineering Internship", URL: "https://notify.careers/postings/123", Snippet: "Software intern opening"},
		{Title: "Software Engineering Intern", URL: "https://www.swiftcruit.ai/jobs/software-engineer-intern-2027-graduates-31221", Snippet: "Software intern opening"},
		{Title: "Software Engineer Intern 2027", URL: "https://expatjobboard.com/jobs/15721", Snippet: "IMC software intern opening"},
		{Title: "Software Engineer Intern", URL: "https://startup.jobs/software-engineer-intern-123", Snippet: "Software intern opening"},
		{Title: "Machine Learning Engineer Intern", URL: "https://en.wizbii.com/company/acme/job/ml-intern", Snippet: "AI intern opening"},
		{Title: "Software Engineer Intern", URL: "https://www.remoterocketship.com/jobs/software-engineer/internships", Snippet: "Software intern opening"},
		{Title: "Software Engineer Intern 2027", URL: "https://careers.airwallex.com/job/123/software-engineer-intern-2027", Snippet: "Apply for the internship"},
		{Title: "2027 Early Career Software Engineer", URL: "https://job-boards.greenhouse.io/andurilindustries/jobs/123", Snippet: "New grad software engineer role"},
	}
	filtered := filterSearchResultsForSource(source, results, 5)
	if len(filtered) != 2 {
		t.Fatalf("filtered results = %#v, want two employer/ATS pages", filtered)
	}
	for _, result := range filtered {
		if blockedMarketSearchAggregator(result.URL) {
			t.Fatalf("blocked aggregator survived filtering: %#v", result)
		}
	}
}

func TestTinyFishMarketSearchUsesDeeperBoundedResultSet(t *testing.T) {
	market := Source{Metadata: map[string]string{"source_kind": "market_search"}}
	if got := tinyFishResultLimit(market, defaultTinyFishMaxResults); got != defaultTinyFishMarketMaxResults {
		t.Fatalf("market result limit = %d, want %d", got, defaultTinyFishMarketMaxResults)
	}
	routine := Source{Metadata: map[string]string{"source_kind": "tinyfish_search"}}
	if got := tinyFishResultLimit(routine, defaultTinyFishMaxResults); got != defaultTinyFishMaxResults {
		t.Fatalf("routine result limit = %d, want %d", got, defaultTinyFishMaxResults)
	}
	if got := tinyFishResultLimit(market, 12); got != 12 {
		t.Fatalf("explicit larger market limit = %d, want 12", got)
	}
}

func TestTinyFishOfficialCareersFallbackScopesEarlyCareerSearch(t *testing.T) {
	source := Source{
		ID: "auto-aumovio-official", Name: "Aumovio", URL: "https://jobs.aumovio.com",
		Tier: TierSearchDiscovery, Metadata: map[string]string{"source_kind": "official_careers"},
	}
	query := searchQueryForSource(source)
	for _, required := range []string{"site:jobs.aumovio.com", `"Aumovio"`, `"software engineer intern"`, `"new grad software engineer"`} {
		if !strings.Contains(query, required) {
			t.Fatalf("official careers query %q missing %q", query, required)
		}
	}
}

func TestTinyFishMarketSearchFallsBackToTopResultsAfterFetchFailure(t *testing.T) {
	results := make([]tinyfish.SearchResult, 0, defaultTinyFishMarketMaxResults)
	for index := 0; index < defaultTinyFishMarketMaxResults; index++ {
		results = append(results, tinyfish.SearchResult{
			Title:    "Software Engineer Intern 2027",
			URL:      fmt.Sprintf("https://jobs.example.com/intern-%d", index),
			Snippet:  "Software engineering internship in New York, United States.",
			SiteName: "Example AI",
		})
	}
	client := &fakeTinyFishClient{
		search:      tinyfish.SearchResponse{Results: results},
		fetchErrors: []error{errors.New("full fetch timeout"), nil},
		fetchResponses: []tinyfish.FetchResponse{{}, {Results: []tinyfish.FetchResult{{
			URL: results[0].URL, Title: results[0].Title,
			Markdown: "Software Engineer Intern 2027\nNew York, United States\nBuild backend systems.",
		}}}},
	}
	extractor := NewTinyFishSearchExtractor(client)
	result, err := extractor.Extract(context.Background(), Source{
		ID: "market-fallback", Name: "Market discovery", URL: "tinyfish://search/fallback",
		Tier: TierSearchDiscovery, Metadata: map[string]string{"source_kind": "market_search", "query": "software engineer intern 2027"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.gotFetches) != 2 || len(client.gotFetches[0].URLs) != defaultTinyFishMarketMaxResults || len(client.gotFetches[1].URLs) != defaultTinyFishMarketFallbackResults {
		t.Fatalf("fetch attempts = %#v", client.gotFetches)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("fallback jobs = %#v", result.Jobs)
	}
	assertEvidence(t, result.RawEvidence, "tinyfish_fetch_fallback", "retried top 4 of 8 market results after the full fetch failed")
}

func TestHostedBoardCompanyNameSplitsKnownCompactSuffixes(t *testing.T) {
	tests := map[string]string{
		"andurilindustries": "Anduril Industries",
		"citadelsecurities": "Citadel Securities",
		"radix-trading":     "Radix Trading",
		"scaleai":           "Scale AI",
		"merklescience":     "Merkle Science",
	}
	for slug, want := range tests {
		if got := hostedBoardCompanyName(slug); got != want {
			t.Errorf("hostedBoardCompanyName(%q) = %q, want %q", slug, got, want)
		}
	}
	if got := tinyFishFetchedPostingCompany("Software Engineer Intern", tinyfish.FetchResult{URL: "https://careers.airwallex.com/job/123"}, tinyfish.SearchResult{SiteName: "careers.airwallex.com"}, "https://careers.airwallex.com/job/123"); got != "Airwallex" {
		t.Fatalf("domain-like site name company = %q, want Airwallex", got)
	}
}

func TestTinyFishSearchExtractorSkipsFetchWhenScopedResultsAreOffSite(t *testing.T) {
	client := &fakeTinyFishClient{
		search: tinyfish.SearchResponse{
			Results: []tinyfish.SearchResult{
				{
					Title:   "Software Engineer Intern - Aggregated copy",
					URL:     "https://mirror.example/jobs/salesforce-software-engineer-intern",
					Snippet: "Software engineering internship copied from Salesforce careers.",
				},
			},
		},
	}
	extractor := NewTinyFishSearchExtractor(client)

	_, err := extractor.Extract(context.Background(), Source{
		ID:   "salesforce-offsite-only",
		Name: "Salesforce",
		URL:  "https://careers.salesforce.com/en/jobs/?search=software%20engineer%20intern",
		Tier: TierSearchDiscovery,
		Metadata: map[string]string{
			"source_kind": "salesforce_careers",
		},
	})
	if !errors.Is(err, ErrNoJobs) {
		t.Fatalf("err = %v, want ErrNoJobs", err)
	}
	if len(client.gotFetch.URLs) != 0 {
		t.Fatalf("fetch urls = %#v, want no fetch after off-site scoped results", client.gotFetch.URLs)
	}
}

func TestTinyFishSearchExtractorReturnsFetchErrorWhenAllSelectedFetchesFail(t *testing.T) {
	client := &fakeTinyFishClient{
		search: tinyfish.SearchResponse{
			Results: []tinyfish.SearchResult{
				{
					Title:   "Software Engineer Intern - Salesforce",
					URL:     "https://careers.salesforce.com/en/jobs/jr-123/software-engineer-intern",
					Snippet: "Software engineering internship for 2026 graduates.",
				},
			},
		},
		fetch: tinyfish.FetchResponse{
			Errors: []tinyfish.FetchError{
				{
					URL:     "https://careers.salesforce.com/en/jobs/jr-123/software-engineer-intern",
					Code:    "blocked",
					Message: "fetch blocked by upstream",
				},
			},
		},
	}
	extractor := NewTinyFishSearchExtractor(client)

	_, err := extractor.Extract(context.Background(), Source{
		ID:   "salesforce-fetch-fail",
		Name: "Salesforce",
		URL:  "https://careers.salesforce.com/en/jobs/?search=software%20engineer%20intern",
		Tier: TierSearchDiscovery,
		Metadata: map[string]string{
			"source_kind": "salesforce_careers",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "tinyfish fetch failed for all selected search results") || !strings.Contains(err.Error(), "fetch blocked by upstream") {
		t.Fatalf("err = %v, want actionable TinyFish fetch failure", err)
	}
	if errors.Is(err, ErrNoJobs) {
		t.Fatalf("err = %v, should not be classified as ErrNoJobs when provider fetch failed", err)
	}
}

func TestTinyFishSearchExtractorSeedsTargetMarketSources(t *testing.T) {
	extractor := NewTinyFishSearchExtractor(nil)
	sources := extractor.Sources()
	// Core market sources that must always be present with their exact queries.
	coreWant := map[string]struct {
		query    string
		location string
		cadence  string
	}{
		"tinyfish-us-early-career": {
			query:    `"software engineer intern" OR "new grad software engineer" careers jobs`,
			location: "US",
			cadence:  "30m",
		},
		"tinyfish-singapore-early-career": {
			query:    `"software engineer intern" OR "new grad software engineer" Singapore careers jobs`,
			location: "Singapore",
			cadence:  "30m",
		},
		"tinyfish-uk-early-career": {
			query:    `"software engineer intern" OR "graduate software engineer" UK careers jobs`,
			location: "United Kingdom",
			cadence:  "30m",
		},
		"tinyfish-canada-early-career": {
			query:    `"software engineer intern" OR "new grad software engineer" Canada careers jobs`,
			location: "Canada",
			cadence:  "30m",
		},
		"tinyfish-hong-kong-early-career": {
			query:    `"software engineer intern" OR "graduate software engineer" "Hong Kong" careers jobs`,
			location: "Hong Kong",
			cadence:  "30m",
		},
		"tinyfish-finance-tech-early-career": {
			query:    `"software engineer intern" OR "new grad software engineer" hedge fund trading quant fintech careers jobs`,
			location: "US",
			cadence:  "30m",
		},
		"tinyfish-ai-infra-devtools-early-career": {
			query:    `"software engineer intern" OR "new grad software engineer" AI infrastructure devtools careers jobs`,
			location: "US",
			cadence:  "30m",
		},
		"tinyfish-big-tech-unicorn-early-career": {
			query:    `"software engineer intern" OR "new grad software engineer" big tech unicorn careers jobs`,
			location: "US",
			cadence:  "30m",
		},
	}
	if len(sources) < len(coreWant) {
		t.Fatalf("sources = %d, want at least %d", len(sources), len(coreWant))
	}
	// Validate all sources have required fields.
	seenIDs := make(map[string]bool, len(sources))
	for _, source := range sources {
		if source.ID == "" {
			t.Fatalf("source missing ID: %#v", source)
		}
		if seenIDs[source.ID] {
			t.Fatalf("duplicate source ID %q", source.ID)
		}
		seenIDs[source.ID] = true
		switch source.Tier {
		case TierSearchDiscovery:
			if source.Metadata["query"] == "" {
				t.Fatalf("%s missing query metadata", source.ID)
			}
			if source.Metadata["kind"] != "tinyfish_search" {
				t.Fatalf("%s kind = %q, want tinyfish_search", source.ID, source.Metadata["kind"])
			}
		case TierBrowserAgent:
			if source.Metadata["goal"] == "" {
				t.Fatalf("%s missing browser-agent goal metadata", source.ID)
			}
		default:
			t.Fatalf("%s tier = %q, want %q or %q", source.ID, source.Tier, TierSearchDiscovery, TierBrowserAgent)
		}
		if source.Metadata["cadence"] == "" {
			t.Fatalf("%s missing cadence metadata", source.ID)
		}
	}
	// Verify core sources have their exact expected queries.
	for _, source := range sources {
		expected, ok := coreWant[source.ID]
		if !ok {
			continue // extended source, only required fields checked above
		}
		if source.Metadata["query"] != expected.query {
			t.Fatalf("%s query = %q, want %q", source.ID, source.Metadata["query"], expected.query)
		}
		if source.Metadata["location"] != expected.location {
			t.Fatalf("%s location = %q, want %q", source.ID, source.Metadata["location"], expected.location)
		}
		if source.Metadata["cadence"] != expected.cadence {
			t.Fatalf("%s cadence = %q, want %q", source.ID, source.Metadata["cadence"], expected.cadence)
		}
	}
}

func TestTinyFishSearchExtractorRestoresTargetMarketSyntheticIntent(t *testing.T) {
	cases := []struct {
		name     string
		url      string
		query    string
		location string
	}{
		{
			name:     "US",
			url:      "tinyfish://search/us-early-career-software",
			query:    `"software engineer intern" OR "new grad software engineer" careers jobs`,
			location: "US",
		},
		{
			name:     "Singapore",
			url:      "tinyfish://search/singapore-early-career-software",
			query:    `"software engineer intern" OR "new grad software engineer" Singapore careers jobs`,
			location: "Singapore",
		},
		{
			name:     "UK",
			url:      "tinyfish://search/uk-early-career-software",
			query:    `"software engineer intern" OR "graduate software engineer" UK careers jobs`,
			location: "United Kingdom",
		},
		{
			name:     "Canada",
			url:      "tinyfish://search/canada-early-career-software",
			query:    `"software engineer intern" OR "new grad software engineer" Canada careers jobs`,
			location: "Canada",
		},
		{
			name:     "Hong Kong",
			url:      "tinyfish://search/hong-kong-early-career-software",
			query:    `"software engineer intern" OR "graduate software engineer" "Hong Kong" careers jobs`,
			location: "Hong Kong",
		},
		{
			name:     "finance tech",
			url:      "tinyfish://search/finance-tech-early-career-software",
			query:    `"software engineer intern" OR "new grad software engineer" hedge fund trading quant fintech careers jobs`,
			location: "US",
		},
		{
			name:     "AI infra devtools",
			url:      "tinyfish://search/ai-infra-devtools-early-career-software",
			query:    `"software engineer intern" OR "new grad software engineer" AI infrastructure devtools careers jobs`,
			location: "US",
		},
		{
			name:     "big tech unicorn",
			url:      "tinyfish://search/big-tech-unicorn-early-career-software",
			query:    `"software engineer intern" OR "new grad software engineer" big tech unicorn careers jobs`,
			location: "US",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := Source{
				ID:   "source_db_copy",
				Name: "search_discovery",
				URL:  tc.url,
				Tier: TierSearchDiscovery,
				Metadata: map[string]string{
					"source_kind":         "search_discovery",
					"extraction_strategy": "search_discovery",
				},
			}
			if got := searchQueryForSource(source); got != tc.query {
				t.Fatalf("query = %q, want %q", got, tc.query)
			}
			if got := searchLocationForSource(source); got != tc.location {
				t.Fatalf("location = %q, want %q", got, tc.location)
			}
		})
	}
}

func TestTinyFishSearchExtractorDerivesSearchIntentFromSourceURL(t *testing.T) {
	client := &fakeTinyFishClient{
		search: tinyfish.SearchResponse{
			Results: []tinyfish.SearchResult{
				{
					Title:    "Software Engineer Intern - Meridian Robotics",
					URL:      "https://meridian.example/careers/software-engineer-intern",
					Snippet:  "Software engineering internship in Singapore.",
					SiteName: "Meridian Robotics",
				},
			},
		},
		fetch: tinyfish.FetchResponse{
			Results: []tinyfish.FetchResult{
				{
					URL:      "https://meridian.example/careers/software-engineer-intern",
					Title:    "Software Engineer Intern",
					Markdown: "Software Engineer Intern\n\nSingapore\n\nBuild robotics platform services during a 2026 internship.",
				},
			},
		},
	}
	extractor := NewTinyFishSearchExtractor(client)

	_, err := extractor.Extract(context.Background(), Source{
		ID:   "source_linkedin_search",
		Name: "Meridian Robotics",
		URL:  "https://www.linkedin.com/jobs/search/?keywords=Meridian+Robotics%20software%20engineer%20intern&location=Singapore",
		Tier: TierSearchDiscovery,
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if client.gotSearch.Query != "Meridian Robotics software engineer intern" {
		t.Fatalf("query = %q, want URL keywords", client.gotSearch.Query)
	}
	if client.gotSearch.Location != "Singapore" {
		t.Fatalf("location = %q, want URL location", client.gotSearch.Location)
	}
}

func TestTinyFishSearchExtractorRestoresSyntheticSourceIntent(t *testing.T) {
	client := &fakeTinyFishClient{
		search: tinyfish.SearchResponse{
			Results: []tinyfish.SearchResult{
				{
					Title:    "Software Engineer Intern - Singapore",
					URL:      "https://meridian.example/careers/software-engineer-intern",
					Snippet:  "Software engineering internship in Singapore.",
					SiteName: "Meridian Robotics",
				},
			},
		},
		fetch: tinyfish.FetchResponse{
			Results: []tinyfish.FetchResult{
				{
					URL:      "https://meridian.example/careers/software-engineer-intern",
					Title:    "Software Engineer Intern",
					Markdown: "Software Engineer Intern\n\nSingapore\n\nBuild backend services during a 2026 internship.",
				},
			},
		},
	}
	extractor := NewTinyFishSearchExtractor(client)

	_, err := extractor.Extract(context.Background(), Source{
		ID:   "source_db_copy",
		Name: "search_discovery",
		URL:  "tinyfish://search/singapore-early-career-software",
		Tier: TierSearchDiscovery,
		Metadata: map[string]string{
			"source_kind":         "search_discovery",
			"extraction_strategy": "search_discovery",
		},
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if client.gotSearch.Query != `"software engineer intern" OR "new grad software engineer" Singapore careers jobs` {
		t.Fatalf("query = %q, want restored Singapore search intent", client.gotSearch.Query)
	}
	if client.gotSearch.Location != "Singapore" {
		t.Fatalf("location = %q, want Singapore", client.gotSearch.Location)
	}
}

func TestTinyFishSearchExtractorScopesHostedFallbackBoardQueries(t *testing.T) {
	cases := []struct {
		name string
		kind string
		url  string
		want string
	}{
		{name: "gem", kind: "gem", url: "https://jobs.gem.com/meridian-robotics", want: "site:jobs.gem.com/meridian-robotics Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "avature", kind: "avature", url: "https://MeridianRobotics.avature.net/careers", want: "site:meridianrobotics.avature.net/careers Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "hireology", kind: "hireology", url: "https://careers.hireology.com/meridian-robotics", want: "site:careers.hireology.com/meridian-robotics Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "workstream", kind: "workstream", url: "https://www.workstream.us/j/meridian-robotics", want: "site:www.workstream.us/j/meridian-robotics Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "jobylon", kind: "jobylon", url: "https://jobs.jobylon.com/meridian-robotics", want: "site:jobs.jobylon.com/meridian-robotics Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "zoho recruit", kind: "zoho_recruit", url: "https://MeridianRobotics.zohorecruit.com/jobs/Careers", want: "site:meridianrobotics.zohorecruit.com/jobs/Careers Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "manatal", kind: "manatal", url: "https://meridian-robotics.manatal.com/jobs", want: "site:meridian-robotics.manatal.com/jobs Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "freshteam", kind: "freshteam", url: "https://meridian-robotics.freshteam.com/jobs", want: "site:meridian-robotics.freshteam.com/jobs Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "join.com", kind: "join_com", url: "https://join.com/companies/meridian-robotics", want: "site:join.com/companies/meridian-robotics Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "talentlyft", kind: "talentlyft", url: "https://meridian-robotics.talentlyft.com", want: "site:meridian-robotics.talentlyft.com Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "homerun", kind: "homerun", url: "https://meridian-robotics.homerun.co", want: "site:meridian-robotics.homerun.co Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "catsone", kind: "catsone", url: "https://meridian-robotics.catsone.com/careers", want: "site:meridian-robotics.catsone.com/careers Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "occupop", kind: "occupop", url: "https://meridian-robotics.occupop.com/job-board", want: "site:meridian-robotics.occupop.com/job-board Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "hibob hiring", kind: "hibob_hiring", url: "https://meridian-robotics.careers.hibob.com/", want: "site:meridian-robotics.careers.hibob.com Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "workable jobs search", kind: "workable_jobs", url: "https://jobs.workable.com/search?query=Meridian+Robotics+software+engineer+intern", want: "site:jobs.workable.com/search Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "rippling jobs", kind: "rippling_jobs", url: "https://jobs.rippling.com/meridian-robotics", want: "site:jobs.rippling.com/meridian-robotics Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "fountain", kind: "fountain", url: "https://jobs.fountain.com/meridian-robotics", want: "site:jobs.fountain.com/meridian-robotics Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "applicantpro", kind: "applicantpro", url: "https://meridian-robotics.applicantpro.com/jobs/", want: "site:meridian-robotics.applicantpro.com/jobs Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "careerplug", kind: "careerplug", url: "https://www.careerplug.com/jobs?company=Meridian+Robotics", want: "site:www.careerplug.com/jobs Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "jobsoid", kind: "jobsoid", url: "https://meridian-robotics.jobsoid.com", want: "site:meridian-robotics.jobsoid.com Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "paycom", kind: "paycom", url: "https://www.paycomonline.net/v4/ats/web.php/jobs?clientkey=MeridianRobotics", want: "site:www.paycomonline.net/v4/ats/web.php/jobs Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "dover", kind: "dover", url: "https://jobs.dover.com/meridian-robotics", want: "site:jobs.dover.com/meridian-robotics Meridian Robotics software engineer intern new grad careers jobs"},
		{name: "yello", kind: "yello", url: "https://app.yello.co/job_boards/meridian-robotics", want: "site:app.yello.co/job_boards/meridian-robotics Meridian Robotics software engineer intern new grad careers jobs"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeTinyFishClient{
				search: tinyfish.SearchResponse{
					Results: []tinyfish.SearchResult{
						{
							Title:    "Backend Software Engineer Intern - Meridian Robotics",
							URL:      tc.url + "/backend-software-engineer-intern",
							Snippet:  "Summer 2026 software engineering internship.",
							SiteName: "Meridian Robotics",
						},
					},
				},
				fetch: tinyfish.FetchResponse{
					Results: []tinyfish.FetchResult{
						{
							URL:      tc.url + "/backend-software-engineer-intern",
							Title:    "Backend Software Engineer Intern",
							Markdown: "Backend Software Engineer Intern\n\nSan Francisco, United States\n\nBuild robotics platform services.",
						},
					},
				},
			}
			extractor := NewTinyFishSearchExtractor(client)

			_, err := extractor.Extract(context.Background(), Source{
				ID:   "source_" + tc.kind + "_meridian",
				Name: "Meridian Robotics",
				URL:  tc.url,
				Tier: TierSearchDiscovery,
				Metadata: map[string]string{
					"source_kind": tc.kind,
				},
			})
			if err != nil {
				t.Fatalf("Extract returned error: %v", err)
			}
			if client.gotSearch.Query != tc.want {
				t.Fatalf("query = %q, want %q", client.gotSearch.Query, tc.want)
			}
		})
	}
}

func TestTinyFishSearchExtractorScopesRemoteSearchBoardQueries(t *testing.T) {
	cases := []struct {
		name string
		kind string
		url  string
		want string
	}{
		{name: "remote ok search param", kind: "remoteok_jobs", url: "https://remoteok.com/remote-software-engineer-jobs?search=Meridian%20Robotics%20software%20engineer%20intern", want: "site:remoteok.com/remote-software-engineer-jobs Meridian Robotics software engineer intern"},
		{name: "we work remotely term param", kind: "weworkremotely_jobs", url: "https://weworkremotely.com/remote-jobs/search?term=Meridian%20Robotics%20software%20engineer%20intern", want: "site:weworkremotely.com/remote-jobs/search Meridian Robotics software engineer intern"},
		{name: "ripplematch keyword param", kind: "ripplematch_jobs", url: "https://ripplematch.com/jobs?keyword=Meridian%20Robotics%20software%20engineer%20intern", want: "site:ripplematch.com/jobs Meridian Robotics software engineer intern"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := Source{
				ID:   "source_" + tc.kind,
				Name: "Remote marketplace",
				URL:  tc.url,
				Metadata: map[string]string{
					"source_kind": tc.kind,
				},
			}
			if got := searchQueryForSource(source); got != tc.want {
				t.Fatalf("query = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTinyFishSearchExtractorScopesBroadDiscoveryBoardQueries(t *testing.T) {
	cases := []struct {
		name string
		kind string
		url  string
		want string
	}{
		{name: "mycareersfuture", kind: "mycareersfuture_sg", url: "https://www.mycareersfuture.gov.sg/search?search=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.mycareersfuture.gov.sg/search Meridian Robotics software engineer intern"},
		{name: "nodeflair", kind: "nodeflair_jobs", url: "https://nodeflair.com/jobs?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:nodeflair.com/jobs Meridian Robotics software engineer intern"},
		{name: "techinasia", kind: "techinasia_jobs", url: "https://www.techinasia.com/jobs/search?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.techinasia.com/jobs/search Meridian Robotics software engineer intern"},
		{name: "jobstreet", kind: "jobstreet_jobs", url: "https://www.jobstreet.com.sg/jobs?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.jobstreet.com.sg/jobs Meridian Robotics software engineer intern"},
		{name: "jobsdb", kind: "jobsdb_jobs", url: "https://hk.jobsdb.com/jobs?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:hk.jobsdb.com/jobs Meridian Robotics software engineer intern"},
		{name: "ctgoodjobs", kind: "ctgoodjobs_jobs", url: "https://www.ctgoodjobs.hk/jobs?keyword=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.ctgoodjobs.hk/jobs Meridian Robotics software engineer intern"},
		{name: "reed uk", kind: "reed_uk_jobs", url: "https://www.reed.co.uk/jobs/software-engineer-intern-jobs?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.reed.co.uk/jobs/software-engineer-intern-jobs Meridian Robotics software engineer intern"},
		{name: "totaljobs uk", kind: "totaljobs_uk_jobs", url: "https://www.totaljobs.com/jobs/software-engineer-intern?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.totaljobs.com/jobs/software-engineer-intern Meridian Robotics software engineer intern"},
		{name: "cv library uk", kind: "cvlibrary_uk_jobs", url: "https://www.cv-library.co.uk/software-engineer-intern-jobs?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.cv-library.co.uk/software-engineer-intern-jobs Meridian Robotics software engineer intern"},
		{name: "gradcracker uk", kind: "gradcracker_jobs", url: "https://www.gradcracker.com/search/computing-technology/software-engineering-internships?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.gradcracker.com/search/computing-technology/software-engineering-internships Meridian Robotics software engineer intern"},
		{name: "rate my placement uk", kind: "ratemyplacement_jobs", url: "https://www.ratemyplacement.co.uk/search-jobs/software-engineering?keyword=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.ratemyplacement.co.uk/search-jobs/software-engineering Meridian Robotics software engineer intern"},
		{name: "internsg", kind: "internsg_jobs", url: "https://www.internsg.com/jobs/?f_p=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.internsg.com/jobs Meridian Robotics software engineer intern"},
		{name: "grad singapore", kind: "gradsingapore_jobs", url: "https://gradsingapore.com/search-jobs?keyword=Meridian+Robotics%20software%20engineer%20intern", want: "site:gradsingapore.com/search-jobs Meridian Robotics software engineer intern"},
		{name: "jobscentral singapore", kind: "jobscentral_sg_jobs", url: "https://jobscentral.com.sg/jobs?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:jobscentral.com.sg/jobs Meridian Robotics software engineer intern"},
		{name: "workopolis", kind: "workopolis_jobs", url: "https://www.workopolis.com/jobsearch/find-jobs?ak=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.workopolis.com/jobsearch/find-jobs Meridian Robotics software engineer intern"},
		{name: "job bank canada", kind: "jobbank_canada", url: "https://www.jobbank.gc.ca/jobsearch/jobsearch?searchstring=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.jobbank.gc.ca/jobsearch/jobsearch Meridian Robotics software engineer intern"},
		{name: "efinancialcareers", kind: "efinancialcareers_jobs", url: "https://www.efinancialcareers.com/search?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.efinancialcareers.com/search Meridian Robotics software engineer intern"},
		{name: "dice", kind: "dice_jobs", url: "https://www.dice.com/jobs?q=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.dice.com/jobs Meridian Robotics software engineer intern"},
		{name: "glassdoor", kind: "glassdoor_jobs", url: "https://www.glassdoor.com/Job/jobs.htm?sc.keyword=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.glassdoor.com/Job/jobs.htm Meridian Robotics software engineer intern"},
		{name: "ziprecruiter", kind: "ziprecruiter_jobs", url: "https://www.ziprecruiter.com/jobs-search?search=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.ziprecruiter.com/jobs-search Meridian Robotics software engineer intern"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeTinyFishClient{
				search: tinyfish.SearchResponse{
					Results: []tinyfish.SearchResult{
						{
							Title:    "Backend Software Engineer Intern - Meridian Robotics",
							URL:      tc.url + "&job=backend-software-engineer-intern",
							Snippet:  "Summer 2026 software engineering internship.",
							SiteName: "Meridian Robotics",
						},
					},
				},
				fetch: tinyfish.FetchResponse{
					Results: []tinyfish.FetchResult{
						{
							URL:      tc.url + "&job=backend-software-engineer-intern",
							Title:    "Backend Software Engineer Intern",
							Markdown: "Backend Software Engineer Intern\n\nSingapore\n\nBuild robotics platform services.",
						},
					},
				},
			}
			extractor := NewTinyFishSearchExtractor(client)

			_, err := extractor.Extract(context.Background(), Source{
				ID:   "source_" + tc.kind + "_meridian",
				Name: "Meridian Robotics",
				URL:  tc.url,
				Tier: TierSearchDiscovery,
				Metadata: map[string]string{
					"source_kind": tc.kind,
				},
			})
			if err != nil {
				t.Fatalf("Extract returned error: %v", err)
			}
			if client.gotSearch.Query != tc.want {
				t.Fatalf("query = %q, want %q", client.gotSearch.Query, tc.want)
			}
		})
	}
}

func TestTinyFishSearchExtractorScopesPrimaryDiscoveryBoardQueries(t *testing.T) {
	cases := []struct {
		name string
		kind string
		url  string
		want string
	}{
		{name: "google serp", kind: "google_serp_search", url: "https://www.google.com/search?q=Meridian+Robotics+software+engineer+intern+jobs", want: "site:www.google.com/search Meridian Robotics software engineer intern jobs"},
		{name: "x social", kind: "x_social_search", url: "https://x.com/search?q=Meridian+Robotics+hiring+software+engineer+intern&f=live", want: "site:x.com/search Meridian Robotics hiring software engineer intern"},
		{name: "hacker news jobs", kind: "hackernews_jobs", url: "https://hn.algolia.com/?query=Meridian+Robotics%20software%20engineer%20intern&sort=byDate&type=story", want: "site:hn.algolia.com Meridian Robotics software engineer intern"},
		{name: "reddit jobs search", kind: "reddit_jobs_search", url: "https://www.reddit.com/search/?q=Meridian+Robotics%20software%20engineer%20intern%20hiring&sort=new", want: "site:www.reddit.com/search Meridian Robotics software engineer intern hiring"},
		{name: "linkedin", kind: "linkedin_search", url: "https://www.linkedin.com/jobs/search/?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.linkedin.com/jobs/search Meridian Robotics software engineer intern"},
		{name: "linkedin careers", kind: "linkedin_careers", url: "https://careers.linkedin.com/pathways-programs/internships/Technical", want: "site:careers.linkedin.com/pathways-programs/internships/Technical Meridian Robotics"},
		{name: "discord careers", kind: "discord_careers", url: "https://discord.com/careers", want: "site:discord.com/careers Meridian Robotics"},
		{name: "gitlab careers", kind: "gitlab_careers", url: "https://about.gitlab.com/jobs/", want: "site:about.gitlab.com/jobs Meridian Robotics"},
		{name: "twilio careers", kind: "twilio_careers", url: "https://www.twilio.com/en-us/company/jobs", want: "site:www.twilio.com/en-us/company/jobs Meridian Robotics"},
		{name: "samsara careers", kind: "samsara_careers", url: "https://www.samsara.com/company/careers/", want: "site:www.samsara.com/company/careers Meridian Robotics"},
		{name: "airtable careers", kind: "airtable_careers", url: "https://www.airtable.com/careers", want: "site:www.airtable.com/careers Meridian Robotics"},
		{name: "netflix careers", kind: "netflix_careers", url: "https://jobs.netflix.com/careers/internships", want: "site:jobs.netflix.com/careers/internships Meridian Robotics"},
		{name: "atlassian careers", kind: "atlassian_careers", url: "https://www.atlassian.com/company/careers/earlycareers", want: "site:www.atlassian.com/company/careers/earlycareers Meridian Robotics"},
		{name: "canva careers", kind: "canva_careers", url: "https://www.lifeatcanva.com/en/jobs/", want: "site:www.lifeatcanva.com/en/jobs Meridian Robotics"},
		{name: "dropbox careers", kind: "dropbox_careers", url: "https://www.dropbox.jobs/en/emerging-talent/", want: "site:www.dropbox.jobs/en/emerging-talent Meridian Robotics"},
		{name: "robinhood careers", kind: "robinhood_careers", url: "https://careers.robinhood.com/", want: "site:careers.robinhood.com Meridian Robotics"},
		{name: "doordash careers", kind: "doordash_careers", url: "https://careersatdoordash.com/university-careers/", want: "site:careersatdoordash.com/university-careers Meridian Robotics"},
		{name: "airbnb careers", kind: "airbnb_careers", url: "https://careers.airbnb.com/", want: "site:careers.airbnb.com Meridian Robotics"},
		{name: "palantir careers", kind: "palantir_careers", url: "https://www.palantir.com/careers/open-positions/", want: "site:www.palantir.com/careers/open-positions Meridian Robotics"},
		{name: "lockheed careers", kind: "lockheed_careers", url: "https://www.lockheedmartin.com/en-us/careers/candidates/students-early-careers.html", want: "site:www.lockheedmartin.com/en-us/careers/candidates/students-early-careers.html Meridian Robotics"},
		{name: "northrop careers", kind: "northrop_careers", url: "https://jobs.northropgrumman.com/careers?query=intern", want: "site:jobs.northropgrumman.com/careers intern"},
		{name: "datadog careers", kind: "datadog_careers", url: "https://careers.datadoghq.com/all-jobs/", want: "site:careers.datadoghq.com/all-jobs Meridian Robotics"},
		{name: "reddit careers", kind: "reddit_careers", url: "https://www.redditinc.com/careers", want: "site:www.redditinc.com/careers Meridian Robotics"},
		{name: "pinterest careers", kind: "pinterest_careers", url: "https://www.pinterestcareers.com/jobs/", want: "site:www.pinterestcareers.com/jobs Meridian Robotics"},
		{name: "plaid careers", kind: "plaid_careers", url: "https://plaid.com/careers/", want: "site:plaid.com/careers Meridian Robotics"},
		{name: "brex careers", kind: "brex_careers", url: "https://www.brex.com/careers", want: "site:www.brex.com/careers Meridian Robotics"},
		{name: "linear careers", kind: "linear_careers", url: "https://linear.app/careers", want: "site:linear.app/careers Meridian Robotics"},
		{name: "asana careers", kind: "asana_careers", url: "https://asana.com/jobs/all", want: "site:asana.com/jobs/all Meridian Robotics"},
		{name: "instacart careers", kind: "instacart_careers", url: "https://www.instacart.careers/current-openings", want: "site:www.instacart.careers/current-openings Meridian Robotics"},
		{name: "mercury careers", kind: "mercury_careers", url: "https://mercury.com/jobs", want: "site:mercury.com/jobs Meridian Robotics"},
		{name: "glean careers", kind: "glean_careers", url: "https://www.glean.com/careers", want: "site:www.glean.com/careers Meridian Robotics"},
		{name: "cohere careers", kind: "cohere_careers", url: "https://cohere.com/careers", want: "site:cohere.com/careers Meridian Robotics"},
		{name: "anduril careers", kind: "anduril_careers", url: "https://www.anduril.com/careers", want: "site:www.anduril.com/careers Meridian Robotics"},
		{name: "indeed", kind: "indeed_search", url: "https://www.indeed.com/jobs?q=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.indeed.com/jobs Meridian Robotics software engineer intern"},
		{name: "wellfound", kind: "wellfound_search", url: "https://wellfound.com/jobs?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:wellfound.com/jobs Meridian Robotics software engineer intern"},
		{name: "welcome to the jungle", kind: "talent_marketplace", url: "https://www.welcometothejungle.com/en/jobs?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.welcometothejungle.com/en/jobs Meridian Robotics software engineer intern"},
		{name: "handshake", kind: "handshake_search", url: "https://app.joinhandshake.com/stu/postings?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:app.joinhandshake.com/stu/postings Meridian Robotics software engineer intern"},
		{name: "simplify", kind: "simplify_jobs", url: "https://simplify.jobs/jobs?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:simplify.jobs/jobs Meridian Robotics software engineer intern"},
		{name: "startup jobs", kind: "startup_jobs", url: "https://startup.jobs/?q=Meridian+Robotics%20software%20engineer%20intern", want: "site:startup.jobs Meridian Robotics software engineer intern"},
		{name: "levels fyi", kind: "levels_fyi_jobs", url: "https://www.levels.fyi/jobs/software-engineer/internship/?search=Meridian+Robotics", want: "site:www.levels.fyi/jobs/software-engineer/internship Meridian Robotics"},
		{name: "cord", kind: "cord_jobs", url: "https://cord.com/search/jobs/software-developer?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:cord.com/search/jobs/software-developer Meridian Robotics software engineer intern"},
		{name: "untapped", kind: "untapped_jobs", url: "https://www.untapped.io/app/discover/jobs?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.untapped.io/app/discover/jobs Meridian Robotics software engineer intern"},
		{name: "climatebase", kind: "climatebase_jobs", url: "https://climatebase.org/jobs?q=Meridian+Robotics%20software%20engineer%20intern", want: "site:climatebase.org/jobs Meridian Robotics software engineer intern"},
		{name: "hiringcafe", kind: "hiringcafe_jobs", url: "https://hiring.cafe/?search=Meridian+Robotics%20software%20engineer%20intern", want: "site:hiring.cafe Meridian Robotics software engineer intern"},
		{name: "usajobs", kind: "usajobs_search", url: "https://www.usajobs.gov/Search/Results?k=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.usajobs.gov/Search/Results Meridian Robotics software engineer intern"},
		{name: "governmentjobs", kind: "governmentjobs_search", url: "https://www.governmentjobs.com/jobs?keyword=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.governmentjobs.com/jobs Meridian Robotics software engineer intern"},
		{name: "meta careers", kind: "meta_careers", url: "https://www.metacareers.com/jobsearch/?q=Meridian+Robotics%20software%20engineer%20intern", want: "Meridian Robotics software engineer intern"},
		{name: "tiktok careers", kind: "tiktok_careers", url: "https://careers.tiktok.com/position?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:careers.tiktok.com/position Meridian Robotics software engineer intern"},
		{name: "bytedance careers", kind: "bytedance_careers", url: "https://jobs.bytedance.com/en/position?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:jobs.bytedance.com/en/position Meridian Robotics software engineer intern"},
		{name: "anthropic careers", kind: "anthropic_careers", url: "https://www.anthropic.com/careers/jobs", want: "site:www.anthropic.com/careers/jobs Meridian Robotics"},
		{name: "databricks careers", kind: "databricks_careers", url: "https://www.databricks.com/company/careers/open-positions?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.databricks.com/company/careers/open-positions Meridian Robotics software engineer intern"},
		{name: "salesforce careers", kind: "salesforce_careers", url: "https://careers.salesforce.com/en/jobs/?search=Meridian+Robotics%20software%20engineer%20intern", want: "site:careers.salesforce.com/en/jobs Meridian Robotics software engineer intern"},
		{name: "adobe careers", kind: "adobe_careers", url: "https://careers.adobe.com/us/en/search-results?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:careers.adobe.com/us/en/search-results Meridian Robotics software engineer intern"},
		{name: "mongodb careers", kind: "mongodb_careers", url: "https://www.mongodb.com/company/careers/students-and-graduates", want: "site:www.mongodb.com/company/careers/students-and-graduates Meridian Robotics"},
		{name: "servicenow careers", kind: "servicenow_careers", url: "https://careers.servicenow.com/jobs/?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:careers.servicenow.com/jobs Meridian Robotics software engineer intern"},
		{name: "tesla careers", kind: "tesla_careers", url: "https://www.tesla.com/careers/search/?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.tesla.com/careers/search Meridian Robotics software engineer intern"},
		{name: "spacex careers", kind: "spacex_careers", url: "https://www.spacex.com/careers/jobs?search=Meridian+Robotics%20software%20engineer", want: "site:www.spacex.com/careers/jobs Meridian Robotics software engineer"},
		{name: "neuralink careers", kind: "neuralink_careers", url: "https://neuralink.com/careers/", want: "site:neuralink.com/careers Meridian Robotics"},
		{name: "bloomberg careers", kind: "bloomberg_careers", url: "https://www.bloomberg.com/company/careers/working-here/engineering/", want: "site:www.bloomberg.com/company/careers/working-here/engineering Meridian Robotics"},
		{name: "nvidia careers", kind: "nvidia_careers", url: "https://www.nvidia.com/en-us/about-nvidia/careers/university-recruiting/", want: "site:www.nvidia.com/en-us/about-nvidia/careers/university-recruiting Meridian Robotics"},
		{name: "roblox careers", kind: "roblox_careers", url: "https://careers.roblox.com/jobs?search=Meridian+Robotics%20software%20engineer%20intern", want: "site:careers.roblox.com/jobs Meridian Robotics software engineer intern"},
		{name: "coinbase careers", kind: "coinbase_careers", url: "https://www.coinbase.com/careers/positions?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.coinbase.com/careers/positions Meridian Robotics software engineer intern"},
		{name: "ramp careers", kind: "ramp_careers", url: "https://ramp.com/emerging-talent", want: "site:ramp.com/emerging-talent Meridian Robotics"},
		{name: "d e shaw careers", kind: "deshaw_careers", url: "https://www.deshaw.com/careers?keyword=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.deshaw.com/careers Meridian Robotics software engineer intern"},
		{name: "sig careers", kind: "sig_careers", url: "https://careers.sig.com/jobs?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:careers.sig.com/jobs Meridian Robotics software engineer intern"},
		{name: "virtu careers", kind: "virtu_careers", url: "https://www.virtu.com/careers/?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.virtu.com/careers Meridian Robotics software engineer intern"},
		{name: "hrt careers", kind: "hrt_careers", url: "https://www.hudsonrivertrading.com/student-opportunities/", want: "site:www.hudsonrivertrading.com/student-opportunities Meridian Robotics"},
		{name: "optiver careers", kind: "optiver_careers", url: "https://www.optiver.com/join-us/jobs/?query=Meridian+Robotics%20software%20engineer%20intern", want: "Meridian Robotics software engineer intern"},
		{name: "imc careers", kind: "imc_careers", url: "https://www.imc.com/us/search-careers?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.imc.com/us/search-careers Meridian Robotics software engineer intern"},
		{name: "jump careers", kind: "jump_careers", url: "https://www.jumptrading.com/hr/students-new-grads", want: "site:www.jumptrading.com/hr/students-new-grads Meridian Robotics"},
		{name: "two sigma careers", kind: "twosigma_careers", url: "https://careers.twosigma.com/careers/OpenRoles?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:careers.twosigma.com/careers/OpenRoles Meridian Robotics software engineer intern"},
		{name: "drw careers", kind: "drw_careers", url: "https://www.drw.com/work-at-drw/listings?filterType=campus&value=Campus", want: "site:www.drw.com/work-at-drw/listings Meridian Robotics"},
		{name: "jpmorgan careers", kind: "jpmorgan_careers", url: "https://www.jpmorganchase.com/careers/explore-opportunities/programs/software-engineer-summer", want: "site:www.jpmorganchase.com/careers/explore-opportunities/programs/software-engineer-summer Meridian Robotics"},
		{name: "goldman careers", kind: "goldman_careers", url: "https://www.goldmansachs.com/careers/our-firm/engineering?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.goldmansachs.com/careers/our-firm/engineering Meridian Robotics software engineer intern"},
		{name: "morgan stanley careers", kind: "morganstanley_careers", url: "https://www.morganstanley.com/people-opportunities/students-graduates", want: "site:www.morganstanley.com/people-opportunities/students-graduates Meridian Robotics"},
		{name: "capital one careers", kind: "capitalone_careers", url: "https://www.capitalonecareers.com/internship-programs", want: "site:www.capitalonecareers.com/internship-programs Meridian Robotics"},
		{name: "blackrock careers", kind: "blackrock_careers", url: "https://careers.blackrock.com/students-and-graduates-functions-software-engineering", want: "site:careers.blackrock.com/students-and-graduates-functions-software-engineering Meridian Robotics"},
		{name: "visa careers", kind: "visa_careers", url: "https://corporate.visa.com/en/careers/early-careers.html", want: "site:corporate.visa.com/en/careers/early-careers.html Meridian Robotics"},
		{name: "mastercard careers", kind: "mastercard_careers", url: "https://careers.mastercard.com/us/en/software-engineering-jobs?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:careers.mastercard.com/us/en/software-engineering-jobs Meridian Robotics software engineer intern"},
		{name: "paypal careers", kind: "paypal_careers", url: "https://careers.pypl.com/university-hiring/intern-hub/", want: "site:careers.pypl.com/university-hiring/intern-hub Meridian Robotics"},
		{name: "block careers", kind: "block_careers", url: "https://block.xyz/careers/jobs?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:block.xyz/careers/jobs Meridian Robotics software engineer intern"},
		{name: "affirm careers", kind: "affirm_careers", url: "https://www.affirm.com/university", want: "site:www.affirm.com/university Meridian Robotics"},
		{name: "chime careers", kind: "chime_careers", url: "https://careers.chime.com/jobs/", want: "site:careers.chime.com/jobs Meridian Robotics"},
		{name: "cursor careers", kind: "cursor_careers", url: "https://cursor.com/careers?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:cursor.com/careers Meridian Robotics software engineer intern"},
		{name: "xai careers", kind: "xai_careers", url: "https://x.ai/careers", want: "site:x.ai/careers Meridian Robotics"},
		{name: "scale careers", kind: "scale_careers", url: "https://scale.com/careers", want: "site:scale.com/careers Meridian Robotics"},
		{name: "figma careers", kind: "figma_careers", url: "https://www.figma.com/careers/?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.figma.com/careers Meridian Robotics software engineer intern"},
		{name: "vercel careers", kind: "vercel_careers", url: "https://vercel.com/careers?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:vercel.com/careers Meridian Robotics software engineer intern"},
		{name: "notion careers", kind: "notion_careers", url: "https://www.notion.com/careers?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.notion.com/careers Meridian Robotics software engineer intern"},
		{name: "cloudflare careers", kind: "cloudflare_careers", url: "https://www.cloudflare.com/careers/", want: "site:www.cloudflare.com/careers Meridian Robotics"},
		{name: "the muse", kind: "themuse_jobs", url: "https://www.themuse.com/search?keyword=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.themuse.com/search Meridian Robotics software engineer intern"},
		{name: "simplyhired", kind: "simplyhired_jobs", url: "https://www.simplyhired.com/search?q=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.simplyhired.com/search Meridian Robotics software engineer intern"},
		{name: "careerbuilder", kind: "careerbuilder_jobs", url: "https://www.careerbuilder.com/jobs?keywords=Meridian+Robotics%20software%20engineer%20intern", want: "site:www.careerbuilder.com/jobs Meridian Robotics software engineer intern"},
		{name: "seek singapore", kind: "seek_jobs", url: "https://www.seek.com.sg/software-engineer-intern-jobs?keywords=Meridian+Robotics", want: "site:www.seek.com.sg/software-engineer-intern-jobs Meridian Robotics"},
		{name: "gradconnection singapore", kind: "gradconnection_jobs", url: "https://sg.gradconnection.com/graduate-jobs/engineering-software/", want: "site:sg.gradconnection.com/graduate-jobs/engineering-software Meridian Robotics"},
		{name: "prosple internships", kind: "prosple_jobs", url: "https://prosple.com/software-engineering-internships", want: "site:prosple.com/software-engineering-internships Meridian Robotics"},
		{name: "bright network internships", kind: "brightnetwork_jobs", url: "https://www.brightnetwork.co.uk/internships/software-development/", want: "site:www.brightnetwork.co.uk/internships/software-development Meridian Robotics"},
		{name: "gradcracker internships", kind: "gradcracker_jobs", url: "https://www.gradcracker.com/search/computing-technology/software-engineering-internships", want: "site:www.gradcracker.com/search/computing-technology/software-engineering-internships Meridian Robotics"},
		{name: "rate my placement jobs", kind: "ratemyplacement_jobs", url: "https://www.ratemyplacement.co.uk/search-jobs/software-engineering", want: "site:www.ratemyplacement.co.uk/search-jobs/software-engineering Meridian Robotics"},
		{name: "internsg jobs", kind: "internsg_jobs", url: "https://www.internsg.com/jobs/", want: "site:www.internsg.com/jobs Meridian Robotics"},
		{name: "grad singapore jobs", kind: "gradsingapore_jobs", url: "https://gradsingapore.com/search-jobs", want: "site:gradsingapore.com/search-jobs Meridian Robotics"},
		{name: "jobscentral singapore jobs", kind: "jobscentral_sg_jobs", url: "https://jobscentral.com.sg/jobs", want: "site:jobscentral.com.sg/jobs Meridian Robotics"},
		{name: "custom url", kind: "custom_url", url: "https://example.com/jobs/students?query=Meridian+Robotics%20software%20engineer%20intern", want: "site:example.com/jobs/students Meridian Robotics software engineer intern"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := Source{
				ID:   "source_" + tc.kind,
				Name: "Meridian Robotics",
				URL:  tc.url,
				Metadata: map[string]string{
					"source_kind": tc.kind,
				},
			}
			if got := searchQueryForSource(source); got != tc.want {
				t.Fatalf("query = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTinyFishSearchExtractorMetadataOverridesURLSearchIntent(t *testing.T) {
	client := &fakeTinyFishClient{
		search: tinyfish.SearchResponse{
			Results: []tinyfish.SearchResult{
				{
					Title:    "New Grad Backend Engineer",
					URL:      "https://meridian.example/careers/new-grad-backend",
					Snippet:  "New grad software engineering role in New York.",
					SiteName: "Meridian Robotics",
				},
			},
		},
		fetch: tinyfish.FetchResponse{
			Results: []tinyfish.FetchResult{
				{
					URL:      "https://meridian.example/careers/new-grad-backend",
					Title:    "New Grad Backend Engineer",
					Markdown: "New Grad Backend Engineer\n\nNew York, United States\n\nBuild Go APIs for robotics infrastructure.",
				},
			},
		},
	}
	extractor := NewTinyFishSearchExtractor(client)

	_, err := extractor.Extract(context.Background(), Source{
		ID:   "source_indeed_search",
		Name: "Meridian Robotics",
		URL:  "https://www.indeed.com/jobs?q=ignored&l=ignored",
		Tier: TierSearchDiscovery,
		Metadata: map[string]string{
			"query":    "Meridian Robotics new grad backend",
			"location": "United States",
		},
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if client.gotSearch.Query != "Meridian Robotics new grad backend" {
		t.Fatalf("query = %q, want metadata query", client.gotSearch.Query)
	}
	if client.gotSearch.Location != "United States" {
		t.Fatalf("location = %q, want metadata location", client.gotSearch.Location)
	}
}

func TestTinyFishCompanyFromJobURLExtractsATSBoardNames(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "ashby",
			url:  "https://jobs.ashbyhq.com/notion/5b15697c-fa91-4511-9482-c98a6ff29f90",
			want: "Notion",
		},
		{
			name: "greenhouse",
			url:  "https://job-boards.greenhouse.io/astranis/jobs/4681183006",
			want: "Astranis",
		},
		{
			name: "lever",
			url:  "https://jobs.lever.co/ramp/software-engineer-intern-fall-2026",
			want: "Ramp",
		},
		{
			name: "stripes companies path",
			url:  "https://jobs.stripes.co/companies/databricks/jobs/39286073-software-engineering-intern-2026",
			want: "Databricks",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := companyFromJobURL(tt.url); got != tt.want {
				t.Fatalf("companyFromJobURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestTinyFishPostingCompanyIgnoresGenericATSSiteName(t *testing.T) {
	posting, _, ok := postingFromTinyFishFetch(
		tinyfish.FetchResult{
			URL:      "https://jobs.ashbyhq.com/cohere/8c035d3d-081d-4c8a-914a-72f4efaad254",
			Title:    "Software Engineer Intern (Fall / Winter 2026)",
			Markdown: "# Software Engineer Intern (Fall / Winter 2026)\n\n## Location Canada; United States\n\nEmployment Type Intern\n\nBuild AI systems with backend infrastructure teams.",
		},
		tinyfish.SearchResult{
			Title:    "Software Engineer Intern (Fall / Winter 2026) @ Cohere - Jobs",
			URL:      "https://jobs.ashbyhq.com/cohere/8c035d3d-081d-4c8a-914a-72f4efaad254",
			Snippet:  "Location Canada; United States. Employment Type Intern.",
			SiteName: "jobs.ashbyhq.com",
		},
		time.Date(2026, 7, 3, 4, 0, 0, 0, time.UTC),
	)
	if !ok {
		t.Fatal("postingFromTinyFishFetch rejected live Ashby internship")
	}
	if posting.Company != "Cohere" {
		t.Fatalf("Company = %q, want Cohere", posting.Company)
	}
}

func TestTinyFishSearchExtractorParsesRelativeFetchedPostedDate(t *testing.T) {
	now := time.Date(2026, 6, 23, 14, 0, 0, 0, time.UTC)
	client := &fakeTinyFishClient{
		search: tinyfish.SearchResponse{
			Results: []tinyfish.SearchResult{
				{
					Title:    "Software Engineer Intern",
					URL:      "https://meridian.example/careers/software-engineer-intern",
					Snippet:  "Software engineering internship. Posted 3 days ago.",
					SiteName: "Meridian Robotics",
				},
			},
		},
		fetch: tinyfish.FetchResponse{
			Results: []tinyfish.FetchResult{
				{
					URL:      "https://meridian.example/careers/software-engineer-intern",
					Title:    "Software Engineer Intern",
					Markdown: "Software Engineer Intern\n\nSingapore\n\nPosted 3 days ago. Build robotics platform services during a 2026 internship.",
				},
			},
		},
	}
	extractor := NewTinyFishSearchExtractor(client)
	extractor.now = func() time.Time { return now }

	result, err := extractor.Extract(context.Background(), Source{
		ID:       "tinyfish-relative-date",
		Name:     "Meridian Robotics",
		URL:      "tinyfish://search/relative-date",
		Tier:     TierSearchDiscovery,
		Metadata: map[string]string{"query": "software engineer intern"},
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	want := time.Date(2026, 6, 20, 14, 0, 0, 0, time.UTC)
	if result.Jobs[0].PostedAt == nil || !result.Jobs[0].PostedAt.Equal(want) {
		t.Fatalf("posted at = %v, want %s", result.Jobs[0].PostedAt, want)
	}
}

func TestTinyFishAIExtractorFetchesMessyPageAndNormalizesMultipleJobs(t *testing.T) {
	now := time.Date(2026, 6, 23, 14, 0, 0, 0, time.UTC)
	client := &fakeTinyFishClient{
		fetch: tinyfish.FetchResponse{
			Results: []tinyfish.FetchResult{{
				URL:   "https://example.com/careers",
				Title: "Meridian Robotics Careers",
				Markdown: `
# Meridian Robotics careers

## Software Engineer Intern, AI Platform
Location: Singapore
Apply: https://example.com/apply/intern-ai
Posted June 20, 2026
Build Go and Python services for robotics AI systems during a 2026 internship.

## New Grad Backend Engineer
Location: New York, United States
Apply URL: https://example.com/apply/new-grad-backend
Posted 3 days ago
Own distributed systems, PostgreSQL, and Redis infrastructure for robot fleets.

## Office Manager
Location: Singapore
Apply: https://example.com/apply/office
Keep the office running.
`,
			}},
		},
	}
	extractor := NewTinyFishAIExtractor(client)
	extractor.now = func() time.Time { return now }

	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_ai_messy",
		Name: "Meridian Robotics",
		URL:  "https://example.com/careers",
		Tier: TierAIExtraction,
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if client.gotFetch.Format != "markdown" || len(client.gotFetch.URLs) != 1 || client.gotFetch.URLs[0] != "https://example.com/careers" {
		t.Fatalf("fetch request = %#v", client.gotFetch)
	}
	if result.Strategy != TierAIExtraction {
		t.Fatalf("strategy = %q, want %q", result.Strategy, TierAIExtraction)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("jobs = %d, want 2: %#v", len(result.Jobs), result.Jobs)
	}
	first := result.Jobs[0]
	if first.Company != "Meridian Robotics" || first.Level != "internship" || first.Country != "Singapore" || first.ApplyURL != "https://example.com/apply/intern-ai" {
		t.Fatalf("first job = %#v", first)
	}
	second := result.Jobs[1]
	if second.Level != "new_grad" || second.Country != "US" || second.PostedAt == nil || !second.PostedAt.Equal(time.Date(2026, 6, 20, 14, 0, 0, 0, time.UTC)) {
		t.Fatalf("second job = %#v", second)
	}
	assertEvidence(t, result.RawEvidence, "ai_fetch_url", "https://example.com/careers")
}

func TestTinyFishAIExtractorReturnsNoJobsForIrrelevantPage(t *testing.T) {
	client := &fakeTinyFishClient{
		fetch: tinyfish.FetchResponse{
			Results: []tinyfish.FetchResult{{
				URL:      "https://example.com/about",
				Title:    "About Meridian",
				Markdown: "About our company, investors, press, and office culture.",
			}},
		},
	}
	extractor := NewTinyFishAIExtractor(client)

	_, err := extractor.Extract(context.Background(), Source{
		ID:   "source_ai_empty",
		Name: "Meridian Robotics",
		URL:  "https://example.com/about",
		Tier: TierAIExtraction,
	})
	if err != ErrNoJobs {
		t.Fatalf("err = %v, want ErrNoJobs", err)
	}
}

func TestTinyFishAIExtractorRejectsGenericEngineeringContentHeadline(t *testing.T) {
	client := &fakeTinyFishClient{
		fetch: tinyfish.FetchResponse{
			Results: []tinyfish.FetchResult{{
				URL:   "https://example.com/careers",
				Title: "Example careers",
				Markdown: `
## Transforming engineers: How we grew AI coding adoption
Our software engineering interns and new graduates use AI coding tools.
Location: London, United Kingdom
`,
			}},
		},
	}
	extractor := NewTinyFishAIExtractor(client)

	_, err := extractor.Extract(context.Background(), Source{
		ID:   "source_ai_generic_headline",
		Name: "Example",
		URL:  "https://example.com/careers",
		Tier: TierAIExtraction,
	})
	if err != ErrNoJobs {
		t.Fatalf("err = %v, want ErrNoJobs", err)
	}
}

func TestTinyFishAIExtractorDeduplicatesRepeatedJobBlocks(t *testing.T) {
	client := &fakeTinyFishClient{
		fetch: tinyfish.FetchResponse{
			Results: []tinyfish.FetchResult{{
				URL:   "https://example.com/careers",
				Title: "Example careers",
				Markdown: `
## Graduate Software Engineer (2027 Start)
Location: New York, United States
Apply: https://example.com/apply/graduate-software-engineer

## Graduate Software Engineer (2027 Start)
Location: New York, United States
Apply: https://example.com/apply/graduate-software-engineer
`,
			}},
		},
	}
	extractor := NewTinyFishAIExtractor(client)

	result, err := extractor.Extract(context.Background(), Source{
		ID:   "source_ai_duplicate_blocks",
		Name: "Example",
		URL:  "https://example.com/careers",
		Tier: TierAIExtraction,
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1: %#v", len(result.Jobs), result.Jobs)
	}
}

func TestTinyFishAIExtractorRejectsListingRootAsApplyURL(t *testing.T) {
	client := &fakeTinyFishClient{
		fetch: tinyfish.FetchResponse{
			Results: []tinyfish.FetchResult{{
				URL:   "https://example.com/join-us/jobs/",
				Title: "Example jobs",
				Markdown: `
## Graduate Software Engineer (2027 Start)
Location: New York, United States
Build trading infrastructure as a graduate software engineer.
`,
			}},
		},
	}
	extractor := NewTinyFishAIExtractor(client)

	_, err := extractor.Extract(context.Background(), Source{
		ID:   "source_ai_listing_root",
		Name: "Example",
		URL:  "https://example.com/join-us/jobs/",
		Tier: TierAIExtraction,
	})
	if err != ErrNoJobs {
		t.Fatalf("err = %v, want ErrNoJobs", err)
	}
}

func TestTinyFishAgentExtractorRunsAgentAndNormalizesJobs(t *testing.T) {
	client := &fakeTinyFishAgentClient{
		run: tinyfish.AutomationRunResponse{
			RunID:      "run_agent_1",
			Status:     "COMPLETED",
			NumOfSteps: 6,
			Result: json.RawMessage(`{
				"jobs": [{
					"company": "Example Systems",
					"title": "Software Engineer Intern, AI Platform",
					"location": "Singapore",
					"country": "Singapore",
					"apply_url": "https://example.com/apply",
					"source_url": "https://example.com/careers",
					"level": "internship",
					"role_family": "software",
					"confidence": 0.84,
					"evidence": "Agent found a live internship card after opening the careers page."
				}]
			}`),
		},
	}
	extractor := NewTinyFishAgentExtractor(client)

	result, err := extractor.Extract(context.Background(), Source{
		ID:   "agent-source",
		Name: "Example careers",
		URL:  "https://example.com/careers",
		Tier: TierBrowserAgent,
		Metadata: map[string]string{
			"goal":            "Find software engineering internships.",
			"browser_profile": "lite",
		},
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}

	if client.gotRequest.URL != "https://example.com/careers" || client.gotRequest.Goal != "Find software engineering internships." {
		t.Fatalf("agent request = %#v", client.gotRequest)
	}
	if client.gotRequest.BrowserProfile != "lite" {
		t.Fatalf("browser profile = %q, want lite", client.gotRequest.BrowserProfile)
	}
	if client.gotRequest.OutputSchema["type"] != "object" {
		t.Fatalf("output schema = %#v, want object schema", client.gotRequest.OutputSchema)
	}
	if result.Strategy != TierBrowserAgent {
		t.Fatalf("strategy = %q, want %q", result.Strategy, TierBrowserAgent)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	job := result.Jobs[0]
	if job.Company != "Example Systems" || job.Title != "Software Engineer Intern, AI Platform" {
		t.Fatalf("job = %#v", job)
	}
	if job.Country != "Singapore" || job.Strategy != TierBrowserAgent {
		t.Fatalf("job normalized fields = %#v", job)
	}
	assertEvidence(t, result.RawEvidence, "agent_run_id", "run_agent_1")
	assertEvidence(t, result.RawEvidence, "agent_status", "COMPLETED")
	assertEvidence(t, result.RawEvidence, "agent_steps", "6")
}

func TestTinyFishAgentExtractorReturnsNoJobsForEmptyResult(t *testing.T) {
	client := &fakeTinyFishAgentClient{
		run: tinyfish.AutomationRunResponse{
			RunID:  "run_empty",
			Status: "COMPLETED",
			Result: json.RawMessage(`{"jobs":[]}`),
		},
	}
	extractor := NewTinyFishAgentExtractor(client)

	_, err := extractor.Extract(context.Background(), Source{
		ID:   "agent-source",
		Name: "Example careers",
		URL:  "https://example.com/careers",
		Tier: TierBrowserAgent,
	})
	if err != ErrNoJobs {
		t.Fatalf("err = %v, want ErrNoJobs", err)
	}
}

func TestTinyFishAgentExtractorParsesFencedJSONResult(t *testing.T) {
	client := &fakeTinyFishAgentClient{
		run: tinyfish.AutomationRunResponse{
			RunID:  "run_fenced",
			Status: "COMPLETED",
			Result: json.RawMessage("```json\n{\"jobs\":[{\"company\":\"Meridian Robotics\",\"title\":\"Software Engineering Intern, Autonomy\",\"location\":\"Singapore\",\"apply_url\":\"https://meridian.example/apply\",\"evidence\":\"Agent opened the internship card.\"}]}\n```"),
		},
	}
	extractor := NewTinyFishAgentExtractor(client)

	result, err := extractor.Extract(context.Background(), Source{
		ID:   "agent-fenced",
		Name: "Meridian Robotics",
		URL:  "https://meridian.example/careers",
		Tier: TierBrowserAgent,
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(result.Jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(result.Jobs))
	}
	if got := result.Jobs[0].Title; got != "Software Engineering Intern, Autonomy" {
		t.Fatalf("title = %q, want fenced JSON job title", got)
	}
}

func TestTinyFishAgentExtractorParsesProseWrappedJSONArray(t *testing.T) {
	client := &fakeTinyFishAgentClient{
		run: tinyfish.AutomationRunResponse{
			RunID:  "run_array",
			Status: "COMPLETED",
			Result: json.RawMessage("I found these roles:\n[{\"company\":\"Quanta Ledger\",\"title\":\"New Grad Software Engineer, Trading Infrastructure\",\"country\":\"US\",\"apply_url\":\"https://quanta.example/apply\"}]\nDone."),
		},
	}
	extractor := NewTinyFishAgentExtractor(client)

	result, err := extractor.Extract(context.Background(), Source{
		ID:   "agent-array",
		Name: "Quanta Ledger",
		URL:  "https://quanta.example/careers",
		Tier: TierBrowserAgent,
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(result.Jobs) != 1 || result.Jobs[0].Company != "Quanta Ledger" {
		t.Fatalf("jobs = %#v, want job from prose-wrapped array", result.Jobs)
	}
}

func TestTinyFishAgentExtractorPollsAsyncRunAndNormalizesJobs(t *testing.T) {
	client := &fakeTinyFishAsyncAgentClient{
		start: tinyfish.AutomationResponse{RunID: "run_async_1"},
		runs: []tinyfish.AutomationRunResponse{
			{RunID: "run_async_1", Status: "RUNNING"},
			{
				RunID:      "run_async_1",
				Status:     "COMPLETED",
				NumOfSteps: 8,
				Result: json.RawMessage(`{
					"jobs": [{
						"company": "Async Systems",
						"title": "New Grad Software Engineer, Infrastructure",
						"location": "New York, NY",
						"country": "US",
						"apply_url": "https://example.com/apply",
						"source_url": "https://example.com/careers",
						"level": "new_grad",
						"role_family": "infra",
						"confidence": 0.86,
						"evidence": "Async run opened the role detail page."
					}]
				}`),
			},
		},
	}
	extractor := NewTinyFishAgentExtractor(client)

	result, err := extractor.Extract(context.Background(), Source{
		ID:   "agent-source",
		Name: "Async careers",
		URL:  "https://example.com/careers",
		Tier: TierBrowserAgent,
		Metadata: map[string]string{
			"poll_interval": "1ms",
		},
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if client.runCalls != 0 {
		t.Fatalf("RunAutomation calls = %d, want async path", client.runCalls)
	}
	if client.getCalls != 2 {
		t.Fatalf("GetAutomationRun calls = %d, want 2", client.getCalls)
	}
	if client.gotStart.URL != "https://example.com/careers" || client.gotStart.Goal == "" {
		t.Fatalf("start request = %#v", client.gotStart)
	}
	if len(result.Jobs) != 1 || result.Jobs[0].Company != "Async Systems" {
		t.Fatalf("jobs = %#v", result.Jobs)
	}
	assertEvidence(t, result.RawEvidence, "agent_mode", "async")
	assertEvidence(t, result.RawEvidence, "agent_polls", "2")
}

func TestTinyFishAgentPollIntervalIsBounded(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]string
		want     time.Duration
	}{
		{
			name:     "default",
			metadata: nil,
			want:     defaultTinyFishAgentPollInterval,
		},
		{
			name:     "hot poll is raised to minimum",
			metadata: map[string]string{"poll_interval": "1ms"},
			want:     minTinyFishAgentPollInterval,
		},
		{
			name:     "runaway poll is capped",
			metadata: map[string]string{"agent_poll_interval": "10m"},
			want:     maxTinyFishAgentPollInterval,
		},
		{
			name:     "invalid falls back",
			metadata: map[string]string{"poll_interval": "soon"},
			want:     defaultTinyFishAgentPollInterval,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentPollInterval(Source{Metadata: tt.metadata})
			if got != tt.want {
				t.Fatalf("agentPollInterval = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestTinyFishAgentExtractorCancelsAsyncRunOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeTinyFishAsyncAgentClient{
		start: tinyfish.AutomationResponse{RunID: "run_cancel_1"},
		runs: []tinyfish.AutomationRunResponse{
			{RunID: "run_cancel_1", Status: "RUNNING"},
		},
		cancel: tinyfish.AutomationCancelResponse{RunID: "run_cancel_1", Status: "CANCELLED"},
		onGet: func(call int) {
			if call == 1 {
				cancel()
			}
		},
	}
	extractor := NewTinyFishAgentExtractor(client)

	result, err := extractor.Extract(ctx, Source{
		ID:       "agent-source",
		Name:     "Async careers",
		URL:      "https://example.com/careers",
		Tier:     TierBrowserAgent,
		Metadata: map[string]string{"poll_interval": time.Hour.String()},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if client.cancelRunID != "run_cancel_1" {
		t.Fatalf("cancelRunID = %q, want run_cancel_1", client.cancelRunID)
	}
	assertEvidence(t, result.RawEvidence, "agent_run_id", "run_cancel_1")
	assertEvidence(t, result.RawEvidence, "agent_cancel_status", "CANCELLED")
}

type fakeTinyFishClient struct {
	search         tinyfish.SearchResponse
	fetch          tinyfish.FetchResponse
	fetchResponses []tinyfish.FetchResponse
	fetchErrors    []error

	gotSearch  tinyfish.SearchRequest
	gotFetch   tinyfish.FetchRequest
	gotFetches []tinyfish.FetchRequest
}

type tinyFishQualityFixture struct {
	Name    string                 `json:"name"`
	Now     string                 `json:"now"`
	Targets tinyFishQualityTargets `json:"targets"`
	Source  tinyFishQualitySource  `json:"source"`
	Cases   []tinyFishQualityCase  `json:"cases"`
}

type tinyFishQualityTargets struct {
	Precision     float64 `json:"precision"`
	GoodRecall    float64 `json:"good_recall"`
	JunkRejection float64 `json:"junk_rejection"`
}

type tinyFishQualitySource struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	URL      string            `json:"url"`
	Metadata map[string]string `json:"metadata"`
}

type tinyFishQualityCase struct {
	ID     string                `json:"id"`
	Label  string                `json:"label"`
	Reason string                `json:"reason"`
	Search tinyfish.SearchResult `json:"search"`
	Fetch  tinyfish.FetchResult  `json:"fetch"`
}

type tinyFishQualityReport struct {
	Fixture       string                    `json:"fixture"`
	Targets       tinyFishQualityTargets    `json:"targets"`
	Precision     float64                   `json:"precision"`
	GoodRecall    float64                   `json:"good_recall"`
	JunkRejection float64                   `json:"junk_rejection"`
	Accepted      int                       `json:"accepted"`
	Rejected      int                       `json:"rejected"`
	AcceptedGood  int                       `json:"accepted_good"`
	RejectedGood  int                       `json:"rejected_good"`
	AcceptedJunk  int                       `json:"accepted_junk"`
	RejectedJunk  int                       `json:"rejected_junk"`
	Decisions     []tinyFishQualityDecision `json:"decisions"`
}

type tinyFishQualityDecision struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	URL      string `json:"url"`
	Reason   string `json:"reason"`
	Accepted bool   `json:"accepted"`
	Title    string `json:"title,omitempty"`
	Company  string `json:"company,omitempty"`
	Location string `json:"location,omitempty"`
}

func (c *fakeTinyFishClient) Search(ctx context.Context, request tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
	if err := ctx.Err(); err != nil {
		return tinyfish.SearchResponse{}, err
	}
	c.gotSearch = request
	return c.search, nil
}

func (c *fakeTinyFishClient) Fetch(ctx context.Context, request tinyfish.FetchRequest) (tinyfish.FetchResponse, error) {
	if err := ctx.Err(); err != nil {
		return tinyfish.FetchResponse{}, err
	}
	c.gotFetch = request
	call := len(c.gotFetches)
	c.gotFetches = append(c.gotFetches, request)
	if call < len(c.fetchErrors) && c.fetchErrors[call] != nil {
		return tinyfish.FetchResponse{}, c.fetchErrors[call]
	}
	if call < len(c.fetchResponses) {
		return c.fetchResponses[call], nil
	}
	return c.fetch, nil
}

type fakeTinyFishAgentClient struct {
	run        tinyfish.AutomationRunResponse
	gotRequest tinyfish.AutomationRequest
}

func (c *fakeTinyFishAgentClient) RunAutomation(ctx context.Context, request tinyfish.AutomationRequest) (tinyfish.AutomationRunResponse, error) {
	if err := ctx.Err(); err != nil {
		return tinyfish.AutomationRunResponse{}, err
	}
	c.gotRequest = request
	return c.run, nil
}

type fakeTinyFishAsyncAgentClient struct {
	start  tinyfish.AutomationResponse
	runs   []tinyfish.AutomationRunResponse
	cancel tinyfish.AutomationCancelResponse
	onGet  func(call int)

	gotStart    tinyfish.AutomationRequest
	getCalls    int
	runCalls    int
	cancelRunID string
}

func (c *fakeTinyFishAsyncAgentClient) RunAutomation(ctx context.Context, request tinyfish.AutomationRequest) (tinyfish.AutomationRunResponse, error) {
	c.runCalls++
	return tinyfish.AutomationRunResponse{}, nil
}

func (c *fakeTinyFishAsyncAgentClient) StartAutomation(ctx context.Context, request tinyfish.AutomationRequest) (tinyfish.AutomationResponse, error) {
	if err := ctx.Err(); err != nil {
		return tinyfish.AutomationResponse{}, err
	}
	c.gotStart = request
	return c.start, nil
}

func (c *fakeTinyFishAsyncAgentClient) GetAutomationRun(ctx context.Context, runID string) (tinyfish.AutomationRunResponse, error) {
	if err := ctx.Err(); err != nil {
		return tinyfish.AutomationRunResponse{}, err
	}
	c.getCalls++
	if c.onGet != nil {
		c.onGet(c.getCalls)
	}
	if len(c.runs) == 0 {
		return tinyfish.AutomationRunResponse{RunID: runID, Status: "COMPLETED", Result: json.RawMessage(`{"jobs":[]}`)}, nil
	}
	idx := c.getCalls - 1
	if idx >= len(c.runs) {
		idx = len(c.runs) - 1
	}
	return c.runs[idx], nil
}

func (c *fakeTinyFishAsyncAgentClient) CancelAutomation(ctx context.Context, runID string) (tinyfish.AutomationCancelResponse, error) {
	if err := ctx.Err(); err != nil {
		return tinyfish.AutomationCancelResponse{}, err
	}
	c.cancelRunID = runID
	if c.cancel.RunID == "" {
		c.cancel.RunID = runID
	}
	return c.cancel, nil
}

func assertEvidence(t *testing.T, evidence []Evidence, field string, want string) {
	t.Helper()
	for _, item := range evidence {
		if item.Field == field && item.Text == want {
			return
		}
	}
	t.Fatalf("evidence %q = %q not found in %#v", field, want, evidence)
}
