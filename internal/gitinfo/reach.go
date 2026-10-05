// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package gitinfo

import (
	"context"
	"errors"
	"fmt"

	runner "github.com/blairham/stevedore/internal/run"
)

// ErrNotOnBranch reports that HEAD is not reachable from the branch.
var ErrNotOnBranch = errors.New("HEAD is not on the branch")

// ErrShallow reports that the answer is unknown because the clone is
// shallow: the commits linking HEAD to the branch may simply not be there.
var ErrShallow = errors.New("shallow clone")

// CheckReachable verifies that HEAD is reachable from branch — the commit is
// on it, or is it — and returns the ref it checked against.
//
// The branch is read from origin ("origin/main") when the repository has an
// origin remote, and is fetched when that ref is missing or does not contain
// HEAD yet: a fresh CI checkout of a tag may have no remote-tracking branch at
// all, and a local one may simply be stale. Without an origin the local branch
// is the only record there is.
//
// The returned error wraps ErrNotOnBranch or ErrShallow for the two answers a
// caller turns into advice; anything else means the branch could not be found.
func CheckReachable(ctx context.Context, dir, branch string) (string, error) {
	if _, err := run(ctx, dir, "remote", "get-url", "origin"); err != nil {
		ref := "refs/heads/" + branch
		if !hasCommit(ctx, dir, ref) {
			return branch, fmt.Errorf("no origin remote and no local branch %q to check HEAD against", branch)
		}
		return branch, reachable(ctx, dir, ref)
	}
	name, ref := "origin/"+branch, "refs/remotes/origin/"+branch
	if hasCommit(ctx, dir, ref) && reachable(ctx, dir, ref) == nil {
		return name, nil
	}
	if err := runner.Refresh(ctx, "git", "-C", dir, "fetch", "--quiet", "origin", "+refs/heads/"+branch+":"+ref); err != nil {
		return name, fmt.Errorf("fetch %s: %w", name, err)
	}
	return name, reachable(ctx, dir, ref)
}

// reachable reports whether HEAD is an ancestor of (or equal to) ref.
func reachable(ctx context.Context, dir, ref string) error {
	head := output(ctx, dir, "rev-parse", "HEAD")
	if mb := output(ctx, dir, "merge-base", "HEAD", ref); head != "" && mb == head {
		return nil
	}
	if output(ctx, dir, "rev-parse", "--is-shallow-repository") == "true" {
		return ErrShallow
	}
	return ErrNotOnBranch
}

func hasCommit(ctx context.Context, dir, ref string) bool {
	_, err := run(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}
