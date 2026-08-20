package dashboard

import (
	"time"

	"github.com/hwennnn/radar/internal/pipeline"
)

// healthState is a small dashboard-test projection. Runtime readiness behavior
// itself remains covered in internal/app.
type healthState struct {
	current HealthSnapshot
}

func (s *healthState) Snapshot() HealthSnapshot {
	return s.current
}

func (s *healthState) recordReadOnly(runtime *pipeline.RuntimeState) {
	s.current.Ready = true
	if runtime == nil {
		return
	}
	s.current.Degraded = runtime.LastCycleState == "degraded"
	if runtime.LastCycleFinished != nil {
		s.current.LastCycleAt = *runtime.LastCycleFinished
	}
}

func (s *healthState) recordCycle(report pipeline.RunReport, discovery pipeline.DiscoveryReport, delivery pipeline.DeliveryReport, err error) {
	s.current = HealthSnapshot{
		Ready:            err == nil,
		Degraded:         err == nil && (report.SourcesFailed > 0 || discovery.CandidatesFailed > 0 || delivery.Failed > 0),
		LastCycleAt:      time.Now().UTC(),
		LastCycleError:   err != nil,
		SourcesSucceeded: report.SourcesSucceeded,
		SourcesFailed:    report.SourcesFailed,
		DeliveryFailures: delivery.Failed,
	}
}
