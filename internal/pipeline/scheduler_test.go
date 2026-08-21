package pipeline

import (
	"testing"
	"time"
)

func TestScheduleSourcesPrioritizesUnseenThenOldest(t *testing.T) {
	now := time.Date(2026, 8, 21, 3, 0, 0, 0, time.UTC)
	sources := []Source{{ID: "b"}, {ID: "c"}, {ID: "a"}}
	statuses := []SourceStatus{
		{SourceID: "a", State: "success", LastAttemptAt: now.Add(-time.Hour)},
		{SourceID: "b", State: "success", LastAttemptAt: now.Add(-2 * time.Hour)},
	}
	got := ScheduleSources(sources, statuses, now, 2)
	if len(got) != 2 || got[0].ID != "c" || got[1].ID != "b" {
		t.Fatalf("scheduled = %#v, want unseen c then oldest b", got)
	}
}

func TestScheduleSourcesBacksOffFailuresDurably(t *testing.T) {
	now := time.Date(2026, 8, 21, 3, 0, 0, 0, time.UTC)
	failedAt := now.Add(-29 * time.Minute)
	sources := []Source{{ID: "retrying"}, {ID: "healthy"}}
	statuses := []SourceStatus{
		{SourceID: "retrying", State: "failure", LastAttemptAt: failedAt, LastFailureAt: &failedAt, ConsecutiveFailures: 2},
		{SourceID: "healthy", State: "success", LastAttemptAt: now.Add(-time.Hour)},
	}
	got := ScheduleSources(sources, statuses, now, 10)
	if len(got) != 1 || got[0].ID != "healthy" {
		t.Fatalf("scheduled = %#v, want only healthy while retry is backed off", got)
	}

	got = ScheduleSources(sources, statuses, now.Add(time.Minute), 10)
	if len(got) != 2 || got[0].ID != "healthy" || got[1].ID != "retrying" {
		t.Fatalf("scheduled after backoff = %#v", got)
	}
}

func TestSourceRetryDelayCaps(t *testing.T) {
	if got := SourceRetryDelay(1); got != 15*time.Minute {
		t.Fatalf("first retry = %s", got)
	}
	if got := SourceRetryDelay(100); got != MaxSourceRetryDelay {
		t.Fatalf("capped retry = %s", got)
	}
}
