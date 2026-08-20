package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hwennnn/radar/internal/pipeline"
)

type extractorFunc func(context.Context, pipeline.Source) (pipeline.ExtractionResult, error)

func (f extractorFunc) Extract(ctx context.Context, source pipeline.Source) (pipeline.ExtractionResult, error) {
	return f(ctx, source)
}

func TestLoggingExtractorReportsSourceProgress(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	times := []time.Time{
		time.Date(2026, 8, 17, 1, 2, 3, 0, time.UTC),
		time.Date(2026, 8, 17, 1, 2, 4, 500_000_000, time.UTC),
	}
	nowIndex := 0
	extractor := loggingExtractor{
		logger: logger,
		now: func() time.Time {
			value := times[nowIndex]
			nowIndex++
			return value
		},
		inner: extractorFunc(func(context.Context, pipeline.Source) (pipeline.ExtractionResult, error) {
			return pipeline.ExtractionResult{Complete: true, Observations: []pipeline.Observation{{Title: "Software Engineer Intern"}}}, nil
		}),
	}

	_, err := extractor.Extract(context.Background(), pipeline.Source{ID: "example-ashby", Company: "Example", Provider: "ashby"})
	if err != nil {
		t.Fatal(err)
	}
	logs := output.String()
	for _, want := range []string{
		`"msg":"source extraction started"`, `"source_id":"example-ashby"`,
		`"msg":"source extraction complete"`, `"elapsed_ms":1500`,
		`"observations":1`, `"empty":false`, `"complete":true`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs missing %s:\n%s", want, logs)
		}
	}
}

func TestLoggingExtractorReportsFailure(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	extractor := loggingExtractor{
		logger: logger,
		inner: extractorFunc(func(context.Context, pipeline.Source) (pipeline.ExtractionResult, error) {
			return pipeline.ExtractionResult{}, errors.New("provider unavailable")
		}),
	}

	_, err := extractor.Extract(context.Background(), pipeline.Source{ID: "broken-source", Company: "Broken", Provider: "workday"})
	if err == nil {
		t.Fatal("expected extraction failure")
	}
	logs := output.String()
	for _, want := range []string{
		`"msg":"source extraction failed"`, `"source_id":"broken-source"`,
		`"provider":"workday"`, `"error":"provider unavailable"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs missing %s:\n%s", want, logs)
		}
	}
}
