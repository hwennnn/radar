package main

import (
	"testing"

	"github.com/hwennnn/radar/internal/core"
)

func TestMergeCompanyLogoDomainsUsesOfficialSeedWebsites(t *testing.T) {
	domains := mergeCompanyLogoDomains(nil, core.DiscoverySeed{Candidates: []core.DiscoveryCandidate{
		{Name: "Aquatic Capital Management", Website: "https://www.aquatic.com/careers"},
		{Name: "D. E. Shaw", Website: "https://www.deshaw.com"},
		{Name: "Missing Website"},
	}})

	for company, want := range map[string]string{
		"Aquatic Capital Management": "aquatic.com",
		"D. E. Shaw":                 "deshaw.com",
	} {
		if got := companyLogoDomain(company, domains); got != want {
			t.Fatalf("logo domain for %s = %q, want %q", company, got, want)
		}
	}
	if got := companyLogoDomain("Missing Website", domains); got != "" {
		t.Fatalf("company without an official website received domain %q", got)
	}
}

func TestVerifiedCompanyLogoDomainsCoverJobFacingAliases(t *testing.T) {
	domains := loadCompanyLogoDomains("testdata/does-not-exist.json")
	for company, want := range map[string]string{
		"ByteDance":          "bytedance.com",
		"Citadel Securities": "citadelsecurities.com",
		"Citadelsecurities":  "citadelsecurities.com",
		"TikTok":             "tiktok.com",
	} {
		if got := companyLogoDomain(company, domains); got != want {
			t.Fatalf("fallback logo domain for %s = %q, want %q", company, got, want)
		}
	}
}

func TestLogoDomainFromWebsiteRejectsNonHTTPURLs(t *testing.T) {
	for _, raw := range []string{"", "javascript:alert(1)", "//example.com", "not a URL"} {
		if got := logoDomainFromWebsite(raw); got != "" {
			t.Fatalf("unsafe website %q produced logo domain %q", raw, got)
		}
	}
}

func TestCompanyLogoDomainsCoverDiscoveryRegistryAndVerifiedCatalog(t *testing.T) {
	domains := loadCompanyLogoDomains("../../config/discovery-seed.json")
	if len(domains) < 450 {
		t.Fatalf("logo registry unexpectedly small: %d domains", len(domains))
	}
	catalog, err := loadCatalogFile("../../config/sources.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, company := range catalog.Companies {
		if companyLogoDomain(company.Name, domains) == "" {
			t.Errorf("verified company %q has no logo domain", company.Name)
		}
	}
}
