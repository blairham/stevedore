// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package gitinfo derives release metadata (version, commit, branch) from git.
package gitinfo

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	runner "github.com/blairham/stevedore/internal/run"
	"github.com/blairham/stevedore/internal/semver"
)

// Info is the git-derived state used to build the template context.
type Info struct {
	// Version is the semver-ish version without a leading "v". For a clean
	// checkout with a tag on HEAD this is the tag minus "v"; otherwise a
	// snapshot version based on LatestTag.
	Version string
	// Tag is the version tag pointing exactly at HEAD (the highest, when
	// there are several), or empty when HEAD has none. Tags that are not
	// semantic versions ("deploy-prod") are ignored.
	// It is the only tag a release may be named after: a tag further back
	// names a release that was already cut from a different commit.
	Tag string
	// LatestTag is the most recent version tag reachable from HEAD — Tag when HEAD is
	// tagged, otherwise the last release before it (may be empty). It seeds
	// snapshot versions and the changelog range, never a release name.
	LatestTag string
	// Commit is the full HEAD SHA.
	Commit string
	// ShortCommit is the abbreviated HEAD SHA.
	ShortCommit string
	// CommitTime is HEAD's committer date; zero in a repository with no
	// commits. Unlike the wall clock it is the same on every build of the
	// commit, which is what makes it the reproducible-build timestamp.
	CommitTime time.Time
	// SourceURL is the origin remote as a browsable https URL
	// ("https://github.com/acme/app" for git@github.com:acme/app.git), with
	// any credentials dropped; empty with no origin remote.
	SourceURL string
	// Branch is the current branch name, verbatim from git. On a detached HEAD
	// — how every tag-triggered CI job checks a release out — git reports the
	// literal string "HEAD", which is why Branches exists.
	Branch string
	// Detached reports whether HEAD points at a commit rather than a branch.
	Detached bool
	// Branches lists the branch names containing HEAD: local branches, and
	// remote-tracking branches both with and without their remote prefix
	// ("origin/main" and "main"). On a detached HEAD this is the only way to
	// tell which branch a release was cut from.
	Branches []string
	// Dirty reports whether the working tree has uncommitted changes.
	Dirty bool
	// PreviousTag is the last release before this one, used for changelog
	// ranges and the SBOM dependency diff (may be empty). With a tag on HEAD
	// it is the tag before Tag; on an untagged HEAD it is LatestTag, since the
	// commits being released are exactly those since that tag.
	PreviousTag string
}

// Gather collects git state for the repository at dir.
func Gather(ctx context.Context, dir string) (*Info, error) {
	if _, err := run(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	info := &Info{}

	info.Commit = output(ctx, dir, "rev-parse", "HEAD")
	info.ShortCommit = output(ctx, dir, "rev-parse", "--short", "HEAD")
	info.SourceURL = HTTPSURL(output(ctx, dir, "remote", "get-url", "origin"))
	if ct, err := strconv.ParseInt(output(ctx, dir, "log", "-1", "--format=%ct", "HEAD"), 10, 64); err == nil {
		info.CommitTime = time.Unix(ct, 0).UTC()
	}
	info.Branch = output(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	info.Detached = info.Branch == "HEAD"
	info.Branches = branchesContaining(ctx, dir)
	info.Dirty = output(ctx, dir, "status", "--porcelain") != ""

	// The exact tag on HEAD and the latest reachable tag are kept apart: only
	// the former may become a clean release version. Collapsing them released
	// an untagged commit under the previous release's version.
	//
	// Only version tags count. A commit often carries other tags too (a
	// lightweight "deploy-prod" marker), and git's own choice among several
	// tags on one commit is alphabetical, which made such a marker the
	// release version.
	info.Tag = highestVersionTagAt(ctx, dir, "HEAD")
	info.LatestTag = nearestVersionTag(ctx, dir, "HEAD")

	switch {
	case info.Tag != "":
		info.PreviousTag = nearestVersionTag(ctx, dir, "HEAD^")
	default:
		info.PreviousTag = info.LatestTag
	}

	info.Version = deriveVersion(info)
	return info, nil
}

// highestVersionTagAt returns the highest-precedence version tag pointing at
// rev, or "" when rev carries none.
func highestVersionTagAt(ctx context.Context, dir, rev string) string {
	out, err := run(ctx, dir, "tag", "--points-at", rev)
	if err != nil {
		return ""
	}
	best := ""
	var bestV semver.Version
	for _, tag := range strings.Split(out, "\n") {
		v, ok := semver.Parse(tag)
		if !ok {
			continue
		}
		// Equal precedence (v1.0.0 vs 1.0.0, or differing build metadata)
		// falls back to the name, so the choice never depends on git's order.
		if c := v.Compare(bestV); best == "" || c > 0 || (c == 0 && tag > best) {
			best, bestV = tag, v
		}
	}
	return best
}

// nearestVersionTag returns the version tag on the nearest commit reachable
// from rev that carries one (the highest, when it carries several), or "".
//
// The --match globs narrow describe to tags that start like a version; a tag
// that passes them but is not one ("v2-legacy") is excluded and describe asked
// again, so it cannot hide an older real version behind it.
func nearestVersionTag(ctx context.Context, dir, rev string) string {
	args := []string{"describe", "--tags", "--abbrev=0", "--match", "v[0-9]*", "--match", "[0-9]*"}
	for range maxDescribeRetries {
		tag, err := run(ctx, dir, append(args, rev)...)
		if err != nil {
			return ""
		}
		if _, ok := semver.Parse(tag); ok {
			if best := highestVersionTagAt(ctx, dir, tag+"^{commit}"); best != "" {
				return best
			}
			return tag
		}
		args = append(args, "--exclude", tag)
	}
	return ""
}

// maxDescribeRetries bounds how many version-looking non-version tags
// nearestVersionTag will step past.
const maxDescribeRetries = 64

// branchesContaining lists the branches that contain HEAD. A tag-triggered CI
// job checks out a detached HEAD, so "which branch is this?" cannot be answered
// by asking git for the current branch — it has to be answered by asking which
// branches the commit is reachable from.
//
// Remote-tracking branches are reported under both spellings ("origin/main" and
// "main") because the answer callers want is the branch name as a human writes
// it in config, and on a fresh CI checkout the only ref that exists is the
// remote-tracking one. A shallow clone has no such refs at all and returns
// nothing here, which callers must treat as "unknown", not as "no".
func branchesContaining(ctx context.Context, dir string) []string {
	out, err := run(ctx, dir, "for-each-ref", "--format=%(refname)", "--contains", "HEAD", "refs/heads", "refs/remotes")
	if err != nil || out == "" {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	add := func(name string) {
		// "origin/HEAD" contributes a bare "HEAD", which is the one name that
		// would make the detached-HEAD branch string compare equal by accident.
		if name == "" || name == "HEAD" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	for _, ref := range strings.Split(out, "\n") {
		ref = strings.TrimSpace(ref)
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			// Local branches keep their slashes: refs/heads/feat/x is "feat/x".
			add(strings.TrimPrefix(ref, "refs/heads/"))
		case strings.HasPrefix(ref, "refs/remotes/"):
			rest := strings.TrimPrefix(ref, "refs/remotes/")
			add(rest)
			if _, branch, ok := strings.Cut(rest, "/"); ok {
				add(branch)
			}
		}
	}
	return names
}

// HTTPSURL normalizes a git remote URL to the https URL of the repository:
// scp-style ("git@host:owner/repo.git"), ssh://, git:// and http(s):// remotes
// all become "https://host/owner/repo", without user info, port or ".git".
// A local path, or anything else without a host, yields "".
func HTTPSURL(remote string) string {
	remote = strings.TrimSpace(remote)
	host, path, ok := splitURLRemote(remote)
	if !ok {
		host, path = splitSCPRemote(remote)
	}
	if i := strings.LastIndexByte(host, '@'); i >= 0 {
		host = host[i+1:] // user info, and with it any token
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if len(host) < 2 || path == "" || strings.ContainsRune(host+path, '\\') {
		return ""
	}
	return "https://" + host + "/" + path
}

// splitURLRemote splits a scheme://[user@]host[:port]/path remote; ok is false
// when remote has no scheme. A scheme with no web page behind it (a local
// file remote) yields an empty host.
func splitURLRemote(remote string) (host, path string, ok bool) {
	scheme, rest, ok := strings.Cut(remote, "://")
	if !ok {
		return "", "", false
	}
	switch strings.ToLower(scheme) {
	case "https", "http", "ssh", "git", "git+ssh", "ssh+git":
	default:
		return "", "", true
	}
	host, path, _ = strings.Cut(rest, "/")
	if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.Contains(host[i:], "@") {
		host = host[:i] // the port: ssh's 22 is not the web server's
	}
	return host, path, true
}

// splitSCPRemote splits git's scp-like [user@]host:path syntax. A colon after
// a slash makes it a local path instead ("./a:b"), as git itself reads it.
func splitSCPRemote(remote string) (host, path string) {
	i := strings.IndexByte(remote, ':')
	if i < 0 || strings.Contains(remote[:i], "/") {
		return "", ""
	}
	return remote[:i], remote[i+1:]
}

// OnBranch reports whether HEAD is on the named branch. On a detached HEAD it
// falls back to reachability, which is what makes a release cut from a tag
// recognizable as a release off the default branch.
func (i *Info) OnBranch(branch string) bool {
	if branch == "" {
		return false
	}
	if i.Detached {
		// Branch is the literal "HEAD" here and carries no information — which
		// is also why it must not be compared: it would match a default branch
		// configured, however oddly, as "HEAD".
		return slices.Contains(i.Branches, branch)
	}
	// On a real branch, git's answer is the user's intent. A side branch that
	// happens to sit at the same commit as main is still a side branch, and
	// should not move a floating tag.
	return i.Branch == branch
}

// deriveVersion produces a clean version string only for a clean checkout with a
// tag on HEAD, and a snapshot version otherwise.
func deriveVersion(i *Info) string {
	if i.Tag != "" && !i.Dirty {
		return strings.TrimPrefix(i.Tag, "v")
	}
	return i.SnapshotVersion()
}

// SnapshotBase is the version a snapshot is built on: the latest reachable tag
// minus any leading "v", or "0.0.0" in a repository with no tags.
func (i *Info) SnapshotBase() string {
	if i.LatestTag != "" {
		return strings.TrimPrefix(i.LatestTag, "v")
	}
	return "0.0.0"
}

// SnapshotVersion is the snapshot form of the version,
// "<base>-SNAPSHOT-<short sha>[-dirty]", whatever the state of HEAD.
func (i *Info) SnapshotVersion() string {
	return SnapshotOf(i.SnapshotBase(), i)
}

// SnapshotMarker separates a snapshot version's base from its suffix.
const SnapshotMarker = "-SNAPSHOT-"

// IsSnapshotVersion reports whether version is in the snapshot form SnapshotOf
// produces, and returns the base it was built on ("1.4.0" for
// "1.4.0-SNAPSHOT-9f8e7d6").
func IsSnapshotVersion(version string) (base string, ok bool) {
	base, _, ok = strings.Cut(version, SnapshotMarker)
	return base, ok
}

// SnapshotOf appends the snapshot suffix for HEAD to base. It is shared with
// the non-git versioning strategies so every snapshot reads the same way.
func SnapshotOf(base string, i *Info) string {
	sc := ""
	if i != nil {
		sc = i.ShortCommit
	}
	if sc == "" {
		sc = "unknown"
	}
	v := base + SnapshotMarker + sc
	if i != nil && i.Dirty {
		v += "-dirty"
	}
	return v
}

// ReleaseTag is the git tag a release of version is published under: the tag
// on HEAD when it names that version, otherwise "v"+version. A tag on HEAD
// that names some other version (a non-git strategy computed its own) is not
// this release's name.
func (i *Info) ReleaseTag(version string) string {
	if i != nil && i.Tag != "" && strings.TrimPrefix(i.Tag, "v") == version {
		return i.Tag
	}
	return "v" + version
}

// CommitsSince returns commit subjects (and bodies) reachable from HEAD but not
// from ref. If ref is empty, all commits are returned. Newest first.
func CommitsSince(ctx context.Context, dir, ref string) ([]Commit, error) {
	args := []string{"log", "--no-merges", "--pretty=format:%H%x1f%s%x1f%an%x1e"}
	if ref != "" {
		args = append(args, ref+"..HEAD")
	}
	out, err := run(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		fields := strings.Split(rec, "\x1f")
		if len(fields) < 3 {
			continue
		}
		commits = append(commits, Commit{
			SHA:     fields[0],
			Subject: fields[1],
			Author:  fields[2],
		})
	}
	return commits, nil
}

// Commit is a single git commit summary.
type Commit struct {
	SHA     string
	Subject string
	Author  string
}

// output is run for the best-effort fields of Info: a repository with no
// commits yet has no HEAD to describe, and that is "" rather than a failure.
func output(ctx context.Context, dir string, args ...string) string {
	out, err := run(ctx, dir, args...)
	if err != nil {
		return ""
	}
	return out
}

// run is a read-only git query in dir, through the Runner ctx carries (see
// run.WithRunner): it executes under --dry-run and is echoed under --verbose.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	return runner.Query(ctx, "git", append([]string{"-C", dir}, args...)...)
}
