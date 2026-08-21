package pipeline

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type applyURLStoreFake struct {
	candidates []ApplyURLCandidate
	recorded   []ApplyURLCheck
	err        error
}

func (s *applyURLStoreFake) ListApplyURLsDue(context.Context, time.Time, int) ([]ApplyURLCandidate, error) {
	return s.candidates, s.err
}

func (s *applyURLStoreFake) RecordApplyURLCheck(_ context.Context, check ApplyURLCheck) error {
	s.recorded = append(s.recorded, check)
	return s.err
}

type applyURLDoer func(*http.Request) (*http.Response, error)

func (do applyURLDoer) Do(request *http.Request) (*http.Response, error) { return do(request) }

func TestApplyURLCheckerClassifiesLiveTerminalAndTransientResponses(t *testing.T) {
	store := &applyURLStoreFake{candidates: []ApplyURLCandidate{
		{JobID: "live", ApplyURL: "https://jobs.example/live"},
		{JobID: "gone", ApplyURL: "https://jobs.example/gone"},
		{JobID: "soft", ApplyURL: "https://jobs.example/soft"},
		{JobID: "busy", ApplyURL: "https://jobs.example/busy"},
	}}
	client := applyURLDoer(func(request *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, "<html>job</html>"
		switch {
		case strings.HasSuffix(request.URL.Path, "/gone"):
			status = http.StatusGone
		case strings.HasSuffix(request.URL.Path, "/soft"):
			body = `<main class="dbException404Page">missing</main>`
		case strings.HasSuffix(request.URL.Path, "/busy"):
			status = http.StatusTooManyRequests
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	report, err := (ApplyURLChecker{Store: store, Client: client, Now: func() time.Time { return now }}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Attempted != 4 || report.Live != 1 || report.Gone != 2 || report.Unknown != 1 {
		t.Fatalf("report = %#v", report)
	}
	if len(store.recorded) != 4 {
		t.Fatalf("recorded %d checks, want 4", len(store.recorded))
	}
	if store.recorded[0].Outcome != ApplyURLLive || store.recorded[0].NextCheckAt != now.Add(6*time.Hour) {
		t.Fatalf("live check = %#v", store.recorded[0])
	}
	if store.recorded[1].Outcome != ApplyURLGone || store.recorded[1].NextCheckAt != now.Add(30*time.Minute) {
		t.Fatalf("gone check = %#v", store.recorded[1])
	}
	if store.recorded[3].Outcome != ApplyURLUnchecked || store.recorded[3].StatusCode != http.StatusTooManyRequests {
		t.Fatalf("transient check = %#v", store.recorded[3])
	}
}

func TestApplyURLCheckerRetainsRequestErrorsAsUnknown(t *testing.T) {
	store := &applyURLStoreFake{candidates: []ApplyURLCandidate{{JobID: "job", ApplyURL: "https://jobs.example/job"}}}
	client := applyURLDoer(func(*http.Request) (*http.Response, error) { return nil, errors.New("timeout") })
	report, err := (ApplyURLChecker{Store: store, Client: client}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Unknown != 1 || len(report.Errors) != 1 || store.recorded[0].Outcome != ApplyURLUnchecked {
		t.Fatalf("report=%#v recorded=%#v", report, store.recorded)
	}
}
