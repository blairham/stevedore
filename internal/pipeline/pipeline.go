// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package pipeline orchestrates the build/push/sign/sbom/changelog stages.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blairham/stevedore/internal/builder"
	"github.com/blairham/stevedore/internal/changed"
	"github.com/blairham/stevedore/internal/changelog"
	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/fingerprint"
	"github.com/blairham/stevedore/internal/gitinfo"
	"github.com/blairham/stevedore/internal/preflight"
	"github.com/blairham/stevedore/internal/projgraph"
	"github.com/blairham/stevedore/internal/publish"
	"github.com/blairham/stevedore/internal/run"
	"github.com/blairham/stevedore/internal/sbom"
	"github.com/blairham/stevedore/internal/sbomdiff"
	"github.com/blairham/stevedore/internal/scanner"
	"github.com/blairham/stevedore/internal/semver"
	"github.com/blairham/stevedore/internal/signer"
	"github.com/blairham/stevedore/internal/summary"
	"github.com/blairham/stevedore/internal/tester"
	"github.com/blairham/stevedore/internal/tmpl"
	"github.com/blairham/stevedore/internal/versioner"
)

// progress is where human-readable progress is written. Release redirects it to
// stderr under --output json so stdout carries only the JSON document.
var progress io.Writer = os.Stdout

// Options controls a pipeline invocation.
type Options struct {
	// Context bounds every external command the invocation starts. Nil means
	// context.Background().
	Context       context.Context
	ConfigPath    string
	Dir           string // repository root
	Snapshot      bool
	Push          bool // release: always true; build: false unless overridden
	DryRun        bool
	Verbose       bool
	SkipSign      bool
	SkipSBOM      bool
	SkipScan      bool
	SkipTest      bool
	NoPush        bool // build (and change-detect) but don't push; skips push-dependent stages
	Parallel      int  // build up to N images concurrently (default 1)
	SkipChangelog bool
	SkipPublish   bool   // skip GitHub release, announce and notify webhook
	OnlyChanged   bool   // skip images whose build inputs are unchanged (fingerprint state)
	ChangedSince  string // git ref: skip images not touched by the diff since this ref
	SoftVersion   bool   // tolerate version-resolution failure (check): warn + placeholder
	OutputJSON    bool   // emit a JSON release summary to stdout (progress to stderr)
	// Only restricts the run to these image IDs and builds them unconditionally
	// (change detection is the planner's job — see the plan command). Matrix
	// mode: each CI job runs `release --only <ids>` for one plan entry.
	Only []string
	// PinVersions overrides per-image version resolution (id → version), so a
	// matrix job tags exactly what the plan resolved.
	PinVersions map[string]string
	// SplitPlatforms restricts the build to these platforms and pushes the
	// result untagged, by digest (split mode: one native-arch CI leg). Digests
	// land under dist/digests/ for a later `merge` run; the release tail
	// (scan/test/sign/sbom/changelog/publish) is skipped.
	SplitPlatforms []string
	// FromDigests skips building entirely: each image's per-arch digests
	// (written by split legs into dist/digests/) are merged into a tagged
	// manifest list via `imagetools create`, then the release tail runs.
	FromDigests bool
	// SplitPerPlatform makes the plan emit one matrix entry per build group
	// per platform (with a native runner hint) instead of one per group.
	SplitPerPlatform bool
	// KeepGoing builds every group even after one fails, and fails the run
	// at the end. Without it the first failure stops dispatching more
	// groups. Either way, the groups that did push are recorded (markers,
	// notifications, summary) before the run fails.
	KeepGoing bool
	// BuildAll builds every image regardless of change detection: a tagged
	// release under versioning.require_tag.
	BuildAll bool
	// AllowNonDefaultBranch lets a real (non-snapshot) release publish from a
	// commit that is not on the default branch.
	AllowNonDefaultBranch bool
	Now                   time.Time
}

// context returns the invocation's context. It carries a Runner with the
// invocation's dry-run and verbose settings, so a package whose API takes only
// a context (internal/changed) still echoes what it runs under --dry-run and
// --verbose.
func (o Options) context() context.Context {
	ctx := o.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return run.WithRunner(ctx, run.New(ctx, o.DryRun, o.Verbose))
}

// ImagePlan is a fully-resolved plan for one image.
type ImagePlan struct {
	Image config.Image
	Repos []string
	Refs  []string // repo:tag cartesian product actually published
	// Floating is the subset of Refs that are floating tags (see
	// floatingTag): applied last, and never looked up as commit tags.
	Floating  map[string]bool
	BuildArgs []string
	Labels    map[string]string
	CacheFrom []string
	CacheTo   []string
	// Paths are the resolved dependency globs for change detection (per-image
	// Paths plus any graph-resolved directories). Empty means change detection
	// falls back to the build context (see changeScope).
	Paths []string
	// Version is the version this image was tagged with — its own under per-image
	// registry versioning, otherwise the release version.
	Version string
	// SourceDateEpoch is the SOURCE_DATE_EPOCH passed to the build, or "" for
	// none (source_date_epoch: false, or a repository with no commits).
	SourceDateEpoch string
}

// Prepared bundles the loaded config, git info, and resolved plans.
type Prepared struct {
	Config *config.Config
	Git    *gitinfo.Info
	Ctx    *tmpl.Context
	Plans  []ImagePlan
}

// Prepare loads config, gathers git state, and resolves image plans.
func Prepare(o Options) (*Prepared, error) {
	cfg, err := loadConfig(o)
	if err != nil {
		return nil, err
	}
	gi, err := gitinfo.Gather(o.context(), o.Dir)
	if err != nil {
		return nil, err
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}

	// Resolve the release version via the configured strategy (git, registry,
	// static, env, or command) and let it drive the template context.
	ver, err := resolveVersion(cfg, gi, o)
	if err != nil {
		return nil, err
	}
	gi.Version = ver

	ctx := tmpl.NewContext(cfg.ProjectName, cfg.DefaultBranch, gi, o.Snapshot, now, envMap())

	// Under the registry strategy each image is versioned from its own repo;
	// versionFor is nil for other strategies (one version for all images).
	var versionFor func(string) (string, error)
	if isRegistryStrategy(cfg.Versioning.Strategy) {
		versionFor, err = imageVersionResolver(cfg, gi, o, ctx)
		if err != nil {
			return nil, err
		}
	}
	plans, err := resolvePlans(cfg, ctx, o.Snapshot, versionFor, o.PinVersions, o.Only)
	if err != nil {
		return nil, err
	}
	// Resolve each image's dependency paths for change detection (per-image
	// paths plus any project-graph expansion).
	for i := range plans {
		paths, err := resolveImagePaths(o.Dir, cfg, plans[i].Image)
		if err != nil {
			return nil, err
		}
		plans[i].Paths = paths
	}
	return &Prepared{Config: cfg, Git: gi, Ctx: ctx, Plans: plans}, nil
}

// loadConfig loads and validates the config, including the image IDs that
// --only and --pin-version refer to.
func loadConfig(o Options) (*config.Config, error) {
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := validateImageIDs(cfg, o); err != nil {
		return nil, err
	}
	return cfg, nil
}

// imageVersionResolver returns a per-image version resolver for the registry
// strategy: each image's version comes from its own repository (highest semver +
// bump). The other strategies apply one version to every image and need none,
// so Prepare only calls this under the registry strategy. When versioning.repo
// is pinned, all images resolve from that one repo (i.e. a unified version).
func imageVersionResolver(cfg *config.Config, gi *gitinfo.Info, o Options, ctx *tmpl.Context) (func(string) (string, error), error) {
	vcfg := cfg.Versioning
	if vcfg.Repo != "" {
		rendered, err := tmpl.Render(vcfg.Repo, ctx)
		if err != nil {
			return nil, fmt.Errorf("render versioning repo %q: %w", vcfg.Repo, err)
		}
		vcfg.Repo = rendered
	}
	r := run.New(o.context(), o.DryRun, o.Verbose)
	list := tagLister(cfg, r)
	warned := false
	return func(repo string) (string, error) {
		v, err := versioner.Resolve(versioner.Input{
			Cfg:      vcfg,
			Git:      gi,
			Snapshot: o.Snapshot,
			Repo:     repo, // ignored when vcfg.Repo is pinned
			Getenv:   os.Getenv,
			ListTags: list,
			RunCmd:   func(command string) (string, error) { return r.Capture("sh", "-c", command) },
		})
		if err != nil && o.SoftVersion {
			if !warned {
				fmt.Fprintf(os.Stderr, "warning: could not resolve image versions (%v); showing placeholder\n", err)
				warned = true
			}
			return unresolvedVersion, nil
		}
		return v, err
	}, nil
}

// resolveImagePaths returns the image's change-detection globs: its explicit
// Paths, plus the transitive project-graph directories when a resolver is set.
func resolveImagePaths(dir string, cfg *config.Config, img config.Image) ([]string, error) {
	paths := append([]string(nil), img.Paths...)
	if cfg.ChangeDetection.Resolver == "dotnet" && img.Project != "" {
		deps, err := projgraph.DotnetDeps(dir, img.Project)
		if err != nil {
			return nil, fmt.Errorf("image %s: resolve project graph: %w", img.ID, err)
		}
		paths = append(paths, deps...)
	}
	return dedupeStrings(paths), nil
}

// scopedFingerprintPaths returns the globs a fingerprint should hash: the
// image's resolved paths plus the shared paths. Empty when the image is unscoped
// (so Compute falls back to the whole build context).
func scopedFingerprintPaths(imgPaths, shared []string) []string {
	if len(imgPaths) == 0 {
		return nil
	}
	return append(append([]string(nil), imgPaths...), shared...)
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// validateImageIDs rejects --only / --pin-version references to image IDs that
// don't exist in the config, so a typo fails fast instead of silently building
// nothing (or resolving a version nobody pinned).
func validateImageIDs(cfg *config.Config, o Options) error {
	known := map[string]bool{}
	for _, img := range cfg.Images {
		known[img.ID] = true
	}
	for _, id := range o.Only {
		if !known[id] {
			return fmt.Errorf("--only: unknown image id %q", id)
		}
	}
	for id := range o.PinVersions {
		if !known[id] {
			return fmt.Errorf("--pin-version: unknown image id %q", id)
		}
	}
	return nil
}

// firstSelectedImage returns the first config image included by --only (config
// order), or the first image overall when there is no selection.
func firstSelectedImage(cfg *config.Config, only []string) *config.Image {
	if len(cfg.Images) == 0 {
		return nil
	}
	if len(only) == 0 {
		return &cfg.Images[0]
	}
	sel := map[string]bool{}
	for _, id := range only {
		sel[id] = true
	}
	for i := range cfg.Images {
		if sel[cfg.Images[i].ID] {
			return &cfg.Images[i]
		}
	}
	return nil
}

// resolvePlans renders tags/repos/build-args/labels and computes the published
// references. Floating tags (see floatingTag) are dropped off the default
// branch, in a snapshot build, and on a prerelease version unless
// prerelease_floating_tags allows them. A pinned version (pins[id]) overrides
// resolution for that image entirely. A non-empty `only` restricts plans to
// those image IDs (config order preserved) — excluded images are skipped
// before version resolution, so a matrix job's credentials only need registry
// access to its own repositories.
func resolvePlans(cfg *config.Config, ctx *tmpl.Context, snapshot bool, versionFor func(string) (string, error), pins map[string]string, only []string) ([]ImagePlan, error) {
	selected := map[string]bool{}
	for _, id := range only {
		selected[id] = true
	}
	var plans []ImagePlan
	// Floating tags withheld this run, reported once at the end rather than
	// once per image x repository.
	withheld := &withheldTags{seen: map[string]bool{}}
	defer func() { warnWithheldFloating(withheld, cfg.DefaultBranch, ctx) }()
	for _, img := range cfg.Images {
		if len(selected) > 0 && !selected[img.ID] {
			continue
		}
		plan, err := resolvePlan(img, ctx, floatingPolicy{snapshot: snapshot, onPrerelease: cfg.PrereleaseFloatingTags}, versionFor, pins, withheld)
		if err != nil {
			return nil, err
		}
		if cfg.SourceDateEpochEnabled() {
			plan.SourceDateEpoch = sourceDateEpoch(plan.BuildArgs, ctx)
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// sourceDateEpoch is the SOURCE_DATE_EPOCH a build gets: an image's own
// SOURCE_DATE_EPOCH build arg, else one already in the environment (the
// reproducible-builds convention is that the caller's value wins), else HEAD's
// commit time. "" when there is none of these.
func sourceDateEpoch(buildArgs []string, ctx *tmpl.Context) string {
	for _, a := range buildArgs {
		if v, ok := strings.CutPrefix(a, sourceDateEpochVar+"="); ok {
			return v
		}
	}
	if v := os.Getenv(sourceDateEpochVar); v != "" {
		return v
	}
	if ctx.CommitTimestamp > 0 {
		return strconv.FormatInt(ctx.CommitTimestamp, 10)
	}
	return ""
}

const sourceDateEpochVar = "SOURCE_DATE_EPOCH"

// floatingPolicy is what decides whether a floating tag may publish this run.
type floatingPolicy struct {
	snapshot     bool
	onPrerelease bool // prerelease_floating_tags
}

// withholdReason says why floating tags may not publish for an image whose
// template context is ctx, or "" when they may.
func (fp floatingPolicy) withholdReason(ctx *tmpl.Context) string {
	switch {
	case fp.snapshot:
		return reasonSnapshot
	case !ctx.IsDefault:
		return reasonOffDefault
	case ctx.IsPrerelease() && !fp.onPrerelease:
		return reasonPrerelease
	}
	return ""
}

const (
	reasonSnapshot   = "snapshot"
	reasonOffDefault = "off-default-branch"
	reasonPrerelease = "prerelease"
)

// withheldTags collects the floating tags a run declined to publish, in the
// order first seen, with why.
type withheldTags struct {
	seen  map[string]bool
	order []withheldTag
}

type withheldTag struct {
	tag, reason, version string
}

func (w *withheldTags) add(tag, reason, version string) {
	if key := reason + "\x00" + tag; !w.seen[key] {
		w.seen[key] = true
		w.order = append(w.order, withheldTag{tag: tag, reason: reason, version: version})
	}
}

// resolvePlan renders one image's plan; see resolvePlans.
func resolvePlan(img config.Image, ctx *tmpl.Context, fp floatingPolicy, versionFor func(string) (string, error), pins map[string]string, withheld *withheldTags) (ImagePlan, error) {
	// Repositories are rendered with the release context (they key on .Env,
	// not .Version), and drive per-image version resolution.
	repos, err := tmpl.RenderAll(img.Repositories, ctx)
	if err != nil {
		return ImagePlan{}, fmt.Errorf("image %s repositories: %w", img.ID, err)
	}
	imgVersion, imgCtx, err := imageVersion(img.ID, repos, ctx, versionFor, pins)
	if err != nil {
		return ImagePlan{}, err
	}
	plan := ImagePlan{Image: img, Repos: repos, Version: imgVersion}

	var tags []string
	fields := []struct {
		name string
		in   []string
		out  *[]string
	}{
		{"tags", img.Tags, &tags},
		{"build_args", img.BuildArgs, &plan.BuildArgs},
		{"cache_from", img.CacheFrom, &plan.CacheFrom},
		{"cache_to", img.CacheTo, &plan.CacheTo},
	}
	for _, f := range fields {
		if *f.out, err = tmpl.RenderAll(f.in, imgCtx); err != nil {
			return ImagePlan{}, fmt.Errorf("image %s %s: %w", img.ID, f.name, err)
		}
	}
	plan.Labels = map[string]string{}
	for k, v := range img.Labels {
		rv, err := tmpl.Render(v, imgCtx)
		if err != nil {
			return ImagePlan{}, fmt.Errorf("image %s label %s: %w", img.ID, k, err)
		}
		plan.Labels[k] = rv
	}

	floating := make([]bool, len(tags))
	for i, tag := range tags {
		if floating[i], err = floatingTag(img.Tags[i], tag, imgCtx); err != nil {
			return ImagePlan{}, fmt.Errorf("image %s tags: %w", img.ID, err)
		}
	}
	reason := fp.withholdReason(imgCtx)
	plan.Refs, plan.Floating = publishedRefs(repos, tags, floating, reason != "", func(tag string) {
		// A snapshot withholding "latest" is the point of a snapshot; off
		// the default branch or on a prerelease it is a decision the user
		// needs to see, because the alternative is finding out from the
		// registry weeks later.
		if reason != reasonSnapshot {
			withheld.add(tag, reason, imgVersion)
		}
	})
	if len(plan.Refs) == 0 {
		return ImagePlan{}, fmt.Errorf("image %s: no publishable references after tag resolution", img.ID)
	}
	return plan, nil
}

// imageVersion determines an image's version and the template context that
// carries it. A pin (from the plan) wins outright; under per-image registry
// versioning it comes from the image's own repo; otherwise it's the release
// version.
func imageVersion(id string, repos []string, ctx *tmpl.Context, versionFor func(string) (string, error), pins map[string]string) (string, *tmpl.Context, error) {
	if pin, ok := pins[id]; ok {
		return pin, ctx.WithVersion(pin), nil
	}
	if versionFor != nil && len(repos) > 0 {
		v, err := versionFor(repos[0])
		if err != nil {
			return "", nil, fmt.Errorf("image %s version: %w", id, err)
		}
		return v, ctx.WithVersion(v), nil
	}
	return ctx.Version, ctx, nil
}

// publishedRefs is the repo:tag product, minus floating tags (floating[i] for
// tags[i]) when withholdFloating is set; each withheld tag is passed to
// onWithheld. The floating refs that are published are returned as a set.
func publishedRefs(repos, tags []string, floating []bool, withholdFloating bool, onWithheld func(string)) ([]string, map[string]bool) {
	var refs []string
	floatingRefs := map[string]bool{}
	for _, repo := range repos {
		for i, tag := range tags {
			if floating[i] {
				if withholdFloating {
					onWithheld(tag)
					continue
				}
				floatingRefs[repo+":"+tag] = true
			}
			refs = append(refs, repo+":"+tag)
		}
	}
	return refs, floatingRefs
}

// warnWithheldFloating explains, once per run, why floating tags are not being
// published. It writes to stderr rather than the progress writer so that `plan`
// and `--output json` keep a clean stdout.
func warnWithheldFloating(w *withheldTags, defaultBranch string, ctx *tmpl.Context) {
	var offDefault, prerelease []string
	var preVersion string
	for _, t := range w.order {
		switch t.reason {
		case reasonOffDefault:
			offDefault = append(offDefault, strconv.Quote(t.tag))
		case reasonPrerelease:
			prerelease = append(prerelease, strconv.Quote(t.tag))
			if preVersion == "" {
				preVersion = t.version
			}
		}
	}
	if len(prerelease) > 0 {
		// Expected and by design, so a note rather than a warning — but named,
		// so nobody wonders why :latest did not move.
		fmt.Fprintf(os.Stderr, "note: not publishing floating tag(s) %s: %s is a prerelease "+
			"(set prerelease_floating_tags: true to publish them anyway)\n",
			strings.Join(prerelease, ", "), preVersion)
	}
	if len(offDefault) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "warning: not publishing floating tag(s) %s: HEAD is not on the default branch %q\n",
		strings.Join(offDefault, ", "), defaultBranch)
	if ctx.Detached {
		// The common cause, and the one with a one-line fix. A shallow clone
		// has no branch refs for the commit to be reachable from, so a detached
		// HEAD looks identical to a genuine side branch.
		fmt.Fprintf(os.Stderr, "         HEAD is detached, so the branch is resolved from the refs containing the commit; "+
			"that needs the full history (actions/checkout: fetch-depth: 0).\n")
	}
}

// isFloating reports whether a rendered tag is named as a mutable pointer:
// "latest" or anything ending in "-latest".
func isFloating(tag string) bool {
	t := strings.ToLower(tag)
	return t == "latest" || strings.HasSuffix(t, "-latest")
}

// pinningFields are the template fields that make a tag name one release
// rather than a line of them: a tag that uses any of these is never floating
// on account of also using .Major or .Minor ("{{ .Major }}.{{ .Minor }}.{{
// .Patch }}" is a version tag, not a pointer).
var pinningFields = []string{"Patch", "Prerelease", "Version", "Tag", "LatestTag", "Commit", "ShortCommit", "Date", "Timestamp", "CommitDate", "CommitTimestamp"}

// floatingTag reports whether the tag rendered as tag from the template src is
// floating — a pointer that moves from release to release, so it may only
// publish on the default branch of a real, non-prerelease release, and is
// applied last. That is:
//   - a tag named like one: "latest", "*-latest";
//   - a template built on .Major or .Minor without anything that pins a single
//     release ("{{ .Major }}", "v{{ .Major }}.{{ .Minor }}", "{{ .Major }}-alpine");
//   - a tag that spells the version's own major or major.minor ("1", "v1.2"
//     for 1.2.3), however the template produced it.
func floatingTag(src, tag string, ctx *tmpl.Context) (bool, error) {
	if isFloating(tag) {
		return true, nil
	}
	fields, err := tmpl.Fields(src)
	if err != nil {
		return false, err
	}
	if (fields["Major"] || fields["Minor"]) && !slices.ContainsFunc(pinningFields, func(f string) bool { return fields[f] }) {
		return true, nil
	}
	if v, ok := semver.Parse(ctx.Version); ok {
		t := strings.TrimPrefix(tag, "v")
		if t == strconv.Itoa(v.Major) || t == fmt.Sprintf("%d.%d", v.Major, v.Minor) {
			return true, nil
		}
	}
	return false, nil
}

// Release runs the full pipeline: build+push, sign, SBOM, changelog. With
// NoPush it builds (honoring change detection) but publishes nothing and skips
// every stage that needs a pushed artifact (sign, SBOM, scan, provenance,
// GitHub release, announce).
func Release(o Options) error {
	o, err := releaseOptions(o)
	if err != nil {
		return err
	}
	if o.OutputJSON {
		// Keep stdout clean for the JSON document.
		progress = os.Stderr
		defer func() { progress = os.Stdout }()
	}
	o, err = applyRequireTag(o)
	if err != nil {
		return err
	}
	p, err := Prepare(o)
	if err != nil {
		return err
	}
	err = preflightRelease(o, p)
	if err != nil {
		return err
	}
	r := run.New(o.context(), o.DryRun, o.Verbose)

	fpPath, state, err := loadReleaseState(o, p)
	if err != nil {
		return err
	}

	// Pre-pass: change detection + fingerprints, then group identical build
	// specs (shared with the plan command).
	evals, err := evaluateImages(o, p, state)
	if err != nil {
		return err
	}
	toBuild, skipped := groupPlans(o.Dir, evals)
	if len(o.SplitPlatforms) > 0 {
		toBuild, skipped = splitLegGroups(toBuild, skipped, o.SplitPlatforms)
	}
	return buildAndFinish(o, p, r, toBuild, skipped, fpPath, state)
}

// buildAndFinish builds the groups and runs the release tail. A group that
// fails does not cost the ones that succeeded their record: by then they are
// pushed, signed and tagged, and without a marker, a notification and a
// summary the next run would release them again as a new version. So the tail
// always runs for whatever was built, and the build failure is returned with
// whatever else went wrong.
func buildAndFinish(o Options, p *Prepared, r *run.Runner, toBuild [][]imageEval, skipped []imageEval, fpPath string, state fingerprint.State) error {
	result := summary.Result{Project: p.Config.ProjectName, Snapshot: o.Snapshot, Degraded: degradedStages(o, p.Config)}
	result.Images = reportGroups(toBuild, skipped)

	built, depDiffSections, buildErr := buildGroups(o, p, r, toBuild, state)
	result.Images = append(result.Images, built...)
	sort.Slice(result.Images, func(i, j int) bool { return result.Images[i].ID < result.Images[j].ID })

	return finishRelease(o, p, r, result, fpPath, state, depDiffSections, buildErr)
}

// finishRelease is everything after the builds: it records the new
// fingerprints, advances the release markers, notifies, writes the changelog,
// publishes and emits the summary. The images are already out by now, so a
// stage that fails does not stop the ones after it: every stage runs, the
// summary is always written, and the failures are reported together at the
// end.
//
// buildErr is the failure of any group that did not build. The groups that
// did are recorded as usual, but the release as a whole is incomplete, so it
// writes no changelog and is not published (GitHub release, announce); the
// re-run that builds the rest does that.
func finishRelease(o Options, p *Prepared, r *run.Runner, result summary.Result, fpPath string, state fingerprint.State, depDiffSections []string, buildErr error) error {
	var errs []error
	if buildErr != nil {
		errs = append(errs, buildErr)
	}
	if recordsFingerprints(o) {
		if err := state.Save(fpPath); err != nil {
			errs = append(errs, fmt.Errorf("save fingerprint state: %w", err))
		}
	}

	markerErrs := advanceMarkers(o, p, result.Images)

	if err := notifyWebhook(o, p, r, result.Images); err != nil {
		errs = append(errs, err)
	}

	if buildErr != nil {
		fmt.Fprintln(progress, "==> a build failed: recorded what was pushed, but no changelog, GitHub release or announcement")
	} else if changelogPath, err := writeChangelog(o, p, depDiffSections); err != nil {
		errs = append(errs, err)
	} else if err := publishStage(o, p, r, changelogPath, result.Images); err != nil {
		errs = append(errs, err)
	}

	if err := emitSummary(o, p, result); err != nil {
		errs = append(errs, err)
	}

	if len(markerErrs) > 0 {
		errs = append(errs, fmt.Errorf("images published, but %d release marker(s) could not advance: %w",
			len(markerErrs), errors.Join(markerErrs...)))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if len(o.SplitPlatforms) > 0 {
		fmt.Fprintln(progress, "==> split build complete — assemble with `stevedore merge`")
	} else {
		fmt.Fprintln(progress, "==> release complete")
	}
	return nil
}

// publishStage creates the GitHub release and posts the announcements, for a
// real release that built something: a run whose every image was skipped
// released no version, so there is nothing to name a release after.
//
// An --only run never publishes. --only is matrix mode, one job per plan
// entry, and every job publishing would create the same GitHub release N
// times (or N releases named after N image versions) and announce N times;
// like a split leg, a matrix job leaves that to one final step, `stevedore
// publish`.
func publishStage(o Options, p *Prepared, r *run.Runner, changelogPath string, images []summary.Image) error {
	if o.NoPush || o.Snapshot || o.SkipPublish {
		return nil
	}
	if len(o.Only) > 0 {
		if p.Config.Release.GitHub.Enabled || p.Config.Announce.Slack.Enabled || p.Config.Announce.Discord.Enabled {
			fmt.Fprintln(progress, "==> --only: no GitHub release or announcement from a matrix job; run `stevedore publish` once after the matrix")
		}
		return nil
	}
	refs := builtRefs(images)
	if len(refs) == 0 {
		fmt.Fprintln(progress, "==> nothing was built; no GitHub release or announcement")
		return nil
	}
	return publishRelease(r, p, changelogPath, refs)
}

// Publish is the last step of a matrix release, run once after every
// `release --only` job has finished: it writes the changelog, creates the
// GitHub release and posts the announcements that the matrix jobs leave out.
// Given the --only and --pin-version of every plan entry that built, the
// release is named after the version those jobs pushed and the announcement
// carries their refs, rather than re-resolving a version (which, under
// registry versioning, would now be the next one).
func Publish(o Options) error {
	p, err := Prepare(o)
	if err != nil {
		return err
	}
	err = guardReleasable(p.Git, p.Config.Versioning.Strategy)
	if err == nil {
		err = guardDefaultBranch(o, p.Config.DefaultBranch)
	}
	if err == nil && !o.DryRun {
		err = checkTools(o.context(), p.Config, preflight.Opts{GitHubRelease: p.Config.Release.GitHub.Enabled, VersionsPinned: allPinned(p.Plans, o.PinVersions)})
	}
	if err == nil && !o.DryRun && p.Config.Release.GitHub.Enabled {
		err = checkGitHubAuth(o)
	}
	if err != nil {
		return err
	}
	err = mkdirDist(filepath.Join(o.Dir, p.Config.Dist))
	if err != nil {
		return fmt.Errorf("create dist dir: %w", err)
	}
	r := run.New(o.context(), o.DryRun, o.Verbose)
	changelogPath, err := writeChangelog(o, p, nil)
	if err != nil {
		return err
	}
	var refs []string
	for _, plan := range p.Plans {
		refs = append(refs, plan.Refs...)
	}
	if err := publishRelease(r, p, changelogPath, refs); err != nil {
		return err
	}
	fmt.Fprintln(progress, "==> publish complete")
	return nil
}

// recordsFingerprints reports whether this run may become the baseline for the
// next --only-changed run. Only a run that actually published what it built
// qualifies: a fingerprint hashes the inputs alone, not the version or the push
// mode, so one saved by a --no-push validation, a snapshot or a split leg
// would match the next real release and make it skip images nobody released.
// A merge run is the real publish of a split release, so it records.
func recordsFingerprints(o Options) bool {
	return !o.DryRun && !o.NoPush && !o.Snapshot && len(o.SplitPlatforms) == 0
}

// reportGroups announces the skipped images and the shared builds, and returns
// the skipped images' summary entries.
func reportGroups(toBuild [][]imageEval, skipped []imageEval) []summary.Image {
	images := make([]summary.Image, 0, len(skipped))
	for _, m := range skipped {
		fmt.Fprintf(progress, "==> skipping %s (%s)\n", m.plan.Image.ID, m.reason)
		images = append(images, summary.Image{ID: m.plan.Image.ID, Skipped: true, Reason: m.reason})
	}
	for _, grp := range toBuild {
		if len(grp) > 1 {
			fmt.Fprintf(progress, "==> %d images share one build: %s\n", len(grp), strings.Join(evalIDs(grp), ", "))
		}
	}
	return images
}

// loadReleaseState creates dist/ and loads the fingerprint state from it.
// Fingerprint state drives --only-changed. Every release reads it; only a
// real publish writes it back (see recordsFingerprints), so the next
// --only-changed run compares against what was last released. A dry run
// writes nothing, dist/ included: it only reads whatever state is there.
func loadReleaseState(o Options, p *Prepared) (string, fingerprint.State, error) {
	if !o.DryRun {
		if err := mkdirDist(filepath.Join(o.Dir, p.Config.Dist)); err != nil {
			return "", nil, fmt.Errorf("create dist dir: %w", err)
		}
	}
	fpPath := filepath.Join(o.Dir, p.Config.Dist, "fingerprints.json")
	state, err := fingerprint.Load(fpPath)
	if err != nil {
		return "", nil, err
	}
	return fpPath, state, nil
}

// BuildPushOptions turns a `build` invocation into the `build --push` release
// run: a multi-arch snapshot that is pushed, scanned, smoke-tested and tagged,
// and nothing more. The release extras — signing, SBOM, changelog, and every
// outbound announcement (GitHub release, announce, notify webhook) — belong
// to `release`. The notify webhook in particular exists to trigger CD, and an
// inner-loop push must never be what deploys. The gates stay on: they are
// cheap next to the build, and a tag only lands once they pass.
func BuildPushOptions(o Options) Options {
	o.Snapshot = true
	o.Push = true
	o.SkipSign = true
	o.SkipSBOM = true
	o.SkipChangelog = true
	o.SkipPublish = true
	return o
}

// releaseOptions applies the option implications of a release run: a split
// leg cannot be --no-push and leaves the tail to the merge run, and --no-push
// tolerates an unresolvable version.
func releaseOptions(o Options) (Options, error) {
	if len(o.SplitPlatforms) > 0 {
		if o.NoPush {
			return o, fmt.Errorf("--split pushes per-arch images by digest; drop --no-push")
		}
		// A split leg only builds and pushes by digest; everything that
		// operates on the final manifest list belongs to the merge run.
		o.SkipChangelog = true
		o.SkipPublish = true
	}
	o.Push = !o.NoPush
	if o.NoPush {
		// A validate-only build discards the tag, so a version that can't be
		// resolved (e.g. registry/ECR unreachable in CI) shouldn't fail the run.
		o.SoftVersion = true
	}
	return o, nil
}

// preflightRelease refuses an unreleasable tree and checks that the tools this
// run needs are installed.
func preflightRelease(o Options, p *Prepared) error {
	if !o.Snapshot {
		if err := guardReleasable(p.Git, p.Config.Versioning.Strategy); err != nil {
			return err
		}
		// A split leg pushes only untagged digests; the merge run that
		// tags them is held to the default branch instead. A --no-push run
		// publishes nothing at all.
		if !o.NoPush && len(o.SplitPlatforms) == 0 {
			if err := guardDefaultBranch(o, p.Config.DefaultBranch); err != nil {
				return err
			}
		}
	}
	if err := enforcePolicy(o, p.Config); err != nil {
		return err
	}
	if o.DryRun {
		return nil
	}
	if !o.Snapshot {
		if err := checkSecrets(p.Plans); err != nil {
			return err
		}
	}
	ghRelease := !o.NoPush && !o.Snapshot && !o.SkipPublish && len(o.Only) == 0 && p.Config.Release.GitHub.Enabled
	pinned := allPinned(p.Plans, o.PinVersions)
	// --no-push builds only; none of the push-dependent tools are required.
	opts := preflight.Opts{Sign: !o.NoPush && !o.SkipSign, SBOM: !o.NoPush && !o.SkipSBOM, Scan: !o.NoPush && !o.SkipScan, GitHubRelease: ghRelease, VersionsPinned: pinned}
	if len(o.SplitPlatforms) > 0 {
		// A split leg needs only the build toolchain — the merge run
		// carries the sign/scan/sbom/publish requirements.
		opts = preflight.Opts{VersionsPinned: pinned}
	}
	if err := checkTools(o.context(), p.Config, opts); err != nil {
		return err
	}
	if ghRelease {
		return checkGitHubAuth(o)
	}
	return nil
}

// buildGroups builds each group, up to o.Parallel groups at a time, recording
// each built member's fingerprint in state. It returns the built images'
// summaries and dependency-diff sections — of every group that succeeded,
// even when another failed — and the failures, joined. The first failure
// stops dispatch unless o.KeepGoing.
func buildGroups(o Options, p *Prepared, r *run.Runner, toBuild [][]imageEval, state fingerprint.State) ([]summary.Image, []string, error) {
	workers := max(o.Parallel, 1)
	if len(toBuild) > 0 {
		workers = min(workers, len(toBuild))
	}
	if workers > 1 {
		fmt.Fprintf(progress, "==> building %d group(s), up to %d in parallel\n", len(toBuild), workers)
	}
	var (
		mu       sync.Mutex
		errs     []error
		wg       sync.WaitGroup
		sem      = make(chan struct{}, workers)
		images   []summary.Image
		depDiffs []string
	)
	for _, grp := range toBuild {
		// Wait for a free slot before deciding: only then has the group that
		// held it finished, and with it the failure that should stop us.
		sem <- struct{}{}
		mu.Lock()
		stop := len(errs) > 0 && !o.KeepGoing
		mu.Unlock()
		if stop {
			<-sem
			break // a build already failed; stop dispatching more
		}
		wg.Add(1)
		go func(grp []imageEval) {
			defer wg.Done()
			defer func() { <-sem }()
			irs, dep, err := buildGroup(o, p, r, grp)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", grp[0].plan.Image.ID, err))
				return
			}
			images = append(images, irs...)
			for _, m := range grp {
				state[m.plan.Image.ID] = m.fp
			}
			if dep != "" {
				depDiffs = append(depDiffs, dep)
			}
		}(grp)
	}
	wg.Wait()
	return images, depDiffs, errors.Join(errs...)
}

// advancesMarker reports whether an image's release marker moves: it was
// built, or it was found already released (its commit tags exist from this
// commit) — skipped, but released, so its marker moves like a build's.
func advancesMarker(im summary.Image) bool {
	return !im.Skipped || im.AlreadyReleased
}

// advanceMarkers advances each built image's release marker to HEAD (and
// pushes it) so the next run's change detection diffs against this release.
// Only on a real push of a real release — but regardless of how THIS run
// selected its images (marker diff, --changed-since, or --only): the marker
// records "last released", not "last marker-diffed", so matrix jobs running
// `release --only <id>` keep every image's baseline current.
// Split legs never advance markers: "released" means the merged manifest
// list was published, which is the merge run's outcome.
//
// A marker that cannot advance FAILS the run, but only after the
// notifications and publishing that follow: the images are already out, and
// CD still has to hear about them. It used to be a warning, which let one
// diverged marker turn every later release into a silent full rebuild
// behind a green job. A marker already ahead of HEAD (an older release
// re-run) is the one non-failure. The failures are returned for Release to
// report last.
func advanceMarkers(o Options, p *Prepared, images []summary.Image) []error {
	cd := p.Config.ChangeDetection
	if !cd.MarkerRefs || o.DryRun || o.NoPush || o.Snapshot || len(o.SplitPlatforms) > 0 {
		return nil
	}
	var markerErrs []error
	for _, im := range images {
		if !advancesMarker(im) {
			continue
		}
		ref := changed.MarkerRef(cd.MarkerPrefix, im.ID)
		err := changed.AdvanceMarker(o.context(), o.Dir, ref)
		switch {
		case err == nil:
			fmt.Fprintf(progress, "==> advanced release marker %s\n", ref)
		case errors.Is(err, changed.ErrMarkerAhead):
			fmt.Fprintf(progress, "==> left release marker %s: %v\n", ref, err)
		default:
			fmt.Fprintf(progress, "error: advance release marker %s: %v\n", ref, err)
			markerErrs = append(markerErrs, fmt.Errorf("advance release marker %s: %w", ref, err))
		}
	}
	return markerErrs
}

// notifyWebhook fires the machine-readable post-push notifications, once per
// pushed image, so a CD system can react to the new digests. Unlike announce
// they also fire on snapshots (the payload carries the snapshot flag) — but
// never on split legs, where the merged manifest list hasn't been published
// yet.
func notifyWebhook(o Options, p *Prepared, r *run.Runner, images []summary.Image) error {
	if !p.Config.Notify.Webhook.Enabled || o.NoPush || o.SkipPublish {
		return nil
	}
	var notes []publish.Notification
	for _, im := range images {
		if im.Skipped {
			continue
		}
		notes = append(notes, publish.Notification{
			Project:      p.Config.ProjectName,
			Snapshot:     o.Snapshot,
			Image:        im.ID,
			Version:      im.Version,
			Digest:       im.Digest,
			Repositories: im.Repositories,
			Refs:         im.Refs,
		})
	}
	if err := publish.Notify(r, p.Config.Notify.Webhook, notes); err != nil {
		return err
	}
	if len(notes) > 0 {
		fmt.Fprintf(progress, "==> notified webhook of %d pushed image(s)\n", len(notes))
	}
	return nil
}

// writeChangelog writes dist/CHANGELOG.md, with the dependency-diff sections
// appended, and returns its path — or "" when the changelog is off.
func writeChangelog(o Options, p *Prepared, depDiffSections []string) (string, error) {
	if o.SkipChangelog || !p.Config.Changelog.Enabled {
		return "", nil
	}
	notes, err := changelog.Generate(o.context(), p.Config.Changelog, p.Git, o.Dir)
	if err != nil {
		return "", err
	}
	if len(depDiffSections) > 0 {
		notes += "\n## Dependency changes\n\n" + strings.Join(depDiffSections, "")
	}
	path := filepath.Join(o.Dir, p.Config.Dist, "CHANGELOG.md")
	if o.DryRun {
		// Generated, so a broken changelog config still fails the dry run,
		// but not written: the path is only what the echoed commands name.
		fmt.Fprintf(progress, "==> changelog would be written to %s\n", path)
		return path, nil
	}
	if err := writeDistFile(path, []byte(notes)); err != nil {
		return "", fmt.Errorf("write changelog: %w", err)
	}
	fmt.Fprintf(progress, "==> changelog written to %s\n", path)
	return path, nil
}

// mkdirDist and writeDistFile create dist/ and the files under it. They keep
// the conventional 0755/0644 rather than gosec's 0750/0600 on purpose: the
// image runs as root, so `docker run -v "$PWD:/src"` leaves dist/ owned by
// root, and owner-only modes would make the release's outputs (changelog,
// summary, split digests) unreadable to the host user and to the CI step that
// uploads them. Nothing written there is secret.
func mkdirDist(dir string) error {
	return os.MkdirAll(dir, 0o755) //nolint:gosec // see above: dist/ must be readable by non-owners
}

func writeDistFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644) //nolint:gosec // see mkdirDist
}

// Merge is the second half of a split release: it stitches the per-arch
// digests the split legs pushed (dist/digests/) into one manifest list per
// image, then runs the full release tail on the merged artifact — tagging it
// only once the gates have passed.
func Merge(o Options) error {
	o.FromDigests = true
	return Release(o)
}

// emitSummary writes the release report: a GitHub Actions job-summary table
// (when running in Actions), a JSON artifact under dist/, and — under
// --output json — the JSON document to stdout.
//
// A dry run writes none of those files, the Actions ones included:
// $GITHUB_OUTPUT would hand a later step placeholder digests as if they had
// been pushed. --output json still prints the document.
func emitSummary(o Options, p *Prepared, result summary.Result) error {
	if !o.DryRun {
		if err := result.WriteGitHubStepSummary(); err != nil {
			fmt.Fprintf(progress, "warning: could not write GitHub step summary: %v\n", err)
		}
		if err := result.WriteGitHubOutput(); err != nil {
			fmt.Fprintf(progress, "warning: could not write GitHub summary output: %v\n", err)
		}
	}
	data, err := result.JSON()
	if err != nil {
		return err
	}
	if !o.DryRun {
		out := filepath.Join(o.Dir, p.Config.Dist, "release-summary.json")
		if err := writeDistFile(out, append(data, '\n')); err != nil {
			return fmt.Errorf("write release summary: %w", err)
		}
		fmt.Fprintf(progress, "==> summary written to %s\n", out)
	}
	if o.OutputJSON {
		fmt.Fprintln(os.Stdout, string(data))
	}
	return nil
}

// publishRelease creates a GitHub release and posts announcements per config.
// refs are the references this run actually pushed.
func publishRelease(r *run.Runner, p *Prepared, changelogPath string, refs []string) error {
	// Named after the computed version, never after whatever tag git can
	// reach: on an untagged HEAD that tag is a previous release, and a non-git
	// strategy computes a version the tag on HEAD need not match.
	tag := p.Git.ReleaseTag(p.Ctx.Version)

	if p.Config.Release.GitHub.Enabled {
		notes := changelogPath
		if notes == "" {
			return fmt.Errorf("github release needs changelog notes; enable changelog or drop --skip-changelog")
		}
		var assets []string // SBOMs, if generated, make good release assets
		title := fmt.Sprintf("%s %s", p.Config.ProjectName, p.Ctx.Version)
		if err := publish.GitHubRelease(r, p.Config.Release.GitHub, tag, p.Git.Commit, title, notes, p.Ctx.IsPrerelease(), assets); err != nil {
			return err
		}
		fmt.Fprintf(progress, "==> GitHub release %s created\n", tag)
	}

	if p.Config.Announce.Slack.Enabled || p.Config.Announce.Discord.Enabled {
		body, err := announceBody(p)
		if err != nil {
			return err
		}
		msg := publish.Message{
			ProjectName: p.Config.ProjectName,
			Version:     p.Ctx.Version,
			Tag:         tag,
			Refs:        refs,
			Body:        body,
		}
		if err := publish.Announce(r, p.Config.Announce, msg); err != nil {
			return err
		}
		fmt.Fprintln(progress, "==> release announced")
	}
	return nil
}

// dependencyDiff obtains the previous release's SBOM (by running syft against
// the previous version's image) and diffs it against the current SBOM at
// currentPath. Best-effort: if the previous image or SBOM is unavailable it logs
// and returns "", rather than failing the release.
func dependencyDiff(r *run.Runner, p *Prepared, plan ImagePlan, currentPath string) string {
	format := p.Config.SBOM.Format
	curData, err := os.ReadFile(filepath.Clean(currentPath))
	if err != nil {
		fmt.Fprintf(progress, "    (dependency diff skipped: %v)\n", err)
		return ""
	}
	prevVersion := strings.TrimPrefix(p.Git.PreviousTag, "v")
	prevRef := plan.Repos[0] + ":" + prevVersion
	prevOut, err := r.Capture("syft", prevRef, "-o", format)
	if err != nil {
		fmt.Fprintf(progress, "    (dependency diff skipped for %s: previous image %s not scannable)\n", plan.Image.ID, prevRef)
		return ""
	}
	curPkgs, err := sbomdiff.Packages(curData, format)
	if err != nil {
		fmt.Fprintf(progress, "    (dependency diff skipped: %v)\n", err)
		return ""
	}
	prevPkgs, err := sbomdiff.Packages([]byte(prevOut), format)
	if err != nil {
		fmt.Fprintf(progress, "    (dependency diff skipped: %v)\n", err)
		return ""
	}
	res := sbomdiff.Diff(prevPkgs, curPkgs)
	heading := fmt.Sprintf("%s (since %s)", plan.Image.ID, p.Git.PreviousTag)
	md := res.Markdown(heading)
	if md == "" {
		md = fmt.Sprintf("### %s (since %s)\n\n_No dependency changes._\n\n", plan.Image.ID, p.Git.PreviousTag)
	}
	return md
}

// defaultAnnounceTemplate is used when a webhook has no template of its own.
const defaultAnnounceTemplate = "🚀 {{ .ProjectName }} {{ .Version }} released"

// announceBody renders the announcement text, preferring a configured template
// (Slack's, then Discord's) over the default.
func announceBody(p *Prepared) (string, error) {
	tpl := defaultAnnounceTemplate
	if t := p.Config.Announce.Slack.Template; t != "" {
		tpl = t
	} else if t := p.Config.Announce.Discord.Template; t != "" {
		tpl = t
	}
	return tmpl.Render(tpl, p.Ctx)
}

// buildGroup builds one artifact for a group of images that share an identical
// build spec, pushing it to every member's repositories/tags in a single buildx
// invocation, then runs the post-build stages once (the artifact is shared) and
// signs every member repository by digest. It returns one summary entry per
// member. Safe to call concurrently.
func buildGroup(o Options, p *Prepared, r *run.Runner, grp []imageEval) ([]summary.Image, string, error) {
	released, err := releasedMembers(o, p, r, grp)
	if err != nil {
		return groupSummaries(o, p, grp), "", err
	}
	if len(released) == 0 {
		return buildGroupMembers(o, p, r, grp)
	}
	// Members whose commit tags already exist from this commit were released
	// by an earlier run: rebuild nothing for them and tag nothing, but report
	// them so their release markers still advance.
	irs := groupSummaries(o, p, grp)
	var out []summary.Image
	var rest []imageEval
	for i, m := range grp {
		at, ok := released[m.plan.Image.ID]
		if !ok {
			rest = append(rest, m)
			continue
		}
		fmt.Fprintf(progress, "==> %s already released from this commit (%s exists); not rebuilding\n", m.plan.Image.ID, at.Ref)
		ir := irs[i]
		ir.Skipped, ir.AlreadyReleased, ir.Pushed = true, true, false
		ir.Signed, ir.SBOM, ir.Provenance, ir.Tested = false, false, false, false
		ir.Reason = "already released: " + at.Ref + " exists"
		ir.Digest = at.Digest
		out = append(out, ir)
	}
	if len(rest) == 0 {
		return out, "", nil
	}
	built, dep, err := buildGroupMembers(o, p, r, rest)
	return append(out, built...), dep, err
}

// buildGroupMembers is buildGroup once the already-released members are out.
func buildGroupMembers(o Options, p *Prepared, r *run.Runner, grp []imageEval) ([]summary.Image, string, error) {
	plans := make([]ImagePlan, len(grp))
	for i, m := range grp {
		plans[i] = m.plan
	}
	rep := plans[0]
	refs, repos := unionRefsRepos(plans)
	floating := unionFloating(plans)
	irs := groupSummaries(o, p, grp)

	label := rep.Image.ID
	if len(plans) > 1 {
		label = fmt.Sprintf("%s (+%d)", rep.Image.ID, len(plans)-1)
	}

	// Split leg: build the given platform(s) natively, push untagged by
	// digest, record the digest for the merge run, and stop — every stage
	// below operates on the manifest list the merge run creates.
	if len(o.SplitPlatforms) > 0 {
		return irs, "", buildSplitLeg(o, p, r, grp, label, repos, irs)
	}

	digest, err := buildOrMerge(o, p, r, rep, label, refs, repos)
	if err != nil {
		return irs, "", err
	}
	digest = dryRunDigest(digest, o.DryRun)
	for i := range irs {
		irs[i].Digest = digest
	}

	// With --no-push there is no published artifact, so every stage that
	// operates on the pushed digest is skipped.
	if o.NoPush {
		return irs, "", nil
	}
	depSection, err := postBuild(o, p, r, rep, repos, digest, irs)
	if err != nil {
		return irs, "", err
	}
	// Only now, with every gate passed and the digest signed, do the tags
	// appear. Until this point the artifact exists in the registries by
	// digest alone, so a failed gate leaves nothing for a consumer to pull by
	// name.
	commit, short := headCommit(p)
	return irs, depSection, applyTags(r, repos, orderRefsForTagging(refs, floating, commit, short), digest)
}

// applyTags points every published tag at the already-pushed digest, one
// `imagetools create` per repository. --prefer-index=false makes each one a
// carbon copy: without it, buildx wraps a single-platform manifest in a fresh
// index, so the tag would name a different digest from the one that was
// scanned, tested and signed.
func applyTags(r *run.Runner, repos, refs []string, digest string) error {
	for _, repo := range repos {
		tags := refsForRepo(repo, refs)
		if len(tags) == 0 {
			continue
		}
		args := imagetoolsCreate("--prefer-index=false")
		for _, t := range tags {
			args = append(args, "--tag", t)
		}
		args = append(args, digestRef(repo, digest))
		if err := r.Run("docker", args...); err != nil {
			return fmt.Errorf("tag %s: %w", repo, err)
		}
	}
	return nil
}

// refsForRepo returns the refs that belong to repo.
func refsForRepo(repo string, refs []string) []string {
	var out []string
	for _, ref := range refs {
		if strings.HasPrefix(ref, repo+":") {
			out = append(out, ref)
		}
	}
	return out
}

// unionRefsRepos returns the union of every plan's refs and repos (deduped,
// order-stable).
func unionRefsRepos(plans []ImagePlan) (refs, repos []string) {
	for _, plan := range plans {
		refs = append(refs, plan.Refs...)
		repos = append(repos, plan.Repos...)
	}
	return dedupeStrings(refs), dedupeStrings(repos)
}

// unionFloating is the set of floating refs across a group's plans.
func unionFloating(plans []ImagePlan) map[string]bool {
	out := map[string]bool{}
	for _, plan := range plans {
		for ref := range plan.Floating {
			out[ref] = true
		}
	}
	return out
}

// groupSummaries returns the summary entry for each group member, recording
// which stages this run will apply to it.
func groupSummaries(o Options, p *Prepared, grp []imageEval) []summary.Image {
	split := len(o.SplitPlatforms) > 0
	irs := make([]summary.Image, len(grp))
	for i, m := range grp {
		plan := m.plan
		irs[i] = summary.Image{
			ID:           plan.Image.ID,
			Version:      plan.Version,
			Refs:         plan.Refs,
			Repositories: plan.Repos,
			Pushed:       !o.NoPush && !o.DryRun,
			Reason:       m.reason,
			Signed:       !split && !o.NoPush && !o.SkipSign && p.Config.Sign.Cosign.Enabled,
			SBOM:         !split && !o.NoPush && !o.SkipSBOM && p.Config.SBOM.Enabled,
			Provenance:   !o.NoPush && p.Config.Provenance.Enabled,
			Tested:       !split && !o.NoPush && !o.SkipTest && p.Config.Test.Enabled,
		}
		if split {
			// A split leg pushes by digest only — no tagged refs exist yet.
			irs[i].Refs = nil
		}
	}
	return irs
}

// dryRunDigest stands in for the digest a dry run never learns.
func dryRunDigest(digest string, dryRun bool) string {
	if digest == "" && dryRun {
		return "sha256:<digest-resolved-at-build-time>"
	}
	return digest
}

// buildSplitLeg builds the leg's platform(s), pushes them untagged by digest,
// and records the digest under dist/digests/ for the merge run.
func buildSplitLeg(o Options, p *Prepared, r *run.Runner, grp []imageEval, label string, repos []string, irs []summary.Image) error {
	// Only the leg's platforms the image is configured for: splitLegGroups
	// already dropped the groups with none, so this is never empty.
	platforms := legPlatforms(o.SplitPlatforms, grp[0].plan.Image.Platforms)
	if len(platforms) == 0 {
		// An empty list would drop --platform and build the host's default.
		return fmt.Errorf("image %s is not configured for split platform(s) %s", grp[0].plan.Image.ID, strings.Join(o.SplitPlatforms, ","))
	}
	fmt.Fprintf(progress, "==> building %s (%s, by digest)\n", label, strings.Join(platforms, ","))
	spec := toSpec(grp[0].plan, o.Dir, true, false, p.Config.Provenance)
	spec.Platforms = platforms
	spec.PushByDigest = true
	spec.Refs = repos // untagged: bare repo names
	digest, err := builder.Build(r, spec)
	if err != nil {
		return err
	}
	digest = dryRunDigest(digest, o.DryRun)
	for i := range irs {
		irs[i].Digest = digest
	}
	if o.DryRun {
		return nil
	}
	return writeSplitDigest(o.Dir, p.Config.Dist, evalIDs(grp), platforms, digest)
}

// buildOrMerge produces the group's artifact and returns its digest: built and
// pushed untagged, by digest, to every member repository, or — in merge mode —
// assembled from the per-arch digests the split legs already pushed. Nothing
// is tagged here; applyTags does that once the gates have passed. Under
// --no-push the build only validates and returns no digest.
func buildOrMerge(o Options, p *Prepared, r *run.Runner, rep ImagePlan, label string, refs, repos []string) (string, error) {
	verb := "building"
	if o.FromDigests {
		verb = "merging"
	}
	fmt.Fprintf(progress, "==> %s %s\n", verb, label)
	for _, ref := range refs {
		fmt.Fprintf(progress, "    - %s\n", ref)
	}
	if o.FromDigests {
		// Merge mode: the split legs already built and pushed per-arch images
		// by digest; assemble them into one untagged manifest list per repo.
		return mergeGroup(r, o, rep, p.Config.Dist, repos)
	}
	spec := toSpec(rep, o.Dir, !o.NoPush, false, p.Config.Provenance)
	spec.Refs = refs
	if !o.NoPush {
		// Push the one build untagged to every member repository; the tags
		// follow the gates.
		spec.PushByDigest = true
		spec.Refs = repos
	}
	return builder.Build(r, spec)
}

// postBuild runs the stages that operate on the pushed (still untagged)
// digest, gates first: scan, smoke test, sign, then SBOM. It returns the
// dependency-diff section, if one was produced. Signing and attesting happen
// before tagging, so a tag never names an unsigned image, not even briefly.
func postBuild(o Options, p *Prepared, r *run.Runner, rep ImagePlan, repos []string, digest string, irs []summary.Image) (string, error) {
	ref := digestRef(repos[0], digest)

	// Scan before signing: never sign or ship an image that fails the gate. One
	// artifact → scan once.
	if !o.SkipScan && p.Config.Scan.Enabled {
		if err := scanGate(o, p, r, rep.Image.ID, ref, irs); err != nil {
			return "", err
		}
	}

	// Smoke test the shared artifact once.
	if !o.SkipTest && p.Config.Test.Enabled {
		fmt.Fprintf(progress, "    smoke test %s: docker run %s\n", rep.Image.ID, strings.Join(p.Config.Test.Cmd, " "))
		if err := tester.Run(r, p.Config.Test, ref); err != nil {
			return "", fmt.Errorf("smoke test gate failed: %w", err)
		}
	}

	// Sign every member repository by digest.
	if !o.SkipSign {
		if err := signer.Sign(r, p.Config.Sign.Cosign, repos, digest); err != nil {
			return "", err
		}
	}

	if o.SkipSBOM || !p.Config.SBOM.Enabled {
		return "", nil
	}
	return sbomStage(o, p, r, rep, repos, digest)
}

// scanGate scans the artifact at ref, records the counts on every member's
// summary, and fails when the findings breach scan.fail_on.
func scanGate(o Options, p *Prepared, r *run.Runner, id, ref string, irs []summary.Image) error {
	res, err := scanner.Scan(r, p.Config.Scan, filepath.Join(o.Dir, p.Config.Dist), id, ref)
	if err != nil {
		return err
	}
	if res == nil {
		return nil
	}
	for _, w := range res.ExpiredWarnings() {
		fmt.Fprintf(progress, "    warning: %s\n", w)
	}
	if o.DryRun {
		return nil
	}
	for i := range irs {
		irs[i].Vulns = res.Counts
	}
	fmt.Fprintf(progress, "    scan %s (%s): %s\n", id, res.Scanner, res.Summary())
	if err := res.GateError(p.Config.Scan.FailOn); err != nil {
		return fmt.Errorf("vulnerability gate failed: %w", err)
	}
	return nil
}

// sbomStage generates the SBOM, attests it when signing is on, and returns the
// dependency diff against the previous release when one is configured.
func sbomStage(o Options, p *Prepared, r *run.Runner, rep ImagePlan, repos []string, digest string) (string, error) {
	ref := digestRef(repos[0], digest)
	sbomPath, err := sbom.Generate(r, p.Config.SBOM, filepath.Join(o.Dir, p.Config.Dist), rep.Image.ID, ref)
	if err != nil {
		return "", err
	}
	if sbomPath == "" {
		return "", nil
	}
	if p.Config.SBOM.Attest && !o.SkipSign && p.Config.Sign.Cosign.Enabled {
		pt := sbom.PredicateType(p.Config.SBOM.Format)
		if err := signer.Attest(r, p.Config.Sign.Cosign, repos, digest, sbomPath, pt); err != nil {
			return "", err
		}
	}
	if p.Config.Changelog.DependencyDiff && !o.DryRun && p.Git.PreviousTag != "" {
		return dependencyDiff(r, p, rep, sbomPath), nil
	}
	return "", nil
}

// buildKey hashes the parts of an image plan that determine the built artifact,
// so images with an identical build (differing only by destination repo/tag)
// group together and build once. Repositories, tags, and cache settings are
// excluded — they don't change the artifact.
func buildKey(dir string, plan ImagePlan) string {
	h := sha256.New()
	fmt.Fprintf(h, "dockerfile=%s\n", abs(dir, plan.Image.Dockerfile))
	fmt.Fprintf(h, "context=%s\n", abs(dir, plan.Image.Context))
	fmt.Fprintf(h, "target=%s\n", plan.Image.Target)
	plats := append([]string(nil), plan.Image.Platforms...)
	sort.Strings(plats)
	fmt.Fprintf(h, "platforms=%s\n", strings.Join(plats, ","))
	args := append([]string(nil), plan.BuildArgs...)
	sort.Strings(args)
	for _, a := range args {
		fmt.Fprintf(h, "arg=%s\n", a)
	}
	lkeys := make([]string, 0, len(plan.Labels))
	for k := range plan.Labels {
		lkeys = append(lkeys, k)
	}
	sort.Strings(lkeys)
	for _, k := range lkeys {
		fmt.Fprintf(h, "label=%s=%s\n", k, plan.Labels[k])
	}
	for _, s := range plan.Image.Secrets {
		fmt.Fprintf(h, "secret=%s\n", s.ID)
	}
	for _, f := range plan.Image.ExtraFlags {
		fmt.Fprintf(h, "flag=%s\n", f)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// builtRefs flattens the published references of the images this run built.
// A skipped image's plan still carries refs for the version it would have
// had, but nothing was pushed under them.
func builtRefs(images []summary.Image) []string {
	var refs []string
	for _, im := range images {
		if !im.Skipped {
			refs = append(refs, im.Refs...)
		}
	}
	return refs
}

// localPlatform picks the one platform a local --load build uses: the host's
// architecture when the image is built for it, so an Apple Silicon machine
// does not emulate amd64 for its inner loop, and otherwise the first listed.
func localPlatform(platforms []string, goarch string) string {
	for _, p := range platforms {
		goos, arch, _ := strings.Cut(p, "/")
		if goos == "linux" && (arch == goarch || strings.HasPrefix(arch, goarch+"/")) {
			return p
		}
	}
	return platforms[0]
}

// Build runs a local build (single platform, --load) without publishing.
func Build(o Options) error {
	p, err := Prepare(o)
	if err != nil {
		return err
	}
	if !o.DryRun {
		// Local builds only need the build toolchain, never cosign/syft.
		if err := checkTools(o.context(), p.Config, preflight.Opts{}); err != nil {
			return err
		}
	}
	r := run.New(o.context(), o.DryRun, o.Verbose)
	for _, plan := range p.Plans {
		// A local --load build cannot handle a manifest list, so pick one platform.
		spec := toSpec(plan, o.Dir, false, true, config.Provenance{})
		if len(spec.Platforms) > 1 {
			spec.Platforms = []string{localPlatform(spec.Platforms, runtime.GOARCH)}
		}
		fmt.Fprintf(progress, "==> building %s (local, %s)\n", plan.Image.ID, strings.Join(spec.Platforms, ","))
		for _, ref := range spec.Refs {
			fmt.Fprintf(progress, "    - %s\n", ref)
		}
		if _, err := builder.Build(r, spec); err != nil {
			return err
		}
	}
	return nil
}

func toSpec(plan ImagePlan, dir string, push, load bool, prov config.Provenance) builder.Spec {
	return builder.Spec{
		ID:              plan.Image.ID,
		Dockerfile:      abs(dir, plan.Image.Dockerfile),
		Context:         abs(dir, plan.Image.Context),
		Target:          plan.Image.Target,
		Platforms:       plan.Image.Platforms,
		BuildArgs:       plan.BuildArgs,
		Labels:          plan.Labels,
		Secrets:         plan.Image.Secrets,
		Refs:            plan.Refs,
		Push:            push,
		Load:            load,
		Provenance:      prov.Enabled,
		ProvenanceMode:  prov.Mode,
		CacheFrom:       plan.CacheFrom,
		CacheTo:         plan.CacheTo,
		ExtraFlags:      plan.Image.ExtraFlags,
		SourceDateEpoch: plan.SourceDateEpoch,
	}
}

// resolveVersion derives the release version using the configured strategy. The
// crane/command hooks are read-only queries, so they run even in dry-run mode to
// give `check` and dry-runs an accurate preview.
func resolveVersion(cfg *config.Config, gi *gitinfo.Info, o Options) (string, error) {
	r := run.New(o.context(), o.DryRun, o.Verbose)

	// Anchor the release-level version on the first *selected* image so a
	// matrix job (`release --only ...`) never queries a repository outside its
	// slice of the config.
	first := firstSelectedImage(cfg, o.Only)
	var defaultRepo string
	if first != nil && len(first.Repositories) > 0 {
		defaultRepo = first.Repositories[0]
	}

	vcfg := cfg.Versioning
	// Registry versioning with the anchor image pinned: the pin is the
	// release version — no registry read needed at all. Under a pinned
	// versioning.repo every image shares one version, so the pins agree.
	if isRegistryStrategy(vcfg.Strategy) && first != nil {
		if pin, ok := o.PinVersions[first.ID]; ok {
			return pin, nil
		}
	}

	repo := vcfg.Repo
	if repo == "" {
		repo = defaultRepo
	}
	// The registry/ecr strategies query a repo by name, so render any templating
	// (e.g. {{ .Env.REGISTRY }}) to a concrete reference before it is queried.
	if isRegistryStrategy(vcfg.Strategy) && repo != "" {
		now := o.Now
		if now.IsZero() {
			now = time.Now()
		}
		rctx := tmpl.NewContext(cfg.ProjectName, cfg.DefaultBranch, gi, o.Snapshot, now, envMap())
		rendered, err := tmpl.Render(repo, rctx)
		if err != nil {
			return "", fmt.Errorf("render versioning repo %q: %w", repo, err)
		}
		repo = rendered
		vcfg.Repo = "" // resolved into Input.Repo below
	}

	ver, err := versioner.Resolve(versioner.Input{
		Cfg:      vcfg,
		Git:      gi,
		Snapshot: o.Snapshot,
		Repo:     repo,
		Getenv:   os.Getenv,
		ListTags: tagLister(cfg, r),
		RunCmd: func(command string) (string, error) {
			return r.Capture("sh", "-c", command)
		},
	})
	if err != nil && o.SoftVersion {
		fmt.Fprintf(os.Stderr, "warning: could not resolve version (%v); showing placeholder\n", err)
		return unresolvedVersion, nil
	}
	return ver, err
}

// unresolvedVersion is the placeholder shown by `check` when a registry/ecr
// version can't be resolved (e.g. offline or unauthenticated).
const unresolvedVersion = "0.0.0-unresolved"

// The versioning strategies that list a repository's tags.
const (
	strategyRegistry = "registry"
	strategyECR      = "ecr"
)

// isRegistryStrategy reports whether the strategy derives the version by listing
// a repository's tags.
func isRegistryStrategy(s string) bool {
	return s == strategyRegistry || s == strategyECR
}

// tagLister returns the tag-listing function for the configured strategy: crane
// for "registry", the aws CLI for "ecr".
func tagLister(cfg *config.Config, r *run.Runner) func(string) ([]string, error) {
	if cfg.Versioning.Strategy == strategyECR {
		region := cfg.Versioning.Region
		return func(repoURI string) ([]string, error) {
			name, reg := parseECRRepo(repoURI)
			if region != "" {
				reg = region
			}
			args := []string{
				"ecr", "describe-images", "--repository-name", name,
				"--query", "imageDetails[].imageTags[]", "--output", "text",
			}
			if reg != "" {
				args = append(args, "--region", reg)
			}
			out, err := r.Capture("aws", args...)
			if err != nil {
				return nil, err
			}
			return strings.Fields(out), nil
		}
	}
	return func(repo string) ([]string, error) {
		out, err := r.Capture("crane", "ls", repo)
		if err != nil {
			return nil, err
		}
		return splitLines(out), nil
	}
}

// parseECRRepo splits an ECR repository URI into its repository name and region.
// e.g. 123.dkr.ecr.us-east-1.amazonaws.com/acme/checkout -> ("acme/checkout", "us-east-1").
func parseECRRepo(uri string) (name, region string) {
	host := uri
	if h, n, ok := strings.Cut(uri, "/"); ok {
		host, name = h, n
	} else {
		name = uri
	}
	if _, rest, ok := strings.Cut(host, ".ecr."); ok {
		if reg, _, ok := strings.Cut(rest, "."); ok {
			region = reg
		}
	}
	return name, region
}

func splitLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// checkTools verifies the external tools this run needs are on PATH, failing
// early with install hints rather than partway through the pipeline.
func checkTools(ctx context.Context, cfg *config.Config, o preflight.Opts) error {
	return preflight.Verify(preflight.Check(ctx, preflight.Requirements(cfg, o)))
}

func guardReleasable(gi *gitinfo.Info, strategy string) error {
	if gi.Dirty {
		return fmt.Errorf("working tree is dirty; commit changes or use --snapshot")
	}
	// Only the git strategy needs a tag on HEAD to source the version; the other
	// strategies derive it elsewhere (registry, static, env, command). A tag
	// further back does not count: it names a release already cut from another
	// commit, and reusing it would overwrite that release's image tags.
	if (strategy == "git" || strategy == "") && gi.Tag == "" {
		if gi.LatestTag != "" {
			return fmt.Errorf("no git tag on HEAD (latest reachable tag %s is on an earlier commit); tag a release, switch versioning.strategy, or use --snapshot", gi.LatestTag)
		}
		return fmt.Errorf("no git tag on HEAD; tag a release, switch versioning.strategy, or use --snapshot")
	}
	return nil
}

// guardDefaultBranch refuses a real release from a commit that is not on the
// default branch. The tag and clean-tree guards say nothing about where a
// commit came from, so without this a manual dispatch from a feature branch
// publishes real versions of every image from code nobody merged.
func guardDefaultBranch(o Options, branch string) error {
	if o.AllowNonDefaultBranch {
		return nil
	}
	ref, err := gitinfo.CheckReachable(o.context(), o.Dir, branch)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gitinfo.ErrNotOnBranch):
		return fmt.Errorf("HEAD is not on the default branch (%s); release from a merged commit, use --snapshot, or pass --allow-non-default-branch", ref)
	case errors.Is(err, gitinfo.ErrShallow):
		return fmt.Errorf("cannot tell whether HEAD is on the default branch (%s): the clone is shallow; fetch full history (actions/checkout `fetch-depth: 0`, or `git fetch --unshallow`), or pass --allow-non-default-branch", ref)
	default:
		return fmt.Errorf("check HEAD is on the default branch: %w; set default_branch, or pass --allow-non-default-branch", err)
	}
}

func digestRef(repo, digest string) string {
	if digest == "" {
		return repo
	}
	return repo + "@" + digest
}

func abs(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

func envMap() map[string]string {
	m := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}
