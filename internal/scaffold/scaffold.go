// Package scaffold generates a starter stevedore config by scanning a repository
// for Dockerfiles.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skipDirs are never scanned for Dockerfiles.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	"dist": true, "obj": true, "bin": true, ".idea": true, ".vscode": true,
}

// Image is a detected buildable image.
type Image struct {
	ID         string
	Dockerfile string // path relative to the repo root
	Context    string // path relative to the repo root
}

// ScanDockerfiles finds Dockerfiles under dir and turns each into an Image. The
// id is derived from the Dockerfile's directory (or the project name at root),
// and the context defaults to that directory.
func ScanDockerfiles(dir, projectName string) ([]Image, error) {
	var imgs []Image
	seenID := map[string]int{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !isDockerfile(d.Name()) {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		relDir := filepath.Dir(rel)
		baseID := projectName
		if relDir != "." {
			baseID = filepath.Base(relDir)
		}
		if baseID == "" {
			baseID = "app"
		}
		// Disambiguate duplicate ids (e.g. several root-level *.Dockerfile).
		id := baseID
		if n := seenID[baseID]; n > 0 {
			id = fmt.Sprintf("%s-%d", baseID, n+1)
		}
		seenID[baseID]++

		ctx := relDir
		if ctx == "" {
			ctx = "."
		}
		imgs = append(imgs, Image{ID: id, Dockerfile: filepath.ToSlash(rel), Context: filepath.ToSlash(ctx)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(imgs, func(i, j int) bool { return imgs[i].Dockerfile < imgs[j].Dockerfile })
	return imgs, nil
}

func isDockerfile(name string) bool {
	return name == "Dockerfile" ||
		strings.HasPrefix(name, "Dockerfile.") ||
		strings.HasSuffix(name, ".Dockerfile")
}

// Render produces a starter .stevedore.yaml for the detected images. When imgs
// is empty it falls back to a single generic image.
func Render(projectName, owner string, imgs []Image) string {
	if projectName == "" {
		projectName = "app"
	}
	if owner == "" {
		owner = "your-org"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# stevedore configuration — scaffolded by `stevedore init`.\n")
	fmt.Fprintf(&b, "# docs: https://github.com/blairham/stevedore\n")
	fmt.Fprintf(&b, "version: 1\n\nproject_name: %s\ndefault_branch: main\n\n", projectName)

	b.WriteString("images:\n")
	if len(imgs) == 0 {
		imgs = []Image{{ID: projectName, Dockerfile: "Dockerfile", Context: "."}}
	}
	for _, img := range imgs {
		fmt.Fprintf(&b, "  - id: %s\n", img.ID)
		fmt.Fprintf(&b, "    dockerfile: %s\n", img.Dockerfile)
		fmt.Fprintf(&b, "    context: %s   # set to \".\" if the build copies from the repo root\n", img.Context)
		b.WriteString("    platforms: [linux/amd64, linux/arm64]\n")
		fmt.Fprintf(&b, "    repositories: [\"ghcr.io/%s/%s\"]\n", owner, img.ID)
		b.WriteString("    tags: [\"{{ .Version }}\", \"{{ .ShortCommit }}\", \"latest\"]\n")
		b.WriteString("    build_args: [\"VERSION={{ .Version }}\"]\n\n")
	}

	b.WriteString(`sign:
  cosign:
    enabled: true          # keyless (OIDC) signing; set key: for a keyed setup

sbom:
  enabled: true
  format: spdx-json

scan:
  enabled: true
  fail_on: critical        # block the release on findings at/above this severity

provenance:
  enabled: true            # SLSA build provenance

changelog:
  enabled: true
  exclude: ["^chore:", "^docs:", "^test:"]

# For a monorepo, scope change detection so unchanged images are skipped:
# change_detection:
#   shared_paths: ["Dockerfile", "*.sln"]
# and give each image 'paths:' (or 'project:' with resolver: dotnet).
`)
	return b.String()
}
