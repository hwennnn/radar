package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultMarketSourceProbeLimit = 8
	marketDiscoveryRetry          = 6 * time.Hour
)

var marketCandidateSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// MarketSearchSources are persistent broad-search feeds. Unlike the company
// seed, these searches can discover both jobs and previously unknown
// companies. Query text is encoded in the URL so the small Source contract
// remains transport-neutral.
func MarketSearchSources() []Source {
	type searchSpec struct {
		id       string
		query    string
		location string
	}
	// Search engines return only a small top-K. Separate provider, role, market,
	// timing, and company-segment feeds keep discovery open-ended instead of
	// repeatedly surfacing the same incumbents. Yearless feeds catch postings
	// whose titles omit a graduation year; downstream eligibility still requires
	// explicit early-career evidence before a delivery decision exists.
	specs := []searchSpec{
		{"market-greenhouse-graduate-software-2027", `site:job-boards.greenhouse.io "2027" ("software engineer graduate" OR "new grad software engineer" OR "early career software engineer")`, ""},
		{"market-greenhouse-platform-early-career", `site:job-boards.greenhouse.io ("software engineer intern" OR "new grad") (backend OR platform OR infrastructure OR distributed systems)`, ""},
		{"market-new-grad-software-2027-us", `("new grad software engineer 2027" OR "early career software engineer 2027") jobs careers`, "United States"},
		{"market-software-intern-2027", `("software engineer intern 2027" OR "software engineering internship 2027") jobs careers`, ""},
		{"market-lever-software-2027", `site:jobs.lever.co "2027" ("graduate software engineer" OR "new grad software engineer" OR "software engineer intern")`, ""},
		{"market-lever-data-security-early-career", `site:jobs.lever.co ("new grad" OR intern OR university) ("data engineer" OR "security engineer" OR "cloud engineer")`, ""},
		{"market-singapore-software-2027", `("software engineer graduate 2027" OR "new grad software engineer 2027" OR "software engineer intern 2027") Singapore careers jobs`, "Singapore"},
		{"market-singapore-ai-data-early-career", `("machine learning engineer intern" OR "data engineer intern" OR "AI engineer intern" OR "graduate research engineer") Singapore`, "Singapore"},
		{"market-ashby-graduate-ai-data-2027", `site:jobs.ashbyhq.com "2027" ("graduate machine learning engineer" OR "new grad data engineer" OR "AI engineer intern")`, ""},
		{"market-ashby-startup-early-career", `site:jobs.ashbyhq.com ("software engineer intern" OR "new graduate" OR "early career") (AI OR infrastructure OR developer tools OR data)`, ""},
		{"market-workable-software-early-career", `site:apply.workable.com ("software engineer intern" OR "graduate software engineer" OR "junior backend engineer")`, ""},
		{"market-smartrecruiters-university-engineering", `site:careers.smartrecruiters.com (university OR graduate OR intern) (software OR machine learning OR data OR platform) engineer`, ""},
		{"market-workday-campus-engineering", `site:myworkdayjobs.com (campus OR university OR "new grad" OR intern) (software OR data OR machine learning) engineer`, ""},
		{"market-gem-software-early-career", `site:jobs.gem.com ("software engineer intern" OR "new grad software engineer" OR "early career engineer")`, ""},
		{"market-yc-software-early-career", `site:ycombinator.com/companies ("software engineer" OR "machine learning engineer") (intern OR "new grad" OR "early career")`, ""},
		{"market-yc-ai-infra-early-career", `site:ycombinator.com/companies ("software engineer" OR "research engineer") (AI OR infrastructure OR database OR "developer tools") (intern OR graduate OR "early career")`, ""},
		{"market-top-quant-2027", `("software engineer" OR "quantitative developer" OR "quantitative researcher") (intern OR "new grad" OR graduate OR "2027") ("Jane Street" OR "Citadel Securities" OR "Hudson River Trading" OR "D. E. Shaw" OR "Five Rings" OR Optiver OR IMC) careers`, ""},
		{"market-big-tech-university-2027", `("software engineer" OR "machine learning engineer") (intern OR university OR graduate OR "2027") (Google OR Meta OR Microsoft OR Amazon OR Apple OR NVIDIA) careers`, ""},
		{"market-large-tech-early-career", `("software engineer intern" OR "new grad software engineer" OR "university software engineer") (cloud OR semiconductor OR cybersecurity OR "data platform" OR "developer platform") (careers OR jobs)`, ""},
		{"market-unicorn-cloud-data-early-career", `("software engineer intern" OR "new grad software engineer" OR "early career engineer") (unicorn OR "Series C" OR "Series D") (cloud OR data OR infrastructure OR payments OR "developer tools") careers`, ""},
		{"market-us-infra-security-early-career", `("platform engineer intern" OR "infrastructure engineer intern" OR "security engineer intern" OR "cloud engineer new grad") careers`, "United States"},
		{"market-us-data-ai-early-career", `("data engineer intern" OR "machine learning engineer intern" OR "research engineer intern" OR "applied scientist intern") careers`, "United States"},
		{"market-canada-software-ai-early-career", `("new grad software engineer" OR "software engineer intern" OR "machine learning intern") Canada careers`, "Canada"},
		{"market-uk-graduate-engineering", `("graduate software engineer" OR "technology graduate programme" OR "software engineering internship") UK careers`, "United Kingdom"},
		{"market-remote-software-ai-early-career", `("software engineer intern" OR "new grad software engineer" OR "AI engineer intern") remote careers`, "Remote"},
		{"market-quant-engineering-us", `("quantitative developer intern" OR "quantitative researcher intern" OR "trading systems intern" OR "software engineer intern") trading careers`, "United States"},
		{"market-quant-engineering-global", `("graduate quantitative developer" OR "technology graduate" OR "quant research intern") (market maker OR hedge fund OR systematic trading)`, ""},
		{"market-developer-tools-early-career", `("software engineer intern" OR "new grad software engineer") (developer tools OR database OR observability OR cloud infrastructure) startup careers`, ""},
		{"market-fintech-early-career", `("software engineer intern" OR "new grad software engineer" OR "data engineer intern") (fintech OR payments OR crypto infrastructure) careers`, ""},
	}
	sources := make([]Source, 0, len(specs))
	for _, spec := range specs {
		sources = append(sources, marketSearchSource(spec.id, spec.query, spec.location))
	}
	return sources
}

func marketSearchSource(id, query, location string) Source {
	values := url.Values{"query": []string{query}}
	if strings.TrimSpace(location) != "" {
		values.Set("location", location)
	}
	return Source{
		ID:       id,
		Company:  "Market discovery",
		Provider: "market_search",
		URL:      "tinyfish://search/" + id + "?" + values.Encode(),
	}
}

// MarketObservationExtractor records broad-search results for source discovery
// while returning an authoritative empty snapshot to the routine runner.
// Search results are control-plane evidence, not trusted job-feed records: a
// company board must pass the production probe and become a promoted source
// before any of its jobs can enter persistence or delivery. It is safe if
// extraction later becomes concurrent.
type MarketObservationExtractor struct {
	inner        Extractor
	mu           sync.Mutex
	observations []Observation
}

func NewMarketObservationExtractor(inner Extractor) *MarketObservationExtractor {
	return &MarketObservationExtractor{inner: inner}
}

func (e *MarketObservationExtractor) Extract(ctx context.Context, source Source) (ExtractionResult, error) {
	if e == nil || e.inner == nil {
		return ExtractionResult{}, errors.New("lite: market observation extractor is required")
	}
	result, err := e.inner.Extract(ctx, source)
	if err == nil && result.Complete && strings.EqualFold(strings.TrimSpace(source.Provider), "market_search") {
		e.mu.Lock()
		e.observations = append(e.observations, result.Observations...)
		e.mu.Unlock()
		return ExtractionResult{Observations: []Observation{}, Complete: true}, nil
	}
	return result, err
}

// DrainMarketObservations returns each successful broad-search observation at
// most once to the source promoter.
func (e *MarketObservationExtractor) DrainMarketObservations() []Observation {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	observations := append([]Observation(nil), e.observations...)
	e.observations = e.observations[:0]
	return observations
}

type MarketDiscoveryReport struct {
	ObservationsSeen    int `json:"observations_seen"`
	CompaniesDiscovered int `json:"companies_discovered"`
	SourcesDerived      int `json:"sources_derived"`
	SourcesProbed       int `json:"sources_probed"`
	SourcesHealthy      int `json:"sources_healthy"`
	SourcesEmpty        int `json:"sources_empty"`
	SourcesRejected     int `json:"sources_rejected"`
	SourcesPromoted     int `json:"sources_promoted"`
	SourcesMonitored    int `json:"sources_already_monitored"`
}

type MarketDiscoveryRepository interface {
	SeedDiscoveryCandidates(context.Context, []DiscoveryCandidate) error
	RecordDiscoveryFailure(context.Context, DiscoveryCandidateRecord, *Source, error, time.Time, time.Time) error
	RecordDiscoverySuccess(context.Context, DiscoveryCandidateRecord, Source, int, float64, string, time.Time, time.Time) (bool, error)
	ListDiscoveredSources(context.Context) ([]Source, error)
}

// MarketSourcePromoter turns companies and apply links found by broad market
// searches into durable discovery candidates. Recognized ATS boards are probed
// immediately and enter routine monitoring only after a complete, non-empty,
// technically relevant snapshot.
type MarketSourcePromoter struct {
	Extractor Extractor
	Store     MarketDiscoveryRepository
	// KnownSources is the verified catalog. It prevents a search result from
	// creating a second discovery company/source for a board Radar already owns.
	KnownSources []Source
	ProbeLimit   int
	Now          func() time.Time
	Logger       *slog.Logger
}

type marketSourceCandidate struct {
	candidate DiscoveryCandidate
	resolved  resolvedDiscoverySource
}

func (p MarketSourcePromoter) Run(ctx context.Context, observations []Observation) (MarketDiscoveryReport, error) {
	report := MarketDiscoveryReport{ObservationsSeen: len(observations)}
	if len(observations) == 0 {
		return report, nil
	}
	if p.Extractor == nil {
		return report, errors.New("lite: market source promoter extractor is required")
	}
	if p.Store == nil {
		return report, errors.New("lite: market source promoter store is required")
	}
	candidates, sources := deriveMarketCandidates(observations)
	registeredSources, err := p.Store.ListDiscoveredSources(ctx)
	if err != nil {
		return report, fmt.Errorf("list registered market-discovered sources: %w", err)
	}
	knownSourceKeys := make(map[string]struct{}, len(p.KnownSources))
	knownCompanyNames := make(map[string]struct{}, len(p.KnownSources))
	for _, source := range p.KnownSources {
		knownSourceKeys[MarketSourceKey(source)] = struct{}{}
		knownCompanyNames[normalizedText(source.Company)] = struct{}{}
	}
	registeredSourceKeys := make(map[string]struct{}, len(registeredSources))
	for _, source := range registeredSources {
		registeredSourceKeys[MarketSourceKey(source)] = struct{}{}
	}
	knownCandidateIDs := make(map[string]struct{})
	for _, item := range sources {
		key := MarketSourceKey(item.resolved.Source)
		_, known := knownSourceKeys[key]
		_, registered := registeredSourceKeys[key]
		if known || registered {
			knownCandidateIDs[item.candidate.ID] = struct{}{}
		}
	}
	filteredCandidates := candidates[:0]
	for _, candidate := range candidates {
		_, knownBySource := knownCandidateIDs[candidate.ID]
		_, knownByName := knownCompanyNames[normalizedText(candidate.Name)]
		if knownBySource || knownByName {
			continue
		}
		filteredCandidates = append(filteredCandidates, candidate)
	}
	candidates = filteredCandidates
	filteredSources := sources[:0]
	for _, item := range sources {
		key := MarketSourceKey(item.resolved.Source)
		_, known := knownSourceKeys[key]
		_, registered := registeredSourceKeys[key]
		if known || registered {
			report.SourcesMonitored++
			p.log(ctx, slog.LevelInfo, "source_already_monitored", "market-derived source is already registered for durable triage",
				"candidate_id", item.candidate.ID, "company", item.candidate.Name,
				"provider", item.resolved.Source.Provider, "url", item.resolved.Source.URL)
			continue
		}
		filteredSources = append(filteredSources, item)
	}
	sources = filteredSources
	report.CompaniesDiscovered = len(candidates)
	report.SourcesDerived = len(sources)
	p.log(ctx, slog.LevelInfo, "batch_started", "market discovery promotion batch started",
		"observations", len(observations), "companies_discovered", len(candidates), "sources_derived", len(sources))
	if len(candidates) > 0 {
		if err := p.Store.SeedDiscoveryCandidates(ctx, candidates); err != nil {
			return report, fmt.Errorf("seed market-discovered companies: %w", err)
		}
	}
	limit := p.ProbeLimit
	if limit <= 0 {
		limit = defaultMarketSourceProbeLimit
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	for _, item := range sources {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if report.SourcesProbed >= limit {
			break
		}
		report.SourcesProbed++
		checkedAt := now().UTC()
		candidateRecord := DiscoveryCandidateRecord{DiscoveryCandidate: item.candidate}
		p.log(ctx, slog.LevelInfo, "source_probe_started", "probing source derived from a market-search job",
			"candidate_id", item.candidate.ID, "company", item.candidate.Name,
			"source_id", item.resolved.Source.ID, "provider", item.resolved.Source.Provider, "url", item.resolved.Source.URL)
		extraction, extractErr := p.Extractor.Extract(ctx, item.resolved.Source)
		if extractErr != nil || !extraction.Complete {
			report.SourcesRejected++
			if extractErr == nil {
				extractErr = errors.New("source probe returned an incomplete snapshot")
			}
			nextAttempt := checkedAt.Add(marketDiscoveryRetry)
			retryClass := "quarantine"
			if transientRetryAt, transient := transientDiscoveryRetryAt(extractErr, checkedAt, candidateRecord.Attempts); transient {
				nextAttempt = transientRetryAt
				retryClass = "transient"
			}
			if err := p.Store.RecordDiscoveryFailure(ctx, candidateRecord, &item.resolved.Source, extractErr, checkedAt, nextAttempt); err != nil {
				return report, fmt.Errorf("record market source probe failure: %w", err)
			}
			p.log(ctx, slog.LevelWarn, "source_probe_failed", "market-derived source failed production verification",
				"candidate_id", item.candidate.ID, "company", item.candidate.Name,
				"provider", item.resolved.Source.Provider, "url", item.resolved.Source.URL,
				"error", CompactDiscoveryError(extractErr.Error()), "retry_class", retryClass,
				"retry_in_seconds", int(nextAttempt.Sub(checkedAt).Seconds()), "next_attempt_at", nextAttempt)
			continue
		}
		quality := assessDiscoverySnapshotQuality(extraction.Observations)
		if len(extraction.Observations) > 0 && (quality.Usable == 0 || quality.Relevant == 0) {
			report.SourcesRejected++
			qualityErr := fmt.Errorf("market-derived source returned %d postings but no relevant technical job roles", len(extraction.Observations))
			if err := p.Store.RecordDiscoveryFailure(ctx, candidateRecord, &item.resolved.Source, qualityErr, checkedAt, checkedAt.Add(marketDiscoveryRetry)); err != nil {
				return report, fmt.Errorf("record market source quality failure: %w", err)
			}
			p.log(ctx, slog.LevelWarn, "source_quality_rejected", "market-derived source contained no technical job roles",
				"candidate_id", item.candidate.ID, "company", item.candidate.Name,
				"provider", item.resolved.Source.Provider, "url", item.resolved.Source.URL,
				"observed_count", len(extraction.Observations), "sample_titles", quality.SampleTitles)
			continue
		}
		report.SourcesHealthy++
		if len(extraction.Observations) == 0 {
			report.SourcesEmpty++
		}
		promoted, err := p.Store.RecordDiscoverySuccess(
			ctx, candidateRecord, item.resolved.Source, len(extraction.Observations),
			item.resolved.Confidence, item.resolved.Evidence, checkedAt, checkedAt.Add(marketDiscoveryRetry),
		)
		if err != nil {
			return report, fmt.Errorf("record market source success: %w", err)
		}
		if promoted {
			report.SourcesPromoted++
			p.log(ctx, slog.LevelInfo, "source_promoted", "market-discovered source promoted into routine monitoring",
				"candidate_id", item.candidate.ID, "company", item.candidate.Name,
				"source_id", item.resolved.Source.ID, "provider", item.resolved.Source.Provider,
				"url", item.resolved.Source.URL, "observed_count", len(extraction.Observations))
		} else if len(extraction.Observations) > 0 {
			report.SourcesMonitored++
			p.log(ctx, slog.LevelInfo, "source_already_monitored", "healthy market-derived source is already owned by routine monitoring",
				"candidate_id", item.candidate.ID, "company", item.candidate.Name,
				"source_id", item.resolved.Source.ID, "provider", item.resolved.Source.Provider,
				"url", item.resolved.Source.URL, "observed_count", len(extraction.Observations))
		} else {
			p.log(ctx, slog.LevelInfo, "source_validating", "market-derived empty source remains quarantined",
				"candidate_id", item.candidate.ID, "company", item.candidate.Name,
				"source_id", item.resolved.Source.ID, "provider", item.resolved.Source.Provider,
				"url", item.resolved.Source.URL)
		}
	}
	p.log(ctx, slog.LevelInfo, "batch_completed", "market discovery promotion batch completed",
		"observations", report.ObservationsSeen, "companies_discovered", report.CompaniesDiscovered,
		"sources_derived", report.SourcesDerived, "sources_probed", report.SourcesProbed,
		"sources_healthy", report.SourcesHealthy, "sources_empty", report.SourcesEmpty,
		"sources_rejected", report.SourcesRejected, "sources_promoted", report.SourcesPromoted,
		"sources_already_monitored", report.SourcesMonitored)
	return report, nil
}

func MarketSourceKey(source Source) string {
	provider := strings.ToLower(strings.TrimSpace(source.Provider))
	rawURL := strings.TrimRight(strings.TrimSpace(source.URL), "/")
	parsed, err := url.Parse(rawURL)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		// These providers identify a board by a case-insensitive tenant/board
		// slug. Normalizing only those paths avoids changing native providers
		// whose route or query may be case-sensitive.
		switch provider {
		case "ashby", "gem", "greenhouse", "lever", "smartrecruiters", "workable", "yc_jobs":
			parsed.Path = strings.ToLower(strings.TrimRight(parsed.Path, "/"))
		}
		rawURL = parsed.String()
	}
	return provider + "|" + rawURL
}

func (p MarketSourcePromoter) log(ctx context.Context, level slog.Level, event, message string, attrs ...any) {
	if p.Logger == nil {
		return
	}
	base := []any{"component", "radar_lite_market_discovery", "event", event}
	p.Logger.Log(ctx, level, message, append(base, attrs...)...)
}

func deriveMarketCandidates(observations []Observation) ([]DiscoveryCandidate, []marketSourceCandidate) {
	candidatesByID := make(map[string]DiscoveryCandidate)
	sourcesByKey := make(map[string]marketSourceCandidate)
	for _, observation := range observations {
		if BlockedMarketCandidateWebsite(observation.ApplyURL) {
			continue
		}
		company := CompactMarketCompany(observation.Company)
		if company == "" || BlockedCompany(company) {
			continue
		}
		website := marketCandidateWebsite(observation.ApplyURL)
		candidate := DiscoveryCandidate{
			ID: marketCandidateID(company), Name: company, Website: website,
			Tags: []string{"auto-market-search"},
		}
		candidatesByID[candidate.ID] = candidate
		resolved, ok := sourceFromDiscoveryURL(candidate, observation.ApplyURL, 0.90, "market_search:"+observation.SourceID)
		if !ok {
			continue
		}
		key := strings.ToLower(resolved.Source.Provider) + "|" + strings.TrimRight(resolved.Source.URL, "/")
		if _, exists := sourcesByKey[key]; !exists {
			sourcesByKey[key] = marketSourceCandidate{candidate: candidate, resolved: resolved}
		}
	}

	candidates := make([]DiscoveryCandidate, 0, len(candidatesByID))
	for _, candidate := range candidatesByID {
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	sourceKeys := make([]string, 0, len(sourcesByKey))
	for key := range sourcesByKey {
		sourceKeys = append(sourceKeys, key)
	}
	sort.Strings(sourceKeys)
	sources := make([]marketSourceCandidate, 0, len(sourceKeys))
	for _, key := range sourceKeys {
		sources = append(sources, sourcesByKey[key])
	}
	return candidates, sources
}

func BlockedMarketCandidateWebsite(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return true
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	for _, blocked := range []string{
		"brightnetwork.co.uk", "builtin.com", "careerbuilder.com", "dice.com", "expatjobboard.com",
		"builtinsf.com", "cryptocurrencyjobs.co", "deepfinresearch.com", "efinancialcareers.com",
		"extern.com", "glassdoor.com", "gradconnection.com", "handshake.com", "hiring.cafe", "indeed.com",
		"interninsider.me", "internships.com", "jobright.ai", "levels.fyi", "linkedin.com",
		"jorb.ai", "monster.com", "notify.careers", "prosple.com", "remoterocketship.com", "ripplematch.com", "simplify.jobs",
		"startup.jobs", "swiftcruit.ai", "talent.com", "targetjobs.co.uk", "tealhq.com", "themuse.com",
		"wayup.com", "wellfound.com", "workatastartup.com", "ziprecruiter.com",
		"wizbii.com",
	} {
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return true
		}
	}
	return false
}

func CompactMarketCompany(company string) string {
	company = strings.Join(strings.Fields(strings.ToValidUTF8(strings.TrimSpace(company), "")), " ")
	if len(company) < 2 || len(company) > 100 {
		return ""
	}
	normalized := normalizedText(company)
	if BlockedMarketCandidateName(company) {
		return ""
	}
	if normalized == "" || hasAnyPhrase(normalized, []string{
		"unknown company", "confidential company", "market discovery", "linkedin", "indeed",
		"glassdoor", "ziprecruiter", "job board", "job search", "multiple companies",
	}) {
		return ""
	}
	switch strings.TrimSpace(normalized) {
	case "sonyglobal":
		return "Sony"
	case "walleyecapital external students":
		return "Walleye Capital"
	}
	return company
}

func BlockedMarketCandidateName(company string) bool {
	_, blocked := map[string]struct{}{
		"base": {}, "builtin sf": {}, "builtinsf": {}, "campusjobs": {},
		"deepfinresearch": {}, "efinancialcareers": {}, "extern": {},
		"jorb": {}, "novaflow s25": {}, "remote rocketship": {},
		"remoterocketship": {}, "startup": {}, "work at a startup": {},
	}[strings.TrimSpace(normalizedText(company))]
	return blocked
}

func marketCandidateID(company string) string {
	normalized := strings.ToLower(strings.TrimSpace(company))
	slug := strings.Trim(marketCandidateSlugPattern.ReplaceAllString(normalized, "-"), "-")
	if slug == "" {
		slug = "company"
	}
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	hash := sha256.Sum256([]byte(normalized))
	return "market-" + slug + "-" + hex.EncodeToString(hash[:3])
}

func marketCandidateWebsite(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if host == "ycombinator.com" {
		// A YC profile is job-platform evidence, not the company's own website.
		// Leaving Website empty avoids treating every YC page as same-company
		// ownership during later source resolution.
		return ""
	}
	if strings.HasSuffix(host, "greenhouse.io") || strings.HasSuffix(host, "ashbyhq.com") ||
		strings.HasSuffix(host, "lever.co") || strings.HasSuffix(host, "myworkdayjobs.com") ||
		host == "jobs.gem.com" || host == "careers.smartrecruiters.com" || host == "apply.workable.com" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Hostname()
}
