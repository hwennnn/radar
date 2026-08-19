package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealCatalogFilesKeepRoutineAndDiscoverySeparate(t *testing.T) {
	catalogFile := openConfigFile(t, "sources.json")
	catalog, err := LoadCatalog(catalogFile)
	if err != nil {
		t.Fatalf("LoadCatalog(real file) error = %v", err)
	}
	if err := catalogFile.Close(); err != nil {
		t.Fatalf("close verified catalog: %v", err)
	}

	if got := len(catalog.Companies); got != 49 {
		t.Fatalf("verified companies = %d, want 49", got)
	}

	companyByID := make(map[string]Company, len(catalog.Companies))
	sourceURLs := make(map[string]string, len(catalog.Companies))
	for _, company := range catalog.Companies {
		identity := strings.ToLower(company.ID + " " + company.Name)
		if strings.Contains(identity, "tencent") || strings.Contains(identity, "wechat") {
			t.Fatalf("blocked company leaked into verified catalog: %#v", company)
		}
		companyByID[company.ID] = company
		for _, source := range company.Sources {
			if previous, exists := sourceURLs[source.URL]; exists {
				t.Fatalf("source URL %q is shared by %q and %q", source.URL, previous, source.ID)
			}
			sourceURLs[source.URL] = source.ID
		}
	}
	if got := len(catalog.RoutineSources()); got != 49 {
		t.Fatalf("routine sources = %d, want one per verified company", got)
	}

	assertCatalogSource(t, companyByID, "abridge", "ashby", "https://jobs.ashbyhq.com/abridge")
	assertCatalogSource(t, companyByID, "binance", "lever", "https://jobs.lever.co/binance")
	assertCatalogSource(t, companyByID, "databricks", "greenhouse", "https://boards.greenhouse.io/databricks")
	assertCatalogSource(t, companyByID, "gemini", "greenhouse", "https://job-boards.greenhouse.io/gemini")
	assertCatalogSource(t, companyByID, "grab", "smartrecruiters", "https://careers.smartrecruiters.com/Grab")
	assertCatalogSource(t, companyByID, "nvidia", "workday", "https://nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite?q=software%20engineer%20intern")
	assertCatalogSource(t, companyByID, "oracle", "oracle_recruiting", "https://eeho.fa.us2.oraclecloud.com/hcmUI/CandidateExperience/en/sites/jobsearch?keyword=software%20engineer%20intern")
	assertCatalogSource(t, companyByID, "roblox", "greenhouse", "https://job-boards.greenhouse.io/roblox")
	assertCatalogSource(t, companyByID, "salesforce", "workday", "https://salesforce.wd12.myworkdayjobs.com/External_Career_Site?q=software%20engineer%20intern")
	assertCatalogSource(t, companyByID, "stripe", "greenhouse", "https://job-boards.greenhouse.io/stripe")

	seedFile := openConfigFile(t, "discovery-seed.json")
	seed, err := LoadDiscoverySeed(seedFile)
	if err != nil {
		t.Fatalf("LoadDiscoverySeed(real file) error = %v", err)
	}
	if err := seedFile.Close(); err != nil {
		t.Fatalf("close discovery seed: %v", err)
	}
	if got := len(seed.Candidates); got != 474 {
		t.Fatalf("discovery candidates = %d, want 474", got)
	}

	ai, quant := 0, 0
	candidateByID := make(map[string]DiscoveryCandidate, len(seed.Candidates))
	tiktokHeld := false
	for _, candidate := range seed.Candidates {
		candidateByID[candidate.ID] = candidate
		if _, routine := companyByID[candidate.ID]; routine {
			t.Fatalf("discovery candidate %q is already a verified routine company", candidate.ID)
		}
		if candidate.ID == "tiktok" {
			tiktokHeld = true
			if candidate.Website != "https://careers.tiktok.com" || !hasAllTags(candidate.Tags, "big-tech", "early-career", "source-needed") {
				t.Fatalf("TikTok discovery hold is incomplete: %#v", candidate)
			}
		}
		for _, tag := range candidate.Tags {
			switch tag {
			case "ai":
				ai++
			case "quant":
				quant++
			}
		}
	}
	if ai < 10 || quant < 10 {
		t.Fatalf("discovery coverage ai=%d quant=%d, want at least 10 each", ai, quant)
	}
	if !tiktokHeld {
		t.Fatal("TikTok must remain seed-only until a truthful routine adapter exists")
	}
	for _, id := range []string{"airbnb", "coinbase", "faire", "front", "posthog", "retool", "supabase"} {
		candidate, exists := candidateByID[id]
		if !exists || !hasAllTags(candidate.Tags, "yc-top") {
			t.Fatalf("top YC discovery candidate %q is missing or untagged: %#v", id, candidate)
		}
	}
	for _, id := range []string{"github", "hashicorp", "kla", "lam-research", "linkedin", "paypal", "samsung", "sandisk", "zscaler"} {
		candidate, exists := candidateByID[id]
		if !exists || !hasAllTags(candidate.Tags, "priority-1", "big-tech") {
			t.Fatalf("must-cover big-tech candidate %q is missing or mis-tiered: %#v", id, candidate)
		}
	}
	if missing := MissingDiscoveryCandidates(catalog, seed); len(missing) != len(seed.Candidates) {
		t.Fatalf("missing discovery candidates = %d, want all %d seed-only candidates", len(missing), len(seed.Candidates))
	}
	if got := len(catalog.RoutineSources()); got != 49 {
		t.Fatalf("discovery inspection changed routine sources: got %d, want 49", got)
	}
	coverage := AuditUniverse(catalog, seed)
	if !coverage.Pass {
		t.Fatalf("real company universe failed coverage audit: %+v", coverage)
	}
	for focus, minimum := range universeFocusMinimums {
		if coverage.CandidateFocus[focus] < minimum {
			t.Fatalf("career-bar focus %s=%d, want at least %d", focus, coverage.CandidateFocus[focus], minimum)
		}
	}
}

func hasAllTags(tags []string, required ...string) bool {
	present := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		present[tag] = struct{}{}
	}
	for _, tag := range required {
		if _, exists := present[tag]; !exists {
			return false
		}
	}
	return true
}

func openConfigFile(t *testing.T, name string) *os.File {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "config", name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	return file
}

func assertCatalogSource(t *testing.T, companies map[string]Company, id, provider, url string) {
	t.Helper()
	company, exists := companies[id]
	if !exists {
		t.Fatalf("verified catalog is missing %q", id)
	}
	if len(company.Sources) != 1 {
		t.Fatalf("%s sources = %d, want 1", id, len(company.Sources))
	}
	if source := company.Sources[0]; source.Provider != provider || source.URL != url {
		t.Fatalf("%s source = %#v, want provider=%q url=%q", id, source, provider, url)
	}
}
