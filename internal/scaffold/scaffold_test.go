package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanDockerfiles(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "apps", "frontend", "Dockerfile"))
	write(t, filepath.Join(dir, "apps", "backend", "Dockerfile"))
	write(t, filepath.Join(dir, "node_modules", "pkg", "Dockerfile")) // must be skipped
	write(t, filepath.Join(dir, "worker.Dockerfile"))                 // root, suffix form

	imgs, err := ScanDockerfiles(dir, "myproj")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, im := range imgs {
		got[im.ID] = im.Context
	}
	if _, ok := got["frontend"]; !ok {
		t.Errorf("expected frontend image, got %v", got)
	}
	if got["backend"] != "apps/backend" {
		t.Errorf("backend context = %q", got["backend"])
	}
	// Root *.Dockerfile → id from project name, context ".".
	if got["myproj"] != "." {
		t.Errorf("root Dockerfile should map to project id at context '.': %v", got)
	}
	for id := range got {
		if strings.Contains(id, "pkg") {
			t.Errorf("node_modules Dockerfile should have been skipped: %v", got)
		}
	}
}

func TestRenderEmptyFallback(t *testing.T) {
	out := Render("demo", "acme", nil)
	if !strings.Contains(out, "project_name: demo") {
		t.Error("missing project name")
	}
	if !strings.Contains(out, "id: demo") {
		t.Error("empty scan should produce one generic image with the project id")
	}
	if !strings.Contains(out, "ghcr.io/acme/demo") {
		t.Error("owner not applied to repository")
	}
}

func TestRenderMultiImage(t *testing.T) {
	out := Render("proj", "acme", []Image{
		{ID: "frontend", Dockerfile: "apps/frontend/Dockerfile", Context: "apps/frontend"},
		{ID: "backend", Dockerfile: "apps/backend/Dockerfile", Context: "apps/backend"},
	})
	if strings.Count(out, "- id:") != 2 {
		t.Errorf("expected 2 images:\n%s", out)
	}
	if !strings.Contains(out, "dockerfile: apps/frontend/Dockerfile") {
		t.Error("frontend dockerfile path missing")
	}
}
