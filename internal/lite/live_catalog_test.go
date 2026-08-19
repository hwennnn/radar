package lite

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hwennnn/radar/internal/scraper"
)

// TestLiveRobloxGreenhouseSource is an opt-in conformance check for the
// consumer-platform source that must not regress to research-only coverage.
// It never persists jobs or creates delivery decisions.
func TestLiveRobloxGreenhouseSource(t *testing.T) {
	if os.Getenv("RADAR_LITE_LIVE_SOURCE_TESTS") != "true" {
		t.Skip("set RADAR_LITE_LIVE_SOURCE_TESTS=true to query the public Roblox board")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := NewATSExtractor(scraper.ATSOptions{
		Client: scraper.NewSafeHTTPClient(20 * time.Second),
	}).Extract(ctx, Source{
		ID:       "roblox-greenhouse",
		Company:  "Roblox",
		Provider: "greenhouse",
		URL:      "https://job-boards.greenhouse.io/roblox",
	})
	if err != nil {
		t.Fatalf("extract official Roblox board: %v", err)
	}
	if !result.Complete || len(result.Observations) < 100 {
		t.Fatalf("Roblox snapshot incomplete or implausibly small: complete=%v observations=%d", result.Complete, len(result.Observations))
	}

	foundRelevant := false
	for _, observation := range result.Observations {
		if observation.Company != "Roblox" || !strings.HasPrefix(observation.SourceNativeID, "greenhouse:") {
			t.Fatalf("untrusted Roblox identity: %#v", observation)
		}
		title := strings.ToLower(observation.Title)
		if strings.Contains(title, "early career") || strings.Contains(title, "software engineer intern") {
			foundRelevant = true
		}
	}
	if !foundRelevant {
		t.Fatal("Roblox board is healthy but has no early-career software signal")
	}
}
