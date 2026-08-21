package pipeline

import (
	"errors"
	"testing"
)

func TestDiscoveryFailureClass(t *testing.T) {
	tests := []struct {
		message  string
		code     string
		terminal bool
	}{
		{"source request timed out", DiscoveryFailureTimeout, false},
		{"429 rate limited", DiscoveryFailureRateLimited, false},
		{"source returned an incomplete snapshot", DiscoveryFailureIncomplete, false},
		{"discovered source does not match candidate company identity", DiscoveryFailureOwnershipMismatch, true},
		{"company lacks high-signal target evidence", DiscoveryFailureCompanyQuality, true},
		{"structured board returned postings but no relevant technical job roles", DiscoveryFailureNontechnical, true},
		{"market result points to blocked job aggregator", DiscoveryFailureAggregator, true},
	}
	for _, test := range tests {
		code, terminal := DiscoveryFailureClass(errors.New(test.message))
		if code != test.code || terminal != test.terminal {
			t.Fatalf("DiscoveryFailureClass(%q) = %q/%v, want %q/%v", test.message, code, terminal, test.code, test.terminal)
		}
	}
}
