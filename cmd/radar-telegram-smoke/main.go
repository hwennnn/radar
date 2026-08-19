package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hwennnn/radar/internal/notify"
)

const telegramAPIBase = "https://api.telegram.org"

type config struct {
	token             string
	chatID            string
	expectedBot       string
	publishingEnabled bool
	send              bool
	confirmedChannel  string
}

type botIdentity struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type chatIdentity struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Title    string `json:"title"`
	Username string `json:"username"`
}

type chatMembership struct {
	Status          string `json:"status"`
	CanPostMessages bool   `json:"can_post_messages"`
}

type telegramAPI interface {
	GetMe(context.Context) (botIdentity, error)
	GetChat(context.Context, string) (chatIdentity, error)
	GetChatMember(context.Context, string, int64) (chatMembership, error)
}

type telegramBotAPI struct {
	baseURL string
	token   string
	client  *http.Client
}

type apiResponse[T any] struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Result      T      `json:"result"`
}

func main() {
	cfg, err := loadConfig(os.Getenv, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "telegram smoke configuration:", err)
		os.Exit(2)
	}

	client := notify.NewWebhookHTTPClient(10 * time.Second)
	api := &telegramBotAPI{baseURL: telegramAPIBase, token: cfg.token, client: client}
	outbox := notify.NewTelegramOutbox(cfg.token, cfg.chatID, client)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := run(ctx, cfg, api, outbox, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "telegram smoke failed:", err)
		os.Exit(1)
	}
}

func loadConfig(getenv func(string) string, args []string) (config, error) {
	flags := flag.NewFlagSet("radar-telegram-smoke", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var send bool
	var confirmedChannel string
	flags.BoolVar(&send, "send", false, "send the three labeled smoke-test messages")
	flags.StringVar(&confirmedChannel, "confirm-channel", "", "repeat the exact destination channel when sending")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, errors.New("unexpected positional arguments")
	}

	cfg := config{
		token:             strings.TrimSpace(getenv("RADAR_LITE_TELEGRAM_BOT_TOKEN")),
		chatID:            normalizeChannel(getenv("RADAR_LITE_TELEGRAM_CHAT_ID")),
		expectedBot:       normalizeUsername(getenv("RADAR_LITE_EXPECTED_TELEGRAM_BOT")),
		publishingEnabled: getenv("RADAR_LITE_PUBLISHING_ENABLED") == "true",
		send:              send,
		confirmedChannel:  normalizeChannel(confirmedChannel),
	}
	if cfg.expectedBot == "" {
		cfg.expectedBot = "radar_swe_jobs_bot"
	}
	if cfg.token == "" {
		return config{}, errors.New("RADAR_LITE_TELEGRAM_BOT_TOKEN is required")
	}
	if cfg.chatID == "" {
		return config{}, errors.New("RADAR_LITE_TELEGRAM_CHAT_ID is required")
	}
	if cfg.send && !cfg.publishingEnabled {
		return config{}, errors.New("--send requires RADAR_LITE_PUBLISHING_ENABLED=true")
	}
	if cfg.send && cfg.confirmedChannel != cfg.chatID {
		return config{}, fmt.Errorf("--send requires --confirm-channel %s", cfg.chatID)
	}
	return cfg, nil
}

func run(ctx context.Context, cfg config, api telegramAPI, outbox notify.Outbox, output io.Writer) error {
	bot, err := api.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("verify bot identity: %w", err)
	}
	if got := normalizeUsername(bot.Username); got != cfg.expectedBot {
		return fmt.Errorf("bot identity mismatch: got @%s, want @%s", got, cfg.expectedBot)
	}

	chat, err := api.GetChat(ctx, cfg.chatID)
	if err != nil {
		return fmt.Errorf("verify channel identity: %w", err)
	}
	if chat.Type != "channel" {
		return fmt.Errorf("destination %s is %q, want Telegram channel", cfg.chatID, chat.Type)
	}
	if got := normalizeUsername(chat.Username); got != normalizeUsername(cfg.chatID) {
		return fmt.Errorf("channel identity mismatch: got @%s, want %s", got, cfg.chatID)
	}

	membership, err := api.GetChatMember(ctx, cfg.chatID, bot.ID)
	if err != nil {
		return fmt.Errorf("verify channel permission: %w", err)
	}
	if membership.Status != "creator" && membership.Status != "administrator" {
		return fmt.Errorf("@%s is %q in %s, want administrator", cfg.expectedBot, membership.Status, cfg.chatID)
	}
	if membership.Status != "creator" && !membership.CanPostMessages {
		return fmt.Errorf("@%s cannot post messages in %s", cfg.expectedBot, cfg.chatID)
	}

	fmt.Fprintf(output, "Telegram preflight passed: @%s -> %s (%s, post permission confirmed)\n", cfg.expectedBot, cfg.chatID, membership.Status)
	if !cfg.send {
		fmt.Fprintln(output, "Dry run only: no Telegram message was sent.")
		return nil
	}
	if outbox == nil {
		return errors.New("Telegram sender is unavailable")
	}

	for _, message := range smokeMessages(cfg.chatID) {
		if err := outbox.Enqueue(ctx, message); err != nil {
			return fmt.Errorf("send %q: %w", message.Subject, err)
		}
	}
	fmt.Fprintf(output, "Sent %d labeled test postings to %s.\n", len(smokeMessages(cfg.chatID)), cfg.chatID)
	return nil
}

func smokeMessages(recipient string) []notify.Message {
	roles := []struct {
		title    string
		company  string
		location string
	}{
		{title: "[TEST] Software Engineering Intern", company: "Radar Lite", location: "New York, NY / Remote"},
		{title: "[TEST] New Grad Software Engineer", company: "Radar Lite", location: "San Francisco, CA"},
		{title: "[TEST] Quant Engineering Intern", company: "Radar Lite", location: "Chicago, IL"},
	}
	messages := make([]notify.Message, 0, len(roles))
	for index, role := range roles {
		messages = append(messages, notify.Message{
			ID:        fmt.Sprintf("radar-lite-smoke-%d", index+1),
			Channel:   "telegram",
			Recipient: recipient,
			Subject:   role.title,
			DedupeKey: fmt.Sprintf("radar-lite-smoke-%d", index+1),
			Metadata: map[string]string{
				"title":     role.title,
				"company":   role.company,
				"location":  role.location,
				"review":    "Test message — do not apply",
				"apply_url": "https://earlycareerradar.com/#today",
			},
			CreatedAt: time.Now().UTC(),
		})
	}
	return messages
}

func (api *telegramBotAPI) GetMe(ctx context.Context) (botIdentity, error) {
	return callTelegram[botIdentity](ctx, api, "getMe", nil)
}

func (api *telegramBotAPI) GetChat(ctx context.Context, chatID string) (chatIdentity, error) {
	return callTelegram[chatIdentity](ctx, api, "getChat", url.Values{"chat_id": {chatID}})
}

func (api *telegramBotAPI) GetChatMember(ctx context.Context, chatID string, userID int64) (chatMembership, error) {
	return callTelegram[chatMembership](ctx, api, "getChatMember", url.Values{
		"chat_id": {chatID}, "user_id": {strconv.FormatInt(userID, 10)},
	})
}

func callTelegram[T any](ctx context.Context, api *telegramBotAPI, method string, values url.Values) (T, error) {
	var zero T
	if api == nil || api.client == nil {
		return zero, errors.New("Telegram client is unavailable")
	}
	endpoint := strings.TrimRight(api.baseURL, "/") + "/bot" + api.token + "/" + method
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return zero, errors.New("build Telegram request")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := api.client.Do(request)
	if err != nil {
		return zero, errors.New("Telegram request failed")
	}
	defer response.Body.Close()
	var payload apiResponse[T]
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return zero, errors.New("decode Telegram response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !payload.OK {
		description := strings.TrimSpace(payload.Description)
		if description == "" {
			description = response.Status
		}
		return zero, fmt.Errorf("Telegram API rejected request: %s", description)
	}
	return payload.Result, nil
}

func normalizeUsername(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "@")
}

func normalizeChannel(value string) string {
	value = normalizeUsername(value)
	if value == "" {
		return ""
	}
	return "@" + value
}
