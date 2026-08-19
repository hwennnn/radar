package provider

import (
	"context"
	"time"
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

type Posting struct {
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
	Jobs        []Posting
	RawEvidence []Evidence
	Confidence  float64
	Strategy    Tier
	Live        bool
	FetchedAt   time.Time
	Diagnostics map[string]string
}

type Engine interface {
	Name() string
	Extract(ctx context.Context, source Source) (Result, error)
}
