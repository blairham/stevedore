// Package projgraph resolves a project's transitive source dependencies so that
// change detection can scope each image to exactly the directories it is built
// from — even when many images share one Dockerfile and build context.
package projgraph

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var projectRefRe = regexp.MustCompile(`(?i)<ProjectReference\s+[^>]*Include="([^"]+)"`)

// DotnetDeps walks the <ProjectReference> graph of the .NET project at
// projectRel (a path relative to repoRoot) and returns the repo-relative
// directories it transitively depends on, as "dir/**" globs, including the
// project's own directory. Cycles are handled; a referenced project that can't
// be read is an error, since a silently-missing edge would under-scope change
// detection and skip a build that should have run.
func DotnetDeps(repoRoot, projectRel string) ([]string, error) {
	visited := map[string]bool{}
	dirs := map[string]bool{}

	var walk func(projAbs string) error
	walk = func(projAbs string) error {
		projAbs = filepath.Clean(projAbs)
		if visited[projAbs] {
			return nil
		}
		visited[projAbs] = true

		relDir, err := filepath.Rel(repoRoot, filepath.Dir(projAbs))
		if err != nil {
			return err
		}
		dirs[filepath.ToSlash(relDir)] = true

		data, err := os.ReadFile(projAbs)
		if err != nil {
			return fmt.Errorf("read project %s: %w", projAbs, err)
		}
		for _, m := range projectRefRe.FindAllStringSubmatch(string(data), -1) {
			// MSBuild references use Windows-style backslashes even on Linux.
			ref := strings.ReplaceAll(m[1], `\`, "/")
			refAbs := filepath.Join(filepath.Dir(projAbs), ref)
			if err := walk(refAbs); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(filepath.Join(repoRoot, projectRel)); err != nil {
		return nil, err
	}

	globs := make([]string, 0, len(dirs))
	for d := range dirs {
		if d == "." || d == "" {
			globs = append(globs, "**")
			continue
		}
		globs = append(globs, d+"/**")
	}
	sort.Strings(globs)
	return globs, nil
}
