package main

import (
	"testing"

	"github.com/hwennnn/radar/internal/core"
)

func TestCompanyPresentationsUseSeedTagsAndVerifiedFallbacks(t *testing.T) {
	presentations := loadCompanyPresentations("../../config/discovery-seed.json")
	for company, want := range map[string]string{
		"Aquatic Capital Management": "📈 Quant / trading",
		"Cloudflare":                 "🏙 Big tech",
		"Stripe":                     "🚀 Startup / unicorn",
		"OpenAI":                     "🧠 AI company",
		"Replit":                     "🟠 YC company",
		"Unknown Systems":            "💻 Tech",
	} {
		if got := companyPresentationLabel(company, presentations); got != want {
			t.Fatalf("companyPresentationLabel(%q) = %q, want %q", company, got, want)
		}
	}
}

func TestPostingLocationMarker(t *testing.T) {
	for _, test := range []struct {
		name     string
		country  string
		location string
		want     string
	}{
		{name: "single country", country: "United States", location: "Austin, TX", want: "🇺🇸"},
		{name: "singapore", country: "Singapore", location: "Singapore", want: "🇸🇬"},
		{name: "country codes", country: "US; UK", location: "Remote", want: "🌐"},
		{name: "multi country", country: "", location: "Chicago; London", want: "🌐"},
		{name: "unknown", country: "", location: "Remote", want: "📍"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := postingLocationMarker(test.country, test.location); got != test.want {
				t.Fatalf("postingLocationMarker(%q, %q) = %q, want %q", test.country, test.location, got, test.want)
			}
		})
	}
}

func TestPostingPresentationLabels(t *testing.T) {
	posting := core.Posting{
		Title:          "Machine Learning Platform Engineer Intern",
		EmploymentType: "Internship",
	}
	if got := postingTrackLabel(posting); got != "Internship" {
		t.Fatalf("postingTrackLabel() = %q", got)
	}
	if got := postingCategoryLabel(posting); got != "AI / ML" {
		t.Fatalf("postingCategoryLabel() = %q", got)
	}
}
