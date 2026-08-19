package lite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

type extractorFunc func(context.Context, Source) (ExtractionResult, error)

func (f extractorFunc) Extract(ctx context.Context, source Source) (ExtractionResult, error) {
	return f(ctx, source)
}

func completeExtraction(observations ...Observation) ExtractionResult {
	return ExtractionResult{Observations: observations, Complete: true}
}

type runnerStoreFake struct {
	jobs            map[string]Posting
	bootstrap       map[string]bool
	bootstrapValues map[string]json.RawMessage
	bootstrapWrites int
	enqueues        int
	decisions       map[string]Delivery
	successes       map[string]int
	failures        map[string]string
	observeErr      error
	observeErrAt    int
	observeCalls    int
	activateErr     error
	finalizeErr     error
	finalized       map[string][]string
	bootstrapGetErr error
	bootstrapSetErr error
	successErr      error
	failureErr      error
}

func newRunnerStoreFake() *runnerStoreFake {
	return &runnerStoreFake{jobs: map[string]Posting{}, bootstrap: map[string]bool{}, bootstrapValues: map[string]json.RawMessage{}, decisions: map[string]Delivery{}, successes: map[string]int{}, failures: map[string]string{}, finalized: map[string][]string{}}
}

func (s *runnerStoreFake) FinalizeSourceSnapshot(_ context.Context, sourceID string, activeJobIDs []string) error {
	if s.finalizeErr != nil {
		return s.finalizeErr
	}
	s.finalized[sourceID] = append([]string(nil), activeJobIDs...)
	return nil
}

func (s *runnerStoreFake) ObserveAndEnqueue(_ context.Context, observation Observation, target *DeliveryTarget) (Posting, bool, Delivery, bool, error) {
	s.observeCalls++
	if s.observeErr != nil && (s.observeErrAt == 0 || s.observeCalls == s.observeErrAt) {
		return Posting{}, false, Delivery{}, false, s.observeErr
	}
	key := observation.Company + "|" + observation.Title + "|" + observation.Location
	posting, exists := s.jobs[key]
	created := !exists
	if created {
		posting = Posting{
			ID: fmt.Sprintf("job-%d", len(s.jobs)+1), Company: observation.Company,
			Title: observation.Title, Location: observation.Location, Country: observation.Country, EmploymentType: observation.EmploymentType,
			Level: observation.Level, ApplyURL: observation.ApplyURL, Description: observation.Description,
			FirstSeenAt: observation.ObservedAt, LastSeenAt: observation.ObservedAt,
		}
		s.jobs[key] = posting
	}
	if target == nil {
		return posting, created, Delivery{}, false, nil
	}
	decisionKey := posting.ID + "|" + target.Channel + "|" + target.Recipient
	if delivery, ok := s.decisions[decisionKey]; ok {
		return posting, created, delivery, false, nil
	}
	delivery := Delivery{ID: int64(len(s.decisions) + 1), JobID: posting.ID, Channel: target.Channel, Recipient: target.Recipient}
	if target.Suppress {
		delivery.Status = "suppressed"
	} else if target.Stage {
		delivery.Status = "staged"
	} else {
		delivery.Status = "pending"
		s.enqueues++
	}
	s.decisions[decisionKey] = delivery
	return posting, created, delivery, true, nil
}

func (s *runnerStoreFake) ActivateDeliveries(_ context.Context, ids []int64, channel, recipient string) (int, error) {
	if s.activateErr != nil {
		return 0, s.activateErr
	}
	wanted := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	activated := 0
	for key, delivery := range s.decisions {
		if _, ok := wanted[delivery.ID]; !ok || delivery.Channel != channel || delivery.Recipient != recipient || delivery.Status != "staged" {
			continue
		}
		delivery.Status = "pending"
		s.decisions[key] = delivery
		activated++
	}
	s.enqueues += activated
	return activated, nil
}

func (s *runnerStoreFake) RecordSourceSuccess(_ context.Context, sourceID string, count int, _ time.Time) error {
	if s.successErr != nil {
		return s.successErr
	}
	s.successes[sourceID] = count
	return nil
}

func (s *runnerStoreFake) RecordSourceFailure(_ context.Context, sourceID string, cause error, _ time.Time) error {
	if s.failureErr != nil {
		return s.failureErr
	}
	s.failures[sourceID] = cause.Error()
	return nil
}

func (s *runnerStoreFake) GetBootstrapState(_ context.Context, key string) (BootstrapState, error) {
	if s.bootstrapGetErr != nil {
		return BootstrapState{}, s.bootstrapGetErr
	}
	if !s.bootstrap[key] {
		return BootstrapState{}, sql.ErrNoRows
	}
	value := s.bootstrapValues[key]
	if len(value) == 0 {
		value = json.RawMessage(`{"complete":true}`)
	}
	return BootstrapState{Key: key, Value: value}, nil
}

func (s *runnerStoreFake) SetBootstrapState(_ context.Context, key string, value json.RawMessage) error {
	if s.bootstrapSetErr != nil {
		return s.bootstrapSetErr
	}
	s.bootstrap[key] = true
	s.bootstrapValues[key] = append(json.RawMessage(nil), value...)
	s.bootstrapWrites++
	return nil
}

func TestRunnerBootstrapsWithoutSendingAndRepeatedRunsDoNotEnqueueTwice(t *testing.T) {
	store := newRunnerStoreFake()
	run := 0
	extractor := extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
		run++
		jobs := []Observation{{
			SourceID: source.ID, SourceNativeID: "existing", Company: source.Company,
			Title: "Software Engineer Intern", Location: "Singapore", ApplyURL: "https://example.test/existing",
		}}
		if run >= 2 {
			jobs = append(jobs, Observation{
				SourceID: source.ID, SourceNativeID: "new", Company: source.Company,
				Title: "Backend Engineer New Grad", Location: "New York", ApplyURL: "https://example.test/new",
			})
		}
		return completeExtraction(jobs...), nil
	})
	runner := Runner{
		Sources:   []Source{{ID: "acme-greenhouse", Company: "Acme", Provider: "greenhouse", URL: "https://example.test"}},
		Extractor: extractor, Store: store, Channel: "telegram", Recipient: "chat-1",
		Now: func() time.Time { return time.Date(2026, 8, 16, 1, 0, 0, 0, time.UTC) },
	}
	bootstrapKey := versionedBootstrapKey("routine", runner.Sources[0], runner.Channel, runner.Recipient)

	first, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Bootstrapping || first.SourcesBootstrapped != 1 || first.Enqueued != 0 || store.enqueues != 0 || !store.bootstrap[bootstrapKey] {
		t.Fatalf("unexpected bootstrap report/store: %#v, enqueues=%d bootstrap=%v", first, store.enqueues, store.bootstrap)
	}

	second, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Bootstrapping || second.Enqueued != 1 || store.enqueues != 1 {
		t.Fatalf("unexpected second report/store: %#v, enqueues=%d", second, store.enqueues)
	}

	third, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if third.Enqueued != 0 || store.enqueues != 1 {
		t.Fatalf("repeated job enqueued twice: %#v, enqueues=%d", third, store.enqueues)
	}
}

func TestRunnerPublishesEligibleFirstSnapshotWhenEnabled(t *testing.T) {
	store := newRunnerStoreFake()
	runner := Runner{
		Sources: []Source{{ID: "acme-greenhouse", Company: "Acme", Provider: "greenhouse", URL: "https://example.test"}},
		Extractor: extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
			return completeExtraction(Observation{
				SourceID: source.ID, SourceNativeID: "first", Company: source.Company,
				Title: "Software Engineer Intern", Location: "Singapore", ApplyURL: "https://example.test/first",
			}), nil
		}),
		Store: store, Channel: "telegram", Recipient: "chat-1", PublishBootstrap: true,
		Now: func() time.Time { return time.Date(2026, 8, 19, 1, 0, 0, 0, time.UTC) },
	}

	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Bootstrapping || report.SourcesBootstrapped != 1 || report.Enqueued != 1 || store.enqueues != 1 {
		t.Fatalf("first eligible snapshot was not published: report=%#v enqueues=%d", report, store.enqueues)
	}
	for _, delivery := range store.decisions {
		if delivery.Status != "pending" {
			t.Fatalf("first eligible snapshot status = %q, want pending", delivery.Status)
		}
	}
}

func TestRunnerUsesDedicatedUniversityBoardAsEarlyCareerEvidence(t *testing.T) {
	store := newRunnerStoreFake()
	titles := []string{
		"Quantitative Researcher (Full-Time - Master's/Bachelor's)",
		"Quantitative Researcher (Full-Time - PhD+)",
		"Quantitative Technologist (C++ Intern)",
		"Quantitative Technologist (Full-Time - C++ Developer)",
		"Quantitative Technologist (Full-Time - DevOps - Night Shift)",
		"Quantitative Technologist (Full-Time - DevOps & Systems Engineering)",
		"Quantitative Business Analyst",
		"Quantitative Technologist (Full-Time - FPGA Engineer, PhD)",
	}
	extractor := extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
		observations := make([]Observation, 0, len(titles))
		for index, title := range titles {
			observations = append(observations, Observation{
				SourceID: source.ID, SourceNativeID: fmt.Sprintf("radix-%d", index),
				Company: source.Company, Title: title, Country: "US", Location: "Chicago",
				EmploymentType: "full_time", Level: "unknown",
			})
		}
		return completeExtraction(observations...), nil
	})
	runner := Runner{
		Sources: []Source{{
			ID: "auto-radix-trading-greenhouse", Company: "Radix Trading", Provider: "greenhouse",
			URL: "https://job-boards.greenhouse.io/radixuniversity",
		}},
		Extractor: extractor, Store: store, Channel: "telegram", Recipient: "chat-1",
	}

	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.EligibleCreated != 6 || len(store.decisions) != 6 || report.Enqueued != 0 {
		t.Fatalf("university board must accept six technical roles and suppress its bootstrap: report=%#v decisions=%d", report, len(store.decisions))
	}
	for _, posting := range store.jobs {
		if posting.Level != "early career" {
			t.Fatalf("source scope was not persisted as level evidence: %#v", posting)
		}
	}
}

func TestSourceHasEarlyCareerScopeRejectsGeneralAndExperiencedBoards(t *testing.T) {
	for _, source := range []Source{
		{URL: "https://job-boards.greenhouse.io/radixexperienced"},
		{URL: "https://job-boards.greenhouse.io/radix"},
		{URL: "https://example.test/jobs?q=university"},
	} {
		if sourceHasEarlyCareerScope(source) {
			t.Fatalf("general source must not supply timing evidence: %s", source.URL)
		}
	}
	if !sourceHasEarlyCareerScope(Source{URL: "https://job-boards.greenhouse.io/radixuniversity"}) {
		t.Fatal("dedicated university source should supply timing evidence")
	}
}

func TestRunnerBootstrapsSourcesIndependentlyAcrossFailureAndRecovery(t *testing.T) {
	store := newRunnerStoreFake()
	run := 0
	extractor := extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
		if source.ID == "broken" && run < 2 {
			return ExtractionResult{}, errors.New("board unavailable")
		}
		if source.ID == "broken" {
			jobs := []Observation{{Company: "Recovered", Title: "Software Engineer Intern", Location: "Singapore"}}
			if run >= 3 {
				jobs = append(jobs, Observation{Company: "Recovered", Title: "Backend Engineer New Grad", Location: "Singapore"})
			}
			return completeExtraction(jobs...), nil
		}
		if run >= 1 {
			return completeExtraction(Observation{Company: "Healthy", Title: "Software Engineer Intern", Location: "Singapore"}), nil
		}
		return completeExtraction(), nil
	})
	runner := Runner{
		Sources: []Source{
			{ID: "broken", Company: "Broken", Provider: "lever", URL: "https://broken.test"},
			{ID: "empty", Company: "Empty", Provider: "ashby", URL: "https://empty.test"},
		},
		Extractor: extractor, Store: store, Channel: "telegram", Recipient: "chat-1",
	}
	emptyBootstrapKey := versionedBootstrapKey("routine", runner.Sources[1], runner.Channel, runner.Recipient)
	brokenBootstrapKey := versionedBootstrapKey("routine", runner.Sources[0], runner.Channel, runner.Recipient)

	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("one healthy source should isolate extraction failure: %v", err)
	}
	if report.SourcesAttempted != 2 || report.SourcesFailed != 1 || report.SourcesSucceeded != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if store.failures["broken"] != "board unavailable" {
		t.Fatalf("failure was not recorded: %#v", store.failures)
	}
	if count, ok := store.successes["empty"]; !ok || count != 0 {
		t.Fatalf("successful zero was not recorded: %#v", store.successes)
	}
	if !store.bootstrap[emptyBootstrapKey] || store.bootstrap[brokenBootstrapKey] {
		t.Fatalf("trusted empty should bootstrap while failed source remains pending: %#v", store.bootstrap)
	}

	run = 1
	report, err = runner.Run(context.Background())
	if err != nil {
		t.Fatalf("healthy source should keep degraded run nonfatal: %v", err)
	}
	if report.Enqueued != 1 || !store.bootstrap[emptyBootstrapKey] {
		t.Fatalf("first job after trusted empty should enqueue: report=%#v bootstrap=%#v", report, store.bootstrap)
	}

	run = 2
	report, err = runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Enqueued != 0 || !store.bootstrap[brokenBootstrapKey] {
		t.Fatalf("recovered source should baseline without alerting: %#v bootstrap=%#v", report, store.bootstrap)
	}

	run = 3
	report, err = runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Enqueued != 1 {
		t.Fatalf("new job after recovery baseline was not enqueued: %#v", report)
	}
}

func TestRunnerDoesNotMarkBootstrapAfterPersistenceFailure(t *testing.T) {
	store := newRunnerStoreFake()
	store.observeErr = errors.New("database unavailable")
	runner := Runner{
		Sources: []Source{{ID: "source", Company: "Acme", Provider: "lever", URL: "https://example.test"}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			return completeExtraction(Observation{Company: "Acme", Title: "Software Engineer Intern"}), nil
		}),
		Store: store, Channel: "telegram", Recipient: "chat-1",
	}
	bootstrapKey := versionedBootstrapKey("routine", runner.Sources[0], runner.Channel, runner.Recipient)
	report, err := runner.Run(context.Background())
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	if report.SourcesSucceeded != 0 || report.SourcesFailed != 1 {
		t.Fatalf("source health report was not truthful: %#v", report)
	}
	if _, ok := store.successes["source"]; ok || store.failures["source"] == "" {
		t.Fatalf("persistence failure was overwritten by success: successes=%#v failures=%#v", store.successes, store.failures)
	}
	if store.bootstrap[bootstrapKey] {
		t.Fatal("bootstrap marked despite incomplete persistence pass")
	}
}

func TestRunnerStagesDeliveryDecisionsUntilWholeSourceSnapshotPersists(t *testing.T) {
	store := newRunnerStoreFake()
	store.observeErr = errors.New("database unavailable")
	store.observeErrAt = 2
	runner := Runner{
		Sources: []Source{{ID: "source", Company: "Acme", Provider: "lever", URL: "https://example.test"}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			return completeExtraction(
				Observation{Company: "Acme", Title: "Software Engineer Intern", SourceNativeID: "one"},
				Observation{Company: "Acme", Title: "Backend Engineer New Grad", SourceNativeID: "two"},
			), nil
		}),
		Store: store, Channel: "telegram", Recipient: "chat-1",
	}

	report, err := runner.Run(context.Background())
	if err == nil || report.SourcesFailed != 1 {
		t.Fatalf("partial source persistence must fail: report=%#v err=%v", report, err)
	}
	if len(store.decisions) != 0 || store.enqueues != 0 {
		t.Fatalf("partial source snapshot created publishable decisions: decisions=%#v enqueues=%d", store.decisions, store.enqueues)
	}
	if _, finalized := store.finalized["source"]; finalized {
		t.Fatal("partial source persistence finalized active jobs")
	}
}

func TestRunnerLeavesPartialDeliveryPhaseUnpublishable(t *testing.T) {
	store := newRunnerStoreFake()
	runner := Runner{
		Sources: []Source{{ID: "source", Company: "Acme", Provider: "lever", URL: "https://example.test"}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			return completeExtraction(
				Observation{Company: "Acme", Title: "Software Engineer Intern", SourceNativeID: "one", Location: "New York", Country: "US"},
				Observation{Company: "Acme", Title: "Backend Engineer New Grad", SourceNativeID: "two", Location: "New York", Country: "US"},
			), nil
		}),
		Store: store, Channel: "telegram", Recipient: "chat-1",
	}
	bootstrapKey := versionedBootstrapKey("routine", runner.Sources[0], runner.Channel, runner.Recipient)
	store.bootstrap[bootstrapKey] = true
	store.bootstrapValues[bootstrapKey] = json.RawMessage(`{"state":"ready"}`)
	// Two observation writes succeed, then the first staged decision succeeds
	// and the second decision fails.
	store.observeErr = errors.New("database unavailable")
	store.observeErrAt = 4

	report, err := runner.Run(context.Background())
	if err == nil || report.SourcesFailed != 1 || report.Enqueued != 0 || store.enqueues != 0 {
		t.Fatalf("partial delivery phase became publishable: report=%#v enqueues=%d err=%v", report, store.enqueues, err)
	}
	if len(store.decisions) != 1 {
		t.Fatalf("staged decisions=%#v, want one durable staged decision", store.decisions)
	}
	for _, decision := range store.decisions {
		if decision.Status != "staged" {
			t.Fatalf("partial decision status=%q, want staged", decision.Status)
		}
	}
}

func TestRunnerActivationFailureLeavesDecisionsUnpublishable(t *testing.T) {
	store := newRunnerStoreFake()
	store.activateErr = errors.New("activation unavailable")
	runner := Runner{
		Sources: []Source{{ID: "source", Company: "Acme", Provider: "lever", URL: "https://example.test"}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			return completeExtraction(Observation{Company: "Acme", Title: "Software Engineer Intern", SourceNativeID: "one", Location: "New York", Country: "US"}), nil
		}),
		Store: store, Channel: "telegram", Recipient: "chat-1",
	}
	bootstrapKey := versionedBootstrapKey("routine", runner.Sources[0], runner.Channel, runner.Recipient)
	store.bootstrap[bootstrapKey] = true
	store.bootstrapValues[bootstrapKey] = json.RawMessage(`{"state":"ready"}`)

	report, err := runner.Run(context.Background())
	if err == nil || report.Enqueued != 0 || store.enqueues != 0 {
		t.Fatalf("activation failure became publishable: report=%#v enqueues=%d err=%v", report, store.enqueues, err)
	}
	for _, decision := range store.decisions {
		if decision.Status != "staged" {
			t.Fatalf("activation failure decision status=%q, want staged", decision.Status)
		}
	}
}

func TestRunnerFinalizesCompleteEmptySnapshot(t *testing.T) {
	store := newRunnerStoreFake()
	runner := Runner{
		Sources:   []Source{{ID: "source", Company: "Acme", Provider: "lever", URL: "https://example.test"}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) { return completeExtraction(), nil }),
		Store:     store, Channel: "telegram", Recipient: "chat-1",
	}

	report, err := runner.Run(context.Background())
	if err != nil || report.SourcesSucceeded != 1 {
		t.Fatalf("complete empty snapshot failed: report=%#v err=%v", report, err)
	}
	activeIDs, finalized := store.finalized["source"]
	if !finalized || len(activeIDs) != 0 {
		t.Fatalf("complete empty snapshot finalization=%v active=%v", finalized, activeIDs)
	}
}

func TestRunnerSnapshotFinalizationFailureIsFatalAndUnpublishable(t *testing.T) {
	store := newRunnerStoreFake()
	store.finalizeErr = errors.New("snapshot state unavailable")
	runner := Runner{
		Sources: []Source{{ID: "source", Company: "Acme", Provider: "lever", URL: "https://example.test"}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			return completeExtraction(Observation{
				Company: "Acme", Title: "Software Engineer Intern", SourceNativeID: "one",
				Location: "New York", Country: "US",
			}), nil
		}),
		Store: store, Channel: "telegram", Recipient: "chat-1",
	}
	bootstrapKey := versionedBootstrapKey("routine", runner.Sources[0], runner.Channel, runner.Recipient)
	store.bootstrap[bootstrapKey] = true
	store.bootstrapValues[bootstrapKey] = json.RawMessage(`{"state":"ready"}`)

	report, err := runner.Run(context.Background())
	if err == nil || report.SourcesFailed != 1 || report.Enqueued != 0 || len(store.decisions) != 0 {
		t.Fatalf("failed snapshot finalization became publishable: report=%#v decisions=%#v err=%v", report, store.decisions, err)
	}
}

func TestRunnerEvaluatesPostingAgeAtCycleTime(t *testing.T) {
	store := newRunnerStoreFake()
	cycleTime := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	postedAt := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	runner := Runner{
		Sources: []Source{{ID: "source", Company: "Acme", Provider: "lever", URL: "https://example.test"}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			return completeExtraction(Observation{
				Company: "Acme", Title: "Software Engineer Intern", SourceNativeID: "stale",
				Location: "New York", Country: "US", PostedAt: &postedAt,
			}), nil
		}),
		Store: store, Channel: "telegram", Recipient: "chat-1", Now: func() time.Time { return cycleTime },
	}
	bootstrapKey := versionedBootstrapKey("routine", runner.Sources[0], runner.Channel, runner.Recipient)
	store.bootstrap[bootstrapKey] = true
	store.bootstrapValues[bootstrapKey] = json.RawMessage(`{"state":"ready"}`)

	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.EligibleCreated != 0 || report.Enqueued != 0 || len(store.decisions) != 0 {
		t.Fatalf("posting stale at cycle time became publishable: report=%#v decisions=%#v", report, store.decisions)
	}
}

func TestRunnerReturnsErrorWhenAllSourcesFail(t *testing.T) {
	store := newRunnerStoreFake()
	runner := Runner{
		Sources: []Source{
			{ID: "one", Company: "One", Provider: "lever", URL: "https://one.test"},
			{ID: "two", Company: "Two", Provider: "ashby", URL: "https://two.test"},
		},
		Extractor: extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
			return ExtractionResult{}, fmt.Errorf("%s unavailable", source.ID)
		}),
		Store: store, Channel: "telegram", Recipient: "chat-1",
	}

	report, err := runner.Run(context.Background())
	if err == nil || report.SourcesSucceeded != 0 || report.SourcesFailed != 2 {
		t.Fatalf("all-source failure must fail run: report=%#v err=%v", report, err)
	}
	if len(store.failures) != 2 {
		t.Fatalf("source failures were not recorded: %#v", store.failures)
	}
}

func TestRunnerTreatsIncompleteSnapshotAsSourceFailure(t *testing.T) {
	store := newRunnerStoreFake()
	runner := Runner{
		Sources: []Source{{ID: "source", Company: "Acme", Provider: "custom", URL: "https://example.test"}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			return ExtractionResult{Observations: []Observation{}}, nil
		}),
		Store: store, Channel: "telegram", Recipient: "chat-1",
	}

	report, err := runner.Run(context.Background())
	if err == nil || report.SourcesFailed != 1 || store.failures["source"] == "" {
		t.Fatalf("incomplete empty must fail closed: report=%#v err=%v failures=%#v", report, err, store.failures)
	}
}

func TestRunnerRebaselinesAfterReadySourceFailure(t *testing.T) {
	store := newRunnerStoreFake()
	phase := 0
	runner := Runner{
		Sources: []Source{{ID: "source", Company: "Acme", Provider: "lever", URL: "https://example.test"}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			switch phase {
			case 1:
				return ExtractionResult{}, errors.New("temporary parser failure")
			case 2:
				return completeExtraction(
					Observation{Company: "Acme", Title: "Software Engineer Intern", Location: "Singapore"},
					Observation{Company: "Acme", Title: "Backend Engineer New Grad", Location: "Singapore"},
				), nil
			case 3:
				return completeExtraction(
					Observation{Company: "Acme", Title: "Software Engineer Intern", Location: "Singapore"},
					Observation{Company: "Acme", Title: "Backend Engineer New Grad", Location: "Singapore"},
					Observation{Company: "Acme", Title: "Machine Learning Engineer Intern", Location: "Singapore"},
				), nil
			default:
				return completeExtraction(Observation{Company: "Acme", Title: "Software Engineer Intern", Location: "Singapore"}), nil
			}
		}),
		Store: store, Channel: "telegram", Recipient: "chat-1",
	}
	key := versionedBootstrapKey("routine", runner.Sources[0], runner.Channel, runner.Recipient)

	if report, err := runner.Run(context.Background()); err != nil || report.Enqueued != 0 {
		t.Fatalf("initial baseline failed: report=%#v err=%v", report, err)
	}
	phase = 1
	if _, err := runner.Run(context.Background()); err == nil {
		t.Fatal("single source extraction failure must fail the run")
	}
	var failedState struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(store.bootstrapValues[key], &failedState); err != nil || failedState.State != bootstrapRebaseline {
		t.Fatalf("failure did not persist rebaseline state: value=%s err=%v", store.bootstrapValues[key], err)
	}
	phase = 2
	if report, err := runner.Run(context.Background()); err != nil || report.Enqueued != 0 || report.SourcesBootstrapped != 1 {
		t.Fatalf("recovery snapshot should be suppressed baseline: report=%#v err=%v", report, err)
	}
	phase = 3
	if report, err := runner.Run(context.Background()); err != nil || report.Enqueued != 1 {
		t.Fatalf("new job after recovery did not enqueue once: report=%#v err=%v", report, err)
	}
}

func TestRunnerTreatsLegacyBootstrapRowAsReady(t *testing.T) {
	store := newRunnerStoreFake()
	runner := Runner{
		Sources: []Source{{ID: "source", Company: "Acme", Provider: "lever", URL: "https://example.test"}},
		Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
			return completeExtraction(Observation{Company: "Acme", Title: "Software Engineer Intern", Location: "Singapore"}), nil
		}),
		Store: store, Channel: "telegram", Recipient: "chat-1",
	}
	key := versionedBootstrapKey("routine", runner.Sources[0], runner.Channel, runner.Recipient)
	store.bootstrap[key] = true
	store.bootstrapValues[key] = json.RawMessage(`{"completed_at":"2026-08-16T00:00:00Z"}`)

	report, err := runner.Run(context.Background())
	if err != nil || report.Bootstrapping || report.Enqueued != 1 {
		t.Fatalf("legacy bootstrap row was not treated as ready: report=%#v err=%v", report, err)
	}
}

func TestRunnerRejectsMalformedBootstrapState(t *testing.T) {
	for name, value := range map[string]json.RawMessage{
		"invalid json":         json.RawMessage(`{`),
		"empty object":         json.RawMessage(`{}`),
		"null":                 json.RawMessage(`null`),
		"array":                json.RawMessage(`[]`),
		"unknown state":        json.RawMessage(`{"state":"maybe"}`),
		"bad legacy timestamp": json.RawMessage(`{"completed_at":"yesterday"}`),
	} {
		t.Run(name, func(t *testing.T) {
			store := newRunnerStoreFake()
			runner := Runner{
				Sources: []Source{{ID: "source", Company: "Acme", Provider: "lever", URL: "https://example.test"}},
				Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
					t.Fatal("extractor must not run after an unreadable bootstrap state")
					return ExtractionResult{}, nil
				}),
				Store: store, Channel: "telegram", Recipient: "chat-1",
			}
			key := versionedBootstrapKey("routine", runner.Sources[0], runner.Channel, runner.Recipient)
			store.bootstrap[key] = true
			store.bootstrapValues[key] = value

			report, err := runner.Run(context.Background())
			if err == nil || report.SourcesFailed != 1 || report.SourcesSucceeded != 0 {
				t.Fatalf("malformed bootstrap state must fail closed: report=%#v err=%v", report, err)
			}
		})
	}
}

func TestRunnerCycleDeadlineRemainsFatalAfterAnotherSourceSucceeds(t *testing.T) {
	store := newRunnerStoreFake()
	runner := Runner{
		Sources: []Source{
			{ID: "healthy", Company: "Healthy", Provider: "ashby", URL: "https://healthy.test"},
			{ID: "timeout", Company: "Timeout", Provider: "lever", URL: "https://timeout.test"},
		},
		Extractor: extractorFunc(func(ctx context.Context, source Source) (ExtractionResult, error) {
			if source.ID == "healthy" {
				return completeExtraction(), nil
			}
			<-ctx.Done()
			return ExtractionResult{}, ctx.Err()
		}),
		Store: store, Channel: "telegram", Recipient: "chat-1",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	report, err := runner.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || report.SourcesSucceeded != 1 || report.SourcesFailed != 1 {
		t.Fatalf("cycle deadline was downgraded: report=%#v err=%v", report, err)
	}
}

func TestRunnerBootstrapPersistenceFailureIsFatalDespiteHealthySource(t *testing.T) {
	store := newRunnerStoreFake()
	failedSource := Source{ID: "failed", Company: "Failed", Provider: "lever", URL: "https://failed.test"}
	failedKey := versionedBootstrapKey("routine", failedSource, "telegram", "chat-1")
	store.bootstrap[failedKey] = true
	store.bootstrapValues[failedKey] = json.RawMessage(`{"state":"ready"}`)
	store.bootstrapSetErr = errors.New("bootstrap database unavailable")
	runner := Runner{
		Sources: []Source{
			failedSource,
			{ID: "healthy", Company: "Healthy", Provider: "ashby", URL: "https://healthy.test"},
		},
		Extractor: extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
			if source.ID == "failed" {
				return ExtractionResult{}, errors.New("parser failed")
			}
			return completeExtraction(Observation{Company: "Healthy", Title: "Software Engineer Intern"}), nil
		}),
		Store: store, Channel: "telegram", Recipient: "chat-1",
	}

	report, err := runner.Run(context.Background())
	if err == nil || report.SourcesSucceeded != 0 {
		// The healthy source also needs to write its initial bootstrap marker,
		// which shares the simulated failing persistence boundary.
		t.Fatalf("bootstrap persistence failure must be fatal: report=%#v err=%v", report, err)
	}
}

func TestRunnerSourceStatusPersistenceFailureIsFatal(t *testing.T) {
	t.Run("success status", func(t *testing.T) {
		store := newRunnerStoreFake()
		store.successErr = errors.New("source status unavailable")
		runner := Runner{
			Sources: []Source{{ID: "healthy", Company: "Healthy", Provider: "lever", URL: "https://healthy.test"}},
			Extractor: extractorFunc(func(context.Context, Source) (ExtractionResult, error) {
				return completeExtraction(), nil
			}),
			Store: store, Channel: "telegram", Recipient: "chat-1",
		}
		if _, err := runner.Run(context.Background()); err == nil {
			t.Fatal("success-status persistence failure must fail the run")
		}
		key := versionedBootstrapKey("routine", runner.Sources[0], runner.Channel, runner.Recipient)
		if store.bootstrap[key] {
			t.Fatal("bootstrap advanced despite source-status persistence failure")
		}
	})

	t.Run("failure status with another healthy source", func(t *testing.T) {
		store := newRunnerStoreFake()
		store.failureErr = errors.New("source status unavailable")
		runner := Runner{
			Sources: []Source{
				{ID: "failed", Company: "Failed", Provider: "lever", URL: "https://failed.test"},
				{ID: "healthy", Company: "Healthy", Provider: "ashby", URL: "https://healthy.test"},
			},
			Extractor: extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
				if source.ID == "failed" {
					return ExtractionResult{}, errors.New("parser failed")
				}
				return completeExtraction(), nil
			}),
			Store: store, Channel: "telegram", Recipient: "chat-1",
		}
		report, err := runner.Run(context.Background())
		if err == nil || report.SourcesSucceeded != 1 {
			t.Fatalf("failure-status persistence must remain fatal despite healthy source: report=%#v err=%v", report, err)
		}
	})
}

func TestRunnerRebaselinesWhenSourceOrDestinationChanges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Runner)
	}{
		{name: "url", mutate: func(r *Runner) { r.Sources[0].URL = "https://example.test/repaired" }},
		{name: "provider", mutate: func(r *Runner) { r.Sources[0].Provider = "lever" }},
		{name: "channel", mutate: func(r *Runner) { r.Channel = "log" }},
		{name: "recipient", mutate: func(r *Runner) { r.Recipient = "chat-2" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newRunnerStoreFake()
			phase := 0
			runner := Runner{
				Sources: []Source{{ID: "source", Company: "Acme", Provider: "greenhouse", URL: "https://example.test/original"}},
				Extractor: extractorFunc(func(_ context.Context, source Source) (ExtractionResult, error) {
					jobs := []Observation{{SourceID: source.ID, Company: source.Company, Title: "Software Engineer Intern", Location: "Singapore"}}
					if phase >= 1 {
						jobs = append(jobs, Observation{SourceID: source.ID, Company: source.Company, Title: "Backend Engineer New Grad", Location: "Singapore"})
					}
					if phase >= 2 {
						jobs = append(jobs, Observation{SourceID: source.ID, Company: source.Company, Title: "Machine Learning Engineer Intern", Location: "Singapore"})
					}
					return completeExtraction(jobs...), nil
				}),
				Store: store, Channel: "telegram", Recipient: "chat-1",
			}
			if report, err := runner.Run(context.Background()); err != nil || report.Enqueued != 0 {
				t.Fatalf("initial baseline report=%#v err=%v", report, err)
			}

			test.mutate(&runner)
			phase = 1
			if report, err := runner.Run(context.Background()); err != nil || report.Enqueued != 0 || report.SourcesBootstrapped != 1 {
				t.Fatalf("changed configuration did not rebaseline: report=%#v err=%v", report, err)
			}

			phase = 2
			if report, err := runner.Run(context.Background()); err != nil || report.Enqueued != 1 {
				t.Fatalf("new job after changed baseline did not alert once: report=%#v err=%v", report, err)
			}
		})
	}
}
