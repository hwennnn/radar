package core

import (
	"fmt"
	"sort"
	"strings"
)

// UniverseCoverage is the machine-readable release contract for Radar Lite's
// company discovery breadth. It deliberately measures the inert research
// queue as well as verified sources: a company can be covered before its board
// is healthy enough to enter routine crawling.
type UniverseCoverage struct {
	Pass                bool           `json:"pass"`
	VerifiedCompanies   int            `json:"verified_companies"`
	DiscoveryCandidates int            `json:"discovery_candidates"`
	TotalCompanies      int            `json:"total_companies"`
	RoutineSources      int            `json:"routine_sources"`
	CandidateCategories map[string]int `json:"candidate_categories"`
	CandidatePriorities map[string]int `json:"candidate_priorities"`
	CandidateProvenance map[string]int `json:"candidate_provenance"`
	CandidateFocus      map[string]int `json:"candidate_focus"`
	CandidatesWithSites int            `json:"candidates_with_websites"`
	CriticalMissing     []string       `json:"critical_missing,omitempty"`
	Errors              []string       `json:"errors,omitempty"`
}

var universeFocusMinimums = map[string]int{
	"yc-top":                  25,
	"speedyapply-high-signal": 95,
	"priority-1-quant":        38,
	"priority-1-big-tech":     100,
	"priority-1-unicorn":      110,
}

var universeMinimums = map[string]int{
	"total":           520,
	"candidates":      470,
	"ai":              100,
	"big-tech":        130,
	"unicorn":         160,
	"quant":           85,
	"developer-tools": 110,
	"security":        45,
	"semiconductor":   35,
	"fintech":         50,
}

var universeSegments = map[string]struct{}{
	"ai": {}, "big-tech": {}, "data": {}, "developer-tools": {},
	"engineering-heavy": {}, "fintech": {}, "quant": {}, "security": {},
	"semiconductor": {}, "unicorn": {},
}

var universeCriticalCompanies = []string{
	"abnormal-security", "airbnb", "amazon", "anaplan", "anthropic", "apple", "apptronik", "asml", "atlassian",
	"block", "bloomberg", "bosch", "bridgewater-associates", "canva", "cato-networks", "cboe-global-markets", "ciena", "citadel", "citadel-securities", "cloudflare", "cohere", "cohesity", "coinbase", "composio", "coreweave",
	"crowdstrike", "cursor", "d-e-shaw", "databricks", "datadog",
	"deepgram", "deepmind", "digitalocean", "discord", "doordash", "draftkings", "drw", "duolingo", "dv-trading", "epic-games", "esri", "factset", "faire", "fidelity-investments", "figma", "five-rings", "front", "g-research", "ge-healthcare", "general-motors", "github",
	"google", "hashicorp", "hudson-river-trading", "imc", "instacart", "jane-street", "jump-trading", "kla", "lam-research", "linkedin", "lyft",
	"intel", "ixl-learning", "mastercard", "meta", "mercor", "microchip-technology", "microsoft", "mistral-ai", "netflix", "notion", "nvidia", "nxp-semiconductors", "openai", "opswat",
	"optiver", "paypal", "perplexity", "philips", "pinterest", "point72", "quantbot-technologies", "ramp", "reddit", "red-hat", "redwood-materials", "remitly", "retell-ai", "rivian", "roblox", "samsung", "sandisk", "scale-ai", "simplisafe", "snap", "snowflake", "spacex", "spotify", "state-street", "stevens-capital-management", "symbotic",
	"posthog", "pulumi", "qualcomm", "qube-research-technologies", "redpanda", "renaissance-technologies", "retool",
	"sap", "squarepoint-capital", "stripe", "susquehanna", "tailscale", "temporal", "tiktok", "together-ai",
	"thumbtack", "tower-research-capital", "tsmc", "twitch", "two-sigma", "uber", "uniswap-labs", "unity", "vast-data", "vercel", "verkada", "virtu-financial",
	"weights-biases", "weride-ai", "whoop", "workday", "xai", "xtx-markets", "zoox", "zscaler",
}

var universeBlockedCompanies = map[string]struct{}{
	"anduril": {}, "helsing": {}, "simplifyjobs-2026": {},
	"simplifyjobs-2027": {}, "simplifyjobs-new-grad": {}, "tencent": {},
	"wechat": {}, "vanshb03": {}, "vanshb03-new-grad": {},
	"rethinkjobs-summer-2026": {}, "zshah101-summer-2027-fall-2026": {},
}

// AuditUniverse rejects shallow or unauditable company lists before a release.
// It has no network or persistence side effects and is safe for every agent and
// CI job to run.
func AuditUniverse(catalog Catalog, seed DiscoverySeed) UniverseCoverage {
	report := UniverseCoverage{
		VerifiedCompanies:   len(catalog.Companies),
		DiscoveryCandidates: len(seed.Candidates),
		RoutineSources:      len(catalog.RoutineSources()),
		CandidateCategories: make(map[string]int),
		CandidatePriorities: make(map[string]int),
		CandidateProvenance: make(map[string]int),
		CandidateFocus:      make(map[string]int),
	}

	companyIDs := make(map[string]struct{}, len(catalog.Companies)+len(seed.Candidates))
	for _, company := range catalog.Companies {
		companyIDs[company.ID] = struct{}{}
	}
	for _, candidate := range seed.Candidates {
		if _, exists := companyIDs[candidate.ID]; exists {
			report.Errors = append(report.Errors, fmt.Sprintf("company %q exists in both verified catalog and discovery seed", candidate.ID))
		}
		companyIDs[candidate.ID] = struct{}{}
		if strings.TrimSpace(candidate.Website) != "" {
			report.CandidatesWithSites++
		}

		priorityCount, segmentCount, provenanceCount := 0, 0, 0
		hasPriorityOne := false
		for _, tag := range candidate.Tags {
			switch tag {
			case "priority-1", "priority-2", "priority-3":
				report.CandidatePriorities[tag]++
				priorityCount++
			}
			if tag == "priority-1" {
				hasPriorityOne = true
			}
			if tag == "yc-top" {
				report.CandidateFocus[tag]++
			}
			if tag == "benchmark-speedyapply-2027" {
				report.CandidateFocus["speedyapply-high-signal"]++
			}
			if _, segment := universeSegments[tag]; segment {
				report.CandidateCategories[tag]++
				segmentCount++
			}
			if universeProvenanceTag(tag) {
				report.CandidateProvenance[tag]++
				provenanceCount++
			}
		}
		if priorityCount != 1 {
			report.Errors = append(report.Errors, fmt.Sprintf("candidate %q must have exactly one priority tag", candidate.ID))
		}
		if segmentCount == 0 {
			report.Errors = append(report.Errors, fmt.Sprintf("candidate %q has no target-bar category tag", candidate.ID))
		}
		if provenanceCount == 0 {
			report.Errors = append(report.Errors, fmt.Sprintf("candidate %q has no research provenance tag", candidate.ID))
		}
		if _, blocked := universeBlockedCompanies[candidate.ID]; blocked {
			report.Errors = append(report.Errors, fmt.Sprintf("blocked company/feed %q is present", candidate.ID))
		}
		if hasPriorityOne {
			for _, segment := range []string{"quant", "big-tech", "unicorn"} {
				if candidateHasTag(candidate.Tags, segment) {
					report.CandidateFocus["priority-1-"+segment]++
				}
			}
		}
	}

	report.TotalCompanies = len(companyIDs)
	for _, id := range universeCriticalCompanies {
		if _, exists := companyIDs[id]; !exists {
			report.CriticalMissing = append(report.CriticalMissing, id)
		}
	}
	if len(report.CriticalMissing) > 0 {
		report.Errors = append(report.Errors, fmt.Sprintf("%d critical companies are missing", len(report.CriticalMissing)))
	}
	if report.TotalCompanies < universeMinimums["total"] {
		report.Errors = append(report.Errors, fmt.Sprintf("total companies %d < %d", report.TotalCompanies, universeMinimums["total"]))
	}
	if report.DiscoveryCandidates < universeMinimums["candidates"] {
		report.Errors = append(report.Errors, fmt.Sprintf("discovery candidates %d < %d", report.DiscoveryCandidates, universeMinimums["candidates"]))
	}
	for _, category := range []string{"ai", "big-tech", "unicorn", "quant", "developer-tools", "security", "semiconductor", "fintech"} {
		if report.CandidateCategories[category] < universeMinimums[category] {
			report.Errors = append(report.Errors, fmt.Sprintf("%s candidates %d < %d", category, report.CandidateCategories[category], universeMinimums[category]))
		}
	}
	for _, focus := range []string{"yc-top", "speedyapply-high-signal", "priority-1-quant", "priority-1-big-tech", "priority-1-unicorn"} {
		if report.CandidateFocus[focus] < universeFocusMinimums[focus] {
			report.Errors = append(report.Errors, fmt.Sprintf("%s focus candidates %d < %d", focus, report.CandidateFocus[focus], universeFocusMinimums[focus]))
		}
	}
	sort.Strings(report.CriticalMissing)
	sort.Strings(report.Errors)
	report.Pass = len(report.Errors) == 0
	return report
}

func candidateHasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

func universeProvenanceTag(tag string) bool {
	for _, prefix := range []string{"benchmark-", "curated-", "forbes-", "futuriom-", "futuriom50-", "nasdaq-", "pwc-", "quant-benchmark-", "yc-"} {
		if strings.HasPrefix(tag, prefix) {
			return true
		}
	}
	return false
}
