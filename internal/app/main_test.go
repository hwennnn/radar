package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hwennnn/radar/internal/delivery"
	"github.com/hwennnn/radar/internal/pipeline"
)

func TestLoadConfigRoutineUsesLiteDatabaseBeforeFallback(t *testing.T) {
	environment := map[string]string{
		"RADAR_LITE_DATABASE_URL":  "postgres://lite",
		"DATABASE_URL":             "postgres://legacy",
		"RADAR_LITE_INTERVAL":      "20m",
		"RADAR_LITE_CYCLE_TIMEOUT": "7m",
	}
	cfg, err := loadConfig(nil, mapLookup(environment))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.databaseURL != "postgres://lite" || cfg.interval != 20*time.Minute || cfg.cycleTimeout != 7*time.Minute {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadConfigRoutineEnablesAutodiscoveryWhenTinyFishIsConfigured(t *testing.T) {
	cfg, err := loadConfig(nil, mapLookup(map[string]string{
		"DATABASE_URL":                        "postgres://example",
		"TINYFISH_API_KEY":                    "tinyfish-secret",
		"RADAR_LITE_DISCOVERY_BATCH":          "7",
		"RADAR_LITE_DISCOVERY_TIMEOUT":        "12s",
		"RADAR_LITE_DISCOVERY_RETRY":          "2h",
		"RADAR_LITE_DISCOVERY_EMPTY_RETRY":    "30m",
		"RADAR_LITE_TINYFISH_SEARCH_BASE_URL": "https://search.example.test",
		"RADAR_LITE_TINYFISH_FETCH_BASE_URL":  "https://fetch.example.test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.tinyFishAPIKey != "tinyfish-secret" || cfg.discoveryBatch != 7 || cfg.discoveryTimeout != 12*time.Second ||
		cfg.discoveryRetry != 2*time.Hour || cfg.discoveryEmptyRetry != 30*time.Minute ||
		cfg.tinyFishSearchBaseURL != "https://search.example.test" || cfg.tinyFishFetchBaseURL != "https://fetch.example.test" {
		t.Fatalf("unexpected discovery config: %+v", cfg)
	}
}

func TestLoadConfigRoutineUsesBoundedEightCompanyDiscoveryDefault(t *testing.T) {
	cfg, err := loadConfig(nil, mapLookup(map[string]string{"DATABASE_URL": "postgres://example"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.discoveryBatch != 16 {
		t.Fatalf("discovery batch = %d, want 16", cfg.discoveryBatch)
	}
}

func TestLoadConfigReconcileRequiresTinyFishButNeverPublishing(t *testing.T) {
	_, err := loadConfig([]string{"reconcile"}, mapLookup(map[string]string{"DATABASE_URL": "postgres://example"}))
	if err == nil || !strings.Contains(err.Error(), "TINYFISH_API_KEY") {
		t.Fatalf("expected TinyFish requirement, got %v", err)
	}
	cfg, err := loadConfig([]string{"reconcile"}, mapLookup(map[string]string{
		"DATABASE_URL":             "postgres://example",
		"TINYFISH_API_KEY":         "tinyfish-secret",
		"RADAR_LITE_DELIVERY_MODE": "log",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.mode != "reconcile" || cfg.deliveryMode != "log" || cfg.telegramToken != "" {
		t.Fatalf("unexpected reconcile config: %+v", cfg)
	}
}

func TestLoadConfigMarketOnceRequiresTinyFishAndStaysLogOnly(t *testing.T) {
	_, err := loadConfig([]string{"market-once"}, mapLookup(map[string]string{"DATABASE_URL": "postgres://example"}))
	if err == nil || !strings.Contains(err.Error(), "TINYFISH_API_KEY") {
		t.Fatalf("expected TinyFish requirement, got %v", err)
	}
	cfg, err := loadConfig([]string{"market-once"}, mapLookup(map[string]string{
		"DATABASE_URL":     "postgres://example",
		"TINYFISH_API_KEY": "tinyfish-secret",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.mode != "market" || !cfg.marketOnly || !cfg.once || cfg.deliveryMode != "log" {
		t.Fatalf("unexpected market-once config: %+v", cfg)
	}
}

func TestLoadConfigRejectsInvalidDiscoveryBatch(t *testing.T) {
	_, err := loadConfig(nil, mapLookup(map[string]string{
		"DATABASE_URL":               "postgres://example",
		"RADAR_LITE_DISCOVERY_BATCH": "0",
	}))
	if err == nil || !strings.Contains(err.Error(), "RADAR_LITE_DISCOVERY_BATCH") {
		t.Fatalf("expected invalid discovery batch, got %v", err)
	}
}

func TestLoadConfigTelegramRequiresBothCredentials(t *testing.T) {
	_, err := loadConfig(nil, mapLookup(map[string]string{
		"DATABASE_URL":             "postgres://example",
		"RADAR_LITE_DELIVERY_MODE": "telegram",
		"RADAR_TELEGRAM_BOT_TOKEN": "secret-token",
	}))
	if err == nil || !strings.Contains(err.Error(), "requires both") {
		t.Fatalf("expected missing credential rejection, got %v", err)
	}
}

func TestLoadConfigTelegramUsesLiteAliasesAndChatAsRecipient(t *testing.T) {
	cfg, err := loadConfig(nil, mapLookup(map[string]string{
		"DATABASE_URL":                  "postgres://example",
		"RADAR_LITE_DELIVERY_MODE":      "telegram",
		"RADAR_LITE_PUBLISHING_ENABLED": "true",
		"RADAR_LITE_TELEGRAM_BOT_TOKEN": "secret-token",
		"RADAR_LITE_TELEGRAM_CHAT_ID":   "chat-123",
		"RADAR_LITE_RECIPIENT":          "ignored-preview-recipient",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.telegramToken != "secret-token" || cfg.telegramChat != "chat-123" || cfg.recipient != "chat-123" {
		t.Fatalf("unexpected telegram config: recipient=%q token_present=%t chat=%q", cfg.recipient, cfg.telegramToken != "", cfg.telegramChat)
	}
}

func TestLoadConfigTelegramRequiresExactPublishingEnablement(t *testing.T) {
	for name, value := range map[string]*string{
		"missing": nil,
		"false":   stringPointer("false"),
		"invalid": stringPointer("TRUE"),
	} {
		t.Run(name, func(t *testing.T) {
			environment := map[string]string{
				"DATABASE_URL":                  "postgres://example",
				"RADAR_LITE_DELIVERY_MODE":      "telegram",
				"RADAR_LITE_TELEGRAM_BOT_TOKEN": "secret-token",
				"RADAR_LITE_TELEGRAM_CHAT_ID":   "chat-123",
			}
			if value != nil {
				environment["RADAR_LITE_PUBLISHING_ENABLED"] = *value
			}
			_, err := loadConfig(nil, mapLookup(environment))
			if err == nil || !strings.Contains(err.Error(), "RADAR_LITE_PUBLISHING_ENABLED") {
				t.Fatalf("expected publishing gate rejection, got %v", err)
			}
		})
	}
}

func TestLoadConfigDrainRequiresExplicitTelegramPublishing(t *testing.T) {
	cfg, err := loadConfig([]string{"drain"}, mapLookup(map[string]string{
		"DATABASE_URL":                  "postgres://example",
		"RADAR_LITE_DELIVERY_MODE":      "telegram",
		"RADAR_LITE_PUBLISHING_ENABLED": "true",
		"RADAR_LITE_TELEGRAM_BOT_TOKEN": "secret-token",
		"RADAR_LITE_TELEGRAM_CHAT_ID":   "@earlycareerradar",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.mode != "drain" || cfg.recipient != "@earlycareerradar" || !cfg.publishingEnabled {
		t.Fatalf("unexpected drain config: %+v", cfg)
	}
}

func TestRunRoutineRequiresDatabaseBeforeCatalogOrProvider(t *testing.T) {
	err := run(context.Background(), []string{"once"}, mapLookup(map[string]string{
		"RADAR_LITE_CATALOG": "/path/that/must/not/be/opened.json",
	}), &bytes.Buffer{}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected database requirement before runtime construction, got %v", err)
	}
}

func TestRunRoutineSanitizesMalformedDatabaseURL(t *testing.T) {
	directory := t.TempDir()
	catalogPath := filepath.Join(directory, "catalog.json")
	writeTestFile(t, catalogPath, `{"companies":[{"id":"known","name":"Known","sources":[{"id":"known-jobs","provider":"greenhouse","url":"https://example.com/jobs"}]}]}`)
	databaseURL := "postgres://dummy-user:dummy-secret-marker@%zz"
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	err := runRoutine(context.Background(), config{
		catalogPath:  catalogPath,
		databaseURL:  databaseURL,
		deliveryMode: "log",
		recipient:    "local-preview",
	}, logger)
	if err == nil {
		t.Fatal("expected malformed database URL to fail")
	}
	logger.Error("radar stopped", "error", err)
	loggable := err.Error() + "\n" + logs.String()
	for _, secret := range []string{databaseURL, "dummy-user", "dummy-secret-marker"} {
		if strings.Contains(loggable, secret) {
			t.Fatalf("database startup error leaked %q: %s", secret, loggable)
		}
	}
	if !strings.Contains(err.Error(), "database configuration or connectivity check failed") {
		t.Fatalf("database startup error is not actionable: %v", err)
	}
}

func TestDiscoverNeedsNoDatabaseAndOnlyPrintsMissingCandidates(t *testing.T) {
	directory := t.TempDir()
	catalogPath := filepath.Join(directory, "catalog.json")
	seedPath := filepath.Join(directory, "seed.json")
	writeTestFile(t, catalogPath, `{"companies":[{"id":"known","name":"Known","sources":[{"id":"known-jobs","provider":"greenhouse","url":"https://example.com/jobs"}]}]}`)
	writeTestFile(t, seedPath, `{"candidates":[{"id":"known","name":"Known"},{"id":"missing","name":"Missing","tags":["ai"]}]}`)

	var output bytes.Buffer
	err := run(context.Background(), []string{"discover"}, mapLookup(map[string]string{
		"RADAR_LITE_CATALOG":        catalogPath,
		"RADAR_LITE_DISCOVERY_SEED": seedPath,
	}), &output, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Missing []pipeline.DiscoveryCandidate `json:"missing_candidates"`
		Count   int                           `json:"count"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Count != 1 || len(result.Missing) != 1 || result.Missing[0].ID != "missing" {
		t.Fatalf("unexpected discovery result: %+v", result)
	}
}

func TestAuditNeedsNoDatabaseAndFailsShallowUniverse(t *testing.T) {
	directory := t.TempDir()
	catalogPath := filepath.Join(directory, "catalog.json")
	seedPath := filepath.Join(directory, "seed.json")
	writeTestFile(t, catalogPath, `{"companies":[{"id":"known","name":"Known","sources":[{"id":"known-jobs","provider":"greenhouse","url":"https://example.com/jobs"}]}]}`)
	writeTestFile(t, seedPath, `{"candidates":[{"id":"missing","name":"Missing","tags":["priority-1","ai","curated-2026"]}]}`)

	var output bytes.Buffer
	err := run(context.Background(), []string{"audit"}, mapLookup(map[string]string{
		"RADAR_LITE_CATALOG":        catalogPath,
		"RADAR_LITE_DISCOVERY_SEED": seedPath,
	}), &output, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err == nil || !strings.Contains(err.Error(), "coverage audit failed") {
		t.Fatalf("expected coverage failure, got %v", err)
	}
	if !strings.Contains(output.String(), `"pass": false`) {
		t.Fatalf("audit output omitted machine-readable failure: %s", output.String())
	}
}

func TestHealthHandlerKeepsLivenessIndependentOfCycleFailure(t *testing.T) {
	state := &healthState{}
	state.recordCycle(pipeline.RunReport{}, pipeline.DiscoveryReport{}, pipeline.DeliveryReport{}, errors.New("cycle failed"))
	response := httptest.NewRecorder()
	state.handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected liveness response: %d %s", response.Code, response.Body.String())
	}
}

func TestReadOnlyReadinessRefreshesDurableRuntimeOnEveryRequest(t *testing.T) {
	oldFinished := time.Date(2026, 8, 17, 1, 0, 0, 0, time.UTC)
	newFinished := oldFinished.Add(2 * time.Hour)
	state := &healthState{}
	state.recordReadOnly(&pipeline.RuntimeState{
		LastCycleState: "success", LastCycleFinished: &oldFinished,
		SourcesSucceeded: 81,
	})
	state.setRuntimeReader(operationalReaderFunc(func(context.Context) (pipeline.OperationalState, error) {
		return pipeline.OperationalState{Runtime: &pipeline.RuntimeState{
			LastCycleState: "degraded", LastCycleFinished: &newFinished,
			SourcesSucceeded: 92, SourcesFailed: 0,
		}}, nil
	}), true)

	response := httptest.NewRecorder()
	state.handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Degraded         bool      `json:"degraded"`
		LastCycleAt      time.Time `json:"last_cycle_at"`
		SourcesSucceeded int       `json:"sources_succeeded"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Degraded || !payload.LastCycleAt.Equal(newFinished) || payload.SourcesSucceeded != 92 {
		t.Fatalf("stale readiness payload: %+v", payload)
	}
}

func TestReadinessFailsClosedWhenDurableRuntimeCannotBeRead(t *testing.T) {
	state := &healthState{}
	state.recordReadOnly(nil)
	state.setRuntimeReader(operationalReaderFunc(func(context.Context) (pipeline.OperationalState, error) {
		return pipeline.OperationalState{}, errors.New("database unavailable")
	}), true)
	response := httptest.NewRecorder()
	state.handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"state_error":true`) {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestRoutineReadinessStaysUnavailableDuringFirstOwnedCycle(t *testing.T) {
	started := time.Date(2026, 8, 18, 1, 0, 0, 0, time.UTC)
	state := &healthState{}
	state.setRuntimeReader(operationalReaderFunc(func(context.Context) (pipeline.OperationalState, error) {
		return pipeline.OperationalState{Runtime: &pipeline.RuntimeState{
			ActiveOwner: "worker-first-cycle", ActiveStartedAt: &started,
			LastCycleState: "pending",
		}}, nil
	}), false)

	response := httptest.NewRecorder()
	state.handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"ready":false`) {
		t.Fatalf("first-cycle readiness must be false: %s", response.Body.String())
	}
}

type operationalReaderFunc func(context.Context) (pipeline.OperationalState, error)

func (f operationalReaderFunc) ReadOperationalState(ctx context.Context) (pipeline.OperationalState, error) {
	return f(ctx)
}

func TestCycleResultStatusDistinguishesManagedDegradation(t *testing.T) {
	if got := cycleResultStatus(pipeline.RunReport{}, pipeline.DiscoveryReport{}, pipeline.DeliveryReport{}, errors.New("database failed")); got != "failure" {
		t.Fatalf("fatal cycle status = %q", got)
	}
	if got := cycleResultStatus(pipeline.RunReport{SourcesFailed: 1}, pipeline.DiscoveryReport{}, pipeline.DeliveryReport{}, nil); got != "degraded" {
		t.Fatalf("partial source status = %q", got)
	}
	if got := cycleResultStatus(pipeline.RunReport{}, pipeline.DiscoveryReport{CandidatesFailed: 1}, pipeline.DeliveryReport{}, nil); got != "degraded" {
		t.Fatalf("discovery status = %q", got)
	}
	if got := cycleResultStatus(pipeline.RunReport{}, pipeline.DiscoveryReport{}, pipeline.DeliveryReport{Failed: 1}, nil); got != "degraded" {
		t.Fatalf("delivery status = %q", got)
	}
	if got := cycleResultStatus(pipeline.RunReport{SourcesSucceeded: 70}, pipeline.DiscoveryReport{}, pipeline.DeliveryReport{}, nil); got != "success" {
		t.Fatalf("healthy status = %q", got)
	}
}

func TestDeliveryPumpDrainsAgainWithoutWaitingForTheCrawlToFinish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	report, err := runDeliveryPump(ctx, time.Millisecond, func(context.Context) (pipeline.DeliveryReport, error) {
		calls++
		if calls == 1 {
			return pipeline.DeliveryReport{}, nil
		}
		cancel()
		return pipeline.DeliveryReport{Claimed: 1, Sent: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || report.Claimed != 1 || report.Sent != 1 {
		t.Fatalf("pump calls=%d report=%#v, want a second live drain with one send", calls, report)
	}
}

func TestDeliveryPumpKeepsPollingAfterDrainFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	report, err := runDeliveryPump(ctx, time.Millisecond, func(context.Context) (pipeline.DeliveryReport, error) {
		calls++
		if calls == 1 {
			return pipeline.DeliveryReport{Failed: 1}, errors.New("temporary database failure")
		}
		cancel()
		return pipeline.DeliveryReport{Claimed: 1, Sent: 1}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "temporary database failure") {
		t.Fatalf("pump error=%v, want retained temporary failure", err)
	}
	if calls != 2 || report.Failed != 1 || report.Sent != 1 {
		t.Fatalf("pump calls=%d report=%#v, want failure isolation and later send", calls, report)
	}
}

func TestDeliveryPumpRejectsInvalidConfiguration(t *testing.T) {
	if _, err := runDeliveryPump(context.Background(), time.Second, nil); err == nil {
		t.Fatal("nil drain function must fail")
	}
	if _, err := runDeliveryPump(context.Background(), 0, func(context.Context) (pipeline.DeliveryReport, error) {
		return pipeline.DeliveryReport{}, nil
	}); err == nil {
		t.Fatal("non-positive poll interval must fail")
	}
}

func TestHealthHandlerReadinessTracksLastCompletedCycle(t *testing.T) {
	state := &healthState{}
	assertReadiness(t, state, http.StatusServiceUnavailable, readinessExpectation{})

	state.recordCycle(pipeline.RunReport{SourcesSucceeded: 48}, pipeline.DiscoveryReport{}, pipeline.DeliveryReport{}, nil)
	assertReadiness(t, state, http.StatusOK, readinessExpectation{ready: true, sourcesSucceeded: 48})

	state.recordCycle(pipeline.RunReport{SourcesSucceeded: 3}, pipeline.DiscoveryReport{}, pipeline.DeliveryReport{}, errors.New("database failed"))
	assertReadiness(t, state, http.StatusServiceUnavailable, readinessExpectation{cycleError: true, sourcesSucceeded: 3})
}

func TestHealthHandlerReportsManagedFailuresAsDegradedAndReady(t *testing.T) {
	state := &healthState{}
	state.recordCycle(
		pipeline.RunReport{SourcesSucceeded: 45, SourcesFailed: 3},
		pipeline.DiscoveryReport{},
		pipeline.DeliveryReport{Failed: 2},
		nil,
	)
	assertReadiness(t, state, http.StatusOK, readinessExpectation{
		ready: true, degraded: true, sourcesSucceeded: 45, sourcesFailed: 3, deliveryFailures: 2,
	})
}

func TestHealthHandlerReportsDiscoveryOnlyFailureAsDegradedAndReady(t *testing.T) {
	state := &healthState{}
	state.recordCycle(
		pipeline.RunReport{SourcesSucceeded: 70},
		pipeline.DiscoveryReport{CandidatesAttempted: 4, CandidatesFailed: 1},
		pipeline.DeliveryReport{},
		nil,
	)
	assertReadiness(t, state, http.StatusOK, readinessExpectation{
		ready: true, degraded: true, sourcesSucceeded: 70,
	})
}

func TestLogSenderRejectsInvalidPayload(t *testing.T) {
	err := (logSender{logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))}).Send(
		context.Background(), pipeline.Delivery{Payload: json.RawMessage(`{`)},
	)
	if err == nil {
		t.Fatal("expected invalid payload error")
	}
}

func TestOutboxSenderMapsPostingWithoutNetwork(t *testing.T) {
	fake := &fakeOutbox{}
	posting := pipeline.Posting{
		Company: "Example AI", Title: "Software Engineer Intern", Location: "Singapore",
		Country: "Singapore", EmploymentType: "Internship", ApplyURL: "https://example.com/apply",
		FirstSeenAt: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
	}
	payload, err := json.Marshal(posting)
	if err != nil {
		t.Fatal(err)
	}
	delivery := pipeline.Delivery{ID: 7, JobID: "job-7", Channel: "telegram", Recipient: "chat-7", Payload: payload}
	if err := (outboxSender{outbox: fake}).Send(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 || fake.message.Subject != posting.Title || fake.message.Recipient != "chat-7" {
		t.Fatalf("unexpected message: calls=%d message=%+v", fake.calls, fake.message)
	}
	if fake.message.Metadata["company"] != posting.Company || fake.message.Metadata["location"] != posting.Location || fake.message.Metadata["apply_url"] != posting.ApplyURL {
		t.Fatalf("posting metadata not mapped: %+v", fake.message.Metadata)
	}
	if fake.message.Metadata["track"] != "Internship" || fake.message.Metadata["category"] != "Software" || fake.message.Metadata["location_marker"] != "🇸🇬" {
		t.Fatalf("posting presentation metadata not mapped: %+v", fake.message.Metadata)
	}
}

func TestDiscoverIgnoresTelegramEnvironment(t *testing.T) {
	directory := t.TempDir()
	catalogPath := filepath.Join(directory, "catalog.json")
	seedPath := filepath.Join(directory, "seed.json")
	writeTestFile(t, catalogPath, `{"companies":[{"id":"known","name":"Known","sources":[{"id":"known-jobs","provider":"greenhouse","url":"https://example.com/jobs"}]}]}`)
	writeTestFile(t, seedPath, `{"candidates":[]}`)

	var output bytes.Buffer
	err := run(context.Background(), []string{"discover"}, mapLookup(map[string]string{
		"RADAR_LITE_CATALOG":            catalogPath,
		"RADAR_LITE_DISCOVERY_SEED":     seedPath,
		"RADAR_LITE_DELIVERY_MODE":      "telegram",
		"RADAR_LITE_PUBLISHING_ENABLED": "not-true",
	}), &output, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunRoutesTelegramCheckWithoutDatabaseConfiguration(t *testing.T) {
	err := run(context.Background(), []string{"telegram-check"}, mapLookup(nil), &strings.Builder{}, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "TELEGRAM_BOT_TOKEN is required") {
		t.Fatalf("run() error = %v, want Telegram check configuration error", err)
	}
}

type fakeOutbox struct {
	calls   int
	message delivery.Message
}

func (f *fakeOutbox) Enqueue(_ context.Context, message delivery.Message) error {
	f.calls++
	f.message = message
	return nil
}

func mapLookup(values map[string]string) lookupEnv {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func stringPointer(value string) *string {
	return &value
}

type readinessExpectation struct {
	ready            bool
	degraded         bool
	cycleError       bool
	sourcesSucceeded int
	sourcesFailed    int
	deliveryFailures int
}

func assertReadiness(t *testing.T, state *healthState, wantStatus int, want readinessExpectation) {
	t.Helper()
	response := httptest.NewRecorder()
	state.handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != wantStatus {
		t.Fatalf("unexpected readiness status: got %d want %d; body=%s", response.Code, wantStatus, response.Body.String())
	}
	var body struct {
		Ready            bool      `json:"ready"`
		Degraded         bool      `json:"degraded"`
		LastCycleAt      time.Time `json:"last_cycle_at"`
		LastCycleError   bool      `json:"last_cycle_error"`
		SourcesSucceeded int       `json:"sources_succeeded"`
		SourcesFailed    int       `json:"sources_failed"`
		DeliveryFailures int       `json:"delivery_failures"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Ready != want.ready || body.Degraded != want.degraded || body.LastCycleError != want.cycleError ||
		body.SourcesSucceeded != want.sourcesSucceeded || body.SourcesFailed != want.sourcesFailed ||
		body.DeliveryFailures != want.deliveryFailures {
		t.Fatalf("unexpected readiness body: %+v", body)
	}
	if wantStatus == http.StatusServiceUnavailable && !want.cycleError && !body.LastCycleAt.IsZero() {
		t.Fatalf("readiness before first cycle must have a zero last-cycle timestamp: %+v", body)
	}
	if (wantStatus == http.StatusOK || want.cycleError) && body.LastCycleAt.IsZero() {
		t.Fatalf("completed cycle must include its timestamp: %+v", body)
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
