package lite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hwennnn/radar/internal/tinyfish"
)

const (
	defaultDiscoveryBatch            = 16
	defaultDiscoveryCandidateTimeout = 45 * time.Second
	defaultDiscoveryRetry            = 6 * time.Hour
	defaultDiscoveryTransientRetry   = 5 * time.Minute
	defaultDiscoveryRefresh          = 7 * 24 * time.Hour
	maxDiscoveryTransientRetry       = time.Hour
	maxDiscoveryRetry                = 7 * 24 * time.Hour
	defaultDiscoveryEmptyRetry       = time.Hour
	defaultDiscoveryFailureThreshold = 3
	maxDiscoverySearchResults        = 8
	maxDiscoveryFetchURLs            = 4
	maxDiscoverySourceProbes         = 3
)

var (
	discoveryURLPattern = regexp.MustCompile(`https?://[^\s<>\[\](){}"']+`)
	languagePathPattern = regexp.MustCompile(`^[a-z]{2}(?:-[A-Z]{2})?$`)
)

// DiscoveryCandidateRecord is the durable scheduling state for one company
// that Radar is trying to resolve into a monitorable structured source.
type DiscoveryCandidateRecord struct {
	DiscoveryCandidate
	State         string     `json:"state"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
}

// DiscoveredSource is a source proposed by TinyFish and verified by the real
// ATS extractor. Only State=promoted enters routine monitoring.
type DiscoveredSource struct {
	Source
	CandidateID          string     `json:"candidate_id"`
	State                string     `json:"state"`
	Confidence           float64    `json:"confidence"`
	ObservedCount        int        `json:"observed_count"`
	ConsecutiveSuccesses int        `json:"consecutive_successes"`
	ConsecutiveFailures  int        `json:"consecutive_failures"`
	LastCheckedAt        time.Time  `json:"last_checked_at"`
	LastSuccessAt        *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt        *time.Time `json:"last_failure_at,omitempty"`
	PromotedAt           *time.Time `json:"promoted_at,omitempty"`
	LastError            string     `json:"last_error,omitempty"`
	Evidence             string     `json:"evidence,omitempty"`
}

type DiscoveryReport struct {
	CandidatesAttempted int `json:"candidates_attempted"`
	SourcesResolved     int `json:"sources_resolved"`
	SourcesProbed       int `json:"sources_probed"`
	SourcesHealthy      int `json:"sources_healthy"`
	SourcesEmpty        int `json:"sources_empty"`
	SourcesRejected     int `json:"sources_rejected"`
	SourcesPromoted     int `json:"sources_promoted"`
	SourcesDemoted      int `json:"sources_demoted"`
	CandidatesFailed    int `json:"candidates_failed"`
}

type TinyFishDiscoveryClient interface {
	Search(context.Context, tinyfish.SearchRequest) (tinyfish.SearchResponse, error)
	Fetch(context.Context, tinyfish.FetchRequest) (tinyfish.FetchResponse, error)
}

type DiscoveryRepository interface {
	SeedDiscoveryCandidates(context.Context, []DiscoveryCandidate) error
	ListDueDiscoveryCandidates(context.Context, time.Time, int) ([]DiscoveryCandidateRecord, error)
	RecordDiscoveryFailure(context.Context, DiscoveryCandidateRecord, *Source, error, time.Time, time.Time) error
	RecordDiscoverySuccess(context.Context, DiscoveryCandidateRecord, Source, int, float64, string, time.Time, time.Time) (bool, error)
	ListPromotedSources(context.Context) ([]Source, error)
	DemoteUnhealthyDiscoveredSources(context.Context, int, time.Time) (int, error)
}

type DiscoveryRunner struct {
	Candidates       []DiscoveryCandidate
	Client           TinyFishDiscoveryClient
	Extractor        Extractor
	Store            DiscoveryRepository
	Batch            int
	CandidateTimeout time.Duration
	RetryDelay       time.Duration
	EmptyRetryDelay  time.Duration
	FailureThreshold int
	Now              func() time.Time
	Logger           *slog.Logger
}

type resolvedDiscoverySource struct {
	Source     Source
	Confidence float64
	Evidence   string
}

type discoverySnapshotQuality struct {
	Usable       int
	Relevant     int
	Rejected     int
	SampleTitles []string
}

func (r DiscoveryRunner) Run(ctx context.Context) (DiscoveryReport, error) {
	var report DiscoveryReport
	if r.Client == nil {
		return report, errors.New("lite discovery: TinyFish client is required")
	}
	if r.Extractor == nil {
		return report, errors.New("lite discovery: production extractor is required")
	}
	if r.Store == nil {
		return report, errors.New("lite discovery: store is required")
	}
	if err := r.Store.SeedDiscoveryCandidates(ctx, r.Candidates); err != nil {
		return report, fmt.Errorf("seed discovery candidates: %w", err)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	checkedAt := now().UTC()
	failureThreshold := r.FailureThreshold
	if failureThreshold <= 0 {
		failureThreshold = defaultDiscoveryFailureThreshold
	}
	demoted, err := r.Store.DemoteUnhealthyDiscoveredSources(ctx, failureThreshold, checkedAt)
	if err != nil {
		return report, fmt.Errorf("triage promoted source health: %w", err)
	}
	report.SourcesDemoted = demoted
	if demoted > 0 {
		r.logEvent(ctx, slog.LevelWarn, "sources_demoted", "automatically demoted unhealthy discovered sources", "sources_demoted", demoted, "failure_threshold", failureThreshold)
	}

	batch := r.Batch
	if batch <= 0 {
		batch = defaultDiscoveryBatch
	}
	candidates, err := r.Store.ListDueDiscoveryCandidates(ctx, checkedAt, batch)
	if err != nil {
		return report, fmt.Errorf("list due discovery candidates: %w", err)
	}
	r.logEvent(ctx, slog.LevelInfo, "batch_started", "autodiscovery batch started", "due_candidates", len(candidates), "batch_limit", batch)
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.CandidatesAttempted++
		r.logEvent(ctx, slog.LevelInfo, "candidate_started", "autodiscovery candidate started",
			"candidate_id", candidate.ID, "company", candidate.Name, "attempt", candidate.Attempts+1, "previous_state", candidate.State)
		candidateCtx, cancel := context.WithTimeout(ctx, r.candidateTimeout())
		sources, resolveErr := r.resolveCandidate(candidateCtx, candidate)
		cancel()
		attemptedAt := now().UTC()
		if resolveErr != nil {
			report.CandidatesFailed++
			nextAttempt := attemptedAt.Add(r.retryDelayFor(candidate.Attempts))
			retryClass := "quarantine"
			if transientRetryAt, transient := transientDiscoveryRetryAt(resolveErr, attemptedAt, candidate.Attempts); transient {
				nextAttempt = transientRetryAt
				retryClass = "transient"
			}
			if err := r.Store.RecordDiscoveryFailure(ctx, candidate, nil, resolveErr, attemptedAt, nextAttempt); err != nil {
				return report, fmt.Errorf("record discovery search failure: %w", err)
			}
			r.logEvent(ctx, slog.LevelWarn, "candidate_resolution_failed", "TinyFish could not resolve a structured source",
				"candidate_id", candidate.ID, "company", candidate.Name, "error", compactDiscoveryError(resolveErr.Error()),
				"retry_class", retryClass, "retry_in_seconds", int(nextAttempt.Sub(attemptedAt).Seconds()), "next_attempt_at", nextAttempt)
			continue
		}
		report.SourcesResolved += len(sources)
		if len(sources) == 0 {
			report.CandidatesFailed++
			nextAttempt := attemptedAt.Add(r.retryDelayFor(candidate.Attempts))
			if err := r.Store.RecordDiscoveryFailure(ctx, candidate, nil, errors.New("no structured ATS source found"), attemptedAt, nextAttempt); err != nil {
				return report, fmt.Errorf("record unresolved discovery candidate: %w", err)
			}
			r.logEvent(ctx, slog.LevelWarn, "candidate_unresolved", "TinyFish returned no candidate-owned structured source",
				"candidate_id", candidate.ID, "company", candidate.Name, "next_attempt_at", nextAttempt)
			continue
		}
		r.logEvent(ctx, slog.LevelInfo, "candidate_resolved", "TinyFish resolved structured source candidates",
			"candidate_id", candidate.ID, "company", candidate.Name, "sources_resolved", len(sources))

		healthy := false
		for index, resolved := range sources {
			if index >= maxDiscoverySourceProbes {
				break
			}
			r.logEvent(ctx, slog.LevelInfo, "source_probe_started", "probing discovered source with production extractor",
				"candidate_id", candidate.ID, "company", candidate.Name, "source_id", resolved.Source.ID,
				"provider", resolved.Source.Provider, "url", resolved.Source.URL, "confidence", resolved.Confidence, "probe", index+1)
			report.SourcesProbed++
			probeCtx, probeCancel := context.WithTimeout(ctx, r.candidateTimeout())
			extraction, probeErr := r.Extractor.Extract(probeCtx, resolved.Source)
			probeCancel()
			probeAt := now().UTC()
			if probeErr != nil || !extraction.Complete {
				report.SourcesRejected++
				if probeErr == nil {
					probeErr = errors.New("ATS extractor returned an incomplete snapshot")
				}
				nextAttempt := probeAt.Add(r.retryDelayFor(candidate.Attempts))
				retryClass := "quarantine"
				if transientRetryAt, transient := transientDiscoveryRetryAt(probeErr, probeAt, candidate.Attempts); transient {
					nextAttempt = transientRetryAt
					retryClass = "transient"
				}
				if err := r.Store.RecordDiscoveryFailure(ctx, candidate, &resolved.Source, probeErr, probeAt, nextAttempt); err != nil {
					return report, fmt.Errorf("record discovery probe failure: %w", err)
				}
				r.logEvent(ctx, slog.LevelWarn, "source_probe_failed", "discovered source failed production verification",
					"candidate_id", candidate.ID, "company", candidate.Name, "source_id", resolved.Source.ID,
					"provider", resolved.Source.Provider, "url", resolved.Source.URL,
					"error", compactDiscoveryError(probeErr.Error()), "retry_class", retryClass,
					"retry_in_seconds", int(nextAttempt.Sub(probeAt).Seconds()), "next_attempt_at", nextAttempt)
				continue
			}
			quality := assessDiscoverySnapshotQuality(extraction.Observations)
			if len(extraction.Observations) > 0 && (quality.Usable == 0 || quality.Relevant == 0) {
				report.SourcesRejected++
				qualityErr := fmt.Errorf("structured board returned %d postings but no relevant technical job roles; usable=%d relevant=%d sample titles: %s", len(extraction.Observations), quality.Usable, quality.Relevant, strings.Join(quality.SampleTitles, " | "))
				nextAttempt := probeAt.Add(r.retryDelayFor(candidate.Attempts))
				if err := r.Store.RecordDiscoveryFailure(ctx, candidate, &resolved.Source, qualityErr, probeAt, nextAttempt); err != nil {
					return report, fmt.Errorf("record discovery quality failure: %w", err)
				}
				r.logEvent(ctx, slog.LevelWarn, "source_quality_rejected", "discovered source contained no active job-role postings",
					"candidate_id", candidate.ID, "company", candidate.Name, "source_id", resolved.Source.ID,
					"provider", resolved.Source.Provider, "url", resolved.Source.URL,
					"observed_count", len(extraction.Observations), "usable_count", quality.Usable, "relevant_count", quality.Relevant,
					"rejected_count", quality.Rejected, "sample_titles", quality.SampleTitles, "next_attempt_at", nextAttempt)
				continue
			}

			healthy = true
			report.SourcesHealthy++
			nextAttempt := probeAt.Add(defaultDiscoveryRefresh)
			if len(extraction.Observations) == 0 {
				report.SourcesEmpty++
				nextAttempt = probeAt.Add(r.emptyRetryDelay())
			}
			promoted, err := r.Store.RecordDiscoverySuccess(
				ctx, candidate, resolved.Source, len(extraction.Observations),
				resolved.Confidence, resolved.Evidence, probeAt, nextAttempt,
			)
			if err != nil {
				return report, fmt.Errorf("record healthy discovered source: %w", err)
			}
			if promoted {
				report.SourcesPromoted++
				r.logEvent(ctx, slog.LevelInfo, "source_promoted", "verified source promoted into routine monitoring",
					"candidate_id", candidate.ID, "company", candidate.Name, "source_id", resolved.Source.ID,
					"provider", resolved.Source.Provider, "url", resolved.Source.URL, "confidence", resolved.Confidence,
					"observed_count", len(extraction.Observations), "usable_count", quality.Usable, "relevant_count", quality.Relevant,
					"rejected_count", quality.Rejected, "sample_titles", quality.SampleTitles)
				// Continue through the bounded candidate set. Large companies often
				// split university hiring by geography; stopping at the first healthy
				// board silently loses the remaining regions.
				continue
			}
			if len(extraction.Observations) > 0 {
				r.logEvent(ctx, slog.LevelInfo, "source_already_monitored", "healthy source is already owned by routine monitoring",
					"candidate_id", candidate.ID, "company", candidate.Name, "source_id", resolved.Source.ID,
					"provider", resolved.Source.Provider, "url", resolved.Source.URL, "confidence", resolved.Confidence,
					"observed_count", len(extraction.Observations), "next_attempt_at", nextAttempt)
				continue
			}
			r.logEvent(ctx, slog.LevelInfo, "source_validating", "empty source remains quarantined until it contains a real posting",
				"candidate_id", candidate.ID, "company", candidate.Name, "source_id", resolved.Source.ID,
				"provider", resolved.Source.Provider, "url", resolved.Source.URL, "confidence", resolved.Confidence,
				"observed_count", len(extraction.Observations), "next_attempt_at", nextAttempt)
		}
		if !healthy {
			report.CandidatesFailed++
			r.logEvent(ctx, slog.LevelWarn, "candidate_failed", "all resolved sources failed ATS verification",
				"candidate_id", candidate.ID, "company", candidate.Name, "sources_resolved", len(sources))
		}
	}
	r.logEvent(ctx, slog.LevelInfo, "batch_completed", "autodiscovery batch completed",
		"candidates_attempted", report.CandidatesAttempted, "candidates_failed", report.CandidatesFailed,
		"sources_resolved", report.SourcesResolved, "sources_probed", report.SourcesProbed,
		"sources_healthy", report.SourcesHealthy, "sources_empty", report.SourcesEmpty,
		"sources_rejected", report.SourcesRejected,
		"sources_promoted", report.SourcesPromoted, "sources_demoted", report.SourcesDemoted)
	return report, nil
}

func assessDiscoverySnapshotQuality(observations []Observation) discoverySnapshotQuality {
	quality := discoverySnapshotQuality{SampleTitles: make([]string, 0, 3)}
	for _, observation := range observations {
		title := compactDiscoveryEvidence(observation.Title)
		if title != "" && len(quality.SampleTitles) < 3 {
			quality.SampleTitles = append(quality.SampleTitles, title)
		}
		normalizedTitle := normalizedText(title)
		if title == "" || hasAnyPhrase(normalizedTitle, rejectedEventPhrases) || hasAnyPhrase(normalizedTitle, []string{
			"register your interest", "expression of interest", "talent pool", "work experience programme",
			"work experience program", "careers newsletter", "job alert signup",
		}) {
			quality.Rejected++
			continue
		}
		quality.Usable++
		if discoveryTechnicalTitle(normalizedTitle) {
			quality.Relevant++
		}
	}
	return quality
}

func discoveryTechnicalTitle(normalizedTitle string) bool {
	if hasAnyPhrase(normalizedTitle, acceptedRolePhrases) {
		return true
	}
	if hasAnyPhrase(normalizedTitle, []string{"quant", "quantitative"}) && hasPhrase(normalizedTitle, "analyst") {
		return true
	}
	// Language-specific developer titles are strong enough to establish a
	// technical board even when they omit the word software. A bare engineer,
	// developer, or scientist token is not: it also describes mechanical,
	// hardware, structural, business-development, and laboratory roles.
	if hasAnyPhrase(normalizedTitle, []string{
		"c developer", "java developer", "python developer", "golang developer", "go developer", "rust developer",
	}) {
		return true
	}
	aiOrData := hasAnyPhrase(normalizedTitle, []string{
		"ai", "genai", "ml", "machine learning", "artificial intelligence", "llm", "nlp",
		"computer vision", "deep learning", "data", "quant", "quantitative",
	})
	technicalWork := hasAnyPhrase(normalizedTitle, []string{"research", "trader", "trading"})
	return aiOrData && technicalWork
}

func (r DiscoveryRunner) logEvent(ctx context.Context, level slog.Level, event, message string, attrs ...any) {
	if r.Logger == nil {
		return
	}
	base := []any{"component", "radar_lite_discovery", "event", event}
	r.Logger.Log(ctx, level, message, append(base, attrs...)...)
}

func (r DiscoveryRunner) resolveCandidate(ctx context.Context, candidate DiscoveryCandidateRecord) ([]resolvedDiscoverySource, error) {
	search, err := r.Client.Search(ctx, tinyfish.SearchRequest{Query: discoveryQuery(candidate.DiscoveryCandidate)})
	if err != nil {
		return nil, err
	}
	results := search.Results
	if len(results) > maxDiscoverySearchResults {
		results = results[:maxDiscoverySearchResults]
	}
	resolved := resolveSearchSources(candidate.DiscoveryCandidate, results)
	matchedResults, rejectedRoutes := discoverySearchDiagnostics(candidate.DiscoveryCandidate, results)
	r.logEvent(ctx, slog.LevelInfo, "tinyfish_search_completed", "TinyFish candidate source search completed",
		"candidate_id", candidate.ID, "company", candidate.Name, "search_results", len(results),
		"candidate_matched_results", matchedResults, "accepted_routes", len(resolved), "rejected_routes", rejectedRoutes)
	if len(resolved) > 0 {
		return resolved, nil
	}

	fetchURLs := discoveryFetchURLs(candidate.DiscoveryCandidate, results)
	if len(fetchURLs) == 0 {
		return officialCareersFallback(candidate.DiscoveryCandidate), nil
	}
	fetched, err := r.Client.Fetch(ctx, tinyfish.FetchRequest{URLs: fetchURLs, Format: "markdown"})
	if err != nil {
		return nil, err
	}
	resolved = resolveFetchedSources(candidate.DiscoveryCandidate, fetched.Results)
	if len(resolved) == 0 {
		resolved = officialCareersFallback(candidate.DiscoveryCandidate)
		if len(resolved) > 0 {
			r.logEvent(ctx, slog.LevelInfo, "official_fallback_resolved", "no structured ATS found; probing researched official domain",
				"candidate_id", candidate.ID, "company", candidate.Name,
				"provider", resolved[0].Source.Provider, "url", resolved[0].Source.URL)
		}
	}
	r.logEvent(ctx, slog.LevelInfo, "tinyfish_fetch_completed", "TinyFish candidate page fetch completed",
		"candidate_id", candidate.ID, "company", candidate.Name, "requested_pages", len(fetchURLs),
		"fetched_pages", len(fetched.Results), "fetch_errors", len(fetched.Errors), "accepted_routes", len(resolved))
	return resolved, nil
}

// officialCareersFallback turns a researched company domain into a bounded,
// same-site TinyFish search source when no structured ATS route can be found.
// It is deliberately last-resort: the production extractor still has to return
// a complete, non-empty, technical snapshot before the source is promoted.
func officialCareersFallback(candidate DiscoveryCandidate) []resolvedDiscoverySource {
	raw := strings.TrimSpace(candidate.Website)
	if raw == "" || blockedMarketCandidateWebsite(raw) {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
		return nil
	}
	host := strings.ToLower(parsed.Hostname())
	if port := parsed.Port(); port != "" {
		host += ":" + port
	}
	boardURL := "https://" + host
	if !sameDiscoverySite(candidate.Website, boardURL) {
		return nil
	}
	hash := sha256.Sum256([]byte("official_careers|" + boardURL))
	return []resolvedDiscoverySource{{
		Source: Source{
			ID:       "auto-" + candidate.ID + "-official-careers-" + hex.EncodeToString(hash[:4]),
			Company:  strings.TrimSpace(candidate.Name),
			Provider: "official_careers",
			URL:      boardURL,
		},
		Confidence: 0.84,
		Evidence:   "researched official domain fallback",
	}}
}

func discoverySearchDiagnostics(candidate DiscoveryCandidate, results []tinyfish.SearchResult) (int, []string) {
	matched := 0
	rejected := make([]string, 0, 3)
	for _, result := range results {
		if !discoverySearchResultMatches(candidate, result) {
			continue
		}
		matched++
		if _, accepted := sourceFromDiscoveryURL(candidate, result.URL, 0.96, "diagnostic"); accepted {
			continue
		}
		if len(rejected) < 3 && looksLikeStructuredDiscoveryRoute(result.URL) {
			rejected = append(rejected, compactDiscoveryEvidence(result.URL))
		}
	}
	return matched, rejected
}

func looksLikeStructuredDiscoveryRoute(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if provider, _ := officialDiscoveryRoute(parsed); provider != "" {
		return true
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	return strings.HasSuffix(host, "greenhouse.io") || strings.HasSuffix(host, "ashbyhq.com") ||
		strings.HasSuffix(host, "lever.co") || host == "jobs.gem.com" || strings.HasSuffix(host, "myworkdayjobs.com") ||
		host == "careers.smartrecruiters.com" || host == "apply.workable.com"
}

func (r DiscoveryRunner) candidateTimeout() time.Duration {
	if r.CandidateTimeout > 0 {
		return r.CandidateTimeout
	}
	return defaultDiscoveryCandidateTimeout
}

func (r DiscoveryRunner) retryDelay() time.Duration {
	if r.RetryDelay > 0 {
		return r.RetryDelay
	}
	return defaultDiscoveryRetry
}

func (r DiscoveryRunner) retryDelayFor(previousAttempts int) time.Duration {
	delay := r.retryDelay()
	if delay <= 0 {
		delay = defaultDiscoveryRetry
	}
	if previousAttempts < 0 {
		previousAttempts = 0
	}
	for range min(previousAttempts, 8) {
		if delay >= maxDiscoveryRetry/2 {
			return maxDiscoveryRetry
		}
		delay *= 2
	}
	if delay > maxDiscoveryRetry {
		return maxDiscoveryRetry
	}
	return delay
}

// transientDiscoveryRetryAt keeps provider outages and rate limits on a short
// recovery loop while deterministic ownership/quality failures retain the
// slower quarantine schedule. Retry-After is honored up to the global durable
// retry cap.
func transientDiscoveryRetryAt(cause error, attemptedAt time.Time, previousAttempts int) (time.Time, bool) {
	if !transientExtractionError(cause) {
		return time.Time{}, false
	}
	delay := defaultDiscoveryTransientRetry
	if previousAttempts < 0 {
		previousAttempts = 0
	}
	for range min(previousAttempts, 8) {
		if delay >= maxDiscoveryTransientRetry/2 {
			delay = maxDiscoveryTransientRetry
			break
		}
		delay *= 2
	}
	var hinted retryAfterError
	if errors.As(cause, &hinted) {
		if retryAfter := hinted.RetryAfter(); retryAfter > delay {
			delay = retryAfter
		}
	}
	if delay > maxDiscoveryRetry {
		delay = maxDiscoveryRetry
	}
	return attemptedAt.Add(delay), true
}

func (r DiscoveryRunner) emptyRetryDelay() time.Duration {
	if r.EmptyRetryDelay > 0 {
		return r.EmptyRetryDelay
	}
	return defaultDiscoveryEmptyRetry
}

func discoveryQuery(candidate DiscoveryCandidate) string {
	scopes := []string{
		"site:job-boards.greenhouse.io", "site:boards.greenhouse.io", "site:jobs.ashbyhq.com",
		"site:jobs.lever.co", "site:jobs.gem.com", "site:myworkdayjobs.com",
		"site:careers.smartrecruiters.com", "site:apply.workable.com",
	}
	if website, err := url.Parse(strings.TrimSpace(candidate.Website)); err == nil && website.Hostname() != "" {
		scopes = append([]string{"site:" + strings.ToLower(website.Hostname())}, scopes...)
	}
	return fmt.Sprintf(`%q official jobs careers (%s)`, strings.TrimSpace(candidate.Name), strings.Join(scopes, " OR "))
}

func resolveSearchSources(candidate DiscoveryCandidate, results []tinyfish.SearchResult) []resolvedDiscoverySource {
	var sources []resolvedDiscoverySource
	for _, result := range results {
		if !discoverySearchResultMatches(candidate, result) {
			continue
		}
		if source, ok := sourceFromDiscoveryURL(candidate, result.URL, 0.96, "tinyfish_search:"+compactDiscoveryEvidence(result.Title)); ok {
			sources = append(sources, source)
		}
	}
	return dedupeResolvedSources(sources)
}

func resolveFetchedSources(candidate DiscoveryCandidate, results []tinyfish.FetchResult) []resolvedDiscoverySource {
	var sources []resolvedDiscoverySource
	for _, result := range results {
		content := firstNonEmptyString(result.Markdown, result.Text, result.Content)
		if content == "" {
			continue
		}
		trustedOfficialPage := sameDiscoverySite(candidate.Website, result.URL) || candidateEvidenceMatches(candidate, result.Title+" "+content)
		if !trustedOfficialPage {
			continue
		}
		for _, foundURL := range discoveryURLPattern.FindAllString(content, -1) {
			if source, ok := sourceFromDiscoveryURL(candidate, foundURL, 0.92, "tinyfish_fetch:"+compactDiscoveryEvidence(result.URL)); ok {
				sources = append(sources, source)
			}
		}
	}
	return dedupeResolvedSources(sources)
}

func discoveryFetchURLs(candidate DiscoveryCandidate, results []tinyfish.SearchResult) []string {
	seen := make(map[string]struct{})
	urls := make([]string, 0, maxDiscoveryFetchURLs)
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || len(urls) >= maxDiscoveryFetchURLs {
			return
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return
		}
		if _, exists := seen[raw]; exists {
			return
		}
		seen[raw] = struct{}{}
		urls = append(urls, raw)
	}
	add(candidate.Website)
	for _, result := range results {
		if discoverySearchResultMatches(candidate, result) {
			add(result.URL)
		}
	}
	return urls
}

func discoverySearchResultMatches(candidate DiscoveryCandidate, result tinyfish.SearchResult) bool {
	return sameDiscoverySite(candidate.Website, result.URL) ||
		candidateEvidenceMatches(candidate, result.Title+" "+result.Snippet+" "+result.URL)
}

func sourceFromDiscoveryURL(candidate DiscoveryCandidate, raw string, confidence float64, evidence string) (resolvedDiscoverySource, bool) {
	raw = strings.TrimRight(html.UnescapeString(strings.TrimSpace(raw)), ".,;:!?)\"'")
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return resolvedDiscoverySource{}, false
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	segments := discoveryPathSegments(parsed.Path)
	provider, boardURL := "", ""
	switch {
	case strings.HasSuffix(host, "greenhouse.io"):
		var slug string
		if host == "boards-api.greenhouse.io" && len(segments) >= 3 && segments[0] == "v1" && segments[1] == "boards" {
			slug = segments[2]
		} else if len(segments) > 0 {
			slug = segments[0]
		}
		if slug != "" {
			provider, boardURL = "greenhouse", "https://job-boards.greenhouse.io/"+strings.ToLower(slug)
		}
	case strings.HasSuffix(host, "ashbyhq.com"):
		var slug string
		if host == "api.ashbyhq.com" && len(segments) >= 3 && segments[0] == "posting-api" && segments[1] == "job-board" {
			slug = segments[2]
		} else if len(segments) > 0 {
			slug = segments[0]
		}
		if slug != "" {
			provider, boardURL = "ashby", "https://jobs.ashbyhq.com/"+strings.ToLower(slug)
		}
	case strings.HasSuffix(host, "lever.co"):
		var slug string
		if strings.HasPrefix(host, "api.") && len(segments) >= 3 && segments[0] == "v0" && segments[1] == "postings" {
			slug = segments[2]
		} else if len(segments) > 0 {
			slug = segments[0]
		}
		if slug != "" {
			provider, boardURL = "lever", "https://jobs.lever.co/"+strings.ToLower(slug)
		}
	case host == "jobs.gem.com" && len(segments) > 0:
		provider, boardURL = "gem", "https://jobs.gem.com/"+strings.ToLower(segments[0])
	case strings.HasSuffix(host, "myworkdayjobs.com"):
		if len(segments) > 0 {
			index := 0
			if languagePathPattern.MatchString(segments[0]) && len(segments) > 1 {
				index = 1
			}
			provider, boardURL = "workday", "https://"+parsed.Hostname()+"/"+segments[index]
		}
	case host == "careers.smartrecruiters.com" && len(segments) > 0:
		provider, boardURL = "smartrecruiters", "https://careers.smartrecruiters.com/"+strings.ToLower(segments[0])
	case host == "apply.workable.com" && len(segments) > 0:
		provider, boardURL = "workable", "https://apply.workable.com/"+strings.ToLower(segments[0])
	default:
		provider, boardURL = officialDiscoveryRoute(parsed)
	}
	if provider == "" || boardURL == "" {
		return resolvedDiscoverySource{}, false
	}
	if _, supported := supportedProviders[provider]; !supported {
		return resolvedDiscoverySource{}, false
	}
	if !discoveryRouteMatchesCandidate(candidate, provider, boardURL) {
		return resolvedDiscoverySource{}, false
	}
	hash := sha256.Sum256([]byte(provider + "|" + boardURL))
	providerID := strings.Trim(marketCandidateSlugPattern.ReplaceAllString(strings.ToLower(provider), "-"), "-")
	if providerID == "" {
		return resolvedDiscoverySource{}, false
	}
	sourceID := "auto-" + candidate.ID + "-" + providerID + "-" + hex.EncodeToString(hash[:4])
	return resolvedDiscoverySource{
		Source:     Source{ID: sourceID, Company: strings.TrimSpace(candidate.Name), Provider: provider, URL: boardURL},
		Confidence: confidence,
		Evidence:   compactDiscoveryEvidence(evidence),
	}, true
}

func discoveryRouteMatchesCandidate(candidate DiscoveryCandidate, provider, boardURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(boardURL))
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	segments := discoveryPathSegments(parsed.Path)
	if provider == "yc_jobs" {
		if host != "ycombinator.com" || len(segments) < 3 || segments[0] != "companies" || segments[2] != "jobs" {
			return false
		}
		routeIdentity := compactDiscoveryIdentity(segments[1])
		for _, identity := range []string{compactDiscoveryIdentity(candidate.ID), compactDiscoveryIdentity(candidate.Name)} {
			if identity != "" && (routeIdentity == identity || strings.Contains(identity, routeIdentity) || strings.Contains(routeIdentity, identity)) {
				return true
			}
		}
		return false
	}
	if provider == "citadel_careers" {
		classified, canonical := officialDiscoveryRoute(parsed)
		return classified == provider && canonical != "" && sameDiscoverySite(candidate.Website, canonical)
	}
	if provider == "official_careers" {
		return !blockedMarketCandidateWebsite(boardURL) && sameDiscoverySite(candidate.Website, boardURL)
	}
	if _, usesSearch := searchDiscoveryProviders[provider]; usesSearch {
		classified, canonical := officialDiscoveryRoute(parsed)
		return classified == provider && canonical != "" && sameDiscoverySite(candidate.Website, canonical)
	}
	routeParts := make([]string, 0, 3)
	switch provider {
	case "greenhouse", "ashby", "lever", "gem", "smartrecruiters", "workable":
		if len(segments) > 0 {
			routeParts = append(routeParts, segments[0])
		}
	case "workday":
		if firstHostPart := strings.Split(host, ".")[0]; firstHostPart != "" {
			routeParts = append(routeParts, firstHostPart)
		}
		if len(segments) > 0 {
			routeParts = append(routeParts, segments[0])
		}
	default:
		return false
	}
	routeIdentity := compactDiscoveryIdentity(strings.Join(routeParts, " "))
	if routeIdentity == "" {
		return false
	}
	// Discovery is specifically for early-career monitoring. Provider routes
	// explicitly scoped to experienced/executive/event inventory are narrower
	// than the company and must not become its routine board.
	for _, blocked := range []string{"experienced", "executive", "events"} {
		if strings.Contains(routeIdentity, blocked) {
			return false
		}
	}
	identities := []string{
		compactDiscoveryIdentity(candidate.Name),
		compactDiscoveryIdentity(candidate.ID),
	}
	if website, parseErr := url.Parse(strings.TrimSpace(candidate.Website)); parseErr == nil {
		if websiteLabel := discoveryWebsiteBrand(website.Hostname()); websiteLabel != "" {
			identities = append(identities, compactDiscoveryIdentity(websiteLabel))
		}
	}
	// Search results often contain staffing firms whose board slug starts with
	// the target's brand (for example CitadelSearch for Citadel). Treat common
	// recruiter/agency affixes as an ownership conflict before the looser brand
	// containment checks below. Legitimate legal suffixes such as Systems or
	// Securities remain accepted.
	for _, identity := range identities {
		if discoveryRouteHasRecruiterAffix(routeIdentity, identity) {
			return false
		}
		if discoveryRouteHasConflictingAffix(routeIdentity, identity) {
			return false
		}
	}
	for _, identity := range identities {
		if identity == "" {
			continue
		}
		if routeIdentity == identity || (len(identity) >= 4 && (strings.Contains(routeIdentity, identity) || strings.Contains(identity, routeIdentity))) {
			return true
		}
	}
	for _, token := range strings.Fields(normalizedText(candidate.Name)) {
		if len(token) < 4 || genericDiscoveryBrandToken(token) {
			continue
		}
		if strings.Contains(routeIdentity, compactDiscoveryIdentity(token)) {
			return true
		}
	}
	return false
}

func officialDiscoveryRoute(parsed *url.URL) (string, string) {
	if parsed == nil {
		return "", ""
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	path := "/" + strings.Trim(strings.ToLower(parsed.EscapedPath()), "/")
	segments := discoveryPathSegments(parsed.Path)
	switch {
	case host == "ycombinator.com" && len(segments) >= 3 && segments[0] == "companies" && segments[2] == "jobs":
		return "yc_jobs", "https://www.ycombinator.com/companies/" + strings.ToLower(segments[1]) + "/jobs"
	case host == "citadel.com" && (path == "/career-sitemap.xml" || strings.Contains(path, "/careers")):
		return "citadel_careers", "https://www.citadel.com/career-sitemap.xml"
	case (host == "cursor.com" || host == "anysphere.inc") && strings.Contains(path, "/careers"):
		return "cursor_careers", "https://cursor.com/careers"
	case host == "deshaw.com" && strings.Contains(path, "/careers/internships"):
		return "deshaw_careers", "https://www.deshaw.com/careers/internships"
	case host == "deshaw.com" && strings.Contains(path, "/careers"):
		return "deshaw_careers", "https://www.deshaw.com/careers"
	case host == "groq.com" && strings.Contains(path, "/careers"):
		return "groq_careers", "https://groq.com/careers-at-groq"
	case host == "oldmissioncapital.com" && strings.Contains(path, "/working-at-old-mission"):
		return "oldmission_careers", "https://www.oldmissioncapital.com/working-at-old-mission/"
	case host == "oldmissioncapital.com" && strings.Contains(path, "/careers"):
		return "oldmission_careers", "https://www.oldmissioncapital.com/careers/"
	case host == "careers.sig.com" && strings.Contains(path, "/jobs"):
		return "sig_careers", "https://careers.sig.com/jobs"
	case host == "sig.com" && strings.Contains(path, "/careers"):
		return "sig_careers", "https://sig.com/careers/interns-co-ops/"
	case host == "careers.tiktok.com" && strings.Contains(path, "/position"):
		return "tiktok_careers", "https://careers.tiktok.com/position?keywords=software%20engineer%20intern&type=2"
	case host == "careers.twosigma.com" && strings.Contains(path, "/careers"):
		return "twosigma_careers", "https://careers.twosigma.com/careers/OpenRoles"
	case host == "twosigma.com" && strings.Contains(path, "/careers"):
		return "twosigma_careers", "https://www.twosigma.com/careers/internships/"
	default:
		return "", ""
	}
}

func discoveryRouteHasRecruiterAffix(routeIdentity, candidateIdentity string) bool {
	if routeIdentity == "" || candidateIdentity == "" {
		return false
	}
	for _, affix := range []string{
		"search", "staffing", "recruiting", "recruitment", "headhunting",
		"executivesearch", "talent", "talentsolutions",
	} {
		if routeIdentity == candidateIdentity+affix || routeIdentity == affix+candidateIdentity {
			return true
		}
	}
	return false
}

func discoveryRouteHasConflictingAffix(routeIdentity, candidateIdentity string) bool {
	if routeIdentity == "" || candidateIdentity == "" {
		return false
	}
	base := strings.TrimPrefix(routeIdentity, "the")
	if !strings.HasPrefix(base, candidateIdentity) {
		return false
	}
	suffix := strings.TrimPrefix(base, candidateIdentity)
	if suffix == "" {
		return false
	}
	allDigits := true
	for _, character := range suffix {
		if character < '0' || character > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return true
	}
	for _, conflicting := range []string{
		"alliance", "health", "healthcare", "hotel", "hotels", "hotelandresorts", "hotelsandresorts", "resort", "resorts",
		"logistics", "logisticsanddistribution",
	} {
		if suffix == conflicting {
			return true
		}
	}
	return false
}

func discoveryWebsiteBrand(host string) string {
	host = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.")
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return ""
	}
	for _, label := range labels[:len(labels)-1] {
		label = strings.TrimSpace(label)
		if len(label) < 3 || genericDiscoveryBrandToken(label) {
			continue
		}
		return label
	}
	return ""
}

func compactDiscoveryIdentity(value string) string {
	return strings.Join(strings.Fields(normalizedText(value)), "")
}

func genericDiscoveryBrandToken(token string) bool {
	switch token {
	case "capital", "careers", "company", "group", "international", "jobs", "markets", "research", "search", "securities", "systems", "technologies", "technology", "trading":
		return true
	default:
		return false
	}
}

func discoveryPathSegments(path string) []string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	segments := parts[:0]
	for _, part := range parts {
		if clean := strings.TrimSpace(part); clean != "" {
			segments = append(segments, clean)
		}
	}
	return segments
}

func candidateEvidenceMatches(candidate DiscoveryCandidate, evidence string) bool {
	haystack := normalizedText(evidence)
	name := strings.TrimSpace(candidate.Name)
	id := strings.ReplaceAll(strings.TrimSpace(candidate.ID), "-", " ")
	if name != "" && hasPhrase(haystack, name) {
		return true
	}
	if id != "" && hasPhrase(haystack, id) {
		return true
	}
	// Single-token brands (xAI, Groq, Modal) cannot satisfy a multi-token
	// phrase, so require their complete token as a word rather than accepting
	// any partial company token from a noisy search result.
	nameTokens := strings.Fields(normalizedText(name))
	if len(nameTokens) == 1 && len(nameTokens[0]) >= 3 {
		return hasPhrase(haystack, nameTokens[0])
	}
	return false
}

func sameDiscoverySite(left, right string) bool {
	leftURL, leftErr := url.Parse(strings.TrimSpace(left))
	rightURL, rightErr := url.Parse(strings.TrimSpace(right))
	if leftErr != nil || rightErr != nil || leftURL.Hostname() == "" || rightURL.Hostname() == "" {
		return false
	}
	leftHost := strings.TrimPrefix(strings.ToLower(leftURL.Hostname()), "www.")
	rightHost := strings.TrimPrefix(strings.ToLower(rightURL.Hostname()), "www.")
	return leftHost == rightHost || strings.HasSuffix(leftHost, "."+rightHost) || strings.HasSuffix(rightHost, "."+leftHost)
}

func dedupeResolvedSources(input []resolvedDiscoverySource) []resolvedDiscoverySource {
	byKey := make(map[string]resolvedDiscoverySource, len(input))
	for _, source := range input {
		key := marketSourceKey(source.Source)
		if current, exists := byKey[key]; !exists || source.Confidence > current.Confidence {
			byKey[key] = source
		}
	}
	output := make([]resolvedDiscoverySource, 0, len(byKey))
	for _, source := range byKey {
		output = append(output, source)
	}
	sort.Slice(output, func(i, j int) bool {
		if output[i].Confidence != output[j].Confidence {
			return output[i].Confidence > output[j].Confidence
		}
		left := output[i].Source.Provider + "|" + output[i].Source.URL
		right := output[j].Source.Provider + "|" + output[j].Source.URL
		return left < right
	})
	return output
}

func MergeRoutineSources(base, discovered []Source) []Source {
	byKey := make(map[string]Source, len(base)+len(discovered))
	for _, source := range append(append([]Source(nil), base...), discovered...) {
		key := marketSourceKey(source)
		if _, exists := byKey[key]; !exists {
			byKey[key] = source
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	merged := make([]Source, 0, len(keys))
	for _, key := range keys {
		merged = append(merged, byKey[key])
	}
	return merged
}

func compactDiscoveryEvidence(value string) string {
	value = strings.Join(strings.Fields(strings.ToValidUTF8(strings.TrimSpace(value), "")), " ")
	return truncateText(value, 500)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
