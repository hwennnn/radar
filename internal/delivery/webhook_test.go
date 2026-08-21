package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestDefaultHTTPClientUsesBoundedPrivateClient(t *testing.T) {
	client, ok := defaultHTTPClient(nil).(*http.Client)
	if !ok {
		t.Fatalf("defaultHTTPClient(nil) = %T, want *http.Client", defaultHTTPClient(nil))
	}
	if client == http.DefaultClient {
		t.Fatal("defaultHTTPClient(nil) returned http.DefaultClient, want bounded private client")
	}
	if client.Timeout != defaultWebhookHTTPTimeout {
		t.Fatalf("timeout = %s, want %s", client.Timeout, defaultWebhookHTTPTimeout)
	}
	if client.Transport == nil || client.Transport == http.DefaultTransport {
		t.Fatalf("transport = %#v, want private-network guarded transport", client.Transport)
	}
}

func TestWebhookDefaultClientBlocksPrivateTargets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	outbox := newTelegramOutboxWithEndpoint(server.URL, "test-chat", nil)
	err := outbox.Enqueue(context.Background(), Message{Body: "Radar match"})
	if err == nil {
		t.Fatal("Enqueue() error = nil, want private target rejection")
	}
	if !strings.Contains(err.Error(), "resolved private address blocked") {
		t.Fatalf("Enqueue() error = %q, want private target rejection", err)
	}
}

func TestDefaultHTTPClientPreservesProvidedClient(t *testing.T) {
	provided := &http.Client{Timeout: 123 * time.Millisecond}
	if got := defaultHTTPClient(provided); got != provided {
		t.Fatal("defaultHTTPClient did not preserve provided client")
	}
}

func TestTelegramOutboxSendsMessage(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("content type = %q, want application/json", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	outbox := newTelegramOutboxWithEndpoint(server.URL, "chat-123", server.Client())
	err := outbox.Enqueue(context.Background(), Message{
		Subject: "Backend intern at Stripe",
		Body:    "Radar high-signal match. Apply: https://example.com",
		Metadata: map[string]string{
			"title":     "Backend intern",
			"company":   "Stripe",
			"location":  "New York, NY",
			"score":     "93",
			"reason":    "you rated Stripe yes",
			"apply_url": "https://example.com",
		},
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if payload["chat_id"] != "chat-123" {
		t.Fatalf("chat_id = %#v, want chat-123", payload["chat_id"])
	}
	text, _ := payload["text"].(string)
	want := "✨ <b>Role · Stripe</b>\n<a href=\"https://example.com\">Backend intern ↗</a>\n📍 New York, NY\n⭐ <b>93/100 fit</b>\n💡 you rated Stripe yes"
	if text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
	if payload["parse_mode"] != "HTML" {
		t.Fatalf("parse_mode = %#v, want HTML", payload["parse_mode"])
	}
	if payload["disable_web_page_preview"] != true {
		t.Fatalf("disable_web_page_preview = %#v, want true", payload["disable_web_page_preview"])
	}
	if _, ok := payload["reply_markup"]; ok {
		t.Fatalf("reply_markup = %#v, want linked title without inline keyboard", payload["reply_markup"])
	}
}

func TestTelegramOutboxOmitsButtonForUnsafeApplyURL(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	outbox := newTelegramOutboxWithEndpoint(server.URL, "chat-123", server.Client())
	err := outbox.Enqueue(context.Background(), Message{Metadata: map[string]string{
		"title": "Backend intern", "company": "Stripe", "apply_url": "javascript:alert(1)",
	}})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if _, ok := payload["reply_markup"]; ok {
		t.Fatalf("reply_markup = %#v, want unsafe URL omitted", payload["reply_markup"])
	}
	text, _ := payload["text"].(string)
	if strings.Contains(text, "javascript:") || strings.Contains(text, "Apply") {
		t.Fatalf("text = %q, want unsafe apply URL omitted", text)
	}
}

func TestTelegramOutboxRendersJobMessage(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	outbox := newTelegramOutboxWithEndpoint(server.URL, "@earlycareerradar", server.Client())
	err := outbox.Enqueue(context.Background(), Message{Metadata: map[string]string{
		"title":           "Quantitative Researcher, Intern (Summer 2027)",
		"company":         "Aquatic Capital Management",
		"company_type":    "📈 Quant / trading",
		"track":           "Internship",
		"category":        "Quant",
		"location":        "Chicago; London",
		"location_marker": "🌐",
		"apply_url":       "https://example.com/jobs/123",
	}})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	want := "💼 <b>Internship · Aquatic Capital Management</b>\n<a href=\"https://example.com/jobs/123\">Quantitative Researcher, Intern (Summer 2027) ↗</a>\n📍 Chicago · London"
	if got := payload["text"]; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	if _, ok := payload["reply_markup"]; ok {
		t.Fatalf("reply_markup = %#v, want linked title without inline keyboard", payload["reply_markup"])
	}
}

func TestTelegramOutboxRendersCompactNewGradMessage(t *testing.T) {
	msg := Message{Metadata: map[string]string{
		"title":           "Quantitative Researcher - New Grad",
		"company":         "Headlands Technologies",
		"company_type":    "📈 Quant / trading",
		"track":           "New Grad",
		"category":        "Software",
		"location":        "Amsterdam; Chicago; London; New York",
		"location_marker": "🌍",
		"apply_url":       "https://example.com/headlands/new-grad",
	}}

	want := "🎓 <b>New grad · Headlands Technologies</b>\n<a href=\"https://example.com/headlands/new-grad\">Quantitative Researcher - New Grad ↗</a>\n📍 Amsterdam · Chicago · London · New York"
	if got := renderTelegramText(msg); got != want {
		t.Fatalf("renderTelegramText() = %q, want %q", got, want)
	}
	for _, line := range strings.Split(renderTelegramText(msg), "\n") {
		if strings.TrimRight(line, " \t") != line {
			t.Fatalf("renderTelegramText() contains trailing whitespace: %q", line)
		}
	}
}

func TestTelegramOutboxCapsProseLikeTitlesAndAvoidsLooseSections(t *testing.T) {
	msg := Message{Metadata: map[string]string{
		"title":     "Work as a software engineering intern for 12–14 weeks building and shipping projects that run",
		"company":   "Builtinaustin",
		"track":     "Internship",
		"location":  "San Francisco, CA, United States",
		"apply_url": "https://example.com/jobs/intern",
	}}

	got := renderTelegramText(msg)
	want := "💼 <b>Internship · Builtinaustin</b>\n<a href=\"https://example.com/jobs/intern\">Work as a software engineering intern for 12–14 weeks building and ship… ↗</a>\n📍 San Francisco, CA, United States"
	if got != want {
		t.Fatalf("renderTelegramText() = %q, want %q", got, want)
	}
	if strings.Contains(got, "\n\n") || strings.Contains(got, "New internship") {
		t.Fatalf("renderTelegramText() = %q, want compact hierarchy", got)
	}
}

func TestTelegramOutboxEscapesStructuredMessageHTML(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	outbox := newTelegramOutboxWithEndpoint(server.URL, "chat-123", server.Client())
	err := outbox.Enqueue(context.Background(), Message{
		Subject: "Backend <intern>",
		Metadata: map[string]string{
			"title":     "Backend <intern>",
			"company":   "Stripe & Co",
			"score":     "93",
			"reason":    "uses Go < Redis",
			"apply_url": "https://example.com/apply?team=infra&level=intern",
		},
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	text, _ := payload["text"].(string)
	if strings.Contains(text, "<intern>") || strings.Contains(text, "Stripe & Co") || strings.Contains(text, "Go < Redis") {
		t.Fatalf("text = %q, want escaped structured fields", text)
	}
	if !strings.Contains(text, "Backend &lt;intern&gt;") ||
		!strings.Contains(text, "Stripe &amp; Co") ||
		!strings.Contains(text, "Go &lt; Redis") {
		t.Fatalf("text = %q, want escaped title/company/reason", text)
	}
}

func TestTelegramOutboxFallsBackForGenericMessages(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	outbox := newTelegramOutboxWithEndpoint(server.URL, "chat-123", server.Client())
	err := outbox.Enqueue(context.Background(), Message{
		Subject: "Radar test notification",
		Body:    "Telegram env is wired.",
	})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	text, _ := payload["text"].(string)
	if !strings.Contains(text, "Radar test notification") || !strings.Contains(text, "Telegram env is wired.") {
		t.Fatalf("text = %q, want generic subject and body", text)
	}
	if _, ok := payload["parse_mode"]; ok {
		t.Fatalf("parse_mode = %#v, want omitted for generic messages", payload["parse_mode"])
	}
}

func TestWebhookTextTruncationPreservesUTF8(t *testing.T) {
	text := truncateWebhookText("   "+strings.Repeat("統計", 120)+"   ", 101)
	if len(text) > 101 {
		t.Fatalf("truncated text len = %d, want <= 101", len(text))
	}
	if !utf8.ValidString(text) {
		t.Fatalf("truncated text is not valid UTF-8: %q", text)
	}
	if !strings.HasSuffix(text, "...") {
		t.Fatalf("truncated text = %q, want ellipsis", text)
	}
	if strings.HasPrefix(text, " ") || strings.HasSuffix(strings.TrimSuffix(text, "..."), " ") {
		t.Fatalf("truncated text = %q, want trimmed visible evidence", text)
	}
}

func TestWebhookTinyLimitTruncationPreservesUTF8(t *testing.T) {
	text := truncateWebhookText(strings.Repeat("統", 4), 4)
	if len(text) > 4 {
		t.Fatalf("truncated text len = %d, want <= 4", len(text))
	}
	if !utf8.ValidString(text) {
		t.Fatalf("truncated text is not valid UTF-8: %q", text)
	}
}

func TestWebhookTransportErrorRedactsEndpointSecrets(t *testing.T) {
	endpoint := "https://hooks.slack.com/services/T000/B000/secret-token"
	outbox := &WebhookOutbox{
		provider: "slack",
		endpoint: endpoint,
		client:   errorHTTPDoer{err: fmt.Errorf(`Post "%s": dial tcp timeout`, endpoint)},
		payload: func(Message) (any, error) {
			return map[string]any{"text": "Radar match"}, nil
		},
	}

	err := outbox.Enqueue(context.Background(), Message{Body: "Radar match"})
	if err == nil {
		t.Fatal("Enqueue() error = nil, want transport failure")
	}
	text := err.Error()
	if strings.Contains(text, endpoint) || strings.Contains(text, "secret-token") {
		t.Fatalf("transport error leaked endpoint secret: %q", text)
	}
	if !strings.Contains(text, "slack webhook request failed") || !strings.Contains(text, redactedWebhookEndpoint) {
		t.Fatalf("transport error = %q, want provider and redacted endpoint evidence", text)
	}
}

func TestTelegramOutboxReturnsStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "webhook unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	outbox := newTelegramOutboxWithEndpoint(server.URL, "test-chat", server.Client())
	err := outbox.Enqueue(context.Background(), Message{Body: "Radar match"})
	if err == nil || !strings.Contains(err.Error(), "telegram webhook failed") || !strings.Contains(err.Error(), "502") {
		t.Fatalf("Enqueue() error = %v, want Telegram status error", err)
	}
}

func TestWebhookStatusErrorRedactsEchoedEndpointSecrets(t *testing.T) {
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "failed posting to "+endpoint, http.StatusBadGateway)
	}))
	defer server.Close()
	endpoint = server.URL + "/services/T000/B000/body-secret"

	outbox := &WebhookOutbox{
		provider: "slack",
		endpoint: endpoint,
		client:   server.Client(),
		payload: func(Message) (any, error) {
			return map[string]any{"text": "Radar match"}, nil
		},
	}
	err := outbox.Enqueue(context.Background(), Message{Body: "Radar match"})
	if err == nil {
		t.Fatal("Enqueue() error = nil, want status failure")
	}
	text := err.Error()
	if strings.Contains(text, endpoint) || strings.Contains(text, "body-secret") {
		t.Fatalf("status error leaked endpoint secret: %q", text)
	}
	if !strings.Contains(text, redactedWebhookEndpoint) {
		t.Fatalf("status error = %q, want redacted endpoint evidence", text)
	}
}

func TestWebhookOutboxPreservesRetryAfterHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer server.Close()

	outbox := newTelegramOutboxWithEndpoint(server.URL, "test-chat", server.Client())
	err := outbox.Enqueue(context.Background(), Message{Body: "Radar match"})
	var retryable interface{ RetryAfter() time.Duration }
	if !errors.As(err, &retryable) {
		t.Fatalf("Enqueue() error = %T %v, want retry-after error", err, err)
	}
	if got := retryable.RetryAfter(); got != 7*time.Second {
		t.Fatalf("retry after = %s, want 7s", got)
	}
}

func TestTelegramOutboxReadsRetryAfterFromResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"parameters":{"retry_after":9}}`))
	}))
	defer server.Close()

	outbox := newTelegramOutboxWithEndpoint(server.URL, "chat-1", server.Client())
	err := outbox.Enqueue(context.Background(), Message{Recipient: "chat-1", Subject: "test"})
	var retryable interface{ RetryAfter() time.Duration }
	if !errors.As(err, &retryable) || retryable.RetryAfter() != 9*time.Second {
		t.Fatalf("retry hint error=%v delay=%v", err, retryable)
	}
}

func TestWebhookRetryAfterCapsRunawayHints(t *testing.T) {
	if got := parseRetryAfter("86400", func() time.Time { return time.Unix(0, 0).UTC() }); got != MaxDeliveryRetryDelay {
		t.Fatalf("numeric retry after = %s, want cap %s", got, MaxDeliveryRetryDelay)
	}
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	retryAt := now.Add(24 * time.Hour).Format(http.TimeFormat)
	if got := parseRetryAfter(retryAt, func() time.Time { return now }); got != MaxDeliveryRetryDelay {
		t.Fatalf("date retry after = %s, want cap %s", got, MaxDeliveryRetryDelay)
	}
}

type errorHTTPDoer struct {
	err error
}

func (d errorHTTPDoer) Do(*http.Request) (*http.Response, error) {
	return nil, d.err
}
