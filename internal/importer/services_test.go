package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeServices(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestFromServicesDirDefaults(t *testing.T) {
	dir := writeServices(t, map[string]string{
		"api.yaml": `
name: api
image: ghcr.io/acme/api
dockerfile: docker/Dockerfile
target: runtime
project: src/Api/Api.csproj
sourcePaths:
  - src/Api/**
  - src/Shared/**
`,
		"web.yml": `
name: web
image:
  - ghcr.io/acme/web
  - 123.dkr.ecr.us-east-1.amazonaws.com/acme/web
`,
		"notes.txt": "not a manifest",
	})

	imgs, err := FromServicesDir(dir, DefaultServiceMapping())
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 2 {
		t.Fatalf("want 2 images, got %d: %+v", len(imgs), imgs)
	}
	api := imgs[0] // name-ordered: api.yaml, web.yml
	if api.ID != "api" || api.Dockerfile != "docker/Dockerfile" || api.Target != "runtime" {
		t.Errorf("api = %+v", api)
	}
	if len(api.Repositories) != 1 || api.Repositories[0] != "ghcr.io/acme/api" {
		t.Errorf("api repos = %v", api.Repositories)
	}
	if len(api.Paths) != 2 || api.Paths[0] != "src/Api/**" {
		t.Errorf("api paths = %v", api.Paths)
	}
	if len(api.BuildArgs) != 1 || api.BuildArgs[0] != "PROJECT=src/Api/Api.csproj" {
		t.Errorf("api build_args = %v", api.BuildArgs)
	}
	web := imgs[1]
	if web.ID != "web" || len(web.Repositories) != 2 {
		t.Errorf("web = %+v", web)
	}
	if len(web.BuildArgs) != 0 {
		t.Errorf("web has no project field, build_args = %v", web.BuildArgs)
	}
}

func TestFromServicesDirCustomMapping(t *testing.T) {
	dir := writeServices(t, map[string]string{
		"checkout.yaml": `
service: checkout
repo: 123.dkr.ecr.us-east-1.amazonaws.com/acme/checkout
build:
  dockerfile: Dockerfile.services
  stage: final
  project: Checkout.Api
source_paths:
  - src/Checkout/**
`,
	})

	m := ServiceMapping{
		ID:           "service",
		Repositories: "repo",
		Dockerfile:   "build.dockerfile",
		Target:       "build.stage",
		Paths:        "source_paths",
		BuildArgs:    map[string]string{"BUILD_PROJECT": "build.project"},
	}
	imgs, err := FromServicesDir(dir, m)
	if err != nil {
		t.Fatal(err)
	}
	img := imgs[0]
	if img.ID != "checkout" || img.Dockerfile != "Dockerfile.services" || img.Target != "final" {
		t.Errorf("img = %+v", img)
	}
	if len(img.BuildArgs) != 1 || img.BuildArgs[0] != "BUILD_PROJECT=Checkout.Api" {
		t.Errorf("build_args = %v", img.BuildArgs)
	}
	if len(img.Paths) != 1 || img.Paths[0] != "src/Checkout/**" {
		t.Errorf("paths = %v", img.Paths)
	}
}

func TestFromServicesDirIDFallsBackToFilename(t *testing.T) {
	dir := writeServices(t, map[string]string{
		"billing.yaml": "image: ghcr.io/acme/billing\n",
	})
	imgs, err := FromServicesDir(dir, DefaultServiceMapping())
	if err != nil {
		t.Fatal(err)
	}
	if imgs[0].ID != "billing" {
		t.Errorf("id = %q, want filename fallback \"billing\"", imgs[0].ID)
	}
}

func TestFromServicesDirErrors(t *testing.T) {
	if _, err := FromServicesDir(t.TempDir(), DefaultServiceMapping()); err == nil {
		t.Error("empty dir should error")
	}

	dir := writeServices(t, map[string]string{"api.yaml": "name: api\n"})
	if _, err := FromServicesDir(dir, DefaultServiceMapping()); err == nil || !strings.Contains(err.Error(), "image") {
		t.Errorf("manifest without repositories should error mentioning the field, got %v", err)
	}

	dir = writeServices(t, map[string]string{"bad.yaml": "{a: b"})
	if _, err := FromServicesDir(dir, DefaultServiceMapping()); err == nil {
		t.Error("unparsable manifest should error")
	}
}

func TestRenderYAMLServices(t *testing.T) {
	dir := writeServices(t, map[string]string{
		"api.yaml": `
name: api
image: ghcr.io/acme/api
project: src/Api/Api.csproj
sourcePaths: [src/Api/**]
`,
	})
	imgs, err := FromServicesDir(dir, DefaultServiceMapping())
	if err != nil {
		t.Fatal(err)
	}
	out := RenderYAML("acme", "services (.platform/services)", imgs)
	for _, want := range []string{
		"imported from services (.platform/services)",
		"- id: api",
		"repositories:\n      - ghcr.io/acme/api",
		"build_args:\n      - PROJECT=src/Api/Api.csproj",
		"paths:\n      - \"src/Api/**\"",
		"tags:\n      - \"{{ .Version }}\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered YAML missing %q:\n%s", want, out)
		}
	}
}
