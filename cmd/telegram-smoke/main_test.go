package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hwennnn/radar/internal/delivery"
)

type fakeTelegramAPI struct {
	bot        botIdentity
	chat       chatIdentity
	membership chatMembership
	err        error
}

func (api fakeTelegramAPI) GetMe(context.Context) (botIdentity, error) {
	return api.bot, api.err
}

func (api fakeTelegramAPI) GetChat(context.Context, string) (chatIdentity, error) {
	return api.chat, api.err
}

func (api fakeTelegramAPI) GetChatMember(context.Context, string, int64) (chatMembership, error) {
	return api.membership, api.err
}

type recordingOutbox struct {
	messages []delivery.Message
}

func (outbox *recordingOutbox) Enqueue(_ context.Context, message delivery.Message) error {
	outbox.messages = append(outbox.messages, message)
	return nil
}

func validEnvironment(key string) string {
	values := map[string]string{
		"RADAR_LITE_TELEGRAM_BOT_TOKEN":    "test-token",
		"RADAR_LITE_TELEGRAM_CHAT_ID":      "@earlycareerradar",
		"RADAR_LITE_EXPECTED_TELEGRAM_BOT": "@radar_swe_jobs_bot",
		"RADAR_LITE_PUBLISHING_ENABLED":    "true",
	}
	return values[key]
}

func validAPI() fakeTelegramAPI {
	return fakeTelegramAPI{
		bot:        botIdentity{ID: 42, Username: "radar_swe_jobs_bot"},
		chat:       chatIdentity{ID: -1001, Type: "channel", Username: "earlycareerradar", Title: "Early Career Radar"},
		membership: chatMembership{Status: "administrator", CanPostMessages: true},
	}
}

func TestLoadConfigRequiresExplicitPublishingForSend(t *testing.T) {
	getenv := func(key string) string {
		if key == "RADAR_LITE_PUBLISHING_ENABLED" {
			return "false"
		}
		return validEnvironment(key)
	}
	_, err := loadConfig(getenv, []string{"--send", "--confirm-channel", "@earlycareerradar"})
	if err == nil || !strings.Contains(err.Error(), "PUBLISHING_ENABLED=true") {
		t.Fatalf("loadConfig() error = %v, want publishing refusal", err)
	}
}

func TestLoadConfigRequiresExactChannelConfirmation(t *testing.T) {
	_, err := loadConfig(validEnvironment, []string{"--send", "--confirm-channel", "@somewhereelse"})
	if err == nil || !strings.Contains(err.Error(), "--confirm-channel @earlycareerradar") {
		t.Fatalf("loadConfig() error = %v, want exact channel refusal", err)
	}
}

func TestRunDryRunChecksIdentityWithoutSending(t *testing.T) {
	cfg, err := loadConfig(validEnvironment, nil)
	if err != nil {
		t.Fatal(err)
	}
	outbox := &recordingOutbox{}
	var output strings.Builder
	if err := run(context.Background(), cfg, validAPI(), outbox, &output); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if len(outbox.messages) != 0 {
		t.Fatalf("messages = %d, want 0", len(outbox.messages))
	}
	if !strings.Contains(output.String(), "Dry run only") {
		t.Fatalf("output = %q, want dry-run evidence", output.String())
	}
}

func TestRunSendEmitsThreeClearlyLabeledMessages(t *testing.T) {
	cfg, err := loadConfig(validEnvironment, []string{"--send", "--confirm-channel", "@earlycareerradar"})
	if err != nil {
		t.Fatal(err)
	}
	outbox := &recordingOutbox{}
	if err := run(context.Background(), cfg, validAPI(), outbox, &strings.Builder{}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if len(outbox.messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(outbox.messages))
	}
	for _, message := range outbox.messages {
		if message.Recipient != "@earlycareerradar" {
			t.Fatalf("recipient = %q", message.Recipient)
		}
		if !strings.HasPrefix(message.Subject, "[TEST]") {
			t.Fatalf("subject = %q, want [TEST] prefix", message.Subject)
		}
		if message.Metadata["review"] != "Test message — do not apply" {
			t.Fatalf("review = %q", message.Metadata["review"])
		}
	}
}

func TestRunRefusesWrongBotOrPermission(t *testing.T) {
	cfg, err := loadConfig(validEnvironment, nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		api  fakeTelegramAPI
		want string
	}{
		{name: "wrong bot", api: fakeTelegramAPI{
			bot: botIdentity{ID: 42, Username: "other_bot"},
		}, want: "bot identity mismatch"},
		{name: "not admin", api: fakeTelegramAPI{
			bot:        botIdentity{ID: 42, Username: "radar_swe_jobs_bot"},
			chat:       chatIdentity{Type: "channel", Username: "earlycareerradar"},
			membership: chatMembership{Status: "member"},
		}, want: "want administrator"},
		{name: "api failure", api: fakeTelegramAPI{err: errors.New("offline")}, want: "verify bot identity"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := run(context.Background(), cfg, test.api, &recordingOutbox{}, &strings.Builder{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want %q", err, test.want)
			}
		})
	}
}
