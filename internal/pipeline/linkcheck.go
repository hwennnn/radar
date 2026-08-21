package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	ApplyURLUnchecked = "unchecked"
	ApplyURLLive      = "live"
	ApplyURLGone      = "gone"
)

// ApplyURLCandidate is a durable job URL whose next validation is due.
type ApplyURLCandidate struct {
	JobID           string
	ApplyURL        string
	State           string
	ConsecutiveGone int
}

// ApplyURLCheck records one bounded validation attempt.
type ApplyURLCheck struct {
	JobID       string
	ApplyURL    string
	Outcome     string
	StatusCode  int
	CheckedAt   time.Time
	NextCheckAt time.Time
}

type ApplyURLStore interface {
	ListApplyURLsDue(context.Context, time.Time, int) ([]ApplyURLCandidate, error)
	RecordApplyURLCheck(context.Context, ApplyURLCheck) error
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type ApplyURLChecker struct {
	Store        ApplyURLStore
	Client       HTTPDoer
	Limit        int
	HealthyAfter time.Duration
	GoneRetry    time.Duration
	UnknownRetry time.Duration
	Now          func() time.Time
}

type ApplyURLReport struct {
	Attempted int
	Live      int
	Gone      int
	Unknown   int
	Errors    []error
}

// Run revisits a bounded set of active apply URLs. Two consecutive terminal
// responses are required before persistence hides a posting; transient
// failures remain visible and retry with backoff.
func (c ApplyURLChecker) Run(ctx context.Context) (ApplyURLReport, error) {
	var report ApplyURLReport
	if c.Store == nil || c.Client == nil {
		return report, errors.New("apply URL checker requires a store and HTTP client")
	}
	limit := c.Limit
	if limit <= 0 {
		limit = 32
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	checkedAt := now().UTC()
	candidates, err := c.Store.ListApplyURLsDue(ctx, checkedAt, limit)
	if err != nil {
		return report, fmt.Errorf("list apply URLs due: %w", err)
	}
	var persistenceErrors []error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return report, errors.Join(err, errors.Join(persistenceErrors...))
		}
		report.Attempted++
		outcome, statusCode, requestErr := c.check(ctx, candidate.ApplyURL)
		if requestErr != nil {
			report.Errors = append(report.Errors, fmt.Errorf("check %s: %w", candidate.JobID, requestErr))
		}
		next := checkedAt.Add(c.unknownRetry())
		switch outcome {
		case ApplyURLLive:
			report.Live++
			next = checkedAt.Add(c.healthyAfter())
		case ApplyURLGone:
			report.Gone++
			if candidate.ConsecutiveGone+1 >= 2 {
				next = checkedAt.Add(c.healthyAfter())
			} else {
				next = checkedAt.Add(c.goneRetry())
			}
		default:
			report.Unknown++
		}
		if err := c.Store.RecordApplyURLCheck(ctx, ApplyURLCheck{
			JobID: candidate.JobID, ApplyURL: candidate.ApplyURL, Outcome: outcome,
			StatusCode: statusCode, CheckedAt: checkedAt, NextCheckAt: next,
		}); err != nil {
			persistenceErrors = append(persistenceErrors, fmt.Errorf("record apply URL check %s: %w", candidate.JobID, err))
		}
	}
	return report, errors.Join(persistenceErrors...)
}

func (c ApplyURLChecker) check(ctx context.Context, applyURL string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, applyURL, nil)
	if err != nil {
		return ApplyURLUnchecked, 0, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "Radar link monitor/1.0")
	response, err := c.Client.Do(req)
	if err != nil {
		return ApplyURLUnchecked, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		return ApplyURLGone, response.StatusCode, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ApplyURLUnchecked, response.StatusCode, nil
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return ApplyURLUnchecked, response.StatusCode, readErr
	}
	if softNotFound(body) {
		return ApplyURLGone, response.StatusCode, nil
	}
	return ApplyURLLive, response.StatusCode, nil
}

func softNotFound(body []byte) bool {
	text := strings.ToLower(string(body))
	for _, marker := range []string{
		"dbexception404page",
		"<title>404",
		"<title>page not found",
		"data-testid=\"not-found\"",
		"data-testid='not-found'",
		"\"statuscode\":404",
		">job not found<",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func (c ApplyURLChecker) healthyAfter() time.Duration {
	if c.HealthyAfter > 0 {
		return c.HealthyAfter
	}
	return 6 * time.Hour
}

func (c ApplyURLChecker) goneRetry() time.Duration {
	if c.GoneRetry > 0 {
		return c.GoneRetry
	}
	return 30 * time.Minute
}

func (c ApplyURLChecker) unknownRetry() time.Duration {
	if c.UnknownRetry > 0 {
		return c.UnknownRetry
	}
	return time.Hour
}
