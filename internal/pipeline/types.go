package pipeline

import (
	"encoding/json"
	"time"
)

// Observation is one sighting of a posting from one configured source.
type Observation struct {
	SourceID       string
	SourceNativeID string
	Company        string
	Title          string
	Location       string
	Country        string
	EmploymentType string
	Level          string
	ApplyURL       string
	Description    string
	PostedAt       *time.Time
	ObservedAt     time.Time
	// SnapshotPending keeps provenance visibility unchanged until a complete
	// source pass atomically reconciles its active job set.
	SnapshotPending bool
}

// Posting is Radar's durable, source-independent representation of a job.
type Posting struct {
	ID             string
	Company        string
	Title          string
	Location       string
	Country        string
	EmploymentType string
	Level          string
	ApplyURL       string
	Description    string
	PostedAt       *time.Time
	FirstSeenAt    time.Time
	LastSeenAt     time.Time
}

type DeliveryTarget struct {
	Channel   string
	Recipient string
	Suppress  bool
	Stage     bool
}

// Delivery is one durable, replay-safe notification for a job and recipient.
type Delivery struct {
	ID             int64
	JobID          string
	Channel        string
	Recipient      string
	Payload        json.RawMessage
	Status         string
	Attempts       int
	ClaimOwner     string
	ClaimExpiresAt time.Time
	NextAttemptAt  time.Time
	LastError      string
	CreatedAt      time.Time
	SentAt         *time.Time
}

// SourceStatus preserves the important distinction between a successful empty
// crawl and a failed crawl. LastSuccessAt is advanced by successful zero-result
// runs too.
type SourceStatus struct {
	SourceID            string
	State               string
	ObservedCount       int
	LastAttemptAt       time.Time
	LastSuccessAt       *time.Time
	LastFailureAt       *time.Time
	ConsecutiveFailures int
	LastError           string
}

type BootstrapState struct {
	Key       string
	Value     json.RawMessage
	UpdatedAt time.Time
}

// RuntimeState is the durable handoff between crawler and read-only UI
// processes. Current cycle ownership and the last completed result survive
// process restarts without exposing provider or delivery secrets.
type RuntimeState struct {
	ActiveOwner       string
	ActiveStartedAt   *time.Time
	LastCycleState    string
	LastCycleStarted  *time.Time
	LastCycleFinished *time.Time
	SourcesAttempted  int
	SourcesSucceeded  int
	SourcesFailed     int
	Observed          int
	Created           int
	EligibleCreated   int
	Enqueued          int
	DeliveriesSent    int
	DeliveryFailures  int
	LastError         string
	UpdatedAt         time.Time
}

// CycleResult is the compact, transport-neutral result persisted after one
// routine pass. Status must be success, degraded, or failure.
type CycleResult struct {
	Status           string
	SourcesAttempted int
	SourcesSucceeded int
	SourcesFailed    int
	Observed         int
	Created          int
	EligibleCreated  int
	Enqueued         int
	DeliveriesSent   int
	DeliveryFailures int
	LastError        string
	FinishedAt       time.Time
}

// OperationalState is a transactionally consistent snapshot of Radar's
// durable pipeline state. It is intentionally transport-neutral so the local
// dashboard, health checks, and future operators all read the same truth.
type OperationalState struct {
	GeneratedAt         time.Time
	CanonicalJobs       int
	IdentityAliases     int
	SourceObservations  int
	MultiSourceJobs     int
	DeliveryCounts      map[string]int
	CandidateCounts     map[string]int
	DiscoveredCounts    map[string]int
	PromotedSources     []Source
	RoutineSourceStatus []SourceStatus
	Runtime             *RuntimeState
}
