package pipeline

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestMarketSearchSourcesContainTargeted2027Queries(t *testing.T) {
	sources := MarketSearchSources()
	if len(sources) != 29 {
		t.Fatalf("market sources = %d, want 29 bounded search families", len(sources))
	}
	seenGraduate, seenIntern, seenAI, seenLever, seenSingapore := false, false, false, false, false
	seenYearless, seenYC, seenWorkday, seenQuant, seenDevtools := false, false, false, false, false
	seenYCAI, seenTopQuant, seenBigTech, seenLargeTech, seenUnicorn := false, false, false, false, false
	ids := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if source.Provider != "market_search" || source.Company != "Market discovery" {
			t.Fatalf("unexpected market source: %#v", source)
		}
		if _, exists := ids[source.ID]; exists {
			t.Fatalf("duplicate market source id %q", source.ID)
		}
		ids[source.ID] = struct{}{}
		parsed, err := url.Parse(source.URL)
		if err != nil || parsed.Scheme != "tinyfish" || parsed.Host != "search" {
			t.Fatalf("invalid market source URL %q: %v", source.URL, err)
		}
		query := strings.ToLower(parsed.Query().Get("query"))
		seenGraduate = seenGraduate || (strings.Contains(query, "software engineer graduate") && strings.Contains(query, "2027"))
		seenIntern = seenIntern || strings.Contains(query, "software engineer intern 2027")
		seenAI = seenAI || (strings.Contains(query, "machine learning engineer") && strings.Contains(query, "2027"))
		seenLever = seenLever || strings.Contains(query, "site:jobs.lever.co")
		seenSingapore = seenSingapore || parsed.Query().Get("location") == "Singapore"
		seenYearless = seenYearless || (strings.Contains(source.ID, "early-career") && !strings.Contains(query, "2027"))
		seenYC = seenYC || strings.Contains(query, "site:ycombinator.com/companies")
		seenWorkday = seenWorkday || strings.Contains(query, "site:myworkdayjobs.com")
		seenQuant = seenQuant || strings.Contains(source.ID, "quant-engineering")
		seenDevtools = seenDevtools || strings.Contains(query, "developer tools")
		seenYCAI = seenYCAI || source.ID == "market-yc-ai-infra-early-career"
		seenTopQuant = seenTopQuant || source.ID == "market-top-quant-2027"
		seenBigTech = seenBigTech || source.ID == "market-big-tech-university-2027"
		seenLargeTech = seenLargeTech || source.ID == "market-large-tech-early-career"
		seenUnicorn = seenUnicorn || source.ID == "market-unicorn-cloud-data-early-career"
	}
	if !seenGraduate || !seenIntern || !seenAI || !seenLever || !seenSingapore || !seenYearless || !seenYC || !seenWorkday || !seenQuant || !seenDevtools || !seenYCAI || !seenTopQuant || !seenBigTech || !seenLargeTech || !seenUnicorn {
		t.Fatalf("market queries missing target coverage: graduate=%v intern=%v ai=%v lever=%v singapore=%v yearless=%v yc=%v workday=%v quant=%v devtools=%v yc_ai=%v top_quant=%v big_tech=%v large_tech=%v unicorn=%v",
			seenGraduate, seenIntern, seenAI, seenLever, seenSingapore, seenYearless, seenYC, seenWorkday, seenQuant, seenDevtools,
			seenYCAI, seenTopQuant, seenBigTech, seenLargeTech, seenUnicorn)
	}
}

func TestMarketObservationExtractorCapturesOnlySuccessfulMarketResults(t *testing.T) {
	inner := extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
		return completeExtraction(Observation{SourceID: source.ID, Company: "Acme", Title: "Graduate Software Engineer 2027"}), nil
	})
	capturing := NewMarketObservationExtractor(inner)
	passthrough, err := capturing.Extract(context.Background(), Source{ID: "ats", Provider: "greenhouse"})
	if err != nil {
		t.Fatal(err)
	}
	if len(passthrough.Observations) != 1 {
		t.Fatalf("trusted source observations were hidden: %#v", passthrough)
	}
	if got := capturing.DrainMarketObservations(); len(got) != 0 {
		t.Fatalf("captured non-market observations: %#v", got)
	}
	marketResult, err := capturing.Extract(context.Background(), MarketSearchSources()[0])
	if err != nil {
		t.Fatal(err)
	}
	if !marketResult.Complete || len(marketResult.Observations) != 0 {
		t.Fatalf("market evidence leaked into routine ingestion: %#v", marketResult)
	}
	if got := capturing.DrainMarketObservations(); len(got) != 1 || got[0].Company != "Acme" {
		t.Fatalf("captured market observations = %#v", got)
	}
	if got := capturing.DrainMarketObservations(); len(got) != 0 {
		t.Fatalf("drain replayed observations: %#v", got)
	}
}

func TestDeriveMarketCandidatesCreatesDurableCompaniesAndCanonicalBoards(t *testing.T) {
	observations := []Observation{
		{SourceID: "market-1", Company: "Acme AI", ApplyURL: "https://job-boards.greenhouse.io/acmeai/jobs/123"},
		{SourceID: "market-2", Company: "Acme AI", ApplyURL: "https://job-boards.greenhouse.io/acmeai/jobs/456"},
		{SourceID: "market-3", Company: "NewCo", ApplyURL: "https://newco.example/careers/graduate-engineer"},
		{SourceID: "market-4", Company: "LinkedIn", ApplyURL: "https://linkedin.com/jobs/123"},
		{SourceID: "market-5", Company: "Anduril Industries", ApplyURL: "https://job-boards.greenhouse.io/andurilindustries/jobs/789"},
		{SourceID: "market-6", Company: "IMC Trading", ApplyURL: "https://expatjobboard.com/jobs/15721"},
	}
	candidates, sources := deriveMarketCandidates(observations)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %#v, want Acme AI and NewCo", candidates)
	}
	if len(sources) != 1 {
		t.Fatalf("sources = %#v, want one canonical board", sources)
	}
	if got := sources[0].resolved.Source; got.Provider != "greenhouse" || got.URL != "https://job-boards.greenhouse.io/acmeai" || got.Company != "Acme AI" {
		t.Fatalf("canonical source = %#v", got)
	}
	if candidates[0].ID == "" || !strings.HasPrefix(candidates[0].ID, "market-") {
		t.Fatalf("market candidate id = %q", candidates[0].ID)
	}
}

func TestDeriveMarketCandidatesRejectsAggregatorCompanyArtifacts(t *testing.T) {
	candidates, sources := deriveMarketCandidates([]Observation{
		{SourceID: "market-1", Company: "BuiltInSF", ApplyURL: "https://jobs.ashbyhq.com/realco/123"},
		{SourceID: "market-2", Company: "Base", ApplyURL: "https://jobs.ashbyhq.com/base/123"},
		{SourceID: "market-3", Company: "RemoteRocketship", ApplyURL: "https://remoterocketship.com/jobs/123"},
		{SourceID: "market-4", Company: "Startup", ApplyURL: "https://jobs.ashbyhq.com/startup/123"},
		{SourceID: "market-5", Company: "Addepar", ApplyURL: "https://startup.jobs/software-engineer-intern-123"},
		{SourceID: "market-6", Company: "IBM", ApplyURL: "https://en.wizbii.com/company/ibm/job/ml-intern"},
		{SourceID: "market-7", Company: "BuiltinChicago", ApplyURL: "https://www.builtinchicago.org/job/ml-intern"},
		{SourceID: "market-8", Company: "Aijobs", ApplyURL: "https://aijobs.net/job/ml-intern"},
		{SourceID: "market-9", Company: "App", ApplyURL: "https://app.welcometothejungle.com/jobs/123"},
		{SourceID: "market-10", Company: "Careerhub", ApplyURL: "https://careerhub.students.duke.edu/jobs/example"},
	})
	if len(candidates) != 0 || len(sources) != 0 {
		t.Fatalf("aggregator artifacts survived: candidates=%#v sources=%#v", candidates, sources)
	}
}

func TestDeriveMarketCandidatesRecognizesYCCompanyJobBoards(t *testing.T) {
	candidates, sources := deriveMarketCandidates([]Observation{{
		SourceID: "market-yc-software-early-career", Company: "Boundary",
		ApplyURL: "https://www.ycombinator.com/companies/boundary/jobs/abc-software-engineer-intern",
	}})
	if len(candidates) != 1 || candidates[0].Website != "" {
		t.Fatalf("YC candidate = %#v", candidates)
	}
	if len(sources) != 1 {
		t.Fatalf("YC sources = %#v", sources)
	}
	got := sources[0].resolved.Source
	if got.Provider != "yc_jobs" || got.URL != "https://www.ycombinator.com/companies/boundary/jobs" || got.Company != "Boundary" {
		t.Fatalf("YC source = %#v", got)
	}
}

func TestMarketSourcePromoterProbesAndPromotesRelevantBoard(t *testing.T) {
	store := &discoveryRepositoryFake{promoteOnSuccess: true}
	extractor := extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
		if source.Provider != "greenhouse" || source.URL != "https://job-boards.greenhouse.io/acmeai" {
			t.Fatalf("unexpected source probe: %#v", source)
		}
		return completeExtraction(Observation{
			SourceID: source.ID, Company: "Acme AI", Title: "Graduate Software Engineer 2027",
			Location: "New York, NY", ApplyURL: source.URL + "/jobs/123",
		}), nil
	})
	promoter := MarketSourcePromoter{
		Extractor: extractor,
		Store:     store,
		Now:       func() time.Time { return time.Date(2026, 8, 18, 4, 0, 0, 0, time.UTC) },
	}
	report, err := promoter.Run(context.Background(), []Observation{{
		SourceID: "market-graduate-software-2027", Company: "Acme AI",
		Title: "Graduate Software Engineer 2027", ApplyURL: "https://job-boards.greenhouse.io/acmeai/jobs/123",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.CompaniesDiscovered != 1 || report.SourcesDerived != 1 || report.SourcesProbed != 1 || report.SourcesHealthy != 1 || report.SourcesPromoted != 1 {
		t.Fatalf("market report = %#v", report)
	}
	if len(store.seeded) != 1 || len(store.successSources) != 1 || store.successCounts[0] != 1 {
		t.Fatalf("store state seeded=%#v sources=%#v counts=%#v", store.seeded, store.successSources, store.successCounts)
	}
}

func TestMarketSourcePromoterQuarantinesEmptyAndNontechnicalBoards(t *testing.T) {
	store := &discoveryRepositoryFake{}
	extractor := extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
		if strings.Contains(source.URL, "emptyco") {
			return completeExtraction(), nil
		}
		return completeExtraction(Observation{Company: source.Company, Title: "Customer Support Specialist"}), nil
	})
	report, err := (MarketSourcePromoter{Extractor: extractor, Store: store}).Run(context.Background(), []Observation{
		{SourceID: "market-1", Company: "EmptyCo", ApplyURL: "https://jobs.ashbyhq.com/emptyco/123"},
		{SourceID: "market-2", Company: "SupportCo", ApplyURL: "https://jobs.lever.co/supportco/123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SourcesProbed != 2 || report.SourcesHealthy != 1 || report.SourcesEmpty != 1 || report.SourcesRejected != 1 || report.SourcesPromoted != 0 {
		t.Fatalf("market report = %#v", report)
	}
	if len(store.failures) != 1 || len(store.successSources) != 1 || store.successCounts[0] != 0 {
		t.Fatalf("store failures=%#v successes=%#v counts=%#v", store.failures, store.successSources, store.successCounts)
	}
}

func TestMarketSourcePromoterSkipsAlreadyMonitoredBoard(t *testing.T) {
	monitored := Source{ID: "acme-ai", Company: "Acme AI", Provider: "greenhouse", URL: "https://job-boards.greenhouse.io/acmeai"}
	store := &discoveryRepositoryFake{promotedSources: []Source{monitored}}
	extractorCalls := 0
	report, err := (MarketSourcePromoter{
		Store: store,
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			extractorCalls++
			return completeExtraction(), nil
		}),
	}).Run(context.Background(), []Observation{{
		SourceID: "market-2027", Company: "Acme AI",
		Title: "2027 Early Career Software Engineer", ApplyURL: monitored.URL + "/jobs/123",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.CompaniesDiscovered != 0 || report.SourcesDerived != 0 || report.SourcesMonitored != 1 || report.SourcesProbed != 0 || report.SourcesPromoted != 0 || extractorCalls != 0 {
		t.Fatalf("report=%#v extractor_calls=%d", report, extractorCalls)
	}
}

func TestMarketSourcePromoterSkipsVerifiedBoardWithoutCreatingCandidate(t *testing.T) {
	store := &discoveryRepositoryFake{}
	extractorCalls := 0
	report, err := (MarketSourcePromoter{
		Store: store,
		KnownSources: []Source{{
			ID: "abridge", Company: "Abridge", Provider: "ashby", URL: "https://jobs.ashbyhq.com/abridge",
		}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			extractorCalls++
			return completeExtraction(), nil
		}),
	}).Run(context.Background(), []Observation{{
		SourceID: "market-ashby", Company: "Abridge",
		ApplyURL: "https://jobs.ashbyhq.com/Abridge/123",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.CompaniesDiscovered != 0 || report.SourcesDerived != 0 || report.SourcesMonitored != 1 || extractorCalls != 0 || len(store.seeded) != 0 {
		t.Fatalf("report=%#v extractor_calls=%d seeded=%#v", report, extractorCalls, store.seeded)
	}
}
