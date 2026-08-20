package pipeline

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxExplicitPostingAge = 180 * 24 * time.Hour

// blockedCompanies is deliberately small and deterministic. These employers
// are either explicitly outside the user's target set or primarily sell into
// defense/government markets. Sector-specific roles at otherwise mixed
// companies continue to be handled by blockedSectorPhrases below.
var blockedCompanies = []string{
	"tencent", "wechat",
	"anduril", "lockheed martin", "northrop grumman", "raytheon", "rtx",
	"general dynamics", "l3harris", "leidos", "bae systems",
}

var blockedSectorPhrases = []string{
	"government", "public sector", "defense", "defence", "federal", "usg",
}

var rejectedRolePhrases = []string{
	"customer support", "technical support", "help desk", "it support", "it operations",
	"sales", "account executive", "business development", "business analyst",
	"business operations", "people operations", "sales operations", "recruiting operations",
	"operations analyst", "operations manager", "operations specialist",
	"trading operations engineer",
	"program manager", "product manager", "project manager",
	"engineering manager", "manager", "director", "head", "vice president", "vp",
	"senior", "sr", "staff", "principal", "lead engineer",
	"qa", "test", "testing", "qa engineer", "quality assurance", "quality engineer",
	"sdet", "engineer in test", "test engineer", "software test", "test automation", "automation test",
	"recruiter", "talent acquisition", "educator", "instructor", "teacher",
	"cybersecurity analyst", "security analyst", "fundamental research analyst", "market data specialist",
	"hardware", "fpga", "asic", "electrical engineer", "mechanical engineer", "manufacturing engineer",
}

var rejectedEventPhrases = []string{
	"challenge", "competition", "hackathon", "talent community",
}

var rejectedEditorialTitlePhrases = []string{
	"your guide to", "career guide", "internship guide", "how to get an internship", "internship tips",
}

var rejectedEditorialURLSegments = []string{
	"blog", "article", "articles", "resource", "resources", "guide", "guides", "news", "events",
}

var acceptedTimingPhrases = []string{
	"intern", "internship", "new grad", "new graduate", "recent graduate",
	"university graduate", "graduate", "early career", "entry level",
	"college grad", "co op", "working student", "campus", "junior",
}

var acceptedRolePhrases = []string{
	"software engineer", "software developer", "software engineering", "swe", "software development", "software intern",
	"graduate developer", "application developer", "kernel engineer",
	"backend engineer", "backend engineering", "backend developer",
	"frontend engineer", "frontend engineering", "frontend developer",
	"front end engineer", "front end engineering", "front end developer", "full stack", "fullstack",
	"platform engineer", "platform engineering", "infrastructure engineer", "infrastructure engineering",
	"site reliability engineer", "production engineer", "systems engineer", "systems engineering", "linux engineer", "network engineer",
	"operations engineer", "devops engineer", "cloud engineer",
	"security engineer", "security researcher", "cybersecurity", "application security", "information security",
	"data engineer", "data engineers", "data engineering", "data scientist", "data science",
	"machine learning", "ml engineer", "ml researcher", "artificial intelligence",
	"ai engineer", "ai researcher", "ai research", "ai builder", "applied scientist", "research engineer", "model shaping",
	"privacy and civil liberties engineer", "privacy civil liberties engineer",
	"member of technical staff", "mts", "coding llm", "coding llms", "llm engineer",
	"algorithm developer", "algorithm development", "trading systems engineer",
	"quantitative developer", "quantitative development", "quantitative researcher", "quantitative research", "quantitative technologist",
	"quantitative analyst", "quantitative engineer", "quantitative trader", "quantitative trading", "quantitative strategist",
	"quant developer", "quant researcher", "quant engineer", "quant trader", "quant trading",
}

var (
	usStateCodePattern    = regexp.MustCompile(`(?:^|[,;/][ ]*)(AL|AK|AZ|AR|CA|CO|CT|DE|FL|GA|HI|ID|IL|IA|KS|KY|LA|ME|MD|MA|MI|MN|MS|MO|MT|NE|NV|NH|NJ|NM|NY|NC|ND|OH|OK|OR|PA|RI|SC|SD|TN|TX|UT|VT|VA|WA|WV|WI|WY|DC)(?:$|[,;/])`)
	yearPattern           = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)[0-9]{2})(?:$|[^0-9])`)
	targetLocationPhrases = []string{
		"singapore", "united states", "united states of america", "usa", "us",
		"new york", "new york city", "nyc", "chicago", "san francisco", "sf office",
		"seattle", "san jose", "san diego", "palo alto", "mountain view", "redmond",
		"denver", "honolulu", "austin", "foster city", "los gatos", "santa clara",
		"washington dc", "washington d c", "bellevue",
	}
	nonTargetTitleLocations = []string{
		"france", "poland", "germany", "berlin", "paris", "united kingdom", "london",
		"india", "australia", "hong kong", "china", "japan", "south korea", "canada",
	}
)

// Eligible is the single-user deterministic publish boundary. A posting must
// be an explicitly early-career technical role in Singapore or the US. Missing
// or ambiguous geography and timing fail closed.
func Eligible(posting Posting) bool {
	return EligibleAt(posting, time.Now().UTC())
}

// EligibleAt keeps the publish boundary deterministic for evaluation while
// allowing the production caller to reject explicitly stale seasonal roles.
func EligibleAt(posting Posting, now time.Time) bool {
	if BlockedCompany(posting.Company) {
		return false
	}
	title := normalizedText(posting.Title)
	if hasAnyPhrase(title, []string{"tencent", "wechat"}) {
		return false
	}
	if titleHasOnlyNonTargetGeography(title) || staleExplicitTiming(title, now) || stalePostedAt(posting.PostedAt, now) {
		return false
	}
	if hasAnyPhrase(title, rejectedEditorialTitlePhrases) || editorialApplyURL(posting.ApplyURL) {
		return false
	}
	if !actionableGeography(posting) {
		return false
	}

	if hasAnyPhrase(title, blockedSectorPhrases) || hasAnyPhrase(title, rejectedEventPhrases) {
		return false
	}
	if hasPhrase(normalizedText(posting.Company), "palantir") && hasPhrase(title, "intel") {
		return false
	}

	memberOfTechnicalStaff := hasPhrase(title, "member of technical staff")
	for _, rejected := range rejectedRolePhrases {
		if rejected == "staff" && memberOfTechnicalStaff {
			continue
		}
		if hasPhrase(title, rejected) {
			return false
		}
	}

	// Analyst titles are outside the software-focused boundary, except for the
	// deliberately supported quantitative-trading early-career path.
	if hasPhrase(title, "analyst") && !hasAnyPhrase(title, []string{"quantitative analyst", "quantitative trading analyst", "quant trading analyst"}) {
		return false
	}

	aiResearchScientist := hasPhrase(title, "research scientist") && hasAnyPhrase(title, []string{
		"ai", "genai", "ml", "machine learning", "llm", "nlp", "computer vision", "applied vision", "deep learning",
	})
	aiMLResearchOrEngineering := hasAnyPhrase(title, []string{
		"ai", "genai", "ml", "machine learning", "artificial intelligence", "nlp", "computer vision", "applied vision", "deep learning",
	}) && hasAnyPhrase(title, []string{"research", "engineer", "engineering", "scientist"})
	if !hasAnyPhrase(title, acceptedRolePhrases) && !aiResearchScientist && !aiMLResearchOrEngineering {
		return false
	}

	titleHasTiming := hasAnyPhrase(title, acceptedTimingPhrases)
	employment := normalizedText(posting.EmploymentType)
	if hasPhrase(employment, "experienced") && !titleHasTiming {
		return false
	}
	level := normalizedText(posting.Level)
	return titleHasTiming || hasAnyPhrase(employment, acceptedTimingPhrases) || hasAnyPhrase(level, acceptedTimingPhrases)
}

func editorialApplyURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return false
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	for _, segment := range segments {
		segment, err = url.PathUnescape(segment)
		if err != nil {
			continue
		}
		segment = strings.ToLower(strings.TrimSpace(segment))
		for _, rejected := range rejectedEditorialURLSegments {
			if segment == rejected {
				return true
			}
		}
	}
	return false
}

func BlockedCompany(company string) bool {
	return hasAnyPhrase(normalizedText(company), blockedCompanies)
}

func actionableGeography(posting Posting) bool {
	country := normalizedText(posting.Country)
	location := normalizedText(posting.Location)
	targetCountry := hasAnyPhrase(country, []string{"singapore", "sg", "united states", "united states of america", "usa", "us"})
	targetLocation := hasAnyPhrase(location, targetLocationPhrases) || usStateCodePattern.MatchString(strings.TrimSpace(posting.Location))
	// Location is normally more specific than country. Fail closed when the two
	// fields conflict instead of letting a broad or malformed US country value
	// override an explicit foreign office.
	if hasAnyPhrase(location, nonTargetTitleLocations) && !targetLocation {
		return false
	}
	if targetCountry || targetLocation {
		return true
	}
	// Some ATS boards collapse a specific title location to "In-Office". A
	// target city/state in the title is stronger evidence than that generic
	// placeholder, but never overrides an explicit foreign title location.
	title := normalizedText(posting.Title)
	if !genericLocation(location) {
		return false
	}
	return hasAnyPhrase(title, targetLocationPhrases) || usStateCodePattern.MatchString(strings.TrimSpace(posting.Title))
}

func genericLocation(location string) bool {
	switch strings.TrimSpace(location) {
	case "", "in office", "remote", "hybrid", "unknown", "multiple locations", "various locations":
		return true
	default:
		return false
	}
}

func titleHasOnlyNonTargetGeography(title string) bool {
	return hasAnyPhrase(title, nonTargetTitleLocations) && !hasAnyPhrase(title, targetLocationPhrases)
}

func staleExplicitTiming(title string, now time.Time) bool {
	if now.IsZero() {
		return false
	}
	matches := yearPattern.FindAllStringSubmatch(title, -1)
	for _, match := range matches {
		year, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		if year < now.Year() {
			return true
		}
		if year != now.Year() {
			continue
		}
		if hasPhrase(title, "spring") && now.Month() >= time.June {
			return true
		}
		if hasPhrase(title, "summer") && now.Month() >= time.August {
			return true
		}
	}
	return false
}

func stalePostedAt(postedAt *time.Time, now time.Time) bool {
	if postedAt == nil || postedAt.IsZero() || now.IsZero() {
		return false
	}
	return postedAt.UTC().Before(now.UTC().Add(-maxExplicitPostingAge))
}

func normalizedText(value string) string {
	var builder strings.Builder
	builder.Grow(len(value) + 2)
	builder.WriteByte(' ')
	wasSpace := true
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			wasSpace = false
		} else if !wasSpace {
			builder.WriteByte(' ')
			wasSpace = true
		}
	}
	if !wasSpace {
		builder.WriteByte(' ')
	}
	return builder.String()
}

func hasPhrase(normalized, phrase string) bool {
	return strings.Contains(normalized, normalizedText(phrase))
}

func hasAnyPhrase(normalized string, phrases []string) bool {
	for _, phrase := range phrases {
		if hasPhrase(normalized, phrase) {
			return true
		}
	}
	return false
}
