package postgres

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hwennnn/radar/internal/pipeline"
	"github.com/lib/pq"
)

type DeliveryDrainer = pipeline.DeliveryDrainer

type senderFunc func(context.Context, Delivery) error

func (f senderFunc) Send(ctx context.Context, delivery Delivery) error {
	return f(ctx, delivery)
}

func deliveryRetryDelay(base time.Duration, attempts int) time.Duration {
	return pipeline.DeliveryRetryDelay(base, attempts)
}

const maxDeliveryRetryDelay = pipeline.MaxDeliveryRetryDelay

func TestNewPostgresStoreRejectsUnsafeSchema(t *testing.T) {
	for _, schema := range []string{"radar-lite", "public; DROP SCHEMA public", "UPPER", "" + string(make([]byte, 64))} {
		if _, err := NewPostgresStore(&sql.DB{}, PostgresOptions{Schema: schema}); err == nil {
			t.Fatalf("expected schema %q to be rejected", schema)
		}
	}
	store, err := NewPostgresStore(&sql.DB{}, PostgresOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if store.Schema() != DefaultSchema {
		t.Fatalf("default schema = %q, want %q", store.Schema(), DefaultSchema)
	}
}

func TestPostgresStoreRestartReplayAndCrossSourceDedupe(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	firstSeen := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	postedAt := firstSeen.Add(-24 * time.Hour)

	first, created, err := store.Observe(ctx, Observation{
		SourceID: "greenhouse:acme", SourceNativeID: "42", Company: "Acme, Inc.",
		Title: "Software Engineer, New Grad", Location: "New York, NY", Country: "US",
		EmploymentType: "Full-time", Level: "New Grad",
		ApplyURL: "https://boards.example/jobs/42?utm_source=feed", Description: "Initial role description.", PostedAt: &postedAt, ObservedAt: firstSeen,
	})
	if err != nil || !created {
		t.Fatalf("first observation created=%v err=%v", created, err)
	}
	changedURL, created, err := store.Observe(ctx, Observation{
		SourceID: "greenhouse:acme", SourceNativeID: "42", Company: "Acme Inc",
		Title: "Software Engineer - New Grad", Location: "New York NY", Country: "United States",
		EmploymentType: "Full-time", Level: "New Grad",
		ApplyURL: "https://apply.example/acme/42?utm_campaign=redirect", Description: "Enriched role description.", ObservedAt: firstSeen.Add(30 * time.Minute),
	})
	if err != nil || created || changedURL.ID != first.ID {
		t.Fatalf("native replay after URL change posting=%#v created=%v err=%v", changedURL, created, err)
	}

	// Construct a new store over the same database to prove state survives a
	// process restart. A distinct source/native ID converges through the same
	// canonical apply URL.
	restarted, err := NewPostgresStore(db, PostgresOptions{Schema: store.Schema()})
	if err != nil {
		t.Fatal(err)
	}
	second, created, err := restarted.Observe(ctx, Observation{
		SourceID: "lever:acme", SourceNativeID: "different-id", Company: "ACME INC",
		Title: "Software Engineer - New Grad", Location: "New York NY", Country: "US",
		EmploymentType: "Full-time", Level: "Entry level",
		ApplyURL: "https://apply.example/acme/42?source=lever", ObservedAt: firstSeen.Add(time.Hour),
	})
	if err != nil || created {
		t.Fatalf("cross-source replay created=%v err=%v", created, err)
	}
	if second.ID != first.ID || !second.FirstSeenAt.Equal(firstSeen) || !second.LastSeenAt.Equal(firstSeen.Add(time.Hour)) {
		t.Fatalf("deduped posting = %#v; first = %#v", second, first)
	}
	if second.EmploymentType != "Full-time" || second.Level != "Entry level" {
		t.Fatalf("structured timing fields were not updated: %#v", second)
	}
	if second.Country != "US" || second.Description != "Enriched role description." {
		t.Fatalf("country/description did not survive restart and enrichment: %#v", second)
	}
	if second.PostedAt == nil || !second.PostedAt.Equal(postedAt) {
		t.Fatalf("posted_at did not survive restart and enrichment: %#v", second)
	}

	var jobs int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM "`+store.Schema()+`"."jobs"`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("jobs = %d, want 1", jobs)
	}
	var sourceObservations int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM "`+store.Schema()+`"."job_source_observations" WHERE job_id = $1`, first.ID).Scan(&sourceObservations); err != nil {
		t.Fatal(err)
	}
	if sourceObservations != 2 {
		t.Fatalf("source observations = %d, want two retained source relationships", sourceObservations)
	}
}

func TestPostgresStoreConvergesLegacyCrossDomainRequisitionDuplicates(t *testing.T) {
	database, store := integrationStore(t)
	ctx := context.Background()
	const (
		uuid        = "6cdb0f39-234a-4234-b1f1-cb48a1fa2795"
		canonicalID = "legacy-airwallex-branded"
		duplicateID = "legacy-airwallex-ashby"
		brandedURL  = "https://careers.airwallex.com/job/" + uuid + "/software-engineer-intern-2027"
		ashbyURL    = "https://jobs.ashbyhq.com/airwallex/" + uuid + "/application"
	)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := database.ExecContext(ctx, `
INSERT INTO `+store.table("jobs")+` (id, company, title, location, country, employment_type, level, apply_url, first_seen_at, last_seen_at)
VALUES
  ($1, 'Airwallex', 'Software Engineer – Intern 2027', 'Singapore', 'Singapore', 'Intern', 'internship', $3, $5, $5),
  ($2, 'Airwallex', 'Software Engineer - Intern 2027', 'SG - Singapore', 'Singapore', 'Intern', 'internship', $4, $6, $6)`,
		canonicalID, duplicateID, brandedURL, ashbyURL, now.Add(-time.Hour), now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO `+store.table("job_identities")+` (identity_key, job_id) VALUES
  ($1, $5), ($2, $5), ($3, $6), ($4, $6)`,
		"native:market:branded", "url:"+brandedURL,
		"native:ashby:ashby:airwallex:"+uuid, "url:"+ashbyURL,
		canonicalID, duplicateID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO `+store.table("job_source_observations")+`
  (job_id, source_id, source_native_id, first_observed_at, last_observed_at) VALUES
  ($1, 'market', 'branded', $3, $3),
  ($2, 'ashby', $4, $3, $3)`, canonicalID, duplicateID, now.Add(-time.Minute), "ashby:airwallex:"+uuid); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO `+store.table("deliveries")+` (job_id, channel, recipient, payload, status) VALUES
  ($1, 'log', 'merge-recipient', jsonb_build_object('ID', $1::text), 'suppressed'),
	($2, 'log', 'merge-recipient', jsonb_build_object('ID', $2::text), 'pending'),
	($1, 'log', 'stage-recipient', jsonb_build_object('ID', $1::text), 'failed'),
	($2, 'log', 'stage-recipient', jsonb_build_object('ID', $2::text), 'staged')`, canonicalID, duplicateID); err != nil {
		t.Fatal(err)
	}
	if _, created, err := store.Observe(ctx, Observation{
		SourceID: "ashby", SourceNativeID: "ashby:airwallex:" + uuid,
		Company: "Airwallex", Title: "Software Engineer - Intern 2027", Location: "SG - Singapore", Country: "Singapore",
		EmploymentType: "Intern", Level: "internship", ApplyURL: ashbyURL, ObservedAt: now,
	}); err != nil || created {
		t.Fatalf("ashby alias learning created=%v err=%v", created, err)
	}
	posting, created, err := store.Observe(ctx, Observation{
		SourceID: "market", SourceNativeID: "branded",
		Company: "Airwallex", Title: "Software Engineer – Intern 2027", Location: "Singapore", Country: "Singapore",
		EmploymentType: "Intern", Level: "internship", ApplyURL: brandedURL, ObservedAt: now.Add(time.Minute),
	})
	if err != nil || created || posting.ID != canonicalID {
		t.Fatalf("converged posting=%#v created=%v err=%v", posting, created, err)
	}
	var jobs, identities, observations, deliveries int
	for query, target := range map[string]*int{
		`SELECT count(*) FROM ` + store.table("jobs") + ` WHERE company = 'Airwallex'`:                             &jobs,
		`SELECT count(*) FROM ` + store.table("job_identities") + ` WHERE job_id = '` + canonicalID + `'`:          &identities,
		`SELECT count(*) FROM ` + store.table("job_source_observations") + ` WHERE job_id = '` + canonicalID + `'`: &observations,
		`SELECT count(*) FROM ` + store.table("deliveries") + ` WHERE job_id = '` + canonicalID + `'`:              &deliveries,
	} {
		if err := database.QueryRowContext(ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if jobs != 1 || identities != 7 || observations != 2 || deliveries != 2 {
		t.Fatalf("jobs=%d identities=%d observations=%d deliveries=%d", jobs, identities, observations, deliveries)
	}
	var status, payloadID string
	if err := database.QueryRowContext(ctx, `SELECT status, payload->>'ID' FROM `+store.table("deliveries")+` WHERE job_id = $1`, canonicalID).Scan(&status, &payloadID); err != nil {
		t.Fatal(err)
	}
	if status != "suppressed" || payloadID != canonicalID {
		t.Fatalf("merged delivery status=%q payload_id=%q", status, payloadID)
	}
	if err := database.QueryRowContext(ctx, `SELECT status, payload->>'ID' FROM `+store.table("deliveries")+` WHERE job_id = $1 AND recipient = 'stage-recipient'`, canonicalID).Scan(&status, &payloadID); err != nil {
		t.Fatal(err)
	}
	if status != "staged" || payloadID != canonicalID {
		t.Fatalf("merged staged delivery status=%q payload_id=%q", status, payloadID)
	}
}

func TestPostgresStoreListsCompactFeedAndSourceState(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	older := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	newer := older.Add(time.Hour)

	for _, observation := range []Observation{
		{
			SourceID: "ashby:older", SourceNativeID: "older-1", Company: "Older AI",
			Title: "Software Engineer Intern", Location: "Singapore", Country: "SG",
			EmploymentType: "Internship", Level: "internship",
			ApplyURL: "https://jobs.example/older", Description: "Not needed by the compact feed.", ObservedAt: older,
		},
		{
			SourceID: "greenhouse:newer", SourceNativeID: "newer-1", Company: "Newer Systems",
			Title: "Backend Engineer, New Grad", Location: "New York, NY", Country: "US",
			EmploymentType: "Full-time", Level: "new_grad",
			ApplyURL: "https://jobs.example/newer", Description: "Also omitted.", ObservedAt: newer,
		},
		{
			SourceID: "market-software-early-career", SourceNativeID: "untrusted-1", Company: "Search Artifact",
			Title: "Software Engineer Intern", Location: "Singapore", Country: "SG",
			EmploymentType: "Internship", Level: "internship",
			ApplyURL: "https://aggregator.example/untrusted", ObservedAt: newer.Add(time.Minute),
		},
	} {
		if _, created, err := store.Observe(ctx, observation); err != nil || !created {
			t.Fatalf("observe created=%v err=%v", created, err)
		}
	}
	if err := store.RecordSourceSuccess(ctx, "ashby:older", 1, newer); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSourceFailure(ctx, "greenhouse:newer", errors.New("fixture failure"), newer); err != nil {
		t.Fatal(err)
	}

	postings, err := store.ListPostings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 2 || postings[0].Company != "Newer Systems" || postings[1].Company != "Older AI" {
		t.Fatalf("unexpected feed order: %#v", postings)
	}
	for _, posting := range postings {
		if posting.Company == "Search Artifact" {
			t.Fatalf("market-search control-plane evidence leaked into feed: %#v", postings)
		}
	}
	if postings[0].Description != "" || postings[1].Description != "" {
		t.Fatalf("compact feed loaded descriptions: %#v", postings)
	}

	statuses, err := store.ListSourceStatuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 || statuses[0].SourceID != "ashby:older" || statuses[0].State != "success" ||
		statuses[1].SourceID != "greenhouse:newer" || statuses[1].State != "failure" {
		t.Fatalf("unexpected source statuses: %#v", statuses)
	}
	operational, err := store.ReadOperationalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if operational.CanonicalJobs != 2 || operational.IdentityAliases < 2 || operational.SourceObservations != 2 || operational.MultiSourceJobs != 0 {
		t.Fatalf("unexpected operational dedupe state: %#v", operational)
	}
	if len(operational.RoutineSourceStatus) != 2 || len(operational.PromotedSources) != 0 || sumCountMap(operational.DeliveryCounts) != 0 {
		t.Fatalf("unexpected operational state: %#v", operational)
	}
}

func TestPostgresStoreCycleLeaseSerializesInstancesAndPersistsRuntime(t *testing.T) {
	db, first := integrationStore(t)
	ctx := context.Background()
	started := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)

	lease, acquired, err := first.TryAcquireCycle(ctx, "worker-one", started)
	if err != nil || !acquired || lease == nil {
		t.Fatalf("first lease acquired=%v lease=%v err=%v", acquired, lease, err)
	}
	restarted, err := NewPostgresStore(db, PostgresOptions{Schema: first.Schema()})
	if err != nil {
		t.Fatal(err)
	}
	blocked, acquired, err := restarted.TryAcquireCycle(ctx, "worker-two", started.Add(time.Second))
	if err != nil || acquired || blocked != nil {
		t.Fatalf("competing lease acquired=%v lease=%v err=%v", acquired, blocked, err)
	}
	operational, err := restarted.ReadOperationalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if operational.Runtime == nil || operational.Runtime.ActiveOwner != "worker-one" || operational.Runtime.ActiveStartedAt == nil ||
		!operational.Runtime.ActiveStartedAt.Equal(started) {
		t.Fatalf("active runtime = %#v", operational.Runtime)
	}

	finished := started.Add(45 * time.Second)
	if err := lease.Complete(ctx, CycleResult{
		Status: "success", SourcesAttempted: 70, SourcesSucceeded: 70,
		Observed: 9300, Created: 12, EligibleCreated: 2, Enqueued: 2,
		DeliveriesSent: 2, FinishedAt: finished,
	}); err != nil {
		t.Fatal(err)
	}
	operational, err = restarted.ReadOperationalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runtime := operational.Runtime
	if runtime == nil || runtime.ActiveOwner != "" || runtime.ActiveStartedAt != nil || runtime.LastCycleState != "success" ||
		runtime.LastCycleStarted == nil || !runtime.LastCycleStarted.Equal(started) || runtime.LastCycleFinished == nil ||
		!runtime.LastCycleFinished.Equal(finished) || runtime.SourcesAttempted != 70 || runtime.SourcesSucceeded != 70 ||
		runtime.Observed != 9300 || runtime.Created != 12 || runtime.EligibleCreated != 2 || runtime.Enqueued != 2 ||
		runtime.DeliveriesSent != 2 {
		t.Fatalf("completed runtime = %#v", runtime)
	}

	secondLease, acquired, err := restarted.TryAcquireCycle(ctx, "worker-two", finished.Add(time.Second))
	if err != nil || !acquired || secondLease == nil {
		t.Fatalf("post-release lease acquired=%v lease=%v err=%v", acquired, secondLease, err)
	}
	if err := secondLease.Release(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresStoreUncertainCycleAcquisitionCleanupCannotLeakLock(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lockName := "radar-lite-cycle:" + store.Schema()
	var acquired bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, lockName).Scan(&acquired); err != nil || !acquired {
		t.Fatalf("prepare uncertain lock acquired=%v err=%v", acquired, err)
	}
	if err := cleanupUncertainCycleLease(conn, lockName); err != nil {
		t.Fatal(err)
	}

	lease, acquired, err := store.TryAcquireCycle(ctx, "worker-after-uncertain-result", time.Now().UTC())
	if err != nil || !acquired || lease == nil {
		t.Fatalf("lock leaked after uncertain cleanup acquired=%v lease=%v err=%v", acquired, lease, err)
	}
	if err := lease.Release(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresStoreInvalidCompletionReleasesLease(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	lease, acquired, err := store.TryAcquireCycle(ctx, "invalid-result-owner", time.Now().UTC())
	if err != nil || !acquired || lease == nil {
		t.Fatalf("first lease acquired=%v lease=%v err=%v", acquired, lease, err)
	}
	if err := lease.Complete(ctx, CycleResult{Status: "not-a-status"}); err == nil {
		t.Fatal("invalid completion must fail")
	}
	operational, err := store.ReadOperationalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if operational.Runtime == nil || operational.Runtime.ActiveOwner != "" || operational.Runtime.ActiveStartedAt != nil {
		t.Fatalf("invalid completion retained active state: %#v", operational.Runtime)
	}
	second, acquired, err := store.TryAcquireCycle(ctx, "worker-after-invalid-result", time.Now().UTC())
	if err != nil || !acquired || second == nil {
		t.Fatalf("invalid completion leaked lease acquired=%v lease=%v err=%v", acquired, second, err)
	}
	if err := second.Release(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresStoreCycleLeaseSurvivesOwnerProcessCrash(t *testing.T) {
	_, store := integrationStore(t)
	command := exec.Command(os.Args[0], "-test.run=^TestPostgresStoreCycleLeaseCrashHelper$")
	command.Env = append(os.Environ(),
		"RADAR_LITE_LEASE_CRASH_HELPER=1",
		"RADAR_LITE_LEASE_CRASH_SCHEMA="+store.Schema(),
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	lineResult := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		line, readErr := bufio.NewReader(stdout).ReadString('\n')
		lineResult <- struct {
			line string
			err  error
		}{line: line, err: readErr}
	}()
	select {
	case result := <-lineResult:
		if result.err != nil || strings.TrimSpace(result.line) != "lease-acquired" {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatalf("crash helper did not acquire lease line=%q err=%v stderr=%s", result.line, result.err, stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("crash helper timed out: %s", stderr.String())
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("crash helper unexpectedly exited cleanly")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		lease, acquired, acquireErr := store.TryAcquireCycle(context.Background(), "worker-after-crash", time.Now().UTC())
		if acquireErr != nil {
			t.Fatal(acquireErr)
		}
		if acquired {
			if lease == nil {
				t.Fatal("acquired crash-takeover lease is nil")
			}
			if err := lease.Release(context.Background()); err != nil {
				t.Fatal(err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("database did not release crashed owner's advisory lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPostgresStoreCycleLeaseCrashHelper(t *testing.T) {
	if os.Getenv("RADAR_LITE_LEASE_CRASH_HELPER") != "1" {
		return
	}
	databaseURL := os.Getenv("RADAR_TEST_DATABASE_URL")
	schema := os.Getenv("RADAR_LITE_LEASE_CRASH_SCHEMA")
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewPostgresStore(db, PostgresOptions{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	lease, acquired, err := store.TryAcquireCycle(context.Background(), "crash-helper", time.Now().UTC())
	if err != nil || !acquired || lease == nil {
		t.Fatalf("helper lease acquired=%v lease=%v err=%v", acquired, lease, err)
	}
	fmt.Fprintln(os.Stdout, "lease-acquired")
	select {}
}

func sumCountMap(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func TestPostgresStoreDiscoveryPromotionAndAutomaticHealthDemotion(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidate := DiscoveryCandidate{ID: "perplexity", Name: "Perplexity", Website: "https://www.perplexity.ai", Tags: []string{"priority-1", "benchmark-hiremepls", "ai"}}
	if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{candidate}); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueDiscoveryCandidates(ctx, now.Add(time.Minute), 10)
	if err != nil || len(due) != 1 || due[0].ID != candidate.ID || len(due[0].Tags) != 3 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	source := Source{ID: "auto-perplexity-ashby-1234", Company: "Perplexity", Provider: "ashby", URL: "https://jobs.ashbyhq.com/perplexity"}
	promoted, err := store.RecordDiscoverySuccess(ctx, due[0], source, 0, 0.96, "first healthy empty", now, now.Add(time.Hour))
	if err != nil || promoted {
		t.Fatalf("first empty promoted=%v err=%v", promoted, err)
	}
	promoted, err = store.RecordDiscoverySuccess(ctx, due[0], source, 0, 0.96, "second healthy empty", now.Add(time.Hour), now.Add(2*time.Hour))
	if err != nil || promoted {
		t.Fatalf("second empty promoted=%v err=%v", promoted, err)
	}
	sources, err := store.ListPromotedSources(ctx)
	if err != nil || len(sources) != 0 {
		t.Fatalf("promoted sources=%#v err=%v", sources, err)
	}
	promoted, err = store.RecordDiscoverySuccess(ctx, due[0], source, 4, 0.96, "first nonempty snapshot", now.Add(2*time.Hour), now.Add(3*time.Hour))
	if err != nil || !promoted {
		t.Fatalf("nonempty promoted=%v err=%v", promoted, err)
	}
	sources, err = store.ListPromotedSources(ctx)
	if err != nil || len(sources) != 1 || sources[0].ID != source.ID {
		t.Fatalf("promoted nonempty sources=%#v err=%v", sources, err)
	}

	for index := 0; index < 3; index++ {
		if err := store.RecordSourceFailure(ctx, source.ID, errors.New("board unavailable"), now.Add(time.Duration(index+2)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	demoted, err := store.DemoteUnhealthyDiscoveredSources(ctx, 3, now.Add(6*time.Hour))
	if err != nil || demoted != 1 {
		t.Fatalf("demoted=%d err=%v", demoted, err)
	}
	sources, err = store.ListPromotedSources(ctx)
	if err != nil || len(sources) != 0 {
		t.Fatalf("sources after demotion=%#v err=%v", sources, err)
	}
	statuses, err := store.ListSourceStatuses(ctx)
	if err != nil || len(statuses) != 0 {
		t.Fatalf("demoted discovered source leaked into active health: statuses=%#v err=%v", statuses, err)
	}
	due, err = store.ListDueDiscoveryCandidates(ctx, now.Add(6*time.Hour), 10)
	if err != nil || len(due) != 1 || due[0].State != "retry" {
		t.Fatalf("rediscovery due=%#v err=%v", due, err)
	}
}

func TestPostgresStorePrioritizesFreshMarketCandidates(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	candidates := []DiscoveryCandidate{
		{ID: "aaa-general", Name: "General Seed"},
		{ID: "zzz-market", Name: "Market Discovery", Tags: []string{"auto-market-search"}},
	}
	if err := store.SeedDiscoveryCandidates(ctx, candidates); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueDiscoveryCandidates(ctx, time.Now().UTC().Add(time.Minute), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != "zzz-market" {
		t.Fatalf("first due candidate = %#v, want market-discovered company", due)
	}
}

func TestPostgresStoreSchedulesPromotedCandidateForPeriodicSourceRefresh(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidate := DiscoveryCandidate{ID: "regional", Name: "Regional Tech", Tags: []string{"priority-1"}}
	if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{candidate}); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueDiscoveryCandidates(ctx, now.Add(time.Minute), 1)
	if err != nil || len(due) != 1 {
		t.Fatalf("initial due=%#v err=%v", due, err)
	}
	refreshAt := now.Add(7 * 24 * time.Hour)
	source := Source{ID: "regional-us", Company: candidate.Name, Provider: "ashby", URL: "https://jobs.ashbyhq.com/regionaltech"}
	if promoted, err := store.RecordDiscoverySuccess(ctx, due[0], source, 3, 0.96, "verified", now, refreshAt); err != nil || !promoted {
		t.Fatalf("promoted=%v err=%v", promoted, err)
	}
	if early, err := store.ListDueDiscoveryCandidates(ctx, refreshAt.Add(-time.Minute), 10); err != nil || len(early) != 0 {
		t.Fatalf("early refresh=%#v err=%v", early, err)
	}
	refreshed, err := store.ListDueDiscoveryCandidates(ctx, refreshAt.Add(time.Minute), 10)
	if err != nil || len(refreshed) != 1 || refreshed[0].State != "promoted" {
		t.Fatalf("refresh due=%#v err=%v", refreshed, err)
	}
}

func TestPostgresStoreCompactsHistoricalCaseVariantDiscoveryRoutes(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidate := DiscoveryCandidate{ID: "lambda", Name: "Lambda", Tags: []string{"priority-1"}}
	if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{candidate}); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueDiscoveryCandidates(ctx, now.Add(time.Minute), 1)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	for index, route := range []string{"https://jobs.ashbyhq.com/Lambda", "https://jobs.ashbyhq.com/lambda/"} {
		source := Source{ID: fmt.Sprintf("lambda-%d", index), Company: "Lambda", Provider: "ashby", URL: route}
		if promoted, err := store.RecordDiscoverySuccess(ctx, due[0], source, 2, 0.96, "legacy", now.Add(time.Duration(index)*time.Second), now.Add(7*24*time.Hour)); err != nil || !promoted {
			t.Fatalf("route %d promoted=%v err=%v", index, promoted, err)
		}
	}
	compacted, err := store.SuppressDuplicateDiscoveredSources(ctx, now.Add(time.Minute))
	if err != nil || compacted != 1 {
		t.Fatalf("compacted=%d err=%v", compacted, err)
	}
	sources, err := store.ListPromotedSources(ctx)
	if err != nil || len(sources) != 1 {
		t.Fatalf("promoted sources=%#v err=%v", sources, err)
	}
}

func TestPostgresStorePrioritizesTieredResearchCandidates(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	candidates := []DiscoveryCandidate{
		{ID: "priority-three", Name: "Priority Three", Tags: []string{"priority-3"}},
		{ID: "priority-one", Name: "Priority One", Tags: []string{"priority-1"}},
		{ID: "priority-two", Name: "Priority Two", Tags: []string{"priority-2"}},
	}
	if err := store.SeedDiscoveryCandidates(ctx, candidates); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueDiscoveryCandidates(ctx, time.Now().UTC().Add(time.Minute), 3)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{"priority-one", "priority-two", "priority-three"} {
		if due[index].ID != want {
			t.Fatalf("due[%d] = %q, want %q", index, due[index].ID, want)
		}
	}
}

func TestPostgresStoreInterleavesMarketAndResearchCandidates(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	candidates := []DiscoveryCandidate{
		{ID: "market-a", Name: "Market A", Tags: []string{"auto-market-search"}},
		{ID: "market-b", Name: "Market B", Tags: []string{"auto-market-search"}},
		{ID: "research-a", Name: "Research A", Tags: []string{"priority-1"}},
		{ID: "research-b", Name: "Research B", Tags: []string{"priority-1"}},
	}
	if err := store.SeedDiscoveryCandidates(ctx, candidates); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueDiscoveryCandidates(ctx, time.Now().UTC().Add(time.Minute), 4)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{"market-a", "research-a", "market-b", "research-b"} {
		if due[index].ID != want {
			t.Fatalf("due[%d] = %q, want %q", index, due[index].ID, want)
		}
	}
}

func TestPostgresStoreInterleavesCareerBarDiscoveryLanes(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	candidates := []DiscoveryCandidate{
		{ID: "market-a", Name: "Market A", Tags: []string{"auto-market-search"}},
		{ID: "market-b", Name: "Market B", Tags: []string{"auto-market-search"}},
		{ID: "yc-a", Name: "YC A", Tags: []string{"priority-1", "yc-top", "unicorn"}},
		{ID: "yc-b", Name: "YC B", Tags: []string{"priority-2", "yc-top", "unicorn"}},
		{ID: "quant-a", Name: "Quant A", Tags: []string{"priority-1", "quant"}},
		{ID: "big-tech-a", Name: "Big Tech A", Tags: []string{"priority-1", "big-tech"}},
		{ID: "unicorn-a", Name: "Unicorn A", Tags: []string{"priority-1", "unicorn"}},
		{ID: "general-a", Name: "General A", Tags: []string{"priority-1", "developer-tools"}},
	}
	if err := store.SeedDiscoveryCandidates(ctx, candidates); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueDiscoveryCandidates(ctx, time.Now().UTC().Add(time.Minute), len(candidates))
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{
		"market-a", "yc-a", "quant-a", "big-tech-a", "unicorn-a", "general-a", "market-b", "yc-b",
	} {
		if due[index].ID != want {
			t.Fatalf("due[%d] = %q, want %q (due=%#v)", index, due[index].ID, want, due)
		}
	}
}

func TestPostgresStoreDiscoveryCountsOneCandidateAttemptAcrossMultipleRoutes(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidate := DiscoveryCandidate{ID: "millennium", Name: "Millennium", Website: "https://www.mlp.com"}
	if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{candidate}); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueDiscoveryCandidates(ctx, now.Add(time.Minute), 1)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	for index, source := range []Source{
		{ID: "auto-millennium-workable", Company: "Millennium", Provider: "workable", URL: "https://apply.workable.com/millennium"},
		{ID: "auto-millennium-greenhouse", Company: "Millennium", Provider: "greenhouse", URL: "https://job-boards.greenhouse.io/millennium"},
	} {
		promoted, recordErr := store.RecordDiscoverySuccess(
			ctx, due[0], source, 0, 0.92, "healthy empty", now.Add(time.Duration(index)*time.Second), now.Add(time.Hour),
		)
		if recordErr != nil || promoted {
			t.Fatalf("route %d promoted=%v err=%v", index, promoted, recordErr)
		}
	}
	var attempts int
	var state string
	if err := db.QueryRowContext(ctx, `SELECT attempts, state FROM "`+store.Schema()+`"."discovery_candidates" WHERE id = 'millennium'`).Scan(&attempts, &state); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || state != "validating" {
		t.Fatalf("attempts=%d state=%q, want one validating attempt", attempts, state)
	}
}

func TestPostgresStoreDiscoveryFailurePersistsRetryAndSourceHealth(t *testing.T) {
	database, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidate := DiscoveryCandidate{ID: "baseten", Name: "Baseten", Website: "https://www.baseten.co"}
	if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{candidate}); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueDiscoveryCandidates(ctx, now.Add(time.Minute), 1)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	source := Source{ID: "auto-baseten-ashby-1234", Company: "Baseten", Provider: "ashby", URL: "https://jobs.ashbyhq.com/baseten"}
	retryAt := now.Add(6 * time.Hour)
	if err := store.RecordDiscoveryFailure(ctx, due[0], &source, errors.New("temporary probe failure"), now, retryAt); err != nil {
		t.Fatal(err)
	}
	due, err = store.ListDueDiscoveryCandidates(ctx, retryAt, 1)
	if err != nil || len(due) != 1 || due[0].State != "retry" || due[0].Attempts != 1 || !strings.Contains(due[0].LastError, "temporary probe") {
		t.Fatalf("retry=%#v err=%v", due, err)
	}
	var state, message string
	var failures int
	if err := database.QueryRowContext(ctx, `SELECT state, consecutive_failures, last_error FROM `+store.table("discovered_sources")+` WHERE id = $1`, source.ID).Scan(&state, &failures, &message); err != nil {
		t.Fatal(err)
	}
	if state != "unhealthy" || failures != 1 || !strings.Contains(message, "temporary probe") {
		t.Fatalf("source state=%s failures=%d error=%q", state, failures, message)
	}
}

func TestPostgresStoreDiscoveryNonemptyBoardPromotesImmediatelyAndDedupesOwnership(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidates := []DiscoveryCandidate{
		{ID: "xai", Name: "xAI", Website: "https://x.ai"},
		{ID: "xai-alias", Name: "xAI Alias", Website: "https://x.ai/careers"},
	}
	if err := store.SeedDiscoveryCandidates(ctx, candidates); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueDiscoveryCandidates(ctx, now.Add(time.Minute), 10)
	if err != nil || len(due) != 2 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	byID := make(map[string]DiscoveryCandidateRecord, len(due))
	for _, record := range due {
		byID[record.ID] = record
	}
	source := Source{ID: "auto-xai-greenhouse-1234", Company: "xAI", Provider: "greenhouse", URL: "https://job-boards.greenhouse.io/xai"}
	promoted, err := store.RecordDiscoverySuccess(ctx, byID["xai"], source, 12, 0.96, "search result", now, now.Add(24*time.Hour))
	if err != nil || !promoted {
		t.Fatalf("nonempty promoted=%v err=%v", promoted, err)
	}
	aliasSource := source
	aliasSource.ID = "auto-xai-alias-greenhouse-9999"
	aliasSource.Company = "xAI Alias"
	promoted, err = store.RecordDiscoverySuccess(ctx, byID["xai-alias"], aliasSource, 12, 0.96, "same board", now.Add(time.Minute), now.Add(24*time.Hour))
	if err != nil || promoted {
		t.Fatalf("duplicate promoted=%v err=%v", promoted, err)
	}
	due, err = store.ListDueDiscoveryCandidates(ctx, now.Add(365*24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range due {
		if record.ID == "xai-alias" {
			t.Fatalf("duplicate candidate remained scheduled: %#v", record)
		}
	}
}

func TestPostgresStoreDiscoveryDemotesPreviouslyPromotedIdentityMismatch(t *testing.T) {
	database, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidate := DiscoveryCandidate{ID: "coreweave", Name: "CoreWeave", Website: "https://www.coreweave.com", Tags: []string{"priority-1", "curated-2026", "ai"}}
	if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{candidate}); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueDiscoveryCandidates(ctx, now.Add(time.Minute), 1)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	source := Source{ID: "auto-coreweave-ashby-1234", Company: "CoreWeave", Provider: "ashby", URL: "https://jobs.ashbyhq.com/coreweave"}
	if promoted, err := store.RecordDiscoverySuccess(ctx, due[0], source, 4, 0.96, "valid board", now, now.Add(24*time.Hour)); err != nil || !promoted {
		t.Fatalf("promoted=%v err=%v", promoted, err)
	}
	if _, created, err := store.Observe(ctx, Observation{
		SourceID: source.ID, SourceNativeID: "role-1", Company: "CoreWeave",
		Title: "Software Engineer Intern", Location: "New York, NY", Country: "US",
		EmploymentType: "Internship", Level: "internship", ApplyURL: "https://example.com/role-1", ObservedAt: now,
	}); err != nil || !created {
		t.Fatalf("observe promoted-source job created=%v err=%v", created, err)
	}
	if postings, err := store.ListPostings(ctx); err != nil || len(postings) != 1 {
		t.Fatalf("postings before identity triage=%#v err=%v", postings, err)
	}
	// Simulate a row promoted by an older resolver before board-ownership
	// validation was introduced. The next triage must self-heal it.
	if _, err := database.ExecContext(ctx, `UPDATE `+store.table("discovered_sources")+` SET url = 'https://jobs.ashbyhq.com/meticulous' WHERE id = $1`, source.ID); err != nil {
		t.Fatal(err)
	}
	demoted, err := store.DemoteUnhealthyDiscoveredSources(ctx, 3, now.Add(time.Hour))
	if err != nil || demoted != 1 {
		t.Fatalf("demoted=%d err=%v", demoted, err)
	}
	promotedSources, err := store.ListPromotedSources(ctx)
	if err != nil || len(promotedSources) != 0 {
		t.Fatalf("promoted after identity triage=%#v err=%v", promotedSources, err)
	}
	if postings, err := store.ListPostings(ctx); err != nil || len(postings) != 0 {
		t.Fatalf("demoted discovery jobs remained visible: postings=%#v err=%v", postings, err)
	}
	due, err = store.ListDueDiscoveryCandidates(ctx, now.Add(time.Hour), 1)
	if err != nil || len(due) != 1 || due[0].State != "retry" || !strings.Contains(due[0].LastError, "does not match") {
		t.Fatalf("retry candidate=%#v err=%v", due, err)
	}
}

func TestPostgresStoreParksPreviouslyPromotedBlockedCompany(t *testing.T) {
	database, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidate := DiscoveryCandidate{ID: "anduril", Name: "Anduril Industries", Website: "https://www.anduril.com", Tags: []string{"priority-1", "curated-2026"}}
	if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{candidate}); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueDiscoveryCandidates(ctx, now.Add(time.Minute), 1)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	source := Source{ID: "auto-anduril-greenhouse", Company: "Anduril Industries", Provider: "greenhouse", URL: "https://job-boards.greenhouse.io/andurilindustries"}
	if promoted, err := store.RecordDiscoverySuccess(ctx, due[0], source, 10, 0.98, "legacy promotion", now, now.Add(time.Hour)); err != nil || !promoted {
		t.Fatalf("legacy promotion=%v err=%v", promoted, err)
	}
	demoted, err := store.DemoteUnhealthyDiscoveredSources(ctx, 3, now.Add(time.Minute))
	if err != nil || demoted != 1 {
		t.Fatalf("demoted=%d err=%v", demoted, err)
	}
	var sourceState, candidateState, sourceError, candidateError string
	if err := database.QueryRowContext(ctx, `SELECT state, last_error FROM `+store.table("discovered_sources")+` WHERE id = $1`, source.ID).Scan(&sourceState, &sourceError); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT state, last_error FROM `+store.table("discovery_candidates")+` WHERE id = $1`, candidate.ID).Scan(&candidateState, &candidateError); err != nil {
		t.Fatal(err)
	}
	if sourceState != "unhealthy" || candidateState != "duplicate" ||
		!strings.Contains(sourceError, "excluded by target policy") || !strings.Contains(candidateError, "excluded by target policy") {
		t.Fatalf("source=%q/%q candidate=%q/%q", sourceState, sourceError, candidateState, candidateError)
	}
	if due, err := store.ListDueDiscoveryCandidates(ctx, now.Add(365*24*time.Hour), 10); err != nil || len(due) != 0 {
		t.Fatalf("blocked candidate remained due: due=%#v err=%v", due, err)
	}
}

func TestPostgresStoreParksBlockedMarketAggregatorCandidate(t *testing.T) {
	database, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidate := DiscoveryCandidate{
		ID: "market-imc-trading", Name: "IMC Trading", Website: "https://expatjobboard.com",
		Tags: []string{"auto-market-search"},
	}
	if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{candidate}); err != nil {
		t.Fatal(err)
	}
	demoted, err := store.DemoteUnhealthyDiscoveredSources(ctx, 3, now)
	if err != nil || demoted != 0 {
		t.Fatalf("demoted=%d err=%v", demoted, err)
	}
	var state, lastError string
	if err := database.QueryRowContext(ctx, `SELECT state, last_error FROM `+store.table("discovery_candidates")+` WHERE id = $1`, candidate.ID).Scan(&state, &lastError); err != nil {
		t.Fatal(err)
	}
	if state != "duplicate" || !strings.Contains(lastError, "blocked job aggregator") {
		t.Fatalf("state=%q error=%q", state, lastError)
	}
	if due, err := store.ListDueDiscoveryCandidates(ctx, now.Add(365*24*time.Hour), 10); err != nil || len(due) != 0 {
		t.Fatalf("blocked aggregator candidate remained due: due=%#v err=%v", due, err)
	}
}

func TestPostgresStoreRejectsPreviouslyPromotedLowSignalCompany(t *testing.T) {
	database, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidate := DiscoveryCandidate{
		ID: "random-startup", Name: "Random Startup", Website: "https://random.example",
		Tags: []string{"priority-1", "benchmark-speedyapply-2027", "ai"},
	}
	if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{candidate}); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueDiscoveryCandidates(ctx, now.Add(time.Minute), 1)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	source := Source{ID: "legacy-random-ashby", Company: candidate.Name, Provider: "ashby", URL: "https://jobs.ashbyhq.com/randomstartup"}
	if promoted, err := store.RecordDiscoverySuccess(ctx, due[0], source, 3, .95, "legacy promotion", now, now.Add(time.Hour)); err != nil || !promoted {
		t.Fatalf("legacy promotion=%t err=%v", promoted, err)
	}
	if demoted, err := store.DemoteUnhealthyDiscoveredSources(ctx, 3, now.Add(time.Minute)); err != nil || demoted != 1 {
		t.Fatalf("demoted=%d err=%v", demoted, err)
	}
	var sourceState, candidateState, sourceCode, candidateCode string
	if err := database.QueryRowContext(ctx, `SELECT state, failure_code FROM `+store.table("discovered_sources")+` WHERE id=$1`, source.ID).Scan(&sourceState, &sourceCode); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT state, failure_code FROM `+store.table("discovery_candidates")+` WHERE id=$1`, candidate.ID).Scan(&candidateState, &candidateCode); err != nil {
		t.Fatal(err)
	}
	if sourceState != "rejected" || candidateState != "parked" || sourceCode != pipeline.DiscoveryFailureCompanyQuality || candidateCode != pipeline.DiscoveryFailureCompanyQuality {
		t.Fatalf("source=%s/%s candidate=%s/%s", sourceState, sourceCode, candidateState, candidateCode)
	}
	if sources, err := store.ListPromotedSources(ctx); err != nil || len(sources) != 0 {
		t.Fatalf("low-signal source remained promoted: %#v err=%v", sources, err)
	}

	qualified := candidate
	qualified.Tags = []string{"priority-1", "curated-2026", "ai"}
	if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{qualified}); err != nil {
		t.Fatal(err)
	}
	due, err = store.ListDueDiscoveryCandidates(ctx, now.Add(2*time.Minute), 1)
	if err != nil || len(due) != 1 || due[0].ID != candidate.ID || due[0].State != "retry" || due[0].FailureCode != "" || due[0].LastError != "" {
		t.Fatalf("quality recovery due=%#v err=%v", due, err)
	}
}

func TestPostgresStoreDemotesPromotedOfficialAggregatorButKeepsOwnedATS(t *testing.T) {
	database, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	for _, candidate := range []DiscoveryCandidate{
		{ID: "market-builtin", Name: "BuiltinChicago", Website: "https://www.builtinchicago.org", Tags: []string{"auto-market-search"}},
		{ID: "market-imc", Name: "IMC Trading", Website: "https://www.canarywharfian.co.uk", Tags: []string{"priority-1", "quant", "quant-benchmark-2026"}},
	} {
		if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{candidate}); err != nil {
			t.Fatal(err)
		}
	}
	for _, values := range []struct{ id, candidateID, company, provider, route string }{
		{"builtin-source", "market-builtin", "BuiltinChicago", "official_careers", "https://www.builtinchicago.org"},
		{"imc-source", "market-imc", "IMC Trading", "greenhouse", "https://job-boards.greenhouse.io/imc"},
	} {
		if _, err := database.ExecContext(ctx, `INSERT INTO `+store.table("discovered_sources")+`
            (id, candidate_id, company, provider, url, state, last_checked_at)
            VALUES ($1,$2,$3,$4,$5,'promoted',$6)`, values.id, values.candidateID, values.company, values.provider, values.route, now); err != nil {
			t.Fatal(err)
		}
	}

	if demoted, err := store.DemoteUnhealthyDiscoveredSources(ctx, 3, now.Add(time.Minute)); err != nil || demoted != 1 {
		t.Fatalf("demoted=%d err=%v", demoted, err)
	}
	var builtinState, imcState string
	if err := database.QueryRowContext(ctx, `SELECT state FROM `+store.table("discovered_sources")+` WHERE id='builtin-source'`).Scan(&builtinState); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `SELECT state FROM `+store.table("discovered_sources")+` WHERE id='imc-source'`).Scan(&imcState); err != nil {
		t.Fatal(err)
	}
	if builtinState != "rejected" || imcState != "promoted" {
		t.Fatalf("builtin=%q imc=%q", builtinState, imcState)
	}
}

func TestPostgresStoreDiscoveryQuarantinesLegacyAmbiguousCandidateRoute(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidate := DiscoveryCandidate{ID: "millennium", Name: "Millennium", Website: "https://www.mlp.com", Tags: []string{"priority-1", "quant", "quant-benchmark-2026"}}
	if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{candidate}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO "`+store.Schema()+`"."discovered_sources"
    (id, candidate_id, company, provider, url, state, last_checked_at)
VALUES ('legacy-millennium-health', 'millennium', 'Millennium', 'workable',
        'https://apply.workable.com/millennium-health', 'candidate', $1)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE "`+store.Schema()+`"."discovery_candidates" SET state = 'validating' WHERE id = 'millennium'`); err != nil {
		t.Fatal(err)
	}
	demoted, err := store.DemoteUnhealthyDiscoveredSources(ctx, 3, now.Add(time.Minute))
	if err != nil || demoted != 1 {
		t.Fatalf("demoted=%d err=%v", demoted, err)
	}
	var sourceState, candidateState string
	if err := db.QueryRowContext(ctx, `SELECT state FROM "`+store.Schema()+`"."discovered_sources" WHERE id = 'legacy-millennium-health'`).Scan(&sourceState); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT state FROM "`+store.Schema()+`"."discovery_candidates" WHERE id = 'millennium'`).Scan(&candidateState); err != nil {
		t.Fatal(err)
	}
	if sourceState != "unhealthy" || candidateState != "retry" {
		t.Fatalf("source=%q candidate=%q", sourceState, candidateState)
	}
}

func TestPostgresStoreListsLegacyReadOnlySchemaWithoutCountry(t *testing.T) {
	database, store := integrationStore(t)
	ctx := context.Background()
	observedAt := time.Now().UTC().Truncate(time.Microsecond)
	if _, created, err := store.Observe(ctx, Observation{
		SourceID: "legacy:jobs", SourceNativeID: "legacy-1", Company: "Legacy Systems",
		Title: "Software Engineer Intern", Location: "Seattle, WA", Country: "US",
		EmploymentType: "Internship", Level: "internship",
		ApplyURL: "https://jobs.example/legacy", ObservedAt: observedAt,
	}); err != nil || !created {
		t.Fatalf("observe created=%v err=%v", created, err)
	}
	if _, err := database.ExecContext(ctx, `ALTER TABLE `+store.table("jobs")+` DROP COLUMN country`); err != nil {
		t.Fatal(err)
	}

	postings, err := store.ListPostings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 1 || postings[0].Company != "Legacy Systems" || postings[0].Country != "" {
		t.Fatalf("unexpected legacy feed: %#v", postings)
	}
}

func TestPostgresStoreKeepsDistinctRequisitionsWithSameGenericFields(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	base := Observation{
		SourceID: "greenhouse:acme", Company: "Acme", Title: "Software Engineer",
		Location: "New York, NY", ObservedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
	first := base
	first.SourceNativeID = "req-100"
	first.ApplyURL = "https://boards.example/acme/apply"
	second := base
	second.SourceNativeID = "req-200"
	second.ApplyURL = "https://boards.example/acme/apply"

	firstPosting, firstCreated, firstDelivery, firstDeliveryCreated, err := store.ObserveAndEnqueue(ctx, first, &DeliveryTarget{Channel: "telegram", Recipient: "chat-1"})
	if err != nil || !firstCreated || !firstDeliveryCreated {
		t.Fatalf("first requisition created=%v deliveryCreated=%v err=%v", firstCreated, firstDeliveryCreated, err)
	}
	secondPosting, secondCreated, secondDelivery, secondDeliveryCreated, err := store.ObserveAndEnqueue(ctx, second, &DeliveryTarget{Channel: "telegram", Recipient: "chat-1"})
	if err != nil || !secondCreated || !secondDeliveryCreated {
		t.Fatalf("second requisition created=%v deliveryCreated=%v err=%v", secondCreated, secondDeliveryCreated, err)
	}
	if firstPosting.ID == secondPosting.ID {
		t.Fatal("distinct requisitions with generic matching fields collapsed into one job")
	}
	if firstDelivery.ID == secondDelivery.ID || firstDelivery.JobID == secondDelivery.JobID {
		t.Fatalf("distinct requisitions shared delivery: first=%#v second=%#v", firstDelivery, secondDelivery)
	}
}

func TestPostgresStoreKeepsDifferentCompaniesWithSharedGenericURLDistinct(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	sharedURL := "https://apply.example/jobs/apply?utm_source=board"

	alpha, alphaCreated, alphaDelivery, alphaDeliveryCreated, err := store.ObserveAndEnqueue(ctx, Observation{
		SourceID: "greenhouse:alpha", SourceNativeID: "alpha-1", Company: "Alpha Labs",
		Title: "Software Engineer", ApplyURL: sharedURL,
	}, &DeliveryTarget{Channel: "telegram", Recipient: "chat-shared"})
	if err != nil || !alphaCreated || !alphaDeliveryCreated {
		t.Fatalf("alpha created=%v deliveryCreated=%v err=%v", alphaCreated, alphaDeliveryCreated, err)
	}
	beta, betaCreated, betaDelivery, betaDeliveryCreated, err := store.ObserveAndEnqueue(ctx, Observation{
		SourceID: "lever:beta", SourceNativeID: "beta-1", Company: "Beta Systems",
		Title: "Software Engineer", ApplyURL: sharedURL,
	}, &DeliveryTarget{Channel: "telegram", Recipient: "chat-shared"})
	if err != nil || !betaCreated || !betaDeliveryCreated {
		t.Fatalf("beta created=%v deliveryCreated=%v err=%v", betaCreated, betaDeliveryCreated, err)
	}
	if alpha.ID == beta.ID || alphaDelivery.ID == betaDelivery.ID || alphaDelivery.JobID == betaDelivery.JobID {
		t.Fatalf("different companies converged: alpha=%#v/%#v beta=%#v/%#v", alpha, alphaDelivery, beta, betaDelivery)
	}
	if jobs, deliveries := countAtomicRows(t, ctx, db, store.Schema()); jobs != 2 || deliveries != 2 {
		t.Fatalf("shared generic URL rows jobs=%d deliveries=%d, want 2/2", jobs, deliveries)
	}
}

func TestPostgresStoreKeepsURLOnlyDifferentCompaniesDistinct(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	sharedURL := "https://apply.example/jobs/generic?source=board"

	alphaObservation := Observation{
		SourceID: "careers:alpha", Company: "Alpha Labs", Title: "Software Engineer", ApplyURL: sharedURL,
	}
	alpha, alphaCreated, alphaDelivery, alphaDeliveryCreated, err := store.ObserveAndEnqueue(ctx, alphaObservation, &DeliveryTarget{Channel: "telegram", Recipient: "chat-url-only"})
	if err != nil || !alphaCreated || !alphaDeliveryCreated {
		t.Fatalf("alpha created=%v deliveryCreated=%v err=%v", alphaCreated, alphaDeliveryCreated, err)
	}
	alphaAlias := alphaObservation
	alphaAlias.SourceID = "mirror:alpha"
	alphaAlias.Company = "ALPHA LABS"
	alphaReplay, alphaReplayCreated, alphaReplayDelivery, alphaReplayDeliveryCreated, err := store.ObserveAndEnqueue(ctx, alphaAlias, &DeliveryTarget{Channel: "telegram", Recipient: "chat-url-only"})
	if err != nil || alphaReplayCreated || alphaReplayDeliveryCreated || alphaReplay.ID != alpha.ID || alphaReplayDelivery.ID != alphaDelivery.ID {
		t.Fatalf("same-company URL-only alias posting=%#v created=%v delivery=%#v deliveryCreated=%v err=%v", alphaReplay, alphaReplayCreated, alphaReplayDelivery, alphaReplayDeliveryCreated, err)
	}
	betaObservation := Observation{
		SourceID: "careers:beta", Company: "Beta Systems", Title: "Software Engineer", ApplyURL: sharedURL,
	}
	beta, betaCreated, betaDelivery, betaDeliveryCreated, err := store.ObserveAndEnqueue(ctx, betaObservation, &DeliveryTarget{Channel: "telegram", Recipient: "chat-url-only"})
	if err != nil || !betaCreated || !betaDeliveryCreated {
		t.Fatalf("beta created=%v deliveryCreated=%v err=%v", betaCreated, betaDeliveryCreated, err)
	}
	if alpha.ID == beta.ID || alphaDelivery.ID == betaDelivery.ID || alphaDelivery.JobID == betaDelivery.JobID {
		t.Fatalf("URL-only companies converged: alpha=%#v/%#v beta=%#v/%#v", alpha, alphaDelivery, beta, betaDelivery)
	}
	if jobs, deliveries := countAtomicRows(t, ctx, db, store.Schema()); jobs != 2 || deliveries != 2 {
		t.Fatalf("URL-only shared URL rows jobs=%d deliveries=%d, want 2/2", jobs, deliveries)
	}

	betaReplay, replayCreated, betaReplayDelivery, replayDeliveryCreated, err := store.ObserveAndEnqueue(ctx, betaObservation, &DeliveryTarget{Channel: "telegram", Recipient: "chat-url-only"})
	if err != nil || replayCreated || replayDeliveryCreated || betaReplay.ID != beta.ID || betaReplayDelivery.ID != betaDelivery.ID {
		t.Fatalf("beta replay posting=%#v created=%v delivery=%#v deliveryCreated=%v err=%v", betaReplay, replayCreated, betaReplayDelivery, replayDeliveryCreated, err)
	}
}

func TestPostgresStoreConvergesCompanySpacingAliasesWithSameApplyURL(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	target := &DeliveryTarget{Channel: "telegram", Recipient: "chat-company-alias"}
	applyURL := "https://www.citadelsecurities.com/careers/details/software-engineer-intern-us"

	canonical, canonicalCreated, canonicalDelivery, canonicalDeliveryCreated, err := store.ObserveAndEnqueue(ctx, Observation{
		SourceID: "citadel-careers", SourceNativeID: "citadel-software-engineer-intern-us",
		Company: "Citadel Securities", Title: "Software Engineer - Intern (US)", ApplyURL: applyURL,
	}, target)
	if err != nil || !canonicalCreated || !canonicalDeliveryCreated {
		t.Fatalf("canonical created=%v deliveryCreated=%v err=%v", canonicalCreated, canonicalDeliveryCreated, err)
	}
	alias, aliasCreated, aliasDelivery, aliasDeliveryCreated, err := store.ObserveAndEnqueue(ctx, Observation{
		SourceID: "auto-citadelsecurities", SourceNativeID: "software-engineer-intern-us",
		Company: "Citadelsecurities", Title: "Software Engineer – Intern (US)", ApplyURL: applyURL,
	}, target)
	if err != nil || aliasCreated || aliasDeliveryCreated {
		t.Fatalf("alias created=%v deliveryCreated=%v err=%v", aliasCreated, aliasDeliveryCreated, err)
	}
	if alias.ID != canonical.ID || aliasDelivery.ID != canonicalDelivery.ID {
		t.Fatalf("company alias split posting/delivery: canonical=%#v/%#v alias=%#v/%#v", canonical, canonicalDelivery, alias, aliasDelivery)
	}
	if jobs, deliveries := countAtomicRows(t, ctx, db, store.Schema()); jobs != 1 || deliveries != 1 {
		t.Fatalf("company alias rows jobs=%d deliveries=%d, want 1/1", jobs, deliveries)
	}
}

func TestPostgresStoreRepairsLegacyCompanySpacingURLAliases(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	const (
		canonicalID = "legacy-citadel-canonical"
		duplicateID = "legacy-citadel-spacing"
		applyURL    = "https://www.citadelsecurities.com/careers/details/software-engineer-intern-us"
	)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.ExecContext(ctx, `
INSERT INTO `+store.table("jobs")+` (id, company, title, location, employment_type, level, apply_url, first_seen_at, last_seen_at)
VALUES
  ($1, 'Citadel Securities', 'Software Engineer - Intern (US)', 'United States', 'Intern', 'internship', $3, $4, $4),
  ($2, 'Citadelsecurities', 'Software Engineer – Intern (US)', 'United States', 'Intern', 'internship', $3, $5, $5)`,
		canonicalID, duplicateID, applyURL, now.Add(-time.Hour), now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO `+store.table("job_identities")+` (identity_key, job_id) VALUES
  ($1, $6), ($2, $6), ($3, $6), ($4, $7), ($5, $7)`,
		"company-url:citadel securities|url:"+applyURL,
		"native:citadel securities careers:citadel-software-engineer-intern-us",
		"url:"+applyURL,
		"company-url:citadelsecurities|url:"+applyURL,
		"native:auto market citadelsecurities:software-engineer-intern-us",
		canonicalID, duplicateID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO `+store.table("deliveries")+` (job_id, channel, recipient, payload, status, sent_at)
VALUES
  ($1, 'telegram', '@earlycareerradar', jsonb_build_object('ID', $1::text), 'sent', $3),
  ($2, 'telegram', '@earlycareerradar', jsonb_build_object('ID', $2::text), 'pending', NULL)`,
		canonicalID, duplicateID, now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	posting, created, delivery, deliveryCreated, err := store.ObserveAndEnqueue(ctx, Observation{
		SourceID: "citadel securities careers", SourceNativeID: "citadel-software-engineer-intern-us",
		Company: "Citadel Securities", Title: "Software Engineer - Intern (US)", Location: "United States",
		EmploymentType: "Intern", Level: "internship", ApplyURL: applyURL, ObservedAt: now,
	}, &DeliveryTarget{Channel: "telegram", Recipient: "@earlycareerradar"})
	if err != nil || created || deliveryCreated {
		t.Fatalf("repair created=%v deliveryCreated=%v err=%v", created, deliveryCreated, err)
	}
	if posting.ID != canonicalID || delivery.JobID != canonicalID || delivery.Status != "sent" {
		t.Fatalf("repaired posting/delivery=%#v/%#v", posting, delivery)
	}
	if jobs, deliveries := countAtomicRows(t, ctx, db, store.Schema()); jobs != 1 || deliveries != 1 {
		t.Fatalf("legacy company alias rows jobs=%d deliveries=%d, want 1/1", jobs, deliveries)
	}
	var duplicateRows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+store.table("jobs")+` WHERE id = $1`, duplicateID).Scan(&duplicateRows); err != nil {
		t.Fatal(err)
	}
	if duplicateRows != 0 {
		t.Fatalf("legacy duplicate rows=%d, want 0", duplicateRows)
	}
}

func TestPostgresStoreNativeEnrichmentConvergesWithCompanyURLIdentity(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	sharedURL := "https://apply.example/jobs/shared?utm_source=generic"
	target := &DeliveryTarget{Channel: "telegram", Recipient: "chat-enrichment"}

	_, alphaCreated, _, alphaDeliveryCreated, err := store.ObserveAndEnqueue(ctx, Observation{
		SourceID: "careers:alpha", Company: "Alpha Labs", Title: "Software Engineer", ApplyURL: sharedURL,
	}, target)
	if err != nil || !alphaCreated || !alphaDeliveryCreated {
		t.Fatalf("alpha created=%v deliveryCreated=%v err=%v", alphaCreated, alphaDeliveryCreated, err)
	}
	betaObservation := Observation{
		SourceID: "careers:beta", Company: "Beta Systems", Title: "Software Engineer", ApplyURL: sharedURL,
	}
	beta, betaCreated, betaDelivery, betaDeliveryCreated, err := store.ObserveAndEnqueue(ctx, betaObservation, target)
	if err != nil || !betaCreated || !betaDeliveryCreated {
		t.Fatalf("beta URL-only created=%v deliveryCreated=%v err=%v", betaCreated, betaDeliveryCreated, err)
	}

	betaEnriched := betaObservation
	betaEnriched.SourceID = "greenhouse:beta"
	betaEnriched.SourceNativeID = "Beta-Req-42"
	betaEnriched.Title = "Software Engineer, Platform"
	enriched, enrichedCreated, enrichedDelivery, enrichedDeliveryCreated, err := store.ObserveAndEnqueue(ctx, betaEnriched, target)
	if err != nil || enrichedCreated || enrichedDeliveryCreated {
		t.Fatalf("beta enrichment created=%v deliveryCreated=%v err=%v", enrichedCreated, enrichedDeliveryCreated, err)
	}
	if enriched.ID != beta.ID || enrichedDelivery.ID != betaDelivery.ID {
		t.Fatalf("beta enrichment split posting/delivery: before=%#v/%#v after=%#v/%#v", beta, betaDelivery, enriched, enrichedDelivery)
	}

	var betaJobs, betaDeliveries int
	if err := db.QueryRowContext(ctx, `
SELECT
  (SELECT count(*) FROM "`+store.Schema()+`"."jobs" WHERE lower(company) = 'beta systems'),
  (SELECT count(*) FROM "`+store.Schema()+`"."deliveries" d JOIN "`+store.Schema()+`"."jobs" j ON j.id = d.job_id WHERE lower(j.company) = 'beta systems')`).Scan(&betaJobs, &betaDeliveries); err != nil {
		t.Fatal(err)
	}
	if betaJobs != 1 || betaDeliveries != 1 {
		t.Fatalf("Beta rows jobs=%d deliveries=%d, want 1/1", betaJobs, betaDeliveries)
	}
}

func TestPostgresStoreEnsureSchemaMigratesDeliveryUniquenessAndAllowsChannelsConcurrently(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	posting, created, err := store.Observe(ctx, Observation{
		SourceID: "ashby:channels", SourceNativeID: "channel-1", Company: "Channels Co",
		Title: "Backend Engineer", ApplyURL: "https://jobs.channels.test/1",
	})
	if err != nil || !created {
		t.Fatalf("observe created=%v err=%v", created, err)
	}

	if _, err := db.ExecContext(ctx, `DROP INDEX "`+store.Schema()+`".lite_deliveries_job_channel_recipient_uidx`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE "`+store.Schema()+`"."deliveries" ADD CONSTRAINT deliveries_job_id_recipient_key UNIQUE (job_id, recipient)`); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("migrate old delivery uniqueness: %v", err)
	}

	type result struct {
		delivery Delivery
		created  bool
		err      error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, channel := range []string{"log", "telegram"} {
		wg.Add(1)
		go func(channel string) {
			defer wg.Done()
			delivery, deliveryCreated, enqueueErr := store.EnqueueDelivery(ctx, posting.ID, channel, "same-recipient", nil)
			results <- result{delivery: delivery, created: deliveryCreated, err: enqueueErr}
		}(channel)
	}
	wg.Wait()
	close(results)
	seen := make(map[string]Delivery)
	for result := range results {
		if result.err != nil || !result.created {
			t.Fatalf("enqueue delivery=%#v created=%v err=%v", result.delivery, result.created, result.err)
		}
		seen[result.delivery.Channel] = result.delivery
	}
	if len(seen) != 2 || seen["log"].ID == seen["telegram"].ID {
		t.Fatalf("channel deliveries = %#v", seen)
	}
}

func TestPostgresDeliveryDrainerClaimsOnlyConfiguredChannelAndRecipient(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	posting, created, err := store.Observe(ctx, Observation{
		SourceID: "ashby:claim-target", SourceNativeID: "claim-target-1", Company: "Target Co",
		Title: "Backend Engineer", ApplyURL: "https://jobs.target.test/1",
	})
	if err != nil || !created {
		t.Fatalf("observe created=%v err=%v", created, err)
	}
	exact, exactCreated, err := store.EnqueueDelivery(ctx, posting.ID, "telegram", "new-recipient", nil)
	if err != nil || !exactCreated {
		t.Fatalf("exact delivery created=%v err=%v", exactCreated, err)
	}
	wrongChannel, wrongChannelCreated, err := store.EnqueueDelivery(ctx, posting.ID, "log", "new-recipient", nil)
	if err != nil || !wrongChannelCreated {
		t.Fatalf("wrong-channel delivery created=%v err=%v", wrongChannelCreated, err)
	}
	wrongRecipient, wrongRecipientCreated, err := store.EnqueueDelivery(ctx, posting.ID, "telegram", "old-recipient", nil)
	if err != nil || !wrongRecipientCreated {
		t.Fatalf("wrong-recipient delivery created=%v err=%v", wrongRecipientCreated, err)
	}

	var sent []int64
	drainer := DeliveryDrainer{
		Store: store, Owner: "target-worker", Channel: "telegram", Recipient: "new-recipient", Limit: 10,
		Sender: senderFunc(func(_ context.Context, delivery Delivery) error {
			sent = append(sent, delivery.ID)
			return nil
		}),
	}
	report, err := drainer.Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Claimed != 1 || report.Sent != 1 || len(sent) != 1 || sent[0] != exact.ID {
		t.Fatalf("report=%#v sent=%v exact=%d", report, sent, exact.ID)
	}

	for _, delivery := range []Delivery{wrongChannel, wrongRecipient} {
		var status, owner string
		var attempts int
		if err := db.QueryRowContext(ctx, `SELECT status, attempts, claim_owner FROM "`+store.Schema()+`"."deliveries" WHERE id = $1`, delivery.ID).Scan(&status, &attempts, &owner); err != nil {
			t.Fatal(err)
		}
		if status != "pending" || attempts != 0 || owner != "" {
			t.Fatalf("non-target delivery %d status=%s attempts=%d owner=%q", delivery.ID, status, attempts, owner)
		}
	}
}

func TestPostgresDeliveryRetriesBeyondFiveFailuresThenSendsFromSameRow(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	delivery := enqueueTestDelivery(t, ctx, store, "retry-beyond-five")
	now := time.Now().UTC().Truncate(time.Microsecond)
	sendCalls, successfulSends := 0, 0
	drainer := DeliveryDrainer{
		Store: store, Owner: "retry-worker", Channel: "telegram", Recipient: "chat-1", Limit: 1,
		RetryDelay: time.Hour, Now: func() time.Time { return now },
		Sender: senderFunc(func(context.Context, Delivery) error {
			sendCalls++
			if sendCalls <= 6 {
				return errors.New("managed transport outage")
			}
			successfulSends++
			return nil
		}),
	}

	for attempt := 0; attempt < 6; attempt++ {
		report, err := drainer.Drain(ctx)
		if err != nil || report.Claimed != 1 || report.Failed != 1 || report.Sent != 0 {
			t.Fatalf("attempt %d report=%#v err=%v", attempt+1, report, err)
		}
		var status string
		var attempts int
		var retryAt time.Time
		if err := db.QueryRowContext(ctx, `SELECT status, attempts, next_attempt_at FROM "`+store.Schema()+`"."deliveries" WHERE id = $1`, delivery.ID).Scan(&status, &attempts, &retryAt); err != nil {
			t.Fatal(err)
		}
		wantDelay := deliveryRetryDelay(time.Hour, attempt)
		if status != "pending" || attempts != attempt+1 || !retryAt.Equal(now.Add(wantDelay)) {
			t.Fatalf("attempt %d row=%s/%d retry=%s, want pending/%d retry=%s", attempt+1, status, attempts, retryAt, attempt+1, now.Add(wantDelay))
		}
		if attempt >= 3 && wantDelay != maxDeliveryRetryDelay {
			t.Fatalf("attempt %d delay=%s, want capped %s", attempt+1, wantDelay, maxDeliveryRetryDelay)
		}
		if _, err := db.ExecContext(ctx, `UPDATE "`+store.Schema()+`"."deliveries" SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, delivery.ID); err != nil {
			t.Fatal(err)
		}
	}

	report, err := drainer.Drain(ctx)
	if err != nil || report.Claimed != 1 || report.Sent != 1 || report.Failed != 0 {
		t.Fatalf("success report=%#v err=%v", report, err)
	}
	var rowCount, attempts int
	var status string
	if err := db.QueryRowContext(ctx, `SELECT count(*), max(status), max(attempts) FROM "`+store.Schema()+`"."deliveries" WHERE id = $1`, delivery.ID).Scan(&rowCount, &status, &attempts); err != nil {
		t.Fatal(err)
	}
	if sendCalls != 7 || successfulSends != 1 || rowCount != 1 || status != "sent" || attempts != 7 {
		t.Fatalf("calls=%d successes=%d rows=%d status=%s attempts=%d", sendCalls, successfulSends, rowCount, status, attempts)
	}
	if replay, created, err := store.EnqueueDelivery(ctx, delivery.JobID, "telegram", "chat-1", nil); err != nil || created || replay.ID != delivery.ID || replay.Status != "sent" {
		t.Fatalf("sent replay=%#v created=%v err=%v", replay, created, err)
	}
}

func TestPostgresDeliveryRevivesOnlyDueLegacyFailedRowsForExactTarget(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	posting, created, err := store.Observe(ctx, Observation{
		SourceID: "ashby:legacy-retry", SourceNativeID: "legacy-1", Company: "Legacy Retry",
		Title: "Software Engineer Intern", ApplyURL: "https://jobs.legacy-retry.test/1",
	})
	if err != nil || !created {
		t.Fatalf("observe created=%v err=%v", created, err)
	}
	exact, _, err := store.EnqueueDelivery(ctx, posting.ID, "telegram", "current-chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	wrongChannel, _, err := store.EnqueueDelivery(ctx, posting.ID, "log", "current-chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	wrongRecipient, _, err := store.EnqueueDelivery(ctx, posting.ID, "telegram", "old-chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE "`+store.Schema()+`"."deliveries" SET status = 'failed', attempts = 12, next_attempt_at = now() - interval '1 minute' WHERE id = ANY($1)`, pq.Array([]int64{exact.ID, wrongChannel.ID, wrongRecipient.ID})); err != nil {
		t.Fatal(err)
	}

	var sent []int64
	drainer := DeliveryDrainer{
		Store: store, Owner: "upgrade-worker", Channel: "telegram", Recipient: "current-chat", Limit: 10,
		Sender: senderFunc(func(_ context.Context, delivery Delivery) error {
			sent = append(sent, delivery.ID)
			return nil
		}),
	}
	report, err := drainer.Drain(ctx)
	if err != nil || report.Claimed != 1 || report.Sent != 1 || len(sent) != 1 || sent[0] != exact.ID {
		t.Fatalf("report=%#v sent=%v err=%v", report, sent, err)
	}
	for _, untouched := range []Delivery{wrongChannel, wrongRecipient} {
		var status, owner string
		var attempts int
		if err := db.QueryRowContext(ctx, `SELECT status, attempts, claim_owner FROM "`+store.Schema()+`"."deliveries" WHERE id = $1`, untouched.ID).Scan(&status, &attempts, &owner); err != nil {
			t.Fatal(err)
		}
		if status != "failed" || attempts != 12 || owner != "" {
			t.Fatalf("non-target %d status=%s attempts=%d owner=%q", untouched.ID, status, attempts, owner)
		}
	}
}

func TestPostgresStoreConcurrentObserveAndDeliveryClaim(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	observedAt := time.Now().UTC().Truncate(time.Microsecond)

	const concurrency = 8
	ids := make(chan string, concurrency)
	decisionCreations := make(chan bool, concurrency)
	errs := make(chan error, concurrency)
	var wg sync.WaitGroup
	for index := 0; index < concurrency; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			posting, _, _, deliveryCreated, err := store.ObserveAndEnqueue(ctx, Observation{
				SourceID: fmt.Sprintf("source-%d", index), SourceNativeID: fmt.Sprint(index),
				Company: "Concurrent Corp", Title: "Backend Engineer Intern", Location: "Remote",
				ApplyURL: fmt.Sprintf("https://jobs.example/shared/42?utm_source=source-%d", index), ObservedAt: observedAt,
			}, &DeliveryTarget{Channel: "telegram", Recipient: "chat-1"})
			if err != nil {
				errs <- err
				return
			}
			ids <- posting.ID
			decisionCreations <- deliveryCreated
		}(index)
	}
	wg.Wait()
	close(ids)
	close(decisionCreations)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var canonicalID string
	for id := range ids {
		if canonicalID == "" {
			canonicalID = id
		} else if id != canonicalID {
			t.Fatalf("concurrent observations split into %q and %q", canonicalID, id)
		}
	}
	createdDecisions := 0
	for created := range decisionCreations {
		if created {
			createdDecisions++
		}
	}
	if createdDecisions != 1 {
		t.Fatalf("concurrent observations created %d delivery decisions, want 1", createdDecisions)
	}

	delivery, created, err := store.EnqueueDelivery(ctx, canonicalID, "telegram", "chat-1", json.RawMessage(`{"text":"job"}`))
	if err != nil || created {
		t.Fatalf("enqueue created=%v err=%v", created, err)
	}
	if replay, replayCreated, err := store.EnqueueDelivery(ctx, canonicalID, "telegram", "chat-1", nil); err != nil || replayCreated || replay.ID != delivery.ID {
		t.Fatalf("replay delivery=%#v created=%v err=%v", replay, replayCreated, err)
	}

	type claimResult struct {
		deliveries []Delivery
		err        error
	}
	claims := make(chan claimResult, 2)
	for _, owner := range []string{"worker-a", "worker-b"} {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			claimed, err := store.ClaimDeliveries(ctx, owner, "telegram", "chat-1", 1, time.Minute)
			claims <- claimResult{deliveries: claimed, err: err}
		}(owner)
	}
	wg.Wait()
	close(claims)
	var claimed []Delivery
	for result := range claims {
		if result.err != nil {
			t.Fatal(result.err)
		}
		claimed = append(claimed, result.deliveries...)
	}
	if len(claimed) != 1 {
		t.Fatalf("atomic claims returned %d deliveries, want 1", len(claimed))
	}
	if err := store.MarkDeliverySent(ctx, claimed[0].ID, claimed[0].ClaimOwner); err != nil {
		t.Fatal(err)
	}
	if replay, replayCreated, err := store.EnqueueDelivery(ctx, canonicalID, "telegram", "chat-1", nil); err != nil || replayCreated || replay.Status != "sent" {
		t.Fatalf("sent replay delivery=%#v created=%v err=%v", replay, replayCreated, err)
	}
}

func TestPostgresStoreObserveAndEnqueueIsAtomicAndReplaySafe(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	observation := Observation{
		SourceID: "ashby:atomic", SourceNativeID: "atomic-1", Company: "Atomic AI",
		Title: "Software Engineer Intern", Location: "Remote", EmploymentType: "Internship",
		ApplyURL: "https://jobs.atomic.test/1", ObservedAt: time.Now().UTC().Truncate(time.Microsecond),
	}

	if _, _, _, _, err := store.ObserveAndEnqueue(ctx, observation, &DeliveryTarget{Channel: "telegram"}); err == nil {
		t.Fatal("expected invalid delivery target to fail")
	}
	jobs, deliveries := countAtomicRows(t, ctx, db, store.Schema())
	if jobs != 0 || deliveries != 0 {
		t.Fatalf("invalid target left jobs=%d deliveries=%d, want 0/0", jobs, deliveries)
	}

	posting, created, delivery, deliveryCreated, err := store.ObserveAndEnqueue(ctx, observation, &DeliveryTarget{Channel: "telegram", Recipient: "chat-atomic"})
	if err != nil || !created || !deliveryCreated {
		t.Fatalf("atomic first sighting created=%v deliveryCreated=%v err=%v", created, deliveryCreated, err)
	}
	if delivery.JobID != posting.ID || delivery.Status != "pending" {
		t.Fatalf("atomic delivery = %#v, posting = %#v", delivery, posting)
	}
	if jobs, deliveries = countAtomicRows(t, ctx, db, store.Schema()); jobs != 1 || deliveries != 1 {
		t.Fatalf("atomic rows jobs=%d deliveries=%d, want 1/1", jobs, deliveries)
	}

	replayed, replayCreated, replayDelivery, replayDeliveryCreated, err := store.ObserveAndEnqueue(ctx, observation, &DeliveryTarget{Channel: "telegram", Recipient: "chat-atomic"})
	if err != nil || replayCreated || replayDeliveryCreated {
		t.Fatalf("replay created=%v deliveryCreated=%v err=%v", replayCreated, replayDeliveryCreated, err)
	}
	if replayed.ID != posting.ID || replayDelivery.ID != delivery.ID || replayDelivery.Status != "pending" {
		t.Fatalf("replay posting=%#v delivery=%#v", replayed, replayDelivery)
	}
	if jobs, deliveries = countAtomicRows(t, ctx, db, store.Schema()); jobs != 1 || deliveries != 1 {
		t.Fatalf("replay rows jobs=%d deliveries=%d, want 1/1", jobs, deliveries)
	}
}

func TestPostgresStoreFinalizesSourceVisibilityAtomically(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	observation := Observation{
		SourceID: "ashby:atomic-snapshot", SourceNativeID: "snapshot-1", Company: "Atomic Snapshot AI",
		Title: "Software Engineer Intern", Location: "New York", ApplyURL: "https://jobs.atomic-snapshot.test/1",
		ObservedAt: time.Now().UTC(), SnapshotPending: true,
	}
	posting, _, delivery, _, err := store.ObserveAndEnqueue(ctx, observation, &DeliveryTarget{
		Channel: "telegram", Recipient: "chat-snapshot", Stage: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	broken := SourcePassFinalization{
		SourceID: observation.SourceID, ActiveJobIDs: []string{posting.ID}, DeliveryIDs: []int64{delivery.ID},
		Channel: "telegram", Recipient: "chat-snapshot", ObservedCount: 1, AttemptedAt: observation.ObservedAt,
		BootstrapKey: "snapshot-bootstrap", BootstrapValue: json.RawMessage(`{"state":`),
	}
	if _, err := store.FinalizeSourcePass(ctx, broken); err == nil {
		t.Fatal("invalid bootstrap state should abort source finalization")
	}
	var active bool
	var deliveryStatus string
	if err := db.QueryRowContext(ctx, `SELECT active FROM `+store.table("job_source_observations")+` WHERE job_id = $1`, posting.ID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT status FROM `+store.table("deliveries")+` WHERE id = $1`, delivery.ID).Scan(&deliveryStatus); err != nil {
		t.Fatal(err)
	}
	var statusRows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+store.table("source_status")+` WHERE source_id = $1`, observation.SourceID).Scan(&statusRows); err != nil {
		t.Fatal(err)
	}
	if active || deliveryStatus != "staged" || statusRows != 0 {
		t.Fatalf("aborted finalization leaked state: active=%v delivery=%q status_rows=%d", active, deliveryStatus, statusRows)
	}

	ready := broken
	ready.BootstrapValue = json.RawMessage(`{"state":"ready"}`)
	activated, err := store.FinalizeSourcePass(ctx, ready)
	if err != nil || activated != 1 {
		t.Fatalf("activated=%d err=%v", activated, err)
	}
	var sourceState string
	var bootstrapRows int
	if err := db.QueryRowContext(ctx, `SELECT active FROM `+store.table("job_source_observations")+` WHERE job_id = $1`, posting.ID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT status FROM `+store.table("deliveries")+` WHERE id = $1`, delivery.ID).Scan(&deliveryStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT state FROM `+store.table("source_status")+` WHERE source_id = $1`, observation.SourceID).Scan(&sourceState); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+store.table("bootstrap_state")+` WHERE key = $1`, ready.BootstrapKey).Scan(&bootstrapRows); err != nil {
		t.Fatal(err)
	}
	if !active || deliveryStatus != "pending" || sourceState != "success" || bootstrapRows != 1 {
		t.Fatalf("committed finalization state: active=%v delivery=%q source=%q bootstrap=%d", active, deliveryStatus, sourceState, bootstrapRows)
	}
}

func TestPostgresStoreCanonicalFieldsPreferStrongerSourceAuthority(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	applyURL := "https://jobs.authority.test/role-1"
	strong, created, err := store.Observe(ctx, Observation{
		SourceID: "reviewed-source", SourceNativeID: "reviewed-1", Authority: 10, Company: "Authority AI",
		Title: "Software Engineer Intern", Location: "New York", ApplyURL: applyURL,
	})
	if err != nil || !created {
		t.Fatalf("strong observation created=%v err=%v", created, err)
	}
	weak, created, err := store.Observe(ctx, Observation{
		SourceID: "auto-authority-source", SourceNativeID: "auto-1", Authority: 30, Company: "Authority AI",
		Title: "Join our amazing team", Location: "Everywhere", ApplyURL: applyURL,
	})
	if err != nil || created || weak.ID != strong.ID {
		t.Fatalf("weak observation posting=%#v created=%v err=%v", weak, created, err)
	}
	var title, location, canonicalSource string
	var authority int
	if err := db.QueryRowContext(ctx, `SELECT title, location, canonical_source_id, canonical_authority FROM `+store.table("jobs")+` WHERE id = $1`, strong.ID).Scan(&title, &location, &canonicalSource, &authority); err != nil {
		t.Fatal(err)
	}
	if title != "Software Engineer Intern" || location != "New York" || canonicalSource != "reviewed-source" || authority != 10 {
		t.Fatalf("canonical fields were downgraded: title=%q location=%q source=%q authority=%d", title, location, canonicalSource, authority)
	}
}

func TestPostgresStoreDeliveryClaimRequiresCurrentAdmissionAndActiveSource(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	posting, _, delivery, _, err := store.ObserveAndEnqueue(ctx, Observation{
		SourceID: "greenhouse:delivery-guard", SourceNativeID: "guard-1", Company: "Guard AI",
		Title: "Software Engineer Intern", ApplyURL: "https://jobs.guard.test/1",
	}, &DeliveryTarget{Channel: "telegram", Recipient: "chat-guard"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE `+store.table("jobs")+` SET admission_policy_version = 'superseded' WHERE id = $1`, posting.ID); err != nil {
		t.Fatal(err)
	}
	if next, err := store.NextDeliveryAttemptAt(ctx, "telegram", "chat-guard"); err != nil || next != nil {
		t.Fatalf("superseded delivery wake=%v err=%v", next, err)
	}
	if claimed, err := store.ClaimDeliveries(ctx, "guard-worker", "telegram", "chat-guard", 1, time.Minute); err != nil || len(claimed) != 0 {
		t.Fatalf("superseded delivery claimed=%#v err=%v", claimed, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE `+store.table("jobs")+` SET admission_policy_version = $2 WHERE id = $1`, posting.ID, pipeline.JobAdmissionPolicyVersion); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimDeliveries(ctx, "guard-worker", "telegram", "chat-guard", 1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != delivery.ID {
		t.Fatalf("current delivery claimed=%#v err=%v", claimed, err)
	}
}

func TestPostgresStoreStagedDeliveryRequiresExplicitActivation(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	observation := Observation{
		SourceID: "ashby:staged", SourceNativeID: "staged-1", Company: "Staged AI",
		Title: "Software Engineer Intern", Location: "New York", ApplyURL: "https://jobs.staged.test/1",
	}
	_, _, decision, created, err := store.ObserveAndEnqueue(ctx, observation, &DeliveryTarget{
		Channel: "telegram", Recipient: "chat-staged", Stage: true,
	})
	if err != nil || !created || decision.Status != "staged" {
		t.Fatalf("staged decision=%#v created=%v err=%v", decision, created, err)
	}
	claimed, err := store.ClaimDeliveries(ctx, "worker", "telegram", "chat-staged", 10, time.Minute)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("staged decision was claimable: claimed=%#v err=%v", claimed, err)
	}
	if activated, err := store.ActivateDeliveries(ctx, []int64{decision.ID}, "telegram", "wrong-recipient"); err == nil || activated != 0 {
		t.Fatalf("wrong target activation must fail atomically: activation=%d err=%v", activated, err)
	}
	if activated, err := store.ActivateDeliveries(ctx, []int64{decision.ID, decision.ID}, "telegram", "chat-staged"); err != nil || activated != 1 {
		t.Fatalf("activation=%d err=%v", activated, err)
	}
	if next, err := store.NextDeliveryAttemptAt(ctx, "telegram", "chat-staged"); err != nil || next == nil || next.After(time.Now().UTC()) {
		t.Fatalf("activated delivery next attempt=%v err=%v, want due now", next, err)
	}
	claimed, err = store.ClaimDeliveries(ctx, "worker", "telegram", "chat-staged", 10, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != decision.ID {
		t.Fatalf("activated decision not claimable: claimed=%#v err=%v", claimed, err)
	}
	if next, err := store.NextDeliveryAttemptAt(ctx, "telegram", "chat-staged"); err != nil || next == nil || !next.Equal(claimed[0].ClaimExpiresAt) {
		t.Fatalf("claimed delivery wake=%v err=%v, want lease expiry %s", next, err, claimed[0].ClaimExpiresAt)
	}
	if err := store.MarkDeliverySent(ctx, decision.ID, "worker"); err != nil {
		t.Fatal(err)
	}
	if next, err := store.NextDeliveryAttemptAt(ctx, "telegram", "chat-staged"); err != nil || next != nil {
		t.Fatalf("sent delivery wake=%v err=%v, want none", next, err)
	}
}

func TestPostgresStoreFinalizesActiveSourceSnapshots(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	sharedA, _, err := store.Observe(ctx, Observation{
		SourceID: "ashby:source-a", SourceNativeID: "shared-a", Company: "Shared AI", Title: "Software Engineer Intern",
		ApplyURL: "https://jobs.shared.test/role", ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	sharedB, _, err := store.Observe(ctx, Observation{
		SourceID: "greenhouse:source-b", SourceNativeID: "shared-b", Company: "Shared AI", Title: "Software Engineer Intern",
		ApplyURL: "https://jobs.shared.test/role", ObservedAt: now,
	})
	if err != nil || sharedB.ID != sharedA.ID {
		t.Fatalf("cross-source posting=%#v canonical=%#v err=%v", sharedB, sharedA, err)
	}
	retired, _, err := store.Observe(ctx, Observation{
		SourceID: "ashby:source-a", SourceNativeID: "retired", Company: "Shared AI", Title: "Backend Engineer Intern",
		ApplyURL: "https://jobs.shared.test/retired", ObservedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.FinalizeSourceSnapshot(ctx, "ashby:source-a", nil); err != nil {
		t.Fatal(err)
	}
	postings, err := store.ListPostings(ctx)
	if err != nil || len(postings) != 1 || postings[0].ID != sharedA.ID {
		t.Fatalf("one active cross-source observation should preserve shared job: postings=%#v err=%v", postings, err)
	}
	if err := store.FinalizeSourceSnapshot(ctx, "greenhouse:source-b", nil); err != nil {
		t.Fatal(err)
	}
	postings, err = store.ListPostings(ctx)
	if err != nil || len(postings) != 0 {
		t.Fatalf("fully retired postings remained visible: postings=%#v err=%v", postings, err)
	}

	reappeared := Observation{
		SourceID: "ashby:source-a", SourceNativeID: "retired", Company: "Shared AI", Title: "Backend Engineer Intern",
		ApplyURL: "https://jobs.shared.test/retired", ObservedAt: now.Add(time.Hour), SnapshotPending: true,
	}
	if posting, created, err := store.Observe(ctx, reappeared); err != nil || created || posting.ID != retired.ID {
		t.Fatalf("reappeared posting=%#v created=%v err=%v", posting, created, err)
	}
	postings, err = store.ListPostings(ctx)
	if err != nil || len(postings) != 0 {
		t.Fatalf("pending snapshot reactivated posting before finalization: postings=%#v err=%v", postings, err)
	}
	if err := store.FinalizeSourceSnapshot(ctx, "ashby:source-a", []string{retired.ID}); err != nil {
		t.Fatal(err)
	}
	postings, err = store.ListPostings(ctx)
	if err != nil || len(postings) != 1 || postings[0].ID != retired.ID {
		t.Fatalf("reappeared posting was not reactivated: postings=%#v err=%v", postings, err)
	}

	pending, created, err := store.Observe(ctx, Observation{
		SourceID: "ashby:source-a", SourceNativeID: "pending-new", Company: "Shared AI", Title: "Platform Engineer Intern",
		ApplyURL: "https://jobs.shared.test/pending-new", ObservedAt: now.Add(2 * time.Hour), SnapshotPending: true,
	})
	if err != nil || !created {
		t.Fatalf("pending new posting=%#v created=%v err=%v", pending, created, err)
	}
	postings, err = store.ListPostings(ctx)
	if err != nil || len(postings) != 1 || postings[0].ID != retired.ID {
		t.Fatalf("partial new observation leaked into active feed: postings=%#v err=%v", postings, err)
	}
}

func TestPostgresStoreRevalidatesApplyURLsWithoutHidingTransientFailures(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	posting, created, err := store.Observe(ctx, Observation{
		SourceID: "greenhouse:databricks", SourceNativeID: "greenhouse:8732364002",
		Company: "Databricks", Title: "Software Engineering Intern",
		ApplyURL: "https://databricks.test/company/careers/job?gh_jid=8732364002", ObservedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("observe created=%v err=%v", created, err)
	}
	due, err := store.ListApplyURLsDue(ctx, now, 10)
	if err != nil || len(due) != 1 || due[0].JobID != posting.ID {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	record := func(outcome string, status int, at time.Time) {
		t.Helper()
		if err := store.RecordApplyURLCheck(ctx, ApplyURLCheck{
			JobID: posting.ID, ApplyURL: posting.ApplyURL, Outcome: outcome,
			StatusCode: status, CheckedAt: at, NextCheckAt: at.Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	record(pipeline.ApplyURLGone, 404, now)
	if postings, err := store.ListPostings(ctx); err != nil || len(postings) != 1 {
		t.Fatalf("one terminal result hid posting: postings=%#v err=%v", postings, err)
	}
	record(pipeline.ApplyURLGone, 410, now.Add(time.Hour))
	if postings, err := store.ListPostings(ctx); err != nil || len(postings) != 0 {
		t.Fatalf("confirmed gone posting remained visible: postings=%#v err=%v", postings, err)
	}
	record(pipeline.ApplyURLLive, 200, now.Add(2*time.Hour))
	if postings, err := store.ListPostings(ctx); err != nil || len(postings) != 1 {
		t.Fatalf("recovered posting remained hidden: postings=%#v err=%v", postings, err)
	}

	updated, created, err := store.Observe(ctx, Observation{
		SourceID: "greenhouse:databricks", SourceNativeID: "greenhouse:8732364002",
		Company: "Databricks", Title: "Software Engineering Intern",
		ApplyURL: "https://databricks.test/company/careers/job?gh_jid=9999999999", ObservedAt: now.Add(3 * time.Hour),
	})
	if err != nil || created || updated.ID != posting.ID {
		t.Fatalf("updated posting=%#v created=%v err=%v", updated, created, err)
	}
	due, err = store.ListApplyURLsDue(ctx, now.Add(3*time.Hour), 10)
	if err != nil || len(due) != 1 || due[0].ApplyURL != updated.ApplyURL || due[0].State != pipeline.ApplyURLUnchecked {
		t.Fatalf("refreshed URL not reset for validation: due=%#v err=%v", due, err)
	}
}

func TestPostgresStoreQuarantineRestoreAndExplainAreAuditable(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	posting, created, err := store.Observe(ctx, Observation{
		SourceID: "greenhouse:incident-source", SourceNativeID: "job-1", Company: "Incident Co",
		Title: "Software Engineer Intern", ApplyURL: "https://jobs.incident.test/1", ObservedAt: now,
	})
	if err != nil || !created {
		t.Fatalf("observe posting=%#v created=%v err=%v", posting, created, err)
	}
	if postings, err := store.ListPostings(ctx); err != nil || len(postings) != 1 {
		t.Fatalf("visible before quarantine: postings=%#v err=%v", postings, err)
	}
	if err := store.QuarantineSource(ctx, "greenhouse:incident-source", "copied aggregator inventory", "test-operator", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if postings, err := store.ListPostings(ctx); err != nil || len(postings) != 0 {
		t.Fatalf("quarantined source remained visible: postings=%#v err=%v", postings, err)
	}
	explanation, err := store.ExplainSource(ctx, "greenhouse:incident-source")
	if err != nil || explanation.Control == nil || explanation.Control.State != "quarantined" || len(explanation.Events) != 1 {
		t.Fatalf("explanation=%#v err=%v", explanation, err)
	}
	if err := store.RestoreSource(ctx, "greenhouse:incident-source", "verified company board", "test-operator", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if postings, err := store.ListPostings(ctx); err != nil || len(postings) != 1 {
		t.Fatalf("restored source not visible: postings=%#v err=%v", postings, err)
	}
	explanation, err = store.ExplainSource(ctx, "greenhouse:incident-source")
	if err != nil || explanation.Control.State != "active" || len(explanation.Events) != 2 {
		t.Fatalf("restored explanation=%#v err=%v", explanation, err)
	}
}

func TestPostgresStoreParksTerminalDiscoveryFailureWithEvidence(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	candidate := DiscoveryCandidate{ID: "incident-mployee", Name: "Mployee", Website: "https://mployee.example"}
	if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{candidate}); err != nil {
		t.Fatal(err)
	}
	source := Source{ID: "auto-incident-mployee-greenhouse", Company: "Mployee", Provider: "greenhouse", URL: "https://job-boards.greenhouse.io/mployee"}
	err := store.RecordDiscoveryFailure(ctx, DiscoveryCandidateRecord{DiscoveryCandidate: candidate}, &source,
		errors.New("structured board returned postings but no relevant technical job roles"), now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var candidateState, candidateCode, sourceState, sourceCode string
	if err := db.QueryRowContext(ctx, `SELECT state, failure_code FROM `+store.table("discovery_candidates")+` WHERE id = $1`, candidate.ID).Scan(&candidateState, &candidateCode); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT state, failure_code FROM `+store.table("discovered_sources")+` WHERE id = $1`, source.ID).Scan(&sourceState, &sourceCode); err != nil {
		t.Fatal(err)
	}
	if candidateState != "parked" || sourceState != "rejected" || candidateCode != pipeline.DiscoveryFailureNontechnical || sourceCode != pipeline.DiscoveryFailureNontechnical {
		t.Fatalf("candidate=%s/%s source=%s/%s", candidateState, candidateCode, sourceState, sourceCode)
	}
	var events int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+store.table("discovery_events")+` WHERE candidate_id = $1 AND outcome = 'parked'`, candidate.ID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("events=%d err=%v", events, err)
	}
}

func TestPostgresStorePersistsTelegramReceiptAndParksAmbiguousOutcome(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	delivery := enqueueTestDelivery(t, ctx, store, "receipt")
	claimed, err := store.ClaimDeliveries(ctx, "receipt-worker", "telegram", "chat-1", 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	receipt := pipeline.DeliveryReceipt{Provider: "telegram", ProviderMessageID: "321", ProviderChatID: "-10042", AcceptedAt: time.Now().UTC()}
	if err := store.MarkDeliverySentWithReceipt(ctx, delivery.ID, "receipt-worker", receipt); err != nil {
		t.Fatal(err)
	}
	var status, providerMessageID string
	var storedReceipt []byte
	if err := db.QueryRowContext(ctx, `SELECT status, provider_message_id, receipt FROM `+store.table("deliveries")+` WHERE id = $1`, delivery.ID).Scan(&status, &providerMessageID, &storedReceipt); err != nil {
		t.Fatal(err)
	}
	var decodedReceipt pipeline.DeliveryReceipt
	if err := json.Unmarshal(storedReceipt, &decodedReceipt); err != nil {
		t.Fatal(err)
	}
	if status != "sent" || providerMessageID != "321" || decodedReceipt.ProviderChatID != "-10042" {
		t.Fatalf("status=%s provider_message_id=%s receipt=%s", status, providerMessageID, storedReceipt)
	}

	ambiguous := enqueueTestDelivery(t, ctx, store, "ambiguous")
	claimed, err = store.ClaimDeliveries(ctx, "ambiguous-worker", "telegram", "chat-1", 1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != ambiguous.ID {
		t.Fatalf("ambiguous claim=%#v err=%v", claimed, err)
	}
	if err := store.MarkDeliveryAmbiguous(ctx, ambiguous.ID, "ambiguous-worker", "response lost", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT status FROM `+store.table("deliveries")+` WHERE id = $1`, ambiguous.ID).Scan(&status); err != nil || status != "uncertain" {
		t.Fatalf("ambiguous status=%s err=%v", status, err)
	}
}

func TestOperationalStateReportsOnlyWorkDueNow(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	candidate := DiscoveryCandidate{ID: "future-candidate", Name: "Future Candidate", Website: "https://future.example"}
	if err := store.SeedDiscoveryCandidates(ctx, []DiscoveryCandidate{candidate}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordDiscoveryFailure(ctx, DiscoveryCandidateRecord{DiscoveryCandidate: candidate}, nil, errors.New("temporary network error"), now, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, created, _, deliveryCreated, err := store.ObserveAndEnqueue(ctx, Observation{
		SourceID: "greenhouse:due", SourceNativeID: "due-1", Company: "Due Co", Title: "Software Engineer Intern",
		ApplyURL: "https://jobs.due.test/1", ObservedAt: now,
	}, &DeliveryTarget{Channel: "telegram", Recipient: "due-chat"})
	if err != nil || !created || !deliveryCreated {
		t.Fatalf("created=%v deliveryCreated=%v err=%v", created, deliveryCreated, err)
	}
	state, err := store.ReadOperationalState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.DiscoveryDue != 0 || state.ApplyURLsDue != 1 || state.DeliveriesDue != 1 {
		t.Fatalf("due state = discovery:%d apply:%d delivery:%d", state.DiscoveryDue, state.ApplyURLsDue, state.DeliveriesDue)
	}
}

func TestPostgresStoreSuppressedDecisionStaysSuppressedOnReplay(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	observation := Observation{
		SourceID: "greenhouse:bootstrap", SourceNativeID: "boot-1", Company: "Bootstrap Co",
		Title: "Software Engineer Intern", Location: "Remote", ApplyURL: "https://jobs.bootstrap.test/1",
	}
	target := &DeliveryTarget{Channel: "telegram", Recipient: "chat-bootstrap", Suppress: true}
	posting, created, decision, decisionCreated, err := store.ObserveAndEnqueue(ctx, observation, target)
	if err != nil || !created || !decisionCreated || decision.Status != "suppressed" {
		t.Fatalf("bootstrap decision posting=%#v created=%v decision=%#v decisionCreated=%v err=%v", posting, created, decision, decisionCreated, err)
	}

	replayed, replayCreated, existing, existingCreated, err := store.ObserveAndEnqueue(ctx, observation, &DeliveryTarget{Channel: "telegram", Recipient: "chat-bootstrap"})
	if err != nil || replayCreated || existingCreated {
		t.Fatalf("normal replay posting=%#v created=%v decision=%#v decisionCreated=%v err=%v", replayed, replayCreated, existing, existingCreated, err)
	}
	if existing.ID != decision.ID || existing.Status != "suppressed" {
		t.Fatalf("normal replay changed suppressed decision: before=%#v after=%#v", decision, existing)
	}
	claimed, err := store.ClaimDeliveries(ctx, "worker", "telegram", "chat-bootstrap", 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed %d suppressed deliveries", len(claimed))
	}
}

func TestPostgresStoreExistingJobCanAcquireOnePendingDecision(t *testing.T) {
	db, store := integrationStore(t)
	ctx := context.Background()
	observation := Observation{
		SourceID: "lever:enriched", SourceNativeID: "enriched-1", Company: "Enriched Co",
		Title: "Backend Engineer Intern", Location: "Remote", ApplyURL: "https://jobs.enriched.test/1",
	}
	posting, created, err := store.Observe(ctx, observation)
	if err != nil || !created {
		t.Fatalf("initial observe posting=%#v created=%v err=%v", posting, created, err)
	}

	enriched := observation
	enriched.EmploymentType = "Internship"
	replayed, replayCreated, delivery, deliveryCreated, err := store.ObserveAndEnqueue(ctx, enriched, &DeliveryTarget{Channel: "telegram", Recipient: "chat-enriched"})
	if err != nil || replayCreated || !deliveryCreated {
		t.Fatalf("enriched replay posting=%#v created=%v delivery=%#v deliveryCreated=%v err=%v", replayed, replayCreated, delivery, deliveryCreated, err)
	}
	if replayed.ID != posting.ID || delivery.JobID != posting.ID || delivery.Status != "pending" {
		t.Fatalf("enriched decision posting=%#v delivery=%#v", replayed, delivery)
	}
	jobs, deliveries := countAtomicRows(t, ctx, db, store.Schema())
	if jobs != 1 || deliveries != 1 {
		t.Fatalf("enriched rows jobs=%d deliveries=%d, want 1/1", jobs, deliveries)
	}
}

func TestPostgresDeliveryDrainerCancellationDoesNotOverclaimOrSpendUnsentAttempt(t *testing.T) {
	db, store := integrationStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	first := enqueueTestDelivery(t, ctx, store, "blocked-1")
	second := enqueueTestDelivery(t, ctx, store, "blocked-2")
	started := make(chan struct{})
	drainer := DeliveryDrainer{
		Store: store, Owner: "blocking-worker", Channel: "telegram", Recipient: "chat-1", Limit: 10, RetryDelay: time.Hour,
		Sender: senderFunc(func(sendCtx context.Context, _ Delivery) error {
			close(started)
			<-sendCtx.Done()
			return sendCtx.Err()
		}),
	}
	done := make(chan error, 1)
	go func() {
		_, err := drainer.Drain(ctx)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Drain error = %v, want context canceled", err)
	}

	var firstAttempts, secondAttempts int
	var firstStatus, secondStatus string
	if err := db.QueryRowContext(context.Background(), `
SELECT
  (SELECT attempts FROM "`+store.Schema()+`"."deliveries" WHERE id = $1),
  (SELECT status FROM "`+store.Schema()+`"."deliveries" WHERE id = $1),
  (SELECT attempts FROM "`+store.Schema()+`"."deliveries" WHERE id = $2),
  (SELECT status FROM "`+store.Schema()+`"."deliveries" WHERE id = $2)`, first.ID, second.ID).Scan(&firstAttempts, &firstStatus, &secondAttempts, &secondStatus); err != nil {
		t.Fatal(err)
	}
	if firstAttempts != 1 || firstStatus != "pending" || secondAttempts != 0 || secondStatus != "pending" {
		t.Fatalf("first=%s/%d second=%s/%d", firstStatus, firstAttempts, secondStatus, secondAttempts)
	}
}

func TestPostgresDeliveryDrainerFinalizesSuccessAfterParentCancellation(t *testing.T) {
	db, store := integrationStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	delivery := enqueueTestDelivery(t, ctx, store, "cancel-success")
	drainer := DeliveryDrainer{
		Store: store, Owner: "cancel-worker", Channel: "telegram", Recipient: "chat-1", Limit: 1,
		Sender: senderFunc(func(context.Context, Delivery) error {
			cancel()
			return nil
		}),
	}
	report, err := drainer.Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Sent != 1 {
		t.Fatalf("report = %#v", report)
	}
	var status string
	var attempts int
	var sentAt *time.Time
	if err := db.QueryRowContext(context.Background(), `SELECT status, attempts, sent_at FROM "`+store.Schema()+`"."deliveries" WHERE id = $1`, delivery.ID).Scan(&status, &attempts, &sentAt); err != nil {
		t.Fatal(err)
	}
	if status != "sent" || attempts != 1 || sentAt == nil {
		t.Fatalf("delivery status=%s attempts=%d sentAt=%v", status, attempts, sentAt)
	}
}

func enqueueTestDelivery(t *testing.T, ctx context.Context, store *PostgresStore, nativeID string) Delivery {
	t.Helper()
	_, created, delivery, deliveryCreated, err := store.ObserveAndEnqueue(ctx, Observation{
		SourceID: "test:delivery", SourceNativeID: nativeID, Company: "Delivery Co",
		Title: "Engineer " + nativeID, ApplyURL: "https://jobs.delivery.test/" + nativeID,
	}, &DeliveryTarget{Channel: "telegram", Recipient: "chat-1"})
	if err != nil || !created || !deliveryCreated {
		t.Fatalf("enqueue %q created=%v deliveryCreated=%v err=%v", nativeID, created, deliveryCreated, err)
	}
	return delivery
}

func countAtomicRows(t *testing.T, ctx context.Context, db *sql.DB, schema string) (int, int) {
	t.Helper()
	var jobs, deliveries int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM "`+schema+`"."jobs"), (SELECT count(*) FROM "`+schema+`"."deliveries")`).Scan(&jobs, &deliveries); err != nil {
		t.Fatal(err)
	}
	return jobs, deliveries
}

func TestPostgresStoreSourceZeroFailureAndBootstrapState(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	failedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	if err := store.RecordSourceFailure(ctx, "ashby:example", errors.New("upstream timeout"), failedAt); err != nil {
		t.Fatal(err)
	}
	succeededAt := failedAt.Add(time.Minute)
	if err := store.RecordSourceSuccess(ctx, "ashby:example", 0, succeededAt); err != nil {
		t.Fatal(err)
	}
	status, err := store.GetSourceStatus(ctx, "ashby:example")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "success" || status.ObservedCount != 0 || status.LastSuccessAt == nil || status.ConsecutiveFailures != 0 || status.LastError != "" {
		t.Fatalf("successful zero status = %#v", status)
	}
	if status.LastFailureAt == nil {
		t.Fatal("successful zero should retain prior failure history")
	}

	if err := store.SetBootstrapState(ctx, "catalog-v1", json.RawMessage(`{"loaded":true}`)); err != nil {
		t.Fatal(err)
	}
	state, err := store.GetBootstrapState(ctx, "catalog-v1")
	if err != nil {
		t.Fatal(err)
	}
	if string(state.Value) != `{"loaded": true}` && string(state.Value) != `{"loaded":true}` {
		t.Fatalf("bootstrap value = %s", state.Value)
	}
}

func TestPostgresStoreSourceStatusIgnoresStaleOutcomes(t *testing.T) {
	_, store := integrationStore(t)
	ctx := context.Background()
	older := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
	newer := older.Add(time.Minute)

	t.Run("newer success remains authoritative", func(t *testing.T) {
		const sourceID = "source:stale-failure"
		if err := store.RecordSourceSuccess(ctx, sourceID, 7, newer); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordSourceFailure(ctx, sourceID, errors.New("stale failure"), older); err != nil {
			t.Fatalf("stale failure should be a successful no-op: %v", err)
		}
		status, err := store.GetSourceStatus(ctx, sourceID)
		if err != nil {
			t.Fatal(err)
		}
		if status.State != "success" || status.ObservedCount != 7 || !status.LastAttemptAt.Equal(newer) ||
			status.LastSuccessAt == nil || !status.LastSuccessAt.Equal(newer) || status.LastFailureAt != nil ||
			status.ConsecutiveFailures != 0 || status.LastError != "" {
			t.Fatalf("status after stale failure = %#v", status)
		}
	})

	t.Run("newer failure remains authoritative", func(t *testing.T) {
		const sourceID = "source:stale-success"
		if err := store.RecordSourceFailure(ctx, sourceID, errors.New("current failure"), newer); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordSourceSuccess(ctx, sourceID, 9, older); err != nil {
			t.Fatalf("stale success should be a successful no-op: %v", err)
		}
		status, err := store.GetSourceStatus(ctx, sourceID)
		if err != nil {
			t.Fatal(err)
		}
		if status.State != "failure" || status.ObservedCount != 0 || !status.LastAttemptAt.Equal(newer) ||
			status.LastSuccessAt != nil || status.LastFailureAt == nil || !status.LastFailureAt.Equal(newer) ||
			status.ConsecutiveFailures != 1 || status.LastError != "current failure" {
			t.Fatalf("status after stale success = %#v", status)
		}
	})
}

func integrationStore(t *testing.T) (*sql.DB, *PostgresStore) {
	t.Helper()
	databaseURL := os.Getenv("RADAR_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("RADAR_TEST_DATABASE_URL is not set")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connect to RADAR_TEST_DATABASE_URL: %v", err)
	}
	schema := fmt.Sprintf("radar_lite_test_%d", time.Now().UnixNano())
	store, err := NewPostgresStore(db, PostgresOptions{Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`)
	})
	return db, store
}
