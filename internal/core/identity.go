package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var trackingQueryKeys = map[string]struct{}{
	"campaign": {}, "fbclid": {}, "gclid": {}, "gh_jid": {}, "gh_src": {}, "mc_cid": {},
	"lever-source": {}, "lever-via": {}, "mc_eid": {}, "ref": {}, "referrer": {}, "source": {}, "src": {},
	"tracking": {},
}

var requisitionUUIDPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func canonicalText(value string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(strings.TrimSpace(value)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}), " ")
}

// sameCompanyIdentity tolerates presentation-only spacing differences emitted
// by discovery providers (for example "Citadel Securities" versus
// "Citadelsecurities") without weakening the cross-company URL guard.
func sameCompanyIdentity(left, right string) bool {
	compact := func(value string) string {
		return strings.ReplaceAll(canonicalText(value), " ", "")
	}
	left, right = compact(left), compact(right)
	return left != "" && left == right
}

// CanonicalApplyURL removes fragments and common acquisition/tracking params,
// normalizes the host/path, and orders meaningful query parameters.
func CanonicalApplyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	if parsed.User != nil {
		return ""
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	hostname := strings.ToLower(parsed.Hostname())
	// Greenhouse serves the same requisition through both historical board
	// hosts, and often repeats the path job ID in gh_jid. Treat those transport
	// variants as one strong apply identity so verified and newly discovered
	// boards cannot create duplicate openings.
	if hostname == "boards.greenhouse.io" || hostname == "job-boards.greenhouse.io" {
		hostname = "job-boards.greenhouse.io"
		parsed.Scheme = "https"
	}
	port := parsed.Port()
	if (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		port = ""
	}
	parsed.Host = hostname
	if port != "" {
		parsed.Host += ":" + port
	}
	parsed.Fragment = ""
	cleanPath := strings.TrimSpace(parsed.EscapedPath())
	if cleanPath == "" {
		cleanPath = "/"
	}
	// A single trailing slash is routine presentation noise. Keep every other
	// path byte—including duplicate slashes and dot segments—because an origin
	// is allowed to assign those paths different requisitions.
	if len(cleanPath) > 1 {
		cleanPath = strings.TrimSuffix(cleanPath, "/")
	}
	cleanPath = uppercasePercentEscapes(cleanPath)
	decodedPath, err := url.PathUnescape(cleanPath)
	if err != nil {
		return ""
	}
	parsed.Path = decodedPath
	// Retaining RawPath is essential: reserved escapes such as %2F carry path
	// segment identity and must not collapse into a literal slash.
	parsed.RawPath = cleanPath

	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		_, exact := trackingQueryKeys[lower]
		if exact || strings.HasPrefix(lower, "utm_") {
			query.Del(key)
			continue
		}
		sort.Strings(query[key])
	}
	parsed.RawQuery = query.Encode()
	return strings.TrimSuffix(parsed.String(), "/")
}

func uppercasePercentEscapes(value string) string {
	bytes := []byte(value)
	for index := 0; index+2 < len(bytes); index++ {
		if bytes[index] != '%' {
			continue
		}
		if bytes[index+1] >= 'a' && bytes[index+1] <= 'f' {
			bytes[index+1] -= 'a' - 'A'
		}
		if bytes[index+2] >= 'a' && bytes[index+2] <= 'f' {
			bytes[index+2] -= 'a' - 'A'
		}
		index += 2
	}
	return string(bytes)
}

func identityKeys(observation Observation) ([]string, error) {
	company := canonicalText(observation.Company)
	title := canonicalText(observation.Title)
	location := canonicalText(observation.Location)
	if company == "" || title == "" {
		return nil, errors.New("lite: company and title are required")
	}

	keys := make([]string, 0, 3)
	if sourceID, nativeID := canonicalText(observation.SourceID), strings.TrimSpace(observation.SourceNativeID); sourceID != "" && nativeID != "" {
		keys = append(keys, "native:"+sourceID+":"+nativeID)
	}
	if applyURL := CanonicalApplyURL(observation.ApplyURL); applyURL != "" {
		if uuid := strings.ToLower(requisitionUUIDPattern.FindString(applyURL)); uuid != "" {
			keys = append(keys, "requisition:"+company+":"+uuid)
		}
		keys = append(keys, "url:"+applyURL)
	}
	// A company/title/location fingerprint is intentionally weak: companies
	// regularly publish multiple distinct requisitions with the same generic
	// title and location. Only use it when the source supplied neither strong
	// native identity nor a valid apply URL.
	if len(keys) == 0 {
		keys = append(keys, "posting:"+company+"|"+title+"|"+location)
	}
	return keys, nil
}

func stablePostingID(keys []string) string {
	seed := keys[0]
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:16])
}
