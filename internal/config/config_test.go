package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAppliesDefaults(t *testing.T) {
	p := writeTemp(t, ".stevedore.yaml", `
project_name: demo
images:
  - repositories:
      - ghcr.io/x/demo
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 {
		t.Errorf("version default = %d, want 1", c.Version)
	}
	if c.DefaultBranch != "main" {
		t.Errorf("default_branch = %q, want main", c.DefaultBranch)
	}
	if c.Dist != "dist" {
		t.Errorf("dist = %q, want dist", c.Dist)
	}
	if c.SBOM.Generator != "syft" || c.SBOM.Format != "spdx-json" {
		t.Errorf("sbom defaults = %+v", c.SBOM)
	}
	if c.Changelog.Sort != "asc" {
		t.Errorf("changelog.sort = %q, want asc", c.Changelog.Sort)
	}
	img := c.Images[0]
	if img.ID != "demo" {
		t.Errorf("image id default = %q, want demo (project_name)", img.ID)
	}
	if img.Dockerfile != "Dockerfile" || img.Context != "." {
		t.Errorf("dockerfile/context defaults = %q/%q", img.Dockerfile, img.Context)
	}
	if len(img.Platforms) != 1 || img.Platforms[0] != "linux/amd64" {
		t.Errorf("platform default = %v", img.Platforms)
	}
	if len(img.Tags) != 1 || img.Tags[0] != "{{ .Version }}" {
		t.Errorf("tags default = %v", img.Tags)
	}
}

func TestLoadIDFallbackWithoutProjectName(t *testing.T) {
	p := writeTemp(t, ".stevedore.yaml", `
images:
  - repositories: [ghcr.io/x/a]
  - repositories: [ghcr.io/x/b]
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Images[0].ID != "image0" || c.Images[1].ID != "image1" {
		t.Errorf("id fallbacks = %q, %q", c.Images[0].ID, c.Images[1].ID)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	p := writeTemp(t, ".stevedore.yaml", `
project_name: demo
bogus_field: true
images:
  - repositories: [ghcr.io/x/demo]
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "no images",
			cfg:     Config{Version: 1},
			wantErr: true,
		},
		{
			name: "image without repositories",
			cfg: Config{Version: 1, Images: []Image{
				{ID: "a"},
			}},
			wantErr: true,
		},
		{
			name: "duplicate ids",
			cfg: Config{Version: 1, Images: []Image{
				{ID: "a", Repositories: []string{"r1"}},
				{ID: "a", Repositories: []string{"r2"}},
			}},
			wantErr: true,
		},
		{
			name: "secret without env or file",
			cfg: Config{Version: 1, Images: []Image{
				{ID: "a", Repositories: []string{"r1"}, Secrets: []Secret{{ID: "s"}}},
			}},
			wantErr: true,
		},
		{
			name: "secret with empty id",
			cfg: Config{Version: 1, Images: []Image{
				{ID: "a", Repositories: []string{"r1"}, Secrets: []Secret{{Env: "E"}}},
			}},
			wantErr: true,
		},
		{
			name: "unsupported version",
			cfg: Config{Version: 2, Images: []Image{
				{ID: "a", Repositories: []string{"r1"}},
			}},
			wantErr: true,
		},
		{
			name: "valid",
			cfg: Config{Version: 1, Images: []Image{
				{ID: "a", Repositories: []string{"r1"}, Secrets: []Secret{{ID: "s", Env: "E"}}},
			}},
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateVersioning(t *testing.T) {
	base := func(v Versioning) Config {
		return Config{Version: 1, Images: []Image{{ID: "a", Repositories: []string{"r"}}}, Versioning: v}
	}
	cases := []struct {
		name    string
		v       Versioning
		wantErr bool
	}{
		{"empty defaults to git", Versioning{}, false},
		{"git", Versioning{Strategy: "git"}, false},
		{"registry ok", Versioning{Strategy: "registry", Bump: "minor", Lister: "crane"}, false},
		{"registry bad bump", Versioning{Strategy: "registry", Bump: "sideways"}, true},
		{"registry bad lister", Versioning{Strategy: "registry", Lister: "skopeo"}, true},
		{"static needs value", Versioning{Strategy: "static"}, true},
		{"static ok", Versioning{Strategy: "static", Value: "1.0.0"}, false},
		{"env needs var", Versioning{Strategy: "env"}, true},
		{"command needs command", Versioning{Strategy: "command"}, true},
		{"unknown strategy", Versioning{Strategy: "tarot"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base(tc.v)
			if err := cfg.Validate(); (err != nil) != tc.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestVersioningDefaults(t *testing.T) {
	p := writeTemp(t, ".stevedore.yaml", `
project_name: demo
versioning:
  strategy: registry
images:
  - repositories: [ghcr.io/x/demo]
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Versioning.Bump != "patch" || c.Versioning.Lister != "crane" || c.Versioning.Initial != "0.1.0" {
		t.Errorf("registry defaults not applied: %+v", c.Versioning)
	}
}

func TestValidateCosignKeyMustExist(t *testing.T) {
	cfg := Config{Version: 1, Images: []Image{{ID: "a", Repositories: []string{"r1"}}}}
	cfg.Sign.Cosign.Key = filepath.Join(t.TempDir(), "does-not-exist.key")
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing cosign key file")
	}

	keyPath := writeTemp(t, "cosign.key", "fake")
	cfg.Sign.Cosign.Key = keyPath
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid key file should pass: %v", err)
	}
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	if _, err := Discover(dir); err == nil {
		t.Fatal("expected error when no config present")
	}
	// stevedore.yaml is lower priority than .stevedore.yaml.
	if err := os.WriteFile(filepath.Join(dir, "stevedore.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".stevedore.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != ".stevedore.yaml" {
		t.Errorf("Discover picked %q, want .stevedore.yaml (highest priority)", filepath.Base(got))
	}
}
