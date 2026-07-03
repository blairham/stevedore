// Package gitinfo derives release metadata (version, commit, branch) from git.
package gitinfo

import (
	"fmt"
	"os/exec"
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
	// Branch is the current branch name.
	Branch string
	// Dirty reports whether the working tree has uncommitted changes.
	Dirty bool
	// PreviousTag is the tag before Tag, used for changelog ranges (may be empty).
	PreviousTag string
}

// Gather collects git state for the repository at dir.
func Gather(dir string) (*Info, error) {
	if _, err := run(dir, "rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	info := &Info{}

	info.Commit, _ = run(dir, "rev-parse", "HEAD")
	info.ShortCommit, _ = run(dir, "rev-parse", "--short", "HEAD")
	info.Branch, _ = run(dir, "rev-parse", "--abbrev-ref", "HEAD")

	status, _ := run(dir, "status", "--porcelain")
	info.Dirty = status != ""

	// Exact tag on HEAD, if any.
	if tag, err := run(dir, "describe", "--tags", "--exact-match"); err == nil {
		info.Tag = tag
	} else if tag, err := run(dir, "describe", "--tags", "--abbrev=0"); err == nil {
		// Most recent tag reachable from HEAD (not necessarily on HEAD).
		info.Tag = tag
	}

	if info.Tag != "" {
		if prev, err := run(dir, "describe", "--tags", "--abbrev=0", info.Tag+"^"); err == nil {
			info.PreviousTag = prev
		}
	}

	info.Version = deriveVersion(info)
	return info, nil
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
func CommitsSince(dir, ref string) ([]Commit, error) {
	args := []string{"log", "--no-merges", "--pretty=format:%H%x1f%s%x1f%an%x1e"}
	if ref != "" {
		args = append(args, ref+"..HEAD")
	}
	out, err := run(dir, args...)
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

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
