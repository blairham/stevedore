// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package changed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree creates each file (with its parent dirs) under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func loadScope(t *testing.T, root, context, dockerfile string) *ContextScope {
	t.Helper()
	s, ok, err := LoadContextScope(root,
		filepath.Join(root, filepath.FromSlash(context)),
		filepath.Join(root, filepath.FromSlash(dockerfile)),
		filepath.Join(root, ".stevedore.yaml"))
	if err != nil || !ok {
		t.Fatalf("LoadContextScope(%s) = ok %v, err %v", context, ok, err)
	}
	return s
}

func TestContextScopeDockerignore(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".stevedore.yaml":            "",
		"services/api/Dockerfile":    "FROM scratch",
		"services/api/main.go":       "",
		"services/api/.dockerignore": "# docs are not built\n*.md\n!KEEP.md\n/docs\n**/*.log\ntmp/\n  \n",
	})
	s := loadScope(t, root, "services/api", "services/api/Dockerfile")
	if got, want := s.Describe(), "context services/api (.dockerignore)"; got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}

	for file, want := range map[string]bool{
		"services/api/main.go":         true,
		"services/api/pkg/x/y.go":      true,
		"services/api/README.md":       false, // *.md
		"services/api/KEEP.md":         true,  // re-included by !KEEP.md
		"services/api/docs/guide.txt":  false, // /docs excludes the dir and all below it
		"services/api/pkg/docs/a.txt":  true,  // /docs is anchored to the context root
		"services/api/a/b/c.log":       false, // **/*.log at any depth
		"services/api/run.log":         false, // ** also matches zero dirs
		"services/api/tmp/cache.bin":   false, // tmp/ is cleaned to tmp
		"services/api/pkg/README.md":   true,  // *.md is one segment: root-level only
		"services/api/Dockerfile":      true,
		"services/api/.dockerignore":   true, // editing it changes the context
		".stevedore.yaml":              true, // build args/target/platforms live here
		"./services/api/main.go":       true,
		"README.md":                    false, // outside the context
		"services/web/main.go":         false,
		"services/api-gateway/main.go": false, // a sibling sharing the prefix
	} {
		if got := s.Contains(file); got != want {
			t.Errorf("Contains(%q) = %v, want %v", file, got, want)
		}
	}
}

// BuildKit reads `<Dockerfile>.dockerignore` next to the Dockerfile in
// preference to the context's .dockerignore — and the Dockerfile may live
// outside the context.
func TestContextScopeDockerfileSpecificIgnoreWins(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".dockerignore":                      "src/generated\n",
		"docker/api.Dockerfile":              "FROM scratch",
		"docker/api.Dockerfile.dockerignore": "web\n",
		"docker/web.Dockerfile":              "FROM scratch",
		"src/generated/x.go":                 "",
		"web/index.html":                     "",
	})
	api := loadScope(t, root, ".", "docker/api.Dockerfile")
	if got, want := api.Describe(), "context . (api.Dockerfile.dockerignore)"; got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
	for file, want := range map[string]bool{
		"web/index.html":                     false, // excluded by the Dockerfile's own ignore file
		"src/generated/x.go":                 true,  // the context's .dockerignore is not consulted
		"docker/api.Dockerfile":              true,
		"docker/api.Dockerfile.dockerignore": true,
		"docker/web.Dockerfile":              true, // inside the root context, not ignored
	} {
		if got := api.Contains(file); got != want {
			t.Errorf("api: Contains(%q) = %v, want %v", file, got, want)
		}
	}

	// web has no Dockerfile-specific file, so the context's applies.
	web := loadScope(t, root, ".", "docker/web.Dockerfile")
	if got, want := web.Describe(), "context . (.dockerignore)"; got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
	if web.Contains("src/generated/x.go") {
		t.Error("web: src/generated is excluded by the context .dockerignore")
	}
	if !web.Contains("web/index.html") {
		t.Error("web: web/index.html is in its context")
	}
}

func TestContextScopeNoIgnoreFile(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"Dockerfile": "FROM scratch"})
	s := loadScope(t, root, ".", "Dockerfile")
	if got, want := s.Describe(), "context ."; got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
	if !s.Contains("README.md") || !s.Contains("a/b/c") {
		t.Error("a root context with no dockerignore holds every file")
	}
}

func TestLoadContextScopeNotLocal(t *testing.T) {
	root := t.TempDir()
	for _, ctx := range []string{
		"https://github.com/acme/app.git", // a remote context
		filepath.Join(root, "missing"),
		filepath.Dir(root), // a directory outside the repository
	} {
		s, ok, err := LoadContextScope(root, ctx, filepath.Join(root, "Dockerfile"), "")
		if err != nil || ok || s != nil {
			t.Errorf("LoadContextScope(%q) = %v, %v, %v; want unscoped", ctx, s, ok, err)
		}
	}
}

func TestEvaluateTable(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"services/api/Dockerfile":    "FROM scratch",
		"services/api/.dockerignore": "*.md\n",
	})
	ctxScope := Scope{Context: loadScope(t, root, "services/api", "services/api/Dockerfile")}
	globScope := Scope{Paths: []string{"services/api/**"}}
	shared := []string{"go.work"}

	for _, tc := range []struct {
		name       string
		scope      Scope
		files      []string
		want       bool
		scoped     bool
		reasonPart string
	}{
		{"context: empty diff", ctxScope, nil, false, true, "no files changed"},
		{"globs: empty diff", globScope, nil, false, true, "no files changed"},
		{"context: README outside", ctxScope, []string{"README.md"}, false, true, "no matching files in context services/api (.dockerignore)"},
		{"context: ignored file inside", ctxScope, []string{"services/api/NOTES.md"}, false, true, "no matching files in context services/api"},
		{"context: source inside", ctxScope, []string{"README.md", "services/api/main.go"}, true, true, "services/api/main.go (in context services/api (.dockerignore))"},
		{"context: shared path", ctxScope, []string{"go.work"}, true, true, `go.work (matched "go.work")`},
		{"globs: match", globScope, []string{"services/api/NOTES.md"}, true, true, "matched"},
		{"globs: no match", globScope, []string{"README.md"}, false, true, "no matching files"},
		{"unscoped: always changed", Scope{}, []string{"README.md"}, true, false, "unscoped"},
		{"unscoped: even with an empty diff", Scope{}, nil, true, false, "unscoped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Evaluate(tc.scope, shared, tc.files)
			if d.Changed != tc.want || d.Scoped != tc.scoped || !strings.Contains(d.Reason, tc.reasonPart) {
				t.Errorf("Evaluate = %+v, want changed=%v scoped=%v reason containing %q", d, tc.want, tc.scoped, tc.reasonPart)
			}
		})
	}
}
