package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hwennnn/radar/internal/source/scraper"
)

// Extractor is the deliberately small extraction boundary used by the lite
// runner. Production uses ScraperExtractor; tests can supply an in-memory fake.
type Extractor interface {
	Extract(context.Context, Source) (ExtractionResult, error)
}

// ExtractionResult distinguishes an authoritative snapshot (including a
// genuinely empty board) from an incomplete parse. Runner only advances a
// source's bootstrap state after a complete snapshot.
type ExtractionResult struct {
	Observations []Observation
	Complete     bool
}

// ScraperExtractor adapts Radar's existing, fixture-tested ATS extractor. It
// keeps ATS parsing out of the lite runtime while avoiding a dependency on the
// legacy worker, queue, matching, or notification pipeline.
type ScraperExtractor struct {
	extractor scraper.Extractor
	tier      scraper.Tier
}

// DiscoveryAwareExtractor keeps provider routing explicit: deterministic ATS
// providers use Structured, while reviewed official search/fetch providers use
// Search. A missing required route fails closed instead of silently invoking
// the wrong extractor.
type DiscoveryAwareExtractor struct {
	structured Extractor
	search     Extractor
}

var authoritativeEmptyProviders = map[string]struct{}{
	"akuna_careers": {}, "amazon_jobs": {}, "apple_jobs": {},
	"ashby": {}, "bytedance_careers": {}, "citadel_careers": {},
	"citadel_securities_careers": {}, "eightfold_apply": {},
	"eightfold_pcsx": {}, "gem": {}, "greenhouse": {}, "ibm_careers": {},
	"janestreet_careers": {}, "lever": {}, "oracle_recruiting": {},
	"market_search": {}, "rippling": {}, "smartrecruiters": {}, "workable": {}, "workday": {},
	"yc_jobs": {},
}

func NewScraperExtractor(extractor scraper.Extractor) *ScraperExtractor {
	return NewScraperExtractorAtTier(extractor, scraper.TierATS)
}

func NewScraperExtractorAtTier(extractor scraper.Extractor, tier scraper.Tier) *ScraperExtractor {
	if tier == "" {
		tier = scraper.TierATS
	}
	return &ScraperExtractor{extractor: extractor, tier: tier}
}

func NewATSExtractor(opts scraper.ATSOptions) *ScraperExtractor {
	return NewScraperExtractor(scraper.NewATSExtractor(opts))
}

func NewDiscoveryAwareExtractor(structured, search Extractor) *DiscoveryAwareExtractor {
	return &DiscoveryAwareExtractor{structured: structured, search: search}
}

func (e *DiscoveryAwareExtractor) Extract(ctx context.Context, source Source) (ExtractionResult, error) {
	if e == nil {
		return ExtractionResult{}, errors.New("lite: discovery-aware extractor is required")
	}
	provider := strings.ToLower(strings.TrimSpace(source.Provider))
	if _, usesSearch := searchDiscoveryProviders[provider]; usesSearch {
		if e.search == nil {
			return ExtractionResult{}, fmt.Errorf("lite: provider %s requires TinyFish search/fetch extraction", provider)
		}
		return e.search.Extract(ctx, source)
	}
	if e.structured == nil {
		return ExtractionResult{}, errors.New("lite: structured extractor is required")
	}
	return e.structured.Extract(ctx, source)
}

func (e *ScraperExtractor) Extract(ctx context.Context, source Source) (ExtractionResult, error) {
	if e == nil || e.extractor == nil {
		return ExtractionResult{}, errors.New("lite: extractor is required")
	}

	result, err := e.extractor.Extract(ctx, scraper.Source{
		ID:       source.ID,
		Name:     source.Company,
		URL:      source.URL,
		Tier:     e.tier,
		Metadata: map[string]string{"source_kind": source.Provider},
	})
	_, allowsEmpty := authoritativeEmptyProviders[strings.ToLower(strings.TrimSpace(source.Provider))]
	if errors.Is(err, scraper.ErrNoJobs) && allowsEmpty {
		return ExtractionResult{Observations: []Observation{}, Complete: true}, nil
	}
	if errors.Is(err, scraper.ErrNoJobs) {
		return ExtractionResult{}, fmt.Errorf("lite: %s source returned no usable jobs: %w", e.tier, scraper.ErrNoJobs)
	}
	if err != nil {
		return ExtractionResult{}, err
	}
	if len(result.Jobs) == 0 && allowsEmpty {
		return ExtractionResult{Observations: []Observation{}, Complete: true}, nil
	}
	if len(result.Jobs) == 0 {
		return ExtractionResult{}, fmt.Errorf("lite: %s source returned no usable jobs: %w", e.tier, scraper.ErrNoJobs)
	}

	observedAt := result.FetchedAt.UTC()
	observations := make([]Observation, 0, len(result.Jobs))
	for _, job := range result.Jobs {
		company := strings.TrimSpace(job.Company)
		// A company-specific source is a stronger employer identity than an AI
		// payload or ATS display label. Market search is deliberately the only
		// multi-employer source and therefore keeps the extracted company.
		if strings.TrimSpace(source.Company) != "" && !strings.EqualFold(strings.TrimSpace(source.Provider), "market_search") {
			company = source.Company
		} else if company == "" {
			company = source.Company
		}
		location := strings.TrimSpace(job.Location)
		country := strings.TrimSpace(job.Country)
		if _, usesSearch := searchDiscoveryProviders[strings.ToLower(strings.TrimSpace(source.Provider))]; usesSearch {
			if titleLocation, titleCountry := explicitTitleLocation(job.Title); titleLocation != "" {
				location = titleLocation
				country = titleCountry
			}
		}
		description := firstEvidenceText(job.Evidence, "description")
		level := strings.TrimSpace(job.Level)
		if level == "" || hasAnyPhrase(normalizedText(level), []string{"unknown", "not stated", "unspecified"}) {
			if explicitRoleLevelDescription(description) {
				level = "entry level"
			}
		}
		observations = append(observations, Observation{
			SourceID:       source.ID,
			SourceNativeID: job.SourceJobID,
			Company:        company,
			Title:          job.Title,
			Location:       location,
			Country:        country,
			EmploymentType: job.EmploymentType,
			Level:          level,
			ApplyURL:       job.ApplyURL,
			Description:    description,
			PostedAt:       job.PostedAt,
			ObservedAt:     observedAt,
		})
	}
	return ExtractionResult{Observations: observations, Complete: true}, nil
}

// explicitTitleLocation corrects only strongly delimited geography in an
// official search/fetch result. This guards against an agent copying the search
// locale into a role whose title explicitly names another office, while
// avoiding guesses from ordinary title words.
func explicitTitleLocation(title string) (string, string) {
	lower := strings.ToLower(strings.TrimSpace(title))
	locations := []struct {
		phrases  []string
		location string
		country  string
	}{
		{[]string{"new york", "new york city", "nyc"}, "New York, NY, United States", "United States"},
		{[]string{"singapore"}, "Singapore", "Singapore"},
		{[]string{"united states", "usa", "us"}, "United States", "United States"},
	}
	for _, candidate := range locations {
		for _, phrase := range candidate.phrases {
			if strings.Contains(lower, "("+phrase+")") ||
				strings.Contains(lower, "- "+phrase) ||
				strings.Contains(lower, "– "+phrase) ||
				strings.HasSuffix(lower, " "+phrase) {
				return candidate.location, candidate.country
			}
		}
	}
	return "", ""
}

// explicitRoleLevelDescription accepts only role-scoped statements. Generic
// mentions of interns, qualifications, or links to separate campus postings do
// not supply timing evidence, and explicit negation always wins.
func explicitRoleLevelDescription(description string) bool {
	normalized := normalizedText(description)
	if normalized == "" || hasAnyPhrase(normalized, []string{
		"not intended for internship", "not intended for new graduate", "not intended for entry level",
		"recent graduate please see our campus postings", "recent graduates please see our campus postings",
		"student or recent graduate please see our campus postings", "students or recent graduates please see our campus postings",
	}) {
		return false
	}
	if hasPhrase(normalized, "role entry level") {
		return true
	}
	roleStart := strings.Index(normalized, " about the role ")
	if roleStart < 0 {
		return false
	}
	roleStatement := normalized[roleStart:]
	roleStatement = TruncateText(roleStatement, 320)
	return hasAnyPhrase(roleStatement, []string{
		"is an entry level individual contributor role", "is an entry level role", "is a new graduate role",
	})
}

func firstEvidenceText(evidence []scraper.Evidence, field string) string {
	for _, item := range evidence {
		if strings.EqualFold(strings.TrimSpace(item.Field), field) {
			if text := strings.TrimSpace(item.Text); text != "" {
				return text
			}
		}
	}
	return ""
}
