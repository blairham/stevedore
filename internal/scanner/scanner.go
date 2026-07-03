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

// Scan scans ref with the configured scanner, writes the raw report to distDir,
// and returns a normalized Report. In dry-run mode it echoes the command and
// returns an empty report.
func Scan(r *run.Runner, cfg config.Scan, distDir, imageID, ref string) (*Report, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	name, args, raw := command(cfg, ref, distDir, imageID)
	if r.DryRun {
		_ = r.Run(name, args...)
		return &Report{Scanner: cfg.Scanner, Ref: ref, Counts: map[string]int{}}, nil
	}
	out, err := r.Capture(name, args...)
	if err != nil {
		return nil, fmt.Errorf("%s scan of %s: %w", cfg.Scanner, ref, err)
	}
	if raw != "" {
		if werr := os.WriteFile(raw, []byte(out), 0o644); werr != nil {
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
	case "trivy":
		args = append([]string{"image", "--quiet", "--format", "json"}, cfg.Args...)
		return "trivy", append(args, ref), rawPath
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
	const max = 20
	if len(shown) > max {
		shown = shown[:max]
	}
	for _, v := range shown {
		fmt.Fprintf(&b, "\n  - [%s] %s (%s %s)", strings.ToUpper(v.Severity), v.ID, v.Package, v.Version)
	}
	if len(rep.Blocking) > max {
		fmt.Fprintf(&b, "\n  ... and %d more", len(rep.Blocking)-max)
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
	case "trivy":
		return parseTrivy(data)
	default:
		return parseGrype(data)
	}
}

func parseGrype(data []byte) ([]Vuln, error) {
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

func parseTrivy(data []byte) ([]Vuln, error) {
	var doc struct {
		Results []struct {
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
