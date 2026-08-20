package dashboard

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hwennnn/radar/internal/pipeline"
)

type Store interface {
	feedStore
	ReadOperationalState(context.Context) (pipeline.OperationalState, error)
}

type Config struct {
	BaseSources          []pipeline.Source
	TotalSources         int
	LogoDomains          map[string]string
	RuntimeMode          string
	CycleTimeout         time.Duration
	DeliveryMode         string
	TelegramTokenPresent bool
	TelegramChatPresent  bool
	PublishingEnabled    bool
}

type statusServer struct {
	store  Store
	health HealthProvider
	config Config
	logger *slog.Logger
}

type statusResponse struct {
	GeneratedAt time.Time        `json:"generated_at"`
	State       string           `json:"state"`
	Runtime     statusRuntime    `json:"runtime"`
	Sources     statusSources    `json:"sources"`
	Discovery   statusDiscovery  `json:"discovery"`
	Dedupe      statusDedupe     `json:"dedupe"`
	Deliveries  statusDeliveries `json:"deliveries"`
	Telegram    statusTelegram   `json:"telegram"`
}

type statusRuntime struct {
	Mode             string     `json:"mode"`
	CrawlerEmbedded  bool       `json:"crawler_embedded"`
	Ready            bool       `json:"ready"`
	Degraded         bool       `json:"degraded"`
	CycleRunning     bool       `json:"cycle_running"`
	CycleStale       bool       `json:"cycle_stale"`
	ActiveSince      *time.Time `json:"active_since"`
	LastCycleState   string     `json:"last_cycle_state"`
	LastCycleAt      *time.Time `json:"last_cycle_at"`
	LastCycleError   bool       `json:"last_cycle_error"`
	SourcesAttempted int        `json:"sources_attempted"`
	SourcesSucceeded int        `json:"sources_succeeded"`
	SourcesFailed    int        `json:"sources_failed"`
	Observed         int        `json:"observed"`
	Created          int        `json:"created"`
	EligibleCreated  int        `json:"eligible_created"`
	Enqueued         int        `json:"enqueued"`
	DeliveriesSent   int        `json:"deliveries_sent"`
	DeliveryFailures int        `json:"delivery_failures"`
}

type statusSources struct {
	Configured   int                   `json:"configured"`
	Observed     int                   `json:"observed"`
	Healthy      int                   `json:"healthy"`
	HealthyEmpty int                   `json:"healthy_empty"`
	Failed       int                   `json:"failed"`
	Pending      int                   `json:"pending"`
	Failures     []statusSourceFailure `json:"failures"`
	Monitored    []statusSource        `json:"monitored"`
}

type statusSource struct {
	SourceID      string     `json:"source_id"`
	Company       string     `json:"company"`
	Provider      string     `json:"provider"`
	LogoDomain    string     `json:"logo_domain,omitempty"`
	State         string     `json:"state"`
	ObservedCount int        `json:"observed_count"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
}

type statusSourceFailure struct {
	SourceID            string    `json:"source_id"`
	Company             string    `json:"company"`
	Provider            string    `json:"provider"`
	LastAttemptAt       time.Time `json:"last_attempt_at"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	LastError           string    `json:"last_error"`
}

type statusDiscovery struct {
	Candidates        int `json:"candidates"`
	Due               int `json:"due"`
	PromotedCompanies int `json:"promoted_companies"`
	PromotedSources   int `json:"promoted_sources"`
	CandidateSources  int `json:"candidate_sources"`
	UnhealthySources  int `json:"unhealthy_sources"`
}

type statusDedupe struct {
	CanonicalJobs      int `json:"canonical_jobs"`
	IdentityAliases    int `json:"identity_aliases"`
	SourceObservations int `json:"source_observations"`
	MultiSourceJobs    int `json:"multi_source_jobs"`
}

type statusDeliveries struct {
	Total      int `json:"total"`
	Staged     int `json:"staged"`
	Pending    int `json:"pending"`
	Claimed    int `json:"claimed"`
	Sent       int `json:"sent"`
	Failed     int `json:"failed"`
	Suppressed int `json:"suppressed"`
}

type statusTelegram struct {
	State                     string `json:"state"`
	DeliveryMode              string `json:"delivery_mode"`
	CredentialsPresent        bool   `json:"credentials_present"`
	PublishingGateEnabled     bool   `json:"publishing_gate_enabled"`
	ReadyForUserAuthorization bool   `json:"ready_for_user_authorization"`
	ExternalPublishingActive  bool   `json:"external_publishing_active"`
}

func (s statusServer) handler(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if s.store == nil {
		writeFeedError(w, http.StatusServiceUnavailable, "operational state is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	operational, err := s.store.ReadOperationalState(ctx)
	if err != nil {
		logger := s.logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Error("read Radar operational state", "error", err)
		writeFeedError(w, http.StatusInternalServerError, "could not load operational state")
		return
	}
	response := buildStatusResponse(operational, s.config, s.health)
	_ = json.NewEncoder(w).Encode(response)
}

func buildStatusResponse(operational pipeline.OperationalState, cfg Config, health HealthProvider) statusResponse {
	sourceMetadata := make(map[string]pipeline.Source, len(cfg.BaseSources))
	for _, source := range cfg.BaseSources {
		sourceMetadata[source.ID] = source
	}
	for _, source := range operational.PromotedSources {
		sourceMetadata[source.ID] = source
	}
	sources := statusSources{
		Configured: len(cfg.BaseSources) + operational.DiscoveredCounts["promoted"],
		Failures:   make([]statusSourceFailure, 0),
		Monitored:  make([]statusSource, 0, len(sourceMetadata)+len(operational.RoutineSourceStatus)),
	}
	if cfg.TotalSources > sources.Configured {
		sources.Configured = cfg.TotalSources
	}
	statusByID := make(map[string]pipeline.SourceStatus, len(operational.RoutineSourceStatus))
	for _, current := range operational.RoutineSourceStatus {
		statusByID[current.SourceID] = current
		sources.Observed++
		switch current.State {
		case "success":
			sources.Healthy++
			if current.ObservedCount == 0 {
				sources.HealthyEmpty++
			}
		case "failure":
			sources.Failed++
			metadata := sourceMetadata[current.SourceID]
			sources.Failures = append(sources.Failures, statusSourceFailure{
				SourceID: current.SourceID, Company: metadata.Company, Provider: metadata.Provider,
				LastAttemptAt: current.LastAttemptAt, ConsecutiveFailures: current.ConsecutiveFailures,
				LastError: sanitizeOperationalError(current.LastError),
			})
		}
	}
	for sourceID, metadata := range sourceMetadata {
		// Market-search feeds are control-plane coverage, not employers. They
		// still count toward aggregate health and failures, but keeping them out
		// of the company roster prevents 29 synthetic "companies" from obscuring
		// the actual monitored employers.
		if metadata.Provider == "market_search" {
			delete(statusByID, sourceID)
			continue
		}
		item := statusSource{
			SourceID: sourceID, Company: metadata.Company, Provider: metadata.Provider,
			LogoDomain: companyLogoDomain(metadata.Company, cfg.LogoDomains), State: "pending",
		}
		if current, ok := statusByID[sourceID]; ok {
			item.State = current.State
			item.ObservedCount = current.ObservedCount
			if !current.LastAttemptAt.IsZero() {
				attemptedAt := current.LastAttemptAt
				item.LastAttemptAt = &attemptedAt
			}
			delete(statusByID, sourceID)
		}
		sources.Monitored = append(sources.Monitored, item)
	}
	// Preserve provider rows that are not part of the current static catalog or
	// promoted-source registry. Old market-search rows are intentionally hidden
	// from the employer roster; aggregate source health above still includes
	// them.
	for sourceID, current := range statusByID {
		if strings.HasPrefix(sourceID, "market-") {
			continue
		}
		item := statusSource{
			SourceID: sourceID, Company: sourceID,
			LogoDomain: companyLogoDomain(sourceID, cfg.LogoDomains), State: current.State,
			ObservedCount: current.ObservedCount,
		}
		if !current.LastAttemptAt.IsZero() {
			attemptedAt := current.LastAttemptAt
			item.LastAttemptAt = &attemptedAt
		}
		sources.Monitored = append(sources.Monitored, item)
	}
	sort.SliceStable(sources.Monitored, func(i, j int) bool {
		left, right := strings.ToLower(sources.Monitored[i].Company), strings.ToLower(sources.Monitored[j].Company)
		if left != right {
			return left < right
		}
		return sources.Monitored[i].SourceID < sources.Monitored[j].SourceID
	})
	if sources.Observed > sources.Configured {
		sources.Configured = sources.Observed
	}
	if sources.Configured > sources.Observed {
		sources.Pending = sources.Configured - sources.Observed
	}
	sort.SliceStable(sources.Failures, func(i, j int) bool {
		if sources.Failures[i].ConsecutiveFailures != sources.Failures[j].ConsecutiveFailures {
			return sources.Failures[i].ConsecutiveFailures > sources.Failures[j].ConsecutiveFailures
		}
		return sources.Failures[i].SourceID < sources.Failures[j].SourceID
	})

	deliveries := statusDeliveries{
		Staged: operational.DeliveryCounts["staged"], Pending: operational.DeliveryCounts["pending"], Claimed: operational.DeliveryCounts["claimed"],
		Sent: operational.DeliveryCounts["sent"], Failed: operational.DeliveryCounts["failed"],
		Suppressed: operational.DeliveryCounts["suppressed"],
	}
	deliveries.Total = deliveries.Staged + deliveries.Pending + deliveries.Claimed + deliveries.Sent + deliveries.Failed + deliveries.Suppressed
	discovery := statusDiscovery{
		Candidates:        sumCounts(operational.CandidateCounts),
		Due:               operational.CandidateCounts["pending"] + operational.CandidateCounts["retry"] + operational.CandidateCounts["validating"],
		PromotedCompanies: operational.CandidateCounts["promoted"],
		PromotedSources:   operational.DiscoveredCounts["promoted"],
		CandidateSources:  operational.DiscoveredCounts["candidate"],
		UnhealthySources:  operational.DiscoveredCounts["unhealthy"],
	}
	runtime := statusRuntime{Mode: strings.TrimSpace(cfg.RuntimeMode), CrawlerEmbedded: cfg.RuntimeMode != "serve"}
	if durable := operational.Runtime; durable != nil {
		runtime.CycleRunning = durable.ActiveOwner != ""
		runtime.ActiveSince = durable.ActiveStartedAt
		runtime.LastCycleState = durable.LastCycleState
		runtime.LastCycleAt = durable.LastCycleFinished
		runtime.LastCycleError = durable.LastCycleState == "failure"
		runtime.Degraded = durable.LastCycleState == "degraded"
		runtime.Ready = runtime.CycleRunning || durable.LastCycleState == "success" || durable.LastCycleState == "degraded"
		runtime.SourcesAttempted = durable.SourcesAttempted
		runtime.SourcesSucceeded = durable.SourcesSucceeded
		runtime.SourcesFailed = durable.SourcesFailed
		runtime.Observed = durable.Observed
		runtime.Created = durable.Created
		runtime.EligibleCreated = durable.EligibleCreated
		runtime.Enqueued = durable.Enqueued
		runtime.DeliveriesSent = durable.DeliveriesSent
		runtime.DeliveryFailures = durable.DeliveryFailures
		if runtime.CycleRunning && runtime.ActiveSince != nil {
			cycleTimeout := cfg.CycleTimeout
			if cycleTimeout <= 0 {
				cycleTimeout = 20 * time.Minute
			}
			if operational.GeneratedAt.After(runtime.ActiveSince.Add(cycleTimeout + time.Minute)) {
				runtime.CycleStale = true
				runtime.LastCycleError = true
			}
		}
	}
	if health != nil {
		current := health.Snapshot()
		if current.Ready {
			runtime.Ready = true
		}
		if !current.LastCycleAt.IsZero() && (runtime.LastCycleAt == nil || current.LastCycleAt.After(*runtime.LastCycleAt)) {
			lastCycleAt := current.LastCycleAt
			runtime.LastCycleAt = &lastCycleAt
			runtime.LastCycleError = current.LastCycleError
			runtime.Degraded = current.Degraded
			runtime.SourcesSucceeded, runtime.SourcesFailed = current.SourcesSucceeded, current.SourcesFailed
			runtime.DeliveryFailures = current.DeliveryFailures
			if current.LastCycleError {
				runtime.LastCycleState = "failure"
			} else if current.Degraded {
				runtime.LastCycleState = "degraded"
			} else {
				runtime.LastCycleState = "success"
			}
		}
	}
	telegram := buildTelegramStatus(cfg)
	state := "healthy"
	if sources.Failed > 0 || deliveries.Failed > 0 || deliveries.Staged > 0 || runtime.Degraded || runtime.LastCycleError || runtime.CycleStale {
		state = "degraded"
	} else if sources.Pending > 0 || sources.Observed == 0 {
		state = "pending"
	}
	return statusResponse{
		GeneratedAt: operational.GeneratedAt, State: state, Runtime: runtime, Sources: sources,
		Discovery: discovery,
		Dedupe: statusDedupe{
			CanonicalJobs: operational.CanonicalJobs, IdentityAliases: operational.IdentityAliases,
			SourceObservations: operational.SourceObservations, MultiSourceJobs: operational.MultiSourceJobs,
		},
		Deliveries: deliveries, Telegram: telegram,
	}
}

func buildTelegramStatus(cfg Config) statusTelegram {
	credentialsPresent := cfg.TelegramTokenPresent && cfg.TelegramChatPresent
	status := statusTelegram{
		State: "log_only", DeliveryMode: cfg.DeliveryMode, CredentialsPresent: credentialsPresent,
		PublishingGateEnabled:     cfg.PublishingEnabled,
		ReadyForUserAuthorization: credentialsPresent && !cfg.PublishingEnabled,
	}
	if cfg.DeliveryMode == "telegram" {
		switch {
		case !credentialsPresent:
			status.State = "credentials_missing"
		case !cfg.PublishingEnabled:
			status.State = "locked"
		default:
			status.State = "enabled"
			status.ExternalPublishingActive = true
		}
	} else if credentialsPresent {
		status.State = "locked"
	}
	return status
}

// HealthSnapshot is the dashboard-facing projection of process readiness.
type HealthSnapshot struct {
	Ready            bool
	Degraded         bool
	LastCycleAt      time.Time
	LastCycleError   bool
	SourcesSucceeded int
	SourcesFailed    int
	DeliveryFailures int
}

// HealthProvider lets the dashboard consume runtime health without owning the
// writer lifecycle.
type HealthProvider interface {
	Snapshot() HealthSnapshot
}

// Test-local aliases keep the response-focused tests concise.
type dashboardConfig = Config
type dashboardStore = Store

func sumCounts(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func sanitizeOperationalError(message string) string {
	normalized := strings.ToLower(strings.TrimSpace(message))
	switch {
	case normalized == "":
		return "Source check failed"
	case strings.Contains(normalized, "429") || strings.Contains(normalized, "rate limit"):
		return "Provider rate limited this source"
	case strings.Contains(normalized, "400"):
		return "Provider rejected the source request"
	case strings.Contains(normalized, "timeout") || strings.Contains(normalized, "deadline exceeded"):
		return "Source request timed out"
	case strings.Contains(normalized, "tls") || strings.Contains(normalized, "certificate"):
		return "Secure connection to the source failed"
	case strings.Contains(normalized, "identity does not match"):
		return "Discovered board identity no longer matches the company"
	case strings.Contains(normalized, "incomplete snapshot"):
		return "Source returned an incomplete job snapshot"
	default:
		return "Source check failed; inspect service logs for the private diagnostic"
	}
}
