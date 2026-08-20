package pipeline

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Catalog is the complete, strictly allowlisted set of companies that routine
// runs may crawl and self-validate. Discovery candidates use a separate type.
type Catalog struct {
	Companies []Company `json:"companies"`
}

type Company struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Sources []Source `json:"sources"`
}

type Source struct {
	ID       string `json:"id"`
	Company  string `json:"-"`
	Provider string `json:"provider"`
	URL      string `json:"url"`
}

var catalogIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// supportedProviders is the explicit promotion boundary for routine crawling.
// Every kind is backed by either a deterministic adapter or the bounded
// TinyFish search/fetch extractor. Discovery cannot introduce an arbitrary
// provider or browser route without a reviewed code change.
var supportedProviders = map[string]struct{}{
	"akuna_careers":              {},
	"amazon_jobs":                {},
	"apple_jobs":                 {},
	"ashby":                      {},
	"bytedance_careers":          {},
	"citadel_careers":            {},
	"citadel_securities_careers": {},
	"cursor_careers":             {},
	"deshaw_careers":             {},
	"eightfold_apply":            {},
	"eightfold_pcsx":             {},
	"gem":                        {},
	"google_careers":             {},
	"greenhouse":                 {},
	"groq_careers":               {},
	"ibm_careers":                {},
	"janestreet_careers":         {},
	"lever":                      {},
	"meta_careers":               {},
	"oldmission_careers":         {},
	"official_careers":           {},
	"oracle_recruiting":          {},
	"rippling":                   {},
	"sig_careers":                {},
	"smartrecruiters":            {},
	"tiktok_careers":             {},
	"twosigma_careers":           {},
	"workable":                   {},
	"workday":                    {},
	"yc_jobs":                    {},
}

// searchDiscoveryProviders are official company routes whose routine
// extraction is intentionally handled by bounded TinyFish search/fetch rather
// than by the structured ATS adapter.
var searchDiscoveryProviders = map[string]struct{}{
	"cursor_careers":     {},
	"deshaw_careers":     {},
	"groq_careers":       {},
	"market_search":      {},
	"oldmission_careers": {},
	"official_careers":   {},
	"sig_careers":        {},
	"tiktok_careers":     {},
	"twosigma_careers":   {},
	"yc_jobs":            {},
}

// LoadCatalog decodes one JSON document and rejects unknown fields, trailing
// values, invalid URLs, missing fields, and duplicate company/source IDs.
func LoadCatalog(r io.Reader) (Catalog, error) {
	var catalog Catalog
	if err := decodeStrictJSON(r, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode verified catalog: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func (c Catalog) Validate() error {
	companyIDs := make(map[string]struct{}, len(c.Companies))
	sourceIDs := make(map[string]struct{})
	for i, company := range c.Companies {
		where := fmt.Sprintf("companies[%d]", i)
		if err := validCatalogID(company.ID); err != nil {
			return fmt.Errorf("%s.id: %w", where, err)
		}
		if _, exists := companyIDs[company.ID]; exists {
			return fmt.Errorf("duplicate company id %q", company.ID)
		}
		companyIDs[company.ID] = struct{}{}
		if strings.TrimSpace(company.Name) == "" {
			return fmt.Errorf("%s.name is required", where)
		}
		if len(company.Sources) == 0 {
			return fmt.Errorf("%s.sources must contain at least one routine source", where)
		}
		for j, source := range company.Sources {
			sourceWhere := fmt.Sprintf("%s.sources[%d]", where, j)
			if err := validCatalogID(source.ID); err != nil {
				return fmt.Errorf("%s.id: %w", sourceWhere, err)
			}
			if _, exists := sourceIDs[source.ID]; exists {
				return fmt.Errorf("duplicate source id %q", source.ID)
			}
			sourceIDs[source.ID] = struct{}{}
			if _, supported := supportedProviders[source.Provider]; !supported {
				return fmt.Errorf("%s.provider %q is not supported for routine crawling", sourceWhere, source.Provider)
			}
			if err := ValidHTTPURL(source.URL); err != nil {
				return fmt.Errorf("%s.url: %w", sourceWhere, err)
			}
		}
	}
	return nil
}

// RoutineSources returns only sources from the allowlisted catalog, in stable
// company/source order. DiscoverySeed has no method that can enter this path.
func (c Catalog) RoutineSources() []Source {
	companies := append([]Company(nil), c.Companies...)
	sort.Slice(companies, func(i, j int) bool { return companies[i].ID < companies[j].ID })
	var sources []Source
	for _, company := range companies {
		companySources := append([]Source(nil), company.Sources...)
		sort.Slice(companySources, func(i, j int) bool { return companySources[i].ID < companySources[j].ID })
		for i := range companySources {
			companySources[i].Company = company.Name
		}
		sources = append(sources, companySources...)
	}
	return sources
}

func decodeStrictJSON(r io.Reader, dst any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return fmt.Errorf("read trailing JSON: %w", err)
	}
	return nil
}

func validCatalogID(id string) error {
	if !catalogIDPattern.MatchString(id) {
		return fmt.Errorf("must be a lowercase kebab-case id")
	}
	return nil
}

func ValidHTTPURL(raw string) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return fmt.Errorf("must be an absolute http(s) URL")
	}
	return nil
}

// ValidateDiscoverySource applies the same catalog boundary to a dynamically
// promoted source before persistence.
func ValidateDiscoverySource(source Source) error {
	if err := validCatalogID(strings.TrimSpace(source.ID)); err != nil {
		return fmt.Errorf("lite: discovered source id: %w", err)
	}
	if strings.TrimSpace(source.Company) == "" {
		return fmt.Errorf("lite: discovered source company is required")
	}
	provider := strings.TrimSpace(source.Provider)
	if _, supported := supportedProviders[provider]; !supported {
		return fmt.Errorf("lite: discovered provider %q is unsupported", provider)
	}
	if err := ValidHTTPURL(strings.TrimSpace(source.URL)); err != nil {
		return fmt.Errorf("lite: discovered source URL: %w", err)
	}
	return nil
}
