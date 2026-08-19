package core

import (
	"os"
	"strings"
	"testing"
)

func TestLoadCatalogAndRoutineSources(t *testing.T) {
	catalog, err := LoadCatalog(strings.NewReader(`{
		"companies": [
			{"id":"stripe","name":"Stripe","sources":[{"id":"stripe-greenhouse","provider":"greenhouse","url":"https://boards.greenhouse.io/stripe"}]},
			{"id":"anthropic","name":"Anthropic","sources":[{"id":"anthropic-ashby","provider":"ashby","url":"https://jobs.ashbyhq.com/anthropic"}]}
		]
	}`))
	if err != nil {
		t.Fatalf("LoadCatalog() error = %v", err)
	}
	sources := catalog.RoutineSources()
	if len(sources) != 2 || sources[0].ID != "anthropic-ashby" || sources[0].Company != "Anthropic" || sources[1].ID != "stripe-greenhouse" || sources[1].Company != "Stripe" {
		t.Fatalf("RoutineSources() = %#v", sources)
	}
}

func TestLoadCatalogRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{"unknown field", `{"companies":[],"automatic_discovery":true}`, "unknown field"},
		{"trailing value", `{"companies":[]} {}`, "trailing JSON"},
		{"bad company id", `{"companies":[{"id":"OpenAI","name":"OpenAI","sources":[]}]}`, "lowercase kebab-case"},
		{"duplicate company", `{"companies":[{"id":"openai","name":"OpenAI","sources":[{"id":"a","provider":"ashby","url":"https://example.com/a"}]},{"id":"openai","name":"Other","sources":[{"id":"b","provider":"ashby","url":"https://example.com/b"}]}]}`, "duplicate company id"},
		{"duplicate source globally", `{"companies":[{"id":"openai","name":"OpenAI","sources":[{"id":"jobs","provider":"ashby","url":"https://example.com/a"}]},{"id":"stripe","name":"Stripe","sources":[{"id":"jobs","provider":"greenhouse","url":"https://example.com/b"}]}]}`, "duplicate source id"},
		{"missing source", `{"companies":[{"id":"openai","name":"OpenAI","sources":[]}]}`, "at least one routine source"},
		{"unknown provider", `{"companies":[{"id":"openai","name":"OpenAI","sources":[{"id":"openai-jobs","provider":"made_up","url":"https://example.com/jobs"}]}]}`, "not supported for routine crawling"},
		{"static is not promoted", `{"companies":[{"id":"openai","name":"OpenAI","sources":[{"id":"openai-jobs","provider":"static","url":"https://example.com/jobs"}]}]}`, "not supported for routine crawling"},
		{"bad url", `{"companies":[{"id":"openai","name":"OpenAI","sources":[{"id":"openai-jobs","provider":"ashby","url":"javascript:alert(1)"}]}]}`, "absolute http(s) URL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadCatalog(strings.NewReader(test.json))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadCatalog() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestVerifiedCatalogUsesOnlyPromotedProviders(t *testing.T) {
	file, err := os.Open("../../config/sources.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })

	catalog, err := LoadCatalog(file)
	if err != nil {
		t.Fatalf("LoadCatalog(real catalog) error = %v", err)
	}
	sources := catalog.RoutineSources()
	if len(sources) != 49 {
		t.Fatalf("real catalog has %d routine sources, want 49", len(sources))
	}
	providers := make(map[string]struct{})
	for _, source := range sources {
		providers[source.Provider] = struct{}{}
	}
	if len(providers) != 18 {
		t.Fatalf("real catalog uses %d provider kinds, want 18", len(providers))
	}
	for provider := range providers {
		if _, exists := supportedProviders[provider]; !exists {
			t.Errorf("real catalog provider %q is not allowlisted", provider)
		}
	}
}
