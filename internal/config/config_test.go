// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
			name: "notify webhook without url_env",
			cfg: Config{Version: 1, Images: []Image{
				{ID: "a", Repositories: []string{"r1"}},
			}, Notify: Notify{Webhook: NotifyWebhook{Enabled: true}}},
			wantErr: true,
		},
		{
			name: "notify webhook with url_env",
			cfg: Config{Version: 1, Images: []Image{
				{ID: "a", Repositories: []string{"r1"}},
			}, Notify: Notify{Webhook: NotifyWebhook{Enabled: true, URLEnv: "DEPLOY_HOOK"}}},
			wantErr: false,
		},
		{
			name: "valid",
			cfg: Config{Version: 1, Images: []Image{
				{ID: "a", Repositories: []string{"r1"}, Secrets: []Secret{{ID: "s", Env: "E"}}},
			}},
			wantErr: false,
		},
		{
			name: "policy requires every known stage",
			cfg: Config{Version: 1, Images: []Image{
				{ID: "a", Repositories: []string{"r1"}},
			}, Policy: Policy{Require: []string{"scan", "test", "sign", "sbom"}}},
			wantErr: false,
		},
		{
			name: "policy requires an unknown stage",
			cfg: Config{Version: 1, Images: []Image{
				{ID: "a", Repositories: []string{"r1"}},
			}, Policy: Policy{Require: []string{"scna"}}},
			wantErr: true,
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

func TestValidateScanArgs(t *testing.T) {
	base := func(scanner string, args ...string) Config {
		return Config{
			Version: 1, Images: []Image{{ID: "a", Repositories: []string{"r"}}},
			Scan: Scan{Enabled: true, Scanner: scanner, FailOn: "high", Args: args},
		}
	}
	cases := []struct {
		name     string
		cfg      Config
		wantFlag string // "" means valid
	}{
		{"trivy harmless args", base("trivy", "--skip-dirs", "/tmp", "--ignore-unfixed"), ""},
		{"grype harmless args", base("grype", "--only-fixed", "--scope", "all-layers"), ""},
		{"grype -f is fail-on, not format", base("grype", "-f", "high"), ""},
		{"trivy --format sarif", base("trivy", "--format", "sarif"), "--format"},
		{"trivy --format=cyclonedx", base("trivy", "--format=cyclonedx"), "--format"},
		{"trivy -f sarif", base("trivy", "-f", "sarif"), "-f"},
		{"trivy -fsarif", base("trivy", "-fsarif"), "-f"},
		{"trivy -o file", base("trivy", "-o", "out.json"), "-o"},
		{"trivy --output=file", base("trivy", "--output=out.json"), "--output"},
		{"trivy --template", base("trivy", "--template", "@x.tpl"), "--template"},
		{"grype -o sarif", base("grype", "-o", "sarif"), "-o"},
		{"grype -o=sarif", base("grype", "-o=sarif"), "-o"},
		{"grype --output cyclonedx-json", base("grype", "--output", "cyclonedx-json"), "--output"},
		{"grype --file", base("grype", "--file", "out.json"), "--file"},
		{"grype -t", base("grype", "-t", "x.tpl"), "-t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantFlag == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error naming %q", tc.wantFlag)
			}
			if !strings.Contains(err.Error(), "scan.args") || !strings.Contains(err.Error(), fmt.Sprintf("%q", tc.wantFlag)) {
				t.Errorf("Validate() = %v, want it to name scan.args and %q", err, tc.wantFlag)
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

func TestValidateCosignPublicKeyMustExist(t *testing.T) {
	cfg := Config{Version: 1, Images: []Image{{ID: "a", Repositories: []string{"r1"}}}}
	cfg.Sign.Cosign.PublicKey = filepath.Join(t.TempDir(), "does-not-exist.pub")
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "sign.cosign.public_key") {
		t.Fatalf("expected a public_key error for a missing file, got %v", err)
	}
	cfg.Sign.Cosign.PublicKey = writeTemp(t, "cosign.pub", "fake")
	if err := cfg.Validate(); err != nil {
		t.Errorf("valid public key file should pass: %v", err)
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

// scan.fail_on defaults to critical only when it is absent: "none" and an
// explicit "" both mean scan and report without gating (#46), and must survive
// Load's defaulting and pass validation.
func TestLoadScanFailOn(t *testing.T) {
	cases := []struct {
		name, line, want string
	}{
		{"absent", "", "critical"},
		{"null", "  fail_on:\n", "critical"},
		{"explicit empty", "  fail_on: \"\"\n", FailOnNone},
		{"none", "  fail_on: none\n", FailOnNone},
		{"high", "  fail_on: high\n", "high"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTemp(t, ".stevedore.yaml", "project_name: demo\nscan:\n  enabled: true\n"+tc.line+
				"images:\n  - repositories: [ghcr.io/x/demo]\n")
			c, err := Load(p)
			if err != nil {
				t.Fatal(err)
			}
			if c.Scan.FailOn != tc.want {
				t.Errorf("FailOn = %q, want %q", c.Scan.FailOn, tc.want)
			}
			if err := c.Validate(); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}

// scan.ignore accepts bare IDs (the original form) and {id, reason, expires}
// mappings in one list. An unquoted date is a YAML timestamp and must still
// decode to its text; a misspelled key must fail rather than yield an ignore
// that never expires.
func TestLoadScanIgnoreForms(t *testing.T) {
	load := func(t *testing.T, ignore string) (*Config, error) {
		t.Helper()
		p := writeTemp(t, ".stevedore.yaml", "project_name: demo\nscan:\n  enabled: true\n  ignore:\n"+ignore+
			"images:\n  - repositories: [ghcr.io/x/demo]\n")
		c, err := Load(p)
		if err != nil {
			return nil, err
		}
		return c, c.Validate()
	}

	c, err := load(t, "    - CVE-1\n    - id: CVE-2\n      reason: not reachable\n      expires: 2026-12-31\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []ScanIgnore{{ID: "CVE-1"}, {ID: "CVE-2", Reason: "not reachable", Expires: "2026-12-31"}}
	if !slices.Equal(c.Scan.Ignore, want) {
		t.Errorf("Ignore = %+v, want %+v", c.Scan.Ignore, want)
	}

	for name, ignore := range map[string]string{
		"unknown key": "    - id: CVE-2\n      expiry: 2026-12-31\n",
		"bad date":    "    - id: CVE-2\n      expires: next year\n",
		"missing id":  "    - reason: nope\n",
		"nested":      "    - [CVE-1]\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := load(t, ignore); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestValidateScanVEXMustExist(t *testing.T) {
	s := Scan{Enabled: true, Scanner: "grype", FailOn: "high", VEX: []string{filepath.Join(t.TempDir(), "missing.json")}}
	if err := s.validate(); err == nil {
		t.Fatal("expected error for missing VEX document")
	}
	s.VEX = []string{writeTemp(t, "doc.vex.json", "{}")}
	if err := s.validate(); err != nil {
		t.Errorf("existing VEX document should pass: %v", err)
	}
}

func TestValidateNotifyPayloadTemplateParses(t *testing.T) {
	cfg := Config{Version: 1, Images: []Image{{ID: "a", Repositories: []string{"r1"}}}}
	cfg.Notify.Webhook = NotifyWebhook{Enabled: true, URLEnv: "X", PayloadTemplate: `{"s": {{ json .Image }`}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "notify.webhook.payload_template") {
		t.Fatalf("an unparseable payload template should fail validation, got %v", err)
	}
	cfg.Notify.Webhook.PayloadTemplate = `{"s": {{ json .Image }}}`
	if err := cfg.Validate(); err != nil {
		t.Errorf("a valid payload template should pass: %v", err)
	}
}

func TestNotifyWebhookIsRequiredDefaultsTrue(t *testing.T) {
	f, tr := false, true
	for _, c := range []struct {
		in   *bool
		want bool
	}{{nil, true}, {&tr, true}, {&f, false}} {
		if got := (NotifyWebhook{Required: c.in}).IsRequired(); got != c.want {
			t.Errorf("IsRequired(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestValidateCache(t *testing.T) {
	base := func(c Cache) *Config {
		return &Config{Version: 1, Cache: c, Images: []Image{{ID: "a", Repositories: []string{"r/a"}}}}
	}
	for _, c := range []struct {
		cache Cache
		ok    bool
	}{
		{Cache{}, true},
		{Cache{Type: "none"}, true},
		{Cache{Type: "gha"}, true},
		{Cache{Type: "gha", Ref: "x"}, false},
		{Cache{Type: "registry"}, false},
		{Cache{Type: "registry", Ref: "ghcr.io/acme/cache", Mode: "min"}, true},
		{Cache{Type: "local", Ref: "/tmp/c", Mode: "all"}, false},
		{Cache{Type: "s3"}, false},
		{Cache{Ref: "x"}, false},
	} {
		if err := base(c.cache).Validate(); (err == nil) != c.ok {
			t.Errorf("%+v: Validate = %v, want ok=%v", c.cache, err, c.ok)
		}
	}
}

func TestValidateOutputs(t *testing.T) {
	base := func(o Outputs) *Config {
		return &Config{Version: 1, Outputs: o, Images: []Image{{ID: "a", Repositories: []string{"r/a"}}}}
	}
	for _, c := range []struct {
		o  Outputs
		ok bool
	}{
		{Outputs{}, true},
		{Outputs{File: "images.yaml", Template: "{{ range .Images }}{{ .Ref }}{{ end }}"}, true},
		{Outputs{File: "images.yaml"}, false},
		{Outputs{Template: "x"}, false},
		{Outputs{File: "images.yaml", Template: "{{ range .Images }"}, false},
	} {
		if err := base(c.o).Validate(); (err == nil) != c.ok {
			t.Errorf("%+v: Validate = %v, want ok=%v", c.o, err, c.ok)
		}
	}
}
