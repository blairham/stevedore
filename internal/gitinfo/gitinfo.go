// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package gitinfo derives release metadata (version, commit, branch) from git.
package gitinfo

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// Info is the git-derived state used to build the template context.
type Info struct {
	// Version is the semver-ish version without a leading "v". For a tagged
	// clean checkout this is the tag minus "v"; otherwise a snapshot version.
	Version string
	// Tag is the most recent tag reachable from HEAD (may be empty).
	Tag string
	// Commit is the full HEAD SHA.
	Commit string
	// ShortCommit is the abbreviated HEAD SHA.
	ShortCommit string
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
	// PreviousTag is the tag before Tag, used for changelog ranges (may be empty).
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
	info.Branch = output(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	info.Detached = info.Branch == "HEAD"
	info.Branches = branchesContaining(ctx, dir)
	info.Dirty = output(ctx, dir, "status", "--porcelain") != ""

	// Exact tag on HEAD, if any.
	if tag, err := run(ctx, dir, "describe", "--tags", "--exact-match"); err == nil {
		info.Tag = tag
	} else if tag, err := run(ctx, dir, "describe", "--tags", "--abbrev=0"); err == nil {
		// Most recent tag reachable from HEAD (not necessarily on HEAD).
		info.Tag = tag
	}

	if info.Tag != "" {
		if prev, err := run(ctx, dir, "describe", "--tags", "--abbrev=0", info.Tag+"^"); err == nil {
			info.PreviousTag = prev
		}
	}

	info.Version = deriveVersion(info)
	return info, nil
}

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

// deriveVersion produces a clean version string for a tagged clean checkout, or
// a snapshot version otherwise.
func deriveVersion(i *Info) string {
	if i.Tag != "" && !i.Dirty {
		return strings.TrimPrefix(i.Tag, "v")
	}
	base := "0.0.0"
	if i.Tag != "" {
		base = strings.TrimPrefix(i.Tag, "v")
	}
	sc := i.ShortCommit
	if sc == "" {
		sc = "unknown"
	}
	v := fmt.Sprintf("%s-SNAPSHOT-%s", base, sc)
	if i.Dirty {
		v += "-dirty"
	}
	return v
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

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
