package fingerprint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/blairham/stevedore/internal/config"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func demoImage() config.Image {
	return config.Image{
		ID:         "app",
		Dockerfile: "Dockerfile",
		Context:    ".",
		Platforms:  []string{"linux/amd64"},
		BuildArgs:  []string{"VERSION={{ .Version }}"},
	}
}

func TestComputeStableAndSensitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n")

	img := demoImage()
	fp1, err := Compute(dir, img, "dist", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Recomputing with no change is stable.
	fp2, _ := Compute(dir, img, "dist", nil)
	if fp1 != fp2 {
		t.Errorf("fingerprint not stable: %s != %s", fp1, fp2)
	}

	// Changing a context file changes the fingerprint.
	writeFile(t, filepath.Join(dir, "main.go"), "package main // edited\n")
	fp3, _ := Compute(dir, img, "dist", nil)
	if fp3 == fp1 {
		t.Error("fingerprint should change when a context file changes")
	}
}

func TestComputeIgnoresDistAndGit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\n")
	writeFile(t, filepath.Join(dir, "app.txt"), "x")
	img := demoImage()

	fp1, _ := Compute(dir, img, "dist", nil)

	// Files under dist/ and .git/ must not affect the fingerprint.
	writeFile(t, filepath.Join(dir, "dist", "artifact.json"), "noise")
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main")
	fp2, _ := Compute(dir, img, "dist", nil)
	if fp1 != fp2 {
		t.Errorf("dist/ and .git/ should be ignored: %s != %s", fp1, fp2)
	}
}

func TestComputeSensitiveToBuildInputs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\n")
	base := demoImage()
	baseFP, _ := Compute(dir, base, "dist", nil)

	target := base
	target.Target = "builder"
	if fp, _ := Compute(dir, target, "dist", nil); fp == baseFP {
		t.Error("changing target should change the fingerprint")
	}

	args := base
	args.BuildArgs = []string{"FOO=bar"}
	if fp, _ := Compute(dir, args, "dist", nil); fp == baseFP {
		t.Error("changing build_args should change the fingerprint")
	}

	plat := base
	plat.Platforms = []string{"linux/amd64", "linux/arm64"}
	if fp, _ := Compute(dir, plat, "dist", nil); fp == baseFP {
		t.Error("changing platforms should change the fingerprint")
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fingerprints.json")

	// Missing file loads as empty.
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 0 {
		t.Errorf("missing state should be empty, got %v", s)
	}

	s["app"] = "abc123"
	s["web"] = "def456"
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded["app"] != "abc123" || loaded["web"] != "def456" {
		t.Errorf("round-trip mismatch: %v", loaded)
	}
}
