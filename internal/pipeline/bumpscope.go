// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"fmt"
	"strings"

	"github.com/blairham/stevedore/internal/changed"
	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/gitinfo"
)

// versionResolver resolves one image's version under per-image registry
// versioning, from repo (the image's first repository, rendered).
type versionResolver func(img config.Image, repo string) (string, error)

// perImageBump reports whether bump: auto decides each image's bump from its
// own commits: under registry/ecr, where every image is versioned from its
// own repository. A pinned versioning.repo is one unified version, which has
// to reflect every change in the repository, and the git strategy has one tag
// for the whole release; both keep the repository-wide range.
func perImageBump(v config.Versioning) bool {
	return v.Bump == config.BumpAuto && isRegistryStrategy(v.Strategy) && v.Repo == ""
}

// commitSource lists the commits a bump: auto release carries. It is shared by
// every resolution in one Prepare, so the repository root and the release
// markers are looked up at most once.
type commitSource struct {
	o   Options
	cfg *config.Config
	gi  *gitinfo.Info

	repoRoot       string
	markersFetched bool
}

func newCommitSource(o Options, cfg *config.Config, gi *gitinfo.Info) *commitSource {
	return &commitSource{o: o, cfg: cfg, gi: gi}
}

// whole lists every commit since the git tag of the release named base — see
// releaseSince. Every commit counts for every image.
func (s *commitSource) whole() func(string) ([]gitinfo.Commit, error) {
	return func(base string) ([]gitinfo.Commit, error) {
		return gitinfo.CommitsSince(s.o.context(), s.o.Dir, releaseSince(s.o, s.gi, base))
	}
}

// forImage lists the commits since img's last release that touched what img
// is built from, and names that scope for the version's reason. Outside
// per-image bump: auto it is whole, with no scope.
//
// The scope is change detection's (see changeScope): the image's resolved
// paths, or its build context minus its dockerignore, with
// change_detection.shared_paths counting for every image; an image with
// neither (a remote context) cannot be narrowed and sees every commit. The
// image's last release is its release marker when marker_refs is on and the
// marker exists, and otherwise the git tag of the version it bumps from.
func (s *commitSource) forImage(img config.Image) (func(string) ([]gitinfo.Commit, error), string, error) {
	if !perImageBump(s.cfg.Versioning) {
		return s.whole(), "", nil
	}
	scope, err := s.scope(img)
	if err != nil {
		return nil, "", err
	}
	shared := s.cfg.ChangeDetection.SharedPaths
	list := func(base string) ([]gitinfo.Commit, error) {
		since, err := s.since(img, base)
		if err != nil {
			return nil, err
		}
		commits, err := gitinfo.CommitsSince(s.o.context(), s.o.Dir, since)
		if err != nil || len(commits) == 0 {
			return commits, err
		}
		files, err := changed.FilesByCommit(s.o.context(), s.o.Dir, since)
		if err != nil {
			return nil, err
		}
		return touching(commits, files, scope, shared), nil
	}
	return list, describeScope(scope), nil
}

// release lists the commits the release-level version is decided by: under
// per-image bump: auto that version is the anchor image's (first), so they are
// scoped to it as its plan's are; otherwise they are the whole repository's.
func (s *commitSource) release(first *config.Image) (func(string) ([]gitinfo.Commit, error), string, error) {
	if first == nil {
		return s.whole(), "", nil
	}
	return s.forImage(*first)
}

// scope is img's change-detection scope in repository-relative terms.
func (s *commitSource) scope(img config.Image) (changed.Scope, error) {
	paths, err := resolveImagePaths(s.o.Dir, s.cfg, img)
	if err != nil {
		return changed.Scope{}, err
	}
	if s.repoRoot == "" {
		if s.repoRoot, err = changed.RepoRoot(s.o.context(), s.o.Dir); err != nil {
			return changed.Scope{}, err
		}
	}
	return changeScope(s.o, s.repoRoot, ImagePlan{Image: img, Paths: paths})
}

// since is the commit img's last release was cut from: its release marker
// when marker_refs is on and the image has one (the merge base, if the
// marker has diverged from HEAD), otherwise the git tag of base.
func (s *commitSource) since(img config.Image, base string) (string, error) {
	cd := s.cfg.ChangeDetection
	if !cd.MarkerRefs {
		return releaseSince(s.o, s.gi, base), nil
	}
	ctx := s.o.context()
	if !s.markersFetched {
		if err := changed.FetchMarkers(ctx, s.o.Dir, cd.MarkerPrefix); err != nil {
			return "", err
		}
		s.markersFetched = true
	}
	ref := changed.MarkerRef(cd.MarkerPrefix, img.ID)
	if !changed.RefExists(ctx, s.o.Dir, ref) {
		return releaseSince(s.o, s.gi, base), nil
	}
	mb, _, err := changed.MarkerBase(ctx, s.o.Dir, ref)
	return mb, err
}

// releaseSince is the git tag of the release named base. Under the git
// strategy that is the latest reachable tag. Under registry/ecr base comes
// from the registry, and its tag is the one release.github created
// ("v"+base); with no such tag the range falls back to the latest reachable
// version tag, and with none of those to the whole history ("").
func releaseSince(o Options, gi *gitinfo.Info, base string) string {
	since := gi.LatestTag
	if strings.TrimPrefix(since, "v") != base {
		if tag := gitinfo.VersionTag(o.context(), o.Dir, base); tag != "" {
			since = tag
		}
	}
	return since
}

// touching keeps the commits whose files (files, keyed by SHA) fall in scope
// or match shared, in order. It is change detection's rule applied to one
// commit at a time, so a commit counts toward an image's bump exactly when it
// alone would have marked that image changed.
func touching(
	commits []gitinfo.Commit,
	files map[string][]string,
	scope changed.Scope,
	shared []string,
) []gitinfo.Commit {
	var out []gitinfo.Commit
	for _, c := range commits {
		if changed.Evaluate(scope, shared, files[c.SHA]).Changed {
			out = append(out, c)
		}
	}
	return out
}

// describeScope names an image's scope for a version reason: its paths, or its
// build context; "" when it is unscoped and so sees every commit.
func describeScope(scope changed.Scope) string {
	switch {
	case len(scope.Paths) > 0:
		const shown = 3
		if len(scope.Paths) > shown {
			return fmt.Sprintf("%s (+%d more)", strings.Join(scope.Paths[:shown], ", "), len(scope.Paths)-shown)
		}
		return strings.Join(scope.Paths, ", ")
	case scope.Context != nil:
		return scope.Context.Describe()
	}
	return ""
}
