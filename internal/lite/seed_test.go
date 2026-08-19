package lite

import (
	"strings"
	"testing"
)

func TestMissingDiscoveryCandidatesExcludesVerifiedCompanies(t *testing.T) {
	catalog := Catalog{Companies: []Company{{ID: "openai", Name: "OpenAI", Sources: []Source{{ID: "openai-ashby", Provider: "ashby", URL: "https://jobs.ashbyhq.com/openai"}}}}}
	seed, err := LoadDiscoverySeed(strings.NewReader(`{"candidates":[
		{"id":"xai","name":"xAI","website":"https://x.ai","tags":["ai"]},
		{"id":"openai","name":"OpenAI","tags":["ai"]},
		{"id":"citadel-securities","name":"Citadel Securities","tags":["quant"]}
	]}`))
	if err != nil {
		t.Fatalf("LoadDiscoverySeed() error = %v", err)
	}
	missing := MissingDiscoveryCandidates(catalog, seed)
	if len(missing) != 2 || missing[0].ID != "citadel-securities" || missing[1].ID != "xai" {
		t.Fatalf("MissingDiscoveryCandidates() = %#v", missing)
	}
	if len(catalog.RoutineSources()) != 1 {
		t.Fatalf("discovery seed changed routine sources: %#v", catalog.RoutineSources())
	}
}

func TestLoadDiscoverySeedRejectsExecutableSourcesAndInvalidCandidates(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{"sources are not allowed", `{"candidates":[{"id":"xai","name":"xAI","sources":[{"url":"https://example.com"}]}]}`, "unknown field"},
		{"duplicate candidate", `{"candidates":[{"id":"xai","name":"xAI"},{"id":"xai","name":"X"}]}`, "duplicate discovery candidate id"},
		{"invalid website", `{"candidates":[{"id":"xai","name":"xAI","website":"x.ai"}]}`, "absolute http(s) URL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadDiscoverySeed(strings.NewReader(test.json))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadDiscoverySeed() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
