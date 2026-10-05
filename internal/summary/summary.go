// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package summary builds the machine- and human-readable report of a release:
// a JSON document, and a Markdown table for the GitHub Actions job summary.
package summary

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Image is one image's outcome in a release.
type Image struct {
	ID      string   `json:"id"`
	Version string   `json:"version,omitempty"`
	Digest  string   `json:"digest,omitempty"`
	Refs    []string `json:"refs,omitempty"`
	// DigestRefs are repository@digest for every repository, set for an image
	// whose digest is a published one (pushed by this run, or already released
	// from this commit) — what a GitOps consumer pins to.
	DigestRefs []string `json:"digest_refs,omitempty"`
	// Repositories are the bare repos (no tag) this image publishes to.
	Repositories []string `json:"repositories,omitempty"`
	// Pushed reports whether the refs were actually published (false under
	// --no-push validation builds) — notification consumers key off this.
	Pushed bool `json:"pushed"`
	// Reason says why the image built ("src/… since its release marker") or
	// why it was skipped ("inputs unchanged").
	Reason  string `json:"reason,omitempty"`
	Skipped bool   `json:"skipped"`
	// AlreadyReleased marks a skipped image whose commit tags already exist
	// from this commit: an earlier run released it, so nothing was pushed but
	// its release marker still advances.
	AlreadyReleased bool `json:"already_released,omitempty"`
	Signed          bool `json:"signed"`
	SBOM            bool `json:"sbom"`
	Provenance      bool `json:"provenance"`
	Tested          bool `json:"tested"`
	// Vulns counts the distinct findings across every scanned platform.
	Vulns map[string]int `json:"vulns,omitempty"`
	// Platforms records the gates per platform of the image, so a variant that
	// was not smoke tested is visible rather than hidden behind Tested.
	Platforms []Platform `json:"platforms,omitempty"`
}

// Platform is one platform's gate results.
type Platform struct {
	Platform string         `json:"platform"`
	Vulns    map[string]int `json:"vulns,omitempty"`
	Scanned  bool           `json:"scanned"`
	Tested   bool           `json:"tested"`
	// TestSkipped is why the smoke test did not run on this platform.
	TestSkipped string `json:"test_skipped,omitempty"`
	// SBOM is the path of this platform's SBOM, when one was generated.
	SBOM string `json:"sbom,omitempty"`
}

// PlatformEntry returns the entry for platform, appending one if absent.
func (img *Image) PlatformEntry(platform string) *Platform {
	for i := range img.Platforms {
		if img.Platforms[i].Platform == platform {
			return &img.Platforms[i]
		}
	}
	img.Platforms = append(img.Platforms, Platform{Platform: platform})
	return &img.Platforms[len(img.Platforms)-1]
}

// Result is the whole release outcome.
type Result struct {
	Project  string `json:"project"`
	Snapshot bool   `json:"snapshot"`
	// Degraded names the stages (scan, test, sign, sbom) a real release ran
	// without — skipped by flag or disabled in the config.
	Degraded []string `json:"degraded,omitempty"`
	Images   []Image  `json:"images"`
}

// JSON renders the result as indented JSON.
func (r Result) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Pinned returns the images that have DigestRefs, in order.
func (r Result) Pinned() []Image {
	var out []Image
	for _, img := range r.Images {
		if len(img.DigestRefs) > 0 {
			out = append(out, img)
		}
	}
	return out
}

// WriteGitHubOutput appends step outputs to the file named by $GITHUB_OUTPUT,
// if set — the composite action republishes them so workflows can drive
// per-image follow-ups without knowing the dist path. No-op outside GitHub
// Actions. They are:
//   - summary: the compact single-line JSON;
//   - refs / digests: JSON objects keyed by image id, of each pinned image's
//     first repository@digest and its digest — fromJSON(...).api in a workflow;
//   - ref / digest: the same for the one pinned image, set only when exactly
//     one image was pinned, so a single-image repo needs no fromJSON.
func (r Result) WriteGitHubOutput() error {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return nil
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	lines := []string{"summary=" + string(data)}
	pinned := r.Pinned()
	refs, digests := map[string]string{}, map[string]string{}
	for _, img := range pinned {
		refs[img.ID], digests[img.ID] = img.DigestRefs[0], img.Digest
	}
	for name, m := range map[string]map[string]string{"refs": refs, "digests": digests} {
		b, merr := json.Marshal(m)
		if merr != nil {
			return merr
		}
		lines = append(lines, name+"="+string(b))
	}
	if len(pinned) == 1 {
		lines = append(lines, "ref="+pinned[0].DigestRefs[0], "digest="+pinned[0].Digest)
	}
	sort.Strings(lines[1:])
	f, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G703: a file the Actions runner names
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, strings.Join(lines, "\n"))
	return err
}

// Markdown renders a GitHub-friendly summary table.
func (r Result) Markdown() string {
	var b strings.Builder
	title := r.Project
	if title == "" {
		title = "release"
	}
	fmt.Fprintf(&b, "## stevedore release — %s\n\n", title)
	if len(r.Degraded) > 0 {
		fmt.Fprintf(&b, "> **Degraded release:** ran without %s (skipped or disabled).\n\n", strings.Join(r.Degraded, ", "))
	}
	b.WriteString("| image | version | digest | signed | sbom | prov | test | vulns |\n")
	b.WriteString("|-------|---------|--------|:------:|:----:|:----:|:----:|-------|\n")
	for _, img := range r.Images {
		if img.Skipped {
			reason := img.Reason
			if reason == "" {
				reason = "unchanged"
			}
			fmt.Fprintf(&b, "| `%s` | — | _skipped (%s)_ | | | | | |\n", img.ID, reason)
			continue
		}
		fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s | %s | %s | %s | %s |\n",
			img.ID, dash(img.Version), shortDigest(img.Digest),
			check(img.Signed), check(img.SBOM), check(img.Provenance), check(img.Tested),
			vulnCell(img.Vulns))
	}
	platformTable(&b, r.Images)
	return b.String()
}

// platformTable renders the per-platform gate results of every multi-platform
// image, where one row per image would hide an untested variant.
func platformTable(b *strings.Builder, imgs []Image) {
	header := false
	for _, img := range imgs {
		if img.Skipped || len(img.Platforms) < 2 {
			continue
		}
		if !header {
			b.WriteString("\n| image | platform | scanned | test | vulns |\n")
			b.WriteString("|-------|----------|:-------:|:----:|-------|\n")
			header = true
		}
		for _, pl := range img.Platforms {
			test := check(pl.Tested)
			if pl.TestSkipped != "" {
				test = "_skipped_"
			}
			fmt.Fprintf(b, "| `%s` | %s | %s | %s | %s |\n", img.ID, pl.Platform, check(pl.Scanned), test, vulnCell(pl.Vulns))
		}
	}
}

// WriteGitHubStepSummary appends the Markdown table to the file named by
// $GITHUB_STEP_SUMMARY, if set. It is a no-op outside GitHub Actions.
func (r Result) WriteGitHubStepSummary() error {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G703: a file the Actions runner names
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(r.Markdown() + "\n")
	return err
}

func check(ok bool) string {
	if ok {
		return "✓"
	}
	return ""
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	if d == "" {
		return "—"
	}
	return d
}

// vulnCell renders the vulnerability tally most-severe first, or "clean".
func vulnCell(counts map[string]int) string {
	if len(counts) == 0 {
		return "—"
	}
	order := []string{"critical", "high", "medium", "low", "negligible"}
	var parts []string
	total := 0
	for _, s := range order {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
			total += n
		}
	}
	// Include any severities not in the known order (stable, deterministic).
	var extra []string
	for s, n := range counts {
		if n > 0 && !slices.Contains(order, s) {
			extra = append(extra, fmt.Sprintf("%d %s", n, s))
		}
	}
	sort.Strings(extra)
	parts = append(parts, extra...)
	if total == 0 && len(parts) == 0 {
		return "clean"
	}
	return strings.Join(parts, ", ")
}
