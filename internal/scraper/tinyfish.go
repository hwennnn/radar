package scraper

import (
	"net/url"
	"regexp"
	"strings"
)

// companyFromURL and stableStringID predate the TinyFish package split and are
// also used by the parent package's ATS and static extractors.
func companyFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	host = strings.TrimPrefix(host, "jobs.")
	host = strings.TrimPrefix(host, "careers.")
	if host == "" {
		return ""
	}
	parts := strings.Split(host, ".")
	if len(parts) == 0 {
		return ""
	}
	return titleWords(strings.ReplaceAll(parts[0], "-", " "))
}

func stableStringID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "tinyfish-result"
	}
	if len(value) > 96 {
		return value[:96]
	}
	return value
}

func titleWords(value string) string {
	words := strings.Fields(value)
	for i, word := range words {
		if word == "" {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}
