package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxWebhookErrorBody       = 512
	maxWebhookSuccessBody     = 64 << 10
	defaultWebhookHTTPTimeout = 10 * time.Second
	redactedWebhookEndpoint   = "<redacted webhook endpoint>"
	telegramTitleRuneLimit    = 72
)

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type WebhookOutbox struct {
	provider string
	endpoint string
	client   httpDoer
	payload  func(Message) (any, error)
}

type WebhookStatusError struct {
	Provider   string
	Status     string
	Body       string
	retryAfter time.Duration
}

type WebhookRequestError struct {
	Provider  string
	Evidence  string
	ambiguous bool
}

func (e WebhookRequestError) Error() string {
	return fmt.Sprintf("%s webhook request failed: %s", e.Provider, e.Evidence)
}

func (e WebhookRequestError) AmbiguousDelivery() bool { return e.ambiguous }

type Receipt struct {
	Provider          string
	ProviderMessageID string
	ProviderChatID    string
	AcceptedAt        time.Time
}

func (e WebhookStatusError) Error() string {
	if strings.TrimSpace(e.Body) == "" {
		return fmt.Sprintf("%s webhook failed: status %s", e.Provider, e.Status)
	}
	return fmt.Sprintf("%s webhook failed: status %s: %s", e.Provider, e.Status, strings.TrimSpace(e.Body))
}

func (e WebhookStatusError) RetryAfter() time.Duration {
	return e.retryAfter
}

func NewTelegramOutbox(botToken string, chatID string, client *http.Client) *WebhookOutbox {
	endpoint := "https://api.telegram.org/bot" + strings.TrimSpace(botToken) + "/sendMessage"
	return newTelegramOutboxWithEndpoint(endpoint, chatID, client)
}

func newTelegramOutboxWithEndpoint(endpoint string, chatID string, client *http.Client) *WebhookOutbox {
	return &WebhookOutbox{
		provider: "telegram",
		endpoint: strings.TrimSpace(endpoint),
		client:   defaultHTTPClient(client),
		payload: func(msg Message) (any, error) {
			text, parseMode := renderTelegramTextWithMode(msg)
			payload := map[string]any{
				"chat_id":                  strings.TrimSpace(firstNonEmpty(msg.Recipient, chatID)),
				"text":                     text,
				"disable_web_page_preview": true,
			}
			if parseMode != "" {
				payload["parse_mode"] = parseMode
			}
			return payload, nil
		},
	}
}

func NewWebhookHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultWebhookHTTPTimeout
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = safeWebhookDialContext
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func (o *WebhookOutbox) Enqueue(ctx context.Context, msg Message) error {
	_, err := o.EnqueueWithReceipt(ctx, msg)
	return err
}

func (o *WebhookOutbox) EnqueueWithReceipt(ctx context.Context, msg Message) (Receipt, error) {
	if o == nil {
		return Receipt{}, nil
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if strings.TrimSpace(o.endpoint) == "" {
		return Receipt{}, fmt.Errorf("%s webhook endpoint is not configured", o.provider)
	}
	payload, err := o.payload(msg)
	if err != nil {
		return Receipt{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Receipt{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint, bytes.NewReader(body))
	if err != nil {
		return Receipt{}, webhookRequestError(o.provider, o.endpoint, err, false)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return Receipt{}, webhookRequestError(o.provider, o.endpoint, err, o.provider == "telegram" && ambiguousWebhookTransportError(err))
	}
	defer resp.Body.Close()
	bodyLimit := int64(maxWebhookErrorBody)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		bodyLimit = maxWebhookSuccessBody
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if readErr != nil {
			return Receipt{}, webhookRequestError(o.provider, o.endpoint, readErr, o.provider == "telegram")
		}
		if o.provider == "telegram" {
			return parseTelegramReceipt(data), nil
		}
		return Receipt{Provider: o.provider, AcceptedAt: time.Now().UTC()}, nil
	}
	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now)
	if retryAfter == 0 && o.provider == "telegram" {
		retryAfter = parseTelegramRetryAfter(data)
	}
	return Receipt{}, WebhookStatusError{
		Provider:   o.provider,
		Status:     resp.Status,
		Body:       sanitizeWebhookErrorEvidence(string(data), o.endpoint),
		retryAfter: retryAfter,
	}
}

func ambiguousWebhookTransportError(err error) bool {
	if err == nil {
		return false
	}
	var networkError net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || (errors.As(err, &networkError) && networkError.Timeout())
}

func parseTelegramReceipt(body []byte) Receipt {
	var response struct {
		Result struct {
			MessageID int64 `json:"message_id"`
			Date      int64 `json:"date"`
			Chat      struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"result"`
	}
	if json.Unmarshal(body, &response) != nil || response.Result.MessageID == 0 {
		return Receipt{Provider: "telegram", AcceptedAt: time.Now().UTC()}
	}
	receipt := Receipt{
		Provider: "telegram", ProviderMessageID: strconv.FormatInt(response.Result.MessageID, 10),
		ProviderChatID: strconv.FormatInt(response.Result.Chat.ID, 10), AcceptedAt: time.Now().UTC(),
	}
	if response.Result.Date > 0 {
		receipt.AcceptedAt = time.Unix(response.Result.Date, 0).UTC()
	}
	return receipt
}

func parseTelegramRetryAfter(body []byte) time.Duration {
	var response struct {
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal(body, &response) != nil || response.Parameters.RetryAfter <= 0 {
		return 0
	}
	return boundedWebhookRetryAfter(time.Duration(response.Parameters.RetryAfter) * time.Second)
}

func renderTelegramText(msg Message) string {
	text, _ := renderTelegramTextWithMode(msg)
	return text
}

func renderTelegramTextWithMode(msg Message) (string, string) {
	if text, ok := renderStructuredTelegramHTML(msg, 3900); ok {
		return text, "HTML"
	}
	return renderWebhookText(msg, 3900), ""
}

func renderStructuredTelegramHTML(msg Message, limit int) (string, bool) {
	meta := msg.Metadata
	title := strings.TrimSpace(firstNonEmpty(meta["title"], msg.Subject))
	company := strings.TrimSpace(meta["company"])
	location := strings.TrimSpace(meta["location"])
	score := strings.TrimSpace(meta["score"])
	applyURL := strings.TrimSpace(meta["apply_url"])
	reason := strings.TrimSpace(meta["reason"])
	review := strings.TrimSpace(meta["review"])
	track := strings.TrimSpace(meta["track"])
	companyType := strings.TrimSpace(meta["company_type"])
	category := strings.TrimSpace(meta["category"])

	if title == "" || company == "" {
		return "", false
	}

	lines := []string{
		telegramJobHeadline(track, company),
		telegramTitleLine(title, applyURL),
	}
	if location != "" {
		lines = append(lines, "📍 "+html.EscapeString(telegramInlineList(location)))
	}
	if classification := telegramClassificationLine(companyType, category); classification != "" {
		lines = append(lines, classification)
	}
	if score != "" {
		lines = append(lines, "⭐ <b>"+html.EscapeString(score)+"/100 fit</b>")
	}
	if review != "" {
		lines = append(lines, "⚠️ <b>Check:</b> "+html.EscapeString(review))
	} else if reason != "" {
		lines = append(lines, "💡 "+html.EscapeString(reason))
	}
	text := strings.Join(lines, "\n")
	if len(text) > limit {
		if plain, ok := renderStructuredMatchText(msg, limit); ok {
			return html.EscapeString(plain), true
		}
		text = html.EscapeString(truncateWebhookText(text, limit))
	}
	return text, true
}

func telegramClassificationLine(companyType string, category string) string {
	companyType = strings.Join(strings.Fields(strings.TrimSpace(companyType)), " ")
	category = strings.Join(strings.Fields(strings.TrimSpace(category)), " ")
	if companyType == "" {
		if category == "" {
			return ""
		}
		return "🏷 " + html.EscapeString(category)
	}
	if category == "" || strings.Contains(strings.ToLower(companyType), strings.ToLower(category)) {
		return html.EscapeString(companyType)
	}
	return html.EscapeString(companyType + " · " + category)
}

func telegramJobHeadline(track string, company string) string {
	label := "Role"
	emoji := "✨"
	switch strings.ToLower(strings.TrimSpace(track)) {
	case "intern", "internship":
		emoji, label = "💼", "Internship"
	case "new grad", "new graduate", "graduate":
		emoji, label = "🎓", "New grad"
	}
	return emoji + " <b>" + html.EscapeString(label+" · "+strings.TrimSpace(company)) + "</b>"
}

func telegramTitleLine(title string, applyURL string) string {
	title = html.EscapeString(telegramDisplayTitle(title))
	if safeURL := telegramButtonURL(applyURL); safeURL != "" {
		return `<a href="` + html.EscapeString(safeURL) + `">` + title + ` ↗</a>`
	}
	return title
}

func telegramDisplayTitle(title string) string {
	title = strings.Join(strings.Fields(strings.TrimSpace(title)), " ")
	runes := []rune(title)
	if len(runes) <= telegramTitleRuneLimit {
		return title
	}
	return strings.TrimSpace(string(runes[:telegramTitleRuneLimit-1])) + "…"
}

func telegramInlineList(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ';' })
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			clean = append(clean, part)
		}
	}
	return strings.Join(clean, " · ")
}

func telegramButtonURL(value string) string {
	value = strings.TrimSpace(value)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return value
}

func renderStructuredMatchText(msg Message, limit int) (string, bool) {
	meta := msg.Metadata
	title := strings.TrimSpace(firstNonEmpty(meta["title"], msg.Subject))
	company := strings.TrimSpace(meta["company"])
	location := strings.TrimSpace(meta["location"])
	score := strings.TrimSpace(meta["score"])
	applyURL := strings.TrimSpace(meta["apply_url"])
	reason := strings.TrimSpace(meta["reason"])
	review := strings.TrimSpace(meta["review"])

	if title == "" || company == "" {
		return "", false
	}

	lines := []string{"Radar match"}
	if score != "" {
		lines[0] += " " + score
	}
	lines = append(lines, title)
	if review != "" {
		lines = append(lines, company)
		if location != "" {
			lines = append(lines, location)
		}
		lines = append(lines, "", "Review", review)
	} else {
		lines = append(lines, company)
		if location != "" {
			lines = append(lines, location)
		}
		if reason != "" {
			lines = append(lines, "", "Why", reason)
		}
	}
	if applyURL != "" {
		lines = append(lines, "", "Apply", applyURL)
	}
	return truncateWebhookText(strings.Join(lines, "\n"), limit), true
}

func renderWebhookText(msg Message, limit int) string {
	if text, ok := renderStructuredMatchText(msg, limit); ok {
		return text
	}
	subject := strings.TrimSpace(msg.Subject)
	body := strings.TrimSpace(msg.Body)
	switch {
	case subject != "" && body != "":
		return truncateWebhookText(subject+"\n\n"+body, limit)
	case body != "":
		return truncateWebhookText(body, limit)
	case subject != "":
		return truncateWebhookText(subject, limit)
	default:
		return "Radar notification"
	}
}

func webhookRequestError(provider string, endpoint string, err error, ambiguous bool) error {
	if err == nil {
		return nil
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "webhook"
	}
	return WebhookRequestError{Provider: provider, Evidence: sanitizeWebhookErrorEvidence(err.Error(), endpoint), ambiguous: ambiguous}
}

func sanitizeWebhookErrorEvidence(value string, endpoint string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	endpoint = strings.TrimSpace(endpoint)
	if endpoint != "" {
		value = strings.ReplaceAll(value, endpoint, redactedWebhookEndpoint)
	}
	return truncateWebhookText(value, maxWebhookErrorBody)
}

func parseRetryAfter(value string, now func() time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := time.ParseDuration(value + "s"); err == nil && seconds > 0 {
		return boundedWebhookRetryAfter(seconds)
	}
	parsed, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	if now == nil {
		now = time.Now
	}
	delay := parsed.Sub(now())
	if delay < 0 {
		return 0
	}
	return boundedWebhookRetryAfter(delay)
}

func boundedWebhookRetryAfter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	if delay > MaxDeliveryRetryDelay {
		return MaxDeliveryRetryDelay
	}
	return delay
}

func truncateWebhookText(text string, limit int) string {
	text = strings.TrimSpace(strings.ToValidUTF8(text, ""))
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return trimWebhookTextToBytes(text, limit)
	}
	return trimWebhookTextToBytes(text, limit-3) + "..."
}

func trimWebhookTextToBytes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8RuneStart(text[limit]) {
		limit--
	}
	return strings.TrimSpace(text[:limit])
}

func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

func defaultHTTPClient(client *http.Client) httpDoer {
	if client != nil {
		return client
	}
	return NewWebhookHTTPClient(defaultWebhookHTTPTimeout)
}

func safeWebhookDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("host resolved no addresses")
	}

	dialer := &net.Dialer{}
	var lastErr error
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok || privateWebhookAddr(addr) {
			return nil, fmt.Errorf("resolved private address blocked")
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("host resolved no dialable addresses")
}

func privateWebhookAddr(addr netip.Addr) bool {
	return addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsUnspecified()
}
