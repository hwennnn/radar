package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hwennnn/radar/internal/dashboard"
	"github.com/hwennnn/radar/internal/delivery"
	"github.com/hwennnn/radar/internal/pipeline"
	"github.com/hwennnn/radar/internal/postgres"
	"github.com/hwennnn/radar/internal/source/scraper"
	"github.com/hwennnn/radar/internal/source/scraper/tinyfishextractor"
	"github.com/hwennnn/radar/internal/source/tinyfish"
)

const (
	defaultCatalogPath       = "config/sources.json"
	defaultSeedPath          = "config/discovery-seed.json"
	atsHTTPTimeout           = 60 * time.Second
	telegramDeliveryInterval = 3200 * time.Millisecond
	realtimeDeliveryPoll     = 500 * time.Millisecond
	realtimeDeliveryBatch    = 25
)

type config struct {
	mode                  string
	databaseURL           string
	schema                string
	catalogPath           string
	seedPath              string
	deliveryMode          string
	recipient             string
	telegramToken         string
	telegramChat          string
	publishingEnabled     bool
	interval              time.Duration
	cycleTimeout          time.Duration
	healthAddress         string
	once                  bool
	marketOnly            bool
	tinyFishAPIKey        string
	tinyFishSearchBaseURL string
	tinyFishFetchBaseURL  string
	discoveryBatch        int
	discoveryTimeout      time.Duration
	discoveryRetry        time.Duration
	discoveryEmptyRetry   time.Duration
}

type lookupEnv func(string) (string, bool)

type deliveryPumpResult struct {
	report pipeline.DeliveryReport
	err    error
}

// Run starts Radar with the process environment while keeping command wiring
// out of the application package.
func Run(ctx context.Context, args []string, stdout io.Writer, logger *slog.Logger) error {
	return run(ctx, args, os.LookupEnv, stdout, logger)
}

func run(ctx context.Context, args []string, getenv lookupEnv, stdout io.Writer, logger *slog.Logger) error {
	if len(args) > 0 && strings.EqualFold(strings.TrimSpace(args[0]), "telegram-check") {
		return delivery.RunTelegramCheck(ctx, func(key string) string {
			value, _ := getenv(key)
			return value
		}, args[1:], stdout)
	}
	cfg, err := loadConfig(args, getenv)
	if err != nil {
		return err
	}
	if cfg.mode == "discover" || cfg.mode == "audit" {
		return runDiscovery(cfg, stdout, cfg.mode == "audit")
	}
	if cfg.mode == "serve" {
		return runServe(ctx, cfg, logger)
	}
	if cfg.mode == "reconcile" {
		return runReconcile(ctx, cfg, stdout, logger)
	}
	if cfg.mode == "drain" {
		return runDrain(ctx, cfg, stdout, logger)
	}
	return runRoutine(ctx, cfg, logger)
}

func loadConfig(args []string, getenv lookupEnv) (config, error) {
	cfg := config{
		mode:          "routine",
		schema:        envOr(getenv, "RADAR_LITE_SCHEMA", pipeline.DefaultSchema),
		catalogPath:   envOr(getenv, "RADAR_LITE_CATALOG", defaultCatalogPath),
		seedPath:      envOr(getenv, "RADAR_LITE_DISCOVERY_SEED", defaultSeedPath),
		deliveryMode:  strings.ToLower(envOr(getenv, "RADAR_LITE_DELIVERY_MODE", "log")),
		recipient:     envOr(getenv, "RADAR_LITE_RECIPIENT", "local-preview"),
		healthAddress: envOr(getenv, "RADAR_LITE_HEALTH_ADDR", ":8080"),
	}
	if len(args) > 1 {
		return config{}, fmt.Errorf("usage: radar [routine|once|market-once|serve|discover|audit|reconcile|drain]")
	}
	if len(args) == 1 {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "routine":
		case "once":
			cfg.once = true
		case "market", "market-once":
			cfg.mode = "market"
			cfg.once = true
			cfg.marketOnly = true
		case "serve", "web":
			cfg.mode = "serve"
		case "discover", "catalog-gap":
			cfg.mode = "discover"
		case "audit", "coverage":
			cfg.mode = "audit"
		case "reconcile", "discover-live":
			cfg.mode = "reconcile"
		case "drain":
			cfg.mode = "drain"
		default:
			return config{}, fmt.Errorf("unknown mode %q; use routine, once, market-once, serve, discover, audit, reconcile, or drain", args[0])
		}
	}

	if cfg.mode == "discover" || cfg.mode == "audit" {
		return cfg, nil
	}
	cfg.databaseURL = firstEnv(getenv, "RADAR_LITE_DATABASE_URL", "DATABASE_URL")
	if strings.TrimSpace(cfg.databaseURL) == "" {
		return config{}, errors.New("RADAR_LITE_DATABASE_URL or DATABASE_URL is required")
	}
	cfg.telegramToken = firstEnv(getenv, "RADAR_LITE_TELEGRAM_BOT_TOKEN", "RADAR_TELEGRAM_BOT_TOKEN")
	cfg.telegramChat = firstEnv(getenv, "RADAR_LITE_TELEGRAM_CHAT_ID", "RADAR_TELEGRAM_CHAT_ID")
	publishingValue, publishingSet := getenv("RADAR_LITE_PUBLISHING_ENABLED")
	cfg.publishingEnabled = publishingSet && publishingValue == "true"
	if cfg.mode == "serve" {
		return cfg, nil
	}
	cfg.tinyFishAPIKey = firstEnv(getenv, "RADAR_LITE_TINYFISH_API_KEY", "TINYFISH_API_KEY")
	cfg.tinyFishSearchBaseURL = firstEnv(getenv, "RADAR_LITE_TINYFISH_SEARCH_BASE_URL")
	cfg.tinyFishFetchBaseURL = firstEnv(getenv, "RADAR_LITE_TINYFISH_FETCH_BASE_URL")
	if cfg.mode == "reconcile" && cfg.tinyFishAPIKey == "" {
		return config{}, errors.New("reconcile mode requires RADAR_LITE_TINYFISH_API_KEY or TINYFISH_API_KEY")
	}
	if cfg.marketOnly && cfg.tinyFishAPIKey == "" {
		return config{}, errors.New("market-once mode requires RADAR_LITE_TINYFISH_API_KEY or TINYFISH_API_KEY")
	}
	switch cfg.deliveryMode {
	case "log":
	case "telegram":
		if cfg.telegramToken == "" || cfg.telegramChat == "" {
			return config{}, errors.New("telegram mode requires both RADAR_LITE_TELEGRAM_BOT_TOKEN (or RADAR_TELEGRAM_BOT_TOKEN) and RADAR_LITE_TELEGRAM_CHAT_ID (or RADAR_TELEGRAM_CHAT_ID)")
		}
		if !cfg.publishingEnabled {
			return config{}, errors.New("telegram mode requires RADAR_LITE_PUBLISHING_ENABLED to be explicitly set to true")
		}
		cfg.recipient = cfg.telegramChat
	default:
		return config{}, fmt.Errorf("RADAR_LITE_DELIVERY_MODE %q is unsupported; use log or telegram", cfg.deliveryMode)
	}
	var err error
	cfg.interval, err = durationEnv(getenv, "RADAR_LITE_INTERVAL", 15*time.Minute)
	if err != nil {
		return config{}, err
	}
	cfg.cycleTimeout, err = durationEnv(getenv, "RADAR_LITE_CYCLE_TIMEOUT", 20*time.Minute)
	if err != nil {
		return config{}, err
	}
	cfg.discoveryBatch, err = integerEnv(getenv, "RADAR_LITE_DISCOVERY_BATCH", 16, 1, 100)
	if err != nil {
		return config{}, err
	}
	cfg.discoveryTimeout, err = durationEnv(getenv, "RADAR_LITE_DISCOVERY_TIMEOUT", 45*time.Second)
	if err != nil {
		return config{}, err
	}
	cfg.discoveryRetry, err = durationEnv(getenv, "RADAR_LITE_DISCOVERY_RETRY", 6*time.Hour)
	if err != nil {
		return config{}, err
	}
	cfg.discoveryEmptyRetry, err = durationEnv(getenv, "RADAR_LITE_DISCOVERY_EMPTY_RETRY", time.Hour)
	if err != nil {
		return config{}, err
	}
	return cfg, nil
}

func runDiscovery(cfg config, output io.Writer, enforceCoverage bool) error {
	catalogFile, err := os.Open(filepath.Clean(cfg.catalogPath))
	if err != nil {
		return fmt.Errorf("open verified catalog: %w", err)
	}
	defer catalogFile.Close()
	catalog, err := pipeline.LoadCatalog(catalogFile)
	if err != nil {
		return err
	}
	seedFile, err := os.Open(filepath.Clean(cfg.seedPath))
	if err != nil {
		return fmt.Errorf("open discovery seed: %w", err)
	}
	defer seedFile.Close()
	seed, err := pipeline.LoadDiscoverySeed(seedFile)
	if err != nil {
		return err
	}

	missing := pipeline.MissingDiscoveryCandidates(catalog, seed)
	coverage := pipeline.AuditUniverse(catalog, seed)
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if enforceCoverage {
		if err := encoder.Encode(coverage); err != nil {
			return err
		}
		if !coverage.Pass {
			return fmt.Errorf("radar universe coverage audit failed: %s", strings.Join(coverage.Errors, "; "))
		}
		return nil
	}
	if err := encoder.Encode(struct {
		Missing  []pipeline.DiscoveryCandidate `json:"missing_candidates"`
		Count    int                           `json:"count"`
		Coverage pipeline.UniverseCoverage     `json:"coverage"`
	}{Missing: missing, Count: len(missing), Coverage: coverage}); err != nil {
		return err
	}
	return nil
}

func runDrain(ctx context.Context, cfg config, output io.Writer, logger *slog.Logger) error {
	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store, closeStore, err := openStore(startupCtx, cfg, false)
	if err != nil {
		return err
	}
	defer closeStore()
	sender, err := newDeliverySender(cfg, logger)
	if err != nil {
		return err
	}
	drainer := pipeline.DeliveryDrainer{
		Store: store, Sender: sender, Owner: processOwner(),
		Channel: cfg.deliveryMode, Recipient: cfg.recipient,
		Limit: 300, Lease: 2 * time.Minute, RetryDelay: time.Minute,
	}
	if cfg.deliveryMode == "telegram" {
		drainer.MinInterval = telegramDeliveryInterval
	}
	report, err := drainer.Drain(ctx)
	if encodeErr := json.NewEncoder(output).Encode(report); encodeErr != nil {
		err = errors.Join(err, encodeErr)
	}
	return err
}

func runRoutine(ctx context.Context, cfg config, logger *slog.Logger) error {
	catalog, err := loadCatalogFile(cfg.catalogPath)
	if err != nil {
		return err
	}
	baseSources := catalog.RoutineSources()
	if cfg.marketOnly {
		baseSources = nil
	}
	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store, closeStore, err := openStore(startupCtx, cfg, true)
	if err != nil {
		return err
	}
	defer closeStore()
	suppressedKnown, err := store.SuppressKnownDiscoveredSources(startupCtx, catalog.RoutineSources(), time.Now().UTC())
	if err != nil {
		return fmt.Errorf("suppress discovered copies of verified sources: %w", err)
	}
	if suppressedKnown > 0 {
		logger.Info("redundant discovered sources suppressed", "count", suppressedKnown)
	}
	suppressedDuplicates, err := store.SuppressDuplicateDiscoveredSources(startupCtx, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("suppress duplicate discovered sources: %w", err)
	}
	if suppressedDuplicates > 0 {
		logger.Info("duplicate discovered source routes compacted", "count", suppressedDuplicates)
	}
	promotedSources, err := store.ListPromotedSources(startupCtx)
	if err != nil {
		return fmt.Errorf("load promoted discovery sources: %w", err)
	}
	if cfg.marketOnly {
		promotedSources = nil
	}
	marketSources := []pipeline.Source(nil)
	if cfg.tinyFishAPIKey != "" {
		marketSources = pipeline.MarketSearchSources()
	}
	sources := runtimeSources(baseSources, promotedSources, marketSources, cfg.marketOnly)
	dashboardSources := pipeline.MergeRoutineSources(baseSources, marketSources)

	health := &healthState{}
	health.setRuntimeReader(store, false)
	server, serverErrors := startWebServer(cfg.healthAddress, health, store, dashboard.Config{
		BaseSources: dashboardSources, TotalSources: len(sources), RuntimeMode: cfg.mode,
		LogoDomains:  dashboard.LoadCompanyLogoDomains(cfg.seedPath),
		CycleTimeout: cfg.cycleTimeout,
		DeliveryMode: cfg.deliveryMode, TelegramTokenPresent: cfg.telegramToken != "",
		TelegramChatPresent: cfg.telegramChat != "", PublishingEnabled: cfg.publishingEnabled,
	}, logger)
	if server != nil {
		defer func() {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			_ = server.Shutdown(shutdownCtx)
		}()
	}

	productionExtractor := newLiteExtractor(cfg, logger)
	marketObservations := pipeline.NewMarketObservationExtractor(productionExtractor)
	extractor := pipeline.Extractor(marketObservations)
	var discoveryRunner *pipeline.DiscoveryRunner
	if cfg.tinyFishAPIKey != "" && !cfg.marketOnly {
		seed, err := loadDiscoverySeedFile(cfg.seedPath)
		if err != nil {
			return err
		}
		candidates := pipeline.MissingDiscoveryCandidates(catalog, seed)
		discoveryRunner = newDiscoveryRunner(cfg, candidates, extractor, store, logger)
		logger.Info("autodiscovery enabled", "candidates", len(candidates), "batch", cfg.discoveryBatch)
	} else if cfg.marketOnly {
		logger.Info("seed autodiscovery skipped", "reason", "market-only pass")
	} else {
		logger.Info("autodiscovery disabled", "reason", "TinyFish API key is not configured")
	}
	sender, err := newDeliverySender(cfg, logger)
	if err != nil {
		return err
	}
	owner := processOwner()
	runner := pipeline.Runner{
		Sources: sources, Extractor: extractor, Store: store,
		Channel: cfg.deliveryMode, Recipient: cfg.recipient,
		PublishBootstrap: cfg.deliveryMode == "telegram",
	}
	deliveryLimit := 100
	deliveryTimeout := 30 * time.Second
	deliveryInterval := time.Duration(0)
	if cfg.deliveryMode == "telegram" {
		// Telegram channels are limited to about 20 posts per minute. Leave
		// enough headroom for minor scheduling and network jitter.
		deliveryLimit = 300
		deliveryTimeout = 6 * time.Minute
		deliveryInterval = telegramDeliveryInterval
	}
	drainer := pipeline.DeliveryDrainer{
		Store: store, Sender: sender, Owner: owner,
		Channel: cfg.deliveryMode, Recipient: cfg.recipient,
		Limit: deliveryLimit, Lease: 2 * time.Minute, RetryDelay: time.Minute,
		MinInterval: deliveryInterval,
	}
	realtimeDrainer := drainer
	realtimeDrainer.Limit = realtimeDeliveryBatch

	logger.Info("radar ready", "sources", len(runner.Sources), "schema", store.Schema(), "delivery_mode", cfg.deliveryMode)
	for {
		cycleStartedAt := time.Now().UTC()
		leaseCtx, leaseCancel := context.WithTimeout(ctx, 5*time.Second)
		cycleLease, acquired, leaseErr := store.TryAcquireCycle(leaseCtx, owner, cycleStartedAt)
		leaseCancel()
		if leaseErr != nil {
			health.recordCycle(pipeline.RunReport{}, pipeline.DiscoveryReport{}, pipeline.DeliveryReport{}, leaseErr)
			logger.Error("routine cycle ownership failed", "error", leaseErr)
			if cfg.once {
				return leaseErr
			}
			if err := waitForNextCycle(ctx, serverErrors, cfg.interval); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			continue
		}
		if !acquired {
			operational, stateErr := store.ReadOperationalState(ctx)
			if stateErr != nil {
				health.recordCycle(pipeline.RunReport{}, pipeline.DiscoveryReport{}, pipeline.DeliveryReport{}, stateErr)
				logger.Error("routine standby state unavailable", "error", stateErr)
			} else {
				health.recordStandby(operational.Runtime)
				logger.Info("routine cycle skipped", "reason", "another instance owns the schema", "schema", store.Schema())
			}
			if cfg.once {
				return stateErr
			}
			if err := waitForNextCycle(ctx, serverErrors, cfg.interval); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			continue
		}

		// Start draining as soon as this process owns the cycle. This clears
		// replayable backlog during discovery and publishes newly activated rows
		// while later sources are still crawling.
		deliveryPumpCtx, stopDeliveryPump := context.WithCancel(ctx)
		deliveryPumpDone := make(chan deliveryPumpResult, 1)
		go func() {
			pumpReport, pumpErr := runDeliveryPump(deliveryPumpCtx, realtimeDeliveryPoll, realtimeDrainer.Drain)
			deliveryPumpDone <- deliveryPumpResult{report: pumpReport, err: pumpErr}
		}()

		cycleCtx, cycleCancel := context.WithTimeout(ctx, cfg.cycleTimeout)
		var discoveryReport pipeline.DiscoveryReport
		var discoveryErr error
		if cfg.marketOnly {
			// Market-only is a bounded diagnostic pass: it does not spend
			// quota on the fixed company seed or alter unrelated source health.
		} else if discoveryRunner != nil {
			discoveryReport, discoveryErr = discoveryRunner.Run(cycleCtx)
		} else {
			discoveryReport.SourcesDemoted, discoveryErr = store.DemoteUnhealthyDiscoveredSources(cycleCtx, 3, time.Now().UTC())
		}
		promoted, promotedErr := store.ListPromotedSources(cycleCtx)
		if promotedErr != nil {
			discoveryErr = errors.Join(discoveryErr, fmt.Errorf("load promoted discovery sources: %w", promotedErr))
		} else {
			runner.Sources = runtimeSources(baseSources, promoted, marketSources, cfg.marketOnly)
		}
		report, runErr := runner.Run(cycleCtx)
		marketReport, marketErr := (pipeline.MarketSourcePromoter{
			Extractor: productionExtractor,
			Store:     store,
			// Discovery runs before market promotion. Include the refreshed
			// runtime set so a board promoted earlier in this same cycle is not
			// re-created under a market-derived candidate alias.
			KnownSources: runner.Sources,
			Logger:       logger,
		}).Run(cycleCtx, marketObservations.DrainMarketObservations())
		if marketErr == nil && marketReport.SourcesPromoted > 0 {
			promoted, promotedErr := store.ListPromotedSources(cycleCtx)
			if promotedErr != nil {
				marketErr = fmt.Errorf("reload market-promoted discovery sources: %w", promotedErr)
			} else {
				runner.Sources = runtimeSources(baseSources, promoted, marketSources, cfg.marketOnly)
			}
		}
		cycleCancel()
		stopDeliveryPump()
		pumpResult := <-deliveryPumpDone
		deliveryCtx, deliveryCancel := context.WithTimeout(ctx, deliveryTimeout)
		finalDeliveryReport, finalDeliveryErr := drainer.Drain(deliveryCtx)
		deliveryCancel()
		deliveryReport := mergeDeliveryReports(pumpResult.report, finalDeliveryReport)
		deliveryErr := errors.Join(pumpResult.err, finalDeliveryErr)
		cycleErr := errors.Join(discoveryErr, runErr, marketErr, deliveryErr)
		cycleResult := pipeline.CycleResult{
			Status:           cycleResultStatus(report, discoveryReport, deliveryReport, cycleErr),
			SourcesAttempted: report.SourcesAttempted, SourcesSucceeded: report.SourcesSucceeded,
			SourcesFailed: report.SourcesFailed, Observed: report.Observed, Created: report.Created,
			EligibleCreated: report.EligibleCreated, Enqueued: report.Enqueued,
			DeliveriesSent: deliveryReport.Sent, DeliveryFailures: deliveryReport.Failed,
			FinishedAt: time.Now().UTC(),
		}
		if cycleErr != nil {
			cycleResult.LastError = "cycle failed; inspect structured runtime logs"
		}
		finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		finalizeErr := cycleLease.Complete(finalizeCtx, cycleResult)
		finalizeCancel()
		cycleErr = errors.Join(cycleErr, finalizeErr)
		health.recordCycle(report, discoveryReport, deliveryReport, cycleErr)
		logger.Info("routine cycle complete",
			"discovery_attempted", discoveryReport.CandidatesAttempted,
			"discovery_resolved", discoveryReport.SourcesResolved,
			"discovery_probed", discoveryReport.SourcesProbed,
			"discovery_healthy", discoveryReport.SourcesHealthy,
			"discovery_empty", discoveryReport.SourcesEmpty,
			"discovery_rejected", discoveryReport.SourcesRejected,
			"discovery_promoted", discoveryReport.SourcesPromoted,
			"discovery_demoted", discoveryReport.SourcesDemoted,
			"discovery_failed", discoveryReport.CandidatesFailed,
			"market_observations", marketReport.ObservationsSeen,
			"market_companies_discovered", marketReport.CompaniesDiscovered,
			"market_sources_derived", marketReport.SourcesDerived,
			"market_sources_probed", marketReport.SourcesProbed,
			"market_sources_healthy", marketReport.SourcesHealthy,
			"market_sources_empty", marketReport.SourcesEmpty,
			"market_sources_rejected", marketReport.SourcesRejected,
			"market_sources_promoted", marketReport.SourcesPromoted,
			"market_sources_already_monitored", marketReport.SourcesMonitored,
			"monitored_sources", len(runner.Sources),
			"sources_attempted", report.SourcesAttempted,
			"sources_succeeded", report.SourcesSucceeded,
			"sources_failed", report.SourcesFailed,
			"sources_bootstrapped", report.SourcesBootstrapped,
			"observed", report.Observed,
			"created", report.Created,
			"eligible_created", report.EligibleCreated,
			"enqueued", report.Enqueued,
			"deliveries_sent", deliveryReport.Sent,
			"deliveries_failed", deliveryReport.Failed,
			"bootstrapping", report.Bootstrapping,
			"error", cycleErr,
		)
		if cfg.once {
			return cycleErr
		}
		if err := waitForNextCycle(ctx, serverErrors, cfg.interval); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

type deliveryDrainFunc func(context.Context) (pipeline.DeliveryReport, error)

func runDeliveryPump(ctx context.Context, interval time.Duration, drain deliveryDrainFunc) (pipeline.DeliveryReport, error) {
	var total pipeline.DeliveryReport
	if drain == nil {
		return total, errors.New("delivery drain function is required")
	}
	if interval <= 0 {
		return total, errors.New("delivery poll interval must be positive")
	}
	var pumpErr error
	for {
		report, err := drain(ctx)
		total = mergeDeliveryReports(total, report)
		if err != nil && !errors.Is(err, context.Canceled) {
			// Retain evidence that this cycle experienced a drain failure without
			// accumulating one copy of the same outage on every poll.
			pumpErr = err
		}
		if ctx.Err() != nil {
			return total, pumpErr
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return total, pumpErr
		case <-timer.C:
		}
	}
}

func mergeDeliveryReports(left, right pipeline.DeliveryReport) pipeline.DeliveryReport {
	left.Claimed += right.Claimed
	left.Sent += right.Sent
	left.Failed += right.Failed
	left.Errors = append(left.Errors, right.Errors...)
	return left
}

func cycleResultStatus(report pipeline.RunReport, discovery pipeline.DiscoveryReport, delivery pipeline.DeliveryReport, err error) string {
	if err != nil {
		return "failure"
	}
	if report.SourcesFailed > 0 || discovery.CandidatesFailed > 0 || delivery.Failed > 0 {
		return "degraded"
	}
	return "success"
}

func runtimeSources(base, promoted, market []pipeline.Source, marketOnly bool) []pipeline.Source {
	if marketOnly {
		return pipeline.MergeRoutineSources(nil, market)
	}
	return pipeline.MergeRoutineSources(pipeline.MergeRoutineSources(base, promoted), market)
}

func waitForNextCycle(ctx context.Context, serverErrors <-chan error, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case serverErr := <-serverErrors:
		return serverErr
	case <-timer.C:
		return nil
	}
}

func runReconcile(ctx context.Context, cfg config, output io.Writer, logger *slog.Logger) error {
	catalog, err := loadCatalogFile(cfg.catalogPath)
	if err != nil {
		return err
	}
	seed, err := loadDiscoverySeedFile(cfg.seedPath)
	if err != nil {
		return err
	}
	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store, closeStore, err := openStore(startupCtx, cfg, true)
	if err != nil {
		return err
	}
	defer closeStore()
	if _, err := store.SuppressKnownDiscoveredSources(startupCtx, catalog.RoutineSources(), time.Now().UTC()); err != nil {
		return fmt.Errorf("suppress discovered copies of verified sources: %w", err)
	}
	if _, err := store.SuppressDuplicateDiscoveredSources(startupCtx, time.Now().UTC()); err != nil {
		return fmt.Errorf("suppress duplicate discovered sources: %w", err)
	}
	extractor := newLiteExtractor(cfg, logger)
	runner := newDiscoveryRunner(cfg, pipeline.MissingDiscoveryCandidates(catalog, seed), extractor, store, logger)
	report, err := runner.Run(ctx)
	if err != nil {
		return err
	}
	promoted, err := store.ListPromotedSources(ctx)
	if err != nil {
		return err
	}
	logger.Info("autodiscovery reconciliation complete", "attempted", report.CandidatesAttempted, "promoted", report.SourcesPromoted, "monitored_discovered_sources", len(promoted))
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(struct {
		Report          pipeline.DiscoveryReport `json:"report"`
		PromotedSources []pipeline.Source        `json:"promoted_sources"`
	}{Report: report, PromotedSources: promoted})
}

func newDiscoveryRunner(cfg config, candidates []pipeline.DiscoveryCandidate, extractor pipeline.Extractor, store *pipeline.PostgresStore, logger *slog.Logger) *pipeline.DiscoveryRunner {
	client := pipeline.NewRetryingTinyFishDiscoveryClient(tinyfish.Client{
		APIKey: cfg.tinyFishAPIKey, SearchBaseURL: cfg.tinyFishSearchBaseURL,
		FetchBaseURL: cfg.tinyFishFetchBaseURL,
	}, pipeline.DiscoveryClientRetryOptions{
		MaxAttempts: 3,
		Delay:       time.Second,
		MaxDelay:    5 * time.Second,
		OnRetry: func(operation string, nextAttempt int, delay time.Duration, err error) {
			logger.Warn("transient TinyFish discovery request failed; retrying",
				"component", "radar_lite_discovery",
				"event", "tinyfish_request_retry",
				"operation", operation,
				"next_attempt", nextAttempt,
				"delay_ms", delay.Milliseconds(),
				"error", err,
			)
		},
	})
	return &pipeline.DiscoveryRunner{
		Candidates: candidates,
		Client:     client,
		Extractor:  extractor, Store: store, Batch: cfg.discoveryBatch,
		CandidateTimeout: cfg.discoveryTimeout, RetryDelay: cfg.discoveryRetry,
		EmptyRetryDelay: cfg.discoveryEmptyRetry, Logger: logger,
	}
}

func newLiteExtractor(cfg config, logger *slog.Logger) pipeline.Extractor {
	if logger == nil {
		logger = slog.Default()
	}
	structured := pipeline.NewATSExtractor(scraper.ATSOptions{
		Client:                       scraper.NewSafeHTTPClient(atsHTTPTimeout),
		SmartRecruitersDetailMaxJobs: 8,
		// Large Workday boards otherwise spend the entire per-company discovery
		// deadline fetching every description. Summaries preserve job identity;
		// bounded details enrich the most recent rows without blocking coverage.
		WorkdayDetailMaxJobs: 8,
		WorkdayDetailTimeout: 4 * time.Second,
	})
	var search pipeline.Extractor
	if cfg.tinyFishAPIKey != "" {
		client := tinyfish.Client{
			APIKey: cfg.tinyFishAPIKey, SearchBaseURL: cfg.tinyFishSearchBaseURL,
			FetchBaseURL: cfg.tinyFishFetchBaseURL,
		}
		search = pipeline.NewScraperExtractorAtTier(
			tinyfishextractor.NewTinyFishSearchExtractor(client),
			scraper.TierSearchDiscovery,
		)
	}
	resilient := pipeline.NewRetryingExtractor(
		pipeline.NewDiscoveryAwareExtractor(structured, search),
		pipeline.ExtractionRetryOptions{
			MaxAttempts: 3,
			Delay:       500 * time.Millisecond,
			MaxDelay:    5 * time.Second,
			OnRetry: func(source pipeline.Source, nextAttempt int, err error) {
				logger.Warn("transient source extraction failure; retrying",
					"source_id", source.ID,
					"company", source.Company,
					"provider", source.Provider,
					"next_attempt", nextAttempt,
					"error", err,
				)
			},
		},
	)
	return loggingExtractor{
		inner:  resilient,
		logger: logger,
	}
}

func runServe(ctx context.Context, cfg config, logger *slog.Logger) error {
	catalog, err := loadCatalogFile(cfg.catalogPath)
	if err != nil {
		return err
	}
	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store, closeStore, err := openStore(startupCtx, cfg, false)
	if err != nil {
		return err
	}
	defer closeStore()
	if _, err := store.ListPostings(startupCtx); err != nil {
		return errors.New("open Radar job feed: database query failed")
	}
	if _, err := store.ListSourceStatuses(startupCtx); err != nil {
		return errors.New("open Radar source health: database query failed")
	}

	health := &healthState{}
	health.setRuntimeReader(store, true)
	operational, err := store.ReadOperationalState(startupCtx)
	if err != nil {
		return errors.New("open Radar operational state: database query failed")
	}
	health.recordReadOnly(operational.Runtime)
	// Market-search controls are durable source-status rows even though a
	// read-only process does not need a TinyFish key. Include their static
	// metadata so status counts and labels stay consistent across topologies.
	baseSources := pipeline.MergeRoutineSources(catalog.RoutineSources(), pipeline.MarketSearchSources())
	totalSources := len(baseSources) + operational.DiscoveredCounts["promoted"]
	// A read-only web process intentionally has no TinyFish key, but it still
	// represents the last routine owner's persisted market-search sources.
	// Source status is the authoritative active-set floor in that topology.
	if observedSources := len(operational.RoutineSourceStatus); observedSources > totalSources {
		totalSources = observedSources
	}
	server, serverErrors := startWebServer(cfg.healthAddress, health, store, dashboard.Config{
		BaseSources: baseSources, TotalSources: totalSources,
		LogoDomains: dashboard.LoadCompanyLogoDomains(cfg.seedPath),
		RuntimeMode: cfg.mode, CycleTimeout: cfg.cycleTimeout, DeliveryMode: cfg.deliveryMode,
		TelegramTokenPresent: cfg.telegramToken != "", TelegramChatPresent: cfg.telegramChat != "",
		PublishingEnabled: cfg.publishingEnabled,
	}, logger)
	if server == nil {
		return errors.New("serve mode requires RADAR_LITE_HEALTH_ADDR to be enabled")
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	logger.Info("radar UI ready", "address", cfg.healthAddress, "schema", store.Schema())
	select {
	case <-ctx.Done():
		return nil
	case serverErr := <-serverErrors:
		return serverErr
	}
}

func openStore(ctx context.Context, cfg config, ensureSchema bool) (*pipeline.PostgresStore, func(), error) {
	db, err := postgres.OpenPostgres(ctx, cfg.databaseURL, postgres.Options{
		MaxOpenConns: 4, MaxIdleConns: 1, ConnMaxLifetime: 30 * time.Minute,
	})
	if err != nil {
		return nil, func() {}, errors.New("connect to Radar Postgres: database configuration or connectivity check failed")
	}
	closeStore := func() { _ = db.Close() }
	store, err := pipeline.NewPostgresStore(db, pipeline.PostgresOptions{Schema: cfg.schema})
	if err != nil {
		closeStore()
		return nil, func() {}, errors.New("initialize Radar Postgres store: configuration is invalid")
	}
	if ensureSchema {
		if err := store.EnsureSchema(ctx); err != nil {
			closeStore()
			return nil, func() {}, errors.New("initialize Radar Postgres schema: migration failed")
		}
	}
	return store, closeStore, nil
}

func loadCatalogFile(path string) (pipeline.Catalog, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return pipeline.Catalog{}, fmt.Errorf("open verified catalog: %w", err)
	}
	defer file.Close()
	return pipeline.LoadCatalog(file)
}

func loadDiscoverySeedFile(path string) (pipeline.DiscoverySeed, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return pipeline.DiscoverySeed{}, fmt.Errorf("open discovery seed: %w", err)
	}
	defer file.Close()
	return pipeline.LoadDiscoverySeed(file)
}

type logSender struct{ logger *slog.Logger }

func (s logSender) Send(ctx context.Context, delivery pipeline.Delivery) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var posting pipeline.Posting
	if err := json.Unmarshal(delivery.Payload, &posting); err != nil {
		return fmt.Errorf("decode delivery payload: %w", err)
	}
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.InfoContext(ctx, "job preview",
		"delivery_id", delivery.ID,
		"job_id", delivery.JobID,
		"company", posting.Company,
		"title", posting.Title,
		"location", posting.Location,
		"apply_url", posting.ApplyURL,
	)
	return nil
}

type outboxSender struct {
	outbox        delivery.Outbox
	presentations map[string]dashboard.CompanyPresentation
}

func (s outboxSender) Send(ctx context.Context, delivery pipeline.Delivery) error {
	if s.outbox == nil {
		return errors.New("notification outbox is required")
	}
	posting, err := decodePosting(delivery)
	if err != nil {
		return err
	}
	return s.outbox.Enqueue(ctx, postingMessage(delivery, posting, s.presentations))
}

func newDeliverySender(cfg config, logger *slog.Logger) (pipeline.Sender, error) {
	switch cfg.deliveryMode {
	case "log":
		return logSender{logger: logger}, nil
	case "telegram":
		if cfg.telegramToken == "" || cfg.telegramChat == "" {
			return nil, errors.New("telegram credentials are required")
		}
		client := delivery.NewWebhookHTTPClient(10 * time.Second)
		return outboxSender{
			outbox:        delivery.NewTelegramOutbox(cfg.telegramToken, cfg.telegramChat, client),
			presentations: dashboard.LoadCompanyPresentations(cfg.seedPath),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported delivery mode %q", cfg.deliveryMode)
	}
}

func decodePosting(item pipeline.Delivery) (pipeline.Posting, error) {
	var posting pipeline.Posting
	if err := json.Unmarshal(item.Payload, &posting); err != nil {
		return pipeline.Posting{}, fmt.Errorf("decode delivery payload: %w", err)
	}
	return posting, nil
}

func postingMessage(item pipeline.Delivery, posting pipeline.Posting, presentations map[string]dashboard.CompanyPresentation) delivery.Message {
	location := strings.TrimSpace(posting.Location)
	if location == "" {
		location = "Location not stated"
	}
	return delivery.Message{
		ID:        fmt.Sprintf("lite-%d", item.ID),
		Channel:   "telegram",
		Recipient: item.Recipient,
		Subject:   strings.TrimSpace(posting.Title),
		Body:      fmt.Sprintf("%s\n📍 %s\n%s", strings.TrimSpace(posting.Company), location, strings.TrimSpace(posting.ApplyURL)),
		DedupeKey: item.JobID,
		Metadata: map[string]string{
			"company":         strings.TrimSpace(posting.Company),
			"company_type":    dashboard.CompanyPresentationLabel(posting.Company, presentations),
			"title":           strings.TrimSpace(posting.Title),
			"track":           dashboard.PostingTrackLabel(posting),
			"category":        dashboard.PostingCategoryLabel(posting),
			"location":        location,
			"location_marker": dashboard.PostingLocationMarker(posting.Country, posting.Location),
			"apply_url":       strings.TrimSpace(posting.ApplyURL),
		},
		CreatedAt: posting.FirstSeenAt,
	}
}

type healthState struct {
	mu               sync.RWMutex
	runtimeReader    operationalStateReader
	readOnly         bool
	ready            bool
	degraded         bool
	lastCycleAt      time.Time
	lastCycleFail    bool
	sourcesSucceeded int
	sourcesFailed    int
	deliveryFailures int
}

type operationalStateReader interface {
	ReadOperationalState(context.Context) (pipeline.OperationalState, error)
}

func (s *healthState) recordCycle(report pipeline.RunReport, discovery pipeline.DiscoveryReport, delivery pipeline.DeliveryReport, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastCycleAt = time.Now().UTC()
	s.lastCycleFail = err != nil
	s.ready = err == nil
	s.degraded = err == nil && (report.SourcesFailed > 0 || discovery.CandidatesFailed > 0 || delivery.Failed > 0)
	s.sourcesSucceeded = report.SourcesSucceeded
	s.sourcesFailed = report.SourcesFailed
	s.deliveryFailures = delivery.Failed
}

func (s *healthState) recordStandby(runtime *pipeline.RuntimeState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Ownership proves only that a worker is attempting a cycle. Readiness
	// requires at least one completed usable cycle; otherwise a brand-new
	// deployment could report ready while its first extraction is still in
	// flight (or about to fail).
	s.ready = runtime != nil && (runtime.LastCycleState == "success" || runtime.LastCycleState == "degraded")
	s.degraded = runtime != nil && runtime.LastCycleState == "degraded"
	s.lastCycleFail = runtime != nil && runtime.LastCycleState == "failure"
	if runtime == nil {
		return
	}
	if runtime.LastCycleFinished != nil {
		s.lastCycleAt = *runtime.LastCycleFinished
	}
	s.sourcesSucceeded = runtime.SourcesSucceeded
	s.sourcesFailed = runtime.SourcesFailed
	s.deliveryFailures = runtime.DeliveryFailures
}

func (s *healthState) recordReadOnly(runtime *pipeline.RuntimeState) {
	s.recordStandby(runtime)
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
}

func (s *healthState) setRuntimeReader(reader operationalStateReader, readOnly bool) {
	s.mu.Lock()
	s.runtimeReader = reader
	s.readOnly = readOnly
	s.mu.Unlock()
}

func (s *healthState) refreshRuntime(ctx context.Context) error {
	s.mu.RLock()
	reader, readOnly := s.runtimeReader, s.readOnly
	s.mu.RUnlock()
	if reader == nil {
		return nil
	}
	operational, err := reader.ReadOperationalState(ctx)
	if err != nil {
		return err
	}
	if readOnly {
		s.recordReadOnly(operational.Runtime)
	} else {
		s.recordStandby(operational.Runtime)
	}
	return nil
}

func (s *healthState) Snapshot() dashboard.HealthSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return dashboard.HealthSnapshot{
		Ready: s.ready, Degraded: s.degraded, LastCycleAt: s.lastCycleAt,
		LastCycleError: s.lastCycleFail, SourcesSucceeded: s.sourcesSucceeded,
		SourcesFailed: s.sourcesFailed, DeliveryFailures: s.deliveryFailures,
	}
}

func (s *healthState) handler() http.Handler {
	mux := http.NewServeMux()
	s.registerHealthRoutes(mux)
	return mux
}

func (s *healthState) registerHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`+"\n")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
		defer cancel()
		if err := s.refreshRuntime(ctx); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ready": false, "degraded": true, "state_error": true,
			})
			return
		}
		current := s.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		if !current.Ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":             current.Ready,
			"degraded":          current.Degraded,
			"last_cycle_at":     current.LastCycleAt,
			"last_cycle_error":  current.LastCycleError,
			"sources_succeeded": current.SourcesSucceeded,
			"sources_failed":    current.SourcesFailed,
			"delivery_failures": current.DeliveryFailures,
		})
	})
}

func newServerHandler(state *healthState, store dashboard.Store, cfg dashboard.Config, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	state.registerHealthRoutes(mux)
	dashboard.Register(mux, store, cfg, state, logger)
	return mux
}

func startWebServer(address string, state *healthState, store dashboard.Store, cfg dashboard.Config, logger *slog.Logger) (*http.Server, <-chan error) {
	errorsChannel := make(chan error, 1)
	if strings.TrimSpace(address) == "" || strings.TrimSpace(address) == "-" {
		return nil, errorsChannel
	}
	server := &http.Server{
		Addr: address, Handler: newServerHandler(state, store, cfg, logger),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("radar web server listening", "address", address)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errorsChannel <- fmt.Errorf("web server: %w", err)
		}
	}()
	return server, errorsChannel
}

func envOr(getenv lookupEnv, key, fallback string) string {
	if value, ok := getenv(key); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func firstEnv(getenv lookupEnv, keys ...string) string {
	for _, key := range keys {
		if value, ok := getenv(key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func durationEnv(getenv lookupEnv, key string, fallback time.Duration) (time.Duration, error) {
	raw := envOr(getenv, key, fallback.String())
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return duration, nil
}

func integerEnv(getenv lookupEnv, key string, fallback, minimum, maximum int) (int, error) {
	raw := envOr(getenv, key, strconv.Itoa(fallback))
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", key, minimum, maximum)
	}
	return value, nil
}

func processOwner() string {
	hostname, _ := os.Hostname()
	return fmt.Sprintf("%s-%d", hostname, os.Getpid())
}
