// Package versioner derives the release version string from a configurable
// source: git tags, an existing registry's tags, a static value, an environment
// variable, or the output of a command.
package versioner

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/gitinfo"
)

// Input bundles everything Resolve needs. The Getenv/ListTags/RunCmd hooks are
// injected so the resolver stays pure and testable.
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
}

// Resolve returns the release version string per the configured strategy. The
// result never carries a leading "v" (templates add one if desired).
func Resolve(in Input) (string, error) {
	switch in.Cfg.Strategy {
	case "git", "":
		// Preserve the historical git behavior verbatim, including its own
		// snapshot/dirty handling.
		return in.Git.Version, nil
	case "registry", "ecr":
		// Both list existing tags and bump the highest semver; the tag lister
		// (crane vs. aws) is injected by the caller.
		return resolveRegistry(in)
	case "static":
		return snapshotize(strings.TrimPrefix(in.Cfg.Value, "v"), in), nil
	case "env":
		val := getenv(in)(in.Cfg.Env)
		if val == "" {
			return "", fmt.Errorf("versioning: env var %q is empty", in.Cfg.Env)
		}
		return snapshotize(strings.TrimPrefix(val, "v"), in), nil
	case "command":
		if in.RunCmd == nil {
			return "", fmt.Errorf("versioning: command strategy has no command runner")
		}
		out, err := in.RunCmd(in.Cfg.Command)
		if err != nil {
			return "", fmt.Errorf("versioning command %q: %w", in.Cfg.Command, err)
		}
		return snapshotize(strings.TrimPrefix(firstLine(out), "v"), in), nil
	default:
		return "", fmt.Errorf("versioning: unsupported strategy %q", in.Cfg.Strategy)
	}
}

// resolveRegistry lists the repository's tags, finds the highest semver, and
// bumps it. When the repository has no semver tags, the configured initial
// version is used as-is.
func resolveRegistry(in Input) (string, error) {
	if in.ListTags == nil {
		return "", fmt.Errorf("versioning: registry strategy has no tag lister")
	}
	repo := in.Cfg.Repo
	if repo == "" {
		repo = in.Repo
	}
	if repo == "" {
		return "", fmt.Errorf("versioning: registry strategy needs a repo (set versioning.repo or define an image)")
	}
	tags, err := in.ListTags(repo)
	if err != nil {
		return "", fmt.Errorf("versioning: list tags for %s: %w", repo, err)
	}
	highest, found := highestSemver(tags)
	var base string
	if !found {
		base = strings.TrimPrefix(in.Cfg.Initial, "v")
	} else {
		base = bump(highest, in.Cfg.Bump).String()
	}
	return snapshotize(base, in), nil
}

// snapshotize appends the snapshot suffix (mirroring gitinfo) when building a
// snapshot; otherwise it returns base unchanged.
func snapshotize(base string, in Input) string {
	if !in.Snapshot {
		return base
	}
	sc := "unknown"
	if in.Git != nil && in.Git.ShortCommit != "" {
		sc = in.Git.ShortCommit
	}
	v := fmt.Sprintf("%s-SNAPSHOT-%s", base, sc)
	if in.Git != nil && in.Git.Dirty {
		v += "-dirty"
	}
	return v
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

// --- minimal semver (MAJOR.MINOR.PATCH only) ---

type semver struct{ major, minor, patch int }

func (v semver) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

func (v semver) less(o semver) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	if v.minor != o.minor {
		return v.minor < o.minor
	}
	return v.patch < o.patch
}

// parseSemver accepts an optional leading "v" and exactly three dot-separated
// non-negative integers. Prerelease/build-metadata tags are rejected so that
// only stable releases participate in "highest existing version" selection.
func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, false
		}
		nums[i] = n
	}
	return semver{nums[0], nums[1], nums[2]}, true
}

// highestSemver returns the greatest parseable stable version among tags.
func highestSemver(tags []string) (semver, bool) {
	var parsed []semver
	for _, t := range tags {
		if v, ok := parseSemver(t); ok {
			parsed = append(parsed, v)
		}
	}
	if len(parsed) == 0 {
		return semver{}, false
	}
	sort.Slice(parsed, func(i, j int) bool { return parsed[i].less(parsed[j]) })
	return parsed[len(parsed)-1], true
}

// bump increments v according to part (patch|minor|major).
func bump(v semver, part string) semver {
	switch part {
	case "major":
		return semver{v.major + 1, 0, 0}
	case "minor":
		return semver{v.major, v.minor + 1, 0}
	default: // patch
		return semver{v.major, v.minor, v.patch + 1}
	}
}
