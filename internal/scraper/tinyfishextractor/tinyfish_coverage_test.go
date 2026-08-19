package tinyfishextractor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type tinyFishFallbackProof struct {
	Capability string
	ProofTest  string
}

var tinyFishFallbackProofs = []tinyFishFallbackProof{
	{Capability: "search_fetch_normalization", ProofTest: "TestTinyFishSearchExtractorSearchesFetchesAndNormalizesJobs"},
	{Capability: "search_result_noise_filtering", ProofTest: "TestTinyFishSearchExtractorSkipsNoisyDiscoveryResults"},
	{Capability: "scoped_source_search", ProofTest: "TestTinyFishSearchExtractorKeepsScopedSearchResultsOnSourceSite"},
	{Capability: "offsite_scope_guard", ProofTest: "TestTinyFishSearchExtractorSkipsFetchWhenScopedResultsAreOffSite"},
	{Capability: "fetch_error_classification", ProofTest: "TestTinyFishSearchExtractorReturnsFetchErrorWhenAllSelectedFetchesFail"},
	{Capability: "target_market_seed_inventory", ProofTest: "TestTinyFishSearchExtractorSeedsTargetMarketSources"},
	{Capability: "synthetic_source_intent", ProofTest: "TestTinyFishSearchExtractorRestoresTargetMarketSyntheticIntent"},
	{Capability: "hosted_board_query_scoping", ProofTest: "TestTinyFishSearchExtractorScopesHostedFallbackBoardQueries"},
	{Capability: "remote_board_query_scoping", ProofTest: "TestTinyFishSearchExtractorScopesRemoteSearchBoardQueries"},
	{Capability: "broad_discovery_query_scoping", ProofTest: "TestTinyFishSearchExtractorScopesBroadDiscoveryBoardQueries"},
	{Capability: "primary_discovery_query_scoping", ProofTest: "TestTinyFishSearchExtractorScopesPrimaryDiscoveryBoardQueries"},
	{Capability: "metadata_override", ProofTest: "TestTinyFishSearchExtractorMetadataOverridesURLSearchIntent"},
	{Capability: "relative_posted_date", ProofTest: "TestTinyFishSearchExtractorParsesRelativeFetchedPostedDate"},
	{Capability: "ai_extraction", ProofTest: "TestTinyFishAIExtractorFetchesMessyPageAndNormalizesMultipleJobs"},
	{Capability: "ai_no_jobs_guard", ProofTest: "TestTinyFishAIExtractorReturnsNoJobsForIrrelevantPage"},
	{Capability: "agent_json_normalization", ProofTest: "TestTinyFishAgentExtractorRunsAgentAndNormalizesJobs"},
	{Capability: "agent_fenced_json", ProofTest: "TestTinyFishAgentExtractorParsesFencedJSONResult"},
	{Capability: "agent_wrapped_json", ProofTest: "TestTinyFishAgentExtractorParsesProseWrappedJSONArray"},
	{Capability: "agent_async_polling", ProofTest: "TestTinyFishAgentExtractorPollsAsyncRunAndNormalizesJobs"},
	{Capability: "agent_cancel_on_context", ProofTest: "TestTinyFishAgentExtractorCancelsAsyncRunOnContextCancel"},
}

func TestTinyFishFallbackCoverageHasDeterministicProofs(t *testing.T) {
	fixtures := readTinyFishTestFile(t)
	for _, proof := range tinyFishFallbackProofs {
		if proof.Capability == "" || proof.ProofTest == "" {
			t.Fatalf("incomplete TinyFish fallback proof: %#v", proof)
		}
		signature := "func " + proof.ProofTest + "("
		if !strings.Contains(fixtures, signature) {
			t.Fatalf("TinyFish fallback capability %q proof test %q not found in tinyfish_test.go", proof.Capability, proof.ProofTest)
		}
	}
}

func TestTinyFishFallbackCoverageHasUsefulShape(t *testing.T) {
	if len(tinyFishFallbackProofs) < 15 {
		t.Fatalf("TinyFish fallback proof contract has %d capabilities, want at least 15", len(tinyFishFallbackProofs))
	}
	capabilities := map[string]struct{}{}
	tests := map[string]struct{}{}
	for _, proof := range tinyFishFallbackProofs {
		if _, seen := capabilities[proof.Capability]; seen {
			t.Fatalf("duplicate TinyFish fallback capability %q", proof.Capability)
		}
		if _, seen := tests[proof.ProofTest]; seen {
			t.Fatalf("duplicate TinyFish fallback proof test %q", proof.ProofTest)
		}
		capabilities[proof.Capability] = struct{}{}
		tests[proof.ProofTest] = struct{}{}
	}
	for _, required := range []string{
		"search_fetch_normalization",
		"target_market_seed_inventory",
		"ai_extraction",
		"agent_json_normalization",
		"agent_async_polling",
		"agent_cancel_on_context",
	} {
		if _, ok := capabilities[required]; !ok {
			t.Fatalf("TinyFish fallback proof contract missing %q", required)
		}
	}
}

func readTinyFishTestFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "tinyfish_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
