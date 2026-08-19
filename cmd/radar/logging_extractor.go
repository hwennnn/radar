package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/hwennnn/radar/internal/core"
)

// loggingExtractor makes long multi-source cycles self-explanatory without
// coupling the durable lite runner to a concrete logging implementation.
type loggingExtractor struct {
	inner  core.Extractor
	logger *slog.Logger
	now    func() time.Time
}

func (e loggingExtractor) Extract(ctx context.Context, source core.Source) (core.ExtractionResult, error) {
	logger := e.logger
	if logger == nil {
		logger = slog.Default()
	}
	now := e.now
	if now == nil {
		now = time.Now
	}
	startedAt := now()
	logger.InfoContext(ctx, "source extraction started",
		"source_id", source.ID,
		"company", source.Company,
		"provider", source.Provider,
	)

	result, err := e.inner.Extract(ctx, source)
	elapsed := now().Sub(startedAt)
	if err != nil {
		logger.WarnContext(ctx, "source extraction failed",
			"source_id", source.ID,
			"company", source.Company,
			"provider", source.Provider,
			"elapsed_ms", elapsed.Milliseconds(),
			"error", err,
		)
		return result, err
	}
	logger.InfoContext(ctx, "source extraction complete",
		"source_id", source.ID,
		"company", source.Company,
		"provider", source.Provider,
		"elapsed_ms", elapsed.Milliseconds(),
		"observations", len(result.Observations),
		"empty", len(result.Observations) == 0,
		"complete", result.Complete,
	)
	return result, nil
}
