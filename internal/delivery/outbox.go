package delivery

import (
	"context"
	"log/slog"
	"time"
)

type Message struct {
	ID        string
	Channel   string
	Recipient string
	Subject   string
	Body      string
	DedupeKey string
	Metadata  map[string]string
	CreatedAt time.Time
}

type Outbox interface {
	Enqueue(ctx context.Context, msg Message) error
}

type LoggingOutbox struct {
	logger *slog.Logger
	now    func() time.Time
}

func NewLoggingOutbox(logger *slog.Logger) *LoggingOutbox {
	if logger == nil {
		logger = slog.Default()
	}
	return &LoggingOutbox{
		logger: logger,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

func (o *LoggingOutbox) Enqueue(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = o.now()
	}
	if msg.Channel == "" {
		msg.Channel = "log"
	}

	attrs := []any{
		"channel", msg.Channel,
		"recipient", msg.Recipient,
		"subject", msg.Subject,
		"dedupe_key", msg.DedupeKey,
		"created_at", msg.CreatedAt.Format(time.RFC3339),
	}
	for key, value := range msg.Metadata {
		attrs = append(attrs, key, value)
	}
	o.logger.InfoContext(ctx, msg.Body, attrs...)
	return nil
}
