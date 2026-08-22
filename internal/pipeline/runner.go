package pipeline

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
)

const defaultBootstrapKey = "routine"

const (
	bootstrapReady      = "ready"
	bootstrapRebaseline = "rebaseline"
)

// RunnerStore is the complete persistence boundary for a one-shot routine run.
// postgres.PostgresStore implements it directly; tests use an in-memory fake.
type RunnerStore interface {
	ObserveAndEnqueue(context.Context, Observation, *DeliveryTarget) (Posting, bool, Delivery, bool, error)
	RecordRejectedObservation(context.Context, RejectedObservation) error
	FinalizeSourcePass(context.Context, SourcePassFinalization) (int, error)
	RecordSourceFailure(context.Context, string, error, time.Time) error
	GetBootstrapState(context.Context, string) (BootstrapState, error)
	SetBootstrapState(context.Context, string, json.RawMessage) error
}

type Runner struct {
	Sources          []Source
	Extractor        Extractor
	Store            RunnerStore
	Channel          string
	Recipient        string
	BootstrapKey     string
	PublishBootstrap bool
	Now              func() time.Time
}

type RunReport struct {
	Bootstrapping       bool
	SourcesBootstrapped int
	SourcesAttempted    int
	SourcesSucceeded    int
	SourcesFailed       int
	Observed            int
	Created             int
	EligibleCreated     int
	Enqueued            int
	Errors              []error
}

// Run performs one bounded pass over the verified source catalog. Individual
// extraction failures are recorded and isolated when at least one source
// succeeds. Bootstrap, persistence, and source-status failures are operational
// errors and always fail the run.
func (r Runner) Run(ctx context.Context) (RunReport, error) {
	var report RunReport
	if r.Extractor == nil {
		return report, errors.New("lite: extractor is required")
	}
	if r.Store == nil {
		return report, errors.New("lite: store is required")
	}
	channel, recipient := strings.TrimSpace(r.Channel), strings.TrimSpace(r.Recipient)
	if channel == "" || recipient == "" {
		return report, errors.New("lite: delivery channel and recipient are required")
	}
	if len(r.Sources) == 0 {
		return report, errors.New("lite: at least one verified source is required")
	}

	bootstrapKey := strings.TrimSpace(r.BootstrapKey)
	if bootstrapKey == "" {
		bootstrapKey = defaultBootstrapKey
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	var operationalErrors []error
	for _, source := range r.Sources {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		report.SourcesAttempted++
		attemptedAt := now().UTC()
		sourceBootstrapKey := versionedBootstrapKey(bootstrapKey, source, channel, recipient)
		bootstrapMode, bootstrapErr := r.bootstrapMode(ctx, sourceBootstrapKey)
		if bootstrapErr != nil {
			sourceErr := fmt.Errorf("source %s read bootstrap state: %w", source.ID, bootstrapErr)
			report.SourcesFailed++
			report.Errors = append(report.Errors, sourceErr)
			operationalErrors = append(operationalErrors, sourceErr)
			if statusErr := r.Store.RecordSourceFailure(ctx, source.ID, sourceErr, attemptedAt); statusErr != nil {
				statusErr = fmt.Errorf("source %s record failure: %w", source.ID, statusErr)
				report.Errors = append(report.Errors, statusErr)
				operationalErrors = append(operationalErrors, statusErr)
			}
			continue
		}

		extraction, extractErr := r.Extractor.Extract(ctx, source)
		if extractErr == nil && ctx.Err() != nil {
			extractErr = ctx.Err()
		}
		if extractErr == nil && !strings.EqualFold(strings.TrimSpace(source.Provider), "market_search") {
			if reported, mismatch := snapshotOwnershipMismatch(source, extraction.Observations); mismatch {
				extractErr = fmt.Errorf("reported employer %q does not match source company identity %q", reported, source.Company)
			}
		}
		if extractErr == nil && !extraction.Complete {
			extractErr = errors.New("extractor returned an incomplete snapshot")
		}
		if extractErr != nil {
			report.SourcesFailed++
			sourceErr := fmt.Errorf("source %s extract: %w", source.ID, extractErr)
			report.Errors = append(report.Errors, sourceErr)
			// A cycle shutdown or budget expiry says nothing about source health.
			// Preserve the last real outcome and bootstrap state so the fair
			// scheduler can retry this route without manufacturing a failure.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return report, errors.Join(ctxErr, sourceErr, errors.Join(operationalErrors...))
			}
			failureCtx, failureCancel := sourceFinalizationContext(ctx)
			if bootstrapMode == bootstrapReady {
				if stateErr := r.setBootstrapMode(failureCtx, sourceBootstrapKey, bootstrapRebaseline, now().UTC()); stateErr != nil {
					stateErr = fmt.Errorf("source %s mark for rebaseline: %w", source.ID, stateErr)
					report.Errors = append(report.Errors, stateErr)
					operationalErrors = append(operationalErrors, stateErr)
				}
			}
			if statusErr := r.Store.RecordSourceFailure(failureCtx, source.ID, extractErr, attemptedAt); statusErr != nil {
				statusErr = fmt.Errorf("source %s record failure: %w", source.ID, statusErr)
				report.Errors = append(report.Errors, statusErr)
				operationalErrors = append(operationalErrors, statusErr)
			}
			failureCancel()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return report, errors.Join(ctxErr, sourceErr, errors.Join(operationalErrors...))
			}
			continue
		}

		observations := extraction.Observations
		report.Observed += len(observations)
		sourceBootstrapping := bootstrapMode != bootstrapReady
		if sourceBootstrapping {
			report.Bootstrapping = true
		}
		sourceComplete := true
		var sourceErrors []error
		stagedDeliveryIDs := make(map[int64]struct{})
		type preparedObservation struct {
			observation Observation
			postingID   string
			eligible    bool
		}
		prepared := make([]preparedObservation, 0, len(observations))
		for _, observation := range observations {
			if err := ctx.Err(); err != nil {
				sourceComplete = false
				sourceErrors = append(sourceErrors, err)
				break
			}
			if observation.SourceID == "" {
				observation.SourceID = source.ID
			}
			if observation.Company == "" {
				observation.Company = source.Company
			}
			if observation.ObservedAt.IsZero() {
				observation.ObservedAt = attemptedAt
			}
			observation.Authority = sourceAuthority(source)
			observation.SnapshotPending = true
			enrichObservationWithSourceScope(&observation, source)
			candidate := Posting{
				Company: observation.Company, Title: observation.Title, Location: observation.Location,
				Country:        observation.Country,
				EmploymentType: observation.EmploymentType, Level: observation.Level,
				ApplyURL: observation.ApplyURL, Description: observation.Description, PostedAt: observation.PostedAt,
			}
			decision := EvaluateJobAdmissionAt(candidate, attemptedAt)
			if !decision.Accepted {
				if rejectErr := r.Store.RecordRejectedObservation(ctx, RejectedObservation{
					Observation: observation, Code: decision.Code, PolicyVersion: decision.PolicyVersion,
				}); rejectErr != nil {
					sourceComplete = false
					sourceErrors = append(sourceErrors, fmt.Errorf("persist rejected observation: %w", rejectErr))
					break
				}
				continue
			}
			posting, created, _, _, observeErr := r.Store.ObserveAndEnqueue(ctx, observation, nil)
			if observeErr != nil {
				sourceComplete = false
				sourceErrors = append(sourceErrors, fmt.Errorf("persist observation: %w", observeErr))
				break
			}
			prepared = append(prepared, preparedObservation{observation: observation, postingID: posting.ID, eligible: true})
			if created {
				report.Created++
				report.EligibleCreated++
			}
		}
		// Delivery decisions are a second phase. A partially persisted source
		// snapshot therefore cannot leave publishable rows behind.
		if sourceComplete {
			for _, item := range prepared {
				if !item.eligible {
					continue
				}
				if err := ctx.Err(); err != nil {
					sourceComplete = false
					sourceErrors = append(sourceErrors, err)
					break
				}
				publishable := !sourceBootstrapping || r.PublishBootstrap
				target := &DeliveryTarget{Channel: channel, Recipient: recipient, Suppress: !publishable, Stage: publishable}
				_, _, delivery, _, observeErr := r.Store.ObserveAndEnqueue(ctx, item.observation, target)
				if observeErr != nil {
					sourceComplete = false
					sourceErrors = append(sourceErrors, fmt.Errorf("persist delivery decision: %w", observeErr))
					break
				}
				if !target.Suppress {
					if delivery.Status == "staged" {
						stagedDeliveryIDs[delivery.ID] = struct{}{}
					}
				}
			}
		}
		if sourceComplete {
			activeJobIDs := make([]string, 0, len(prepared))
			for _, item := range prepared {
				activeJobIDs = append(activeJobIDs, item.postingID)
			}
			deliveryIDs := make([]int64, 0, len(stagedDeliveryIDs))
			for id := range stagedDeliveryIDs {
				deliveryIDs = append(deliveryIDs, id)
			}
			finalization := SourcePassFinalization{
				SourceID: source.ID, ActiveJobIDs: activeJobIDs, DeliveryIDs: deliveryIDs,
				Channel: channel, Recipient: recipient, ObservedCount: len(observations), AttemptedAt: attemptedAt,
			}
			if sourceBootstrapping {
				finalization.BootstrapKey = sourceBootstrapKey
				value, marshalErr := bootstrapStateValue(bootstrapReady, now().UTC())
				if marshalErr != nil {
					sourceComplete = false
					sourceErrors = append(sourceErrors, fmt.Errorf("encode bootstrap state: %w", marshalErr))
				} else {
					finalization.BootstrapValue = value
				}
			}
			if sourceComplete {
				activated, finalizeErr := r.Store.FinalizeSourcePass(ctx, finalization)
				if finalizeErr != nil {
					sourceComplete = false
					sourceErrors = append(sourceErrors, fmt.Errorf("finalize source pass: %w", finalizeErr))
				} else {
					report.Enqueued += activated
					if sourceBootstrapping {
						report.SourcesBootstrapped++
					}
				}
			}
		}
		if sourceComplete {
			report.SourcesSucceeded++
			continue
		}

		report.SourcesFailed++
		sourceErr := errors.Join(sourceErrors...)
		wrappedSourceErr := fmt.Errorf("source %s: %w", source.ID, sourceErr)
		report.Errors = append(report.Errors, wrappedSourceErr)
		operationalErrors = append(operationalErrors, wrappedSourceErr)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return report, errors.Join(ctxErr, errors.Join(operationalErrors...))
		}
		failureCtx, failureCancel := sourceFinalizationContext(ctx)
		if bootstrapMode == bootstrapReady {
			if stateErr := r.setBootstrapMode(failureCtx, sourceBootstrapKey, bootstrapRebaseline, now().UTC()); stateErr != nil {
				stateErr = fmt.Errorf("source %s mark for rebaseline: %w", source.ID, stateErr)
				report.Errors = append(report.Errors, stateErr)
				operationalErrors = append(operationalErrors, stateErr)
			}
		}
		if statusErr := r.Store.RecordSourceFailure(failureCtx, source.ID, sourceErr, attemptedAt); statusErr != nil {
			statusErr = fmt.Errorf("source %s record failure: %w", source.ID, statusErr)
			report.Errors = append(report.Errors, statusErr)
			operationalErrors = append(operationalErrors, statusErr)
		}
		failureCancel()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return report, errors.Join(ctxErr, errors.Join(operationalErrors...))
		}
	}
	if len(operationalErrors) > 0 {
		return report, errors.Join(operationalErrors...)
	}
	if report.SourcesSucceeded == 0 {
		return report, fmt.Errorf("lite: all sources failed: %w", errors.Join(report.Errors...))
	}
	return report, nil
}

// sourceAuthority keeps a weaker auto-discovered route from replacing fields
// already supplied by the reviewed catalog. Lower values are stronger.
func sourceAuthority(source Source) int {
	id := strings.ToLower(strings.TrimSpace(source.ID))
	provider := strings.ToLower(strings.TrimSpace(source.Provider))
	switch {
	case provider == "market_search" || strings.HasPrefix(id, "market-"):
		return 30
	case strings.HasPrefix(id, "auto-"):
		return 20
	default:
		return 10
	}
}

// enrichObservationWithSourceScope turns an explicitly early-career board
// boundary into durable timing evidence. This is deliberately limited to
// dedicated board slugs; a general company board or search query never gains
// early-career status merely because its descriptions mention students.
func enrichObservationWithSourceScope(observation *Observation, source Source) {
	if observation == nil || !sourceHasEarlyCareerScope(source) {
		return
	}
	level := strings.TrimSpace(observation.Level)
	if level == "" || hasAnyPhrase(normalizedText(level), []string{"unknown", "not stated", "unspecified"}) {
		observation.Level = "early career"
	}
}

func sourceHasEarlyCareerScope(source Source) bool {
	parsed, err := url.Parse(strings.TrimSpace(source.URL))
	if err != nil {
		return false
	}
	slug := strings.ToLower(path.Base(strings.Trim(parsed.Path, "/")))
	slug = strings.NewReplacer("-", "", "_", "").Replace(slug)
	for _, suffix := range []string{"university", "earlycareer", "newgrad", "graduates"} {
		if strings.HasSuffix(slug, suffix) {
			return true
		}
	}
	return false
}

func (r Runner) bootstrapMode(ctx context.Context, key string) (string, error) {
	state, err := r.Store.GetBootstrapState(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// Rows written before explicit state existed represented completed
	// bootstraps. Preserve that contract during the in-place migration.
	var value struct {
		State       string `json:"state"`
		CompletedAt string `json:"completed_at"`
	}
	if len(state.Value) == 0 {
		return "", errors.New("bootstrap state is empty")
	}
	if err := json.Unmarshal(state.Value, &value); err != nil {
		return "", fmt.Errorf("decode bootstrap state: %w", err)
	}
	value.State = strings.TrimSpace(value.State)
	if value.State == "" {
		completedAt := strings.TrimSpace(value.CompletedAt)
		if completedAt == "" {
			return "", errors.New("bootstrap state has neither state nor legacy completed_at")
		}
		if _, err := time.Parse(time.RFC3339Nano, completedAt); err != nil {
			return "", fmt.Errorf("decode legacy bootstrap completed_at: %w", err)
		}
		return bootstrapReady, nil
	}
	switch value.State {
	case bootstrapReady, bootstrapRebaseline:
		return value.State, nil
	default:
		return "", fmt.Errorf("unknown bootstrap state %q", value.State)
	}
}

func sourceFinalizationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent.Err() == nil {
		return parent, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
}

func (r Runner) setBootstrapMode(ctx context.Context, key, state string, at time.Time) error {
	value, err := bootstrapStateValue(state, at)
	if err != nil {
		return err
	}
	return r.Store.SetBootstrapState(ctx, key, value)
}

func bootstrapStateValue(state string, at time.Time) (json.RawMessage, error) {
	return json.Marshal(map[string]string{
		"state":        state,
		"completed_at": at.UTC().Format(time.RFC3339Nano),
	})
}

func versionedBootstrapKey(prefix string, source Source, channel, recipient string) string {
	canonicalURL := CanonicalApplyURL(source.URL)
	if canonicalURL == "" {
		canonicalURL = strings.TrimSpace(source.URL)
	}
	seed := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(source.Provider)),
		canonicalURL,
		strings.ToLower(strings.TrimSpace(channel)),
		strings.TrimSpace(recipient),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return prefix + ":" + source.ID + ":" + fmt.Sprintf("%x", sum[:])
}
