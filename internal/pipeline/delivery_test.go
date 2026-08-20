package pipeline

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type deliveryStoreFake struct {
	mu               sync.Mutex
	claimed          []Delivery
	sent             []int64
	failed           []int64
	released         []int64
	claimLimits      []int
	claimChannels    []string
	claimRecipients  []string
	retryAt          time.Time
	retryAts         []time.Time
	onClaim          func()
	finalizeCanceled []bool
}

func (s *deliveryStoreFake) ClaimDeliveries(_ context.Context, _, channel, recipient string, limit int, _ time.Duration) ([]Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimLimits = append(s.claimLimits, limit)
	s.claimChannels = append(s.claimChannels, channel)
	s.claimRecipients = append(s.claimRecipients, recipient)
	if len(s.claimed) == 0 {
		return nil, nil
	}
	delivery := s.claimed[0]
	s.claimed = s.claimed[1:]
	if s.onClaim != nil {
		s.onClaim()
	}
	return []Delivery{delivery}, nil
}
func (s *deliveryStoreFake) ReleaseDelivery(ctx context.Context, id int64, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizeCanceled = append(s.finalizeCanceled, ctx.Err() != nil)
	s.released = append(s.released, id)
	return nil
}
func (s *deliveryStoreFake) MarkDeliverySent(ctx context.Context, id int64, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizeCanceled = append(s.finalizeCanceled, ctx.Err() != nil)
	s.sent = append(s.sent, id)
	return nil
}
func (s *deliveryStoreFake) MarkDeliveryFailed(ctx context.Context, id int64, _, _ string, retryAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizeCanceled = append(s.finalizeCanceled, ctx.Err() != nil)
	s.failed = append(s.failed, id)
	s.retryAt = retryAt
	s.retryAts = append(s.retryAts, retryAt)
	return nil
}

func TestDeliveryRetryDelayIsExponentialAndCapped(t *testing.T) {
	for _, test := range []struct {
		attempts int
		want     time.Duration
	}{
		{attempts: 0, want: time.Minute},
		{attempts: 1, want: 2 * time.Minute},
		{attempts: 5, want: 32 * time.Minute},
		{attempts: 20, want: maxDeliveryRetryDelay},
		{attempts: 1000, want: maxDeliveryRetryDelay},
	} {
		if got := deliveryRetryDelay(time.Minute, test.attempts); got != test.want {
			t.Fatalf("attempts=%d delay=%s, want %s", test.attempts, got, test.want)
		}
	}
	if got := deliveryRetryDelay(12*time.Hour, 0); got != maxDeliveryRetryDelay {
		t.Fatalf("oversized base delay=%s, want %s", got, maxDeliveryRetryDelay)
	}
}

func TestDeliveryDrainerUsesExistingAttemptsForCappedBackoff(t *testing.T) {
	now := time.Date(2026, 8, 17, 1, 0, 0, 0, time.UTC)
	store := &deliveryStoreFake{claimed: []Delivery{{ID: 9, Channel: "telegram", Recipient: "chat-1", Attempts: 99}}}
	drainer := DeliveryDrainer{
		Store: store, Sender: senderFunc(func(context.Context, Delivery) error { return errors.New("offline") }),
		Owner: "lite-1", Channel: "telegram", Recipient: "chat-1", Limit: 1,
		RetryDelay: time.Minute, Now: func() time.Time { return now },
	}
	if report, err := drainer.Drain(context.Background()); err != nil || report.Failed != 1 {
		t.Fatalf("Drain report=%#v error=%v", report, err)
	}
	if want := now.Add(maxDeliveryRetryDelay); len(store.retryAts) != 1 || !store.retryAts[0].Equal(want) {
		t.Fatalf("retry times=%v, want [%s]", store.retryAts, want)
	}
}

func TestDeliveryDrainerReleasesClaimCanceledBeforeSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &deliveryStoreFake{claimed: []Delivery{{ID: 1, Channel: "telegram", Recipient: "chat-1"}, {ID: 2, Channel: "telegram", Recipient: "chat-1"}}, onClaim: cancel}
	sends := 0
	drainer := DeliveryDrainer{Store: store, Sender: senderFunc(func(context.Context, Delivery) error {
		sends++
		return nil
	}), Owner: "lite-1", Channel: "telegram", Recipient: "chat-1", Limit: 10}

	report, err := drainer.Drain(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Drain error = %v, want context canceled", err)
	}
	if report.Claimed != 1 || sends != 0 || len(store.released) != 1 || store.released[0] != 1 {
		t.Fatalf("report=%#v sends=%d released=%v", report, sends, store.released)
	}
	if len(store.claimed) != 1 || store.claimed[0].ID != 2 {
		t.Fatalf("unprocessed queue = %#v, want delivery 2", store.claimed)
	}
	if store.finalizeCanceled[0] {
		t.Fatal("release reused the canceled parent context")
	}
}

func TestDeliveryDrainerFinalizesSuccessfulSendAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &deliveryStoreFake{claimed: []Delivery{{ID: 1, Channel: "telegram", Recipient: "chat-1"}}}
	drainer := DeliveryDrainer{Store: store, Sender: senderFunc(func(context.Context, Delivery) error {
		cancel()
		return nil
	}), Owner: "lite-1", Channel: "telegram", Recipient: "chat-1", Limit: 1}

	report, err := drainer.Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Sent != 1 || len(store.sent) != 1 || store.sent[0] != 1 {
		t.Fatalf("report=%#v sent=%v", report, store.sent)
	}
	if store.finalizeCanceled[0] {
		t.Fatal("sent finalization reused the canceled parent context")
	}
}

type senderFunc func(context.Context, Delivery) error

func (f senderFunc) Send(ctx context.Context, delivery Delivery) error { return f(ctx, delivery) }

func TestDeliveryDrainerRejectsAndReleasesMismatchedClaim(t *testing.T) {
	store := &deliveryStoreFake{claimed: []Delivery{{ID: 7, Channel: "log", Recipient: "chat-1"}}}
	sends := 0
	drainer := DeliveryDrainer{
		Store: store, Owner: "lite-1", Channel: "telegram", Recipient: "chat-1", Limit: 1,
		Sender: senderFunc(func(context.Context, Delivery) error {
			sends++
			return nil
		}),
	}

	report, err := drainer.Drain(context.Background())
	if err == nil || !strings.Contains(err.Error(), "target mismatch") {
		t.Fatalf("Drain error = %v, want target mismatch", err)
	}
	if report.Claimed != 1 || sends != 0 || len(store.released) != 1 || store.released[0] != 7 {
		t.Fatalf("report=%#v sends=%d released=%v", report, sends, store.released)
	}
}

func TestDeliveryDrainerRequiresClaimTarget(t *testing.T) {
	store := &deliveryStoreFake{}
	drainer := DeliveryDrainer{Store: store, Sender: senderFunc(func(context.Context, Delivery) error { return nil }), Owner: "lite-1"}
	if _, err := drainer.Drain(context.Background()); err == nil || !strings.Contains(err.Error(), "channel and recipient") {
		t.Fatalf("Drain error = %v, want target validation", err)
	}
}

func TestDeliveryDrainerMarksSuccessAndSchedulesFailedSend(t *testing.T) {
	now := time.Date(2026, 8, 16, 2, 0, 0, 0, time.UTC)
	store := &deliveryStoreFake{claimed: []Delivery{{ID: 1, Channel: "telegram", Recipient: "chat-1"}, {ID: 2, Channel: "telegram", Recipient: "chat-1"}}}
	drainer := DeliveryDrainer{
		Store: store, Owner: "lite-1", Channel: "telegram", Recipient: "chat-1", RetryDelay: 3 * time.Minute, Now: func() time.Time { return now },
		Sender: senderFunc(func(_ context.Context, delivery Delivery) error {
			if delivery.ID == 2 {
				return errors.New("telegram unavailable")
			}
			return nil
		}),
	}

	report, err := drainer.Drain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Claimed != 2 || report.Sent != 1 || report.Failed != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if len(store.sent) != 1 || store.sent[0] != 1 || len(store.failed) != 1 || store.failed[0] != 2 {
		t.Fatalf("unexpected delivery updates: sent=%v failed=%v", store.sent, store.failed)
	}
	for _, limit := range store.claimLimits {
		if limit != 1 {
			t.Fatalf("claim limit = %d, want one-at-a-time claims", limit)
		}
	}
	for index := range store.claimChannels {
		if store.claimChannels[index] != "telegram" || store.claimRecipients[index] != "chat-1" {
			t.Fatalf("claim target = %q/%q", store.claimChannels[index], store.claimRecipients[index])
		}
	}
	if want := now.Add(3 * time.Minute); !store.retryAt.Equal(want) {
		t.Fatalf("retry at = %s, want %s", store.retryAt, want)
	}
}

func TestDeliveryDrainerPacesMessagesAfterTheFirst(t *testing.T) {
	store := &deliveryStoreFake{claimed: []Delivery{
		{ID: 1, Channel: "telegram", Recipient: "chat-1"},
		{ID: 2, Channel: "telegram", Recipient: "chat-1"},
		{ID: 3, Channel: "telegram", Recipient: "chat-1"},
	}}
	var waits []time.Duration
	drainer := DeliveryDrainer{
		Store: store, Owner: "lite-1", Channel: "telegram", Recipient: "chat-1", Limit: 3,
		MinInterval: 1100 * time.Millisecond,
		Wait: func(_ context.Context, interval time.Duration) error {
			waits = append(waits, interval)
			return nil
		},
		Sender: senderFunc(func(context.Context, Delivery) error { return nil }),
	}

	report, err := drainer.Drain(context.Background())
	if err != nil || report.Sent != 3 {
		t.Fatalf("Drain report=%#v error=%v", report, err)
	}
	if len(waits) != 2 || waits[0] != 1100*time.Millisecond || waits[1] != 1100*time.Millisecond {
		t.Fatalf("delivery waits=%v, want two 1.1s intervals", waits)
	}
}
