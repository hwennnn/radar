package pipeline

import "strings"

const (
	DiscoveryFailureUnknown           = "unknown"
	DiscoveryFailureTimeout           = "timeout"
	DiscoveryFailureRateLimited       = "rate_limited"
	DiscoveryFailureRouteMissing      = "route_missing"
	DiscoveryFailureIncomplete        = "incomplete_snapshot"
	DiscoveryFailureOwnershipMismatch = "ownership_mismatch"
	DiscoveryFailureProviderMismatch  = "provider_mismatch"
	DiscoveryFailureNontechnical      = "nontechnical_board"
	DiscoveryFailureAggregator        = "aggregator"
	DiscoveryFailureCompanyQuality    = "company_quality"
)

// DiscoveryFailureClass converts private provider errors into stable control-
// plane codes. Terminal failures are parked until new evidence or an operator
// explicitly restores the candidate; transient failures retain normal backoff.
func DiscoveryFailureClass(cause error) (code string, terminal bool) {
	if cause == nil {
		return DiscoveryFailureUnknown, false
	}
	message := strings.ToLower(strings.TrimSpace(cause.Error()))
	switch {
	case strings.Contains(message, "blocked job aggregator"), strings.Contains(message, "aggregator/search listing"):
		return DiscoveryFailureAggregator, true
	case strings.Contains(message, "high-signal target evidence"), strings.Contains(message, "company quality gate"):
		return DiscoveryFailureCompanyQuality, true
	case strings.Contains(message, "identity does not match"), strings.Contains(message, "does not match candidate company identity"):
		return DiscoveryFailureOwnershipMismatch, true
	case strings.Contains(message, "unsupported provider"), strings.Contains(message, "provider mismatch"):
		return DiscoveryFailureProviderMismatch, true
	case strings.Contains(message, "no relevant technical"), strings.Contains(message, "no active job-role"):
		return DiscoveryFailureNontechnical, true
	case strings.Contains(message, "429"), strings.Contains(message, "rate limit"):
		return DiscoveryFailureRateLimited, false
	case strings.Contains(message, "timeout"), strings.Contains(message, "timed out"), strings.Contains(message, "deadline exceeded"):
		return DiscoveryFailureTimeout, false
	case strings.Contains(message, "404"), strings.Contains(message, "not found"):
		return DiscoveryFailureRouteMissing, false
	case strings.Contains(message, "incomplete snapshot"):
		return DiscoveryFailureIncomplete, false
	default:
		return DiscoveryFailureUnknown, false
	}
}
