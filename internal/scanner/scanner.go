// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package scanner runs a vulnerability scanner (grype or trivy) against a built
// image and, when configured, gates the release on a severity threshold.
package scanner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

// severityRank maps a normalized severity to an ordinal for threshold
// comparison. Unknown severities rank 0 and never trip the gate.
var severityRank = map[string]int{
	"negligible": 1,
	"low":        2,
	"medium":     3,
	"high":       4,
	"critical":   5,
}

// Vuln is a single reported vulnerability, normalized across scanners.
type Vuln struct {
	ID       string
	Severity string // lowercase normalized
	Package  string
	Version  string
}

// Report summarizes a scan.
type Report struct {
	Scanner string
	Ref     string
	Vulns   []Vuln
	// Counts is severity -> number of vulns (after ignores applied).
	Counts map[string]int
	// Blocking are the vulns at or above the fail_on threshold.
	Blocking []Vuln
}

// trivy is both the scanner name in config and its executable.
const trivy = "trivy"

// Scan scans ref with the configured scanner, writes the raw report to distDir,
// and returns a normalized Report. In dry-run mode, or with scanning disabled,
// it returns an empty report; dry-run also echoes the command.
func Scan(r *run.Runner, cfg config.Scan, distDir, imageID, ref string) (*Report, error) {
	if !cfg.Enabled {
		return &Report{Scanner: cfg.Scanner, Ref: ref, Counts: map[string]int{}}, nil
	}
	name, args, raw := command(cfg, ref, distDir, imageID)
	if r.DryRun {
		r.Preview(name, args...)
		return &Report{Scanner: cfg.Scanner, Ref: ref, Counts: map[string]int{}}, nil
	}
	out, err := r.Capture(name, args...)
	if err != nil {
		return nil, fmt.Errorf("%s scan of %s: %w", cfg.Scanner, ref, err)
	}
	if raw != "" {
		if werr := os.WriteFile(raw, []byte(out), 0o644); werr != nil { //nolint:gosec // G306: dist/ is read by non-owners (see pipeline.mkdirDist)
			return nil, fmt.Errorf("write scan report: %w", werr)
		}
	}

	vulns, err := parse(cfg.Scanner, []byte(out))
	if err != nil {
		return nil, err
	}
	return buildReport(cfg, ref, vulns), nil
}

// command returns the scanner invocation and the path its JSON is saved to.
func command(cfg config.Scan, ref, distDir, imageID string) (name string, args []string, rawPath string) {
	rawPath = filepath.Join(distDir, fmt.Sprintf("scan-%s.json", imageID))
	switch cfg.Scanner {
	case trivy:
		args = append([]string{"image", "--quiet", "--format", "json"}, cfg.Args...)
		return trivy, append(args, ref), rawPath
	default: // grype
		args = append([]string{ref, "-o", "json"}, cfg.Args...)
		return "grype", args, rawPath
	}
}

// buildReport applies ignores, tallies counts, and computes the blocking set.
func buildReport(cfg config.Scan, ref string, vulns []Vuln) *Report {
	ignore := map[string]bool{}
	for _, id := range cfg.Ignore {
		ignore[strings.ToUpper(id)] = true
	}
	rep := &Report{Scanner: cfg.Scanner, Ref: ref, Counts: map[string]int{}}
	threshold := severityRank[cfg.FailOn] // 0 when FailOn is empty -> no gate
	for _, v := range vulns {
		if ignore[strings.ToUpper(v.ID)] {
			continue
		}
		rep.Vulns = append(rep.Vulns, v)
		rep.Counts[v.Severity]++
		if threshold > 0 && severityRank[v.Severity] >= threshold {
			rep.Blocking = append(rep.Blocking, v)
		}
	}
	return rep
}

// Summary renders a one-line severity tally, most severe first.
func (rep *Report) Summary() string {
	if len(rep.Vulns) == 0 {
		return "no vulnerabilities found"
	}
	order := []string{"critical", "high", "medium", "low", "negligible"}
	var parts []string
	for _, s := range order {
		if n := rep.Counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	return strings.Join(parts, ", ")
}

// GateError returns a non-nil error naming the blocking vulnerabilities when the
// report trips the configured threshold.
func (rep *Report) GateError(failOn string) error {
	if len(rep.Blocking) == 0 {
		return nil
	}
	sort.Slice(rep.Blocking, func(i, j int) bool {
		return severityRank[rep.Blocking[i].Severity] > severityRank[rep.Blocking[j].Severity]
	})
	var b strings.Builder
	fmt.Fprintf(&b, "%d vulnerabilit%s at or above %q in %s:", len(rep.Blocking), plural(len(rep.Blocking)), failOn, rep.Ref)
	shown := rep.Blocking
	const maxShown = 20
	if len(shown) > maxShown {
		shown = shown[:maxShown]
	}
	for _, v := range shown {
		fmt.Fprintf(&b, "\n  - [%s] %s (%s %s)", strings.ToUpper(v.Severity), v.ID, v.Package, v.Version)
	}
	if len(rep.Blocking) > maxShown {
		fmt.Fprintf(&b, "\n  ... and %d more", len(rep.Blocking)-maxShown)
	}
	return fmt.Errorf("%s", b.String())
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// --- scanner-specific JSON parsing ---

func parse(scanner string, data []byte) ([]Vuln, error) {
	switch scanner {
	case trivy:
		return parseTrivy(data)
	default:
		return parseGrype(data)
	}
}

// requireKeys fails unless data is a JSON object carrying every key in keys
// with a non-null value. Decoding into a struct alone cannot tell "the scanner
// found nothing" from "this is not the scanner's JSON report at all": a SARIF
// or CycloneDX document (an overridden output format) or a future schema
// change unmarshals cleanly into an empty struct, and an empty report passes
// the gate. Requiring the report's identifying keys makes that fail closed.
func requireKeys(scanner string, data []byte, keys ...string) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return fmt.Errorf("parse %s output: %w", scanner, err)
	}
	for _, k := range keys {
		v, ok := top[k]
		if !ok || string(v) == "null" {
			return fmt.Errorf("parse %s output: not a %s JSON report (no %q key); is the output format overridden in scan.args?", scanner, scanner, k)
		}
	}
	return nil
}

func parseGrype(data []byte) ([]Vuln, error) {
	// grype always emits "matches", as [] when the image is clean.
	if err := requireKeys("grype", data, "matches"); err != nil {
		return nil, err
	}
	var doc struct {
		Matches []struct {
			Vulnerability struct {
				ID       string `json:"id"`
				Severity string `json:"severity"`
			} `json:"vulnerability"`
			Artifact struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"artifact"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse grype output: %w", err)
	}
	vulns := make([]Vuln, 0, len(doc.Matches))
	for _, m := range doc.Matches {
		vulns = append(vulns, Vuln{
			ID:       m.Vulnerability.ID,
			Severity: strings.ToLower(m.Vulnerability.Severity),
			Package:  m.Artifact.Name,
			Version:  m.Artifact.Version,
		})
	}
	return vulns, nil
}

// trivySchemaVersion is the trivy JSON report schema this parser understands.
const trivySchemaVersion = 2

func parseTrivy(data []byte) ([]Vuln, error) {
	// trivy omits "Results" entirely for a clean image, so its presence cannot
	// be required; "SchemaVersion" is always present and identifies the report.
	if err := requireKeys(trivy, data, "SchemaVersion"); err != nil {
		return nil, err
	}
	var doc struct {
		SchemaVersion int `json:"SchemaVersion"`
		Results       []struct {
			Vulnerabilities []struct {
				VulnerabilityID  string `json:"VulnerabilityID"`
				Severity         string `json:"Severity"`
				PkgName          string `json:"PkgName"`
				InstalledVersion string `json:"InstalledVersion"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse trivy output: %w", err)
	}
	if doc.SchemaVersion != trivySchemaVersion {
		return nil, fmt.Errorf("parse trivy output: unsupported SchemaVersion %d (want %d)", doc.SchemaVersion, trivySchemaVersion)
	}
	var vulns []Vuln
	for _, r := range doc.Results {
		for _, v := range r.Vulnerabilities {
			vulns = append(vulns, Vuln{
				ID:       v.VulnerabilityID,
				Severity: strings.ToLower(v.Severity),
				Package:  v.PkgName,
				Version:  v.InstalledVersion,
			})
		}
	}
	return vulns, nil
}
