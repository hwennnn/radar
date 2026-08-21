package pipeline

import (
	"sort"
	"time"
)

const MaxSourceRetryDelay = 24 * time.Hour

// SourceRetryDelay keeps a persistently failing route from consuming every
// cycle while still revisiting it automatically. attempts is the number of
// consecutive failures already recorded for the route.
func SourceRetryDelay(attempts int) time.Duration {
	delay := 15 * time.Minute
	for attempt := 1; attempt < attempts; attempt++ {
		if delay >= MaxSourceRetryDelay/2 {
			return MaxSourceRetryDelay
		}
		delay *= 2
	}
	return delay
}

// ScheduleSources selects a fair, bounded routine batch. Never-observed routes
// run first, followed by the least recently attempted due routes. Failure
// backoff is derived from durable source status, so restarts cannot reset it.
func ScheduleSources(sources []Source, statuses []SourceStatus, now time.Time, limit int) []Source {
	if limit <= 0 || limit > len(sources) {
		limit = len(sources)
	}
	byID := make(map[string]SourceStatus, len(statuses))
	for _, status := range statuses {
		byID[status.SourceID] = status
	}

	type candidate struct {
		source      Source
		lastAttempt time.Time
	}
	due := make([]candidate, 0, len(sources))
	for _, source := range sources {
		status, observed := byID[source.ID]
		if observed && status.State == "failure" && status.LastFailureAt != nil &&
			now.Before(status.LastFailureAt.Add(SourceRetryDelay(status.ConsecutiveFailures))) {
			continue
		}
		due = append(due, candidate{source: source, lastAttempt: status.LastAttemptAt})
	}
	sort.SliceStable(due, func(i, j int) bool {
		left, right := due[i], due[j]
		if left.lastAttempt.IsZero() != right.lastAttempt.IsZero() {
			return left.lastAttempt.IsZero()
		}
		if !left.lastAttempt.Equal(right.lastAttempt) {
			return left.lastAttempt.Before(right.lastAttempt)
		}
		return left.source.ID < right.source.ID
	})
	if len(due) > limit {
		due = due[:limit]
	}
	selected := make([]Source, len(due))
	for index := range due {
		selected[index] = due[index].source
	}
	return selected
}
