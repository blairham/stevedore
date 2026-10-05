// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package scanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
)

const grypeJSON = `{
  "matches": [
    {"vulnerability": {"id": "CVE-1", "severity": "Critical"}, "artifact": {"name": "openssl", "version": "1.0"}},
    {"vulnerability": {"id": "CVE-2", "severity": "High"},     "artifact": {"name": "curl", "version": "7.0"}},
    {"vulnerability": {"id": "CVE-3", "severity": "Low"},      "artifact": {"name": "zlib", "version": "1.2"}}
  ]
}`

const trivyJSON = `{
  "SchemaVersion": 2,
  "Results": [
    {"Vulnerabilities": [
      {"VulnerabilityID": "CVE-1", "Severity": "CRITICAL", "PkgName": "openssl", "InstalledVersion": "1.0"},
      {"VulnerabilityID": "CVE-3", "Severity": "LOW", "PkgName": "zlib", "InstalledVersion": "1.2"}
    ]}
  ]
}`

func TestParseGrype(t *testing.T) {
	vulns, err := parse("grype", []byte(grypeJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(vulns) != 3 {
		t.Fatalf("want 3 vulns, got %d", len(vulns))
	}
	if vulns[0].ID != "CVE-1" || vulns[0].Severity != "critical" || vulns[0].Package != "openssl" {
		t.Errorf("unexpected first vuln: %+v", vulns[0])
	}
}

func TestParseTrivy(t *testing.T) {
	vulns, err := parse("trivy", []byte(trivyJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(vulns) != 2 {
		t.Fatalf("want 2 vulns, got %d", len(vulns))
	}
	if vulns[0].Severity != "critical" || vulns[1].Severity != "low" {
		t.Errorf("severities not normalized: %+v", vulns)
	}
}

// The fixtures in testdata are real scanner output, captured from trivy 0.75.0
// and grype 0.120.0 (local paths rewritten). The clean ones are the shape a
// vulnerability-free image produces; the sarif/cyclonedx ones are what the
// scanner prints when scan.args overrides the output format.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A clean scan must parse as zero vulnerabilities, not as an error: trivy
// omits "Results" entirely for a clean target and grype emits "matches": [].
func TestParseCleanReports(t *testing.T) {
	cases := []struct{ scanner, file string }{
		{"trivy", "trivy-clean-image.json"},
		{"trivy", "trivy-clean-fs.json"},
		{"grype", "grype-clean.json"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			vulns, err := parse(tc.scanner, fixture(t, tc.file))
			if err != nil {
				t.Fatalf("parse(%s) = %v, want a clean report", tc.file, err)
			}
			if len(vulns) != 0 {
				t.Fatalf("parse(%s) = %d vulns, want 0", tc.file, len(vulns))
			}
		})
	}
}

// Valid JSON that is not the scanner's report must fail, never read as clean:
// an empty report passes the gate and the image gets signed (#37).
func TestParseRejectsForeignDocuments(t *testing.T) {
	cases := []struct {
		name, scanner string
		data          []byte
	}{
		{"trivy sarif", "trivy", fixture(t, "trivy-sarif.json")},
		{"trivy cyclonedx", "trivy", fixture(t, "trivy-cyclonedx.json")},
		{"grype sarif", "grype", fixture(t, "grype-sarif.json")},
		{"grype cyclonedx", "grype", fixture(t, "grype-cyclonedx.json")},
		{"grype report fed to trivy", "trivy", fixture(t, "grype-clean.json")},
		{"trivy report fed to grype", "grype", fixture(t, "trivy-clean-image.json")},
		{"trivy empty object", "trivy", []byte(`{}`)},
		{"grype empty object", "grype", []byte(`{}`)},
		{"grype null matches", "grype", []byte(`{"matches": null}`)},
		{"trivy future schema", "trivy", []byte(`{"SchemaVersion": 3, "Findings": []}`)},
		{"trivy json array", "trivy", []byte(`[]`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vulns, err := parse(tc.scanner, tc.data)
			if err == nil {
				t.Fatalf("parse = %d vulns and no error, want an error", len(vulns))
			}
		})
	}
}

func TestBuildReportGating(t *testing.T) {
	vulns, _ := parse("grype", []byte(grypeJSON))

	// fail_on high -> critical + high block, low does not.
	rep := buildReport(config.Scan{FailOn: "high"}, "ref", vulns)
	if len(rep.Blocking) != 2 {
		t.Errorf("fail_on=high should block 2 (critical+high), got %d: %+v", len(rep.Blocking), rep.Blocking)
	}
	if rep.GateError("high") == nil {
		t.Error("expected gate error at fail_on=high")
	}

	// fail_on critical -> only critical blocks.
	rep = buildReport(config.Scan{FailOn: "critical"}, "ref", vulns)
	if len(rep.Blocking) != 1 || rep.Blocking[0].ID != "CVE-1" {
		t.Errorf("fail_on=critical should block only CVE-1, got %+v", rep.Blocking)
	}

	// no threshold -> report only, never blocks.
	rep = buildReport(config.Scan{FailOn: ""}, "ref", vulns)
	if len(rep.Blocking) != 0 {
		t.Errorf("empty fail_on should not block, got %+v", rep.Blocking)
	}
	if rep.GateError("") != nil {
		t.Error("no threshold should yield no gate error")
	}
}

func TestBuildReportIgnore(t *testing.T) {
	vulns, _ := parse("grype", []byte(grypeJSON))
	// Ignore the critical (case-insensitive) so it neither counts nor blocks.
	rep := buildReport(config.Scan{FailOn: "high", Ignore: []string{"cve-1"}}, "ref", vulns)
	if rep.Counts["critical"] != 0 {
		t.Errorf("ignored CVE-1 should not be counted: %+v", rep.Counts)
	}
	if len(rep.Blocking) != 1 || rep.Blocking[0].ID != "CVE-2" {
		t.Errorf("only CVE-2 (high) should block after ignoring CVE-1, got %+v", rep.Blocking)
	}
}

func TestSummary(t *testing.T) {
	vulns, _ := parse("grype", []byte(grypeJSON))
	rep := buildReport(config.Scan{FailOn: "critical"}, "ref", vulns)
	got := rep.Summary()
	// Most severe first.
	if got != "1 critical, 1 high, 1 low" {
		t.Errorf("Summary() = %q", got)
	}

	empty := buildReport(config.Scan{}, "ref", nil)
	if empty.Summary() != "no vulnerabilities found" {
		t.Errorf("empty Summary() = %q", empty.Summary())
	}
}

func TestGateErrorMentionsSeverityAndCVE(t *testing.T) {
	vulns, _ := parse("grype", []byte(grypeJSON))
	rep := buildReport(config.Scan{FailOn: "critical"}, "myimg@sha256:abc", vulns)
	msg := rep.GateError("critical").Error()
	for _, want := range []string{"CRITICAL", "CVE-1", "myimg@sha256:abc"} {
		if !strings.Contains(msg, want) {
			t.Errorf("gate error missing %q: %s", want, msg)
		}
	}
}

func TestCommand(t *testing.T) {
	name, args, raw := command(config.Scan{Scanner: "grype"}, "img:tag", "dist", "app")
	if name != "grype" || args[0] != "img:tag" {
		t.Errorf("grype command = %s %v", name, args)
	}
	if raw != "dist/scan-app.json" {
		t.Errorf("raw path = %q", raw)
	}

	name, args, _ = command(config.Scan{Scanner: "trivy"}, "img:tag", "dist", "app")
	if name != "trivy" || args[0] != "image" {
		t.Errorf("trivy command = %s %v", name, args)
	}
	if args[len(args)-1] != "img:tag" {
		t.Errorf("trivy ref should be last: %v", args)
	}
}
