package core

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// DiscoverySeed is the company-level research queue. Candidates deliberately
// do not contain executable sources: the reconciler must resolve and validate
// a structured board before it can enter routine monitoring.
type DiscoverySeed struct {
	Candidates []DiscoveryCandidate `json:"candidates"`
}

type DiscoveryCandidate struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Website string   `json:"website,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

func LoadDiscoverySeed(r io.Reader) (DiscoverySeed, error) {
	var seed DiscoverySeed
	if err := decodeStrictJSON(r, &seed); err != nil {
		return DiscoverySeed{}, fmt.Errorf("decode discovery seed: %w", err)
	}
	if err := seed.Validate(); err != nil {
		return DiscoverySeed{}, err
	}
	return seed, nil
}

func (s DiscoverySeed) Validate() error {
	ids := make(map[string]struct{}, len(s.Candidates))
	for i, candidate := range s.Candidates {
		where := fmt.Sprintf("candidates[%d]", i)
		if err := validCatalogID(candidate.ID); err != nil {
			return fmt.Errorf("%s.id: %w", where, err)
		}
		if _, exists := ids[candidate.ID]; exists {
			return fmt.Errorf("duplicate discovery candidate id %q", candidate.ID)
		}
		ids[candidate.ID] = struct{}{}
		if strings.TrimSpace(candidate.Name) == "" {
			return fmt.Errorf("%s.name is required", where)
		}
		if candidate.Website != "" {
			if err := validHTTPURL(candidate.Website); err != nil {
				return fmt.Errorf("%s.website: %w", where, err)
			}
		}
		tags := make(map[string]struct{}, len(candidate.Tags))
		for j, tag := range candidate.Tags {
			if err := validCatalogID(tag); err != nil {
				return fmt.Errorf("%s.tags[%d]: %w", where, j, err)
			}
			if _, exists := tags[tag]; exists {
				return fmt.Errorf("%s.tags contains duplicate %q", where, tag)
			}
			tags[tag] = struct{}{}
		}
	}
	return nil
}

// MissingDiscoveryCandidates returns seed companies that do not already have a
// verified catalog entry. It has no side effects; persistence and promotion are
// owned by DiscoveryRunner.
func MissingDiscoveryCandidates(catalog Catalog, seed DiscoverySeed) []DiscoveryCandidate {
	verified := make(map[string]struct{}, len(catalog.Companies))
	for _, company := range catalog.Companies {
		verified[company.ID] = struct{}{}
	}
	missing := make([]DiscoveryCandidate, 0, len(seed.Candidates))
	for _, candidate := range seed.Candidates {
		if _, exists := verified[candidate.ID]; !exists {
			missing = append(missing, candidate)
		}
	}
	sort.Slice(missing, func(i, j int) bool {
		leftLane, rightLane := discoveryFocusLane(missing[i].Tags), discoveryFocusLane(missing[j].Tags)
		if leftLane != rightLane {
			return leftLane < rightLane
		}
		leftPriority, rightPriority := discoveryPriority(missing[i].Tags), discoveryPriority(missing[j].Tags)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return missing[i].ID < missing[j].ID
	})
	return missing
}

// discoveryFocusLane keeps the research queue aligned with Radar's career bar.
// A candidate can belong to several segments; the first matching lane is its
// scheduling lane so every bounded batch can make progress across the target
// universe instead of being consumed by a large alphabetical priority tier.
func discoveryFocusLane(tags []string) int {
	for lane, tag := range []string{"auto-market-search", "yc-top", "quant", "big-tech", "unicorn"} {
		for _, candidateTag := range tags {
			if candidateTag == tag {
				return lane
			}
		}
	}
	return 5
}

func discoveryPriority(tags []string) int {
	for _, tag := range tags {
		switch tag {
		case "priority-1", "auto-market-search":
			return 1
		case "priority-2":
			return 2
		case "priority-3":
			return 3
		}
	}
	return 4
}
