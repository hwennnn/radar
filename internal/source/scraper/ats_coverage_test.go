package scraper

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type priorityATSPlatform struct {
	Kind      string
	Category  string
	ProofTest string
}

var priorityATSPlatforms = []priorityATSPlatform{
	{Kind: "greenhouse", Category: "startup", ProofTest: "TestATSExtractorExtractsGreenhouseJobs"},
	{Kind: "lever", Category: "startup", ProofTest: "TestATSExtractorExtractsLeverJobs"},
	{Kind: "ashby", Category: "startup", ProofTest: "TestATSExtractorExtractsAshbyJobs"},
	{Kind: "workable_jobs", Category: "startup", ProofTest: "TestATSExtractorExtractsWorkableJobs"},
	{Kind: "recruitee", Category: "startup", ProofTest: "TestATSExtractorExtractsRecruiteeJobs"},
	{Kind: "smartrecruiters", Category: "startup", ProofTest: "TestATSExtractorExtractsSmartRecruitersJobs"},
	{Kind: "comeet", Category: "startup", ProofTest: "TestATSExtractorExtractsComeetJobs"},
	{Kind: "teamtailor", Category: "startup", ProofTest: "TestATSExtractorExtractsTeamtailorJobsFromHostedBoard"},
	{Kind: "jobvite", Category: "startup", ProofTest: "TestATSExtractorExtractsJobviteJobsFromHostedBoard"},
	{Kind: "jazzhr", Category: "startup", ProofTest: "TestATSExtractorExtractsJazzHRJobsFromHostedBoard"},
	{Kind: "bamboohr", Category: "startup", ProofTest: "TestATSExtractorExtractsBambooHRJobsFromPublicCareersAPI"},
	{Kind: "rippling_jobs", Category: "startup", ProofTest: "TestATSExtractorExtractsRipplingJobsFromBoardAPI"},
	{Kind: "personio", Category: "regional", ProofTest: "TestATSExtractorExtractsPersonioJobs"},
	{Kind: "pinpoint", Category: "regional", ProofTest: "TestATSExtractorExtractsPinpointJobs"},
	{Kind: "breezy", Category: "regional", ProofTest: "TestATSExtractorExtractsBreezyJobs"},
	{Kind: "join_com", Category: "regional", ProofTest: "TestATSExtractorExtractsJOINBoardJobs"},
	{Kind: "workday", Category: "enterprise", ProofTest: "TestATSExtractorExtractsWorkdayJobs"},
	{Kind: "icims", Category: "enterprise", ProofTest: "TestATSExtractorExtractsICIMSJobsFromSitemap"},
	{Kind: "successfactors", Category: "enterprise", ProofTest: "TestATSExtractorExtractsSuccessFactorsJobsFromRSSFeed"},
	{Kind: "adp_workforce_now", Category: "enterprise", ProofTest: "TestATSExtractorExtractsADPWorkforceNowJobsFromPublicAPI"},
	{Kind: "ukg_pro", Category: "enterprise", ProofTest: "TestATSExtractorExtractsUKGProJobsFromHydratedJobBoard"},
	{Kind: "dayforce", Category: "enterprise", ProofTest: "TestATSExtractorExtractsDayforceBoardSearchJobs"},
	{Kind: "oracle_recruiting", Category: "enterprise", ProofTest: "TestATSExtractorExtractsOracleRecruitingBoard"},
	{Kind: "paylocity", Category: "enterprise", ProofTest: "TestATSExtractorExtractsPaylocityHostedJobs"},
	{Kind: "paycom", Category: "enterprise", ProofTest: "TestATSExtractorExtractsPaycomBoardJobs"},
	{Kind: "jibe", Category: "enterprise", ProofTest: "TestATSExtractorExtractsJibeJobPostingJSONLD"},
	{Kind: "recruiterbox", Category: "enterprise", ProofTest: "TestATSExtractorExtractsTrakstarRSSJobs"},
	{Kind: "apple_jobs", Category: "big_tech", ProofTest: "TestATSExtractorExtractsAppleJobsSearchResults"},
	{Kind: "stripe_jobs", Category: "big_tech", ProofTest: "TestATSExtractorExtractsStripeJobsSearchResults"},
	{Kind: "amazon_jobs", Category: "big_tech", ProofTest: "TestATSExtractorExtractsAmazonJobsSearchResults"},
	{Kind: "eightfold_pcsx", Category: "big_tech", ProofTest: "TestATSExtractorExtractsEightfoldPCSXSearchResults"},
	{Kind: "google_careers", Category: "big_tech", ProofTest: "TestATSExtractorExtractsGoogleCareersSearchResults"},
	{Kind: "openai_careers", Category: "big_tech", ProofTest: "TestATSExtractorExtractsOpenAICareersJobs"},
	{Kind: "microsoft_careers", Category: "big_tech", ProofTest: "TestATSExtractorExtractsEightfoldPCSXSearchResults"},
	{Kind: "github_job_list", Category: "community", ProofTest: "TestATSExtractorExtractsGitHubCommunityJobList"},
	{Kind: "taleo", Category: "enterprise", ProofTest: "TestATSExtractorExtractsTaleoJobs"},
}

func TestPriorityATSPlatformsSupported(t *testing.T) {
	supported := SupportedATSSourceKinds()
	for _, platform := range priorityATSPlatforms {
		if _, ok := supported[platform.Kind]; !ok {
			t.Fatalf("priority ATS platform %q (%s) is missing from SupportedATSSourceKinds", platform.Kind, platform.Category)
		}
	}
}

func TestPriorityATSPlatformsHaveDeterministicProofTests(t *testing.T) {
	fixtures := readATSTestFile(t)
	for _, platform := range priorityATSPlatforms {
		if platform.ProofTest == "" {
			t.Fatalf("priority ATS platform %q has no deterministic proof test", platform.Kind)
		}
		signature := "func " + platform.ProofTest + "("
		if !strings.Contains(fixtures, signature) {
			t.Fatalf("priority ATS platform %q proof test %q not found in ats_test.go", platform.Kind, platform.ProofTest)
		}
	}
}

func TestPriorityATSPlatformsContractHasUsefulShape(t *testing.T) {
	if len(priorityATSPlatforms) < 30 {
		t.Fatalf("priority ATS platform contract has %d platforms, want at least 30", len(priorityATSPlatforms))
	}
	categories := map[string]int{}
	kinds := map[string]struct{}{}
	for _, platform := range priorityATSPlatforms {
		if _, seen := kinds[platform.Kind]; seen {
			t.Fatalf("duplicate priority ATS platform kind %q", platform.Kind)
		}
		kinds[platform.Kind] = struct{}{}
		categories[platform.Category]++
	}
	for _, category := range []string{"startup", "enterprise", "big_tech", "regional", "community"} {
		if categories[category] == 0 {
			t.Fatalf("priority ATS platform contract missing %q category; got %#v", category, categories)
		}
	}
}

func readATSTestFile(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "ats_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
