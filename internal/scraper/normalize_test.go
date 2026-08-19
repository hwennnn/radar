package scraper

import (
	"context"
	"errors"
	"testing"
)

func TestNormalizePostingInfersFields(t *testing.T) {
	job, err := NormalizePosting(JobPosting{
		Company:    "  Quanta   Ledger ",
		Title:      " New Grad Software Engineer, Trading Infrastructure ",
		Location:   "London, United Kingdom",
		ApplyURL:   " https://example.com/apply ",
		Confidence: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	if job.Company != "Quanta Ledger" {
		t.Fatalf("company = %q", job.Company)
	}
	if job.Level != "new_grad" {
		t.Fatalf("level = %q, want new_grad", job.Level)
	}
	if job.RoleFamily != "infrastructure" {
		t.Fatalf("role family = %q, want infrastructure", job.RoleFamily)
	}
	if job.Country != "UK" {
		t.Fatalf("country = %q, want UK", job.Country)
	}
	if job.Confidence != 1 {
		t.Fatalf("confidence = %v, want clamp to 1", job.Confidence)
	}
}

func TestNormalizePostingLevelInferenceUsesTimingWordBoundaries(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{title: "International Software Engineer", want: "unknown"},
		{title: "Internal Tools Engineer", want: "unknown"},
		{title: "Internals Software Engineer", want: "unknown"},
		{title: "Cooperative Systems Engineer", want: "unknown"},
		{title: "Software Engineer Intern", want: "internship"},
		{title: "Software Engineering Internship", want: "internship"},
		{title: "Software Engineer Co-op", want: "internship"},
		{title: "Software Engineer Coop", want: "internship"},
		{title: "New Grad Software Engineer", want: "new_grad"},
		{title: "Graduate Software Engineer", want: "new_grad"},
		{title: "Entry-Level Software Engineer", want: "early_career"},
		{title: "Early-Career Software Engineer", want: "early_career"},
	}

	for _, test := range tests {
		t.Run(test.title, func(t *testing.T) {
			job, err := NormalizePosting(JobPosting{
				Company:  "Acme",
				Title:    test.title,
				ApplyURL: "https://example.com/apply",
			})
			if err != nil {
				t.Fatal(err)
			}
			if job.Level != test.want {
				t.Fatalf("level = %q, want %q", job.Level, test.want)
			}
		})
	}
}

func TestNormalizePostingRequiresApplyURL(t *testing.T) {
	_, err := NormalizePosting(JobPosting{
		Company: "Nimbus Systems",
		Title:   "Software Engineering Intern",
	})
	if !errors.Is(err, ErrMissingApplyURL) {
		t.Fatalf("err = %v, want ErrMissingApplyURL", err)
	}
}

func TestStaticExtractorReturnsNormalizedSampleJobs(t *testing.T) {
	extractor := NewStaticExtractor()
	result, err := extractor.Extract(context.Background(), SampleSources()[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Jobs) != 3 {
		t.Fatalf("jobs = %d, want 3", len(result.Jobs))
	}
	for _, job := range result.Jobs {
		if job.Company == "" || job.Title == "" || job.ApplyURL == "" {
			t.Fatalf("job was not normalized: %+v", job)
		}
		if job.Strategy != TierStaticFetch {
			t.Fatalf("strategy = %q, want %q", job.Strategy, TierStaticFetch)
		}
	}
}
