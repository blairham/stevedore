package sbomdiff

import (
	"strings"
	"testing"
)

const spdx = `{"packages":[
  {"name":"openssl","versionInfo":"1.1.1"},
  {"name":"zlib","versionInfo":"1.2.11"},
  {"name":"curl","versionInfo":"7.80.0"}
]}`

const cyclonedx = `{"components":[
  {"name":"openssl","version":"1.1.1"},
  {"name":"zlib","version":"1.2.11"}
]}`

func TestParseSPDX(t *testing.T) {
	pkgs, err := Packages([]byte(spdx), "spdx-json")
	if err != nil {
		t.Fatal(err)
	}
	if pkgs["openssl"] != "1.1.1" || pkgs["curl"] != "7.80.0" || len(pkgs) != 3 {
		t.Errorf("spdx parse = %v", pkgs)
	}
}

func TestParseCycloneDX(t *testing.T) {
	pkgs, err := Packages([]byte(cyclonedx), "cyclonedx-json")
	if err != nil {
		t.Fatal(err)
	}
	if pkgs["zlib"] != "1.2.11" || len(pkgs) != 2 {
		t.Errorf("cyclonedx parse = %v", pkgs)
	}
}

func TestPackagesFormatFallback(t *testing.T) {
	// Wrong hint should still parse via fallback.
	pkgs, err := Packages([]byte(spdx), "unknown-format")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 3 {
		t.Errorf("fallback parse = %v", pkgs)
	}
}

func TestDiff(t *testing.T) {
	old := map[string]string{"openssl": "1.1.1", "zlib": "1.2.11", "gone": "1.0"}
	newp := map[string]string{"openssl": "3.0.0", "zlib": "1.2.11", "added": "2.0"}
	r := Diff(old, newp)

	if len(r.Added) != 1 || r.Added[0].Name != "added" {
		t.Errorf("added = %v", r.Added)
	}
	if len(r.Removed) != 1 || r.Removed[0].Name != "gone" {
		t.Errorf("removed = %v", r.Removed)
	}
	if len(r.Changed) != 1 || r.Changed[0].Name != "openssl" || r.Changed[0].From != "1.1.1" || r.Changed[0].To != "3.0.0" {
		t.Errorf("changed = %v", r.Changed)
	}
	if r.Empty() {
		t.Error("result should not be empty")
	}
}

func TestDiffEmpty(t *testing.T) {
	same := map[string]string{"a": "1"}
	r := Diff(same, same)
	if !r.Empty() {
		t.Errorf("identical maps should diff empty: %+v", r)
	}
	if r.Markdown("h") != "" {
		t.Error("empty diff should render no markdown")
	}
}

func TestMarkdown(t *testing.T) {
	r := Diff(
		map[string]string{"openssl": "1.1.1", "gone": "1.0"},
		map[string]string{"openssl": "3.0.0", "new": "2.0"},
	)
	md := r.Markdown("myimage (since v1.0.0)")
	for _, want := range []string{
		"### myimage (since v1.0.0)",
		"**Upgraded**",
		"openssl: 1.1.1 → 3.0.0",
		"**Added**",
		"new 2.0",
		"**Removed**",
		"gone 1.0",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestDiffEndToEnd(t *testing.T) {
	// Parse two SBOMs and diff them, exercising the whole path.
	oldPkgs, _ := Packages([]byte(cyclonedx), "cyclonedx-json") // openssl 1.1.1, zlib
	newPkgs, _ := Packages([]byte(spdx), "spdx-json")           // openssl 1.1.1, zlib, curl
	r := Diff(oldPkgs, newPkgs)
	if len(r.Added) != 1 || r.Added[0].Name != "curl" {
		t.Errorf("expected curl added, got %v", r.Added)
	}
	if !r.Empty() && len(r.Changed) != 0 {
		t.Errorf("openssl/zlib unchanged, got changed=%v", r.Changed)
	}
}
