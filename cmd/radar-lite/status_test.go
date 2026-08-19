package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hwennnn/radar/internal/lite"
)

func TestStatusHandlerReportsDurableStateAndLockedTelegram(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	store := fakeFeedStore{operational: lite.OperationalState{
		GeneratedAt: now, CanonicalJobs: 9000, IdentityAliases: 9100,
		SourceObservations: 9250, MultiSourceJobs: 42,
		DeliveryCounts:   map[string]int{"sent": 12, "suppressed": 210, "pending": 3, "staged": 1},
		CandidateCounts:  map[string]int{"promoted": 21, "pending": 8, "retry": 2},
		DiscoveredCounts: map[string]int{"promoted": 21, "candidate": 4, "unhealthy": 1},
		PromotedSources:  []lite.Source{{ID: "auto-ai", Company: "Auto AI", Provider: "ashby"}},
		RoutineSourceStatus: []lite.SourceStatus{
			{SourceID: "verified", State: "success", ObservedCount: 4, LastAttemptAt: now},
			{SourceID: "auto-ai", State: "failure", ConsecutiveFailures: 2, LastAttemptAt: now, LastError: "request failed: https://user:secret@example.test returned 429"},
		},
	}}
	response := httptest.NewRecorder()
	(statusServer{store: store, health: &healthState{}, config: dashboardConfig{
		BaseSources: []lite.Source{{ID: "verified", Company: "Verified", Provider: "greenhouse"}},
		RuntimeMode: "serve", DeliveryMode: "log", TelegramTokenPresent: true, TelegramChatPresent: true,
	}}).handler(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
	}
	var body statusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.State != "degraded" || body.Sources.Configured != 22 || body.Sources.Healthy != 1 || body.Sources.Failed != 1 || body.Sources.Pending != 20 {
		t.Fatalf("unexpected source state: %+v", body.Sources)
	}
	if len(body.Sources.Monitored) != 2 || body.Sources.Monitored[0].Company != "Auto AI" || body.Sources.Monitored[0].State != "failure" ||
		body.Sources.Monitored[1].Company != "Verified" || body.Sources.Monitored[1].State != "success" {
		t.Fatalf("unexpected monitored source roster: %+v", body.Sources.Monitored)
	}
	if body.Discovery.PromotedSources != 21 || body.Dedupe.MultiSourceJobs != 42 || body.Deliveries.Total != 226 || body.Deliveries.Staged != 1 {
		t.Fatalf("unexpected durable state: %+v", body)
	}
	if body.Telegram.State != "locked" || !body.Telegram.ReadyForUserAuthorization || body.Telegram.ExternalPublishingActive {
		t.Fatalf("unexpected Telegram state: %+v", body.Telegram)
	}
	encoded := response.Body.String()
	if strings.Contains(encoded, "secret") || strings.Contains(encoded, "example.test") {
		t.Fatalf("private source diagnostic leaked: %s", encoded)
	}
}

func TestStatusRosterIncludesPendingVerifiedCompany(t *testing.T) {
	response := buildStatusResponse(lite.OperationalState{
		GeneratedAt: time.Now(),
		RoutineSourceStatus: []lite.SourceStatus{
			{SourceID: "stripe-greenhouse", State: "success", ObservedCount: 30},
		},
	}, dashboardConfig{BaseSources: []lite.Source{
		{ID: "stripe-greenhouse", Company: "Stripe", Provider: "greenhouse"},
		{ID: "roblox-greenhouse", Company: "Roblox", Provider: "greenhouse"},
	}, LogoDomains: map[string]string{normalizeFeedCompany("Roblox"): "roblox.com"}}, nil)

	if len(response.Sources.Monitored) != 2 {
		t.Fatalf("monitored roster = %+v", response.Sources.Monitored)
	}
	roblox := response.Sources.Monitored[0]
	if roblox.Company != "Roblox" || roblox.SourceID != "roblox-greenhouse" || roblox.Provider != "greenhouse" ||
		roblox.LogoDomain != "roblox.com" || roblox.State != "pending" {
		t.Fatalf("Roblox roster item = %+v", roblox)
	}
}

func TestStatusCountsMarketSearchWithoutListingItAsACompany(t *testing.T) {
	response := buildStatusResponse(lite.OperationalState{
		GeneratedAt: time.Now(),
		RoutineSourceStatus: []lite.SourceStatus{
			{SourceID: "stripe-greenhouse", State: "success", ObservedCount: 30},
			{SourceID: "market-top-quant-2027", State: "success", ObservedCount: 0},
		},
	}, dashboardConfig{BaseSources: []lite.Source{
		{ID: "stripe-greenhouse", Company: "Stripe", Provider: "greenhouse"},
		{ID: "market-top-quant-2027", Company: "Market discovery", Provider: "market_search"},
	}}, nil)

	if response.Sources.Configured != 2 || response.Sources.Observed != 2 || response.Sources.Healthy != 2 || response.Sources.HealthyEmpty != 1 {
		t.Fatalf("market source health was not counted: %+v", response.Sources)
	}
	if len(response.Sources.Monitored) != 1 || response.Sources.Monitored[0].Company != "Stripe" {
		t.Fatalf("control-plane market source leaked into company roster: %+v", response.Sources.Monitored)
	}
}

func TestStatusDegradesWhileDeliveryDecisionsAreStaged(t *testing.T) {
	response := buildStatusResponse(lite.OperationalState{
		GeneratedAt:         time.Now(),
		DeliveryCounts:      map[string]int{"staged": 2},
		DiscoveredCounts:    map[string]int{"promoted": 1},
		RoutineSourceStatus: []lite.SourceStatus{{SourceID: "verified", State: "success", ObservedCount: 1}},
	}, dashboardConfig{BaseSources: []lite.Source{{ID: "verified", Company: "Verified", Provider: "greenhouse"}}}, nil)

	if response.State != "degraded" || response.Deliveries.Staged != 2 || response.Deliveries.Total != 2 {
		t.Fatalf("staged delivery health = %+v", response)
	}
}

func TestStatusHandlerEncodesHealthyFailuresAsEmptyArray(t *testing.T) {
	response := httptest.NewRecorder()
	(statusServer{store: fakeFeedStore{operational: lite.OperationalState{
		GeneratedAt: time.Now(),
		RoutineSourceStatus: []lite.SourceStatus{
			{SourceID: "verified", State: "success", ObservedCount: 4},
		},
	}}, config: dashboardConfig{
		BaseSources: []lite.Source{{ID: "verified", Company: "Verified", Provider: "greenhouse"}},
		RuntimeMode: "serve", DeliveryMode: "log",
	}}).handler(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"failures":[]`) {
		t.Fatalf("healthy status must encode failures as an array: %d %s", response.Code, response.Body.String())
	}
}

func TestStatusHandlerKeepsQuarantinedDiscoveryOutOfActiveHealth(t *testing.T) {
	statuses := make([]lite.SourceStatus, 0, 70)
	for i := 0; i < 70; i++ {
		statuses = append(statuses, lite.SourceStatus{SourceID: fmt.Sprintf("source-%d", i), State: "success", ObservedCount: 1})
	}
	response := httptest.NewRecorder()
	(statusServer{store: fakeFeedStore{operational: lite.OperationalState{
		GeneratedAt:         time.Now(),
		RoutineSourceStatus: statuses,
		DiscoveredCounts:    map[string]int{"promoted": 22, "unhealthy": 5},
	}}, config: dashboardConfig{TotalSources: 70, RuntimeMode: "serve", DeliveryMode: "log"}}).
			handler(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	var body statusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || body.State != "healthy" || body.Sources.Healthy != 70 || body.Discovery.UnhealthySources != 5 {
		t.Fatalf("quarantine must remain visible without degrading active health: status=%d body=%+v", response.Code, body)
	}
}

func TestStatusConfiguredCountIncludesPersistedMarketSources(t *testing.T) {
	statuses := make([]lite.SourceStatus, 0, 81)
	for i := 0; i < 81; i++ {
		statuses = append(statuses, lite.SourceStatus{SourceID: fmt.Sprintf("source-%d", i), State: "success", ObservedCount: 1})
	}
	response := buildStatusResponse(lite.OperationalState{
		GeneratedAt:         time.Now(),
		RoutineSourceStatus: statuses,
		DiscoveredCounts:    map[string]int{"promoted": 27},
	}, dashboardConfig{TotalSources: 75, RuntimeMode: "serve", DeliveryMode: "log"}, nil)

	if response.Sources.Configured != 81 || response.Sources.Observed != 81 || response.Sources.Healthy != 81 {
		t.Fatalf("persisted market sources must produce truthful 81/81 health: %+v", response.Sources)
	}
}

func TestStatusHandlerReadsDurableCrawlerCycleFromSeparateUI(t *testing.T) {
	finished := time.Date(2026, 8, 18, 1, 30, 0, 0, time.UTC)
	started := finished.Add(-8 * time.Minute)
	health := &healthState{}
	health.recordReadOnly(&lite.RuntimeState{LastCycleState: "success", LastCycleFinished: &finished})
	response := buildStatusResponse(lite.OperationalState{
		GeneratedAt: time.Now(),
		Runtime: &lite.RuntimeState{
			LastCycleState: "success", LastCycleStarted: &started, LastCycleFinished: &finished,
			SourcesAttempted: 70, SourcesSucceeded: 70, Observed: 9378, Created: 28,
		},
	}, dashboardConfig{RuntimeMode: "serve", TotalSources: 70}, health)

	if response.Runtime.CrawlerEmbedded || !response.Runtime.Ready || response.Runtime.LastCycleState != "success" ||
		response.Runtime.LastCycleAt == nil || !response.Runtime.LastCycleAt.Equal(finished) ||
		response.Runtime.SourcesAttempted != 70 || response.Runtime.SourcesSucceeded != 70 || response.Runtime.Observed != 9378 ||
		response.Runtime.Created != 28 {
		t.Fatalf("durable runtime response = %#v", response.Runtime)
	}
}

func TestStatusHandlerShowsCrossServiceCycleInProgress(t *testing.T) {
	started := time.Date(2026, 8, 18, 2, 0, 0, 0, time.UTC)
	response := buildStatusResponse(lite.OperationalState{
		GeneratedAt: time.Now(),
		Runtime:     &lite.RuntimeState{ActiveOwner: "worker-one", ActiveStartedAt: &started, LastCycleState: "pending"},
	}, dashboardConfig{RuntimeMode: "serve", TotalSources: 70}, nil)
	if !response.Runtime.CycleRunning || response.Runtime.ActiveSince == nil || !response.Runtime.ActiveSince.Equal(started) || !response.Runtime.Ready {
		t.Fatalf("running runtime response = %#v", response.Runtime)
	}
}

func TestStatusHandlerFlagsStaleCrossServiceCycle(t *testing.T) {
	started := time.Date(2026, 8, 18, 2, 0, 0, 0, time.UTC)
	response := buildStatusResponse(lite.OperationalState{
		GeneratedAt: started.Add(22 * time.Minute),
		Runtime: &lite.RuntimeState{
			ActiveOwner: "dead-worker", ActiveStartedAt: &started, LastCycleState: "success",
		},
	}, dashboardConfig{RuntimeMode: "serve", TotalSources: 70, CycleTimeout: 20 * time.Minute}, nil)
	if response.State != "degraded" || !response.Runtime.CycleRunning || !response.Runtime.CycleStale || !response.Runtime.LastCycleError {
		t.Fatalf("stale runtime response = %#v", response)
	}
}

func TestStatusHandlerDoesNotOverwriteDurableDiscoveryDegradation(t *testing.T) {
	finished := time.Now().UTC().Add(-time.Second)
	health := &healthState{}
	health.recordCycle(
		lite.RunReport{SourcesAttempted: 70, SourcesSucceeded: 70},
		lite.DiscoveryReport{CandidatesAttempted: 4, CandidatesFailed: 1},
		lite.DeliveryReport{},
		nil,
	)
	response := buildStatusResponse(lite.OperationalState{
		GeneratedAt: time.Now().UTC(),
		Runtime: &lite.RuntimeState{
			LastCycleState: "degraded", LastCycleFinished: &finished,
			SourcesAttempted: 70, SourcesSucceeded: 70,
		},
	}, dashboardConfig{RuntimeMode: "routine", TotalSources: 70}, health)
	if response.State != "degraded" || !response.Runtime.Ready || !response.Runtime.Degraded ||
		response.Runtime.LastCycleState != "degraded" || response.Runtime.LastCycleError {
		t.Fatalf("discovery degradation was overwritten: %#v", response.Runtime)
	}
}

func TestStatusHandlerKeepsPrivateStoreErrorsOutOfResponse(t *testing.T) {
	var logs strings.Builder
	response := httptest.NewRecorder()
	(statusServer{
		store:  fakeFeedStore{err: errors.New("postgres://user:private@example")},
		logger: slog.New(slog.NewTextHandler(&logs, nil)),
	}).handler(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("unsafe response: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(logs.String(), "private") {
		t.Fatal("expected private diagnostic to remain in logs")
	}
}

func TestBuildTelegramStatusRequiresModeCredentialsAndGate(t *testing.T) {
	cases := []struct {
		name   string
		config dashboardConfig
		state  string
		active bool
	}{
		{"log only", dashboardConfig{DeliveryMode: "log"}, "log_only", false},
		{"missing credentials", dashboardConfig{DeliveryMode: "telegram"}, "credentials_missing", false},
		{"locked", dashboardConfig{DeliveryMode: "telegram", TelegramTokenPresent: true, TelegramChatPresent: true}, "locked", false},
		{"enabled", dashboardConfig{DeliveryMode: "telegram", TelegramTokenPresent: true, TelegramChatPresent: true, PublishingEnabled: true}, "enabled", true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			status := buildTelegramStatus(test.config)
			if status.State != test.state || status.ExternalPublishingActive != test.active {
				t.Fatalf("unexpected status: %+v", status)
			}
		})
	}
}

var _ dashboardStore = fakeFeedStore{}
