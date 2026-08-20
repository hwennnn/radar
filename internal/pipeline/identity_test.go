package pipeline

import (
	"reflect"
	"testing"
)

func TestCanonicalApplyURLStripsTrackingAndSortsMeaningfulQuery(t *testing.T) {
	got := CanonicalApplyURL("HTTPS://Jobs.Example.com:443/jobs/42/?utm_source=board&b=2&a=1&source=linkedin&lever-source=board&lever-via=feed#apply")
	want := "https://jobs.example.com/jobs/42?a=1&b=2"
	if got != want {
		t.Fatalf("CanonicalApplyURL() = %q, want %q", got, want)
	}
}

func TestCanonicalApplyURLPreservesReservedPathEscapes(t *testing.T) {
	escaped := CanonicalApplyURL("https://jobs.example/jobs/a%2fb?utm_source=feed")
	literal := CanonicalApplyURL("https://jobs.example/jobs/a/b")
	if escaped != "https://jobs.example/jobs/a%2Fb" {
		t.Fatalf("escaped URL = %q", escaped)
	}
	if escaped == literal {
		t.Fatalf("reserved escaped path %q collapsed into literal path %q", escaped, literal)
	}
}

func TestCanonicalApplyURLDoesNotCollapseDistinctPathShapes(t *testing.T) {
	doubleSlash := CanonicalApplyURL("https://jobs.example/jobs//req-42")
	singleSlash := CanonicalApplyURL("https://jobs.example/jobs/req-42")
	if doubleSlash == singleSlash {
		t.Fatalf("distinct origin paths collapsed to %q", doubleSlash)
	}
}

func TestCanonicalApplyURLConvergesGreenhouseBoardVariants(t *testing.T) {
	legacy := CanonicalApplyURL("http://boards.greenhouse.io/neuralink/jobs/5469298003?gh_jid=5469298003")
	current := CanonicalApplyURL("https://job-boards.greenhouse.io/neuralink/jobs/5469298003?gh_jid=")
	if legacy != current || current != "https://job-boards.greenhouse.io/neuralink/jobs/5469298003" {
		t.Fatalf("greenhouse identities did not converge: legacy=%q current=%q", legacy, current)
	}
}

func TestIdentityKeysPreferStrongNativeAndURLAliases(t *testing.T) {
	got, err := IdentityKeys(Observation{
		SourceID: "Greenhouse", SourceNativeID: "ABC-42",
		Company: " Example, Inc. ", Title: "Software Engineer — New Grad", Location: "New York, NY",
		ApplyURL: "https://jobs.example/job/42?gh_src=feed",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"native:greenhouse:ABC-42",
		"url:https://jobs.example/job/42",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IdentityKeys() = %#v, want %#v", got, want)
	}
}

func TestIdentityKeysPreserveOpaqueNativeIDCasing(t *testing.T) {
	upper, err := IdentityKeys(Observation{SourceID: "ATS", SourceNativeID: "Req-Aa", Company: "Acme", Title: "Engineer"})
	if err != nil {
		t.Fatal(err)
	}
	lower, err := IdentityKeys(Observation{SourceID: "ATS", SourceNativeID: "req-aa", Company: "Acme", Title: "Engineer"})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(upper, lower) {
		t.Fatalf("case-distinct opaque native IDs collapsed: %v", upper)
	}
}

func TestIdentityKeysShareCompanyScopedRequisitionUUIDAcrossDomains(t *testing.T) {
	const uuid = "6cdb0f39-234a-4234-b1f1-cb48a1fa2795"
	branded, err := IdentityKeys(Observation{
		SourceID: "market", SourceNativeID: "branded", Company: "Airwallex", Title: "Software Engineer Intern",
		ApplyURL: "https://careers.airwallex.com/job/" + uuid + "/software-engineer-intern",
	})
	if err != nil {
		t.Fatal(err)
	}
	ats, err := IdentityKeys(Observation{
		SourceID: "ashby", SourceNativeID: "ats", Company: "Airwallex", Title: "Software Engineer Intern",
		ApplyURL: "https://jobs.ashbyhq.com/airwallex/" + uuid + "/application",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "requisition:airwallex:" + uuid
	if !containsString(branded, want) || !containsString(ats, want) {
		t.Fatalf("shared requisition identity missing: branded=%#v ats=%#v", branded, ats)
	}
	other, err := IdentityKeys(Observation{
		SourceID: "ashby", SourceNativeID: "other", Company: "Different Company", Title: "Software Engineer Intern",
		ApplyURL: "https://jobs.ashbyhq.com/different/" + uuid + "/application",
	})
	if err != nil {
		t.Fatal(err)
	}
	if containsString(other, want) {
		t.Fatalf("requisition UUID crossed company boundary: %#v", other)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestIdentityKeysUseFingerprintOnlyWithoutStrongIdentity(t *testing.T) {
	got, err := IdentityKeys(Observation{Company: "Example, Inc.", Title: "Software Engineer", Location: "Remote"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"posting:example inc|software engineer|remote"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IdentityKeys() = %#v, want %#v", got, want)
	}
}

func TestIdentityKeysRequireCompanyAndTitle(t *testing.T) {
	if _, err := IdentityKeys(Observation{Company: "Example"}); err == nil {
		t.Fatal("expected missing title to fail")
	}
}

func TestStablePostingIDUsesCanonicalURLWhenItIsOnlyStrongIdentity(t *testing.T) {
	one, _ := IdentityKeys(Observation{Company: "Acme", Title: "Engineer", Location: "Remote", ApplyURL: "https://jobs.acme.test/42?utm_source=one"})
	two, _ := IdentityKeys(Observation{Company: "ACME", Title: "Engineer", Location: "remote", ApplyURL: "https://jobs.acme.test/42?source=two"})
	if StablePostingID(one) != StablePostingID(two) {
		t.Fatal("tracking variants of the same apply URL should produce the same posting ID")
	}
}
