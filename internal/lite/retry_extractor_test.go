package lite

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hwennnn/radar/internal/tinyfish"
)

type temporaryExtractionError struct{}

func (temporaryExtractionError) Error() string   { return "temporary network failure" }
func (temporaryExtractionError) Timeout() bool   { return true }
func (temporaryExtractionError) Temporary() bool { return true }

func TestRetryingExtractorRecoversTransientFailure(t *testing.T) {
	calls := 0
	retryAttempt := 0
	extractor := NewRetryingExtractor(extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
		calls++
		if calls == 1 {
			return ExtractionResult{}, temporaryExtractionError{}
		}
		return completeExtraction(Observation{Title: "Software Engineer Intern"}), nil
	}), ExtractionRetryOptions{
		MaxAttempts: 2,
		OnRetry: func(_ Source, nextAttempt int, _ error) {
			retryAttempt = nextAttempt
		},
	})

	result, err := extractor.Extract(context.Background(), Source{ID: "source"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || retryAttempt != 2 || len(result.Observations) != 1 {
		t.Fatalf("calls=%d retry_attempt=%d result=%#v", calls, retryAttempt, result)
	}
}

func TestRetryingExtractorDoesNotRetryDeterministicFailure(t *testing.T) {
	calls := 0
	want := errors.New("company identity mismatch")
	extractor := NewRetryingExtractor(extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
		calls++
		return ExtractionResult{}, want
	}), ExtractionRetryOptions{MaxAttempts: 2})

	_, err := extractor.Extract(context.Background(), Source{ID: "source"})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestRetryingExtractorStopsDuringBackoffCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	extractor := NewRetryingExtractor(extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
		return ExtractionResult{}, temporaryExtractionError{}
	}), ExtractionRetryOptions{
		MaxAttempts: 2,
		Delay:       time.Minute,
		OnRetry: func(Source, int, error) {
			cancel()
		},
	})

	started := time.Now()
	_, err := extractor.Extract(ctx, Source{ID: "source"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context cancellation", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("cancellation did not interrupt retry delay")
	}
}

func TestRetryingExtractorRecoversRetryableHTTPStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "typed tinyfish rate limit", err: &tinyfish.HTTPError{Method: "GET", StatusCode: 429}},
		{name: "embedded tinyfish rate limit", err: &tinyfish.APIError{Code: "rate_limited", Message: "quota exhausted"}},
		{name: "tinyfish provider batch unavailable", err: errors.New("tinyfish fetch failed for all selected search results: provider_unavailable")},
		{name: "ATS status text", err: errors.New("ats fetch failed: 503 Service Unavailable")},
		{name: "provider status text", err: errors.New("GET https://jobs.example.test returned HTTP 502")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			extractor := NewRetryingExtractor(extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
				calls++
				if calls == 1 {
					return ExtractionResult{}, test.err
				}
				return completeExtraction(Observation{Title: "Software Engineer Intern"}), nil
			}), ExtractionRetryOptions{MaxAttempts: 2})

			if _, err := extractor.Extract(context.Background(), Source{ID: "source"}); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("calls=%d, want 2", calls)
			}
		})
	}
}

func TestRetryingExtractorDoesNotRetryPermanentHTTPStatus(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 422} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			extractor := NewRetryingExtractor(extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
				calls++
				return ExtractionResult{}, fmt.Errorf("ats fetch failed: %d permanent", status)
			}), ExtractionRetryOptions{MaxAttempts: 3})

			if _, err := extractor.Extract(context.Background(), Source{ID: "source"}); err == nil || calls != 1 {
				t.Fatalf("error=%v calls=%d", err, calls)
			}
		})
	}
}

func TestRetryingExtractorDefersLongRetryAfterToDurableScheduler(t *testing.T) {
	calls := 0
	extractor := NewRetryingExtractor(extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
		calls++
		return ExtractionResult{}, &tinyfish.HTTPError{
			Method: "GET", StatusCode: 429, RetryAfterDelay: time.Minute,
		}
	}), ExtractionRetryOptions{MaxAttempts: 3, Delay: time.Millisecond, MaxDelay: time.Second})

	if _, err := extractor.Extract(context.Background(), Source{ID: "source"}); err == nil || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}
