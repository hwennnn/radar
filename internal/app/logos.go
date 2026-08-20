package app

import (
	"net/url"
	"strings"

	"github.com/hwennnn/radar/internal/pipeline"
)

// verifiedCompanyLogoDomains covers monitored companies whose public brand
// domain is not represented by the discovery seed or whose job-facing name is
// an alias of the seed name. Seed websites fill the rest of the registry.
var verifiedCompanyLogoDomains = map[string]string{
	"abridge":                "abridge.com",
	"adobe":                  "adobe.com",
	"akuna capital":          "akunacapital.com",
	"amazon":                 "amazon.com",
	"anthropic":              "anthropic.com",
	"apple":                  "apple.com",
	"belvedere trading":      "belvederetrading.com",
	"binance":                "binance.com",
	"brex":                   "brex.com",
	"bytedance":              "bytedance.com",
	"citadel securities":     "citadelsecurities.com",
	"citadelsecurities":      "citadelsecurities.com",
	"cloudflare":             "cloudflare.com",
	"cohere":                 "cohere.com",
	"confluent":              "confluent.io",
	"databricks":             "databricks.com",
	"drw":                    "drw.com",
	"figma":                  "figma.com",
	"gemini":                 "gemini.com",
	"glean":                  "glean.com",
	"google":                 "google.com",
	"grab":                   "grab.com",
	"hudson river trading":   "hudsonrivertrading.com",
	"ibm":                    "ibm.com",
	"imc":                    "imc.com",
	"jane street":            "janestreet.com",
	"jump trading":           "jumptrading.com",
	"meta":                   "meta.com",
	"microsoft":              "microsoft.com",
	"netflix":                "netflix.com",
	"notion":                 "notion.so",
	"nvidia":                 "nvidia.com",
	"openai":                 "openai.com",
	"optiver":                "optiver.com",
	"oracle":                 "oracle.com",
	"palantir":               "palantir.com",
	"point72":                "point72.com",
	"ramp":                   "ramp.com",
	"replit":                 "replit.com",
	"rippling":               "rippling.com",
	"roblox":                 "roblox.com",
	"salesforce":             "salesforce.com",
	"scale ai":               "scale.com",
	"sentry":                 "sentry.io",
	"servicenow":             "servicenow.com",
	"snowflake":              "snowflake.com",
	"stripe":                 "stripe.com",
	"tiktok":                 "tiktok.com",
	"tower research capital": "tower-research.com",
	"vercel":                 "vercel.com",
	"virtu financial":        "virtu.com",
}

func loadCompanyLogoDomains(seedPath string) map[string]string {
	domains := make(map[string]string, len(verifiedCompanyLogoDomains))
	for company, domain := range verifiedCompanyLogoDomains {
		domains[normalizeFeedCompany(company)] = domain
	}
	seed, err := loadDiscoverySeedFile(seedPath)
	if err != nil {
		return domains
	}
	return mergeCompanyLogoDomains(domains, seed)
}

func mergeCompanyLogoDomains(domains map[string]string, seed pipeline.DiscoverySeed) map[string]string {
	if domains == nil {
		domains = make(map[string]string)
	}
	for _, candidate := range seed.Candidates {
		domain := logoDomainFromWebsite(candidate.Website)
		if domain == "" {
			continue
		}
		domains[normalizeFeedCompany(candidate.Name)] = domain
	}
	return domains
}

func logoDomainFromWebsite(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	host = strings.TrimPrefix(host, "www.")
	if host == "" || strings.ContainsAny(host, " /\\") {
		return ""
	}
	return host
}

func companyLogoDomain(company string, domains map[string]string) string {
	return domains[normalizeFeedCompany(company)]
}
