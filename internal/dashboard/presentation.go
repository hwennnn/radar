package dashboard

import (
	"sort"
	"strings"

	"github.com/hwennnn/radar/internal/pipeline"
)

type companyPresentation struct {
	Label string
	Rank  int
}

// CompanyPresentation describes the compact company label used by delivery
// and dashboard views.
type CompanyPresentation = companyPresentation

func LoadCompanyPresentations(seedPath string) map[string]CompanyPresentation {
	return loadCompanyPresentations(seedPath)
}

func CompanyPresentationLabel(company string, presentations map[string]CompanyPresentation) string {
	return companyPresentationLabel(company, presentations)
}

func PostingTrackLabel(posting pipeline.Posting) string {
	return postingTrackLabel(posting)
}

func PostingCategoryLabel(posting pipeline.Posting) string {
	return postingCategoryLabel(posting)
}

func PostingLocationMarker(country, location string) string {
	return postingLocationMarker(country, location)
}

var verifiedCompanyPresentations = map[string]companyPresentation{
	"abridge":                {Label: "🧠 AI company", Rank: 3},
	"adobe":                  {Label: "🏙 Big tech", Rank: 2},
	"akuna capital":          {Label: "📈 Quant / trading", Rank: 1},
	"amazon":                 {Label: "🏙 Big tech", Rank: 2},
	"anthropic":              {Label: "🧠 AI company", Rank: 3},
	"apple":                  {Label: "🏙 Big tech", Rank: 2},
	"belvedere trading":      {Label: "📈 Quant / trading", Rank: 1},
	"brex":                   {Label: "🚀 Startup / unicorn", Rank: 5},
	"bytedance":              {Label: "🏙 Big tech", Rank: 2},
	"citadel securities":     {Label: "📈 Quant / trading", Rank: 1},
	"cloudflare":             {Label: "🏙 Big tech", Rank: 2},
	"cohere":                 {Label: "🧠 AI company", Rank: 3},
	"databricks":             {Label: "🧠 AI company", Rank: 3},
	"drw":                    {Label: "📈 Quant / trading", Rank: 1},
	"figma":                  {Label: "🚀 Startup / unicorn", Rank: 5},
	"glean":                  {Label: "🧠 AI company", Rank: 3},
	"google":                 {Label: "🏙 Big tech", Rank: 2},
	"hudson river trading":   {Label: "📈 Quant / trading", Rank: 1},
	"ibm":                    {Label: "🏙 Big tech", Rank: 2},
	"imc":                    {Label: "📈 Quant / trading", Rank: 1},
	"jane street":            {Label: "📈 Quant / trading", Rank: 1},
	"jump trading":           {Label: "📈 Quant / trading", Rank: 1},
	"meta":                   {Label: "🏙 Big tech", Rank: 2},
	"microsoft":              {Label: "🏙 Big tech", Rank: 2},
	"netflix":                {Label: "🏙 Big tech", Rank: 2},
	"notion":                 {Label: "🚀 Startup / unicorn", Rank: 5},
	"nvidia":                 {Label: "🏙 Big tech", Rank: 2},
	"openai":                 {Label: "🧠 AI company", Rank: 3},
	"optiver":                {Label: "📈 Quant / trading", Rank: 1},
	"oracle":                 {Label: "🏙 Big tech", Rank: 2},
	"point72":                {Label: "📈 Quant / trading", Rank: 1},
	"ramp":                   {Label: "🚀 Startup / unicorn", Rank: 5},
	"replit":                 {Label: "🟠 YC company", Rank: 4},
	"rippling":               {Label: "🚀 Startup / unicorn", Rank: 5},
	"roblox":                 {Label: "🏙 Big tech", Rank: 2},
	"salesforce":             {Label: "🏙 Big tech", Rank: 2},
	"scale ai":               {Label: "🧠 AI company", Rank: 3},
	"servicenow":             {Label: "🏙 Big tech", Rank: 2},
	"stripe":                 {Label: "🚀 Startup / unicorn", Rank: 5},
	"tower research capital": {Label: "📈 Quant / trading", Rank: 1},
	"vercel":                 {Label: "🚀 Startup / unicorn", Rank: 5},
	"virtu financial":        {Label: "📈 Quant / trading", Rank: 1},
}

func loadCompanyPresentations(seedPath string) map[string]companyPresentation {
	presentations := make(map[string]companyPresentation, len(verifiedCompanyPresentations))
	for company, presentation := range verifiedCompanyPresentations {
		presentations[company] = presentation
	}
	seed, err := loadDiscoverySeedFile(seedPath)
	if err != nil {
		return presentations
	}
	for _, candidate := range seed.Candidates {
		presentation := presentationFromTags(candidate.Tags)
		if presentation.Label == "" {
			continue
		}
		key := presentationCompanyKey(candidate.Name)
		if existing, ok := presentations[key]; !ok || presentation.Rank < existing.Rank {
			presentations[key] = presentation
		}
	}
	return presentations
}

func presentationFromTags(tags []string) companyPresentation {
	set := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		set[strings.ToLower(strings.TrimSpace(tag))] = struct{}{}
	}
	for _, candidate := range []struct {
		tag          string
		presentation companyPresentation
	}{
		{"quant", companyPresentation{Label: "📈 Quant / trading", Rank: 1}},
		{"big-tech", companyPresentation{Label: "🏙 Big tech", Rank: 2}},
		{"ai", companyPresentation{Label: "🧠 AI company", Rank: 3}},
		{"yc-top", companyPresentation{Label: "🟠 YC company", Rank: 4}},
		{"unicorn", companyPresentation{Label: "🚀 Startup / unicorn", Rank: 5}},
	} {
		if _, ok := set[candidate.tag]; ok {
			return candidate.presentation
		}
	}
	return companyPresentation{}
}

func presentationCompanyKey(company string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(company))), " ")
}

func companyPresentationLabel(company string, presentations map[string]companyPresentation) string {
	if presentation, ok := presentations[presentationCompanyKey(company)]; ok {
		return presentation.Label
	}
	return "💻 Tech"
}

func postingTrackLabel(posting pipeline.Posting) string {
	if feedTrack(posting) == "internship" {
		return "Internship"
	}
	return "New grad"
}

func postingCategoryLabel(posting pipeline.Posting) string {
	switch feedCategory(posting.Title) {
	case "quant":
		return "Quant"
	case "ai_ml":
		return "AI / ML"
	case "data":
		return "Data"
	case "infra_security":
		return "Infra / security"
	default:
		return "Software"
	}
}

func postingLocationMarker(country, location string) string {
	countries := detectedCountries(country + "; " + location)
	if len(countries) > 1 {
		return "🌐"
	}
	if len(countries) == 1 {
		return countryFlag(countries[0])
	}
	return "📍"
}

func detectedCountries(value string) []string {
	normalized := normalizeFeedText(value)
	found := map[string]bool{}
	aliases := []struct {
		code    string
		phrases []string
	}{
		{"US", []string{"united states", "usa", "us", "new york", "san francisco", "san jose", "los angeles", "seattle", "austin", "chicago", "boston", "washington dc", "california", "texas"}},
		{"SG", []string{"singapore", "sg"}},
		{"GB", []string{"united kingdom", "uk", "gb", "london", "england", "scotland"}},
		{"CA", []string{"canada", "toronto", "vancouver", "montreal"}},
		{"HK", []string{"hong kong"}},
		{"AU", []string{"australia", "sydney", "melbourne"}},
		{"FR", []string{"france", "paris"}},
		{"DE", []string{"germany", "berlin", "munich"}},
		{"IE", []string{"ireland", "dublin"}},
		{"NL", []string{"netherlands", "amsterdam"}},
		{"CH", []string{"switzerland", "zurich"}},
		{"IN", []string{"india", "bengaluru", "bangalore", "hyderabad"}},
		{"JP", []string{"japan", "tokyo"}},
	}
	for _, alias := range aliases {
		for _, phrase := range alias.phrases {
			if feedHasPhrase(normalized, phrase) {
				found[alias.code] = true
				break
			}
		}
	}
	codes := make([]string, 0, len(found))
	for code := range found {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
}

func countryFlag(code string) string {
	if len(code) != 2 {
		return "📍"
	}
	runes := []rune(strings.ToUpper(code))
	return string([]rune{0x1F1E6 + runes[0] - 'A', 0x1F1E6 + runes[1] - 'A'})
}
