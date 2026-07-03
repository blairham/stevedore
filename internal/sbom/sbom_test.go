package sbom

import "testing"

func TestPredicateType(t *testing.T) {
	cases := map[string]string{
		"cyclonedx-json": "cyclonedx",
		"cyclonedx":      "cyclonedx",
		"spdx-json":      "spdxjson",
		"":               "spdxjson",
		"anything-else":  "spdxjson",
	}
	for format, want := range cases {
		if got := PredicateType(format); got != want {
			t.Errorf("PredicateType(%q) = %q, want %q", format, got, want)
		}
	}
}
