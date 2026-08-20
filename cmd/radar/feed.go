package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hwennnn/radar/internal/pipeline"
)

const (
	defaultFeedLimit = 50
	maxFeedLimit     = 500
)

type feedStore interface {
	ListPostings(context.Context) ([]pipeline.Posting, error)
	ListSourceStatuses(context.Context) ([]pipeline.SourceStatus, error)
}

type feedServer struct {
	store        feedStore
	totalSources int
	logoDomains  map[string]string
	now          func() time.Time
	logger       *slog.Logger
}

type feedResponse struct {
	Jobs        []feedJob   `json:"jobs"`
	Summary     feedSummary `json:"summary"`
	Total       int         `json:"total"`
	Showing     int         `json:"showing"`
	Limit       int         `json:"limit"`
	Incremental bool        `json:"incremental,omitempty"`
	ActiveIDs   []string    `json:"active_ids,omitempty"`
}

type feedSummary struct {
	EligibleOpenings int        `json:"eligible_openings"`
	GroupedRoles     int        `json:"grouped_roles"`
	Companies        int        `json:"companies"`
	AddedToday       int        `json:"added_today"`
	AddedThisWeek    int        `json:"added_this_week"`
	SourcesTotal     int        `json:"sources_total"`
	SourcesHealthy   int        `json:"sources_healthy"`
	SourcesFailed    int        `json:"sources_failed"`
	LastUpdatedAt    *time.Time `json:"last_updated_at"`
}

type feedJob struct {
	ID             string        `json:"id"`
	Company        string        `json:"company"`
	LogoDomain     string        `json:"logo_domain,omitempty"`
	Title          string        `json:"title"`
	Location       string        `json:"location"`
	Country        string        `json:"country"`
	Track          string        `json:"track"`
	Category       string        `json:"category"`
	ApplyURL       string        `json:"apply_url"`
	OpeningCount   int           `json:"opening_count"`
	Openings       []feedOpening `json:"openings"`
	FirstSeenAt    time.Time     `json:"first_seen_at"`
	LastSeenAt     time.Time     `json:"last_seen_at"`
	PostedAt       *time.Time    `json:"posted_at,omitempty"`
	EmploymentType string        `json:"employment_type"`
	Level          string        `json:"level"`
}

type feedOpening struct {
	ID             string     `json:"id"`
	ApplyURL       string     `json:"apply_url"`
	EmploymentType string     `json:"employment_type"`
	Level          string     `json:"level"`
	FirstSeenAt    time.Time  `json:"first_seen_at"`
	PostedAt       *time.Time `json:"posted_at,omitempty"`
}

func (s feedServer) handler(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if s.store == nil {
		writeFeedError(w, http.StatusServiceUnavailable, "job feed is unavailable")
		return
	}
	since, incremental, err := feedSince(request.URL.Query().Get("since"))
	if err != nil {
		writeFeedError(w, http.StatusBadRequest, "since must be an RFC3339 timestamp")
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	postings, err := s.store.ListPostings(ctx)
	if err != nil {
		s.logError("list feed postings", err)
		writeFeedError(w, http.StatusInternalServerError, "could not load jobs")
		return
	}
	statuses, err := s.store.ListSourceStatuses(ctx)
	if err != nil {
		s.logError("list feed source statuses", err)
		writeFeedError(w, http.StatusInternalServerError, "could not load source health")
		return
	}

	now := time.Now().UTC()
	if s.now != nil {
		now = s.now().UTC()
	}
	jobs, summary := buildFeed(postings, statuses, s.totalSources, now)
	for i := range jobs {
		jobs[i].LogoDomain = companyLogoDomain(jobs[i].Company, s.logoDomains)
	}
	jobs = filterFeed(jobs, request.URL.Query().Get("q"), request.URL.Query().Get("location"), request.URL.Query().Get("track"), request.URL.Query().Get("role"))
	sortFeed(jobs, request.URL.Query().Get("sort"))
	total := len(jobs)
	limit := feedLimit(request.URL.Query().Get("limit"))
	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	showing := len(jobs)
	var activeIDs []string
	if incremental {
		activeIDs = make([]string, 0, len(jobs))
		updates := make([]feedJob, 0)
		for _, job := range jobs {
			activeIDs = append(activeIDs, job.ID)
			if !job.FirstSeenAt.Before(since) {
				updates = append(updates, job)
			}
		}
		jobs = updates
	}
	if jobs == nil {
		jobs = []feedJob{}
	}
	_ = json.NewEncoder(w).Encode(feedResponse{
		Jobs: jobs, Summary: summary, Total: total, Showing: showing, Limit: limit,
		Incremental: incremental, ActiveIDs: activeIDs,
	})
}

func feedSince(raw string) (time.Time, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false, nil
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false, err
	}
	return value.UTC(), true, nil
}

func (s feedServer) logError(message string, err error) {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Error(message, "error", err)
}

func writeFeedError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func buildFeed(postings []pipeline.Posting, statuses []pipeline.SourceStatus, totalSources int, now time.Time) ([]feedJob, feedSummary) {
	type groupedJob struct {
		job      feedJob
		openings map[string]struct{}
	}
	groups := make(map[string]*groupedJob)
	order := make([]string, 0)
	summary := feedSummary{SourcesTotal: totalSources}
	companies := make(map[string]struct{})
	eligibleOpenings := make(map[string]struct{})
	seenApplyURLs := make(map[string]struct{})
	startToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	startWeek := startToday.AddDate(0, 0, -6)

	for _, status := range statuses {
		switch status.State {
		case "success":
			summary.SourcesHealthy++
		case "failure":
			summary.SourcesFailed++
		}
	}
	if summary.SourcesTotal < len(statuses) {
		summary.SourcesTotal = len(statuses)
	}

	for _, posting := range postings {
		if !pipeline.EligibleAt(posting, now) {
			continue
		}
		companyIdentity := normalizeFeedCompany(posting.Company)
		openingIdentity := posting.ID
		if applyURL := safeApplyURL(posting.ApplyURL); applyURL != "" {
			openingIdentity = companyIdentity + "|url:" + applyURL
			// Old rows can predate stronger URL aliases, or two source routes can
			// describe the same application with slightly different metadata. Keep
			// only one public opening for an exact canonical apply destination while
			// retaining separate requisition URLs as separate openings.
			if _, exists := seenApplyURLs[openingIdentity]; exists {
				continue
			}
			seenApplyURLs[openingIdentity] = struct{}{}
		}
		if _, exists := eligibleOpenings[openingIdentity]; !exists {
			eligibleOpenings[openingIdentity] = struct{}{}
			summary.EligibleOpenings++
			if !posting.FirstSeenAt.Before(startToday) {
				summary.AddedToday++
			}
			if !posting.FirstSeenAt.Before(startWeek) {
				summary.AddedThisWeek++
			}
		}
		companies[companyIdentity] = struct{}{}
		if summary.LastUpdatedAt == nil || posting.LastSeenAt.After(*summary.LastUpdatedAt) {
			lastUpdatedAt := posting.LastSeenAt
			summary.LastUpdatedAt = &lastUpdatedAt
		}

		track := feedTrack(posting)
		category := feedCategory(posting.Title)
		key := strings.Join([]string{
			companyIdentity, normalizeFeedText(posting.Title),
			normalizeFeedText(posting.Location), track,
		}, "|")
		group := groups[key]
		if group == nil {
			hash := sha256.Sum256([]byte(key))
			group = &groupedJob{
				job: feedJob{
					ID: hex.EncodeToString(hash[:8]), Company: strings.TrimSpace(posting.Company),
					Title: strings.TrimSpace(posting.Title), Location: strings.TrimSpace(posting.Location),
					Country: strings.TrimSpace(posting.Country), Track: track, Category: category,
					FirstSeenAt: posting.FirstSeenAt, LastSeenAt: posting.LastSeenAt,
					PostedAt:       posting.PostedAt,
					EmploymentType: strings.TrimSpace(posting.EmploymentType), Level: strings.TrimSpace(posting.Level),
				},
				openings: make(map[string]struct{}),
			}
			groups[key] = group
			order = append(order, key)
		}
		openingKey := safeApplyURL(posting.ApplyURL)
		if openingKey == "" {
			openingKey = posting.ID
		}
		if _, exists := group.openings[openingKey]; exists {
			continue
		}
		group.openings[openingKey] = struct{}{}
		opening := feedOpening{
			ID: posting.ID, ApplyURL: safeApplyURL(posting.ApplyURL),
			EmploymentType: strings.TrimSpace(posting.EmploymentType), Level: strings.TrimSpace(posting.Level),
			FirstSeenAt: posting.FirstSeenAt,
			PostedAt:    posting.PostedAt,
		}
		group.job.Openings = append(group.job.Openings, opening)
		if group.job.ApplyURL == "" {
			group.job.ApplyURL = opening.ApplyURL
		}
		if posting.FirstSeenAt.After(group.job.FirstSeenAt) {
			group.job.FirstSeenAt = posting.FirstSeenAt
			group.job.ApplyURL = opening.ApplyURL
			group.job.EmploymentType = opening.EmploymentType
			group.job.Level = opening.Level
			group.job.PostedAt = opening.PostedAt
		}
		if posting.LastSeenAt.After(group.job.LastSeenAt) {
			group.job.LastSeenAt = posting.LastSeenAt
		}
	}

	jobs := make([]feedJob, 0, len(order))
	for _, key := range order {
		group := groups[key]
		sort.SliceStable(group.job.Openings, func(i, j int) bool {
			return group.job.Openings[i].FirstSeenAt.After(group.job.Openings[j].FirstSeenAt)
		})
		group.job.OpeningCount = len(group.job.Openings)
		jobs = append(jobs, group.job)
	}
	summary.GroupedRoles = len(jobs)
	summary.Companies = len(companies)
	return jobs, summary
}

func filterFeed(jobs []feedJob, query, location, track, role string) []feedJob {
	query = normalizeFeedText(query)
	location = strings.ToLower(strings.TrimSpace(location))
	track = strings.ToLower(strings.TrimSpace(track))
	role = strings.ToLower(strings.TrimSpace(role))
	filtered := make([]feedJob, 0, len(jobs))
	for _, job := range jobs {
		if query != "" && !strings.Contains(normalizeFeedText(strings.Join([]string{job.Company, job.Title, job.Location}, " ")), query) {
			continue
		}
		if location != "" && location != "all" && feedRegion(job.Country, job.Location) != location {
			continue
		}
		if track != "" && track != "all" && job.Track != track {
			continue
		}
		if role != "" && role != "all" && job.Category != role {
			continue
		}
		filtered = append(filtered, job)
	}
	return filtered
}

func sortFeed(jobs []feedJob, mode string) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "company":
		sort.SliceStable(jobs, func(i, j int) bool {
			if jobs[i].Company == jobs[j].Company {
				return jobs[i].Title < jobs[j].Title
			}
			return jobs[i].Company < jobs[j].Company
		})
	default:
		sort.SliceStable(jobs, func(i, j int) bool {
			if jobs[i].FirstSeenAt.Equal(jobs[j].FirstSeenAt) {
				return jobs[i].Company < jobs[j].Company
			}
			return jobs[i].FirstSeenAt.After(jobs[j].FirstSeenAt)
		})
	}
}

func feedLimit(raw string) int {
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit <= 0 {
		return defaultFeedLimit
	}
	if limit > maxFeedLimit {
		return maxFeedLimit
	}
	return limit
}

func feedTrack(posting pipeline.Posting) string {
	metadata := normalizeFeedText(strings.Join([]string{posting.Title, posting.EmploymentType, posting.Level}, " "))
	for _, phrase := range []string{"intern", "internship", "co op", "working student"} {
		if feedHasPhrase(metadata, phrase) {
			return "internship"
		}
	}
	return "new_grad"
}

func feedCategory(title string) string {
	normalized := normalizeFeedText(title)
	switch {
	case feedHasAnyPhrase(normalized, []string{"quant", "trading", "algorithm developer"}):
		return "quant"
	case feedHasAnyPhrase(normalized, []string{"machine learning", "ml", "artificial intelligence", "ai", "llm", "research scientist", "applied scientist"}):
		return "ai_ml"
	case feedHasAnyPhrase(normalized, []string{"data engineer", "data scientist", "data science", "data platform"}):
		return "data"
	case feedHasAnyPhrase(normalized, []string{"infrastructure", "platform", "systems", "site reliability", "production engineer", "security", "network", "linux", "devops", "cloud engineer"}):
		return "infra_security"
	default:
		return "software"
	}
}

func feedRegion(country, location string) string {
	value := normalizeFeedText(country + " " + location)
	if feedHasAnyPhrase(value, []string{"singapore", "sg"}) {
		return "singapore"
	}
	return "us"
}

func normalizeFeedText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return ' '
	}, value))), " ")
}

func normalizeFeedCompany(value string) string {
	return strings.ReplaceAll(normalizeFeedText(value), " ", "")
}

func feedHasPhrase(normalized, phrase string) bool {
	return strings.Contains(" "+normalized+" ", " "+normalizeFeedText(phrase)+" ")
}

func feedHasAnyPhrase(normalized string, phrases []string) bool {
	for _, phrase := range phrases {
		if feedHasPhrase(normalized, phrase) {
			return true
		}
	}
	return false
}

func safeApplyURL(raw string) string {
	return pipeline.CanonicalApplyURL(raw)
}
