package tinyfishextractor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hwennnn/radar/internal/source/kind"
	"github.com/hwennnn/radar/internal/source/scraper"
	"github.com/hwennnn/radar/internal/source/tinyfish"
)

type Source = scraper.Source
type Evidence = scraper.Evidence
type JobPosting = scraper.JobPosting
type Result = scraper.Result
type Tier = scraper.Tier

const (
	TierSearchDiscovery = scraper.TierSearchDiscovery
	TierAIExtraction    = scraper.TierAIExtraction
	TierBrowserAgent    = scraper.TierBrowserAgent
)

var (
	ErrNoJobs       = scraper.ErrNoJobs
	NormalizeResult = scraper.NormalizeResult
)

const defaultTinyFishMaxResults = 5
const defaultTinyFishMarketMaxResults = 8
const defaultTinyFishMarketFallbackResults = 4
const defaultTinyFishAgentPollInterval = 2 * time.Second
const minTinyFishAgentPollInterval = 250 * time.Millisecond
const maxTinyFishAgentPollInterval = 30 * time.Second
const defaultTinyFishAgentCancelTimeout = 5 * time.Second
const defaultTinyFishAIBlockLimit = 12

var (
	firstURLPattern            = regexp.MustCompile(`https?://[^\s)\]]+`)
	aggregateJobListingPattern = regexp.MustCompile(`\b\d[\d,]*\+?\s+.*\bjobs?\b`)
	markdownHeadingPattern     = regexp.MustCompile(`^\s*#{1,4}\s+(.+?)\s*$`)
	whitespace                 = regexp.MustCompile(`\s+`)
)

type TinyFishClient interface {
	Search(ctx context.Context, request tinyfish.SearchRequest) (tinyfish.SearchResponse, error)
	Fetch(ctx context.Context, request tinyfish.FetchRequest) (tinyfish.FetchResponse, error)
}

type TinyFishAgentClient interface {
	RunAutomation(ctx context.Context, request tinyfish.AutomationRequest) (tinyfish.AutomationRunResponse, error)
}

type TinyFishAsyncAgentClient interface {
	StartAutomation(ctx context.Context, request tinyfish.AutomationRequest) (tinyfish.AutomationResponse, error)
	GetAutomationRun(ctx context.Context, runID string) (tinyfish.AutomationRunResponse, error)
	CancelAutomation(ctx context.Context, runID string) (tinyfish.AutomationCancelResponse, error)
}

type TinyFishSearchExtractor struct {
	client     TinyFishClient
	now        func() time.Time
	maxResults int
}

type TinyFishAgentExtractor struct {
	client TinyFishAgentClient
	now    func() time.Time
}

type TinyFishAIExtractor struct {
	client     TinyFishClient
	now        func() time.Time
	blockLimit int
}

func NewTinyFishSearchExtractor(client TinyFishClient) *TinyFishSearchExtractor {
	return &TinyFishSearchExtractor{
		client:     client,
		now:        func() time.Time { return time.Now().UTC() },
		maxResults: defaultTinyFishMaxResults,
	}
}

func NewTinyFishAgentExtractor(client TinyFishAgentClient) *TinyFishAgentExtractor {
	return &TinyFishAgentExtractor{
		client: client,
		now:    func() time.Time { return time.Now().UTC() },
	}
}

func NewTinyFishAIExtractor(client TinyFishClient) *TinyFishAIExtractor {
	return &TinyFishAIExtractor{
		client:     client,
		now:        func() time.Time { return time.Now().UTC() },
		blockLimit: defaultTinyFishAIBlockLimit,
	}
}

func (e *TinyFishSearchExtractor) Name() string {
	return "tinyfish-search-fetch"
}

func (e *TinyFishAgentExtractor) Name() string {
	return "tinyfish-agent"
}

func (e *TinyFishAIExtractor) Name() string {
	return "tinyfish-ai-extraction"
}

func (e *TinyFishSearchExtractor) Tier() Tier {
	return TierSearchDiscovery
}

func (e *TinyFishAgentExtractor) Tier() Tier {
	return TierBrowserAgent
}

func (e *TinyFishAIExtractor) Tier() Tier {
	return TierAIExtraction
}

func (e *TinyFishSearchExtractor) Sources() []Source {
	return []Source{
		{
			ID:   "tinyfish-us-early-career",
			Name: "TinyFish US early-career software search",
			URL:  "tinyfish://search/us-early-career-software",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" OR "new grad software engineer" careers jobs`,
				"location": "US",
				"cadence":  "30m",
				"kind":     "tinyfish_search",
			},
		},
		{
			ID:   "tinyfish-singapore-early-career",
			Name: "TinyFish Singapore early-career software search",
			URL:  "tinyfish://search/singapore-early-career-software",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" OR "new grad software engineer" Singapore careers jobs`,
				"location": "Singapore",
				"cadence":  "30m",
				"kind":     "tinyfish_search",
			},
		},
		{
			ID:   "tinyfish-uk-early-career",
			Name: "TinyFish UK early-career software search",
			URL:  "tinyfish://search/uk-early-career-software",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" OR "graduate software engineer" UK careers jobs`,
				"location": "United Kingdom",
				"cadence":  "30m",
				"kind":     "tinyfish_search",
			},
		},
		{
			ID:   "tinyfish-canada-early-career",
			Name: "TinyFish Canada early-career software search",
			URL:  "tinyfish://search/canada-early-career-software",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" OR "new grad software engineer" Canada careers jobs`,
				"location": "Canada",
				"cadence":  "30m",
				"kind":     "tinyfish_search",
			},
		},
		{
			ID:   "tinyfish-hong-kong-early-career",
			Name: "TinyFish Hong Kong early-career software search",
			URL:  "tinyfish://search/hong-kong-early-career-software",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" OR "graduate software engineer" "Hong Kong" careers jobs`,
				"location": "Hong Kong",
				"cadence":  "30m",
				"kind":     "tinyfish_search",
			},
		},
		{
			ID:   "tinyfish-finance-tech-early-career",
			Name: "TinyFish finance-tech early-career software search",
			URL:  "tinyfish://search/finance-tech-early-career-software",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" OR "new grad software engineer" hedge fund trading quant fintech careers jobs`,
				"location": "US",
				"cadence":  "30m",
				"kind":     "tinyfish_search",
			},
		},
		{
			ID:   "tinyfish-ai-infra-devtools-early-career",
			Name: "TinyFish AI infra/devtools early-career software search",
			URL:  "tinyfish://search/ai-infra-devtools-early-career-software",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" OR "new grad software engineer" AI infrastructure devtools careers jobs`,
				"location": "US",
				"cadence":  "30m",
				"kind":     "tinyfish_search",
			},
		},
		{
			ID:   "tinyfish-big-tech-unicorn-early-career",
			Name: "TinyFish big-tech/unicorn early-career software search",
			URL:  "tinyfish://search/big-tech-unicorn-early-career-software",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" OR "new grad software engineer" big tech unicorn careers jobs`,
				"location": "US",
				"cadence":  "30m",
				"kind":     "tinyfish_search",
			},
		},
		// ATS-direct: greenhouse and lever boards return actual company postings, not aggregator pages.
		{
			ID:   "tinyfish-ats-backend-infra-us",
			Name: "TinyFish ATS backend/infra intern search",
			URL:  "tinyfish://search/ats-backend-infra-intern-us",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `(site:greenhouse.io OR site:lever.co) ("backend engineer intern" OR "infrastructure engineer intern" OR "platform engineer intern") 2026`,
				"location": "US",
				"cadence":  "45m",
				"kind":     "tinyfish_search",
			},
		},
		{
			ID:   "tinyfish-ats-ml-ai-intern-us",
			Name: "TinyFish ATS ML/AI engineer intern search",
			URL:  "tinyfish://search/ats-ml-ai-intern-us",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `(site:greenhouse.io OR site:lever.co) ("machine learning engineer intern" OR "AI engineer intern" OR "software engineer intern" "machine learning") 2026`,
				"location": "US",
				"cadence":  "45m",
				"kind":     "tinyfish_search",
			},
		},
		{
			ID:   "tinyfish-ats-swe-intern-uk",
			Name: "TinyFish ATS SWE intern UK search",
			URL:  "tinyfish://search/ats-swe-intern-uk",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `(site:greenhouse.io OR site:lever.co) ("software engineer intern" OR "software engineering internship") London UK 2026`,
				"location": "United Kingdom",
				"cadence":  "45m",
				"kind":     "tinyfish_search",
			},
		},
		// City-specific: finer targeting than country-level queries.
		{
			ID:   "tinyfish-nyc-early-career",
			Name: "TinyFish New York City early-career software search",
			URL:  "tinyfish://search/nyc-early-career-software",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" OR "new grad software engineer" "New York" careers 2026`,
				"location": "New York",
				"cadence":  "45m",
				"kind":     "tinyfish_search",
			},
		},
		{
			ID:   "tinyfish-sf-early-career",
			Name: "TinyFish San Francisco early-career software search",
			URL:  "tinyfish://search/sf-early-career-software",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" OR "new grad software engineer" "San Francisco" careers 2026`,
				"location": "San Francisco",
				"cadence":  "45m",
				"kind":     "tinyfish_search",
			},
		},
		// Remote: broad reach across companies that hire globally.
		{
			ID:   "tinyfish-remote-swe-intern",
			Name: "TinyFish remote SWE intern search",
			URL:  "tinyfish://search/remote-swe-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `"software engineer intern" OR "new grad software engineer" remote 2026 careers`,
				"cadence": "45m",
				"kind":    "tinyfish_search",
			},
		},
		// Quant/trading: high-signal for finance/money motivation.
		{
			ID:   "tinyfish-quant-trading-intern-global",
			Name: "TinyFish quant/trading intern global search",
			URL:  "tinyfish://search/quant-trading-intern-global",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `"quantitative developer intern" OR "quant researcher intern" OR "trading systems intern" "Jane Street" OR "Citadel" OR "Optiver" OR "Two Sigma" OR "IMC" OR "Jump Trading" 2026`,
				"cadence": "60m",
				"kind":    "tinyfish_search",
			},
		},
		// Data/platform engineering: adjacent to backend infra.
		{
			ID:   "tinyfish-data-platform-intern-us",
			Name: "TinyFish data/platform engineering intern search",
			URL:  "tinyfish://search/data-platform-intern-us",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"data engineering intern" OR "data infrastructure intern" OR "platform engineering intern" US careers 2026`,
				"location": "US",
				"cadence":  "45m",
				"kind":     "tinyfish_search",
			},
		},
		// Go/Python backend: skill-targeted query for better role fit.
		{
			ID:   "tinyfish-go-python-backend-intern",
			Name: "TinyFish Go/Python backend intern search",
			URL:  "tinyfish://search/go-python-backend-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `("software engineer intern" OR "backend engineer intern") (Go OR Golang OR Python) careers 2026`,
				"cadence": "60m",
				"kind":    "tinyfish_search",
			},
		},
		// Startup/VC-backed: high growth, upside-motivated companies.
		{
			ID:   "tinyfish-startup-intern-us",
			Name: "TinyFish startup/VC-backed intern search",
			URL:  "tinyfish://search/startup-intern-us",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" (startup OR "Series A" OR "Series B" OR "Series C" OR "YC" OR "Y Combinator") site:lever.co OR site:greenhouse.io 2026`,
				"location": "US",
				"cadence":  "60m",
				"kind":     "tinyfish_search",
			},
		},
		// Europe: Germany, Netherlands, Sweden for broader global reach.
		{
			ID:   "tinyfish-europe-swe-intern",
			Name: "TinyFish Europe SWE intern search",
			URL:  "tinyfish://search/europe-swe-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `"software engineer intern" OR "software engineering internship" (Germany OR Netherlands OR Sweden OR Berlin OR Amsterdam OR Stockholm) 2026`,
				"cadence": "60m",
				"kind":    "tinyfish_search",
			},
		},
		// Australia/NZ: common visa-friendly market for the profile.
		{
			ID:   "tinyfish-australia-swe-intern",
			Name: "TinyFish Australia/NZ SWE intern search",
			URL:  "tinyfish://search/australia-swe-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `"software engineer intern" OR "graduate software engineer" (Australia OR "New Zealand" OR Sydney OR Melbourne OR Auckland) 2026`,
				"cadence": "60m",
				"kind":    "tinyfish_search",
			},
		},
		// AI agents / LLM tooling: matches profile's AI agents interest directly.
		{
			ID:   "tinyfish-ai-agents-llm-intern",
			Name: "TinyFish AI agents/LLM intern search",
			URL:  "tinyfish://search/ai-agents-llm-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `("software engineer intern" OR "AI engineer intern") ("AI agents" OR "LLM" OR "large language model" OR "agentic") careers 2026`,
				"cadence": "45m",
				"kind":    "tinyfish_search",
			},
		},
		// Distributed systems / cloud infra: targets infra-minded roles.
		{
			ID:   "tinyfish-distributed-cloud-intern",
			Name: "TinyFish distributed systems/cloud infra intern search",
			URL:  "tinyfish://search/distributed-cloud-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `("software engineer intern" OR "infrastructure engineer intern") ("distributed systems" OR "cloud infrastructure" OR Kubernetes OR "systems programming") careers 2026`,
				"cadence": "60m",
				"kind":    "tinyfish_search",
			},
		},
		// --- Second expansion: city hubs, verticals, ATS-scoped new-grad, and FAANG ---
		// FAANG + MANGA directly: highest-brand internships by name.
		{
			ID:   "tinyfish-faang-intern",
			Name: "TinyFish FAANG/MANGA intern search",
			URL:  "tinyfish://search/faang-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `"software engineer intern" (Google OR Meta OR Apple OR Amazon OR Microsoft OR Netflix OR Nvidia) site:careers.google.com OR site:jobs.apple.com OR site:amazon.jobs OR site:careers.microsoft.com OR site:nvidia.com 2026`,
				"cadence": "45m",
				"kind":    "tinyfish_search",
			},
		},
		// ATS new-grad US: greenhouse/lever new-grad backend, different from intern.
		{
			ID:   "tinyfish-ats-new-grad-us",
			Name: "TinyFish ATS new grad software engineer US search",
			URL:  "tinyfish://search/ats-new-grad-us",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `(site:greenhouse.io OR site:lever.co) ("new grad software engineer" OR "new graduate software engineer" OR "entry level software engineer") 2026`,
				"location": "US",
				"cadence":  "45m",
				"kind":     "tinyfish_search",
			},
		},
		// ATS new-grad UK: same angle for UK market.
		{
			ID:   "tinyfish-ats-new-grad-uk",
			Name: "TinyFish ATS new grad software engineer UK search",
			URL:  "tinyfish://search/ats-new-grad-uk",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `(site:greenhouse.io OR site:lever.co) ("graduate software engineer" OR "entry level software engineer") London UK 2026`,
				"location": "United Kingdom",
				"cadence":  "45m",
				"kind":     "tinyfish_search",
			},
		},
		// London city hub: more specific than country-level UK query.
		{
			ID:   "tinyfish-london-tech-intern",
			Name: "TinyFish London tech hub intern search",
			URL:  "tinyfish://search/london-tech-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" OR "software engineering internship" London 2026 careers`,
				"location": "London",
				"cadence":  "45m",
				"kind":     "tinyfish_search",
			},
		},
		// Toronto city hub.
		{
			ID:   "tinyfish-toronto-tech-intern",
			Name: "TinyFish Toronto tech hub intern search",
			URL:  "tinyfish://search/toronto-tech-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" OR "new grad software engineer" Toronto 2026 careers`,
				"location": "Toronto",
				"cadence":  "45m",
				"kind":     "tinyfish_search",
			},
		},
		// Zurich: Switzerland fintech and quant hub.
		{
			ID:   "tinyfish-zurich-fintech-intern",
			Name: "TinyFish Zurich fintech/quant intern search",
			URL:  "tinyfish://search/zurich-fintech-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `"software engineer intern" OR "quant developer intern" Zurich Switzerland fintech trading 2026`,
				"location": "Zurich",
				"cadence":  "60m",
				"kind":     "tinyfish_search",
			},
		},
		// Handshake: university-facing intern board, rich early-career content.
		{
			ID:   "tinyfish-handshake-swe-intern",
			Name: "TinyFish Handshake SWE intern search",
			URL:  "tinyfish://search/handshake-swe-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `site:joinhandshake.com "software engineer intern" OR "software engineering intern" 2026`,
				"cadence": "60m",
				"kind":    "tinyfish_search",
			},
		},
		// Levels.fyi internships: high-quality curated list with compensation data.
		{
			ID:   "tinyfish-levelsfyi-intern",
			Name: "TinyFish Levels.fyi internship search",
			URL:  "tinyfish://search/levelsfyi-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `site:levels.fyi internship "software engineer" 2026`,
				"cadence": "60m",
				"kind":    "tinyfish_search",
			},
		},
		// Simplify Jobs aggregator: tracks 2026 internship opening/closing status.
		{
			ID:   "tinyfish-simplify-intern-tracker",
			Name: "TinyFish Simplify 2026 intern tracker search",
			URL:  "tinyfish://search/simplify-intern-tracker",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `site:simplify.jobs "software engineer intern" 2026`,
				"cadence": "60m",
				"kind":    "tinyfish_search",
			},
		},
		// Crypto/Web3: fast-hiring, high-comp sector.
		{
			ID:   "tinyfish-crypto-web3-intern",
			Name: "TinyFish crypto/Web3 SWE intern search",
			URL:  "tinyfish://search/crypto-web3-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `"software engineer intern" (crypto OR blockchain OR web3 OR "DeFi" OR "smart contracts") careers 2026`,
				"cadence": "60m",
				"kind":    "tinyfish_search",
			},
		},
		// Security/Infra: SRE and security engineering interns.
		{
			ID:   "tinyfish-sre-security-intern",
			Name: "TinyFish SRE/security engineering intern search",
			URL:  "tinyfish://search/sre-security-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `("site reliability engineer intern" OR "SRE intern" OR "security engineer intern" OR "DevOps intern") careers 2026`,
				"cadence": "60m",
				"kind":    "tinyfish_search",
			},
		},
		// Cloud provider-specific: AWS, GCP, Azure teams recruit interns separately.
		{
			ID:   "tinyfish-cloud-provider-intern",
			Name: "TinyFish cloud provider intern search",
			URL:  "tinyfish://search/cloud-provider-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `"software engineer intern" (AWS OR "Amazon Web Services" OR GCP OR "Google Cloud" OR Azure) 2026 careers`,
				"cadence": "60m",
				"kind":    "tinyfish_search",
			},
		},
		// LinkedIn search: broad but deep pool via direct LinkedIn job search.
		{
			ID:   "tinyfish-linkedin-swe-intern-us",
			Name: "TinyFish LinkedIn SWE intern US search",
			URL:  "tinyfish://search/linkedin-swe-intern-us",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `site:linkedin.com/jobs "software engineer intern" 2026 United States`,
				"location": "US",
				"cadence":  "45m",
				"kind":     "tinyfish_search",
			},
		},
		// Indeed search: additional aggregator pool.
		{
			ID:   "tinyfish-indeed-swe-intern-us",
			Name: "TinyFish Indeed SWE intern US search",
			URL:  "tinyfish://search/indeed-swe-intern-us",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":    `site:indeed.com "software engineer intern" 2026 United States`,
				"location": "US",
				"cadence":  "45m",
				"kind":     "tinyfish_search",
			},
		},
		// Open-source / dev-infra companies: Stripe, Cloudflare, Vercel, etc.
		{
			ID:   "tinyfish-dev-infra-tools-intern",
			Name: "TinyFish dev infra / open-source company intern search",
			URL:  "tinyfish://search/dev-infra-tools-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `"software engineer intern" (Stripe OR Cloudflare OR Vercel OR Datadog OR HashiCorp OR "Elastic" OR Confluent) 2026 site:greenhouse.io OR site:lever.co`,
				"cadence": "60m",
				"kind":    "tinyfish_search",
			},
		},

		// GitHub job compilation repos — community-maintained lists of open roles.
		{
			ID:   "github-simplify-2026-internships",
			Name: "SimplifyJobs Summer 2026 Internships (GitHub)",
			URL:  "https://github.com/SimplifyJobs/Summer2026-Internships",
			Tier: TierBrowserAgent,
			Metadata: map[string]string{
				"cadence": "2h",
				"goal":    "Extract all software engineering internship and new-grad job listings from the README table on this GitHub page. Each row has: company name, role title, location, and an application link. Return every open role as a separate job.",
			},
		},
		{
			ID:   "github-simplify-new-grad",
			Name: "SimplifyJobs New Grad Positions (GitHub)",
			URL:  "https://github.com/SimplifyJobs/New-Grad-Positions",
			Tier: TierBrowserAgent,
			Metadata: map[string]string{
				"cadence": "2h",
				"goal":    "Extract all software engineering new-grad and entry-level job listings from the README table on this GitHub page. Each row has: company name, role title, location, and an application link. Return every open role.",
			},
		},

		// Reddit job signal sources — posts referencing companies actively
		// conducting OAs or interviews reveal who is actively hiring.
		{
			ID:   "tinyfish-reddit-cscareerquestions-intern",
			Name: "Reddit r/cscareerquestions internship signal",
			URL:  "tinyfish://search/reddit-cscareerquestions-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `site:reddit.com/r/cscareerquestions ("software engineer intern" OR "SWE intern" OR "new grad") 2026 (OA OR "online assessment" OR interview OR hiring OR referral)`,
				"cadence": "90m",
				"kind":    "tinyfish_search",
			},
		},
		{
			ID:   "tinyfish-reddit-csmajors-intern",
			Name: "Reddit r/csMajors internship signal",
			URL:  "tinyfish://search/reddit-csmajors-intern",
			Tier: TierSearchDiscovery,
			Metadata: map[string]string{
				"query":   `site:reddit.com/r/csMajors ("software engineer intern" OR "SWE intern" OR "new grad") 2026 (OA OR "online assessment" OR interview OR hiring)`,
				"cadence": "90m",
				"kind":    "tinyfish_search",
			},
		},
	}
}

func (e *TinyFishSearchExtractor) Extract(ctx context.Context, source Source) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if e.client == nil {
		return Result{}, errors.New("tinyfish client is required")
	}
	if source.ID == "" {
		source = e.Sources()[0]
	}

	query := searchQueryForSource(source)
	search, err := e.client.Search(ctx, tinyfish.SearchRequest{
		Query:    query,
		Location: searchLocationForSource(source),
	})
	if err != nil {
		return Result{}, err
	}

	results := filterSearchResultsForSource(source, search.Results, tinyFishResultLimit(source, e.maxResults))
	if len(results) == 0 {
		return Result{}, ErrNoJobs
	}

	urls := make([]string, 0, len(results))
	searchByURL := make(map[string]tinyfish.SearchResult, len(results))
	for _, result := range results {
		urls = append(urls, result.URL)
		searchByURL[result.URL] = result
	}
	fetched, err := e.client.Fetch(ctx, tinyfish.FetchRequest{
		URLs:   urls,
		Format: "markdown",
	})
	usedFetchFallback := false
	if err != nil && strings.EqualFold(strings.TrimSpace(sourceKind(source)), "market_search") && len(urls) > defaultTinyFishMarketFallbackResults {
		fallbackURLs := append([]string(nil), urls[:defaultTinyFishMarketFallbackResults]...)
		fallback, fallbackErr := e.client.Fetch(ctx, tinyfish.FetchRequest{URLs: fallbackURLs, Format: "markdown"})
		if fallbackErr == nil {
			fetched = fallback
			err = nil
			usedFetchFallback = true
		} else {
			err = errors.Join(err, fmt.Errorf("tinyfish market fallback fetch: %w", fallbackErr))
		}
	}
	if err != nil {
		return Result{}, err
	}
	if len(fetched.Results) == 0 && len(fetched.Errors) > 0 {
		return Result{}, tinyFishFetchErrors(fetched.Errors)
	}

	var jobs []JobPosting
	var evidence []Evidence
	rejectionSamples := make([]Evidence, 0, 3)
	rejected := 0
	fetchedAt := e.now()
	for _, item := range fetched.Results {
		searchResult := searchByURL[item.URL]
		posting, rejectReason, ok := postingFromTinyFishFetch(item, searchResult, fetchedAt)
		if !ok {
			rejected++
			if len(rejectionSamples) < 3 {
				rejectionSamples = append(rejectionSamples, tinyFishRejectionEvidence(item, searchResult, rejectReason))
			}
			continue
		}
		jobs = append(jobs, posting)
		evidence = append(evidence, Evidence{Field: "search_result", Text: searchResult.Snippet, URL: searchResult.URL})
	}
	if rejected > 0 {
		evidence = append(evidence, Evidence{
			Field: "tinyfish_rejection_count",
			Text:  strconv.Itoa(rejected),
			URL:   source.URL,
		})
		evidence = append(evidence, rejectionSamples...)
	}
	if usedFetchFallback {
		evidence = append(evidence, Evidence{
			Field: "tinyfish_fetch_fallback",
			Text:  fmt.Sprintf("retried top %d of %d market results after the full fetch failed", defaultTinyFishMarketFallbackResults, len(urls)),
			URL:   source.URL,
		})
	}

	result := Result{
		Source:      source,
		Jobs:        jobs,
		RawEvidence: evidence,
		Confidence:  0.72,
		Strategy:    TierSearchDiscovery,
		Live:        true,
		FetchedAt:   fetchedAt,
	}
	return NormalizeResult(result)
}

func tinyFishResultLimit(source Source, configured int) int {
	if configured <= 0 {
		configured = defaultTinyFishMaxResults
	}
	if strings.EqualFold(strings.TrimSpace(sourceKind(source)), "market_search") && configured < defaultTinyFishMarketMaxResults {
		return defaultTinyFishMarketMaxResults
	}
	return configured
}

func tinyFishFetchErrors(fetchErrors []tinyfish.FetchError) error {
	if len(fetchErrors) == 0 {
		return ErrNoJobs
	}
	parts := make([]string, 0, len(fetchErrors))
	for _, fetchErr := range fetchErrors {
		urlPart := strings.TrimSpace(fetchErr.URL)
		codePart := strings.TrimSpace(fetchErr.Code)
		messagePart := strings.TrimSpace(fetchErr.Message)
		detail := firstNonEmpty(messagePart, codePart, urlPart, "fetch failed")
		if urlPart != "" && !strings.Contains(detail, urlPart) {
			detail = urlPart + ": " + detail
		}
		parts = append(parts, detail)
	}
	return fmt.Errorf("tinyfish fetch failed for all selected search results: %s", strings.Join(parts, "; "))
}

func (e *TinyFishAIExtractor) Extract(ctx context.Context, source Source) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if e.client == nil {
		return Result{}, errors.New("tinyfish client is required")
	}
	source.URL = strings.TrimSpace(source.URL)
	if source.URL == "" {
		return Result{}, errors.New("ai extraction source url is required")
	}
	if source.Tier == "" {
		source.Tier = TierAIExtraction
	}

	fetched, err := e.client.Fetch(ctx, tinyfish.FetchRequest{
		URLs:   []string{source.URL},
		Format: "markdown",
	})
	if err != nil {
		return Result{}, err
	}

	fetchedAt := e.now()
	var jobs []JobPosting
	var evidence []Evidence
	for _, item := range fetched.Results {
		content := firstNonEmpty(item.Markdown, item.Text, item.Content)
		if strings.TrimSpace(content) == "" {
			continue
		}
		evidence = append(evidence, Evidence{Field: "ai_fetch_url", Text: item.URL, URL: item.URL})
		parsed := postingsFromTinyFishAIContent(item, source, fetchedAt, e.blockLimit)
		jobs = append(jobs, parsed...)
	}
	result := Result{
		Source:      source,
		Jobs:        jobs,
		RawEvidence: evidence,
		Confidence:  confidenceForAIExtraction(jobs),
		Strategy:    TierAIExtraction,
		Live:        len(jobs) > 0,
		FetchedAt:   fetchedAt,
	}
	return NormalizeResult(result)
}

func (e *TinyFishAgentExtractor) Extract(ctx context.Context, source Source) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if e.client == nil {
		return Result{}, errors.New("tinyfish agent client is required")
	}
	source.URL = strings.TrimSpace(source.URL)
	if source.URL == "" {
		return Result{}, errors.New("browser-agent source url is required")
	}
	if source.Tier == "" {
		source.Tier = TierBrowserAgent
	}

	request := tinyfish.AutomationRequest{
		URL:            source.URL,
		Goal:           tinyFishAgentGoal(source),
		BrowserProfile: strings.TrimSpace(source.Metadata["browser_profile"]),
		OutputSchema:   tinyFishAgentJobSchema(),
		AgentConfig: map[string]any{
			"mode":                 "strict",
			"max_steps":            60,
			"max_duration_seconds": 300,
		},
	}
	response, err := e.runAgent(ctx, source, request)
	if err != nil {
		return Result{
			Source:      source,
			RawEvidence: agentEvidence(response, source),
			Confidence:  confidenceForAgentRun(response, nil),
			Strategy:    TierBrowserAgent,
			Live:        false,
			FetchedAt:   e.now(),
		}, err
	}

	jobs, evidence, err := postingsFromTinyFishAgent(response, source)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Source:      source,
		Jobs:        jobs,
		RawEvidence: evidence,
		Confidence:  confidenceForAgentRun(response, jobs),
		Strategy:    TierBrowserAgent,
		Live:        true,
		FetchedAt:   e.now(),
	}
	return NormalizeResult(result)
}

func (e *TinyFishAgentExtractor) runAgent(ctx context.Context, source Source, request tinyfish.AutomationRequest) (tinyfish.AutomationRunResponse, error) {
	if strings.EqualFold(strings.TrimSpace(source.Metadata["agent_mode"]), "sync") {
		return e.client.RunAutomation(ctx, request)
	}
	async, ok := e.client.(TinyFishAsyncAgentClient)
	if !ok {
		return e.client.RunAutomation(ctx, request)
	}
	return runTinyFishAgentAsync(ctx, async, request, source, agentPollInterval(source), defaultTinyFishAgentCancelTimeout)
}

func runTinyFishAgentAsync(ctx context.Context, client TinyFishAsyncAgentClient, request tinyfish.AutomationRequest, source Source, pollInterval time.Duration, cancelTimeout time.Duration) (tinyfish.AutomationRunResponse, error) {
	queued, err := client.StartAutomation(ctx, request)
	if err != nil {
		return tinyfish.AutomationRunResponse{}, err
	}
	runID := strings.TrimSpace(queued.RunID)
	if runID == "" {
		return tinyfish.AutomationRunResponse{}, errors.New("tinyfish automation run id is missing")
	}
	if pollInterval <= 0 {
		pollInterval = defaultTinyFishAgentPollInterval
	}

	var last tinyfish.AutomationRunResponse
	polls := 0
	for {
		if err := ctx.Err(); err != nil {
			return cancelTinyFishAgentRun(ctx, client, runID, last, polls, cancelTimeout, err)
		}
		run, err := client.GetAutomationRun(ctx, runID)
		polls++
		if err != nil {
			return runWithAsyncEvidence(run, runID, polls), err
		}
		last = runWithAsyncEvidence(run, runID, polls)
		switch strings.ToUpper(strings.TrimSpace(last.Status)) {
		case "COMPLETED":
			return last, nil
		case "FAILED", "CANCELLED":
			return last, fmt.Errorf("tinyfish automation finished with status %s", last.Status)
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return cancelTinyFishAgentRun(ctx, client, runID, last, polls, cancelTimeout, ctx.Err())
		case <-timer.C:
		}
	}
}

func cancelTinyFishAgentRun(ctx context.Context, client TinyFishAsyncAgentClient, runID string, last tinyfish.AutomationRunResponse, polls int, timeout time.Duration, cause error) (tinyfish.AutomationRunResponse, error) {
	if timeout <= 0 {
		timeout = defaultTinyFishAgentCancelTimeout
	}
	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	cancelled, cancelErr := client.CancelAutomation(cancelCtx, runID)
	last = runWithAsyncEvidence(last, runID, polls)
	last.CancelStatus = cancelled.Status
	if last.Status == "" {
		last.Status = firstNonEmpty(cancelled.Status, "CANCEL_REQUESTED")
	}
	if cancelErr != nil {
		last.Result = nil
		last.ResultJSON = nil
		return last, errors.Join(cause, fmt.Errorf("cancel tinyfish automation run %s: %w", runID, cancelErr))
	}
	return last, cause
}

func runWithAsyncEvidence(run tinyfish.AutomationRunResponse, runID string, polls int) tinyfish.AutomationRunResponse {
	if run.RunID == "" {
		run.RunID = runID
	}
	run.Polls = polls
	run.Mode = "async"
	return run
}

func agentPollInterval(source Source) time.Duration {
	for _, key := range []string{"poll_interval", "agent_poll_interval"} {
		value := strings.TrimSpace(source.Metadata[key])
		if value == "" {
			continue
		}
		if parsed, err := time.ParseDuration(value); err == nil {
			return clampTinyFishAgentPollInterval(parsed)
		}
	}
	return defaultTinyFishAgentPollInterval
}

func clampTinyFishAgentPollInterval(value time.Duration) time.Duration {
	if value <= 0 {
		return defaultTinyFishAgentPollInterval
	}
	if value < minTinyFishAgentPollInterval {
		return minTinyFishAgentPollInterval
	}
	if value > maxTinyFishAgentPollInterval {
		return maxTinyFishAgentPollInterval
	}
	return value
}

func searchQueryForSource(source Source) string {
	if query := strings.TrimSpace(source.Metadata["query"]); query != "" {
		return query
	}
	if query := syntheticTinyFishSearchQuery(source.URL); query != "" {
		return query
	}
	if query := hostedFallbackBoardQuery(source); query != "" {
		return query
	}
	if query := searchDiscoveryBoardQuery(source); query != "" {
		return query
	}
	values := sourceURLQuery(source.URL)
	if query := searchIntentFromValues(values); query != "" {
		return query
	}
	return strings.TrimSpace(source.Name)
}

func syntheticTinyFishSearchQuery(rawURL string) string {
	key := syntheticTinyFishSearchKey(rawURL)
	switch key {
	case "singapore-early-career-software":
		return `"software engineer intern" OR "new grad software engineer" Singapore careers jobs`
	case "us-early-career-software":
		return `"software engineer intern" OR "new grad software engineer" careers jobs`
	case "uk-early-career-software":
		return `"software engineer intern" OR "graduate software engineer" UK careers jobs`
	case "canada-early-career-software":
		return `"software engineer intern" OR "new grad software engineer" Canada careers jobs`
	case "hong-kong-early-career-software":
		return `"software engineer intern" OR "graduate software engineer" "Hong Kong" careers jobs`
	case "finance-tech-early-career-software":
		return `"software engineer intern" OR "new grad software engineer" hedge fund trading quant fintech careers jobs`
	case "ai-infra-devtools-early-career-software":
		return `"software engineer intern" OR "new grad software engineer" AI infrastructure devtools careers jobs`
	case "big-tech-unicorn-early-career-software":
		return `"software engineer intern" OR "new grad software engineer" big tech unicorn careers jobs`
	default:
		return ""
	}
}

func searchDiscoveryBoardQuery(source Source) string {
	if !isScopedSearchDiscoveryKind(sourceKind(source)) {
		return ""
	}
	scope := sourceSiteScope(source.URL)
	if scope == "" {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(sourceKind(source)), "official_careers") {
		company := sourceCompanyName(source)
		if company == "" {
			return fmt.Sprintf(`site:%s ("software engineer intern" OR "new grad software engineer" OR "machine learning engineer intern" OR "graduate software engineer")`, scope)
		}
		return fmt.Sprintf(`site:%s %q ("software engineer intern" OR "new grad software engineer" OR "machine learning engineer intern" OR "graduate software engineer")`, scope, company)
	}
	intent := searchIntentFromValues(sourceURLQuery(source.URL))
	if intent == "" {
		intent = sourceCompanyName(source)
	}
	if intent == "" {
		return fmt.Sprintf("site:%s software engineer intern new grad jobs", scope)
	}
	return fmt.Sprintf("site:%s %s", scope, intent)
}

func searchIntentFromValues(values url.Values) string {
	for _, key := range []string{"keywords", "keyword", "sc.keyword", "q", "k", "query", "base_query", "search", "term", "ak", "searchstring", "f_p"} {
		if query := strings.TrimSpace(values.Get(key)); query != "" {
			return query
		}
	}
	return ""
}

func hostedFallbackBoardQuery(source Source) string {
	if !isHostedFallbackKind(sourceKind(source)) {
		return ""
	}
	scope := sourceSiteScope(source.URL)
	if scope == "" {
		return ""
	}
	company := sourceCompanyName(source)
	if company == "" {
		company = companyNameFromHostedFallbackURL(source.URL)
	}
	if company == "" {
		return fmt.Sprintf("site:%s software engineer intern new grad careers jobs", scope)
	}
	return fmt.Sprintf("site:%s %s software engineer intern new grad careers jobs", scope, company)
}

func sourceKind(source Source) string {
	for _, key := range []string{"source_kind", "kind", "provider"} {
		if value := strings.ToLower(strings.TrimSpace(source.Metadata[key])); value != "" {
			return value
		}
	}
	return ""
}

func isHostedFallbackKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "gem", "avature", "hireology", "workstream", "jobylon", "zoho_recruit", "manatal", "freshteam", "join_com", "talentlyft", "homerun", "catsone", "occupop", "hibob_hiring", "workable_jobs", "rippling_jobs", "fountain", "applicantpro", "careerplug", "jobsoid", "paycom", "dover", "yello":
		return true
	default:
		return false
	}
}

func isScopedSearchDiscoveryKind(kind string) bool {
	return sourcekind.IsSearchDiscoveryKind(kind)
}

func sourceSiteScope(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" {
		return ""
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	scope := strings.ToLower(parsed.Hostname())
	path := strings.Trim(parsed.EscapedPath(), "/")
	if path == "" {
		return scope
	}
	return scope + "/" + path
}

func sourceCompanyName(source Source) string {
	name := strings.TrimSpace(source.Name)
	if name == "" {
		return ""
	}
	kind := sourceKind(source)
	if strings.EqualFold(name, kind) || strings.EqualFold(name, source.ID) {
		return ""
	}
	return name
}

func companyNameFromHostedFallbackURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	slug := ""
	switch {
	case strings.Contains(host, "gem.com"), strings.Contains(host, "hireology.com"), strings.Contains(host, "jobylon.com"), strings.Contains(host, "recruiterbox.com"), strings.Contains(host, "fountain.com"):
		slug = tinyFishFirstPathSegment(parts)
	case strings.Contains(host, "join.com") && len(parts) >= 2 && parts[0] == "companies":
		slug = parts[1]
	case strings.Contains(host, "workstream.us") && len(parts) >= 2 && parts[0] == "j":
		slug = parts[1]
	case strings.Contains(host, "avature."), strings.Contains(host, "zohorecruit."), strings.Contains(host, "manatal."), strings.Contains(host, "freshteam."), strings.Contains(host, "talentlyft."), strings.Contains(host, "homerun."), strings.Contains(host, "catsone."), strings.Contains(host, "occupop."), strings.Contains(host, "careers.hibob."), strings.Contains(host, "applicantpro."), strings.Contains(host, "jobsoid."):
		slug = firstSubdomain(host)
	}
	return titleWords(strings.ReplaceAll(strings.Trim(slug, "-_"), "-", " "))
}

func tinyFishFirstPathSegment(parts []string) string {
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstSubdomain(host string) string {
	if idx := strings.IndexByte(host, '.'); idx > 0 {
		return host[:idx]
	}
	return ""
}

func searchLocationForSource(source Source) string {
	if location := strings.TrimSpace(source.Metadata["location"]); location != "" {
		return location
	}
	if location := syntheticTinyFishSearchLocation(source.URL); location != "" {
		return location
	}
	values := sourceURLQuery(source.URL)
	for _, key := range []string{"location", "l", "loc_query", "where"} {
		if location := strings.TrimSpace(values.Get(key)); location != "" {
			return location
		}
	}
	return ""
}

func syntheticTinyFishSearchLocation(rawURL string) string {
	key := syntheticTinyFishSearchKey(rawURL)
	switch {
	case strings.HasPrefix(key, "singapore-"):
		return "Singapore"
	case strings.HasPrefix(key, "us-"):
		return "US"
	case strings.HasPrefix(key, "uk-"):
		return "United Kingdom"
	case strings.HasPrefix(key, "canada-"):
		return "Canada"
	case strings.HasPrefix(key, "hong-kong-"):
		return "Hong Kong"
	case strings.HasPrefix(key, "finance-tech-"):
		return "US"
	case strings.HasPrefix(key, "ai-infra-devtools-"):
		return "US"
	case strings.HasPrefix(key, "big-tech-unicorn-"):
		return "US"
	default:
		return ""
	}
}

func syntheticTinyFishSearchKey(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "tinyfish") || !strings.EqualFold(parsed.Host, "search") {
		return ""
	}
	return strings.ToLower(strings.Trim(strings.TrimSpace(parsed.Path), "/"))
}

func sourceURLQuery(rawURL string) url.Values {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.RawQuery == "" {
		return url.Values{}
	}
	return parsed.Query()
}

func filterSearchResultsForSource(source Source, results []tinyfish.SearchResult, max int) []tinyfish.SearchResult {
	if max <= 0 {
		max = defaultTinyFishMaxResults
	}
	scope := ""
	if isScopedSearchDiscoveryKind(sourceKind(source)) {
		scope = sourceSiteScope(source.URL)
	}
	out := make([]tinyfish.SearchResult, 0, max)
	seen := make(map[string]bool, len(results))
	marketSearch := strings.EqualFold(strings.TrimSpace(sourceKind(source)), "market_search")
	for _, result := range results {
		result.URL = strings.TrimSpace(result.URL)
		if result.URL == "" || seen[result.URL] || !resultMatchesSiteScope(result.URL, scope) || !looksLikeCareerResult(result) ||
			(marketSearch && blockedMarketSearchAggregator(result.URL)) {
			continue
		}
		seen[result.URL] = true
		out = append(out, result)
		if len(out) >= max {
			return out
		}
	}
	return out
}

func resultMatchesSiteScope(rawURL string, scope string) bool {
	if scope == "" {
		return true
	}
	resultScope := sourceSiteScope(rawURL)
	return resultScope == scope || strings.HasPrefix(resultScope, scope+"/")
}

func looksLikeCareerResult(result tinyfish.SearchResult) bool {
	haystack := strings.ToLower(result.Title + " " + result.Snippet + " " + result.URL)
	if blockedTinyFishDiscoveryResult(result.URL, haystack) {
		return false
	}
	if looksLikeAggregateJobListing(result.Title, result.URL, result.Snippet) {
		return false
	}
	if !(strings.Contains(haystack, "job") || strings.Contains(haystack, "career") || strings.Contains(haystack, "opening") || strings.Contains(haystack, "position") || strings.Contains(haystack, "role") || strings.Contains(haystack, "internship") || strings.Contains(haystack, "new grad")) {
		return false
	}
	return strings.Contains(haystack, "software") || strings.Contains(haystack, "engineer") || strings.Contains(haystack, "intern") || strings.Contains(haystack, "graduate")
}

func postingFromTinyFishFetch(item tinyfish.FetchResult, search tinyfish.SearchResult, fetchedAt time.Time) (JobPosting, string, bool) {
	content := firstNonEmpty(item.Markdown, item.Text, item.Content, search.Snippet)
	title := tinyFishFetchedPostingTitle(item, search, content)
	haystack := strings.ToLower(title + "\n" + content + "\n" + search.Snippet)
	sourceURL := firstNonEmpty(item.URL, search.URL)
	if blockedTinyFishDiscoveryResult(sourceURL, haystack) {
		return JobPosting{}, "blocked_source", false
	}
	if !isLiveTinyFishPosting(haystack) {
		return JobPosting{}, "closed_or_not_live", false
	}
	if looksLikeTinyFishContentPage(title, sourceURL, content) {
		return JobPosting{}, "content_or_program_page", false
	}
	if !isSpecificEarlyCareerSoftwareRoleTitle(title) || !isEarlyCareerSoftwareText(haystack) {
		return JobPosting{}, "not_specific_early_career_software", false
	}
	if looksLikeAggregateJobListing(title, sourceURL, content) {
		return JobPosting{}, "aggregate_listing", false
	}

	company := tinyFishFetchedPostingCompany(title, item, search, sourceURL)
	location := inferLocationFromText(content)
	if location == "" || strings.EqualFold(location, "unknown") {
		return JobPosting{}, "missing_location", false
	}
	return JobPosting{
		SourceJobID: stableStringID(firstNonEmpty(item.URL, search.URL, title)),
		Company:     company,
		Title:       title,
		Location:    location,
		Country:     normalizeCountry("", location),
		SourceURL:   sourceURL,
		ApplyURL:    sourceURL,
		PostedAt:    parseFetchedPostedAt(content+"\n"+search.Snippet, fetchedAt),
		Live:        true,
		Confidence:  confidenceForFetchedPosting(haystack),
		Strategy:    TierSearchDiscovery,
		Evidence: []Evidence{
			{Field: "title", Text: title, URL: sourceURL},
			{Field: "summary", Text: compactSnippet(content), URL: sourceURL},
		},
	}, "", true
}

func looksLikeTinyFishContentPage(title string, rawURL string, content string) bool {
	normalizedTitle := strings.ToLower(strings.TrimSpace(title))
	normalizedURL := strings.ToLower(strings.TrimSpace(rawURL))
	normalizedContent := strings.ToLower(strings.TrimSpace(content))
	for _, phrase := range []string{
		"intern spotlight",
		"employee spotlight",
		"student spotlight",
		"life at ",
		"meet our interns",
		"intern stories",
		"university program",
		"internship program",
		"early career program",
		"futureforce internships",
	} {
		if strings.Contains(normalizedTitle, phrase) {
			return true
		}
	}
	if strings.Contains(normalizedContent, "does not list a specific open role") ||
		strings.Contains(normalizedContent, "browse open jobs to find a specific role") {
		return true
	}
	if strings.Contains(normalizedURL, "/university/internships") &&
		!strings.Contains(normalizedURL, "/jobs/") {
		return true
	}
	return false
}

func tinyFishRejectionEvidence(item tinyfish.FetchResult, search tinyfish.SearchResult, reason string) Evidence {
	sourceURL := firstNonEmpty(item.URL, search.URL)
	title := tinyFishFetchedPostingTitle(item, search, firstNonEmpty(item.Markdown, item.Text, item.Content, search.Snippet))
	text := reason
	if title != "" {
		text += ": " + title
	}
	return Evidence{
		Field: "tinyfish_rejection_sample",
		Text:  compactSnippet(text),
		URL:   sourceURL,
	}
}

func tinyFishFetchedPostingTitle(item tinyfish.FetchResult, search tinyfish.SearchResult, content string) string {
	company := companyFromJobURL(firstNonEmpty(item.URL, search.URL))
	for _, candidate := range tinyFishTitleCandidates(item, search, content) {
		title := stripKnownCompanySuffix(cleanTitle(candidate), company)
		if isSpecificEarlyCareerSoftwareRoleTitle(title) {
			return title
		}
	}
	return stripKnownCompanySuffix(cleanTitle(firstNonEmpty(item.Title, search.Title, firstLine(content))), company)
}

func tinyFishTitleCandidates(item tinyfish.FetchResult, search tinyfish.SearchResult, content string) []string {
	candidates := make([]string, 0, 8)
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if match := markdownHeadingPattern.FindStringSubmatch(trimmed); len(match) == 2 {
			candidates = append(candidates, match[1])
			continue
		}
		if len(candidates) == 0 {
			candidates = append(candidates, trimmed)
		}
		if len(candidates) >= 5 {
			break
		}
	}
	candidates = append(candidates, item.Title, search.Title)
	return candidates
}

func tinyFishFetchedPostingCompany(title string, item tinyfish.FetchResult, search tinyfish.SearchResult, sourceURL string) string {
	siteName := strings.TrimSpace(search.SiteName)
	if siteName != "" && !isGenericJobBoardName(siteName) {
		return siteName
	}
	return firstNonEmpty(
		companyFromJobURL(sourceURL),
		companyFromTitle(title),
		companyFromTitle(search.Title),
		companyFromURL(item.URL),
		companyFromURL(search.URL),
	)
}

func isGenericJobBoardName(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(lower, ".") && !strings.ContainsAny(lower, " \t") {
		return true
	}
	switch lower {
	case "", "jobs", "job board", "careers", "career page", "stripes", "stripes job board", "jobs.ashbyhq.com", "job-boards.greenhouse.io", "jobs.lever.co":
		return true
	default:
		return strings.HasSuffix(lower, " job board") ||
			strings.HasSuffix(lower, " jobs") ||
			strings.HasSuffix(lower, ".ashbyhq.com") ||
			strings.HasSuffix(lower, ".greenhouse.io") ||
			strings.HasSuffix(lower, ".lever.co")
	}
}

func companyFromJobURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	host := strings.ToLower(parsed.Hostname())
	if len(parts) > 0 {
		switch {
		case strings.Contains(host, "ashbyhq.com"):
			return hostedBoardCompanyName(parts[0])
		case strings.Contains(host, "greenhouse.io"):
			return hostedBoardCompanyName(parts[0])
		case strings.Contains(host, "lever.co"):
			return hostedBoardCompanyName(parts[0])
		}
	}
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "companies" || parts[i] == "company" {
			return titleWords(strings.ReplaceAll(strings.Trim(parts[i+1], "-_"), "-", " "))
		}
	}
	return ""
}

func hostedBoardCompanyName(slug string) string {
	slug = strings.ToLower(strings.Trim(strings.TrimSpace(slug), "-_"))
	if slug == "" {
		return ""
	}
	if strings.ContainsAny(slug, "-_") {
		return titleWords(strings.NewReplacer("-", " ", "_", " ").Replace(slug))
	}
	for _, suffix := range []string{"industries", "technologies", "technology", "securities", "systems", "science", "capital", "trading", "labs"} {
		if prefix := strings.TrimSuffix(slug, suffix); prefix != slug && len(prefix) >= 3 {
			return titleWords(prefix + " " + suffix)
		}
	}
	for _, item := range []struct{ suffix, label string }{{"ai", "AI"}, {"hq", "HQ"}} {
		if prefix := strings.TrimSuffix(slug, item.suffix); prefix != slug && len(prefix) >= 3 {
			return titleWords(prefix) + " " + item.label
		}
	}
	return titleWords(slug)
}

func blockedMarketSearchAggregator(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return true
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	for _, blocked := range []string{
		"aijobs.net", "bebee.com", "brightnetwork.co.uk", "builtin.com", "builtinaustin.com", "builtinchicago.org",
		"builtinla.com", "builtinsf.com", "builtinsingapore.com", "careerbuilder.com", "careerhub.students.duke.edu", "cryptocurrencyjobs.co",
		"deepfinresearch.com", "dice.com", "efinancialcareers.com", "expatjobboard.com", "extern.com", "glassdoor.com",
		"gradconnection.com", "handshake.com", "hiring.cafe", "hirify.me", "indeed.com", "interninsider.me", "internships.com",
		"jobright.ai", "jorb.ai", "levels.fyi", "linkedin.com", "monster.com", "notify.careers", "prosple.com",
		"remoterocketship.com", "ripplematch.com", "simplify.jobs", "spacecrew.com", "startup.jobs", "swiftcruit.ai", "talent.com",
		"targetjobs.co.uk", "tealhq.com", "themuse.com", "wayup.com", "wellfound.com", "wizbii.com",
		"welcometothejungle.com", "workatastartup.com", "ziprecruiter.com",
	} {
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return true
		}
	}
	return false
}

func stripKnownCompanySuffix(title string, company string) string {
	title = strings.TrimSpace(title)
	company = strings.TrimSpace(company)
	if title == "" || company == "" {
		return title
	}
	if strings.HasSuffix(strings.ToLower(title), " "+strings.ToLower(company)) {
		return strings.TrimSpace(title[:len(title)-len(company)])
	}
	return title
}

func blockedTinyFishDiscoveryResult(rawURL string, haystack string) bool {
	host, path := tinyFishSourceURLParts(rawURL)
	switch {
	case strings.Contains(host, "reddit.com"),
		strings.Contains(host, "teamblind.com"),
		strings.Contains(host, "news.ycombinator.com"),
		strings.Contains(host, "quora.com"),
		strings.Contains(host, "medium.com"):
		return true
	case host == "github.com" && !strings.Contains(path, "/jobs"):
		return true
	}
	lower := strings.ToLower(haystack)
	return strings.Contains(lower, "resume review") ||
		strings.Contains(lower, "cscareerquestions") ||
		strings.Contains(lower, "engineeringresumes")
}

func isLiveTinyFishPosting(haystack string) bool {
	for _, phrase := range []string{
		"no longer accepting applications",
		"no longer accepting applicants",
		"this job is closed",
		"this job has closed",
		"job is no longer available",
		"position is no longer available",
		"applications are closed",
		"application window has closed",
		"this position has been filled",
	} {
		if strings.Contains(haystack, phrase) {
			return false
		}
	}
	return true
}

func looksLikeAggregateJobListing(title string, rawURL string, content string) bool {
	lowerTitle := strings.ToLower(title)
	lowerContent := strings.ToLower(content)
	_, path := tinyFishSourceURLParts(rawURL)
	switch {
	case aggregateJobListingPattern.MatchString(lowerTitle):
		return true
	case strings.Contains(lowerTitle, "jobs (with salaries)"),
		strings.HasPrefix(lowerTitle, "discover ") && strings.Contains(lowerTitle, " jobs"),
		strings.Contains(lowerTitle, " job openings"),
		strings.Contains(lowerTitle, "jobs in ") && !strings.Contains(lowerContent, "apply now"):
		return true
	case strings.Contains(path, "/q-") && strings.HasSuffix(path, "-jobs.html"):
		return true
	}
	return false
}

func tinyFishSourceURLParts(rawURL string) (string, string) {
	parsed, err := url.Parse(strings.TrimSpace(strings.ToLower(rawURL)))
	if err != nil || parsed.Hostname() == "" {
		return "", ""
	}
	return parsed.Hostname(), parsed.EscapedPath()
}

func postingsFromTinyFishAIContent(item tinyfish.FetchResult, source Source, fetchedAt time.Time, limit int) []JobPosting {
	if limit <= 0 {
		limit = defaultTinyFishAIBlockLimit
	}
	content := firstNonEmpty(item.Markdown, item.Text, item.Content)
	blocks := aiJobBlocks(content, limit)
	jobs := make([]JobPosting, 0, len(blocks))
	seen := make(map[string]bool, len(blocks))
	for _, block := range blocks {
		posting, ok := postingFromAIBlock(block, item, source, fetchedAt)
		if !ok || seen[posting.SourceJobID] {
			continue
		}
		seen[posting.SourceJobID] = true
		jobs = append(jobs, posting)
	}
	return jobs
}

type aiJobBlock struct {
	title string
	text  string
}

func aiJobBlocks(content string, limit int) []aiJobBlock {
	lines := strings.Split(content, "\n")
	var blocks []aiJobBlock
	var currentTitle string
	var current []string
	flush := func() {
		if strings.TrimSpace(currentTitle) == "" || len(blocks) >= limit {
			current = nil
			return
		}
		text := strings.TrimSpace(strings.Join(current, "\n"))
		blocks = append(blocks, aiJobBlock{title: currentTitle, text: text})
		current = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(current) > 0 {
				current = append(current, "")
			}
			continue
		}
		if title, ok := aiHeadingTitle(trimmed); ok {
			flush()
			currentTitle = title
			continue
		}
		if currentTitle != "" {
			current = append(current, trimmed)
		}
	}
	flush()
	return blocks
}

func aiHeadingTitle(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		trimmed = strings.TrimLeft(trimmed, "#")
	} else if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* "))
	} else {
		return "", false
	}
	trimmed = cleanTitle(trimmed)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if !isSoftwareRoleTitle(lower) {
		return "", false
	}
	if strings.Contains(lower, "office manager") || strings.Contains(lower, "recruiter") || strings.Contains(lower, "sales") {
		return "", false
	}
	return trimmed, true
}

func postingFromAIBlock(block aiJobBlock, item tinyfish.FetchResult, source Source, fetchedAt time.Time) (JobPosting, bool) {
	haystack := strings.ToLower(block.title + "\n" + block.text)
	if !isSpecificEarlyCareerSoftwareRoleTitle(block.title) || !isEarlyCareerSoftwareText(haystack) {
		return JobPosting{}, false
	}
	applyURL := firstNonEmpty(extractApplyURL(block.text), item.URL, source.URL)
	if strings.TrimSpace(applyURL) == "" {
		return JobPosting{}, false
	}
	sourceURL := firstNonEmpty(item.URL, source.URL, applyURL)
	company := firstNonEmpty(source.Name, companyFromURL(source.URL), companyFromURL(item.URL), companyFromTitle(item.Title))
	location := firstNonEmpty(extractLocationLine(block.text), inferLocationFromText(block.text))
	if location == "" || strings.EqualFold(strings.TrimSpace(location), "unknown") {
		return JobPosting{}, false
	}
	if strings.TrimRight(applyURL, "/") == strings.TrimRight(sourceURL, "/") && isTinyFishCareerListingRoot(applyURL) {
		return JobPosting{}, false
	}
	identity := applyURL
	if strings.TrimRight(applyURL, "/") == strings.TrimRight(sourceURL, "/") {
		identity += "\n" + block.title
	}
	return JobPosting{
		SourceJobID: stableStringID(firstNonEmpty(identity, sourceURL, company+" "+block.title)),
		Company:     company,
		Title:       block.title,
		Location:    location,
		Country:     normalizeCountry("", location),
		SourceURL:   sourceURL,
		ApplyURL:    applyURL,
		PostedAt:    parseFetchedPostedAt(block.text, fetchedAt),
		Live:        true,
		Confidence:  confidenceForAIBlock(haystack),
		Strategy:    TierAIExtraction,
		Evidence: []Evidence{
			{Field: "ai_title", Text: block.title, URL: sourceURL},
			{Field: "ai_block", Text: compactSnippet(block.text), URL: sourceURL},
		},
	}, true
}

func isTinyFishCareerListingRoot(rawURL string) bool {
	_, path := tinyFishSourceURLParts(rawURL)
	path = strings.Trim(strings.ToLower(path), "/")
	switch path {
	case "careers", "jobs", "careers/jobs", "join-us/jobs", "open-positions", "career-listing",
		"company/careers", "company/careers/jobs", "careers/search", "careers/search-results":
		return true
	default:
		return false
	}
}

func isSoftwareRoleTitle(lower string) bool {
	return strings.Contains(lower, "software") ||
		strings.Contains(lower, "backend") ||
		strings.Contains(lower, "frontend") ||
		strings.Contains(lower, "full stack") ||
		strings.Contains(lower, "full-stack") ||
		strings.Contains(lower, "platform") ||
		strings.Contains(lower, "infrastructure") ||
		strings.Contains(lower, "machine learning") ||
		strings.HasPrefix(lower, "ai ") ||
		strings.Contains(lower, " ai ") ||
		strings.Contains(lower, "llm") ||
		strings.Contains(lower, "data engineer") ||
		strings.Contains(lower, "quant")
}

func isSpecificEarlyCareerSoftwareRoleTitle(title string) bool {
	lower := strings.ToLower(strings.TrimSpace(title))
	if lower == "" || isGenericTinyFishRoleTitle(lower) {
		return false
	}
	if !isSoftwareRoleTitle(lower) {
		return false
	}
	return strings.Contains(lower, "intern") ||
		strings.Contains(lower, "new grad") ||
		strings.Contains(lower, "new-grad") ||
		strings.Contains(lower, "graduate") ||
		strings.Contains(lower, "university grad") ||
		strings.Contains(lower, "entry level") ||
		strings.Contains(lower, "early career") ||
		strings.Contains(lower, "co-op") ||
		strings.Contains(lower, "coop")
}

func isGenericTinyFishRoleTitle(lower string) bool {
	lower = strings.Trim(strings.TrimSpace(lower), "-| ")
	switch lower {
	case "", "jobs", "job", "careers", "career", "open roles", "open positions", "job board", "stripes job board", "software engineering early career role":
		return true
	}
	return strings.HasPrefix(lower, "jobs at ") ||
		strings.HasPrefix(lower, "careers at ") ||
		strings.HasSuffix(lower, " careers") ||
		strings.HasSuffix(lower, " job board") ||
		strings.Contains(lower, "search results")
}

func extractApplyURL(text string) string {
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "apply") {
			continue
		}
		if match := firstURLPattern.FindString(line); match != "" {
			return strings.TrimRight(match, ".,)")
		}
	}
	return firstURLPattern.FindString(text)
}

func extractLocationLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		before, after, ok := strings.Cut(line, ":")
		if !ok || !strings.Contains(strings.ToLower(before), "location") {
			continue
		}
		if location := normalizeSpace(after); location != "" {
			return location
		}
	}
	return ""
}

func confidenceForAIBlock(haystack string) float64 {
	confidence := 0.66
	if strings.Contains(haystack, "apply") {
		confidence += 0.05
	}
	if strings.Contains(haystack, "posted") {
		confidence += 0.04
	}
	if strings.Contains(haystack, "intern") || strings.Contains(haystack, "new grad") {
		confidence += 0.05
	}
	if confidence > 0.82 {
		return 0.82
	}
	return confidence
}

func confidenceForAIExtraction(jobs []JobPosting) float64 {
	if len(jobs) == 0 {
		return 0
	}
	best := 0.68
	for _, job := range jobs {
		if job.Confidence > best {
			best = job.Confidence
		}
	}
	return best
}

type tinyFishAgentPayload struct {
	Jobs []tinyFishAgentJob `json:"jobs"`
}

type tinyFishAgentJob struct {
	SourceJobID    string  `json:"source_job_id,omitempty"`
	Company        string  `json:"company"`
	Title          string  `json:"title"`
	Location       string  `json:"location,omitempty"`
	Country        string  `json:"country,omitempty"`
	EmploymentType string  `json:"employment_type,omitempty"`
	Level          string  `json:"level,omitempty"`
	RoleFamily     string  `json:"role_family,omitempty"`
	SourceURL      string  `json:"source_url,omitempty"`
	ApplyURL       string  `json:"apply_url,omitempty"`
	PostedAt       string  `json:"posted_at,omitempty"`
	Live           *bool   `json:"live,omitempty"`
	Confidence     float64 `json:"confidence,omitempty"`
	Evidence       string  `json:"evidence,omitempty"`
}

func tinyFishAgentGoal(source Source) string {
	if goal := strings.TrimSpace(source.Metadata["goal"]); goal != "" {
		return goal
	}
	return "Inspect this careers or jobs page like a careful recruiter. Extract only live software engineering internship, new-grad, early-career, infrastructure, backend, frontend, ML, data, AI, or quant engineering roles. Return JSON exactly as {\"jobs\":[{\"company\":string,\"title\":string,\"location\":string,\"country\":string,\"apply_url\":string,\"source_url\":string,\"employment_type\":string,\"level\":string,\"role_family\":string,\"posted_at\":string,\"live\":boolean,\"confidence\":number,\"evidence\":string}]}. Do not apply to anything."
}

func tinyFishAgentJobSchema() map[string]any {
	jobProperties := map[string]any{
		"company":         map[string]any{"type": "string"},
		"title":           map[string]any{"type": "string"},
		"location":        map[string]any{"type": "string"},
		"country":         map[string]any{"type": "string"},
		"apply_url":       map[string]any{"type": "string"},
		"source_url":      map[string]any{"type": "string"},
		"employment_type": map[string]any{"type": "string"},
		"level":           map[string]any{"type": "string"},
		"role_family":     map[string]any{"type": "string"},
		"posted_at":       map[string]any{"type": "string"},
		"live":            map[string]any{"type": "boolean"},
		"confidence":      map[string]any{"type": "number"},
		"evidence":        map[string]any{"type": "string"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"jobs": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":       "object",
					"properties": jobProperties,
					"required":   []string{"company", "title", "apply_url"},
				},
			},
		},
		"required": []string{"jobs"},
	}
}

func postingsFromTinyFishAgent(response tinyfish.AutomationRunResponse, source Source) ([]JobPosting, []Evidence, error) {
	raw := response.Result
	if len(raw) == 0 {
		raw = response.ResultJSON
	}
	if len(raw) == 0 {
		return nil, agentEvidence(response, source), ErrNoJobs
	}

	var payload tinyFishAgentPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		var direct []tinyFishAgentJob
		if listErr := json.Unmarshal(raw, &direct); listErr != nil {
			extracted, extractErr := extractTinyFishAgentJSON(raw)
			if extractErr != nil {
				return nil, nil, err
			}
			if jsonErr := json.Unmarshal(extracted, &payload); jsonErr != nil {
				if listErr := json.Unmarshal(extracted, &direct); listErr != nil {
					return nil, nil, jsonErr
				}
			}
		}
		if len(direct) > 0 {
			payload.Jobs = direct
		}
	}

	jobs := make([]JobPosting, 0, len(payload.Jobs))
	evidence := agentEvidence(response, source)
	for _, item := range payload.Jobs {
		posting, ok := postingFromTinyFishAgentJob(item, source)
		if !ok {
			continue
		}
		jobs = append(jobs, posting)
		if strings.TrimSpace(item.Evidence) != "" {
			evidence = append(evidence, Evidence{Field: "agent_job", Text: item.Evidence, URL: posting.SourceURL})
		}
	}
	if len(jobs) == 0 {
		return nil, evidence, ErrNoJobs
	}
	return jobs, evidence, nil
}

func extractTinyFishAgentJSON(raw []byte) ([]byte, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, ErrNoJobs
	}
	candidates := make([]int, 0, 2)
	for _, opener := range []byte{'{', '['} {
		if idx := strings.IndexByte(text, opener); idx >= 0 {
			candidates = append(candidates, idx)
		}
	}
	if len(candidates) == 0 {
		return nil, errors.New("tinyfish agent result did not contain JSON")
	}
	start := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate < start {
			start = candidate
		}
	}
	end := balancedJSONEnd(text[start:])
	if end <= 0 {
		return nil, errors.New("tinyfish agent result contained incomplete JSON")
	}
	return []byte(text[start : start+end]), nil
}

func balancedJSONEnd(value string) int {
	if value == "" {
		return 0
	}
	var stack []byte
	inString := false
	escaped := false
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) == 0 || stack[len(stack)-1] != ch {
				return 0
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1
			}
		}
	}
	return 0
}

func postingFromTinyFishAgentJob(item tinyFishAgentJob, source Source) (JobPosting, bool) {
	title := strings.TrimSpace(item.Title)
	company := strings.TrimSpace(item.Company)
	sourceURL := firstNonEmpty(item.SourceURL, source.URL)
	applyURL := firstNonEmpty(item.ApplyURL, sourceURL)
	if title == "" || company == "" || applyURL == "" {
		return JobPosting{}, false
	}
	live := true
	if item.Live != nil {
		live = *item.Live
	}
	confidence := item.Confidence
	if confidence == 0 {
		confidence = 0.66
	}
	return JobPosting{
		SourceJobID:    stableStringID(firstNonEmpty(item.SourceJobID, applyURL, sourceURL, company+" "+title)),
		Company:        company,
		Title:          title,
		Location:       item.Location,
		Country:        item.Country,
		EmploymentType: item.EmploymentType,
		Level:          item.Level,
		RoleFamily:     item.RoleFamily,
		SourceURL:      sourceURL,
		ApplyURL:       applyURL,
		PostedAt:       parseAgentPostedAt(item.PostedAt),
		Live:           live,
		Confidence:     confidence,
		Strategy:       TierBrowserAgent,
		Evidence: []Evidence{
			{Field: "agent_evidence", Text: item.Evidence, URL: sourceURL},
		},
	}, true
}

func agentEvidence(response tinyfish.AutomationRunResponse, source Source) []Evidence {
	evidence := []Evidence{
		{Field: "agent_run_id", Text: response.RunID, URL: source.URL},
		{Field: "agent_status", Text: response.Status, URL: source.URL},
	}
	evidence = append(evidence, Evidence{Field: "agent_mode", Text: firstNonEmpty(response.Mode, source.Metadata["agent_mode"], "sync"), URL: source.URL})
	if response.Polls > 0 {
		evidence = append(evidence, Evidence{Field: "agent_polls", Text: fmt.Sprintf("%d", response.Polls), URL: source.URL})
	}
	if response.CancelStatus != "" {
		evidence = append(evidence, Evidence{Field: "agent_cancel_status", Text: response.CancelStatus, URL: source.URL})
	}
	if response.NumOfSteps > 0 {
		evidence = append(evidence, Evidence{Field: "agent_steps", Text: fmt.Sprintf("%d", response.NumOfSteps), URL: source.URL})
	}
	return evidence
}

func confidenceForAgentRun(response tinyfish.AutomationRunResponse, jobs []JobPosting) float64 {
	best := 0.68
	for _, job := range jobs {
		if job.Confidence > best {
			best = job.Confidence
		}
	}
	if strings.EqualFold(response.Status, "COMPLETED") && best < 0.72 {
		best = 0.72
	}
	if best > 0.9 {
		return 0.9
	}
	return best
}

func parseAgentPostedAt(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return &parsed
		}
	}
	return nil
}

func parseFetchedPostedAt(content string, now time.Time) *time.Time {
	content = whitespace.ReplaceAllString(strings.TrimSpace(content), " ")
	if content == "" {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, pattern := range []string{
		`(?i)\b(?:posted|published|updated|date posted)\s*(?:on|at|:)?\s*(\d{4}-\d{2}-\d{2})\b`,
		`(?i)\b(\d{4}-\d{2}-\d{2})\b`,
	} {
		if parsed := parseFetchedDateMatch(content, pattern, []string{time.DateOnly}); parsed != nil {
			return parsed
		}
	}
	if parsed := parseFetchedDateMatch(content, `(?i)\b(?:posted|published|updated|date posted)\s*(?:on|at|:)?\s*([A-Z][a-z]+ \d{1,2}, \d{4})\b`, []string{"January 2, 2006"}); parsed != nil {
		return parsed
	}
	if parsed := parseFetchedDateMatch(content, `(?i)\b(?:posted|published|updated|date posted)\s*(?:on|at|:)?\s*([A-Z][a-z]{2} \d{1,2}, \d{4})\b`, []string{"Jan 2, 2006"}); parsed != nil {
		return parsed
	}
	if match := regexp.MustCompile(`(?i)\bposted\s+(\d{1,3})\s+(day|days|week|weeks)\s+ago\b`).FindStringSubmatch(content); len(match) == 3 {
		amount, err := strconv.Atoi(match[1])
		if err != nil {
			return nil
		}
		days := amount
		if strings.HasPrefix(strings.ToLower(match[2]), "week") {
			days *= 7
		}
		posted := now.UTC().AddDate(0, 0, -days)
		return &posted
	}
	return nil
}

func parseFetchedDateMatch(content string, pattern string, layouts []string) *time.Time {
	match := regexp.MustCompile(pattern).FindStringSubmatch(content)
	if len(match) < 2 {
		return nil
	}
	value := strings.TrimSpace(match[1])
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return &parsed
		}
	}
	return nil
}

func isEarlyCareerSoftwareText(value string) bool {
	hasSoftware := strings.Contains(value, "software") || strings.Contains(value, "backend") || strings.Contains(value, "frontend") || strings.Contains(value, "full stack") || strings.Contains(value, "machine learning") || strings.Contains(value, "infrastructure")
	hasEarlyCareer := strings.Contains(value, "intern") || strings.Contains(value, "new grad") || strings.Contains(value, "graduate") || strings.Contains(value, "early career")
	return hasSoftware && hasEarlyCareer
}

func confidenceForFetchedPosting(value string) float64 {
	score := 0.68
	if strings.Contains(value, "apply") || strings.Contains(value, "job") {
		score += 0.05
	}
	if strings.Contains(value, "intern") || strings.Contains(value, "new grad") {
		score += 0.08
	}
	if strings.Contains(value, "2026") {
		score += 0.04
	}
	if score > 0.88 {
		return 0.88
	}
	return score
}

func cleanTitle(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSpace(strings.TrimLeft(value, "#"))
	value = strings.Trim(value, "-| ")
	if value == "" {
		return "Software Engineering Early Career Role"
	}
	parts := regexp.MustCompile(`\s+[-|]\s+`).Split(value, 2)
	title := strings.TrimSpace(parts[0])
	if idx := strings.LastIndex(strings.ToLower(title), " at "); idx > 0 {
		title = strings.TrimSpace(title[:idx])
	}
	return title
}

func companyFromTitle(title string) string {
	lower := strings.ToLower(title)
	if idx := strings.LastIndex(lower, " at "); idx >= 0 && idx+4 < len(title) {
		return strings.TrimSpace(title[idx+4:])
	}
	return ""
}

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

func inferLocationFromText(content string) string {
	lower := strings.ToLower(content)
	switch {
	case strings.Contains(lower, "singapore"):
		return "Singapore"
	case strings.Contains(lower, "hong kong"):
		return "Hong Kong"
	case strings.Contains(lower, "london"), strings.Contains(lower, "united kingdom"):
		return "London, United Kingdom"
	case strings.Contains(lower, "toronto"):
		return "Toronto, Canada"
	case strings.Contains(lower, "vancouver"):
		return "Vancouver, Canada"
	case strings.Contains(lower, "san francisco"):
		return "San Francisco, CA, United States"
	case strings.Contains(lower, "new york"):
		return "New York, NY, United States"
	case strings.Contains(lower, "remote us"), strings.Contains(lower, "united states"):
		return "United States"
	default:
		return "unknown"
	}
}

func compactSnippet(content string) string {
	content = whitespace.ReplaceAllString(strings.TrimSpace(content), " ")
	if len(content) <= 500 {
		return content
	}
	return strings.TrimSpace(content[:500])
}

func firstLine(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if idx := strings.IndexByte(content, '\n'); idx >= 0 {
		return strings.TrimSpace(content[:idx])
	}
	return content
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeSpace(value string) string {
	return whitespace.ReplaceAllString(strings.TrimSpace(value), " ")
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
