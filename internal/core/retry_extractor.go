package core

import (
	"context"
	"errors"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hwennnn/radar/internal/source/tinyfish"
)

var extractionHTTPStatusPattern = regexp.MustCompile(`(?i)(?:returned\s+http|http(?:\s+status)?\s*:|(?:fetch|post|request)\s+failed:)\s*(\d{3})\b`)

// ExtractionRetryOptions controls a small, cancellation-aware retry boundary
// around source extraction. Retries are intentionally limited to transient
// transport failures; parser, identity, and empty-board decisions still fail
// closed on their first result.
type ExtractionRetryOptions struct {
	MaxAttempts int
	Delay       time.Duration
	MaxDelay    time.Duration
	OnRetry     func(Source, int, error)
}

type retryingExtractor struct {
	inner       Extractor
	maxAttempts int
	delay       time.Duration
	maxDelay    time.Duration
	onRetry     func(Source, int, error)
}

func NewRetryingExtractor(inner Extractor, opts ExtractionRetryOptions) Extractor {
	attempts := opts.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	return &retryingExtractor{
		inner:       inner,
		maxAttempts: attempts,
		delay:       opts.Delay,
		maxDelay:    opts.MaxDelay,
		onRetry:     opts.OnRetry,
	}
}

func (e *retryingExtractor) Extract(ctx context.Context, source Source) (ExtractionResult, error) {
	if e == nil || e.inner == nil {
		return ExtractionResult{}, errors.New("lite: retry extractor requires an inner extractor")
	}
	var result ExtractionResult
	var err error
	for attempt := 1; attempt <= e.maxAttempts; attempt++ {
		result, err = e.inner.Extract(ctx, source)
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil || attempt == e.maxAttempts || !transientExtractionError(err) {
			return result, err
		}
		delay, retryNow := transientRetryDelay(err, e.delay, e.maxDelay, attempt)
		if !retryNow {
			return result, err
		}
		if e.onRetry != nil {
			e.onRetry(source, attempt+1, err)
		}
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
		}
	}
	return result, err
}

func transientExtractionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var tinyFishHTTPError *tinyfish.HTTPError
	if errors.As(err, &tinyFishHTTPError) {
		return transientHTTPStatus(tinyFishHTTPError.StatusCode)
	}
	var tinyFishAPIError *tinyfish.APIError
	if errors.As(err, &tinyFishAPIError) {
		return transientTinyFishCode(tinyFishAPIError.Code)
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "tinyfish") {
		for _, code := range []string{
			"rate_limited", "provider_unavailable", "service_unavailable",
			"temporarily_unavailable", "internal_error", "overloaded", "timed out", "timeout",
		} {
			if strings.Contains(message, code) {
				return true
			}
		}
	}
	if matches := extractionHTTPStatusPattern.FindStringSubmatch(err.Error()); len(matches) == 2 {
		status, parseErr := strconv.Atoi(matches[1])
		return parseErr == nil && transientHTTPStatus(status)
	}
	var networkError net.Error
	return errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())
}

func transientTinyFishCode(code string) bool {
	code = strings.ToLower(strings.TrimSpace(code))
	switch code {
	case "rate_limited", "provider_unavailable", "service_unavailable", "temporarily_unavailable", "internal_error", "overloaded", "timeout":
		return true
	default:
		return false
	}
}

func transientHTTPStatus(status int) bool {
	return status == 408 || status == 425 || status == 429 || status >= 500 && status <= 599
}

type retryAfterError interface {
	RetryAfter() time.Duration
}

// transientRetryDelay returns a bounded exponential delay for an immediate
// retry. A provider Retry-After longer than maxDelay is deliberately not
// shortened: the caller returns the failure so its durable scheduler can retry
// later without hammering a rate-limited service.
func transientRetryDelay(err error, baseDelay, maxDelay time.Duration, failedAttempt int) (time.Duration, bool) {
	delay := baseDelay
	if failedAttempt < 1 {
		failedAttempt = 1
	}
	for range failedAttempt - 1 {
		if maxDelay > 0 && delay >= maxDelay/2 {
			delay = maxDelay
			break
		}
		delay *= 2
	}
	var hinted retryAfterError
	if errors.As(err, &hinted) {
		if retryAfter := hinted.RetryAfter(); retryAfter > 0 {
			if maxDelay > 0 && retryAfter > maxDelay {
				return 0, false
			}
			delay = retryAfter
		}
	}
	if maxDelay > 0 && delay > maxDelay {
		delay = maxDelay
	}
	return delay, true
}
