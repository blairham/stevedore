// Package pipeline orchestrates the build/push/sign/sbom/changelog stages.
package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	SkipPublish   bool   // skip GitHub release + announce
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
	Now         time.Time
}

// ImagePlan is a fully-resolved plan for one image.
type ImagePlan struct {
	Image     config.Image
	Repos     []string
	Refs      []string // repo:tag cartesian product actually published
	BuildArgs []string
	Labels    map[string]string
	CacheFrom []string
	CacheTo   []string
	// Paths are the resolved dependency globs for change detection (per-image
	// Paths plus any graph-resolved directories). Empty means unscoped.
	Paths []string
	// Version is the version this image was tagged with — its own under per-image
	// registry versioning, otherwise the release version.
	Version string
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
	cfg, err := config.Load(o.ConfigPath)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	gi, err := gitinfo.Gather(o.Dir)
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
	versionFor, err := imageVersionResolver(cfg, gi, o, ctx)
	if err != nil {
		return nil, err
	}
	if err := validateImageIDs(cfg, o); err != nil {
		return nil, err
	}
	plans, err := resolvePlans(cfg, ctx, o.Snapshot, versionFor, o.PinVersions)
	if err != nil {
		return nil, err
	}
	plans = filterPlans(plans, o.Only)
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

// imageVersionResolver returns a per-image version resolver for the registry
// strategy: each image's version comes from its own repository (highest semver +
// bump). It returns nil for the other strategies, where one version applies to
// every image. When versioning.repo is pinned, all images resolve from that one
// repo (i.e. a unified version).
func imageVersionResolver(cfg *config.Config, gi *gitinfo.Info, o Options, ctx *tmpl.Context) (func(string) (string, error), error) {
	if !isRegistryStrategy(cfg.Versioning.Strategy) {
		return nil, nil
	}
	vcfg := cfg.Versioning
	if vcfg.Repo != "" {
		rendered, err := tmpl.Render(vcfg.Repo, ctx)
		if err != nil {
			return nil, fmt.Errorf("render versioning repo %q: %w", vcfg.Repo, err)
		}
		vcfg.Repo = rendered
	}
	r := run.New(o.DryRun, o.Verbose)
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

// filterPlans restricts plans to the given image IDs (order preserved from the
// config). A nil/empty filter returns plans unchanged.
func filterPlans(plans []ImagePlan, only []string) []ImagePlan {
	if len(only) == 0 {
		return plans
	}
	want := map[string]bool{}
	for _, id := range only {
		want[id] = true
	}
	var out []ImagePlan
	for _, p := range plans {
		if want[p.Image.ID] {
			out = append(out, p)
		}
	}
	return out
}

// resolvePlans renders tags/repos/build-args/labels and computes the published
// references. Floating "latest" tags are dropped off the default branch or in a
// snapshot build. A pinned version (pins[id]) overrides resolution for that
// image entirely.
func resolvePlans(cfg *config.Config, ctx *tmpl.Context, snapshot bool, versionFor func(string) (string, error), pins map[string]string) ([]ImagePlan, error) {
	var plans []ImagePlan
	for _, img := range cfg.Images {
		// Repositories are rendered with the release context (they key on .Env,
		// not .Version), and drive per-image version resolution.
		repos, err := tmpl.RenderAll(img.Repositories, ctx)
		if err != nil {
			return nil, fmt.Errorf("image %s repositories: %w", img.ID, err)
		}

		// Determine this image's version. A pin (from the plan) wins outright;
		// under per-image registry versioning it comes from the image's own
		// repo; otherwise it's the release version.
		imgCtx := ctx
		imgVersion := ctx.Version
		if pin, ok := pins[img.ID]; ok {
			imgVersion = pin
			imgCtx = ctx.WithVersion(pin)
		} else if versionFor != nil && len(repos) > 0 {
			v, err := versionFor(repos[0])
			if err != nil {
				return nil, fmt.Errorf("image %s version: %w", img.ID, err)
			}
			imgVersion = v
			imgCtx = ctx.WithVersion(v)
		}

		tags, err := tmpl.RenderAll(img.Tags, imgCtx)
		if err != nil {
			return nil, fmt.Errorf("image %s tags: %w", img.ID, err)
		}
		buildArgs, err := tmpl.RenderAll(img.BuildArgs, imgCtx)
		if err != nil {
			return nil, fmt.Errorf("image %s build_args: %w", img.ID, err)
		}
		cacheFrom, err := tmpl.RenderAll(img.CacheFrom, imgCtx)
		if err != nil {
			return nil, fmt.Errorf("image %s cache_from: %w", img.ID, err)
		}
		cacheTo, err := tmpl.RenderAll(img.CacheTo, imgCtx)
		if err != nil {
			return nil, fmt.Errorf("image %s cache_to: %w", img.ID, err)
		}
		labels := map[string]string{}
		for k, v := range img.Labels {
			rv, err := tmpl.Render(v, imgCtx)
			if err != nil {
				return nil, fmt.Errorf("image %s label %s: %w", img.ID, k, err)
			}
			labels[k] = rv
		}

		var refs []string
		for _, repo := range repos {
			for _, tag := range tags {
				if isFloating(tag) && (snapshot || !imgCtx.IsDefault) {
					continue
				}
				refs = append(refs, repo+":"+tag)
			}
		}
		if len(refs) == 0 {
			return nil, fmt.Errorf("image %s: no publishable references after tag resolution", img.ID)
		}
		plans = append(plans, ImagePlan{
			Image:     img,
			Repos:     repos,
			Refs:      refs,
			BuildArgs: buildArgs,
			Labels:    labels,
			CacheFrom: cacheFrom,
			CacheTo:   cacheTo,
			Version:   imgVersion,
		})
	}
	return plans, nil
}

// isFloating reports whether a tag is a mutable pointer that should only move on
// the default branch of a real release.
func isFloating(tag string) bool {
	t := strings.ToLower(tag)
	return t == "latest" || strings.HasSuffix(t, "-latest")
}

// Release runs the full pipeline: build+push, sign, SBOM, changelog. With
// NoPush it builds (honoring change detection) but publishes nothing and skips
// every stage that needs a pushed artifact (sign, SBOM, scan, provenance,
// GitHub release, announce).
func Release(o Options) error {
	o.Push = !o.NoPush
	if o.NoPush {
		// A validate-only build discards the tag, so a version that can't be
		// resolved (e.g. registry/ECR unreachable in CI) shouldn't fail the run.
		o.SoftVersion = true
	}
	if o.OutputJSON {
		// Keep stdout clean for the JSON document.
		progress = os.Stderr
		defer func() { progress = os.Stdout }()
	}
	p, err := Prepare(o)
	if err != nil {
		return err
	}
	result := summary.Result{Project: p.Config.ProjectName, Snapshot: o.Snapshot}
	if !o.Snapshot {
		if err := guardReleasable(p.Git, p.Config.Versioning.Strategy); err != nil {
			return err
		}
	}
	if !o.DryRun {
		ghRelease := !o.NoPush && !o.Snapshot && !o.SkipPublish && p.Config.Release.GitHub.Enabled
		// --no-push builds only; none of the push-dependent tools are required.
		opts := preflight.Opts{Sign: !o.NoPush && !o.SkipSign, SBOM: !o.NoPush && !o.SkipSBOM, Scan: !o.NoPush && !o.SkipScan, GitHubRelease: ghRelease}
		if err := checkTools(p.Config, opts); err != nil {
			return err
		}
	}
	r := run.New(o.DryRun, o.Verbose)

	if err := os.MkdirAll(filepath.Join(o.Dir, p.Config.Dist), 0o755); err != nil {
		return fmt.Errorf("create dist dir: %w", err)
	}

	// Fingerprint state drives --only-changed. It is maintained on every release
	// so the next --only-changed run has a baseline to compare against.
	fpPath := filepath.Join(o.Dir, p.Config.Dist, "fingerprints.json")
	state, err := fingerprint.Load(fpPath)
	if err != nil {
		return err
	}

	var depDiffSections []string
	cd := p.Config.ChangeDetection

	// Pre-pass: change detection + fingerprints, then group identical build
	// specs (shared with the plan command).
	evals, err := evaluateImages(o, p, state)
	if err != nil {
		return err
	}
	toBuild, skipped := groupPlans(o.Dir, evals)
	for _, m := range skipped {
		fmt.Fprintf(progress, "==> skipping %s (%s)\n", m.plan.Image.ID, m.reason)
		result.Images = append(result.Images, summary.Image{ID: m.plan.Image.ID, Skipped: true})
	}
	for _, grp := range toBuild {
		if len(grp) > 1 {
			fmt.Fprintf(progress, "==> %d images share one build: %s\n", len(grp), strings.Join(evalIDs(grp), ", "))
		}
	}

	// Build each group, up to o.Parallel groups at a time.
	workers := max(o.Parallel, 1)
	if len(toBuild) > 0 {
		workers = min(workers, len(toBuild))
	}
	if workers > 1 {
		fmt.Fprintf(progress, "==> building %d group(s), up to %d in parallel\n", len(toBuild), workers)
	}
	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
		sem      = make(chan struct{}, workers)
	)
	for _, grp := range toBuild {
		mu.Lock()
		stop := firstErr != nil
		mu.Unlock()
		if stop {
			break // a build already failed; stop dispatching more
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(grp []imageEval) {
			defer wg.Done()
			defer func() { <-sem }()
			plans := make([]ImagePlan, len(grp))
			for i, m := range grp {
				plans[i] = m.plan
			}
			irs, dep, err := buildGroup(o, p, r, plans)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s: %w", grp[0].plan.Image.ID, err)
				}
				return
			}
			result.Images = append(result.Images, irs...)
			for _, m := range grp {
				state[m.plan.Image.ID] = m.fp
			}
			if dep != "" {
				depDiffSections = append(depDiffSections, dep)
			}
		}(grp)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	sort.Slice(result.Images, func(i, j int) bool { return result.Images[i].ID < result.Images[j].ID })

	if !o.DryRun {
		if err := state.Save(fpPath); err != nil {
			return fmt.Errorf("save fingerprint state: %w", err)
		}
	}

	// Advance each built image's release marker to HEAD (and push it) so the
	// next run's change detection diffs against this release. Only on a real
	// push of a real release — but regardless of how THIS run selected its
	// images (marker diff, --changed-since, or --only): the marker records
	// "last released", not "last marker-diffed", so matrix jobs running
	// `release --only <id>` keep every image's baseline current.
	if cd.MarkerRefs && !o.DryRun && !o.NoPush && !o.Snapshot {
		for _, im := range result.Images {
			if im.Skipped {
				continue
			}
			ref := changed.MarkerRef(cd.MarkerPrefix, im.ID)
			if err := changed.AdvanceMarker(o.Dir, ref); err != nil {
				fmt.Fprintf(progress, "warning: advance release marker %s: %v\n", ref, err)
			} else {
				fmt.Fprintf(progress, "==> advanced release marker %s\n", ref)
			}
		}
	}

	changelogPath := ""
	if !o.SkipChangelog && p.Config.Changelog.Enabled {
		notes, err := changelog.Generate(p.Config.Changelog, p.Git, o.Dir)
		if err != nil {
			return err
		}
		if len(depDiffSections) > 0 {
			notes += "\n## Dependency changes\n\n" + strings.Join(depDiffSections, "")
		}
		changelogPath = filepath.Join(o.Dir, p.Config.Dist, "CHANGELOG.md")
		if err := os.WriteFile(changelogPath, []byte(notes), 0o644); err != nil {
			return fmt.Errorf("write changelog: %w", err)
		}
		fmt.Fprintf(progress, "==> changelog written to %s\n", changelogPath)
	}

	// Publishing (GitHub release + announce) runs only for real releases.
	if !o.NoPush && !o.Snapshot && !o.SkipPublish {
		if err := publishRelease(r, p, changelogPath); err != nil {
			return err
		}
	}

	if err := emitSummary(o, p, result); err != nil {
		return err
	}

	fmt.Fprintln(progress, "==> release complete")
	return nil
}

// emitSummary writes the release report: a GitHub Actions job-summary table
// (when running in Actions), a JSON artifact under dist/, and — under
// --output json — the JSON document to stdout.
func emitSummary(o Options, p *Prepared, result summary.Result) error {
	if err := result.WriteGitHubStepSummary(); err != nil {
		fmt.Fprintf(progress, "warning: could not write GitHub step summary: %v\n", err)
	}
	data, err := result.JSON()
	if err != nil {
		return err
	}
	if !o.DryRun {
		out := filepath.Join(o.Dir, p.Config.Dist, "release-summary.json")
		if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
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
func publishRelease(r *run.Runner, p *Prepared, changelogPath string) error {
	tag := p.Git.Tag
	if tag == "" {
		tag = "v" + p.Ctx.Version
	}

	if p.Config.Release.GitHub.Enabled {
		notes := changelogPath
		if notes == "" {
			return fmt.Errorf("github release needs changelog notes; enable changelog or drop --skip-changelog")
		}
		var assets []string // SBOMs, if generated, make good release assets
		title := fmt.Sprintf("%s %s", p.Config.ProjectName, p.Ctx.Version)
		if err := publish.GitHubRelease(r, p.Config.Release.GitHub, tag, title, notes, assets); err != nil {
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
			Refs:        allRefs(p.Plans),
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
	curData, err := os.ReadFile(currentPath)
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
func buildGroup(o Options, p *Prepared, r *run.Runner, plans []ImagePlan) ([]summary.Image, string, error) {
	rep := plans[0]

	// Union of every member's refs and repos (deduped, order-stable).
	var refs, repos []string
	seenRef, seenRepo := map[string]bool{}, map[string]bool{}
	for _, plan := range plans {
		for _, ref := range plan.Refs {
			if !seenRef[ref] {
				seenRef[ref] = true
				refs = append(refs, ref)
			}
		}
		for _, repo := range plan.Repos {
			if !seenRepo[repo] {
				seenRepo[repo] = true
				repos = append(repos, repo)
			}
		}
	}

	irs := make([]summary.Image, len(plans))
	for i, plan := range plans {
		irs[i] = summary.Image{
			ID:         plan.Image.ID,
			Version:    plan.Version,
			Refs:       plan.Refs,
			Signed:     !o.NoPush && !o.SkipSign && p.Config.Sign.Cosign.Enabled,
			SBOM:       !o.NoPush && !o.SkipSBOM && p.Config.SBOM.Enabled,
			Provenance: !o.NoPush && p.Config.Provenance.Enabled,
			Tested:     !o.NoPush && !o.SkipTest && p.Config.Test.Enabled,
		}
	}

	label := rep.Image.ID
	if len(plans) > 1 {
		label = fmt.Sprintf("%s (+%d)", rep.Image.ID, len(plans)-1)
	}
	fmt.Fprintf(progress, "==> building %s\n", label)
	for _, ref := range refs {
		fmt.Fprintf(progress, "    - %s\n", ref)
	}
	spec := toSpec(rep, o.Dir, !o.NoPush, false, p.Config.Provenance)
	spec.Refs = refs // push the one build to every member's tags
	digest, err := builder.Build(r, spec)
	if err != nil {
		return irs, "", err
	}
	if digest == "" && o.DryRun {
		digest = "sha256:<digest-resolved-at-build-time>"
	}
	for i := range irs {
		irs[i].Digest = digest
	}

	// With --no-push there is no published artifact, so every stage that
	// operates on the pushed digest is skipped.
	if o.NoPush {
		return irs, "", nil
	}

	// Scan before signing: never sign or ship an image that fails the gate. One
	// artifact → scan once.
	if !o.SkipScan && p.Config.Scan.Enabled {
		scanRef := digestRef(repos[0], digest)
		res, err := scanner.Scan(r, p.Config.Scan, filepath.Join(o.Dir, p.Config.Dist), rep.Image.ID, scanRef)
		if err != nil {
			return irs, "", err
		}
		if res != nil && !o.DryRun {
			for i := range irs {
				irs[i].Vulns = res.Counts
			}
			fmt.Fprintf(progress, "    scan %s (%s): %s\n", rep.Image.ID, res.Scanner, res.Summary())
			if err := res.GateError(p.Config.Scan.FailOn); err != nil {
				return irs, "", fmt.Errorf("vulnerability gate failed: %w", err)
			}
		}
	}

	// Smoke test the shared artifact once.
	if !o.SkipTest && p.Config.Test.Enabled {
		testRef := digestRef(repos[0], digest)
		fmt.Fprintf(progress, "    smoke test %s: docker run %s\n", rep.Image.ID, strings.Join(p.Config.Test.Cmd, " "))
		if err := tester.Run(r, p.Config.Test, testRef); err != nil {
			return irs, "", fmt.Errorf("smoke test gate failed: %w", err)
		}
	}

	// Sign every member repository by digest.
	if !o.SkipSign {
		if err := signer.Sign(r, p.Config.Sign.Cosign, repos, digest); err != nil {
			return irs, "", err
		}
	}

	depSection := ""
	if !o.SkipSBOM && p.Config.SBOM.Enabled {
		ref := digestRef(repos[0], digest)
		sbomPath, err := sbom.Generate(r, p.Config.SBOM, filepath.Join(o.Dir, p.Config.Dist), rep.Image.ID, ref)
		if err != nil {
			return irs, "", err
		}
		if p.Config.SBOM.Attest && !o.SkipSign && p.Config.Sign.Cosign.Enabled && sbomPath != "" {
			pt := sbom.PredicateType(p.Config.SBOM.Format)
			if err := signer.Attest(r, p.Config.Sign.Cosign, repos, digest, sbomPath, pt); err != nil {
				return irs, "", err
			}
		}
		if p.Config.Changelog.DependencyDiff && !o.DryRun && sbomPath != "" && p.Git.PreviousTag != "" {
			depSection = dependencyDiff(r, p, rep, sbomPath)
		}
	}
	return irs, depSection, nil
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

// allRefs flattens the published references across all image plans.
func allRefs(plans []ImagePlan) []string {
	var refs []string
	for _, plan := range plans {
		refs = append(refs, plan.Refs...)
	}
	return refs
}

// Build runs a local build (single platform, --load) without publishing.
func Build(o Options) error {
	p, err := Prepare(o)
	if err != nil {
		return err
	}
	if !o.DryRun {
		// Local builds only need the build toolchain, never cosign/syft.
		if err := checkTools(p.Config, preflight.Opts{}); err != nil {
			return err
		}
	}
	r := run.New(o.DryRun, o.Verbose)
	for _, plan := range p.Plans {
		// A local --load build cannot handle a manifest list, so pick one platform.
		spec := toSpec(plan, o.Dir, false, true, config.Provenance{})
		if len(spec.Platforms) > 1 {
			spec.Platforms = spec.Platforms[:1]
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
		ID:             plan.Image.ID,
		Dockerfile:     abs(dir, plan.Image.Dockerfile),
		Context:        abs(dir, plan.Image.Context),
		Target:         plan.Image.Target,
		Platforms:      plan.Image.Platforms,
		BuildArgs:      plan.BuildArgs,
		Labels:         plan.Labels,
		Secrets:        plan.Image.Secrets,
		Refs:           plan.Refs,
		Push:           push,
		Load:           load,
		Provenance:     prov.Enabled,
		ProvenanceMode: prov.Mode,
		CacheFrom:      plan.CacheFrom,
		CacheTo:        plan.CacheTo,
		ExtraFlags:     plan.Image.ExtraFlags,
	}
}

// resolveVersion derives the release version using the configured strategy. The
// crane/command hooks are read-only queries, so they run even in dry-run mode to
// give `check` and dry-runs an accurate preview.
func resolveVersion(cfg *config.Config, gi *gitinfo.Info, o Options) (string, error) {
	r := run.New(o.DryRun, o.Verbose)
	var defaultRepo string
	if len(cfg.Images) > 0 && len(cfg.Images[0].Repositories) > 0 {
		defaultRepo = cfg.Images[0].Repositories[0]
	}

	vcfg := cfg.Versioning
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

// isRegistryStrategy reports whether the strategy derives the version by listing
// a repository's tags.
func isRegistryStrategy(s string) bool {
	return s == "registry" || s == "ecr"
}

// tagLister returns the tag-listing function for the configured strategy: crane
// for "registry", the aws CLI for "ecr".
func tagLister(cfg *config.Config, r *run.Runner) func(string) ([]string, error) {
	if cfg.Versioning.Strategy == "ecr" {
		region := cfg.Versioning.Region
		return func(repoURI string) ([]string, error) {
			name, reg := parseECRRepo(repoURI)
			if region != "" {
				reg = region
			}
			args := []string{"ecr", "describe-images", "--repository-name", name,
				"--query", "imageDetails[].imageTags[]", "--output", "text"}
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
func checkTools(cfg *config.Config, o preflight.Opts) error {
	return preflight.Verify(preflight.Check(preflight.Requirements(cfg, o)))
}

func guardReleasable(gi *gitinfo.Info, strategy string) error {
	if gi.Dirty {
		return fmt.Errorf("working tree is dirty; commit changes or use --snapshot")
	}
	// Only the git strategy needs a tag on HEAD to source the version; the other
	// strategies derive it elsewhere (registry, static, env, command).
	if strategy == "git" && gi.Tag == "" {
		return fmt.Errorf("no git tag on HEAD; tag a release, switch versioning.strategy, or use --snapshot")
	}
	return nil
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
