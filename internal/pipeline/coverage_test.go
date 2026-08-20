package pipeline

import "testing"

func TestAuditUniverseRejectsShallowUnauditableSeed(t *testing.T) {
	report := AuditUniverse(
		Catalog{Companies: []Company{{ID: "known", Name: "Known", Sources: []Source{{ID: "known-jobs", Provider: "greenhouse", URL: "https://example.com/jobs"}}}}},
		DiscoverySeed{Candidates: []DiscoveryCandidate{{ID: "unknown", Name: "Unknown", Tags: []string{"ai"}}}},
	)
	if report.Pass || len(report.Errors) == 0 {
		t.Fatalf("shallow universe unexpectedly passed: %+v", report)
	}
}

func TestDiscoveryPriorityPrefersFocusLanesThenResearchTiers(t *testing.T) {
	seed := DiscoverySeed{Candidates: []DiscoveryCandidate{
		{ID: "low", Name: "Low", Tags: []string{"priority-3"}},
		{ID: "top", Name: "Top", Tags: []string{"priority-1"}},
		{ID: "market", Name: "Market", Tags: []string{"auto-market-search"}},
		{ID: "yc", Name: "YC", Tags: []string{"priority-2", "yc-top"}},
		{ID: "quant", Name: "Quant", Tags: []string{"priority-1", "quant"}},
		{ID: "big-tech", Name: "Big Tech", Tags: []string{"priority-1", "big-tech"}},
		{ID: "unicorn", Name: "Unicorn", Tags: []string{"priority-1", "unicorn"}},
		{ID: "middle", Name: "Middle", Tags: []string{"priority-2"}},
	}}
	missing := MissingDiscoveryCandidates(Catalog{}, seed)
	for index, want := range []string{"market", "yc", "quant", "big-tech", "unicorn", "top", "middle", "low"} {
		if missing[index].ID != want {
			t.Fatalf("missing[%d] = %q, want %q", index, missing[index].ID, want)
		}
	}
}
