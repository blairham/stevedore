package importer

import (
	"strings"
	"testing"
)

func TestSplitRef(t *testing.T) {
	cases := map[string][2]string{
		"ghcr.io/acme/app:1.2.3":                   {"ghcr.io/acme/app", "1.2.3"},
		"ghcr.io/acme/app":                         {"ghcr.io/acme/app", ""},
		"localhost:5000/acme/app:latest":           {"localhost:5000/acme/app", "latest"},
		"localhost:5000/acme/app":                  {"localhost:5000/acme/app", ""}, // port, not tag
		"123.dkr.ecr.us-east-1.amazonaws.com/x:v1": {"123.dkr.ecr.us-east-1.amazonaws.com/x", "v1"},
	}
	for in, want := range cases {
		repo, tag := splitRef(in)
		if repo != want[0] || tag != want[1] {
			t.Errorf("splitRef(%q) = (%q,%q), want (%q,%q)", in, repo, tag, want[0], want[1])
		}
	}
}

const bakeJSON = `{
  "target": {
    "api": {
      "context": ".",
      "dockerfile": "api/Dockerfile",
      "tags": ["ghcr.io/acme/api:1.0", "ghcr.io/acme/api:latest"],
      "platforms": ["linux/amd64", "linux/arm64"],
      "args": {"VERSION": "1.0", "FOO": "bar"},
      "target": "prod",
      "cache-from": ["type=registry,ref=ghcr.io/acme/api:cache"]
    },
    "web": {
      "dockerfile": "web/Dockerfile",
      "tags": ["ghcr.io/acme/web:1.0"]
    }
  }
}`

func TestFromBakeJSON(t *testing.T) {
	imgs, err := FromBakeJSON([]byte(bakeJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 2 {
		t.Fatalf("want 2 images, got %d", len(imgs))
	}
	// sorted by id: api, web
	api := imgs[0]
	if api.ID != "api" || api.Dockerfile != "api/Dockerfile" || api.Target != "prod" {
		t.Errorf("api image = %+v", api)
	}
	if len(api.Repositories) != 1 || api.Repositories[0] != "ghcr.io/acme/api" {
		t.Errorf("api repos = %v", api.Repositories)
	}
	if len(api.Tags) != 2 {
		t.Errorf("api tags = %v", api.Tags)
	}
	if len(api.Platforms) != 2 {
		t.Errorf("api platforms = %v", api.Platforms)
	}
	// args map -> sorted "K=V"
	if strings.Join(api.BuildArgs, ",") != "FOO=bar,VERSION=1.0" {
		t.Errorf("api build_args = %v", api.BuildArgs)
	}
	if len(api.CacheFrom) != 1 {
		t.Errorf("api cache_from = %v", api.CacheFrom)
	}
}

const grYAML = `
dockers:
  - image_templates: ["ghcr.io/acme/app:{{ .Version }}-amd64"]
    dockerfile: Dockerfile
    goarch: amd64
    build_flag_templates:
      - "--build-arg=VERSION={{ .Version }}"
      - "--label=org.opencontainers.image.source=https://github.com/acme/app"
  - image_templates: ["ghcr.io/acme/app:{{ .Version }}-arm64"]
    dockerfile: Dockerfile
    goarch: arm64
    build_flag_templates:
      - "--build-arg=VERSION={{ .Version }}"
`

func TestFromGoReleaserMergesArches(t *testing.T) {
	imgs, err := FromGoReleaser([]byte(grYAML))
	if err != nil {
		t.Fatal(err)
	}
	// Two per-arch dockers with the same repo -> one multi-arch image.
	if len(imgs) != 1 {
		t.Fatalf("want 1 merged image, got %d: %+v", len(imgs), imgs)
	}
	img := imgs[0]
	if img.ID != "app" {
		t.Errorf("id = %q", img.ID)
	}
	if len(img.Platforms) != 2 || img.Platforms[0] != "linux/amd64" {
		t.Errorf("platforms = %v (want both arches)", img.Platforms)
	}
	// Arch suffix stripped, tag deduped to one.
	if len(img.Tags) != 1 || img.Tags[0] != "{{ .Version }}" {
		t.Errorf("tags = %v (want deduped, arch-stripped)", img.Tags)
	}
	if img.Labels["org.opencontainers.image.source"] == "" {
		t.Errorf("label not imported: %v", img.Labels)
	}
}

func TestRenderYAML(t *testing.T) {
	imgs, _ := FromBakeJSON([]byte(bakeJSON))
	out := RenderYAML("acme", "docker-bake", imgs)
	for _, want := range []string{
		"project_name: acme",
		"- id: api",
		"dockerfile: api/Dockerfile",
		"target: prod",
		"platforms: [linux/amd64, linux/arm64]",
		`repositories: ["ghcr.io/acme/api"]`,
		"cache_from:",
		"cosign:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered YAML missing %q:\n%s", want, out)
		}
	}
}
