package scraper

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

var (
	ErrNoJobs          = errors.New("extractor returned no jobs")
	ErrMissingTitle    = errors.New("job posting missing title")
	ErrMissingCompany  = errors.New("job posting missing company")
	ErrMissingApplyURL = errors.New("job posting missing apply url")
)

type Tier string

const (
	TierATS             Tier = "ats"
	TierStaticFetch     Tier = "static_fetch"
	TierSearchDiscovery Tier = "search_discovery"
	TierAIExtraction    Tier = "ai_extraction"
	TierBrowserAgent    Tier = "browser_agent"
)

type Source struct {
	ID       string
	Name     string
	URL      string
	Tier     Tier
	Metadata map[string]string
}

type Evidence struct {
	Field string
	Text  string
	URL   string
}

type JobPosting struct {
	SourceJobID    string
	Company        string
	Title          string
	Location       string
	Country        string
	EmploymentType string
	Level          string
	RoleFamily     string
	SourceURL      string
	ApplyURL       string
	PostedAt       *time.Time
	Live           bool
	Confidence     float64
	Strategy       Tier
	Evidence       []Evidence
}

type Result struct {
	Source      Source
	Jobs        []JobPosting
	RawEvidence []Evidence
	Confidence  float64
	Strategy    Tier
	Live        bool
	FetchedAt   time.Time
	Diagnostics map[string]string
}

type Extractor interface {
	Name() string
	Tier() Tier
	Extract(ctx context.Context, source Source) (Result, error)
}

var (
	whitespace          = regexp.MustCompile(`\s+`)
	aiTokenPattern      = regexp.MustCompile(`\b(ai|aiml|llm|ml)\b`)
	internLevelPattern  = regexp.MustCompile(`\bintern(?:ship)?s?\b|\bco-?op\b`)
	newGradLevelPattern = regexp.MustCompile(`\bnew(?:\s+|-)grad(?:uate)?s?\b|\bgraduates?\b`)
	earlyCareerPattern  = regexp.MustCompile(`\bentry(?:\s+|-)level\b|\bearly(?:\s+|-)career\b`)
)

func NormalizePosting(in JobPosting) (JobPosting, error) {
	out := in
	out.SourceJobID = normalizeSpace(out.SourceJobID)
	out.Company = normalizeSpace(out.Company)
	out.Title = normalizeSpace(out.Title)
	out.Location = normalizeSpace(out.Location)
	out.Country = normalizeCountry(normalizeSpace(out.Country), out.Location)
	out.EmploymentType = normalizeSpace(out.EmploymentType)
	out.Level = normalizeSpace(out.Level)
	out.RoleFamily = normalizeSpace(out.RoleFamily)
	out.SourceURL = strings.TrimSpace(out.SourceURL)
	out.ApplyURL = strings.TrimSpace(out.ApplyURL)
	out.Confidence = clampConfidence(out.Confidence)

	if out.Title == "" {
		return JobPosting{}, ErrMissingTitle
	}
	if out.Company == "" {
		return JobPosting{}, ErrMissingCompany
	}
	if out.ApplyURL == "" {
		return JobPosting{}, ErrMissingApplyURL
	}
	if out.Level == "" {
		out.Level = inferLevel(out.Title)
	}
	if out.RoleFamily == "" {
		out.RoleFamily = inferRoleFamily(out.Title)
	}

	for i := range out.Evidence {
		out.Evidence[i].Field = normalizeSpace(out.Evidence[i].Field)
		out.Evidence[i].Text = normalizeSpace(out.Evidence[i].Text)
		out.Evidence[i].URL = strings.TrimSpace(out.Evidence[i].URL)
	}

	return out, nil
}

func NormalizeResult(in Result) (Result, error) {
	out := in
	out.Source.ID = normalizeSpace(out.Source.ID)
	out.Source.Name = normalizeSpace(out.Source.Name)
	out.Source.URL = strings.TrimSpace(out.Source.URL)
	out.Confidence = clampConfidence(out.Confidence)

	if out.Strategy == "" {
		out.Strategy = out.Source.Tier
	}
	if out.FetchedAt.IsZero() {
		out.FetchedAt = time.Now().UTC()
	}

	if len(out.Jobs) == 0 {
		return Result{}, ErrNoJobs
	}

	jobs := make([]JobPosting, 0, len(out.Jobs))
	for _, job := range out.Jobs {
		if job.Strategy == "" {
			job.Strategy = out.Strategy
		}
		if job.SourceURL == "" {
			job.SourceURL = out.Source.URL
		}
		if job.Confidence == 0 {
			job.Confidence = out.Confidence
		}
		normalized, err := NormalizePosting(job)
		if err != nil {
			return Result{}, err
		}
		jobs = append(jobs, normalized)
	}
	out.Jobs = jobs
	return out, nil
}

func normalizeSpace(value string) string {
	return whitespace.ReplaceAllString(strings.TrimSpace(value), " ")
}

func clampConfidence(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

func inferLevel(title string) string {
	lower := strings.ToLower(title)
	switch {
	case internLevelPattern.MatchString(lower):
		return "internship"
	case newGradLevelPattern.MatchString(lower):
		return "new_grad"
	case earlyCareerPattern.MatchString(lower):
		return "early_career"
	default:
		return "unknown"
	}
}

func inferRoleFamily(title string) string {
	lower := strings.ToLower(title)
	switch {
	case strings.Contains(lower, "machine learning"), strings.Contains(lower, "artificial intelligence"), strings.Contains(lower, "ml_ai"), aiTokenPattern.MatchString(lower):
		return "ml_ai"
	case strings.Contains(lower, "data"):
		return "data"
	case strings.Contains(lower, "frontend"), strings.Contains(lower, "front-end"), strings.Contains(lower, "web"):
		return "frontend"
	case strings.Contains(lower, "backend"), strings.Contains(lower, "back-end"), strings.Contains(lower, "platform"):
		return "backend"
	case strings.Contains(lower, "infrastructure"), strings.Contains(lower, "infra"), strings.Contains(lower, "systems"):
		return "infrastructure"
	case strings.Contains(lower, "full stack"), strings.Contains(lower, "full-stack"):
		return "full_stack"
	default:
		return "software_engineering"
	}
}

func normalizeCountry(country, location string) string {
	if country != "" {
		return country
	}
	lower := strings.ToLower(location)
	switch {
	case strings.Contains(lower, "singapore"):
		return "Singapore"
	case strings.Contains(lower, "hong kong"):
		return "Hong Kong"
	case strings.Contains(lower, "london"), strings.Contains(lower, "united kingdom"), strings.Contains(lower, " uk"):
		return "UK"
	case strings.Contains(lower, "toronto"), strings.Contains(lower, "vancouver"), strings.Contains(lower, "canada"):
		return "Canada"
	case strings.Contains(lower, "united states"), strings.Contains(lower, "new york"), strings.Contains(lower, "seattle"), strings.Contains(lower, "san francisco"), strings.Contains(lower, "remote us"), strings.Contains(lower, "remote in us"):
		return "US"
	default:
		return "unknown"
	}
}
