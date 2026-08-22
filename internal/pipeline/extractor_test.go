package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hwennnn/radar/internal/source/scraper"
)

type scraperExtractorFunc struct {
	extract func(context.Context, scraper.Source) (scraper.Result, error)
}

func (f scraperExtractorFunc) Name() string       { return "fixture" }
func (f scraperExtractorFunc) Tier() scraper.Tier { return scraper.TierATS }
func (f scraperExtractorFunc) Extract(ctx context.Context, source scraper.Source) (scraper.Result, error) {
	return f.extract(ctx, source)
}

func TestScraperExtractorConvertsResultAndSuppliesAdapterMetadata(t *testing.T) {
	fetchedAt := time.Date(2026, time.August, 16, 8, 30, 0, 0, time.FixedZone("SGT", 8*60*60))
	postedAt := fetchedAt.AddDate(0, 0, -7)
	adapter := NewScraperExtractor(scraperExtractorFunc{extract: func(_ context.Context, source scraper.Source) (scraper.Result, error) {
		if source.ID != "openai" || source.Name != "OpenAI" || source.URL != "https://jobs.example/openai" {
			t.Fatalf("unexpected scraper source: %#v", source)
		}
		if source.Metadata["source_kind"] != "ashby" {
			t.Fatalf("unexpected metadata: %#v", source.Metadata)
		}
		return scraper.Result{
			FetchedAt: fetchedAt,
			Jobs: []scraper.JobPosting{{
				SourceJobID:    "job-42",
				Title:          "Software Engineer, New Grad",
				Location:       "San Francisco, CA",
				Country:        "US",
				EmploymentType: "Full-time",
				Level:          "new_grad",
				ApplyURL:       "https://jobs.example/openai/42",
				PostedAt:       &postedAt,
				Evidence: []scraper.Evidence{
					{Field: "description", Text: "Build reliable AI infrastructure."},
				},
			}},
		}, nil
	}})

	got, err := adapter.Extract(context.Background(), Source{
		ID: "openai", Company: "OpenAI", Provider: "ashby",
		URL: "https://jobs.example/openai",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete || len(got.Observations) != 1 {
		t.Fatalf("got %#v, want one complete observation", got)
	}
	observation := got.Observations[0]
	if observation.SourceID != "openai" || observation.SourceNativeID != "job-42" || observation.Company != "OpenAI" {
		t.Fatalf("unexpected observation: %#v", observation)
	}
	if observation.EmploymentType != "Full-time" || observation.Level != "new_grad" {
		t.Fatalf("timing fields were not preserved: %#v", observation)
	}
	if observation.Country != "US" || observation.Description != "Build reliable AI infrastructure." {
		t.Fatalf("country/description evidence was not preserved: %#v", observation)
	}
	if observation.PostedAt == nil || !observation.PostedAt.Equal(postedAt) {
		t.Fatalf("posted_at was not preserved: %#v", observation)
	}
	if !observation.ObservedAt.Equal(fetchedAt.UTC()) {
		t.Fatalf("ObservedAt = %s, want %s", observation.ObservedAt, fetchedAt.UTC())
	}
}

func TestScraperExtractorTrustsOfficialCompanyAndExplicitTitleLocation(t *testing.T) {
	adapter := NewScraperExtractorAtTier(scraperExtractorFunc{extract: func(_ context.Context, _ scraper.Source) (scraper.Result, error) {
		return scraper.Result{Jobs: []scraper.JobPosting{{
			SourceJobID: "5891", Company: "Deshaw",
			Title:    "Quantitative Analyst, Ph.D. Intern (New York) – Summer 2027",
			Location: "Singapore", Country: "Singapore", Level: "internship",
			ApplyURL: "https://www.deshaw.com/careers/quantitative-analyst-ph-d-intern-new-york-summer-2027-5891",
		}}}, nil
	}}, scraper.TierAIExtraction)

	result, err := adapter.Extract(context.Background(), Source{
		ID: "d-e-shaw", Company: "D. E. Shaw", Provider: "deshaw_careers", URL: "https://www.deshaw.com/careers",
	})
	if err != nil || len(result.Observations) != 1 {
		t.Fatalf("extract: result=%#v err=%v", result, err)
	}
	got := result.Observations[0]
	if got.Company != "D. E. Shaw" || got.Location != "New York, NY, United States" || got.Country != "United States" {
		t.Fatalf("official identity/location was not normalized: %#v", got)
	}
}

func TestScraperExtractorDoesNotOverrideMarketSearchEmployer(t *testing.T) {
	adapter := NewScraperExtractorAtTier(scraperExtractorFunc{extract: func(_ context.Context, _ scraper.Source) (scraper.Result, error) {
		return scraper.Result{Jobs: []scraper.JobPosting{{
			SourceJobID: "42", Company: "Airwallex", Title: "Software Engineer Intern 2027",
			Location: "Singapore", Country: "Singapore", ApplyURL: "https://example.test/42",
		}}}, nil
	}}, scraper.TierAIExtraction)

	result, err := adapter.Extract(context.Background(), Source{
		ID: "market", Company: "Market discovery", Provider: "market_search", URL: "https://example.test/search",
	})
	if err != nil || len(result.Observations) != 1 || result.Observations[0].Company != "Airwallex" {
		t.Fatalf("market employer should remain extracted: result=%#v err=%v", result, err)
	}
}

func TestScraperExtractorAllowsExplicitAuthoritativeProviderEmpty(t *testing.T) {
	adapter := NewScraperExtractor(scraperExtractorFunc{extract: func(context.Context, scraper.Source) (scraper.Result, error) {
		return scraper.Result{}, scraper.ErrNoJobs
	}})

	providers := []string{
		"akuna_careers", "amazon_jobs", "apple_jobs", "ashby", "bytedance_careers",
		"citadel_careers", "citadel_securities_careers", "eightfold_apply", "eightfold_pcsx", "gem", "greenhouse", "ibm_careers",
		"janestreet_careers", "lever", "market_search", "oracle_recruiting", "rippling",
		"smartrecruiters", "workable", "workday",
	}
	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			got, err := adapter.Extract(context.Background(), Source{ID: "empty", Company: "Empty", Provider: provider})
			if err != nil || !got.Complete || got.Observations == nil || len(got.Observations) != 0 {
				t.Fatalf("got=%#v error=%v, want successful authoritative empty", got, err)
			}
		})
	}
}

func TestDiscoveryAwareExtractorRoutesOfficialSearchProviders(t *testing.T) {
	var structuredCalls, searchCalls int
	structured := extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
		structuredCalls++
		return completeExtraction(Observation{Title: "Structured"}), nil
	})
	search := extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
		searchCalls++
		return completeExtraction(Observation{Title: "Search"}), nil
	})
	router := NewDiscoveryAwareExtractor(structured, search)

	if _, err := router.Extract(context.Background(), Source{Provider: "greenhouse"}); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"cursor_careers", "deshaw_careers", "groq_careers", "oldmission_careers", "sig_careers", "tiktok_careers", "twosigma_careers"} {
		if _, err := router.Extract(context.Background(), Source{Provider: provider}); err != nil {
			t.Fatalf("%s: %v", provider, err)
		}
	}
	if structuredCalls != 1 || searchCalls != 7 {
		t.Fatalf("structured=%d search=%d", structuredCalls, searchCalls)
	}
}

func TestDiscoveryAwareExtractorFailsClosedWithoutRequiredRoute(t *testing.T) {
	router := NewDiscoveryAwareExtractor(extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
		return completeExtraction(Observation{Title: "Structured"}), nil
	}), nil)
	if _, err := router.Extract(context.Background(), Source{Provider: "cursor_careers"}); err == nil {
		t.Fatal("search/fetch provider must not silently fall back to the ATS extractor")
	}
}

func TestExplicitRoleLevelDescriptionIsAnchoredAndNegationSafe(t *testing.T) {
	tests := []struct {
		name        string
		description string
		want        bool
	}{
		{"cerebras role statement", "About The Role The AI Infrastructure Operations Engineer (SiteOps) is an entry-level individual contributor role focused on deployment.", true},
		{"point72 role heading", "ROLE Entry-Level Quantitative Researchers are responsible for rigorous quantitative research.", true},
		{"databricks explicit exclusion", "It is not intended for internship, new graduate, or entry-level applicants. About the role This is an entry-level role elsewhere.", false},
		{"jump campus redirect", "If you are currently a student or recent graduate, please see our Campus postings which offer intern and full-time opportunities.", false},
		{"generic intern mention", "You will mentor interns and early career engineers on the team.", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := explicitRoleLevelDescription(test.description); got != test.want {
				t.Fatalf("explicitRoleLevelDescription() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestScraperExtractorPersistsExplicitDescriptionLevel(t *testing.T) {
	adapter := NewScraperExtractor(scraperExtractorFunc{extract: func(_ context.Context, source scraper.Source) (scraper.Result, error) {
		return scraper.Result{Jobs: []scraper.JobPosting{{
			SourceJobID: "entry-1", Company: source.Name, Title: "AI Infrastructure Operations Engineer",
			Country: "US", Location: "US Offices", EmploymentType: "FullTime", Level: "unknown",
			Evidence: []scraper.Evidence{{Field: "description", Text: "About The Role The AI Infrastructure Operations Engineer is an entry-level individual contributor role."}},
		}}}, nil
	}})
	result, err := adapter.Extract(context.Background(), Source{ID: "cerebras", Company: "Cerebras", Provider: "ashby"})
	if err != nil || len(result.Observations) != 1 || result.Observations[0].Level != "entry level" {
		t.Fatalf("explicit description timing was not persisted: result=%#v err=%v", result, err)
	}
}

func TestScraperExtractorRejectsUntrustedProviderEmpty(t *testing.T) {
	adapter := NewScraperExtractor(scraperExtractorFunc{extract: func(context.Context, scraper.Source) (scraper.Result, error) {
		return scraper.Result{}, scraper.ErrNoJobs
	}})
	got, err := adapter.Extract(context.Background(), Source{ID: "empty", Company: "Empty", Provider: "custom"})
	if !errors.Is(err, scraper.ErrNoJobs) || got.Complete || got.Observations != nil {
		t.Fatalf("got=%#v error=%v, want untrusted source failure", got, err)
	}
}

func TestScraperExtractorReturnsRealFailures(t *testing.T) {
	want := errors.New("upstream unavailable")
	adapter := NewScraperExtractor(scraperExtractorFunc{extract: func(context.Context, scraper.Source) (scraper.Result, error) {
		return scraper.Result{}, want
	}})
	_, err := adapter.Extract(context.Background(), Source{ID: "broken", Company: "Broken", Provider: "lever"})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestScraperExtractorPreservesTruncatedSnapshotState(t *testing.T) {
	adapter := NewScraperExtractor(scraperExtractorFunc{extract: func(context.Context, scraper.Source) (scraper.Result, error) {
		return scraper.Result{
			Jobs: []scraper.JobPosting{{
				SourceJobID: "one", Company: "Acme", Title: "Software Engineer Intern",
				ApplyURL: "https://jobs.example.test/one",
			}},
			Diagnostics: map[string]string{"completeness_status": "truncated", "has_more": "true"},
		}, nil
	}})

	got, err := adapter.Extract(context.Background(), Source{ID: "acme", Company: "Acme", Provider: "workday"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Complete || len(got.Observations) != 1 {
		t.Fatalf("truncated extraction=%#v, want one observation and incomplete state", got)
	}
}

func TestAuthoritativeEmptyCompletesBootstrapBeforeFutureJobEnqueues(t *testing.T) {
	calls := 0
	extractor := NewScraperExtractor(scraperExtractorFunc{extract: func(_ context.Context, source scraper.Source) (scraper.Result, error) {
		calls++
		if calls == 1 {
			return scraper.Result{}, scraper.ErrNoJobs
		}
		jobs := []scraper.JobPosting{{
			SourceJobID: "future-1", Company: source.Name, Title: "Software Engineer Intern",
			Location: "Singapore", ApplyURL: "https://example.test/jobs/future-1",
		}}
		if calls >= 3 {
			jobs = append(jobs, scraper.JobPosting{
				SourceJobID: "future-2", Company: source.Name, Title: "Backend Engineer New Grad",
				Location: "Singapore", ApplyURL: "https://example.test/jobs/future-2",
			})
		}
		return scraper.Result{Jobs: jobs}, nil
	}})
	store := newRunnerStoreFake()
	runner := Runner{
		Sources:   []Source{{ID: "acme", Company: "Acme", Provider: "greenhouse", URL: "https://example.test/jobs"}},
		Extractor: extractor, Store: store, Channel: "telegram", Recipient: "chat-1",
	}

	if report, err := runner.Run(context.Background()); err != nil || report.SourcesBootstrapped != 1 || report.Enqueued != 0 {
		t.Fatalf("authoritative empty should complete bootstrap: report=%#v err=%v", report, err)
	}
	if report, err := runner.Run(context.Background()); err != nil || report.SourcesBootstrapped != 0 || report.Enqueued != 1 {
		t.Fatalf("first job after authoritative empty should enqueue: report=%#v err=%v", report, err)
	}
	if report, err := runner.Run(context.Background()); err != nil || report.Enqueued != 1 {
		t.Fatalf("new job after baseline did not enqueue: report=%#v err=%v", report, err)
	}
	if report, err := runner.Run(context.Background()); err != nil || report.Enqueued != 0 {
		t.Fatalf("new job enqueued more than once: report=%#v err=%v", report, err)
	}
}
