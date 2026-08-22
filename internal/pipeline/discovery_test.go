package pipeline

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hwennnn/radar/internal/source/tinyfish"
)

type discoveryClientFake struct {
	search func(context.Context, tinyfish.SearchRequest) (tinyfish.SearchResponse, error)
	fetch  func(context.Context, tinyfish.FetchRequest) (tinyfish.FetchResponse, error)
}

func (f discoveryClientFake) Search(ctx context.Context, request tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
	if f.search == nil {
		return tinyfish.SearchResponse{}, nil
	}
	return f.search(ctx, request)
}

func (f discoveryClientFake) Fetch(ctx context.Context, request tinyfish.FetchRequest) (tinyfish.FetchResponse, error) {
	if f.fetch == nil {
		return tinyfish.FetchResponse{}, nil
	}
	return f.fetch(ctx, request)
}

type discoveryRepositoryFake struct {
	due              []DiscoveryCandidateRecord
	seeded           []DiscoveryCandidate
	failures         []error
	failureNext      []time.Time
	successSources   []Source
	successCounts    []int
	promoteOnSuccess bool
	promotedSources  []Source
	demoted          int
	rejectedSignals  []string
}

func (f *discoveryRepositoryFake) SeedDiscoveryCandidates(_ context.Context, candidates []DiscoveryCandidate) error {
	f.seeded = append([]DiscoveryCandidate(nil), candidates...)
	return nil
}

func (f *discoveryRepositoryFake) ListDueDiscoveryCandidates(context.Context, time.Time, int) ([]DiscoveryCandidateRecord, error) {
	return append([]DiscoveryCandidateRecord(nil), f.due...), nil
}

func (f *discoveryRepositoryFake) RecordDiscoveryFailure(_ context.Context, _ DiscoveryCandidateRecord, _ *Source, cause error, _, next time.Time) error {
	f.failures = append(f.failures, cause)
	f.failureNext = append(f.failureNext, next)
	return nil
}

func TestSourceFromDiscoveryURLRecognizesGemAndOfficialCompanyRoutes(t *testing.T) {
	tests := []struct {
		name      string
		candidate DiscoveryCandidate
		raw       string
		provider  string
		url       string
	}{
		{"groq gem", DiscoveryCandidate{ID: "groq", Name: "Groq", Website: "https://groq.com"}, "https://jobs.gem.com/groq/jobs/abc", "gem", "https://jobs.gem.com/groq"},
		{"citadel sitemap", DiscoveryCandidate{ID: "citadel", Name: "Citadel", Website: "https://www.citadel.com"}, "https://www.citadel.com/career-sitemap.xml", "citadel_careers", "https://www.citadel.com/career-sitemap.xml"},
		{"cursor", DiscoveryCandidate{ID: "cursor", Name: "Cursor", Website: "https://www.cursor.com"}, "https://cursor.com/careers", "cursor_careers", "https://cursor.com/careers"},
		{"d e shaw", DiscoveryCandidate{ID: "d-e-shaw", Name: "D. E. Shaw", Website: "https://www.deshaw.com"}, "https://www.deshaw.com/careers/internships", "deshaw_careers", "https://www.deshaw.com/careers/internships"},
		{"groq official", DiscoveryCandidate{ID: "groq", Name: "Groq", Website: "https://groq.com"}, "https://groq.com/careers-at-groq", "groq_careers", "https://groq.com/careers-at-groq"},
		{"old mission", DiscoveryCandidate{ID: "old-mission-capital", Name: "Old Mission Capital", Website: "https://www.oldmissioncapital.com"}, "https://www.oldmissioncapital.com/careers/", "oldmission_careers", "https://www.oldmissioncapital.com/careers/"},
		{"sig", DiscoveryCandidate{ID: "susquehanna", Name: "Susquehanna International Group", Website: "https://sig.com"}, "https://careers.sig.com/jobs", "sig_careers", "https://careers.sig.com/jobs"},
		{"tiktok", DiscoveryCandidate{ID: "tiktok", Name: "TikTok", Website: "https://careers.tiktok.com"}, "https://careers.tiktok.com/position?keywords=software%20engineer%20intern&type=2", "tiktok_careers", "https://careers.tiktok.com/position?keywords=software%20engineer%20intern&type=2"},
		{"two sigma", DiscoveryCandidate{ID: "two-sigma", Name: "Two Sigma", Website: "https://www.twosigma.com"}, "https://www.twosigma.com/careers/internships/", "twosigma_careers", "https://www.twosigma.com/careers/internships/"},
		{"yc jobs", DiscoveryCandidate{ID: "boundary", Name: "Boundary"}, "https://www.ycombinator.com/companies/boundary/jobs/abc-software-engineer-intern", "yc_jobs", "https://www.ycombinator.com/companies/boundary/jobs"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved, ok := sourceFromDiscoveryURL(test.candidate, test.raw, 0.98, "test")
			if !ok || resolved.Source.Provider != test.provider || resolved.Source.URL != test.url {
				t.Fatalf("resolved=%#v ok=%v", resolved, ok)
			}
			if err := validCatalogID(resolved.Source.ID); err != nil {
				t.Fatalf("resolved source id %q is invalid: %v", resolved.Source.ID, err)
			}
		})
	}
}

func TestDiscoveryQueryCoversOfficialSiteAndGem(t *testing.T) {
	query := discoveryQuery(DiscoveryCandidate{ID: "groq", Name: "Groq", Website: "https://groq.com"})
	for _, expected := range []string{"site:groq.com", "site:jobs.gem.com", "site:jobs.ashbyhq.com"} {
		if !strings.Contains(query, expected) {
			t.Fatalf("query %q is missing %q", query, expected)
		}
	}
}

func TestSnapshotOwnershipMismatchUsesExtractorReportedEmployer(t *testing.T) {
	source := Source{ID: "auto-acme", Company: "Acme AI", Provider: "ashby"}
	if reported, mismatch := snapshotOwnershipMismatch(source, []Observation{{Company: "Acme AI", ReportedCompany: "Built In Chicago"}}); !mismatch || reported != "Built In Chicago" {
		t.Fatalf("reported=%q mismatch=%v, want ownership rejection", reported, mismatch)
	}
	if reported, mismatch := snapshotOwnershipMismatch(Source{Company: "Acme"}, []Observation{{Company: "Acme", ReportedCompany: "Acme, Inc."}}); mismatch || reported != "" {
		t.Fatalf("reported=%q mismatch=%v, want matching identity", reported, mismatch)
	}
}

func (f *discoveryRepositoryFake) RecordDiscoverySuccess(_ context.Context, _ DiscoveryCandidateRecord, source Source, observed int, _ float64, _ string, _, _ time.Time) (bool, error) {
	f.successSources = append(f.successSources, source)
	f.successCounts = append(f.successCounts, observed)
	return f.promoteOnSuccess, nil
}

func (f *discoveryRepositoryFake) ListPromotedSources(context.Context) ([]Source, error) {
	return append([]Source(nil), f.promotedSources...), nil
}

func (f *discoveryRepositoryFake) ListDiscoveredSources(context.Context) ([]Source, error) {
	return append([]Source(nil), f.promotedSources...), nil
}

func (f *discoveryRepositoryFake) DemoteUnhealthyDiscoveredSources(context.Context, int, time.Time) (int, error) {
	return f.demoted, nil
}

func (f *discoveryRepositoryFake) RecordRejectedMarketSignal(_ context.Context, observation Observation, code string, _ time.Time) error {
	f.rejectedSignals = append(f.rejectedSignals, observation.Company+":"+code)
	return nil
}

func TestSourceFromDiscoveryURLNormalizesStructuredBoards(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "acme-ai", Name: "Acme AI"}
	tests := []struct {
		name     string
		raw      string
		provider string
		url      string
	}{
		{"greenhouse", "https://job-boards.greenhouse.io/acme/jobs/123", "greenhouse", "https://job-boards.greenhouse.io/acme"},
		{"ashby", "https://jobs.ashbyhq.com/acme/123", "ashby", "https://jobs.ashbyhq.com/acme"},
		{"lever api", "https://api.lever.co/v0/postings/acme/123", "lever", "https://jobs.lever.co/acme"},
		{"workday", "https://acme.wd5.myworkdayjobs.com/en-US/External/job/Seattle/Engineer_123", "workday", "https://acme.wd5.myworkdayjobs.com/External"},
		{"smartrecruiters", "https://careers.smartrecruiters.com/Acme/jobs", "smartrecruiters", "https://careers.smartrecruiters.com/acme"},
		{"workable", "https://apply.workable.com/acme/j/ABC", "workable", "https://apply.workable.com/acme"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved, ok := sourceFromDiscoveryURL(candidate, test.raw, 0.9, "test")
			if !ok {
				t.Fatalf("sourceFromDiscoveryURL(%q) was not recognized", test.raw)
			}
			if resolved.Source.Provider != test.provider || resolved.Source.URL != test.url || resolved.Source.Company != candidate.Name {
				t.Fatalf("resolved = %#v", resolved)
			}
			if !strings.HasPrefix(resolved.Source.ID, "auto-acme-ai-") {
				t.Fatalf("source id %q is not stable auto id", resolved.Source.ID)
			}
		})
	}
}

func TestSourceFromDiscoveryURLRejectsCandidateMentionOnAnotherCompanyBoard(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "coreweave", Name: "CoreWeave", Website: "https://www.coreweave.com"}
	if _, ok := sourceFromDiscoveryURL(candidate, "https://jobs.ashbyhq.com/meticulous/role", 0.96, "CoreWeave job mention"); ok {
		t.Fatal("another company's board must not pass merely because a search result mentions the candidate")
	}
	if resolved, ok := sourceFromDiscoveryURL(candidate, "https://jobs.ashbyhq.com/coreweave/role", 0.96, "CoreWeave careers"); !ok || resolved.Source.URL != "https://jobs.ashbyhq.com/coreweave" {
		t.Fatalf("candidate-owned board rejected: resolved=%#v ok=%v", resolved, ok)
	}
	ssi := DiscoveryCandidate{ID: "safe-superintelligence", Name: "Safe Superintelligence", Website: "https://ssi.inc"}
	if _, ok := sourceFromDiscoveryURL(ssi, "https://jobs.ashbyhq.com/ssi", 0.96, "SSI careers"); !ok {
		t.Fatal("official-domain acronym should be accepted")
	}
	tiktok := DiscoveryCandidate{ID: "tiktok", Name: "TikTok", Website: "https://careers.tiktok.com"}
	if _, ok := sourceFromDiscoveryURL(tiktok, "https://philips.wd3.myworkdayjobs.com/jobs-and-careers", 0.96, "TikTok mentioned on Philips"); ok {
		t.Fatal("generic careers subdomain must not make another company's Workday route look owned")
	}
	radix := DiscoveryCandidate{ID: "radix-trading", Name: "Radix Trading", Website: "https://radixtrading.co"}
	if _, ok := sourceFromDiscoveryURL(radix, "https://job-boards.greenhouse.io/radixexperienced", 0.96, "Radix careers"); ok {
		t.Fatal("experienced-only route must not enter early-career routine monitoring")
	}
	millennium := DiscoveryCandidate{ID: "millennium", Name: "Millennium", Website: "https://www.mlp.com"}
	for _, unrelated := range []string{
		"https://apply.workable.com/millennium-5",
		"https://apply.workable.com/millennium-health",
		"https://apply.workable.com/millennium-hotel-and-resorts",
		"https://job-boards.greenhouse.io/themillenniumalliance",
	} {
		if _, ok := sourceFromDiscoveryURL(millennium, unrelated, 0.96, "Millennium careers"); ok {
			t.Fatalf("ambiguous unrelated route %q must remain quarantined", unrelated)
		}
	}
	coda := DiscoveryCandidate{ID: "coda", Name: "Coda", Website: "https://coda.io"}
	if _, ok := sourceFromDiscoveryURL(coda, "https://apply.workable.com/coda-logistics-and-distribution", 0.96, "Coda careers"); ok {
		t.Fatal("an unrelated logistics company sharing the candidate prefix must remain quarantined")
	}
}

func TestSourceFromDiscoveryURLRejectsRecruiterBoardUsingCandidateBrand(t *testing.T) {
	citadel := DiscoveryCandidate{ID: "citadel", Name: "Citadel", Website: "https://www.citadel.com"}
	if _, ok := sourceFromDiscoveryURL(citadel, "https://careers.smartrecruiters.com/CitadelSearch", 0.96, "Citadel careers"); ok {
		t.Fatal("a recruiting firm that appends Search to the candidate brand must not be promoted")
	}
	if resolved, ok := sourceFromDiscoveryURL(citadel, "https://job-boards.greenhouse.io/citadelsecurities", 0.96, "Citadel careers"); !ok || resolved.Source.URL != "https://job-boards.greenhouse.io/citadelsecurities" {
		t.Fatalf("legitimate corporate suffix was rejected: resolved=%#v ok=%v", resolved, ok)
	}
}

func TestOfficialCareersFallbackUsesOnlyResearchedCompanyDomain(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "aumovio", Name: "Aumovio", Website: "https://jobs.aumovio.com/en/careers"}
	resolved := officialCareersFallback(candidate)
	if len(resolved) != 1 {
		t.Fatalf("official fallback = %#v", resolved)
	}
	got := resolved[0]
	if got.Source.Provider != "official_careers" || got.Source.URL != "https://jobs.aumovio.com" || got.Source.Company != "Aumovio" {
		t.Fatalf("official fallback source = %#v", got.Source)
	}
	if !DiscoveryRouteMatchesCandidate(candidate, got.Source.Provider, got.Source.URL) {
		t.Fatal("same-site official fallback failed ownership validation")
	}
	for _, blocked := range []DiscoveryCandidate{
		{ID: "aggregator", Name: "Aggregator", Website: "https://startup.jobs/company/acme"},
		{ID: "market", Name: "Unknown Market Result", Website: "https://unknown.example", Tags: []string{"auto-market-search"}},
		{ID: "missing", Name: "Missing"},
	} {
		if got := officialCareersFallback(blocked); len(got) != 0 {
			t.Fatalf("blocked fallback survived for %#v: %#v", blocked, got)
		}
	}
}

func TestCandidateEvidenceRequiresWholeBrand(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "old-mission-capital", Name: "Old Mission Capital"}
	if candidateEvidenceMatches(candidate, "Old Dominion Capital careers") {
		t.Fatal("partial company tokens should not establish source ownership")
	}
	if !candidateEvidenceMatches(candidate, "Open roles at Old Mission Capital") {
		t.Fatal("exact company evidence should match")
	}
	if !candidateEvidenceMatches(DiscoveryCandidate{ID: "xai", Name: "xAI"}, "xAI careers") {
		t.Fatal("single-token brand should match as a whole word")
	}
}

func TestResolveFetchedSourcesFindsBoardOnOfficialWebsite(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "perplexity", Name: "Perplexity", Website: "https://www.perplexity.ai"}
	resolved := resolveFetchedSources(candidate, []tinyfish.FetchResult{{
		URL: "https://www.perplexity.ai/careers", Title: "Perplexity Careers",
		Markdown: "Apply on [our jobs board](https://jobs.ashbyhq.com/perplexity).",
	}})
	if len(resolved) != 1 || resolved[0].Source.Provider != "ashby" || resolved[0].Source.URL != "https://jobs.ashbyhq.com/perplexity" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestDiscoveryRunnerPromotesOnlyAfterRealExtractorProbe(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "perplexity", Name: "Perplexity", Website: "https://www.perplexity.ai"}
	repository := &discoveryRepositoryFake{
		due:              []DiscoveryCandidateRecord{{DiscoveryCandidate: candidate, State: "pending"}},
		promoteOnSuccess: true,
		demoted:          1,
	}
	client := discoveryClientFake{search: func(_ context.Context, request tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
		if !strings.Contains(request.Query, "Perplexity") {
			t.Fatalf("unexpected query %q", request.Query)
		}
		return tinyfish.SearchResponse{Results: []tinyfish.SearchResult{{
			Title: "Perplexity Careers", URL: "https://jobs.ashbyhq.com/perplexity/role-1",
		}}}, nil
	}}
	extractorCalls := 0
	runner := DiscoveryRunner{
		Candidates: []DiscoveryCandidate{candidate}, Client: client, Store: repository,
		Extractor: extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
			extractorCalls++
			if source.Provider != "ashby" || source.URL != "https://jobs.ashbyhq.com/perplexity" {
				t.Fatalf("extractor source = %#v", source)
			}
			return completeExtraction(
				Observation{Company: "Perplexity", Title: "Software Engineer Intern"},
				Observation{Company: "Perplexity", Title: "Backend Engineer"},
			), nil
		}),
	}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if extractorCalls != 1 || len(repository.successSources) != 1 || len(repository.failures) != 0 {
		t.Fatalf("extractor=%d successes=%d failures=%d", extractorCalls, len(repository.successSources), len(repository.failures))
	}
	if report.CandidatesAttempted != 1 || report.SourcesResolved != 1 || report.SourcesHealthy != 1 || report.SourcesPromoted != 1 || report.SourcesDemoted != 1 {
		t.Fatalf("report = %#v", report)
	}
	if repository.successCounts[0] != 2 {
		t.Fatalf("observed count = %d, want 2", repository.successCounts[0])
	}
}

func TestDiscoveryRunnerPromotesMultipleHealthyRegionalBoards(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "doordash", Name: "DoorDash", Website: "https://careers.doordash.com"}
	repository := &discoveryRepositoryFake{
		due:              []DiscoveryCandidateRecord{{DiscoveryCandidate: candidate, State: "pending"}},
		promoteOnSuccess: true,
	}
	runner := DiscoveryRunner{
		Candidates: []DiscoveryCandidate{candidate}, Store: repository,
		Client: discoveryClientFake{search: func(context.Context, tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
			return tinyfish.SearchResponse{Results: []tinyfish.SearchResult{
				{Title: "DoorDash Careers", URL: "https://job-boards.greenhouse.io/doordash/jobs/1"},
				{Title: "DoorDash India Careers", URL: "https://job-boards.greenhouse.io/doordashindia/jobs/2"},
			}}, nil
		}},
		Extractor: extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
			return completeExtraction(Observation{Company: source.Company, Title: "Software Engineer Intern"}), nil
		}),
	}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.SourcesResolved != 2 || report.SourcesProbed != 2 || report.SourcesPromoted != 2 || len(repository.successSources) != 2 {
		t.Fatalf("report=%#v sources=%#v", report, repository.successSources)
	}
}

func TestDiscoveryRunnerPromotesOfficialRouteThroughSearchFetchExtractor(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "cursor", Name: "Cursor", Website: "https://www.cursor.com"}
	repository := &discoveryRepositoryFake{
		due:              []DiscoveryCandidateRecord{{DiscoveryCandidate: candidate, State: "pending"}},
		promoteOnSuccess: true,
	}
	structuredCalls, searchCalls := 0, 0
	runner := DiscoveryRunner{
		Candidates: []DiscoveryCandidate{candidate},
		Client: discoveryClientFake{search: func(_ context.Context, request tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
			if !strings.Contains(request.Query, "site:www.cursor.com") {
				t.Fatalf("official site scope missing from %q", request.Query)
			}
			return tinyfish.SearchResponse{Results: []tinyfish.SearchResult{{
				Title: "Careers at Cursor", URL: "https://cursor.com/careers",
			}}}, nil
		}},
		Extractor: NewDiscoveryAwareExtractor(
			extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
				structuredCalls++
				return ExtractionResult{}, errors.New("official route reached ATS extractor")
			}),
			extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
				searchCalls++
				if source.Provider != "cursor_careers" || source.URL != "https://cursor.com/careers" {
					t.Fatalf("search source=%#v", source)
				}
				return completeExtraction(Observation{Company: "Cursor", Title: "Software Engineer, New Grad"}), nil
			}),
		),
		Store: repository,
	}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if structuredCalls != 0 || searchCalls != 1 || report.SourcesProbed != 1 || report.SourcesPromoted != 1 || report.SourcesRejected != 0 {
		t.Fatalf("structured=%d search=%d report=%#v", structuredCalls, searchCalls, report)
	}
}

func TestDiscoveryRunnerEmitsStructuredTakeoverEvents(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "perplexity", Name: "Perplexity", Website: "https://www.perplexity.ai"}
	repository := &discoveryRepositoryFake{
		due:              []DiscoveryCandidateRecord{{DiscoveryCandidate: candidate, State: "pending"}},
		promoteOnSuccess: true,
	}
	var logs bytes.Buffer
	runner := DiscoveryRunner{
		Candidates: []DiscoveryCandidate{candidate},
		Client: discoveryClientFake{search: func(context.Context, tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
			return tinyfish.SearchResponse{Results: []tinyfish.SearchResult{{Title: "Perplexity careers", URL: "https://jobs.ashbyhq.com/perplexity"}}}, nil
		}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			return completeExtraction(Observation{Company: "Perplexity", Title: "Software Engineer Intern"}), nil
		}),
		Store:  repository,
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	output := logs.String()
	for _, expected := range []string{
		`"component":"radar_lite_discovery"`, `"event":"batch_started"`,
		`"event":"candidate_started"`, `"event":"tinyfish_search_completed"`, `"event":"candidate_resolved"`,
		`"event":"source_probe_started"`, `"event":"source_promoted"`,
		`"candidate_id":"perplexity"`, `"provider":"ashby"`, `"observed_count":1`, `"accepted_routes":1`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("structured logs missing %s:\n%s", expected, output)
		}
	}
}

func TestDiscoveryRunnerDoesNotLabelHealthyDuplicateAsEmpty(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "airwallex-alias", Name: "Airwallex", Website: "https://www.airwallex.com"}
	repository := &discoveryRepositoryFake{
		due: []DiscoveryCandidateRecord{{DiscoveryCandidate: candidate, State: "pending"}},
	}
	var logs bytes.Buffer
	runner := DiscoveryRunner{
		Candidates: []DiscoveryCandidate{candidate},
		Client: discoveryClientFake{search: func(context.Context, tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
			return tinyfish.SearchResponse{Results: []tinyfish.SearchResult{{Title: "Airwallex careers", URL: "https://jobs.ashbyhq.com/airwallex"}}}, nil
		}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			return completeExtraction(Observation{Company: "Airwallex", Title: "Software Engineer Intern"}), nil
		}),
		Store:  repository,
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	}

	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	output := logs.String()
	if !strings.Contains(output, `"event":"source_already_monitored"`) {
		t.Fatalf("healthy duplicate event missing:\n%s", output)
	}
	if strings.Contains(output, "empty source remains quarantined") {
		t.Fatalf("healthy duplicate was mislabeled as empty:\n%s", output)
	}
}

func TestDiscoverySearchDiagnosticsExposeRejectedStructuredNearMisses(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "coreweave", Name: "CoreWeave", Website: "https://www.coreweave.com"}
	matched, rejected := discoverySearchDiagnostics(candidate, []tinyfish.SearchResult{
		{Title: "CoreWeave role at Meticulous", URL: "https://jobs.ashbyhq.com/meticulous/role"},
		{Title: "CoreWeave careers", URL: "https://job-boards.greenhouse.io/coreweave"},
		{Title: "Unrelated", URL: "https://jobs.ashbyhq.com/unrelated"},
	})
	if matched != 2 || len(rejected) != 1 || rejected[0] != "https://jobs.ashbyhq.com/meticulous/role" {
		t.Fatalf("matched=%d rejected=%v", matched, rejected)
	}
}

func TestAssessDiscoverySnapshotQualityRejectsEventOnlyBoard(t *testing.T) {
	quality := assessDiscoverySnapshotQuality([]Observation{{
		Title: "Summer Work Experience Programme - Register Your Interest",
	}, {
		Title: "Join our talent community",
	}})
	if quality.Usable != 0 || quality.Relevant != 0 || quality.Rejected != 2 || len(quality.SampleTitles) != 2 {
		t.Fatalf("quality=%#v", quality)
	}
	mixed := assessDiscoverySnapshotQuality([]Observation{
		{Title: "Software Engineer Intern"},
		{Title: "Register Your Interest"},
	})
	if mixed.Usable != 1 || mixed.Relevant != 1 || mixed.Rejected != 1 {
		t.Fatalf("mixed quality=%#v", mixed)
	}
}

func TestAssessDiscoverySnapshotQualityRejectsActiveButIrrelevantBoard(t *testing.T) {
	quality := assessDiscoverySnapshotQuality([]Observation{
		{Title: "Project Coordinator"},
		{Title: "Head of Accounting"},
		{Title: "Director, Corporate Secretarial"},
	})
	if quality.Usable != 3 || quality.Relevant != 0 || quality.Rejected != 0 {
		t.Fatalf("quality=%#v", quality)
	}
}

func TestAssessDiscoverySnapshotQualityRejectsNonSoftwareEngineeringBoard(t *testing.T) {
	quality := assessDiscoverySnapshotQuality([]Observation{
		{Title: "Senior Mechanical Engineer"},
		{Title: "Structural Engineer"},
		{Title: "Hardware Validation Engineer"},
		{Title: "Business Developer"},
	})
	if quality.Usable != 4 || quality.Relevant != 0 || quality.Rejected != 0 {
		t.Fatalf("non-software engineering quality=%#v", quality)
	}

	technical := assessDiscoverySnapshotQuality([]Observation{
		{Title: "Senior Software Engineer"},
		{Title: "C++ Developer - Options Market Making"},
		{Title: "Machine Learning Research Scientist"},
	})
	if technical.Usable != 3 || technical.Relevant != 3 || technical.Rejected != 0 {
		t.Fatalf("software engineering quality=%#v", technical)
	}
}

func TestAssessDiscoverySnapshotQualityAcceptsQuantitativeAnalystInternship(t *testing.T) {
	quality := assessDiscoverySnapshotQuality([]Observation{{
		Company: "D. E. Shaw", Title: "Quantitative Analyst, Ph.D. Intern (New York) – Summer 2027",
	}})
	if quality.Usable != 1 || quality.Relevant != 1 || quality.Rejected != 0 {
		t.Fatalf("quality=%#v", quality)
	}
}

func TestDiscoveryRunnerRejectsEventOnlyBoardAndPromotesNextHealthyRoute(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "maven-securities", Name: "Maven Securities", Website: "https://www.mavensecurities.com"}
	repository := &discoveryRepositoryFake{
		due:              []DiscoveryCandidateRecord{{DiscoveryCandidate: candidate, State: "pending"}},
		promoteOnSuccess: true,
	}
	runner := DiscoveryRunner{
		Candidates: []DiscoveryCandidate{candidate},
		Client: discoveryClientFake{search: func(context.Context, tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
			return tinyfish.SearchResponse{Results: []tinyfish.SearchResult{
				{Title: "Maven Securities events", URL: "https://job-boards.greenhouse.io/mavensecuritiesevents"},
				{Title: "Maven Securities jobs", URL: "https://job-boards.greenhouse.io/mavensecuritiesholdingltd"},
			}}, nil
		}},
		Extractor: extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
			if strings.Contains(source.URL, "events") {
				return completeExtraction(Observation{Title: "Summer Work Experience Programme - Register Your Interest"}), nil
			}
			return completeExtraction(Observation{Title: "C++ Developer - Options Market Making"}), nil
		}),
		Store: repository,
	}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.SourcesResolved != 1 || report.SourcesPromoted != 1 || report.CandidatesFailed != 0 || len(repository.failures) != 0 || len(repository.successSources) != 1 {
		t.Fatalf("report=%#v failures=%v successes=%#v", report, repository.failures, repository.successSources)
	}
	if strings.Contains(repository.successSources[0].URL, "events") || !strings.Contains(repository.successSources[0].URL, "holdingltd") {
		t.Fatalf("promoted wrong source: %#v", repository.successSources[0])
	}
}

func TestDiscoveryRunnerBacksOffUnresolvedCandidate(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "missing-ai", Name: "Missing AI"}
	repository := &discoveryRepositoryFake{due: []DiscoveryCandidateRecord{{DiscoveryCandidate: candidate, State: "pending"}}}
	runner := DiscoveryRunner{
		Candidates: []DiscoveryCandidate{candidate},
		Client: discoveryClientFake{search: func(context.Context, tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
			return tinyfish.SearchResponse{}, nil
		}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			return ExtractionResult{}, errors.New("must not probe without a source")
		}),
		Store: repository,
	}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.CandidatesFailed != 1 || len(repository.failures) != 1 || !strings.Contains(repository.failures[0].Error(), "no structured ATS source") {
		t.Fatalf("report=%#v failures=%v", report, repository.failures)
	}
}

func TestDiscoveryRunnerParksCompanyWithoutQualityEvidenceBeforeResearch(t *testing.T) {
	candidate := DiscoveryCandidate{ID: "smallco", Name: "SmallCo", Tags: []string{"priority-1", "benchmark-speedyapply-2027"}}
	repository := &discoveryRepositoryFake{due: []DiscoveryCandidateRecord{{DiscoveryCandidate: candidate, State: "pending"}}}
	runner := DiscoveryRunner{
		Candidates: []DiscoveryCandidate{candidate}, EnforceCompanyQuality: true,
		Client: discoveryClientFake{search: func(context.Context, tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
			t.Fatal("low-signal company reached discovery search")
			return tinyfish.SearchResponse{}, nil
		}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			t.Fatal("low-signal company reached extractor")
			return ExtractionResult{}, nil
		}),
		Store: repository,
	}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.CandidatesFailed != 1 || len(repository.failures) != 1 {
		t.Fatalf("report=%#v failures=%v", report, repository.failures)
	}
	code, terminal := DiscoveryFailureClass(repository.failures[0])
	if code != DiscoveryFailureCompanyQuality || !terminal {
		t.Fatalf("quality outcome=%q terminal=%t", code, terminal)
	}
}

func TestDiscoveryRunnerExponentiallyBacksOffRepeatedFailures(t *testing.T) {
	now := time.Date(2026, 8, 18, 3, 0, 0, 0, time.UTC)
	candidate := DiscoveryCandidate{ID: "missing-ai", Name: "Missing AI"}
	repository := &discoveryRepositoryFake{due: []DiscoveryCandidateRecord{{
		DiscoveryCandidate: candidate, State: "retry", Attempts: 2,
	}}}
	runner := DiscoveryRunner{
		Candidates: []DiscoveryCandidate{candidate},
		Client: discoveryClientFake{search: func(context.Context, tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
			return tinyfish.SearchResponse{}, nil
		}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			return ExtractionResult{}, errors.New("unexpected probe")
		}),
		Store:      repository,
		RetryDelay: time.Hour,
		Now:        func() time.Time { return now },
	}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repository.failureNext) != 1 || !repository.failureNext[0].Equal(now.Add(4*time.Hour)) {
		t.Fatalf("next retry=%v, want %s", repository.failureNext, now.Add(4*time.Hour))
	}
	if got := runner.retryDelayFor(30); got != 7*24*time.Hour {
		t.Fatalf("retry cap=%s, want 168h", got)
	}
}

func TestDiscoveryRunnerRetriesTransientFailuresOnShortDurableSchedule(t *testing.T) {
	now := time.Date(2026, 8, 18, 3, 0, 0, 0, time.UTC)
	candidate := DiscoveryCandidate{ID: "recoverable-ai", Name: "Recoverable AI"}
	repository := &discoveryRepositoryFake{due: []DiscoveryCandidateRecord{{
		DiscoveryCandidate: candidate, State: "retry", Attempts: 2,
	}}}
	runner := DiscoveryRunner{
		Candidates: []DiscoveryCandidate{candidate},
		Client: discoveryClientFake{search: func(context.Context, tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
			return tinyfish.SearchResponse{}, &tinyfish.HTTPError{Method: "GET", StatusCode: 503}
		}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			return ExtractionResult{}, errors.New("unexpected probe")
		}),
		Store: repository,
		Now:   func() time.Time { return now },
	}

	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := now.Add(20 * time.Minute)
	if len(repository.failureNext) != 1 || !repository.failureNext[0].Equal(want) {
		t.Fatalf("next retry=%v, want %s", repository.failureNext, want)
	}
}

func TestTransientDiscoveryRetryHonorsProviderRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 18, 3, 0, 0, 0, time.UTC)
	retryAt, transient := transientDiscoveryRetryAt(&tinyfish.HTTPError{
		Method: "GET", StatusCode: 429, RetryAfterDelay: 45 * time.Minute,
	}, now, 0)
	if !transient || !retryAt.Equal(now.Add(45*time.Minute)) {
		t.Fatalf("retry_at=%s transient=%t", retryAt, transient)
	}
}

func TestMergeRoutineSourcesKeepsVerifiedSourceOnDuplicateRoute(t *testing.T) {
	base := []Source{{ID: "verified", Company: "Acme", Provider: "ashby", URL: "https://jobs.ashbyhq.com/acme"}}
	discovered := []Source{
		{ID: "auto-duplicate", Company: "Acme AI", Provider: "ashby", URL: "https://jobs.ashbyhq.com/Acme/"},
		{ID: "auto-new", Company: "New AI", Provider: "greenhouse", URL: "https://job-boards.greenhouse.io/newai"},
	}
	merged := MergeRoutineSources(base, discovered)
	if len(merged) != 2 {
		t.Fatalf("merged = %#v", merged)
	}
	for _, source := range merged {
		if source.Provider == "ashby" && source.ID != "verified" {
			t.Fatalf("discovered duplicate replaced verified source: %#v", source)
		}
	}
}
