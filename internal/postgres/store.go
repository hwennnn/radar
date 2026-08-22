package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hwennnn/radar/internal/pipeline"
	"github.com/lib/pq"
)

type (
	BootstrapState           = pipeline.BootstrapState
	ApplyURLCandidate        = pipeline.ApplyURLCandidate
	ApplyURLCheck            = pipeline.ApplyURLCheck
	CycleResult              = pipeline.CycleResult
	Delivery                 = pipeline.Delivery
	DeliveryTarget           = pipeline.DeliveryTarget
	DiscoveryCandidate       = pipeline.DiscoveryCandidate
	DiscoveryCandidateRecord = pipeline.DiscoveryCandidateRecord
	DiscoverySeed            = pipeline.DiscoverySeed
	Observation              = pipeline.Observation
	RejectedObservation      = pipeline.RejectedObservation
	OperationalState         = pipeline.OperationalState
	Posting                  = pipeline.Posting
	RuntimeState             = pipeline.RuntimeState
	Source                   = pipeline.Source
	SourceStatus             = pipeline.SourceStatus
)

const (
	DefaultSchema        = "radar_lite"
	defaultDeliveryLease = 2 * time.Minute
)

var schemaIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

type PostgresOptions struct {
	Schema string
	Now    func() time.Time
}

type PostgresStore struct {
	db     *sql.DB
	schema string
	now    func() time.Time
}

func NewPostgresStore(db *sql.DB, options PostgresOptions) (*PostgresStore, error) {
	if db == nil {
		return nil, errors.New("lite: postgres database is required")
	}
	schema := strings.TrimSpace(options.Schema)
	if schema == "" {
		schema = DefaultSchema
	}
	if !schemaIdentifier.MatchString(schema) {
		return nil, fmt.Errorf("lite: invalid postgres schema %q", schema)
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &PostgresStore{db: db, schema: schema, now: now}, nil
}

func (s *PostgresStore) Schema() string { return s.schema }

func (s *PostgresStore) table(name string) string {
	return `"` + s.schema + `"."` + name + `"`
}

// EnsureSchema owns an isolated schema and never creates or alters Radar's
// existing jobs, profiles, matching, or notification tables.
func (s *PostgresStore) EnsureSchema(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("lite schema: %w", err)
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE SCHEMA IF NOT EXISTS "` + s.schema + `"`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("jobs") + ` (
            id text PRIMARY KEY,
            company text NOT NULL,
            title text NOT NULL,
            location text NOT NULL DEFAULT '',
			country text NOT NULL DEFAULT '',
			employment_type text NOT NULL DEFAULT '',
			level text NOT NULL DEFAULT '',
            apply_url text NOT NULL DEFAULT '',
            description text NOT NULL DEFAULT '',
			posted_at timestamptz,
            first_seen_at timestamptz NOT NULL,
            last_seen_at timestamptz NOT NULL,
            CHECK (last_seen_at >= first_seen_at)
        )`,
		`ALTER TABLE ` + s.table("jobs") + ` ADD COLUMN IF NOT EXISTS country text NOT NULL DEFAULT ''`,
		`ALTER TABLE ` + s.table("jobs") + ` ADD COLUMN IF NOT EXISTS posted_at timestamptz`,
		`ALTER TABLE ` + s.table("jobs") + ` ADD COLUMN IF NOT EXISTS apply_url_state text NOT NULL DEFAULT 'unchecked'`,
		`ALTER TABLE ` + s.table("jobs") + ` ADD COLUMN IF NOT EXISTS apply_url_checked_at timestamptz`,
		`ALTER TABLE ` + s.table("jobs") + ` ADD COLUMN IF NOT EXISTS apply_url_next_check_at timestamptz`,
		`ALTER TABLE ` + s.table("jobs") + ` ADD COLUMN IF NOT EXISTS apply_url_consecutive_gone integer NOT NULL DEFAULT 0`,
		`ALTER TABLE ` + s.table("jobs") + ` ADD COLUMN IF NOT EXISTS apply_url_last_status integer`,
		`CREATE INDEX IF NOT EXISTS lite_jobs_apply_url_due_idx ON ` + s.table("jobs") + `(apply_url_next_check_at, first_seen_at) WHERE apply_url <> ''`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("job_identities") + ` (
            identity_key text PRIMARY KEY,
            job_id text NOT NULL REFERENCES ` + s.table("jobs") + `(id) ON DELETE CASCADE,
            created_at timestamptz NOT NULL DEFAULT now()
        )`,
		`CREATE INDEX IF NOT EXISTS lite_job_identities_job_idx ON ` + s.table("job_identities") + `(job_id)`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("job_source_observations") + ` (
			job_id text NOT NULL REFERENCES ` + s.table("jobs") + `(id) ON DELETE CASCADE,
			source_id text NOT NULL,
			source_native_id text NOT NULL DEFAULT '',
			first_observed_at timestamptz NOT NULL,
			last_observed_at timestamptz NOT NULL,
			active boolean NOT NULL DEFAULT true,
			PRIMARY KEY (job_id, source_id, source_native_id),
			CHECK (last_observed_at >= first_observed_at)
		)`,
		`ALTER TABLE ` + s.table("job_source_observations") + ` ADD COLUMN IF NOT EXISTS active boolean NOT NULL DEFAULT true`,
		`CREATE INDEX IF NOT EXISTS lite_job_source_observations_source_idx ON ` + s.table("job_source_observations") + `(source_id, last_observed_at DESC)`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("job_rejections") + ` (
			source_id text NOT NULL,
			fingerprint text NOT NULL,
			source_native_id text NOT NULL DEFAULT '',
			company text NOT NULL,
			title text NOT NULL,
			location text NOT NULL DEFAULT '',
			country text NOT NULL DEFAULT '',
			apply_url text NOT NULL DEFAULT '',
			code text NOT NULL,
			policy_version text NOT NULL,
			first_observed_at timestamptz NOT NULL,
			last_observed_at timestamptz NOT NULL,
			observation_count integer NOT NULL DEFAULT 1 CHECK (observation_count > 0),
			PRIMARY KEY (source_id, fingerprint, code, policy_version),
			CHECK (last_observed_at >= first_observed_at)
		)`,
		`CREATE INDEX IF NOT EXISTS radar_job_rejections_recent_idx ON ` + s.table("job_rejections") + `(last_observed_at DESC)`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("deliveries") + ` (
            id bigserial PRIMARY KEY,
            job_id text NOT NULL REFERENCES ` + s.table("jobs") + `(id) ON DELETE CASCADE,
            channel text NOT NULL,
            recipient text NOT NULL,
            payload jsonb NOT NULL DEFAULT '{}'::jsonb,
			status text NOT NULL DEFAULT 'pending' CHECK (status IN ('staged', 'pending', 'claimed', 'sent', 'failed', 'suppressed')),
            attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
            claim_owner text NOT NULL DEFAULT '',
            claim_expires_at timestamptz,
            next_attempt_at timestamptz NOT NULL DEFAULT now(),
            last_error text NOT NULL DEFAULT '',
            created_at timestamptz NOT NULL DEFAULT now(),
			sent_at timestamptz
        )`,
		`ALTER TABLE ` + s.table("deliveries") + ` DROP CONSTRAINT IF EXISTS deliveries_job_id_recipient_key`,
		`CREATE UNIQUE INDEX IF NOT EXISTS lite_deliveries_job_channel_recipient_uidx ON ` + s.table("deliveries") + ` (job_id, channel, recipient)`,
		`ALTER TABLE ` + s.table("deliveries") + ` ADD COLUMN IF NOT EXISTS receipt jsonb NOT NULL DEFAULT '{}'::jsonb`,
		`ALTER TABLE ` + s.table("deliveries") + ` ADD COLUMN IF NOT EXISTS provider_message_id text NOT NULL DEFAULT ''`,
		`ALTER TABLE ` + s.table("deliveries") + ` ADD COLUMN IF NOT EXISTS ambiguous_at timestamptz`,
		`CREATE INDEX IF NOT EXISTS lite_deliveries_pending_idx ON ` + s.table("deliveries") + `(next_attempt_at, id) WHERE status IN ('pending', 'claimed')`,
		`CREATE INDEX IF NOT EXISTS lite_deliveries_retryable_idx ON ` + s.table("deliveries") + `(channel, recipient, next_attempt_at, id) WHERE status IN ('pending', 'claimed', 'failed')`,
		`DO $radar_lite_delivery_migration$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = '` + s.table("deliveries") + `'::regclass
          AND conname = 'deliveries_status_check'
    ) THEN
        ALTER TABLE ` + s.table("deliveries") + ` DROP CONSTRAINT deliveries_status_check;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = '` + s.table("deliveries") + `'::regclass
          AND conname = 'deliveries_status_v2_check'
    ) AND NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = '` + s.table("deliveries") + `'::regclass
          AND conname = 'deliveries_status_v3_check'
    ) THEN
        ALTER TABLE ` + s.table("deliveries") + `
            ADD CONSTRAINT deliveries_status_v2_check
            CHECK (status IN ('staged', 'pending', 'claimed', 'sent', 'failed', 'suppressed'));
    END IF;
END
$radar_lite_delivery_migration$;`,
		`DO $radar_delivery_state_v3$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = '` + s.table("deliveries") + `'::regclass
          AND conname = 'deliveries_status_v2_check'
    ) THEN
        ALTER TABLE ` + s.table("deliveries") + ` DROP CONSTRAINT deliveries_status_v2_check;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = '` + s.table("deliveries") + `'::regclass
          AND conname = 'deliveries_status_v3_check'
    ) THEN
        ALTER TABLE ` + s.table("deliveries") + `
            ADD CONSTRAINT deliveries_status_v3_check
            CHECK (status IN ('staged', 'pending', 'claimed', 'sent', 'failed', 'suppressed', 'uncertain'));
    END IF;
END
$radar_delivery_state_v3$;`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("source_status") + ` (
            source_id text PRIMARY KEY,
            state text NOT NULL CHECK (state IN ('success', 'failure')),
            observed_count integer NOT NULL DEFAULT 0 CHECK (observed_count >= 0),
            last_attempt_at timestamptz NOT NULL,
            last_success_at timestamptz,
            last_failure_at timestamptz,
            consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
            last_error text NOT NULL DEFAULT ''
        )`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("bootstrap_state") + ` (
            key text PRIMARY KEY,
            value jsonb NOT NULL DEFAULT '{}'::jsonb,
            updated_at timestamptz NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("runtime_state") + ` (
            key text PRIMARY KEY,
            active_owner text NOT NULL DEFAULT '',
            active_started_at timestamptz,
            last_cycle_state text NOT NULL DEFAULT 'pending' CHECK (last_cycle_state IN ('pending', 'success', 'degraded', 'failure')),
            last_cycle_started_at timestamptz,
            last_cycle_finished_at timestamptz,
            sources_attempted integer NOT NULL DEFAULT 0 CHECK (sources_attempted >= 0),
            sources_succeeded integer NOT NULL DEFAULT 0 CHECK (sources_succeeded >= 0),
            sources_failed integer NOT NULL DEFAULT 0 CHECK (sources_failed >= 0),
            observed integer NOT NULL DEFAULT 0 CHECK (observed >= 0),
            created integer NOT NULL DEFAULT 0 CHECK (created >= 0),
            eligible_created integer NOT NULL DEFAULT 0 CHECK (eligible_created >= 0),
            enqueued integer NOT NULL DEFAULT 0 CHECK (enqueued >= 0),
            deliveries_sent integer NOT NULL DEFAULT 0 CHECK (deliveries_sent >= 0),
            delivery_failures integer NOT NULL DEFAULT 0 CHECK (delivery_failures >= 0),
            last_error text NOT NULL DEFAULT '',
            updated_at timestamptz NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("discovery_candidates") + ` (
            id text PRIMARY KEY,
            name text NOT NULL,
            website text NOT NULL DEFAULT '',
            tags jsonb NOT NULL DEFAULT '[]'::jsonb,
            state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'retry', 'validating', 'promoted', 'duplicate')),
            attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
            next_attempt_at timestamptz NOT NULL DEFAULT now(),
            last_attempt_at timestamptz,
            last_success_at timestamptz,
			last_error text NOT NULL DEFAULT '',
            created_at timestamptz NOT NULL DEFAULT now(),
            updated_at timestamptz NOT NULL DEFAULT now()
		)`,
		`ALTER TABLE ` + s.table("source_status") + ` ADD COLUMN IF NOT EXISTS failure_code text NOT NULL DEFAULT ''`,
		`ALTER TABLE ` + s.table("discovery_candidates") + ` ADD COLUMN IF NOT EXISTS failure_code text NOT NULL DEFAULT ''`,
		`DO $radar_discovery_candidate_state$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = '` + s.table("discovery_candidates") + `'::regclass
          AND conname = 'discovery_candidates_state_check'
    ) THEN
        ALTER TABLE ` + s.table("discovery_candidates") + ` DROP CONSTRAINT discovery_candidates_state_check;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = '` + s.table("discovery_candidates") + `'::regclass
          AND conname = 'discovery_candidates_state_v2_check'
    ) THEN
        ALTER TABLE ` + s.table("discovery_candidates") + `
            ADD CONSTRAINT discovery_candidates_state_v2_check
            CHECK (state IN ('pending', 'retry', 'validating', 'promoted', 'duplicate', 'parked'));
    END IF;
END
$radar_discovery_candidate_state$;`,
		`CREATE INDEX IF NOT EXISTS lite_discovery_candidates_due_idx ON ` + s.table("discovery_candidates") + `(next_attempt_at, id) WHERE state IN ('pending', 'retry', 'validating')`,
		`CREATE INDEX IF NOT EXISTS lite_discovery_candidates_due_v2_idx ON ` + s.table("discovery_candidates") + `(next_attempt_at, id) WHERE state IN ('pending', 'retry', 'validating', 'promoted')`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("discovered_sources") + ` (
            id text PRIMARY KEY,
            candidate_id text NOT NULL REFERENCES ` + s.table("discovery_candidates") + `(id) ON DELETE CASCADE,
            company text NOT NULL,
            provider text NOT NULL,
            url text NOT NULL,
            state text NOT NULL DEFAULT 'candidate' CONSTRAINT discovered_sources_state_v2_check CHECK (state IN ('candidate', 'promoted', 'unhealthy', 'duplicate')),
            confidence double precision NOT NULL DEFAULT 0 CHECK (confidence >= 0 AND confidence <= 1),
            observed_count integer NOT NULL DEFAULT 0 CHECK (observed_count >= 0),
            consecutive_successes integer NOT NULL DEFAULT 0 CHECK (consecutive_successes >= 0),
            consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
            last_checked_at timestamptz NOT NULL,
            last_success_at timestamptz,
            last_failure_at timestamptz,
            promoted_at timestamptz,
            last_error text NOT NULL DEFAULT '',
            evidence text NOT NULL DEFAULT '',
            UNIQUE (provider, url)
		)`,
		`ALTER TABLE ` + s.table("discovered_sources") + ` ADD COLUMN IF NOT EXISTS failure_code text NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS lite_discovered_sources_state_idx ON ` + s.table("discovered_sources") + `(state, id)`,
		`DO $radar_lite_migration$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = '` + s.table("discovered_sources") + `'::regclass
          AND conname = 'discovered_sources_state_check'
    ) THEN
        ALTER TABLE ` + s.table("discovered_sources") + ` DROP CONSTRAINT discovered_sources_state_check;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = '` + s.table("discovered_sources") + `'::regclass
          AND conname = 'discovered_sources_state_v2_check'
    ) AND NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = '` + s.table("discovered_sources") + `'::regclass
          AND conname = 'discovered_sources_state_v3_check'
    ) THEN
        ALTER TABLE ` + s.table("discovered_sources") + `
            ADD CONSTRAINT discovered_sources_state_v2_check
            CHECK (state IN ('candidate', 'promoted', 'unhealthy', 'duplicate'));
    END IF;
END
$radar_lite_migration$;`,
		`DO $radar_discovered_source_state_v3$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = '` + s.table("discovered_sources") + `'::regclass
          AND conname = 'discovered_sources_state_v2_check'
    ) THEN
        ALTER TABLE ` + s.table("discovered_sources") + ` DROP CONSTRAINT discovered_sources_state_v2_check;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = '` + s.table("discovered_sources") + `'::regclass
          AND conname = 'discovered_sources_state_v3_check'
    ) THEN
        ALTER TABLE ` + s.table("discovered_sources") + `
            ADD CONSTRAINT discovered_sources_state_v3_check
            CHECK (state IN ('candidate', 'promoted', 'unhealthy', 'duplicate', 'rejected'));
    END IF;
END
$radar_discovered_source_state_v3$;`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("discovery_events") + ` (
            id bigserial PRIMARY KEY,
            candidate_id text NOT NULL,
            source_id text NOT NULL DEFAULT '',
            outcome text NOT NULL,
            code text NOT NULL DEFAULT '',
            detail text NOT NULL DEFAULT '',
            evidence text NOT NULL DEFAULT '',
            created_at timestamptz NOT NULL DEFAULT now()
        )`,
		`CREATE INDEX IF NOT EXISTS radar_discovery_events_candidate_idx ON ` + s.table("discovery_events") + `(candidate_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("source_controls") + ` (
            source_id text PRIMARY KEY,
            state text NOT NULL CHECK (state IN ('active', 'quarantined')),
            reason text NOT NULL DEFAULT '',
            actor text NOT NULL DEFAULT '',
            updated_at timestamptz NOT NULL DEFAULT now()
        )`,
		`CREATE TABLE IF NOT EXISTS ` + s.table("source_events") + ` (
            id bigserial PRIMARY KEY,
            source_id text NOT NULL,
            action text NOT NULL,
            reason text NOT NULL DEFAULT '',
            actor text NOT NULL DEFAULT '',
            created_at timestamptz NOT NULL DEFAULT now()
        )`,
		`CREATE INDEX IF NOT EXISTS radar_source_events_source_idx ON ` + s.table("source_events") + `(source_id, created_at DESC)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("lite schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("lite schema: %w", err)
	}
	return nil
}

// CycleLease holds one Postgres session advisory lock. The database releases
// it automatically if the process or connection dies, preventing two deploy
// revisions from crawling and draining the same schema concurrently.
type CycleLease struct {
	store   *PostgresStore
	conn    *sql.Conn
	owner   string
	started time.Time
	mu      sync.Mutex
	closed  bool
}

// TryAcquireCycle elects one routine owner per Lite schema. A false result is
// a healthy standby condition, not an error.
func (s *PostgresStore) TryAcquireCycle(ctx context.Context, owner string, startedAt time.Time) (*CycleLease, bool, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil, false, errors.New("lite: cycle owner is required")
	}
	if startedAt.IsZero() {
		startedAt = s.now().UTC()
	} else {
		startedAt = startedAt.UTC()
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("lite cycle lease: %w", err)
	}
	lockName := "radar-lite-cycle:" + s.schema
	var acquired bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, lockName).Scan(&acquired); err != nil {
		// The server may have acquired the session lock even if cancellation or
		// a network failure prevented Scan from receiving the result. Attempt an
		// independent unlock, then always discard the physical connection so an
		// uncertain session can never return to the pool holding the lock.
		cleanupErr := cleanupUncertainCycleLease(conn, lockName)
		return nil, false, errors.Join(fmt.Errorf("lite cycle lease: %w", err), cleanupErr)
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	if _, err := conn.ExecContext(ctx, `
INSERT INTO `+s.table("runtime_state")+` (key, active_owner, active_started_at, updated_at)
VALUES ('routine', $1, $2, $2)
ON CONFLICT (key) DO UPDATE SET
    active_owner = EXCLUDED.active_owner,
    active_started_at = EXCLUDED.active_started_at,
    updated_at = EXCLUDED.updated_at`, owner, startedAt); err != nil {
		lease := &CycleLease{store: s, conn: conn, owner: owner, started: startedAt}
		return nil, false, errors.Join(fmt.Errorf("lite cycle state: %w", err), lease.unlockAndClose(ctx))
	}
	return &CycleLease{store: s, conn: conn, owner: owner, started: startedAt}, true, nil
}

// Complete persists the cycle result before releasing ownership. It is safe to
// call once; callers should use a short finalization context independent of a
// canceled provider request.
func (l *CycleLease) Complete(ctx context.Context, result CycleResult) error {
	if l == nil {
		return errors.New("lite: cycle lease is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errors.New("lite: cycle lease is already closed")
	}
	status := strings.TrimSpace(result.Status)
	if status != "success" && status != "degraded" && status != "failure" {
		validationErr := fmt.Errorf("lite: invalid cycle status %q", status)
		return errors.Join(validationErr, l.clearActiveOwner(ctx), l.unlockAndClose(ctx))
	}
	finishedAt := result.FinishedAt
	if finishedAt.IsZero() {
		finishedAt = l.store.now().UTC()
	} else {
		finishedAt = finishedAt.UTC()
	}
	update, updateErr := l.conn.ExecContext(ctx, `
UPDATE `+l.store.table("runtime_state")+` SET
    active_owner = '', active_started_at = NULL,
    last_cycle_state = $2, last_cycle_started_at = $3, last_cycle_finished_at = $4,
    sources_attempted = $5, sources_succeeded = $6, sources_failed = $7,
    observed = $8, created = $9, eligible_created = $10, enqueued = $11,
    deliveries_sent = $12, delivery_failures = $13, last_error = $14, updated_at = $4
WHERE key = 'routine' AND active_owner = $1`,
		l.owner, status, l.started, finishedAt,
		result.SourcesAttempted, result.SourcesSucceeded, result.SourcesFailed,
		result.Observed, result.Created, result.EligibleCreated, result.Enqueued,
		result.DeliveriesSent, result.DeliveryFailures, pipeline.CompactDiscoveryError(result.LastError),
	)
	if updateErr == nil {
		var affected int64
		affected, updateErr = update.RowsAffected()
		if updateErr == nil && affected != 1 {
			updateErr = errors.New("lite: cycle ownership changed before completion")
		}
	}
	return errors.Join(updateErr, l.unlockAndClose(ctx))
}

// Release relinquishes a lease without recording a completed cycle. It is for
// startup failures before a real cycle result exists.
func (l *CycleLease) Release(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	stateErr := l.clearActiveOwner(ctx)
	return errors.Join(stateErr, l.unlockAndClose(ctx))
}

func (l *CycleLease) clearActiveOwner(ctx context.Context) error {
	_, err := l.conn.ExecContext(ctx, `
UPDATE `+l.store.table("runtime_state")+`
SET active_owner = '', active_started_at = NULL, updated_at = $2
WHERE key = 'routine' AND active_owner = $1`, l.owner, l.store.now().UTC())
	return err
}

func cleanupUncertainCycleLease(conn *sql.Conn, lockName string) error {
	if conn == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// False is expected when cancellation happened before acquisition. The
	// connection is discarded either way, which is the important invariant.
	var unlocked bool
	unlockErr := conn.QueryRowContext(cleanupCtx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, lockName).Scan(&unlocked)
	discardErr := conn.Raw(func(any) error { return driver.ErrBadConn })
	if errors.Is(discardErr, driver.ErrBadConn) {
		discardErr = nil
	}
	closeErr := conn.Close()
	if errors.Is(closeErr, sql.ErrConnDone) {
		closeErr = nil
	}
	return errors.Join(unlockErr, discardErr, closeErr)
}

func (l *CycleLease) unlockAndClose(ctx context.Context) error {
	_ = ctx
	unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lockName := "radar-lite-cycle:" + l.store.schema
	var unlocked bool
	unlockErr := l.conn.QueryRowContext(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, lockName).Scan(&unlocked)
	if unlockErr == nil && !unlocked {
		unlockErr = errors.New("lite: cycle advisory lock was not held")
	}
	var discardErr error
	if unlockErr != nil {
		discardErr = l.conn.Raw(func(any) error { return driver.ErrBadConn })
		if errors.Is(discardErr, driver.ErrBadConn) {
			discardErr = nil
		}
	}
	closeErr := l.conn.Close()
	if errors.Is(closeErr, sql.ErrConnDone) {
		closeErr = nil
	}
	l.closed = true
	return errors.Join(unlockErr, discardErr, closeErr)
}

// Observe persists a sighting and returns whether it created a new canonical
// job. Advisory locks over every identity make concurrent cross-source upserts
// converge on one job without requiring a distributed queue.
func (s *PostgresStore) Observe(ctx context.Context, observation Observation) (Posting, bool, error) {
	posting, created, _, _, err := s.observeAndEnqueue(ctx, observation, nil)
	return posting, created, err
}

func (s *PostgresStore) RecordRejectedObservation(ctx context.Context, rejected RejectedObservation) error {
	sourceID := strings.TrimSpace(rejected.SourceID)
	code := strings.TrimSpace(rejected.Code)
	policyVersion := strings.TrimSpace(rejected.PolicyVersion)
	if sourceID == "" || code == "" || policyVersion == "" {
		return errors.New("radar: rejected observation requires source, code, and policy version")
	}
	observedAt := rejected.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = s.now().UTC()
	}
	seed := strings.Join([]string{
		strings.TrimSpace(rejected.SourceNativeID), pipeline.CanonicalText(rejected.Company),
		pipeline.CanonicalText(rejected.Title), pipeline.CanonicalText(rejected.Location),
		pipeline.CanonicalApplyURL(rejected.ApplyURL),
	}, "|")
	sum := sha256.Sum256([]byte(seed))
	fingerprint := hex.EncodeToString(sum[:16])
	_, err := s.db.ExecContext(ctx, `
INSERT INTO `+s.table("job_rejections")+` AS current
    (source_id, fingerprint, source_native_id, company, title, location, country, apply_url, code, policy_version, first_observed_at, last_observed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
ON CONFLICT (source_id, fingerprint, code, policy_version) DO UPDATE SET
    last_observed_at = GREATEST(current.last_observed_at, EXCLUDED.last_observed_at),
    observation_count = current.observation_count + 1`,
		sourceID, fingerprint, strings.TrimSpace(rejected.SourceNativeID), strings.TrimSpace(rejected.Company),
		strings.TrimSpace(rejected.Title), strings.TrimSpace(rejected.Location), strings.TrimSpace(rejected.Country),
		pipeline.CanonicalApplyURL(rejected.ApplyURL), code, policyVersion, observedAt,
	)
	return err
}

// ObserveAndEnqueue atomically persists a first-seen posting and its delivery.
// A replay never creates another delivery, and any target/insert failure rolls
// the new posting back with the transaction.
func (s *PostgresStore) ObserveAndEnqueue(ctx context.Context, observation Observation, target *DeliveryTarget) (Posting, bool, Delivery, bool, error) {
	return s.observeAndEnqueue(ctx, observation, target)
}

func (s *PostgresStore) observeAndEnqueue(ctx context.Context, observation Observation, target *DeliveryTarget) (Posting, bool, Delivery, bool, error) {
	var targetChannel, targetRecipient string
	if target != nil {
		targetChannel = strings.TrimSpace(target.Channel)
		targetRecipient = strings.TrimSpace(target.Recipient)
		if targetChannel == "" || targetRecipient == "" {
			return Posting{}, false, Delivery{}, false, errors.New("lite: delivery channel and recipient are required")
		}
		if target.Suppress && target.Stage {
			return Posting{}, false, Delivery{}, false, errors.New("lite: delivery decision cannot be both staged and suppressed")
		}
	}
	keys, err := pipeline.IdentityKeys(observation)
	if err != nil {
		return Posting{}, false, Delivery{}, false, err
	}
	companyIdentity := pipeline.CanonicalText(observation.Company)
	keys = addCompanyURLIdentity(keys, companyIdentity)
	observedAt := observation.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = s.now().UTC()
	}
	var postedAt *time.Time
	if observation.PostedAt != nil && !observation.PostedAt.IsZero() {
		value := observation.PostedAt.UTC()
		postedAt = &value
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Posting{}, false, Delivery{}, false, err
	}
	defer tx.Rollback()

	lockKeys := append([]string(nil), keys...)
	sort.Strings(lockKeys)
	for _, key := range lockKeys {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
			return Posting{}, false, Delivery{}, false, err
		}
	}
	if err := s.coalescePostingOwners(ctx, tx, keys, companyIdentity); err != nil {
		return Posting{}, false, Delivery{}, false, err
	}

	posting, err := s.findPostingByIdentities(ctx, tx, keys, companyIdentity)
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		created = true
		posting = Posting{
			ID: pipeline.StablePostingID(keys), Company: strings.TrimSpace(observation.Company),
			Title: strings.TrimSpace(observation.Title), Location: strings.TrimSpace(observation.Location),
			Country:        strings.TrimSpace(observation.Country),
			EmploymentType: strings.TrimSpace(observation.EmploymentType), Level: strings.TrimSpace(observation.Level),
			ApplyURL: pipeline.CanonicalApplyURL(observation.ApplyURL), Description: strings.TrimSpace(observation.Description),
			PostedAt: postedAt, FirstSeenAt: observedAt, LastSeenAt: observedAt,
		}
		err = tx.QueryRowContext(ctx, `
INSERT INTO `+s.table("jobs")+` (id, company, title, location, country, employment_type, level, apply_url, description, posted_at, first_seen_at, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
RETURNING id, company, title, location, country, employment_type, level, apply_url, description, posted_at, first_seen_at, last_seen_at`,
			posting.ID, posting.Company, posting.Title, posting.Location, posting.Country, posting.EmploymentType, posting.Level, posting.ApplyURL, posting.Description, postedAt, observedAt,
		).Scan(&posting.ID, &posting.Company, &posting.Title, &posting.Location, &posting.Country, &posting.EmploymentType, &posting.Level, &posting.ApplyURL, &posting.Description, &posting.PostedAt, &posting.FirstSeenAt, &posting.LastSeenAt)
	} else if err == nil {
		err = tx.QueryRowContext(ctx, `
UPDATE `+s.table("jobs")+` SET
    company = $2,
    title = $3,
	location = CASE WHEN $4 = '' THEN location ELSE $4 END,
	country = CASE WHEN $5 = '' THEN country ELSE $5 END,
	employment_type = CASE WHEN $6 = '' THEN employment_type ELSE $6 END,
	level = CASE WHEN $7 = '' THEN level ELSE $7 END,
	apply_url_state = CASE WHEN $8 <> '' AND apply_url <> $8 THEN 'unchecked' ELSE apply_url_state END,
	apply_url_checked_at = CASE WHEN $8 <> '' AND apply_url <> $8 THEN NULL ELSE apply_url_checked_at END,
	apply_url_next_check_at = CASE WHEN $8 <> '' AND apply_url <> $8 THEN NULL ELSE apply_url_next_check_at END,
	apply_url_consecutive_gone = CASE WHEN $8 <> '' AND apply_url <> $8 THEN 0 ELSE apply_url_consecutive_gone END,
	apply_url_last_status = CASE WHEN $8 <> '' AND apply_url <> $8 THEN NULL ELSE apply_url_last_status END,
	apply_url = CASE WHEN $8 = '' THEN apply_url ELSE $8 END,
	description = CASE WHEN $9 = '' THEN description ELSE $9 END,
	posted_at = COALESCE($10, posted_at),
	first_seen_at = LEAST(first_seen_at, $11),
	last_seen_at = GREATEST(last_seen_at, $11)
WHERE id = $1
RETURNING id, company, title, location, country, employment_type, level, apply_url, description, posted_at, first_seen_at, last_seen_at`,
			posting.ID, strings.TrimSpace(observation.Company), strings.TrimSpace(observation.Title), strings.TrimSpace(observation.Location),
			strings.TrimSpace(observation.Country), strings.TrimSpace(observation.EmploymentType), strings.TrimSpace(observation.Level), pipeline.CanonicalApplyURL(observation.ApplyURL), strings.TrimSpace(observation.Description), postedAt, observedAt,
		).Scan(&posting.ID, &posting.Company, &posting.Title, &posting.Location, &posting.Country, &posting.EmploymentType, &posting.Level, &posting.ApplyURL, &posting.Description, &posting.PostedAt, &posting.FirstSeenAt, &posting.LastSeenAt)
	}
	if err != nil {
		return Posting{}, false, Delivery{}, false, err
	}
	for _, key := range keys {
		result, err := tx.ExecContext(ctx, `INSERT INTO `+s.table("job_identities")+` (identity_key, job_id) VALUES ($1, $2) ON CONFLICT (identity_key) DO NOTHING`, key, posting.ID)
		if err != nil {
			return Posting{}, false, Delivery{}, false, err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			var owner string
			if err := tx.QueryRowContext(ctx, `SELECT job_id FROM `+s.table("job_identities")+` WHERE identity_key = $1`, key).Scan(&owner); err != nil {
				return Posting{}, false, Delivery{}, false, err
			}
			if owner != posting.ID && (strings.HasPrefix(key, "url:") || strings.HasPrefix(key, "company-url:")) && len(keys) > 1 &&
				(strings.HasPrefix(keys[0], "native:") || strings.HasPrefix(keys[0], "company-url:")) {
				continue
			}
			if owner != posting.ID {
				return Posting{}, false, Delivery{}, false, fmt.Errorf("lite: identity %q already belongs to another job", key)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO `+s.table("job_source_observations")+` AS current (job_id, source_id, source_native_id, first_observed_at, last_observed_at, active)
VALUES ($1, $2, $3, $4, $4, NOT $5)
ON CONFLICT (job_id, source_id, source_native_id) DO UPDATE SET
    first_observed_at = LEAST(current.first_observed_at, EXCLUDED.first_observed_at),
	last_observed_at = GREATEST(current.last_observed_at, EXCLUDED.last_observed_at),
	active = current.active OR EXCLUDED.active`,
		posting.ID, strings.TrimSpace(observation.SourceID), strings.TrimSpace(observation.SourceNativeID), observedAt, observation.SnapshotPending,
	); err != nil {
		return Posting{}, false, Delivery{}, false, err
	}
	var delivery Delivery
	deliveryCreated := false
	if target != nil {
		payload, err := json.Marshal(posting)
		if err != nil {
			return Posting{}, false, Delivery{}, false, err
		}
		status := "pending"
		if target.Suppress {
			status = "suppressed"
		} else if target.Stage {
			status = "staged"
		}
		delivery, deliveryCreated, err = s.enqueueDelivery(ctx, tx, posting.ID, targetChannel, targetRecipient, payload, status)
		if err != nil {
			return Posting{}, false, Delivery{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Posting{}, false, Delivery{}, false, err
	}
	return posting, created, delivery, deliveryCreated, nil
}

func addCompanyURLIdentity(keys []string, company string) []string {
	augmented := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		if strings.HasPrefix(key, "url:") {
			augmented = append(augmented, "company-url:"+company+"|"+key)
		}
		augmented = append(augmented, key)
	}
	return augmented
}

// coalescePostingOwners repairs legacy cross-source duplicates when a newly
// learned strong alias (for example a company-scoped requisition UUID) joins
// them. The earliest posting remains canonical; identities, provenance, and
// delivery decisions move transactionally so convergence can never create a
// second notification.
func (s *PostgresStore) coalescePostingOwners(ctx context.Context, tx *sql.Tx, keys []string, company string) error {
	urlKeys := make([]string, 0, 1)
	for _, key := range keys {
		if strings.HasPrefix(key, "url:") {
			urlKeys = append(urlKeys, key)
		}
	}
	rows, err := tx.QueryContext(ctx, `
SELECT j.id, j.company
FROM `+s.table("job_identities")+` AS identity
JOIN `+s.table("jobs")+` AS j ON j.id = identity.job_id
WHERE identity.identity_key = ANY($1)
   OR (
       identity.identity_key LIKE 'company-url:%'
       AND substring(identity.identity_key FROM position('|' IN identity.identity_key) + 1) = ANY($2)
   )
ORDER BY j.first_seen_at, j.id
FOR UPDATE OF j`, pq.Array(keys), pq.Array(urlKeys))
	if err != nil {
		return err
	}
	var owners []string
	seen := make(map[string]struct{})
	for rows.Next() {
		var id, ownerCompany string
		if err := rows.Scan(&id, &ownerCompany); err != nil {
			rows.Close()
			return err
		}
		if !pipeline.SameCompanyIdentity(ownerCompany, company) {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		owners = append(owners, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(owners) < 2 {
		return nil
	}
	canonicalID := owners[0]
	for _, duplicateID := range owners[1:] {
		if err := s.mergePostingInto(ctx, tx, canonicalID, duplicateID); err != nil {
			return fmt.Errorf("lite: merge duplicate posting %s into %s: %w", duplicateID, canonicalID, err)
		}
	}
	return nil
}

func (s *PostgresStore) mergePostingInto(ctx context.Context, tx *sql.Tx, canonicalID, duplicateID string) error {
	if canonicalID == duplicateID {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("jobs")+` AS canonical SET
    location = COALESCE(NULLIF(canonical.location, ''), duplicate.location),
    country = COALESCE(NULLIF(canonical.country, ''), duplicate.country),
    employment_type = COALESCE(NULLIF(canonical.employment_type, ''), duplicate.employment_type),
    level = COALESCE(NULLIF(canonical.level, ''), duplicate.level),
    apply_url = COALESCE(NULLIF(canonical.apply_url, ''), duplicate.apply_url),
    description = COALESCE(NULLIF(canonical.description, ''), duplicate.description),
    posted_at = COALESCE(canonical.posted_at, duplicate.posted_at),
    first_seen_at = LEAST(canonical.first_seen_at, duplicate.first_seen_at),
    last_seen_at = GREATEST(canonical.last_seen_at, duplicate.last_seen_at)
FROM `+s.table("jobs")+` AS duplicate
WHERE canonical.id = $1 AND duplicate.id = $2`, canonicalID, duplicateID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("deliveries")+` AS canonical SET
    status = CASE
        WHEN canonical.status = 'sent' OR duplicate.status = 'sent' THEN 'sent'
        WHEN canonical.status = 'suppressed' OR duplicate.status = 'suppressed' THEN 'suppressed'
        WHEN canonical.status IN ('pending', 'claimed') OR duplicate.status IN ('pending', 'claimed') THEN 'pending'
		WHEN canonical.status = 'staged' OR duplicate.status = 'staged' THEN 'staged'
        ELSE 'failed'
    END,
    attempts = GREATEST(canonical.attempts, duplicate.attempts),
    claim_owner = '', claim_expires_at = NULL,
    next_attempt_at = LEAST(canonical.next_attempt_at, duplicate.next_attempt_at),
    last_error = CASE WHEN canonical.last_error <> '' THEN canonical.last_error ELSE duplicate.last_error END,
    created_at = LEAST(canonical.created_at, duplicate.created_at),
    sent_at = COALESCE(canonical.sent_at, duplicate.sent_at)
FROM `+s.table("deliveries")+` AS duplicate
WHERE canonical.job_id = $1 AND duplicate.job_id = $2
  AND canonical.channel = duplicate.channel AND canonical.recipient = duplicate.recipient`, canonicalID, duplicateID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM `+s.table("deliveries")+` AS duplicate
USING `+s.table("deliveries")+` AS canonical
WHERE duplicate.job_id = $2 AND canonical.job_id = $1
  AND duplicate.channel = canonical.channel AND duplicate.recipient = canonical.recipient`, canonicalID, duplicateID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+s.table("deliveries")+` SET job_id = $1 WHERE job_id = $2`, canonicalID, duplicateID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO `+s.table("job_source_observations")+` AS canonical
    (job_id, source_id, source_native_id, first_observed_at, last_observed_at, active)
SELECT $1, source_id, source_native_id, first_observed_at, last_observed_at, active
FROM `+s.table("job_source_observations")+`
WHERE job_id = $2
ON CONFLICT (job_id, source_id, source_native_id) DO UPDATE SET
    first_observed_at = LEAST(canonical.first_observed_at, EXCLUDED.first_observed_at),
	last_observed_at = GREATEST(canonical.last_observed_at, EXCLUDED.last_observed_at),
	active = canonical.active OR EXCLUDED.active`, canonicalID, duplicateID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.table("job_source_observations")+` WHERE job_id = $1`, duplicateID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+s.table("job_identities")+` SET job_id = $1 WHERE job_id = $2`, canonicalID, duplicateID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("deliveries")+`
SET payload = jsonb_set(payload, '{ID}', to_jsonb($1::text), true)
WHERE job_id = $1 AND status <> 'sent'`, canonicalID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM `+s.table("jobs")+` WHERE id = $1`, duplicateID)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return fmt.Errorf("duplicate job delete affected %d rows", affected)
	}
	return nil
}

func (s *PostgresStore) findPostingByIdentities(ctx context.Context, tx *sql.Tx, keys []string, company string) (Posting, error) {
	if len(keys) > 1 && strings.HasPrefix(keys[0], "native:") {
		posting, err := s.findPostingByIdentityKeys(ctx, tx, keys[:1])
		if err == nil {
			return posting, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return posting, err
		}
		nativeParts := strings.SplitN(keys[0], ":", 3)
		if len(nativeParts) == 3 {
			err = tx.QueryRowContext(ctx, `
SELECT j.id, j.company, j.title, j.location, j.country, j.employment_type, j.level, j.apply_url, j.description, j.posted_at, j.first_seen_at, j.last_seen_at
FROM `+s.table("job_identities")+` i
JOIN `+s.table("jobs")+` j ON j.id = i.job_id
WHERE i.identity_key = ANY($1)
  AND NOT EXISTS (
      SELECT 1 FROM `+s.table("job_identities")+` same_source
      WHERE same_source.job_id = j.id
        AND split_part(same_source.identity_key, ':', 1) = 'native'
        AND split_part(same_source.identity_key, ':', 2) = $2
  )
ORDER BY array_position($1::text[], i.identity_key)
LIMIT 1`, pq.Array(keys[1:]), nativeParts[1]).Scan(
				&posting.ID, &posting.Company, &posting.Title, &posting.Location, &posting.Country, &posting.EmploymentType, &posting.Level, &posting.ApplyURL,
				&posting.Description, &posting.PostedAt, &posting.FirstSeenAt, &posting.LastSeenAt,
			)
			if err == nil && !pipeline.SameCompanyIdentity(posting.Company, company) {
				return Posting{}, sql.ErrNoRows
			}
			return posting, err
		}
	}
	if len(keys) > 1 && strings.HasPrefix(keys[0], "company-url:") && strings.HasPrefix(keys[1], "url:") {
		posting, err := s.findPostingByIdentityKeys(ctx, tx, keys[:1])
		if err == nil {
			if !pipeline.SameCompanyIdentity(posting.Company, company) {
				return Posting{}, sql.ErrNoRows
			}
			return posting, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return posting, err
		}
		posting, err = s.findPostingByIdentityKeys(ctx, tx, keys[1:])
		if err == nil && !pipeline.SameCompanyIdentity(posting.Company, company) {
			return Posting{}, sql.ErrNoRows
		}
		return posting, err
	}
	posting, err := s.findPostingByIdentityKeys(ctx, tx, keys)
	if err == nil {
		for _, key := range keys {
			if strings.HasPrefix(key, "url:") && !pipeline.SameCompanyIdentity(posting.Company, company) {
				return Posting{}, sql.ErrNoRows
			}
		}
	}
	return posting, err
}

func (s *PostgresStore) findPostingByIdentityKeys(ctx context.Context, tx *sql.Tx, keys []string) (Posting, error) {
	var posting Posting
	err := tx.QueryRowContext(ctx, `
SELECT j.id, j.company, j.title, j.location, j.country, j.employment_type, j.level, j.apply_url, j.description, j.posted_at, j.first_seen_at, j.last_seen_at
FROM `+s.table("job_identities")+` i
JOIN `+s.table("jobs")+` j ON j.id = i.job_id
WHERE i.identity_key = ANY($1)
ORDER BY array_position($1::text[], i.identity_key)
LIMIT 1`, pq.Array(keys)).Scan(
		&posting.ID, &posting.Company, &posting.Title, &posting.Location, &posting.Country, &posting.EmploymentType, &posting.Level, &posting.ApplyURL,
		&posting.Description, &posting.PostedAt, &posting.FirstSeenAt, &posting.LastSeenAt,
	)
	return posting, err
}

// EnqueueDelivery is idempotent for a job, channel, and recipient even after
// the row is sent, so routine restarts/replays cannot spam the same destination.
func (s *PostgresStore) EnqueueDelivery(ctx context.Context, jobID, channel, recipient string, payload json.RawMessage) (Delivery, bool, error) {
	return s.enqueueDelivery(ctx, s.db, jobID, channel, recipient, payload, "pending")
}

// FinalizeSourceSnapshot reconciles active provenance only after the caller
// has persisted an entire authoritative source snapshot. Historical jobs stay
// durable for dedupe, while the feed shows a job only if at least one source
// still reports it.
func (s *PostgresStore) FinalizeSourceSnapshot(ctx context.Context, sourceID string, activeJobIDs []string) error {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return errors.New("lite: source is required")
	}
	uniqueIDs := make([]string, 0, len(activeJobIDs))
	seen := make(map[string]struct{}, len(activeJobIDs))
	for _, id := range activeJobIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return errors.New("lite: valid active job IDs are required")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE `+s.table("job_source_observations")+`
SET active = job_id = ANY($2::text[])
WHERE source_id = $1`, sourceID, pq.Array(uniqueIDs))
	return err
}

// ActivateDeliveries makes a fully persisted source pass publishable in one
// statement. Staged rows left by an interrupted pass remain unclaimable and
// can be activated safely when a later complete pass sees the same jobs.
func (s *PostgresStore) ActivateDeliveries(ctx context.Context, ids []int64, channel, recipient string) (int, error) {
	channel, recipient = strings.TrimSpace(channel), strings.TrimSpace(recipient)
	if channel == "" || recipient == "" {
		return 0, errors.New("lite: delivery channel and recipient are required")
	}
	if len(ids) == 0 {
		return 0, nil
	}
	uniqueIDs := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return 0, errors.New("lite: valid staged delivery IDs are required")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE `+s.table("deliveries")+`
SET status = 'pending', next_attempt_at = now()
WHERE id = ANY($1) AND channel = $2 AND recipient = $3 AND status = 'staged'`, pq.Array(uniqueIDs), channel, recipient)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if affected != int64(len(uniqueIDs)) {
		return 0, fmt.Errorf("lite: activated %d of %d staged delivery decisions", affected, len(uniqueIDs))
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(affected), nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *PostgresStore) enqueueDelivery(ctx context.Context, queryer queryRower, jobID, channel, recipient string, payload json.RawMessage, status string) (Delivery, bool, error) {
	jobID, channel, recipient = strings.TrimSpace(jobID), strings.TrimSpace(channel), strings.TrimSpace(recipient)
	if jobID == "" || channel == "" || recipient == "" {
		return Delivery{}, false, errors.New("lite: job, channel, and recipient are required")
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if status != "staged" && status != "pending" && status != "suppressed" {
		return Delivery{}, false, errors.New("lite: delivery decision must be staged, pending, or suppressed")
	}
	var delivery Delivery
	var rawPayload []byte
	var created bool
	err := queryer.QueryRowContext(ctx, `
WITH inserted AS (
    INSERT INTO `+s.table("deliveries")+` (job_id, channel, recipient, payload, status)
    VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT (job_id, channel, recipient) DO NOTHING
    RETURNING *, true AS created
)
SELECT id, job_id, channel, recipient, payload, status, attempts, claim_owner,
       claim_expires_at, next_attempt_at, last_error, created_at, sent_at, created
FROM inserted
UNION ALL
SELECT id, job_id, channel, recipient, payload, status, attempts, claim_owner,
       claim_expires_at, next_attempt_at, last_error, created_at, sent_at, false
FROM `+s.table("deliveries")+`
WHERE job_id = $1 AND channel = $2 AND recipient = $3 AND NOT EXISTS (SELECT 1 FROM inserted)
LIMIT 1`, jobID, channel, recipient, payload, status).Scan(
		&delivery.ID, &delivery.JobID, &delivery.Channel, &delivery.Recipient, &rawPayload,
		&delivery.Status, &delivery.Attempts, &delivery.ClaimOwner, nullableTime(&delivery.ClaimExpiresAt),
		&delivery.NextAttemptAt, &delivery.LastError, &delivery.CreatedAt, &delivery.SentAt, &created,
	)
	if err != nil {
		return Delivery{}, false, err
	}
	delivery.Payload = append(json.RawMessage(nil), rawPayload...)
	return delivery, created, nil
}

func nullableTime(target *time.Time) sql.Scanner { return nullTimeScanner{target: target} }

type nullTimeScanner struct{ target *time.Time }

func (s nullTimeScanner) Scan(value any) error {
	if value == nil {
		*s.target = time.Time{}
		return nil
	}
	valueTime, ok := value.(time.Time)
	if !ok {
		return fmt.Errorf("lite: expected time, got %T", value)
	}
	*s.target = valueTime
	return nil
}

func (s *PostgresStore) ClaimDeliveries(ctx context.Context, owner, channel, recipient string, limit int, lease time.Duration) ([]Delivery, error) {
	owner, channel, recipient = strings.TrimSpace(owner), strings.TrimSpace(channel), strings.TrimSpace(recipient)
	if owner == "" || channel == "" || recipient == "" {
		return nil, errors.New("lite: delivery claim owner, channel, and recipient are required")
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if lease <= 0 {
		lease = defaultDeliveryLease
	}
	expires := s.now().UTC().Add(lease)
	rows, err := s.db.QueryContext(ctx, `
WITH candidates AS (
    SELECT id FROM `+s.table("deliveries")+`
	WHERE channel = $4 AND recipient = $5 AND (
        (status = 'pending' AND next_attempt_at <= now()) OR
		(status = 'claimed' AND claim_expires_at <= now()) OR
		(status = 'failed' AND next_attempt_at <= now())
    )
    ORDER BY id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
UPDATE `+s.table("deliveries")+` d SET
	status = 'claimed', claim_owner = $1, claim_expires_at = $3
FROM candidates c WHERE d.id = c.id
RETURNING d.id, d.job_id, d.channel, d.recipient, d.payload, d.status, d.attempts,
          d.claim_owner, d.claim_expires_at, d.next_attempt_at, d.last_error, d.created_at, d.sent_at`,
		owner, limit, expires, channel, recipient)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deliveries []Delivery
	for rows.Next() {
		var delivery Delivery
		var payload []byte
		if err := rows.Scan(&delivery.ID, &delivery.JobID, &delivery.Channel, &delivery.Recipient, &payload,
			&delivery.Status, &delivery.Attempts, &delivery.ClaimOwner, &delivery.ClaimExpiresAt,
			&delivery.NextAttemptAt, &delivery.LastError, &delivery.CreatedAt, &delivery.SentAt); err != nil {
			return nil, err
		}
		delivery.Payload = append(json.RawMessage(nil), payload...)
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

// NextDeliveryAttemptAt returns the next time this target can make progress.
// Routine mode uses it to wake before the crawl interval instead of leaving a
// Telegram retry dormant in the durable outbox.
func (s *PostgresStore) NextDeliveryAttemptAt(ctx context.Context, channel, recipient string) (*time.Time, error) {
	channel, recipient = strings.TrimSpace(channel), strings.TrimSpace(recipient)
	if channel == "" || recipient == "" {
		return nil, errors.New("lite: delivery channel and recipient are required")
	}
	var next sql.NullTime
	err := s.db.QueryRowContext(ctx, `
SELECT min(CASE WHEN status = 'claimed' THEN claim_expires_at ELSE next_attempt_at END)
FROM `+s.table("deliveries")+`
WHERE channel = $1 AND recipient = $2
  AND status IN ('pending', 'failed', 'claimed')`, channel, recipient).Scan(&next)
	if err != nil {
		return nil, err
	}
	if !next.Valid {
		return nil, nil
	}
	value := next.Time.UTC()
	return &value, nil
}

func (s *PostgresStore) ReleaseDelivery(ctx context.Context, id int64, owner string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE `+s.table("deliveries")+` SET status = 'pending', claim_owner = '', claim_expires_at = NULL WHERE id = $1 AND status = 'claimed' AND claim_owner = $2`, id, owner)
	return requireOne(result, err, "claimed delivery")
}

func (s *PostgresStore) MarkDeliverySent(ctx context.Context, id int64, owner string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE `+s.table("deliveries")+` SET status = 'sent', attempts = attempts + 1, sent_at = now(), claim_owner = '', claim_expires_at = NULL, last_error = '' WHERE id = $1 AND status = 'claimed' AND claim_owner = $2`, id, owner)
	return requireOne(result, err, "claimed delivery")
}

func (s *PostgresStore) MarkDeliverySentWithReceipt(ctx context.Context, id int64, owner string, receipt pipeline.DeliveryReceipt) error {
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE `+s.table("deliveries")+` SET
    status = 'sent', attempts = attempts + 1, sent_at = COALESCE($4, now()),
    claim_owner = '', claim_expires_at = NULL, last_error = '', receipt = $3,
    provider_message_id = $5, ambiguous_at = NULL
WHERE id = $1 AND status = 'claimed' AND claim_owner = $2`,
		id, owner, encoded, nullableReceiptTime(receipt.AcceptedAt), strings.TrimSpace(receipt.ProviderMessageID))
	return requireOne(result, err, "claimed delivery")
}

func nullableReceiptTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func (s *PostgresStore) MarkDeliveryAmbiguous(ctx context.Context, id int64, owner, message string, at time.Time) error {
	if at.IsZero() {
		at = s.now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `UPDATE `+s.table("deliveries")+` SET
    status = 'uncertain', attempts = attempts + 1, ambiguous_at = $3,
    last_error = $4, claim_owner = '', claim_expires_at = NULL
WHERE id = $1 AND status = 'claimed' AND claim_owner = $2`,
		id, owner, at.UTC(), pipeline.TruncateText(message, 1000))
	return requireOne(result, err, "claimed delivery")
}

func (s *PostgresStore) MarkDeliveryFailed(ctx context.Context, id int64, owner, message string, retryAt time.Time) error {
	if retryAt.IsZero() {
		retryAt = s.now().UTC()
	}
	message = pipeline.TruncateText(message, 1000)
	result, err := s.db.ExecContext(ctx, `
UPDATE `+s.table("deliveries")+` SET
	status = 'pending', attempts = attempts + 1, next_attempt_at = $3,
	last_error = $4, claim_owner = '', claim_expires_at = NULL
WHERE id = $1 AND status = 'claimed' AND claim_owner = $2`, id, owner, retryAt, message)
	return requireOne(result, err, "claimed delivery")
}

func requireOne(result sql.Result, err error, name string) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("lite: %s not found", name)
	}
	return nil
}

func (s *PostgresStore) RecordSourceSuccess(ctx context.Context, sourceID string, observedCount int, at time.Time) error {
	if strings.TrimSpace(sourceID) == "" || observedCount < 0 {
		return errors.New("lite: valid source and non-negative observed count are required")
	}
	if at.IsZero() {
		at = s.now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO `+s.table("source_status")+` AS current (source_id, state, observed_count, last_attempt_at, last_success_at)
VALUES ($1, 'success', $2, $3, $3)
ON CONFLICT (source_id) DO UPDATE SET state = 'success', observed_count = EXCLUDED.observed_count,
    last_attempt_at = EXCLUDED.last_attempt_at, last_success_at = EXCLUDED.last_success_at,
	consecutive_failures = 0, last_error = '', failure_code = ''
WHERE current.last_attempt_at <= EXCLUDED.last_attempt_at`, sourceID, observedCount, at)
	return err
}

func (s *PostgresStore) RecordSourceFailure(ctx context.Context, sourceID string, cause error, at time.Time) error {
	if strings.TrimSpace(sourceID) == "" {
		return errors.New("lite: source is required")
	}
	if at.IsZero() {
		at = s.now().UTC()
	}
	message := "unknown failure"
	if cause != nil {
		message = cause.Error()
	}
	message = pipeline.TruncateText(message, 1000)
	failureCode, _ := pipeline.DiscoveryFailureClass(cause)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO `+s.table("source_status")+` AS current (source_id, state, observed_count, last_attempt_at, last_failure_at, consecutive_failures, last_error, failure_code)
VALUES ($1, 'failure', 0, $2, $2, 1, $3, $4)
ON CONFLICT (source_id) DO UPDATE SET state = 'failure', observed_count = 0,
    last_attempt_at = EXCLUDED.last_attempt_at, last_failure_at = EXCLUDED.last_failure_at,
	consecutive_failures = current.consecutive_failures + 1, last_error = EXCLUDED.last_error,
	failure_code = EXCLUDED.failure_code
WHERE current.last_attempt_at <= EXCLUDED.last_attempt_at`, sourceID, at, message, failureCode)
	return err
}

func (s *PostgresStore) GetSourceStatus(ctx context.Context, sourceID string) (SourceStatus, error) {
	var status SourceStatus
	err := s.db.QueryRowContext(ctx, `SELECT source_id, state, observed_count, last_attempt_at, last_success_at, last_failure_at, consecutive_failures, last_error, failure_code FROM `+s.table("source_status")+` WHERE source_id = $1`, sourceID).Scan(
		&status.SourceID, &status.State, &status.ObservedCount, &status.LastAttemptAt,
		&status.LastSuccessAt, &status.LastFailureAt, &status.ConsecutiveFailures, &status.LastError, &status.FailureCode,
	)
	return status, err
}

func (s *PostgresStore) ListSourceControls(ctx context.Context) ([]pipeline.SourceControl, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source_id, state, reason, actor, updated_at FROM `+s.table("source_controls")+` ORDER BY source_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var controls []pipeline.SourceControl
	for rows.Next() {
		var control pipeline.SourceControl
		if err := rows.Scan(&control.SourceID, &control.State, &control.Reason, &control.Actor, &control.UpdatedAt); err != nil {
			return nil, err
		}
		controls = append(controls, control)
	}
	return controls, rows.Err()
}

func (s *PostgresStore) QuarantineSource(ctx context.Context, sourceID, reason, actor string, at time.Time) error {
	return s.setSourceControl(ctx, sourceID, "quarantined", reason, actor, at)
}

func (s *PostgresStore) RestoreSource(ctx context.Context, sourceID, reason, actor string, at time.Time) error {
	return s.setSourceControl(ctx, sourceID, "active", reason, actor, at)
}

func (s *PostgresStore) setSourceControl(ctx context.Context, sourceID, state, reason, actor string, at time.Time) error {
	sourceID, reason, actor = strings.TrimSpace(sourceID), strings.TrimSpace(reason), strings.TrimSpace(actor)
	if sourceID == "" || (state != "active" && state != "quarantined") {
		return errors.New("radar: valid source control is required")
	}
	if reason == "" {
		return errors.New("radar: source control reason is required")
	}
	if actor == "" {
		actor = "operator"
	}
	if at.IsZero() {
		at = s.now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO `+s.table("source_controls")+` (source_id, state, reason, actor, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (source_id) DO UPDATE SET state = EXCLUDED.state, reason = EXCLUDED.reason,
    actor = EXCLUDED.actor, updated_at = EXCLUDED.updated_at`, sourceID, state, pipeline.TruncateText(reason, 1000), actor, at.UTC()); err != nil {
		return err
	}
	action := state
	if state == "active" {
		action = "restored"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+s.table("source_events")+` (source_id, action, reason, actor, created_at) VALUES ($1, $2, $3, $4, $5)`,
		sourceID, action, pipeline.TruncateText(reason, 1000), actor, at.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ExplainSource(ctx context.Context, sourceID string) (pipeline.SourceExplanation, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return pipeline.SourceExplanation{}, errors.New("radar: source is required")
	}
	explanation := pipeline.SourceExplanation{SourceID: sourceID, Events: []pipeline.SourceEvent{}, DiscoveryEvents: []map[string]any{}}
	var control pipeline.SourceControl
	if err := s.db.QueryRowContext(ctx, `SELECT source_id, state, reason, actor, updated_at FROM `+s.table("source_controls")+` WHERE source_id = $1`, sourceID).Scan(
		&control.SourceID, &control.State, &control.Reason, &control.Actor, &control.UpdatedAt,
	); err == nil {
		explanation.Control = &control
	} else if !errors.Is(err, sql.ErrNoRows) {
		return explanation, err
	}
	if status, err := s.GetSourceStatus(ctx, sourceID); err == nil {
		explanation.Status = &status
	} else if !errors.Is(err, sql.ErrNoRows) {
		return explanation, err
	}
	var candidateID, company, provider, rawURL, state, failureCode, lastError string
	if err := s.db.QueryRowContext(ctx, `SELECT candidate_id, company, provider, url, state, failure_code, last_error FROM `+s.table("discovered_sources")+` WHERE id = $1`, sourceID).Scan(
		&candidateID, &company, &provider, &rawURL, &state, &failureCode, &lastError,
	); err == nil {
		explanation.DiscoverySource = map[string]any{"candidate_id": candidateID, "company": company, "provider": provider, "url": rawURL, "state": state, "failure_code": failureCode, "last_error": lastError}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return explanation, err
	}
	eventRows, err := s.db.QueryContext(ctx, `SELECT action, reason, actor, created_at FROM `+s.table("source_events")+` WHERE source_id = $1 ORDER BY created_at DESC LIMIT 20`, sourceID)
	if err != nil {
		return explanation, err
	}
	for eventRows.Next() {
		var event pipeline.SourceEvent
		if err := eventRows.Scan(&event.Action, &event.Reason, &event.Actor, &event.CreatedAt); err != nil {
			eventRows.Close()
			return explanation, err
		}
		explanation.Events = append(explanation.Events, event)
	}
	if err := eventRows.Close(); err != nil {
		return explanation, err
	}
	if candidateID != "" {
		rows, err := s.db.QueryContext(ctx, `SELECT outcome, code, detail, evidence, created_at FROM `+s.table("discovery_events")+` WHERE candidate_id = $1 ORDER BY created_at DESC LIMIT 20`, candidateID)
		if err != nil {
			return explanation, err
		}
		for rows.Next() {
			var outcome, code, detail, evidence string
			var createdAt time.Time
			if err := rows.Scan(&outcome, &code, &detail, &evidence, &createdAt); err != nil {
				rows.Close()
				return explanation, err
			}
			explanation.DiscoveryEvents = append(explanation.DiscoveryEvents, map[string]any{"outcome": outcome, "code": code, "detail": detail, "evidence": evidence, "created_at": createdAt})
		}
		if err := rows.Close(); err != nil {
			return explanation, err
		}
	}
	return explanation, nil
}

// ListPostings returns the compact durable fields needed by Radar's
// read-only feed. Eligibility remains a deterministic product rule applied by
// the caller, so changing the rule never requires rewriting stored jobs.
func (s *PostgresStore) ListPostings(ctx context.Context) ([]Posting, error) {
	rows, err := s.queryCompatiblePostings(ctx, true, true)
	if isUndefinedColumn(err) {
		// Read-only processes intentionally do not migrate. During a rolling
		// deployment, retain active provenance filtering even if the writer has
		// not added link-health columns yet.
		rows, err = s.queryCompatiblePostings(ctx, true, false)
	}
	if isUndefinedColumn(err) {
		// Older schemas may also predate active provenance.
		rows, err = s.queryCompatiblePostings(ctx, false, false)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	postings := make([]Posting, 0)
	for rows.Next() {
		var posting Posting
		if err := rows.Scan(
			&posting.ID, &posting.Company, &posting.Title, &posting.Location,
			&posting.Country, &posting.EmploymentType, &posting.Level,
			&posting.ApplyURL, &posting.PostedAt, &posting.FirstSeenAt, &posting.LastSeenAt,
		); err != nil {
			return nil, err
		}
		postings = append(postings, posting)
	}
	return postings, rows.Err()
}

func (s *PostgresStore) queryCompatiblePostings(ctx context.Context, activeOnly, hideGone bool) (*sql.Rows, error) {
	fields := [][2]string{{"country", "posted_at"}, {"country", "NULL::timestamptz"}, {"''::text", "NULL::timestamptz"}}
	var lastErr error
	for _, field := range fields {
		rows, err := s.queryPostings(ctx, field[0], field[1], activeOnly, hideGone)
		if err == nil {
			return rows, nil
		}
		if !isUndefinedColumn(err) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

func isUndefinedColumn(err error) bool {
	var postgresError *pq.Error
	return errors.As(err, &postgresError) && postgresError.Code == "42703"
}

func (s *PostgresStore) queryPostings(ctx context.Context, countryExpression, postedAtExpression string, activeOnly, hideGone bool) (*sql.Rows, error) {
	activePredicate := ""
	if activeOnly {
		activePredicate = " AND observation.active"
	}
	linkPredicate := ""
	if hideGone {
		linkPredicate = " AND apply_url_state <> 'gone'"
	}
	return s.db.QueryContext(ctx, `
SELECT id, company, title, location, `+countryExpression+`, employment_type, level, apply_url,
       `+postedAtExpression+`, first_seen_at, last_seen_at
FROM `+s.table("jobs")+`
WHERE 1=1 `+linkPredicate+`
AND EXISTS (
    SELECT 1
    FROM `+s.table("job_source_observations")+` AS observation
    LEFT JOIN `+s.table("discovered_sources")+` AS discovered
      ON discovered.id = observation.source_id
	WHERE observation.job_id = `+s.table("jobs")+`.id
	  `+activePredicate+`
	  AND observation.source_id NOT LIKE 'market-%'
      AND (discovered.id IS NULL OR discovered.state = 'promoted')
	  AND NOT EXISTS (SELECT 1 FROM `+s.table("source_controls")+` control WHERE control.source_id = observation.source_id AND control.state = 'quarantined')
)
ORDER BY first_seen_at DESC, company, title, id`)
}

// ListApplyURLsDue returns a bounded fair queue of currently active postings.
// A missing check row is due immediately after an additive migration.
func (s *PostgresStore) ListApplyURLsDue(ctx context.Context, at time.Time, limit int) ([]ApplyURLCandidate, error) {
	if limit <= 0 {
		return nil, errors.New("apply URL check limit must be positive")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT jobs.id, jobs.apply_url, jobs.apply_url_state, jobs.apply_url_consecutive_gone
FROM `+s.table("jobs")+` AS jobs
WHERE jobs.apply_url <> ''
  AND (jobs.apply_url_next_check_at IS NULL OR jobs.apply_url_next_check_at <= $1)
  AND EXISTS (
    SELECT 1
    FROM `+s.table("job_source_observations")+` AS observation
    LEFT JOIN `+s.table("discovered_sources")+` AS discovered
      ON discovered.id = observation.source_id
    WHERE observation.job_id = jobs.id
      AND observation.active
      AND observation.source_id NOT LIKE 'market-%'
      AND (discovered.id IS NULL OR discovered.state = 'promoted')
	  AND NOT EXISTS (SELECT 1 FROM `+s.table("source_controls")+` control WHERE control.source_id = observation.source_id AND control.state = 'quarantined')
  )
ORDER BY
  CASE
    WHEN jobs.apply_url_state = 'unchecked' THEN 0
    WHEN EXISTS (
      SELECT 1 FROM `+s.table("deliveries")+` delivery
      WHERE delivery.job_id = jobs.id AND delivery.status IN ('staged', 'pending', 'claimed')
    ) THEN 1
    WHEN jobs.apply_url_state = 'live' THEN 2
    ELSE 3
  END,
  jobs.first_seen_at DESC,
  jobs.apply_url_next_check_at NULLS FIRST,
  jobs.apply_url_checked_at NULLS FIRST
LIMIT $2`, at.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []ApplyURLCandidate
	for rows.Next() {
		var candidate ApplyURLCandidate
		if err := rows.Scan(&candidate.JobID, &candidate.ApplyURL, &candidate.State, &candidate.ConsecutiveGone); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

// RecordApplyURLCheck ignores a stale result when the source refreshed the job
// to a different URL while the request was in flight. Two consecutive terminal
// results are required before the feed hides the posting.
func (s *PostgresStore) RecordApplyURLCheck(ctx context.Context, check ApplyURLCheck) error {
	outcome := strings.TrimSpace(check.Outcome)
	if outcome != pipeline.ApplyURLLive && outcome != pipeline.ApplyURLGone && outcome != pipeline.ApplyURLUnchecked {
		return fmt.Errorf("invalid apply URL outcome %q", check.Outcome)
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE `+s.table("jobs")+` SET
  apply_url_state = CASE
    WHEN $3 = 'live' THEN 'live'
    WHEN $3 = 'gone' AND apply_url_consecutive_gone + 1 >= 2 THEN 'gone'
    ELSE apply_url_state
  END,
  apply_url_consecutive_gone = CASE
    WHEN $3 = 'live' THEN 0
    WHEN $3 = 'gone' THEN apply_url_consecutive_gone + 1
    ELSE apply_url_consecutive_gone
  END,
  apply_url_checked_at = $5,
  apply_url_next_check_at = $6,
  apply_url_last_status = NULLIF($4, 0)
WHERE id = $1 AND apply_url = $2`,
		check.JobID, pipeline.CanonicalApplyURL(check.ApplyURL), outcome, check.StatusCode,
		check.CheckedAt.UTC(), check.NextCheckAt.UTC())
	return err
}

// ListSourceStatuses exposes the latest outcome for each routine source. A
// healthy empty source remains a success with observed_count=0.
func (s *PostgresStore) ListSourceStatuses(ctx context.Context) ([]SourceStatus, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT source_id, state, observed_count, last_attempt_at, last_success_at,
       last_failure_at, consecutive_failures, last_error, failure_code
FROM `+s.table("source_status")+` AS current
WHERE NOT EXISTS (
    SELECT 1 FROM `+s.table("discovered_sources")+` discovered
    WHERE discovered.id = current.source_id
      AND discovered.state <> 'promoted'
)
ORDER BY source_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	statuses := make([]SourceStatus, 0)
	for rows.Next() {
		var status SourceStatus
		if err := rows.Scan(
			&status.SourceID, &status.State, &status.ObservedCount,
			&status.LastAttemptAt, &status.LastSuccessAt, &status.LastFailureAt,
			&status.ConsecutiveFailures, &status.LastError, &status.FailureCode,
		); err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, rows.Err()
}

// ReadOperationalState returns one repeatable-read view across jobs, identity
// aliases, source provenance, discovery, routine source health, and delivery
// decisions. Dashboard callers never have to stitch together counts captured
// at different moments.
func (s *PostgresStore) ReadOperationalState(ctx context.Context) (OperationalState, error) {
	state := OperationalState{
		GeneratedAt:      s.now().UTC(),
		DeliveryCounts:   make(map[string]int),
		CandidateCounts:  make(map[string]int),
		DiscoveredCounts: make(map[string]int),
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return OperationalState{}, fmt.Errorf("lite operational state: %w", err)
	}
	defer tx.Rollback()

	countQueries := []struct {
		destination *int
		query       string
	}{
		{&state.CanonicalJobs, `SELECT count(*) FROM ` + s.table("jobs") + ` AS job
            WHERE EXISTS (
                SELECT 1 FROM ` + s.table("job_source_observations") + ` observation
                LEFT JOIN ` + s.table("discovered_sources") + ` discovered ON discovered.id = observation.source_id
                WHERE observation.job_id = job.id AND observation.source_id NOT LIKE 'market-%'
                  AND (discovered.id IS NULL OR discovered.state = 'promoted')
				  AND NOT EXISTS (SELECT 1 FROM ` + s.table("source_controls") + ` control WHERE control.source_id = observation.source_id AND control.state = 'quarantined')
            )`},
		{&state.IdentityAliases, `SELECT count(*) FROM ` + s.table("job_identities") + ` AS identity
            WHERE EXISTS (
                SELECT 1 FROM ` + s.table("job_source_observations") + ` observation
                LEFT JOIN ` + s.table("discovered_sources") + ` discovered ON discovered.id = observation.source_id
                WHERE observation.job_id = identity.job_id AND observation.source_id NOT LIKE 'market-%'
                  AND (discovered.id IS NULL OR discovered.state = 'promoted')
				  AND NOT EXISTS (SELECT 1 FROM ` + s.table("source_controls") + ` control WHERE control.source_id = observation.source_id AND control.state = 'quarantined')
            )`},
		{&state.SourceObservations, `SELECT count(*) FROM ` + s.table("job_source_observations") + ` observation
            LEFT JOIN ` + s.table("discovered_sources") + ` discovered ON discovered.id = observation.source_id
            WHERE observation.source_id NOT LIKE 'market-%'
			  AND (discovered.id IS NULL OR discovered.state = 'promoted')
			  AND NOT EXISTS (SELECT 1 FROM ` + s.table("source_controls") + ` control WHERE control.source_id = observation.source_id AND control.state = 'quarantined')`},
		{&state.MultiSourceJobs, `SELECT count(*) FROM (
            SELECT observation.job_id FROM ` + s.table("job_source_observations") + ` observation
            LEFT JOIN ` + s.table("discovered_sources") + ` discovered ON discovered.id = observation.source_id
            WHERE observation.source_id NOT LIKE 'market-%'
              AND (discovered.id IS NULL OR discovered.state = 'promoted')
			  AND NOT EXISTS (SELECT 1 FROM ` + s.table("source_controls") + ` control WHERE control.source_id = observation.source_id AND control.state = 'quarantined')
            GROUP BY observation.job_id HAVING count(DISTINCT observation.source_id) > 1
        ) AS converged`},
	}
	for _, item := range countQueries {
		if err := tx.QueryRowContext(ctx, item.query).Scan(item.destination); err != nil {
			return OperationalState{}, fmt.Errorf("lite operational state: %w", err)
		}
	}
	if err := readGroupedCounts(ctx, tx, `SELECT status, count(*) FROM `+s.table("deliveries")+` GROUP BY status`, state.DeliveryCounts); err != nil {
		return OperationalState{}, fmt.Errorf("lite operational deliveries: %w", err)
	}
	if err := readGroupedCounts(ctx, tx, `SELECT state, count(*) FROM `+s.table("discovery_candidates")+` GROUP BY state`, state.CandidateCounts); err != nil {
		return OperationalState{}, fmt.Errorf("lite operational candidates: %w", err)
	}
	if err := readGroupedCounts(ctx, tx, `SELECT state, count(*) FROM `+s.table("discovered_sources")+` GROUP BY state`, state.DiscoveredCounts); err != nil {
		return OperationalState{}, fmt.Errorf("lite operational discovered sources: %w", err)
	}
	dueQueries := []struct {
		destination *int
		query       string
	}{
		{&state.DiscoveryDue, `SELECT count(*) FROM ` + s.table("discovery_candidates") + `
            WHERE state IN ('pending', 'retry', 'validating', 'promoted') AND next_attempt_at <= $1`},
		{&state.ApplyURLsDue, `SELECT count(*) FROM ` + s.table("jobs") + ` AS jobs
            WHERE jobs.apply_url <> ''
              AND (jobs.apply_url_next_check_at IS NULL OR jobs.apply_url_next_check_at <= $1)
              AND EXISTS (
                  SELECT 1 FROM ` + s.table("job_source_observations") + ` observation
                  LEFT JOIN ` + s.table("discovered_sources") + ` discovered ON discovered.id = observation.source_id
                  WHERE observation.job_id = jobs.id AND observation.active
                    AND observation.source_id NOT LIKE 'market-%'
                    AND (discovered.id IS NULL OR discovered.state = 'promoted')
					AND NOT EXISTS (SELECT 1 FROM ` + s.table("source_controls") + ` control WHERE control.source_id = observation.source_id AND control.state = 'quarantined')
              )`},
		{&state.DeliveriesDue, `SELECT count(*) FROM ` + s.table("deliveries") + `
            WHERE (status IN ('pending', 'failed') AND next_attempt_at <= $1)
               OR (status = 'claimed' AND claim_expires_at <= $1)`},
	}
	for _, item := range dueQueries {
		if err := tx.QueryRowContext(ctx, item.query, state.GeneratedAt).Scan(item.destination); err != nil {
			return OperationalState{}, fmt.Errorf("lite operational due counts: %w", err)
		}
	}
	promotedRows, err := tx.QueryContext(ctx, `
SELECT id, company, provider, url
FROM `+s.table("discovered_sources")+`
WHERE state = 'promoted'
  AND NOT EXISTS (SELECT 1 FROM `+s.table("source_controls")+` control WHERE control.source_id = `+s.table("discovered_sources")+`.id AND control.state = 'quarantined')
ORDER BY company, id`)
	if err != nil {
		return OperationalState{}, fmt.Errorf("lite operational promoted sources: %w", err)
	}
	for promotedRows.Next() {
		var source Source
		if err := promotedRows.Scan(&source.ID, &source.Company, &source.Provider, &source.URL); err != nil {
			promotedRows.Close()
			return OperationalState{}, fmt.Errorf("lite operational promoted sources: %w", err)
		}
		state.PromotedSources = append(state.PromotedSources, source)
	}
	if err := promotedRows.Close(); err != nil {
		return OperationalState{}, fmt.Errorf("lite operational promoted sources: %w", err)
	}
	if err := promotedRows.Err(); err != nil {
		return OperationalState{}, fmt.Errorf("lite operational promoted sources: %w", err)
	}
	controlRows, err := tx.QueryContext(ctx, `SELECT source_id, state, reason, actor, updated_at FROM `+s.table("source_controls")+` ORDER BY source_id`)
	if err != nil {
		return OperationalState{}, fmt.Errorf("lite operational source controls: %w", err)
	}
	for controlRows.Next() {
		var control pipeline.SourceControl
		if err := controlRows.Scan(&control.SourceID, &control.State, &control.Reason, &control.Actor, &control.UpdatedAt); err != nil {
			controlRows.Close()
			return OperationalState{}, fmt.Errorf("lite operational source controls: %w", err)
		}
		state.SourceControls = append(state.SourceControls, control)
	}
	if err := controlRows.Close(); err != nil {
		return OperationalState{}, fmt.Errorf("lite operational source controls: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
SELECT source_id, state, observed_count, last_attempt_at, last_success_at,
       last_failure_at, consecutive_failures, last_error, failure_code
FROM `+s.table("source_status")+` AS current
WHERE NOT EXISTS (
    SELECT 1 FROM `+s.table("discovered_sources")+` discovered
    WHERE discovered.id = current.source_id
      AND discovered.state <> 'promoted'
)
ORDER BY source_id`)
	if err != nil {
		return OperationalState{}, fmt.Errorf("lite operational source status: %w", err)
	}
	for rows.Next() {
		var status SourceStatus
		if err := rows.Scan(
			&status.SourceID, &status.State, &status.ObservedCount,
			&status.LastAttemptAt, &status.LastSuccessAt, &status.LastFailureAt,
			&status.ConsecutiveFailures, &status.LastError, &status.FailureCode,
		); err != nil {
			rows.Close()
			return OperationalState{}, fmt.Errorf("lite operational source status: %w", err)
		}
		state.RoutineSourceStatus = append(state.RoutineSourceStatus, status)
	}
	if err := rows.Close(); err != nil {
		return OperationalState{}, fmt.Errorf("lite operational source status: %w", err)
	}
	if err := rows.Err(); err != nil {
		return OperationalState{}, fmt.Errorf("lite operational source status: %w", err)
	}
	var runtime RuntimeState
	var activeStarted, lastStarted, lastFinished sql.NullTime
	runtimeErr := tx.QueryRowContext(ctx, `
SELECT active_owner, active_started_at, last_cycle_state,
       last_cycle_started_at, last_cycle_finished_at,
       sources_attempted, sources_succeeded, sources_failed,
       observed, created, eligible_created, enqueued,
       deliveries_sent, delivery_failures, last_error, updated_at
FROM `+s.table("runtime_state")+`
WHERE key = 'routine'`).Scan(
		&runtime.ActiveOwner, &activeStarted, &runtime.LastCycleState,
		&lastStarted, &lastFinished,
		&runtime.SourcesAttempted, &runtime.SourcesSucceeded, &runtime.SourcesFailed,
		&runtime.Observed, &runtime.Created, &runtime.EligibleCreated, &runtime.Enqueued,
		&runtime.DeliveriesSent, &runtime.DeliveryFailures, &runtime.LastError, &runtime.UpdatedAt,
	)
	if runtimeErr != nil && !errors.Is(runtimeErr, sql.ErrNoRows) {
		return OperationalState{}, fmt.Errorf("lite operational runtime: %w", runtimeErr)
	}
	if runtimeErr == nil {
		if activeStarted.Valid {
			value := activeStarted.Time
			runtime.ActiveStartedAt = &value
		}
		if lastStarted.Valid {
			value := lastStarted.Time
			runtime.LastCycleStarted = &value
		}
		if lastFinished.Valid {
			value := lastFinished.Time
			runtime.LastCycleFinished = &value
		}
		state.Runtime = &runtime
	}
	if err := tx.Commit(); err != nil {
		return OperationalState{}, fmt.Errorf("lite operational state: %w", err)
	}
	return state, nil
}

func readGroupedCounts(ctx context.Context, tx *sql.Tx, query string, destination map[string]int) error {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return err
		}
		destination[key] = count
	}
	return rows.Err()
}

// SeedDiscoveryCandidates imports the inert discovery seed into durable
// scheduling state. Re-reading the seed updates descriptive fields without
// resetting attempts, health, or promotion decisions, except that a candidate
// parked solely by the company-quality gate becomes retryable when new seed
// evidence now satisfies that gate.
func (s *PostgresStore) SeedDiscoveryCandidates(ctx context.Context, candidates []DiscoveryCandidate) error {
	if err := (DiscoverySeed{Candidates: candidates}).Validate(); err != nil {
		return fmt.Errorf("lite: invalid discovery seed: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, candidate := range candidates {
		tags, err := json.Marshal(candidate.Tags)
		if err != nil {
			return fmt.Errorf("lite: encode discovery tags for %s: %w", candidate.ID, err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO `+s.table("discovery_candidates")+` AS current (id, name, website, tags)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    website = EXCLUDED.website,
    tags = EXCLUDED.tags,
    state = CASE
        WHEN current.state = 'parked'
         AND current.failure_code = $5
         AND $6
        THEN 'retry'
        ELSE current.state
    END,
    next_attempt_at = CASE
        WHEN current.state = 'parked'
         AND current.failure_code = $5
         AND $6
        THEN now()
        ELSE current.next_attempt_at
    END,
    last_error = CASE
        WHEN current.state = 'parked'
         AND current.failure_code = $5
         AND $6
        THEN ''
        ELSE current.last_error
    END,
    failure_code = CASE
        WHEN current.state = 'parked'
         AND current.failure_code = $5
         AND $6
        THEN ''
        ELSE current.failure_code
    END,
    updated_at = now()`,
			candidate.ID, strings.TrimSpace(candidate.Name), strings.TrimSpace(candidate.Website), tags,
			pipeline.DiscoveryFailureCompanyQuality, pipeline.HighSignalDiscoveryCandidate(candidate),
		); err != nil {
			return fmt.Errorf("lite: seed discovery candidate %s: %w", candidate.ID, err)
		}
	}
	return tx.Commit()
}

// RecordRejectedMarketSignal preserves search evidence that was denied
// admission. It is deliberately separate from jobs and monitored sources.
func (s *PostgresStore) RecordRejectedMarketSignal(ctx context.Context, observation Observation, code string, at time.Time) error {
	if at.IsZero() {
		at = s.now().UTC()
	}
	company := pipeline.CompactMarketCompany(observation.Company)
	if company == "" {
		company = strings.TrimSpace(observation.Company)
	}
	candidateID := "market-signal"
	if company != "" {
		candidateID = "market-signal:" + pipeline.CanonicalText(company)
	}
	detail := pipeline.TruncateText(strings.TrimSpace(observation.Title), 500)
	evidence := pipeline.CompactDiscoveryEvidence(observation.ApplyURL)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO `+s.table("discovery_events")+` (candidate_id, source_id, outcome, code, detail, evidence, created_at)
VALUES ($1, $2, 'rejected_signal', $3, $4, $5, $6)`, candidateID, observation.SourceID, strings.TrimSpace(code), detail, evidence, at)
	return err
}

// ListDueDiscoveryCandidates returns only candidates that are not already
// promoted or known duplicates. The durable next_attempt_at field provides
// restart-safe backoff without a queue.
func (s *PostgresStore) ListDueDiscoveryCandidates(ctx context.Context, at time.Time, limit int) ([]DiscoveryCandidateRecord, error) {
	if at.IsZero() {
		at = s.now().UTC()
	}
	if limit <= 0 || limit > 100 {
		limit = pipeline.DefaultDiscoveryBatch
	}
	rows, err := s.db.QueryContext(ctx, `
WITH due AS (
    SELECT id, name, website, tags, state, attempts, next_attempt_at, last_attempt_at, last_error, failure_code,
           CASE
               WHEN tags @> '["auto-market-search"]'::jsonb THEN 0
               WHEN tags @> '["yc-top"]'::jsonb THEN 1
               WHEN tags @> '["quant"]'::jsonb THEN 2
               WHEN tags @> '["big-tech"]'::jsonb THEN 3
               WHEN tags @> '["unicorn"]'::jsonb THEN 4
               ELSE 5
           END AS lane,
           row_number() OVER (
               PARTITION BY CASE
                                WHEN tags @> '["auto-market-search"]'::jsonb THEN 0
                                WHEN tags @> '["yc-top"]'::jsonb THEN 1
                                WHEN tags @> '["quant"]'::jsonb THEN 2
                                WHEN tags @> '["big-tech"]'::jsonb THEN 3
                                WHEN tags @> '["unicorn"]'::jsonb THEN 4
                                ELSE 5
                            END
               ORDER BY CASE
                            WHEN tags @> '["auto-market-search"]'::jsonb THEN 0
                            WHEN tags @> '["priority-1"]'::jsonb THEN 1
                            WHEN tags @> '["priority-2"]'::jsonb THEN 2
                            WHEN tags @> '["priority-3"]'::jsonb THEN 3
                            ELSE 4
                        END,
                        CASE WHEN state = 'promoted' THEN 1 ELSE 0 END,
                        next_attempt_at, id
           ) AS lane_rank
    FROM `+s.table("discovery_candidates")+`
    WHERE state IN ('pending', 'retry', 'validating', 'promoted') AND next_attempt_at <= $1
)
SELECT id, name, website, tags, state, attempts, next_attempt_at, last_attempt_at, last_error, failure_code
FROM due
ORDER BY lane_rank, lane
LIMIT $2`, at, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]DiscoveryCandidateRecord, 0)
	for rows.Next() {
		var record DiscoveryCandidateRecord
		var tags []byte
		if err := rows.Scan(
			&record.ID, &record.Name, &record.Website, &tags, &record.State,
			&record.Attempts, &record.NextAttemptAt, &record.LastAttemptAt, &record.LastError, &record.FailureCode,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(tags, &record.Tags); err != nil {
			return nil, fmt.Errorf("lite: decode discovery tags for %s: %w", record.ID, err)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// RecordDiscoveryFailure preserves candidate/source failure history and
// schedules an automatic retry. A promoted source is not immediately removed:
// routine source health performs that decision after a configurable threshold.
func (s *PostgresStore) RecordDiscoveryFailure(ctx context.Context, candidate DiscoveryCandidateRecord, source *Source, cause error, checkedAt, nextAttemptAt time.Time) error {
	if err := validDiscoveryCandidate(candidate.DiscoveryCandidate); err != nil {
		return err
	}
	if checkedAt.IsZero() {
		checkedAt = s.now().UTC()
	}
	if nextAttemptAt.IsZero() || nextAttemptAt.Before(checkedAt) {
		nextAttemptAt = checkedAt.Add(pipeline.DefaultDiscoveryRetry)
	}
	message := "unknown discovery failure"
	if cause != nil {
		message = cause.Error()
	}
	message = pipeline.CompactDiscoveryError(message)
	failureCode, terminal := pipeline.DiscoveryFailureClass(cause)
	candidateState := "retry"
	sourceState := "unhealthy"
	if terminal {
		candidateState = "parked"
		sourceState = "rejected"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("discovery_candidates")+` SET
    state = CASE WHEN state = 'promoted' THEN state ELSE $6 END,
    attempts = GREATEST(attempts, $5),
    next_attempt_at = $2,
    last_attempt_at = $3,
    last_error = $4,
    failure_code = $7,
    updated_at = $3
WHERE id = $1`, candidate.ID, nextAttemptAt, checkedAt, message, candidate.Attempts+1, candidateState, failureCode); err != nil {
		return err
	}
	if source != nil {
		if err := pipeline.ValidateDiscoverySource(*source); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO `+s.table("discovered_sources")+` AS current (
    id, candidate_id, company, provider, url, state, last_checked_at,
    last_failure_at, consecutive_failures, last_error, failure_code
) VALUES ($1, $2, $3, $4, $5, $8, $6, $6, 1, $7, $9)
ON CONFLICT (provider, url) DO UPDATE SET
    state = CASE WHEN current.state = 'promoted' THEN 'promoted' ELSE EXCLUDED.state END,
    company = EXCLUDED.company,
    last_checked_at = EXCLUDED.last_checked_at,
    last_failure_at = EXCLUDED.last_failure_at,
    consecutive_successes = 0,
    consecutive_failures = current.consecutive_failures + 1,
    last_error = EXCLUDED.last_error,
    failure_code = EXCLUDED.failure_code
WHERE current.candidate_id = EXCLUDED.candidate_id`,
			source.ID, candidate.ID, strings.TrimSpace(source.Company), strings.TrimSpace(source.Provider), strings.TrimSpace(source.URL), checkedAt, message, sourceState, failureCode,
		); err != nil {
			return err
		}
	}
	sourceID := ""
	if source != nil {
		sourceID = strings.TrimSpace(source.ID)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO `+s.table("discovery_events")+` (candidate_id, source_id, outcome, code, detail, created_at)
VALUES ($1, $2, $3, $4, $5, $6)`, candidate.ID, sourceID, candidateState, failureCode, message, checkedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordDiscoverySuccess promotes a non-empty structured board immediately.
// Empty boards remain quarantined: repeated emptiness proves reachability, not
// company ownership, and must never promote a guessed ATS slug.
func (s *PostgresStore) RecordDiscoverySuccess(ctx context.Context, candidate DiscoveryCandidateRecord, source Source, observedCount int, confidence float64, evidence string, checkedAt, nextAttemptAt time.Time) (bool, error) {
	if err := validDiscoveryCandidate(candidate.DiscoveryCandidate); err != nil {
		return false, err
	}
	if err := pipeline.ValidateDiscoverySource(source); err != nil {
		return false, err
	}
	if !pipeline.DiscoveryRouteMatchesCandidate(candidate.DiscoveryCandidate, source.Provider, source.URL) {
		return false, errors.New("lite: discovered source does not match candidate company identity")
	}
	if observedCount < 0 || confidence < 0 || confidence > 1 {
		return false, errors.New("lite: discovery observation count and confidence are invalid")
	}
	if checkedAt.IsZero() {
		checkedAt = s.now().UTC()
	}
	if nextAttemptAt.IsZero() || nextAttemptAt.Before(checkedAt) {
		nextAttemptAt = checkedAt.Add(pipeline.DefaultDiscoveryRetry)
	}
	evidence = pipeline.CompactDiscoveryEvidence(evidence)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "lite-discovery:"+source.Provider+"|"+source.URL); err != nil {
		return false, err
	}

	var owner string
	var previousState string
	var previousSuccesses int
	err = tx.QueryRowContext(ctx, `
SELECT candidate_id, state, consecutive_successes
FROM `+s.table("discovered_sources")+`
WHERE provider = $1 AND url = $2
FOR UPDATE`, source.Provider, source.URL).Scan(&owner, &previousState, &previousSuccesses)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && owner != candidate.ID {
		if _, updateErr := tx.ExecContext(ctx, `
UPDATE `+s.table("discovery_candidates")+` SET
    state = 'duplicate', attempts = GREATEST(attempts, $4), last_attempt_at = $2,
    last_success_at = $2, last_error = $3, updated_at = $2
WHERE id = $1`, candidate.ID, checkedAt, "source already owned by discovery candidate "+owner, candidate.Attempts+1); updateErr != nil {
			return false, updateErr
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}

	successes := previousSuccesses + 1
	promoted := previousState == "promoted" || observedCount > 0
	state := "candidate"
	var promotedAt any
	if promoted {
		state = "promoted"
		promotedAt = checkedAt
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO `+s.table("discovered_sources")+` AS current (
    id, candidate_id, company, provider, url, state, confidence, observed_count,
    consecutive_successes, consecutive_failures, last_checked_at, last_success_at,
    promoted_at, last_error, evidence
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 0, $10, $10, $11, '', $12)
ON CONFLICT (provider, url) DO UPDATE SET
    company = EXCLUDED.company,
    state = EXCLUDED.state,
    confidence = GREATEST(current.confidence, EXCLUDED.confidence),
    observed_count = EXCLUDED.observed_count,
    consecutive_successes = EXCLUDED.consecutive_successes,
    consecutive_failures = 0,
    last_checked_at = EXCLUDED.last_checked_at,
    last_success_at = EXCLUDED.last_success_at,
    promoted_at = COALESCE(current.promoted_at, EXCLUDED.promoted_at),
    last_error = '',
    failure_code = '',
    evidence = EXCLUDED.evidence`,
		source.ID, candidate.ID, strings.TrimSpace(source.Company), strings.TrimSpace(source.Provider), strings.TrimSpace(source.URL),
		state, confidence, observedCount, successes, checkedAt, promotedAt, evidence,
	); err != nil {
		return false, err
	}
	candidateState := "validating"
	if promoted {
		candidateState = "promoted"
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("discovery_candidates")+` SET
    state = $2, attempts = GREATEST(attempts, $5), next_attempt_at = $3,
    last_attempt_at = $4, last_success_at = $4, last_error = '', failure_code = '', updated_at = $4
WHERE id = $1`, candidate.ID, candidateState, nextAttemptAt, checkedAt, candidate.Attempts+1); err != nil {
		return false, err
	}
	outcome := "admitted"
	if !promoted {
		outcome = "healthy_empty"
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO `+s.table("discovery_events")+` (candidate_id, source_id, outcome, code, evidence, created_at)
VALUES ($1, $2, $3, '', $4, $5)`, candidate.ID, source.ID, outcome, evidence, checkedAt); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return promoted && previousState != "promoted", nil
}

func (s *PostgresStore) ListPromotedSources(ctx context.Context) ([]Source, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, company, provider, url
FROM `+s.table("discovered_sources")+`
WHERE state = 'promoted'
ORDER BY company, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := make([]Source, 0)
	for rows.Next() {
		var source Source
		if err := rows.Scan(&source.ID, &source.Company, &source.Provider, &source.URL); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

// ListDiscoveredSources returns every durable discovery route except catalog
// duplicates. The market promoter uses this to avoid bypassing the candidate
// retry scheduler for validating or unhealthy boards.
func (s *PostgresStore) ListDiscoveredSources(ctx context.Context) ([]Source, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, company, provider, url
FROM `+s.table("discovered_sources")+`
WHERE state <> 'duplicate'
ORDER BY company, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := make([]Source, 0)
	for rows.Next() {
		var source Source
		if err := rows.Scan(&source.ID, &source.Company, &source.Provider, &source.URL); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

// SuppressKnownDiscoveredSources removes registry redundancy when broad search
// rediscovers a board already owned by the verified catalog. The discovery
// evidence remains durable, but the duplicate source can no longer inflate
// health counts or enter routine crawling.
func (s *PostgresStore) SuppressKnownDiscoveredSources(ctx context.Context, known []Source, at time.Time) (int, error) {
	if len(known) == 0 {
		return 0, nil
	}
	if at.IsZero() {
		at = s.now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	suppressedCandidates := make(map[string]struct{})
	for _, source := range known {
		rows, queryErr := tx.QueryContext(ctx, `
UPDATE `+s.table("discovered_sources")+` SET
    state = 'duplicate', last_error = 'source is already in verified catalog', last_checked_at = $3
WHERE lower(provider) = lower($1)
  AND lower(rtrim(url, '/')) = lower(rtrim($2, '/'))
  AND state <> 'duplicate'
RETURNING candidate_id`, source.Provider, source.URL, at)
		if queryErr != nil {
			return 0, queryErr
		}
		for rows.Next() {
			var candidateID string
			if scanErr := rows.Scan(&candidateID); scanErr != nil {
				rows.Close()
				return 0, scanErr
			}
			suppressedCandidates[candidateID] = struct{}{}
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return 0, rowsErr
		}
		rows.Close()
	}
	for candidateID := range suppressedCandidates {
		if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("discovery_candidates")+` SET
    state = 'duplicate', last_error = 'source is already in verified catalog', updated_at = $2
WHERE id = $1`, candidateID, at); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(suppressedCandidates), nil
}

// SuppressDuplicateDiscoveredSources compacts historical URL case/trailing
// slash variants while retaining their evidence rows. New discovery routes are
// canonicalized before persistence; this is the restart-safe repair path for
// records created by older versions.
func (s *PostgresStore) SuppressDuplicateDiscoveredSources(ctx context.Context, at time.Time) (int, error) {
	if at.IsZero() {
		at = s.now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
SELECT id, candidate_id, company, provider, url
FROM `+s.table("discovered_sources")+`
WHERE state <> 'duplicate'
ORDER BY CASE state WHEN 'promoted' THEN 0 WHEN 'candidate' THEN 1 ELSE 2 END,
         promoted_at NULLS LAST, last_checked_at, id`)
	if err != nil {
		return 0, err
	}
	type discoveredRoute struct {
		id, candidateID string
		source          Source
	}
	seen := make(map[string]struct{})
	duplicates := make([]discoveredRoute, 0)
	for rows.Next() {
		var route discoveredRoute
		if err := rows.Scan(&route.id, &route.candidateID, &route.source.Company, &route.source.Provider, &route.source.URL); err != nil {
			rows.Close()
			return 0, err
		}
		key := pipeline.MarketSourceKey(route.source)
		if _, exists := seen[key]; exists {
			duplicates = append(duplicates, route)
			continue
		}
		seen[key] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, duplicate := range duplicates {
		if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("discovered_sources")+` SET
    state = 'duplicate', last_error = 'canonical discovery route is already registered', last_checked_at = $2
WHERE id = $1`, duplicate.id, at); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("discovery_candidates")+` AS candidate SET
    state = 'duplicate', last_error = 'canonical discovery route is already registered', updated_at = $2
WHERE candidate.id = $1
  AND NOT EXISTS (
      SELECT 1 FROM `+s.table("discovered_sources")+` AS source
      WHERE source.candidate_id = candidate.id AND source.state <> 'duplicate'
  )`, duplicate.candidateID, at); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(duplicates), nil
}

// DemoteUnhealthyDiscoveredSources reconnects discovery with routine health:
// after repeated real crawl failures, the board stops monitoring and its
// company becomes due for source resolution again.
func (s *PostgresStore) DemoteUnhealthyDiscoveredSources(ctx context.Context, failureThreshold int, at time.Time) (int, error) {
	if failureThreshold <= 0 {
		failureThreshold = pipeline.DefaultDiscoveryFailureThreshold
	}
	if at.IsZero() {
		at = s.now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	demotedCandidates := make(map[string]struct{})
	aggregatorRows, err := tx.QueryContext(ctx, `
SELECT id, name, website
FROM `+s.table("discovery_candidates")+`
WHERE tags @> '["auto-market-search"]'::jsonb
ORDER BY id`)
	if err != nil {
		return 0, err
	}
	type aggregatorCandidate struct{ id, name, website string }
	var blockedCandidates []aggregatorCandidate
	type renamedCandidate struct{ id, name string }
	var renamedCandidates []renamedCandidate
	for aggregatorRows.Next() {
		var candidate aggregatorCandidate
		if err := aggregatorRows.Scan(&candidate.id, &candidate.name, &candidate.website); err != nil {
			aggregatorRows.Close()
			return 0, err
		}
		if pipeline.BlockedMarketCandidateName(candidate.name) || pipeline.BlockedMarketCandidateWebsite(candidate.website) {
			blockedCandidates = append(blockedCandidates, candidate)
		} else if canonicalName := pipeline.CompactMarketCompany(candidate.name); canonicalName != "" && canonicalName != candidate.name {
			renamedCandidates = append(renamedCandidates, renamedCandidate{id: candidate.id, name: canonicalName})
		}
	}
	if err := aggregatorRows.Close(); err != nil {
		return 0, err
	}
	if err := aggregatorRows.Err(); err != nil {
		return 0, err
	}
	for _, candidate := range blockedCandidates {
		if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("discovery_candidates")+` SET
    state = 'duplicate', last_error = 'market result points to blocked job aggregator', updated_at = $2
WHERE id = $1 AND state IN ('pending', 'retry', 'validating')`, candidate.id, at); err != nil {
			return 0, err
		}
	}
	for _, candidate := range renamedCandidates {
		if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("discovery_candidates")+` SET name = $2, updated_at = $3 WHERE id = $1`, candidate.id, candidate.name, at); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("discovered_sources")+` SET company = $2 WHERE candidate_id = $1`, candidate.id, candidate.name); err != nil {
			return 0, err
		}
	}
	identityRows, err := tx.QueryContext(ctx, `
SELECT discovered.id, discovered.candidate_id, discovered.company, discovered.provider, discovered.url,
       candidate.name, candidate.website, candidate.tags
FROM `+s.table("discovered_sources")+` AS discovered
JOIN `+s.table("discovery_candidates")+` AS candidate ON candidate.id = discovered.candidate_id
WHERE discovered.state IN ('promoted', 'candidate')
ORDER BY discovered.id`)
	if err != nil {
		return 0, err
	}
	type invalidRoute struct {
		sourceID       string
		candidateID    string
		reason         string
		candidateState string
		sourceState    string
		failureCode    string
	}
	var invalidRoutes []invalidRoute
	for identityRows.Next() {
		var source Source
		var candidate DiscoveryCandidate
		var tags []byte
		if err := identityRows.Scan(&source.ID, &candidate.ID, &source.Company, &source.Provider, &source.URL, &candidate.Name, &candidate.Website, &tags); err != nil {
			identityRows.Close()
			return 0, err
		}
		if err := json.Unmarshal(tags, &candidate.Tags); err != nil {
			identityRows.Close()
			return 0, err
		}
		switch {
		case !pipeline.HighSignalDiscoveryCandidate(candidate):
			invalidRoutes = append(invalidRoutes, invalidRoute{
				sourceID: source.ID, candidateID: candidate.ID,
				reason: "company lacks high-signal target evidence", candidateState: "parked",
				sourceState: "rejected", failureCode: pipeline.DiscoveryFailureCompanyQuality,
			})
		case pipeline.BlockedCompany(source.Company), pipeline.BlockedCompany(candidate.Name),
			pipeline.BlockedMarketCandidateName(source.Company), pipeline.BlockedMarketCandidateName(candidate.Name):
			invalidRoutes = append(invalidRoutes, invalidRoute{
				sourceID: source.ID, candidateID: candidate.ID,
				reason: "company excluded by target policy", candidateState: "duplicate", sourceState: "unhealthy",
			})
		case source.Provider == "official_careers" && pipeline.BlockedMarketCandidateWebsite(candidate.Website):
			invalidRoutes = append(invalidRoutes, invalidRoute{
				sourceID: source.ID, candidateID: candidate.ID,
				reason: "official route belongs to a job aggregator", candidateState: "duplicate", sourceState: "unhealthy",
			})
		case !pipeline.DiscoveryRouteMatchesCandidate(candidate, source.Provider, source.URL):
			invalidRoutes = append(invalidRoutes, invalidRoute{
				sourceID: source.ID, candidateID: candidate.ID,
				reason: "discovered board identity does not match candidate company", candidateState: "retry", sourceState: "unhealthy",
			})
		}
	}
	if err := identityRows.Close(); err != nil {
		return 0, err
	}
	if err := identityRows.Err(); err != nil {
		return 0, err
	}
	for _, invalid := range invalidRoutes {
		if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("discovered_sources")+` SET
    state = $4, last_checked_at = $2, last_failure_at = $2,
    consecutive_failures = consecutive_failures + 1,
    last_error = $3, failure_code = $5
WHERE id = $1 AND state IN ('promoted', 'candidate')`, invalid.sourceID, at, invalid.reason, invalid.sourceState, invalid.failureCode); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("discovery_candidates")+` SET
	state = $3, next_attempt_at = $2,
	last_error = $4, failure_code = $5, updated_at = $2
WHERE id = $1`, invalid.candidateID, at, invalid.candidateState, invalid.reason, invalid.failureCode); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+s.table("discovery_events")+` (candidate_id, source_id, outcome, code, detail, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
			invalid.candidateID, invalid.sourceID, invalid.candidateState, invalid.failureCode, invalid.reason, at); err != nil {
			return 0, err
		}
		demotedCandidates[invalid.candidateID] = struct{}{}
	}
	rows, err := tx.QueryContext(ctx, `
UPDATE `+s.table("discovered_sources")+` AS discovered SET
    state = 'unhealthy',
    consecutive_failures = status.consecutive_failures,
    last_checked_at = status.last_attempt_at,
    last_failure_at = status.last_failure_at,
    last_error = status.last_error
FROM `+s.table("source_status")+` AS status
WHERE discovered.id = status.source_id
  AND discovered.state = 'promoted'
  AND status.state = 'failure'
  AND status.consecutive_failures >= $1
RETURNING discovered.candidate_id`, failureThreshold)
	if err != nil {
		return 0, err
	}
	var candidates []string
	for rows.Next() {
		var candidateID string
		if err := rows.Scan(&candidateID); err != nil {
			rows.Close()
			return 0, err
		}
		if _, alreadyDemoted := demotedCandidates[candidateID]; !alreadyDemoted {
			candidates = append(candidates, candidateID)
			demotedCandidates[candidateID] = struct{}{}
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, candidateID := range candidates {
		if _, err := tx.ExecContext(ctx, `
UPDATE `+s.table("discovery_candidates")+` SET
    state = 'retry', next_attempt_at = $2, last_error = 'promoted source became unhealthy', updated_at = $2
WHERE id = $1`, candidateID, at); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(demotedCandidates), nil
}

func validDiscoveryCandidate(candidate DiscoveryCandidate) error {
	if err := (DiscoverySeed{Candidates: []DiscoveryCandidate{candidate}}).Validate(); err != nil {
		return fmt.Errorf("lite: invalid discovery candidate: %w", err)
	}
	return nil
}

func (s *PostgresStore) SetBootstrapState(ctx context.Context, key string, value json.RawMessage) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("lite: bootstrap key is required")
	}
	if len(value) == 0 {
		value = json.RawMessage(`{}`)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO `+s.table("bootstrap_state")+` (key, value, updated_at) VALUES ($1, $2, $3) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`, key, value, s.now().UTC())
	return err
}

func (s *PostgresStore) GetBootstrapState(ctx context.Context, key string) (BootstrapState, error) {
	var state BootstrapState
	var value []byte
	err := s.db.QueryRowContext(ctx, `SELECT key, value, updated_at FROM `+s.table("bootstrap_state")+` WHERE key = $1`, key).Scan(&state.Key, &value, &state.UpdatedAt)
	state.Value = append(json.RawMessage(nil), value...)
	return state, err
}
