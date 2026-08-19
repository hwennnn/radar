package ats

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hwennnn/radar/internal/provider"
)

var (
	htmlTagPattern                     = regexp.MustCompile(`<[^>]+>`)
	anchorTagPattern                   = regexp.MustCompile(`(?is)<a\b[^>]*>`)
	anchorHrefPattern                  = regexp.MustCompile(`(?is)\bhref\s*=\s*["']([^"']+)["']`)
	jsonLDScriptPattern                = regexp.MustCompile(`(?is)<script[^>]+type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	jobviteTitlePattern                = regexp.MustCompile(`(?is)<h[12][^>]*class=["'][^"']*jv-header[^"']*["'][^>]*>(.*?)</h[12]>`)
	jobviteCompanyPattern              = regexp.MustCompile(`(?is)function\s+getCompanyName\(\)\s*\{\s*return\s*["']([^"']+)["']`)
	teamtailorApplyPattern             = regexp.MustCompile(`(?is)data-careersite--jobs--form-overlay-job-application-url-value=["']([^"']+)["']`)
	aiTokenPattern                     = regexp.MustCompile(`\b(ai|aiml|llm|ml)\b`)
	internLevelPattern                 = regexp.MustCompile(`\bintern(?:ship)?s?\b|\bco-?op\b`)
	newGradLevelPattern                = regexp.MustCompile(`\bnew(?:\s+|-)grad(?:uate)?s?\b|\bgraduates?\b`)
	earlyCareerPattern                 = regexp.MustCompile(`\bentry(?:\s+|-)level\b|\bearly(?:\s+|-)career\b`)
	greenhouseAvailableLocationPattern = regexp.MustCompile(`(?i)\bavailable locations?\s*:\s*(.{1,120}?)(?:\s+(?:about(?:\s+the)?\s+(?:company|department|role|team)|department|the role|what you|who you|responsibilities)\b|$)`)
)

type Options struct {
	Client                       *http.Client
	GreenhouseBaseURL            string
	LeverGlobalBaseURL           string
	LeverEuropeBaseURL           string
	AshbyBaseURL                 string
	SmartRecruitersBaseURL       string
	SmartRecruitersMaxJobs       int
	SmartRecruitersDetailMaxJobs int
	WorkablePublicBaseURL        string
	WorkableJobsBaseURL          string
	WorkableJobsMaxPages         int
	WorkableJobsMaxJobs          int
	RecruiteeBaseURL             string
	ComeetBaseURL                string
	BambooHRMaxJobs              int
	BreezyMaxJobs                int
	ICIMSMaxJobs                 int
	PersonioMaxJobs              int
	PinpointMaxJobs              int
	JobviteMaxJobs               int
	TeamtailorMaxJobs            int
	OracleRecruitingPageSize     int
	OracleRecruitingMaxPages     int
	OracleRecruitingMaxJobs      int
}

type Engine struct {
	kind                         string
	client                       *http.Client
	greenhouseBaseURL            string
	leverGlobalBaseURL           string
	leverEuropeBaseURL           string
	ashbyBaseURL                 string
	smartRecruitersBaseURL       string
	smartRecruitersMaxJobs       int
	smartRecruitersDetailMaxJobs int
	workablePublicBaseURL        string
	workableJobsBaseURL          string
	workableJobsMaxPages         int
	workableJobsMaxJobs          int
	recruiteeBaseURL             string
	comeetBaseURL                string
	bambooHRMaxJobs              int
	breezyMaxJobs                int
	icimsMaxJobs                 int
	personioMaxJobs              int
	pinpointMaxJobs              int
	jobviteMaxJobs               int
	teamtailorMaxJobs            int
	oracleRecruitingPageSize     int
	oracleRecruitingMaxPages     int
	oracleRecruitingMaxJobs      int
}

func New(kind string, opts Options) *Engine {
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Engine{
		kind:                         strings.ToLower(strings.TrimSpace(kind)),
		client:                       client,
		greenhouseBaseURL:            firstNonEmptyString(opts.GreenhouseBaseURL, "https://boards-api.greenhouse.io/v1/boards"),
		leverGlobalBaseURL:           firstNonEmptyString(opts.LeverGlobalBaseURL, "https://api.lever.co/v0/postings"),
		leverEuropeBaseURL:           firstNonEmptyString(opts.LeverEuropeBaseURL, "https://api.eu.lever.co/v0/postings"),
		ashbyBaseURL:                 firstNonEmptyString(opts.AshbyBaseURL, "https://api.ashbyhq.com/posting-api/job-board"),
		smartRecruitersBaseURL:       firstNonEmptyString(opts.SmartRecruitersBaseURL, "https://api.smartrecruiters.com/v1/companies"),
		smartRecruitersMaxJobs:       boundedInt(opts.SmartRecruitersMaxJobs, 200, 1, 500),
		smartRecruitersDetailMaxJobs: boundedInt(opts.SmartRecruitersDetailMaxJobs, 40, 0, 200),
		workablePublicBaseURL:        firstNonEmptyString(opts.WorkablePublicBaseURL, "https://www.workable.com/api/accounts"),
		workableJobsBaseURL:          firstNonEmptyString(opts.WorkableJobsBaseURL, "https://jobs.workable.com/api/v1/jobs"),
		workableJobsMaxPages:         boundedInt(opts.WorkableJobsMaxPages, 2, 1, 10),
		workableJobsMaxJobs:          boundedInt(opts.WorkableJobsMaxJobs, 50, 1, 200),
		recruiteeBaseURL:             firstNonEmptyString(opts.RecruiteeBaseURL, "https://%s.recruitee.com/api/offers/"),
		comeetBaseURL:                firstNonEmptyString(opts.ComeetBaseURL, "https://www.comeet.co/careers-api/2.0"),
		bambooHRMaxJobs:              boundedInt(opts.BambooHRMaxJobs, 50, 1, 200),
		breezyMaxJobs:                boundedInt(opts.BreezyMaxJobs, 50, 1, 200),
		icimsMaxJobs:                 boundedInt(opts.ICIMSMaxJobs, 50, 1, 200),
		personioMaxJobs:              boundedInt(opts.PersonioMaxJobs, 50, 1, 200),
		pinpointMaxJobs:              boundedInt(opts.PinpointMaxJobs, 50, 1, 200),
		jobviteMaxJobs:               boundedInt(opts.JobviteMaxJobs, 50, 1, 200),
		teamtailorMaxJobs:            boundedInt(opts.TeamtailorMaxJobs, 50, 1, 200),
		oracleRecruitingPageSize:     boundedInt(opts.OracleRecruitingPageSize, 25, 1, 100),
		oracleRecruitingMaxPages:     boundedInt(opts.OracleRecruitingMaxPages, 2, 1, 20),
		oracleRecruitingMaxJobs:      boundedInt(opts.OracleRecruitingMaxJobs, 50, 1, 200),
	}
}

func (e *Engine) Name() string {
	return e.kind + "-provider"
}

func (e *Engine) Extract(ctx context.Context, source provider.Source) (provider.Result, error) {
	switch e.kind {
	case "greenhouse":
		return e.extractGreenhouse(ctx, source)
	case "lever":
		return e.extractLever(ctx, source)
	case "ashby":
		return e.extractAshby(ctx, source)
	case "smartrecruiters":
		return e.extractSmartRecruiters(ctx, source)
	case "workable":
		return e.extractWorkable(ctx, source)
	case "workable_jobs":
		return e.extractWorkableJobs(ctx, source)
	case "recruitee":
		return e.extractRecruitee(ctx, source)
	case "comeet":
		return e.extractComeet(ctx, source)
	case "bamboohr":
		return e.extractBambooHR(ctx, source)
	case "breezy":
		return e.extractBreezy(ctx, source)
	case "icims":
		return e.extractICIMS(ctx, source)
	case "personio":
		return e.extractPersonio(ctx, source)
	case "pinpoint":
		return e.extractPinpoint(ctx, source)
	case "jobvite":
		return e.extractJobvite(ctx, source)
	case "teamtailor":
		return e.extractTeamtailor(ctx, source)
	case "oracle", "oracle_recruiting", "oracle_cloud_recruiting":
		return e.extractOracleRecruiting(ctx, source)
	default:
		return provider.Result{}, fmt.Errorf("unsupported provider ats engine %q", e.kind)
	}
}

func (e *Engine) extractGreenhouse(ctx context.Context, source provider.Source) (provider.Result, error) {
	board, err := greenhouseBoardToken(source.URL)
	if err != nil {
		return provider.Result{}, err
	}
	baseURL := firstNonEmptyString(source.Metadata["greenhouse_base_url"], source.Metadata["base_url"], e.greenhouseBaseURL)
	endpoint, err := joinURL(baseURL, board, "jobs")
	if err != nil {
		return provider.Result{}, err
	}
	q := endpoint.Query()
	q.Set("content", "true")
	endpoint.RawQuery = q.Encode()

	var payload greenhouseResponse
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return provider.Result{}, err
	}
	jobs := make([]provider.Posting, 0, len(payload.Jobs))
	for _, item := range payload.Jobs {
		applyURL := strings.TrimSpace(item.AbsoluteURL)
		description := cleanHTMLText(item.Content)
		location := greenhouseLocationText(item.Location.Name, item.Offices, description)
		jobs = append(jobs, normalizePosting(provider.Posting{
			SourceJobID:    "greenhouse:" + strconv.FormatInt(item.ID, 10),
			Company:        sourceCompany(source, board),
			Title:          item.Title,
			Location:       location,
			EmploymentType: employmentFromText(item.Title, ""),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       parseTimePtr(item.UpdatedAt),
			Live:           true,
			Confidence:     0.94,
			Strategy:       provider.TierATS,
			Evidence: []provider.Evidence{
				{Field: "ats", Text: "Greenhouse structured job board API", URL: endpoint.String()},
				{Field: "description", Text: description, URL: applyURL},
				{Field: "department", Text: greenhouseDepartmentText(item.Departments), URL: applyURL},
			},
		}))
	}
	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.94,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "Greenhouse job board API", URL: endpoint.String()},
		},
		Diagnostics: map[string]string{"provider_engine": e.Name(), "greenhouse_board": board},
	}, nil
}

func (e *Engine) extractLever(ctx context.Context, source provider.Source) (provider.Result, error) {
	site, european, err := leverSite(source.URL)
	if err != nil {
		return provider.Result{}, err
	}
	baseURL := firstNonEmptyString(source.Metadata["lever_base_url"], source.Metadata["base_url"])
	if baseURL == "" {
		baseURL = e.leverGlobalBaseURL
		if european {
			baseURL = e.leverEuropeBaseURL
		}
	}
	endpoint, err := joinURL(baseURL, site)
	if err != nil {
		return provider.Result{}, err
	}
	q := endpoint.Query()
	q.Set("mode", "json")
	endpoint.RawQuery = q.Encode()

	var payload []leverPosting
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return provider.Result{}, err
	}
	jobs := make([]provider.Posting, 0, len(payload))
	company := sourceCompany(source, site)
	for _, item := range payload {
		location := firstNonEmptyString(item.Categories.Location, strings.Join(item.Categories.AllLocations, "; "))
		description := firstNonEmptyString(item.DescriptionPlain, item.Description)
		applyURL := firstNonEmptyString(item.ApplyURL, item.HostedURL)
		jobs = append(jobs, normalizePosting(provider.Posting{
			SourceJobID:    "lever:" + firstNonEmptyString(item.ID, stableJobToken(item.HostedURL, item.Text)),
			Company:        company,
			Title:          item.Text,
			Location:       location,
			Country:        item.Country,
			EmploymentType: item.Categories.Commitment,
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       millisTimePtr(firstNonZeroInt64(item.CreatedAt, item.UpdatedAt)),
			Live:           true,
			Confidence:     0.93,
			Strategy:       provider.TierATS,
			Evidence: []provider.Evidence{
				{Field: "ats", Text: "Lever postings API", URL: endpoint.String()},
				{Field: "description", Text: description, URL: item.HostedURL},
				{Field: "commitment", Text: item.Categories.Commitment, URL: item.HostedURL},
				{Field: "team", Text: firstNonEmptyString(item.Categories.Team, item.Categories.Department), URL: item.HostedURL},
			},
		}))
	}
	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.93,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "Lever postings API", URL: endpoint.String()},
		},
		Diagnostics: map[string]string{"provider_engine": e.Name(), "lever_site": site},
	}, nil
}

func (e *Engine) extractAshby(ctx context.Context, source provider.Source) (provider.Result, error) {
	board, err := ashbyBoardName(source.URL)
	if err != nil {
		return provider.Result{}, err
	}
	baseURL := firstNonEmptyString(source.Metadata["ashby_base_url"], source.Metadata["base_url"], e.ashbyBaseURL)
	endpoint, err := joinURL(baseURL, board)
	if err != nil {
		return provider.Result{}, err
	}
	q := endpoint.Query()
	q.Set("includeCompensation", "true")
	endpoint.RawQuery = q.Encode()

	var payload ashbyResponse
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return provider.Result{}, err
	}
	jobs := make([]provider.Posting, 0, len(payload.Jobs))
	company := sourceCompany(source, board)
	for _, item := range payload.Jobs {
		if item.IsListed != nil && !*item.IsListed {
			continue
		}
		location := ashbyLocation(item)
		applyURL := firstNonEmptyString(item.ApplyURL, item.JobURL)
		jobs = append(jobs, normalizePosting(provider.Posting{
			SourceJobID:    "ashby:" + board + ":" + stableJobToken(item.JobURL, item.Title),
			Company:        company,
			Title:          item.Title,
			Location:       location,
			EmploymentType: item.EmploymentType,
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       parseTimePtr(item.PublishedAt),
			Live:           true,
			Confidence:     0.93,
			Strategy:       provider.TierATS,
			Evidence: []provider.Evidence{
				{Field: "ats", Text: "Ashby posting API", URL: endpoint.String()},
				{Field: "description", Text: item.DescriptionPlain, URL: item.JobURL},
				{Field: "workplace", Text: strings.TrimSpace(item.WorkplaceType), URL: item.JobURL},
				{Field: "team", Text: firstNonEmptyString(item.Team, item.Department), URL: item.JobURL},
			},
		}))
	}
	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.93,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "Ashby posting API", URL: endpoint.String()},
		},
		Diagnostics: paginationDiagnostics(map[string]string{"provider_engine": e.Name(), "ashby_board": board}, 1, len(payload.Jobs), len(jobs), len(payload.Jobs), false),
	}, nil
}

func (e *Engine) extractSmartRecruiters(ctx context.Context, source provider.Source) (provider.Result, error) {
	companyID, err := smartRecruitersCompanyIdentifier(source.URL)
	if err != nil {
		return provider.Result{}, err
	}
	baseURL := firstNonEmptyString(source.Metadata["smartrecruiters_base_url"], source.Metadata["base_url"], e.smartRecruitersBaseURL)
	postings, endpoint, err := e.smartRecruitersPostings(ctx, baseURL, companyID)
	if err != nil {
		return provider.Result{}, err
	}

	jobs := make([]provider.Posting, 0, min(len(postings), e.smartRecruitersMaxJobs))
	for i, posting := range postings {
		if len(jobs) >= e.smartRecruitersMaxJobs {
			break
		}
		if posting.ID != "" && i < e.smartRecruitersDetailMaxJobs {
			detail, err := e.smartRecruitersPostingDetail(ctx, baseURL, companyID, posting.ID)
			if err == nil {
				posting = mergeSmartRecruitersPosting(posting, detail)
			}
		}
		location, country := smartRecruitersLocationText(posting.Location)
		applyURL := smartRecruitersApplyURL(companyID, posting)
		description := smartRecruitersDescription(posting.JobAd)
		jobToken := firstNonEmptyString(posting.ID, posting.UUID, stableJobToken(applyURL, posting.Name))
		company := sourceCompany(source, firstNonEmptyString(posting.Company.Name, posting.Company.Identifier, companyID))
		jobs = append(jobs, normalizePosting(provider.Posting{
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
			Strategy:       provider.TierATS,
			Evidence: []provider.Evidence{
				{Field: "ats", Text: "SmartRecruiters Posting API", URL: endpoint.String()},
				{Field: "description", Text: description, URL: applyURL},
				{Field: "department", Text: posting.Department.Label, URL: applyURL},
				{Field: "function", Text: posting.Function.Label, URL: applyURL},
			},
		}))
	}
	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.91,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "SmartRecruiters Posting API", URL: endpoint.String()},
		},
		Diagnostics: map[string]string{"provider_engine": e.Name(), "smartrecruiters_company": companyID},
	}, nil
}

func (e *Engine) smartRecruitersPostings(ctx context.Context, baseURL string, companyID string) ([]smartRecruitersPosting, *url.URL, error) {
	endpoint, err := joinURL(firstNonEmptyString(baseURL, "https://api.smartrecruiters.com/v1/companies"), companyID, "postings")
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

func (e *Engine) smartRecruitersPostingDetail(ctx context.Context, baseURL string, companyID string, postingID string) (smartRecruitersPosting, error) {
	endpoint, err := joinURL(firstNonEmptyString(baseURL, "https://api.smartrecruiters.com/v1/companies"), companyID, "postings", postingID)
	if err != nil {
		return smartRecruitersPosting{}, err
	}
	var detail smartRecruitersPosting
	if err := e.getJSON(ctx, endpoint.String(), &detail); err != nil {
		return smartRecruitersPosting{}, err
	}
	return detail, nil
}

func (e *Engine) extractWorkable(ctx context.Context, source provider.Source) (provider.Result, error) {
	account, err := workableAccountSlug(source.URL)
	if err != nil {
		return provider.Result{}, err
	}
	baseURL := firstNonEmptyString(source.Metadata["workable_public_base_url"], source.Metadata["base_url"], e.workablePublicBaseURL)
	endpoint, err := joinURL(baseURL, account)
	if err != nil {
		return provider.Result{}, err
	}
	q := endpoint.Query()
	q.Set("details", "true")
	endpoint.RawQuery = q.Encode()

	var payload workableResponse
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return provider.Result{}, err
	}
	jobs := make([]provider.Posting, 0, len(payload.Jobs))
	company := sourceCompany(source, firstNonEmptyString(payload.Name, account))
	for _, item := range payload.Jobs {
		if !workablePublished(item.State) {
			continue
		}
		location, country := workableJobLocation(item)
		description := workableDescription(item)
		applyURL := firstNonEmptyString(item.ApplicationURL, item.URL, item.Shortlink)
		jobURL := firstNonEmptyString(item.URL, item.Shortlink, applyURL)
		jobToken := firstNonEmptyString(item.Shortcode, item.ID, stableJobToken(jobURL, item.Title))
		jobs = append(jobs, normalizePosting(provider.Posting{
			SourceJobID:    "workable:" + account + ":" + jobToken,
			Company:        company,
			Title:          firstNonEmptyString(item.FullTitle, item.Title),
			Location:       location,
			Country:        country,
			EmploymentType: employmentFromText(item.Title, firstNonEmptyString(item.EmploymentType, item.EmploymentTypeAlt, item.WorkType, item.WorkTypeSnake)),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       parseTimePtr(firstNonEmptyString(item.PublishedAt, item.PublishedOn, item.CreatedAt, item.Created, item.Updated)),
			Live:           true,
			Confidence:     0.92,
			Strategy:       provider.TierATS,
			Evidence: []provider.Evidence{
				{Field: "ats", Text: "Workable public account API", URL: endpoint.String()},
				{Field: "description", Text: description, URL: jobURL},
				{Field: "department", Text: item.Department, URL: jobURL},
				{Field: "location", Text: location, URL: jobURL},
			},
		}))
	}
	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.92,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "Workable public account API", URL: endpoint.String()},
		},
		Diagnostics: map[string]string{"provider_engine": e.Name(), "workable_account": account},
	}, nil
}

func (e *Engine) extractWorkableJobs(ctx context.Context, source provider.Source) (provider.Result, error) {
	query := workableJobsQuery(source.URL)
	baseURL := firstNonEmptyString(source.Metadata["workable_jobs_base_url"], source.Metadata["base_url"], e.workableJobsBaseURL)
	endpoint, err := parseSourceURL(baseURL)
	if err != nil {
		return provider.Result{}, err
	}
	jobs := make([]provider.Posting, 0, e.workableJobsMaxJobs)
	rawEvidence := []provider.Evidence{}
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
			return provider.Result{}, err
		}
		rawEvidence = append(rawEvidence, provider.Evidence{Field: "ats_endpoint", Text: "Workable Jobs public search API", URL: pageURL.String()})
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
		return provider.Result{}, errors.New("no workable jobs found")
	}
	return provider.Result{
		Source:      source,
		Jobs:        jobs,
		Confidence:  0.86,
		Strategy:    provider.TierATS,
		Live:        true,
		FetchedAt:   time.Now().UTC(),
		RawEvidence: rawEvidence,
		Diagnostics: map[string]string{"provider_engine": e.Name(), "workable_jobs_query": query},
	}, nil
}

func (e *Engine) extractRecruitee(ctx context.Context, source provider.Source) (provider.Result, error) {
	companySlug, err := recruiteeCompanySlug(source.URL)
	if err != nil {
		return provider.Result{}, err
	}
	baseURL := firstNonEmptyString(source.Metadata["recruitee_base_url"], source.Metadata["base_url"], e.recruiteeBaseURL)
	endpoint, err := recruiteeOffersEndpoint(baseURL, companySlug)
	if err != nil {
		return provider.Result{}, err
	}

	var payload recruiteeResponse
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return provider.Result{}, err
	}

	jobs := make([]provider.Posting, 0, len(payload.Offers))
	company := sourceCompany(source, companySlug)
	for _, offer := range payload.Offers {
		if !recruiteeLiveJob(offer) {
			continue
		}
		applyURL := firstNonEmptyString(offer.CareersURL, offer.URL, recruiteeOfferURL(companySlug, offer.Slug))
		location, country := recruiteeOfferLocation(offer)
		description := cleanHTMLText(strings.Join(compactStringList(offer.Description, offer.Requirements), " "))
		jobToken := firstNonEmptyString(offer.Slug, recruiteeOfferID(offer.ID), stableJobToken(applyURL, offer.Title))
		if jobToken == "" {
			continue
		}
		jobs = append(jobs, normalizePosting(provider.Posting{
			SourceJobID:    "recruitee:" + companySlug + ":" + jobToken,
			Company:        company,
			Title:          offer.Title,
			Location:       location,
			Country:        country,
			EmploymentType: employmentFromText(offer.Title, offer.EmploymentType),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       parseTimePtr(firstNonEmptyString(offer.PublishedAt, offer.CreatedAt, offer.UpdatedAt)),
			Live:           true,
			Confidence:     0.91,
			Strategy:       provider.TierATS,
			Evidence: []provider.Evidence{
				{Field: "ats", Text: "Recruitee Careers Site API", URL: endpoint.String()},
				{Field: "description", Text: description, URL: applyURL},
				{Field: "department", Text: offer.Department, URL: applyURL},
				{Field: "location", Text: location, URL: applyURL},
			},
		}))
	}
	if len(jobs) == 0 {
		return provider.Result{}, errors.New("no recruitee jobs found")
	}
	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.91,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "Recruitee Careers Site API", URL: endpoint.String()},
		},
		Diagnostics: map[string]string{"provider_engine": e.Name(), "recruitee_company": companySlug},
	}, nil
}

func (e *Engine) extractComeet(ctx context.Context, source provider.Source) (provider.Result, error) {
	config, err := comeetConfigFromSource(source)
	if err != nil {
		return provider.Result{}, err
	}
	baseURL := firstNonEmptyString(source.Metadata["comeet_base_url"], source.Metadata["base_url"], e.comeetBaseURL)
	endpoint, err := joinURL(baseURL, "company", config.CompanyUID, "positions")
	if err != nil {
		return provider.Result{}, err
	}
	q := endpoint.Query()
	q.Set("token", config.Token)
	q.Set("details", "true")
	endpoint.RawQuery = q.Encode()

	var payload []comeetPosition
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return provider.Result{}, err
	}

	jobs := make([]provider.Posting, 0, len(payload))
	for _, position := range payload {
		if position.IsInternal {
			continue
		}
		location, country := comeetLocationText(position.Location, position.Locations)
		description := comeetDescription(position.Details)
		applyURL := firstNonEmptyString(position.URLActivePage, position.URLComeetHostedPage, position.URLRecruitHostedPage, position.PositionURL)
		jobToken := firstNonEmptyString(position.UID, stableJobToken(applyURL, position.Name))
		if jobToken == "" {
			continue
		}
		company := sourceCompany(source, firstNonEmptyString(position.CompanyName, config.CompanyUID))
		jobs = append(jobs, normalizePosting(provider.Posting{
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
			Strategy:       provider.TierATS,
			Evidence: []provider.Evidence{
				{Field: "ats", Text: "Comeet Careers API", URL: endpoint.String()},
				{Field: "description", Text: description, URL: applyURL},
				{Field: "department", Text: position.Department, URL: applyURL},
				{Field: "location", Text: location, URL: applyURL},
			},
		}))
	}
	if len(jobs) == 0 {
		return provider.Result{}, errors.New("no comeet jobs found")
	}
	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.91,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "Comeet Careers API", URL: endpoint.String()},
		},
		Diagnostics: map[string]string{"provider_engine": e.Name(), "comeet_company_uid": config.CompanyUID},
	}, nil
}

func (e *Engine) extractBambooHR(ctx context.Context, source provider.Source) (provider.Result, error) {
	listURL, err := bambooHRListURL(source.URL)
	if err != nil {
		return provider.Result{}, err
	}
	account := bambooHRAccountToken(source.URL)
	if account == "" {
		return provider.Result{}, fmt.Errorf("bamboohr account missing in %q", source.URL)
	}

	directID := bambooHRJobIDFromURL(source.URL)
	var payload bambooHRListResponse
	listErr := e.getJSON(ctx, listURL.String(), &payload)
	summaries := make([]bambooHRListJob, 0)
	if listErr != nil {
		if ctx.Err() != nil || directID == "" {
			return provider.Result{}, listErr
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

	jobs := make([]provider.Posting, 0, len(summaries))
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
				return provider.Result{}, err
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
	if len(jobs) == 0 {
		return provider.Result{}, errors.New("no bamboohr jobs found")
	}
	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "BambooHR public careers list and detail endpoints", URL: listURL.String()},
		},
		Diagnostics: map[string]string{"provider_engine": e.Name(), "bamboohr_account": account},
	}, nil
}

func (e *Engine) extractICIMS(ctx context.Context, source provider.Source) (provider.Result, error) {
	sitemapURL, err := icimsSitemapURL(source.URL)
	if err != nil {
		return provider.Result{}, err
	}

	var sitemap icimsSitemap
	if err := e.getXML(ctx, sitemapURL.String(), &sitemap); err != nil {
		return provider.Result{}, err
	}

	entries := icimsSitemapJobs(sitemap)
	totalEntries := len(entries)
	truncated := totalEntries > e.icimsMaxJobs
	if len(entries) > e.icimsMaxJobs {
		entries = entries[:e.icimsMaxJobs]
	}
	jobs := make([]provider.Posting, 0, len(entries))
	for _, entry := range entries {
		detailURL, err := icimsDetailURL(entry.Loc)
		if err != nil {
			continue
		}
		document, err := e.getText(ctx, detailURL.String(), "text/html,application/xhtml+xml")
		if err != nil {
			if ctx.Err() != nil {
				return provider.Result{}, err
			}
			continue
		}
		detailBaseURL, err := parseSourceURL(detailURL.String())
		if err != nil {
			continue
		}
		for _, job := range icimsJSONLDJobs(source, entry, detailBaseURL, document) {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return provider.Result{}, errors.New("no icims jobs found")
	}

	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "iCIMS sitemap and JobPosting detail pages", URL: sitemapURL.String()},
		},
		Diagnostics: paginationDiagnostics(map[string]string{"provider_engine": e.Name(), "icims_host": sourceHost(source.URL)}, 1, e.icimsMaxJobs, totalEntries, e.icimsMaxJobs, truncated),
	}, nil
}

func (e *Engine) extractBreezy(ctx context.Context, source provider.Source) (provider.Result, error) {
	endpoint, err := breezyBoardURL(source.URL)
	if err != nil {
		return provider.Result{}, err
	}

	var payload []breezyPosition
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return provider.Result{}, err
	}
	if len(payload) > e.breezyMaxJobs {
		payload = payload[:e.breezyMaxJobs]
	}

	jobs := make([]provider.Posting, 0, len(payload))
	for _, position := range payload {
		description := cleanHTMLText(position.Description)
		location, country := breezyLocationText(position.Location, position.Locations)
		companySlug := firstNonEmptyString(position.Company.FriendlyID, breezyCompanySlug(source.URL))
		jobToken := firstNonEmptyString(position.ID, position.FriendlyID, stableJobToken(position.URL, position.Name))
		if jobToken == "" || strings.TrimSpace(position.Name) == "" {
			continue
		}
		applyURL := firstNonEmptyString(position.URL, breezyHostedURL(source.URL, position.FriendlyID))
		jobs = append(jobs, normalizePosting(provider.Posting{
			SourceJobID:    "breezy:" + companySlug + ":" + jobToken,
			Company:        sourceCompany(source, firstNonEmptyString(position.Company.Name, companySlug)),
			Title:          position.Name,
			Location:       location,
			Country:        country,
			EmploymentType: employmentFromText(position.Name, position.Type.Name),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       parseTimePtr(position.PublishedDate),
			Live:           true,
			Confidence:     0.89,
			Strategy:       provider.TierATS,
			Evidence: []provider.Evidence{
				{Field: "ats", Text: "Breezy public JSON board", URL: endpoint.String()},
				{Field: "description", Text: description, URL: applyURL},
				{Field: "department", Text: position.Department, URL: applyURL},
				{Field: "location", Text: location, URL: applyURL},
			},
		}))
	}
	if len(jobs) == 0 {
		return provider.Result{}, errors.New("no breezy jobs found")
	}

	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.89,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "Breezy public JSON board", URL: endpoint.String()},
		},
		Diagnostics: map[string]string{"provider_engine": e.Name(), "breezy_company": breezyCompanySlug(source.URL)},
	}, nil
}

func (e *Engine) extractPersonio(ctx context.Context, source provider.Source) (provider.Result, error) {
	endpoint, err := personioFeedURL(source.URL)
	if err != nil {
		return provider.Result{}, err
	}

	var payload personioFeed
	if err := e.getXML(ctx, endpoint.String(), &payload); err != nil {
		return provider.Result{}, err
	}

	positions := payload.Positions
	if len(positions) > e.personioMaxJobs {
		positions = positions[:e.personioMaxJobs]
	}
	jobs := make([]provider.Posting, 0, len(positions))
	for _, position := range positions {
		if strings.TrimSpace(position.ID) == "" || strings.TrimSpace(position.Name) == "" {
			continue
		}
		description := personioDescription(position.Descriptions)
		location := personioLocation(position)
		applyURL := personioApplyURL(endpoint, position.ID)
		jobs = append(jobs, normalizePosting(provider.Posting{
			SourceJobID:    "personio:" + strings.TrimSpace(position.ID),
			Company:        sourceCompany(source, position.Subcompany),
			Title:          position.Name,
			Location:       location,
			EmploymentType: personioEmployment(position),
			SourceURL:      source.URL,
			ApplyURL:       applyURL,
			PostedAt:       parseTimePtr(position.CreatedAt),
			Live:           true,
			Confidence:     0.9,
			Strategy:       provider.TierATS,
			Evidence: []provider.Evidence{
				{Field: "ats", Text: "Personio XML job feed", URL: endpoint.String()},
				{Field: "description", Text: description, URL: applyURL},
				{Field: "department", Text: firstNonEmptyString(position.Department, position.RecruitingCategory), URL: applyURL},
				{Field: "location", Text: location, URL: applyURL},
			},
		}))
	}
	if len(jobs) == 0 {
		return provider.Result{}, errors.New("no personio jobs found")
	}

	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.9,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "Personio XML job feed", URL: endpoint.String()},
		},
		Diagnostics: map[string]string{"provider_engine": e.Name(), "personio_host": sourceHost(source.URL)},
	}, nil
}

func (e *Engine) extractPinpoint(ctx context.Context, source provider.Source) (provider.Result, error) {
	endpoint, err := pinpointPostingsURL(source.URL)
	if err != nil {
		return provider.Result{}, err
	}

	var payload pinpointResponse
	if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
		return provider.Result{}, err
	}

	postings := payload.Data
	if len(postings) > e.pinpointMaxJobs {
		postings = postings[:e.pinpointMaxJobs]
	}
	jobs := make([]provider.Posting, 0, len(postings))
	companySlug := pinpointCompanySlug(source.URL)
	for _, posting := range postings {
		description := pinpointDescription(posting)
		location := pinpointLocationText(posting.Location)
		jobToken := firstNonEmptyString(posting.ID, stableJobToken(posting.URL, posting.Title))
		if jobToken == "" || strings.TrimSpace(posting.Title) == "" {
			continue
		}
		jobs = append(jobs, normalizePosting(provider.Posting{
			SourceJobID:    "pinpoint:" + jobToken,
			Company:        sourceCompany(source, companySlug),
			Title:          posting.Title,
			Location:       location,
			EmploymentType: employmentFromText(posting.Title, firstNonEmptyString(posting.EmploymentTypeText, posting.EmploymentType)),
			SourceURL:      source.URL,
			ApplyURL:       posting.URL,
			Live:           true,
			Confidence:     0.89,
			Strategy:       provider.TierATS,
			Evidence: []provider.Evidence{
				{Field: "ats", Text: "Pinpoint public postings JSON", URL: endpoint.String()},
				{Field: "description", Text: description, URL: posting.URL},
				{Field: "location", Text: location, URL: posting.URL},
				{Field: "workplace_type", Text: firstNonEmptyString(posting.WorkplaceTypeText, posting.WorkplaceType), URL: posting.URL},
			},
		}))
	}
	if len(jobs) == 0 {
		return provider.Result{}, errors.New("no pinpoint jobs found")
	}

	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.89,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "Pinpoint public postings JSON", URL: endpoint.String()},
		},
		Diagnostics: map[string]string{"provider_engine": e.Name(), "pinpoint_company": companySlug},
	}, nil
}

func (e *Engine) extractJobvite(ctx context.Context, source provider.Source) (provider.Result, error) {
	boardURL, err := jobviteBoardURL(source.URL)
	if err != nil {
		return provider.Result{}, err
	}
	companySlug, err := jobviteCompanySlug(source.URL)
	if err != nil {
		return provider.Result{}, err
	}

	directURL := jobviteDirectJobLink(source.URL)
	links := make([]jobviteJobLink, 0)
	document, err := e.getText(ctx, boardURL.String(), "text/html,application/xhtml+xml")
	if err != nil {
		if ctx.Err() != nil || directURL == "" {
			return provider.Result{}, err
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

	jobs := make([]provider.Posting, 0, len(links))
	for _, link := range links {
		detailDocument, err := e.getText(ctx, link.URL, "text/html,application/xhtml+xml")
		if err != nil {
			if ctx.Err() != nil {
				return provider.Result{}, err
			}
			continue
		}
		detailBaseURL, err := parseSourceURL(link.URL)
		if err != nil {
			continue
		}
		detailJobs := jobviteJSONLDJobs(source, companySlug, detailBaseURL, detailDocument)
		if len(detailJobs) == 0 {
			if job, ok := jobvitePostingFromHTML(source, companySlug, detailBaseURL, detailDocument); ok {
				detailJobs = append(detailJobs, job)
			}
		}
		jobs = append(jobs, detailJobs...)
	}
	if len(jobs) == 0 {
		return provider.Result{}, errors.New("no jobvite jobs found")
	}

	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.85,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "Jobvite hosted job board and JobPosting detail pages", URL: boardURL.String()},
		},
		Diagnostics: map[string]string{"provider_engine": e.Name(), "jobvite_company": companySlug},
	}, nil
}

func (e *Engine) extractTeamtailor(ctx context.Context, source provider.Source) (provider.Result, error) {
	boardURL, err := teamtailorBoardURL(source.URL)
	if err != nil {
		return provider.Result{}, err
	}
	account := teamtailorAccountToken(source.URL)
	if account == "" {
		return provider.Result{}, fmt.Errorf("teamtailor account missing in %q", source.URL)
	}

	directURL := teamtailorDirectJobLink(source.URL)
	document, err := e.getText(ctx, boardURL.String(), "text/html,application/xhtml+xml")
	links := make([]teamtailorJobLink, 0)
	if err != nil {
		if ctx.Err() != nil || directURL == "" {
			return provider.Result{}, err
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

	jobs := make([]provider.Posting, 0, len(links))
	for _, link := range links {
		detailDocument, err := e.getText(ctx, link.URL, "text/html,application/xhtml+xml")
		if err != nil {
			if ctx.Err() != nil {
				return provider.Result{}, err
			}
			continue
		}
		detailBaseURL, err := parseSourceURL(link.URL)
		if err != nil {
			continue
		}
		for _, job := range teamtailorJSONLDJobs(source, detailBaseURL, detailDocument) {
			jobs = append(jobs, normalizeTeamtailorJob(source, account, link.URL, detailDocument, job))
		}
	}
	if len(jobs) == 0 {
		return provider.Result{}, errors.New("no teamtailor jobs found")
	}
	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.86,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "Teamtailor hosted job board and JobPosting detail pages", URL: boardURL.String()},
		},
		Diagnostics: map[string]string{"provider_engine": e.Name(), "teamtailor_account": account},
	}, nil
}

func (e *Engine) extractOracleRecruiting(ctx context.Context, source provider.Source) (provider.Result, error) {
	config, err := oracleRecruitingConfigFromURL(source.URL)
	if err != nil {
		return provider.Result{}, err
	}
	if config.JobID != "" {
		job, ok := e.oracleRecruitingDetailPosting(ctx, source, config)
		if !ok {
			return provider.Result{}, errors.New("no oracle recruiting jobs found")
		}
		return provider.Result{
			Source:     source,
			Jobs:       []provider.Posting{job},
			Confidence: 0.84,
			Strategy:   provider.TierATS,
			Live:       true,
			FetchedAt:  time.Now().UTC(),
			RawEvidence: []provider.Evidence{
				{Field: "ats_endpoint", Text: "Oracle Recruiting Candidate Experience detail page", URL: job.ApplyURL},
			},
			Diagnostics: paginationDiagnostics(map[string]string{"provider_engine": e.Name(), "oracle_site_number": config.SiteNumber}, 1, 1, 1, 1, false),
		}, nil
	}

	jobs := make([]provider.Posting, 0, e.oracleRecruitingMaxJobs)
	var evidenceURL string
	pagesFetched, totalAvailable, requisitionsFetched := 0, 0, 0
	hasMore := false
	resultLimitReached := false
	for page := 0; page < e.oracleRecruitingMaxPages && len(jobs) < e.oracleRecruitingMaxJobs; page++ {
		offset := page * e.oracleRecruitingPageSize
		endpoint, err := oracleRecruitingSearchURL(source.URL, config, e.oracleRecruitingPageSize, offset)
		if err != nil {
			return provider.Result{}, err
		}
		evidenceURL = endpoint.String()
		var payload oracleRecruitingResponse
		if err := e.getJSON(ctx, endpoint.String(), &payload); err != nil {
			return provider.Result{}, err
		}
		requisitions, total := oracleRecruitingRequisitions(payload)
		pagesFetched++
		totalAvailable = total
		requisitionsFetched += len(requisitions)
		if len(requisitions) == 0 {
			hasMore = false
			break
		}
		for _, requisition := range requisitions {
			if len(jobs) >= e.oracleRecruitingMaxJobs {
				resultLimitReached = true
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
		hasMore = len(requisitions) >= e.oracleRecruitingPageSize && !(total > 0 && offset+len(requisitions) >= total) && len(jobs) < e.oracleRecruitingMaxJobs
		if !hasMore {
			break
		}
	}
	if len(jobs) == 0 {
		return provider.Result{}, errors.New("no oracle recruiting jobs found")
	}

	return provider.Result{
		Source:     source,
		Jobs:       jobs,
		Confidence: 0.85,
		Strategy:   provider.TierATS,
		Live:       true,
		FetchedAt:  time.Now().UTC(),
		RawEvidence: []provider.Evidence{
			{Field: "ats_endpoint", Text: "Oracle Recruiting Candidate Experience public requisitions API", URL: evidenceURL},
		},
		Diagnostics: paginationDiagnostics(map[string]string{"provider_engine": e.Name(), "oracle_site_number": config.SiteNumber}, pagesFetched, e.oracleRecruitingPageSize, totalAvailable, e.oracleRecruitingMaxJobs, hasMore || resultLimitReached || totalAvailable > requisitionsFetched),
	}, nil
}

func paginationDiagnostics(values map[string]string, pagesFetched, pageSize, totalAvailable, resultLimit int, hasMore bool) map[string]string {
	status, reason := "complete", "all_pages_exhausted"
	if hasMore {
		status, reason = "truncated", "result_or_page_limit_reached"
	}
	values["completeness_status"] = status
	values["completeness_reason"] = reason
	values["pages_fetched"] = strconv.Itoa(pagesFetched)
	values["page_size"] = strconv.Itoa(pageSize)
	values["total_available"] = strconv.Itoa(totalAvailable)
	values["result_limit"] = strconv.Itoa(resultLimit)
	values["has_more"] = strconv.FormatBool(hasMore)
	return values
}

func (e *Engine) oracleRecruitingDetailPosting(ctx context.Context, source provider.Source, config oracleRecruitingConfig) (provider.Posting, bool) {
	detailURL := oracleRecruitingJobURL(source.URL, config, config.JobID)
	page, err := e.getText(ctx, firstNonEmptyString(detailURL, source.URL), "text/html")
	if err != nil {
		return provider.Posting{}, false
	}
	detail := oracleRecruitingDetailFromHTML(page)
	req := oracleRecruitingRequisition{
		ID:                  config.JobID,
		Title:               detail.Title,
		ShortDescriptionStr: detail.Description,
	}
	return oracleRecruitingPosting(source, config, req, firstNonEmptyString(detailURL, source.URL), detail)
}

func (e *Engine) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s returned HTTP %d", endpoint, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (e *Engine) getXML(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/xml,text/xml")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s returned HTTP %d", endpoint, resp.StatusCode)
	}
	return xml.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
}

func (e *Engine) getText(ctx context.Context, endpoint string, accept string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", accept)
	resp, err := e.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GET %s returned HTTP %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
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
	Shortcode         string            `json:"shortcode"`
	Title             string            `json:"title"`
	FullTitle         string            `json:"full_title"`
	State             string            `json:"state"`
	Department        string            `json:"department"`
	Description       string            `json:"description"`
	FullDescription   string            `json:"full_description"`
	Requirements      string            `json:"requirements"`
	Benefits          string            `json:"benefits"`
	URL               string            `json:"url"`
	ApplicationURL    string            `json:"application_url"`
	Shortlink         string            `json:"shortlink"`
	EmploymentType    string            `json:"employment_type"`
	EmploymentTypeAlt string            `json:"employmentType"`
	WorkType          string            `json:"worktype"`
	WorkTypeSnake     string            `json:"work_type"`
	Location          workableLocation  `json:"location"`
	Locations         workableLocations `json:"locations"`
	PublishedAt       string            `json:"published_at"`
	PublishedOn       string            `json:"published_on"`
	CreatedAt         string            `json:"created_at"`
	Created           string            `json:"created"`
	Updated           string            `json:"updated"`
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

type icimsSitemap struct {
	URLs []icimsSitemapEntry `xml:"url"`
}

type icimsSitemapEntry struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod"`
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

type jobviteJobLink struct {
	URL string
}

type teamtailorJobLink struct {
	URL string
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

func greenhouseBoardToken(rawURL string) (string, error) {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return "", err
	}
	if token := strings.TrimSpace(parsed.Query().Get("for")); token != "" {
		return token, nil
	}
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
		if subdomain != "" && subdomain != "www" && subdomain != "apply" && subdomain != "jobs" {
			return subdomain, nil
		}
	}
	return "", errors.New("workable account slug is required")
}

func parseSourceURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid source url: %s", rawURL)
	}
	return parsed, nil
}

func nonEmptyPathParts(parsed *url.URL) []string {
	rawParts := strings.Split(parsed.EscapedPath(), "/")
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		part, _ = url.PathUnescape(part)
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func firstPathSegment(parsed *url.URL) string {
	parts := nonEmptyPathParts(parsed)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func nilIfEmpty(value string, label string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", label)
	}
	return nil
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

func sourceCompany(source provider.Source, fallback string) string {
	if strings.TrimSpace(source.Name) != "" {
		return strings.TrimSpace(source.Name)
	}
	for _, key := range []string{"company", "company_name"} {
		if value := normalizeSpace(source.Metadata[key]); value != "" {
			return value
		}
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

func workablePublished(state string) bool {
	state = strings.ToLower(strings.TrimSpace(state))
	return state == "" || state == "published" || state == "active" || state == "open"
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
		if strings.EqualFold(value, "TELECOMMUTE") {
			return "Remote"
		}
		return normalizeSpace(value)
	}
	return strings.Join(compactStringList(
		loc.Name,
		loc.City,
		firstNonEmptyString(loc.Region, loc.Subregion),
		canonicalCountry(firstNonEmptyString(loc.Country, loc.CountryName, loc.CountryNameAlt)),
	), ", ")
}

func workableCountry(loc workableLocation) string {
	switch strings.ToUpper(strings.TrimSpace(loc.CountryCode)) {
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
	if country := canonicalCountry(firstNonEmptyString(loc.Country, loc.CountryName, loc.CountryNameAlt)); country != "" {
		return country
	}
	parts := strings.Split(loc.LocationStr, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		if country := canonicalCountry(strings.TrimSpace(parts[i])); country != "" {
			return country
		}
	}
	return ""
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

func workableJobsPosting(source provider.Source, endpoint string, item workableJob) (provider.Posting, bool) {
	title := firstNonEmptyString(item.FullTitle, item.Title)
	id := firstNonEmptyString(item.ID, item.Shortcode, stableJobToken(item.URL, title))
	company := firstNonEmptyString(item.Company.Title, source.Name, item.Company.ID)
	applyURL := firstNonEmptyString(item.ApplicationURL, item.URL, item.Shortlink)
	if title == "" || id == "" || company == "" || applyURL == "" {
		return provider.Posting{}, false
	}
	location, country := workableJobLocation(item)
	description := workableDescription(item)
	postedAt := parseTimePtr(firstNonEmptyString(item.PublishedAt, item.PublishedOn, item.CreatedAt, item.Created, item.Updated))
	companyToken := firstNonEmptyString(item.Company.ID, strings.ToLower(strings.ReplaceAll(normalizeSpace(company), " ", "-")))
	job := normalizePosting(provider.Posting{
		SourceJobID:    "workable_jobs:" + companyToken + ":" + id,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: employmentFromText(title, firstNonEmptyString(item.EmploymentType, item.EmploymentTypeAlt, item.WorkType, item.WorkTypeSnake)),
		SourceURL:      firstNonEmptyString(item.URL, item.Shortlink, source.URL),
		ApplyURL:       applyURL,
		PostedAt:       postedAt,
		Live:           true,
		Confidence:     0.86,
		Strategy:       provider.TierATS,
		Evidence: []provider.Evidence{
			{Field: "ats", Text: "Workable Jobs public search API", URL: endpoint},
			{Field: "description", Text: description, URL: firstNonEmptyString(item.URL, applyURL)},
			{Field: "department", Text: item.Department, URL: firstNonEmptyString(item.URL, applyURL)},
			{Field: "location", Text: location, URL: firstNonEmptyString(item.URL, applyURL)},
			{Field: "company", Text: firstNonEmptyString(item.Company.Title, item.Company.Website), URL: firstNonEmptyString(item.Company.URL, item.Company.Website)},
		},
	})
	return job, true
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

func comeetConfigFromSource(source provider.Source) (comeetConfig, error) {
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

func bambooHRDetailPosting(source provider.Source, account string, detailURL string, summary bambooHRListJob, detail bambooHRDetailJob) (provider.Posting, bool) {
	status := strings.ToLower(strings.TrimSpace(detail.JobOpeningStatus))
	if status != "" && status != "open" {
		return provider.Posting{}, false
	}

	id := firstNonEmptyString(detail.ID, summary.ID, bambooHRJobIDFromURL(detailURL))
	title := firstNonEmptyString(detail.JobOpeningName, summary.JobOpeningName)
	if id == "" || title == "" {
		return provider.Posting{}, false
	}
	location, country := bambooHRLocationText(
		firstNonZeroBambooHRLocation(detail.Location, summary.Location),
		firstNonZeroBambooHRATSLocation(detail.ATSLocation, summary.ATSLocation),
		firstNonNilBool(detail.IsRemote, summary.IsRemote),
	)
	description := cleanHTMLText(detail.Description)
	applyURL := firstNonEmptyString(detail.JobOpeningShareURL, bambooHRPublicJobURL(detailURL, id), detailURL)
	evidence := []provider.Evidence{
		{Field: "ats", Text: "BambooHR public careers API", URL: detailURL},
	}
	if description != "" {
		evidence = append(evidence, provider.Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if department := firstNonEmptyString(detail.DepartmentLabel, summary.DepartmentLabel); department != "" {
		evidence = append(evidence, provider.Evidence{Field: "department", Text: department, URL: applyURL})
	}
	if detail.Compensation != "" {
		evidence = append(evidence, provider.Evidence{Field: "compensation", Text: detail.Compensation, URL: applyURL})
	}
	if detail.MinimumExperience != "" {
		evidence = append(evidence, provider.Evidence{Field: "minimum_experience", Text: detail.MinimumExperience, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, provider.Evidence{Field: "location", Text: location, URL: applyURL})
	}

	return normalizePosting(provider.Posting{
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
		Strategy:       provider.TierATS,
		Evidence:       evidence,
	}), true
}

func bambooHRSummaryPosting(source provider.Source, account string, detailURL string, summary bambooHRListJob) (provider.Posting, bool) {
	id := strings.TrimSpace(summary.ID)
	title := strings.TrimSpace(summary.JobOpeningName)
	if id == "" || title == "" {
		return provider.Posting{}, false
	}
	location, country := bambooHRLocationText(summary.Location, summary.ATSLocation, summary.IsRemote)
	applyURL := firstNonEmptyString(bambooHRPublicJobURL(detailURL, id), source.URL)
	evidence := []provider.Evidence{
		{Field: "ats", Text: "BambooHR public careers list summary", URL: detailURL},
	}
	if summary.DepartmentLabel != "" {
		evidence = append(evidence, provider.Evidence{Field: "department", Text: summary.DepartmentLabel, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, provider.Evidence{Field: "location", Text: location, URL: applyURL})
	}
	return normalizePosting(provider.Posting{
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
		Strategy:       provider.TierATS,
		Evidence:       evidence,
	}), true
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
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return ""
	}
	parts := strings.Split(host, ".")
	if len(parts) >= 3 && parts[len(parts)-2] == "bamboohr" && parts[len(parts)-1] == "com" {
		return stableAccountToken(parts[0])
	}
	return stableAccountToken(host)
}

func stableAccountToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("_", "-", ".", "-", " ", "-", ":", "-")
	value = replacer.Replace(value)
	return strings.Trim(value, "-")
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

func icimsJSONLDJobs(source provider.Source, entry icimsSitemapEntry, detailURL *url.URL, document string) []provider.Posting {
	jobs := make([]provider.Posting, 0)
	detailURLString := detailURL.String()
	for _, job := range teamtailorJSONLDJobs(source, detailURL, document) {
		jobs = append(jobs, normalizeICIMSJob(source, entry, detailURLString, document, job))
	}
	return jobs
}

func normalizeICIMSJob(source provider.Source, entry icimsSitemapEntry, detailURL string, document string, job provider.Posting) provider.Posting {
	jobID := firstNonEmptyString(icimsJobIDFromURL(entry.Loc), icimsJobIDFromURL(job.ApplyURL), stableJobToken(entry.Loc, job.Title))
	job.SourceJobID = "icims:" + jobID
	job.SourceURL = source.URL
	job.ApplyURL = firstNonEmptyString(icimsApplyURL(document, detailURL), job.ApplyURL, entry.Loc)
	job.Strategy = provider.TierATS
	job.Confidence = 0.86
	if job.PostedAt == nil {
		job.PostedAt = parseTimePtr(entry.LastMod)
	}
	job.Evidence = append(job.Evidence, provider.Evidence{Field: "ats", Text: "iCIMS sitemap and JobPosting detail page", URL: detailURL})
	return normalizePosting(job)
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
	baseURL, err := parseSourceURL(detailURL)
	if err != nil {
		return ""
	}
	for _, anchor := range anchorTagPattern.FindAllString(document, -1) {
		href := anchorHref(anchor)
		if strings.Contains(strings.ToLower(href), "mode=apply") {
			return resolveURL(baseURL, href)
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
		detailURL := normalizeJobviteDetailURL(resolveURL(baseURL, href))
		if detailURL == "" || jobviteJobIDFromURL(detailURL) == "" {
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

func normalizeJobviteJob(source provider.Source, companySlug string, detailURL string, document string, job provider.Posting) provider.Posting {
	jobID := firstNonEmptyString(jobviteJobIDFromURL(detailURL), jobviteJobIDFromURL(job.ApplyURL), job.SourceJobID, stableJobToken(detailURL, job.Title))
	job.SourceJobID = "jobvite:" + companySlug + ":" + jobID
	job.SourceURL = source.URL
	job.Company = firstNonEmptyString(job.Company, jobviteCompanyName(document), sourceCompany(source, companySlug))
	job.ApplyURL = firstNonEmptyString(jobviteApplyURL(document, detailURL), job.ApplyURL, detailURL)
	job.Strategy = provider.TierATS
	job.Confidence = 0.85
	job.Evidence = append(job.Evidence, provider.Evidence{Field: "ats", Text: "Jobvite hosted job board and JobPosting detail page", URL: detailURL})
	return normalizePosting(job)
}

func jobviteJSONLDJobs(source provider.Source, companySlug string, detailURL *url.URL, document string) []provider.Posting {
	jobs := make([]provider.Posting, 0)
	detailURLString := detailURL.String()
	for _, job := range teamtailorJSONLDJobs(source, detailURL, document) {
		jobs = append(jobs, normalizeJobviteJob(source, companySlug, detailURLString, document, job))
	}
	return jobs
}

func jobvitePostingFromHTML(source provider.Source, companySlug string, detailURL *url.URL, document string) (provider.Posting, bool) {
	title := cleanHTMLText(firstRegexpGroup(jobviteTitlePattern, document))
	if title == "" {
		return provider.Posting{}, false
	}
	detailURLString := detailURL.String()
	description := cleanHTMLText(htmlClassSegment(document, "jv-job-detail-description", "job-description-meta", "jv-job-detail-bottom-actions", "jv-current-openings"))
	location := firstNonEmptyString(jobviteMetaValue(document, "Location"), jobviteLocationFromDetailMeta(document))
	employment := employmentFromText(title, jobviteMetaValue(document, "Employment Type"))
	return normalizeJobviteJob(source, companySlug, detailURLString, document, provider.Posting{
		SourceJobID:    jobviteJobIDFromURL(detailURLString),
		Company:        firstNonEmptyString(jobviteCompanyName(document), sourceCompany(source, companySlug)),
		Title:          title,
		Location:       location,
		EmploymentType: employment,
		SourceURL:      source.URL,
		ApplyURL:       firstNonEmptyString(jobviteApplyURL(document, detailURLString), detailURLString),
		Live:           true,
		Confidence:     0.78,
		Strategy:       provider.TierATS,
		Evidence: []provider.Evidence{
			{Field: "html", Text: "Jobvite hosted detail page", URL: detailURLString},
			{Field: "description", Text: description, URL: detailURLString},
			{Field: "location", Text: location, URL: detailURLString},
		},
	}), true
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
		return resolveURL(baseURL, href)
	}
	return ""
}

func jobviteCompanyName(document string) string {
	return html.UnescapeString(firstRegexpGroup(jobviteCompanyPattern, document))
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
		detailURL := normalizeTeamtailorDetailURL(resolveURL(baseURL, anchorHref(anchor)))
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

func normalizeTeamtailorJob(source provider.Source, account string, detailURL string, document string, job provider.Posting) provider.Posting {
	jobID := firstNonEmptyString(teamtailorJobIDFromURL(detailURL), teamtailorJobIDFromURL(job.ApplyURL), job.SourceJobID, stableJobToken(detailURL, job.Title))
	job.SourceJobID = "teamtailor:" + account + ":" + jobID
	job.SourceURL = source.URL
	job.ApplyURL = firstNonEmptyString(teamtailorApplyURL(document, detailURL), job.ApplyURL, detailURL)
	job.Strategy = provider.TierATS
	job.Confidence = 0.86
	job.Evidence = append(job.Evidence, provider.Evidence{Field: "ats", Text: "Teamtailor hosted job board and JobPosting detail page", URL: detailURL})
	return normalizePosting(job)
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
		return resolveURL(baseURL, html.UnescapeString(strings.TrimSpace(match[1])))
	}
	return strings.TrimRight(detailURL, "/") + "/applications/new"
}

func teamtailorAccountToken(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return ""
	}
	parts := strings.Split(host, ".")
	if len(parts) >= 3 && parts[len(parts)-2] == "teamtailor" && parts[len(parts)-1] == "com" {
		return stableAccountToken(parts[0])
	}
	return stableAccountToken(host)
}

func teamtailorJSONLDJobs(source provider.Source, baseURL *url.URL, document string) []provider.Posting {
	matches := jsonLDScriptPattern.FindAllStringSubmatch(document, -1)
	jobs := make([]provider.Posting, 0, len(matches))
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
			job, ok := postingFromJSONLD(source, baseURL, node)
			if ok {
				jobs = append(jobs, job)
			}
		}
	}
	return jobs
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

func oracleRecruitingPosting(source provider.Source, config oracleRecruitingConfig, req oracleRecruitingRequisition, detailURL string, detail oracleRecruitingDetail) (provider.Posting, bool) {
	id := strings.TrimSpace(req.ID)
	title := firstNonEmptyString(detail.Title, req.Title)
	if id == "" || title == "" {
		return provider.Posting{}, false
	}
	description := oracleRecruitingDescription(req, detail)
	location := firstNonEmptyString(req.PrimaryLocation)
	country := canonicalCountry(req.PrimaryLocationCountry)
	applyURL := firstNonEmptyString(detailURL, oracleRecruitingJobURL(source.URL, config, id), source.URL)
	evidence := []provider.Evidence{
		{Field: "ats", Text: "Oracle Recruiting Candidate Experience public requisitions API", URL: source.URL},
	}
	if description != "" {
		evidence = append(evidence, provider.Evidence{Field: "description", Text: description, URL: applyURL})
	}
	if location != "" {
		evidence = append(evidence, provider.Evidence{Field: "location", Text: location, URL: applyURL})
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
			evidence = append(evidence, provider.Evidence{Field: field.name, Text: field.text, URL: applyURL})
		}
	}
	if req.HotJobFlag {
		evidence = append(evidence, provider.Evidence{Field: "hot_job", Text: "true", URL: applyURL})
	}
	if req.TrendingFlag {
		evidence = append(evidence, provider.Evidence{Field: "trending", Text: "true", URL: applyURL})
	}

	return normalizePosting(provider.Posting{
		SourceJobID:    "oracle_recruiting:" + stableAccountToken(config.SiteNumber) + ":" + id,
		Company:        sourceCompany(source, firstNonEmptyString(detail.SiteName, req.LegalEmployer, req.Organization, config.SiteNumber)),
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: oracleRecruitingEmploymentType(title, req),
		SourceURL:      source.URL,
		ApplyURL:       applyURL,
		PostedAt:       parseTimePtr(req.PostedDate),
		Live:           true,
		Confidence:     0.85,
		Strategy:       provider.TierATS,
		Evidence:       evidence,
	}), true
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

func postingFromJSONLD(source provider.Source, baseURL *url.URL, node map[string]any) (provider.Posting, bool) {
	title := jsonLDStringField(node, "title")
	if title == "" {
		return provider.Posting{}, false
	}
	rawURL := firstNonEmptyString(jsonLDStringField(node, "url"), jsonLDStringField(node, "sameAs"), baseURL.String())
	applyURL := resolveURL(baseURL, rawURL)
	company := firstNonEmptyString(jsonLDNestedString(node["hiringOrganization"], "name"), sourceCompany(source, sourceHost(source.URL)))
	location, country := jsonLDJobLocation(node["jobLocation"])
	description := cleanHTMLText(jsonLDStringField(node, "description", "responsibilities", "skills"))
	sourceJobID := firstNonEmptyString(jsonLDIdentifier(node["identifier"]), stableJobToken(applyURL, title))
	if sourceJobID == "" {
		return provider.Posting{}, false
	}
	return normalizePosting(provider.Posting{
		SourceJobID:    sourceJobID,
		Company:        company,
		Title:          title,
		Location:       location,
		Country:        country,
		EmploymentType: strings.ToLower(strings.Join(jsonLDStringList(node["employmentType"]), ",")),
		SourceURL:      source.URL,
		ApplyURL:       firstNonEmptyString(applyURL, source.URL),
		PostedAt:       parseTimePtr(jsonLDStringField(node, "datePosted")),
		Live:           true,
		Confidence:     0.82,
		Strategy:       provider.TierATS,
		Evidence: []provider.Evidence{
			{Field: "json_ld", Text: "schema.org JobPosting", URL: source.URL},
			{Field: "description", Text: description, URL: applyURL},
			{Field: "location", Text: location, URL: applyURL},
		},
	}), true
}

func collectJSONLDJobPostings(value any, out *[]map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		if jsonLDTypeContains(typed["@type"], "JobPosting") {
			*out = append(*out, typed)
		}
		for _, child := range typed {
			collectJSONLDJobPostings(child, out)
		}
	case []any:
		for _, child := range typed {
			collectJSONLDJobPostings(child, out)
		}
	}
}

func jsonLDTypeContains(value any, want string) bool {
	for _, item := range jsonLDStringList(value) {
		if strings.EqualFold(item, want) {
			return true
		}
	}
	return false
}

func jsonLDStringField(node map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := jsonLDString(node[key]); value != "" {
			return value
		}
	}
	return ""
}

func jsonLDNestedString(value any, keys ...string) string {
	switch typed := value.(type) {
	case map[string]any:
		return jsonLDStringField(typed, keys...)
	case []any:
		for _, item := range typed {
			if value := jsonLDNestedString(item, keys...); value != "" {
				return value
			}
		}
	}
	return ""
}

func jsonLDIdentifier(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		return firstNonEmptyString(jsonLDStringField(typed, "value"), jsonLDStringField(typed, "name"), jsonLDStringField(typed, "@id"))
	case []any:
		for _, item := range typed {
			if id := jsonLDIdentifier(item); id != "" {
				return id
			}
		}
	}
	return ""
}

func jsonLDJobLocation(value any) (string, string) {
	switch typed := value.(type) {
	case map[string]any:
		return jsonLDPlaceLocation(typed)
	case []any:
		locations := make([]string, 0, len(typed))
		country := ""
		for _, item := range typed {
			locationText, itemCountry := jsonLDJobLocation(item)
			if locationText != "" {
				locations = append(locations, locationText)
			}
			if country == "" {
				country = itemCountry
			}
		}
		return strings.Join(compactStringList(locations...), "; "), country
	default:
		return "", ""
	}
}

func jsonLDPlaceLocation(place map[string]any) (string, string) {
	address, _ := place["address"].(map[string]any)
	if address == nil {
		if name := jsonLDStringField(place, "name"); name != "" {
			return name, ""
		}
		return "", ""
	}
	country := canonicalCountry(jsonLDString(address["addressCountry"]))
	location := strings.Join(compactStringList(
		jsonLDStringField(address, "addressLocality"),
		jsonLDStringField(address, "addressRegion"),
		country,
	), ", ")
	if location == "" {
		return jsonLDStringField(place, "name"), country
	}
	return location, country
}

func jsonLDStringList(value any) []string {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{strings.TrimSpace(typed)}
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			values = append(values, jsonLDStringList(item)...)
		}
		return values
	default:
		if value := jsonLDString(value); value != "" {
			return []string{value}
		}
		return nil
	}
}

func jsonLDString(value any) string {
	switch typed := value.(type) {
	case string:
		return normalizeSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case map[string]any:
		return firstNonEmptyString(jsonLDStringField(typed, "name"), jsonLDStringField(typed, "value"), jsonLDStringField(typed, "@id"))
	default:
		return ""
	}
}

func anchorHref(anchor string) string {
	match := anchorHrefPattern.FindStringSubmatch(anchor)
	if len(match) < 2 {
		return ""
	}
	return html.UnescapeString(strings.TrimSpace(match[1]))
}

func resolveURL(base *url.URL, rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || base == nil {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return base.ResolveReference(parsed).String()
}

func sourceHost(rawURL string) string {
	parsed, err := parseSourceURL(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
}

func cleanHTMLText(value string) string {
	value = html.UnescapeString(value)
	value = htmlTagPattern.ReplaceAllString(value, " ")
	return normalizeSpace(value)
}

func firstRegexpGroup(pattern *regexp.Regexp, value string) string {
	match := pattern.FindStringSubmatch(value)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
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
		if match := pattern.FindStringSubmatch(tag); len(match) >= 2 {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

func htmlClassSegment(document string, className string, endMarkers ...string) string {
	pattern := regexp.MustCompile(`(?is)<[^>]*class=["'][^"']*` + regexp.QuoteMeta(className) + `[^"']*["'][^>]*>`)
	match := pattern.FindStringIndex(document)
	if len(match) != 2 {
		return ""
	}
	lower := strings.ToLower(document)
	end := len(document)
	for _, marker := range endMarkers {
		if marker == "" {
			continue
		}
		if idx := strings.Index(lower[match[1]:], strings.ToLower(marker)); idx >= 0 && match[1]+idx < end {
			end = match[1] + idx
		}
	}
	return document[match[1]:end]
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

func normalizePosting(in provider.Posting) provider.Posting {
	out := in
	out.SourceJobID = normalizeSpace(out.SourceJobID)
	out.Company = normalizeSpace(out.Company)
	out.Title = normalizeSpace(out.Title)
	out.Location = normalizeSpace(out.Location)
	out.Country = normalizeCountry(normalizeSpace(out.Country), out.Location)
	out.EmploymentType = normalizeSpace(out.EmploymentType)
	out.Level = normalizeSpace(out.Level)
	out.RoleFamily = normalizeSpace(out.RoleFamily)
	out.SourceURL = strings.TrimSpace(out.SourceURL)
	out.ApplyURL = strings.TrimSpace(out.ApplyURL)
	if out.Level == "" {
		out.Level = inferLevel(out.Title)
	}
	if out.RoleFamily == "" {
		out.RoleFamily = inferRoleFamily(out.Title)
	}
	for i := range out.Evidence {
		out.Evidence[i].Field = normalizeSpace(out.Evidence[i].Field)
		out.Evidence[i].Text = normalizeSpace(out.Evidence[i].Text)
		out.Evidence[i].URL = strings.TrimSpace(out.Evidence[i].URL)
	}
	return out
}

func inferLevel(title string) string {
	lower := strings.ToLower(title)
	switch {
	case internLevelPattern.MatchString(lower):
		return "internship"
	case newGradLevelPattern.MatchString(lower):
		return "new_grad"
	case earlyCareerPattern.MatchString(lower):
		return "early_career"
	default:
		return "unknown"
	}
}

func inferRoleFamily(title string) string {
	lower := strings.ToLower(title)
	switch {
	case strings.Contains(lower, "machine learning"), strings.Contains(lower, "artificial intelligence"), strings.Contains(lower, "ml_ai"), aiTokenPattern.MatchString(lower):
		return "ml_ai"
	case strings.Contains(lower, "data"):
		return "data"
	case strings.Contains(lower, "frontend"), strings.Contains(lower, "front-end"), strings.Contains(lower, "web"):
		return "frontend"
	case strings.Contains(lower, "backend"), strings.Contains(lower, "back-end"), strings.Contains(lower, "platform"):
		return "backend"
	case strings.Contains(lower, "infrastructure"), strings.Contains(lower, "infra"), strings.Contains(lower, "systems"):
		return "infrastructure"
	case strings.Contains(lower, "full stack"), strings.Contains(lower, "full-stack"):
		return "full_stack"
	default:
		return "software_engineering"
	}
}

func normalizeCountry(country, location string) string {
	if country != "" {
		return canonicalCountry(country)
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

func canonicalCountry(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToUpper(value) {
	case "US", "USA":
		return "US"
	case "GB", "UK":
		return "UK"
	case "SG":
		return "Singapore"
	case "HK":
		return "Hong Kong"
	case "CA":
		return "Canada"
	case "AU":
		return "Australia"
	case "DE":
		return "Germany"
	case "FR":
		return "France"
	case "IN":
		return "India"
	case "JP":
		return "Japan"
	case "NL":
		return "Netherlands"
	case "IL":
		return "Israel"
	case "ES":
		return "Spain"
	case "SE":
		return "Sweden"
	case "PL":
		return "Poland"
	case "IE":
		return "Ireland"
	case "CH":
		return "Switzerland"
	case "BR":
		return "Brazil"
	case "MX":
		return "Mexico"
	case "NZ":
		return "New Zealand"
	}
	switch strings.ToLower(value) {
	case "united states", "united states of america":
		return "US"
	case "united kingdom", "great britain", "england", "scotland", "wales":
		return "UK"
	case "singapore":
		return "Singapore"
	case "hong kong":
		return "Hong Kong"
	case "canada":
		return "Canada"
	case "australia":
		return "Australia"
	case "germany", "deutschland":
		return "Germany"
	case "france":
		return "France"
	case "india":
		return "India"
	case "japan":
		return "Japan"
	case "netherlands", "the netherlands", "holland":
		return "Netherlands"
	case "israel":
		return "Israel"
	case "spain":
		return "Spain"
	case "sweden":
		return "Sweden"
	}
	return value
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeSpace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
