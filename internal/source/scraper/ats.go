package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hwennnn/radar/internal/source/provider"
	atsprovider "github.com/hwennnn/radar/internal/source/provider/ats"
	workdayprovider "github.com/hwennnn/radar/internal/source/provider/workday"
)

var (
	ErrUnsupportedATS                  = errors.New("unsupported ats source")
	defaultATSHTTPTimeout              = 10 * time.Second
	htmlTagPattern                     = regexp.MustCompile(`<[^>]+>`)
	anchorTagPattern                   = regexp.MustCompile(`(?is)<a\b[^>]*>.*?</a>`)
	hrefAttrPattern                    = regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["']`)
	jobviteTitlePattern                = regexp.MustCompile(`(?is)<h[12][^>]*class=["'][^"']*jv-header[^"']*["'][^>]*>(.*?)</h[12]>`)
	jobviteCompanyNamePattern          = regexp.MustCompile(`(?is)function\s+getCompanyName\(\)\s*\{\s*return\s*["']([^"']+)["']`)
	teamtailorApplyPattern             = regexp.MustCompile(`(?is)data-careersite--jobs--form-overlay-job-application-url-value=["']([^"']+)["']`)
	greenhouseAvailableLocationPattern = regexp.MustCompile(`(?i)\bavailable locations?\s*:\s*(.{1,120}?)(?:\s+(?:about(?:\s+the)?\s+(?:company|department|role|team)|department|the role|what you|who you|responsibilities)\b|$)`)
	nextDataScriptPattern              = regexp.MustCompile(`(?is)<script[^>]+id=["']__NEXT_DATA__["'][^>]*>(.*?)</script>`)
	googleCareersCardPattern           = regexp.MustCompile(`(?is)<li\b[^>]*class=["'][^"']*\blLd3Je\b[^"']*["'][^>]*>`)
	googleCareersTitlePattern          = regexp.MustCompile(`(?is)<h3\b[^>]*class=["'][^"']*\bQJPWVe\b[^"']*["'][^>]*>(.*?)</h3>`)
	googleCareersDetailTitlePattern    = regexp.MustCompile(`(?is)<h1\b[^>]*>(.*?)</h1>`)
	googleCareersIDPattern             = regexp.MustCompile(`(?is)\bjsdata=["'][^"']*Aiqs8c;([^;"']+)`)
	googleCareersHrefIDPattern         = regexp.MustCompile(`(?is)(?:^|/)jobs/results/([^/?#"'&]+)`)
	googleCareersLinkPattern           = regexp.MustCompile(`(?is)<a\b[^>]*class=["'][^"']*\bWpHeLc\b[^"']*["'][^>]*>`)
	googleCareersLocationPattern       = regexp.MustCompile(`(?is)<span\b[^>]*class=["'][^"']*\br0wTof\b[^"']*["'][^>]*>(.*?)</span>`)
	googleCareersRequirementsPattern   = regexp.MustCompile(`(?is)<div\b[^>]*class=["'][^"']*\bXsxa1e\b[^"']*["'][^>]*>(.*?)</div>`)
	googleCareersSectionPattern        = regexp.MustCompile(`(?is)<(?:section|div)\b[^>]*(?:data-test-id|class)=["'][^"']*(?:job-detail|description|qualifications|responsibilities|about)[^"']*["'][^>]*>(.*?)</(?:section|div)>`)
	worldQuantCareersListPattern       = regexp.MustCompile(`(?is)<ul\b[^>]*\bid=["']careers_list["'][^>]*>(.*?)</ul>`)
	worldQuantCareersLinkPattern       = regexp.MustCompile(`(?is)<a\b[^>]*class=["'][^"']*\bfo-link\b[^"']*["'][^>]*>.*?</a>`)
	worldQuantCareersTitlePattern      = regexp.MustCompile(`(?is)<h4\b[^>]*>(.*?)</h4>`)
	worldQuantCareersLocationPattern   = regexp.MustCompile(`(?is)<span\b[^>]*class=["'][^"']*\bfo-location\b[^"']*["'][^>]*>(.*?)</span>`)
	imcCareersCardPattern              = regexp.MustCompile(`(?is)<a\b[^>]*href=["'](/(?:us|eu|ap|in)/careers/jobs/(\d+))["'][^>]*>(.*?)</a>`)
	imcCareersTitlePattern             = regexp.MustCompile(`(?is)<h2\b[^>]*>(.*?)</h2>`)
	imcCareersSpanPattern              = regexp.MustCompile(`(?is)<span\b[^>]*>(.*?)</span>`)
	freshteamTitlePattern              = regexp.MustCompile(`(?is)<(?:span|h[1-6]|div)\b[^>]*class=["'][^"']*(?:job-title|job_name|job-name|title)[^"']*["'][^>]*>(.*?)</(?:span|h[1-6]|div)>`)
	freshteamHeadingPattern            = regexp.MustCompile(`(?is)<h[1-6]\b[^>]*>(.*?)</h[1-6]>`)
	freshteamLocationPattern           = regexp.MustCompile(`(?is)<(?:span|div|p)\b[^>]*class=["'][^"']*(?:job-location|location)[^"']*["'][^>]*>(.*?)</(?:span|div|p)>`)
	freshteamEmploymentPattern         = regexp.MustCompile(`(?is)<(?:span|div|p)\b[^>]*class=["'][^"']*(?:job-type|employment|work-type)[^"']*["'][^>]*>(.*?)</(?:span|div|p)>`)
	applicantProDomainIDPattern        = regexp.MustCompile(`(?is)["']?domain_id["']?\s*[:=]\s*["']?(\d+)`)
	zohoRecruitJobsInputPattern        = regexp.MustCompile(`(?is)<input\b[^>]*\bid=["']jobs["'][^>]*>`)
	manatalJobIDPattern                = regexp.MustCompile(`(?is)(?:^|/)jobs/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?:$|[/?#])`)
	occupopRowPattern                  = regexp.MustCompile(`(?is)<tr\b[^>]*>\s*<td>\s*<h4\b[^>]*class=["'][^"']*\btitle\b[^"']*["'][^>]*>(.*?)</h4>.*?</tr>`)
	occupopSmallPattern                = regexp.MustCompile(`(?is)<small\b[^>]*class=["'][^"']*\b(location|category|type)\b[^"']*["'][^>]*>(.*?)</small>`)
	workstreamCardStartPattern         = regexp.MustCompile(`(?is)<div\b[^>]*class=["'][^"']*\bposition-card\b[^"']*["'][^>]*>`)
	workstreamAddressPattern           = regexp.MustCompile(`(?is)<div\b[^>]*class=["'][^"']*\bposition-address\b[^"']*["'][^>]*>(.*?)</div>`)
	workstreamDescriptionPattern       = regexp.MustCompile(`(?is)<div\b[^>]*class=["'][^"']*\bposition-short-desc\b[^"']*["'][^>]*>(.*?)</div>`)
	workstreamTagPattern               = regexp.MustCompile(`(?is)<span\b[^>]*class=["'][^"']*\btag\b[^"']*["'][^>]*>(.*?)</span>`)
	workstreamPayPattern               = regexp.MustCompile(`(?is)data-icon=["']rate-of-pay["'][^>]*>\s*</img>\s*<span\b[^>]*>(.*?)</span>`)
	workstreamShortDetailIDPattern     = regexp.MustCompile(`(?i)^[a-f0-9]{6,}$`)
	stripeJobsRowPattern               = regexp.MustCompile(`(?is)<tr\b[^>]*class=["'][^"']*\bTableRow\b[^"']*["'][^>]*>(.*?)</tr>`)
	stripeJobsLinkPattern              = regexp.MustCompile(`(?is)<a\b[^>]*class=["'][^"']*\bJobsListings__link\b[^"']*["'][^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	stripeJobsDepartmentPattern        = regexp.MustCompile(`(?is)<td\b[^>]*class=["'][^"']*\bJobsListings__tableCell--departments\b[^"']*["'][^>]*>(.*?)</td>`)
	stripeJobsCountryPattern           = regexp.MustCompile(`(?is)<img\b[^>]*class=["'][^"']*\bFlag\b[^"']*["'][^>]*alt=["']([^"']+)["']`)
	stripeJobsLocationPattern          = regexp.MustCompile(`(?is)<span\b[^>]*class=["'][^"']*\bJobsListings__locationDisplayName\b[^"']*["'][^>]*>(.*?)</span>`)
	careerPlugNumericIDPattern         = regexp.MustCompile(`^\d+$`)
	paycomSessionJWTPattern            = regexp.MustCompile(`"sessionJWT"\s*:\s*"([^"]+)"`)
	paycomLibConfigPattern             = regexp.MustCompile(`"libConfig"\s*:\s*"((?:\\.|[^"])*)"`)
	paycomClientKeyPattern             = regexp.MustCompile(`(?i)(?:clientkey=|/portal/)([A-Z0-9]{12,})`)
	avatureDetailIDPattern             = regexp.MustCompile(`(?is)(?:^|/)JobDetail/[^/?#]+/([^/?#]+)(?:$|[/?#])`)
	avatureApplyPattern                = regexp.MustCompile(`(?is)<a\b[^>]*href=["']([^"']+)["'][^>]*>\s*Apply Now\s*</a>`)
	avatureRichTextPattern             = regexp.MustCompile(`(?is)<div\b[^>]*class=["'][^"']*\bfield--rich-text\b[^"']*["'][^>]*>.*?<div\b[^>]*class=["'][^"']*\barticle__content__view__field__value\b[^"']*["'][^>]*>\s*(.*?)\s*</div>\s*</div>`)
	hireologyStartingDataPattern       = regexp.MustCompile(`(?is)var\s+startingData\s*=\s*(\{.*?\});`)
	hireologyDetailIDPattern           = regexp.MustCompile(`(?is)(?:^|/)(\d+)/description(?:$|[/?#])`)
	githubJobListRowPattern            = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
	githubJobListCellPattern           = regexp.MustCompile(`(?is)<td\b[^>]*>(.*?)</td>`)
	githubJobListHrefPattern           = regexp.MustCompile(`(?is)<a\b[^>]+href=["']([^"']+)["'][^>]*>`)
	githubJobListMarkdownLinkPattern   = regexp.MustCompile(`\[[^\]]+\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	githubJobListMarkdownTextPattern   = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
)

var supportedATSSourceKinds = map[string]struct{}{
	"adp_my_jobs":                {},
	"adp_myjobs":                 {},
	"adp_workforce_now":          {},
	"adp_workforcenow":           {},
	"akuna_careers":              {},
	"amazon":                     {},
	"amazon_jobs":                {},
	"apple":                      {},
	"apple_jobs":                 {},
	"applicant_pro":              {},
	"applicantpro":               {},
	"ashby":                      {},
	"avature":                    {},
	"bamboohr":                   {},
	"breezy":                     {},
	"bytedance_careers":          {},
	"careerplug":                 {},
	"catsone":                    {},
	"citadel_careers":            {},
	"citadel_securities_careers": {},
	"comeet":                     {},
	"dayforce":                   {},
	"dover":                      {},
	"directemployers":            {},
	"eightfold":                  {},
	"eightfold_apply":            {},
	"eightfold_pcsx":             {},
	"fountain":                   {},
	"freshteam":                  {},
	"gem":                        {},
	"github_job_list":            {},
	"github_jobs":                {},
	"google":                     {},
	"google_careers":             {},
	"greenhouse":                 {},
	"hibob":                      {},
	"hibob_hiring":               {},
	"hireology":                  {},
	"homerun":                    {},
	"ibm_careers":                {},
	"icims":                      {},
	"imc_careers":                {},
	"jazzhr":                     {},
	"janestreet_careers":         {},
	"jibe":                       {},
	"jobscore":                   {},
	"jobsoid":                    {},
	"jobvite":                    {},
	"jobylon":                    {},
	"jobsyn":                     {},
	"join":                       {},
	"join_com":                   {},
	"lever":                      {},
	"manatal":                    {},
	"meta_careers":               {},
	"microsoft":                  {},
	"microsoft_careers":          {},
	"myjobs":                     {},
	"occupop":                    {},
	"openai":                     {},
	"openai_careers":             {},
	"optiver_careers":            {},
	"oracle":                     {},
	"oracle_cloud_recruiting":    {},
	"oracle_recruiting":          {},
	"orc":                        {},
	"paycom":                     {},
	"paylocity":                  {},
	"pcsx":                       {},
	"personio":                   {},
	"phenom":                     {},
	"phenom_people":              {},
	"phenompeople":               {},
	"pinpoint":                   {},
	"polymer":                    {},
	"radancy":                    {},
	"recruiterbox":               {},
	"recruitee":                  {},
	"rippling":                   {},
	"rippling_hosted":            {},
	"rippling_jobs":              {},
	"smartrecruiters":            {},
	"snowflake_careers":          {},
	"stripe_jobs":                {},
	"successfactors":             {},
	"talentbrew":                 {},
	"talent_lyft":                {},
	"talentlyft":                 {},
	"taleo":                      {},
	"teamtailor":                 {},
	"trakstar":                   {},
	"ukg":                        {},
	"ukg_pro":                    {},
	"ultipro":                    {},
	"workable":                   {},
	"workable_jobs":              {},
	"workday":                    {},
	"workforcenow":               {},
	"workstream":                 {},
	"walmart_careers":            {},
	"whatnot_careers":            {},
	"worldquant_careers":         {},
	"zoho_recruit":               {},
	"zohorecruit":                {},
}

func SupportedATSSourceKinds() map[string]struct{} {
	out := make(map[string]struct{}, len(supportedATSSourceKinds))
	for kind := range supportedATSSourceKinds {
		out[kind] = struct{}{}
	}
	return out
}

type ATSOptions struct {
	Client                       *http.Client
	GreenhouseBaseURL            string
	LeverGlobalBaseURL           string
	LeverEuropeBaseURL           string
	AshbyJobBoardBaseURL         string
	WorkablePublicBaseURL        string
	WorkableJobsBaseURL          string
	WorkableJobsMaxPages         int
	WorkableJobsMaxJobs          int
	RecruiteeBaseURL             string
	ComeetBaseURL                string
	SmartRecruitersBaseURL       string
	SmartRecruitersMaxJobs       int
	SmartRecruitersDetailMaxJobs int
	ComeetHostedMaxJobs          int
	PolymerPublicBaseURL         string
	WorkdayPageSize              int
	WorkdayMaxPages              int
	WorkdayDetailMaxJobs         int
	WorkdayDetailTimeout         time.Duration
	PolymerMaxPages              int
	ICIMSMaxJobs                 int
	JazzHRMaxJobs                int
	JobviteMaxJobs               int
	TeamtailorMaxJobs            int
	BambooHRMaxJobs              int
	RipplingPageSize             int
	RipplingMaxPages             int
	RipplingMaxJobs              int
	SuccessFactorsMaxJobs        int
	ADPWorkforceNowPageSize      int
	ADPWorkforceNowMaxPages      int
	ADPWorkforceNowMaxJobs       int
	ADPMyJobsConfigBaseURL       string
	ADPMyJobsAPIBaseURL          string
	ADPMyJobsPageSize            int
	ADPMyJobsMaxPages            int
	ADPMyJobsMaxJobs             int
	UKGMaxJobs                   int
	DayforcePageSize             int
	DayforceMaxPages             int
	DayforceMaxJobs              int
	ByteDanceBaseURL             string
	ByteDancePageSize            int
	ByteDanceMaxPages            int
	ByteDanceMaxJobs             int
	OracleRecruitingPageSize     int
	OracleRecruitingMaxPages     int
	OracleRecruitingMaxJobs      int
	PaylocityMaxJobs             int
	PaycomMaxJobs                int
	PaycomDetailMaxJobs          int
	AvatureMaxJobs               int
	AvatureDetailMaxJobs         int
	JobylonMaxJobs               int
	ZohoRecruitMaxJobs           int
	ManatalMaxJobs               int
	JOINMaxJobs                  int
	OccupopMaxJobs               int
	WorkstreamMaxJobs            int
	WorkstreamDetailMaxJobs      int
	CareerPlugMaxJobs            int
	HireologyMaxJobs             int
	GemMaxJobs                   int
	GemDetailMaxJobs             int
	DoverMaxJobs                 int
	TrakstarMaxJobs              int
	JobsoidMaxJobs               int
	FreshteamMaxJobs             int
	HomerunMaxJobs               int
	CATSOneMaxJobs               int
	HiBobHiringMaxJobs           int
	FountainMaxJobs              int
	ApplicantProMaxJobs          int
	TalentLyftBaseURL            string
	TalentLyftPageSize           int
	TalentLyftMaxPages           int
	TalentLyftMaxJobs            int
	TalentLyftDetailPages        bool
	PhenomPeopleMaxPages         int
	PhenomPeopleMaxJobs          int
	JibeMaxJobs                  int
	JobScoreMaxJobs              int
	TalentBrewMaxJobs            int
	AppleJobsMaxJobs             int
	AppleJobsDetailMaxJobs       int
	StripeJobsMaxJobs            int
	AmazonJobsPageSize           int
	AmazonJobsMaxPages           int
	AmazonJobsMaxJobs            int
	AmazonJobsDetailMaxJobs      int
	GoogleCareersMaxJobs         int
	GoogleCareersDetailMaxJobs   int
	EightfoldPCSXMaxPages        int
	EightfoldPCSXMaxJobs         int
	EightfoldPCSXDetailMaxJobs   int
	EightfoldApplyPageSize       int
	EightfoldApplyMaxPages       int
	EightfoldApplyMaxJobs        int
	OpenAICareersMaxJobs         int
	GitHubJobListMaxJobs         int
	JobsynBaseURL                string
	JobsynPageSize               int
	JobsynMaxPages               int
	JobsynMaxJobs                int
	IBMSearchAPIBaseURL          string
	IBMSearchMaxJobs             int
	CitadelSecuritiesMaxJobs     int
	WhatnotMaxJobs               int
	WalmartPageSize              int
	WalmartMaxPages              int
	WalmartMaxJobs               int
	WorldQuantMaxJobs            int
	TaleoBaseURL                 string
	TaleoMaxJobs                 int
}

type ATSExtractor struct {
	client                       *http.Client
	greenhouseBaseURL            string
	leverGlobalBaseURL           string
	leverEuropeBaseURL           string
	ashbyJobBoardBaseURL         string
	workablePublicBaseURL        string
	workableJobsBaseURL          string
	workableJobsMaxPages         int
	workableJobsMaxJobs          int
	recruiteeBaseURL             string
	smartRecruitersBaseURL       string
	smartRecruitersMaxJobs       int
	smartRecruitersDetailMaxJobs int
	comeetBaseURL                string
	comeetHostedMaxJobs          int
	polymerPublicBaseURL         string
	workdayPageSize              int
	workdayMaxPages              int
	polymerMaxPages              int
	icimsMaxJobs                 int
	jazzHRMaxJobs                int
	jobviteMaxJobs               int
	teamtailorMaxJobs            int
	bambooHRMaxJobs              int
	ripplingPageSize             int
	ripplingMaxPages             int
	ripplingMaxJobs              int
	successFactorsMaxJobs        int
	adpWorkforceNowPageSize      int
	adpWorkforceNowMaxPages      int
	adpWorkforceNowMaxJobs       int
	adpMyJobsConfigBaseURL       string
	adpMyJobsAPIBaseURL          string
	adpMyJobsPageSize            int
	adpMyJobsMaxPages            int
	adpMyJobsMaxJobs             int
	ukgMaxJobs                   int
	dayforcePageSize             int
	dayforceMaxPages             int
	dayforceMaxJobs              int
	byteDanceBaseURL             string
	byteDancePageSize            int
	byteDanceMaxPages            int
	byteDanceMaxJobs             int
	oracleRecruitingPageSize     int
	oracleRecruitingMaxPages     int
	oracleRecruitingMaxJobs      int
	paylocityMaxJobs             int
	paycomMaxJobs                int
	paycomDetailMaxJobs          int
	avatureMaxJobs               int
	avatureDetailMaxJobs         int
	jobylonMaxJobs               int
	zohoRecruitMaxJobs           int
	manatalMaxJobs               int
	joinMaxJobs                  int
	occupopMaxJobs               int
	workstreamMaxJobs            int
	workstreamDetailMaxJobs      int
	careerPlugMaxJobs            int
	hireologyMaxJobs             int
	gemMaxJobs                   int
	gemDetailMaxJobs             int
	doverMaxJobs                 int
	trakstarMaxJobs              int
	jobsoidMaxJobs               int
	freshteamMaxJobs             int
	homerunMaxJobs               int
	catsOneMaxJobs               int
	hibobHiringMaxJobs           int
	fountainMaxJobs              int
	applicantProMaxJobs          int
	talentLyftBaseURL            string
	talentLyftPageSize           int
	talentLyftMaxPages           int
	talentLyftMaxJobs            int
	talentLyftDetailPages        bool
	phenomPeopleMaxPages         int
	phenomPeopleMaxJobs          int
	jibeMaxJobs                  int
	jobScoreMaxJobs              int
	talentBrewMaxJobs            int
	appleJobsMaxJobs             int
	appleJobsDetailMaxJobs       int
	stripeJobsMaxJobs            int
	amazonJobsPageSize           int
	amazonJobsMaxPages           int
	amazonJobsMaxJobs            int
	amazonJobsDetailMaxJobs      int
	googleCareersMaxJobs         int
	googleCareersDetailMaxJobs   int
	eightfoldPCSXMaxPages        int
	eightfoldPCSXMaxJobs         int
	eightfoldPCSXDetailMaxJobs   int
	eightfoldApplyPageSize       int
	eightfoldApplyMaxPages       int
	eightfoldApplyMaxJobs        int
	openAICareersMaxJobs         int
	githubJobListMaxJobs         int
	jobsynBaseURL                string
	jobsynPageSize               int
	jobsynMaxPages               int
	jobsynMaxJobs                int
	ibmSearchAPIBaseURL          string
	ibmSearchMaxJobs             int
	citadelSecuritiesMaxJobs     int
	whatnotMaxJobs               int
	walmartPageSize              int
	walmartMaxPages              int
	walmartMaxJobs               int
	worldQuantMaxJobs            int
	taleoBaseURL                 string
	taleoMaxJobs                 int
	greenhouseEngine             provider.Engine
	leverEngine                  provider.Engine
	ashbyEngine                  provider.Engine
	smartRecruitersEngine        provider.Engine
	workableEngine               provider.Engine
	workableJobsEngine           provider.Engine
	recruiteeEngine              provider.Engine
	comeetEngine                 provider.Engine
	bambooHREngine               provider.Engine
	breezyEngine                 provider.Engine
	icimsEngine                  provider.Engine
	personioEngine               provider.Engine
	pinpointEngine               provider.Engine
	jobviteEngine                provider.Engine
	teamtailorEngine             provider.Engine
	oracleRecruitingEngine       provider.Engine
	workdayEngine                provider.Engine
}

func NewATSExtractor(opts ATSOptions) *ATSExtractor {
	client := opts.Client
	if client == nil {
		client = NewSafeHTTPClient(defaultATSHTTPTimeout)
	}
	workdayPageSize := boundedInt(opts.WorkdayPageSize, 20, 1, 100)
	workdayMaxPages := boundedInt(opts.WorkdayMaxPages, 5, 1, 20)
	return &ATSExtractor{
		client:                       client,
		greenhouseBaseURL:            firstNonEmptyString(opts.GreenhouseBaseURL, "https://boards-api.greenhouse.io/v1/boards"),
		leverGlobalBaseURL:           firstNonEmptyString(opts.LeverGlobalBaseURL, "https://api.lever.co/v0/postings"),
		leverEuropeBaseURL:           firstNonEmptyString(opts.LeverEuropeBaseURL, "https://api.eu.lever.co/v0/postings"),
		ashbyJobBoardBaseURL:         firstNonEmptyString(opts.AshbyJobBoardBaseURL, "https://api.ashbyhq.com/posting-api/job-board"),
		workablePublicBaseURL:        firstNonEmptyString(opts.WorkablePublicBaseURL, "https://www.workable.com/api/accounts"),
		workableJobsBaseURL:          firstNonEmptyString(opts.WorkableJobsBaseURL, "https://jobs.workable.com/api/v1/jobs"),
		workableJobsMaxPages:         boundedInt(opts.WorkableJobsMaxPages, 2, 1, 10),
		workableJobsMaxJobs:          boundedInt(opts.WorkableJobsMaxJobs, 50, 1, 200),
		recruiteeBaseURL:             firstNonEmptyString(opts.RecruiteeBaseURL, "https://%s.recruitee.com/api/offers/"),
		smartRecruitersBaseURL:       firstNonEmptyString(opts.SmartRecruitersBaseURL, "https://api.smartrecruiters.com/v1/companies"),
		smartRecruitersMaxJobs:       boundedInt(opts.SmartRecruitersMaxJobs, 200, 1, 500),
		smartRecruitersDetailMaxJobs: boundedInt(opts.SmartRecruitersDetailMaxJobs, 40, 0, 200),
		comeetBaseURL:                firstNonEmptyString(opts.ComeetBaseURL, "https://www.comeet.co/careers-api/2.0"),
		comeetHostedMaxJobs:          boundedInt(opts.ComeetHostedMaxJobs, 50, 1, 200),
		polymerPublicBaseURL:         firstNonEmptyString(opts.PolymerPublicBaseURL, "https://api.polymer.co/v1/hire/organizations"),
		workdayPageSize:              workdayPageSize,
		workdayMaxPages:              workdayMaxPages,
		polymerMaxPages:              boundedInt(opts.PolymerMaxPages, 5, 1, 20),
		icimsMaxJobs:                 boundedInt(opts.ICIMSMaxJobs, 50, 1, 200),
		jazzHRMaxJobs:                boundedInt(opts.JazzHRMaxJobs, 50, 1, 200),
		jobviteMaxJobs:               boundedInt(opts.JobviteMaxJobs, 50, 1, 200),
		teamtailorMaxJobs:            boundedInt(opts.TeamtailorMaxJobs, 50, 1, 200),
		bambooHRMaxJobs:              boundedInt(opts.BambooHRMaxJobs, 50, 1, 200),
		ripplingPageSize:             boundedInt(opts.RipplingPageSize, 50, 1, 100),
		ripplingMaxPages:             boundedInt(opts.RipplingMaxPages, 5, 1, 20),
		ripplingMaxJobs:              boundedInt(opts.RipplingMaxJobs, 50, 1, 200),
		successFactorsMaxJobs:        boundedInt(opts.SuccessFactorsMaxJobs, 50, 1, 200),
		adpWorkforceNowPageSize:      boundedInt(opts.ADPWorkforceNowPageSize, 50, 1, 100),
		adpWorkforceNowMaxPages:      boundedInt(opts.ADPWorkforceNowMaxPages, 5, 1, 20),
		adpWorkforceNowMaxJobs:       boundedInt(opts.ADPWorkforceNowMaxJobs, 50, 1, 200),
		adpMyJobsConfigBaseURL:       firstNonEmptyString(opts.ADPMyJobsConfigBaseURL, "https://myjobs.adp.com/public/staffing/v1/career-site"),
		adpMyJobsAPIBaseURL:          firstNonEmptyString(opts.ADPMyJobsAPIBaseURL, "https://my.adp.com/myadp_prefix/mycareer/public/staffing/v1/job-requisitions"),
		adpMyJobsPageSize:            boundedInt(opts.ADPMyJobsPageSize, 50, 1, 100),
		adpMyJobsMaxPages:            boundedInt(opts.ADPMyJobsMaxPages, 5, 1, 20),
		adpMyJobsMaxJobs:             boundedInt(opts.ADPMyJobsMaxJobs, 50, 1, 200),
		ukgMaxJobs:                   boundedInt(opts.UKGMaxJobs, 50, 1, 200),
		dayforcePageSize:             boundedInt(opts.DayforcePageSize, 25, 1, 100),
		dayforceMaxPages:             boundedInt(opts.DayforceMaxPages, 5, 1, 20),
		dayforceMaxJobs:              boundedInt(opts.DayforceMaxJobs, 75, 1, 200),
		byteDanceBaseURL:             firstNonEmptyString(opts.ByteDanceBaseURL, "https://jobs.bytedance.com/api/v1/public/supplier"),
		byteDancePageSize:            boundedInt(opts.ByteDancePageSize, 50, 1, 50),
		byteDanceMaxPages:            boundedInt(opts.ByteDanceMaxPages, 10, 1, 20),
		byteDanceMaxJobs:             boundedInt(opts.ByteDanceMaxJobs, 500, 1, 1000),
		oracleRecruitingPageSize:     boundedInt(opts.OracleRecruitingPageSize, 25, 1, 100),
		oracleRecruitingMaxPages:     boundedInt(opts.OracleRecruitingMaxPages, 5, 1, 20),
		oracleRecruitingMaxJobs:      boundedInt(opts.OracleRecruitingMaxJobs, 75, 1, 200),
		paylocityMaxJobs:             boundedInt(opts.PaylocityMaxJobs, 50, 1, 200),
		paycomMaxJobs:                boundedInt(opts.PaycomMaxJobs, 50, 1, 200),
		paycomDetailMaxJobs:          boundedInt(opts.PaycomDetailMaxJobs, 10, 0, 50),
		avatureMaxJobs:               boundedInt(opts.AvatureMaxJobs, 50, 1, 200),
		avatureDetailMaxJobs:         boundedInt(opts.AvatureDetailMaxJobs, 10, 0, 50),
		jobylonMaxJobs:               boundedInt(opts.JobylonMaxJobs, 50, 1, 200),
		zohoRecruitMaxJobs:           boundedInt(opts.ZohoRecruitMaxJobs, 50, 1, 200),
		manatalMaxJobs:               boundedInt(opts.ManatalMaxJobs, 50, 1, 200),
		joinMaxJobs:                  boundedInt(opts.JOINMaxJobs, 50, 1, 200),
		occupopMaxJobs:               boundedInt(opts.OccupopMaxJobs, 50, 1, 200),
		workstreamMaxJobs:            boundedInt(opts.WorkstreamMaxJobs, 50, 1, 200),
		workstreamDetailMaxJobs:      boundedInt(opts.WorkstreamDetailMaxJobs, 5, 0, 50),
		careerPlugMaxJobs:            boundedInt(opts.CareerPlugMaxJobs, 50, 1, 200),
		hireologyMaxJobs:             boundedInt(opts.HireologyMaxJobs, 50, 1, 200),
		gemMaxJobs:                   boundedInt(opts.GemMaxJobs, 50, 1, 200),
		gemDetailMaxJobs:             boundedInt(opts.GemDetailMaxJobs, 10, 0, 50),
		doverMaxJobs:                 boundedInt(opts.DoverMaxJobs, 50, 1, 200),
		trakstarMaxJobs:              boundedInt(opts.TrakstarMaxJobs, 50, 1, 200),
		jobsoidMaxJobs:               boundedInt(opts.JobsoidMaxJobs, 50, 1, 200),
		freshteamMaxJobs:             boundedInt(opts.FreshteamMaxJobs, 50, 1, 200),
		homerunMaxJobs:               boundedInt(opts.HomerunMaxJobs, 50, 1, 200),
		catsOneMaxJobs:               boundedInt(opts.CATSOneMaxJobs, 50, 1, 200),
		hibobHiringMaxJobs:           boundedInt(opts.HiBobHiringMaxJobs, 50, 1, 200),
		fountainMaxJobs:              boundedInt(opts.FountainMaxJobs, 50, 1, 200),
		applicantProMaxJobs:          boundedInt(opts.ApplicantProMaxJobs, 50, 1, 200),
		talentLyftBaseURL:            firstNonEmptyString(opts.TalentLyftBaseURL, "https://api.talentlyft.com"),
		talentLyftPageSize:           boundedInt(opts.TalentLyftPageSize, 50, 1, 100),
		talentLyftMaxPages:           boundedInt(opts.TalentLyftMaxPages, 5, 1, 20),
		talentLyftMaxJobs:            boundedInt(opts.TalentLyftMaxJobs, 75, 1, 200),
		talentLyftDetailPages:        opts.TalentLyftDetailPages,
		phenomPeopleMaxPages:         boundedInt(opts.PhenomPeopleMaxPages, 5, 1, 20),
		phenomPeopleMaxJobs:          boundedInt(opts.PhenomPeopleMaxJobs, 50, 1, 200),
		jibeMaxJobs:                  boundedInt(opts.JibeMaxJobs, 50, 1, 200),
		jobScoreMaxJobs:              boundedInt(opts.JobScoreMaxJobs, 50, 1, 200),
		talentBrewMaxJobs:            boundedInt(opts.TalentBrewMaxJobs, 50, 1, 200),
		appleJobsMaxJobs:             boundedInt(opts.AppleJobsMaxJobs, 50, 1, 200),
		appleJobsDetailMaxJobs:       boundedInt(opts.AppleJobsDetailMaxJobs, 5, 1, 50),
		stripeJobsMaxJobs:            boundedInt(opts.StripeJobsMaxJobs, 75, 1, 200),
		amazonJobsPageSize:           boundedInt(opts.AmazonJobsPageSize, 25, 1, 100),
		amazonJobsMaxPages:           boundedInt(opts.AmazonJobsMaxPages, 3, 1, 20),
		amazonJobsMaxJobs:            boundedInt(opts.AmazonJobsMaxJobs, 75, 1, 200),
		amazonJobsDetailMaxJobs:      boundedInt(opts.AmazonJobsDetailMaxJobs, 5, 1, 50),
		googleCareersMaxJobs:         boundedInt(opts.GoogleCareersMaxJobs, 50, 1, 200),
		googleCareersDetailMaxJobs:   boundedInt(opts.GoogleCareersDetailMaxJobs, 5, 1, 50),
		eightfoldPCSXMaxPages:        boundedInt(opts.EightfoldPCSXMaxPages, 3, 1, 20),
		eightfoldPCSXMaxJobs:         boundedInt(opts.EightfoldPCSXMaxJobs, 75, 1, 200),
		eightfoldPCSXDetailMaxJobs:   boundedInt(opts.EightfoldPCSXDetailMaxJobs, 5, 1, 50),
		eightfoldApplyPageSize:       boundedInt(opts.EightfoldApplyPageSize, 25, 1, 100),
		eightfoldApplyMaxPages:       boundedInt(opts.EightfoldApplyMaxPages, 3, 1, 20),
		eightfoldApplyMaxJobs:        boundedInt(opts.EightfoldApplyMaxJobs, 75, 1, 200),
		openAICareersMaxJobs:         boundedInt(opts.OpenAICareersMaxJobs, 75, 1, 300),
		githubJobListMaxJobs:         boundedInt(opts.GitHubJobListMaxJobs, 75, 1, 300),
		jobsynBaseURL:                firstNonEmptyString(opts.JobsynBaseURL, "https://prod-search-api.jobsyn.org/api"),
		jobsynPageSize:               boundedInt(opts.JobsynPageSize, 25, 1, 100),
		jobsynMaxPages:               boundedInt(opts.JobsynMaxPages, 3, 1, 20),
		jobsynMaxJobs:                boundedInt(opts.JobsynMaxJobs, 75, 1, 300),
		ibmSearchAPIBaseURL:          firstNonEmptyString(opts.IBMSearchAPIBaseURL, "https://www-api.ibm.com/search/api/v2"),
		ibmSearchMaxJobs:             boundedInt(opts.IBMSearchMaxJobs, 50, 1, 200),
		citadelSecuritiesMaxJobs:     boundedInt(opts.CitadelSecuritiesMaxJobs, 50, 1, 200),
		whatnotMaxJobs:               boundedInt(opts.WhatnotMaxJobs, 200, 1, 500),
		walmartPageSize:              boundedInt(opts.WalmartPageSize, 20, 1, 100),
		walmartMaxPages:              boundedInt(opts.WalmartMaxPages, 5, 1, 20),
		walmartMaxJobs:               boundedInt(opts.WalmartMaxJobs, 100, 1, 500),
		worldQuantMaxJobs:            boundedInt(opts.WorldQuantMaxJobs, 200, 1, 500),
		taleoBaseURL:                 opts.TaleoBaseURL,
		taleoMaxJobs:                 boundedInt(opts.TaleoMaxJobs, 100, 1, 500),
		greenhouseEngine: atsprovider.New("greenhouse", atsprovider.Options{
			Client:            client,
			GreenhouseBaseURL: firstNonEmptyString(opts.GreenhouseBaseURL, "https://boards-api.greenhouse.io/v1/boards"),
		}),
		leverEngine: atsprovider.New("lever", atsprovider.Options{
			Client:             client,
			LeverGlobalBaseURL: firstNonEmptyString(opts.LeverGlobalBaseURL, "https://api.lever.co/v0/postings"),
			LeverEuropeBaseURL: firstNonEmptyString(opts.LeverEuropeBaseURL, "https://api.eu.lever.co/v0/postings"),
		}),
		ashbyEngine: atsprovider.New("ashby", atsprovider.Options{
			Client:       client,
			AshbyBaseURL: firstNonEmptyString(opts.AshbyJobBoardBaseURL, "https://api.ashbyhq.com/posting-api/job-board"),
		}),
		smartRecruitersEngine: atsprovider.New("smartrecruiters", atsprovider.Options{
			Client:                       client,
			SmartRecruitersBaseURL:       firstNonEmptyString(opts.SmartRecruitersBaseURL, "https://api.smartrecruiters.com/v1/companies"),
			SmartRecruitersMaxJobs:       boundedInt(opts.SmartRecruitersMaxJobs, 200, 1, 500),
			SmartRecruitersDetailMaxJobs: boundedInt(opts.SmartRecruitersDetailMaxJobs, 40, 0, 200),
		}),
		workableEngine: atsprovider.New("workable", atsprovider.Options{
			Client:                client,
			WorkablePublicBaseURL: firstNonEmptyString(opts.WorkablePublicBaseURL, "https://www.workable.com/api/accounts"),
		}),
		workableJobsEngine: atsprovider.New("workable_jobs", atsprovider.Options{
			Client:               client,
			WorkableJobsBaseURL:  firstNonEmptyString(opts.WorkableJobsBaseURL, "https://jobs.workable.com/api/v1/jobs"),
			WorkableJobsMaxPages: boundedInt(opts.WorkableJobsMaxPages, 2, 1, 10),
			WorkableJobsMaxJobs:  boundedInt(opts.WorkableJobsMaxJobs, 50, 1, 200),
		}),
		recruiteeEngine: atsprovider.New("recruitee", atsprovider.Options{
			Client:           client,
			RecruiteeBaseURL: firstNonEmptyString(opts.RecruiteeBaseURL, "https://%s.recruitee.com/api/offers/"),
		}),
		comeetEngine: atsprovider.New("comeet", atsprovider.Options{
			Client:        client,
			ComeetBaseURL: firstNonEmptyString(opts.ComeetBaseURL, "https://www.comeet.co/careers-api/2.0"),
		}),
		bambooHREngine: atsprovider.New("bamboohr", atsprovider.Options{
			Client:          client,
			BambooHRMaxJobs: boundedInt(opts.BambooHRMaxJobs, 50, 1, 200),
		}),
		breezyEngine: atsprovider.New("breezy", atsprovider.Options{
			Client:        client,
			BreezyMaxJobs: 50,
		}),
		icimsEngine: atsprovider.New("icims", atsprovider.Options{
			Client:       client,
			ICIMSMaxJobs: boundedInt(opts.ICIMSMaxJobs, 50, 1, 200),
		}),
		personioEngine: atsprovider.New("personio", atsprovider.Options{
			Client:          client,
			PersonioMaxJobs: 50,
		}),
		pinpointEngine: atsprovider.New("pinpoint", atsprovider.Options{
			Client:          client,
			PinpointMaxJobs: 50,
		}),
		jobviteEngine: atsprovider.New("jobvite", atsprovider.Options{
			Client:         client,
			JobviteMaxJobs: boundedInt(opts.JobviteMaxJobs, 50, 1, 200),
		}),
		teamtailorEngine: atsprovider.New("teamtailor", atsprovider.Options{
			Client:            client,
			TeamtailorMaxJobs: boundedInt(opts.TeamtailorMaxJobs, 50, 1, 200),
		}),
		oracleRecruitingEngine: atsprovider.New("oracle_recruiting", atsprovider.Options{
			Client:                   client,
			OracleRecruitingPageSize: boundedInt(opts.OracleRecruitingPageSize, 25, 1, 100),
			OracleRecruitingMaxPages: boundedInt(opts.OracleRecruitingMaxPages, 5, 1, 20),
			OracleRecruitingMaxJobs:  boundedInt(opts.OracleRecruitingMaxJobs, 75, 1, 200),
		}),
		workdayEngine: workdayprovider.New(workdayprovider.Options{
			Client: client, PageSize: workdayPageSize, MaxPages: workdayMaxPages,
			DetailMaxJobs: opts.WorkdayDetailMaxJobs, DetailTimeout: opts.WorkdayDetailTimeout,
		}),
	}
}

func (e *ATSExtractor) Name() string {
	return "ats-adapter"
}

func (e *ATSExtractor) Tier() Tier {
	return TierATS
}

func (e *ATSExtractor) Extract(ctx context.Context, source Source) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	kind := atsKind(source)
	if _, ok := supportedATSSourceKinds[kind]; !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrUnsupportedATS, firstNonEmptyString(kind, source.URL))
	}
	switch kind {
	case "greenhouse":
		return e.extractGreenhouse(ctx, source)
	case "lever":
		return e.extractLever(ctx, source)
	case "ashby":
		return e.extractAshby(ctx, source)
	case "workable":
		return e.extractWorkable(ctx, source)
	case "workable_jobs":
		return e.extractWorkableJobs(ctx, source)
	case "recruitee":
		return e.extractRecruitee(ctx, source)
	case "smartrecruiters":
		return e.extractSmartRecruiters(ctx, source)
	case "janestreet_careers":
		return e.extractJaneStreetCareers(ctx, source)
	case "akuna_careers":
		return e.extractAkunaCareers(ctx, source)
	case "comeet":
		return e.extractComeet(ctx, source)
	case "workday":
		return e.extractWorkday(ctx, source)
	case "breezy":
		return e.extractBreezy(ctx, source)
	case "personio":
		return e.extractPersonio(ctx, source)
	case "pinpoint":
		return e.extractPinpoint(ctx, source)
	case "polymer":
		return e.extractPolymer(ctx, source)
	case "icims":
		return e.extractICIMS(ctx, source)
	case "jazzhr":
		return e.extractJazzHR(ctx, source)
	case "jobvite":
		return e.extractJobvite(ctx, source)
	case "teamtailor":
		return e.extractTeamtailor(ctx, source)
	case "bamboohr":
		return e.extractBambooHR(ctx, source)
	case "rippling":
		return e.extractRippling(ctx, source)
	case "rippling_jobs", "rippling_hosted":
		return e.extractRipplingJobs(ctx, source)
	case "successfactors":
		return e.extractSuccessFactors(ctx, source)
	case "adp_workforcenow", "adp_workforce_now", "workforcenow":
		return e.extractADPWorkforceNow(ctx, source)
	case "adp_myjobs", "myjobs", "adp_my_jobs":
		return e.extractADPMyJobs(ctx, source)
	case "ukg", "ukg_pro", "ultipro":
		return e.extractUKGPro(ctx, source)
	case "dayforce":
		return e.extractDayforce(ctx, source)
	case "bytedance_careers":
		return e.extractByteDanceCareers(ctx, source)
	case "oracle_recruiting", "oracle_cloud_recruiting", "oracle", "orc":
		return e.extractOracleRecruiting(ctx, source)
	case "paylocity":
		return e.extractPaylocity(ctx, source)
	case "paycom":
		return e.extractPaycom(ctx, source)
	case "avature":
		return e.extractAvature(ctx, source)
	case "jobylon":
		return e.extractJobylon(ctx, source)
	case "zoho_recruit", "zohorecruit":
		return e.extractZohoRecruit(ctx, source)
	case "manatal":
		return e.extractManatal(ctx, source)
	case "join", "join_com":
		return e.extractJOIN(ctx, source)
	case "occupop":
		return e.extractOccupop(ctx, source)
	case "workstream":
		return e.extractWorkstream(ctx, source)
	case "careerplug":
		return e.extractCareerPlug(ctx, source)
	case "hireology":
		return e.extractHireology(ctx, source)
	case "dover":
		return e.extractDover(ctx, source)
	case "recruiterbox", "trakstar":
		return e.extractTrakstar(ctx, source)
	case "gem":
		return e.extractGem(ctx, source)
	case "jobsoid":
		return e.extractJobsoid(ctx, source)
	case "freshteam":
		return e.extractFreshteam(ctx, source)
	case "homerun":
		return e.extractHomerun(ctx, source)
	case "catsone":
		return e.extractCATSOne(ctx, source)
	case "hibob_hiring", "hibob":
		return e.extractHiBobHiring(ctx, source)
	case "fountain":
		return e.extractFountain(ctx, source)
	case "applicantpro", "applicant_pro":
		return e.extractApplicantPro(ctx, source)
	case "talentlyft", "talent_lyft":
		return e.extractTalentLyft(ctx, source)
	case "phenom_people", "phenompeople", "phenom", "snowflake_careers":
		return e.extractPhenomPeople(ctx, source)
	case "jibe", "radancy":
		return e.extractJibe(ctx, source)
	case "jobscore":
		return e.extractJobScore(ctx, source)
	case "talentbrew":
		return e.extractTalentBrew(ctx, source)
	case "apple_jobs", "apple":
		return e.extractAppleJobs(ctx, source)
	case "stripe_jobs", "stripe":
		return e.extractStripeJobs(ctx, source)
	case "amazon_jobs", "amazon":
		return e.extractAmazonJobs(ctx, source)
	case "google_careers", "google":
		return e.extractGoogleCareers(ctx, source)
	case "imc_careers":
		return e.extractIMCCareers(ctx, source)
	case "eightfold_pcsx", "eightfold", "pcsx", "microsoft_careers", "microsoft":
		return e.extractEightfoldPCSX(ctx, source)
	case "eightfold_apply":
		return e.extractEightfoldApply(ctx, source)
	case "openai_careers", "openai":
		return e.extractOpenAICareers(ctx, source)
	case "optiver_careers":
		return e.extractOptiverCareers(ctx, source)
	case "github_job_list", "github_jobs":
		return e.extractGitHubJobList(ctx, source)
	case "directemployers", "jobsyn":
		return e.extractJobsyn(ctx, source)
	case "meta_careers":
		return e.extractMetaCareers(ctx, source)
	case "ibm_careers":
		return e.extractIBMCareers(ctx, source)
	case "citadel_careers", "citadel_securities_careers":
		return e.extractCitadelSecuritiesCareers(ctx, source)
	case "whatnot_careers":
		return e.extractWhatnotCareers(ctx, source)
	case "walmart_careers":
		return e.extractWalmartCareers(ctx, source)
	case "worldquant_careers":
		return e.extractWorldQuantCareers(ctx, source)
	case "taleo":
		return e.extractTaleo(ctx, source)
	default:
		return Result{}, fmt.Errorf("%w: %s", ErrUnsupportedATS, firstNonEmptyString(kind, source.URL))
	}
}

func (e *ATSExtractor) extractGreenhouse(ctx context.Context, source Source) (Result, error) {
	if e.greenhouseEngine != nil {
		return providerResultToScraper(e.greenhouseEngine.Extract(ctx, scraperSourceToProvider(source)))
	}
	board, err := greenhouseBoardToken(source.URL)
	if err != nil {
		return Result{}, err
	}
	endpoint, err := joinURL(e.greenhouseBaseURL, board, "jobs")
	if err != nil {
		return Result{}, err
	}
	q := endpoint.Query()
	q.Set("content", "true")
	endpoint.RawQuery = q.Encode()

	var payload greenhouseResponse
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0, len(payload.Jobs))
	for _, item := range payload.Jobs {
		applyURL := strings.TrimSpace(item.AbsoluteURL)
		description := cleanHTMLText(item.Content)
		postedAt := parseTimePtr(item.UpdatedAt)
		location := greenhouseLocationText(item.Location.Name, item.Offices, description)
		company := sourceCompany(source, board)
		job := JobPosting{
			SourceJobID:    "greenhouse:" + strconv.FormatInt(item.ID, 10),
			Company:        company,
			Title:          item.Title,
			Location:       location,
			EmploymentType: employmentFromText(item.Title, ""),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       postedAt,
			Live:           true,
			Confidence:     0.94,
			Strategy:       TierATS,
			Evidence: []Evidence{
				{Field: "ats", Text: "Greenhouse structured job board API", URL: endpoint.String()},
				{Field: "description", Text: description, URL: applyURL},
				{Field: "department", Text: greenhouseDepartmentText(item.Departments), URL: applyURL},
			},
		}
		jobs = append(jobs, job)
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.94,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Greenhouse job board API", URL: endpoint.String()},
		},
	})
}

func (e *ATSExtractor) extractLever(ctx context.Context, source Source) (Result, error) {
	if e.leverEngine != nil {
		return providerResultToScraper(e.leverEngine.Extract(ctx, scraperSourceToProvider(source)))
	}
	site, european, err := leverSite(source.URL)
	if err != nil {
		return Result{}, err
	}
	base := e.leverGlobalBaseURL
	if european {
		base = e.leverEuropeBaseURL
	}
	endpoint, err := joinURL(base, site)
	if err != nil {
		return Result{}, err
	}
	q := endpoint.Query()
	q.Set("mode", "json")
	endpoint.RawQuery = q.Encode()

	var payload []leverPosting
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0, len(payload))
	company := sourceCompany(source, site)
	for _, item := range payload {
		location := firstNonEmptyString(item.Categories.Location, strings.Join(item.Categories.AllLocations, "; "))
		description := firstNonEmptyString(item.DescriptionPlain, item.Description)
		applyURL := firstNonEmptyString(item.ApplyURL, item.HostedURL)
		postedAt := millisTimePtr(firstNonZeroInt64(item.CreatedAt, item.UpdatedAt))
		jobs = append(jobs, JobPosting{
			SourceJobID:    "lever:" + firstNonEmptyString(item.ID, stableJobToken(item.HostedURL, item.Text)),
			Company:        company,
			Title:          item.Text,
			Location:       location,
			Country:        item.Country,
			EmploymentType: item.Categories.Commitment,
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       postedAt,
			Live:           true,
			Confidence:     0.93,
			Strategy:       TierATS,
			Evidence: []Evidence{
				{Field: "ats", Text: "Lever postings API", URL: endpoint.String()},
				{Field: "description", Text: description, URL: item.HostedURL},
				{Field: "commitment", Text: item.Categories.Commitment, URL: item.HostedURL},
				{Field: "team", Text: firstNonEmptyString(item.Categories.Team, item.Categories.Department), URL: item.HostedURL},
			},
		})
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.93,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Lever postings API", URL: endpoint.String()},
		},
	})
}

func (e *ATSExtractor) extractAshby(ctx context.Context, source Source) (Result, error) {
	if e.ashbyEngine != nil {
		return providerResultToScraper(e.ashbyEngine.Extract(ctx, scraperSourceToProvider(source)))
	}
	board, err := ashbyBoardName(source.URL)
	if err != nil {
		return Result{}, err
	}
	endpoint, err := joinURL(e.ashbyJobBoardBaseURL, board)
	if err != nil {
		return Result{}, err
	}
	q := endpoint.Query()
	q.Set("includeCompensation", "true")
	endpoint.RawQuery = q.Encode()

	var payload ashbyResponse
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0, len(payload.Jobs))
	company := sourceCompany(source, board)
	for _, item := range payload.Jobs {
		if item.IsListed != nil && !*item.IsListed {
			continue
		}
		location := ashbyLocation(item)
		applyURL := firstNonEmptyString(item.ApplyURL, item.JobURL)
		postedAt := parseTimePtr(item.PublishedAt)
		jobs = append(jobs, JobPosting{
			SourceJobID:    "ashby:" + board + ":" + stableJobToken(item.JobURL, item.Title),
			Company:        company,
			Title:          item.Title,
			Location:       location,
			EmploymentType: item.EmploymentType,
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       postedAt,
			Live:           true,
			Confidence:     0.93,
			Strategy:       TierATS,
			Evidence: []Evidence{
				{Field: "ats", Text: "Ashby posting API", URL: endpoint.String()},
				{Field: "description", Text: item.DescriptionPlain, URL: item.JobURL},
				{Field: "workplace", Text: strings.TrimSpace(item.WorkplaceType), URL: item.JobURL},
				{Field: "team", Text: firstNonEmptyString(item.Team, item.Department), URL: item.JobURL},
			},
		})
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.93,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Ashby posting API", URL: endpoint.String()},
		},
	})
}

func (e *ATSExtractor) extractWorkable(ctx context.Context, source Source) (Result, error) {
	if e.workableEngine != nil {
		return providerResultToScraper(e.workableEngine.Extract(ctx, scraperSourceToProvider(source)))
	}
	account, err := workableAccountSlug(source.URL)
	if err != nil {
		return Result{}, err
	}
	endpoint, err := joinURL(e.workablePublicBaseURL, account)
	if err != nil {
		return Result{}, err
	}
	q := endpoint.Query()
	q.Set("details", "true")
	endpoint.RawQuery = q.Encode()

	var payload workableResponse
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0, len(payload.Jobs))
	company := sourceCompany(source, firstNonEmptyString(payload.Name, account))
	for _, item := range payload.Jobs {
		if !workablePublished(item.State) {
			continue
		}
		location, country := workableJobLocation(item)
		description := workableDescription(item)
		applyURL := firstNonEmptyString(item.ApplicationURL, item.URL, item.Shortlink)
		jobURL := firstNonEmptyString(item.URL, item.Shortlink, applyURL)
		postedAt := parseTimePtr(firstNonEmptyString(item.PublishedAt, item.PublishedOn, item.CreatedAt))
		jobToken := firstNonEmptyString(item.Shortcode, item.ID, stableJobToken(jobURL, item.Title))
		jobs = append(jobs, JobPosting{
			SourceJobID:    "workable:" + account + ":" + jobToken,
			Company:        company,
			Title:          firstNonEmptyString(item.FullTitle, item.Title),
			Location:       location,
			Country:        country,
			EmploymentType: employmentFromText(item.Title, firstNonEmptyString(item.EmploymentType, item.WorkType)),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       postedAt,
			Live:           true,
			Confidence:     0.92,
			Strategy:       TierATS,
			Evidence: []Evidence{
				{Field: "ats", Text: "Workable public account API", URL: endpoint.String()},
				{Field: "description", Text: description, URL: jobURL},
				{Field: "department", Text: item.Department, URL: jobURL},
				{Field: "location", Text: location, URL: jobURL},
			},
		})
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.92,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Workable public account API", URL: endpoint.String()},
		},
	})
}

func (e *ATSExtractor) extractWorkableJobs(ctx context.Context, source Source) (Result, error) {
	if e.workableJobsEngine != nil {
		return providerResultToScraper(e.workableJobsEngine.Extract(ctx, scraperSourceToProvider(source)))
	}
	query := workableJobsQuery(source.URL)
	endpoint, err := parseSourceURL(e.workableJobsBaseURL)
	if err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, e.workableJobsMaxJobs)
	rawEvidence := []Evidence{}
	pageToken := ""
	for page := 0; page < e.workableJobsMaxPages && len(jobs) < e.workableJobsMaxJobs; page++ {
		pageURL := *endpoint
		q := pageURL.Query()
		if query != "" {
			q.Set("query", query)
		}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		pageURL.RawQuery = q.Encode()

		var payload workableJobsResponse
		if err := e.getJSON(ctx, pageURL.String(), &payload); err != nil {
			return Result{}, err
		}
		rawEvidence = append(rawEvidence, Evidence{Field: "ats_endpoint", Text: "Workable Jobs public search API", URL: pageURL.String()})
		for _, item := range payload.Jobs {
			if len(jobs) >= e.workableJobsMaxJobs {
				break
			}
			if !workablePublished(item.State) {
				continue
			}
			if job, ok := workableJobsPosting(source, pageURL.String(), item); ok {
				jobs = append(jobs, job)
			}
		}
		pageToken = strings.TrimSpace(payload.NextPageToken)
		if pageToken == "" {
			break
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:      source,
		Jobs:        jobs,
		Confidence:  0.86,
		Strategy:    TierATS,
		Live:        true,
		FetchedAt:   time.Now().UTC(),
		RawEvidence: rawEvidence,
	})
}

func (e *ATSExtractor) extractRecruitee(ctx context.Context, source Source) (Result, error) {
	if e.recruiteeEngine != nil {
		return providerResultToScraper(e.recruiteeEngine.Extract(ctx, scraperSourceToProvider(source)))
	}
	companySlug, err := recruiteeCompanySlug(source.URL)
	if err != nil {
		return Result{}, err
	}
	endpoint, err := recruiteeOffersEndpoint(e.recruiteeBaseURL, companySlug)
	if err != nil {
		return Result{}, err
	}

	var payload recruiteeResponse
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0, len(payload.Offers))
	company := sourceCompany(source, companySlug)
	for _, offer := range payload.Offers {
		if !recruiteeLiveJob(offer) {
			continue
		}
		applyURL := firstNonEmptyString(offer.CareersURL, offer.URL, recruiteeOfferURL(companySlug, offer.Slug))
		location, country := recruiteeOfferLocation(offer)
		description := cleanHTMLText(strings.Join(compactStringList(offer.Description, offer.Requirements), " "))
		postedAt := parseTimePtr(firstNonEmptyString(offer.PublishedAt, offer.CreatedAt, offer.UpdatedAt))
		jobToken := firstNonEmptyString(offer.Slug, recruiteeOfferID(offer.ID), stableJobToken(applyURL, offer.Title))
		jobs = append(jobs, JobPosting{
			SourceJobID:    "recruitee:" + companySlug + ":" + jobToken,
			Company:        company,
			Title:          offer.Title,
			Location:       location,
			Country:        country,
			EmploymentType: employmentFromText(offer.Title, offer.EmploymentType),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       postedAt,
			Live:           true,
			Confidence:     0.91,
			Strategy:       TierATS,
			Evidence: []Evidence{
				{Field: "ats", Text: "Recruitee Careers Site API", URL: endpoint.String()},
				{Field: "description", Text: description, URL: applyURL},
				{Field: "department", Text: offer.Department, URL: applyURL},
				{Field: "location", Text: location, URL: applyURL},
			},
		})
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.91,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Recruitee Careers Site API", URL: endpoint.String()},
		},
	})
}

func (e *ATSExtractor) extractSmartRecruiters(ctx context.Context, source Source) (Result, error) {
	if e.smartRecruitersEngine != nil {
		return providerResultToScraper(e.smartRecruitersEngine.Extract(ctx, scraperSourceToProvider(source)))
	}
	companyID, err := smartRecruitersCompanyIdentifier(source.URL)
	if err != nil {
		return Result{}, err
	}

	postings, endpoint, err := e.smartRecruitersPostings(ctx, companyID)
	if err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0, min(len(postings), e.smartRecruitersMaxJobs))
	for i, posting := range postings {
		if len(jobs) >= e.smartRecruitersMaxJobs {
			break
		}
		if posting.ID != "" && i < e.smartRecruitersDetailMaxJobs {
			detail, err := e.smartRecruitersPostingDetail(ctx, companyID, posting.ID)
			if err == nil {
				posting = mergeSmartRecruitersPosting(posting, detail)
			}
		}
		location, country := smartRecruitersLocationText(posting.Location)
		applyURL := smartRecruitersApplyURL(companyID, posting)
		description := smartRecruitersDescription(posting.JobAd)
		jobToken := firstNonEmptyString(posting.ID, posting.UUID, stableJobToken(applyURL, posting.Name))
		company := sourceCompany(source, firstNonEmptyString(posting.Company.Name, posting.Company.Identifier, companyID))
		jobs = append(jobs, JobPosting{
			SourceJobID:    "smartrecruiters:" + companyID + ":" + jobToken,
			Company:        company,
			Title:          posting.Name,
			Location:       location,
			Country:        country,
			EmploymentType: employmentFromText(posting.Name, firstNonEmptyString(posting.TypeOfEmployment.Label, posting.ExperienceLevel.Label)),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       parseTimePtr(firstNonEmptyString(posting.ReleasedDate, posting.PostedDate)),
			Live:           true,
			Confidence:     0.91,
			Strategy:       TierATS,
			Evidence: []Evidence{
				{Field: "ats", Text: "SmartRecruiters Posting API", URL: endpoint.String()},
				{Field: "description", Text: description, URL: applyURL},
				{Field: "department", Text: posting.Department.Label, URL: applyURL},
				{Field: "function", Text: posting.Function.Label, URL: applyURL},
			},
		})
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.91,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "SmartRecruiters Posting API", URL: endpoint.String()},
		},
	})
}

func (e *ATSExtractor) extractJaneStreetCareers(ctx context.Context, source Source) (Result, error) {
	base, err := sourceBaseURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	jobsEndpoint, err := joinURL(base, "jobs", "main.json")
	if err != nil {
		return Result{}, err
	}
	directoriesEndpoint, err := joinURL(base, "static", "position-directories.json")
	if err != nil {
		return Result{}, err
	}

	var payload []janeStreetJob
	if err := e.getJSON(ctx, jobsEndpoint.String(), &payload); err != nil {
		return Result{}, err
	}
	var directories []string
	if err := e.getJSON(ctx, directoriesEndpoint.String(), &directories); err != nil {
		return Result{}, err
	}
	allowed := make(map[string]struct{}, len(directories))
	for _, id := range directories {
		id = strings.TrimSpace(id)
		if id != "" {
			allowed[id] = struct{}{}
		}
	}

	jobs := make([]JobPosting, 0, min(len(payload), 200))
	for _, item := range payload {
		if len(jobs) >= 200 {
			break
		}
		if len(allowed) > 0 {
			if _, ok := allowed[strconv.FormatInt(item.ID, 10)]; !ok {
				continue
			}
		}
		job, ok := janeStreetPosting(source, base, item)
		if !ok {
			continue
		}
		jobs = append(jobs, job)
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.87,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Jane Street public jobs JSON", URL: jobsEndpoint.String()},
		},
	})
}

func (e *ATSExtractor) extractAkunaCareers(ctx context.Context, source Source) (Result, error) {
	endpoint, err := akunaJobsFeedURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	var payload []akunaJob
	if err := e.getJSON(ctx, endpoint, &payload); err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, min(len(payload), 200))
	for _, item := range payload {
		if len(jobs) >= 200 {
			break
		}
		job, ok := akunaPosting(source, endpoint, item)
		if !ok {
			continue
		}
		jobs = append(jobs, job)
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Akuna public jobs JSON", URL: endpoint},
		},
	})
}

func (e *ATSExtractor) smartRecruitersPostings(ctx context.Context, companyID string) ([]smartRecruitersPosting, *url.URL, error) {
	endpoint, err := joinURL(e.smartRecruitersBaseURL, companyID, "postings")
	if err != nil {
		return nil, nil, err
	}

	limit := min(100, e.smartRecruitersMaxJobs)
	postings := make([]smartRecruitersPosting, 0, limit)
	for offset := 0; offset < 1000; offset += limit {
		pageURL := *endpoint
		q := pageURL.Query()
		q.Set("limit", strconv.Itoa(limit))
		if offset > 0 {
			q.Set("offset", strconv.Itoa(offset))
		}
		pageURL.RawQuery = q.Encode()

		var payload smartRecruitersPostingsResponse
		if err := e.getJSON(ctx, pageURL.String(), &payload); err != nil {
			return nil, nil, err
		}
		postings = append(postings, payload.Content...)
		if len(postings) >= e.smartRecruitersMaxJobs {
			postings = postings[:e.smartRecruitersMaxJobs]
			break
		}
		if len(payload.Content) == 0 {
			break
		}
		if payload.TotalFound > 0 && len(postings) >= payload.TotalFound {
			break
		}
		if len(payload.Content) < limit {
			break
		}
	}
	return postings, endpoint, nil
}

func (e *ATSExtractor) smartRecruitersPostingDetail(ctx context.Context, companyID string, postingID string) (smartRecruitersPosting, error) {
	endpoint, err := joinURL(e.smartRecruitersBaseURL, companyID, "postings", postingID)
	if err != nil {
		return smartRecruitersPosting{}, err
	}
	var detail smartRecruitersPosting
	if err := e.getJSON(ctx, endpoint.String(), &detail); err != nil {
		return smartRecruitersPosting{}, err
	}
	return detail, nil
}

func (e *ATSExtractor) extractComeet(ctx context.Context, source Source) (Result, error) {
	if e.comeetEngine != nil {
		result, err := providerResultToScraper(e.comeetEngine.Extract(ctx, scraperSourceToProvider(source)))
		if err == nil || !comeetHostedPage(source.URL) {
			return result, err
		}
	}
	config, err := comeetConfigFromSource(source)
	if err != nil {
		if comeetHostedPage(source.URL) {
			return e.extractComeetHosted(ctx, source)
		}
		return Result{}, err
	}
	endpoint, err := joinURL(e.comeetBaseURL, "company", config.CompanyUID, "positions")
	if err != nil {
		return Result{}, err
	}
	q := endpoint.Query()
	q.Set("token", config.Token)
	q.Set("details", "true")
	endpoint.RawQuery = q.Encode()

	var payload []comeetPosition
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0, len(payload))
	for _, position := range payload {
		if position.IsInternal {
			continue
		}
		location, country := comeetLocationText(position.Location, position.Locations)
		description := comeetDescription(position.Details)
		applyURL := firstNonEmptyString(position.URLActivePage, position.URLComeetHostedPage, position.URLRecruitHostedPage, position.PositionURL)
		jobToken := firstNonEmptyString(position.UID, stableJobToken(applyURL, position.Name))
		company := sourceCompany(source, firstNonEmptyString(position.CompanyName, config.CompanyUID))
		jobs = append(jobs, JobPosting{
			SourceJobID:    "comeet:" + config.CompanyUID + ":" + jobToken,
			Company:        company,
			Title:          position.Name,
			Location:       location,
			Country:        country,
			EmploymentType: employmentFromText(position.Name, position.EmploymentType),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       parseTimePtr(position.TimeUpdated),
			Live:           true,
			Confidence:     0.91,
			Strategy:       TierATS,
			Evidence: []Evidence{
				{Field: "ats", Text: "Comeet Careers API", URL: endpoint.String()},
				{Field: "description", Text: description, URL: applyURL},
				{Field: "department", Text: position.Department, URL: applyURL},
				{Field: "location", Text: location, URL: applyURL},
			},
		})
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.91,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Comeet Careers API", URL: endpoint.String()},
		},
	})
}

func (e *ATSExtractor) extractComeetHosted(ctx context.Context, source Source) (Result, error) {
	static := NewStaticExtractor(StaticOptions{
		Client:         e.client,
		MaxSitemapURLs: e.comeetHostedMaxJobs,
	})
	result, err := static.Extract(ctx, source)
	if err != nil {
		return Result{}, err
	}
	jobs := result.Jobs
	if len(jobs) > e.comeetHostedMaxJobs {
		jobs = jobs[:e.comeetHostedMaxJobs]
	}
	for i := range jobs {
		jobs[i] = normalizeComeetHostedPosting(source, jobs[i])
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.8,
		Strategy:   TierATS,
		Live:       result.Live,
		FetchedAt:  result.FetchedAt,
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Comeet hosted page JSON-LD or XML sitemap", URL: source.URL},
		},
	})
}

func (e *ATSExtractor) extractWorkday(ctx context.Context, source Source) (Result, error) {
	if e.workdayEngine != nil {
		return providerResultToScraper(e.workdayEngine.Extract(ctx, scraperSourceToProvider(source)))
	}
	config, err := workdayConfigFromSource(source)
	if err != nil {
		return Result{}, err
	}
	searchEndpoint, err := joinURL(config.BaseURL, "wday", "cxs", config.Tenant, config.Site, "jobs")
	if err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0, e.workdayPageSize)
	searchText := sourceSearchText(source)
	pagesFetched, totalAvailable := 0, 0
	hasMore := false
	for page := 0; page < e.workdayMaxPages; page++ {
		offset := page * e.workdayPageSize
		var payload workdaySearchResponse
		req := workdaySearchRequest{
			AppliedFacets: map[string]any{},
			Limit:         e.workdayPageSize,
			Offset:        offset,
			SearchText:    searchText,
		}
		if err := e.postJSON(ctx, searchEndpoint.String(), req, &payload); err != nil {
			return Result{}, err
		}
		pagesFetched++
		totalAvailable = payload.Total
		if len(payload.JobPostings) == 0 {
			hasMore = false
			break
		}
		for _, posting := range payload.JobPostings {
			job, err := e.workdayJobPosting(ctx, source, config, searchEndpoint.String(), posting)
			if err != nil {
				return Result{}, err
			}
			if job.SourceJobID != "" {
				jobs = append(jobs, job)
			}
		}
		hasMore = len(payload.JobPostings) >= e.workdayPageSize && !(payload.Total > 0 && offset+len(payload.JobPostings) >= payload.Total)
		if !hasMore {
			break
		}
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.88,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Workday CXS jobs API", URL: searchEndpoint.String()},
		},
		Diagnostics: scraperPaginationDiagnostics(pagesFetched, e.workdayPageSize, totalAvailable, e.workdayPageSize*e.workdayMaxPages, hasMore),
	})
}

func scraperSourceToProvider(source Source) provider.Source {
	return provider.Source{
		ID:       source.ID,
		Name:     source.Name,
		URL:      source.URL,
		Tier:     provider.Tier(source.Tier),
		Metadata: source.Metadata,
	}
}

func providerResultToScraper(result provider.Result, err error) (Result, error) {
	if err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, len(result.Jobs))
	for _, job := range result.Jobs {
		evidence := make([]Evidence, 0, len(job.Evidence))
		for _, item := range job.Evidence {
			evidence = append(evidence, Evidence{Field: item.Field, Text: item.Text, URL: item.URL})
		}
		jobs = append(jobs, JobPosting{
			SourceJobID:    job.SourceJobID,
			Company:        job.Company,
			Title:          job.Title,
			Location:       job.Location,
			Country:        job.Country,
			EmploymentType: job.EmploymentType,
			Level:          job.Level,
			RoleFamily:     job.RoleFamily,
			SourceURL:      job.SourceURL,
			ApplyURL:       job.ApplyURL,
			PostedAt:       job.PostedAt,
			Live:           job.Live,
			Confidence:     job.Confidence,
			Strategy:       Tier(job.Strategy),
			Evidence:       evidence,
		})
	}
	rawEvidence := make([]Evidence, 0, len(result.RawEvidence))
	for _, item := range result.RawEvidence {
		rawEvidence = append(rawEvidence, Evidence{Field: item.Field, Text: item.Text, URL: item.URL})
	}
	return NormalizeResult(Result{
		Source: Source{
			ID:       result.Source.ID,
			Name:     result.Source.Name,
			URL:      result.Source.URL,
			Tier:     Tier(result.Source.Tier),
			Metadata: result.Source.Metadata,
		},
		Jobs:        jobs,
		RawEvidence: rawEvidence,
		Confidence:  result.Confidence,
		Strategy:    Tier(result.Strategy),
		Live:        result.Live,
		FetchedAt:   result.FetchedAt,
		Diagnostics: copyStringMap(result.Diagnostics),
	})
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func scraperPaginationDiagnostics(pagesFetched, pageSize, totalAvailable, resultLimit int, hasMore bool) map[string]string {
	status, reason := "complete", "all_pages_exhausted"
	if hasMore {
		status, reason = "truncated", "result_or_page_limit_reached"
	}
	return map[string]string{
		"completeness_status": status,
		"completeness_reason": reason,
		"pages_fetched":       strconv.Itoa(pagesFetched),
		"page_size":           strconv.Itoa(pageSize),
		"total_available":     strconv.Itoa(totalAvailable),
		"result_limit":        strconv.Itoa(resultLimit),
		"has_more":            strconv.FormatBool(hasMore),
	}
}

func (e *ATSExtractor) extractBreezy(ctx context.Context, source Source) (Result, error) {
	if e.breezyEngine != nil {
		if result, err := providerResultToScraper(e.breezyEngine.Extract(ctx, scraperSourceToProvider(source))); err == nil {
			return result, nil
		}
	}
	endpoint, err := breezyBoardURL(source.URL)
	if err != nil {
		return Result{}, err
	}

	var payload []breezyPosition
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0, len(payload))
	for _, position := range payload {
		description := cleanHTMLText(position.Description)
		location, country := breezyLocationText(position.Location, position.Locations)
		companySlug := firstNonEmptyString(position.Company.FriendlyID, breezyCompanySlug(source.URL))
		jobToken := firstNonEmptyString(position.ID, position.FriendlyID, stableJobToken(position.URL, position.Name))
		applyURL := firstNonEmptyString(position.URL, breezyHostedURL(source.URL, position.FriendlyID))
		jobs = append(jobs, JobPosting{
			SourceJobID:    "breezy:" + companySlug + ":" + jobToken,
			Company:        sourceCompany(source, firstNonEmptyString(position.Company.Name, companySlug)),
			Title:          position.Name,
			Location:       location,
			Country:        country,
			EmploymentType: employmentFromText(position.Name, position.Type.Name),
			RoleFamily:     inferRoleFamily(position.Name + " " + description),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       parseTimePtr(position.PublishedDate),
			Live:           true,
			Confidence:     0.89,
			Strategy:       TierATS,
			Evidence: []Evidence{
				{Field: "ats", Text: "Breezy public JSON board", URL: endpoint.String()},
				{Field: "description", Text: description, URL: applyURL},
				{Field: "department", Text: position.Department, URL: applyURL},
				{Field: "location", Text: location, URL: applyURL},
			},
		})
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.89,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Breezy public JSON board", URL: endpoint.String()},
		},
	})
}

func (e *ATSExtractor) extractPersonio(ctx context.Context, source Source) (Result, error) {
	if e.personioEngine != nil {
		if result, err := providerResultToScraper(e.personioEngine.Extract(ctx, scraperSourceToProvider(source))); err == nil {
			return result, nil
		}
	}
	endpoint, err := personioFeedURL(source.URL)
	if err != nil {
		return Result{}, err
	}

	var payload personioFeed
	if err := e.getXML(ctx, endpoint.String(), &payload); err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0, len(payload.Positions))
	for _, position := range payload.Positions {
		description := personioDescription(position.Descriptions)
		location := personioLocation(position)
		applyURL := personioApplyURL(endpoint, position.ID)
		jobs = append(jobs, JobPosting{
			SourceJobID:    "personio:" + strings.TrimSpace(position.ID),
			Company:        sourceCompany(source, position.Subcompany),
			Title:          position.Name,
			Location:       location,
			EmploymentType: personioEmployment(position),
			RoleFamily:     inferRoleFamily(position.Name + " " + position.Occupation + " " + position.OccupationCategory + " " + description),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       parseTimePtr(position.CreatedAt),
			Live:           true,
			Confidence:     0.9,
			Strategy:       TierATS,
			Evidence: []Evidence{
				{Field: "ats", Text: "Personio XML job feed", URL: endpoint.String()},
				{Field: "description", Text: description, URL: applyURL},
				{Field: "department", Text: firstNonEmptyString(position.Department, position.RecruitingCategory), URL: applyURL},
				{Field: "location", Text: location, URL: applyURL},
			},
		})
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.9,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Personio XML job feed", URL: endpoint.String()},
		},
	})
}

func (e *ATSExtractor) extractPinpoint(ctx context.Context, source Source) (Result, error) {
	if e.pinpointEngine != nil {
		if result, err := providerResultToScraper(e.pinpointEngine.Extract(ctx, scraperSourceToProvider(source))); err == nil {
			return result, nil
		}
	}
	endpoint, err := pinpointPostingsURL(source.URL)
	if err != nil {
		return Result{}, err
	}

	var payload pinpointResponse
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0, len(payload.Data))
	for _, posting := range payload.Data {
		description := pinpointDescription(posting)
		location := pinpointLocationText(posting.Location)
		jobToken := firstNonEmptyString(posting.ID, stableJobToken(posting.URL, posting.Title))
		jobs = append(jobs, JobPosting{
			SourceJobID:    "pinpoint:" + jobToken,
			Company:        sourceCompany(source, pinpointCompanySlug(source.URL)),
			Title:          posting.Title,
			Location:       location,
			EmploymentType: employmentFromText(posting.Title, firstNonEmptyString(posting.EmploymentTypeText, posting.EmploymentType)),
			RoleFamily:     inferRoleFamily(posting.Title + " " + description),
			SourceURL:      source.URL,
			ApplyURL:       posting.URL,
			Live:           true,
			Confidence:     0.89,
			Strategy:       TierATS,
			Evidence: []Evidence{
				{Field: "ats", Text: "Pinpoint public postings JSON", URL: endpoint.String()},
				{Field: "description", Text: description, URL: posting.URL},
				{Field: "location", Text: location, URL: posting.URL},
				{Field: "workplace_type", Text: firstNonEmptyString(posting.WorkplaceTypeText, posting.WorkplaceType), URL: posting.URL},
			},
		})
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.89,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Pinpoint public postings JSON", URL: endpoint.String()},
		},
	})
}

func (e *ATSExtractor) extractPolymer(ctx context.Context, source Source) (Result, error) {
	organizationSlug, err := polymerOrganizationSlug(source.URL, source.Metadata)
	if err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0)
	rawEvidence := []Evidence{}
	for page := 1; page <= e.polymerMaxPages; page++ {
		payload, endpoint, err := e.polymerJobsPage(ctx, organizationSlug, page)
		if err != nil {
			return Result{}, err
		}
		rawEvidence = append(rawEvidence, Evidence{Field: "ats_endpoint", Text: "Polymer public jobs API", URL: endpoint})

		for _, listing := range payload.Items {
			posting := listing
			detailURL := ""
			if jobID := polymerJobID(listing); jobID != "" {
				detail, endpoint, err := e.polymerJobDetail(ctx, organizationSlug, jobID)
				if err == nil {
					posting = mergePolymerJob(listing, detail)
					detailURL = endpoint
				} else if ctx.Err() != nil {
					return Result{}, err
				}
			}
			jobs = append(jobs, polymerJobPosting(source, organizationSlug, posting, endpoint, detailURL))
		}

		if payload.Meta.IsLast {
			break
		}
		if payload.Meta.NextPage > page {
			page = payload.Meta.NextPage - 1
		}
	}

	return NormalizeResult(Result{
		Source:      source,
		Jobs:        jobs,
		Confidence:  0.88,
		Strategy:    TierATS,
		Live:        true,
		FetchedAt:   time.Now().UTC(),
		RawEvidence: rawEvidence,
	})
}

func (e *ATSExtractor) polymerJobsPage(ctx context.Context, organizationSlug string, page int) (polymerJobsResponse, string, error) {
	endpoint, err := polymerJobsURL(e.polymerPublicBaseURL, organizationSlug, page)
	if err != nil {
		return polymerJobsResponse{}, "", err
	}
	var payload polymerJobsResponse
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return polymerJobsResponse{}, endpoint.String(), err
	}
	return payload, endpoint.String(), nil
}

func (e *ATSExtractor) polymerJobDetail(ctx context.Context, organizationSlug string, jobID string) (polymerJob, string, error) {
	endpoint, err := joinURL(e.polymerPublicBaseURL, organizationSlug, "jobs", jobID)
	if err != nil {
		return polymerJob{}, "", err
	}
	var payload polymerJob
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return polymerJob{}, endpoint.String(), err
	}
	return payload, endpoint.String(), nil
}

func (e *ATSExtractor) extractICIMS(ctx context.Context, source Source) (Result, error) {
	if e.icimsEngine != nil {
		if result, err := providerResultToScraper(e.icimsEngine.Extract(ctx, scraperSourceToProvider(source))); err == nil {
			return result, nil
		}
	}
	sitemapURL, err := icimsSitemapURL(source.URL)
	if err != nil {
		return Result{}, err
	}

	var sitemap icimsSitemap
	if err := e.getXML(ctx, sitemapURL.String(), &sitemap); err != nil {
		return Result{}, err
	}

	staticExtractor := NewStaticExtractor(StaticOptions{Client: e.client})
	entries := icimsSitemapJobs(sitemap)
	totalEntries := len(entries)
	truncated := totalEntries > e.icimsMaxJobs
	if len(entries) > e.icimsMaxJobs {
		entries = entries[:e.icimsMaxJobs]
	}
	jobs := make([]JobPosting, 0, len(entries))
	for _, entry := range entries {
		detailURL, err := icimsDetailURL(entry.Loc)
		if err != nil {
			continue
		}
		document, err := e.getText(ctx, detailURL.String(), "text/html,application/xhtml+xml")
		if err != nil {
			if ctx.Err() != nil {
				return Result{}, err
			}
			continue
		}
		detailBaseURL, err := parseSourceURL(detailURL.String())
		if err != nil {
			continue
		}
		for _, job := range staticExtractor.extractJSONLDJobs(source, detailBaseURL, document) {
			jobs = append(jobs, normalizeICIMSJob(source, entry, detailURL.String(), document, job))
		}
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "iCIMS sitemap and JobPosting detail pages", URL: sitemapURL.String()},
		},
		Diagnostics: scraperPaginationDiagnostics(1, e.icimsMaxJobs, totalEntries, e.icimsMaxJobs, truncated),
	})
}

func (e *ATSExtractor) extractJazzHR(ctx context.Context, source Source) (Result, error) {
	boardURL, err := jazzHRBoardURL(source.URL)
	if err != nil {
		return Result{}, err
	}

	directURL := jazzHRDirectJobLink(source.URL)
	document, err := e.getText(ctx, boardURL.String(), "text/html,application/xhtml+xml")
	links := make([]jazzHRJobLink, 0)
	if err != nil {
		if ctx.Err() != nil || directURL == "" {
			return Result{}, err
		}
	} else {
		links = jazzHRJobLinks(boardURL, document)
	}

	if directURL != "" {
		links = prependUniqueJazzHRLink(links, jazzHRJobLink{URL: directURL})
	}
	if len(links) > e.jazzHRMaxJobs {
		links = links[:e.jazzHRMaxJobs]
	}

	staticExtractor := NewStaticExtractor(StaticOptions{Client: e.client})
	jobs := make([]JobPosting, 0, len(links))
	for _, link := range links {
		detailDocument, err := e.getText(ctx, link.URL, "text/html,application/xhtml+xml")
		if err != nil {
			if ctx.Err() != nil {
				return Result{}, err
			}
			continue
		}
		detailBaseURL, err := parseSourceURL(link.URL)
		if err != nil {
			continue
		}
		for _, job := range staticExtractor.extractJSONLDJobs(source, detailBaseURL, detailDocument) {
			jobs = append(jobs, normalizeJazzHRJob(source, link, job))
		}
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.85,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "JazzHR hosted job board and JobPosting detail pages", URL: boardURL.String()},
		},
	})
}

func (e *ATSExtractor) extractJobvite(ctx context.Context, source Source) (Result, error) {
	if e.jobviteEngine != nil {
		if result, err := providerResultToScraper(e.jobviteEngine.Extract(ctx, scraperSourceToProvider(source))); err == nil {
			return result, nil
		}
	}
	boardURL, err := jobviteBoardURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	companySlug, err := jobviteCompanySlug(source.URL)
	if err != nil {
		return Result{}, err
	}

	directURL := jobviteDirectJobLink(source.URL)
	document, err := e.getText(ctx, boardURL.String(), "text/html,application/xhtml+xml")
	links := make([]jobviteJobLink, 0)
	if err != nil {
		if ctx.Err() != nil || directURL == "" {
			return Result{}, err
		}
	} else {
		links = jobviteJobLinks(boardURL, companySlug, document)
	}

	if directURL != "" {
		links = prependUniqueJobviteLink(links, jobviteJobLink{URL: directURL})
	}
	if len(links) > e.jobviteMaxJobs {
		links = links[:e.jobviteMaxJobs]
	}

	staticExtractor := NewStaticExtractor(StaticOptions{Client: e.client})
	jobs := make([]JobPosting, 0, len(links))
	for _, link := range links {
		detailDocument, err := e.getText(ctx, link.URL, "text/html,application/xhtml+xml")
		if err != nil {
			if ctx.Err() != nil {
				return Result{}, err
			}
			continue
		}
		detailBaseURL, err := parseSourceURL(link.URL)
		if err != nil {
			continue
		}
		detailJobs := staticExtractor.extractJSONLDJobs(source, detailBaseURL, detailDocument)
		if len(detailJobs) == 0 {
			if job, ok := jobvitePostingFromHTML(source, companySlug, detailBaseURL, detailDocument); ok {
				detailJobs = append(detailJobs, job)
			}
		}
		for _, job := range detailJobs {
			jobs = append(jobs, normalizeJobviteJob(source, companySlug, link.URL, detailDocument, job))
		}
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.85,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Jobvite hosted job board and JobPosting detail pages", URL: boardURL.String()},
		},
	})
}

func (e *ATSExtractor) extractTeamtailor(ctx context.Context, source Source) (Result, error) {
	if e.teamtailorEngine != nil {
		if result, err := providerResultToScraper(e.teamtailorEngine.Extract(ctx, scraperSourceToProvider(source))); err == nil {
			return result, nil
		}
	}
	boardURL, err := teamtailorBoardURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	account := teamtailorAccountToken(source.URL)
	if account == "" {
		return Result{}, fmt.Errorf("teamtailor account missing in %q", source.URL)
	}

	directURL := teamtailorDirectJobLink(source.URL)
	document, err := e.getText(ctx, boardURL.String(), "text/html,application/xhtml+xml")
	links := make([]teamtailorJobLink, 0)
	if err != nil {
		if ctx.Err() != nil || directURL == "" {
			return Result{}, err
		}
	} else {
		links = teamtailorJobLinks(boardURL, document)
	}

	if directURL != "" {
		links = prependUniqueTeamtailorLink(links, teamtailorJobLink{URL: directURL})
	}
	if len(links) > e.teamtailorMaxJobs {
		links = links[:e.teamtailorMaxJobs]
	}

	staticExtractor := NewStaticExtractor(StaticOptions{Client: e.client})
	jobs := make([]JobPosting, 0, len(links))
	for _, link := range links {
		detailDocument, err := e.getText(ctx, link.URL, "text/html,application/xhtml+xml")
		if err != nil {
			if ctx.Err() != nil {
				return Result{}, err
			}
			continue
		}
		detailBaseURL, err := parseSourceURL(link.URL)
		if err != nil {
			continue
		}
		for _, job := range staticExtractor.extractJSONLDJobs(source, detailBaseURL, detailDocument) {
			jobs = append(jobs, normalizeTeamtailorJob(source, account, link.URL, detailDocument, job))
		}
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Teamtailor hosted job board and JobPosting detail pages", URL: boardURL.String()},
		},
	})
}

func (e *ATSExtractor) extractBambooHR(ctx context.Context, source Source) (Result, error) {
	if e.bambooHREngine != nil {
		if result, err := providerResultToScraper(e.bambooHREngine.Extract(ctx, scraperSourceToProvider(source))); err == nil {
			return result, nil
		}
	}
	listURL, err := bambooHRListURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	account := bambooHRAccountToken(source.URL)
	if account == "" {
		return Result{}, fmt.Errorf("bamboohr account missing in %q", source.URL)
	}

	directID := bambooHRJobIDFromURL(source.URL)
	var payload bambooHRListResponse
	listErr := e.getJSON(ctx, listURL.String(), &payload)
	summaries := make([]bambooHRListJob, 0)
	if listErr != nil {
		if ctx.Err() != nil || directID == "" {
			return Result{}, listErr
		}
	} else {
		summaries = bambooHRListJobs(payload)
	}
	if directID != "" {
		summaries = prependUniqueBambooHRJob(summaries, bambooHRListJob{ID: directID})
	}
	if len(summaries) > e.bambooHRMaxJobs {
		summaries = summaries[:e.bambooHRMaxJobs]
	}

	jobs := make([]JobPosting, 0, len(summaries))
	for _, summary := range summaries {
		id := strings.TrimSpace(summary.ID)
		if id == "" {
			continue
		}
		detailURL, err := bambooHRDetailURL(source.URL, id)
		if err != nil {
			continue
		}
		var detailPayload bambooHRDetailResponse
		if err := e.getJSON(ctx, detailURL.String(), &detailPayload); err != nil {
			if ctx.Err() != nil {
				return Result{}, err
			}
			if job, ok := bambooHRSummaryPosting(source, account, detailURL.String(), summary); ok {
				jobs = append(jobs, job)
			}
			continue
		}
		if job, ok := bambooHRDetailPosting(source, account, detailURL.String(), summary, detailPayload.Result.JobOpening); ok {
			jobs = append(jobs, job)
		}
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "BambooHR public careers list and detail endpoints", URL: listURL.String()},
		},
	})
}

func (e *ATSExtractor) extractRippling(ctx context.Context, source Source) (Result, error) {
	board, err := ripplingBoardSlug(source.URL)
	if err != nil {
		return Result{}, err
	}
	directID := ripplingJobIDFromURL(source.URL)

	summaries := make([]ripplingJobSummary, 0)
	var listURL *url.URL
	var listErr error
	pagesFetched, totalItems := 0, 0
	hasMore := false
	for page := 0; page < e.ripplingMaxPages; page++ {
		listURL, err = ripplingJobsAPIURL(source.URL, board, page, e.ripplingPageSize)
		if err != nil {
			return Result{}, err
		}
		var payload ripplingJobsResponse
		if err := e.getJSON(ctx, listURL.String(), &payload); err != nil {
			listErr = err
			break
		}
		pagesFetched++
		totalItems = payload.TotalItems
		summaries = mergeRipplingJobSummaries(summaries, payload.Items)
		sourceHasMore := len(payload.Items) >= e.ripplingPageSize && (payload.TotalPages == 0 || page+1 < payload.TotalPages)
		hasMore = sourceHasMore || payload.TotalItems > len(summaries)
		if len(summaries) >= e.ripplingMaxJobs || !sourceHasMore {
			break
		}
	}
	if listErr != nil && len(summaries) == 0 {
		if ctx.Err() != nil || directID == "" {
			return Result{}, listErr
		}
	}
	if directID != "" {
		summaries = prependUniqueRipplingJob(summaries, ripplingJobSummary{ID: directID})
	}
	if len(summaries) > e.ripplingMaxJobs {
		summaries = summaries[:e.ripplingMaxJobs]
	}

	jobs := make([]JobPosting, 0, len(summaries))
	for _, summary := range summaries {
		id := firstNonEmptyString(summary.ID, ripplingJobIDFromURL(summary.URL))
		if id == "" {
			continue
		}
		detailURL, err := ripplingDetailAPIURL(source.URL, board, id)
		if err != nil {
			continue
		}
		var detail ripplingJobDetail
		if err := e.getJSON(ctx, detailURL.String(), &detail); err != nil {
			if ctx.Err() != nil {
				return Result{}, err
			}
			if job, ok := ripplingSummaryPosting(source, board, detailURL.String(), summary); ok {
				jobs = append(jobs, job)
			}
			continue
		}
		if job, ok := ripplingDetailPosting(source, board, detailURL.String(), summary, detail); ok {
			jobs = append(jobs, job)
		}
	}

	evidenceURL := ""
	if listURL != nil {
		evidenceURL = listURL.String()
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Rippling public board and job detail API", URL: evidenceURL},
		},
		Diagnostics: scraperPaginationDiagnostics(pagesFetched, e.ripplingPageSize, totalItems, e.ripplingMaxJobs, hasMore),
	})
}

func (e *ATSExtractor) extractRipplingJobs(ctx context.Context, source Source) (Result, error) {
	static := NewStaticExtractor(StaticOptions{
		Client:         e.client,
		MaxSitemapURLs: e.ripplingMaxJobs,
	})
	result, err := static.Extract(ctx, source)
	if err != nil {
		return Result{}, err
	}
	jobs := result.Jobs
	if len(jobs) > e.ripplingMaxJobs {
		jobs = jobs[:e.ripplingMaxJobs]
	}
	for i := range jobs {
		jobs[i] = normalizeRipplingJobsPosting(source, jobs[i])
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.8,
		Strategy:   TierATS,
		Live:       result.Live,
		FetchedAt:  result.FetchedAt,
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Rippling Jobs hosted page JSON-LD or XML sitemap", URL: source.URL},
		},
	})
}

func (e *ATSExtractor) extractSuccessFactors(ctx context.Context, source Source) (Result, error) {
	feedURL, err := successFactorsFeedURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	account := stableAccountToken(sourceHost(source.URL))
	if account == "" {
		return Result{}, fmt.Errorf("successfactors host missing in %q", source.URL)
	}

	var feed successFactorsRSS
	if err := e.getXML(ctx, feedURL.String(), &feed); err != nil {
		return Result{}, err
	}
	items := successFactorsItems(feed)
	if len(items) > e.successFactorsMaxJobs {
		items = items[:e.successFactorsMaxJobs]
	}

	jobs := make([]JobPosting, 0, len(items))
	for _, item := range items {
		if job, ok := successFactorsPosting(source, account, feedURL.String(), item); ok {
			jobs = append(jobs, job)
		}
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "SAP SuccessFactors Recruiting Marketing RSS job feed", URL: feedURL.String()},
		},
	})
}

func (e *ATSExtractor) extractADPWorkforceNow(ctx context.Context, source Source) (Result, error) {
	config, err := adpWorkforceNowConfigFromURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	account := stableAccountToken(config.CID)
	if account == "" {
		return Result{}, fmt.Errorf("adp workforce now cid missing in %q", source.URL)
	}

	var listURL *url.URL
	var listErr error
	summaries := make([]adpWorkforceNowJob, 0, e.adpWorkforceNowPageSize)
	for page := 0; page < e.adpWorkforceNowMaxPages; page++ {
		skip := page * e.adpWorkforceNowPageSize
		listURL, err = adpWorkforceNowListURL(source.URL, config, skip, e.adpWorkforceNowPageSize)
		if err != nil {
			return Result{}, err
		}
		var payload adpWorkforceNowResponse
		if err := e.getJSON(ctx, listURL.String(), &payload); err != nil {
			listErr = err
			break
		}
		summaries = mergeADPWorkforceNowJobs(summaries, payload.JobRequisitions)
		if len(summaries) >= e.adpWorkforceNowMaxJobs || len(payload.JobRequisitions) < e.adpWorkforceNowPageSize || (payload.Meta.TotalNumber > 0 && len(summaries) >= payload.Meta.TotalNumber) {
			break
		}
	}
	if listErr != nil && len(summaries) == 0 {
		if ctx.Err() != nil || config.JobID == "" {
			return Result{}, listErr
		}
	}
	if config.JobID != "" {
		summaries = prependUniqueADPWorkforceNowJob(summaries, adpWorkforceNowSyntheticJob(config.JobID, config.JWID))
	}
	if len(summaries) > e.adpWorkforceNowMaxJobs {
		summaries = summaries[:e.adpWorkforceNowMaxJobs]
	}

	jobs := make([]JobPosting, 0, len(summaries))
	for _, summary := range summaries {
		id := firstNonEmptyString(adpWorkforceNowJobID(summary), config.JobID)
		if id == "" {
			continue
		}
		detailURL, err := adpWorkforceNowDetailURL(source.URL, config, id)
		if err != nil {
			continue
		}
		var detail adpWorkforceNowJob
		if err := e.getJSON(ctx, detailURL.String(), &detail); err != nil {
			if ctx.Err() != nil {
				return Result{}, err
			}
			if job, ok := adpWorkforceNowPosting(source, config, account, summary, adpWorkforceNowJob{}, detailURL.String()); ok {
				job.Confidence = 0.72
				jobs = append(jobs, job)
			}
			continue
		}
		if job, ok := adpWorkforceNowPosting(source, config, account, summary, detail, detailURL.String()); ok {
			jobs = append(jobs, job)
		}
	}

	evidenceURL := ""
	if listURL != nil {
		evidenceURL = listURL.String()
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "ADP Workforce Now public staffing job requisitions API", URL: evidenceURL},
		},
	})
}

func (e *ATSExtractor) extractADPMyJobs(ctx context.Context, source Source) (Result, error) {
	config, err := adpMyJobsConfigFromURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	account := stableAccountToken(config.Domain)
	if account == "" {
		return Result{}, fmt.Errorf("adp myjobs domain missing in %q", source.URL)
	}

	careerSiteURL, err := adpMyJobsCareerSiteURL(e.adpMyJobsConfigBaseURL, config.Domain)
	if err != nil {
		return Result{}, err
	}
	var careerSite adpMyJobsCareerSite
	if err := e.getJSON(ctx, careerSiteURL.String(), &careerSite); err != nil {
		return Result{}, err
	}
	token := strings.TrimSpace(careerSite.MyJobsToken)
	if token == "" {
		return Result{}, fmt.Errorf("adp myjobs token missing for %q", config.Domain)
	}
	headers := adpMyJobsHeaders(token)

	summaries := make([]adpMyJobsRequisition, 0, e.adpMyJobsPageSize)
	var listURL *url.URL
	var listErr error
	for page := 0; page < e.adpMyJobsMaxPages; page++ {
		skip := page * e.adpMyJobsPageSize
		listURL, err = adpMyJobsListURL(e.adpMyJobsAPIBaseURL, skip, e.adpMyJobsPageSize)
		if err != nil {
			return Result{}, err
		}
		var payload adpMyJobsResponse
		if err := e.getJSONWithHeaders(ctx, listURL.String(), headers, &payload); err != nil {
			listErr = err
			break
		}
		summaries = mergeADPMyJobsRequisitions(summaries, payload.JobRequisitions)
		if len(summaries) >= e.adpMyJobsMaxJobs || len(payload.JobRequisitions) < e.adpMyJobsPageSize || (payload.Count > 0 && len(summaries) >= payload.Count) {
			break
		}
	}
	if config.ReqID != "" {
		summaries = prependUniqueADPMyJobsRequisition(summaries, adpMyJobsSyntheticRequisition(config.ReqID))
	}
	if listErr != nil && len(summaries) == 0 {
		if ctx.Err() != nil || config.ReqID == "" {
			return Result{}, listErr
		}
	}
	if len(summaries) > e.adpMyJobsMaxJobs {
		summaries = summaries[:e.adpMyJobsMaxJobs]
	}

	jobs := make([]JobPosting, 0, len(summaries))
	for _, summary := range summaries {
		id := firstNonEmptyString(adpMyJobsReqID(summary), config.ReqID)
		if id == "" {
			continue
		}
		detailURL, err := adpMyJobsDetailAPIURL(e.adpMyJobsAPIBaseURL, id)
		if err != nil {
			continue
		}
		var detailPayload adpMyJobsResponse
		if err := e.getJSONWithHeaders(ctx, detailURL.String(), headers, &detailPayload); err != nil {
			if ctx.Err() != nil {
				return Result{}, err
			}
			if job, ok := adpMyJobsJobPosting(source, config, account, careerSite, summary, adpMyJobsRequisition{}, detailURL.String()); ok {
				job.Confidence = 0.72
				jobs = append(jobs, job)
			}
			continue
		}
		detail := firstADPMyJobsRequisition(detailPayload.JobRequisitions)
		if job, ok := adpMyJobsJobPosting(source, config, account, careerSite, summary, detail, detailURL.String()); ok {
			jobs = append(jobs, job)
		}
	}

	evidenceURL := careerSiteURL.String()
	if listURL != nil {
		evidenceURL = listURL.String()
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "ADP MyJobs public staffing job requisitions API", URL: evidenceURL},
		},
	})
}

func (e *ATSExtractor) extractUKGPro(ctx context.Context, source Source) (Result, error) {
	config, err := ukgProConfigFromURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	page, err := e.getText(ctx, source.URL, "text/html")
	if err != nil {
		return Result{}, err
	}
	opportunities, htmlErr := ukgProOpportunitiesFromHTML(page)
	evidenceText := "UKG Pro Recruiting hydrated job board"
	evidenceURL := source.URL
	if htmlErr != nil || len(opportunities) == 0 {
		if loadURL := ukgProLoadSearchURL(source.URL, page); loadURL != "" {
			loaded, err := e.extractUKGProLoadSearch(ctx, loadURL)
			if err != nil {
				return Result{}, err
			}
			opportunities = loaded
			evidenceText = "UKG Pro Recruiting load search results API"
			evidenceURL = loadURL
		} else if htmlErr != nil {
			return Result{}, htmlErr
		}
	}
	if len(opportunities) > e.ukgMaxJobs {
		opportunities = opportunities[:e.ukgMaxJobs]
	}
	detailTemplate := ukgProStringConfigValue(page, "opportunityLinkUrl")
	account := stableAccountToken(config.Account)
	jobs := make([]JobPosting, 0, len(opportunities))
	for _, opportunity := range opportunities {
		if job, ok := ukgProPosting(source, config, account, detailTemplate, evidenceText, opportunity); ok {
			jobs = append(jobs, job)
		}
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.84,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: evidenceText, URL: evidenceURL},
		},
	})
}

func (e *ATSExtractor) extractUKGProLoadSearch(ctx context.Context, loadURL string) ([]ukgProOpportunity, error) {
	opportunities := make([]ukgProOpportunity, 0, e.ukgMaxJobs)
	for skip := 0; len(opportunities) < e.ukgMaxJobs; {
		top := e.ukgMaxJobs - len(opportunities)
		if top > 50 {
			top = 50
		}
		req := ukgProLoadSearchRequest{
			OpportunitySearch: ukgProOpportunitySearch{
				QueryString:    "",
				Filters:        []any{},
				Top:            top,
				Skip:           skip,
				LocationIDs:    []string{},
				JobCategoryIDs: []string{},
				OrderBy: []ukgProSearchOrder{
					{Value: "postedDateDesc", PropertyName: "PostedDate", Ascending: false},
				},
			},
		}
		var payload ukgProLoadSearchResponse
		if err := e.postJSON(ctx, loadURL, req, &payload); err != nil {
			return nil, err
		}
		rawCount := len(payload.Opportunities)
		opportunities = mergeUKGProOpportunities(opportunities, ukgProCleanOpportunities(payload.Opportunities))
		if rawCount == 0 || rawCount < top || (payload.TotalCount > 0 && skip+rawCount >= payload.TotalCount) {
			break
		}
		skip += rawCount
	}
	return opportunities, nil
}

func (e *ATSExtractor) extractDayforce(ctx context.Context, source Source) (Result, error) {
	config, err := dayforceConfigFromURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	if config.JobID == "" {
		return e.extractDayforceBoard(ctx, source, config)
	}
	page, err := e.getText(ctx, source.URL, "text/html")
	if err != nil {
		return Result{}, err
	}
	nextData, err := dayforceNextDataFromHTML(page)
	if err != nil {
		return Result{}, err
	}
	if nextData.Props.PageProps.JobData.JobPostingID == 0 && strings.TrimSpace(nextData.Props.PageProps.JobData.JobTitle) == "" {
		return Result{}, ErrNoJobs
	}
	if nextData.Query.ClientNamespace != "" {
		config.ClientNamespace = nextData.Query.ClientNamespace
	}
	if nextData.Query.CareerSiteXRefCode != "" {
		config.JobBoardCode = nextData.Query.CareerSiteXRefCode
	}
	if nextData.Query.ID != "" {
		config.JobID = nextData.Query.ID
	}
	job, ok := dayforcePosting(source, config, nextData.Props.PageProps.JobData)
	if !ok {
		return Result{}, ErrNoJobs
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       []JobPosting{job},
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Dayforce Next.js job detail payload", URL: source.URL},
		},
	})
}

func (e *ATSExtractor) extractDayforceBoard(ctx context.Context, source Source, config dayforceConfig) (Result, error) {
	endpoint, err := dayforceSearchEndpoint(source.URL, config)
	if err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, e.dayforcePageSize)
	for page := 0; page < e.dayforceMaxPages && len(jobs) < e.dayforceMaxJobs; page++ {
		start := page * e.dayforcePageSize
		request := dayforceSearchRequest{
			ClientNamespace: strings.TrimSpace(config.ClientNamespace),
			JobBoardCode:    strings.TrimSpace(config.JobBoardCode),
			CultureCode:     firstNonEmptyString(config.Culture, "en-US"),
			DistanceUnit:    0,
			PaginationStart: start,
			PageSize:        e.dayforcePageSize,
		}
		var payload dayforceSearchResponse
		if err := e.postJSON(ctx, endpoint.String(), request, &payload); err != nil {
			return Result{}, err
		}
		if len(payload.JobPostings) == 0 {
			break
		}
		for _, posting := range payload.JobPostings {
			if len(jobs) >= e.dayforceMaxJobs {
				break
			}
			job, ok := dayforcePosting(source, config, posting)
			if ok {
				jobs = append(jobs, job)
			}
		}
		if payload.MaxCount > 0 && start+len(payload.JobPostings) >= payload.MaxCount {
			break
		}
		if payload.Count > 0 && payload.Count < e.dayforcePageSize {
			break
		}
		if len(payload.JobPostings) < e.dayforcePageSize {
			break
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.84,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Dayforce geo jobposting search API", URL: endpoint.String()},
		},
	})
}

func (e *ATSExtractor) extractByteDanceCareers(ctx context.Context, source Source) (Result, error) {
	endpoint, err := joinURL(e.byteDanceBaseURL, "search", "job", "posts")
	if err != nil {
		return Result{}, err
	}
	keyword := byteDanceKeyword(source.URL)
	if keyword == "" {
		keyword = "software engineer intern"
	}
	headers := map[string]string{
		"Origin":          "https://joinbytedance.com",
		"accept-language": "en",
		"website-path":    "en",
		"x-tt-env":        "boe_epam_api",
	}
	jobs := make([]JobPosting, 0, e.byteDancePageSize)
	seen := make(map[string]struct{}, e.byteDancePageSize)
	pagesFetched, totalAvailable, offset := 0, 0, 0
	hasMore := false
	for page := 0; page < e.byteDanceMaxPages && len(jobs) < e.byteDanceMaxJobs; page++ {
		request := byteDanceSearchRequest{Keyword: keyword, Limit: e.byteDancePageSize, Offset: offset}
		var payload byteDanceSearchResponse
		if err := e.postJSONWithHeaders(ctx, endpoint.String(), request, headers, &payload); err != nil {
			return Result{}, err
		}
		if payload.Code != 0 {
			return Result{}, fmt.Errorf("bytedance careers search failed: code %d", payload.Code)
		}
		pagesFetched++
		if payload.Data.Count > totalAvailable {
			totalAvailable = payload.Data.Count
		}
		items := payload.Data.JobPostList
		if len(items) == 0 {
			hasMore = false
			break
		}
		for _, item := range items {
			if len(jobs) >= e.byteDanceMaxJobs {
				hasMore = true
				break
			}
			job, ok := byteDancePosting(source, endpoint.String(), item)
			if !ok {
				continue
			}
			if _, duplicate := seen[job.SourceJobID]; duplicate {
				continue
			}
			seen[job.SourceJobID] = struct{}{}
			jobs = append(jobs, job)
		}
		offset += len(items)
		hasMore = (totalAvailable > 0 && offset < totalAvailable) || len(items) == e.byteDancePageSize
		if !hasMore || len(items) < e.byteDancePageSize {
			break
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	if totalAvailable == 0 {
		totalAvailable = offset
	}
	if pagesFetched >= e.byteDanceMaxPages && totalAvailable > offset {
		hasMore = true
	}
	if len(jobs) >= e.byteDanceMaxJobs && totalAvailable > len(jobs) {
		hasMore = true
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.88,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "ByteDance public supplier search API", URL: endpoint.String()},
		},
		Diagnostics: scraperPaginationDiagnostics(pagesFetched, e.byteDancePageSize, totalAvailable, e.byteDanceMaxJobs, hasMore),
	})
}

func (e *ATSExtractor) extractIMCCareers(ctx context.Context, source Source) (Result, error) {
	page, err := e.getText(ctx, source.URL, "text/html")
	if err != nil {
		return Result{}, err
	}
	matches := imcCareersCardPattern.FindAllStringSubmatch(page, -1)
	jobs := make([]JobPosting, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		job, ok := imcCareersPosting(source, match[1], match[2], match[3])
		if !ok {
			continue
		}
		if _, exists := seen[job.SourceJobID]; exists {
			continue
		}
		seen[job.SourceJobID] = struct{}{}
		jobs = append(jobs, job)
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.78,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "IMC careers server-rendered job cards", URL: source.URL},
		},
	})
}

func (e *ATSExtractor) extractOracleRecruiting(ctx context.Context, source Source) (Result, error) {
	if e.oracleRecruitingEngine != nil {
		if result, err := providerResultToScraper(e.oracleRecruitingEngine.Extract(ctx, scraperSourceToProvider(source))); err == nil {
			return result, nil
		}
	}
	config, err := oracleRecruitingConfigFromURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	if config.JobID != "" {
		job, ok := e.oracleRecruitingDetailPosting(ctx, source, config)
		if !ok {
			return Result{}, ErrNoJobs
		}
		return NormalizeResult(Result{
			Source:     source,
			Jobs:       []JobPosting{job},
			Confidence: 0.84,
			Strategy:   TierATS,
			Live:       true,
			FetchedAt:  time.Now().UTC(),
			RawEvidence: []Evidence{
				{Field: "ats_endpoint", Text: "Oracle Recruiting Candidate Experience detail page", URL: job.ApplyURL},
			},
		})
	}

	jobs := make([]JobPosting, 0, e.oracleRecruitingMaxJobs)
	var evidenceURL string
	for page := 0; page < e.oracleRecruitingMaxPages && len(jobs) < e.oracleRecruitingMaxJobs; page++ {
		offset := page * e.oracleRecruitingPageSize
		endpoint, err := oracleRecruitingSearchURL(source.URL, config, e.oracleRecruitingPageSize, offset)
		if err != nil {
			return Result{}, err
		}
		evidenceURL = endpoint.String()
		var payload oracleRecruitingResponse
		if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
			return Result{}, err
		}
		requisitions, total := oracleRecruitingRequisitions(payload)
		if len(requisitions) == 0 {
			break
		}
		for _, requisition := range requisitions {
			if len(jobs) >= e.oracleRecruitingMaxJobs {
				break
			}
			detailURL := oracleRecruitingJobURL(source.URL, config, requisition.ID)
			detail := oracleRecruitingDetail{}
			if detailURL != "" {
				if pageText, err := e.getText(ctx, detailURL, "text/html"); err == nil {
					detail = oracleRecruitingDetailFromHTML(pageText)
				}
			}
			if job, ok := oracleRecruitingPosting(source, config, requisition, detailURL, detail); ok {
				jobs = append(jobs, job)
			}
		}
		if total > 0 && offset+len(requisitions) >= total {
			break
		}
		if len(requisitions) < e.oracleRecruitingPageSize {
			break
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}

	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.85,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Oracle Recruiting Candidate Experience public requisitions API", URL: evidenceURL},
		},
	})
}

func (e *ATSExtractor) oracleRecruitingDetailPosting(ctx context.Context, source Source, config oracleRecruitingConfig) (JobPosting, bool) {
	detailURL := oracleRecruitingJobURL(source.URL, config, config.JobID)
	page, err := e.getText(ctx, firstNonEmptyString(detailURL, source.URL), "text/html")
	if err != nil {
		return JobPosting{}, false
	}
	detail := oracleRecruitingDetailFromHTML(page)
	req := oracleRecruitingRequisition{
		ID:                  config.JobID,
		Title:               detail.Title,
		ShortDescriptionStr: detail.Description,
	}
	return oracleRecruitingPosting(source, config, req, firstNonEmptyString(detailURL, source.URL), detail)
}

func (e *ATSExtractor) extractPaylocity(ctx context.Context, source Source) (Result, error) {
	config, err := paylocityConfigFromURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	if config.JobID != "" {
		job, ok := e.paylocityDetailPosting(ctx, source, config)
		if !ok {
			return Result{}, ErrNoJobs
		}
		return NormalizeResult(Result{
			Source:     source,
			Jobs:       []JobPosting{job},
			Confidence: 0.84,
			Strategy:   TierATS,
			Live:       true,
			FetchedAt:  time.Now().UTC(),
			RawEvidence: []Evidence{
				{Field: "ats_endpoint", Text: "Paylocity hosted job detail JSON-LD", URL: job.ApplyURL},
			},
		})
	}
	if config.FeedAPI {
		return e.extractPaylocityFeed(ctx, source, config)
	}

	page, err := e.getText(ctx, source.URL, "text/html")
	if err != nil {
		return Result{}, err
	}
	pageData, err := paylocityPageDataFromHTML(page)
	if err != nil {
		return Result{}, err
	}
	company := firstNonEmptyString(pageData.ModuleTitle, pageData.DisplayName, config.CompanySlug)
	jobs := make([]JobPosting, 0, len(pageData.Jobs))
	for _, item := range pageData.Jobs {
		if len(jobs) >= e.paylocityMaxJobs {
			break
		}
		if item.IsInternal {
			continue
		}
		id := paylocityJobID(item.JobID)
		detailURL := paylocityHostedURL(source.URL, "Details", id, company, item.JobTitle)
		detail := paylocityDetail{}
		if detailURL != "" {
			if document, err := e.getText(ctx, detailURL, "text/html"); err == nil {
				detail = paylocityDetailFromHTML(source, detailURL, document)
			}
		}
		if job, ok := paylocityPosting(source, config, company, item, detail); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.84,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Paylocity hosted jobs pageData and detail JSON-LD", URL: source.URL},
		},
	})
}

func (e *ATSExtractor) extractPaylocityFeed(ctx context.Context, source Source, config paylocityConfig) (Result, error) {
	payload, err := e.getText(ctx, source.URL, "application/json,application/xml,text/xml")
	if err != nil {
		return Result{}, err
	}
	feed, err := paylocityFeedFromPayload([]byte(payload))
	if err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, len(feed.jobs()))
	for _, item := range feed.jobs() {
		if len(jobs) >= e.paylocityMaxJobs {
			break
		}
		if job, ok := paylocityFeedPosting(source, config, feed, item); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.84,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Paylocity public job feed " + feed.format(), URL: source.URL},
		},
	})
}

func (e *ATSExtractor) extractPaycom(ctx context.Context, source Source) (Result, error) {
	document, err := e.getText(ctx, source.URL, "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	session := paycomPortalSessionFromHTML(document)
	if session.SessionJWT == "" {
		return Result{}, ErrNoJobs
	}
	if override := strings.TrimSpace(source.Metadata["paycom_service_url"]); override != "" {
		session.ServiceURL = override
	}
	if session.ServiceURL == "" {
		session.ServiceURL = "https://portal-applicant-tracking.us-cent.paycomonline.net/"
	}

	if jobID := paycomJobIDFromURL(source.URL); jobID != "" {
		detail, ok, err := e.paycomDetail(ctx, session, jobID)
		if err != nil {
			return Result{}, err
		}
		if !ok {
			return Result{}, ErrNoJobs
		}
		job, ok := paycomPosting(source, paycomJobPreview{JobID: detail.JobID, JobTitle: detail.JobTitle, Locations: detail.Location}, detail)
		if !ok {
			return Result{}, ErrNoJobs
		}
		return NormalizeResult(Result{
			Source:     source,
			Jobs:       []JobPosting{job},
			Confidence: 0.84,
			Strategy:   TierATS,
			Live:       true,
			FetchedAt:  time.Now().UTC(),
			RawEvidence: []Evidence{
				{Field: "ats_endpoint", Text: "Paycom public job detail API", URL: paycomAPIURL(session.ServiceURL, "api/ats/job-postings/"+jobID)},
			},
		})
	}

	var response paycomPreviewSearchResponse
	payload := paycomPreviewSearchPayload{
		Skip: 0,
		Take: e.paycomMaxJobs,
		FiltersForQuery: paycomPreviewFilters{
			Categories:        []string{},
			Departments:       []string{},
			EmploymentTypes:   []string{},
			Locations:         []string{},
			PositionTypes:     []string{},
			TravelTypes:       []string{},
			ShiftTypes:        []string{},
			OtherFilters:      []string{},
			KeywordSearchText: "",
			Location:          "",
			SortOption:        "",
		},
	}
	searchURL := paycomAPIURL(session.ServiceURL, "api/ats/job-posting-previews/search")
	if err := e.postJSONWithHeaders(ctx, searchURL, payload, paycomHeaders(session), &response); err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, min(len(response.JobPostingPreviews), e.paycomMaxJobs))
	detailCount := 0
	for _, preview := range response.JobPostingPreviews {
		if len(jobs) >= e.paycomMaxJobs {
			break
		}
		detail := paycomJobDetail{}
		if detailCount < e.paycomDetailMaxJobs && preview.id() != "" {
			if enriched, ok, err := e.paycomDetail(ctx, session, preview.id()); err == nil && ok {
				detail = enriched
				detailCount++
			}
		}
		if job, ok := paycomPosting(source, preview, detail); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.82,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Paycom public job posting previews API", URL: searchURL},
		},
	})
}

func (e *ATSExtractor) paycomDetail(ctx context.Context, session paycomPortalSession, jobID string) (paycomJobDetail, bool, error) {
	var response paycomDetailResponse
	endpoint := paycomAPIURL(session.ServiceURL, "api/ats/job-postings/"+strings.TrimSpace(jobID))
	if err := e.getJSONWithHeaders(ctx, endpoint, paycomHeaders(session), &response); err != nil {
		return paycomJobDetail{}, false, err
	}
	if response.JobPosting.JobID == 0 && strings.TrimSpace(response.JobPosting.JobTitle) == "" {
		return paycomJobDetail{}, false, nil
	}
	return response.JobPosting, true, nil
}

func (e *ATSExtractor) extractDover(ctx context.Context, source Source) (Result, error) {
	slug := doverSlugFromURL(source.URL)
	if slug == "" {
		return Result{}, ErrNoJobs
	}
	baseURL := doverBaseURL(source.URL)
	var page doverCareersPage
	pageURL := baseURL + "/api/v1/careers-page-slug/" + url.PathEscape(slug)
	if err := e.getJSON(ctx, pageURL, &page); err != nil {
		return Result{}, err
	}
	if page.ID == "" {
		return Result{}, ErrNoJobs
	}

	var response doverJobsResponse
	jobsURL := baseURL + "/api/v1/careers-page/" + url.PathEscape(page.ID) + "/jobs"
	if err := e.getJSON(ctx, jobsURL, &response); err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0, min(len(response.Results), e.doverMaxJobs))
	for _, item := range response.Results {
		if len(jobs) >= e.doverMaxJobs {
			break
		}
		if job, ok := doverPosting(source, slug, page, item); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.84,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Dover public careers-page jobs API", URL: jobsURL},
		},
	})
}

const (
	optiverPageSize        = 16
	optiverMaxJobs         = 200
	metaCareersSearchDocID = "27129360303422352"
	metaCareersCountDocID  = "26210170368675892"
)

var metaCareersLSDPattern = regexp.MustCompile(`\["LSD",\[\],\{"token":"([^"]+)"`)

type metaCareersSearchResponse struct {
	Data struct {
		Search struct {
			Jobs []metaCareersJob `json:"all_jobs"`
		} `json:"job_search_with_featured_jobs_v2"`
	} `json:"data"`
	Errors []json.RawMessage `json:"errors"`
}

type metaCareersCountResponse struct {
	Data struct {
		Search struct {
			JobCount int `json:"job_count"`
		} `json:"job_search_with_featured_jobs_v2"`
	} `json:"data"`
	Errors []json.RawMessage `json:"errors"`
}

type metaCareersJob struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Locations []string `json:"locations"`
	Teams     []string `json:"teams"`
	SubTeams  []string `json:"sub_teams"`
}

func (e *ATSExtractor) extractMetaCareers(ctx context.Context, source Source) (Result, error) {
	pageURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	queryText := firstNonEmptyString(pageURL.Query().Get("q"), "software engineer intern")
	pageURL.Path = "/jobsearch/"
	pageURL.RawQuery = url.Values{"q": []string{queryText}}.Encode()
	pageURL.Fragment = ""
	document, err := e.getText(ctx, pageURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	match := metaCareersLSDPattern.FindStringSubmatch(document)
	if len(match) != 2 || strings.TrimSpace(match[1]) == "" {
		return Result{}, errors.New("meta careers bootstrap token missing")
	}
	lsd := match[1]
	variables, err := json.Marshal(map[string]any{
		"search_input": map[string]any{
			"q": queryText, "divisions": []string{}, "offices": []string{}, "roles": []string{},
			"leadership_levels": []string{}, "saved_jobs": []string{}, "saved_searches": []string{},
			"sub_teams": []string{}, "teams": []string{}, "is_leadership": false,
			"is_remote_only": false, "sort_by_new": false, "results_per_page": nil,
		},
		"viewasUserID": nil,
		"isLoggedIn":   false,
	})
	if err != nil {
		return Result{}, err
	}
	form := url.Values{
		"lsd":                      []string{lsd},
		"fb_api_caller_class":      []string{"RelayModern"},
		"fb_api_req_friendly_name": []string{"CareersJobSearchResultsV2DataQuery"},
		"variables":                []string{string(variables)},
		"doc_id":                   []string{firstNonEmptyString(source.Metadata["meta_doc_id"], metaCareersSearchDocID)},
	}
	endpoint := *pageURL
	endpoint.Path = "/graphql"
	endpoint.RawQuery = ""
	var payload metaCareersSearchResponse
	if err := e.postForm(ctx, endpoint.String(), form, map[string]string{
		"x-fb-friendly-name": "CareersJobSearchResultsV2DataQuery",
		"x-fb-lsd":           lsd,
		"Referer":            pageURL.String(),
	}, &payload); err != nil {
		return Result{}, err
	}
	if len(payload.Errors) > 0 {
		return Result{}, errors.New("meta careers graphql error")
	}
	countVariables, err := json.Marshal(map[string]any{"search_input": map[string]any{
		"q": queryText, "divisions": []string{}, "offices": []string{}, "roles": []string{},
		"leadership_levels": []string{}, "saved_jobs": []string{}, "saved_searches": []string{},
		"sub_teams": []string{}, "teams": []string{}, "is_leadership": false,
		"is_remote_only": false, "sort_by_new": false, "page": 1, "results_per_page": nil,
	}})
	if err != nil {
		return Result{}, err
	}
	countForm := url.Values{
		"lsd":                      []string{lsd},
		"fb_api_caller_class":      []string{"RelayModern"},
		"fb_api_req_friendly_name": []string{"CareersJobSearchHideFiltersBarV2Query"},
		"variables":                []string{string(countVariables)},
		"doc_id":                   []string{firstNonEmptyString(source.Metadata["meta_count_doc_id"], metaCareersCountDocID)},
	}
	var countPayload metaCareersCountResponse
	if err := e.postForm(ctx, endpoint.String(), countForm, map[string]string{
		"x-fb-friendly-name": "CareersJobSearchHideFiltersBarV2Query",
		"x-fb-lsd":           lsd,
		"Referer":            pageURL.String(),
	}, &countPayload); err != nil {
		return Result{}, err
	}
	if len(countPayload.Errors) > 0 || countPayload.Data.Search.JobCount <= 0 {
		return Result{}, errors.New("meta careers count query error")
	}
	jobs := make([]JobPosting, 0, len(payload.Data.Search.Jobs))
	for _, item := range payload.Data.Search.Jobs {
		id, title := strings.TrimSpace(item.ID), strings.TrimSpace(item.Title)
		if id == "" || title == "" {
			continue
		}
		applyURL := pageURL.ResolveReference(&url.URL{Path: "/profile/job_details/" + url.PathEscape(id)}).String()
		contextText := strings.Join(append(append([]string{title}, item.Teams...), item.SubTeams...), " ")
		jobs = append(jobs, JobPosting{
			SourceJobID:    "meta:" + id,
			Company:        sourceCompany(source, "Meta"),
			Title:          title,
			Location:       strings.Join(compactStringList(item.Locations...), "; "),
			EmploymentType: employmentFromText(contextText, ""),
			Level:          inferLevel(contextText),
			RoleFamily:     inferRoleFamily(contextText),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			Live:           true,
			Confidence:     0.9,
			Strategy:       TierATS,
			Evidence: []Evidence{
				{Field: "ats", Text: "Meta Careers official GraphQL search", URL: endpoint.String()},
				{Field: "team", Text: strings.Join(item.Teams, "; "), URL: applyURL},
				{Field: "sub_team", Text: strings.Join(item.SubTeams, "; "), URL: applyURL},
			},
		})
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:      source,
		Jobs:        jobs,
		Confidence:  0.9,
		Strategy:    TierATS,
		Live:        true,
		FetchedAt:   time.Now().UTC(),
		Diagnostics: scraperPaginationDiagnostics(1, len(jobs), countPayload.Data.Search.JobCount, len(jobs), countPayload.Data.Search.JobCount > len(jobs)),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Meta Careers official GraphQL search", URL: endpoint.String()},
		},
	})
}

type optiverJobsResponse struct {
	Items      []optiverJob `json:"items"`
	TotalCount int          `json:"totalCount"`
}

type optiverJob struct {
	Title       string `json:"title"`
	Location    string `json:"location"`
	Experience  string `json:"experience"`
	Domain      string `json:"domain"`
	Href        string `json:"href"`
	ComponentID int    `json:"componentID"`
}

func (e *ATSExtractor) extractOptiverCareers(ctx context.Context, source Source) (Result, error) {
	boardURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	endpoint := *boardURL
	endpoint.Path = "/en/api/v1/jobs"
	endpoint.RawQuery = ""
	endpoint.Fragment = ""

	jobs := make([]JobPosting, 0, optiverMaxJobs)
	pagesFetched, totalAvailable := 0, 0
	for offset := 0; offset < optiverMaxJobs; offset += optiverPageSize {
		pageURL := endpoint
		query := pageURL.Query()
		query.Set("from", strconv.Itoa(offset))
		query.Set("size", strconv.Itoa(optiverPageSize))
		pageURL.RawQuery = query.Encode()

		var response optiverJobsResponse
		if err := e.getJSON(ctx, pageURL.String(), &response); err != nil {
			return Result{}, err
		}
		pagesFetched++
		totalAvailable = response.TotalCount
		for _, item := range response.Items {
			applyURL, err := boardURL.Parse(strings.TrimSpace(item.Href))
			if err != nil || strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Href) == "" {
				continue
			}
			identity := strings.TrimSpace(item.Href)
			if item.ComponentID != 0 {
				identity = strconv.Itoa(item.ComponentID)
			}
			jobs = append(jobs, JobPosting{
				SourceJobID:    "optiver:" + identity,
				Company:        sourceCompany(source, "Optiver"),
				Title:          item.Title,
				Location:       item.Location,
				EmploymentType: item.Experience,
				RoleFamily:     item.Domain,
				SourceURL:      applyURL.String(),
				ApplyURL:       applyURL.String(),
				Live:           true,
				Confidence:     0.9,
				Strategy:       TierATS,
				Evidence: []Evidence{
					{Field: "experience", Text: item.Experience, URL: applyURL.String()},
					{Field: "domain", Text: item.Domain, URL: applyURL.String()},
				},
			})
			if len(jobs) >= optiverMaxJobs {
				break
			}
		}
		if len(response.Items) == 0 || offset+len(response.Items) >= response.TotalCount || len(jobs) >= optiverMaxJobs {
			break
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	hasMore := totalAvailable > len(jobs)
	return NormalizeResult(Result{
		Source:      source,
		Jobs:        jobs,
		Confidence:  0.9,
		Strategy:    TierATS,
		Live:        true,
		FetchedAt:   time.Now().UTC(),
		Diagnostics: scraperPaginationDiagnostics(pagesFetched, optiverPageSize, totalAvailable, optiverMaxJobs, hasMore),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Optiver public jobs API", URL: endpoint.String()},
		},
	})
}

func (e *ATSExtractor) extractTrakstar(ctx context.Context, source Source) (Result, error) {
	feedURL, boardSlug, err := trakstarFeedURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	payload, err := e.getText(ctx, feedURL.String(), "application/rss+xml,application/xml,text/xml")
	if err != nil {
		return Result{}, err
	}
	var feed trakstarRSS
	if err := xml.Unmarshal([]byte(payload), &feed); err != nil {
		return Result{}, err
	}
	items := trakstarItems(feed)
	totalAvailable := len(items)
	truncated := totalAvailable > e.trakstarMaxJobs
	if len(items) > e.trakstarMaxJobs {
		items = items[:e.trakstarMaxJobs]
	}
	jobs := make([]JobPosting, 0, len(items))
	for _, item := range items {
		if job, ok := trakstarPosting(source, boardSlug, feedURL.String(), feed, item); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:      source,
		Jobs:        jobs,
		Confidence:  0.84,
		Strategy:    TierATS,
		Live:        true,
		FetchedAt:   time.Now().UTC(),
		Diagnostics: scraperPaginationDiagnostics(1, totalAvailable, totalAvailable, e.trakstarMaxJobs, truncated),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Trakstar Hire Recruiterbox RSS job feed", URL: feedURL.String()},
		},
	})
}

func (e *ATSExtractor) extractAvature(ctx context.Context, source Source) (Result, error) {
	pageURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	if avatureJobIDFromURL(pageURL.String()) != "" {
		job, ok, err := e.avatureDetailPosting(ctx, source, pageURL.String())
		if err != nil {
			return Result{}, err
		}
		if !ok {
			return Result{}, ErrNoJobs
		}
		return NormalizeResult(Result{
			Source:     source,
			Jobs:       []JobPosting{job},
			Confidence: 0.84,
			Strategy:   TierATS,
			Live:       true,
			FetchedAt:  time.Now().UTC(),
			RawEvidence: []Evidence{
				{Field: "ats_endpoint", Text: "Avature JobDetail page", URL: pageURL.String()},
			},
		})
	}
	feedURL := avatureFeedURL(pageURL, e.avatureMaxJobs)
	payload, err := e.getText(ctx, feedURL, "application/rss+xml,application/xml,text/xml")
	if err != nil {
		return Result{}, err
	}
	var feed avatureRSS
	if err := xml.Unmarshal([]byte(payload), &feed); err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, min(len(feed.Channel.Items), e.avatureMaxJobs))
	detailCount := 0
	for _, item := range feed.Channel.Items {
		if len(jobs) >= e.avatureMaxJobs {
			break
		}
		detail := avatureDetail{}
		if detailCount < e.avatureDetailMaxJobs && strings.TrimSpace(item.link()) != "" {
			if enriched, ok, err := e.avatureDetail(ctx, item.link()); err == nil && ok {
				detail = enriched
				detailCount++
			}
		}
		if job, ok := avaturePosting(source, feedURL, item, detail); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.84,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Avature public SearchJobs RSS feed", URL: feedURL},
		},
	})
}

func (e *ATSExtractor) avatureDetailPosting(ctx context.Context, source Source, detailURL string) (JobPosting, bool, error) {
	detail, ok, err := e.avatureDetail(ctx, detailURL)
	if err != nil || !ok {
		return JobPosting{}, false, err
	}
	item := avatureRSSItem{
		Title:   detail.Title,
		Link:    detailURL,
		GUID:    avatureGUID{Value: detailURL},
		PubDate: detail.PostedAtText,
	}
	job, normalized := avaturePosting(source, detailURL, item, detail)
	return job, normalized, nil
}

func (e *ATSExtractor) avatureDetail(ctx context.Context, detailURL string) (avatureDetail, bool, error) {
	document, err := e.getText(ctx, detailURL, "text/html,application/xhtml+xml")
	if err != nil {
		return avatureDetail{}, false, err
	}
	title := firstNonEmptyString(htmlMetaContent(document, "og:title"), avatureTitleFromDocument(document))
	location := cleanHTMLText(avatureFieldValue(document, "Location"))
	businessArea := cleanHTMLText(avatureFieldValue(document, "Business Area"))
	description := cleanHTMLText(firstRegexpGroup(avatureRichTextPattern, document))
	applyURL := avatureResolveURL(detailURL, firstRegexpGroup(avatureApplyPattern, document))
	if title == "" && description == "" {
		return avatureDetail{}, false, nil
	}
	return avatureDetail{
		Title:        title,
		Location:     location,
		BusinessArea: businessArea,
		Description:  description,
		ApplyURL:     applyURL,
		PostedAtText: "",
	}, true, nil
}

func (e *ATSExtractor) extractJobylon(ctx context.Context, source Source) (Result, error) {
	if !jobylonFeedSourceURL(source.URL) {
		return e.extractJobylonBoard(ctx, source)
	}
	feed, err := e.jobylonFeed(ctx, source.URL)
	if err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, len(feed.Jobs))
	for _, item := range feed.Jobs {
		if len(jobs) >= e.jobylonMaxJobs {
			break
		}
		if job, ok := jobylonPosting(source, item); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Jobylon public feed", URL: source.URL},
		},
	})
}

func (e *ATSExtractor) extractJobylonBoard(ctx context.Context, source Source) (Result, error) {
	boardURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, boardURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	links := jobylonBoardDetailLinks(boardURL, document, e.jobylonMaxJobs)
	if len(links) == 0 {
		return Result{}, ErrNoJobs
	}

	static := NewStaticExtractor()
	jobs := make([]JobPosting, 0, len(links))
	for _, link := range links {
		if len(jobs) >= e.jobylonMaxJobs {
			break
		}
		detailURL, err := parseSourceURL(link)
		if err != nil {
			continue
		}
		detailDocument, err := e.getText(ctx, detailURL.String(), "text/html,application/xhtml+xml")
		if err != nil {
			continue
		}
		detailSource := source
		detailSource.URL = detailURL.String()
		for _, job := range static.extractJSONLDJobs(detailSource, detailURL, detailDocument) {
			if normalized, ok := normalizeJobylonDetailPosting(source, detailURL, job); ok {
				jobs = append(jobs, normalized)
				break
			}
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.84,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Jobylon hosted board and JobPosting detail pages", URL: boardURL.String()},
		},
	})
}

func (e *ATSExtractor) jobylonFeed(ctx context.Context, endpoint string) (jobylonFeed, error) {
	payload, err := e.getText(ctx, endpoint, "application/json,application/xml,text/xml")
	if err != nil {
		return jobylonFeed{}, err
	}
	trimmed := bytes.TrimSpace([]byte(payload))
	if len(trimmed) == 0 {
		return jobylonFeed{}, ErrNoJobs
	}
	var feed jobylonFeed
	if trimmed[0] == '{' || trimmed[0] == '[' {
		if trimmed[0] == '[' {
			var jobs []jobylonJob
			if err := json.Unmarshal(trimmed, &jobs); err != nil {
				return jobylonFeed{}, err
			}
			return jobylonFeed{Jobs: jobs}, nil
		}
		if err := json.Unmarshal(trimmed, &feed); err != nil {
			return jobylonFeed{}, err
		}
		return feed, nil
	}
	if err := xml.Unmarshal(trimmed, &feed); err != nil {
		return jobylonFeed{}, err
	}
	return feed, nil
}

func (e *ATSExtractor) extractZohoRecruit(ctx context.Context, source Source) (Result, error) {
	boardURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, boardURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	rows, err := zohoRecruitRows(document)
	if err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, min(len(rows), e.zohoRecruitMaxJobs))
	for _, row := range rows {
		if len(jobs) >= e.zohoRecruitMaxJobs {
			break
		}
		if row.Publish != nil && !*row.Publish {
			continue
		}
		if job, ok := zohoRecruitPosting(source, boardURL, row); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.76,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Zoho Recruit career-site jobs payload", URL: boardURL.String()},
		},
	})
}

func (e *ATSExtractor) extractManatal(ctx context.Context, source Source) (Result, error) {
	boardURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, boardURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	links := manatalJobLinks(boardURL, document)
	if len(links) == 0 {
		return Result{}, ErrNoJobs
	}
	static := NewStaticExtractor(StaticOptions{
		Client:         e.client,
		MaxSitemapURLs: 1,
	})
	jobs := make([]JobPosting, 0, min(len(links), e.manatalMaxJobs))
	for _, link := range links {
		if len(jobs) >= e.manatalMaxJobs {
			break
		}
		detailSource := source
		detailSource.URL = link.URL
		result, err := static.Extract(ctx, detailSource)
		if err != nil || len(result.Jobs) == 0 {
			if job, ok := manatalFallbackPosting(source, link); ok {
				jobs = append(jobs, job)
			}
			continue
		}
		job := result.Jobs[0]
		jobs = append(jobs, normalizeManatalPosting(source, link, job))
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.8,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Manatal hosted career page with JSON-LD detail pages", URL: boardURL.String()},
		},
	})
}

func (e *ATSExtractor) extractJOIN(ctx context.Context, source Source) (Result, error) {
	pageURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, pageURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}

	if joinJobIDFromURL(pageURL) != "" {
		static := NewStaticExtractor(StaticOptions{Client: e.client, MaxSitemapURLs: 1})
		result, err := static.Extract(ctx, source)
		if err != nil {
			return Result{}, err
		}
		if len(result.Jobs) == 0 {
			return Result{}, ErrNoJobs
		}
		jobs := make([]JobPosting, 0, min(len(result.Jobs), e.joinMaxJobs))
		for _, job := range result.Jobs {
			if len(jobs) >= e.joinMaxJobs {
				break
			}
			jobs = append(jobs, normalizeJOINDetailPosting(source, pageURL, job))
		}
		return NormalizeResult(Result{
			Source:     source,
			Jobs:       jobs,
			Confidence: 0.83,
			Strategy:   TierATS,
			Live:       true,
			FetchedAt:  time.Now().UTC(),
			RawEvidence: []Evidence{
				{Field: "ats_endpoint", Text: "JOIN hosted JobPosting detail JSON-LD", URL: pageURL.String()},
			},
		})
	}

	board, err := joinBoardFromHTML(document)
	if err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, min(len(board.State.Jobs.Items), e.joinMaxJobs))
	for _, item := range board.State.Jobs.Items {
		if len(jobs) >= e.joinMaxJobs {
			break
		}
		if job, ok := joinPosting(source, pageURL, board, item); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.83,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "JOIN hosted company page Next.js jobs state", URL: pageURL.String()},
		},
	})
}

func (e *ATSExtractor) extractOccupop(ctx context.Context, source Source) (Result, error) {
	pageURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, pageURL.String(), "text/html,application/xhtml+xml,application/json")
	if err != nil {
		return Result{}, err
	}

	if occupopSharedJobID(pageURL) != "" {
		static := NewStaticExtractor(StaticOptions{Client: e.client, MaxSitemapURLs: 1})
		result, err := static.Extract(ctx, source)
		if err == nil && len(result.Jobs) > 0 {
			jobs := make([]JobPosting, 0, min(len(result.Jobs), e.occupopMaxJobs))
			for _, job := range result.Jobs {
				if len(jobs) >= e.occupopMaxJobs {
					break
				}
				jobs = append(jobs, normalizeOccupopDetailPosting(source, pageURL, job))
			}
			return NormalizeResult(Result{
				Source:     source,
				Jobs:       jobs,
				Confidence: 0.78,
				Strategy:   TierATS,
				Live:       true,
				FetchedAt:  time.Now().UTC(),
				RawEvidence: []Evidence{
					{Field: "ats_endpoint", Text: "Occupop shared job detail", URL: pageURL.String()},
				},
			})
		}
	}

	rows := occupopRows(pageURL, document)
	jobs := make([]JobPosting, 0, min(len(rows), e.occupopMaxJobs))
	for _, row := range rows {
		if len(jobs) >= e.occupopMaxJobs {
			break
		}
		if job, ok := occupopPosting(source, pageURL, row); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.78,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Occupop jobs-frame hosted board", URL: pageURL.String()},
		},
	})
}

func (e *ATSExtractor) extractWorkstream(ctx context.Context, source Source) (Result, error) {
	pageURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, pageURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	static := NewStaticExtractor(StaticOptions{Client: e.client, MaxSitemapURLs: 1})

	if workstreamDetailJobID(pageURL) != "" {
		jobs := static.extractJSONLDJobs(source, pageURL, document)
		if len(jobs) == 0 {
			return Result{}, ErrNoJobs
		}
		normalized := make([]JobPosting, 0, min(len(jobs), e.workstreamMaxJobs))
		for _, job := range jobs {
			if len(normalized) >= e.workstreamMaxJobs {
				break
			}
			normalized = append(normalized, normalizeWorkstreamDetailPosting(source, pageURL, workstreamCard{}, job))
		}
		return NormalizeResult(Result{
			Source:     source,
			Jobs:       normalized,
			Confidence: 0.84,
			Strategy:   TierATS,
			Live:       true,
			FetchedAt:  time.Now().UTC(),
			RawEvidence: []Evidence{
				{Field: "ats_endpoint", Text: "Workstream hosted JobPosting detail JSON-LD", URL: pageURL.String()},
			},
		})
	}

	cards := workstreamCards(pageURL, document)
	if positionsURL := workstreamPositionsURL(pageURL, document); positionsURL != "" && positionsURL != pageURL.String() {
		positionsDocument, err := e.getText(ctx, positionsURL, "text/html,application/xhtml+xml")
		if err == nil {
			positionsBase, parseErr := parseSourceURL(positionsURL)
			if parseErr == nil {
				cards = append(cards, workstreamCards(positionsBase, positionsDocument)...)
			}
		}
	}
	cards = dedupeWorkstreamCards(cards)
	jobs := make([]JobPosting, 0, min(len(cards), e.workstreamMaxJobs))
	detailAttempts := 0
	for _, card := range cards {
		if len(jobs) >= e.workstreamMaxJobs {
			break
		}
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if detailAttempts < e.workstreamDetailMaxJobs && card.URL != "" {
			detailAttempts++
			detailDocument, err := e.getText(ctx, card.URL, "text/html,application/xhtml+xml")
			if err == nil {
				if detailURL, parseErr := parseSourceURL(card.URL); parseErr == nil {
					detailJobs := static.extractJSONLDJobs(source, detailURL, detailDocument)
					if len(detailJobs) > 0 {
						jobs = append(jobs, normalizeWorkstreamDetailPosting(source, detailURL, card, detailJobs[0]))
						continue
					}
				}
			}
		}
		if job, ok := workstreamCardPosting(source, pageURL, card); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.82,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Workstream hosted board cards with bounded detail JSON-LD enrichment", URL: pageURL.String()},
		},
	})
}

func (e *ATSExtractor) extractCareerPlug(ctx context.Context, source Source) (Result, error) {
	pageURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, pageURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	static := NewStaticExtractor(StaticOptions{Client: e.client, MaxSitemapURLs: 1})
	extracted := static.extractJSONLDJobs(source, pageURL, document)
	jobs := make([]JobPosting, 0, min(len(extracted), e.careerPlugMaxJobs))
	for _, job := range extracted {
		if len(jobs) >= e.careerPlugMaxJobs {
			break
		}
		if normalized, ok := normalizeCareerPlugPosting(source, pageURL, job); ok {
			jobs = append(jobs, normalized)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.84,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "CareerPlug hosted JobPosting JSON-LD", URL: pageURL.String()},
		},
	})
}

func (e *ATSExtractor) extractJibe(ctx context.Context, source Source) (Result, error) {
	pageURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, pageURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	static := NewStaticExtractor(StaticOptions{Client: e.client, MaxSitemapURLs: 1})
	extracted := static.extractJSONLDJobs(source, pageURL, document)
	jobs := make([]JobPosting, 0, min(len(extracted), e.jibeMaxJobs))
	for _, job := range extracted {
		if len(jobs) >= e.jibeMaxJobs {
			break
		}
		if normalized, ok := normalizeJibePosting(source, pageURL, job); ok {
			jobs = append(jobs, normalized)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.82,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Jibe/Radancy hosted JobPosting JSON-LD", URL: pageURL.String()},
		},
	})
}

func (e *ATSExtractor) extractJobScore(ctx context.Context, source Source) (Result, error) {
	pageURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, pageURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	static := NewStaticExtractor(StaticOptions{Client: e.client, MaxSitemapURLs: 1})
	extracted := static.extractJSONLDJobs(source, pageURL, document)
	jobs := make([]JobPosting, 0, min(len(extracted), e.jobScoreMaxJobs))
	for _, job := range extracted {
		if len(jobs) >= e.jobScoreMaxJobs {
			break
		}
		if normalized, ok := normalizeJobScorePosting(source, pageURL, job); ok {
			jobs = append(jobs, normalized)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.82,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "JobScore hosted JobPosting JSON-LD", URL: pageURL.String()},
		},
	})
}

func (e *ATSExtractor) extractTalentBrew(ctx context.Context, source Source) (Result, error) {
	pageURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, pageURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	static := NewStaticExtractor(StaticOptions{Client: e.client, MaxSitemapURLs: 1})
	extracted := static.extractJSONLDJobs(source, pageURL, document)
	jobs := make([]JobPosting, 0, min(len(extracted), e.talentBrewMaxJobs))
	for _, job := range extracted {
		if len(jobs) >= e.talentBrewMaxJobs {
			break
		}
		if normalized, ok := normalizeTalentBrewPosting(source, pageURL, job); ok {
			jobs = append(jobs, normalized)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.82,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "TalentBrew hosted JobPosting JSON-LD", URL: pageURL.String()},
		},
	})
}

func (e *ATSExtractor) extractHireology(ctx context.Context, source Source) (Result, error) {
	pageURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, pageURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	if id := hireologyJobIDFromURL(pageURL.String()); id != "" {
		jobs := e.extractHireologyJSONLDJobs(source, pageURL, document)
		if len(jobs) == 0 {
			return Result{}, ErrNoJobs
		}
		return NormalizeResult(Result{
			Source:     source,
			Jobs:       jobs,
			Confidence: 0.84,
			Strategy:   TierATS,
			Live:       true,
			FetchedAt:  time.Now().UTC(),
			RawEvidence: []Evidence{
				{Field: "ats_endpoint", Text: "Hireology hosted JobPosting JSON-LD", URL: pageURL.String()},
			},
		})
	}
	config, err := hireologyConfigFromHTML(document)
	if err != nil {
		jobs := e.extractHireologyJSONLDJobs(source, pageURL, document)
		if len(jobs) == 0 {
			return Result{}, err
		}
		return NormalizeResult(Result{
			Source:     source,
			Jobs:       jobs,
			Confidence: 0.82,
			Strategy:   TierATS,
			Live:       true,
			FetchedAt:  time.Now().UTC(),
			RawEvidence: []Evidence{
				{Field: "ats_endpoint", Text: "Hireology hosted JobPosting JSON-LD", URL: pageURL.String()},
			},
		})
	}
	endpoint, err := hireologyJobsEndpoint(config)
	if err != nil {
		return Result{}, err
	}
	var payload hireologyJobsResponse
	headers := map[string]string{"Authorization": "Bearer " + config.APIToken}
	if err := e.getJSONWithHeaders(ctx, endpoint, headers, &payload); err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, min(len(payload.Data), e.hireologyMaxJobs))
	for _, item := range payload.Data {
		if len(jobs) >= e.hireologyMaxJobs {
			break
		}
		if job, ok := hireologyPosting(source, pageURL, config, item, endpoint); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Hireology public careers API", URL: endpoint},
		},
	})
}

func (e *ATSExtractor) extractHireologyJSONLDJobs(source Source, pageURL *url.URL, document string) []JobPosting {
	static := NewStaticExtractor(StaticOptions{Client: e.client, MaxSitemapURLs: 1})
	extracted := static.extractJSONLDJobs(source, pageURL, document)
	jobs := make([]JobPosting, 0, min(len(extracted), e.hireologyMaxJobs))
	for _, job := range extracted {
		if len(jobs) >= e.hireologyMaxJobs {
			break
		}
		if normalized, ok := normalizeHireologyJSONLDPosting(source, pageURL, job); ok {
			jobs = append(jobs, normalized)
		}
	}
	return jobs
}

func (e *ATSExtractor) extractGem(ctx context.Context, source Source) (Result, error) {
	pageURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	boardID := gemBoardID(pageURL)
	if boardID == "" {
		return Result{}, ErrNoJobs
	}
	endpoint := gemGraphQLEndpoint(pageURL)
	var payload gemBoardGraphQLResponse
	request := gemGraphQLRequest{
		OperationName: "JobBoardList",
		Variables: map[string]string{
			"boardId": boardID,
		},
		Query: gemBoardListQuery,
	}
	if err := e.postJSON(ctx, endpoint, request, &payload); err != nil {
		return Result{}, err
	}
	if len(payload.Errors) > 0 {
		return Result{}, fmt.Errorf("gem graphql error: %s", payload.Errors[0].Message)
	}
	items := payload.Data.Postings.JobPostings
	jobs := make([]JobPosting, 0, min(len(items), e.gemMaxJobs))
	detailCount := 0
	for _, item := range items {
		if len(jobs) >= e.gemMaxJobs {
			break
		}
		detail := gemExternalJobPosting{}
		detailEndpoint := endpoint
		if detailCount < e.gemDetailMaxJobs && strings.TrimSpace(item.ExtID) != "" {
			if enriched, err := e.fetchGemDetail(ctx, endpoint, boardID, item.ExtID); err == nil {
				detail = enriched
				detailEndpoint = endpoint
				detailCount++
			}
		}
		if job, ok := gemPosting(source, pageURL, boardID, payload.Data.Board, item, detail, detailEndpoint); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Gem public job board GraphQL", URL: endpoint},
		},
	})
}

func (e *ATSExtractor) fetchGemDetail(ctx context.Context, endpoint string, boardID string, extID string) (gemExternalJobPosting, error) {
	var payload gemDetailGraphQLResponse
	request := gemGraphQLRequest{
		OperationName: "ExternalJobPostingQuery",
		Variables: map[string]string{
			"boardId": boardID,
			"extId":   extID,
		},
		Query: gemExternalJobPostingQuery,
	}
	if err := e.postJSON(ctx, endpoint, request, &payload); err != nil {
		return gemExternalJobPosting{}, err
	}
	if len(payload.Errors) > 0 {
		return gemExternalJobPosting{}, fmt.Errorf("gem detail graphql error: %s", payload.Errors[0].Message)
	}
	return payload.Data.Posting, nil
}

func (e *ATSExtractor) extractJobsoid(ctx context.Context, source Source) (Result, error) {
	endpoint, err := jobsoidJobsEndpoint(source.URL)
	if err != nil {
		return Result{}, err
	}
	var payload jobsoidJobsResponse
	if err := e.getJSON(ctx, endpoint, &payload); err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, len(payload.jobs()))
	for _, item := range payload.jobs() {
		if len(jobs) >= e.jobsoidMaxJobs {
			break
		}
		if job, ok := jobsoidPosting(source, item, endpoint); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.85,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Jobsoid published jobs API", URL: endpoint},
		},
	})
}

func (e *ATSExtractor) extractFreshteam(ctx context.Context, source Source) (Result, error) {
	endpoint, err := freshteamJobsEndpoint(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, endpoint, "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	cards := freshteamJobCards(document)
	jobs := make([]JobPosting, 0, len(cards))
	for _, card := range cards {
		if len(jobs) >= e.freshteamMaxJobs {
			break
		}
		if job, ok := freshteamPosting(source, endpoint, card); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.78,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Freshteam hosted careers page", URL: endpoint},
		},
	})
}

func (e *ATSExtractor) extractHomerun(ctx context.Context, source Source) (Result, error) {
	static := NewStaticExtractor(StaticOptions{
		Client:         e.client,
		MaxSitemapURLs: e.homerunMaxJobs,
	})
	result, err := static.Extract(ctx, source)
	if err != nil {
		return Result{}, err
	}
	jobs := result.Jobs
	if len(jobs) > e.homerunMaxJobs {
		jobs = jobs[:e.homerunMaxJobs]
	}
	for i := range jobs {
		jobs[i].SourceJobID = "homerun:" + strings.TrimPrefix(jobs[i].SourceJobID, "homerun:")
		jobs[i].Strategy = TierATS
		jobs[i].Confidence = 0.8
		jobs[i].Evidence = append(jobs[i].Evidence, Evidence{Field: "ats", Text: "Homerun hosted careers page JSON-LD or sitemap", URL: source.URL})
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.8,
		Strategy:   TierATS,
		Live:       result.Live,
		FetchedAt:  result.FetchedAt,
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Homerun hosted careers page JSON-LD or XML sitemap", URL: source.URL},
		},
	})
}

func (e *ATSExtractor) extractCATSOne(ctx context.Context, source Source) (Result, error) {
	static := NewStaticExtractor(StaticOptions{
		Client:         e.client,
		MaxSitemapURLs: e.catsOneMaxJobs,
	})
	result, err := static.Extract(ctx, source)
	if err != nil {
		return Result{}, err
	}
	jobs := result.Jobs
	if len(jobs) > e.catsOneMaxJobs {
		jobs = jobs[:e.catsOneMaxJobs]
	}
	for i := range jobs {
		jobs[i].SourceJobID = "catsone:" + strings.TrimPrefix(jobs[i].SourceJobID, "catsone:")
		jobs[i].Strategy = TierATS
		jobs[i].Confidence = 0.8
		jobs[i].Evidence = append(jobs[i].Evidence, Evidence{Field: "ats", Text: "CATS hosted careers portal JSON-LD or sitemap", URL: source.URL})
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.8,
		Strategy:   TierATS,
		Live:       result.Live,
		FetchedAt:  result.FetchedAt,
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "CATS hosted careers portal JSON-LD or XML sitemap", URL: source.URL},
		},
	})
}

func (e *ATSExtractor) extractHiBobHiring(ctx context.Context, source Source) (Result, error) {
	static := NewStaticExtractor(StaticOptions{
		Client:         e.client,
		MaxSitemapURLs: e.hibobHiringMaxJobs,
	})
	result, err := static.Extract(ctx, source)
	if err != nil {
		return Result{}, err
	}
	jobs := result.Jobs
	if len(jobs) > e.hibobHiringMaxJobs {
		jobs = jobs[:e.hibobHiringMaxJobs]
	}
	for i := range jobs {
		jobs[i] = normalizeHiBobHiringPosting(source, jobs[i])
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.8,
		Strategy:   TierATS,
		Live:       result.Live,
		FetchedAt:  result.FetchedAt,
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "HiBob Hiring hosted careers page JSON-LD or XML sitemap", URL: source.URL},
		},
	})
}

func (e *ATSExtractor) extractFountain(ctx context.Context, source Source) (Result, error) {
	static := NewStaticExtractor(StaticOptions{
		Client:         e.client,
		MaxSitemapURLs: e.fountainMaxJobs,
	})
	result, err := static.Extract(ctx, source)
	if err != nil {
		return Result{}, err
	}
	jobs := result.Jobs
	if len(jobs) > e.fountainMaxJobs {
		jobs = jobs[:e.fountainMaxJobs]
	}
	for i := range jobs {
		jobs[i] = normalizeFountainPosting(source, jobs[i])
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.78,
		Strategy:   TierATS,
		Live:       result.Live,
		FetchedAt:  result.FetchedAt,
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Fountain hosted careers page JSON-LD or XML sitemap", URL: source.URL},
		},
	})
}

func (e *ATSExtractor) extractApplicantPro(ctx context.Context, source Source) (Result, error) {
	boardURL, err := applicantProBoardURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, boardURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	domainID := firstRegexpGroup(applicantProDomainIDPattern, document)
	if domainID == "" {
		return Result{}, errors.New("applicantpro domain_id not found")
	}
	endpoint, err := applicantProJobsEndpoint(boardURL, domainID)
	if err != nil {
		return Result{}, err
	}
	var payload applicantProJobsResponse
	headers := map[string]string{"Referer": boardURL.String()}
	if err := e.getJSONWithHeaders(ctx, endpoint, headers, &payload); err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, len(payload.Data.Jobs))
	for _, item := range payload.Data.Jobs {
		if len(jobs) >= e.applicantProMaxJobs {
			break
		}
		detailURL := applicantProDetailEndpoint(boardURL, domainID, rawJSONToken(item.ID))
		detail := applicantProJobDetail{}
		if detailURL != "" {
			var detailPayload applicantProDetailResponse
			if err := e.getJSONWithHeaders(ctx, detailURL, headers, &detailPayload); err == nil {
				detail = detailPayload.Data
			} else if ctx.Err() != nil {
				return Result{}, err
			}
		}
		if job, ok := applicantProPosting(source, item, detail, endpoint); ok {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "ApplicantPro public jobs API", URL: endpoint},
		},
	})
}

func (e *ATSExtractor) extractTalentLyft(ctx context.Context, source Source) (Result, error) {
	subdomain, err := talentLyftSubdomain(source.URL)
	if err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, e.talentLyftMaxJobs)
	endpointEvidence := ""
	headers := talentLyftHeaders(source)

	for page := 1; page <= e.talentLyftMaxPages && len(jobs) < e.talentLyftMaxJobs; page++ {
		endpoint, err := talentLyftJobsEndpoint(e.talentLyftBaseURL, subdomain, page, e.talentLyftPageSize, e.talentLyftDetailPages)
		if err != nil {
			return Result{}, err
		}
		if endpointEvidence == "" {
			endpointEvidence = endpoint
		}
		var payload talentLyftJobsResponse
		if err := e.getJSONWithHeaders(ctx, endpoint, headers, &payload); err != nil {
			return Result{}, err
		}
		for _, item := range payload.results() {
			if len(jobs) >= e.talentLyftMaxJobs {
				break
			}
			if job, ok := talentLyftPosting(source, subdomain, item, endpoint); ok {
				jobs = append(jobs, job)
			}
		}
		if !payload.hasNextPage(page) {
			break
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "TalentLyft public jobs API", URL: endpointEvidence},
		},
	})
}

func (e *ATSExtractor) extractPhenomPeople(ctx context.Context, source Source) (Result, error) {
	config := phenomPeopleConfig{}
	jobs := make([]JobPosting, 0, e.phenomPeopleMaxJobs)
	seen := map[string]struct{}{}
	offset := 0
	total := 0
	evidenceURL := source.URL

	for page := 0; page < e.phenomPeopleMaxPages && len(jobs) < e.phenomPeopleMaxJobs; page++ {
		endpoint := source.URL
		if page > 0 {
			nextEndpoint, err := phenomPeopleSearchURL(source.URL, offset)
			if err != nil {
				return Result{}, err
			}
			endpoint = nextEndpoint
		}
		document, err := e.getText(ctx, endpoint, "text/html,application/xhtml+xml")
		if err != nil {
			return Result{}, err
		}
		evidenceURL = endpoint
		if config.RefNum == "" {
			pageConfig, err := phenomPeopleConfigFromHTML(document)
			if err != nil {
				return Result{}, err
			}
			config = pageConfig
		}
		ddo, err := phenomPeopleDDOFromHTML(document)
		if err != nil {
			return Result{}, err
		}
		data := ddo.refineData()
		if total == 0 {
			total = data.TotalHits
		}
		if len(data.Hits) == 0 {
			break
		}
		for _, hit := range data.Hits {
			offset++
			if len(jobs) >= e.phenomPeopleMaxJobs {
				break
			}
			job, ok := phenomPeoplePosting(source, config, hit)
			if !ok {
				continue
			}
			if _, exists := seen[job.SourceJobID]; exists {
				continue
			}
			seen[job.SourceJobID] = struct{}{}
			jobs = append(jobs, job)
		}
		if total > 0 && offset >= total {
			break
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.84,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Phenom People embedded search-results DDO", URL: evidenceURL},
		},
	})
}

func (e *ATSExtractor) extractStripeJobs(ctx context.Context, source Source) (Result, error) {
	pageURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, pageURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	jobs := stripeJobsPostings(source, pageURL, document, e.stripeJobsMaxJobs)
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.84,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Stripe official jobs search table", URL: pageURL.String()},
		},
	})
}

func (e *ATSExtractor) extractAppleJobs(ctx context.Context, source Source) (Result, error) {
	document, err := e.getText(ctx, source.URL, "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	data, err := appleJobsHydrationFromHTML(document)
	if err != nil {
		return Result{}, err
	}
	locale := firstNonEmptyString(data.LoaderData.Root.Locale, appleJobsLocaleFromURLString(source.URL, "en-us"))
	results := data.LoaderData.Search.SearchResults
	if len(results) > e.appleJobsMaxJobs {
		results = results[:e.appleJobsMaxJobs]
	}
	jobs := make([]JobPosting, 0, len(results))
	seen := map[string]struct{}{}
	detailAttempts := 0
	for _, item := range results {
		if detailAttempts < e.appleJobsDetailMaxJobs {
			detailAttempts++
			if detail, err := e.appleJobsDetailPosting(ctx, source, locale, item); err == nil {
				item = appleJobsMergePosting(item, detail)
			}
		}
		job, ok := appleJobsJobPosting(source, locale, item)
		if !ok {
			continue
		}
		if _, exists := seen[job.SourceJobID]; exists {
			continue
		}
		seen[job.SourceJobID] = struct{}{}
		jobs = append(jobs, job)
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	evidenceText := "Apple Jobs static-router hydration data"
	if data.LoaderData.Search.TotalRecords > 0 {
		evidenceText = fmt.Sprintf("%s (%d total records)", evidenceText, data.LoaderData.Search.TotalRecords)
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: evidenceText, URL: source.URL},
		},
	})
}

func (e *ATSExtractor) appleJobsDetailPosting(ctx context.Context, source Source, locale string, item appleJobsPosting) (appleJobsPosting, error) {
	detailURL := appleJobsDetailURL(source, locale, item)
	if detailURL == "" || detailURL == source.URL {
		return appleJobsPosting{}, errors.New("apple jobs detail URL unavailable")
	}
	document, err := e.getText(ctx, detailURL, "text/html,application/xhtml+xml")
	if err != nil {
		return appleJobsPosting{}, err
	}
	postings, err := appleJobsPostingsFromHTML(document)
	if err != nil {
		return appleJobsPosting{}, err
	}
	wantID := firstNonEmptyString(item.ID, item.ReqID, item.JobPositionID, item.PositionID)
	for _, posting := range postings {
		if firstNonEmptyString(posting.ID, posting.ReqID, posting.JobPositionID, posting.PositionID) == wantID {
			return posting, nil
		}
	}
	if len(postings) > 0 {
		return postings[0], nil
	}
	return appleJobsPosting{}, ErrNoJobs
}

func (e *ATSExtractor) extractAmazonJobs(ctx context.Context, source Source) (Result, error) {
	endpoint := amazonJobsSearchEndpoint(source.URL)
	if endpoint == "" {
		return Result{}, errors.New("amazon jobs search endpoint is required")
	}
	jobs := make([]JobPosting, 0, e.amazonJobsPageSize)
	seen := map[string]struct{}{}
	total := 0
	detailAttempts := 0
	for page := 0; page < e.amazonJobsMaxPages && len(jobs) < e.amazonJobsMaxJobs; page++ {
		start := page * e.amazonJobsPageSize
		request := amazonJobsSearchPayload(source.URL, start, e.amazonJobsPageSize)
		var payload amazonJobsSearchResponse
		if err := e.postJSON(ctx, endpoint, request, &payload); err != nil {
			return Result{}, err
		}
		if total == 0 {
			total = payload.Found
		}
		if len(payload.SearchHits) == 0 {
			break
		}
		for _, hit := range payload.SearchHits {
			if len(jobs) >= e.amazonJobsMaxJobs {
				break
			}
			var detail JobPosting
			if detailAttempts < e.amazonJobsDetailMaxJobs {
				detailAttempts++
				if enriched, err := e.amazonJobsDetailPosting(ctx, source, hit); err == nil {
					detail = enriched
				}
			}
			job, ok := amazonJobsJobPosting(source, hit, detail)
			if !ok {
				continue
			}
			if _, exists := seen[job.SourceJobID]; exists {
				continue
			}
			seen[job.SourceJobID] = struct{}{}
			jobs = append(jobs, job)
		}
		if total > 0 && start+len(payload.SearchHits) >= total {
			break
		}
		if len(payload.SearchHits) < e.amazonJobsPageSize {
			break
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	evidenceText := "Amazon Jobs search API"
	if total > 0 {
		evidenceText = fmt.Sprintf("%s (%d total records)", evidenceText, total)
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: evidenceText, URL: endpoint},
		},
	})
}

func (e *ATSExtractor) extractEightfoldPCSX(ctx context.Context, source Source) (Result, error) {
	config, err := eightfoldPCSXConfigFromSource(source)
	if err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, e.eightfoldPCSXMaxJobs)
	seen := map[string]struct{}{}
	total := 0
	lastEndpoint := ""
	detailAttempts := 0
	start := 0
	for page := 0; page < e.eightfoldPCSXMaxPages && len(jobs) < e.eightfoldPCSXMaxJobs; page++ {
		endpoint := eightfoldPCSXSearchURL(config, start)
		lastEndpoint = endpoint
		var payload eightfoldPCSXSearchResponse
		if err := e.getJSON(ctx, endpoint, &payload); err != nil {
			return Result{}, err
		}
		if err := payload.searchErr(); err != nil {
			return Result{}, err
		}
		if total == 0 {
			total = payload.Data.Count
		}
		if len(payload.Data.Positions) == 0 {
			break
		}
		for _, position := range payload.Data.Positions {
			if len(jobs) >= e.eightfoldPCSXMaxJobs {
				break
			}
			detail := eightfoldPCSXPosition{}
			if detailAttempts < e.eightfoldPCSXDetailMaxJobs {
				detailAttempts++
				if enriched, err := e.eightfoldPCSXDetail(ctx, config, position.ID); err == nil {
					detail = enriched
				} else if ctx.Err() != nil {
					return Result{}, err
				}
			}
			job, ok := eightfoldPCSXPosting(source, config, position, detail)
			if !ok {
				continue
			}
			if _, exists := seen[job.SourceJobID]; exists {
				continue
			}
			seen[job.SourceJobID] = struct{}{}
			jobs = append(jobs, job)
		}
		start += len(payload.Data.Positions)
		if total > 0 && start >= total {
			break
		}
		if len(payload.Data.Positions) == 0 {
			break
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	evidenceText := "Eightfold PCSX search API"
	if total > 0 {
		evidenceText = fmt.Sprintf("%s (%d total records)", evidenceText, total)
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.84,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: evidenceText, URL: lastEndpoint},
		},
	})
}

func (e *ATSExtractor) eightfoldPCSXDetail(ctx context.Context, config eightfoldPCSXConfig, id int64) (eightfoldPCSXPosition, error) {
	if id <= 0 {
		return eightfoldPCSXPosition{}, errors.New("eightfold pcsx position id is required")
	}
	var payload eightfoldPCSXDetailResponse
	if err := e.getJSON(ctx, eightfoldPCSXDetailURL(config, id), &payload); err != nil {
		return eightfoldPCSXPosition{}, err
	}
	if err := payload.detailErr(); err != nil {
		return eightfoldPCSXPosition{}, err
	}
	return payload.Data, nil
}

func (e *ATSExtractor) extractEightfoldApply(ctx context.Context, source Source) (Result, error) {
	config, err := eightfoldApplyConfigFromSource(source)
	if err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, e.eightfoldApplyMaxJobs)
	seen := map[string]struct{}{}
	total := 0
	lastEndpoint := ""
	start := 0
	for page := 0; page < e.eightfoldApplyMaxPages && len(jobs) < e.eightfoldApplyMaxJobs; page++ {
		endpoint := eightfoldApplySearchURL(config, start, e.eightfoldApplyPageSize)
		lastEndpoint = endpoint
		var payload eightfoldApplySearchResponse
		if err := e.getJSON(ctx, endpoint, &payload); err != nil {
			return Result{}, err
		}
		if total == 0 {
			total = payload.Count
		}
		if len(payload.Positions) == 0 {
			break
		}
		for _, position := range payload.Positions {
			if len(jobs) >= e.eightfoldApplyMaxJobs {
				break
			}
			job, ok := eightfoldApplyPosting(source, config, position)
			if !ok {
				continue
			}
			if _, exists := seen[job.SourceJobID]; exists {
				continue
			}
			seen[job.SourceJobID] = struct{}{}
			jobs = append(jobs, job)
		}
		start += len(payload.Positions)
		if total > 0 && start >= total {
			break
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	evidenceText := "Eightfold apply jobs API"
	if total > 0 {
		evidenceText = fmt.Sprintf("%s (%d total records)", evidenceText, total)
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.83,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: evidenceText, URL: lastEndpoint},
		},
	})
}

func (e *ATSExtractor) extractGoogleCareers(ctx context.Context, source Source) (Result, error) {
	document, err := e.getText(ctx, source.URL, "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	cards := googleCareersJobCards(document)
	if len(cards) > e.googleCareersMaxJobs {
		cards = cards[:e.googleCareersMaxJobs]
	}
	jobs := make([]JobPosting, 0, len(cards))
	seen := map[string]struct{}{}
	detailAttempts := 0
	for _, card := range cards {
		var detail googleCareersDetail
		if detailAttempts < e.googleCareersDetailMaxJobs {
			detailAttempts++
			if enriched, err := e.googleCareersDetail(ctx, source, card); err == nil {
				detail = enriched
			}
		}
		job, ok := googleCareersPosting(source, card, detail)
		if !ok {
			continue
		}
		if _, exists := seen[job.SourceJobID]; exists {
			continue
		}
		seen[job.SourceJobID] = struct{}{}
		jobs = append(jobs, job)
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.82,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Google Careers rendered search result cards", URL: source.URL},
		},
	})
}

func (e *ATSExtractor) extractOpenAICareers(ctx context.Context, source Source) (Result, error) {
	document, err := e.getText(ctx, source.URL, "text/html,application/xhtml+xml")
	if err != nil {
		return Result{}, err
	}
	jobs := openAICareersPostings(source, document, e.openAICareersMaxJobs)
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.8,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "OpenAI Careers rendered search result links with Ashby apply URLs", URL: source.URL},
		},
	})
}

func (e *ATSExtractor) extractGitHubJobList(ctx context.Context, source Source) (Result, error) {
	override := strings.TrimSpace(source.Metadata["github_raw_url"])
	candidates := []string{override}
	var err error
	if override == "" {
		candidates, err = githubJobListRawCandidates(source.URL)
		if err != nil {
			return Result{}, err
		}
	}
	document := ""
	rawURL := ""
	var lastErr error
	for _, candidate := range candidates {
		document, lastErr = e.getText(ctx, candidate, "text/plain,text/markdown,*/*;q=0.8")
		if lastErr == nil {
			rawURL = candidate
			break
		}
		if ctx.Err() != nil {
			return Result{}, lastErr
		}
	}
	if rawURL == "" {
		return Result{}, lastErr
	}
	jobs := githubJobListPostings(source, rawURL, document, e.githubJobListMaxJobs)
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.78,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "GitHub community early-career job list", URL: rawURL},
		},
	})
}

func (e *ATSExtractor) extractCitadelSecuritiesCareers(ctx context.Context, source Source) (Result, error) {
	sitemapURL, err := citadelCareerSitemapURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	document, err := e.getText(ctx, sitemapURL, "application/xml,text/xml,text/plain;q=0.9,*/*;q=0.8")
	if err != nil {
		return Result{}, err
	}
	entries, nested, err := parseStaticSitemap([]byte(document))
	if err != nil {
		return Result{}, err
	}
	for _, item := range nested {
		if strings.Contains(strings.ToLower(item.Loc), "career-sitemap.xml") {
			nestedURL := strings.TrimSpace(item.Loc)
			if nestedURL == "" {
				continue
			}
			document, err = e.getText(ctx, nestedURL, "application/xml,text/xml,text/plain;q=0.9,*/*;q=0.8")
			if err != nil {
				return Result{}, err
			}
			entries, _, err = parseStaticSitemap([]byte(document))
			if err != nil {
				return Result{}, err
			}
			sitemapURL = nestedURL
			break
		}
	}

	maxJobs := boundedInt(intFromMetadata(source.Metadata, "citadel_max_jobs"), e.citadelSecuritiesMaxJobs, 1, 200)
	jobs := make([]JobPosting, 0, len(entries))
	seen := make(map[string]struct{})
	for _, entry := range entries {
		job, ok := citadelSitemapPosting(source, entry)
		if !ok {
			continue
		}
		key := strings.ToLower(job.ApplyURL)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		jobs = append(jobs, job)
		if len(jobs) >= maxJobs {
			break
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source: source,
		Jobs:   jobs,
		RawEvidence: []Evidence{{
			Field: "ats_endpoint",
			Text:  "Citadel official career sitemap",
			URL:   sitemapURL,
		}},
		Confidence: 0.68,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
	})
}

func citadelCareerSitemapURL(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("citadel source url must be absolute")
	}
	if strings.HasSuffix(strings.ToLower(parsed.Path), "career-sitemap.xml") {
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String(), nil
	}
	parsed.Path = "/career-sitemap.xml"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func citadelSitemapPosting(source Source, entry staticSitemapURL) (JobPosting, bool) {
	detailURL := strings.TrimSpace(entry.Loc)
	if detailURL == "" {
		return JobPosting{}, false
	}
	parsed, err := url.Parse(detailURL)
	if err != nil {
		return JobPosting{}, false
	}
	slug := strings.Trim(strings.TrimSpace(path.Base(parsed.Path)), "/")
	title, ok := citadelTitleFromSlug(slug)
	if !ok {
		return JobPosting{}, false
	}
	location := citadelLocationFromSlug(slug)
	return JobPosting{
		SourceJobID:    stableStringID("citadel:" + slug),
		Company:        firstNonEmptyString(source.Name, "Citadel Securities"),
		Title:          title,
		Location:       location,
		Country:        citadelCountryFromLocation(location),
		EmploymentType: citadelEmploymentTypeFromTitle(title),
		Level:          citadelLevelFromTitle(title),
		RoleFamily:     inferRoleFamily(title),
		SourceURL:      detailURL,
		ApplyURL:       detailURL,
		PostedAt:       parseTimePtr(entry.LastMod),
		Live:           true,
		Confidence:     0.68,
		Strategy:       TierATS,
		Evidence: []Evidence{{
			Field: "ats",
			Text:  "Citadel official career sitemap entry",
			URL:   detailURL,
		}},
	}, true
}

func citadelTitleFromSlug(slug string) (string, bool) {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(slug)), "/")
	if normalized == "" || strings.Contains(normalized, "campus-referrals") || strings.Contains(normalized, "campus-27") {
		return "", false
	}
	for _, blocked := range []string{
		"senior-",
		"contractor",
		"fpga",
		"trader",
		"trading",
		"operations",
		"client-execution",
		"phd",
	} {
		if strings.Contains(normalized, blocked) {
			return "", false
		}
	}
	titleBySlug := map[string]string{
		"c-market-data-engineer":                   "C++ Market Data Engineer",
		"cloud-platform-engineer":                  "Cloud Platform Engineer",
		"c-software-engineer":                      "C++ Software Engineer",
		"c-software-engineer-2":                    "C++ Software Engineer",
		"machine-learning-engineer":                "Machine Learning Engineer",
		"kdb-q-engineer":                           "Kdb+/Q Engineer",
		"quantitative-developer-research-engineer": "Quantitative Developer / Research Engineer",
		"platform-engineer":                        "Platform Engineer",
		"quantitative-research-engineer":           "Quantitative Research Engineer",
		"rates-credit-c-engineer":                  "Rates/Credit C++ Engineer",
		"research-engineer":                        "Research Engineer",
		"site-reliability-engineer":                "Site Reliability Engineer",
		"site-reliability-engineer-2":              "Site Reliability Engineer",
		"software-engineer-intern-australia":       "Software Engineer - Intern (Australia)",
		"software-engineer-intern-us":              "Software Engineer - Intern (US)",
		"software-engineer-university-graduate-us": "Software Engineer - University Graduate (US)",
		"ui-engineer-global-trading-applications":  "UI Engineer - Global Trading Applications",
		"regulatory-reporting-engineer":            "Regulatory Reporting Engineer",
	}
	if title, ok := titleBySlug[normalized]; ok {
		return title, true
	}
	if !citadelAllowedEngineeringSlug(normalized) {
		return "", false
	}
	return titleFromSlug(normalized), true
}

func citadelAllowedEngineeringSlug(slug string) bool {
	for _, phrase := range []string{
		"software-engineer",
		"platform-engineer",
		"market-data-engineer",
		"machine-learning-engineer",
		"kdb-q-engineer",
		"quantitative-developer",
		"research-engineer",
		"site-reliability-engineer",
		"ui-engineer",
		"reporting-engineer",
		"c-engineer",
	} {
		if strings.Contains(slug, phrase) {
			return true
		}
	}
	return false
}

func citadelLocationFromSlug(slug string) string {
	slug = strings.ToLower(slug)
	switch {
	case strings.Contains(slug, "australia"):
		return "Australia"
	case strings.Contains(slug, "asia"):
		return "Asia"
	case strings.Contains(slug, "europe"):
		return "Europe"
	case strings.Contains(slug, "new-york"):
		return "New York, NY"
	case strings.Contains(slug, "miami"):
		return "Miami, FL"
	case strings.Contains(slug, "us"):
		return "United States"
	default:
		return ""
	}
}

func citadelCountryFromLocation(location string) string {
	switch strings.ToLower(strings.TrimSpace(location)) {
	case "united states", "new york, ny", "miami, fl":
		return "US"
	case "australia":
		return "Australia"
	default:
		return ""
	}
}

func citadelEmploymentTypeFromTitle(title string) string {
	if strings.Contains(strings.ToLower(title), "intern") {
		return "internship"
	}
	return "full_time"
}

func citadelLevelFromTitle(title string) string {
	lower := strings.ToLower(title)
	switch {
	case strings.Contains(lower, "intern"):
		return "internship"
	case strings.Contains(lower, "graduate"):
		return "new_grad"
	default:
		return ""
	}
}

func (e *ATSExtractor) extractJobsyn(ctx context.Context, source Source) (Result, error) {
	baseURL := strings.TrimRight(firstNonEmptyString(source.Metadata["jobsyn_base_url"], source.Metadata["base_url"], e.jobsynBaseURL), "/")
	if baseURL == "" {
		return Result{}, errors.New("jobsyn base URL is empty")
	}
	query := firstNonEmptyString(source.Metadata["jobsyn_query"], source.Metadata["query"], sourceURLQueryValue(source.URL, "q"), sourceURLQueryValue(source.URL, "keyword"), "software engineer intern")
	location := firstNonEmptyString(source.Metadata["jobsyn_location"], source.Metadata["location"], sourceURLQueryValue(source.URL, "location"))
	origin := firstNonEmptyString(source.Metadata["jobsyn_origin"], source.Metadata["x_origin"], sourceHost(source.URL), "metacareers.dejobs.org")
	pageSize := boundedInt(intFromMetadata(source.Metadata, "jobsyn_page_size"), e.jobsynPageSize, 1, 100)
	maxPages := boundedInt(intFromMetadata(source.Metadata, "jobsyn_max_pages"), e.jobsynMaxPages, 1, 20)
	maxJobs := boundedInt(intFromMetadata(source.Metadata, "jobsyn_max_jobs"), e.jobsynMaxJobs, 1, 300)

	jobs := make([]JobPosting, 0, maxJobs)
	seen := map[string]struct{}{}
	firstEndpoint := ""
	for page := 1; page <= maxPages && len(jobs) < maxJobs; page++ {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		parsed, err := parseSourceURL(baseURL + "/v1/solr/search")
		if err != nil {
			return Result{}, err
		}
		q := parsed.Query()
		q.Set("q", query)
		q.Set("page", strconv.Itoa(page))
		q.Set("num_items", strconv.Itoa(pageSize))
		if location != "" {
			q.Set("location", location)
		}
		parsed.RawQuery = q.Encode()
		if firstEndpoint == "" {
			firstEndpoint = parsed.String()
		}

		var payload jobsynSearchResponse
		if err := e.getJSONWithHeaders(ctx, parsed.String(), map[string]string{"X-Origin": origin}, &payload); err != nil {
			return Result{}, err
		}
		if len(payload.Jobs) == 0 {
			break
		}
		for _, item := range payload.Jobs {
			if len(jobs) >= maxJobs {
				break
			}
			job, ok := jobsynPosting(source, origin, parsed.String(), item)
			if !ok {
				continue
			}
			if _, ok := seen[job.SourceJobID]; ok {
				continue
			}
			seen[job.SourceJobID] = struct{}{}
			jobs = append(jobs, job)
		}
		if !payload.Pagination.HasMorePages || payload.Pagination.TotalPages > 0 && float64(page) >= payload.Pagination.TotalPages {
			break
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.82,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "DirectEmployers/jobsyn Solr search API", URL: firstEndpoint},
		},
	})
}

func (e *ATSExtractor) extractIBMCareers(ctx context.Context, source Source) (Result, error) {
	query := firstNonEmptyString(source.Metadata["ibm_query"], source.Metadata["query"], sourceURLQueryValue(source.URL, "q"), "software engineer OR software developer OR data engineer")
	maxJobs := boundedInt(intFromMetadata(source.Metadata, "ibm_max_jobs"), e.ibmSearchMaxJobs, 1, 200)
	req := ibmSearchRequest{
		AppID:  "careers",
		Scopes: []string{"careers2"},
		Query: map[string]any{
			"bool": map[string]any{
				"must": []any{
					map[string]any{"simple_query_string": map[string]any{
						"query": query,
						"fields": []string{
							"keywords^1", "body^1", "url^2", "description^2", "h1s_content^2", "title^3",
						},
					}},
				},
			},
		},
		PostFilter: map[string]any{
			"terms": map[string]any{"field_keyword_18": []string{"Internship", "Entry Level"}},
		},
		Size: boundedInt(maxJobs, 50, 1, 100),
		Sort: []map[string]string{{"_score": "desc"}, {"pageviews": "desc"}},
		Lang: "zz",
		Source: []string{
			"_id", "title", "url", "description", "language", "entitled",
			"field_keyword_17", "field_keyword_08", "field_keyword_18", "field_keyword_19",
		},
	}
	var payload ibmSearchResponse
	if err := e.postJSON(ctx, firstNonEmptyString(source.Metadata["ibm_search_api_url"], e.ibmSearchAPIBaseURL), req, &payload); err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, min(len(payload.Hits.Hits), maxJobs))
	seen := map[string]struct{}{}
	for _, hit := range payload.Hits.Hits {
		if len(jobs) >= maxJobs {
			break
		}
		job, ok := ibmSearchPosting(source, hit)
		if !ok {
			continue
		}
		if _, ok := seen[job.SourceJobID]; ok {
			continue
		}
		seen[job.SourceJobID] = struct{}{}
		jobs = append(jobs, job)
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.82,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "IBM careers embedded search API", URL: firstNonEmptyString(source.Metadata["ibm_search_api_url"], e.ibmSearchAPIBaseURL)},
		},
	})
}

type taleoSearchRequest struct {
	MultipleContestID bool   `json:"multipleContestId"`
	SortingSelection  string `json:"sortingSelection"`
	StartRow          int    `json:"startRow"`
	MaximumRows       int    `json:"maximumRows"`
}

type taleoSearchResponse struct {
	RequisitionList []taleoRequisition `json:"requisitionList"`
}

type taleoRequisition struct {
	ContestNo           string `json:"contestNo"`
	Title               string `json:"title"`
	CityTown            string `json:"cityTown"`
	State               string `json:"state"`
	Country             string `json:"country"`
	JobLevelLabel       string `json:"jobLevelLabel"`
	ReferencePartnerURL string `json:"referencePartnerURL"`
}

type jobsynSearchResponse struct {
	Jobs       []jobsynJob       `json:"jobs"`
	Pagination jobsynPagination  `json:"pagination"`
	Meta       map[string]any    `json:"meta"`
	Filters    map[string][]any  `json:"filters"`
	Featured   []json.RawMessage `json:"featured_jobs"`
}

type jobsynPagination struct {
	HasMorePages bool    `json:"has_more_pages"`
	Page         float64 `json:"page"`
	PageSize     float64 `json:"page_size"`
	Total        float64 `json:"total"`
	TotalPages   float64 `json:"total_pages"`
}

type jobsynJob struct {
	GUID              string   `json:"guid"`
	ReqID             string   `json:"reqid"`
	ID                string   `json:"id"`
	CompanyExact      string   `json:"company_exact"`
	TitleExact        string   `json:"title_exact"`
	TitleSlug         string   `json:"title_slug"`
	LocationExact     string   `json:"location_exact"`
	CityExact         string   `json:"city_exact"`
	CountryExact      string   `json:"country_exact"`
	CountryShortExact string   `json:"country_short_exact"`
	Description       string   `json:"description"`
	DateNew           string   `json:"date_new"`
	DateAdded         string   `json:"date_added"`
	DateUpdated       string   `json:"date_updated"`
	AllLocations      []string `json:"all_locations"`
	IsPosted          *bool    `json:"is_posted"`
}

type ibmSearchRequest struct {
	AppID      string              `json:"appId"`
	Scopes     []string            `json:"scopes"`
	Query      map[string]any      `json:"query"`
	PostFilter map[string]any      `json:"post_filter"`
	Size       int                 `json:"size"`
	Sort       []map[string]string `json:"sort"`
	Lang       string              `json:"lang"`
	Source     []string            `json:"_source"`
}

type ibmSearchResponse struct {
	Hits ibmSearchHits `json:"hits"`
}

type ibmSearchHits struct {
	Total ibmSearchTotal `json:"total"`
	Hits  []ibmSearchHit `json:"hits"`
}

type ibmSearchTotal struct {
	Value    int    `json:"value"`
	Relation string `json:"relation"`
}

type ibmSearchHit struct {
	ID     string          `json:"_id"`
	Score  float64         `json:"_score"`
	Source ibmSearchSource `json:"_source"`
}

type ibmSearchSource struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	WorkMode    string `json:"field_keyword_17"`
	JobFamily   string `json:"field_keyword_08"`
	CareerStage string `json:"field_keyword_18"`
	Location    string `json:"field_keyword_19"`
}

func jobsynPosting(source Source, origin string, endpoint string, item jobsynJob) (JobPosting, bool) {
	title := firstNonEmptyString(item.TitleExact)
	id := firstNonEmptyString(item.ReqID, item.GUID, item.ID)
	company := sourceCompany(source, firstNonEmptyString(item.CompanyExact, source.Name))
	if title == "" || id == "" || company == "" {
		return JobPosting{}, false
	}
	location := firstNonEmptyString(item.LocationExact, strings.Join(compactStringList(item.AllLocations...), ", "))
	country := canonicalCountry(firstNonEmptyString(item.CountryExact, item.CountryShortExact, normalizeCountry("", location)))
	description := cleanHTMLText(item.Description)
	applyURL := jobsynJobURL(origin, item)
	if applyURL == "" {
		applyURL = source.URL
	}
	return JobPosting{
		SourceJobID:    "jobsyn:" + id,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, ""),
		Level:          inferLevel(title),
		RoleFamily:     inferRoleFamily(title + " " + description),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(firstNonEmptyString(item.DateNew, item.DateUpdated, item.DateAdded)),
		Live:           item.IsPosted == nil || *item.IsPosted,
		Confidence:     0.82,
		Strategy:       TierATS,
		Evidence: []Evidence{
			{Field: "ats", Text: "DirectEmployers/jobsyn Solr search API", URL: endpoint},
			{Field: "description", Text: description, URL: applyURL},
			{Field: "location", Text: location, URL: applyURL},
			{Field: "reqid", Text: item.ReqID, URL: applyURL},
		},
	}, true
}

func jobsynJobURL(origin string, item jobsynJob) string {
	host := strings.TrimSpace(origin)
	if host == "" || strings.Contains(host, "/") {
		return ""
	}
	guid := strings.TrimSpace(item.GUID)
	titleSlug := strings.TrimSpace(item.TitleSlug)
	locationSlug := simpleURLSlug(item.LocationExact)
	if guid == "" || titleSlug == "" || locationSlug == "" {
		return ""
	}
	return "https://" + host + "/" + locationSlug + "/" + titleSlug + "/" + guid + "/job/"
}

func simpleURLSlug(value string) string {
	value = strings.ToLower(normalizeSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func ibmSearchPosting(source Source, hit ibmSearchHit) (JobPosting, bool) {
	item := hit.Source
	title := normalizeSpace(item.Title)
	applyURL := strings.TrimSpace(item.URL)
	id := firstNonEmptyString(ibmJobID(applyURL), hit.ID, stableJobToken(applyURL, title))
	if title == "" || applyURL == "" || id == "" {
		return JobPosting{}, false
	}
	if !ibmEngineeringTitleAllowed(title, item.JobFamily) {
		return JobPosting{}, false
	}
	location := normalizeSpace(item.Location)
	country := ibmCountry(location)
	description := cleanHTMLText(item.Description)
	return JobPosting{
		SourceJobID:    "ibm:" + id,
		Company:        sourceCompany(source, "IBM"),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: ibmEmploymentType(item.CareerStage, title),
		Level:          ibmLevel(item.CareerStage, title),
		RoleFamily:     inferRoleFamily(strings.Join(compactStringList(title, item.JobFamily, description), " ")),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		Live:           true,
		Confidence:     0.82,
		Strategy:       TierATS,
		Evidence: []Evidence{
			{Field: "ats", Text: "IBM careers embedded search API", URL: source.URL},
			{Field: "description", Text: description, URL: applyURL},
			{Field: "job_family", Text: item.JobFamily, URL: applyURL},
			{Field: "career_stage", Text: item.CareerStage, URL: applyURL},
			{Field: "work_mode", Text: item.WorkMode, URL: applyURL},
		},
	}, true
}

func ibmEngineeringTitleAllowed(title string, jobFamily string) bool {
	normalizedTitle := ibmNormalizedPhrase(title)
	normalizedFamily := ibmNormalizedPhrase(jobFamily)
	if normalizedTitle == "" {
		return false
	}
	for _, phrase := range []string{
		"sales specialist",
		"technical sales",
		"leadership development",
		"design",
		"consultant",
		"consulting",
		"hardware",
	} {
		if strings.Contains(normalizedTitle, ibmNormalizedPhrase(phrase)) {
			return false
		}
	}
	if strings.Contains(normalizedFamily, "software engineering") || strings.Contains(normalizedFamily, "data analytics") {
		for _, phrase := range []string{
			"engineer",
			"developer",
			"data scientist",
			"data engineer",
			"application developer",
			"intern",
			"trainee",
			"associate engineer",
		} {
			if strings.Contains(normalizedTitle, ibmNormalizedPhrase(phrase)) {
				return true
			}
		}
	}
	return false
}

func ibmNormalizedPhrase(value string) string {
	value = strings.ToLower(normalizeSpace(value))
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	return strings.Join(fields, " ")
}

func ibmJobID(applyURL string) string {
	parsed, err := parseSourceURL(applyURL)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Query().Get("jobId"))
}

func ibmCountry(location string) string {
	location = normalizeSpace(location)
	if location == "" || strings.EqualFold(location, "Multiple Cities") {
		return "unknown"
	}
	parts := strings.Split(location, ",")
	last := strings.TrimSpace(parts[len(parts)-1])
	return canonicalCountry(last)
}

func ibmEmploymentType(stage string, title string) string {
	if strings.EqualFold(strings.TrimSpace(stage), "Internship") {
		return "internship"
	}
	return employmentFromText(title, stage)
}

func ibmLevel(stage string, title string) string {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "internship":
		return "internship"
	case "entry level":
		return "early_career"
	default:
		return inferLevel(title)
	}
}

func (e *ATSExtractor) extractWhatnotCareers(ctx context.Context, source Source) (Result, error) {
	endpoint, err := whatnotJobsEndpoint(source.URL)
	if err != nil {
		return Result{}, err
	}
	maxJobs := boundedInt(intFromMetadata(source.Metadata, "whatnot_max_jobs"), e.whatnotMaxJobs, 1, 500)
	var payload whatnotJobsResponse
	if err := e.getJSON(ctx, endpoint, &payload); err != nil {
		return Result{}, err
	}

	jobs := make([]JobPosting, 0, min(len(payload.Results), maxJobs))
	seen := map[string]struct{}{}
	for _, item := range payload.Results {
		if len(jobs) >= maxJobs {
			break
		}
		job, ok := whatnotPosting(source, endpoint, item)
		if !ok {
			continue
		}
		if _, ok := seen[job.SourceJobID]; ok {
			continue
		}
		seen[job.SourceJobID] = struct{}{}
		jobs = append(jobs, job)
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.92,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Whatnot public jobs API", URL: endpoint},
		},
	})
}

func (e *ATSExtractor) extractWalmartCareers(ctx context.Context, source Source) (Result, error) {
	query := firstNonEmptyString(
		source.Metadata["walmart_query"],
		source.Metadata["search_query"],
		source.Metadata["query"],
		sourceURLQueryValue(source.URL, "searchQuery"),
		sourceURLQueryValue(source.URL, "q"),
		sourceURLQueryValue(source.URL, "query"),
		"software engineer",
	)
	locale := firstNonEmptyString(source.Metadata["walmart_locale"], source.Metadata["locale"], "en_US")
	filter := firstNonEmptyString(source.Metadata["walmart_filter"], source.Metadata["filter"])
	pageSize := boundedInt(intFromMetadata(source.Metadata, "walmart_page_size"), e.walmartPageSize, 1, 100)
	maxPages := boundedInt(intFromMetadata(source.Metadata, "walmart_max_pages"), e.walmartMaxPages, 1, 20)
	maxJobs := boundedInt(intFromMetadata(source.Metadata, "walmart_max_jobs"), e.walmartMaxJobs, 1, 500)

	jobs := make([]JobPosting, 0, maxJobs)
	seen := map[string]struct{}{}
	firstEndpoint := ""
	pagesFetched, totalAvailable := 0, 0
	hasMore := false
	for pageNumber := 0; pageNumber < maxPages && len(jobs) < maxJobs; pageNumber++ {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		endpoint, err := walmartSearchEndpoint(source.URL, pageNumber, pageSize, locale)
		if err != nil {
			return Result{}, err
		}
		if firstEndpoint == "" {
			firstEndpoint = endpoint
		}
		request := walmartSearchRequest{
			Query:       query,
			BasicSearch: false,
			Filter:      filter,
			Locale:      locale,
		}
		var payload walmartSearchResponse
		if err := e.postJSON(ctx, endpoint, request, &payload); err != nil {
			return Result{}, err
		}
		pagesFetched++
		totalAvailable = payload.TotalJobs
		if len(payload.Jobs) == 0 {
			hasMore = false
			break
		}
		for _, item := range payload.Jobs {
			if len(jobs) >= maxJobs {
				break
			}
			job, ok := walmartPosting(source, endpoint, item)
			if !ok {
				continue
			}
			if _, ok := seen[job.SourceJobID]; ok {
				continue
			}
			seen[job.SourceJobID] = struct{}{}
			jobs = append(jobs, job)
		}
		hasMore = len(payload.Jobs) >= pageSize && !(payload.TotalJobs > 0 && (pageNumber+1)*pageSize >= payload.TotalJobs)
		if !hasMore {
			break
		}
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.90,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Walmart careers hybrid search API", URL: firstEndpoint},
		},
		Diagnostics: scraperPaginationDiagnostics(pagesFetched, pageSize, totalAvailable, maxJobs, hasMore || totalAvailable > len(jobs)),
	})
}

func (e *ATSExtractor) extractWorldQuantCareers(ctx context.Context, source Source) (Result, error) {
	document, err := e.getText(ctx, source.URL, "text/html")
	if err != nil {
		return Result{}, err
	}
	listMatch := worldQuantCareersListPattern.FindStringSubmatch(document)
	if len(listMatch) < 2 {
		return Result{}, ErrNoJobs
	}
	maxJobs := boundedInt(intFromMetadata(source.Metadata, "worldquant_max_jobs"), e.worldQuantMaxJobs, 1, 500)
	baseURL, err := parseSourceURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	jobs := make([]JobPosting, 0, maxJobs)
	seen := map[string]struct{}{}
	for _, link := range worldQuantCareersLinkPattern.FindAllString(listMatch[1], -1) {
		if len(jobs) >= maxJobs {
			break
		}
		job, ok := worldQuantPosting(source, baseURL, link)
		if !ok {
			continue
		}
		if _, ok := seen[job.SourceJobID]; ok {
			continue
		}
		seen[job.SourceJobID] = struct{}{}
		jobs = append(jobs, job)
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "WorldQuant server-rendered career listing", URL: source.URL},
		},
	})
}

func worldQuantPosting(source Source, baseURL *url.URL, link string) (JobPosting, bool) {
	href := html.UnescapeString(htmlAttrValue(link, "href"))
	reference, err := url.Parse(strings.TrimSpace(href))
	if err != nil || href == "" {
		return JobPosting{}, false
	}
	applyURL := baseURL.ResolveReference(reference)
	id := strings.TrimSpace(applyURL.Query().Get("id"))
	titleMatch := worldQuantCareersTitlePattern.FindStringSubmatch(link)
	if id == "" || len(titleMatch) < 2 {
		return JobPosting{}, false
	}
	title := cleanHTMLText(titleMatch[1])
	if title == "" {
		return JobPosting{}, false
	}
	location := ""
	if locationMatch := worldQuantCareersLocationPattern.FindStringSubmatch(link); len(locationMatch) > 1 {
		location = cleanHTMLText(locationMatch[1])
	}
	country := worldQuantCountry(location)
	context := strings.Join(compactStringList(title, location), " ")
	return JobPosting{
		SourceJobID:    "worldquant:" + id,
		Company:        sourceCompany(source, "WorldQuant"),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, ""),
		Level:          inferLevel(context),
		RoleFamily:     inferRoleFamily(context),
		SourceURL:      source.URL,
		ApplyURL:       applyURL.String(),
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence: []Evidence{
			{Field: "ats", Text: "WorldQuant server-rendered career listing", URL: source.URL},
			{Field: "location", Text: location, URL: applyURL.String()},
		},
	}, true
}

func worldQuantCountry(location string) string {
	parts := strings.Split(location, ",")
	if len(parts) == 0 {
		return "unknown"
	}
	country := canonicalCountry(strings.TrimSpace(parts[len(parts)-1]))
	if country == "" {
		return "unknown"
	}
	return country
}

func whatnotJobsEndpoint(rawURL string) (string, error) {
	endpoint, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	endpoint.Path = "/api/jobs"
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint.String(), nil
}

func whatnotPosting(source Source, endpoint string, item whatnotJob) (JobPosting, bool) {
	if item.IsListed == nil || !*item.IsListed {
		return JobPosting{}, false
	}
	id := firstNonEmptyString(item.ID, item.JobID)
	title := normalizeSpace(item.Title)
	applyURL := firstNonEmptyString(item.ApplyLink, item.ExternalLink)
	if id == "" || title == "" || applyURL == "" {
		return JobPosting{}, false
	}
	location := whatnotLocation(item)
	context := strings.Join(compactStringList(title, item.DepartmentName, item.TeamName, item.EmploymentType, item.WorkplaceType), " ")
	evidence := []Evidence{
		{Field: "ats", Text: "Whatnot public jobs API", URL: endpoint},
		{Field: "department", Text: item.DepartmentName, URL: applyURL},
		{Field: "team", Text: item.TeamName, URL: applyURL},
		{Field: "workplace", Text: item.WorkplaceType, URL: applyURL},
	}
	if item.ShouldDisplayCompensationInfo && item.CompensationTierSummary != "" {
		evidence = append(evidence, Evidence{Field: "compensation", Text: item.CompensationTierSummary, URL: applyURL})
	}
	return JobPosting{
		SourceJobID:    "whatnot:" + id,
		Company:        sourceCompany(source, "Whatnot"),
		Title:          title,
		Location:       location,
		Country:        whatnotCountry(firstNonEmptyString(item.LocationName, item.LocationExternalName)),
		EmploymentType: employmentFromText(title, item.EmploymentType),
		Level:          inferLevel(context),
		RoleFamily:     inferRoleFamily(context),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(item.PublishedDate),
		Live:           true,
		Confidence:     0.92,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func whatnotLocation(item whatnotJob) string {
	locations := compactStringList(firstNonEmptyString(item.LocationName, item.LocationExternalName))
	seen := map[string]struct{}{}
	for _, location := range locations {
		seen[strings.ToLower(location)] = struct{}{}
	}
	for _, location := range compactStringList(item.SecondaryLocationNames...) {
		key := strings.ToLower(location)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		locations = append(locations, location)
	}
	return strings.Join(locations, "; ")
}

func whatnotCountry(location string) string {
	country := normalizeCountry("", location)
	if country != "unknown" {
		return country
	}
	parts := strings.Split(location, ",")
	if len(parts) > 1 && isUSStateCode(strings.TrimSpace(parts[len(parts)-1])) {
		return "US"
	}
	return "unknown"
}

func isUSStateCode(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "AL", "AK", "AZ", "AR", "CA", "CO", "CT", "DE", "FL", "GA", "HI", "ID", "IL", "IN", "IA", "KS", "KY", "LA", "ME", "MD", "MA", "MI", "MN", "MS", "MO", "MT", "NE", "NV", "NH", "NJ", "NM", "NY", "NC", "ND", "OH", "OK", "OR", "PA", "RI", "SC", "SD", "TN", "TX", "UT", "VT", "VA", "WA", "WV", "WI", "WY", "DC":
		return true
	default:
		return false
	}
}

func walmartSearchEndpoint(rawURL string, pageNumber int, pageSize int, locale string) (string, error) {
	endpoint, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	endpoint.Path = "/api/ai/search-ai/api/v1/combined/hybrid-search"
	query := url.Values{}
	query.Set("page", strconv.Itoa(max(0, pageNumber)))
	query.Set("size", strconv.Itoa(max(1, pageSize)))
	query.Set("locale", firstNonEmptyString(locale, "en_US"))
	endpoint.RawQuery = query.Encode()
	endpoint.Fragment = ""
	return endpoint.String(), nil
}

func walmartPosting(source Source, endpoint string, item walmartSearchJob) (JobPosting, bool) {
	status := strings.TrimSpace(item.Metadata.RequisitionStatus)
	if !strings.EqualFold(status, "open") {
		return JobPosting{}, false
	}
	id := firstNonEmptyString(item.Metadata.JobID, strings.TrimSuffix(item.ID, "-External"))
	title := firstNonEmptyString(item.Metadata.JobPostingTitle, item.Metadata.Title)
	applyURL := walmartJobURL(source, id)
	if id == "" || title == "" || applyURL == "" {
		return JobPosting{}, false
	}
	country := canonicalCountry(item.Metadata.PrimaryLocationCountry)
	location := strings.Join(compactStringList(item.Metadata.PrimaryLocationCity, item.Metadata.PrimaryLocationState, country), ", ")
	employment := strings.Join(compactStringList(item.Metadata.EmploymentTypes...), ", ")
	categories := strings.Join(compactStringList(item.Metadata.Categories...), ", ")
	areas := strings.Join(compactStringList(item.Metadata.Areas...), ", ")
	description := cleanHTMLText(item.Text)
	context := strings.Join(compactStringList(title, categories, areas, employment, description), " ")
	return JobPosting{
		SourceJobID:    "walmart:" + id,
		Company:        sourceCompany(source, firstNonEmptyString(item.Metadata.Brand, "Walmart")),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, employment),
		Level:          inferLevel(context),
		RoleFamily:     inferRoleFamily(context),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       millisTimePtr(item.Metadata.JobPostingStartDate),
		Live:           true,
		Confidence:     0.90,
		Strategy:       TierATS,
		Evidence: []Evidence{
			{Field: "ats", Text: "Walmart careers hybrid search API", URL: endpoint},
			{Field: "description", Text: description, URL: applyURL},
			{Field: "category", Text: categories, URL: applyURL},
			{Field: "area", Text: areas, URL: applyURL},
			{Field: "requisition_status", Text: status, URL: applyURL},
		},
	}, true
}

func walmartJobURL(source Source, jobID string) string {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return ""
	}
	endpoint, err := parseSourceURL(source.URL)
	if err != nil {
		return ""
	}
	country := firstNonEmptyString(source.Metadata["walmart_country"], "us")
	language := firstNonEmptyString(source.Metadata["walmart_language"], "en")
	parts := nonEmptyPathParts(endpoint)
	if len(parts) >= 2 && len(parts[0]) == 2 && len(parts[1]) == 2 {
		country = parts[0]
		language = parts[1]
	}
	endpoint.Path = path.Join("/", country, language, "jobs", jobID)
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint.String()
}

type whatnotJobsResponse struct {
	Results []whatnotJob `json:"results"`
}

type whatnotJob struct {
	ID                            string   `json:"id"`
	JobID                         string   `json:"jobId"`
	Title                         string   `json:"title"`
	DepartmentName                string   `json:"departmentName"`
	TeamName                      string   `json:"teamName"`
	LocationName                  string   `json:"locationName"`
	LocationExternalName          string   `json:"locationExternalName"`
	SecondaryLocationNames        []string `json:"secondaryLocationNames"`
	WorkplaceType                 string   `json:"workplaceType"`
	EmploymentType                string   `json:"employmentType"`
	IsListed                      *bool    `json:"isListed"`
	PublishedDate                 string   `json:"publishedDate"`
	ExternalLink                  string   `json:"externalLink"`
	ApplyLink                     string   `json:"applyLink"`
	CompensationTierSummary       string   `json:"compensationTierSummary"`
	ShouldDisplayCompensationInfo bool     `json:"shouldDisplayCompensationOnJobBoard"`
}

type walmartSearchRequest struct {
	Query       string `json:"query"`
	BasicSearch bool   `json:"basicSearch"`
	Filter      string `json:"filter"`
	Locale      string `json:"locale"`
}

type walmartSearchResponse struct {
	Jobs      []walmartSearchJob `json:"jobs"`
	TotalJobs int                `json:"totalJobs"`
}

type walmartSearchJob struct {
	ID       string                `json:"id"`
	Text     string                `json:"text"`
	Metadata walmartSearchMetadata `json:"metadata"`
}

type walmartSearchMetadata struct {
	Title                  string   `json:"title"`
	JobPostingTitle        string   `json:"jobPostingTitle"`
	JobID                  string   `json:"jobId"`
	JobPostingStartDate    int64    `json:"jobPostingStartDate"`
	PrimaryLocationCity    string   `json:"primaryLocationCity"`
	PrimaryLocationState   string   `json:"primaryLocationState"`
	PrimaryLocationCountry string   `json:"primaryLocationCountry"`
	RequisitionStatus      string   `json:"requisitionStatus"`
	EmploymentTypes        []string `json:"employmentTypes"`
	Brand                  string   `json:"brand"`
	Categories             []string `json:"categories"`
	Areas                  []string `json:"areas"`
}

func (e *ATSExtractor) extractTaleo(ctx context.Context, source Source) (Result, error) {
	tenant, err := taleoTenantFromURL(source.URL)
	if err != nil {
		return Result{}, err
	}
	baseURL := e.taleoBaseURL
	if baseURL == "" {
		baseURL = "https://" + tenant + ".taleo.net"
	}
	endpoint := baseURL + "/careersection/rest/jobboard/searchjobs"

	req := taleoSearchRequest{
		MultipleContestID: false,
		SortingSelection:  "BRIEF_TITLE",
		StartRow:          1,
		MaximumRows:       e.taleoMaxJobs,
	}
	var payload taleoSearchResponse
	if err := e.postJSON(ctx, endpoint, req, &payload); err != nil {
		return Result{}, err
	}
	if len(payload.RequisitionList) == 0 {
		return Result{}, ErrNoJobs
	}

	company := sourceCompany(source, tenant)
	jobs := make([]JobPosting, 0, len(payload.RequisitionList))
	for _, item := range payload.RequisitionList {
		if ctx.Err() != nil {
			break
		}
		contestNo := strings.TrimSpace(item.ContestNo)
		title := strings.TrimSpace(item.Title)
		if title == "" {
			continue
		}
		applyURL := strings.TrimSpace(item.ReferencePartnerURL)
		if applyURL == "" && contestNo != "" {
			applyURL = baseURL + "/careersection/2/jobdetail.ftl?job=" + contestNo
		}
		locationParts := []string{}
		if city := strings.TrimSpace(item.CityTown); city != "" {
			locationParts = append(locationParts, city)
		}
		if state := strings.TrimSpace(item.State); state != "" {
			locationParts = append(locationParts, state)
		}
		if country := strings.TrimSpace(item.Country); country != "" {
			locationParts = append(locationParts, country)
		}
		location := strings.Join(locationParts, ", ")
		jobID := firstNonEmptyString(contestNo, stableJobToken(applyURL, title))
		job := JobPosting{
			SourceJobID:    "taleo:" + tenant + ":" + jobID,
			Company:        company,
			Title:          title,
			Location:       location,
			EmploymentType: employmentFromText(title, item.JobLevelLabel),
			RoleFamily:     inferRoleFamily(title),
			SourceURL:      source.URL,
			ApplyURL:       firstNonEmptyString(applyURL, source.URL),
			Live:           true,
			Confidence:     0.85,
			Strategy:       TierATS,
			Evidence: []Evidence{
				{Field: "ats", Text: "Oracle Taleo REST job search API", URL: endpoint},
			},
		}
		jobs = append(jobs, job)
	}
	if len(jobs) == 0 {
		return Result{}, ErrNoJobs
	}
	return NormalizeResult(Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.85,
		Strategy:   TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []Evidence{
			{Field: "ats_endpoint", Text: "Oracle Taleo REST job search API", URL: endpoint},
		},
	})
}

func taleoTenantFromURL(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(parsed.Hostname())
	if idx := strings.Index(host, ".taleo.net"); idx > 0 {
		return host[:idx], nil
	}
	// Fall back to metadata or first path segment for custom domains
	return firstPathSegment(parsed), nilIfEmpty(firstPathSegment(parsed), "taleo tenant")
}

func (e *ATSExtractor) googleCareersDetail(ctx context.Context, source Source, card string) (googleCareersDetail, error) {
	detailURL := googleCareersDetailURL(source, card)
	if detailURL == "" || detailURL == source.URL {
		return googleCareersDetail{}, errors.New("google careers detail URL unavailable")
	}
	document, err := e.getText(ctx, detailURL, "text/html,application/xhtml+xml")
	if err != nil {
		return googleCareersDetail{}, err
	}
	detail := googleCareersDetailFromHTML(document)
	if detail.empty() {
		return googleCareersDetail{}, ErrNoJobs
	}
	return detail, nil
}

func (e *ATSExtractor) paylocityDetailPosting(ctx context.Context, source Source, config paylocityConfig) (JobPosting, bool) {
	page, err := e.getText(ctx, source.URL, "text/html")
	if err != nil {
		return JobPosting{}, false
	}
	detail := paylocityDetailFromHTML(source, source.URL, page)
	item := paylocityJob{
		JobID:    paylocityParseJobID(config.JobID),
		JobTitle: detail.Job.Title,
	}
	return paylocityPosting(source, config, firstNonEmptyString(detail.Job.Company, config.CompanySlug), item, detail)
}

func (e *ATSExtractor) workdayJobPosting(ctx context.Context, source Source, config workdayConfig, searchEndpoint string, posting workdayPostingSummary) (JobPosting, error) {
	detail, detailURL, err := e.workdayPostingDetail(ctx, config, posting.ExternalPath)
	if err != nil && ctx.Err() != nil {
		return JobPosting{}, err
	}

	title := firstNonEmptyString(detail.Info.Title, posting.Title)
	location := firstNonEmptyString(detail.Info.LocationsText, workdayRawText(detail.Info.JobRequisitionLocation), workdayRawText(detail.Info.Location), posting.LocationsText)
	country := workdayCountry(firstNonEmptyString(workdayRawText(detail.Info.Country), location))
	description := cleanHTMLText(detail.Info.JobDescription)
	jobReqID := firstNonEmptyString(detail.Info.JobReqID, posting.JobReqID(), stableJobToken(posting.ExternalPath, title))
	applyURL := firstNonEmptyString(detail.Info.ExternalURL, workdayHostedURL(config, posting.ExternalPath), source.URL)
	employment := employmentFromText(title, firstNonEmptyString(detail.Info.TimeType, posting.TimeType))
	postedAt := parseTimePtr(firstNonEmptyString(workdayRawText(detail.Info.Posted), detail.Info.StartDate, posting.PostedOn))

	evidence := []Evidence{
		{Field: "ats", Text: "Workday CXS jobs API", URL: searchEndpoint},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: firstNonEmptyString(detailURL, applyURL)})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
	}

	if detail.Info.CanApply != nil && !*detail.Info.CanApply {
		return JobPosting{}, nil
	}

	return JobPosting{
		SourceJobID:    "workday:" + config.Tenant + ":" + config.Site + ":" + jobReqID,
		Company:        sourceCompany(source, config.Tenant),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		RoleFamily:     inferRoleFamily(title + " " + description),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       postedAt,
		Live:           true,
		Confidence:     0.88,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, nil
}

func (e *ATSExtractor) workdayPostingDetail(ctx context.Context, config workdayConfig, externalPath string) (workdayPostingDetail, string, error) {
	if strings.TrimSpace(externalPath) == "" {
		return workdayPostingDetail{}, "", errors.New("workday external path is required")
	}
	endpoint, err := joinURL(config.BaseURL, "wday", "cxs", config.Tenant, config.Site, strings.TrimLeft(externalPath, "/"))
	if err != nil {
		return workdayPostingDetail{}, "", err
	}
	var detail workdayPostingDetail
	if err := e.getJSON(ctx, endpoint.String(), &detail); err != nil {
		return workdayPostingDetail{}, endpoint.String(), err
	}
	return detail, endpoint.String(), nil
}

func (e *ATSExtractor) getJSON(ctx context.Context, endpoint string, out any) error {
	return e.getJSONWithHeaders(ctx, endpoint, nil, out)
}

func (e *ATSExtractor) getJSONWithHeaders(ctx context.Context, endpoint string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "RadarJobIntel/0.1 (+https://radar.local)")
	for key, value := range headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, strings.TrimSpace(value))
		}
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ats fetch failed: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (e *ATSExtractor) postJSON(ctx context.Context, endpoint string, payload any, out any) error {
	return e.postJSONWithHeaders(ctx, endpoint, payload, nil, out)
}

func (e *ATSExtractor) postJSONWithHeaders(ctx context.Context, endpoint string, payload any, headers map[string]string, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "RadarJobIntel/0.1 (+https://radar.local)")
	for key, value := range headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, strings.TrimSpace(value))
		}
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ats post failed: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (e *ATSExtractor) postForm(ctx context.Context, endpoint string, form url.Values, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "RadarJobIntel/0.1 (+https://radar.local)")
	for key, value := range headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, strings.TrimSpace(value))
		}
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ats form post failed: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (e *ATSExtractor) getXML(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/xml,text/xml")
	req.Header.Set("User-Agent", "RadarJobIntel/0.1 (+https://radar.local)")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ats xml fetch failed: %s", resp.Status)
	}
	return xml.NewDecoder(resp.Body).Decode(out)
}

func (e *ATSExtractor) getText(ctx context.Context, endpoint string, accept string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", firstNonEmptyString(accept, "text/plain,*/*"))
	req.Header.Set("User-Agent", "RadarJobIntel/0.1 (+https://radar.local)")
	resp, err := e.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ats text fetch failed: %s", resp.Status)
	}
	body, err := readLimited(resp.Body, defaultStaticMaxBodyBytes)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

type greenhouseResponse struct {
	Jobs []greenhouseJob `json:"jobs"`
}

type greenhouseJob struct {
	ID          int64                 `json:"id"`
	Title       string                `json:"title"`
	UpdatedAt   string                `json:"updated_at"`
	Location    greenhouseLocation    `json:"location"`
	AbsoluteURL string                `json:"absolute_url"`
	Content     string                `json:"content"`
	Departments []greenhouseNamedItem `json:"departments"`
	Offices     []greenhouseOffice    `json:"offices"`
}

type greenhouseLocation struct {
	Name string `json:"name"`
}

type greenhouseNamedItem struct {
	Name string `json:"name"`
}

type greenhouseOffice struct {
	Name     string `json:"name"`
	Location string `json:"location"`
}

type leverPosting struct {
	ID               string          `json:"id"`
	Text             string          `json:"text"`
	Categories       leverCategories `json:"categories"`
	Country          string          `json:"country"`
	Description      string          `json:"description"`
	DescriptionPlain string          `json:"descriptionPlain"`
	HostedURL        string          `json:"hostedUrl"`
	ApplyURL         string          `json:"applyUrl"`
	CreatedAt        int64           `json:"createdAt"`
	UpdatedAt        int64           `json:"updatedAt"`
}

type leverCategories struct {
	Location     string   `json:"location"`
	Commitment   string   `json:"commitment"`
	Team         string   `json:"team"`
	Department   string   `json:"department"`
	AllLocations []string `json:"allLocations"`
}

type ashbyResponse struct {
	Jobs []ashbyJob `json:"jobs"`
}

type ashbyJob struct {
	Title              string                   `json:"title"`
	Location           string                   `json:"location"`
	SecondaryLocations []ashbySecondaryLocation `json:"secondaryLocations"`
	Department         string                   `json:"department"`
	Team               string                   `json:"team"`
	IsListed           *bool                    `json:"isListed"`
	IsRemote           bool                     `json:"isRemote"`
	WorkplaceType      string                   `json:"workplaceType"`
	DescriptionPlain   string                   `json:"descriptionPlain"`
	PublishedAt        string                   `json:"publishedAt"`
	EmploymentType     string                   `json:"employmentType"`
	JobURL             string                   `json:"jobUrl"`
	ApplyURL           string                   `json:"applyUrl"`
}

type ashbySecondaryLocation struct {
	Location string `json:"location"`
}

type workableResponse struct {
	Name string        `json:"name"`
	Jobs []workableJob `json:"jobs"`
}

type workableJobsResponse struct {
	Title         string        `json:"title"`
	TotalSize     int           `json:"totalSize"`
	NextPageToken string        `json:"nextPageToken"`
	Jobs          []workableJob `json:"jobs"`
}

type workableJob struct {
	ID                string            `json:"id"`
	Title             string            `json:"title"`
	FullTitle         string            `json:"full_title"`
	Shortcode         string            `json:"shortcode"`
	State             string            `json:"state"`
	Department        string            `json:"department"`
	URL               string            `json:"url"`
	ApplicationURL    string            `json:"application_url"`
	Shortlink         string            `json:"shortlink"`
	Location          workableLocation  `json:"location"`
	Locations         workableLocations `json:"locations"`
	Description       string            `json:"description"`
	FullDescription   string            `json:"full_description"`
	Requirements      string            `json:"requirements"`
	Benefits          string            `json:"benefits"`
	CreatedAt         string            `json:"created_at"`
	Created           string            `json:"created"`
	Updated           string            `json:"updated"`
	PublishedAt       string            `json:"published_at"`
	PublishedOn       string            `json:"published_on"`
	EmploymentType    string            `json:"employment_type"`
	EmploymentTypeAlt string            `json:"employmentType"`
	WorkType          string            `json:"worktype"`
	Company           workableCompany   `json:"company"`
	Workplace         string            `json:"workplace"`
}

type workableLocation struct {
	LocationStr    string `json:"location_str"`
	Name           string `json:"name"`
	Country        string `json:"country"`
	CountryName    string `json:"country_name"`
	CountryNameAlt string `json:"countryName"`
	CountryCode    string `json:"country_code"`
	Region         string `json:"region"`
	City           string `json:"city"`
	Telecommuting  bool   `json:"telecommuting"`
	WorkplaceType  string `json:"workplace_type"`
	Subregion      string `json:"subregion"`
}

type workableLocations []workableLocation

func (locations *workableLocations) UnmarshalJSON(data []byte) error {
	var objects []workableLocation
	if err := json.Unmarshal(data, &objects); err == nil {
		*locations = objects
		return nil
	}
	var stringsPayload []string
	if err := json.Unmarshal(data, &stringsPayload); err != nil {
		return err
	}
	out := make([]workableLocation, 0, len(stringsPayload))
	for _, value := range stringsPayload {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		loc := workableLocation{LocationStr: value}
		if strings.EqualFold(value, "TELECOMMUTE") {
			loc.Telecommuting = true
			loc.WorkplaceType = "remote"
			loc.LocationStr = "Remote"
		}
		out = append(out, loc)
	}
	*locations = out
	return nil
}

type workableCompany struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Website string `json:"website"`
	URL     string `json:"url"`
}

type recruiteeResponse struct {
	Offers []recruiteeOffer `json:"offers"`
}

type recruiteeOffer struct {
	ID             int64               `json:"id"`
	Slug           string              `json:"slug"`
	Title          string              `json:"title"`
	Kind           string              `json:"kind"`
	Status         string              `json:"status"`
	State          string              `json:"state"`
	Department     string              `json:"department"`
	CareersURL     string              `json:"careers_url"`
	URL            string              `json:"url"`
	Description    string              `json:"description"`
	Requirements   string              `json:"requirements"`
	Location       string              `json:"location"`
	Locations      []recruiteeLocation `json:"locations"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
	PublishedAt    string              `json:"published_at"`
	EmploymentType string              `json:"employment_type"`
}

type recruiteeLocation struct {
	Name        string `json:"name"`
	City        string `json:"city"`
	State       string `json:"state"`
	Region      string `json:"region"`
	Country     string `json:"country"`
	CountryCode string `json:"country_code"`
}

type smartRecruitersPostingsResponse struct {
	Limit      int                      `json:"limit"`
	Offset     int                      `json:"offset"`
	TotalFound int                      `json:"totalFound"`
	Content    []smartRecruitersPosting `json:"content"`
}

type smartRecruitersPosting struct {
	ID               string                  `json:"id"`
	UUID             string                  `json:"uuid"`
	Name             string                  `json:"name"`
	ReleasedDate     string                  `json:"releasedDate"`
	PostedDate       string                  `json:"postedDate"`
	ApplyURL         string                  `json:"applyUrl"`
	JobAdURL         string                  `json:"jobAdUrl"`
	Ref              string                  `json:"ref"`
	Company          smartRecruitersCompany  `json:"company"`
	Location         smartRecruitersLocation `json:"location"`
	Department       smartRecruitersLabel    `json:"department"`
	Function         smartRecruitersLabel    `json:"function"`
	TypeOfEmployment smartRecruitersLabel    `json:"typeOfEmployment"`
	ExperienceLevel  smartRecruitersLabel    `json:"experienceLevel"`
	JobAd            smartRecruitersJobAd    `json:"jobAd"`
}

type smartRecruitersCompany struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
}

type smartRecruitersLocation struct {
	City        string `json:"city"`
	Region      string `json:"region"`
	Country     string `json:"country"`
	CountryCode string `json:"countryCode"`
	Remote      bool   `json:"remote"`
}

type smartRecruitersLabel struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type smartRecruitersJobAd struct {
	CompanyDescription    json.RawMessage                   `json:"companyDescription"`
	JobDescription        json.RawMessage                   `json:"jobDescription"`
	Qualifications        json.RawMessage                   `json:"qualifications"`
	AdditionalInformation json.RawMessage                   `json:"additionalInformation"`
	Sections              map[string]smartRecruitersSection `json:"sections"`
}

type smartRecruitersSection struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

type janeStreetJob struct {
	ID           int64  `json:"id"`
	Position     string `json:"position"`
	Category     string `json:"category"`
	Availability string `json:"availability"`
	City         string `json:"city"`
	Overview     string `json:"overview"`
	Team         string `json:"team"`
	Duration     string `json:"duration"`
}

type akunaJob struct {
	ID          int64    `json:"id"`
	Title       string   `json:"title"`
	Location    string   `json:"location"`
	LocationRaw string   `json:"locationRaw"`
	Departments []string `json:"departments"`
	Experience  string   `json:"experience"`
	Specialties []string `json:"specialties"`
	AbsoluteURL string   `json:"absolute_url"`
	UpdatedAt   string   `json:"updated_at"`
	Content     string   `json:"content"`
}

type comeetConfig struct {
	CompanyUID string
	Token      string
}

type comeetPosition struct {
	Name                 string           `json:"name"`
	Department           string           `json:"department"`
	UID                  string           `json:"uid"`
	CompanyName          string           `json:"company_name"`
	EmploymentType       string           `json:"employment_type"`
	ExperienceLevel      string           `json:"experience_level"`
	URLComeetHostedPage  string           `json:"url_comeet_hosted_page"`
	URLRecruitHostedPage string           `json:"url_recruit_hosted_page"`
	URLActivePage        string           `json:"url_active_page"`
	PositionURL          string           `json:"position_url"`
	TimeUpdated          string           `json:"time_updated"`
	WorkplaceType        string           `json:"workplace_type"`
	IsInternal           bool             `json:"is_internal"`
	Location             comeetLocation   `json:"location"`
	Details              []comeetDetail   `json:"details"`
	Locations            []comeetLocation `json:"locations"`
}

type comeetLocation struct {
	Name     string `json:"name"`
	Country  string `json:"country"`
	City     string `json:"city"`
	State    string `json:"state"`
	IsRemote bool   `json:"is_remote"`
}

type comeetDetail struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Order int    `json:"order"`
}

type workdayConfig struct {
	BaseURL          string
	Tenant           string
	Site             string
	PublicPathPrefix string
}

type workdaySearchRequest struct {
	AppliedFacets map[string]any `json:"appliedFacets"`
	Limit         int            `json:"limit"`
	Offset        int            `json:"offset"`
	SearchText    string         `json:"searchText"`
}

type workdaySearchResponse struct {
	Total       int                     `json:"total"`
	JobPostings []workdayPostingSummary `json:"jobPostings"`
}

type workdayPostingSummary struct {
	Title         string   `json:"title"`
	ExternalPath  string   `json:"externalPath"`
	LocationsText string   `json:"locationsText"`
	PostedOn      string   `json:"postedOn"`
	TimeType      string   `json:"timeType"`
	BulletFields  []string `json:"bulletFields"`
}

func (p workdayPostingSummary) JobReqID() string {
	for _, field := range p.BulletFields {
		field = strings.TrimSpace(field)
		if field != "" {
			return field
		}
	}
	if token := stableJobToken(p.ExternalPath, p.Title); token != "" {
		if idx := strings.LastIndex(token, "_"); idx >= 0 && idx+1 < len(token) {
			return token[idx+1:]
		}
		return token
	}
	return ""
}

type workdayPostingDetail struct {
	Info workdayPostingInfo `json:"jobPostingInfo"`
}

type workdayPostingInfo struct {
	ID                     string          `json:"id"`
	Title                  string          `json:"title"`
	JobDescription         string          `json:"jobDescription"`
	JobReqID               string          `json:"jobReqId"`
	ExternalURL            string          `json:"externalUrl"`
	LocationsText          string          `json:"locationsText"`
	JobRequisitionLocation json.RawMessage `json:"jobRequisitionLocation"`
	Location               json.RawMessage `json:"location"`
	Country                json.RawMessage `json:"country"`
	TimeType               string          `json:"timeType"`
	Posted                 json.RawMessage `json:"posted"`
	PostedOn               string          `json:"postedOn"`
	StartDate              string          `json:"startDate"`
	CanApply               *bool           `json:"canApply"`
}

type breezyPosition struct {
	ID            string           `json:"id"`
	FriendlyID    string           `json:"friendly_id"`
	Name          string           `json:"name"`
	URL           string           `json:"url"`
	PublishedDate string           `json:"published_date"`
	Type          breezyType       `json:"type"`
	Location      breezyLocation   `json:"location"`
	Locations     []breezyLocation `json:"locations"`
	Department    string           `json:"department"`
	Salary        string           `json:"salary"`
	Company       breezyCompany    `json:"company"`
	Description   string           `json:"description"`
}

type breezyType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type breezyCompany struct {
	Name       string `json:"name"`
	FriendlyID string `json:"friendly_id"`
}

type breezyLocation struct {
	Country  breezyCountry `json:"country"`
	City     string        `json:"city"`
	Name     string        `json:"name"`
	Primary  bool          `json:"primary"`
	IsRemote bool          `json:"is_remote"`
}

type breezyCountry struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type personioFeed struct {
	Positions []personioPosition `xml:"position"`
}

type personioPosition struct {
	ID                 string                   `xml:"id"`
	Subcompany         string                   `xml:"subcompany"`
	Office             string                   `xml:"office"`
	AdditionalOffices  []string                 `xml:"additionalOffices>office"`
	Department         string                   `xml:"department"`
	RecruitingCategory string                   `xml:"recruitingCategory"`
	Name               string                   `xml:"name"`
	Descriptions       []personioJobDescription `xml:"jobDescriptions>jobDescription"`
	EmploymentType     string                   `xml:"employmentType"`
	Seniority          string                   `xml:"seniority"`
	Schedule           string                   `xml:"schedule"`
	YearsOfExperience  string                   `xml:"yearsOfExperience"`
	Occupation         string                   `xml:"occupation"`
	OccupationCategory string                   `xml:"occupationCategory"`
	CreatedAt          string                   `xml:"createdAt"`
}

type personioJobDescription struct {
	Name  string `xml:"name"`
	Value string `xml:"value"`
}

type pinpointResponse struct {
	Data []pinpointPosting `json:"data"`
}

type pinpointPosting struct {
	ID                       string            `json:"id"`
	Title                    string            `json:"title"`
	URL                      string            `json:"url"`
	Description              string            `json:"description"`
	KeyResponsibilities      string            `json:"key_responsibilities"`
	SkillsKnowledgeExpertise string            `json:"skills_knowledge_expertise"`
	Benefits                 string            `json:"benefits"`
	EmploymentType           string            `json:"employment_type"`
	EmploymentTypeText       string            `json:"employment_type_text"`
	WorkplaceType            string            `json:"workplace_type"`
	WorkplaceTypeText        string            `json:"workplace_type_text"`
	Location                 pinpointLocation  `json:"location"`
	Job                      map[string]string `json:"job"`
}

type pinpointLocation struct {
	ID         string `json:"id"`
	City       string `json:"city"`
	Name       string `json:"name"`
	Province   string `json:"province"`
	PostalCode string `json:"postal_code"`
}

type polymerJobsResponse struct {
	Items []polymerJob `json:"items"`
	Meta  polymerMeta  `json:"meta"`
}

type polymerMeta struct {
	IsLast           bool   `json:"is_last"`
	IsFirst          bool   `json:"is_first"`
	Page             int    `json:"page"`
	NextPage         int    `json:"next_page"`
	Count            int    `json:"count"`
	Total            int    `json:"total"`
	OrganizationName string `json:"organization_name"`
}

type polymerJob struct {
	ID                                          int64    `json:"id"`
	JobID                                       int64    `json:"job_id"`
	HashID                                      string   `json:"hash_id"`
	Title                                       string   `json:"title"`
	Description                                 string   `json:"description"`
	Country                                     string   `json:"country"`
	StateRegion                                 string   `json:"state_region"`
	City                                        string   `json:"city"`
	DisplayLocation                             string   `json:"display_location"`
	OrganizationName                            string   `json:"organization_name"`
	Kind                                        string   `json:"kind"`
	KindPretty                                  string   `json:"kind_pretty"`
	PublishedAt                                 string   `json:"published_at"`
	CreatedAt                                   string   `json:"created_at"`
	UpdatedAt                                   string   `json:"updated_at"`
	JobPostURL                                  string   `json:"job_post_url"`
	JobApplicationDescriptionURL                string   `json:"job_application_description_url"`
	RemotenessPretty                            string   `json:"remoteness_pretty"`
	JobCategoryName                             string   `json:"job_category_name"`
	Department                                  string   `json:"department"`
	SalaryPretty                                string   `json:"salary_pretty"`
	RemoteRestrictionCountryList                []string `json:"remote_restriction_country_list"`
	RemoteRestrictionCountryResidencyIsRequired bool     `json:"remote_restriction_country_residency_is_required"`
	RemoteRestrictionOverlapHours               int      `json:"remote_restriction_overlap_hours"`
	RemoteRestrictionOverlapHoursIsRequired     bool     `json:"remote_restriction_overlap_hours_is_required"`
	RemoteRestrictionTimezoneUTCOffsetSeconds   int      `json:"remote_restriction_timezone_utc_offset_seconds"`
	RemoteRestrictionCity                       string   `json:"remote_restriction_city"`
	RemoteRestrictionCityGooglePlaceID          string   `json:"remote_restriction_city_google_place_id"`
}

type icimsSitemap struct {
	URLs []icimsSitemapEntry `xml:"url"`
}

type icimsSitemapEntry struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod"`
}

type jazzHRJobLink struct {
	URL string
}

type jobviteJobLink struct {
	URL string
}

type teamtailorJobLink struct {
	URL string
}

type manatalJobLink struct {
	URL      string
	ID       string
	Title    string
	Location string
	Country  string
}

type occupopRow struct {
	URL      string
	ID       string
	Title    string
	Location string
	Category string
	Type     string
}

type workstreamCard struct {
	URL          string
	ID           string
	Title        string
	Location     string
	Description  string
	Employment   string
	Compensation string
}

type joinBoardData struct {
	State joinInitialState
}

type joinNextData struct {
	Props struct {
		PageProps struct {
			InitialState joinInitialState `json:"initialState"`
		} `json:"pageProps"`
	} `json:"props"`
}

type joinInitialState struct {
	Company joinCompanyState `json:"company"`
	Jobs    joinJobsState    `json:"jobs"`
}

type joinCompanyState struct {
	Name   string `json:"name"`
	Domain string `json:"domain"`
}

type joinJobsState struct {
	Items []joinJobItem `json:"items"`
}

type joinJobItem struct {
	ID              json.RawMessage `json:"id"`
	IDParam         string          `json:"idParam"`
	Title           string          `json:"title"`
	CreatedAt       string          `json:"createdAt"`
	WorkplaceType   string          `json:"workplaceType"`
	City            joinCity        `json:"city"`
	Country         joinCountry     `json:"country"`
	EmploymentType  joinNameState   `json:"employmentType"`
	Category        joinNameState   `json:"category"`
	SalaryFrequency string          `json:"salaryFrequency"`
	Settings        joinJobSettings `json:"settings"`
}

type joinCity struct {
	CityName    string `json:"cityName"`
	CountryName string `json:"countryName"`
	RegionName  string `json:"regionName"`
}

type joinCountry struct {
	ISO3166 string `json:"iso3166"`
}

type joinNameState struct {
	Name string `json:"name"`
}

type joinJobSettings struct {
	ShowSalary bool `json:"showSalary"`
}

type bambooHRListResponse struct {
	Result []bambooHRListJob `json:"result"`
}

type bambooHRListJob struct {
	ID                    string              `json:"id"`
	JobOpeningName        string              `json:"jobOpeningName"`
	DepartmentLabel       string              `json:"departmentLabel"`
	EmploymentStatusLabel string              `json:"employmentStatusLabel"`
	Location              bambooHRLocation    `json:"location"`
	ATSLocation           bambooHRATSLocation `json:"atsLocation"`
	IsRemote              *bool               `json:"isRemote"`
	LocationType          string              `json:"locationType"`
}

type bambooHRDetailResponse struct {
	Result bambooHRDetailResult `json:"result"`
}

type bambooHRDetailResult struct {
	JobOpening bambooHRDetailJob `json:"jobOpening"`
}

type bambooHRDetailJob struct {
	ID                    string              `json:"id"`
	JobOpeningShareURL    string              `json:"jobOpeningShareUrl"`
	JobOpeningName        string              `json:"jobOpeningName"`
	JobOpeningStatus      string              `json:"jobOpeningStatus"`
	DepartmentLabel       string              `json:"departmentLabel"`
	EmploymentStatusLabel string              `json:"employmentStatusLabel"`
	Location              bambooHRLocation    `json:"location"`
	ATSLocation           bambooHRATSLocation `json:"atsLocation"`
	IsRemote              *bool               `json:"isRemote"`
	Description           string              `json:"description"`
	Compensation          string              `json:"compensation"`
	DatePosted            string              `json:"datePosted"`
	MinimumExperience     string              `json:"minimumExperience"`
	LocationType          string              `json:"locationType"`
}

type bambooHRLocation struct {
	City           string `json:"city"`
	State          string `json:"state"`
	Province       string `json:"province"`
	PostalCode     string `json:"postalCode"`
	AddressCountry string `json:"addressCountry"`
	Country        string `json:"country"`
}

type bambooHRATSLocation struct {
	Country  string `json:"country"`
	State    string `json:"state"`
	Province string `json:"province"`
	City     string `json:"city"`
}

type ripplingJobsResponse struct {
	Items      []ripplingJobSummary `json:"items"`
	Page       int                  `json:"page"`
	PageSize   int                  `json:"pageSize"`
	TotalItems int                  `json:"totalItems"`
	TotalPages int                  `json:"totalPages"`
}

type ripplingJobSummary struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	URL        string             `json:"url"`
	Department ripplingDepartment `json:"department"`
	Locations  []ripplingLocation `json:"locations"`
	Language   string             `json:"language"`
}

type ripplingJobDetail struct {
	UUID            string                 `json:"uuid"`
	Name            string                 `json:"name"`
	URL             string                 `json:"url"`
	CompanyName     string                 `json:"companyName"`
	Description     ripplingDescription    `json:"description"`
	WorkLocations   []string               `json:"workLocations"`
	Department      ripplingDepartment     `json:"department"`
	EmploymentType  ripplingEmploymentType `json:"employmentType"`
	CreatedOn       string                 `json:"createdOn"`
	PayRangeDetails []ripplingPayRange     `json:"payRangeDetails"`
}

type ripplingDescription struct {
	Company string `json:"company"`
	Role    string `json:"role"`
}

type ripplingDepartment struct {
	Name           string   `json:"name"`
	BaseDepartment string   `json:"base_department"`
	DepartmentTree []string `json:"department_tree"`
}

type ripplingEmploymentType struct {
	Label string `json:"label"`
	ID    string `json:"id"`
}

type ripplingLocation struct {
	Name          string `json:"name"`
	Country       string `json:"country"`
	CountryCode   string `json:"countryCode"`
	State         string `json:"state"`
	StateCode     string `json:"stateCode"`
	City          string `json:"city"`
	WorkplaceType string `json:"workplaceType"`
}

type ripplingPayRange struct {
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
	Currency string  `json:"currency"`
}

type successFactorsRSS struct {
	Channel successFactorsChannel `xml:"channel"`
}

type successFactorsChannel struct {
	Title       string               `xml:"title"`
	Description string               `xml:"description"`
	Items       []successFactorsItem `xml:"item"`
}

type successFactorsItem struct {
	Title          string `xml:"title"`
	Description    string `xml:"description"`
	Link           string `xml:"link"`
	GUID           string `xml:"guid"`
	ID             string `xml:"http://base.google.com/ns/1.0 id"`
	ExpirationDate string `xml:"http://base.google.com/ns/1.0 expiration_date"`
	Employer       string `xml:"http://base.google.com/ns/1.0 employer"`
	JobFunction    string `xml:"http://base.google.com/ns/1.0 job_function"`
	Location       string `xml:"http://base.google.com/ns/1.0 location"`
}

type adpWorkforceNowConfig struct {
	CID    string
	CCID   string
	Lang   string
	Locale string
	JobID  string
	JWID   string
}

type adpWorkforceNowResponse struct {
	JobRequisitions []adpWorkforceNowJob `json:"jobRequisitions"`
	Meta            adpWorkforceNowMeta  `json:"meta"`
}

type adpWorkforceNowMeta struct {
	TotalNumber int `json:"totalNumber"`
}

type adpWorkforceNowJob struct {
	ItemID                 string                          `json:"itemID"`
	RequisitionTitle       string                          `json:"requisitionTitle"`
	ClientRequisitionID    string                          `json:"clientRequisitionID"`
	PostDate               string                          `json:"postDate"`
	RequisitionDescription string                          `json:"requisitionDescription"`
	RequisitionLocations   []adpWorkforceNowLocation       `json:"requisitionLocations"`
	WorkLevelCode          adpWorkforceNowCode             `json:"workLevelCode"`
	CustomFieldGroup       adpWorkforceNowCustomFieldGroup `json:"customFieldGroup"`
	PayGradeRange          adpWorkforceNowPayGradeRange    `json:"payGradeRange"`
}

type adpWorkforceNowLocation struct {
	NameCode adpWorkforceNowCode    `json:"nameCode"`
	Address  adpWorkforceNowAddress `json:"address"`
}

type adpWorkforceNowAddress struct {
	CityName                 string              `json:"cityName"`
	LineOne                  string              `json:"lineOne"`
	CountrySubdivisionLevel1 adpWorkforceNowCode `json:"countrySubdivisionLevel1"`
	Country                  adpWorkforceNowCode `json:"country"`
	PostalCode               string              `json:"postalCode"`
}

type adpWorkforceNowCode struct {
	CodeValue string `json:"codeValue"`
	ShortName string `json:"shortName"`
	LongName  string `json:"longName"`
}

type adpWorkforceNowCustomFieldGroup struct {
	StringFields []adpWorkforceNowStringField `json:"stringFields"`
	DateFields   []adpWorkforceNowDateField   `json:"dateFields"`
}

type adpWorkforceNowStringField struct {
	StringValue string              `json:"stringValue"`
	NameCode    adpWorkforceNowCode `json:"nameCode"`
}

type adpWorkforceNowDateField struct {
	DateValue string              `json:"dateValue"`
	NameCode  adpWorkforceNowCode `json:"nameCode"`
}

type adpWorkforceNowPayGradeRange struct {
	MinimumRate adpWorkforceNowMoney `json:"minimumRate"`
	MaximumRate adpWorkforceNowMoney `json:"maximumRate"`
}

type adpWorkforceNowMoney struct {
	AmountValue  float64 `json:"amountValue"`
	CurrencyCode string  `json:"currencyCode"`
}

type adpMyJobsConfig struct {
	Domain string
	Lang   string
	ReqID  string
}

type adpMyJobsCareerSite struct {
	Domain      string `json:"domain"`
	ClientName  string `json:"clientName"`
	MyJobsToken string `json:"myJobsToken"`
}

type adpMyJobsResponse struct {
	Count           int                    `json:"count"`
	JobRequisitions []adpMyJobsRequisition `json:"jobRequisitions"`
}

type adpMyJobsRequisition struct {
	ItemID                 any                       `json:"itemID"`
	ReqID                  string                    `json:"reqId"`
	JobTitle               string                    `json:"jobTitle"`
	RequisitionTitle       string                    `json:"requisitionTitle"`
	ClientRequisitionID    string                    `json:"clientRequisitionID"`
	RequisitionDescription string                    `json:"requisitionDescription"`
	RequisitionLocations   []adpWorkforceNowLocation `json:"requisitionLocations"`
	PostingInstructions    []adpMyJobsPosting        `json:"postingInstructions"`
	ScreeningRequirements  []adpMyJobsRequirement    `json:"screeningRequirements"`
	CustomFieldGroup       adpMyJobsCustomFieldGroup `json:"customFieldGroup"`
	EasyApplyEnabled       bool                      `json:"easyApplyEnabled"`
	Type                   string                    `json:"type"`
	CanApply               bool                      `json:"canApply"`
}

type adpMyJobsPosting struct {
	TimestampLastPosted string                  `json:"timestampLastPosted"`
	PostDate            string                  `json:"postDate"`
	PostingChannel      adpMyJobsPostingChannel `json:"postingChannel"`
}

type adpMyJobsPostingChannel struct {
	InternetAddress string `json:"internetAddress"`
	ChannelID       string `json:"channelID"`
}

type adpMyJobsRequirement struct {
	RequirementDescription string `json:"requirementDescription"`
}

type adpMyJobsCustomFieldGroup struct {
	CodeFields []adpMyJobsCodeField `json:"codeFields"`
}

type adpMyJobsCodeField struct {
	CategoryCode adpWorkforceNowCode `json:"categoryCode"`
	CodeValue    string              `json:"codeValue"`
	LongName     string              `json:"longName"`
	ShortName    string              `json:"shortName"`
}

type ukgProConfig struct {
	Account string
	BoardID string
}

type ukgProOpportunity struct {
	ID                string           `json:"Id"`
	Featured          bool             `json:"Featured"`
	Title             string           `json:"Title"`
	RequisitionNumber string           `json:"RequisitionNumber"`
	FullTime          *bool            `json:"FullTime"`
	JobCategoryName   string           `json:"JobCategoryName"`
	Locations         []ukgProLocation `json:"Locations"`
	PostedDate        string           `json:"PostedDate"`
	BriefDescription  string           `json:"BriefDescription"`
	JobLocationType   *int             `json:"JobLocationType"`
	OpportunityType   *int             `json:"OpportunityType"`
}

type ukgProLoadSearchRequest struct {
	OpportunitySearch ukgProOpportunitySearch `json:"opportunitySearch"`
}

type ukgProOpportunitySearch struct {
	QueryString    string              `json:"QueryString"`
	Filters        []any               `json:"Filters"`
	Top            int                 `json:"Top"`
	Skip           int                 `json:"Skip"`
	LocationIDs    []string            `json:"LocationIds"`
	JobCategoryIDs []string            `json:"JobCategoryIds"`
	FullTime       *bool               `json:"FullTime"`
	OrderBy        []ukgProSearchOrder `json:"OrderBy"`
}

type ukgProSearchOrder struct {
	Value        string `json:"Value"`
	PropertyName string `json:"PropertyName"`
	Ascending    bool   `json:"Ascending"`
}

type ukgProLoadSearchResponse struct {
	Opportunities []ukgProOpportunity `json:"opportunities"`
	TotalCount    int                 `json:"totalCount"`
}

type ukgProLocation struct {
	LocalizedName        string        `json:"LocalizedName"`
	LocalizedLocationID  string        `json:"LocalizedLocationId"`
	LocalizedDescription string        `json:"LocalizedDescription"`
	Address              ukgProAddress `json:"Address"`
}

type ukgProAddress struct {
	City       string     `json:"City"`
	PostalCode string     `json:"PostalCode"`
	State      ukgProCode `json:"State"`
	Country    ukgProCode `json:"Country"`
}

type ukgProCode struct {
	Code string `json:"Code"`
	Name string `json:"Name"`
}

type dayforceConfig struct {
	Culture         string
	ClientNamespace string
	JobBoardCode    string
	JobID           string
}

type dayforceNextData struct {
	Props dayforceNextProps `json:"props"`
	Query dayforceNextQuery `json:"query"`
}

type dayforceNextProps struct {
	PageProps dayforcePageProps `json:"pageProps"`
}

type dayforcePageProps struct {
	JobData dayforceJobData `json:"jobData"`
}

type dayforceNextQuery struct {
	ClientNamespace    string `json:"clientNamespace"`
	CareerSiteXRefCode string `json:"careerSiteXRefCode"`
	ID                 string `json:"id"`
}

type dayforceJobData struct {
	JobPostingID              int64                      `json:"jobPostingId"`
	JobReqID                  int64                      `json:"jobReqId"`
	JobTitle                  string                     `json:"jobTitle"`
	JobDescription            string                     `json:"jobDescription"`
	Description               string                     `json:"description"`
	ShortDescription          string                     `json:"shortDescription"`
	PostingStartTimestampUTC  string                     `json:"postingStartTimestampUTC"`
	PostingExpiryTimestampUTC string                     `json:"postingExpiryTimestampUTC"`
	CreatedTimestampUTC       string                     `json:"createdTimestampUTC"`
	LastModifiedTimestampUTC  string                     `json:"lastModifiedTimestampUTC"`
	ISOCurrencyRegion         string                     `json:"isoCurrencyRegion"`
	JobPostingContent         dayforceJobPostingContent  `json:"jobPostingContent"`
	HasVirtualLocation        bool                       `json:"hasVirtualLocation"`
	PostingLocations          []dayforcePostingLocation  `json:"postingLocations"`
	JobPostingAttributes      []dayforcePostingAttribute `json:"jobPostingAttributes"`
	PostingStatus             int                        `json:"postingStatus"`
}

type dayforceJobPostingContent struct {
	JobDescriptionHeader string `json:"jobDescriptionHeader"`
	JobDescription       string `json:"jobDescription"`
	JobDescriptionFooter string `json:"jobDescriptionFooter"`
}

type dayforcePostingLocation struct {
	FormattedAddress string `json:"formattedAddress"`
	ISOCountryCode   string `json:"isoCountryCode"`
	StateCode        string `json:"stateCode"`
	CityName         string `json:"cityName"`
}

type dayforcePostingAttribute struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
	Type  string          `json:"type"`
}

type dayforceSearchRequest struct {
	ClientNamespace string `json:"clientNamespace"`
	JobBoardCode    string `json:"jobBoardCode"`
	CultureCode     string `json:"cultureCode"`
	DistanceUnit    int    `json:"distanceUnit"`
	PaginationStart int    `json:"paginationStart"`
	PageSize        int    `json:"pageSize,omitempty"`
}

type dayforceSearchResponse struct {
	JobPostings []dayforceJobData `json:"jobPostings"`
	Count       int               `json:"count"`
	MaxCount    int               `json:"maxCount"`
}

type byteDanceSearchRequest struct {
	Keyword string `json:"keyword"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
}

type byteDanceSearchResponse struct {
	Code int `json:"code"`
	Data struct {
		JobPostList []byteDanceJobPost `json:"job_post_list"`
		Count       int                `json:"count"`
	} `json:"data"`
	Message string `json:"message"`
}

type byteDanceJobPost struct {
	ID             string             `json:"id"`
	Code           string             `json:"code"`
	Title          string             `json:"title"`
	Description    string             `json:"description"`
	Requirement    string             `json:"requirement"`
	RecruitType    byteDanceNamedItem `json:"recruit_type"`
	JobCategory    byteDanceNamedItem `json:"job_category"`
	CityInfo       byteDanceLocation  `json:"city_info"`
	JobSubject     byteDanceNamedItem `json:"job_subject"`
	DepartmentInfo byteDanceNamedItem `json:"department_info"`
}

type byteDanceNamedItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	ENName string `json:"en_name"`
}

type byteDanceLocation struct {
	Code   string             `json:"code"`
	Name   string             `json:"name"`
	ENName string             `json:"en_name"`
	Parent *byteDanceLocation `json:"parent"`
}

type oracleRecruitingConfig struct {
	Culture    string
	SiteNumber string
	JobID      string
}

type oracleRecruitingResponse struct {
	Items   []oracleRecruitingSearchItem `json:"items"`
	Count   int                          `json:"count"`
	HasMore bool                         `json:"hasMore"`
	Limit   int                          `json:"limit"`
	Offset  int                          `json:"offset"`
}

type oracleRecruitingSearchItem struct {
	TotalJobsCount int                           `json:"TotalJobsCount"`
	SiteNumber     string                        `json:"SiteNumber"`
	Requisitions   []oracleRecruitingRequisition `json:"requisitionList"`
}

type oracleRecruitingRequisition struct {
	ID                          string `json:"Id"`
	Title                       string `json:"Title"`
	PostedDate                  string `json:"PostedDate"`
	PostingEndDate              string `json:"PostingEndDate"`
	Language                    string `json:"Language"`
	PrimaryLocationCountry      string `json:"PrimaryLocationCountry"`
	PrimaryLocation             string `json:"PrimaryLocation"`
	ShortDescriptionStr         string `json:"ShortDescriptionStr"`
	ExternalQualificationsStr   string `json:"ExternalQualificationsStr"`
	ExternalResponsibilitiesStr string `json:"ExternalResponsibilitiesStr"`
	JobFamily                   string `json:"JobFamily"`
	JobFunction                 string `json:"JobFunction"`
	WorkerType                  string `json:"WorkerType"`
	ContractType                string `json:"ContractType"`
	ManagerLevel                string `json:"ManagerLevel"`
	JobSchedule                 string `json:"JobSchedule"`
	JobShift                    string `json:"JobShift"`
	JobType                     string `json:"JobType"`
	WorkplaceType               string `json:"WorkplaceType"`
	WorkplaceTypeCode           string `json:"WorkplaceTypeCode"`
	BusinessUnit                string `json:"BusinessUnit"`
	Department                  string `json:"Department"`
	Organization                string `json:"Organization"`
	LegalEmployer               string `json:"LegalEmployer"`
	HotJobFlag                  bool   `json:"HotJobFlag"`
	TrendingFlag                bool   `json:"TrendingFlag"`
	BeFirstToApplyFlag          bool   `json:"BeFirstToApplyFlag"`
}

type oracleRecruitingDetail struct {
	Title       string
	Description string
	SiteName    string
}

type paylocityConfig struct {
	FeedID      string
	CompanySlug string
	JobID       string
	FeedAPI     bool
}

type paylocityPageData struct {
	ModuleTitle string         `json:"ModuleTitle"`
	DisplayName string         `json:"displayName"`
	Jobs        []paylocityJob `json:"Jobs"`
}

type paylocityJob struct {
	JobID            int64             `json:"JobId"`
	JobTitle         string            `json:"JobTitle"`
	PublishedDate    string            `json:"PublishedDate"`
	Description      string            `json:"Description"`
	IsInternal       bool              `json:"IsInternal"`
	HiringDepartment string            `json:"HiringDepartment"`
	JobLocation      paylocityLocation `json:"JobLocation"`
	IsRemote         bool              `json:"IsRemote"`
}

type paylocityLocation struct {
	Name                string `json:"Name" xml:"Name"`
	LocationDisplayName string `json:"LocationDisplayName" xml:"LocationDisplayName"`
	City                string `json:"City" xml:"City"`
	State               string `json:"State" xml:"State"`
	Country             string `json:"Country" xml:"Country"`
	Metro               string `json:"Metro" xml:"Metro"`
}

type paylocityDetail struct {
	Job          JobPosting
	Compensation string
}

type paylocityFeedData struct {
	Format            string             `json:"-" xml:"-"`
	DisplayName       string             `json:"displayName" xml:"displayName"`
	DisplayNameLegacy string             `json:"DisplayName" xml:"DisplayName"`
	Jobs              []paylocityFeedJob `json:"jobs" xml:"jobs>job"`
	JobsLegacy        []paylocityFeedJob `json:"Jobs" xml:"Jobs>Job"`
	JobsXML           []paylocityFeedJob `json:"-" xml:"Job"`
	JobsXMLLower      []paylocityFeedJob `json:"-" xml:"job"`
}

type paylocityFeedJob struct {
	JobID                   int64             `json:"jobId" xml:"jobId"`
	JobIDLegacy             int64             `json:"JobId" xml:"JobId"`
	Title                   string            `json:"title" xml:"title"`
	TitleLegacy             string            `json:"Title" xml:"Title"`
	CompanyName             string            `json:"companyName" xml:"companyName"`
	CompanyNameLegacy       string            `json:"CompanyName" xml:"CompanyName"`
	ApplyURL                string            `json:"applyUrl" xml:"applyUrl"`
	ApplyURLLegacy          string            `json:"ApplyUrl" xml:"ApplyUrl"`
	PublishedDate           string            `json:"publishedDate" xml:"publishedDate"`
	PublishedDateLegacy     string            `json:"PublishedDate" xml:"PublishedDate"`
	Description             string            `json:"description" xml:"description"`
	DescriptionLegacy       string            `json:"Description" xml:"Description"`
	Requirements            string            `json:"requirements" xml:"requirements"`
	RequirementsLegacy      string            `json:"Requirements" xml:"Requirements"`
	DisplayURL              string            `json:"displayUrl" xml:"displayUrl"`
	DisplayURLLegacy        string            `json:"DisplayUrl" xml:"DisplayUrl"`
	SalaryDescription       string            `json:"salaryDescription" xml:"salaryDescription"`
	SalaryDescriptionLegacy string            `json:"SalaryDescription" xml:"SalaryDescription"`
	HiringDepartment        string            `json:"hiringDepartment" xml:"hiringDepartment"`
	HiringDepartmentLegacy  string            `json:"HiringDepartment" xml:"HiringDepartment"`
	JobTypes                string            `json:"jobTypes" xml:"jobTypes"`
	JobTypesLegacy          string            `json:"JobTypes" xml:"JobTypes"`
	JobTypesArray           []string          `json:"jobTypesArray" xml:"jobTypesArray>string"`
	JobTypesArrayLegacy     []string          `json:"JobTypesArray" xml:"JobTypesArray>string"`
	JobLocation             paylocityLocation `json:"jobLocation" xml:"jobLocation"`
	JobLocationLegacy       paylocityLocation `json:"JobLocation" xml:"JobLocation"`
}

type paycomPortalSession struct {
	SessionJWT string
	ServiceURL string
}

type paycomLibConfig struct {
	ATSPortalMantleServiceURL string `json:"atsPortalMantleServiceUrl"`
}

type paycomPreviewSearchPayload struct {
	Skip            int                  `json:"skip"`
	Take            int                  `json:"take"`
	FiltersForQuery paycomPreviewFilters `json:"filtersForQuery"`
}

type paycomPreviewFilters struct {
	Categories        []string `json:"categories"`
	Departments       []string `json:"departments"`
	EmploymentTypes   []string `json:"employmentTypes"`
	Locations         []string `json:"locations"`
	PositionTypes     []string `json:"positionTypes"`
	TravelTypes       []string `json:"travelTypes"`
	ShiftTypes        []string `json:"shiftTypes"`
	OtherFilters      []string `json:"otherFilters"`
	KeywordSearchText string   `json:"keywordSearchText"`
	Location          string   `json:"location"`
	SortOption        string   `json:"sortOption"`
}

type paycomPreviewSearchResponse struct {
	JobPostingPreviews      []paycomJobPreview `json:"jobPostingPreviews"`
	JobPostingPreviewsCount int                `json:"jobPostingPreviewsCount"`
}

type paycomJobPreview struct {
	JobID        int64  `json:"jobId"`
	JobTitle     string `json:"jobTitle"`
	PositionType string `json:"positionType"`
	RemoteType   string `json:"remoteType"`
	Locations    string `json:"locations"`
	Description  string `json:"description"`
	PostedOn     string `json:"postedOn"`
}

func (p paycomJobPreview) id() string {
	if p.JobID <= 0 {
		return ""
	}
	return strconv.FormatInt(p.JobID, 10)
}

type paycomDetailResponse struct {
	JobPosting paycomJobDetail `json:"jobPosting"`
}

type paycomJobDetail struct {
	JobID              int64    `json:"jobId"`
	ClientCode         string   `json:"clientCode"`
	JobTitle           string   `json:"jobTitle"`
	Location           string   `json:"location"`
	SecondaryLocations []string `json:"secondaryLocations"`
	City               string   `json:"city"`
	RemoteType         string   `json:"remoteType"`
	SalaryRange        string   `json:"salaryRange"`
	Level              string   `json:"level"`
	StartDate          string   `json:"startDate"`
	EndDate            string   `json:"endDate"`
	PositionType       string   `json:"positionType"`
	JobShift           string   `json:"jobShift"`
	EducationLevel     string   `json:"educationLevel"`
	TravelPercentage   string   `json:"travelPercentage"`
	JobCategory        string   `json:"jobCategory"`
	Description        string   `json:"description"`
	Qualifications     string   `json:"qualifications"`
	GoogleJobJSON      string   `json:"googleJobJson"`
}

type avatureRSS struct {
	Channel avatureRSSChannel `xml:"channel"`
}

type avatureRSSChannel struct {
	Title       string           `xml:"title"`
	Description string           `xml:"description"`
	Link        string           `xml:"link"`
	Items       []avatureRSSItem `xml:"item"`
}

type avatureRSSItem struct {
	Title       string      `xml:"title"`
	Description string      `xml:"description"`
	GUID        avatureGUID `xml:"guid"`
	Link        string      `xml:"link"`
	PubDate     string      `xml:"pubDate"`
}

func (i avatureRSSItem) link() string {
	return firstNonEmptyString(i.Link, i.GUID.Value)
}

type avatureGUID struct {
	Value string `xml:",chardata"`
}

type trakstarRSS struct {
	Channel trakstarRSSChannel `xml:"channel"`
}

type trakstarRSSChannel struct {
	Title       string            `xml:"title"`
	Description string            `xml:"description"`
	Link        string            `xml:"link"`
	Items       []trakstarRSSItem `xml:"item"`
}

type trakstarRSSItem struct {
	Title           string `xml:"title"`
	Description     string `xml:"description"`
	Link            string `xml:"link"`
	GUID            string `xml:"guid"`
	PubDate         string `xml:"pubDate"`
	LocationCity    string `xml:"https://recruiterbox.com/rss/job/ locationCity"`
	LocationState   string `xml:"https://recruiterbox.com/rss/job/ locationState"`
	LocationCountry string `xml:"https://recruiterbox.com/rss/job/ locationCountry"`
	PositionType    string `xml:"https://recruiterbox.com/rss/job/ positionType"`
	Team            string `xml:"https://recruiterbox.com/rss/job/ team"`
}

type avatureDetail struct {
	Title        string
	Location     string
	BusinessArea string
	Description  string
	ApplyURL     string
	PostedAtText string
}

type jobylonFeed struct {
	Jobs []jobylonJob `json:"jobs" xml:"job"`
}

type jobylonJob struct {
	ID             string               `json:"id" xml:"id"`
	Title          string               `json:"title" xml:"title"`
	Slug           string               `json:"slug" xml:"slug"`
	Description    string               `json:"descr" xml:"descr"`
	DescriptionAlt string               `json:"description" xml:"description"`
	Skills         string               `json:"skills" xml:"skills"`
	Function       string               `json:"function" xml:"function"`
	Experience     string               `json:"experience" xml:"experience"`
	EmploymentType string               `json:"employment_type" xml:"employment_type"`
	FromDate       string               `json:"from_date" xml:"from_date"`
	ToDate         string               `json:"to_date" xml:"to_date"`
	Company        jobylonCompany       `json:"company" xml:"company"`
	Department     jobylonDepartment    `json:"departments" xml:"departments"`
	Locations      []jobylonLocation    `json:"locations" xml:"locations>location"`
	URLs           jobylonURLs          `json:"urls" xml:"urls"`
	Extra          []jobylonUnknownNode `json:"-" xml:",any"`
}

type jobylonUnknownNode struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

type jobylonCompany struct {
	Slug    string `json:"slug" xml:"slug"`
	Name    string `json:"name" xml:"name"`
	Website string `json:"website" xml:"website"`
}

type jobylonDepartment struct {
	Description string `json:"descr" xml:"descr"`
	ID          string `json:"id" xml:"id"`
}

type jobylonLocation struct {
	Text         string `json:"text" xml:"text"`
	City         string `json:"city" xml:"city"`
	CityShort    string `json:"city_short" xml:"city_short"`
	Country      string `json:"country" xml:"country"`
	CountryShort string `json:"country_short" xml:"country_short"`
	Area         string `json:"area_1" xml:"area_1"`
}

type jobylonURLs struct {
	Ad    string `json:"ad" xml:"ad"`
	Apply string `json:"apply" xml:"apply"`
}

type zohoRecruitJob struct {
	ID             string `json:"id"`
	JobOpeningName string `json:"Job_Opening_Name"`
	PostingTitle   string `json:"Posting_Title"`
	JobDescription string `json:"Job_Description"`
	DateOpened     string `json:"Date_Opened"`
	JobType        string `json:"Job_Type"`
	Industry       string `json:"Industry"`
	City           string `json:"City"`
	State          string `json:"State"`
	Country        string `json:"Country"`
	RemoteJob      bool   `json:"Remote_Job"`
	Publish        *bool  `json:"Publish"`
	KeepCareerSite bool   `json:"Keep_on_Career_Site"`
	WorkExperience string `json:"Work_Experience"`
	Salary         string `json:"Salary"`
}

type jobsoidJobsResponse struct {
	Items []jobsoidJob
}

const gemBoardListQuery = `
query JobBoardList($boardId: String!) {
  oatsExternalJobPostings(boardId: $boardId) {
    jobPostings {
      id
      extId
      title
      locations {
        id
        name
        city
        isoCountry
        isRemote
        extId
      }
      job {
        id
        department {
          id
          name
          extId
        }
        locationType
        employmentType
      }
    }
  }
  jobBoardExternal(vanityUrlPath: $boardId) {
    id
    teamDisplayName
    descriptionHtml
    pageTitle
  }
}`

const gemExternalJobPostingQuery = `
query ExternalJobPostingQuery($boardId: String!, $extId: String!) {
  oatsExternalJobPosting(boardId: $boardId, extId: $extId) {
    id
    title
    descriptionHtml
    extId
    startDateTs
    firstPublishedTsSec
    locations {
      id
      extId
      name
      city
      isoCountry
      isRemote
    }
    job {
      id
      locationType
      employmentType
      requisitionId
      teamDisplayName
      department {
        id
        extId
        name
      }
      locations {
        id
        extId
        name
        city
        isoCountry
        isRemote
      }
    }
    jobPostSectionHtml {
      introHtml
      outroHtml
    }
    compensationHtml
  }
}`

type gemGraphQLRequest struct {
	OperationName string            `json:"operationName"`
	Variables     map[string]string `json:"variables"`
	Query         string            `json:"query"`
}

type gemGraphQLError struct {
	Message string `json:"message"`
}

type gemBoardGraphQLResponse struct {
	Data   gemBoardGraphQLData `json:"data"`
	Errors []gemGraphQLError   `json:"errors"`
}

type gemBoardGraphQLData struct {
	Postings gemExternalJobPostings `json:"oatsExternalJobPostings"`
	Board    gemJobBoardExternal    `json:"jobBoardExternal"`
}

type gemExternalJobPostings struct {
	JobPostings []gemExternalJobPosting `json:"jobPostings"`
}

type gemDetailGraphQLResponse struct {
	Data   gemDetailGraphQLData `json:"data"`
	Errors []gemGraphQLError    `json:"errors"`
}

type gemDetailGraphQLData struct {
	Posting gemExternalJobPosting `json:"oatsExternalJobPosting"`
}

type gemJobBoardExternal struct {
	ID              string `json:"id"`
	TeamDisplayName string `json:"teamDisplayName"`
	DescriptionHTML string `json:"descriptionHtml"`
	PageTitle       string `json:"pageTitle"`
}

type gemExternalJobPosting struct {
	ID                  string            `json:"id"`
	ExtID               string            `json:"extId"`
	Title               string            `json:"title"`
	DescriptionHTML     string            `json:"descriptionHtml"`
	StartDateTs         int64             `json:"startDateTs"`
	FirstPublishedTsSec int64             `json:"firstPublishedTsSec"`
	Locations           []gemLocation     `json:"locations"`
	Job                 gemJob            `json:"job"`
	JobPostSectionHTML  gemJobSectionHTML `json:"jobPostSectionHtml"`
	CompensationHTML    string            `json:"compensationHtml"`
}

type gemJob struct {
	ID              string        `json:"id"`
	LocationType    string        `json:"locationType"`
	EmploymentType  string        `json:"employmentType"`
	RequisitionID   string        `json:"requisitionId"`
	TeamDisplayName string        `json:"teamDisplayName"`
	Department      gemDepartment `json:"department"`
	Locations       []gemLocation `json:"locations"`
}

type gemDepartment struct {
	ID    string `json:"id"`
	ExtID string `json:"extId"`
	Name  string `json:"name"`
}

type gemLocation struct {
	ID         string `json:"id"`
	ExtID      string `json:"extId"`
	Name       string `json:"name"`
	City       string `json:"city"`
	ISOCountry string `json:"isoCountry"`
	IsRemote   bool   `json:"isRemote"`
}

type gemJobSectionHTML struct {
	IntroHTML string `json:"introHtml"`
	OutroHTML string `json:"outroHtml"`
}

type hireologyStartingData struct {
	APIURL      string `json:"apiUrl"`
	AppURL      string `json:"appUrl"`
	APIToken    string `json:"apiToken"`
	CareersPath string `json:"careersPath"`
}

type hireologyJobsResponse struct {
	Count    int            `json:"count"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Data     []hireologyJob `json:"data"`
}

type hireologyJob struct {
	ID               json.RawMessage        `json:"id"`
	Name             string                 `json:"name"`
	CreatedAt        string                 `json:"created_at"`
	Status           string                 `json:"status"`
	EmploymentStatus string                 `json:"employment_status"`
	JobDescription   string                 `json:"job_description"`
	Locations        []hireologyLocation    `json:"locations"`
	Remote           bool                   `json:"remote"`
	JobFamily        hireologyNamedResource `json:"job_family"`
	CareerSiteURL    string                 `json:"career_site_url"`
	ApplicationPath  string                 `json:"application_path"`
	CareerSitePath   string                 `json:"career_site_path"`
	Organization     hireologyNamedResource `json:"organization"`
	SEODescription   string                 `json:"seo_description"`
	Compensation     hireologyCompensation  `json:"compensation"`
}

type hireologyNamedResource struct {
	ID   json.RawMessage `json:"id"`
	Name string          `json:"name"`
}

type hireologyLocation struct {
	City    string `json:"city"`
	State   string `json:"state"`
	ZipCode string `json:"zip_code"`
	Address string `json:"address"`
}

type hireologyCompensation struct {
	IsRange      bool   `json:"is_comp_range"`
	SingleAmount string `json:"comp_single_amount"`
	RangeMin     string `json:"comp_range_min"`
	RangeMax     string `json:"comp_range_max"`
	Period       string `json:"comp_period"`
	Frequency    string `json:"comp_frequency"`
}

func (r *jobsoidJobsResponse) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '[' {
		return json.Unmarshal(trimmed, &r.Items)
	}
	var wrapper struct {
		Jobs    []jobsoidJob `json:"jobs"`
		Data    []jobsoidJob `json:"data"`
		Results []jobsoidJob `json:"results"`
		Items   []jobsoidJob `json:"items"`
	}
	if err := json.Unmarshal(trimmed, &wrapper); err != nil {
		return err
	}
	r.Items = firstNonEmptyJobsoidJobs(wrapper.Jobs, wrapper.Data, wrapper.Results, wrapper.Items)
	return nil
}

func (r jobsoidJobsResponse) jobs() []jobsoidJob {
	return r.Items
}

type jobsoidJob struct {
	ID              json.RawMessage    `json:"id"`
	JobID           string             `json:"jobId"`
	Code            string             `json:"code"`
	Title           string             `json:"title"`
	JobTitle        string             `json:"jobTitle"`
	Description     string             `json:"description"`
	JobDescription  string             `json:"jobDescription"`
	Department      string             `json:"department"`
	DepartmentName  string             `json:"departmentName"`
	Location        string             `json:"location"`
	LocationName    string             `json:"locationName"`
	City            string             `json:"city"`
	State           string             `json:"state"`
	Country         string             `json:"country"`
	EmploymentType  string             `json:"employmentType"`
	JobType         string             `json:"jobType"`
	DatePosted      string             `json:"datePosted"`
	CreatedAt       string             `json:"createdAt"`
	PublishedAt     string             `json:"publishedAt"`
	JobURL          string             `json:"jobUrl"`
	ApplyURL        string             `json:"applyUrl"`
	ApplyURLSnake   string             `json:"apply_url"`
	URL             string             `json:"url"`
	CustomFields    map[string]string  `json:"customFields"`
	CustomFieldList []jobsoidNameValue `json:"custom_fields"`
}

type jobsoidNameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type applicantProJobsResponse struct {
	Success bool `json:"success"`
	Data    struct {
		JobCount int               `json:"jobCount"`
		Jobs     []applicantProJob `json:"jobs"`
	} `json:"data"`
}

type applicantProDetailResponse struct {
	Success bool                  `json:"success"`
	Data    applicantProJobDetail `json:"data"`
}

type applicantProJob struct {
	ID             json.RawMessage `json:"id"`
	Title          string          `json:"title"`
	JobLocation    string          `json:"jobLocation"`
	City           string          `json:"city"`
	StateName      string          `json:"stateName"`
	State          string          `json:"state"`
	ISO3           string          `json:"iso3"`
	Country        string          `json:"country"`
	OrgTitle       string          `json:"orgTitle"`
	Department     string          `json:"department"`
	Classification string          `json:"classification"`
	EmploymentType string          `json:"employmentType"`
	WorkplaceType  string          `json:"workplaceType"`
	PayType        string          `json:"payType"`
	PayDetails     string          `json:"payDetails"`
	MinSalary      string          `json:"minSalary"`
	MaxSalary      string          `json:"maxSalary"`
	JobURL         string          `json:"jobUrl"`
	URL            string          `json:"url"`
	StartDateRef   string          `json:"startDateRef"`
	EndDateRef     string          `json:"endDateRef"`
	DatePosted     string          `json:"datePosted"`
}

type applicantProJobDetail struct {
	ID                         json.RawMessage `json:"id"`
	Title                      string          `json:"title"`
	AdvertisingDescriptionHTML string          `json:"advertisingDescriptionHtml"`
	AdvertisingDescription     string          `json:"advertisingDescription"`
	Description                string          `json:"description"`
	Benefits                   string          `json:"benefits"`
	City                       string          `json:"city"`
	StateName                  string          `json:"stateName"`
	JobBoardZip                string          `json:"jobBoardZip"`
	PayDetails                 string          `json:"payDetails"`
	JobURL                     string          `json:"jobUrl"`
	URL                        string          `json:"url"`
}

type talentLyftJobsResponse struct {
	ResultsLower  []talentLyftJob `json:"results"`
	Results       []talentLyftJob `json:"Results"`
	Data          []talentLyftJob `json:"data"`
	Items         []talentLyftJob `json:"items"`
	CountLower    int             `json:"count"`
	Count         int             `json:"Count"`
	PageLower     int             `json:"page"`
	Page          int             `json:"Page"`
	PerPageLower  int             `json:"perPage"`
	PerPage       int             `json:"PerPage"`
	PagesLower    talentLyftPages `json:"pages"`
	Pages         talentLyftPages `json:"Pages"`
	OriginalCount int             `json:"OriginalCount"`
}

func (r talentLyftJobsResponse) results() []talentLyftJob {
	return firstNonEmptyTalentLyftJobs(r.Results, r.ResultsLower, r.Data, r.Items)
}

func (r talentLyftJobsResponse) hasNextPage(currentPage int) bool {
	pages := r.Pages
	if pages.empty() {
		pages = r.PagesLower
	}
	if firstNonEmptyString(pages.Next, pages.NextLower) != "" {
		return true
	}
	count := firstNonZeroInt(r.Count, r.CountLower, r.OriginalCount)
	perPage := firstNonZeroInt(r.PerPage, r.PerPageLower)
	page := firstNonZeroInt(r.Page, r.PageLower, currentPage)
	return count > 0 && perPage > 0 && page*perPage < count
}

type talentLyftPages struct {
	Next      string `json:"Next"`
	NextLower string `json:"next"`
}

func (p talentLyftPages) empty() bool {
	return firstNonEmptyString(p.Next, p.NextLower) == ""
}

type talentLyftJob struct {
	ID             json.RawMessage       `json:"id"`
	IDUpper        json.RawMessage       `json:"Id"`
	Title          string                `json:"title"`
	Name           string                `json:"name"`
	JobTitle       string                `json:"jobTitle"`
	Description    string                `json:"description"`
	JobDescription string                `json:"jobDescription"`
	DepartmentName string                `json:"departmentName"`
	Department     talentLyftNamedObject `json:"department"`
	LocationName   string                `json:"locationName"`
	LocationText   string                `json:"-"`
	Location       talentLyftLocation    `json:"-"`
	LocationData   talentLyftLocation    `json:"locationData"`
	LocationObject talentLyftLocation    `json:"location_object"`
	EmploymentType string                `json:"employmentType"`
	Type           string                `json:"type"`
	JobType        string                `json:"jobType"`
	PublishedAt    string                `json:"publishedAt"`
	DatePosted     string                `json:"datePosted"`
	CreatedAt      string                `json:"createdAt"`
	URL            string                `json:"url"`
	JobURL         string                `json:"jobUrl"`
	ApplyURL       string                `json:"applyUrl"`
	ApplyURLSnake  string                `json:"apply_url"`
	Country        string                `json:"country"`
	CustomFields   map[string]string     `json:"customFields"`
}

func (j *talentLyftJob) UnmarshalJSON(data []byte) error {
	type alias talentLyftJob
	var raw struct {
		*alias
		Location json.RawMessage `json:"location"`
	}
	raw.alias = (*alias)(j)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(raw.Location)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		return json.Unmarshal(trimmed, &j.LocationText)
	}
	return json.Unmarshal(trimmed, &j.Location)
}

type talentLyftNamedObject struct {
	Name string `json:"name"`
}

type talentLyftLocation struct {
	Name    string `json:"name"`
	City    string `json:"city"`
	State   string `json:"state"`
	Country string `json:"country"`
}

type phenomPeopleConfig struct {
	RefNum     string `json:"refNum"`
	Locale     string `json:"locale"`
	SiteType   string `json:"siteType"`
	BaseURL    string `json:"baseUrl"`
	BaseDomain string `json:"baseDomain"`
}

type phenomPeopleDDO struct {
	EagerLoadRefineSearch phenomPeopleDDOEntry `json:"eagerLoadRefineSearch"`
	RefineSearch          phenomPeopleDDOEntry `json:"refineSearch"`
}

func (ddo phenomPeopleDDO) refineData() phenomPeopleRefineData {
	eager := ddo.EagerLoadRefineSearch.refineData()
	if eager.TotalHits > 0 || len(eager.Hits) > 0 {
		return eager
	}
	return ddo.RefineSearch.refineData()
}

type phenomPeopleDDOEntry struct {
	Status    json.RawMessage        `json:"status"`
	Hits      int                    `json:"hits"`
	TotalHits int                    `json:"totalHits"`
	Data      phenomPeopleRefineData `json:"data"`
}

func (entry phenomPeopleDDOEntry) refineData() phenomPeopleRefineData {
	data := entry.Data
	if len(data.Hits) == 0 && len(data.Jobs) > 0 {
		data.Hits = data.Jobs
	}
	if data.TotalHits == 0 {
		data.TotalHits = entry.TotalHits
	}
	if data.TotalHits == 0 && entry.Hits > 0 {
		data.TotalHits = entry.Hits
	}
	return data
}

type phenomPeopleRefineData struct {
	TotalHits int               `json:"totalHits"`
	Hits      []phenomPeopleJob `json:"hits"`
	Jobs      []phenomPeopleJob `json:"jobs"`
}

type phenomPeopleJob struct {
	Title             string                  `json:"title"`
	JobSeqNo          string                  `json:"jobSeqNo"`
	JobID             string                  `json:"jobId"`
	ReqID             string                  `json:"reqId"`
	Type              string                  `json:"type"`
	Category          string                  `json:"category"`
	Department        string                  `json:"department"`
	DescriptionTeaser string                  `json:"descriptionTeaser"`
	State             string                  `json:"state"`
	City              string                  `json:"city"`
	Country           string                  `json:"country"`
	Location          string                  `json:"location"`
	MultiLocation     []string                `json:"multi_location"`
	PostedDate        string                  `json:"postedDate"`
	DateCreated       string                  `json:"dateCreated"`
	Locale            string                  `json:"locale"`
	ExternalApply     bool                    `json:"externalApply"`
	ApplyURL          string                  `json:"applyUrl"`
	MLJobParser       phenomPeopleMLJobParser `json:"ml_job_parser"`
}

type phenomPeopleMLJobParser struct {
	DescriptionTeaser        string `json:"descriptionTeaser"`
	DescriptionTeaserATS     string `json:"descriptionTeaser_ats"`
	DescriptionTeaserKeyword string `json:"descriptionTeaser_keyword"`
	DescriptionTeaserFirst   string `json:"descriptionTeaser_first200"`
}

type appleJobsHydration struct {
	LoaderData appleJobsLoaderData `json:"loaderData"`
}

type appleJobsLoaderData struct {
	Root   appleJobsRootData   `json:"root"`
	Search appleJobsSearchData `json:"search"`
}

type appleJobsRootData struct {
	Locale  string `json:"locale"`
	BaseURL string `json:"baseUrl"`
}

type appleJobsSearchData struct {
	SearchResults []appleJobsPosting `json:"searchResults"`
	TotalRecords  int                `json:"totalRecords"`
	RequestURL    string             `json:"requestUrl"`
	Search        string             `json:"search"`
	Page          int                `json:"page"`
}

type appleJobsPosting struct {
	ID                      string              `json:"id"`
	JobSummary              string              `json:"jobSummary"`
	Locations               []appleJobsLocation `json:"locations"`
	PositionID              string              `json:"positionId"`
	PostingDate             string              `json:"postingDate"`
	PostingTitle            string              `json:"postingTitle"`
	PostDateInGMT           string              `json:"postDateInGMT"`
	TransformedPostingTitle string              `json:"transformedPostingTitle"`
	ReqID                   string              `json:"reqId"`
	Team                    appleJobsTeam       `json:"team"`
	Type                    string              `json:"type"`
	HomeOffice              bool                `json:"homeOffice"`
	JobPositionID           string              `json:"jobPositionId"`
	PostExternal            bool                `json:"postExternal"`
	StandardWeeklyHours     float64             `json:"standardWeeklyHours"`
}

type appleJobsLocation struct {
	City          string `json:"city"`
	StateProvince string `json:"stateProvince"`
	CountryName   string `json:"countryName"`
	Metro         string `json:"metro"`
	Region        string `json:"region"`
	Name          string `json:"name"`
	CountryID     string `json:"countryID"`
	Level         int    `json:"level"`
}

type appleJobsTeam struct {
	TeamName string `json:"teamName"`
	TeamID   string `json:"teamID"`
	TeamCode string `json:"teamCode"`
}

type amazonJobsSearchRequest struct {
	JobPostingSearchRequest amazonJobsSearchParams `json:"jobPostingSearchRequest"`
}

type amazonJobsSearchParams struct {
	Query               string               `json:"query"`
	ExcludeFacets       []amazonJobsFacet    `json:"excludeFacets"`
	FilterFacets        []amazonJobsFacet    `json:"filterFacets"`
	Start               int                  `json:"start"`
	Size                int                  `json:"size"`
	Sort                amazonJobsSearchSort `json:"sort"`
	Location            any                  `json:"location"`
	AccessLevel         string               `json:"accessLevel"`
	IncludeFacets       []amazonJobsFacet    `json:"includeFacets"`
	LocationFacets      []amazonJobsFacet    `json:"locationFacets"`
	JobTypeFacets       []amazonJobsFacet    `json:"jobTypeFacets"`
	ContentFilterFacets []amazonJobsFacet    `json:"contentFilterFacets"`
	Treatment           string               `json:"treatment"`
}

type amazonJobsFacet struct {
	Name   string                 `json:"name"`
	Values []amazonJobsFacetValue `json:"values"`
}

type amazonJobsFacetValue struct {
	Name string `json:"name"`
}

type amazonJobsSearchSort struct {
	SortOrder string `json:"sortOrder"`
	SortType  string `json:"sortType"`
}

type amazonJobsSearchResponse struct {
	Found      int             `json:"found"`
	Start      int             `json:"start"`
	SearchHits []amazonJobsHit `json:"searchHits"`
}

type amazonJobsHit struct {
	Fields map[string][]string `json:"fields"`
}

type eightfoldPCSXConfig struct {
	APIBaseURL string
	Domain     string
	Query      string
	Location   string
	Locale     string
}

type eightfoldApplyConfig struct {
	APIBaseURL string
	Domain     string
	Query      string
	Location   string
}

type eightfoldPCSXSearchResponse struct {
	Status int                     `json:"status"`
	Error  eightfoldPCSXError      `json:"error"`
	Data   eightfoldPCSXSearchData `json:"data"`
}

type eightfoldPCSXSearchData struct {
	Positions []eightfoldPCSXPosition `json:"positions"`
	Count     int                     `json:"count"`
}

type eightfoldPCSXDetailResponse struct {
	Status int                   `json:"status"`
	Error  eightfoldPCSXError    `json:"error"`
	Data   eightfoldPCSXPosition `json:"data"`
}

type eightfoldPCSXError struct {
	Message string `json:"message"`
	Body    string `json:"body"`
}

type eightfoldPCSXPosition struct {
	ID                    int64    `json:"id"`
	DisplayJobID          string   `json:"displayJobId"`
	Name                  string   `json:"name"`
	Locations             []string `json:"locations"`
	StandardizedLocations []string `json:"standardizedLocations"`
	PostedTs              int64    `json:"postedTs"`
	CreationTs            int64    `json:"creationTs"`
	Department            string   `json:"department"`
	WorkLocationOption    string   `json:"workLocationOption"`
	LocationFlexibility   string   `json:"locationFlexibility"`
	ATSJobID              string   `json:"atsJobId"`
	PositionURL           string   `json:"positionUrl"`
	JobDescription        string   `json:"jobDescription"`
	EmploymentType        string   `json:"employmentType"`
	RoleType              string   `json:"roleType"`
}

type eightfoldApplySearchResponse struct {
	Positions []eightfoldApplyPosition `json:"positions"`
	Count     int                      `json:"count"`
}

type eightfoldApplyPosition struct {
	ID                   int64    `json:"id"`
	Name                 string   `json:"name"`
	PostingName          string   `json:"posting_name"`
	Location             string   `json:"location"`
	Locations            []string `json:"locations"`
	Department           string   `json:"department"`
	BusinessUnit         string   `json:"business_unit"`
	UpdatedAt            int64    `json:"t_update"`
	CreatedAt            int64    `json:"t_create"`
	ATSJobID             string   `json:"ats_job_id"`
	DisplayJobID         string   `json:"display_job_id"`
	Type                 string   `json:"type"`
	JobDescription       string   `json:"job_description"`
	CanonicalPositionURL string   `json:"canonicalPositionUrl"`
	WorkLocationOption   string   `json:"work_location_option"`
	LocationFlexibility  string   `json:"location_flexibility"`
}

type doverCareersPage struct {
	ID            string `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	PrimaryDomain string `json:"primary_domain"`
}

type doverJobsResponse struct {
	Count   int        `json:"count"`
	Next    string     `json:"next"`
	Results []doverJob `json:"results"`
}

type doverJob struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Locations   []doverLocation `json:"locations"`
	IsPublished bool            `json:"is_published"`
	IsSample    bool            `json:"is_sample"`
}

type doverLocation struct {
	LocationType   string              `json:"location_type"`
	Name           string              `json:"name"`
	LocationOption doverLocationOption `json:"location_option"`
}

type doverLocationOption struct {
	DisplayName  string `json:"display_name"`
	LocationType string `json:"location_type"`
	City         string `json:"city"`
	State        string `json:"state"`
	Country      string `json:"country"`
}

func atsKind(source Source) string {
	for _, key := range []string{"source_kind", "kind", "ats", "provider"} {
		value := strings.ToLower(strings.TrimSpace(source.Metadata[key]))
		if value == "" || isGenericATSKind(value) {
			// Generic tier labels (e.g. source_kind="ats") carry no provider
			// information; fall through to host-based detection instead of
			// failing with "unsupported ats source".
			continue
		}
		return value
	}
	return atsKindFromHost(source)
}

func isGenericATSKind(value string) bool {
	switch value {
	case "ats", "ats_adapter", "structured":
		return true
	}
	return false
}

func atsKindFromHost(source Source) string {
	host := sourceHost(source.URL)
	switch {
	case strings.Contains(host, "greenhouse.io"):
		return "greenhouse"
	case strings.Contains(host, "lever.co"):
		return "lever"
	case strings.Contains(host, "ashbyhq.com"):
		return "ashby"
	case strings.EqualFold(host, "jobs.workable.com"):
		return "workable_jobs"
	case strings.Contains(host, "workable.com"):
		return "workable"
	case strings.Contains(host, "recruitee.com"):
		return "recruitee"
	case strings.Contains(host, "smartrecruiters.com"):
		return "smartrecruiters"
	case strings.Contains(host, "comeet.co"), strings.Contains(host, "comeet.com"):
		return "comeet"
	case strings.Contains(host, "myworkdayjobs.com"), strings.Contains(host, "myworkdaysite.com"), strings.Contains(host, "workdayjobs.com"):
		return "workday"
	case strings.Contains(host, "breezy.hr"):
		return "breezy"
	case strings.Contains(host, "jobs.personio.de"), strings.Contains(host, "jobs.personio.com"):
		return "personio"
	case strings.Contains(host, "pinpointhq.com"):
		return "pinpoint"
	case strings.EqualFold(host, "jobs.polymer.co"), strings.EqualFold(host, "api.polymer.co"):
		return "polymer"
	case strings.Contains(host, "icims.com"):
		return "icims"
	case strings.Contains(host, "applytojob.com"):
		return "jazzhr"
	case strings.Contains(host, "jobs.gem.com"):
		return "gem"
	case strings.Contains(host, "jobvite.com"):
		return "jobvite"
	case strings.Contains(host, "teamtailor.com"):
		return "teamtailor"
	case strings.Contains(host, "bamboohr.com"):
		return "bamboohr"
	case strings.Contains(host, "jobs.rippling.com"):
		return "rippling_jobs"
	case strings.Contains(host, "ats.rippling.com"):
		return "rippling"
	case strings.Contains(host, "successfactors."), strings.Contains(host, "sapsf."), strings.Contains(host, "jobs2web.com"), strings.Contains(host, "jobs.sap.com"):
		return "successfactors"
	case strings.Contains(host, "workforcenow.adp.com"):
		return "adp_workforcenow"
	case strings.Contains(host, "myjobs.adp.com"):
		return "adp_myjobs"
	case ukgProHost(host):
		return "ukg"
	case strings.Contains(host, "jobs.dayforcehcm.com"):
		return "dayforce"
	case strings.Contains(host, "oraclecloud.com") && (strings.Contains(strings.ToLower(source.URL), "/hcmui/candidateexperience/") || strings.Contains(strings.ToLower(source.URL), "recruitingcejobrequisitions")):
		return "oracle_recruiting"
	case strings.Contains(host, "recruiting.paylocity.com"):
		return "paylocity"
	case strings.Contains(host, "paycomonline.net"):
		return "paycom"
	case strings.Contains(host, "avature.net"), strings.Contains(host, "avature.com"):
		return "avature"
	case strings.EqualFold(host, "feed.jobylon.com"):
		return "jobylon"
	case strings.Contains(host, "zohorecruit.com"), strings.Contains(host, "zohorecruit.eu"), strings.Contains(host, "zohorecruit.in"), strings.Contains(host, "zohorecruit.com.au"):
		return "zoho_recruit"
	case strings.Contains(host, "manatal.com"), strings.Contains(host, "careers-page.com"):
		return "manatal"
	case strings.Contains(host, "hire.trakstar.com"), strings.Contains(host, "recruiterbox.com"):
		return "recruiterbox"
	case strings.Contains(host, "join.com"):
		return "join_com"
	case strings.EqualFold(host, "api.occupop.com"), strings.EqualFold(host, "recruitment.cezannehr.com"), strings.HasSuffix(host, ".occupop.com") && !strings.EqualFold(host, "www.occupop.com"):
		return "occupop"
	case strings.Contains(host, "fountain.com"):
		return "fountain"
	case strings.Contains(host, "workstream.us"):
		return "workstream"
	case strings.EqualFold(host, "jobs.dover.com"), strings.EqualFold(host, "app.dover.com"):
		return "dover"
	case strings.Contains(host, "hireology.com"):
		return "hireology"
	case strings.Contains(host, "jobsoid.com"):
		return "jobsoid"
	case strings.Contains(host, "freshteam.com"):
		return "freshteam"
	case strings.Contains(host, "homerun.co"):
		return "homerun"
	case strings.Contains(host, "catsone.com"):
		return "catsone"
	case strings.HasSuffix(host, ".careers.hibob.com"), strings.EqualFold(host, "careers.hibob.com"):
		return "hibob_hiring"
	case strings.Contains(host, "applicantpro.com"), strings.Contains(host, "applicantprojobs.com"):
		return "applicantpro"
	case strings.Contains(host, "talentlyft.com"), strings.Contains(host, "talent-lyft.com"):
		return "talentlyft"
	case careerPlugStructuredHost(host):
		return "careerplug"
	case strings.Contains(host, "phenom.com"), strings.Contains(host, "phenompeople.com"):
		return "phenom_people"
	case strings.EqualFold(host, "jobs.apple.com"):
		return "apple_jobs"
	case strings.EqualFold(host, "amazon.jobs"), strings.EqualFold(host, "www.amazon.jobs"):
		return "amazon_jobs"
	case strings.EqualFold(host, "www.google.com"), strings.EqualFold(host, "google.com"):
		if strings.Contains(strings.ToLower(source.URL), "/about/careers/applications/jobs") {
			return "google_careers"
		}
		return ""
	case strings.EqualFold(host, "jobs.careers.microsoft.com"), strings.EqualFold(host, "apply.careers.microsoft.com"):
		return "eightfold_pcsx"
	case strings.Contains(host, "eightfold.ai"):
		return "eightfold_pcsx"
	case strings.EqualFold(host, "jobs.whatnot.com"):
		return "whatnot_careers"
	case strings.EqualFold(host, "careers.walmart.com"):
		return "walmart_careers"
	case strings.EqualFold(host, "www.worldquant.com"), strings.EqualFold(host, "worldquant.com"):
		if strings.Contains(strings.ToLower(source.URL), "/career-listing") {
			return "worldquant_careers"
		}
		return ""
	case strings.Contains(host, ".taleo.net"):
		return "taleo"
	default:
		return ""
	}
}

func greenhouseBoardToken(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	if token := strings.TrimSpace(parsed.Query().Get("for")); token != "" {
		return token, nil
	}
	// API-shaped URLs (boards-api.greenhouse.io/v1/boards/{token}/jobs) carry
	// the board token as the third path segment, not the first.
	if parts := nonEmptyPathParts(parsed); len(parts) >= 3 && parts[0] == "v1" && parts[1] == "boards" {
		return parts[2], nil
	}
	return firstPathSegment(parsed), nilIfEmpty(firstPathSegment(parsed), "greenhouse board token")
}

func leverSite(rawURL string) (string, bool, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", false, err
	}
	host := strings.ToLower(parsed.Hostname())
	site := firstPathSegment(parsed)
	if strings.Contains(parsed.Path, "/v0/postings/") {
		parts := nonEmptyPathParts(parsed)
		if len(parts) >= 3 {
			site = parts[2]
		}
	}
	if site == "" {
		return "", false, errors.New("lever site slug is required")
	}
	return site, strings.Contains(host, "api.eu.") || strings.Contains(host, "jobs.eu."), nil
}

func ashbyBoardName(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	parts := nonEmptyPathParts(parsed)
	if len(parts) >= 3 && parts[0] == "posting-api" && parts[1] == "job-board" {
		return parts[2], nil
	}
	return firstPathSegment(parsed), nilIfEmpty(firstPathSegment(parsed), "ashby job board name")
}

func workableAccountSlug(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(parsed.Hostname())
	parts := nonEmptyPathParts(parsed)
	if (host == "www.workable.com" || host == "workable.com") && len(parts) >= 3 && parts[0] == "api" && parts[1] == "accounts" {
		return parts[2], nil
	}
	if host == "apply.workable.com" && len(parts) > 0 && parts[0] != "j" {
		return parts[0], nil
	}
	if strings.HasSuffix(host, ".workable.com") {
		subdomain := strings.TrimSuffix(host, ".workable.com")
		if subdomain != "" && subdomain != "apply" && subdomain != "www" {
			return subdomain, nil
		}
	}
	return "", errors.New("workable account slug is required")
}

func recruiteeCompanySlug(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.HasSuffix(host, ".recruitee.com") {
		subdomain := strings.TrimSuffix(host, ".recruitee.com")
		if subdomain != "" && subdomain != "www" {
			return subdomain, nil
		}
	}
	return "", errors.New("recruitee company slug is required")
}

func recruiteeOffersEndpoint(baseURL string, companySlug string) (*url.URL, error) {
	if strings.Contains(baseURL, "%s") {
		baseURL = fmt.Sprintf(baseURL, companySlug)
	}
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid recruitee base url %q", baseURL)
	}
	return parsed, nil
}

func smartRecruitersCompanyIdentifier(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		if strings.EqualFold(part, "companies") && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}
	if len(parts) > 0 {
		return parts[0], nil
	}
	return "", errors.New("smartrecruiters company identifier is required")
}

func mergeSmartRecruitersPosting(summary smartRecruitersPosting, detail smartRecruitersPosting) smartRecruitersPosting {
	out := detail
	if out.ID == "" {
		out.ID = summary.ID
	}
	if out.UUID == "" {
		out.UUID = summary.UUID
	}
	if out.Name == "" {
		out.Name = summary.Name
	}
	if out.ReleasedDate == "" {
		out.ReleasedDate = summary.ReleasedDate
	}
	if out.PostedDate == "" {
		out.PostedDate = summary.PostedDate
	}
	if out.ApplyURL == "" {
		out.ApplyURL = summary.ApplyURL
	}
	if out.JobAdURL == "" {
		out.JobAdURL = summary.JobAdURL
	}
	if out.Ref == "" {
		out.Ref = summary.Ref
	}
	if out.Company.Name == "" && out.Company.Identifier == "" {
		out.Company = summary.Company
	}
	if out.Location == (smartRecruitersLocation{}) {
		out.Location = summary.Location
	}
	if out.Department.Label == "" {
		out.Department = summary.Department
	}
	if out.Function.Label == "" {
		out.Function = summary.Function
	}
	if out.TypeOfEmployment.Label == "" {
		out.TypeOfEmployment = summary.TypeOfEmployment
	}
	if out.ExperienceLevel.Label == "" {
		out.ExperienceLevel = summary.ExperienceLevel
	}
	if smartRecruitersDescription(out.JobAd) == "" {
		out.JobAd = summary.JobAd
	}
	return out
}

func smartRecruitersLocationText(loc smartRecruitersLocation) (string, string) {
	country := canonicalCountry(firstNonEmptyString(loc.CountryCode, loc.Country))
	location := strings.Join(compactStringList(loc.City, loc.Region, country), ", ")
	if loc.Remote {
		if location == "" {
			return "Remote", country
		}
		return location + " or Remote", country
	}
	return location, country
}

func smartRecruitersApplyURL(companyID string, posting smartRecruitersPosting) string {
	if applyURL := firstNonEmptyString(posting.ApplyURL, posting.JobAdURL); applyURL != "" {
		return applyURL
	}
	jobToken := firstNonEmptyString(posting.ID, posting.UUID, stableJobToken(posting.Ref, posting.Name))
	if jobToken == "" {
		return ""
	}
	return "https://jobs.smartrecruiters.com/" + companyID + "/" + jobToken
}

func smartRecruitersDescription(ad smartRecruitersJobAd) string {
	parts := compactStringList(
		smartRecruitersRawText(ad.CompanyDescription),
		smartRecruitersRawText(ad.JobDescription),
		smartRecruitersRawText(ad.Qualifications),
		smartRecruitersRawText(ad.AdditionalInformation),
	)
	for _, key := range []string{"companyDescription", "jobDescription", "qualifications", "additionalInformation"} {
		if section, ok := ad.Sections[key]; ok {
			parts = append(parts, compactStringList(section.Text)...)
		}
	}
	return cleanHTMLText(strings.Join(parts, " "))
}

func smartRecruitersRawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var section smartRecruitersSection
	if err := json.Unmarshal(raw, &section); err == nil {
		return section.Text
	}
	return ""
}

func janeStreetPosting(source Source, baseURL string, item janeStreetJob) (JobPosting, bool) {
	title := normalizeSpace(item.Position)
	if item.ID == 0 || title == "" {
		return JobPosting{}, false
	}
	applyURL := janeStreetJobURL(baseURL, item.ID)
	description := cleanHTMLText(html.UnescapeString(item.Overview))
	location, country := janeStreetLocation(item.City)
	context := strings.Join(compactStringList(title, description, item.Category, item.Team, item.Availability, item.Duration), " ")
	timingContext := strings.Join(compactStringList(title, item.Availability, item.Duration), " ")
	evidence := []Evidence{
		{Field: "ats", Text: "Jane Street public jobs JSON", URL: source.URL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if item.Category != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: item.Category, URL: applyURL})
	}
	if item.Team != "" {
		evidence = append(evidence, Evidence{Field: "team", Text: item.Team, URL: applyURL})
	}
	if item.Duration != "" || item.Availability != "" {
		evidence = append(evidence, Evidence{Field: "employment_type", Text: strings.Join(compactStringList(item.Availability, item.Duration), " / "), URL: applyURL})
	}
	return JobPosting{
		SourceJobID:    "janestreet_careers:" + strconv.FormatInt(item.ID, 10),
		Company:        sourceCompany(source, "Jane Street"),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, item.Availability+" "+item.Duration),
		Level:          inferLevel(timingContext),
		RoleFamily:     inferRoleFamily(context),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		Live:           true,
		Confidence:     0.87,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func janeStreetJobURL(baseURL string, id int64) string {
	endpoint, err := joinURL(baseURL, "join-jane-street", "position", strconv.FormatInt(id, 10))
	if err != nil {
		return strings.TrimRight(baseURL, "/") + "/join-jane-street/position/" + strconv.FormatInt(id, 10) + "/"
	}
	return endpoint.String()
}

func janeStreetLocation(code string) (string, string) {
	parts := strings.Split(strings.TrimSpace(code), "/")
	locations := make([]string, 0, len(parts))
	country := ""
	for _, part := range parts {
		location, locCountry := janeStreetLocationCode(part)
		if location == "" {
			location = normalizeSpace(part)
		}
		if location != "" {
			locations = append(locations, location)
		}
		if country == "" {
			country = locCountry
		}
	}
	location := strings.Join(compactStringList(locations...), "; ")
	if country == "" {
		country = normalizeCountry("", location)
	}
	return location, country
}

func janeStreetLocationCode(code string) (string, string) {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "NYC":
		return "New York, United States", "United States"
	case "LDN":
		return "London, United Kingdom", "United Kingdom"
	case "HKG":
		return "Hong Kong", "Hong Kong"
	case "AMS":
		return "Amsterdam, Netherlands", "Netherlands"
	case "CHI":
		return "Chicago, United States", "United States"
	case "SGP":
		return "Singapore", "Singapore"
	case "MUM":
		return "Mumbai, India", "India"
	case "SHA":
		return "Shanghai, China", "China"
	case "PHL":
		return "Philadelphia, United States", "United States"
	case "SF":
		return "San Francisco, United States", "United States"
	case "ATX":
		return "Austin, United States", "United States"
	case "ALL LOCATIONS":
		return "All Locations", ""
	default:
		return "", ""
	}
}

func akunaJobsFeedURL(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".json") {
		return parsed.String(), nil
	}
	parsed.Path = "/wp-content/uploads/akuna_jobs.json"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func akunaPosting(source Source, endpoint string, item akunaJob) (JobPosting, bool) {
	title := normalizeSpace(item.Title)
	if item.ID == 0 || title == "" || strings.Contains(strings.ToLower(title), "talent community") {
		return JobPosting{}, false
	}
	applyURL := firstNonEmptyString(strings.TrimSpace(item.AbsoluteURL), akunaJobURL(source.URL, item.ID))
	description := cleanHTMLText(html.UnescapeString(item.Content))
	location := firstNonEmptyString(item.LocationRaw, item.Location)
	country := normalizeCountry("", location)
	department := strings.Join(compactStringList(item.Departments...), ", ")
	specialties := strings.Join(compactStringList(item.Specialties...), ", ")
	context := strings.Join(compactStringList(title, description, department, specialties, item.Experience), " ")
	timingContext := strings.Join(compactStringList(title, item.Experience), " ")
	evidence := []Evidence{
		{Field: "ats", Text: "Akuna public jobs JSON", URL: endpoint},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: applyURL})
	}
	if item.Experience != "" {
		evidence = append(evidence, Evidence{Field: "experience", Text: item.Experience, URL: applyURL})
	}
	return JobPosting{
		SourceJobID:    "akuna_careers:" + strconv.FormatInt(item.ID, 10),
		Company:        sourceCompany(source, "Akuna Capital"),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, item.Experience),
		Level:          inferLevel(timingContext),
		RoleFamily:     inferRoleFamily(context),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(item.UpdatedAt),
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func akunaJobURL(rawURL string, id int64) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return rawURL
	}
	parsed.Path = path.Join("/", "careers", "job", strconv.FormatInt(id, 10)) + "/"
	q := url.Values{}
	q.Set("gh_jid", strconv.FormatInt(id, 10))
	parsed.RawQuery = q.Encode()
	parsed.Fragment = ""
	return parsed.String()
}

func comeetConfigFromSource(source Source) (comeetConfig, error) {
	companyUID := firstNonEmptyString(source.Metadata["company_uid"], source.Metadata["comeet_uid"], source.Metadata["uid"])
	token := firstNonEmptyString(source.Metadata["token"], source.Metadata["comeet_token"])

	parsed, err := parseSourceURL(source.URL)
	if err == nil {
		parts := nonEmptyPathParts(parsed)
		for i, part := range parts {
			if strings.EqualFold(part, "company") && i+1 < len(parts) {
				companyUID = firstNonEmptyString(companyUID, parts[i+1])
				break
			}
		}
		token = firstNonEmptyString(token, parsed.Query().Get("token"))
	}
	if companyUID == "" {
		return comeetConfig{}, errors.New("comeet company uid is required")
	}
	if token == "" {
		return comeetConfig{}, errors.New("comeet careers api token is required")
	}
	return comeetConfig{CompanyUID: companyUID, Token: token}, nil
}

func comeetHostedPage(rawURL string) bool {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if !strings.Contains(host, "comeet.co") && !strings.Contains(host, "comeet.com") {
		return true
	}
	return !strings.Contains(strings.ToLower(parsed.Path), "/careers-api/")
}

func comeetLocationText(primary comeetLocation, secondary []comeetLocation) (string, string) {
	locations := make([]string, 0, 1+len(secondary))
	country := ""
	remote := false
	for _, loc := range append([]comeetLocation{primary}, secondary...) {
		locCountry := canonicalCountry(loc.Country)
		if country == "" {
			country = locCountry
		}
		locationText := firstNonEmptyString(loc.Name, strings.Join(compactStringList(loc.City, loc.State, locCountry), ", "))
		if locationText != "" {
			locations = append(locations, locationText)
		}
		remote = remote || loc.IsRemote
	}

	location := strings.Join(compactStringList(locations...), "; ")
	if remote && !strings.Contains(strings.ToLower(location), "remote") {
		if location == "" {
			return "Remote", country
		}
		return location + " or Remote", country
	}
	return location, country
}

func comeetDescription(details []comeetDetail) string {
	parts := make([]string, 0, len(details))
	for _, detail := range details {
		parts = append(parts, compactStringList(detail.Value)...)
	}
	return cleanHTMLText(strings.Join(parts, " "))
}

func workdayConfigFromSource(source Source) (workdayConfig, error) {
	tenant := firstNonEmptyString(source.Metadata["workday_tenant"], source.Metadata["tenant"])
	site := firstNonEmptyString(source.Metadata["workday_site"], source.Metadata["site"])
	baseURL := strings.TrimRight(firstNonEmptyString(source.Metadata["workday_base_url"], source.Metadata["base_url"]), "/")
	publicPrefix := strings.Trim(strings.TrimSpace(source.Metadata["workday_public_path_prefix"]), "/")

	parsed, err := parseSourceURL(source.URL)
	if err != nil {
		return workdayConfig{}, err
	}
	if baseURL == "" {
		baseURL = parsed.Scheme + "://" + parsed.Host
	}
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		switch strings.ToLower(part) {
		case "cxs":
			if i+2 < len(parts) {
				tenant = firstNonEmptyString(tenant, parts[i+1])
				site = firstNonEmptyString(site, parts[i+2])
				publicPrefix = firstNonEmptyString(publicPrefix, site)
			}
		case "recruiting":
			if i+2 < len(parts) {
				tenant = firstNonEmptyString(tenant, parts[i+1])
				site = firstNonEmptyString(site, parts[i+2])
				publicPrefix = firstNonEmptyString(publicPrefix, path.Join("recruiting", parts[i+1], parts[i+2]))
			}
		}
	}

	host := strings.ToLower(parsed.Hostname())
	if tenant == "" && (strings.Contains(host, "myworkdayjobs.com") || strings.Contains(host, "workdayjobs.com")) {
		tenant = strings.Split(host, ".")[0]
	}
	if site == "" {
		site = workdaySiteFromPath(parts)
	}
	if publicPrefix == "" && site != "" {
		publicPrefix = workdayPublicPathPrefix(parts, site)
	}
	if tenant == "" {
		return workdayConfig{}, errors.New("workday tenant is required")
	}
	if site == "" {
		return workdayConfig{}, errors.New("workday site is required")
	}
	return workdayConfig{
		BaseURL:          baseURL,
		Tenant:           tenant,
		Site:             site,
		PublicPathPrefix: publicPrefix,
	}, nil
}

func workdaySiteFromPath(parts []string) string {
	for _, part := range parts {
		lower := strings.ToLower(strings.TrimSpace(part))
		if lower == "" || lower == "job" || lower == "jobs" && len(parts) > 1 && strings.EqualFold(parts[0], "wday") {
			continue
		}
		if lower == "en" || isWorkdayLocale(lower) || lower == "recruiting" || lower == "wday" || lower == "cxs" {
			continue
		}
		return part
	}
	return ""
}

func workdayPublicPathPrefix(parts []string, site string) string {
	for i, part := range parts {
		if strings.EqualFold(part, site) {
			return path.Join(parts[:i+1]...)
		}
	}
	return site
}

func isWorkdayLocale(value string) bool {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(value)), "-")
	if len(parts) < 1 || len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if len(part) != 2 {
			return false
		}
		for _, r := range part {
			if r < 'a' || r > 'z' {
				return false
			}
		}
	}
	return true
}

func workdayHostedURL(config workdayConfig, externalPath string) string {
	if strings.TrimSpace(externalPath) == "" {
		return ""
	}
	hosted, err := joinURL(config.BaseURL, config.PublicPathPrefix, strings.TrimLeft(externalPath, "/"))
	if err != nil {
		return ""
	}
	return hosted.String()
}

func workdayCountry(value string) string {
	value = normalizeSpace(value)
	if value == "" {
		return ""
	}
	if country := canonicalCountry(value); country != value {
		return country
	}
	if strings.Contains(value, ",") {
		first := strings.TrimSpace(strings.Split(value, ",")[0])
		if country := canonicalCountry(first); country != first || len(first) == 2 {
			return country
		}
	}
	return canonicalCountry(value)
}

func workdayRawText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil {
		for _, key := range []string{"descriptor", "name", "location", "value"} {
			if value, ok := object[key]; ok {
				if text := workdayRawText(value); text != "" {
					return text
				}
			}
		}
		if country, ok := object["country"]; ok {
			return workdayRawText(country)
		}
	}
	return ""
}

func breezyBoardURL(rawURL string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	parsed.Path = "/json"
	parsed.RawQuery = "verbose=true"
	parsed.Fragment = ""
	return parsed, nil
}

func breezyCompanySlug(rawURL string) string {
	host := sourceHost(rawURL)
	if strings.HasSuffix(host, ".breezy.hr") {
		return strings.TrimSuffix(host, ".breezy.hr")
	}
	return ""
}

func breezyHostedURL(rawURL string, friendlyID string) string {
	if strings.TrimSpace(friendlyID) == "" {
		return ""
	}
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parsed.Path = path.Join("p", strings.TrimSpace(friendlyID))
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func breezyLocationText(primary breezyLocation, secondary []breezyLocation) (string, string) {
	locations := make([]string, 0, 1+len(secondary))
	country := ""
	remote := false
	for _, loc := range append([]breezyLocation{primary}, secondary...) {
		locCountry := canonicalCountry(firstNonEmptyString(loc.Country.ID, loc.Country.Name))
		if country == "" {
			country = locCountry
		}
		locationText := firstNonEmptyString(loc.Name, strings.Join(compactStringList(loc.City, locCountry), ", "))
		if locationText != "" {
			locations = append(locations, locationText)
		}
		remote = remote || loc.IsRemote
	}

	location := strings.Join(compactStringList(locations...), "; ")
	if remote && !strings.Contains(strings.ToLower(location), "remote") {
		if location == "" {
			return "Remote", country
		}
		return location + " or Remote", country
	}
	return location, country
}

func personioFeedURL(rawURL string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	language := firstNonEmptyString(parsed.Query().Get("language"), "en")
	parsed.Path = "/xml"
	q := url.Values{}
	q.Set("language", language)
	parsed.RawQuery = q.Encode()
	parsed.Fragment = ""
	return parsed, nil
}

func personioApplyURL(feedURL *url.URL, jobID string) string {
	if feedURL == nil || strings.TrimSpace(jobID) == "" {
		return ""
	}
	apply := *feedURL
	apply.Path = path.Join("job", strings.TrimSpace(jobID))
	q := url.Values{}
	q.Set("language", firstNonEmptyString(feedURL.Query().Get("language"), "en"))
	apply.RawQuery = q.Encode()
	apply.Fragment = ""
	return apply.String()
}

func personioDescription(descriptions []personioJobDescription) string {
	parts := make([]string, 0, len(descriptions))
	for _, description := range descriptions {
		parts = append(parts, compactStringList(description.Value)...)
	}
	return cleanHTMLText(strings.Join(parts, " "))
}

func personioLocation(position personioPosition) string {
	locations := compactStringList(position.Office)
	locations = append(locations, compactStringList(position.AdditionalOffices...)...)
	return strings.Join(compactStringList(locations...), "; ")
}

func personioEmployment(position personioPosition) string {
	lower := strings.ToLower(strings.Join(compactStringList(position.Name, position.EmploymentType, position.Schedule), " "))
	switch {
	case strings.Contains(lower, "intern"):
		return "internship"
	case strings.Contains(lower, "part-time"), strings.Contains(lower, "part time"):
		return "part_time"
	case strings.Contains(lower, "full-time"), strings.Contains(lower, "full time"):
		return "full_time"
	case strings.Contains(lower, "contract"):
		return "contract"
	case strings.Contains(lower, "permanent"):
		return "full_time"
	default:
		return employmentFromText(position.Name, firstNonEmptyString(position.Schedule, position.EmploymentType))
	}
}

func pinpointPostingsURL(rawURL string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	parsed.Path = "/postings.json"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func pinpointCompanySlug(rawURL string) string {
	host := sourceHost(rawURL)
	if strings.HasSuffix(host, ".pinpointhq.com") {
		return strings.TrimSuffix(host, ".pinpointhq.com")
	}
	return ""
}

func pinpointLocationText(location pinpointLocation) string {
	return strings.Join(compactStringList(location.Name, location.City, location.Province), ", ")
}

func pinpointDescription(posting pinpointPosting) string {
	return cleanHTMLText(strings.Join(compactStringList(
		posting.Description,
		posting.KeyResponsibilities,
		posting.SkillsKnowledgeExpertise,
		posting.Benefits,
	), " "))
}

func polymerOrganizationSlug(rawURL string, metadata map[string]string) (string, error) {
	for _, key := range []string{"organization_slug", "org_slug", "company_slug", "polymer_slug"} {
		if metadata != nil {
			if value := strings.TrimSpace(metadata[key]); value != "" {
				return value, nil
			}
		}
	}

	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(parsed.Hostname())
	parts := nonEmptyPathParts(parsed)
	switch host {
	case "jobs.polymer.co":
		if len(parts) > 0 {
			return parts[0], nil
		}
	case "api.polymer.co":
		for i := 0; i+1 < len(parts); i++ {
			if parts[i] == "organizations" {
				return parts[i+1], nil
			}
		}
	}
	return "", fmt.Errorf("polymer organization slug is required: %s", rawURL)
}

func polymerJobsURL(baseURL string, organizationSlug string, page int) (*url.URL, error) {
	if err := nilIfEmpty(organizationSlug, "polymer organization slug"); err != nil {
		return nil, err
	}
	endpoint, err := joinURL(baseURL, organizationSlug, "jobs")
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("page", strconv.Itoa(page))
	endpoint.RawQuery = query.Encode()
	return endpoint, nil
}

func polymerJobPosting(source Source, organizationSlug string, posting polymerJob, listURL string, detailURL string) JobPosting {
	description := cleanHTMLText(posting.Description)
	location := firstNonEmptyString(posting.DisplayLocation, strings.Join(compactStringList(posting.City, posting.StateRegion, canonicalCountry(posting.Country)), ", "))
	applyURL := firstNonEmptyString(posting.JobPostURL, posting.JobApplicationDescriptionURL)
	sourceJobID := firstNonEmptyString(polymerJobID(posting), stableJobToken(applyURL, posting.Title))
	evidence := []Evidence{
		{Field: "ats", Text: "Polymer public jobs API", URL: listURL},
		{Field: "description", Text: description, URL: firstNonEmptyString(detailURL, applyURL)},
		{Field: "location", Text: location, URL: applyURL},
		{Field: "remoteness", Text: posting.RemotenessPretty, URL: applyURL},
		{Field: "category", Text: firstNonEmptyString(posting.JobCategoryName, posting.Department), URL: applyURL},
	}
	if posting.SalaryPretty != "" {
		evidence = append(evidence, Evidence{Field: "compensation", Text: posting.SalaryPretty, URL: applyURL})
	}
	if restriction := polymerRemoteRestrictionText(posting); restriction != "" {
		evidence = append(evidence, Evidence{Field: "remote_restriction", Text: restriction, URL: applyURL})
	}
	return JobPosting{
		SourceJobID:    "polymer:" + sourceJobID,
		Company:        sourceCompany(source, firstNonEmptyString(posting.OrganizationName, organizationSlug)),
		Title:          posting.Title,
		Location:       location,
		Country:        canonicalCountry(posting.Country),
		EmploymentType: employmentFromText(posting.Title, firstNonEmptyString(posting.KindPretty, posting.Kind)),
		RoleFamily:     inferRoleFamily(posting.Title + " " + description + " " + posting.JobCategoryName + " " + posting.Department),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(firstNonEmptyString(posting.PublishedAt, posting.CreatedAt)),
		Live:           true,
		Confidence:     0.88,
		Strategy:       TierATS,
		Evidence:       evidence,
	}
}

func mergePolymerJob(listing polymerJob, detail polymerJob) polymerJob {
	if detail.ID == 0 {
		detail.ID = listing.ID
	}
	if detail.JobID == 0 {
		detail.JobID = listing.JobID
	}
	if detail.Title == "" {
		detail.Title = listing.Title
	}
	if detail.Country == "" {
		detail.Country = listing.Country
	}
	if detail.StateRegion == "" {
		detail.StateRegion = listing.StateRegion
	}
	if detail.City == "" {
		detail.City = listing.City
	}
	if detail.DisplayLocation == "" {
		detail.DisplayLocation = listing.DisplayLocation
	}
	if detail.OrganizationName == "" {
		detail.OrganizationName = listing.OrganizationName
	}
	if detail.Kind == "" {
		detail.Kind = listing.Kind
	}
	if detail.KindPretty == "" {
		detail.KindPretty = listing.KindPretty
	}
	if detail.PublishedAt == "" {
		detail.PublishedAt = listing.PublishedAt
	}
	if detail.CreatedAt == "" {
		detail.CreatedAt = listing.CreatedAt
	}
	if detail.UpdatedAt == "" {
		detail.UpdatedAt = listing.UpdatedAt
	}
	if detail.JobPostURL == "" {
		detail.JobPostURL = listing.JobPostURL
	}
	if detail.JobApplicationDescriptionURL == "" {
		detail.JobApplicationDescriptionURL = listing.JobApplicationDescriptionURL
	}
	if detail.RemotenessPretty == "" {
		detail.RemotenessPretty = listing.RemotenessPretty
	}
	if detail.JobCategoryName == "" {
		detail.JobCategoryName = listing.JobCategoryName
	}
	if detail.Department == "" {
		detail.Department = listing.Department
	}
	if detail.SalaryPretty == "" {
		detail.SalaryPretty = listing.SalaryPretty
	}
	if len(detail.RemoteRestrictionCountryList) == 0 {
		detail.RemoteRestrictionCountryList = listing.RemoteRestrictionCountryList
	}
	if !detail.RemoteRestrictionCountryResidencyIsRequired {
		detail.RemoteRestrictionCountryResidencyIsRequired = listing.RemoteRestrictionCountryResidencyIsRequired
	}
	if detail.RemoteRestrictionCity == "" {
		detail.RemoteRestrictionCity = listing.RemoteRestrictionCity
	}
	if detail.RemoteRestrictionOverlapHours == 0 {
		detail.RemoteRestrictionOverlapHours = listing.RemoteRestrictionOverlapHours
	}
	if !detail.RemoteRestrictionOverlapHoursIsRequired {
		detail.RemoteRestrictionOverlapHoursIsRequired = listing.RemoteRestrictionOverlapHoursIsRequired
	}
	if detail.RemoteRestrictionTimezoneUTCOffsetSeconds == 0 {
		detail.RemoteRestrictionTimezoneUTCOffsetSeconds = listing.RemoteRestrictionTimezoneUTCOffsetSeconds
	}
	if detail.RemoteRestrictionCityGooglePlaceID == "" {
		detail.RemoteRestrictionCityGooglePlaceID = listing.RemoteRestrictionCityGooglePlaceID
	}
	return detail
}

func polymerJobID(posting polymerJob) string {
	if id := firstNonZeroInt64(posting.JobID, posting.ID); id > 0 {
		return strconv.FormatInt(id, 10)
	}
	return posting.HashID
}

func polymerRemoteRestrictionText(posting polymerJob) string {
	parts := compactStringList(posting.RemoteRestrictionCity)
	countries := make([]string, 0, len(posting.RemoteRestrictionCountryList))
	for _, country := range posting.RemoteRestrictionCountryList {
		countries = append(countries, canonicalCountry(country))
	}
	if len(countries) > 0 {
		label := "countries: " + strings.Join(compactStringList(countries...), ", ")
		if posting.RemoteRestrictionCountryResidencyIsRequired {
			label += " residency required"
		}
		parts = append(parts, label)
	}
	if posting.RemoteRestrictionOverlapHoursIsRequired && posting.RemoteRestrictionOverlapHours > 0 {
		parts = append(parts, fmt.Sprintf("%d hours overlap required", posting.RemoteRestrictionOverlapHours))
	}
	return strings.Join(parts, "; ")
}

func icimsSitemapURL(rawURL string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	parsed.Path = "/sitemap.xml"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func icimsSitemapJobs(sitemap icimsSitemap) []icimsSitemapEntry {
	entries := make([]icimsSitemapEntry, 0, len(sitemap.URLs))
	seen := map[string]struct{}{}
	for _, entry := range sitemap.URLs {
		loc := strings.TrimSpace(entry.Loc)
		if loc == "" || icimsJobIDFromURL(loc) == "" {
			continue
		}
		key := strings.ToLower(loc)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		entry.Loc = loc
		entries = append(entries, entry)
	}
	return entries
}

func icimsDetailURL(rawURL string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	query := parsed.Query()
	query.Set("in_iframe", "1")
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed, nil
}

func normalizeICIMSJob(source Source, entry icimsSitemapEntry, detailURL string, document string, job JobPosting) JobPosting {
	jobID := firstNonEmptyString(icimsJobIDFromURL(entry.Loc), icimsJobIDFromURL(job.ApplyURL), stableJobToken(entry.Loc, job.Title))
	job.SourceJobID = "icims:" + jobID
	job.SourceURL = source.URL
	job.ApplyURL = firstNonEmptyString(icimsApplyURL(document, detailURL), job.ApplyURL, entry.Loc)
	job.Strategy = TierATS
	job.Confidence = 0.86
	if job.PostedAt == nil {
		job.PostedAt = parseTimePtr(entry.LastMod)
	}
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "iCIMS sitemap and JobPosting detail page", URL: detailURL})
	return job
}

func icimsJobIDFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], "jobs") && numericString(parts[i+1]) {
			return parts[i+1]
		}
	}
	return ""
}

func icimsApplyURL(document string, detailURL string) string {
	for _, match := range hrefAttrPattern.FindAllStringSubmatch(document, -1) {
		if len(match) < 2 {
			continue
		}
		href := html.UnescapeString(strings.TrimSpace(match[1]))
		if strings.Contains(strings.ToLower(href), "mode=apply") {
			baseURL, err := parseSourceURL(detailURL)
			if err != nil {
				return href
			}
			return resolveStaticURL(baseURL, href)
		}
	}
	return ""
}

func jazzHRBoardURL(rawURL string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	parsed.Path = "/apply/jobs/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func jazzHRJobLinks(baseURL *url.URL, document string) []jazzHRJobLink {
	links := make([]jazzHRJobLink, 0)
	seen := map[string]struct{}{}
	for _, anchor := range anchorTagPattern.FindAllString(document, -1) {
		lower := strings.ToLower(anchor)
		if !strings.Contains(lower, "/apply/jobs/details/") && !strings.Contains(lower, "job_title_link") {
			continue
		}
		href := anchorHref(anchor)
		if !strings.Contains(strings.ToLower(href), "/apply/jobs/details/") {
			continue
		}
		detailURL := normalizeJazzHRDetailURL(resolveStaticURL(baseURL, href))
		if detailURL == "" {
			continue
		}
		key := strings.ToLower(detailURL)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		links = append(links, jazzHRJobLink{URL: detailURL})
	}
	return links
}

func anchorHref(anchor string) string {
	match := hrefAttrPattern.FindStringSubmatch(anchor)
	if len(match) < 2 {
		return ""
	}
	return html.UnescapeString(strings.TrimSpace(match[1]))
}

func jazzHRDirectJobLink(rawURL string) string {
	if jazzHRJobToken(rawURL) == "" {
		return ""
	}
	return normalizeJazzHRDetailURL(rawURL)
}

func normalizeJazzHRDetailURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func prependUniqueJazzHRLink(links []jazzHRJobLink, direct jazzHRJobLink) []jazzHRJobLink {
	if strings.TrimSpace(direct.URL) == "" {
		return links
	}
	for _, link := range links {
		if strings.EqualFold(link.URL, direct.URL) {
			return links
		}
	}
	out := make([]jazzHRJobLink, 0, len(links)+1)
	out = append(out, direct)
	out = append(out, links...)
	return out
}

func normalizeJazzHRJob(source Source, link jazzHRJobLink, job JobPosting) JobPosting {
	jobID := firstNonEmptyString(jazzHRJobToken(link.URL), jazzHRJobToken(job.ApplyURL), stableJobToken(firstNonEmptyString(job.ApplyURL, link.URL), job.Title))
	job.SourceJobID = "jazzhr:" + jobID
	job.SourceURL = source.URL
	job.ApplyURL = firstNonEmptyString(job.ApplyURL, link.URL)
	job.Strategy = TierATS
	job.Confidence = 0.85
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "JazzHR hosted job board and JobPosting detail page", URL: link.URL})
	return job
}

func jazzHRJobToken(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	for i := 0; i+1 < len(parts); i++ {
		switch {
		case strings.EqualFold(parts[i], "details"):
			return parts[i+1]
		case strings.EqualFold(parts[i], "apply") && !strings.EqualFold(parts[i+1], "jobs"):
			return parts[i+1]
		}
	}
	return ""
}

func jobviteCompanySlug(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	parts := nonEmptyPathParts(parsed)
	if len(parts) == 0 {
		return "", fmt.Errorf("jobvite company slug missing in %q", rawURL)
	}
	return parts[0], nil
}

func jobviteBoardURL(rawURL string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	slug, err := jobviteCompanySlug(parsed.String())
	if err != nil {
		return nil, err
	}
	parsed.Path = "/" + slug + "/jobs"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func jobviteJobLinks(baseURL *url.URL, companySlug string, document string) []jobviteJobLink {
	links := make([]jobviteJobLink, 0)
	seen := map[string]struct{}{}
	for _, anchor := range anchorTagPattern.FindAllString(document, -1) {
		href := anchorHref(anchor)
		resolvedURL := resolveStaticURL(baseURL, href)
		detailURL := normalizeJobviteDetailURL(resolvedURL)
		if detailURL == "" {
			continue
		}
		if jobviteJobIDFromURL(detailURL) == "" {
			continue
		}
		if slug, err := jobviteCompanySlug(detailURL); err != nil || !strings.EqualFold(slug, companySlug) {
			continue
		}
		key := strings.ToLower(detailURL)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		links = append(links, jobviteJobLink{URL: detailURL})
	}
	return links
}

func jobviteDirectJobLink(rawURL string) string {
	if jobviteJobIDFromURL(rawURL) == "" {
		return ""
	}
	return normalizeJobviteDetailURL(rawURL)
}

func normalizeJobviteDetailURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], "job") {
			parsed.Path = "/" + strings.Join(parts[:i+2], "/")
			parsed.RawQuery = ""
			parsed.Fragment = ""
			return parsed.String()
		}
	}
	return ""
}

func prependUniqueJobviteLink(links []jobviteJobLink, direct jobviteJobLink) []jobviteJobLink {
	if strings.TrimSpace(direct.URL) == "" {
		return links
	}
	for _, link := range links {
		if strings.EqualFold(link.URL, direct.URL) {
			return links
		}
	}
	out := make([]jobviteJobLink, 0, len(links)+1)
	out = append(out, direct)
	out = append(out, links...)
	return out
}

func normalizeJobviteJob(source Source, companySlug string, detailURL string, document string, job JobPosting) JobPosting {
	jobID := firstNonEmptyString(jobviteJobIDFromURL(detailURL), jobviteJobIDFromURL(job.ApplyURL), job.SourceJobID, stableJobToken(detailURL, job.Title))
	job.SourceJobID = "jobvite:" + companySlug + ":" + jobID
	job.SourceURL = source.URL
	job.Company = firstNonEmptyString(job.Company, jobviteCompanyName(document), sourceCompany(source, companySlug))
	job.ApplyURL = firstNonEmptyString(jobviteApplyURL(document, detailURL), job.ApplyURL, detailURL)
	job.Strategy = TierATS
	job.Confidence = 0.85
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "Jobvite hosted job board and JobPosting detail page", URL: detailURL})
	return job
}

func jobvitePostingFromHTML(source Source, companySlug string, detailURL *url.URL, document string) (JobPosting, bool) {
	title := cleanHTMLText(firstRegexpGroup(jobviteTitlePattern, document))
	if title == "" {
		return JobPosting{}, false
	}
	detailURLString := detailURL.String()
	description := cleanHTMLText(htmlClassSegment(document, "jv-job-detail-description", "job-description-meta", "jv-job-detail-bottom-actions", "jv-current-openings"))
	location := firstNonEmptyString(jobviteMetaValue(document, "Location"), jobviteLocationFromDetailMeta(document))
	employment := employmentFromText(title, jobviteMetaValue(document, "Employment Type"))
	return JobPosting{
		SourceJobID:    jobviteJobIDFromURL(detailURLString),
		Company:        firstNonEmptyString(jobviteCompanyName(document), sourceCompany(source, companySlug)),
		Title:          title,
		Location:       location,
		EmploymentType: employment,
		SourceURL:      source.URL,
		ApplyURL:       firstNonEmptyString(jobviteApplyURL(document, detailURLString), detailURLString),
		Live:           true,
		Confidence:     0.78,
		Strategy:       TierATS,
		Evidence: []Evidence{
			{Field: "html", Text: "Jobvite hosted detail page", URL: detailURLString},
			{Field: "description", Text: description, URL: detailURLString},
			{Field: "location", Text: location, URL: detailURLString},
		},
	}, true
}

func jobviteJobIDFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], "job") {
			return parts[i+1]
		}
	}
	return ""
}

func jobviteApplyURL(document string, detailURL string) string {
	baseURL, err := parseSourceURL(detailURL)
	if err != nil {
		return ""
	}
	for _, anchor := range anchorTagPattern.FindAllString(document, -1) {
		lower := strings.ToLower(anchor)
		if !strings.Contains(lower, "jv-button-apply") && !strings.Contains(lower, "/apply") {
			continue
		}
		href := anchorHref(anchor)
		if !strings.Contains(strings.ToLower(href), "/apply") {
			continue
		}
		return resolveStaticURL(baseURL, href)
	}
	return ""
}

func jobviteCompanyName(document string) string {
	return html.UnescapeString(firstRegexpGroup(jobviteCompanyNamePattern, document))
}

func jobviteMetaValue(document string, label string) string {
	segment := htmlClassSegment(document, "job-description-meta", "jv-job-detail-bottom-actions", "jv-current-openings")
	if segment == "" {
		return ""
	}
	pattern := regexp.MustCompile(`(?is)<li[^>]*>\s*<strong[^>]*>\s*` + regexp.QuoteMeta(label) + `\s*</strong>(.*?)</li>`)
	return cleanHTMLText(firstRegexpGroup(pattern, segment))
}

func jobviteLocationFromDetailMeta(document string) string {
	segment := htmlClassSegment(document, "jv-job-detail-meta", "<hr", "jv-job-detail-top-actions", "jv-job-detail-description")
	if segment == "" {
		return ""
	}
	text := cleanHTMLText(segment)
	if strings.Contains(text, "Location") {
		return ""
	}
	return text
}

func htmlClassSegment(document string, className string, endMarkers ...string) string {
	pattern := regexp.MustCompile(`(?is)<[^>]*class=["'][^"']*` + regexp.QuoteMeta(className) + `[^"']*["'][^>]*>`)
	match := pattern.FindStringIndex(document)
	if len(match) != 2 {
		return ""
	}
	lower := strings.ToLower(document)
	start := match[1]
	end := len(document)
	for _, marker := range endMarkers {
		if marker == "" {
			continue
		}
		if index := strings.Index(lower[start:], strings.ToLower(marker)); index >= 0 && start+index < end {
			end = start + index
		}
	}
	if end <= start {
		return ""
	}
	return document[start:end]
}

func firstRegexpGroup(pattern *regexp.Regexp, value string) string {
	match := pattern.FindStringSubmatch(value)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func teamtailorBoardURL(rawURL string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	parsed.Path = "/jobs"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func teamtailorJobLinks(baseURL *url.URL, document string) []teamtailorJobLink {
	links := make([]teamtailorJobLink, 0)
	seen := map[string]struct{}{}
	for _, anchor := range anchorTagPattern.FindAllString(document, -1) {
		href := anchorHref(anchor)
		resolvedURL := resolveStaticURL(baseURL, href)
		detailURL := normalizeTeamtailorDetailURL(resolvedURL)
		if detailURL == "" {
			continue
		}
		key := strings.ToLower(detailURL)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		links = append(links, teamtailorJobLink{URL: detailURL})
	}
	return links
}

func teamtailorDirectJobLink(rawURL string) string {
	if teamtailorJobIDFromURL(rawURL) == "" {
		return ""
	}
	return normalizeTeamtailorDetailURL(rawURL)
}

func normalizeTeamtailorDetailURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], "jobs") && teamtailorJobIDFromSlug(parts[i+1]) != "" {
			parsed.Path = "/" + strings.Join(parts[:i+2], "/")
			parsed.RawQuery = ""
			parsed.Fragment = ""
			return parsed.String()
		}
	}
	return ""
}

func prependUniqueTeamtailorLink(links []teamtailorJobLink, direct teamtailorJobLink) []teamtailorJobLink {
	if strings.TrimSpace(direct.URL) == "" {
		return links
	}
	for _, link := range links {
		if strings.EqualFold(link.URL, direct.URL) {
			return links
		}
	}
	out := make([]teamtailorJobLink, 0, len(links)+1)
	out = append(out, direct)
	out = append(out, links...)
	return out
}

func normalizeTeamtailorJob(source Source, account string, detailURL string, document string, job JobPosting) JobPosting {
	jobID := firstNonEmptyString(teamtailorJobIDFromURL(detailURL), teamtailorJobIDFromURL(job.ApplyURL), job.SourceJobID, stableJobToken(detailURL, job.Title))
	job.SourceJobID = "teamtailor:" + account + ":" + jobID
	job.SourceURL = source.URL
	job.ApplyURL = firstNonEmptyString(teamtailorApplyURL(document, detailURL), job.ApplyURL, detailURL)
	job.Strategy = TierATS
	job.Confidence = 0.86
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "Teamtailor hosted job board and JobPosting detail page", URL: detailURL})
	return job
}

func teamtailorJobIDFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], "jobs") {
			return teamtailorJobIDFromSlug(parts[i+1])
		}
	}
	return ""
}

func teamtailorJobIDFromSlug(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ""
	}
	end := 0
	for end < len(slug) && slug[end] >= '0' && slug[end] <= '9' {
		end++
	}
	if end == 0 {
		return ""
	}
	return slug[:end]
}

func teamtailorApplyURL(document string, detailURL string) string {
	baseURL, err := parseSourceURL(detailURL)
	if err != nil {
		return ""
	}
	if match := teamtailorApplyPattern.FindStringSubmatch(document); len(match) >= 2 {
		return resolveStaticURL(baseURL, html.UnescapeString(strings.TrimSpace(match[1])))
	}
	return strings.TrimRight(detailURL, "/") + "/applications/new"
}

func teamtailorAccountToken(rawURL string) string {
	host := sourceHost(rawURL)
	if host == "" {
		return ""
	}
	parts := strings.Split(host, ".")
	if len(parts) >= 3 && parts[len(parts)-2] == "teamtailor" && parts[len(parts)-1] == "com" {
		return stableAccountToken(parts[0])
	}
	return stableAccountToken(host)
}

func bambooHRListURL(rawURL string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	parsed.Path = "/careers/list"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func bambooHRDetailURL(rawURL string, id string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("bamboohr job id is required")
	}
	parsed.Path = "/" + path.Join("careers", id, "detail")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func bambooHRPublicJobURL(rawURL string, id string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	parsed.Path = "/" + path.Join("careers", id)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func bambooHRJobIDFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	if id := strings.TrimSpace(parsed.Query().Get("id")); id != "" {
		return id
	}
	parts := nonEmptyPathParts(parsed)
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], "careers") && !strings.EqualFold(parts[i+1], "list") {
			return parts[i+1]
		}
	}
	return ""
}

func bambooHRListJobs(payload bambooHRListResponse) []bambooHRListJob {
	jobs := make([]bambooHRListJob, 0, len(payload.Result))
	seen := map[string]struct{}{}
	for _, job := range payload.Result {
		id := strings.TrimSpace(job.ID)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		jobs = append(jobs, job)
	}
	return jobs
}

func prependUniqueBambooHRJob(jobs []bambooHRListJob, direct bambooHRListJob) []bambooHRListJob {
	id := strings.TrimSpace(direct.ID)
	if id == "" {
		return jobs
	}
	selected := direct
	out := make([]bambooHRListJob, 0, len(jobs)+1)
	for _, job := range jobs {
		if strings.EqualFold(strings.TrimSpace(job.ID), id) {
			selected = job
			continue
		}
		out = append(out, job)
	}
	out = append([]bambooHRListJob{selected}, out...)
	return out
}

func bambooHRDetailPosting(source Source, account string, detailURL string, summary bambooHRListJob, detail bambooHRDetailJob) (JobPosting, bool) {
	status := strings.ToLower(strings.TrimSpace(detail.JobOpeningStatus))
	if status != "" && status != "open" {
		return JobPosting{}, false
	}

	id := firstNonEmptyString(detail.ID, summary.ID, bambooHRJobIDFromURL(detailURL))
	title := firstNonEmptyString(detail.JobOpeningName, summary.JobOpeningName)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	location, country := bambooHRLocationText(
		firstNonZeroBambooHRLocation(detail.Location, summary.Location),
		firstNonZeroBambooHRATSLocation(detail.ATSLocation, summary.ATSLocation),
		firstNonNilBool(detail.IsRemote, summary.IsRemote),
	)
	description := cleanHTMLText(detail.Description)
	applyURL := firstNonEmptyString(detail.JobOpeningShareURL, bambooHRPublicJobURL(detailURL, id), detailURL)
	evidence := []Evidence{
		{Field: "ats", Text: "BambooHR public careers API", URL: detailURL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if department := firstNonEmptyString(detail.DepartmentLabel, summary.DepartmentLabel); department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: applyURL})
	}
	if detail.Compensation != "" {
		evidence = append(evidence, Evidence{Field: "compensation", Text: detail.Compensation, URL: applyURL})
	}
	if detail.MinimumExperience != "" {
		evidence = append(evidence, Evidence{Field: "minimum_experience", Text: detail.MinimumExperience, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
	}

	return JobPosting{
		SourceJobID:    "bamboohr:" + account + ":" + id,
		Company:        sourceCompany(source, account),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, firstNonEmptyString(detail.EmploymentStatusLabel, summary.EmploymentStatusLabel)),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(detail.DatePosted),
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func bambooHRSummaryPosting(source Source, account string, detailURL string, summary bambooHRListJob) (JobPosting, bool) {
	id := strings.TrimSpace(summary.ID)
	title := strings.TrimSpace(summary.JobOpeningName)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	location, country := bambooHRLocationText(summary.Location, summary.ATSLocation, summary.IsRemote)
	applyURL := firstNonEmptyString(bambooHRPublicJobURL(detailURL, id), source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "BambooHR public careers list summary", URL: detailURL},
	}
	if summary.DepartmentLabel != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: summary.DepartmentLabel, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
	}
	return JobPosting{
		SourceJobID:    "bamboohr:" + account + ":" + id,
		Company:        sourceCompany(source, account),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, summary.EmploymentStatusLabel),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		Live:           true,
		Confidence:     0.72,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func bambooHRLocationText(location bambooHRLocation, atsLocation bambooHRATSLocation, isRemote *bool) (string, string) {
	country := canonicalCountry(firstNonEmptyString(atsLocation.Country, location.AddressCountry, location.Country))
	city := firstNonEmptyString(atsLocation.City, location.City)
	state := firstNonEmptyString(atsLocation.State, atsLocation.Province, location.State, location.Province)
	text := strings.Join(compactStringList(city, state, country), ", ")
	if isRemote != nil && *isRemote && !strings.Contains(strings.ToLower(text), "remote") {
		if text == "" {
			text = "Remote"
		} else {
			text = "Remote - " + text
		}
	}
	return text, country
}

func firstNonZeroBambooHRLocation(values ...bambooHRLocation) bambooHRLocation {
	for _, value := range values {
		if firstNonEmptyString(value.City, value.State, value.Province, value.AddressCountry, value.Country) != "" {
			return value
		}
	}
	return bambooHRLocation{}
}

func firstNonZeroBambooHRATSLocation(values ...bambooHRATSLocation) bambooHRATSLocation {
	for _, value := range values {
		if firstNonEmptyString(value.City, value.State, value.Province, value.Country) != "" {
			return value
		}
	}
	return bambooHRATSLocation{}
}

func firstNonNilBool(values ...*bool) *bool {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func bambooHRAccountToken(rawURL string) string {
	host := sourceHost(rawURL)
	if host == "" {
		return ""
	}
	parts := strings.Split(host, ".")
	if len(parts) >= 3 && parts[len(parts)-2] == "bamboohr" && parts[len(parts)-1] == "com" {
		return stableAccountToken(parts[0])
	}
	return stableAccountToken(host)
}

func ripplingBoardSlug(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		if strings.EqualFold(part, "board") && i+1 < len(parts) {
			return parts[i+1], nil
		}
		if strings.EqualFold(part, "jobs") && i > 0 {
			return parts[i-1], nil
		}
	}
	for _, part := range parts {
		if ripplingLocalePart(part) || strings.EqualFold(part, "api") || strings.EqualFold(part, "v2") {
			continue
		}
		return part, nil
	}
	return "", errors.New("rippling board slug is required")
}

func ripplingJobsAPIURL(rawURL string, board string, page int, pageSize int) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	board = strings.TrimSpace(board)
	if board == "" {
		return nil, errors.New("rippling board slug is required")
	}
	parsed.Path = "/" + path.Join("api", "v2", "board", board, "jobs")
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("pageSize", strconv.Itoa(pageSize))
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed, nil
}

func ripplingDetailAPIURL(rawURL string, board string, id string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	board = strings.TrimSpace(board)
	id = strings.TrimSpace(id)
	if board == "" {
		return nil, errors.New("rippling board slug is required")
	}
	if id == "" {
		return nil, errors.New("rippling job id is required")
	}
	parsed.Path = "/" + path.Join("api", "v2", "board", board, "jobs", id)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func ripplingHostedJobURL(rawURL string, board string, id string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	board = strings.TrimSpace(board)
	id = strings.TrimSpace(id)
	if board == "" || id == "" {
		return ""
	}
	parsed.Path = "/" + path.Join(board, "jobs", id)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func ripplingJobIDFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		if strings.EqualFold(part, "jobs") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func ripplingLocalePart(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 2 || len(value) > 6 {
		return false
	}
	if len(value) == 2 {
		for _, char := range value {
			if char < 'a' || char > 'z' {
				return false
			}
		}
		return true
	}
	if len(value) >= 5 && value[2] == '-' {
		return true
	}
	return false
}

func mergeRipplingJobSummaries(existing []ripplingJobSummary, incoming []ripplingJobSummary) []ripplingJobSummary {
	out := append([]ripplingJobSummary(nil), existing...)
	indices := make(map[string]int, len(out))
	for index, job := range out {
		id := strings.ToLower(strings.TrimSpace(job.ID))
		if id != "" {
			indices[id] = index
		}
	}
	for _, job := range incoming {
		id := strings.TrimSpace(job.ID)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if index, ok := indices[key]; ok {
			out[index] = mergeRipplingJobSummary(out[index], job)
			continue
		}
		indices[key] = len(out)
		out = append(out, job)
	}
	return out
}

func mergeRipplingJobSummary(existing ripplingJobSummary, incoming ripplingJobSummary) ripplingJobSummary {
	if existing.Name == "" {
		existing.Name = incoming.Name
	}
	if existing.URL == "" {
		existing.URL = incoming.URL
	}
	if existing.Department.Name == "" {
		existing.Department = incoming.Department
	}
	if existing.Language == "" {
		existing.Language = incoming.Language
	}
	existing.Locations = appendRipplingLocations(existing.Locations, incoming.Locations...)
	return existing
}

func appendRipplingLocations(existing []ripplingLocation, incoming ...ripplingLocation) []ripplingLocation {
	out := append([]ripplingLocation(nil), existing...)
	seen := map[string]struct{}{}
	for _, location := range out {
		key := strings.ToLower(strings.Join(compactStringList(location.Name, location.City, location.StateCode, location.CountryCode, location.WorkplaceType), "|"))
		if key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, location := range incoming {
		key := strings.ToLower(strings.Join(compactStringList(location.Name, location.City, location.StateCode, location.CountryCode, location.WorkplaceType), "|"))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, location)
	}
	return out
}

func prependUniqueRipplingJob(jobs []ripplingJobSummary, direct ripplingJobSummary) []ripplingJobSummary {
	id := strings.TrimSpace(direct.ID)
	if id == "" {
		return jobs
	}
	selected := direct
	out := make([]ripplingJobSummary, 0, len(jobs)+1)
	for _, job := range jobs {
		if strings.EqualFold(strings.TrimSpace(job.ID), id) {
			selected = job
			continue
		}
		out = append(out, job)
	}
	out = append([]ripplingJobSummary{selected}, out...)
	return out
}

func ripplingDetailPosting(source Source, board string, detailURL string, summary ripplingJobSummary, detail ripplingJobDetail) (JobPosting, bool) {
	id := firstNonEmptyString(detail.UUID, summary.ID, ripplingJobIDFromURL(detail.URL), ripplingJobIDFromURL(detailURL))
	title := firstNonEmptyString(detail.Name, summary.Name)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	description := cleanHTMLText(firstNonEmptyString(detail.Description.Role, detail.Description.Company))
	location, country := ripplingLocationText(summary.Locations, detail.WorkLocations)
	applyURL := firstNonEmptyString(detail.URL, summary.URL, ripplingHostedJobURL(detailURL, board, id))
	employment := employmentFromText(title, firstNonEmptyString(detail.EmploymentType.ID, detail.EmploymentType.Label))
	evidence := []Evidence{
		{Field: "ats", Text: "Rippling public board and job detail API", URL: detailURL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if company := cleanHTMLText(detail.Description.Company); company != "" {
		evidence = append(evidence, Evidence{Field: "company_description", Text: company, URL: applyURL})
	}
	if department := ripplingDepartmentText(firstNonZeroRipplingDepartment(detail.Department, summary.Department)); department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
	}

	return JobPosting{
		SourceJobID:    "rippling:" + stableAccountToken(board) + ":" + id,
		Company:        firstNonEmptyString(detail.CompanyName, sourceCompany(source, board)),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(detail.CreatedOn),
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func ripplingSummaryPosting(source Source, board string, detailURL string, summary ripplingJobSummary) (JobPosting, bool) {
	id := strings.TrimSpace(summary.ID)
	title := strings.TrimSpace(summary.Name)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	location, country := ripplingLocationText(summary.Locations, nil)
	applyURL := firstNonEmptyString(summary.URL, ripplingHostedJobURL(detailURL, board, id), source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "Rippling public board summary", URL: detailURL},
	}
	if department := ripplingDepartmentText(summary.Department); department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
	}
	return JobPosting{
		SourceJobID:    "rippling:" + stableAccountToken(board) + ":" + id,
		Company:        sourceCompany(source, board),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, ""),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		Live:           true,
		Confidence:     0.72,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func ripplingLocationText(locations []ripplingLocation, workLocations []string) (string, string) {
	parts := make([]string, 0, len(locations)+len(workLocations))
	country := ""
	for _, location := range locations {
		locationCountry := canonicalCountry(firstNonEmptyString(location.CountryCode, location.Country))
		if country == "" {
			country = locationCountry
		}
		text := firstNonEmptyString(location.Name, strings.Join(compactStringList(location.City, firstNonEmptyString(location.StateCode, location.State), locationCountry), ", "))
		if locationCountry != "" && text != "" && !strings.Contains(strings.ToLower(text), strings.ToLower(locationCountry)) {
			text = strings.Join(compactStringList(text, locationCountry), ", ")
		}
		if strings.EqualFold(location.WorkplaceType, "REMOTE") && !strings.Contains(strings.ToLower(text), "remote") {
			if text == "" {
				text = "Remote"
			} else {
				text = "Remote - " + text
			}
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		for _, location := range workLocations {
			if strings.TrimSpace(location) == "" {
				continue
			}
			if country == "" {
				country = normalizeCountry("", location)
				if country == "unknown" {
					country = ""
				}
			}
			parts = append(parts, location)
		}
	}
	return strings.Join(compactStringList(parts...), "; "), country
}

func ripplingDepartmentText(department ripplingDepartment) string {
	if len(department.DepartmentTree) > 0 {
		return strings.Join(compactStringList(department.DepartmentTree...), " / ")
	}
	return firstNonEmptyString(department.Name, department.BaseDepartment)
}

func firstNonZeroRipplingDepartment(values ...ripplingDepartment) ripplingDepartment {
	for _, value := range values {
		if firstNonEmptyString(value.Name, value.BaseDepartment) != "" || len(value.DepartmentTree) > 0 {
			return value
		}
	}
	return ripplingDepartment{}
}

func successFactorsFeedURL(rawURL string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	parts := nonEmptyPathParts(parsed)
	if len(parts) > 0 {
		last := strings.ToLower(parts[len(parts)-1])
		if last == "sitemal.xml" || last == "sitemap.xml" {
			parsed.RawQuery = ""
			parsed.Fragment = ""
			return parsed, nil
		}
	}
	parsed.Path = "/sitemal.xml"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func successFactorsItems(feed successFactorsRSS) []successFactorsItem {
	items := make([]successFactorsItem, 0, len(feed.Channel.Items))
	seen := map[string]struct{}{}
	for _, item := range feed.Channel.Items {
		id := firstNonEmptyString(item.ID, item.GUID, stableJobToken(item.Link, item.Title))
		if id == "" || strings.TrimSpace(item.Title) == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, item)
	}
	return items
}

func successFactorsPosting(source Source, account string, feedURL string, item successFactorsItem) (JobPosting, bool) {
	id := firstNonEmptyString(item.ID, item.GUID, stableJobToken(item.Link, item.Title))
	title := successFactorsTitle(item.Title, item.Location)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	applyURL := firstNonEmptyString(item.Link, source.URL)
	description := cleanHTMLText(item.Description)
	location := firstNonEmptyString(item.Location, successFactorsLocationFromTitle(item.Title))
	country := successFactorsCountry(location)
	employment := employmentFromText(title, item.JobFunction)
	evidence := []Evidence{
		{Field: "ats", Text: "SAP SuccessFactors Recruiting Marketing RSS job feed", URL: feedURL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if item.JobFunction != "" {
		evidence = append(evidence, Evidence{Field: "job_function", Text: item.JobFunction, URL: applyURL})
	}
	if item.ExpirationDate != "" {
		evidence = append(evidence, Evidence{Field: "expiration_date", Text: item.ExpirationDate, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
	}

	return JobPosting{
		SourceJobID:    "successfactors:" + account + ":" + id,
		Company:        firstNonEmptyString(item.Employer, sourceCompany(source, account)),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func adpWorkforceNowConfigFromURL(rawURL string) (adpWorkforceNowConfig, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return adpWorkforceNowConfig{}, err
	}
	query := parsed.Query()
	config := adpWorkforceNowConfig{
		CID:    strings.TrimSpace(query.Get("cid")),
		CCID:   strings.TrimSpace(query.Get("ccId")),
		Lang:   firstNonEmptyString(query.Get("lang"), "en_US"),
		Locale: firstNonEmptyString(query.Get("locale"), query.Get("lang"), "en_US"),
		JobID:  firstNonEmptyString(query.Get("jobId"), adpWorkforceNowJobIDFromPath(parsed)),
		JWID:   strings.TrimSpace(query.Get("jwId")),
	}
	if config.CID == "" {
		return adpWorkforceNowConfig{}, errors.New("adp workforce now cid is required")
	}
	return config, nil
}

func adpWorkforceNowListURL(rawURL string, config adpWorkforceNowConfig, skip int, top int) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	parsed.Path = "/" + path.Join("mascsr", "default", "careercenter", "public", "events", "staffing", "v1", "job-requisitions")
	parsed.RawQuery = adpWorkforceNowQuery(config, map[string]string{
		"$skip": strconv.Itoa(skip),
		"$top":  strconv.Itoa(top),
	}).Encode()
	parsed.Fragment = ""
	return parsed, nil
}

func adpWorkforceNowDetailURL(rawURL string, config adpWorkforceNowConfig, jobID string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return nil, errors.New("adp workforce now job id is required")
	}
	parsed.Path = "/" + path.Join("mascsr", "default", "careercenter", "public", "events", "staffing", "v1", "job-requisitions", jobID)
	parsed.RawQuery = adpWorkforceNowQuery(config, nil).Encode()
	parsed.Fragment = ""
	return parsed, nil
}

func adpWorkforceNowApplyURL(rawURL string, config adpWorkforceNowConfig, jobID string, jwID string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parsed.Path = "/" + path.Join("mascsr", "default", "mdf", "recruitment", "recruitment.html")
	query := adpWorkforceNowQuery(config, map[string]string{
		"type":  "JS",
		"jobId": strings.TrimSpace(jobID),
	})
	if strings.TrimSpace(jwID) != "" {
		query.Set("jwId", strings.TrimSpace(jwID))
	}
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String()
}

func adpWorkforceNowQuery(config adpWorkforceNowConfig, extras map[string]string) url.Values {
	query := url.Values{}
	query.Set("cid", strings.TrimSpace(config.CID))
	if strings.TrimSpace(config.CCID) != "" {
		query.Set("ccId", strings.TrimSpace(config.CCID))
	}
	query.Set("lang", firstNonEmptyString(config.Lang, "en_US"))
	query.Set("locale", firstNonEmptyString(config.Locale, config.Lang, "en_US"))
	for key, value := range extras {
		if strings.TrimSpace(value) != "" {
			query.Set(key, strings.TrimSpace(value))
		}
	}
	return query
}

func adpWorkforceNowJobIDFromPath(parsed *url.URL) string {
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		if strings.EqualFold(part, "job-requisitions") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

func adpWorkforceNowSyntheticJob(jobID string, jwID string) adpWorkforceNowJob {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return adpWorkforceNowJob{}
	}
	return adpWorkforceNowJob{
		ItemID:              strings.TrimSpace(jwID),
		ClientRequisitionID: jobID,
		CustomFieldGroup: adpWorkforceNowCustomFieldGroup{
			StringFields: []adpWorkforceNowStringField{
				{StringValue: jobID, NameCode: adpWorkforceNowCode{CodeValue: "ExternalJobID"}},
			},
		},
	}
}

func mergeADPWorkforceNowJobs(existing []adpWorkforceNowJob, incoming []adpWorkforceNowJob) []adpWorkforceNowJob {
	out := append([]adpWorkforceNowJob(nil), existing...)
	indices := make(map[string]int, len(out))
	for index, job := range out {
		if id := strings.ToLower(adpWorkforceNowJobID(job)); id != "" {
			indices[id] = index
		}
	}
	for _, job := range incoming {
		id := strings.TrimSpace(adpWorkforceNowJobID(job))
		if id == "" || strings.TrimSpace(job.RequisitionTitle) == "" {
			continue
		}
		key := strings.ToLower(id)
		if index, ok := indices[key]; ok {
			out[index] = mergeADPWorkforceNowJob(out[index], job)
			continue
		}
		indices[key] = len(out)
		out = append(out, job)
	}
	return out
}

func mergeADPWorkforceNowJob(primary adpWorkforceNowJob, fallback adpWorkforceNowJob) adpWorkforceNowJob {
	if primary.ItemID == "" {
		primary.ItemID = fallback.ItemID
	}
	if primary.RequisitionTitle == "" {
		primary.RequisitionTitle = fallback.RequisitionTitle
	}
	if primary.ClientRequisitionID == "" {
		primary.ClientRequisitionID = fallback.ClientRequisitionID
	}
	if primary.PostDate == "" {
		primary.PostDate = fallback.PostDate
	}
	if primary.RequisitionDescription == "" {
		primary.RequisitionDescription = fallback.RequisitionDescription
	}
	if len(primary.RequisitionLocations) == 0 {
		primary.RequisitionLocations = fallback.RequisitionLocations
	}
	if primary.WorkLevelCode == (adpWorkforceNowCode{}) {
		primary.WorkLevelCode = fallback.WorkLevelCode
	}
	if len(primary.CustomFieldGroup.StringFields) == 0 {
		primary.CustomFieldGroup.StringFields = fallback.CustomFieldGroup.StringFields
	}
	if len(primary.CustomFieldGroup.DateFields) == 0 {
		primary.CustomFieldGroup.DateFields = fallback.CustomFieldGroup.DateFields
	}
	if primary.PayGradeRange == (adpWorkforceNowPayGradeRange{}) {
		primary.PayGradeRange = fallback.PayGradeRange
	}
	return primary
}

func prependUniqueADPWorkforceNowJob(jobs []adpWorkforceNowJob, direct adpWorkforceNowJob) []adpWorkforceNowJob {
	id := strings.TrimSpace(adpWorkforceNowJobID(direct))
	if id == "" {
		return jobs
	}
	selected := direct
	out := make([]adpWorkforceNowJob, 0, len(jobs)+1)
	for _, job := range jobs {
		if strings.EqualFold(strings.TrimSpace(adpWorkforceNowJobID(job)), id) {
			selected = mergeADPWorkforceNowJob(job, direct)
			continue
		}
		out = append(out, job)
	}
	return append([]adpWorkforceNowJob{selected}, out...)
}

func adpWorkforceNowPosting(source Source, config adpWorkforceNowConfig, account string, summary adpWorkforceNowJob, detail adpWorkforceNowJob, detailURL string) (JobPosting, bool) {
	posting := mergeADPWorkforceNowJob(detail, summary)
	id := firstNonEmptyString(adpWorkforceNowJobID(posting), config.JobID, stableJobToken(detailURL, posting.RequisitionTitle))
	title := strings.TrimSpace(posting.RequisitionTitle)
	if id == "" || title == "" {
		return JobPosting{}, false
	}

	description := cleanHTMLText(posting.RequisitionDescription)
	location, country := adpWorkforceNowLocationText(posting.RequisitionLocations)
	workLevel := firstNonEmptyString(posting.WorkLevelCode.ShortName, posting.WorkLevelCode.CodeValue)
	jobClass := adpWorkforceNowStringValue(posting, "JobClass")
	salaryRange := firstNonEmptyString(adpWorkforceNowStringValue(posting, "SalaryRange"), adpWorkforceNowPayGradeRangeText(posting.PayGradeRange))
	postedAt := parseTimePtr(firstNonEmptyString(posting.PostDate, adpWorkforceNowDateValue(posting, "PostingDate")))
	applyURL := firstNonEmptyString(adpWorkforceNowApplyURL(source.URL, config, id, firstNonEmptyString(posting.ItemID, config.JWID)), source.URL)

	evidence := []Evidence{
		{Field: "ats", Text: "ADP Workforce Now public staffing job requisitions API", URL: detailURL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
	}
	if posting.ClientRequisitionID != "" {
		evidence = append(evidence, Evidence{Field: "client_requisition_id", Text: posting.ClientRequisitionID, URL: applyURL})
	}
	if jobClass != "" {
		evidence = append(evidence, Evidence{Field: "job_class", Text: jobClass, URL: applyURL})
	}
	if workLevel != "" {
		evidence = append(evidence, Evidence{Field: "work_level", Text: workLevel, URL: applyURL})
	}
	if salaryRange != "" {
		evidence = append(evidence, Evidence{Field: "salary_range", Text: salaryRange, URL: applyURL})
	}

	return JobPosting{
		SourceJobID:    "adp_workforcenow:" + account + ":" + id,
		Company:        sourceCompany(source, account),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, firstNonEmptyString(workLevel, jobClass)),
		RoleFamily:     inferRoleFamily(title + " " + description),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       postedAt,
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func adpMyJobsConfigFromURL(rawURL string) (adpMyJobsConfig, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return adpMyJobsConfig{}, err
	}
	parts := nonEmptyPathParts(parsed)
	domain := ""
	if len(parts) > 0 {
		domain = strings.TrimSpace(parts[0])
	}
	config := adpMyJobsConfig{
		Domain: domain,
		Lang:   firstNonEmptyString(parsed.Query().Get("lang"), "en-US"),
		ReqID:  strings.TrimSpace(parsed.Query().Get("reqId")),
	}
	if config.Domain == "" {
		return adpMyJobsConfig{}, errors.New("adp myjobs domain is required")
	}
	return config, nil
}

func adpMyJobsCareerSiteURL(baseURL string, domain string) (*url.URL, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil, errors.New("adp myjobs domain is required")
	}
	return joinURL(baseURL, domain)
}

func adpMyJobsListURL(baseURL string, skip int, top int) (*url.URL, error) {
	parsed, err := parseSourceURL(baseURL)
	if err != nil {
		return nil, err
	}
	query := parsed.Query()
	query.Set("$skip", strconv.Itoa(skip))
	query.Set("$top", strconv.Itoa(top))
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed, nil
}

func adpMyJobsDetailAPIURL(baseURL string, reqID string) (*url.URL, error) {
	reqID = strings.TrimSpace(reqID)
	if reqID == "" {
		return nil, errors.New("adp myjobs req id is required")
	}
	return joinURL(baseURL, "search-meta", reqID)
}

func adpMyJobsApplyURL(rawURL string, config adpMyJobsConfig, reqID string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	reqID = strings.TrimSpace(reqID)
	if reqID == "" {
		return ""
	}
	parsed.Path = "/" + path.Join(config.Domain, "cx", "job-details")
	query := url.Values{}
	query.Set("lang", firstNonEmptyString(config.Lang, "en-US"))
	query.Set("reqId", reqID)
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String()
}

func adpMyJobsHeaders(token string) map[string]string {
	return map[string]string{
		"Accept":      "application/json, text/plain, */*",
		"Origin":      "https://myjobs.adp.com",
		"Referer":     "https://myjobs.adp.com/",
		"myJobsToken": strings.TrimSpace(token),
	}
}

func firstADPMyJobsRequisition(jobs []adpMyJobsRequisition) adpMyJobsRequisition {
	if len(jobs) == 0 {
		return adpMyJobsRequisition{}
	}
	return jobs[0]
}

func mergeADPMyJobsRequisitions(existing []adpMyJobsRequisition, incoming []adpMyJobsRequisition) []adpMyJobsRequisition {
	out := append([]adpMyJobsRequisition(nil), existing...)
	indices := make(map[string]int, len(out))
	for index, job := range out {
		if id := strings.ToLower(adpMyJobsReqID(job)); id != "" {
			indices[id] = index
		}
	}
	for _, job := range incoming {
		id := strings.TrimSpace(adpMyJobsReqID(job))
		if id == "" || strings.TrimSpace(adpMyJobsTitle(job)) == "" {
			continue
		}
		key := strings.ToLower(id)
		if index, ok := indices[key]; ok {
			out[index] = mergeADPMyJobsRequisition(out[index], job)
			continue
		}
		indices[key] = len(out)
		out = append(out, job)
	}
	return out
}

func mergeADPMyJobsRequisition(primary adpMyJobsRequisition, fallback adpMyJobsRequisition) adpMyJobsRequisition {
	if primary.ItemID == nil {
		primary.ItemID = fallback.ItemID
	}
	if primary.ReqID == "" {
		primary.ReqID = fallback.ReqID
	}
	if primary.RequisitionTitle == "" {
		primary.RequisitionTitle = fallback.RequisitionTitle
	}
	if primary.JobTitle == "" {
		primary.JobTitle = fallback.JobTitle
	}
	if primary.ClientRequisitionID == "" {
		primary.ClientRequisitionID = fallback.ClientRequisitionID
	}
	if primary.RequisitionDescription == "" {
		primary.RequisitionDescription = fallback.RequisitionDescription
	}
	if len(primary.RequisitionLocations) == 0 {
		primary.RequisitionLocations = fallback.RequisitionLocations
	}
	if len(primary.PostingInstructions) == 0 {
		primary.PostingInstructions = fallback.PostingInstructions
	}
	if len(primary.ScreeningRequirements) == 0 {
		primary.ScreeningRequirements = fallback.ScreeningRequirements
	}
	if len(primary.CustomFieldGroup.CodeFields) == 0 {
		primary.CustomFieldGroup.CodeFields = fallback.CustomFieldGroup.CodeFields
	}
	if primary.Type == "" {
		primary.Type = fallback.Type
	}
	if !primary.CanApply {
		primary.CanApply = fallback.CanApply
	}
	return primary
}

func prependUniqueADPMyJobsRequisition(jobs []adpMyJobsRequisition, direct adpMyJobsRequisition) []adpMyJobsRequisition {
	id := strings.TrimSpace(adpMyJobsReqID(direct))
	if id == "" {
		return jobs
	}
	selected := direct
	out := make([]adpMyJobsRequisition, 0, len(jobs)+1)
	for _, job := range jobs {
		if strings.EqualFold(strings.TrimSpace(adpMyJobsReqID(job)), id) {
			selected = mergeADPMyJobsRequisition(job, direct)
			continue
		}
		out = append(out, job)
	}
	return append([]adpMyJobsRequisition{selected}, out...)
}

func adpMyJobsSyntheticRequisition(reqID string) adpMyJobsRequisition {
	reqID = strings.TrimSpace(reqID)
	if reqID == "" {
		return adpMyJobsRequisition{}
	}
	return adpMyJobsRequisition{ReqID: reqID}
}

func adpMyJobsJobPosting(source Source, config adpMyJobsConfig, account string, careerSite adpMyJobsCareerSite, summary adpMyJobsRequisition, detail adpMyJobsRequisition, detailURL string) (JobPosting, bool) {
	posting := mergeADPMyJobsRequisition(detail, summary)
	id := firstNonEmptyString(adpMyJobsReqID(posting), config.ReqID, stableJobToken(detailURL, adpMyJobsTitle(posting)))
	title := strings.TrimSpace(adpMyJobsTitle(posting))
	if id == "" || title == "" {
		return JobPosting{}, false
	}

	description := cleanHTMLText(posting.RequisitionDescription)
	requirements := cleanHTMLText(adpMyJobsRequirementsText(posting.ScreeningRequirements))
	location, country := adpWorkforceNowLocationText(posting.RequisitionLocations)
	employment := adpMyJobsCodeValue(posting, "RTiReq_typeOfFulltime")
	postedAt := parseTimePtr(adpMyJobsPostedAt(posting.PostingInstructions))
	applyURL := firstNonEmptyString(adpMyJobsApplyURL(source.URL, config, id), adpMyJobsPostingChannelURL(posting.PostingInstructions), source.URL)
	company := firstNonEmptyString(sourceCompany(source, account), careerSite.ClientName)

	evidence := []Evidence{
		{Field: "ats", Text: "ADP MyJobs public staffing job requisitions API", URL: detailURL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if requirements != "" {
		evidence = append(evidence, Evidence{Field: "requirements", Text: requirements, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
	}
	if posting.ClientRequisitionID != "" {
		evidence = append(evidence, Evidence{Field: "client_requisition_id", Text: posting.ClientRequisitionID, URL: applyURL})
	}
	if employment != "" {
		evidence = append(evidence, Evidence{Field: "employment_type", Text: employment, URL: applyURL})
	}

	return JobPosting{
		SourceJobID:    "adp_myjobs:" + account + ":" + id,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, employment),
		RoleFamily:     inferRoleFamily(title + " " + description + " " + requirements),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       postedAt,
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func adpMyJobsReqID(job adpMyJobsRequisition) string {
	return firstNonEmptyString(job.ReqID, scalarString(job.ItemID), job.ClientRequisitionID)
}

func adpMyJobsTitle(job adpMyJobsRequisition) string {
	return firstNonEmptyString(job.RequisitionTitle, job.JobTitle)
}

func adpMyJobsRequirementsText(requirements []adpMyJobsRequirement) string {
	parts := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		parts = append(parts, requirement.RequirementDescription)
	}
	return strings.Join(compactStringList(parts...), "\n")
}

func adpMyJobsPostedAt(postings []adpMyJobsPosting) string {
	for _, posting := range postings {
		if value := firstNonEmptyString(posting.TimestampLastPosted, posting.PostDate); value != "" {
			return value
		}
	}
	return ""
}

func adpMyJobsPostingChannelURL(postings []adpMyJobsPosting) string {
	for _, posting := range postings {
		if value := strings.TrimSpace(posting.PostingChannel.InternetAddress); value != "" {
			return value
		}
	}
	return ""
}

func adpMyJobsCodeValue(job adpMyJobsRequisition, name string) string {
	for _, field := range job.CustomFieldGroup.CodeFields {
		if adpWorkforceNowCodeMatches(field.CategoryCode, name) {
			return firstNonEmptyString(field.LongName, field.ShortName, field.CodeValue)
		}
	}
	return ""
}

func scalarString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func adpWorkforceNowJobID(job adpWorkforceNowJob) string {
	return firstNonEmptyString(adpWorkforceNowStringValue(job, "ExternalJobID"), job.ClientRequisitionID, job.ItemID)
}

func adpWorkforceNowStringValue(job adpWorkforceNowJob, name string) string {
	for _, field := range job.CustomFieldGroup.StringFields {
		if adpWorkforceNowCodeMatches(field.NameCode, name) {
			return strings.TrimSpace(field.StringValue)
		}
	}
	return ""
}

func adpWorkforceNowDateValue(job adpWorkforceNowJob, name string) string {
	for _, field := range job.CustomFieldGroup.DateFields {
		if adpWorkforceNowCodeMatches(field.NameCode, name) {
			return strings.TrimSpace(field.DateValue)
		}
	}
	return ""
}

func adpWorkforceNowCodeMatches(code adpWorkforceNowCode, name string) bool {
	want := compactATSFieldKey(name)
	return compactATSFieldKey(code.CodeValue) == want || compactATSFieldKey(code.ShortName) == want || compactATSFieldKey(code.LongName) == want
}

func compactATSFieldKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func adpWorkforceNowLocationText(locations []adpWorkforceNowLocation) (string, string) {
	parts := make([]string, 0, len(locations))
	country := ""
	for _, location := range locations {
		countryText := firstNonEmptyString(location.Address.Country.LongName, location.Address.Country.CodeValue)
		addressText := strings.Join(compactStringList(
			location.Address.CityName,
			location.Address.CountrySubdivisionLevel1.CodeValue,
			firstNonEmptyString(countryText, location.Address.PostalCode),
		), ", ")
		text := firstNonEmptyString(
			location.NameCode.ShortName,
			location.NameCode.LongName,
			addressText,
			location.NameCode.CodeValue,
		)
		if text == "" {
			continue
		}
		if country == "" {
			country = firstNonEmptyString(canonicalCountry(countryText), adpWorkforceNowCountry(text))
		}
		parts = append(parts, text)
	}
	return strings.Join(compactStringList(parts...), "; "), country
}

func adpWorkforceNowCountry(location string) string {
	lower := strings.ToLower(location)
	switch {
	case strings.Contains(lower, "united states"), strings.Contains(lower, ", us"), strings.HasSuffix(lower, " us"):
		return "US"
	case strings.Contains(lower, "singapore"), strings.Contains(lower, ", sg"), strings.HasSuffix(lower, " sg"):
		return "Singapore"
	case strings.Contains(lower, "hong kong"), strings.Contains(lower, ", hk"), strings.HasSuffix(lower, " hk"):
		return "Hong Kong"
	case strings.Contains(lower, "canada"), strings.Contains(lower, ", ca"), strings.HasSuffix(lower, " ca"):
		return "Canada"
	case strings.Contains(lower, "united kingdom"), strings.Contains(lower, ", uk"), strings.Contains(lower, ", gb"), strings.Contains(lower, "london"):
		return "UK"
	}
	parts := strings.Split(location, ",")
	if len(parts) > 0 {
		country := canonicalCountry(parts[len(parts)-1])
		if country != "" && country != parts[len(parts)-1] {
			return country
		}
	}
	return ""
}

func adpWorkforceNowPayGradeRangeText(payRange adpWorkforceNowPayGradeRange) string {
	minimum := payRange.MinimumRate.AmountValue
	maximum := payRange.MaximumRate.AmountValue
	currency := firstNonEmptyString(payRange.MinimumRate.CurrencyCode, payRange.MaximumRate.CurrencyCode)
	switch {
	case minimum > 0 && maximum > 0:
		return strings.TrimSpace(fmt.Sprintf("%.2f to %.2f %s", minimum, maximum, currency))
	case minimum > 0:
		return strings.TrimSpace(fmt.Sprintf("%.2f+ %s", minimum, currency))
	case maximum > 0:
		return strings.TrimSpace(fmt.Sprintf("up to %.2f %s", maximum, currency))
	default:
		return ""
	}
}

func ukgProHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return strings.HasPrefix(host, "recruiting") && strings.HasSuffix(host, ".ultipro.com")
}

func ukgProConfigFromURL(rawURL string) (ukgProConfig, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ukgProConfig{}, err
	}
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		if strings.EqualFold(part, "JobBoard") && i > 0 && i+1 < len(parts) {
			return ukgProConfig{Account: parts[i-1], BoardID: parts[i+1]}, nil
		}
	}
	return ukgProConfig{}, errors.New("ukg job board account and board id are required")
}

func ukgProOpportunitiesFromHTML(page string) ([]ukgProOpportunity, error) {
	arrayText, err := ukgProJSArrayValue(page, "opportunities")
	if err != nil {
		return nil, err
	}
	var opportunities []ukgProOpportunity
	if err := json.Unmarshal([]byte(arrayText), &opportunities); err != nil {
		return nil, err
	}
	return ukgProCleanOpportunities(opportunities), nil
}

func ukgProCleanOpportunities(opportunities []ukgProOpportunity) []ukgProOpportunity {
	out := make([]ukgProOpportunity, 0, len(opportunities))
	seen := map[string]struct{}{}
	for _, opportunity := range opportunities {
		id := ukgProOpportunityID(opportunity)
		if id == "" || strings.TrimSpace(opportunity.Title) == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, opportunity)
	}
	return out
}

func mergeUKGProOpportunities(existing []ukgProOpportunity, incoming []ukgProOpportunity) []ukgProOpportunity {
	out := append([]ukgProOpportunity(nil), existing...)
	seen := make(map[string]struct{}, len(out))
	for _, opportunity := range out {
		if id := strings.ToLower(ukgProOpportunityID(opportunity)); id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, opportunity := range incoming {
		id := strings.ToLower(ukgProOpportunityID(opportunity))
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, opportunity)
	}
	return out
}

func ukgProOpportunityID(opportunity ukgProOpportunity) string {
	return firstNonEmptyString(opportunity.ID, opportunity.RequisitionNumber)
}

func ukgProJSArrayValue(page string, key string) (string, error) {
	start := jsPropertyValueOffset(page, key)
	if start < 0 {
		return "", fmt.Errorf("ukg hydrated %s array not found", key)
	}
	arrayStart := strings.Index(page[start:], "[")
	if arrayStart < 0 {
		return "", fmt.Errorf("ukg hydrated %s array start not found", key)
	}
	arrayStart += start
	return balancedJSArray(page, arrayStart)
}

func balancedJSArray(value string, start int) (string, error) {
	depth := 0
	arrayStart := -1
	inString := false
	escaped := false
	var quote byte
	for i := start; i < len(value); i++ {
		char := value[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == quote {
				inString = false
			}
			continue
		}
		switch char {
		case '"', '\'':
			inString = true
			quote = char
		case '[':
			if depth == 0 {
				arrayStart = i
			}
			depth++
		case ']':
			depth--
			if depth == 0 && arrayStart >= 0 {
				return value[arrayStart : i+1], nil
			}
			if depth < 0 {
				return "", errors.New("ukg hydrated array has mismatched brackets")
			}
		}
	}
	return "", errors.New("ukg hydrated array is unterminated")
}

func ukgProStringConfigValue(page string, key string) string {
	start := jsPropertyValueOffset(page, key)
	if start < 0 {
		return ""
	}
	rest := page[start:]
	quoteIndex := strings.IndexAny(rest, `"'`)
	if quoteIndex < 0 {
		return ""
	}
	quote := rest[quoteIndex]
	valueStart := quoteIndex
	escaped := false
	for i := quoteIndex + 1; i < len(rest); i++ {
		char := rest[i]
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if char == quote {
			raw := rest[valueStart : i+1]
			if unquoted, err := strconv.Unquote(raw); err == nil {
				return unquoted
			}
			return strings.Trim(raw, `"'`)
		}
	}
	return ""
}

func ukgProLoadSearchURL(rawURL string, page string) string {
	base, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	if raw := ukgProStringConfigValue(page, "loadUrl"); raw != "" {
		parsed, err := url.Parse(html.UnescapeString(raw))
		if err == nil {
			copy := *base.ResolveReference(parsed)
			copy.Fragment = ""
			return copy.String()
		}
	}
	config, err := ukgProConfigFromURL(rawURL)
	if err != nil {
		return ""
	}
	copy := *base
	copy.Path = "/" + path.Join(config.Account, "JobBoard", config.BoardID, "JobBoardView", "LoadSearchResults")
	copy.RawQuery = ""
	copy.Fragment = ""
	return copy.String()
}

func jsPropertyValueOffset(page string, key string) int {
	pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(key) + `\s*:`)
	match := pattern.FindStringIndex(page)
	if match == nil {
		return -1
	}
	return match[1]
}

func ukgProOpportunityDetailURL(rawURL string, template string, opportunityID string) string {
	opportunityID = strings.TrimSpace(opportunityID)
	if opportunityID == "" {
		return ""
	}
	base, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	var detail *url.URL
	if strings.TrimSpace(template) != "" {
		parsedTemplate, err := url.Parse(html.UnescapeString(template))
		if err == nil {
			detail = base.ResolveReference(parsedTemplate)
		}
	}
	if detail == nil {
		config, err := ukgProConfigFromURL(rawURL)
		if err != nil {
			return ""
		}
		copy := *base
		detail = &copy
		detail.Path = "/" + path.Join(config.Account, "JobBoard", config.BoardID, "OpportunityDetail")
	}
	query := detail.Query()
	query.Set("opportunityId", opportunityID)
	detail.RawQuery = query.Encode()
	detail.Fragment = ""
	return detail.String()
}

func ukgProPosting(source Source, config ukgProConfig, account string, detailTemplate string, evidenceText string, opportunity ukgProOpportunity) (JobPosting, bool) {
	id := ukgProOpportunityID(opportunity)
	title := strings.TrimSpace(opportunity.Title)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	applyURL := firstNonEmptyString(ukgProOpportunityDetailURL(source.URL, detailTemplate, id), source.URL)
	description := cleanHTMLText(opportunity.BriefDescription)
	location, country := ukgProLocationText(opportunity.Locations)
	evidence := []Evidence{
		{Field: "ats", Text: firstNonEmptyString(evidenceText, "UKG Pro Recruiting hydrated job board"), URL: source.URL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if opportunity.RequisitionNumber != "" {
		evidence = append(evidence, Evidence{Field: "requisition_number", Text: opportunity.RequisitionNumber, URL: applyURL})
	}
	if opportunity.JobCategoryName != "" {
		evidence = append(evidence, Evidence{Field: "job_category", Text: opportunity.JobCategoryName, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
	}

	return JobPosting{
		SourceJobID:    "ukg:" + account + ":" + id,
		Company:        sourceCompany(source, config.Account),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: ukgProEmploymentType(opportunity),
		RoleFamily:     inferRoleFamily(title + " " + description),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(opportunity.PostedDate),
		Live:           true,
		Confidence:     0.84,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func ukgProEmploymentType(opportunity ukgProOpportunity) string {
	if strings.Contains(strings.ToLower(opportunity.Title+" "+opportunity.JobCategoryName), "intern") {
		return "internship"
	}
	if opportunity.FullTime != nil {
		if *opportunity.FullTime {
			return "full_time"
		}
		return "part_time"
	}
	return employmentFromText(opportunity.Title, opportunity.JobCategoryName)
}

func ukgProLocationText(locations []ukgProLocation) (string, string) {
	parts := make([]string, 0, len(locations))
	country := ""
	for _, location := range locations {
		locationCountry := canonicalCountry(firstNonEmptyString(location.Address.Country.Code, location.Address.Country.Name))
		if country == "" {
			country = locationCountry
		}
		text := strings.Join(compactStringList(location.Address.City, location.Address.State.Code, locationCountry), ", ")
		if text == "" {
			text = strings.Join(compactStringList(location.LocalizedName, location.LocalizedDescription, locationCountry), ", ")
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(compactStringList(parts...), "; "), country
}

func dayforceConfigFromURL(rawURL string) (dayforceConfig, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return dayforceConfig{}, err
	}
	parts := nonEmptyPathParts(parsed)
	config := dayforceConfig{Culture: "en-US"}
	switch {
	case len(parts) >= 5 && dayforceLooksLikeCulture(parts[0]) && strings.EqualFold(parts[3], "jobs"):
		config.Culture = parts[0]
		config.ClientNamespace = parts[1]
		config.JobBoardCode = parts[2]
		config.JobID = parts[4]
	case len(parts) >= 4 && strings.EqualFold(parts[2], "jobs"):
		config.ClientNamespace = parts[0]
		config.JobBoardCode = parts[1]
		config.JobID = parts[3]
	case len(parts) >= 3 && dayforceLooksLikeCulture(parts[0]):
		config.Culture = parts[0]
		config.ClientNamespace = parts[1]
		config.JobBoardCode = parts[2]
	case len(parts) >= 2:
		config.ClientNamespace = parts[0]
		config.JobBoardCode = parts[1]
	default:
		return dayforceConfig{}, errors.New("dayforce client namespace and job board code are required")
	}
	if strings.TrimSpace(config.ClientNamespace) == "" || strings.TrimSpace(config.JobBoardCode) == "" {
		return dayforceConfig{}, errors.New("dayforce client namespace and job board code are required")
	}
	return config, nil
}

func dayforceLooksLikeCulture(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) == 2 {
		return true
	}
	return len(value) >= 5 && value[2] == '-'
}

func dayforceHostedJobURL(rawURL string, config dayforceConfig, jobID string) string {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return ""
	}
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	culture := firstNonEmptyString(config.Culture, "en-US")
	client := strings.TrimSpace(config.ClientNamespace)
	board := strings.TrimSpace(config.JobBoardCode)
	if client == "" || board == "" {
		return ""
	}
	copy := *parsed
	copy.Path = "/" + path.Join(culture, client, board, "jobs", jobID)
	copy.RawQuery = ""
	copy.Fragment = ""
	return copy.String()
}

func dayforceSearchEndpoint(rawURL string, config dayforceConfig) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	client := strings.TrimSpace(config.ClientNamespace)
	if client == "" {
		return nil, errors.New("dayforce client namespace is required")
	}
	copy := *parsed
	copy.Path = "/" + path.Join("api", "geo", client, "jobposting", "search")
	copy.RawQuery = ""
	copy.Fragment = ""
	return &copy, nil
}

func dayforceNextDataFromHTML(page string) (dayforceNextData, error) {
	match := nextDataScriptPattern.FindStringSubmatch(page)
	if len(match) < 2 {
		return dayforceNextData{}, errors.New("dayforce next data payload not found")
	}
	raw := strings.TrimSpace(match[1])
	var nextData dayforceNextData
	if err := json.Unmarshal([]byte(raw), &nextData); err == nil {
		return nextData, nil
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(raw)), &nextData); err != nil {
		return dayforceNextData{}, err
	}
	return nextData, nil
}

func dayforcePosting(source Source, config dayforceConfig, job dayforceJobData) (JobPosting, bool) {
	id := firstNonEmptyString(dayforceIntID(job.JobPostingID), config.JobID, dayforceIntID(job.JobReqID))
	title := strings.TrimSpace(job.JobTitle)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	description := dayforceDescription(job)
	location, country := dayforceLocationText(job.PostingLocations)
	jobFunction := dayforceAttribute(job, "JobFunction")
	payType := dayforceAttribute(job, "PayType")
	compensation := dayforceCompensationText(job)
	applyURL := firstNonEmptyString(dayforceHostedJobURL(source.URL, config, id), source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "Dayforce Next.js job detail payload", URL: source.URL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if job.JobReqID > 0 {
		evidence = append(evidence, Evidence{Field: "job_req_id", Text: strconv.FormatInt(job.JobReqID, 10), URL: applyURL})
	}
	if jobFunction != "" {
		evidence = append(evidence, Evidence{Field: "job_function", Text: jobFunction, URL: applyURL})
	}
	if payType != "" {
		evidence = append(evidence, Evidence{Field: "pay_type", Text: payType, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
	}
	if compensation != "" {
		evidence = append(evidence, Evidence{Field: "compensation", Text: compensation, URL: applyURL})
	}
	if job.PostingExpiryTimestampUTC != "" {
		evidence = append(evidence, Evidence{Field: "expires_at", Text: job.PostingExpiryTimestampUTC, URL: applyURL})
	}

	return JobPosting{
		SourceJobID:    "dayforce:" + stableAccountToken(config.ClientNamespace) + ":" + stableAccountToken(config.JobBoardCode) + ":" + id,
		Company:        sourceCompany(source, config.ClientNamespace),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, firstNonEmptyString(payType, jobFunction)),
		RoleFamily:     inferRoleFamily(title + " " + description + " " + jobFunction),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(firstNonEmptyString(job.PostingStartTimestampUTC, job.CreatedTimestampUTC, job.LastModifiedTimestampUTC)),
		Live:           job.PostingStatus == 0 || job.PostingStatus == 1,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func dayforceIntID(value int64) string {
	if value <= 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

func dayforceDescription(job dayforceJobData) string {
	return cleanHTMLText(strings.Join(compactStringList(
		job.JobPostingContent.JobDescriptionHeader,
		job.ShortDescription,
		job.Description,
		job.JobDescription,
		job.JobPostingContent.JobDescription,
		job.JobPostingContent.JobDescriptionFooter,
	), " "))
}

func dayforceAttribute(job dayforceJobData, name string) string {
	for _, attr := range job.JobPostingAttributes {
		if strings.EqualFold(strings.TrimSpace(attr.Name), name) {
			return dayforceAttributeValue(attr)
		}
	}
	return ""
}

func dayforceAttributeValue(attr dayforcePostingAttribute) string {
	if len(attr.Value) == 0 || string(attr.Value) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(attr.Value, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number float64
	if err := json.Unmarshal(attr.Value, &number); err == nil {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	var boolValue bool
	if err := json.Unmarshal(attr.Value, &boolValue); err == nil {
		return strconv.FormatBool(boolValue)
	}
	return strings.Trim(strings.TrimSpace(string(attr.Value)), `"`)
}

func dayforceCompensationText(job dayforceJobData) string {
	minimum := dayforceAttribute(job, "HiringMinRate")
	maximum := dayforceAttribute(job, "HiringMaxRate")
	currency := strings.TrimSpace(job.ISOCurrencyRegion)
	switch {
	case minimum != "" && maximum != "" && currency != "":
		return minimum + " to " + maximum + " " + currency
	case minimum != "" && maximum != "":
		return minimum + " to " + maximum
	case minimum != "" && currency != "":
		return minimum + "+ " + currency
	case minimum != "":
		return minimum + "+"
	case maximum != "" && currency != "":
		return "up to " + maximum + " " + currency
	case maximum != "":
		return "up to " + maximum
	default:
		return ""
	}
}

func dayforceLocationText(locations []dayforcePostingLocation) (string, string) {
	parts := make([]string, 0, len(locations))
	country := ""
	for _, location := range locations {
		locationCountry := canonicalCountry(location.ISOCountryCode)
		if country == "" {
			country = locationCountry
		}
		text := strings.Join(compactStringList(location.CityName, location.StateCode, locationCountry), ", ")
		if text == "" {
			text = cleanHTMLText(location.FormattedAddress)
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(compactStringList(parts...), "; "), country
}

func byteDanceKeyword(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	query := parsed.Query()
	return firstNonEmptyString(query.Get("keywords"), query.Get("keyword"), query.Get("q"), query.Get("query"))
}

func byteDancePosting(source Source, endpoint string, item byteDanceJobPost) (JobPosting, bool) {
	title := normalizeSpace(item.Title)
	id := firstNonEmptyString(item.ID, item.Code)
	if title == "" || id == "" {
		return JobPosting{}, false
	}
	description := cleanHTMLText(strings.Join(compactStringList(item.Description, item.Requirement), " "))
	location, country := byteDanceLocationText(item.CityInfo)
	context := strings.Join(compactStringList(title, description, item.RecruitType.ENName, item.JobCategory.ENName, item.JobSubject.ENName, item.DepartmentInfo.ENName), " ")
	timingContext := strings.Join(compactStringList(title, item.RecruitType.ENName, item.JobSubject.ENName), " ")
	applyURL := byteDanceApplyURL(id)
	return JobPosting{
		SourceJobID:    "bytedance_careers:" + id,
		Company:        sourceCompany(source, "ByteDance"),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, firstNonEmptyString(item.RecruitType.ENName, item.JobSubject.ENName)),
		Level:          inferLevel(timingContext),
		RoleFamily:     inferRoleFamily(context),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		Live:           true,
		Confidence:     0.88,
		Strategy:       TierATS,
		Evidence: []Evidence{
			{Field: "ats", Text: "ByteDance public supplier search API", URL: endpoint},
			{Field: "description", Text: description, URL: applyURL},
			{Field: "department", Text: firstNonEmptyString(item.DepartmentInfo.ENName, item.JobCategory.ENName), URL: applyURL},
			{Field: "location", Text: location, URL: applyURL},
			{Field: "job_subject", Text: item.JobSubject.ENName, URL: applyURL},
		},
	}, true
}

func byteDanceApplyURL(id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	return "https://jobs.bytedance.com/en/position/" + strings.TrimSpace(id)
}

func byteDanceLocationText(loc byteDanceLocation) (string, string) {
	parts := make([]string, 0, 3)
	country := ""
	for current := &loc; current != nil; current = current.Parent {
		name := firstNonEmptyString(current.ENName, current.Name)
		if name == "" {
			continue
		}
		if current.Parent == nil {
			country = canonicalCountry(name)
		}
		parts = append(parts, canonicalCountry(name))
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(compactStringList(parts...), ", "), country
}

func imcCareersPosting(source Source, path string, id string, cardHTML string) (JobPosting, bool) {
	title := ""
	if match := imcCareersTitlePattern.FindStringSubmatch(cardHTML); len(match) > 1 {
		title = cleanHTMLText(match[1])
	}
	id = strings.TrimSpace(id)
	if title == "" || id == "" {
		return JobPosting{}, false
	}
	applyURL := imcCareersAbsoluteURL(source.URL, strings.TrimSpace(path)+"/apply")
	jobURL := imcCareersAbsoluteURL(source.URL, strings.TrimSpace(path))
	location := imcCareersLocation(cardHTML)
	context := strings.Join(compactStringList(title, location), " ")
	return JobPosting{
		SourceJobID:    "imc_careers:" + id,
		Company:        sourceCompany(source, "IMC"),
		Title:          title,
		Location:       location,
		Country:        canonicalCountry(location),
		EmploymentType: employmentFromText(title, ""),
		Level:          inferLevel(context),
		RoleFamily:     inferRoleFamily(context),
		SourceURL:      source.URL,
		ApplyURL:       firstNonEmptyString(applyURL, jobURL, source.URL),
		Live:           true,
		Confidence:     0.78,
		Strategy:       TierATS,
		Evidence: []Evidence{
			{Field: "ats", Text: "IMC careers server-rendered job card", URL: source.URL},
			{Field: "location", Text: location, URL: jobURL},
		},
	}, true
}

func imcCareersLocation(cardHTML string) string {
	values := make([]string, 0, 4)
	for _, match := range imcCareersSpanPattern.FindAllStringSubmatch(cardHTML, -1) {
		if len(match) < 2 {
			continue
		}
		text := cleanHTMLText(match[1])
		if text == "" || strings.EqualFold(text, "Apply Now") {
			continue
		}
		values = append(values, text)
	}
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func imcCareersAbsoluteURL(baseURL string, ref string) string {
	parsed, err := parseSourceURL(baseURL)
	if err != nil {
		return ref
	}
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return ref
	}
	return parsed.ResolveReference(u).String()
}

func oracleRecruitingConfigFromURL(rawURL string) (oracleRecruitingConfig, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return oracleRecruitingConfig{}, err
	}
	config := oracleRecruitingConfig{Culture: "en"}
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		switch {
		case strings.EqualFold(part, "CandidateExperience") && i+1 < len(parts):
			config.Culture = parts[i+1]
		case strings.EqualFold(part, "sites") && i+1 < len(parts):
			config.SiteNumber = parts[i+1]
		case strings.EqualFold(part, "job") && i+1 < len(parts):
			config.JobID = parts[i+1]
		}
	}
	if config.SiteNumber == "" {
		config.SiteNumber = oracleRecruitingSiteNumberFromFinder(parsed.Query().Get("finder"))
	}
	if config.SiteNumber == "" {
		config.SiteNumber = strings.TrimSpace(parsed.Query().Get("siteNumber"))
	}
	if config.SiteNumber == "" {
		return oracleRecruitingConfig{}, errors.New("oracle recruiting site number is required")
	}
	return config, nil
}

func oracleRecruitingSiteNumberFromFinder(finder string) string {
	for _, part := range strings.FieldsFunc(finder, func(r rune) bool {
		return r == ';' || r == ','
	}) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "siteNumber") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func oracleRecruitingSearchURL(rawURL string, config oracleRecruitingConfig, limit int, offset int) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	endpoint := *parsed
	endpoint.Path = "/hcmRestApi/resources/latest/recruitingCEJobRequisitions"
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	query := endpoint.Query()
	query.Set("onlyData", "true")
	query.Set("expand", "all")
	finder := []string{
		"siteNumber=" + strings.TrimSpace(config.SiteNumber),
		"limit=" + strconv.Itoa(limit),
		"offset=" + strconv.Itoa(offset),
	}
	if keyword := strings.TrimSpace(parsed.Query().Get("keyword")); keyword != "" {
		finder = append(finder, "keyword="+keyword)
	}
	query.Set("finder", "findReqs;"+strings.Join(finder, ","))
	endpoint.RawQuery = query.Encode()
	return &endpoint, nil
}

func oracleRecruitingRequisitions(payload oracleRecruitingResponse) ([]oracleRecruitingRequisition, int) {
	total := 0
	requisitions := make([]oracleRecruitingRequisition, 0)
	for _, item := range payload.Items {
		if total == 0 {
			total = item.TotalJobsCount
		}
		requisitions = append(requisitions, item.Requisitions...)
	}
	return requisitions, total
}

func oracleRecruitingJobURL(rawURL string, config oracleRecruitingConfig, jobID string) string {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return ""
	}
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	culture := firstNonEmptyString(config.Culture, "en")
	copy := *parsed
	copy.Path = "/" + path.Join("hcmUI", "CandidateExperience", culture, "sites", config.SiteNumber, "job", jobID)
	copy.RawQuery = ""
	copy.Fragment = ""
	return copy.String()
}

func oracleRecruitingDetailFromHTML(page string) oracleRecruitingDetail {
	return oracleRecruitingDetail{
		Title:       htmlMetaContent(page, "og:title"),
		Description: htmlMetaContent(page, "og:description"),
		SiteName:    htmlMetaContent(page, "og:site_name"),
	}
}

func oracleRecruitingPosting(source Source, config oracleRecruitingConfig, req oracleRecruitingRequisition, detailURL string, detail oracleRecruitingDetail) (JobPosting, bool) {
	id := strings.TrimSpace(req.ID)
	title := firstNonEmptyString(detail.Title, req.Title)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	description := oracleRecruitingDescription(req, detail)
	location := firstNonEmptyString(req.PrimaryLocation)
	country := canonicalCountry(req.PrimaryLocationCountry)
	applyURL := firstNonEmptyString(detailURL, oracleRecruitingJobURL(source.URL, config, id), source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "Oracle Recruiting Candidate Experience public requisitions API", URL: source.URL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
	}
	for _, field := range []struct {
		name string
		text string
	}{
		{name: "job_function", text: req.JobFunction},
		{name: "job_family", text: req.JobFamily},
		{name: "worker_type", text: req.WorkerType},
		{name: "job_schedule", text: req.JobSchedule},
		{name: "workplace_type", text: firstNonEmptyString(req.WorkplaceType, req.WorkplaceTypeCode)},
		{name: "organization", text: firstNonEmptyString(req.Organization, req.Department, req.BusinessUnit, req.LegalEmployer)},
		{name: "posting_end_date", text: req.PostingEndDate},
	} {
		if field.text != "" {
			evidence = append(evidence, Evidence{Field: field.name, Text: field.text, URL: applyURL})
		}
	}
	if req.HotJobFlag {
		evidence = append(evidence, Evidence{Field: "hot_job", Text: "true", URL: applyURL})
	}
	if req.TrendingFlag {
		evidence = append(evidence, Evidence{Field: "trending", Text: "true", URL: applyURL})
	}

	return JobPosting{
		SourceJobID:    "oracle_recruiting:" + stableAccountToken(config.SiteNumber) + ":" + id,
		Company:        sourceCompany(source, firstNonEmptyString(detail.SiteName, req.LegalEmployer, req.Organization, config.SiteNumber)),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: oracleRecruitingEmploymentType(title, req),
		RoleFamily:     inferRoleFamily(title + " " + description + " " + req.JobFunction),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(req.PostedDate),
		Live:           true,
		Confidence:     0.85,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func oracleRecruitingDescription(req oracleRecruitingRequisition, detail oracleRecruitingDetail) string {
	return cleanHTMLText(firstNonEmptyString(
		detail.Description,
		req.ShortDescriptionStr,
		strings.Join(compactStringList(req.ExternalResponsibilitiesStr, req.ExternalQualificationsStr), " "),
	))
}

func oracleRecruitingEmploymentType(title string, req oracleRecruitingRequisition) string {
	combined := strings.ToLower(strings.Join(compactStringList(title, req.WorkerType, req.ContractType, req.JobSchedule, req.JobType), " "))
	switch {
	case strings.Contains(combined, "intern"):
		return "internship"
	case strings.Contains(combined, "contract"):
		return "contract"
	case strings.Contains(combined, "part time"), strings.Contains(combined, "part-time"):
		return "part_time"
	case strings.Contains(combined, "full time"), strings.Contains(combined, "full-time"):
		return "full_time"
	default:
		return employmentFromText(title, firstNonEmptyString(req.WorkerType, req.ContractType, req.JobSchedule, req.JobType))
	}
}

func htmlMetaContent(page string, name string) string {
	pattern := regexp.MustCompile(`(?is)<meta\b[^>]*(?:property|name)=["']` + regexp.QuoteMeta(name) + `["'][^>]*>`)
	tag := pattern.FindString(page)
	if tag == "" {
		return ""
	}
	return html.UnescapeString(htmlAttrValue(tag, "content"))
}

func htmlAttrValue(tag string, attr string) string {
	for _, quote := range []string{`"`, `'`} {
		pattern := regexp.MustCompile(`(?is)\b` + regexp.QuoteMeta(attr) + `\s*=\s*` + regexp.QuoteMeta(quote) + `([^` + quote + `]*)` + regexp.QuoteMeta(quote))
		if match := pattern.FindStringSubmatch(tag); len(match) > 1 {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

func paylocityConfigFromURL(rawURL string) (paylocityConfig, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return paylocityConfig{}, err
	}
	parts := nonEmptyPathParts(parsed)
	config := paylocityConfig{}
	for i, part := range parts {
		switch {
		case strings.EqualFold(part, "All") && i+1 < len(parts):
			config.FeedID = parts[i+1]
			if i+2 < len(parts) {
				config.CompanySlug = parts[i+2]
			}
		case strings.EqualFold(part, "Details") || strings.EqualFold(part, "Apply"):
			if i+1 < len(parts) {
				config.JobID = parts[i+1]
			}
			if i+2 < len(parts) {
				config.CompanySlug = parts[i+2]
			}
		case strings.EqualFold(part, "jobs") && i > 0 && strings.EqualFold(parts[i-1], "feed") && i+1 < len(parts):
			config.FeedID = parts[i+1]
			config.FeedAPI = true
		}
	}
	if config.FeedID == "" && config.JobID == "" {
		return paylocityConfig{}, errors.New("paylocity feed id or job id is required")
	}
	return config, nil
}

func paylocityPageDataFromHTML(page string) (paylocityPageData, error) {
	start := strings.Index(page, "window.pageData")
	if start < 0 {
		return paylocityPageData{}, errors.New("paylocity pageData not found")
	}
	objectStart := strings.Index(page[start:], "{")
	if objectStart < 0 {
		return paylocityPageData{}, errors.New("paylocity pageData object not found")
	}
	objectText, err := balancedJSObject(page, start+objectStart)
	if err != nil {
		return paylocityPageData{}, err
	}
	var data paylocityPageData
	if err := json.Unmarshal([]byte(objectText), &data); err != nil {
		return paylocityPageData{}, err
	}
	return data, nil
}

func paylocityFeedFromPayload(payload []byte) (paylocityFeedData, error) {
	trimmed := trimPaylocityPayload(payload)
	if len(trimmed) == 0 {
		return paylocityFeedData{}, errors.New("paylocity feed payload is empty")
	}
	if trimmed[0] == '<' {
		return paylocityFeedFromXML(trimmed)
	}
	return paylocityFeedFromJSON(trimmed)
}

func paylocityFeedFromJSON(payload json.RawMessage) (paylocityFeedData, error) {
	trimmed := trimPaylocityPayload(payload)
	if len(trimmed) == 0 {
		return paylocityFeedData{}, errors.New("paylocity feed payload is empty")
	}
	if trimmed[0] == '[' {
		var jobs []paylocityFeedJob
		if err := json.Unmarshal(trimmed, &jobs); err != nil {
			return paylocityFeedData{}, err
		}
		return paylocityFeedData{Format: "JSON", Jobs: jobs}, nil
	}
	var data paylocityFeedData
	if err := json.Unmarshal(trimmed, &data); err != nil {
		return paylocityFeedData{}, err
	}
	data.Format = "JSON"
	return data, nil
}

func paylocityFeedFromXML(payload []byte) (paylocityFeedData, error) {
	trimmed := trimPaylocityPayload(payload)
	if len(trimmed) == 0 {
		return paylocityFeedData{}, errors.New("paylocity feed payload is empty")
	}
	var data paylocityFeedData
	if err := xml.Unmarshal(trimmed, &data); err != nil {
		return paylocityFeedData{}, err
	}
	data.Format = "XML"
	return data, nil
}

func trimPaylocityPayload(payload []byte) []byte {
	trimmed := bytes.TrimSpace(payload)
	trimmed = bytes.TrimPrefix(trimmed, []byte("\xef\xbb\xbf"))
	return bytes.TrimSpace(trimmed)
}

func balancedJSObject(value string, start int) (string, error) {
	depth := 0
	objectStart := -1
	inString := false
	escaped := false
	var quote byte
	for i := start; i < len(value); i++ {
		char := value[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == quote {
				inString = false
			}
			continue
		}
		switch char {
		case '"', '\'':
			inString = true
			quote = char
		case '{':
			if depth == 0 {
				objectStart = i
			}
			depth++
		case '}':
			depth--
			if depth == 0 && objectStart >= 0 {
				return value[objectStart : i+1], nil
			}
			if depth < 0 {
				return "", errors.New("paylocity pageData object has mismatched braces")
			}
		}
	}
	return "", errors.New("paylocity pageData object is unterminated")
}

func phenomPeopleConfigFromHTML(page string) (phenomPeopleConfig, error) {
	objectText, err := phenomPeopleObjectFromHTML(page, []string{"var phApp = phApp ||", "phApp = phApp ||"}, "phenom phApp config")
	if err != nil {
		return phenomPeopleConfig{}, err
	}
	var config phenomPeopleConfig
	if err := unmarshalEmbeddedJSONObject(objectText, &config); err != nil {
		return phenomPeopleConfig{}, err
	}
	if config.RefNum == "" && config.BaseURL == "" && config.BaseDomain == "" {
		return phenomPeopleConfig{}, errors.New("phenom phApp config is empty")
	}
	return config, nil
}

func phenomPeopleDDOFromHTML(page string) (phenomPeopleDDO, error) {
	objectText, err := phenomPeopleObjectFromHTML(page, []string{"phApp.ddo ="}, "phenom ddo")
	if err != nil {
		return phenomPeopleDDO{}, err
	}
	var ddo phenomPeopleDDO
	if err := unmarshalEmbeddedJSONObject(objectText, &ddo); err != nil {
		return phenomPeopleDDO{}, err
	}
	return ddo, nil
}

func phenomPeopleObjectFromHTML(page string, markers []string, label string) (string, error) {
	for _, marker := range markers {
		start := strings.Index(page, marker)
		if start < 0 {
			continue
		}
		objectStart := strings.Index(page[start:], "{")
		if objectStart < 0 {
			return "", fmt.Errorf("%s object not found", label)
		}
		objectText, err := balancedJSObject(page, start+objectStart)
		if err != nil {
			return "", err
		}
		return objectText, nil
	}
	return "", fmt.Errorf("%s not found", label)
}

func unmarshalEmbeddedJSONObject(objectText string, out any) error {
	if err := json.Unmarshal([]byte(objectText), out); err == nil {
		return nil
	} else if unescaped := html.UnescapeString(objectText); unescaped != objectText {
		return json.Unmarshal([]byte(unescaped), out)
	} else {
		return err
	}
}

func phenomPeopleSearchURL(rawURL string, from int) (string, error) {
	endpoint, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	q := endpoint.Query()
	q.Set("from", strconv.Itoa(max(0, from)))
	q.Set("s", "1")
	endpoint.RawQuery = q.Encode()
	return endpoint.String(), nil
}

func phenomPeopleJobURL(config phenomPeopleConfig, hit phenomPeopleJob) string {
	id := strings.TrimSpace(hit.JobSeqNo)
	if id == "" {
		return ""
	}
	base := firstNonEmptyString(config.BaseURL, config.BaseDomain)
	if base == "" {
		return ""
	}
	endpoint, err := joinURL(base, "job", id)
	if err != nil {
		return ""
	}
	return endpoint.String()
}

func phenomPeoplePosting(source Source, config phenomPeopleConfig, hit phenomPeopleJob) (JobPosting, bool) {
	id := firstNonEmptyString(hit.JobSeqNo, hit.JobID, hit.ReqID)
	title := strings.TrimSpace(hit.Title)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	description := cleanHTMLText(firstNonEmptyString(
		hit.DescriptionTeaser,
		hit.MLJobParser.DescriptionTeaser,
		hit.MLJobParser.DescriptionTeaserKeyword,
		hit.MLJobParser.DescriptionTeaserFirst,
		hit.MLJobParser.DescriptionTeaserATS,
	))
	requirements := cleanHTMLText(firstNonEmptyString(
		hit.MLJobParser.DescriptionTeaserATS,
		hit.MLJobParser.DescriptionTeaserKeyword,
		hit.MLJobParser.DescriptionTeaserFirst,
	))
	location, country := phenomPeopleLocation(hit)
	applyURL := firstNonEmptyString(hit.ApplyURL, phenomPeopleJobURL(config, hit), source.URL)
	evidenceURL := firstNonEmptyString(applyURL, source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "Phenom People embedded search-results DDO", URL: source.URL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: evidenceURL})
	}
	if requirements != "" && requirements != description {
		evidence = append(evidence, Evidence{Field: "requirements", Text: requirements, URL: evidenceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: evidenceURL})
	}
	if hit.Category != "" {
		evidence = append(evidence, Evidence{Field: "category", Text: hit.Category, URL: evidenceURL})
	}
	if hit.Department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: hit.Department, URL: evidenceURL})
	}
	if hit.ReqID != "" {
		evidence = append(evidence, Evidence{Field: "req_id", Text: hit.ReqID, URL: evidenceURL})
	}
	account := stableAccountToken(firstNonEmptyString(config.RefNum, sourceHost(source.URL), sourceCompany(source, "")))
	if account == "" {
		account = "phenom"
	}
	return JobPosting{
		SourceJobID:    "phenom_people:" + account + ":" + id,
		Company:        sourceCompany(source, config.RefNum),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, firstNonEmptyString(hit.Type, hit.Category, hit.Department)),
		RoleFamily:     inferRoleFamily(title + " " + description + " " + hit.Category + " " + hit.Department),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(firstNonEmptyString(hit.PostedDate, hit.DateCreated)),
		Live:           true,
		Confidence:     0.84,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func phenomPeopleLocation(hit phenomPeopleJob) (string, string) {
	location := firstNonEmptyString(
		hit.Location,
		strings.Join(compactStringList(hit.MultiLocation...), "; "),
		strings.Join(compactStringList(hit.City, hit.State, hit.Country), ", "),
	)
	country := firstNonEmptyString(canonicalCountry(hit.Country), adpWorkforceNowCountry(location))
	return location, country
}

func appleJobsHydrationFromHTML(page string) (appleJobsHydration, error) {
	payload, err := appleJobsHydrationPayload(page)
	if err != nil {
		return appleJobsHydration{}, err
	}
	var data appleJobsHydration
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return appleJobsHydration{}, err
	}
	return data, nil
}

func appleJobsPostingsFromHTML(page string) ([]appleJobsPosting, error) {
	payload, err := appleJobsHydrationPayload(page)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return nil, err
	}
	return appleJobsPostingsFromJSON(raw), nil
}

func appleJobsPostingsFromJSON(raw json.RawMessage) []appleJobsPosting {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil && len(object) > 0 {
		var item appleJobsPosting
		if err := json.Unmarshal(raw, &item); err == nil && item.PostingTitle != "" && firstNonEmptyString(item.ID, item.ReqID, item.JobPositionID, item.PositionID) != "" {
			return []appleJobsPosting{item}
		}
		var postings []appleJobsPosting
		for _, child := range object {
			postings = append(postings, appleJobsPostingsFromJSON(child)...)
		}
		return postings
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err == nil {
		postings := make([]appleJobsPosting, 0, len(list))
		for _, child := range list {
			postings = append(postings, appleJobsPostingsFromJSON(child)...)
		}
		return postings
	}
	return nil
}

func appleJobsHydrationPayload(page string) (string, error) {
	start := strings.Index(page, "window.__staticRouterHydrationData")
	if start < 0 {
		return "", errors.New("apple jobs hydration script not found")
	}
	parseStart := strings.Index(page[start:], "JSON.parse")
	if parseStart < 0 {
		return "", errors.New("apple jobs hydration JSON.parse call not found")
	}
	i := start + parseStart + len("JSON.parse")
	for i < len(page) && (page[i] == ' ' || page[i] == '\n' || page[i] == '\t' || page[i] == '\r') {
		i++
	}
	if i >= len(page) || page[i] != '(' {
		return "", errors.New("apple jobs hydration JSON.parse opening paren not found")
	}
	i++
	for i < len(page) && (page[i] == ' ' || page[i] == '\n' || page[i] == '\t' || page[i] == '\r') {
		i++
	}
	if i >= len(page) || page[i] != '"' {
		return "", errors.New("apple jobs hydration string literal not found")
	}
	end := i + 1
	escaped := false
	for ; end < len(page); end++ {
		char := page[end]
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if char == '"' {
			break
		}
	}
	if end >= len(page) {
		return "", errors.New("apple jobs hydration string literal is unterminated")
	}
	var payload string
	if err := json.Unmarshal([]byte(page[i:end+1]), &payload); err != nil {
		return "", err
	}
	return payload, nil
}

func appleJobsMergePosting(summary appleJobsPosting, detail appleJobsPosting) appleJobsPosting {
	merged := summary
	if detail.ID != "" {
		merged.ID = detail.ID
	}
	if detail.JobSummary != "" {
		merged.JobSummary = detail.JobSummary
	}
	if len(detail.Locations) > 0 {
		merged.Locations = detail.Locations
	}
	if detail.PositionID != "" {
		merged.PositionID = detail.PositionID
	}
	if detail.PostingDate != "" {
		merged.PostingDate = detail.PostingDate
	}
	if detail.PostingTitle != "" {
		merged.PostingTitle = detail.PostingTitle
	}
	if detail.PostDateInGMT != "" {
		merged.PostDateInGMT = detail.PostDateInGMT
	}
	if detail.TransformedPostingTitle != "" {
		merged.TransformedPostingTitle = detail.TransformedPostingTitle
	}
	if detail.ReqID != "" {
		merged.ReqID = detail.ReqID
	}
	if detail.Team.TeamName != "" || detail.Team.TeamID != "" || detail.Team.TeamCode != "" {
		merged.Team = detail.Team
	}
	if detail.Type != "" {
		merged.Type = detail.Type
	}
	if detail.HomeOffice {
		merged.HomeOffice = true
	}
	if detail.JobPositionID != "" {
		merged.JobPositionID = detail.JobPositionID
	}
	if detail.PostExternal {
		merged.PostExternal = true
	}
	if detail.StandardWeeklyHours > 0 {
		merged.StandardWeeklyHours = detail.StandardWeeklyHours
	}
	return merged
}

func stripeJobsPostings(source Source, pageURL *url.URL, document string, maxJobs int) []JobPosting {
	rows := stripeJobsRowPattern.FindAllStringSubmatch(document, -1)
	jobs := make([]JobPosting, 0, min(len(rows), maxJobs))
	seen := map[string]struct{}{}
	for _, rowMatch := range rows {
		if len(jobs) >= maxJobs || len(rowMatch) < 2 {
			break
		}
		row := rowMatch[1]
		linkMatch := stripeJobsLinkPattern.FindStringSubmatch(row)
		if len(linkMatch) < 3 {
			continue
		}
		href := html.UnescapeString(strings.TrimSpace(linkMatch[1]))
		if !strings.Contains(href, "/jobs/listing/") {
			continue
		}
		title := cleanHTMLText(linkMatch[2])
		if title == "" {
			continue
		}
		applyURL := stripeJobsResolveURL(pageURL, href)
		id := firstNonEmptyString(stripeJobsListingID(applyURL), stableJobToken(applyURL, title))
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}

		department := cleanHTMLText(firstRegexpGroup(stripeJobsDepartmentPattern, row))
		location, country := stripeJobsLocation(row)
		context := strings.Join(compactStringList(title, department, location), " ")
		evidence := []Evidence{
			{Field: "ats", Text: "Stripe official jobs search table", URL: pageURL.String()},
		}
		if department != "" {
			evidence = append(evidence, Evidence{Field: "department", Text: department, URL: applyURL})
		}
		if location != "" {
			evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
		}
		jobs = append(jobs, JobPosting{
			SourceJobID:    "stripe_jobs:" + id,
			Company:        sourceCompany(source, "Stripe"),
			Title:          title,
			Location:       location,
			Country:        country,
			EmploymentType: employmentFromText(title, department),
			Level:          inferLevel(context),
			RoleFamily:     inferRoleFamily(context),
			SourceURL:      source.URL,
			ApplyURL:       firstNonEmptyString(applyURL, source.URL),
			Live:           true,
			Confidence:     0.84,
			Strategy:       TierATS,
			Evidence:       evidence,
		})
	}
	return jobs
}

func stripeJobsResolveURL(baseURL *url.URL, href string) string {
	ref, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return ""
	}
	return baseURL.ResolveReference(ref).String()
}

func stripeJobsListingID(applyURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(applyURL))
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		if strings.EqualFold(part, "listing") && i+2 < len(parts) {
			return parts[i+2]
		}
	}
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

func stripeJobsLocation(row string) (string, string) {
	country := canonicalCountry(firstRegexpGroup(stripeJobsCountryPattern, row))
	location := cleanHTMLText(firstRegexpGroup(stripeJobsLocationPattern, row))
	if location == "" {
		return country, country
	}
	if country == "" || strings.EqualFold(canonicalCountry(location), country) {
		return location, country
	}
	return strings.Join(compactStringList(location, country), ", "), country
}

func appleJobsJobPosting(source Source, locale string, item appleJobsPosting) (JobPosting, bool) {
	id := firstNonEmptyString(item.ID, item.ReqID, item.JobPositionID, item.PositionID)
	title := strings.TrimSpace(item.PostingTitle)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	description := cleanHTMLText(item.JobSummary)
	location, country := appleJobsLocationSummary(item)
	company := sourceCompany(source, "Apple")
	applyURL := appleJobsDetailURL(source, locale, item)
	evidenceURL := firstNonEmptyString(applyURL, source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "Apple Jobs static-router hydration data", URL: source.URL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: evidenceURL})
	}
	if item.Team.TeamName != "" {
		evidence = append(evidence, Evidence{Field: "team", Text: item.Team.TeamName, URL: evidenceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: evidenceURL})
	}
	if item.ReqID != "" {
		evidence = append(evidence, Evidence{Field: "req_id", Text: item.ReqID, URL: evidenceURL})
	}
	if item.StandardWeeklyHours > 0 {
		evidence = append(evidence, Evidence{Field: "weekly_hours", Text: strconv.FormatFloat(item.StandardWeeklyHours, 'f', -1, 64) + " hours", URL: evidenceURL})
	}
	account := stableAccountToken(company)
	if account == "" {
		account = "apple"
	}
	return JobPosting{
		SourceJobID:    "apple_jobs:" + account + ":" + id,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: appleJobsEmploymentType(item),
		RoleFamily:     inferRoleFamily(title + " " + description + " " + item.Team.TeamName),
		SourceURL:      source.URL,
		ApplyURL:       firstNonEmptyString(applyURL, source.URL),
		PostedAt:       appleJobsPostedAt(item),
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func appleJobsEmploymentType(item appleJobsPosting) string {
	lower := strings.ToLower(item.PostingTitle + " " + item.JobSummary)
	switch {
	case strings.Contains(lower, "intern"):
		return "internship"
	case strings.Contains(lower, "part-time"), strings.Contains(lower, "part time"):
		return "part_time"
	case item.StandardWeeklyHours > 0 && item.StandardWeeklyHours < 35:
		return "part_time"
	default:
		return "full_time"
	}
}

func appleJobsLocationSummary(item appleJobsPosting) (string, string) {
	locations := make([]string, 0, len(item.Locations))
	country := ""
	for _, loc := range item.Locations {
		if country == "" {
			country = appleJobsCountry(loc)
		}
		if text := appleJobsLocationText(loc); text != "" {
			locations = append(locations, text)
		}
	}
	if len(locations) == 0 && item.HomeOffice {
		locations = append(locations, "Remote")
	}
	return strings.Join(compactStringList(locations...), "; "), country
}

func appleJobsLocationText(loc appleJobsLocation) string {
	country := appleJobsCountry(loc)
	place := firstNonEmptyString(loc.Name, loc.City, loc.Metro, loc.Region)
	state := strings.TrimSpace(loc.StateProvince)
	if place == "" && state == "" {
		return country
	}
	if state == "" && country != "" && canonicalCountry(place) == country {
		return country
	}
	return strings.Join(compactStringList(place, state, country), ", ")
}

func appleJobsCountry(loc appleJobsLocation) string {
	country := canonicalCountry(loc.CountryName)
	code := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(loc.CountryID), "iso-country-"))
	switch code {
	case "USA":
		return "US"
	case "GBR":
		return "UK"
	case "SGP":
		return "Singapore"
	case "HKG":
		return "Hong Kong"
	case "CAN":
		return "Canada"
	}
	if country != "" {
		return country
	}
	return canonicalCountry(code)
}

func appleJobsDetailURL(source Source, locale string, item appleJobsPosting) string {
	id := firstNonEmptyString(item.ID, item.ReqID, item.JobPositionID, item.PositionID)
	if id == "" {
		return source.URL
	}
	slug := firstNonEmptyString(item.TransformedPostingTitle, appleJobsSlug(item.PostingTitle), "job")
	parsed, err := parseSourceURL(source.URL)
	if err != nil {
		return source.URL
	}
	parsed.Path = path.Join("/", appleJobsLocaleFromURL(parsed, locale), "details", id, slug)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if team := strings.TrimSpace(item.Team.TeamCode); team != "" {
		q := parsed.Query()
		q.Set("team", team)
		parsed.RawQuery = q.Encode()
	}
	return parsed.String()
}

func appleJobsLocaleFromURLString(rawURL string, fallback string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return firstNonEmptyString(strings.ToLower(strings.TrimSpace(fallback)), "en-us")
	}
	return appleJobsLocaleFromURL(parsed, fallback)
}

func appleJobsLocaleFromURL(parsed *url.URL, fallback string) string {
	parts := nonEmptyPathParts(parsed)
	if len(parts) > 0 && strings.Contains(parts[0], "-") {
		return strings.ToLower(parts[0])
	}
	return firstNonEmptyString(strings.ToLower(strings.TrimSpace(fallback)), "en-us")
}

func appleJobsSlug(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	lastDash := false
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			builder.WriteRune(char)
			lastDash = false
		default:
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(builder.String(), "-")
}

func appleJobsPostedAt(item appleJobsPosting) *time.Time {
	if postedAt := parseTimePtr(item.PostDateInGMT); postedAt != nil {
		return postedAt
	}
	if parsed, err := time.Parse("Jan 2, 2006", strings.TrimSpace(item.PostingDate)); err == nil {
		utc := parsed.UTC()
		return &utc
	}
	return nil
}

func amazonJobsSearchEndpoint(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parsed.Path = "/api/jobs/search"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func amazonJobsSearchPayload(rawURL string, start int, size int) amazonJobsSearchRequest {
	return amazonJobsSearchRequest{
		JobPostingSearchRequest: amazonJobsSearchParams{
			Query: amazonJobsQuery(rawURL),
			ExcludeFacets: []amazonJobsFacet{
				{Name: "businessCategory", Values: []amazonJobsFacetValue{{Name: "a-confidential-job"}}},
				{Name: "isPostedExternally", Values: []amazonJobsFacetValue{{Name: "0"}}},
				{Name: "isUnsearchable", Values: []amazonJobsFacetValue{{Name: "1"}}},
			},
			FilterFacets:        []amazonJobsFacet{},
			Start:               start,
			Size:                size,
			Sort:                amazonJobsSearchSort{SortOrder: "DESCENDING", SortType: "SCORE"},
			Location:            nil,
			AccessLevel:         "EXTERNAL",
			IncludeFacets:       []amazonJobsFacet{},
			LocationFacets:      []amazonJobsFacet{},
			JobTypeFacets:       []amazonJobsFacet{},
			ContentFilterFacets: []amazonJobsFacet{},
			Treatment:           "OM",
		},
	}
}

func amazonJobsQuery(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	query := parsed.Query()
	for _, key := range []string{"base_query", "query", "q", "keyword", "keywords", "search"} {
		if value := normalizeSpace(query.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func sourceSearchText(source Source) string {
	for _, key := range []string{"search_query", "query", "q", "base_query", "keyword", "keywords", "search"} {
		if value := normalizeSpace(source.Metadata[key]); value != "" {
			return value
		}
	}
	parsed, err := parseSourceURL(source.URL)
	if err != nil {
		return ""
	}
	query := parsed.Query()
	for _, key := range []string{"base_query", "query", "q", "keyword", "keywords", "search"} {
		if value := normalizeSpace(query.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func (e *ATSExtractor) amazonJobsDetailPosting(ctx context.Context, source Source, hit amazonJobsHit) (JobPosting, error) {
	detailURL := amazonJobsDetailURL(source, hit)
	if detailURL == "" || detailURL == source.URL {
		return JobPosting{}, errors.New("amazon jobs detail URL unavailable")
	}
	document, err := e.getText(ctx, detailURL, "text/html,application/xhtml+xml")
	if err != nil {
		return JobPosting{}, err
	}
	parsed, err := parseSourceURL(detailURL)
	if err != nil {
		return JobPosting{}, err
	}
	staticExtractor := NewStaticExtractor()
	jobs := staticExtractor.extractJSONLDJobs(source, parsed, document)
	if len(jobs) == 0 {
		return JobPosting{}, ErrNoJobs
	}
	id := amazonJobsField(hit, "icimsJobId", "jobId", "id")
	for _, job := range jobs {
		if strings.Contains(job.ApplyURL, id) || strings.Contains(job.SourceJobID, id) {
			return job, nil
		}
	}
	return jobs[0], nil
}

func amazonJobsJobPosting(source Source, hit amazonJobsHit, detail JobPosting) (JobPosting, bool) {
	id := amazonJobsField(hit, "icimsJobId", "jobId", "id")
	title := amazonJobsField(hit, "title")
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	description := firstNonEmptyString(firstEvidenceText(detail.Evidence, "description"), cleanHTMLText(strings.Join(compactStringList(amazonJobsField(hit, "description"), amazonJobsField(hit, "shortDescription")), " ")))
	requirements := cleanHTMLText(amazonJobsField(hit, "basicQualifications", "basic_qualifications"))
	preferred := cleanHTMLText(amazonJobsField(hit, "preferredQualifications", "preferred_qualifications"))
	category := amazonJobsField(hit, "category")
	businessCategory := amazonJobsField(hit, "businessCategory", "business_category")
	teamCategory := amazonJobsField(hit, "teamCategory", "team_category")
	location, country := amazonJobsLocation(hit)
	if detail.Location != "" {
		location = detail.Location
	}
	if detail.Country != "" {
		country = detail.Country
	}
	sourceURL := amazonJobsDetailURL(source, hit)
	applyURL := firstNonEmptyString(amazonJobsField(hit, "urlNextStep", "url_next_step"), sourceURL, source.URL)
	evidenceURL := firstNonEmptyString(sourceURL, applyURL, source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "Amazon Jobs search API", URL: source.URL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: evidenceURL})
	}
	if requirements != "" {
		evidence = append(evidence, Evidence{Field: "requirements", Text: requirements, URL: evidenceURL})
	}
	if preferred != "" {
		evidence = append(evidence, Evidence{Field: "preferred_qualifications", Text: preferred, URL: evidenceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: evidenceURL})
	}
	if category != "" {
		evidence = append(evidence, Evidence{Field: "category", Text: category, URL: evidenceURL})
	}
	if businessCategory != "" {
		evidence = append(evidence, Evidence{Field: "business_category", Text: businessCategory, URL: evidenceURL})
	}
	if teamCategory != "" && teamCategory != "no-team-listed" {
		evidence = append(evidence, Evidence{Field: "team_category", Text: teamCategory, URL: evidenceURL})
	}
	if detail.SourceJobID != "" {
		evidence = append(evidence, Evidence{Field: "detail", Text: "Amazon hosted JobPosting detail page", URL: firstNonEmptyString(detail.ApplyURL, sourceURL)})
	}
	account := stableAccountToken(sourceCompany(source, "Amazon"))
	if account == "" {
		account = "amazon"
	}
	roleText := strings.Join(compactStringList(title, description, requirements, preferred, category, businessCategory, teamCategory), " ")
	return JobPosting{
		SourceJobID:    "amazon_jobs:" + account + ":" + id,
		Company:        sourceCompany(source, "Amazon"),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: firstNonEmptyString(detail.EmploymentType, employmentFromText(title, category)),
		RoleFamily:     inferRoleFamily(roleText),
		SourceURL:      firstNonEmptyString(sourceURL, source.URL),
		ApplyURL:       applyURL,
		PostedAt:       firstTimePtr(detail.PostedAt, amazonJobsPostedAt(hit)),
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func amazonJobsField(hit amazonJobsHit, keys ...string) string {
	for _, key := range keys {
		values := hit.Fields[key]
		for _, value := range values {
			if value = normalizeSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func firstTimePtr(values ...*time.Time) *time.Time {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func amazonJobsLocation(hit amazonJobsHit) (string, string) {
	country := canonicalCountry(amazonJobsField(hit, "country"))
	location := firstNonEmptyString(amazonJobsField(hit, "normalizedLocation", "normalized_location"), amazonJobsField(hit, "location"))
	if country == "" {
		country = adpWorkforceNowCountry(location)
	}
	return location, country
}

func amazonJobsDetailURL(source Source, hit amazonJobsHit) string {
	id := amazonJobsField(hit, "icimsJobId", "jobId", "id")
	if id == "" {
		return source.URL
	}
	title := firstNonEmptyString(amazonJobsField(hit, "title"), "job")
	parsed, err := parseSourceURL(source.URL)
	if err != nil {
		return source.URL
	}
	locale := "en"
	parts := nonEmptyPathParts(parsed)
	if len(parts) > 0 && strings.HasPrefix(strings.ToLower(parts[0]), "en") {
		locale = strings.ToLower(parts[0])
	}
	parsed.Path = path.Join("/", locale, "jobs", id, appleJobsSlug(title))
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func amazonJobsPostedAt(hit amazonJobsHit) *time.Time {
	for _, key := range []string{"createdDate", "postedDate", "posted_date", "updatedDate", "updated_date"} {
		value := amazonJobsField(hit, key)
		if value == "" {
			continue
		}
		if postedAt := parseTimePtr(value); postedAt != nil {
			return postedAt
		}
		if epoch, err := strconv.ParseInt(value, 10, 64); err == nil {
			return millisTimePtr(epoch)
		}
	}
	return nil
}

func eightfoldPCSXConfigFromSource(source Source) (eightfoldPCSXConfig, error) {
	parsed, err := parseSourceURL(source.URL)
	if err != nil {
		return eightfoldPCSXConfig{}, err
	}
	metadata := source.Metadata
	host := strings.ToLower(parsed.Hostname())
	query := parsed.Query()
	apiBaseURL := firstNonEmptyString(metadata["api_base_url"], metadata["pcsx_api_base_url"], metadata["base_url"])
	if apiBaseURL == "" {
		switch {
		case eightfoldPCSXMicrosoftHost(host):
			apiBaseURL = "https://apply.careers.microsoft.com"
		default:
			apiBaseURL = parsed.Scheme + "://" + parsed.Host
		}
	}
	apiBase, err := parseSourceURL(apiBaseURL)
	if err != nil {
		return eightfoldPCSXConfig{}, fmt.Errorf("invalid eightfold pcsx api base url: %w", err)
	}
	apiBase.RawQuery = ""
	apiBase.Fragment = ""
	apiBaseURL = strings.TrimRight(apiBase.String(), "/")

	domain := firstNonEmptyString(metadata["domain"], metadata["pcsx_domain"], query.Get("domain"))
	if domain == "" {
		if eightfoldPCSXMicrosoftHost(host) || strings.EqualFold(apiBase.Hostname(), "apply.careers.microsoft.com") {
			domain = "microsoft.com"
		} else {
			domain = host
		}
	}
	if domain == "" {
		return eightfoldPCSXConfig{}, errors.New("eightfold pcsx domain is required")
	}

	return eightfoldPCSXConfig{
		APIBaseURL: apiBaseURL,
		Domain:     strings.ToLower(strings.TrimSpace(domain)),
		Query: firstNonEmptyString(
			metadata["search_query"],
			metadata["query"],
			query.Get("q"),
			query.Get("query"),
			query.Get("keywords"),
			query.Get("keyword"),
			query.Get("search"),
		),
		Location: firstNonEmptyString(
			metadata["location"],
			metadata["loc_query"],
			query.Get("lc"),
			query.Get("location"),
			query.Get("loc_query"),
		),
		Locale: firstNonEmptyString(metadata["locale"], metadata["hl"], query.Get("hl"), "en"),
	}, nil
}

func eightfoldApplyConfigFromSource(source Source) (eightfoldApplyConfig, error) {
	parsed, err := parseSourceURL(source.URL)
	if err != nil {
		return eightfoldApplyConfig{}, err
	}
	metadata := source.Metadata
	query := parsed.Query()
	apiBaseURL := firstNonEmptyString(metadata["api_base_url"], metadata["apply_api_base_url"], metadata["base_url"])
	if apiBaseURL == "" {
		apiBaseURL = parsed.Scheme + "://" + parsed.Host
	}
	apiBase, err := parseSourceURL(apiBaseURL)
	if err != nil {
		return eightfoldApplyConfig{}, fmt.Errorf("invalid eightfold apply api base url: %w", err)
	}
	apiBase.RawQuery = ""
	apiBase.Fragment = ""
	apiBaseURL = strings.TrimRight(apiBase.String(), "/")

	domain := firstNonEmptyString(metadata["domain"], metadata["apply_domain"], query.Get("domain"))
	if domain == "" {
		domain = strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	}
	if domain == "" {
		return eightfoldApplyConfig{}, errors.New("eightfold apply domain is required")
	}

	return eightfoldApplyConfig{
		APIBaseURL: apiBaseURL,
		Domain:     strings.ToLower(strings.TrimSpace(domain)),
		Query: firstNonEmptyString(
			metadata["search_query"],
			metadata["query"],
			query.Get("q"),
			query.Get("query"),
			query.Get("keywords"),
			query.Get("keyword"),
			query.Get("search"),
		),
		Location: firstNonEmptyString(
			metadata["location"],
			metadata["loc_query"],
			query.Get("location"),
			query.Get("loc_query"),
		),
	}, nil
}

func eightfoldPCSXMicrosoftHost(host string) bool {
	return strings.EqualFold(host, "jobs.careers.microsoft.com") ||
		strings.EqualFold(host, "apply.careers.microsoft.com") ||
		strings.EqualFold(host, "careers.microsoft.com")
}

func eightfoldPCSXSearchURL(config eightfoldPCSXConfig, start int) string {
	endpoint, err := joinURL(config.APIBaseURL, "api", "pcsx", "search")
	if err != nil {
		return ""
	}
	q := endpoint.Query()
	q.Set("domain", config.Domain)
	q.Set("location", config.Location)
	q.Set("query", config.Query)
	q.Set("start", strconv.Itoa(start))
	endpoint.RawQuery = q.Encode()
	return endpoint.String()
}

func eightfoldApplySearchURL(config eightfoldApplyConfig, start int, pageSize int) string {
	endpoint, err := joinURL(config.APIBaseURL, "api", "apply", "v2", "jobs")
	if err != nil {
		return ""
	}
	q := endpoint.Query()
	q.Set("domain", config.Domain)
	if config.Query != "" {
		q.Set("query", config.Query)
	}
	if config.Location != "" {
		q.Set("location", config.Location)
	}
	q.Set("start", strconv.Itoa(start))
	q.Set("num", strconv.Itoa(pageSize))
	endpoint.RawQuery = q.Encode()
	return endpoint.String()
}

func eightfoldPCSXDetailURL(config eightfoldPCSXConfig, id int64) string {
	endpoint, err := joinURL(config.APIBaseURL, "api", "pcsx", "position_details")
	if err != nil {
		return ""
	}
	q := endpoint.Query()
	q.Set("domain", config.Domain)
	q.Set("hl", firstNonEmptyString(config.Locale, "en"))
	q.Set("position_id", strconv.FormatInt(id, 10))
	endpoint.RawQuery = q.Encode()
	return endpoint.String()
}

func eightfoldApplyPosting(source Source, config eightfoldApplyConfig, position eightfoldApplyPosition) (JobPosting, bool) {
	id := eightfoldApplyPositionID(position)
	title := normalizeSpace(firstNonEmptyString(position.PostingName, position.Name))
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	company := sourceCompany(source, eightfoldPCSXCompanyFallback(config.Domain))
	location, country := eightfoldApplyLocation(position, config)
	description := cleanHTMLText(position.JobDescription)
	department := normalizeSpace(firstNonEmptyString(position.Department, position.BusinessUnit))
	workLocation := firstNonEmptyString(position.WorkLocationOption, position.LocationFlexibility)
	detailURL := firstNonEmptyString(position.CanonicalPositionURL, eightfoldApplyFallbackURL(config, position.ID))
	evidenceURL := firstNonEmptyString(detailURL, source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "Eightfold apply jobs API", URL: evidenceURL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: evidenceURL})
	}
	if department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: evidenceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: evidenceURL})
	}
	if workLocation != "" {
		evidence = append(evidence, Evidence{Field: "work_location", Text: workLocation, URL: evidenceURL})
	}
	if position.DisplayJobID != "" || position.ATSJobID != "" {
		evidence = append(evidence, Evidence{Field: "job_id", Text: firstNonEmptyString(position.DisplayJobID, position.ATSJobID), URL: evidenceURL})
	}
	account := stableAccountToken(company)
	if account == "" {
		account = stableAccountToken(eightfoldPCSXCompanyFallback(config.Domain))
	}
	roleText := strings.Join(compactStringList(title, description, department, position.Type, workLocation), " ")
	return JobPosting{
		SourceJobID:    "eightfold_apply:" + account + ":" + id,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, department),
		RoleFamily:     inferRoleFamily(roleText),
		SourceURL:      firstNonEmptyString(detailURL, source.URL),
		ApplyURL:       firstNonEmptyString(detailURL, source.URL),
		PostedAt:       millisTimePtr(firstNonZeroInt64(position.UpdatedAt, position.CreatedAt)),
		Live:           true,
		Confidence:     0.83,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func eightfoldPCSXPosting(source Source, config eightfoldPCSXConfig, search eightfoldPCSXPosition, detail eightfoldPCSXPosition) (JobPosting, bool) {
	position := eightfoldPCSXMergePosition(search, detail)
	id := eightfoldPCSXPositionID(position)
	title := normalizeSpace(position.Name)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	detailURL := eightfoldPCSXResolveURL(config, position.PositionURL)
	company := sourceCompany(source, eightfoldPCSXCompanyFallback(config.Domain))
	location, country := eightfoldPCSXLocation(position, config)
	description := cleanHTMLText(position.JobDescription)
	department := normalizeSpace(position.Department)
	workLocation := firstNonEmptyString(position.WorkLocationOption, position.LocationFlexibility)
	employment := employmentFromText(title, firstNonEmptyString(position.EmploymentType, department))
	evidenceURL := firstNonEmptyString(detailURL, source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "Eightfold PCSX search/detail API", URL: evidenceURL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: evidenceURL})
	}
	if department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: evidenceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: evidenceURL})
	}
	if workLocation != "" {
		evidence = append(evidence, Evidence{Field: "work_location", Text: workLocation, URL: evidenceURL})
	}
	if position.EmploymentType != "" {
		evidence = append(evidence, Evidence{Field: "employment_type", Text: position.EmploymentType, URL: evidenceURL})
	}
	if position.DisplayJobID != "" || position.ATSJobID != "" {
		evidence = append(evidence, Evidence{Field: "job_id", Text: firstNonEmptyString(position.DisplayJobID, position.ATSJobID), URL: evidenceURL})
	}
	account := stableAccountToken(company)
	if account == "" {
		account = stableAccountToken(eightfoldPCSXCompanyFallback(config.Domain))
	}
	roleText := strings.Join(compactStringList(title, description, department, position.RoleType, workLocation), " ")
	return JobPosting{
		SourceJobID:    "eightfold_pcsx:" + account + ":" + id,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		RoleFamily:     inferRoleFamily(roleText),
		SourceURL:      firstNonEmptyString(detailURL, source.URL),
		ApplyURL:       firstNonEmptyString(detailURL, source.URL),
		PostedAt:       millisTimePtr(firstNonZeroInt64(position.PostedTs, position.CreationTs)),
		Live:           true,
		Confidence:     0.84,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func eightfoldPCSXMergePosition(search eightfoldPCSXPosition, detail eightfoldPCSXPosition) eightfoldPCSXPosition {
	merged := search
	if detail.ID != 0 {
		merged.ID = detail.ID
	}
	if detail.DisplayJobID != "" {
		merged.DisplayJobID = detail.DisplayJobID
	}
	if detail.Name != "" {
		merged.Name = detail.Name
	}
	if len(detail.Locations) > 0 {
		merged.Locations = detail.Locations
	}
	if len(detail.StandardizedLocations) > 0 {
		merged.StandardizedLocations = detail.StandardizedLocations
	}
	if detail.PostedTs != 0 {
		merged.PostedTs = detail.PostedTs
	}
	if detail.CreationTs != 0 {
		merged.CreationTs = detail.CreationTs
	}
	if detail.Department != "" {
		merged.Department = detail.Department
	}
	if detail.WorkLocationOption != "" {
		merged.WorkLocationOption = detail.WorkLocationOption
	}
	if detail.LocationFlexibility != "" {
		merged.LocationFlexibility = detail.LocationFlexibility
	}
	if detail.ATSJobID != "" {
		merged.ATSJobID = detail.ATSJobID
	}
	if detail.PositionURL != "" {
		merged.PositionURL = detail.PositionURL
	}
	if detail.JobDescription != "" {
		merged.JobDescription = detail.JobDescription
	}
	if detail.EmploymentType != "" {
		merged.EmploymentType = detail.EmploymentType
	}
	if detail.RoleType != "" {
		merged.RoleType = detail.RoleType
	}
	return merged
}

func eightfoldPCSXPositionID(position eightfoldPCSXPosition) string {
	if position.ID > 0 {
		return strconv.FormatInt(position.ID, 10)
	}
	return firstNonEmptyString(position.DisplayJobID, position.ATSJobID)
}

func eightfoldApplyPositionID(position eightfoldApplyPosition) string {
	if position.ID > 0 {
		return strconv.FormatInt(position.ID, 10)
	}
	return firstNonEmptyString(position.DisplayJobID, position.ATSJobID)
}

func eightfoldPCSXLocation(position eightfoldPCSXPosition, config eightfoldPCSXConfig) (string, string) {
	locations := position.StandardizedLocations
	if len(locations) == 0 {
		locations = position.Locations
	}
	location := strings.Join(compactStringList(locations...), "; ")
	country := adpWorkforceNowCountry(location)
	if country == "" {
		country = canonicalCountry(config.Location)
	}
	return location, country
}

func eightfoldApplyLocation(position eightfoldApplyPosition, config eightfoldApplyConfig) (string, string) {
	locations := position.Locations
	if len(locations) == 0 && position.Location != "" {
		locations = []string{position.Location}
	}
	location := strings.Join(compactStringList(locations...), "; ")
	country := adpWorkforceNowCountry(location)
	if country == "" {
		country = canonicalCountry(config.Location)
	}
	return location, country
}

func eightfoldApplyFallbackURL(config eightfoldApplyConfig, id int64) string {
	if id <= 0 {
		return ""
	}
	base, err := parseSourceURL(config.APIBaseURL)
	if err != nil {
		return ""
	}
	base.Path = "/careers/job/" + strconv.FormatInt(id, 10)
	base.RawQuery = ""
	base.Fragment = ""
	return base.String()
}

func eightfoldPCSXResolveURL(config eightfoldPCSXConfig, rawPath string) string {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return ""
	}
	base, err := parseSourceURL(config.APIBaseURL)
	if err != nil {
		return rawPath
	}
	ref, err := url.Parse(rawPath)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

func eightfoldPCSXCompanyFallback(domain string) string {
	domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), "www.")
	if dot := strings.Index(domain, "."); dot > 0 {
		return domain[:dot]
	}
	return domain
}

func (payload eightfoldPCSXSearchResponse) searchErr() error {
	if payload.Status == 0 || payload.Status == http.StatusOK {
		return nil
	}
	return fmt.Errorf("eightfold pcsx search failed: %s", firstNonEmptyString(payload.Error.Message, payload.Error.Body, strconv.Itoa(payload.Status)))
}

func (payload eightfoldPCSXDetailResponse) detailErr() error {
	if payload.Status == 0 || payload.Status == http.StatusOK {
		return nil
	}
	return fmt.Errorf("eightfold pcsx detail failed: %s", firstNonEmptyString(payload.Error.Message, payload.Error.Body, strconv.Itoa(payload.Status)))
}

func googleCareersJobCards(page string) []string {
	matches := googleCareersCardPattern.FindAllStringIndex(page, -1)
	cards := make([]string, 0, len(matches))
	for i, match := range matches {
		start := match[0]
		end := len(page)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		if start < end {
			cards = append(cards, page[start:end])
		}
	}
	return cards
}

type googleCareersDetail struct {
	Title        string
	Description  string
	Requirements string
}

func (detail googleCareersDetail) empty() bool {
	return detail.Title == "" && detail.Description == "" && detail.Requirements == ""
}

func googleCareersPosting(source Source, card string, detail googleCareersDetail) (JobPosting, bool) {
	id := googleCareersID(card)
	title := firstNonEmptyString(detail.Title, googleCareersTitle(card))
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	location, country := googleCareersLocation(card)
	requirements := firstNonEmptyString(detail.Requirements, googleCareersRequirements(card))
	description := detail.Description
	detailURL := googleCareersDetailURL(source, card)
	evidenceURL := firstNonEmptyString(detailURL, source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "Google Careers rendered search result card", URL: source.URL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: evidenceURL})
	}
	if requirements != "" {
		evidence = append(evidence, Evidence{Field: "requirements", Text: requirements, URL: evidenceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: evidenceURL})
	}
	if !detail.empty() {
		evidence = append(evidence, Evidence{Field: "detail", Text: "Google Careers hosted detail page", URL: evidenceURL})
	}
	company := sourceCompany(source, "Google")
	account := stableAccountToken(company)
	if account == "" {
		account = "google"
	}
	roleText := strings.Join(compactStringList(title, description, requirements), " ")
	return JobPosting{
		SourceJobID:    "google_careers:" + account + ":" + id,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, requirements),
		RoleFamily:     inferRoleFamily(roleText),
		SourceURL:      firstNonEmptyString(detailURL, source.URL),
		ApplyURL:       firstNonEmptyString(detailURL, source.URL),
		Live:           true,
		Confidence:     0.82,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func openAICareersPostings(source Source, document string, maxJobs int) []JobPosting {
	baseURL, err := parseSourceURL(source.URL)
	if err != nil {
		return nil
	}
	anchors := anchorTagPattern.FindAllString(document, -1)
	jobs := make([]JobPosting, 0, min(maxJobs, len(anchors)))
	seen := map[string]struct{}{}
	var pending openAICareersLead
	for _, anchor := range anchors {
		rawHref := html.UnescapeString(htmlAttrValue(anchor, "href"))
		href := resolveStaticURL(baseURL, rawHref)
		text := cleanHTMLText(anchor)
		if openAIApplyLink(href, text) {
			if pending.empty() || len(jobs) >= maxJobs {
				continue
			}
			job, ok := openAICareersPosting(source, pending, href)
			if !ok {
				pending = openAICareersLead{}
				continue
			}
			if _, exists := seen[job.SourceJobID]; exists {
				pending = openAICareersLead{}
				continue
			}
			seen[job.SourceJobID] = struct{}{}
			jobs = append(jobs, job)
			pending = openAICareersLead{}
			continue
		}
		if openAIJobLeadLink(rawHref, href, text) {
			pending = openAICareersLead{
				Title:     openAICareersTitle(text),
				DetailURL: href,
				Text:      text,
			}
		}
	}
	return jobs
}

type openAICareersLead struct {
	Title     string
	DetailURL string
	Text      string
}

func (lead openAICareersLead) empty() bool {
	return strings.TrimSpace(lead.Title) == ""
}

func openAIApplyLink(href string, text string) bool {
	lowerHref := strings.ToLower(href)
	lowerText := strings.ToLower(text)
	return strings.Contains(lowerHref, "jobs.ashbyhq.com/openai") ||
		(strings.Contains(lowerText, "apply now") && strings.Contains(lowerHref, "ashbyhq.com"))
}

func openAIJobLeadLink(rawHref string, resolvedHref string, text string) bool {
	lowerHref := strings.ToLower(strings.TrimSpace(rawHref))
	lowerResolved := strings.ToLower(strings.TrimSpace(resolvedHref))
	lowerText := strings.ToLower(text)
	if !strings.Contains(lowerResolved, "openai.com/careers") && !strings.HasPrefix(lowerHref, "/careers") {
		return false
	}
	if lowerText == "" || lowerText == "careers" || strings.Contains(lowerText, "careers at openai") {
		return false
	}
	for _, token := range []string{
		"engineer", "research", "scientist", "manager", "designer", "product", "security",
		"infrastructure", "data", "legal", "finance", "operations", "recruit", "analyst",
	} {
		if strings.Contains(lowerText, token) {
			return true
		}
	}
	return false
}

func openAICareersPosting(source Source, lead openAICareersLead, applyURL string) (JobPosting, bool) {
	title := normalizeSpace(lead.Title)
	if title == "" || strings.TrimSpace(applyURL) == "" {
		return JobPosting{}, false
	}
	location := openAICareersLocation(lead.Text)
	evidenceURL := firstNonEmptyString(lead.DetailURL, applyURL, source.URL)
	return JobPosting{
		SourceJobID:    "openai:" + stableJobToken(applyURL, title),
		Company:        sourceCompany(source, "OpenAI"),
		Title:          title,
		Location:       location,
		Country:        normalizeCountry("", location),
		EmploymentType: employmentFromText(title, ""),
		Level:          inferLevel(title + " " + lead.Text),
		RoleFamily:     inferRoleFamily(title + " " + lead.Text),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		Live:           true,
		Confidence:     0.8,
		Strategy:       TierATS,
		Evidence: []Evidence{
			{Field: "ats", Text: "OpenAI Careers listing links to Ashby apply flow", URL: evidenceURL},
			{Field: "listing", Text: lead.Text, URL: evidenceURL},
		},
	}, true
}

func openAICareersTitle(text string) string {
	text = normalizeSpace(text)
	for _, marker := range []string{
		" Applied AI Engineering ", " Applied AI Infrastructure ", " Core Product & Platform ",
		" Codex - Engineering ", " Research ", " Robotics ", " Safety Systems ", " Security ",
		" Scaling ", " Compute ", " B2B Applications ", " Model Deployment for Business ",
		" Consumer Devices Software ", " Support Automation ", " GTM Innovation ",
	} {
		if before, _, ok := strings.Cut(text, marker); ok {
			return normalizeSpace(before)
		}
	}
	return text
}

func openAICareersLocation(text string) string {
	for _, location := range []string{
		"Remote - US", "San Francisco", "New York City", "New York", "Seattle", "London, UK",
		"London", "Dublin, Ireland", "Dublin", "Singapore", "Hong Kong", "Tokyo", "Paris",
		"Munich",
	} {
		if strings.Contains(text, location) {
			return location
		}
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "2 locations") || strings.Contains(lower, "3 locations") || strings.Contains(lower, "4 locations") {
		return "Multiple locations"
	}
	return ""
}

func googleCareersDetailFromHTML(document string) googleCareersDetail {
	detail := googleCareersDetail{
		Title: strings.TrimSpace(strings.TrimSuffix(googleCareersDetailTitle(document), "- Google")),
	}
	sections := googleCareersDetailSections(document)
	if len(sections) == 0 {
		return detail
	}
	for label, text := range sections {
		lower := strings.ToLower(label)
		switch {
		case strings.Contains(lower, "qualification"), strings.Contains(lower, "minimum"):
			detail.Requirements = strings.Join(compactStringList(detail.Requirements, text), "\n")
		case strings.Contains(lower, "about"), strings.Contains(lower, "description"), strings.Contains(lower, "responsibilit"):
			detail.Description = strings.Join(compactStringList(detail.Description, text), "\n")
		default:
			detail.Description = strings.Join(compactStringList(detail.Description, text), "\n")
		}
	}
	return detail
}

func googleCareersDetailTitle(document string) string {
	title := cleanHTMLText(firstRegexpGroup(googleCareersDetailTitlePattern, document))
	title = strings.TrimSpace(title)
	if strings.EqualFold(strings.TrimSpace(strings.TrimSuffix(title, "- Google")), "job details") {
		return ""
	}
	return title
}

func googleCareersDetailSections(document string) map[string]string {
	sections := map[string]string{}
	for _, match := range googleCareersSectionPattern.FindAllStringSubmatch(document, -1) {
		if len(match) < 2 {
			continue
		}
		label := googleCareersSectionLabel(match[1])
		text := cleanHTMLText(match[1])
		if label == "" || text == "" {
			continue
		}
		sections[label] = text
	}
	return sections
}

func googleCareersSectionLabel(sectionHTML string) string {
	for _, pattern := range []*regexp.Regexp{
		regexp.MustCompile(`(?is)<h[234]\b[^>]*>(.*?)</h[234]>`),
		regexp.MustCompile(`(?is)<strong\b[^>]*>(.*?)</strong>`),
	} {
		if label := cleanHTMLText(firstRegexpGroup(pattern, sectionHTML)); label != "" {
			return label
		}
	}
	text := cleanHTMLText(sectionHTML)
	if text == "" {
		return ""
	}
	if cut := strings.Index(text, "."); cut > 0 && cut < 80 {
		return text[:cut]
	}
	if len(text) > 80 {
		return text[:80]
	}
	return text
}

func googleCareersID(card string) string {
	if id := firstRegexpGroup(googleCareersIDPattern, card); id != "" {
		return id
	}
	if href := googleCareersHref(card); href != "" {
		id := firstRegexpGroup(googleCareersHrefIDPattern, href)
		if cut := strings.Index(id, "-"); cut > 0 {
			id = id[:cut]
		}
		return strings.TrimSpace(id)
	}
	return ""
}

func googleCareersTitle(card string) string {
	return cleanHTMLText(firstRegexpGroup(googleCareersTitlePattern, card))
}

func googleCareersLocation(card string) (string, string) {
	matches := googleCareersLocationPattern.FindAllStringSubmatch(card, -1)
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			values = append(values, cleanHTMLText(match[1]))
		}
	}
	location := strings.Join(compactStringList(values...), "; ")
	return location, adpWorkforceNowCountry(location)
}

func googleCareersRequirements(card string) string {
	matches := googleCareersRequirementsPattern.FindAllStringSubmatch(card, -1)
	values := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			values = append(values, cleanHTMLText(match[1]))
		}
	}
	return strings.Join(compactStringList(values...), "\n")
}

func googleCareersDetailURL(source Source, card string) string {
	href := googleCareersHref(card)
	if href == "" {
		return source.URL
	}
	return googleCareersResolveURL(source.URL, href)
}

func googleCareersHref(card string) string {
	tag := googleCareersLinkPattern.FindString(card)
	if tag == "" {
		return ""
	}
	return html.UnescapeString(htmlAttrValue(tag, "href"))
}

func googleCareersResolveURL(baseURL string, href string) string {
	base, err := parseSourceURL(baseURL)
	if err != nil {
		return strings.TrimSpace(href)
	}
	ref, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return baseURL
	}
	if ref.IsAbs() || strings.HasPrefix(ref.Path, "/") {
		return base.ResolveReference(ref).String()
	}
	if strings.HasPrefix(ref.Path, "jobs/results/") {
		baseParts := strings.ToLower(base.Path)
		prefix := "/about/careers/applications/"
		if index := strings.Index(baseParts, "/jobs/results"); index >= 0 {
			prefix = base.Path[:index+1]
		}
		base.Path = path.Join(prefix, ref.Path)
		base.RawQuery = ref.RawQuery
		base.Fragment = ref.Fragment
		return base.String()
	}
	return base.ResolveReference(ref).String()
}

func paylocityFeedPosting(source Source, config paylocityConfig, feed paylocityFeedData, item paylocityFeedJob) (JobPosting, bool) {
	id := paylocityJobID(item.id())
	title := item.title()
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	company := sourceCompany(source, firstNonEmptyString(item.companyName(), feed.company(), config.CompanySlug, config.FeedID))
	displayURL := item.displayURL()
	applyURL := item.applyURL()
	if applyURL == "" {
		applyURL = paylocityHostedURL(source.URL, "Apply", id, company, title)
	}
	if displayURL == "" {
		displayURL = paylocityHostedURL(source.URL, "Details", id, company, title)
	}
	description := strings.Join(compactStringList(cleanHTMLText(item.description()), cleanHTMLText(item.requirements())), " ")
	department := item.department()
	location, country := paylocityLocationText(item.location())
	salary := cleanHTMLText(item.salary())
	employment := firstNonEmptyString(strings.Join(compactStringList(item.jobTypesArray()...), ", "), cleanHTMLText(item.jobTypes()), employmentFromText(title, department))
	evidenceURL := firstNonEmptyString(displayURL, applyURL, source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "Paylocity public job feed " + feed.format(), URL: source.URL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: evidenceURL})
	}
	if department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: evidenceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: evidenceURL})
	}
	if salary != "" {
		evidence = append(evidence, Evidence{Field: "compensation", Text: salary, URL: evidenceURL})
	}
	account := stableAccountToken(firstNonEmptyString(config.FeedID, config.CompanySlug, company))
	return JobPosting{
		SourceJobID:    "paylocity:" + account + ":" + id,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		RoleFamily:     inferRoleFamily(title + " " + description + " " + department),
		SourceURL:      firstNonEmptyString(displayURL, source.URL),
		ApplyURL:       firstNonEmptyString(applyURL, displayURL, source.URL),
		PostedAt:       parseTimePtr(item.publishedDate()),
		Live:           true,
		Confidence:     0.84,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func avatureFeedURL(pageURL *url.URL, maxJobs int) string {
	copy := *pageURL
	copy.RawQuery = ""
	copy.Fragment = ""
	pathLower := strings.ToLower(strings.TrimRight(copy.Path, "/"))
	switch {
	case strings.HasSuffix(pathLower, "/feed"):
		copy.Path = strings.TrimRight(copy.Path, "/") + "/"
	case strings.Contains(pathLower, "/searchjobs/feed"):
		if !strings.HasSuffix(copy.Path, "/") {
			copy.Path += "/"
		}
	case strings.Contains(pathLower, "/searchjobs"):
		copy.Path = strings.TrimRight(copy.Path, "/") + "/feed/"
	default:
		copy.Path = strings.TrimRight(copy.Path, "/") + "/SearchJobs/feed/"
	}
	query := url.Values{}
	query.Set("jobRecordsPerPage", strconv.Itoa(maxJobs))
	copy.RawQuery = query.Encode()
	return copy.String()
}

func avaturePosting(source Source, evidenceURL string, item avatureRSSItem, detail avatureDetail) (JobPosting, bool) {
	title := firstNonEmptyString(detail.Title, strings.TrimSpace(item.Title))
	link := strings.TrimSpace(item.link())
	id := firstNonEmptyString(avatureJobIDFromURL(link), stableJobToken(link, title))
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	description := firstNonEmptyString(detail.Description, cleanHTMLText(item.Description))
	location := detail.Location
	country := normalizeCountry("", location)
	businessArea := strings.TrimSpace(detail.BusinessArea)
	applyURL := firstNonEmptyString(detail.ApplyURL, link)
	postedAt := parseTimePtr(firstNonEmptyString(detail.PostedAtText, item.PubDate))
	context := strings.Join(compactStringList(title, description, businessArea, location), " ")
	evidence := []Evidence{
		{Field: "ats", Text: "Avature public SearchJobs RSS feed", URL: evidenceURL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: firstNonEmptyString(link, evidenceURL)})
	}
	if businessArea != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: businessArea, URL: firstNonEmptyString(link, evidenceURL)})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: firstNonEmptyString(link, evidenceURL)})
	}
	return JobPosting{
		SourceJobID: "avature:" + id,
		Company:     sourceCompany(source, avatureCompanyFromURL(firstNonEmptyString(link, source.URL))),
		Title:       title,
		Location:    location,
		Country:     country,
		Level:       inferLevel(context),
		RoleFamily:  inferRoleFamily(context),
		SourceURL:   firstNonEmptyString(link, source.URL),
		ApplyURL:    firstNonEmptyString(applyURL, link, source.URL),
		PostedAt:    postedAt,
		Live:        true,
		Confidence:  0.84,
		Strategy:    TierATS,
		Evidence:    evidence,
	}, true
}

func avatureFieldValue(document string, label string) string {
	pattern := regexp.MustCompile(`(?is)<div\b[^>]*class=["'][^"']*\barticle__content__view__field__label\b[^"']*["'][^>]*>\s*` + regexp.QuoteMeta(label) + `\s*</div>\s*<div\b[^>]*class=["'][^"']*\barticle__content__view__field__value\b[^"']*["'][^>]*>\s*(.*?)\s*</div>`)
	return firstRegexpGroup(pattern, document)
}

func avatureTitleFromDocument(document string) string {
	pattern := regexp.MustCompile(`(?is)<title[^>]*>\s*(.*?)\s*</title>`)
	title := cleanHTMLText(firstRegexpGroup(pattern, document))
	if idx := strings.LastIndex(title, " - "); idx > 0 {
		title = strings.TrimSpace(title[:idx])
	}
	return title
}

func avatureResolveURL(baseURL string, rawURL string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return strings.TrimSpace(rawURL)
	}
	return resolveStaticURL(base, rawURL)
}

func avatureJobIDFromURL(rawURL string) string {
	return firstRegexpGroup(avatureDetailIDPattern, rawURL)
}

func avatureCompanyFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	host = strings.TrimSuffix(host, ".avature.net")
	if host == "" || host == "careers" {
		return ""
	}
	return strings.ReplaceAll(host, "-", " ")
}

func jobylonPosting(source Source, item jobylonJob) (JobPosting, bool) {
	id := firstNonEmptyString(item.ID, stableJobToken(item.URLs.Ad, item.Title))
	title := strings.TrimSpace(item.Title)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	company := sourceCompany(source, firstNonEmptyString(item.Company.Name, item.Company.Slug))
	description := strings.Join(compactStringList(cleanHTMLText(firstNonEmptyString(item.Description, item.DescriptionAlt)), cleanHTMLText(item.Skills)), " ")
	department := strings.TrimSpace(item.Department.Description)
	location, country := jobylonLocationText(item.Locations)
	employment := firstNonEmptyString(jobylonExtraValue(item, "employment-type", "employment type"), item.EmploymentType, employmentFromText(title, item.Experience))
	postedAt := parseTimePtr(firstNonEmptyString(jobylonExtraValue(item, "from-date", "from date"), item.FromDate))
	adURL := strings.TrimSpace(item.URLs.Ad)
	applyURL := firstNonEmptyString(item.URLs.Apply, adURL)
	evidenceURL := firstNonEmptyString(adURL, applyURL, source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "Jobylon public feed", URL: source.URL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: evidenceURL})
	}
	if department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: evidenceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: evidenceURL})
	}
	return JobPosting{
		SourceJobID:    "jobylon:" + id,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		Level:          inferLevel(title + " " + description + " " + employment + " " + item.Experience),
		RoleFamily:     inferRoleFamily(title + " " + description + " " + department + " " + item.Function),
		SourceURL:      firstNonEmptyString(adURL, source.URL),
		ApplyURL:       firstNonEmptyString(applyURL, adURL, source.URL),
		PostedAt:       postedAt,
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func normalizeJobylonDetailPosting(source Source, detailURL *url.URL, job JobPosting) (JobPosting, bool) {
	if strings.TrimSpace(job.Title) == "" {
		return JobPosting{}, false
	}
	id := firstNonEmptyString(jobylonJobIDFromURL(detailURL.String()), strings.TrimPrefix(job.SourceJobID, "static:"), stableJobToken(detailURL.String(), job.Title))
	if id == "" {
		return JobPosting{}, false
	}
	job.SourceJobID = "jobylon:" + id
	if job.Company == "" {
		job.Company = sourceCompany(source, jobylonCompanyFromURL(detailURL))
	}
	job.Country = normalizeCountry(job.Country, job.Location)
	job.SourceURL = detailURL.String()
	if job.ApplyURL == "" || strings.EqualFold(strings.TrimRight(job.ApplyURL, "/"), strings.TrimRight(source.URL, "/")) {
		job.ApplyURL = detailURL.String()
	}
	job.Strategy = TierATS
	job.Confidence = 0.84
	context := strings.Join(compactStringList(job.Title, evidenceTextValue(job.Evidence, "description"), job.EmploymentType, job.Location), " ")
	if job.Level == "" {
		job.Level = inferLevel(context)
	}
	if job.RoleFamily == "" {
		job.RoleFamily = inferRoleFamily(context)
	}
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "Jobylon hosted JobPosting detail page", URL: detailURL.String()})
	return job, true
}

func jobylonFeedSourceURL(rawURL string) bool {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.ToLower(parsed.EscapedPath())
	return host == "feed.jobylon.com" || strings.Contains(path, "/feeds/")
}

func jobylonBoardDetailLinks(boardURL *url.URL, document string, limit int) []string {
	matches := hrefAttrPattern.FindAllStringSubmatch(document, -1)
	out := make([]string, 0, min(len(matches), limit))
	seen := map[string]struct{}{}
	for _, match := range matches {
		if len(out) >= limit {
			break
		}
		if len(match) < 2 {
			continue
		}
		candidate := resolveStaticURL(boardURL, html.UnescapeString(match[1]))
		if !jobylonDetailURLAllowed(boardURL, candidate) {
			continue
		}
		key := strings.TrimRight(candidate, "/")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func jobylonDetailURLAllowed(boardURL *url.URL, rawURL string) bool {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return false
	}
	if !strings.EqualFold(parsed.Hostname(), boardURL.Hostname()) {
		return false
	}
	parts := nonEmptyPathParts(parsed)
	if len(parts) < 2 {
		return false
	}
	blocked := map[string]struct{}{
		"applications": {},
		"apply":        {},
		"jobs":         {},
		"login":        {},
		"admin":        {},
	}
	for _, part := range parts {
		if _, ok := blocked[strings.ToLower(part)]; ok {
			return false
		}
	}
	return jobylonCompanyFromURL(parsed) != "" && jobylonJobIDFromURL(parsed.String()) != ""
}

func jobylonCompanyFromURL(parsed *url.URL) string {
	parts := nonEmptyPathParts(parsed)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func jobylonJobIDFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func jobylonLocationText(locations []jobylonLocation) (string, string) {
	values := make([]string, 0, len(locations))
	country := ""
	for _, location := range locations {
		text := firstNonEmptyString(location.Text, strings.Join(compactStringList(location.City, location.Area, location.Country), ", "), strings.Join(compactStringList(location.CityShort, location.Country), ", "))
		if text != "" {
			values = append(values, text)
		}
		if country == "" {
			country = firstNonEmptyString(location.CountryShort, location.Country)
		}
	}
	joined := strings.Join(compactStringList(values...), "; ")
	return joined, normalizeCountry(country, joined)
}

func jobylonExtraValue(item jobylonJob, names ...string) string {
	for _, wanted := range names {
		for _, node := range item.Extra {
			if strings.EqualFold(strings.TrimSpace(node.XMLName.Local), strings.TrimSpace(wanted)) {
				return strings.TrimSpace(node.Value)
			}
		}
	}
	return ""
}

func zohoRecruitRows(document string) ([]zohoRecruitJob, error) {
	tag := zohoRecruitJobsInputPattern.FindString(document)
	if tag == "" {
		return nil, ErrNoJobs
	}
	raw := htmlAttrValue(tag, "value")
	if strings.TrimSpace(raw) == "" {
		return nil, ErrNoJobs
	}
	var rows []zohoRecruitJob
	if err := json.Unmarshal([]byte(html.UnescapeString(raw)), &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func zohoRecruitPosting(source Source, boardURL *url.URL, row zohoRecruitJob) (JobPosting, bool) {
	id := strings.TrimSpace(row.ID)
	title := firstNonEmptyString(row.PostingTitle, row.JobOpeningName)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	description := cleanHTMLText(row.JobDescription)
	location, country := zohoRecruitLocation(row)
	employment := employmentFromText(title, firstNonEmptyString(row.JobType, row.WorkExperience))
	applyURL := zohoRecruitApplyURL(boardURL, id)
	evidence := []Evidence{
		{Field: "ats", Text: "Zoho Recruit career-site jobs payload", URL: boardURL.String()},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if row.Industry != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: row.Industry, URL: boardURL.String()})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: boardURL.String()})
	}
	if row.JobType != "" {
		evidence = append(evidence, Evidence{Field: "employment_type", Text: row.JobType, URL: boardURL.String()})
	}
	return JobPosting{
		SourceJobID:    "zoho_recruit:" + id,
		Company:        sourceCompany(source, ""),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		Level:          inferLevel(title + " " + description + " " + employment + " " + row.WorkExperience),
		RoleFamily:     inferRoleFamily(title + " " + description + " " + row.Industry),
		SourceURL:      boardURL.String(),
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(row.DateOpened),
		Live:           true,
		Confidence:     0.76,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func zohoRecruitLocation(row zohoRecruitJob) (string, string) {
	country := canonicalCountry(row.Country)
	location := strings.Join(compactStringList(row.City, row.State, country), ", ")
	if row.RemoteJob {
		location = strings.Join(compactStringList("Remote", location), " - ")
	}
	return location, normalizeCountry(country, location)
}

func zohoRecruitApplyURL(boardURL *url.URL, id string) string {
	detailURL := *boardURL
	detailURL.RawQuery = ""
	detailURL.Fragment = ""
	detailURL.Path = strings.TrimRight(detailURL.Path, "/") + "/" + url.PathEscape(id)
	return detailURL.String()
}

func manatalJobLinks(baseURL *url.URL, document string) []manatalJobLink {
	links := make([]manatalJobLink, 0)
	seen := map[string]struct{}{}
	for _, anchor := range anchorTagPattern.FindAllString(document, -1) {
		href := anchorHref(anchor)
		id := manatalJobID(href)
		if id == "" {
			continue
		}
		detailURL := resolveStaticURL(baseURL, href)
		if detailURL == "" || strings.HasSuffix(strings.ToLower(detailURL), "/apply") || strings.HasSuffix(strings.ToLower(detailURL), "/refer") {
			continue
		}
		key := strings.ToLower(detailURL)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		title := firstNonEmptyString(htmlAttrValue(anchor, "data-job-title"), cleanHTMLText(anchor))
		location := strings.Join(compactStringList(htmlAttrValue(anchor, "data-job-city"), htmlAttrValue(anchor, "data-job-country")), ", ")
		links = append(links, manatalJobLink{
			URL:      detailURL,
			ID:       id,
			Title:    title,
			Location: location,
			Country:  htmlAttrValue(anchor, "data-job-country"),
		})
	}
	return links
}

func normalizeManatalPosting(source Source, link manatalJobLink, job JobPosting) JobPosting {
	id := firstNonEmptyString(link.ID, manatalJobID(job.ApplyURL), manatalJobID(job.SourceURL), strings.TrimPrefix(job.SourceJobID, "static:"))
	job.SourceJobID = "manatal:" + id
	job.SourceURL = link.URL
	job.ApplyURL = firstNonEmptyString(job.ApplyURL, link.URL)
	if link.Location != "" && job.Location == "" {
		job.Location = link.Location
	}
	if link.Country != "" && job.Country == "" {
		job.Country = normalizeCountry(link.Country, link.Location)
	}
	if job.Company == "" {
		job.Company = sourceCompany(source, "")
	}
	job.Strategy = TierATS
	job.Confidence = 0.8
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "Manatal hosted career page JSON-LD detail", URL: link.URL})
	return job
}

func manatalFallbackPosting(source Source, link manatalJobLink) (JobPosting, bool) {
	if link.ID == "" || link.Title == "" {
		return JobPosting{}, false
	}
	employment := employmentFromText(link.Title, "")
	return JobPosting{
		SourceJobID:    "manatal:" + link.ID,
		Company:        sourceCompany(source, ""),
		Title:          link.Title,
		Location:       link.Location,
		Country:        normalizeCountry(link.Country, link.Location),
		EmploymentType: employment,
		Level:          inferLevel(link.Title + " " + employment),
		RoleFamily:     inferRoleFamily(link.Title),
		SourceURL:      source.URL,
		ApplyURL:       link.URL,
		Live:           true,
		Confidence:     0.72,
		Strategy:       TierATS,
		Evidence: []Evidence{
			{Field: "ats", Text: "Manatal hosted career page card", URL: source.URL},
			{Field: "location", Text: link.Location, URL: source.URL},
		},
	}, true
}

func manatalJobID(rawURL string) string {
	match := manatalJobIDPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(rawURL)))
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func joinBoardFromHTML(document string) (joinBoardData, error) {
	var board joinBoardData
	match := nextDataScriptPattern.FindStringSubmatch(document)
	if len(match) < 2 {
		return board, ErrNoJobs
	}
	var payload joinNextData
	decoder := json.NewDecoder(strings.NewReader(html.UnescapeString(strings.TrimSpace(match[1]))))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return board, err
	}
	board.State = payload.Props.PageProps.InitialState
	if len(board.State.Jobs.Items) == 0 {
		return board, ErrNoJobs
	}
	return board, nil
}

func joinPosting(source Source, boardURL *url.URL, board joinBoardData, item joinJobItem) (JobPosting, bool) {
	title := strings.TrimSpace(item.Title)
	id := firstNonEmptyString(item.IDParam, rawJSONToken(item.ID), stableJobToken(joinJobURL(boardURL, item.IDParam), title))
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	detailURL := firstNonEmptyString(joinJobURL(boardURL, item.IDParam), boardURL.String())
	location, country := joinLocation(item)
	employment := employmentFromText(title, firstNonEmptyString(item.EmploymentType.Name, item.WorkplaceType))
	department := strings.TrimSpace(item.Category.Name)
	context := strings.Join(compactStringList(title, employment, department, item.WorkplaceType), " ")
	evidence := []Evidence{
		{Field: "ats", Text: "JOIN hosted company page Next.js jobs state", URL: boardURL.String()},
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: boardURL.String()})
	}
	if department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: boardURL.String()})
	}
	if item.WorkplaceType != "" {
		evidence = append(evidence, Evidence{Field: "workplace_type", Text: item.WorkplaceType, URL: boardURL.String()})
	}
	return JobPosting{
		SourceJobID:    "join:" + id,
		Company:        sourceCompany(source, firstNonEmptyString(board.State.Company.Name, board.State.Company.Domain)),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		Level:          inferLevel(context),
		RoleFamily:     inferRoleFamily(context),
		SourceURL:      boardURL.String(),
		ApplyURL:       detailURL,
		PostedAt:       parseTimePtr(item.CreatedAt),
		Live:           true,
		Confidence:     0.83,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func normalizeJOINDetailPosting(source Source, detailURL *url.URL, job JobPosting) JobPosting {
	id := firstNonEmptyString(joinJobIDFromURL(detailURL), strings.TrimPrefix(job.SourceJobID, "static:"))
	if id != "" {
		job.SourceJobID = "join:" + id
	}
	job.SourceURL = detailURL.String()
	job.ApplyURL = firstNonEmptyString(job.ApplyURL, detailURL.String())
	if job.Company == "" {
		job.Company = sourceCompany(source, "")
	}
	job.Strategy = TierATS
	job.Confidence = 0.83
	context := strings.Join(compactStringList(job.Title, evidenceTextValue(job.Evidence, "description"), job.EmploymentType), " ")
	if job.Level == "" {
		job.Level = inferLevel(context)
	}
	if job.RoleFamily == "" {
		job.RoleFamily = inferRoleFamily(context)
	}
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "JOIN hosted JobPosting detail JSON-LD", URL: detailURL.String()})
	return job
}

func joinLocation(item joinJobItem) (string, string) {
	location := strings.Join(compactStringList(item.City.CityName, item.City.RegionName, firstNonEmptyString(item.City.CountryName, item.Country.ISO3166)), ", ")
	if strings.EqualFold(item.WorkplaceType, "REMOTE") {
		location = strings.Join(compactStringList("Remote", location), " - ")
	}
	return location, normalizeCountry(firstNonEmptyString(item.Country.ISO3166, item.City.CountryName), location)
}

func joinJobURL(boardURL *url.URL, idParam string) string {
	idParam = strings.Trim(strings.TrimSpace(idParam), "/")
	if idParam == "" {
		return ""
	}
	detailURL := *boardURL
	detailURL.RawQuery = ""
	detailURL.Fragment = ""
	parts := strings.Split(strings.Trim(detailURL.Path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "companies" {
		detailURL.Path = "/" + path.Join("companies", parts[1], idParam)
	} else {
		detailURL.Path = strings.TrimRight(detailURL.Path, "/") + "/" + idParam
	}
	return detailURL.String()
}

func joinJobIDFromURL(parsed *url.URL) string {
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "companies" {
		return parts[2]
	}
	return ""
}

func evidenceTextValue(evidence []Evidence, field string) string {
	for _, item := range evidence {
		if item.Field == field {
			return item.Text
		}
	}
	return ""
}

func occupopRows(baseURL *url.URL, document string) []occupopRow {
	rows := make([]occupopRow, 0)
	seen := map[string]struct{}{}
	for _, match := range occupopRowPattern.FindAllStringSubmatch(document, -1) {
		if len(match) < 2 {
			continue
		}
		block := match[1]
		anchor := anchorTagPattern.FindString(block)
		href := anchorHref(anchor)
		detailURL := resolveStaticURL(baseURL, href)
		title := cleanHTMLText(anchor)
		if detailURL == "" || title == "" {
			continue
		}
		key := strings.ToLower(detailURL)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		row := occupopRow{URL: detailURL, ID: occupopIDFromURL(detailURL), Title: title}
		for _, small := range occupopSmallPattern.FindAllStringSubmatch(block, -1) {
			if len(small) < 3 {
				continue
			}
			value := cleanHTMLText(small[2])
			switch strings.ToLower(strings.TrimSpace(small[1])) {
			case "location":
				row.Location = value
			case "category":
				row.Category = value
			case "type":
				row.Type = value
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func occupopPosting(source Source, boardURL *url.URL, row occupopRow) (JobPosting, bool) {
	id := firstNonEmptyString(row.ID, stableJobToken(row.URL, row.Title))
	if id == "" || row.Title == "" {
		return JobPosting{}, false
	}
	location, country := occupopLocation(row.Location)
	employment := employmentFromText(row.Title, row.Type)
	context := strings.Join(compactStringList(row.Title, row.Category, row.Type, row.Location), " ")
	evidence := []Evidence{
		{Field: "ats", Text: "Occupop jobs-frame hosted board", URL: boardURL.String()},
	}
	if row.Location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: row.Location, URL: boardURL.String()})
	}
	if row.Category != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: row.Category, URL: boardURL.String()})
	}
	if row.Type != "" {
		evidence = append(evidence, Evidence{Field: "employment_type", Text: row.Type, URL: boardURL.String()})
	}
	return JobPosting{
		SourceJobID:    "occupop:" + id,
		Company:        sourceCompany(source, ""),
		Title:          row.Title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		Level:          inferLevel(context),
		RoleFamily:     inferRoleFamily(context),
		SourceURL:      boardURL.String(),
		ApplyURL:       row.URL,
		Live:           true,
		Confidence:     0.78,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func normalizeOccupopDetailPosting(source Source, detailURL *url.URL, job JobPosting) JobPosting {
	id := firstNonEmptyString(occupopSharedJobID(detailURL), strings.TrimPrefix(job.SourceJobID, "static:"))
	if id != "" {
		job.SourceJobID = "occupop:" + id
	}
	job.SourceURL = detailURL.String()
	job.ApplyURL = firstNonEmptyString(job.ApplyURL, detailURL.String())
	if job.Company == "" {
		job.Company = sourceCompany(source, "")
	}
	job.Strategy = TierATS
	job.Confidence = 0.78
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "Occupop shared job detail", URL: detailURL.String()})
	return job
}

func occupopLocation(raw string) (string, string) {
	location := cleanHTMLText(raw)
	candidate := ""
	parts := strings.Split(location, ",")
	if len(parts) > 0 {
		candidate = strings.Trim(parts[len(parts)-1], " \t\r\n()")
	}
	return location, normalizeCountry(candidate, location)
}

func occupopIDFromURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return stableStringID(rawURL)
	}
	return firstNonEmptyString(occupopSharedJobID(parsed), stableStringID(rawURL))
}

func occupopSharedJobID(parsed *url.URL) string {
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] == "shared" && parts[i+1] == "job" {
			return strings.TrimSpace(parts[i+2])
		}
	}
	return ""
}

func workstreamCards(baseURL *url.URL, document string) []workstreamCard {
	starts := workstreamCardStartPattern.FindAllStringIndex(document, -1)
	if len(starts) == 0 {
		return nil
	}
	cards := make([]workstreamCard, 0, len(starts))
	for i, start := range starts {
		end := len(document)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		block := document[start[0]:end]
		anchor := firstWorkstreamJobAnchor(block)
		href := anchorHref(anchor)
		detailURL := resolveStaticURL(baseURL, href)
		id := workstreamIDFromURL(detailURL)
		title := cleanHTMLText(anchor)
		if detailURL == "" || id == "" || title == "" {
			continue
		}
		cards = append(cards, workstreamCard{
			URL:          detailURL,
			ID:           id,
			Title:        title,
			Location:     cleanHTMLText(firstRegexpGroup(workstreamAddressPattern, block)),
			Description:  cleanHTMLText(firstRegexpGroup(workstreamDescriptionPattern, block)),
			Employment:   cleanHTMLText(firstRegexpGroup(workstreamTagPattern, block)),
			Compensation: cleanHTMLText(firstRegexpGroup(workstreamPayPattern, block)),
		})
	}
	return cards
}

func firstWorkstreamJobAnchor(block string) string {
	for _, anchor := range anchorTagPattern.FindAllString(block, -1) {
		href := anchorHref(anchor)
		parsed, err := parseSourceURL(resolveStaticURL(&url.URL{Scheme: "https", Host: "www.workstream.us"}, href))
		if err == nil && workstreamDetailJobID(parsed) != "" {
			return anchor
		}
	}
	return ""
}

func dedupeWorkstreamCards(cards []workstreamCard) []workstreamCard {
	out := make([]workstreamCard, 0, len(cards))
	seen := map[string]struct{}{}
	for _, card := range cards {
		key := strings.ToLower(firstNonEmptyString(card.URL, card.ID))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, card)
	}
	return out
}

func workstreamCardPosting(source Source, boardURL *url.URL, card workstreamCard) (JobPosting, bool) {
	id := firstNonEmptyString(card.ID, workstreamIDFromURL(card.URL), stableJobToken(card.URL, card.Title))
	if id == "" || card.Title == "" {
		return JobPosting{}, false
	}
	location := card.Location
	employment := employmentFromText(card.Title, card.Employment)
	context := strings.Join(compactStringList(card.Title, card.Description, card.Employment, card.Location), " ")
	evidence := []Evidence{
		{Field: "ats", Text: "Workstream hosted board card", URL: boardURL.String()},
	}
	if card.Description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: card.Description, URL: card.URL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: boardURL.String()})
	}
	if card.Employment != "" {
		evidence = append(evidence, Evidence{Field: "employment_type", Text: card.Employment, URL: boardURL.String()})
	}
	if card.Compensation != "" {
		evidence = append(evidence, Evidence{Field: "compensation", Text: card.Compensation, URL: boardURL.String()})
	}
	return JobPosting{
		SourceJobID:    "workstream:" + id,
		Company:        sourceCompany(source, workstreamCompanyFromURL(boardURL)),
		Title:          card.Title,
		Location:       location,
		Country:        normalizeCountry("", location),
		EmploymentType: employment,
		Level:          inferLevel(context),
		RoleFamily:     inferRoleFamily(context),
		SourceURL:      boardURL.String(),
		ApplyURL:       card.URL,
		Live:           true,
		Confidence:     0.76,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func normalizeWorkstreamDetailPosting(source Source, detailURL *url.URL, card workstreamCard, job JobPosting) JobPosting {
	id := firstNonEmptyString(workstreamDetailJobID(detailURL), card.ID, strings.TrimPrefix(job.SourceJobID, "static:"))
	if id != "" {
		job.SourceJobID = "workstream:" + id
	}
	job.SourceURL = detailURL.String()
	job.ApplyURL = firstNonEmptyString(job.ApplyURL, card.URL, detailURL.String())
	if job.Company == "" {
		job.Company = sourceCompany(source, workstreamCompanyFromURL(detailURL))
	}
	if job.Location == "" {
		job.Location = card.Location
	}
	if job.Country == "" {
		job.Country = normalizeCountry("", firstNonEmptyString(card.Location, job.Location))
	}
	if job.EmploymentType == "" {
		job.EmploymentType = employmentFromText(job.Title, firstNonEmptyString(job.EmploymentType, card.Employment))
	}
	context := strings.Join(compactStringList(job.Title, evidenceTextValue(job.Evidence, "description"), card.Description, job.EmploymentType, card.Location), " ")
	if job.Level == "" {
		job.Level = inferLevel(context)
	}
	if job.RoleFamily == "" {
		job.RoleFamily = inferRoleFamily(context)
	}
	job.Strategy = TierATS
	job.Confidence = 0.84
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "Workstream hosted JobPosting detail JSON-LD", URL: detailURL.String()})
	if card.Compensation != "" {
		job.Evidence = append(job.Evidence, Evidence{Field: "compensation", Text: card.Compensation, URL: card.URL})
	}
	return job
}

func workstreamPositionsURL(baseURL *url.URL, document string) string {
	for _, anchor := range anchorTagPattern.FindAllString(document, -1) {
		href := anchorHref(anchor)
		candidate := resolveStaticURL(baseURL, href)
		parsed, err := parseSourceURL(candidate)
		if err != nil || !strings.EqualFold(parsed.Hostname(), baseURL.Hostname()) {
			continue
		}
		parts := nonEmptyPathParts(parsed)
		if len(parts) >= 3 && parts[0] == "j" && parts[len(parts)-1] == "positions" {
			return parsed.String()
		}
	}
	return ""
}

func workstreamIDFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	return workstreamDetailJobID(parsed)
}

func workstreamDetailJobID(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	if len(parts) < 3 || parts[0] != "j" {
		return ""
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" || strings.EqualFold(last, "positions") || strings.EqualFold(last, "locations") || strings.EqualFold(last, "apply") {
		return ""
	}
	if len(parts) >= 5 {
		return last
	}
	if len(parts) == 3 && workstreamShortDetailIDPattern.MatchString(last) {
		return last
	}
	return ""
}

func workstreamCompanyFromURL(parsed *url.URL) string {
	parts := nonEmptyPathParts(parsed)
	if len(parts) >= 3 && parts[0] == "j" {
		return strings.ReplaceAll(parts[2], "-", " ")
	}
	if len(parts) >= 2 && parts[0] == "j" {
		return strings.ReplaceAll(parts[1], "-", " ")
	}
	return ""
}

func normalizeCareerPlugPosting(source Source, pageURL *url.URL, job JobPosting) (JobPosting, bool) {
	id := firstNonEmptyString(careerPlugJobIDFromURL(job.ApplyURL), careerPlugJobIDFromURL(job.SourceURL), careerPlugJobIDFromURL(pageURL.String()), strings.TrimPrefix(job.SourceJobID, "static:"))
	if id == "" || job.Title == "" {
		return JobPosting{}, false
	}
	job.SourceJobID = "careerplug:" + id
	if job.Company == "" {
		job.Company = sourceCompany(source, careerPlugCompanyFromURL(pageURL))
	}
	detailURL := careerPlugJobURL(pageURL, id)
	if job.ApplyURL == "" || strings.EqualFold(strings.TrimRight(job.ApplyURL, "/"), strings.TrimRight(pageURL.String(), "/")) {
		job.ApplyURL = detailURL
	}
	job.SourceURL = pageURL.String()
	job.Strategy = TierATS
	job.Confidence = 0.84
	context := strings.Join(compactStringList(job.Title, evidenceTextValue(job.Evidence, "description"), job.EmploymentType, job.Location), " ")
	if job.Level == "" {
		job.Level = inferLevel(context)
	}
	if job.RoleFamily == "" {
		job.RoleFamily = inferRoleFamily(context)
	}
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "CareerPlug hosted JobPosting JSON-LD", URL: pageURL.String()})
	return job, true
}

func normalizeJibePosting(source Source, pageURL *url.URL, job JobPosting) (JobPosting, bool) {
	if strings.TrimSpace(job.Title) == "" || strings.TrimSpace(job.ApplyURL) == "" {
		return JobPosting{}, false
	}
	id := firstNonEmptyString(strings.TrimPrefix(job.SourceJobID, "static:"), jibeJobIDFromURL(job.ApplyURL), jibeJobIDFromURL(job.SourceURL), jibeJobIDFromURL(pageURL.String()))
	if id == "" {
		id = stableJobToken(job.ApplyURL, job.Title)
	}
	job.SourceJobID = "jibe:" + id
	if job.Company == "" {
		job.Company = sourceCompany(source, companyFromURL(pageURL.String()))
	}
	job.SourceURL = pageURL.String()
	job.Strategy = TierATS
	job.Confidence = 0.82
	context := strings.Join(compactStringList(job.Title, evidenceTextValue(job.Evidence, "description"), job.EmploymentType, job.Location), " ")
	if job.Level == "" {
		job.Level = inferLevel(context)
	}
	if job.RoleFamily == "" {
		job.RoleFamily = inferRoleFamily(context)
	}
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "Jibe/Radancy hosted JobPosting JSON-LD", URL: pageURL.String()})
	return job, true
}

func normalizeJobScorePosting(source Source, pageURL *url.URL, job JobPosting) (JobPosting, bool) {
	if strings.TrimSpace(job.Title) == "" || strings.TrimSpace(job.ApplyURL) == "" {
		return JobPosting{}, false
	}
	id := firstNonEmptyString(strings.TrimPrefix(job.SourceJobID, "static:"), jobScoreJobIDFromURL(job.ApplyURL), jobScoreJobIDFromURL(job.SourceURL), jobScoreJobIDFromURL(pageURL.String()))
	if id == "" {
		id = stableJobToken(job.ApplyURL, job.Title)
	}
	job.SourceJobID = "jobscore:" + id
	if job.Company == "" {
		job.Company = sourceCompany(source, jobScoreCompanyFromURL(pageURL))
	}
	job.SourceURL = pageURL.String()
	job.Strategy = TierATS
	job.Confidence = 0.82
	context := strings.Join(compactStringList(job.Title, evidenceTextValue(job.Evidence, "description"), job.EmploymentType, job.Location), " ")
	if job.Level == "" {
		job.Level = inferLevel(context)
	}
	if job.RoleFamily == "" {
		job.RoleFamily = inferRoleFamily(context)
	}
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "JobScore hosted JobPosting JSON-LD", URL: pageURL.String()})
	return job, true
}

func normalizeTalentBrewPosting(source Source, pageURL *url.URL, job JobPosting) (JobPosting, bool) {
	if strings.TrimSpace(job.Title) == "" || strings.TrimSpace(job.ApplyURL) == "" {
		return JobPosting{}, false
	}
	id := firstNonEmptyString(strings.TrimPrefix(job.SourceJobID, "static:"), talentBrewJobIDFromURL(job.ApplyURL), talentBrewJobIDFromURL(job.SourceURL), talentBrewJobIDFromURL(pageURL.String()))
	if id == "" {
		id = stableJobToken(job.ApplyURL, job.Title)
	}
	job.SourceJobID = "talentbrew:" + id
	if job.Company == "" {
		job.Company = sourceCompany(source, talentBrewCompanyFromURL(pageURL))
	}
	job.SourceURL = pageURL.String()
	job.Strategy = TierATS
	job.Confidence = 0.82
	context := strings.Join(compactStringList(job.Title, evidenceTextValue(job.Evidence, "description"), job.EmploymentType, job.Location), " ")
	if job.Level == "" {
		job.Level = inferLevel(context)
	}
	if job.RoleFamily == "" {
		job.RoleFamily = inferRoleFamily(context)
	}
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "TalentBrew hosted JobPosting JSON-LD", URL: pageURL.String()})
	return job, true
}

func normalizeHiBobHiringPosting(source Source, job JobPosting) JobPosting {
	id := firstNonEmptyString(strings.TrimPrefix(job.SourceJobID, "static:"), stableJobToken(firstNonEmptyString(job.ApplyURL, job.SourceURL, source.URL), job.Title))
	if id != "" {
		job.SourceJobID = "hibob:" + strings.TrimPrefix(id, "hibob:")
	}
	if job.Company == "" {
		job.Company = sourceCompany(source, companyFromURL(source.URL))
	}
	if job.Country == "" {
		job.Country = normalizeCountry("", job.Location)
	}
	context := strings.Join(compactStringList(job.Title, evidenceTextValue(job.Evidence, "description"), job.EmploymentType, job.Location), " ")
	if job.Level == "" {
		job.Level = inferLevel(context)
	}
	if job.RoleFamily == "" {
		job.RoleFamily = inferRoleFamily(context)
	}
	job.Strategy = TierATS
	job.Confidence = 0.8
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "HiBob Hiring hosted careers page JSON-LD or sitemap", URL: source.URL})
	return job
}

func normalizeFountainPosting(source Source, job JobPosting) JobPosting {
	id := firstNonEmptyString(strings.TrimPrefix(job.SourceJobID, "static:"), stableJobToken(firstNonEmptyString(job.ApplyURL, job.SourceURL, source.URL), job.Title))
	if id != "" {
		job.SourceJobID = "fountain:" + strings.TrimPrefix(id, "fountain:")
	}
	if job.Company == "" {
		job.Company = sourceCompany(source, companyFromURL(source.URL))
	}
	if job.Country == "" {
		job.Country = normalizeCountry("", job.Location)
	}
	context := strings.Join(compactStringList(job.Title, evidenceTextValue(job.Evidence, "description"), job.EmploymentType, job.Location), " ")
	if job.Level == "" {
		job.Level = inferLevel(context)
	}
	if job.RoleFamily == "" {
		job.RoleFamily = inferRoleFamily(context)
	}
	job.Strategy = TierATS
	job.Confidence = 0.78
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "Fountain hosted careers page JSON-LD or sitemap", URL: source.URL})
	return job
}

func normalizeRipplingJobsPosting(source Source, job JobPosting) JobPosting {
	id := firstNonEmptyString(strings.TrimPrefix(job.SourceJobID, "static:"), ripplingJobIDFromURL(firstNonEmptyString(job.ApplyURL, job.SourceURL)), stableJobToken(firstNonEmptyString(job.ApplyURL, job.SourceURL, source.URL), job.Title))
	if id != "" {
		job.SourceJobID = "rippling_jobs:" + strings.TrimPrefix(id, "rippling_jobs:")
	}
	if job.Company == "" {
		job.Company = sourceCompany(source, companyFromURL(source.URL))
	}
	if job.Country == "" {
		job.Country = normalizeCountry("", job.Location)
	}
	context := strings.Join(compactStringList(job.Title, evidenceTextValue(job.Evidence, "description"), job.EmploymentType, job.Location), " ")
	if job.Level == "" {
		job.Level = inferLevel(context)
	}
	if job.RoleFamily == "" {
		job.RoleFamily = inferRoleFamily(context)
	}
	job.Strategy = TierATS
	job.Confidence = 0.8
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "Rippling Jobs hosted page JSON-LD or sitemap", URL: source.URL})
	return job
}

func normalizeComeetHostedPosting(source Source, job JobPosting) JobPosting {
	id := firstNonEmptyString(strings.TrimPrefix(job.SourceJobID, "static:"), stableJobToken(firstNonEmptyString(job.ApplyURL, job.SourceURL, source.URL), job.Title))
	if id != "" {
		job.SourceJobID = "comeet:" + strings.TrimPrefix(id, "comeet:")
	}
	if job.Company == "" {
		job.Company = sourceCompany(source, companyFromURL(source.URL))
	}
	if job.Country == "" {
		job.Country = normalizeCountry("", job.Location)
	}
	context := strings.Join(compactStringList(job.Title, evidenceTextValue(job.Evidence, "description"), job.EmploymentType, job.Location), " ")
	if job.Level == "" {
		job.Level = inferLevel(context)
	}
	if job.RoleFamily == "" {
		job.RoleFamily = inferRoleFamily(context)
	}
	job.Strategy = TierATS
	job.Confidence = 0.8
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "Comeet hosted page JSON-LD or sitemap", URL: source.URL})
	return job
}

func jibeJobIDFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		if (part == "jobs" || part == "job") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

func jobScoreJobIDFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		switch part {
		case "jobs", "job", "openings":
			if i+1 < len(parts) {
				return parts[i+1]
			}
		}
	}
	if value := strings.TrimSpace(parsed.Query().Get("sid")); value != "" {
		return value
	}
	if value := strings.TrimSpace(parsed.Query().Get("job_id")); value != "" {
		return value
	}
	return ""
}

func jobScoreCompanyFromURL(parsed *url.URL) string {
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		if part == "careers" && i+1 < len(parts) {
			return strings.ReplaceAll(parts[i+1], "-", " ")
		}
	}
	return companyFromURL(parsed.String())
}

func talentBrewJobIDFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		switch part {
		case "job", "jobs", "jobdetails", "job-detail":
			if i+1 < len(parts) {
				return parts[i+1]
			}
		}
	}
	for _, key := range []string{"jobid", "job_id", "jid", "id"} {
		if value := strings.TrimSpace(parsed.Query().Get(key)); value != "" {
			return value
		}
	}
	if len(parts) > 0 && strings.Contains(strings.ToLower(parts[len(parts)-1]), "job") {
		return parts[len(parts)-1]
	}
	return ""
}

func talentBrewCompanyFromURL(parsed *url.URL) string {
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	host = strings.TrimPrefix(host, "www.")
	if strings.HasSuffix(host, ".talentbrew.com") {
		return strings.ReplaceAll(strings.TrimSuffix(host, ".talentbrew.com"), "-", " ")
	}
	return companyFromURL(parsed.String())
}

func careerPlugStructuredHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return strings.HasSuffix(host, ".careerplug.com") && host != "www.careerplug.com"
}

func careerPlugJobIDFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "jobs" && careerPlugNumericIDPattern.MatchString(parts[i+1]) {
			return parts[i+1]
		}
	}
	return ""
}

func careerPlugJobURL(baseURL *url.URL, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	detailURL := *baseURL
	detailURL.RawQuery = ""
	detailURL.Fragment = ""
	detailURL.Path = "/jobs/" + url.PathEscape(id)
	return detailURL.String()
}

func careerPlugCompanyFromURL(parsed *url.URL) string {
	host := strings.ToLower(parsed.Hostname())
	host = strings.TrimSuffix(host, ".careerplug.com")
	if host == "" || host == "www" {
		return ""
	}
	return strings.ReplaceAll(host, "-", " ")
}

func gemBoardID(parsed *url.URL) string {
	parts := nonEmptyPathParts(parsed)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func gemGraphQLEndpoint(parsed *url.URL) string {
	copy := *parsed
	copy.RawQuery = ""
	copy.Fragment = ""
	copy.Path = "/api/public/graphql"
	return copy.String()
}

func gemPosting(source Source, pageURL *url.URL, boardID string, board gemJobBoardExternal, item gemExternalJobPosting, detail gemExternalJobPosting, endpoint string) (JobPosting, bool) {
	posting := item
	if strings.TrimSpace(detail.ExtID) != "" || strings.TrimSpace(detail.Title) != "" {
		posting = mergeGemPosting(item, detail)
	}
	title := strings.TrimSpace(posting.Title)
	id := firstNonEmptyString(posting.ExtID, posting.ID, stableJobToken(gemJobURL(pageURL, boardID, posting.ExtID), title))
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	description := cleanHTMLText(strings.Join(compactStringList(posting.DescriptionHTML, posting.JobPostSectionHTML.IntroHTML, posting.JobPostSectionHTML.OutroHTML), "\n"))
	compensation := cleanHTMLText(posting.CompensationHTML)
	department := strings.TrimSpace(posting.Job.Department.Name)
	location, country := gemLocationText(posting)
	employment := firstNonEmptyString(posting.Job.EmploymentType, employmentFromText(title, description))
	sourceURL := gemJobURL(pageURL, boardID, id)
	company := sourceCompany(source, firstNonEmptyString(board.TeamDisplayName, posting.Job.TeamDisplayName, board.PageTitle, boardID))
	context := strings.Join(compactStringList(title, description, department, employment), " ")
	evidence := []Evidence{
		{Field: "ats", Text: "Gem public job board GraphQL", URL: endpoint},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: sourceURL})
	}
	if department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: sourceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: sourceURL})
	}
	if compensation != "" {
		evidence = append(evidence, Evidence{Field: "compensation", Text: compensation, URL: sourceURL})
	}
	return JobPosting{
		SourceJobID:    "gem:" + id,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		Level:          inferLevel(context),
		RoleFamily:     inferRoleFamily(context),
		SourceURL:      sourceURL,
		ApplyURL:       sourceURL,
		PostedAt:       gemPostedAt(posting),
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func mergeGemPosting(base gemExternalJobPosting, detail gemExternalJobPosting) gemExternalJobPosting {
	if detail.ID == "" {
		detail.ID = base.ID
	}
	if detail.ExtID == "" {
		detail.ExtID = base.ExtID
	}
	if detail.Title == "" {
		detail.Title = base.Title
	}
	if len(detail.Locations) == 0 {
		detail.Locations = base.Locations
	}
	if detail.Job.ID == "" {
		detail.Job = base.Job
	} else {
		if detail.Job.Department.Name == "" {
			detail.Job.Department = base.Job.Department
		}
		if detail.Job.EmploymentType == "" {
			detail.Job.EmploymentType = base.Job.EmploymentType
		}
		if detail.Job.LocationType == "" {
			detail.Job.LocationType = base.Job.LocationType
		}
		if len(detail.Job.Locations) == 0 {
			detail.Job.Locations = base.Job.Locations
		}
	}
	return detail
}

func gemLocationText(posting gemExternalJobPosting) (string, string) {
	locations := posting.Locations
	if len(locations) == 0 {
		locations = posting.Job.Locations
	}
	values := make([]string, 0, len(locations))
	country := ""
	remote := strings.EqualFold(posting.Job.LocationType, "REMOTE")
	for _, loc := range locations {
		text := firstNonEmptyString(loc.Name, strings.Join(compactStringList(loc.City, loc.ISOCountry), ", "))
		if text != "" {
			values = append(values, text)
		}
		if country == "" {
			country = canonicalCountry(loc.ISOCountry)
		}
		if loc.IsRemote {
			remote = true
		}
	}
	if len(values) == 0 && remote {
		values = append(values, "Remote")
	}
	if country == "" {
		country = normalizeCountry("", strings.Join(values, "; "))
	}
	return strings.Join(compactStringList(values...), "; "), country
}

func gemJobURL(pageURL *url.URL, boardID string, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	copy := *pageURL
	copy.RawQuery = ""
	copy.Fragment = ""
	copy.Path = "/" + strings.Trim(boardID, "/") + "/" + url.PathEscape(id)
	return copy.String()
}

func gemPostedAt(posting gemExternalJobPosting) *time.Time {
	ts := posting.FirstPublishedTsSec
	if ts == 0 {
		ts = posting.StartDateTs
	}
	if ts <= 0 {
		return nil
	}
	if ts > 9999999999 {
		ts = ts / 1000
	}
	t := time.Unix(ts, 0).UTC()
	return &t
}

func hireologyConfigFromHTML(document string) (hireologyStartingData, error) {
	raw := firstRegexpGroup(hireologyStartingDataPattern, document)
	if raw == "" {
		return hireologyStartingData{}, errors.New("hireology startingData not found")
	}
	var config hireologyStartingData
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return hireologyStartingData{}, err
	}
	config.APIURL = strings.TrimRight(strings.TrimSpace(config.APIURL), "/")
	config.AppURL = strings.TrimRight(strings.TrimSpace(firstNonEmptyString(config.AppURL, "https://careers.hireology.com")), "/")
	config.APIToken = strings.TrimSpace(config.APIToken)
	config.CareersPath = strings.Trim(strings.TrimSpace(config.CareersPath), "/")
	if config.APIURL == "" || config.APIToken == "" || config.CareersPath == "" {
		return hireologyStartingData{}, errors.New("hireology startingData missing API url, token, or careers path")
	}
	return config, nil
}

func hireologyJobsEndpoint(config hireologyStartingData) (string, error) {
	base, err := url.Parse(strings.TrimRight(config.APIURL, "/") + "/")
	if err != nil {
		return "", err
	}
	return base.ResolveReference(&url.URL{Path: "public/careers/" + strings.Trim(config.CareersPath, "/")}).String(), nil
}

func hireologyPosting(source Source, pageURL *url.URL, config hireologyStartingData, item hireologyJob, endpoint string) (JobPosting, bool) {
	id := firstNonEmptyString(rawJSONToken(item.ID), hireologyJobIDFromURL(item.CareerSiteURL), hireologyJobIDFromURL(item.CareerSitePath))
	title := strings.TrimSpace(item.Name)
	if id == "" || title == "" || !hireologyOpenStatus(item.Status) {
		return JobPosting{}, false
	}
	description := cleanHTMLText(firstNonEmptyString(item.JobDescription, item.SEODescription))
	location, country := hireologyLocationText(item)
	employment := firstNonEmptyString(item.EmploymentStatus, employmentFromText(title, description))
	sourceURL := hireologyAbsoluteURL(pageURL, firstNonEmptyString(item.CareerSiteURL, item.CareerSitePath, hireologyJobURL(config, id)))
	applyURL := hireologyAbsoluteURL(pageURL, firstNonEmptyString(item.ApplicationPath, sourceURL))
	company := sourceCompany(source, firstNonEmptyString(item.Organization.Name, hireologyCompanyFromURL(pageURL), config.CareersPath))
	compensation := hireologyCompensationText(item.Compensation)
	evidenceURL := firstNonEmptyString(sourceURL, applyURL, pageURL.String())
	evidence := []Evidence{
		{Field: "ats", Text: "Hireology public careers API", URL: endpoint},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: evidenceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: evidenceURL})
	}
	if compensation != "" {
		evidence = append(evidence, Evidence{Field: "compensation", Text: compensation, URL: evidenceURL})
	}
	context := strings.Join(compactStringList(title, description, employment, item.JobFamily.Name), " ")
	return JobPosting{
		SourceJobID:    "hireology:" + id,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		Level:          inferLevel(context),
		RoleFamily:     inferRoleFamily(context),
		SourceURL:      sourceURL,
		ApplyURL:       firstNonEmptyString(applyURL, sourceURL),
		PostedAt:       parseTimePtr(item.CreatedAt),
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func normalizeHireologyJSONLDPosting(source Source, pageURL *url.URL, job JobPosting) (JobPosting, bool) {
	id := firstNonEmptyString(hireologyJobIDFromURL(pageURL.String()), hireologyJobIDFromURL(job.ApplyURL), hireologyJobIDFromURL(job.SourceURL), strings.TrimPrefix(job.SourceJobID, "static:"))
	if id == "" || job.Title == "" {
		return JobPosting{}, false
	}
	job.SourceJobID = "hireology:" + id
	if job.Company == "" {
		job.Company = sourceCompany(source, hireologyCompanyFromURL(pageURL))
	}
	if job.ApplyURL == "" {
		job.ApplyURL = hireologyJobURL(hireologyStartingData{AppURL: pageURL.Scheme + "://" + pageURL.Host, CareersPath: hireologyBoardPath(pageURL)}, id)
	}
	job.SourceURL = pageURL.String()
	job.Strategy = TierATS
	job.Confidence = 0.84
	context := strings.Join(compactStringList(job.Title, evidenceTextValue(job.Evidence, "description"), job.EmploymentType, job.Location), " ")
	if job.Level == "" {
		job.Level = inferLevel(context)
	}
	if job.RoleFamily == "" {
		job.RoleFamily = inferRoleFamily(context)
	}
	job.Evidence = append(job.Evidence, Evidence{Field: "ats", Text: "Hireology hosted JobPosting JSON-LD", URL: pageURL.String()})
	return job, true
}

func hireologyOpenStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "" || status == "open" || status == "published"
}

func hireologyLocationText(item hireologyJob) (string, string) {
	locations := make([]string, 0, len(item.Locations))
	country := ""
	for _, loc := range item.Locations {
		text := strings.Join(compactStringList(loc.City, loc.State), ", ")
		if text == "" {
			text = strings.TrimSpace(loc.Address)
		}
		if text != "" {
			locations = append(locations, text)
		}
		if country == "" && (loc.City != "" || loc.State != "" || loc.Address != "") {
			country = "US"
		}
	}
	if len(locations) == 0 && item.Remote {
		locations = append(locations, "Remote")
	}
	if country == "" {
		country = normalizeCountry("", strings.Join(locations, "; "))
	}
	return strings.Join(compactStringList(locations...), "; "), country
}

func hireologyCompensationText(comp hireologyCompensation) string {
	if comp.IsRange && comp.RangeMin != "" && comp.RangeMax != "" {
		return strings.Join(compactStringList(comp.RangeMin+"-"+comp.RangeMax, comp.Period, comp.Frequency), " ")
	}
	if comp.SingleAmount != "" && comp.SingleAmount != "0.0" {
		return strings.Join(compactStringList(comp.SingleAmount, comp.Period, comp.Frequency), " ")
	}
	return ""
}

func hireologyAbsoluteURL(base *url.URL, rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err == nil && parsed.IsAbs() {
		return parsed.String()
	}
	if strings.HasPrefix(rawURL, "/careers/") {
		rawURL = strings.TrimPrefix(rawURL, "/careers")
	}
	return resolveStaticURL(base, rawURL)
}

func hireologyJobURL(config hireologyStartingData, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	appURL := firstNonEmptyString(config.AppURL, "https://careers.hireology.com")
	return strings.TrimRight(appURL, "/") + "/" + strings.Trim(config.CareersPath, "/") + "/" + url.PathEscape(id) + "/description"
}

func hireologyJobIDFromURL(rawURL string) string {
	return firstRegexpGroup(hireologyDetailIDPattern, rawURL)
}

func hireologyBoardPath(parsed *url.URL) string {
	parts := nonEmptyPathParts(parsed)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func hireologyCompanyFromURL(parsed *url.URL) string {
	return strings.ReplaceAll(hireologyBoardPath(parsed), "-", " ")
}

func jobsoidPosting(source Source, item jobsoidJob, endpoint string) (JobPosting, bool) {
	title := firstNonEmptyString(item.JobTitle, item.Title)
	id := firstNonEmptyString(item.JobID, item.Code, rawJSONToken(item.ID), stableJobToken(firstNonEmptyString(item.ApplyURL, item.ApplyURLSnake, item.JobURL, item.URL), title))
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	description := cleanHTMLText(firstNonEmptyString(item.JobDescription, item.Description))
	department := firstNonEmptyString(item.DepartmentName, item.Department)
	location, country := jobsoidLocation(item)
	employment := firstNonEmptyString(item.EmploymentType, item.JobType, jobsoidCustomField(item, "employment_type", "employment type", "job_type", "job type"), employmentFromText(title, description))
	applyURL := firstNonEmptyString(item.ApplyURL, item.ApplyURLSnake, item.JobURL, item.URL, jobsoidHostedURL(source.URL, id, title))
	postedAt := parseTimePtr(firstNonEmptyString(item.DatePosted, item.PublishedAt, item.CreatedAt))
	evidenceURL := firstNonEmptyString(applyURL, source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "Jobsoid published jobs API", URL: endpoint},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: evidenceURL})
	}
	if department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: evidenceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: evidenceURL})
	}
	return JobPosting{
		SourceJobID:    "jobsoid:" + id,
		Company:        sourceCompany(source, sourceHost(source.URL)),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		Level:          inferLevel(title + " " + description + " " + employment),
		RoleFamily:     inferRoleFamily(title + " " + description + " " + department),
		SourceURL:      firstNonEmptyString(item.JobURL, item.URL, applyURL, source.URL),
		ApplyURL:       applyURL,
		PostedAt:       postedAt,
		Live:           true,
		Confidence:     0.85,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func jobsoidLocation(item jobsoidJob) (string, string) {
	location := firstNonEmptyString(item.Location, item.LocationName, strings.Join(compactStringList(item.City, item.State, item.Country), ", "))
	return location, normalizeCountry(item.Country, location)
}

type freshteamCard struct {
	Href string
	HTML string
}

func freshteamJobCards(document string) []freshteamCard {
	matches := anchorTagPattern.FindAllString(document, -1)
	cards := make([]freshteamCard, 0, len(matches))
	seen := map[string]bool{}
	for _, tag := range matches {
		href := firstRegexpGroup(hrefAttrPattern, tag)
		if href == "" {
			continue
		}
		lowerHref := strings.ToLower(href)
		if !strings.Contains(lowerHref, "/jobs/") {
			continue
		}
		text := cleanHTMLText(tag)
		if text == "" || !freshteamLooksLikeJob(text, tag) {
			continue
		}
		key := strings.TrimSpace(href)
		if seen[key] {
			continue
		}
		seen[key] = true
		cards = append(cards, freshteamCard{Href: href, HTML: tag})
	}
	return cards
}

func freshteamPosting(source Source, endpoint string, card freshteamCard) (JobPosting, bool) {
	baseURL, err := parseSourceURL(endpoint)
	if err != nil {
		return JobPosting{}, false
	}
	applyURL := resolveStaticURL(baseURL, html.UnescapeString(card.Href))
	title := freshteamTitle(card.HTML)
	if title == "" {
		return JobPosting{}, false
	}
	cardText := cleanHTMLText(card.HTML)
	if !freshteamLooksLikeJob(cardText, card.HTML) {
		return JobPosting{}, false
	}
	location := freshteamLocation(card.HTML, cardText)
	employment := firstNonEmptyString(freshteamEmployment(card.HTML), employmentFromText(title, cardText))
	id := freshteamJobID(applyURL, title)
	if id == "" {
		return JobPosting{}, false
	}
	description := freshteamDescription(cardText, title, location, employment)
	evidence := []Evidence{
		{Field: "ats", Text: "Freshteam hosted careers page", URL: endpoint},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
	}
	if employment != "" {
		evidence = append(evidence, Evidence{Field: "employment_type", Text: employment, URL: applyURL})
	}
	return JobPosting{
		SourceJobID:    "freshteam:" + id,
		Company:        sourceCompany(source, sourceHost(source.URL)),
		Title:          title,
		Location:       location,
		Country:        normalizeCountry("", firstNonEmptyString(location, cardText)),
		EmploymentType: employment,
		Level:          inferLevel(title + " " + description + " " + employment),
		RoleFamily:     inferRoleFamily(title + " " + description),
		SourceURL:      source.URL,
		ApplyURL:       firstNonEmptyString(applyURL, source.URL),
		Live:           true,
		Confidence:     0.78,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func freshteamLooksLikeJob(text string, rawHTML string) bool {
	lower := strings.ToLower(text + " " + rawHTML)
	if !strings.Contains(lower, "software") && !strings.Contains(lower, "engineer") && !strings.Contains(lower, "developer") && !strings.Contains(lower, "intern") && !strings.Contains(lower, "graduate") && !strings.Contains(lower, "full time") && !strings.Contains(lower, "full-time") {
		return false
	}
	return !strings.Contains(lower, "no jobs found")
}

func freshteamTitle(rawHTML string) string {
	for _, pattern := range []*regexp.Regexp{freshteamTitlePattern, freshteamHeadingPattern} {
		if title := cleanHTMLText(firstRegexpGroup(pattern, rawHTML)); title != "" {
			return title
		}
	}
	text := cleanHTMLText(rawHTML)
	for _, separator := range []string{" Location:", " Remote ", " Full Time", " Full-Time", " Internship", " Part Time", " Part-Time"} {
		if idx := strings.Index(text, separator); idx > 0 {
			return strings.TrimSpace(text[:idx])
		}
	}
	return text
}

func freshteamLocation(rawHTML string, cardText string) string {
	if location := cleanHTMLText(firstRegexpGroup(freshteamLocationPattern, rawHTML)); location != "" {
		return location
	}
	lower := strings.ToLower(cardText)
	known := []string{
		"San Francisco, California, United States",
		"San Francisco, CA, United States",
		"New York, NY, United States",
		"New York, United States",
		"Seattle, Washington, United States",
		"Toronto, Canada",
		"Vancouver, Canada",
		"London, United Kingdom",
		"Hong Kong",
		"Singapore",
		"Remote",
	}
	for _, value := range known {
		if strings.Contains(lower, strings.ToLower(value)) {
			return value
		}
	}
	return ""
}

func freshteamEmployment(rawHTML string) string {
	value := cleanHTMLText(firstRegexpGroup(freshteamEmploymentPattern, rawHTML))
	if value == "" {
		return ""
	}
	return employmentFromText("", value)
}

func freshteamDescription(cardText string, title string, location string, employment string) string {
	description := strings.TrimSpace(cardText)
	for _, value := range []string{title, location, employment} {
		if value != "" {
			description = strings.TrimSpace(strings.Replace(description, value, " ", 1))
		}
	}
	return normalizeSpace(description)
}

func freshteamJobID(applyURL string, title string) string {
	parsed, err := parseSourceURL(applyURL)
	if err != nil {
		return stableJobToken(applyURL, title)
	}
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		if strings.EqualFold(part, "jobs") && i+1 < len(parts) {
			return stableAccountToken(parts[i+1])
		}
	}
	return stableJobToken(applyURL, title)
}

func applicantProPosting(source Source, item applicantProJob, detail applicantProJobDetail, endpoint string) (JobPosting, bool) {
	id := firstNonEmptyString(rawJSONToken(item.ID), rawJSONToken(detail.ID), stableJobToken(firstNonEmptyString(item.JobURL, item.URL, detail.JobURL, detail.URL), firstNonEmptyString(item.Title, detail.Title)))
	title := firstNonEmptyString(detail.Title, item.Title)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	description := cleanHTMLText(firstNonEmptyString(detail.AdvertisingDescriptionHTML, detail.AdvertisingDescription, detail.Description))
	if detail.Benefits != "" {
		description = strings.Join(compactStringList(description, cleanHTMLText(detail.Benefits)), " ")
	}
	department := firstNonEmptyString(item.OrgTitle, item.Department)
	location := applicantProLocation(item, detail)
	country := applicantProCountry(item, location)
	employment := firstNonEmptyString(item.EmploymentType, item.Classification, employmentFromText(title, description))
	applyURL := firstNonEmptyString(item.JobURL, item.URL, detail.JobURL, detail.URL, applicantProHostedURL(source.URL, id))
	evidenceURL := firstNonEmptyString(applyURL, source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "ApplicantPro public jobs API", URL: endpoint},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: evidenceURL})
	}
	if department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: evidenceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: evidenceURL})
	}
	if pay := firstNonEmptyString(detail.PayDetails, item.PayDetails); pay != "" {
		evidence = append(evidence, Evidence{Field: "compensation", Text: pay, URL: evidenceURL})
	}
	return JobPosting{
		SourceJobID:    "applicantpro:" + id,
		Company:        sourceCompany(source, sourceHost(source.URL)),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		Level:          inferLevel(title + " " + description + " " + employment),
		RoleFamily:     inferRoleFamily(title + " " + description + " " + department),
		SourceURL:      source.URL,
		ApplyURL:       firstNonEmptyString(applyURL, source.URL),
		PostedAt:       parseTimePtr(firstNonEmptyString(item.StartDateRef, item.DatePosted)),
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func applicantProLocation(item applicantProJob, detail applicantProJobDetail) string {
	return firstNonEmptyString(
		item.JobLocation,
		strings.Join(compactStringList(firstNonEmptyString(detail.City, item.City), firstNonEmptyString(detail.StateName, item.StateName, item.State), applicantProCountry(item, "")), ", "),
	)
}

func applicantProCountry(item applicantProJob, location string) string {
	switch strings.ToUpper(strings.TrimSpace(firstNonEmptyString(item.ISO3, item.Country))) {
	case "USA", "US":
		return "US"
	case "CAN", "CA":
		return "Canada"
	case "GBR", "GB", "UK":
		return "UK"
	case "SGP", "SG":
		return "Singapore"
	case "HKG", "HK":
		return "Hong Kong"
	}
	return normalizeCountry(canonicalCountry(firstNonEmptyString(item.Country, item.ISO3)), location)
}

func jobsoidCustomField(item jobsoidJob, names ...string) string {
	for _, name := range names {
		for key, value := range item.CustomFields {
			if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(name)) {
				return strings.TrimSpace(value)
			}
		}
		for _, field := range item.CustomFieldList {
			if strings.EqualFold(strings.TrimSpace(field.Name), strings.TrimSpace(name)) {
				return strings.TrimSpace(field.Value)
			}
		}
	}
	return ""
}

func firstNonEmptyJobsoidJobs(values ...[]jobsoidJob) []jobsoidJob {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func rawJSONToken(value json.RawMessage) string {
	trimmed := strings.TrimSpace(string(value))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if err := json.Unmarshal(value, &number); err == nil {
		return strings.TrimSpace(number.String())
	}
	return strings.Trim(trimmed, `"`)
}

func talentLyftPosting(source Source, subdomain string, item talentLyftJob, endpoint string) (JobPosting, bool) {
	title := firstNonEmptyString(item.Title, item.Name, item.JobTitle)
	id := firstNonEmptyString(rawJSONToken(item.ID), rawJSONToken(item.IDUpper), stableJobToken(firstNonEmptyString(item.ApplyURL, item.ApplyURLSnake, item.JobURL, item.URL), title))
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	description := cleanHTMLText(firstNonEmptyString(item.JobDescription, item.Description))
	department := firstNonEmptyString(item.DepartmentName, item.Department.Name)
	location, country := talentLyftLocationText(item)
	employment := firstNonEmptyString(item.EmploymentType, item.Type, item.JobType, talentLyftCustomField(item, "employmentType", "employment_type", "job type", "type"), employmentFromText(title, description))
	applyURL := firstNonEmptyString(item.ApplyURL, item.ApplyURLSnake, item.JobURL, item.URL, talentLyftHostedURL(source.URL, id, title))
	postedAt := parseTimePtr(firstNonEmptyString(item.PublishedAt, item.DatePosted, item.CreatedAt))
	evidenceURL := firstNonEmptyString(applyURL, source.URL)
	evidence := []Evidence{
		{Field: "ats", Text: "TalentLyft public jobs API", URL: endpoint},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: evidenceURL})
	}
	if department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: evidenceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: evidenceURL})
	}
	return JobPosting{
		SourceJobID:    "talentlyft:" + stableAccountToken(subdomain) + ":" + id,
		Company:        sourceCompany(source, subdomain),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		Level:          inferLevel(title + " " + description + " " + employment),
		RoleFamily:     inferRoleFamily(title + " " + description + " " + department),
		SourceURL:      firstNonEmptyString(item.JobURL, item.URL, applyURL, source.URL),
		ApplyURL:       applyURL,
		PostedAt:       postedAt,
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func talentLyftLocationText(item talentLyftJob) (string, string) {
	locationObject := item.Location
	if locationObject.Name == "" && locationObject.City == "" && locationObject.Country == "" {
		locationObject = item.LocationData
	}
	if locationObject.Name == "" && locationObject.City == "" && locationObject.Country == "" {
		locationObject = item.LocationObject
	}
	location := firstNonEmptyString(item.LocationName, item.LocationText, locationObject.Name, strings.Join(compactStringList(locationObject.City, locationObject.State, locationObject.Country), ", "))
	country := firstNonEmptyString(item.Country, locationObject.Country)
	return location, normalizeCountry(country, location)
}

func talentLyftCustomField(item talentLyftJob, names ...string) string {
	for _, name := range names {
		for key, value := range item.CustomFields {
			if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(name)) {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func firstNonEmptyTalentLyftJobs(values ...[]talentLyftJob) []talentLyftJob {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func paylocityPosting(source Source, config paylocityConfig, company string, item paylocityJob, detail paylocityDetail) (JobPosting, bool) {
	id := paylocityJobID(item.JobID)
	title := firstNonEmptyString(detail.Job.Title, item.JobTitle)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	detailURL := paylocityHostedURL(source.URL, "Details", id, company, title)
	applyURL := paylocityHostedURL(source.URL, "Apply", id, company, title)
	description := firstEvidenceText(detail.Job.Evidence, "description")
	if description == "" {
		description = cleanHTMLText(item.Description)
	}
	location, country := paylocityLocationText(item.JobLocation)
	if detail.Job.Location != "" {
		location = detail.Job.Location
		country = detail.Job.Country
	}
	employment := firstNonEmptyString(detail.Job.EmploymentType, paylocityEmploymentType(title, item))
	postedAt := parseTimePtr(item.PublishedDate)
	if postedAt == nil {
		postedAt = detail.Job.PostedAt
	}
	company = sourceCompany(source, firstNonEmptyString(detail.Job.Company, company, config.CompanySlug, config.FeedID))
	evidence := []Evidence{
		{Field: "ats", Text: "Paylocity hosted jobs pageData", URL: source.URL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: firstNonEmptyString(detailURL, applyURL)})
	}
	if item.HiringDepartment != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: item.HiringDepartment, URL: firstNonEmptyString(detailURL, applyURL)})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: firstNonEmptyString(detailURL, applyURL)})
	}
	if detail.Compensation != "" {
		evidence = append(evidence, Evidence{Field: "compensation", Text: detail.Compensation, URL: firstNonEmptyString(detailURL, applyURL)})
	}
	account := stableAccountToken(firstNonEmptyString(config.FeedID, config.CompanySlug, company))
	return JobPosting{
		SourceJobID:    "paylocity:" + account + ":" + id,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		RoleFamily:     inferRoleFamily(title + " " + description + " " + item.HiringDepartment),
		SourceURL:      source.URL,
		ApplyURL:       firstNonEmptyString(applyURL, detailURL, source.URL),
		PostedAt:       postedAt,
		Live:           true,
		Confidence:     0.84,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func (data paylocityFeedData) company() string {
	return firstNonEmptyString(data.DisplayName, data.DisplayNameLegacy)
}

func (data paylocityFeedData) format() string {
	return firstNonEmptyString(data.Format, "JSON")
}

func (data paylocityFeedData) jobs() []paylocityFeedJob {
	if len(data.Jobs) > 0 {
		return data.Jobs
	}
	if len(data.JobsLegacy) > 0 {
		return data.JobsLegacy
	}
	if len(data.JobsXML) > 0 {
		return data.JobsXML
	}
	return data.JobsXMLLower
}

func (item paylocityFeedJob) id() int64 {
	if item.JobID > 0 {
		return item.JobID
	}
	return item.JobIDLegacy
}

func (item paylocityFeedJob) title() string {
	return firstNonEmptyString(item.Title, item.TitleLegacy)
}

func paycomPortalSessionFromHTML(document string) paycomPortalSession {
	session := paycomPortalSession{
		SessionJWT: strings.TrimSpace(firstRegexpGroup(paycomSessionJWTPattern, document)),
	}
	rawConfig := firstRegexpGroup(paycomLibConfigPattern, document)
	if rawConfig == "" {
		return session
	}
	unquoted, err := strconv.Unquote(`"` + rawConfig + `"`)
	if err != nil {
		return session
	}
	var config paycomLibConfig
	if err := json.Unmarshal([]byte(unquoted), &config); err != nil {
		return session
	}
	session.ServiceURL = strings.TrimSpace(config.ATSPortalMantleServiceURL)
	return session
}

func paycomHeaders(session paycomPortalSession) map[string]string {
	return map[string]string{
		"Authorization":          session.SessionJWT,
		"Locale":                 "en-US",
		"Translation-Highlights": "false",
	}
}

func paycomAPIURL(serviceURL string, path string) string {
	base := strings.TrimRight(firstNonEmptyString(strings.TrimSpace(serviceURL), "https://portal-applicant-tracking.us-cent.paycomonline.net/"), "/")
	return base + "/" + strings.TrimLeft(path, "/")
}

func paycomPosting(source Source, preview paycomJobPreview, detail paycomJobDetail) (JobPosting, bool) {
	id := firstNonEmptyString(paycomDetailID(detail), preview.id())
	title := firstNonEmptyString(detail.JobTitle, preview.JobTitle)
	if id == "" || title == "" {
		return JobPosting{}, false
	}
	location := firstNonEmptyString(paycomDetailLocation(detail), preview.Locations)
	country := normalizeCountry("", location)
	description := cleanHTMLText(firstNonEmptyString(detail.Description, detail.Qualifications, preview.Description))
	employment := employmentFromText(title+" "+detail.PositionType, firstNonEmptyString(detail.PositionType, preview.PositionType))
	department := firstNonEmptyString(detail.JobCategory, detail.Level)
	compensation := strings.TrimSpace(detail.SalaryRange)
	postedAt := parseTimePtr(firstNonEmptyString(preview.PostedOn, detail.StartDate))
	applyURL := paycomJobURL(source.URL, id)
	context := strings.Join(compactStringList(title, description, department, employment, location), " ")
	evidence := []Evidence{
		{Field: "ats", Text: "Paycom public job posting API", URL: applyURL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: applyURL})
	}
	if department != "" {
		evidence = append(evidence, Evidence{Field: "department", Text: department, URL: applyURL})
	}
	if compensation != "" {
		evidence = append(evidence, Evidence{Field: "compensation", Text: compensation, URL: applyURL})
	}
	return JobPosting{
		SourceJobID:    "paycom:" + id,
		Company:        sourceCompany(source, paycomClientKey(source.URL)),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employment,
		Level:          inferLevel(context),
		RoleFamily:     inferRoleFamily(context),
		SourceURL:      source.URL,
		ApplyURL:       firstNonEmptyString(applyURL, source.URL),
		PostedAt:       postedAt,
		Live:           true,
		Confidence:     0.82,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func paycomDetailID(detail paycomJobDetail) string {
	if detail.JobID <= 0 {
		return ""
	}
	return strconv.FormatInt(detail.JobID, 10)
}

func paycomDetailLocation(detail paycomJobDetail) string {
	locations := compactStringList(detail.Location, detail.City)
	locations = append(locations, compactStringList(detail.SecondaryLocations...)...)
	if strings.TrimSpace(detail.RemoteType) != "" {
		locations = append(locations, strings.TrimSpace(detail.RemoteType))
	}
	return strings.Join(compactStringList(locations...), "; ")
}

func paycomJobIDFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	for i, part := range parts {
		if strings.EqualFold(part, "jobs") && i+1 < len(parts) {
			candidate := strings.TrimSpace(parts[i+1])
			if _, err := strconv.ParseInt(candidate, 10, 64); err == nil {
				return candidate
			}
		}
	}
	return ""
}

func paycomClientKey(rawURL string) string {
	return strings.ToUpper(strings.TrimSpace(firstRegexpGroup(paycomClientKeyPattern, rawURL)))
}

func paycomJobURL(rawURL string, jobID string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil || strings.TrimSpace(jobID) == "" {
		return ""
	}
	key := paycomClientKey(rawURL)
	if key == "" {
		return rawURL
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = "/v4/ats/web.php/portal/" + key + "/jobs/" + strings.TrimSpace(jobID)
	return parsed.String()
}

func (item paylocityFeedJob) companyName() string {
	return firstNonEmptyString(item.CompanyName, item.CompanyNameLegacy)
}

func (item paylocityFeedJob) applyURL() string {
	return firstNonEmptyString(item.ApplyURL, item.ApplyURLLegacy)
}

func (item paylocityFeedJob) displayURL() string {
	return firstNonEmptyString(item.DisplayURL, item.DisplayURLLegacy)
}

func (item paylocityFeedJob) publishedDate() string {
	return firstNonEmptyString(item.PublishedDate, item.PublishedDateLegacy)
}

func (item paylocityFeedJob) description() string {
	return firstNonEmptyString(item.Description, item.DescriptionLegacy)
}

func (item paylocityFeedJob) requirements() string {
	return firstNonEmptyString(item.Requirements, item.RequirementsLegacy)
}

func (item paylocityFeedJob) salary() string {
	return firstNonEmptyString(item.SalaryDescription, item.SalaryDescriptionLegacy)
}

func (item paylocityFeedJob) department() string {
	return firstNonEmptyString(item.HiringDepartment, item.HiringDepartmentLegacy)
}

func (item paylocityFeedJob) jobTypes() string {
	return firstNonEmptyString(item.JobTypes, item.JobTypesLegacy)
}

func (item paylocityFeedJob) jobTypesArray() []string {
	if len(item.JobTypesArray) > 0 {
		return item.JobTypesArray
	}
	return item.JobTypesArrayLegacy
}

func (item paylocityFeedJob) location() paylocityLocation {
	if !paylocityLocationEmpty(item.JobLocation) {
		return item.JobLocation
	}
	return item.JobLocationLegacy
}

func paylocityLocationEmpty(location paylocityLocation) bool {
	return firstNonEmptyString(location.Name, location.LocationDisplayName, location.City, location.State, location.Country, location.Metro) == ""
}

func paylocityDetailFromHTML(source Source, detailURL string, document string) paylocityDetail {
	detail := paylocityDetail{Compensation: paylocityJSONLDCompensation(document)}
	parsed, err := parseSourceURL(detailURL)
	if err != nil {
		return detail
	}
	staticExtractor := NewStaticExtractor()
	jobs := staticExtractor.extractJSONLDJobs(source, parsed, document)
	if len(jobs) > 0 {
		detail.Job = jobs[0]
	}
	return detail
}

func paylocityJSONLDCompensation(document string) string {
	matches := jsonLDScriptPattern.FindAllStringSubmatch(document, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		var payload any
		decoder := json.NewDecoder(strings.NewReader(html.UnescapeString(strings.TrimSpace(match[1]))))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			continue
		}
		var nodes []map[string]any
		collectJSONLDJobPostings(payload, &nodes)
		for _, node := range nodes {
			if text := jsonLDSalaryText(node["baseSalary"]); text != "" {
				return text
			}
		}
	}
	return ""
}

func jsonLDSalaryText(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		currency := jsonLDStringField(typed, "currency")
		switch salary := typed["value"].(type) {
		case map[string]any:
			minimum := jsonLDStringField(salary, "minValue")
			maximum := jsonLDStringField(salary, "maxValue")
			unit := jsonLDStringField(salary, "unitText")
			switch {
			case minimum != "" && maximum != "":
				return strings.Join(compactStringList(minimum+" to "+maximum, currency, unit), " ")
			case minimum != "":
				return strings.Join(compactStringList(minimum+"+", currency, unit), " ")
			case maximum != "":
				return strings.Join(compactStringList("up to "+maximum, currency, unit), " ")
			default:
				if value := jsonLDStringField(salary, "value"); value != "" {
					return strings.Join(compactStringList(value, currency, unit), " ")
				}
			}
		default:
			if value := jsonLDString(salary); value != "" {
				return strings.Join(compactStringList(value, currency), " ")
			}
		}
	case []any:
		for _, item := range typed {
			if text := jsonLDSalaryText(item); text != "" {
				return text
			}
		}
	}
	return ""
}

func firstEvidenceText(evidence []Evidence, field string) string {
	for _, item := range evidence {
		if strings.EqualFold(item.Field, field) {
			return item.Text
		}
	}
	return ""
}

func doverPosting(source Source, slug string, page doverCareersPage, item doverJob) (JobPosting, bool) {
	title := normalizeSpace(item.Title)
	id := strings.TrimSpace(item.ID)
	if title == "" || id == "" || !item.IsPublished || item.IsSample {
		return JobPosting{}, false
	}
	location, country := doverLocationText(item.Locations)
	description := strings.Join(compactStringList(title, location), " ")
	return JobPosting{
		SourceJobID:    "dover:" + id,
		Company:        sourceCompany(source, firstNonEmptyString(page.Name, page.Slug, slug)),
		Title:          title,
		Location:       location,
		Country:        country,
		RoleFamily:     inferRoleFamily(title + " " + location),
		Level:          inferLevel(title + " " + location),
		EmploymentType: employmentFromText(title, ""),
		SourceURL:      doverJobURL(source.URL, slug, id),
		ApplyURL:       doverJobURL(source.URL, slug, id),
		Evidence: []Evidence{
			{Field: "ats", Text: "Dover public careers-page jobs API", URL: doverJobURL(source.URL, slug, id)},
			{Field: "description", Text: description, URL: doverJobURL(source.URL, slug, id)},
			{Field: "company", Text: firstNonEmptyString(page.Name, page.PrimaryDomain), URL: source.URL},
		},
	}, true
}

func doverLocationText(locations []doverLocation) (string, string) {
	parts := make([]string, 0, len(locations))
	country := ""
	for _, location := range locations {
		locCountry := canonicalCountry(location.LocationOption.Country)
		if country == "" {
			country = locCountry
		}
		text := firstNonEmptyString(location.Name, location.LocationOption.DisplayName, strings.Join(compactStringList(location.LocationOption.City, location.LocationOption.State, locCountry), ", "))
		if strings.EqualFold(location.LocationType, "remote") && text != "" && !strings.Contains(strings.ToLower(text), "remote") {
			text = "Remote, " + text
		}
		parts = append(parts, text)
	}
	return strings.Join(compactStringList(parts...), "; "), country
}

func doverSlugFromURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	parts := nonEmptyPathParts(parsed)
	if len(parts) == 0 {
		return ""
	}
	if strings.EqualFold(parts[0], "jobs") && len(parts) > 1 {
		return parts[1]
	}
	return parts[0]
}

func doverBaseURL(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err == nil && parsed.Scheme != "" {
		if strings.EqualFold(parsed.Hostname(), "jobs.dover.com") {
			return "https://app.dover.com"
		}
		return parsed.Scheme + "://" + parsed.Host
	}
	return "https://app.dover.com"
}

func doverJobURL(rawURL string, slug string, jobID string) string {
	baseURL := doverBaseURL(rawURL)
	slug = strings.Trim(strings.TrimSpace(slug), "/")
	jobID = strings.Trim(strings.TrimSpace(jobID), "/")
	if slug == "" || jobID == "" {
		return ""
	}
	return baseURL + "/apply/" + url.PathEscape(slug) + "/" + url.PathEscape(jobID)
}

func trakstarFeedURL(rawURL string) (*url.URL, string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, "", err
	}
	host := strings.ToLower(parsed.Hostname())
	parts := nonEmptyPathParts(parsed)
	if len(parts) > 0 && strings.EqualFold(parts[0], "jobfeeds") {
		slug := ""
		if len(parts) > 1 {
			slug = parts[1]
		}
		return parsed, firstNonEmptyString(slug, trakstarBoardSlug(parsed)), nil
	}
	slug := trakstarBoardSlug(parsed)
	if slug == "" {
		return nil, "", ErrNoJobs
	}
	if strings.Contains(host, "recruiterbox.com") || strings.EqualFold(host, "jobs.hire.trakstar.com") {
		parsed.Scheme = "https"
		parsed.Host = slug + ".hire.trakstar.com"
	}
	parsed.Path = path.Join("/jobfeeds", slug)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, slug, nil
}

func trakstarBoardSlug(parsed *url.URL) string {
	host := strings.ToLower(parsed.Hostname())
	parts := nonEmptyPathParts(parsed)
	switch {
	case strings.HasSuffix(host, ".hire.trakstar.com"):
		slug := strings.TrimSuffix(host, ".hire.trakstar.com")
		if slug != "" && slug != "jobs" {
			return slug
		}
	case strings.HasSuffix(host, ".recruiterbox.com"):
		slug := strings.TrimSuffix(host, ".recruiterbox.com")
		if slug != "" && slug != "jobs" {
			return slug
		}
	}
	for _, part := range parts {
		lower := strings.ToLower(strings.TrimSpace(part))
		if lower == "" || lower == "jobs" || lower == "job" || lower == "careers" || lower == "apply" {
			continue
		}
		return part
	}
	return ""
}

func trakstarItems(feed trakstarRSS) []trakstarRSSItem {
	items := make([]trakstarRSSItem, 0, len(feed.Channel.Items))
	seen := map[string]struct{}{}
	for _, item := range feed.Channel.Items {
		id := firstNonEmptyString(item.GUID, item.Link, stableJobToken(item.Link, item.Title))
		if id == "" || strings.TrimSpace(item.Title) == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, item)
	}
	return items
}

func trakstarPosting(source Source, slug string, feedURL string, feed trakstarRSS, item trakstarRSSItem) (JobPosting, bool) {
	title := cleanHTMLText(item.Title)
	id := firstNonEmptyString(stableJobToken(item.GUID, title), stableJobToken(item.Link, title))
	if title == "" || id == "" {
		return JobPosting{}, false
	}
	sourceURL := firstNonEmptyString(item.Link, item.GUID, source.URL)
	applyURL := firstNonEmptyString(trakstarApplyURL(item.Description), sourceURL)
	description := cleanHTMLText(item.Description)
	location, country := trakstarLocation(item)
	context := strings.Join(compactStringList(title, item.PositionType, item.Team, location, description), " ")
	evidence := []Evidence{
		{Field: "ats", Text: "Trakstar Hire Recruiterbox RSS job feed", URL: feedURL},
	}
	if description != "" {
		evidence = append(evidence, Evidence{Field: "description", Text: description, URL: sourceURL})
	}
	if location != "" {
		evidence = append(evidence, Evidence{Field: "location", Text: location, URL: sourceURL})
	}
	if item.Team != "" {
		evidence = append(evidence, Evidence{Field: "team", Text: item.Team, URL: sourceURL})
	}
	return JobPosting{
		SourceJobID:    "recruiterbox:" + firstNonEmptyString(slug, stableAccountToken(feed.Channel.Title), "trakstar") + ":" + id,
		Company:        sourceCompany(source, trakstarCompanyName(feed, slug)),
		Title:          title,
		Location:       location,
		Country:        country,
		RoleFamily:     inferRoleFamily(context),
		Level:          inferLevel(context),
		EmploymentType: employmentFromText(title, item.PositionType),
		SourceURL:      sourceURL,
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(item.PubDate),
		Live:           true,
		Confidence:     0.84,
		Strategy:       TierATS,
		Evidence:       evidence,
	}, true
}

func trakstarCompanyName(feed trakstarRSS, slug string) string {
	title := strings.TrimSpace(feed.Channel.Title)
	for _, prefix := range []string{"Jobs at ", "Careers at "} {
		if strings.HasPrefix(title, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(title, prefix))
		}
	}
	return firstNonEmptyString(title, slug)
}

func trakstarLocation(item trakstarRSSItem) (string, string) {
	country := canonicalCountry(item.LocationCountry)
	location := strings.Join(compactStringList(item.LocationCity, item.LocationState, country), ", ")
	if location == "" {
		location = cleanHTMLText(firstRegexpGroup(regexp.MustCompile(`(?is)Location:\s*([^<\n]+)`), item.Description))
		location = strings.Trim(location, " ,")
		if country == "" {
			country = canonicalCountry(location)
		}
	}
	if country == "" {
		country = canonicalCountry(item.LocationCity)
	}
	return location, country
}

func trakstarApplyURL(description string) string {
	for _, match := range hrefAttrPattern.FindAllStringSubmatch(description, -1) {
		if len(match) < 2 {
			continue
		}
		href := html.UnescapeString(strings.TrimSpace(match[1]))
		if href == "" {
			continue
		}
		if strings.Contains(strings.ToLower(href), "apply") || strings.Contains(strings.ToLower(href), "recruiterbox.com/jobs/") {
			return href
		}
	}
	return ""
}

func githubJobListRawCandidates(rawURL string) ([]string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	host := strings.ToLower(parsed.Hostname())
	parts := nonEmptyPathParts(parsed)
	if host == "raw.githubusercontent.com" {
		return []string{parsed.String()}, nil
	}
	if host != "github.com" || len(parts) < 2 {
		return nil, ErrNoJobs
	}
	owner, repo := parts[0], parts[1]
	if len(parts) >= 5 && parts[2] == "blob" {
		return []string{"https://raw.githubusercontent.com/" + owner + "/" + repo + "/" + parts[3] + "/" + strings.Join(parts[4:], "/")}, nil
	}
	file := "README.md"
	if len(parts) > 2 {
		file = strings.Join(parts[2:], "/")
	}
	return []string{
		"https://raw.githubusercontent.com/" + owner + "/" + repo + "/dev/" + file,
		"https://raw.githubusercontent.com/" + owner + "/" + repo + "/main/" + file,
		"https://raw.githubusercontent.com/" + owner + "/" + repo + "/master/" + file,
	}, nil
}

func githubJobListPostings(source Source, rawURL string, document string, maxJobs int) []JobPosting {
	rows := githubJobListCellRows(document)
	jobs := make([]JobPosting, 0, min(len(rows), maxJobs))
	seen := map[string]struct{}{}
	lastCompany := ""
	for _, row := range rows {
		if len(jobs) >= maxJobs {
			break
		}
		cells := row
		if len(cells) < 4 {
			continue
		}
		rawCompany := githubJobListCellText(cells[0])
		company := ""
		if rawCompany == "↳" || rawCompany == "â†³" {
			company = lastCompany
		} else {
			company = githubJobListCleanDecorations(rawCompany)
		}
		if company == "" {
			continue
		}
		lastCompany = company
		rawTitle := githubJobListCellText(cells[1])
		title := githubJobListCleanDecorations(rawTitle)
		location := githubJobListCellText(cells[2])
		applyCell := githubJobListApplyCell(cells)
		applyURL := githubJobListApplyURL(applyCell)
		if strings.Contains(rawTitle, "🔒") || strings.Contains(strings.ToLower(githubJobListCellText(applyCell)), "closed") || applyURL == "" {
			continue
		}
		id := firstNonEmptyString(stableJobToken(applyURL, title), stableAccountToken(company+"-"+title+"-"+location))
		if id == "" {
			continue
		}
		key := strings.ToLower(company + ":" + id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		postedAt := githubJobListPostedAt(cells)
		context := strings.Join(compactStringList(title, location, company), " ")
		evidence := []Evidence{
			{Field: "ats", Text: "GitHub community early-career job list", URL: rawURL},
			{Field: "location", Text: location, URL: source.URL},
			{Field: "age", Text: githubJobListCellText(cells[len(cells)-1]), URL: source.URL},
		}
		if compensation := githubJobListCompensationText(cells); compensation != "" {
			evidence = append(evidence, Evidence{Field: "compensation", Text: compensation, URL: source.URL})
		}
		evidence = append(evidence, githubJobListFlagEvidence(cells, source.URL)...)
		jobs = append(jobs, JobPosting{
			SourceJobID:    "github_job_list:" + stableAccountToken(company) + ":" + id,
			Company:        company,
			Title:          title,
			Location:       location,
			Country:        normalizeCountry("", location),
			EmploymentType: employmentFromText(title, ""),
			RoleFamily:     inferRoleFamily(context),
			Level:          inferLevel(context),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       postedAt,
			Live:           true,
			Confidence:     0.78,
			Strategy:       TierATS,
			Evidence:       evidence,
		})
	}
	return jobs
}

func githubJobListCellRows(document string) [][]string {
	htmlRows := githubJobListRowPattern.FindAllStringSubmatch(document, -1)
	rows := make([][]string, 0, len(htmlRows))
	for _, row := range htmlRows {
		if len(row) < 2 {
			continue
		}
		if cells := githubJobListHTMLCells(row[1]); len(cells) >= 4 {
			rows = append(rows, cells)
		}
	}
	rows = append(rows, githubJobListMarkdownRows(document)...)
	return rows
}

func githubJobListMarkdownRows(document string) [][]string {
	lines := strings.Split(document, "\n")
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		cells := githubJobListMarkdownCells(line)
		if len(cells) < 4 || githubJobListMarkdownSeparator(cells) || githubJobListHeaderCells(cells) {
			continue
		}
		rows = append(rows, cells)
	}
	return rows
}

func githubJobListMarkdownCells(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") || !strings.Contains(strings.Trim(line, "| "), "|") {
		return nil
	}
	parts := strings.Split(strings.Trim(line, "|"), "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, strings.TrimSpace(part))
	}
	return cells
}

func githubJobListMarkdownSeparator(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cleaned := strings.Trim(cell, " :-")
		if cleaned != "" {
			return false
		}
	}
	return true
}

func githubJobListHeaderCells(cells []string) bool {
	if len(cells) < 2 {
		return false
	}
	first := strings.ToLower(githubJobListCellText(cells[0]))
	second := strings.ToLower(githubJobListCellText(cells[1]))
	return strings.Contains(first, "company") &&
		(strings.Contains(second, "role") || strings.Contains(second, "position") || strings.Contains(second, "title"))
}

func githubJobListCleanDecorations(value string) string {
	replacer := strings.NewReplacer("🔥", "", "🎓", "", "🛂", "", "🇺🇸", "", "🔒", "", "🚀", "")
	return strings.TrimSpace(normalizeSpace(replacer.Replace(value)))
}

func githubJobListFlagEvidence(cells []string, sourceURL string) []Evidence {
	raw := strings.Join(cells, " ")
	clean := cleanHTMLText(raw)
	evidence := make([]Evidence, 0, 4)
	if strings.Contains(raw, "🛂") || strings.Contains(clean, "🛂") {
		evidence = append(evidence, Evidence{Field: "visa", Text: "visa sponsorship marker from GitHub community list", URL: sourceURL})
	}
	if strings.Contains(raw, "🇺🇸") || strings.Contains(clean, "🇺🇸") {
		evidence = append(evidence, Evidence{Field: "authorization", Text: "US work authorization marker from GitHub community list", URL: sourceURL})
	}
	if strings.Contains(raw, "🎓") || strings.Contains(clean, "🎓") {
		evidence = append(evidence, Evidence{Field: "new_grad", Text: "new-grad marker from GitHub community list", URL: sourceURL})
	}
	if strings.Contains(raw, "🔥") || strings.Contains(clean, "🔥") {
		evidence = append(evidence, Evidence{Field: "priority", Text: "high-activity marker from GitHub community list", URL: sourceURL})
	}
	return evidence
}

func githubJobListHTMLCells(row string) []string {
	matches := githubJobListCellPattern.FindAllStringSubmatch(row, -1)
	cells := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			cells = append(cells, match[1])
		}
	}
	return cells
}

func githubJobListCellText(cell string) string {
	cell = githubJobListMarkdownTextPattern.ReplaceAllString(cell, "$1")
	cell = strings.ReplaceAll(cell, "</br>", " ")
	cell = strings.ReplaceAll(cell, "<br>", " ")
	cell = strings.ReplaceAll(cell, "<br/>", " ")
	cell = strings.ReplaceAll(cell, "<br />", " ")
	return cleanHTMLText(cell)
}

func githubJobListApplyCell(cells []string) string {
	if len(cells) >= 5 {
		return cells[len(cells)-2]
	}
	if len(cells) >= 4 {
		return cells[3]
	}
	return ""
}

func githubJobListCompensationText(cells []string) string {
	if len(cells) < 6 {
		return ""
	}
	compensation := githubJobListCellText(cells[len(cells)-3])
	lower := strings.ToLower(compensation)
	if compensation == "" || (!strings.ContainsAny(compensation, "$£€¥") && !strings.Contains(lower, "/hr") && !strings.Contains(lower, "salary")) {
		return ""
	}
	return compensation
}

func githubJobListApplyURL(cell string) string {
	for _, match := range githubJobListHrefPattern.FindAllStringSubmatch(cell, -1) {
		if len(match) < 2 {
			continue
		}
		href := html.UnescapeString(strings.TrimSpace(match[1]))
		lower := strings.ToLower(href)
		if href == "" || strings.Contains(lower, "simplify.jobs/p/") || strings.Contains(lower, "simplify.jobs/c/") || strings.Contains(lower, "utm_medium=company") {
			continue
		}
		return href
	}
	for _, match := range githubJobListMarkdownLinkPattern.FindAllStringSubmatch(cell, -1) {
		if len(match) < 2 {
			continue
		}
		href := html.UnescapeString(strings.TrimSpace(match[1]))
		lower := strings.ToLower(href)
		if href == "" || strings.Contains(lower, "simplify.jobs/p/") || strings.Contains(lower, "simplify.jobs/c/") || strings.Contains(lower, "utm_medium=company") {
			continue
		}
		return href
	}
	return ""
}

func githubJobListPostedAt(cells []string) *time.Time {
	if len(cells) == 0 {
		return nil
	}
	age := strings.ToLower(githubJobListCellText(cells[len(cells)-1]))
	if strings.HasSuffix(age, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(age, "d"))
		if err == nil && days >= 0 && days <= 365 {
			posted := time.Now().UTC().AddDate(0, 0, -days)
			return &posted
		}
	}
	return nil
}

func paylocityLocationText(location paylocityLocation) (string, string) {
	country := canonicalCountry(location.Country)
	text := strings.Join(compactStringList(location.City, location.State, country), ", ")
	if text == "" {
		text = strings.Join(compactStringList(location.LocationDisplayName, location.Name, country), ", ")
	}
	return text, country
}

func paylocityEmploymentType(title string, item paylocityJob) string {
	return employmentFromText(title, item.HiringDepartment)
}

func paylocityJobID(value int64) string {
	if value <= 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

func paylocityParseJobID(value string) int64 {
	parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return parsed
}

func paylocityHostedURL(rawURL string, action string, jobID string, company string, title string) string {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return ""
	}
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	copy := *parsed
	copy.Path = "/" + path.Join("recruiting", "jobs", action, jobID, paylocitySlug(company), paylocitySlug(title))
	copy.RawQuery = ""
	copy.Fragment = ""
	return copy.String()
}

func paylocitySlug(value string) string {
	value = normalizeSpace(value)
	if value == "" {
		return ""
	}
	if !strings.Contains(value, " ") {
		return strings.Trim(value, "/")
	}
	parts := strings.Fields(value)
	for i, part := range parts {
		parts[i] = strings.Trim(part, `/\.,:;'"()[]{}<>`)
	}
	return strings.Join(compactStringList(parts...), "-")
}

func successFactorsTitle(title string, location string) string {
	title = normalizeSpace(title)
	if title == "" {
		return ""
	}
	if location != "" {
		suffix := "(" + strings.TrimSpace(location) + ")"
		if strings.HasSuffix(title, suffix) {
			return strings.TrimSpace(strings.TrimSuffix(title, suffix))
		}
	}
	if strings.HasSuffix(title, ")") {
		open := strings.LastIndex(title, "(")
		if open > 0 {
			candidate := strings.TrimSpace(title[open+1 : len(title)-1])
			if successFactorsLooksLikeLocation(candidate) {
				return strings.TrimSpace(title[:open])
			}
		}
	}
	return title
}

func successFactorsLocationFromTitle(title string) string {
	title = normalizeSpace(title)
	if !strings.HasSuffix(title, ")") {
		return ""
	}
	open := strings.LastIndex(title, "(")
	if open < 0 || open+1 >= len(title)-1 {
		return ""
	}
	candidate := strings.TrimSpace(title[open+1 : len(title)-1])
	if successFactorsLooksLikeLocation(candidate) {
		return candidate
	}
	return ""
}

func successFactorsLooksLikeLocation(value string) bool {
	parts := compactStringList(strings.Split(value, ",")...)
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		country := canonicalCountry(part)
		if country == "US" || country == "Canada" || country == "UK" || country == "Singapore" || country == "Hong Kong" {
			return true
		}
	}
	return false
}

func successFactorsCountry(location string) string {
	parts := compactStringList(strings.Split(location, ",")...)
	for _, part := range parts {
		country := canonicalCountry(part)
		switch country {
		case "US", "Canada", "UK", "Singapore", "Hong Kong":
			return country
		}
	}
	return normalizeCountry("", location)
}

func stableAccountToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func numericString(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func joinURL(base string, parts ...string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return nil, err
	}
	all := append([]string{parsed.Path}, parts...)
	parsed.Path = path.Join(all...)
	return parsed, nil
}

func sourceBaseURL(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	parsed.Path = "/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func jobsoidJobsEndpoint(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	parsed.Path = "/api/v1/jobs"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func jobsoidHostedURL(rawURL string, id string, title string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return strings.TrimSpace(rawURL)
	}
	parsed.Path = path.Join("/j", stableAccountToken(id), stableAccountToken(title))
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func freshteamJobsEndpoint(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	parts := nonEmptyPathParts(parsed)
	if len(parts) == 0 || !strings.EqualFold(parts[0], "jobs") {
		parsed.Path = "/jobs"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func applicantProBoardURL(rawURL string) (*url.URL, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return nil, err
	}
	parts := nonEmptyPathParts(parsed)
	if len(parts) == 0 || !strings.EqualFold(parts[0], "jobs") {
		parsed.Path = "/jobs/"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func applicantProJobsEndpoint(boardURL *url.URL, domainID string) (string, error) {
	if strings.TrimSpace(domainID) == "" {
		return "", errors.New("applicantpro domain id is required")
	}
	endpoint := *boardURL
	endpoint.Path = path.Join("/core/jobs", strings.TrimSpace(domainID))
	params, err := json.Marshal(map[string]any{
		"isInternal":         0,
		"showLocation":       1,
		"showEmploymentType": 1,
		"chatToApplyButton":  "0",
	})
	if err != nil {
		return "", err
	}
	query := endpoint.Query()
	query.Set("getParams", string(params))
	endpoint.RawQuery = query.Encode()
	endpoint.Fragment = ""
	return endpoint.String(), nil
}

func applicantProDetailEndpoint(boardURL *url.URL, domainID string, jobID string) string {
	if strings.TrimSpace(domainID) == "" || strings.TrimSpace(jobID) == "" {
		return ""
	}
	endpoint := *boardURL
	endpoint.Path = path.Join("/core/jobs", strings.TrimSpace(domainID), strings.TrimSpace(jobID), "job-details")
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint.String()
}

func applicantProHostedURL(rawURL string, id string) string {
	parsed, err := applicantProBoardURL(rawURL)
	if err != nil {
		return strings.TrimSpace(rawURL)
	}
	parsed.Path = path.Join("/jobs", strings.TrimSpace(id))
	return parsed.String()
}

func talentLyftSubdomain(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.HasSuffix(host, ".talentlyft.com") {
		return strings.TrimSuffix(host, ".talentlyft.com"), nilIfEmpty(strings.TrimSuffix(host, ".talentlyft.com"), "talentlyft subdomain")
	}
	parts := nonEmptyPathParts(parsed)
	if len(parts) > 0 {
		return parts[0], nil
	}
	return "", errors.New("talentlyft subdomain is required")
}

func talentLyftJobsEndpoint(baseURL string, subdomain string, page int, perPage int, details bool) (string, error) {
	endpoint, err := joinURL(baseURL, "v2", "public", subdomain, "jobs")
	if err != nil {
		return "", err
	}
	q := endpoint.Query()
	q.Set("page", strconv.Itoa(page))
	q.Set("perPage", strconv.Itoa(perPage))
	if details {
		q.Set("details", "true")
	}
	endpoint.RawQuery = q.Encode()
	return endpoint.String(), nil
}

func talentLyftHostedURL(rawURL string, id string, title string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return strings.TrimSpace(rawURL)
	}
	parsed.Path = path.Join("/jobs", stableAccountToken(title)+"-"+stableAccountToken(id))
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func talentLyftHeaders(source Source) map[string]string {
	token := firstNonEmptyString(source.Metadata["talentlyft_token"], source.Metadata["api_token"], source.Metadata["bearer_token"])
	if token == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + strings.TrimPrefix(token, "Bearer ")}
}

func parseSourceURL(rawURL string) (*url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, errors.New("source url is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		parsed, err = url.Parse("https://" + trimmed)
	}
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid source url %q", rawURL)
	}
	return parsed, nil
}

func sourceHost(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func sourceURLQueryValue(rawURL string, key string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Query().Get(key))
}

func intFromMetadata(metadata map[string]string, key string) int {
	value := strings.TrimSpace(metadata[key])
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func firstPathSegment(parsed *url.URL) string {
	parts := nonEmptyPathParts(parsed)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func nonEmptyPathParts(parsed *url.URL) []string {
	rawParts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		if part == "" {
			continue
		}
		unescaped, err := url.PathUnescape(part)
		if err != nil {
			unescaped = part
		}
		parts = append(parts, unescaped)
	}
	return parts
}

func nilIfEmpty(value string, label string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", label)
	}
	return nil
}

func sourceCompany(source Source, fallback string) string {
	if source.Name != "" {
		return source.Name
	}
	return titleFromSlug(fallback)
}

func titleFromSlug(slug string) string {
	slug = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(slug, "-", " "), "_", " "))
	if slug == "" {
		return ""
	}
	parts := strings.Fields(slug)
	for i, part := range parts {
		if len(part) == 0 {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func greenhouseOfficeLocation(offices []greenhouseOffice) string {
	for _, office := range offices {
		if office.Location != "" {
			return office.Location
		}
		if office.Name != "" {
			return office.Name
		}
	}
	return ""
}

func greenhouseLocationText(primary string, offices []greenhouseOffice, description string) string {
	office := greenhouseOfficeLocation(offices)
	if !genericGreenhouseLocation(primary) {
		return strings.TrimSpace(primary)
	}
	if !genericGreenhouseLocation(office) {
		return strings.TrimSpace(office)
	}
	if match := greenhouseAvailableLocationPattern.FindStringSubmatch(description); len(match) == 2 {
		if location := strings.Trim(strings.TrimSpace(match[1]), " ;,."); location != "" {
			return location
		}
	}
	return firstNonEmptyString(primary, office)
}

func genericGreenhouseLocation(location string) bool {
	normalized := strings.Join(strings.Fields(strings.NewReplacer("-", " ", "_", " ").Replace(strings.ToLower(strings.TrimSpace(location)))), " ")
	switch normalized {
	case "", "in office", "hybrid", "remote", "unknown", "multiple locations", "various locations":
		return true
	default:
		return false
	}
}

func greenhouseDepartmentText(departments []greenhouseNamedItem) string {
	names := make([]string, 0, len(departments))
	for _, department := range departments {
		if strings.TrimSpace(department.Name) != "" {
			names = append(names, strings.TrimSpace(department.Name))
		}
	}
	return strings.Join(names, "; ")
}

func ashbyLocation(item ashbyJob) string {
	locations := compactStringList(item.Location)
	for _, secondary := range item.SecondaryLocations {
		locations = append(locations, compactStringList(secondary.Location)...)
	}
	if len(locations) == 0 && item.IsRemote {
		return "Remote"
	}
	return strings.Join(locations, "; ")
}

func workablePublished(state string) bool {
	state = strings.ToLower(strings.TrimSpace(state))
	return state == "" || state == "published"
}

func workableJobLocation(item workableJob) (string, string) {
	locations := make([]string, 0, 1+len(item.Locations))
	country := ""
	for _, loc := range append([]workableLocation{item.Location}, item.Locations...) {
		locationText := workableLocationText(loc)
		if locationText != "" {
			locations = append(locations, locationText)
		}
		if country == "" {
			country = workableCountry(loc)
		}
	}
	if len(locations) == 0 && (item.Location.Telecommuting || strings.EqualFold(item.Location.WorkplaceType, "remote")) {
		locations = append(locations, "Remote")
	}
	return strings.Join(compactStringList(locations...), "; "), country
}

func workableLocationText(loc workableLocation) string {
	if value := firstNonEmptyString(loc.LocationStr); value != "" {
		return value
	}
	return strings.Join(compactStringList(loc.Name, loc.City, firstNonEmptyString(loc.Region, loc.Subregion), firstNonEmptyString(loc.Country, loc.CountryName, loc.CountryNameAlt)), ", ")
}

func workableCountry(loc workableLocation) string {
	code := strings.ToUpper(strings.TrimSpace(loc.CountryCode))
	switch code {
	case "US":
		return "US"
	case "GB", "UK":
		return "UK"
	case "SG":
		return "Singapore"
	case "HK":
		return "Hong Kong"
	case "CA":
		return "Canada"
	}
	country := firstNonEmptyString(loc.Country, loc.CountryName, loc.CountryNameAlt)
	switch strings.ToLower(country) {
	case "united states", "united states of america", "usa":
		return "US"
	case "united kingdom", "great britain", "england":
		return "UK"
	case "singapore":
		return "Singapore"
	case "hong kong":
		return "Hong Kong"
	case "canada":
		return "Canada"
	default:
		return country
	}
}

func workableDescription(item workableJob) string {
	return cleanHTMLText(strings.Join(compactStringList(item.Description, item.FullDescription, item.Requirements, item.Benefits), " "))
}

func workableJobsQuery(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	query := parsed.Query()
	return firstNonEmptyString(query.Get("query"), query.Get("q"), query.Get("search"), firstPathSegment(parsed))
}

func workableJobsPosting(source Source, endpoint string, item workableJob) (JobPosting, bool) {
	title := firstNonEmptyString(item.FullTitle, item.Title)
	id := firstNonEmptyString(item.ID, item.Shortcode, stableJobToken(item.URL, title))
	company := firstNonEmptyString(item.Company.Title, source.Name, item.Company.ID)
	applyURL := firstNonEmptyString(item.ApplicationURL, item.URL, item.Shortlink)
	if title == "" || id == "" || company == "" || applyURL == "" {
		return JobPosting{}, false
	}
	location, country := workableJobLocation(item)
	description := workableDescription(item)
	postedAt := parseTimePtr(firstNonEmptyString(item.PublishedAt, item.PublishedOn, item.CreatedAt, item.Created, item.Updated))
	companyToken := firstNonEmptyString(item.Company.ID, strings.ToLower(strings.ReplaceAll(normalizeSpace(company), " ", "-")))
	return JobPosting{
		SourceJobID:    "workable_jobs:" + companyToken + ":" + id,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, firstNonEmptyString(item.EmploymentType, item.EmploymentTypeAlt, item.WorkType)),
		SourceURL:      firstNonEmptyString(item.URL, item.Shortlink, source.URL),
		ApplyURL:       applyURL,
		PostedAt:       postedAt,
		Live:           true,
		Confidence:     0.86,
		Strategy:       TierATS,
		Evidence: []Evidence{
			{Field: "ats", Text: "Workable Jobs public search API", URL: endpoint},
			{Field: "description", Text: description, URL: firstNonEmptyString(item.URL, applyURL)},
			{Field: "department", Text: item.Department, URL: firstNonEmptyString(item.URL, applyURL)},
			{Field: "location", Text: location, URL: firstNonEmptyString(item.URL, applyURL)},
			{Field: "company", Text: firstNonEmptyString(item.Company.Title, item.Company.Website), URL: firstNonEmptyString(item.Company.URL, item.Company.Website)},
		},
	}, true
}

func recruiteeLiveJob(offer recruiteeOffer) bool {
	if strings.EqualFold(strings.TrimSpace(offer.Kind), "talent_pool") {
		return false
	}
	state := strings.ToLower(firstNonEmptyString(offer.Status, offer.State))
	return state == "" || state == "published" || state == "open"
}

func recruiteeOfferLocation(offer recruiteeOffer) (string, string) {
	locations := compactStringList(offer.Location)
	country := ""
	for _, loc := range offer.Locations {
		locationText := recruiteeLocationText(loc)
		if locationText != "" {
			locations = append(locations, locationText)
		}
		if country == "" {
			country = canonicalCountry(firstNonEmptyString(loc.CountryCode, loc.Country))
		}
	}
	return strings.Join(compactStringList(locations...), "; "), country
}

func recruiteeLocationText(loc recruiteeLocation) string {
	country := canonicalCountry(firstNonEmptyString(loc.CountryCode, loc.Country))
	if loc.Name != "" && (loc.City == "" || strings.EqualFold(loc.Name, loc.City)) {
		return strings.Join(compactStringList(loc.Name, firstNonEmptyString(loc.State, loc.Region), country), ", ")
	}
	return strings.Join(compactStringList(loc.Name, loc.City, firstNonEmptyString(loc.State, loc.Region), country), ", ")
}

func recruiteeOfferURL(companySlug string, offerSlug string) string {
	if strings.TrimSpace(offerSlug) == "" {
		return ""
	}
	return "https://" + companySlug + ".recruitee.com/o/" + strings.TrimSpace(offerSlug)
}

func recruiteeOfferID(id int64) string {
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

func cleanHTMLText(value string) string {
	value = html.UnescapeString(value)
	value = htmlTagPattern.ReplaceAllString(value, " ")
	return normalizeSpace(value)
}

func employmentFromText(title string, fallback string) string {
	lower := strings.ToLower(title + " " + fallback)
	switch {
	case internLevelPattern.MatchString(lower):
		return "internship"
	case strings.Contains(lower, "contract"):
		return "contract"
	case fallback != "":
		return fallback
	default:
		return "full_time"
	}
}

func parseTimePtr(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, time.RFC1123Z, time.RFC1123, "2006-01-02T15:04Z07:00", "2006-01-02T15:04:05.000Z0700", "2006-01-02T15:04:05.000", "2006-01-02T15:04:05", "2006-01-02", "Jan 2, 2006", "2-Jan-2006", "02-Jan-2006"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			utc := parsed.UTC()
			return &utc
		}
	}
	return nil
}

func millisTimePtr(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	if value > 1_000_000_000_000 {
		parsed := time.UnixMilli(value).UTC()
		return &parsed
	}
	parsed := time.Unix(value, 0).UTC()
	return &parsed
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonZeroInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stableJobToken(rawURL string, fallback string) string {
	parsed, err := parseSourceURL(rawURL)
	if err == nil {
		parts := nonEmptyPathParts(parsed)
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	return strings.ToLower(strings.ReplaceAll(normalizeSpace(fallback), " ", "-"))
}

func compactStringList(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = normalizeSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func boundedInt(value, fallback, minValue, maxValue int) int {
	if value == 0 {
		value = fallback
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
