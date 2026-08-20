package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type DeliveryStore interface {
	ClaimDeliveries(context.Context, string, string, string, int, time.Duration) ([]Delivery, error)
	ReleaseDelivery(context.Context, int64, string) error
	MarkDeliverySent(context.Context, int64, string) error
	MarkDeliveryFailed(context.Context, int64, string, string, time.Time) error
}

// Sender is transport-neutral. Telegram is one implementation, but the durable
// claiming and retry behavior is testable without network calls.
type Sender interface {
	Send(context.Context, Delivery) error
}

type DeliveryDrainer struct {
	Store       DeliveryStore
	Sender      Sender
	Owner       string
	Channel     string
	Recipient   string
	Limit       int
	Lease       time.Duration
	RetryDelay  time.Duration
	MinInterval time.Duration
	Now         func() time.Time
	Wait        func(context.Context, time.Duration) error
}

type DeliveryReport struct {
	Claimed int
	Sent    int
	Failed  int
	Errors  []error
}

const deliveryFinalizationTimeout = 5 * time.Second
const maxDeliveryRetryDelay = 6 * time.Hour

func deliveryRetryDelay(base time.Duration, attempts int) time.Duration {
	if base <= 0 {
		base = time.Minute
	}
	if base >= maxDeliveryRetryDelay {
		return maxDeliveryRetryDelay
	}
	for range attempts {
		if base >= maxDeliveryRetryDelay/2 {
			return maxDeliveryRetryDelay
		}
		base *= 2
	}
	return base
}

func (d DeliveryDrainer) Drain(ctx context.Context) (DeliveryReport, error) {
	var report DeliveryReport
	if d.Store == nil || d.Sender == nil {
		return report, errors.New("lite: delivery store and sender are required")
	}
	if strings.TrimSpace(d.Owner) == "" {
		return report, errors.New("lite: delivery owner is required")
	}
	channel, recipient := strings.TrimSpace(d.Channel), strings.TrimSpace(d.Recipient)
	if channel == "" || recipient == "" {
		return report, errors.New("lite: delivery channel and recipient are required")
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	wait := waitForDeliveryInterval
	if d.Wait != nil {
		wait = d.Wait
	}
	limit := d.Limit
	if limit <= 0 || limit > 500 {
		limit = 25
	}
	for report.Claimed < limit {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		deliveries, err := d.Store.ClaimDeliveries(ctx, d.Owner, channel, recipient, 1, d.Lease)
		if err != nil {
			return report, fmt.Errorf("lite: claim deliveries: %w", err)
		}
		if len(deliveries) == 0 {
			break
		}
		delivery := deliveries[0]
		report.Claimed++
		if report.Claimed > 1 && d.MinInterval > 0 {
			if err := wait(ctx, d.MinInterval); err != nil {
				finalizeCtx, cancel := d.finalizationContext(ctx)
				releaseErr := d.Store.ReleaseDelivery(finalizeCtx, delivery.ID, d.Owner)
				cancel()
				if releaseErr != nil {
					report.Errors = append(report.Errors, fmt.Errorf("delivery %d release: %w", delivery.ID, releaseErr))
				}
				return report, errors.Join(err, errors.Join(report.Errors...))
			}
		}
		if delivery.Channel != channel || delivery.Recipient != recipient {
			mismatchErr := fmt.Errorf("delivery %d target mismatch: got %q/%q, want %q/%q", delivery.ID, delivery.Channel, delivery.Recipient, channel, recipient)
			finalizeCtx, cancel := d.finalizationContext(ctx)
			releaseErr := d.Store.ReleaseDelivery(finalizeCtx, delivery.ID, d.Owner)
			cancel()
			if releaseErr != nil {
				mismatchErr = errors.Join(mismatchErr, fmt.Errorf("delivery %d release: %w", delivery.ID, releaseErr))
			}
			return report, mismatchErr
		}
		if err := ctx.Err(); err != nil {
			finalizeCtx, cancel := d.finalizationContext(ctx)
			releaseErr := d.Store.ReleaseDelivery(finalizeCtx, delivery.ID, d.Owner)
			cancel()
			if releaseErr != nil {
				report.Errors = append(report.Errors, fmt.Errorf("delivery %d release: %w", delivery.ID, releaseErr))
			}
			return report, errors.Join(err, errors.Join(report.Errors...))
		}
		if sendErr := d.Sender.Send(ctx, delivery); sendErr != nil {
			report.Failed++
			finalizeCtx, cancel := d.finalizationContext(ctx)
			retryAt := now().UTC().Add(deliveryRetryDelay(d.RetryDelay, delivery.Attempts))
			markErr := d.Store.MarkDeliveryFailed(finalizeCtx, delivery.ID, d.Owner, sendErr.Error(), retryAt)
			cancel()
			if markErr != nil {
				report.Errors = append(report.Errors, fmt.Errorf("delivery %d mark failed: %w", delivery.ID, markErr))
			}
			continue
		}
		finalizeCtx, cancel := d.finalizationContext(ctx)
		err = d.Store.MarkDeliverySent(finalizeCtx, delivery.ID, d.Owner)
		cancel()
		if err != nil {
			report.Errors = append(report.Errors, fmt.Errorf("delivery %d mark sent: %w", delivery.ID, err))
			continue
		}
		report.Sent++
	}
	return report, errors.Join(report.Errors...)
}

func waitForDeliveryInterval(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (d DeliveryDrainer) finalizationContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), deliveryFinalizationTimeout)
}
