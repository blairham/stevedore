// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package versioner derives the release version string from a configurable
// source: git tags, an existing registry's tags, a static value, an environment
// variable, or the output of a command.
package versioner

import (
	"errors"
	"fmt"
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
}

// Reason explains the decision for check and plan output: the bump and the
// commit that decided it ("minor: feat(api): add pagination, a1b2c3d"), why
// there is nothing to release, or "" when there is nothing to explain.
func (d Decision) Reason() string {
	switch {
	case d.Initial:
		return "first release: versioning.initial"
	case !d.Auto:
		return ""
	case d.NothingToRelease:
		return fmt.Sprintf("nothing to release: no commit since %s calls for a version bump", d.Base)
	case d.Bump.Level == conventional.None:
		return fmt.Sprintf("%s: no commit since %s calls for a bump (versioning.on_no_changes: patch)", d.Level, d.Base)
	}
	short := d.Bump.Commit.SHA
	if len(short) > 7 {
		short = short[:7]
	}
	return fmt.Sprintf("%s: %s, %s", d.Level, d.Bump.Commit.Subject, short)
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
// bump: auto an untagged HEAD releases as the latest reachable tag plus the
// bump its commits call for (versioning.initial with no tag at all).
func resolveGit(in Input) (Decision, error) {
	if in.Cfg.Bump != config.BumpAuto || in.Git.Tag != "" {
		// A snapshot is a snapshot even on a tagged commit: --snapshot must
		// never publish under the clean release version.
		if in.Snapshot {
			return plain(in.Git.SnapshotVersion()), nil
		}
		return plain(in.Git.Version), nil
	}
	if in.Git.LatestTag == "" {
		return initial(in), nil
	}
	base, ok := semver.Parse(in.Git.LatestTag)
	if !ok {
		return Decision{}, fmt.Errorf("versioning: latest tag %q is not a semantic version", in.Git.LatestTag)
	}
	return auto(base, in)
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
	switch {
	case !found:
		return initial(in), nil
	case in.Cfg.Bump == config.BumpAuto:
		return auto(highest, in)
	default:
		return plain(snapshotize(highest.Bump(in.Cfg.Bump).String(), in)), nil
	}
}

// initial is the first release: versioning.initial, as there is nothing to
// bump.
func initial(in Input) Decision {
	return Decision{Version: snapshotize(strings.TrimPrefix(in.Cfg.Initial, "v"), in), Initial: true}
}

// auto applies bump: auto to base: the commits since it pick the level.
func auto(base semver.Version, in Input) (Decision, error) {
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
	d := Next(base, conventional.Select(commits, rules), in.Cfg.OnNoChanges)
	if d.NothingToRelease && in.Snapshot {
		// A snapshot has no release to skip: it builds the code as it is,
		// under the version it would follow.
		d.NothingToRelease = false
	}
	d.Version = snapshotize(d.Version, in)
	return d, nil
}

// Next is bump selection: the version after base given the bump the commits
// called for, and what on_no_changes ("" is skip) does when they called for
// none. It is pure, so a rule about which level applies (a 0.x series
// treating breaking changes as minor, a prerelease series) is a change here
// rather than in how the commits are found.
func Next(base semver.Version, sel conventional.Decision, onNoChanges string) Decision {
	d := Decision{Auto: true, Base: base.String(), Level: sel.Level, Bump: sel}
	if d.Level == conventional.None {
		if onNoChanges != config.OnNoChangesPatch {
			d.Version, d.NothingToRelease = d.Base, true
			return d
		}
		d.Level = conventional.Patch
	}
	d.Version = base.Bump(d.Level.String()).String()
	return d
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
