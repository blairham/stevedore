package summary

import (
	"encoding/json"
	"strings"
	"testing"
)

func demo() Result {
	return Result{
		Project: "acme",
		Images: []Image{
			{ID: "checkout", Version: "0.0.336", Digest: "sha256:abcdef0123456789", Signed: true, SBOM: true, Provenance: true, Tested: true, Vulns: map[string]int{"high": 2, "low": 5}},
			{ID: "reconciler", Skipped: true},
		},
	}
}

func TestJSON(t *testing.T) {
	data, err := demo().JSON()
	if err != nil {
		t.Fatal(err)
	}
	var back Result
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if back.Project != "acme" || len(back.Images) != 2 {
		t.Errorf("round-trip mismatch: %+v", back)
	}
	if back.Images[0].Version != "0.0.336" || !back.Images[0].Signed {
		t.Errorf("image fields lost: %+v", back.Images[0])
	}
}

func TestMarkdown(t *testing.T) {
	md := demo().Markdown()
	for _, want := range []string{
		"## stevedore release — acme",
		"`checkout`",
		"0.0.336",
		"abcdef012345", // digest truncated to 12
		"2 high, 5 low",
		"_skipped (unchanged)_",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestVulnCell(t *testing.T) {
	if got := vulnCell(nil); got != "—" {
		t.Errorf("empty vulns = %q", got)
	}
	if got := vulnCell(map[string]int{"critical": 1, "medium": 3}); got != "1 critical, 3 medium" {
		t.Errorf("vulnCell = %q (want most-severe first)", got)
	}
}

func TestShortDigest(t *testing.T) {
	if got := shortDigest("sha256:0123456789abcdef"); got != "0123456789ab" {
		t.Errorf("shortDigest = %q", got)
	}
	if got := shortDigest(""); got != "—" {
		t.Errorf("empty digest = %q", got)
	}
}
