// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package versioner derives the release version string from a configurable
// source: git tags, an existing registry's tags, a static value, an environment
// variable, or the output of a command.
package versioner

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/conventional"
	"github.com/blairham/stevedore/internal/gitinfo"
	"github.com/blairham/stevedore/internal/semver"
)

// Input bundles everything Resolve needs. The Getenv/ListTags/RunCmd/Commits
// hooks are injected so the resolver stays pure and testable.
type Input struct {
	Cfg      config.Versioning
	Git      *gitinfo.Info
	Snapshot bool
	// Repo is the default repository for the registry strategy (typically the
	// first repository of the first image).
	Repo string
	// Getenv reads an environment variable (defaults to os.Getenv when nil).
	Getenv func(string) string
	// ListTags lists the tags of a repository (required for the registry
	// strategy).
	ListTags func(repo string) ([]string, error)
	// RunCmd runs a shell command and returns its stdout (required for the
	// command strategy).
	RunCmd func(command string) (string, error)
	// Commits lists the commits a release after base would carry: those
	// since the release whose version is base (no leading "v"). Required
	// under bump: auto. Which commits those are — the git range, and any
	// filtering to what one image is built from — is the caller's decision;
	// the resolver only classifies them.
	Commits func(base string) ([]gitinfo.Commit, error)
	// Scope names what Commits was narrowed to — one image's paths or
	// build context in a monorepo — for Reason; "" when it lists every
	// commit in the repository.
	Scope string
}

// Decision is a resolved version and, under bump: auto, why it is that
// version.
type Decision struct {
	// Version is the release version, without a leading "v".
	Version string
	// Auto reports that bump: auto chose the version from commits. It is
	// false when auto did not run: a tag on HEAD named the version, or no
	// previous release left Initial as the only answer.
	Auto bool
	// Base is the version the bump was applied to ("" when there was none).
	Base string
	// Level is the bump applied to Base under auto.
	Level conventional.Level
	// Bump is the commit that decided Level, with its parsed message; zero
	// when no commit called for a bump.
	Bump conventional.Decision
	// Initial reports that the version is versioning.initial because there
	// was no previous release to bump.
	Initial bool
	// NothingToRelease reports that, under bump: auto with on_no_changes:
	// skip, no commit since Base called for a bump: there is no new version,
	// and a real release has nothing to publish. Version is then Base. A
	// snapshot is never nothing to release.
	NothingToRelease bool
	// Scope is Input.Scope: what the commits were narrowed to, or "" for
	// the whole repository.
	Scope string
	// PreMajor reports that versioning.pre_major: minor lowered Level by
	// one because Base is 0.x.
	PreMajor bool
	// Series is the prerelease series in flight that this version follows,
	// restarts above, or promotes ("1.5.0-rc.2"); "" when there was none.
	Series string
	// SeriesStep is what the version does to Series: one of the Series*
	// constants, or "" with no Series.
	SeriesStep string
}

// What a version does to the prerelease series in flight.
const (
	// SeriesNext is the next candidate of the same target: 1.5.0-rc.3
	// after 1.5.0-rc.2.
	SeriesNext = "follows"
	// SeriesRestart is a candidate of a higher target: 2.0.0-rc.1 after
	// 1.5.0-rc.2.
	SeriesRestart = "restarts above"
	// SeriesPromote is the release the series was a candidate for: 1.5.0
	// after 1.5.0-rc.2.
	SeriesPromote = "promotes"
	// SeriesSupersede is a release above the series' target: 2.0.0 after
	// 1.5.0-rc.2.
	SeriesSupersede = "supersedes"
)

// Reason explains the decision for check and plan output: the bump and the
// commit that decided it ("minor: feat(api): add pagination, a1b2c3d"), why
// there is nothing to release, or "" when there is nothing to explain.
func (d Decision) Reason() string {
	var r string
	switch {
	case d.Initial:
		r = "first release: versioning.initial"
	case !d.Auto:
		return ""
	case d.NothingToRelease:
		return fmt.Sprintf("nothing to release: no commit since %s%s calls for a version bump", d.Base, d.touching())
	case d.Bump.Level == conventional.None && d.Series != "":
		r = fmt.Sprintf("no commit since %s%s calls for more than the series in flight", d.Base, d.touching())
	case d.Bump.Level == conventional.None:
		r = fmt.Sprintf(
			"%s: no commit since %s%s calls for a bump (versioning.on_no_changes: patch)",
			d.Level, d.Base, d.touching(),
		)
	default:
		short := d.Bump.Commit.SHA
		if len(short) > 7 {
			short = short[:7]
		}
		r = fmt.Sprintf("%s: %s, %s", d.Level, d.Bump.Commit.Subject, short)
	}
	if d.PreMajor {
		r += fmt.Sprintf(" (%s on 0.x: versioning.pre_major: minor)", d.Level+1)
	}
	if d.Series != "" {
		r += fmt.Sprintf("; %s %s", d.SeriesStep, d.Series)
	}
	return r
}

// touching qualifies "no commit since X" with the scope the commits were
// narrowed to, if any.
func (d Decision) touching() string {
	if d.Scope == "" {
		return ""
	}
	return " touching " + d.Scope
}

// ErrNoCommits is returned under bump: auto when Input has no Commits hook.
var ErrNoCommits = errors.New("versioning: bump: auto has no commit lister")

// Resolve returns the release version string per the configured strategy. The
// result never carries a leading "v" (templates add one if desired).
func Resolve(in Input) (string, error) {
	d, err := Decide(in)
	return d.Version, err
}

// Decide resolves the release version per the configured strategy and
// explains it.
func Decide(in Input) (Decision, error) {
	switch in.Cfg.Strategy {
	case "git", "":
		return resolveGit(in)
	case "registry", "ecr":
		// Both list existing tags and bump the highest semver; the tag lister
		// (crane vs. aws) is injected by the caller.
		return resolveRegistry(in)
	case "static":
		return plain(snapshotize(strings.TrimPrefix(in.Cfg.Value, "v"), in)), nil
	case "env":
		val := getenv(in)(in.Cfg.Env)
		if val == "" {
			return Decision{}, fmt.Errorf("versioning: env var %q is empty", in.Cfg.Env)
		}
		return plain(snapshotize(strings.TrimPrefix(val, "v"), in)), nil
	case "command":
		if in.RunCmd == nil {
			return Decision{}, fmt.Errorf("versioning: command strategy has no command runner")
		}
		out, err := in.RunCmd(in.Cfg.Command)
		if err != nil {
			return Decision{}, fmt.Errorf("versioning command %q: %w", in.Cfg.Command, err)
		}
		return plain(snapshotize(strings.TrimPrefix(firstLine(out), "v"), in)), nil
	default:
		return Decision{}, fmt.Errorf("versioning: unsupported strategy %q", in.Cfg.Strategy)
	}
}

func plain(v string) Decision { return Decision{Version: v} }

// resolveGit is the git strategy: the tag on HEAD names the release. Under
// bump: auto an untagged HEAD releases as the latest reachable release tag
// plus the bump its commits call for (versioning.initial with no tag at all),
// continuing or promoting a prerelease series that followed that release.
func resolveGit(in Input) (Decision, error) {
	if in.Cfg.Bump != config.BumpAuto || keepsTag(in) {
		// A snapshot is a snapshot even on a tagged commit: --snapshot must
		// never publish under the clean release version.
		if in.Snapshot {
			return plain(in.Git.SnapshotVersion()), nil
		}
		return plain(in.Git.Version), nil
	}
	release := in.Git.LatestRelease
	if release == "" {
		// An Info that predates LatestRelease (or was built by hand) has only
		// LatestTag; that is the release unless it is a prerelease.
		if v, ok := semver.Parse(in.Git.LatestTag); ok && !v.IsPrerelease() {
			release = in.Git.LatestTag
		}
	}
	var base *semver.Version
	if release != "" {
		v, ok := semver.Parse(release)
		if !ok {
			return Decision{}, fmt.Errorf("versioning: latest tag %q is not a semantic version", release)
		}
		base = &v
	}
	series := gitSeries(in.Git.LatestTag, base)
	if base == nil {
		return initial(in, series)
	}
	return auto(*base, series, in)
}

// keepsTag reports whether, under bump: auto, the tag on HEAD names the
// release. It does unless it is a candidate of a series this run does not
// continue: a run without a prerelease promotes the candidate on HEAD (its
// commit becomes 1.5.0 too), and a run for another identifier starts that
// series there.
func keepsTag(in Input) bool {
	if in.Git.Tag == "" {
		return false
	}
	v, ok := semver.Parse(in.Git.Tag)
	if !ok {
		return true
	}
	id, _, isSeries := seriesOf(v)
	return !isSeries || id == in.Cfg.Prerelease
}

// gitSeries is the prerelease series in flight under the git strategy: the
// latest reachable tag, when it is a series candidate (see seriesOf) above
// the release it follows.
func gitSeries(latest string, release *semver.Version) *semver.Version {
	v, ok := semver.Parse(latest)
	if !ok {
		return nil
	}
	if _, _, isSeries := seriesOf(v); !isSeries || (release != nil && v.Compare(*release) <= 0) {
		return nil
	}
	return &v
}

// resolveRegistry lists the repository's tags, finds the highest semver, and
// bumps it. When the repository has no semver tags, the configured initial
// version is used as-is.
func resolveRegistry(in Input) (Decision, error) {
	if in.ListTags == nil {
		return Decision{}, fmt.Errorf("versioning: registry strategy has no tag lister")
	}
	repo := in.Cfg.Repo
	if repo == "" {
		repo = in.Repo
	}
	if repo == "" {
		return Decision{}, fmt.Errorf("versioning: registry strategy needs a repo (set versioning.repo or define an image)")
	}
	tags, err := in.ListTags(repo)
	if err != nil {
		return Decision{}, fmt.Errorf("versioning: list tags for %s: %w", repo, err)
	}
	highest, found := highestRelease(tags)
	// A fixed bump without a prerelease keeps ignoring prereleases, as it
	// always has: its target never depends on them.
	var series *semver.Version
	if in.Cfg.Bump == config.BumpAuto || in.Cfg.Prerelease != "" {
		series = highestSeries(tags, highest, found)
	}
	switch {
	case !found:
		return initial(in, series)
	case in.Cfg.Bump == config.BumpAuto:
		return auto(highest, series, in)
	default:
		d := Decision{}
		v, err := d.toward(highest.Bump(in.Cfg.Bump), series, in.Cfg.Prerelease)
		if err != nil {
			return Decision{}, err
		}
		d.Version = snapshotize(v.String(), in)
		return d, nil
	}
}

// initial is the first release: versioning.initial, as there is nothing to
// bump — or its prerelease, or the series already in flight toward it.
func initial(in Input, series *semver.Version) (Decision, error) {
	d := Decision{Initial: true}
	first, ok := semver.Parse(strings.TrimSpace(in.Cfg.Initial))
	if !ok || first.IsPrerelease() {
		// Unvalidated config: keep the value as given, as before.
		d.Version = snapshotize(strings.TrimPrefix(in.Cfg.Initial, "v"), in)
		return d, nil
	}
	v, err := d.toward(first, series, in.Cfg.Prerelease)
	if err != nil {
		return Decision{}, err
	}
	d.Version = snapshotize(v.String(), in)
	return d, nil
}

// auto applies bump: auto to base: the commits since it pick the level.
func auto(base semver.Version, series *semver.Version, in Input) (Decision, error) {
	if in.Commits == nil {
		return Decision{}, ErrNoCommits
	}
	commits, err := in.Commits(base.String())
	if err != nil {
		return Decision{}, fmt.Errorf("versioning: list commits since %s: %w", base, err)
	}
	rules, err := Rules(in.Cfg)
	if err != nil {
		return Decision{}, err
	}
	d, err := Next(base, series, conventional.Select(commits, rules), PolicyOf(in.Cfg))
	if err != nil {
		return Decision{}, err
	}
	d.Scope = in.Scope
	if d.NothingToRelease && in.Snapshot {
		// A snapshot has no release to skip: it builds the code as it is,
		// under the version it would follow.
		d.NothingToRelease = false
	}
	d.Version = snapshotize(d.Version, in)
	return d, nil
}

// Policy is the part of versioning: that turns a bump level into a version.
type Policy struct {
	// OnNoChanges is what happens when no commit calls for a bump: skip
	// ("" too) or patch.
	OnNoChanges string
	// PreMajor is major ("" too) or minor: whether a 0.x base takes every
	// level one step lower.
	PreMajor string
	// Prerelease is the series identifier ("rc") to cut a candidate of, or
	// "" to cut a release.
	Prerelease string
}

// PolicyOf reads a Policy from the versioning config.
func PolicyOf(cfg config.Versioning) Policy {
	return Policy{OnNoChanges: cfg.OnNoChanges, PreMajor: cfg.PreMajor, Prerelease: cfg.Prerelease}
}

// Next is bump selection: the version after base (the latest release) given
// the bump the commits since it called for, the prerelease series in flight
// after base (nil when there is none), and the policy. It is pure, so a rule
// about which level applies or how a series moves is a change here rather
// than in how the commits or the series are found.
//
// The level is the commits' (on_no_changes: patch when they call for none;
// with neither, nothing to release — unless a series is in flight, which is
// then itself what there is to release). On a 0.x base, pre_major: minor
// takes it one step lower. The target is base bumped by that level, but never
// below a series in flight: the series' commits are among those since base,
// so a lower target only means the rules changed, and going back would reuse
// or undercut its versions. With a prerelease the version is the next
// candidate of the target (see Decision.toward); without one, the target.
func Next(base semver.Version, series *semver.Version, sel conventional.Decision, p Policy) (Decision, error) {
	d := Decision{Auto: true, Base: base.String(), Level: sel.Level, Bump: sel}
	if d.Level == conventional.None && series == nil {
		if p.OnNoChanges != config.OnNoChangesPatch {
			d.Version, d.NothingToRelease = d.Base, true
			return d, nil
		}
		d.Level = conventional.Patch
	}
	if base.Major == 0 && p.PreMajor == config.PreMajorMinor && d.Level > conventional.Patch {
		d.Level--
		d.PreMajor = true
	}
	target := base
	if d.Level != conventional.None {
		target = base.Bump(d.Level.String())
	}
	v, err := d.toward(target, series, p.Prerelease)
	if err != nil {
		return Decision{}, err
	}
	d.Version = v.String()
	return d, nil
}

// toward is the version a run cuts for target given the series in flight
// (nil for none) and the prerelease identifier ("" for a release), and
// records on d what it does to the series.
//
//   - The target is raised to the series' own target when that is higher.
//   - A release is the target: it promotes the series, or supersedes it.
//   - A candidate of the same target and identifier as the series is its
//     next number (1.5.0-rc.2 → 1.5.0-rc.3); any other is number 1 of the
//     target (2.0.0-rc.1 restarts above 1.5.0-rc.2).
//
// A candidate that would not sort above the series — the identifier changed
// to one that sorts lower, "alpha" after "beta" — is an error rather than a
// version that goes backwards.
func (d *Decision) toward(target semver.Version, series *semver.Version, id string) (semver.Version, error) {
	if series == nil {
		if id == "" {
			return target, nil
		}
		return candidateOf(target, id, 1), nil
	}
	d.Series = series.String()
	if c := core(*series); c.Compare(target) > 0 {
		target = c
	}
	same := core(*series).Compare(target) == 0
	if id == "" {
		d.SeriesStep = SeriesSupersede
		if same {
			d.SeriesStep = SeriesPromote
		}
		return target, nil
	}
	n := 1
	d.SeriesStep = SeriesRestart
	if sid, sn, _ := seriesOf(*series); same && sid == id {
		n = sn + 1
		d.SeriesStep = SeriesNext
	}
	v := candidateOf(target, id, n)
	if v.Compare(*series) <= 0 {
		return semver.Version{}, fmt.Errorf(
			"versioning: prerelease %s would not sort above %s, the prerelease already in flight "+
				"(use an identifier that sorts after %q, or release %s first)",
			v, series, series.Pre[0], core(*series),
		)
	}
	return v, nil
}

// candidateOf is target's prerelease number n of series id: 1.5.0-rc.2.
func candidateOf(target semver.Version, id string, n int) semver.Version {
	target.Pre = []string{id, strconv.Itoa(n)}
	return target
}

// core is v without its prerelease: the release it is a candidate for.
func core(v semver.Version) semver.Version {
	return semver.Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch}
}

// seriesOf reads v as a series candidate — a prerelease of exactly
// "<identifier>.<number>", the shape a prerelease run cuts (1.5.0-rc.2) — and
// returns its identifier and number. Any other prerelease ("1.5.0-beta",
// "1.5.0-alpine", "2.0.0-rc.1.amd64") is not a series stevedore can continue
// or promote, and is left alone.
func seriesOf(v semver.Version) (id string, n int, ok bool) {
	if len(v.Pre) != 2 || !config.ValidPrereleaseID(v.Pre[0]) {
		return "", 0, false
	}
	n, err := strconv.Atoi(v.Pre[1])
	if err != nil || n < 0 || strings.TrimLeft(v.Pre[1], "0123456789") != "" {
		return "", 0, false
	}
	return v.Pre[0], n, true
}

// Rules is the type → level mapping bump: auto applies: the Conventional
// Commits defaults with versioning.bump_types over them.
func Rules(cfg config.Versioning) (conventional.Rules, error) {
	overrides := make(map[string]conventional.Level, len(cfg.BumpTypes))
	for typ, s := range cfg.BumpTypes {
		l, ok := conventional.ParseLevel(s)
		if !ok {
			return nil, fmt.Errorf("versioning.bump_types.%s %q invalid (want none, patch, minor, or major)", typ, s)
		}
		overrides[typ] = l
	}
	return conventional.DefaultRules().With(overrides), nil
}

// snapshotize appends the snapshot suffix (mirroring gitinfo) when building a
// snapshot; otherwise it returns base unchanged.
func snapshotize(base string, in Input) string {
	if !in.Snapshot {
		return base
	}
	return gitinfo.SnapshotOf(base, in.Git)
}

func getenv(in Input) func(string) string {
	if in.Getenv != nil {
		return in.Getenv
	}
	return func(string) string { return "" }
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// parseRelease accepts a release version (semver without a prerelease) with
// an optional leading "v" and surrounding space. Prereleases are rejected so
// that only stable releases participate in "highest existing version"
// selection; build metadata is ignored, as semver says.
func parseRelease(s string) (semver.Version, bool) {
	v, ok := semver.Parse(strings.TrimSpace(s))
	if !ok || v.IsPrerelease() {
		return semver.Version{}, false
	}
	return v, true
}

// highestSeries returns the greatest series candidate (see seriesOf) among
// tags that sorts above release (any, when found is false), or nil.
func highestSeries(tags []string, release semver.Version, found bool) *semver.Version {
	var best *semver.Version
	for _, t := range tags {
		v, ok := semver.Parse(strings.TrimSpace(t))
		if !ok {
			continue
		}
		if _, _, isSeries := seriesOf(v); !isSeries || (found && v.Compare(release) <= 0) {
			continue
		}
		if best == nil || v.Compare(*best) > 0 {
			best = &v
		}
	}
	return best
}

// highestRelease returns the greatest release version among tags.
func highestRelease(tags []string) (semver.Version, bool) {
	var best semver.Version
	found := false
	for _, t := range tags {
		if v, ok := parseRelease(t); ok && (!found || v.Compare(best) > 0) {
			best, found = v, true
		}
	}
	return best, found
}
