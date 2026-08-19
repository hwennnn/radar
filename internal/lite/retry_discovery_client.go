package lite

import (
	"context"
	"errors"
	"time"

	"github.com/hwennnn/radar/internal/tinyfish"
)

// DiscoveryClientRetryOptions controls bounded, in-process retries for the
// TinyFish search/fetch calls used to resolve a company into an official job
// source. Exhausted failures still flow into DiscoveryRunner's durable retry
// schedule.
type DiscoveryClientRetryOptions struct {
	MaxAttempts int
	Delay       time.Duration
	MaxDelay    time.Duration
	OnRetry     func(operation string, nextAttempt int, delay time.Duration, err error)
}

type retryingTinyFishDiscoveryClient struct {
	inner       TinyFishDiscoveryClient
	maxAttempts int
	delay       time.Duration
	maxDelay    time.Duration
	onRetry     func(string, int, time.Duration, error)
}

func NewRetryingTinyFishDiscoveryClient(inner TinyFishDiscoveryClient, opts DiscoveryClientRetryOptions) TinyFishDiscoveryClient {
	attempts := opts.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	return &retryingTinyFishDiscoveryClient{
		inner: inner, maxAttempts: attempts, delay: opts.Delay,
		maxDelay: opts.MaxDelay, onRetry: opts.OnRetry,
	}
}

func (c *retryingTinyFishDiscoveryClient) Search(ctx context.Context, request tinyfish.SearchRequest) (tinyfish.SearchResponse, error) {
	return retryDiscoveryRequest(ctx, c, "search", func(ctx context.Context) (tinyfish.SearchResponse, error) {
		return c.inner.Search(ctx, request)
	})
}

func (c *retryingTinyFishDiscoveryClient) Fetch(ctx context.Context, request tinyfish.FetchRequest) (tinyfish.FetchResponse, error) {
	return retryDiscoveryRequest(ctx, c, "fetch", func(ctx context.Context) (tinyfish.FetchResponse, error) {
		return c.inner.Fetch(ctx, request)
	})
}

func retryDiscoveryRequest[T any](ctx context.Context, client *retryingTinyFishDiscoveryClient, operation string, request func(context.Context) (T, error)) (T, error) {
	var result T
	if client == nil || client.inner == nil {
		return result, errors.New("lite discovery: retry client requires an inner client")
	}
	for attempt := 1; attempt <= client.maxAttempts; attempt++ {
		var err error
		result, err = request(ctx)
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil || attempt == client.maxAttempts || !transientExtractionError(err) {
			return result, err
		}
		delay, retryNow := transientRetryDelay(err, client.delay, client.maxDelay, attempt)
		if !retryNow {
			return result, err
		}
		if client.onRetry != nil {
			client.onRetry(operation, attempt+1, delay, err)
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
	return result, errors.New("lite discovery: retry attempts exhausted")
}
