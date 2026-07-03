// Package sbomdiff compares two SBOMs (SPDX or CycloneDX JSON) and renders the
// added, removed, and upgraded packages as a Markdown section for the changelog.
package sbomdiff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Packages extracts a package-name -> version map from an SBOM. The format is
// the syft format name (spdx-json, cyclonedx-json, ...); parsing is chosen by
// inspecting the document shape, so the format arg is only a hint.
func Packages(data []byte, format string) (map[string]string, error) {
	if strings.HasPrefix(format, "cyclonedx") {
		return parseCycloneDX(data)
	}
	if strings.HasPrefix(format, "spdx") {
		return parseSPDX(data)
	}
	// Unknown hint: try SPDX, then CycloneDX.
	if pkgs, err := parseSPDX(data); err == nil && len(pkgs) > 0 {
		return pkgs, nil
	}
	return parseCycloneDX(data)
}

func parseSPDX(data []byte) (map[string]string, error) {
	var doc struct {
		Packages []struct {
			Name        string `json:"name"`
			VersionInfo string `json:"versionInfo"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse spdx sbom: %w", err)
	}
	out := make(map[string]string, len(doc.Packages))
	for _, p := range doc.Packages {
		if p.Name != "" {
			out[p.Name] = p.VersionInfo
		}
	}
	return out, nil
}

func parseCycloneDX(data []byte) (map[string]string, error) {
	var doc struct {
		Components []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"components"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse cyclonedx sbom: %w", err)
	}
	out := make(map[string]string, len(doc.Components))
	for _, c := range doc.Components {
		if c.Name != "" {
			out[c.Name] = c.Version
		}
	}
	return out, nil
}

// Pkg is a name/version pair.
type Pkg struct {
	Name    string
	Version string
}

// Change is a version change for a package present in both SBOMs.
type Change struct {
	Name string
	From string
	To   string
}

// Result holds the differences between two SBOMs.
type Result struct {
	Added   []Pkg
	Removed []Pkg
	Changed []Change
}

// Diff compares two package maps (old -> new).
func Diff(old, new map[string]string) Result {
	var r Result
	for name, nv := range new {
		ov, ok := old[name]
		if !ok {
			r.Added = append(r.Added, Pkg{name, nv})
		} else if ov != nv {
			r.Changed = append(r.Changed, Change{name, ov, nv})
		}
	}
	for name, ov := range old {
		if _, ok := new[name]; !ok {
			r.Removed = append(r.Removed, Pkg{name, ov})
		}
	}
	sort.Slice(r.Added, func(i, j int) bool { return r.Added[i].Name < r.Added[j].Name })
	sort.Slice(r.Removed, func(i, j int) bool { return r.Removed[i].Name < r.Removed[j].Name })
	sort.Slice(r.Changed, func(i, j int) bool { return r.Changed[i].Name < r.Changed[j].Name })
	return r
}

// Empty reports whether there are no differences.
func (r Result) Empty() bool {
	return len(r.Added) == 0 && len(r.Removed) == 0 && len(r.Changed) == 0
}

// Markdown renders the diff as a changelog section titled with heading. Returns
// "" when there are no changes.
func (r Result) Markdown(heading string) string {
	if r.Empty() {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n\n", heading)
	if len(r.Changed) > 0 {
		b.WriteString("**Upgraded**\n\n")
		for _, c := range r.Changed {
			fmt.Fprintf(&b, "- %s: %s → %s\n", c.Name, orNone(c.From), orNone(c.To))
		}
		b.WriteString("\n")
	}
	if len(r.Added) > 0 {
		b.WriteString("**Added**\n\n")
		for _, p := range r.Added {
			fmt.Fprintf(&b, "- %s %s\n", p.Name, orNone(p.Version))
		}
		b.WriteString("\n")
	}
	if len(r.Removed) > 0 {
		b.WriteString("**Removed**\n\n")
		for _, p := range r.Removed {
			fmt.Fprintf(&b, "- %s %s\n", p.Name, orNone(p.Version))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
