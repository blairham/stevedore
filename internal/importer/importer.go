// Package importer converts an existing docker-bake, GoReleaser, or
// per-service manifest setup into a stevedore config, so teams can adopt
// stevedore without hand-writing one.
package importer

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Image is an imported image, richer than the scaffold form: it carries the
// repositories, tags, build args, and cache settings recovered from the source.
type Image struct {
	ID           string
	Dockerfile   string
	Context      string
	Target       string
	Platforms    []string
	BuildArgs    []string
	Labels       map[string]string
	Repositories []string
	Tags         []string
	Paths        []string
	CacheFrom    []string
	CacheTo      []string
}

// RenderYAML produces a full .stevedore.yaml for the imported images.
func RenderYAML(projectName, source string, imgs []Image) string {
	if projectName == "" {
		projectName = "app"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# stevedore configuration — imported from %s by `stevedore init`.\n", source)
	b.WriteString("# Review repositories/tags and the signing/scan settings below.\n")
	fmt.Fprintf(&b, "version: 1\n\nproject_name: %s\ndefault_branch: main\n\n", projectName)

	b.WriteString("images:\n")
	for _, img := range imgs {
		fmt.Fprintf(&b, "  - id: %s\n", img.ID)
		fmt.Fprintf(&b, "    dockerfile: %s\n", orDefault(img.Dockerfile, "Dockerfile"))
		fmt.Fprintf(&b, "    context: %s\n", orDefault(img.Context, "."))
		if img.Target != "" {
			fmt.Fprintf(&b, "    target: %s\n", img.Target)
		}
		writeList(&b, "platforms", img.Platforms)
		writeList(&b, "repositories", img.Repositories)
		writeList(&b, "tags", defaultTags(img.Tags))
		writeList(&b, "build_args", img.BuildArgs)
		writeList(&b, "paths", img.Paths)
		writeList(&b, "cache_from", img.CacheFrom)
		writeList(&b, "cache_to", img.CacheTo)
		if len(img.Labels) > 0 {
			b.WriteString("    labels:\n")
			for _, k := range sortedKeys(img.Labels) {
				fmt.Fprintf(&b, "      %s: %q\n", k, img.Labels[k])
			}
		}
		b.WriteString("\n")
	}

	b.WriteString(`sign:
  cosign:
    enabled: true

sbom:
  enabled: true
  format: spdx-json

scan:
  enabled: true
  fail_on: critical

provenance:
  enabled: true
  mode: max

changelog:
  enabled: true
  exclude: ["^chore:", "^docs:", "^test:"]
`)
	return b.String()
}

// writeList emits a block-style YAML sequence, quoting entries only when a
// plain scalar would misparse (templates, colons, comments, …).
func writeList(b *strings.Builder, key string, vals []string) {
	if len(vals) == 0 {
		return
	}
	fmt.Fprintf(b, "    %s:\n", key)
	for _, v := range vals {
		fmt.Fprintf(b, "      - %s\n", yamlScalar(v))
	}
}

// yamlScalar returns v quoted when it can't stand as a plain YAML scalar.
func yamlScalar(v string) string {
	if v == "" || v != strings.TrimSpace(v) || strings.ContainsAny(v, "{}[]:#'\"&*?|>%@`,") {
		return fmt.Sprintf("%q", v)
	}
	return v
}

func defaultTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{"{{ .Version }}"}
	}
	return tags
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- docker-bake ---

// FromBakeJSON parses the JSON emitted by `docker buildx bake --print` into
// images. Each bake target becomes one image.
func FromBakeJSON(data []byte) ([]Image, error) {
	var doc struct {
		Target map[string]struct {
			Context    string            `json:"context"`
			Dockerfile string            `json:"dockerfile"`
			Tags       []string          `json:"tags"`
			Platforms  []string          `json:"platforms"`
			Args       map[string]string `json:"args"`
			Labels     map[string]string `json:"labels"`
			Target     string            `json:"target"`
			CacheFrom  []string          `json:"cache-from"`
			CacheTo    []string          `json:"cache-to"`
		} `json:"target"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse bake --print output: %w", err)
	}
	var imgs []Image
	for name, t := range doc.Target {
		repos, tags := splitRefs(t.Tags)
		imgs = append(imgs, Image{
			ID:           name,
			Dockerfile:   orDefault(t.Dockerfile, "Dockerfile"),
			Context:      orDefault(t.Context, "."),
			Target:       t.Target,
			Platforms:    t.Platforms,
			BuildArgs:    mapToArgs(t.Args),
			Labels:       t.Labels,
			Repositories: repos,
			Tags:         tags,
			CacheFrom:    t.CacheFrom,
			CacheTo:      t.CacheTo,
		})
	}
	sort.Slice(imgs, func(i, j int) bool { return imgs[i].ID < imgs[j].ID })
	return imgs, nil
}

// --- GoReleaser ---

// FromGoReleaser parses a GoReleaser config's `dockers:` blocks. Because
// GoReleaser builds one image per architecture and combines them with
// docker_manifests, entries that share a repository are merged into a single
// multi-arch stevedore image (platforms unioned, arch suffixes stripped).
func FromGoReleaser(data []byte) ([]Image, error) {
	var doc struct {
		Dockers []struct {
			ImageTemplates     []string `yaml:"image_templates"`
			Dockerfile         string   `yaml:"dockerfile"`
			Goos               string   `yaml:"goos"`
			Goarch             string   `yaml:"goarch"`
			BuildFlagTemplates []string `yaml:"build_flag_templates"`
		} `yaml:"dockers"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse goreleaser config: %w", err)
	}
	if len(doc.Dockers) == 0 {
		return nil, fmt.Errorf("no `dockers:` blocks found in goreleaser config")
	}

	// Merge by the set of repositories so per-arch entries collapse into one.
	byRepo := map[string]*Image{}
	var order []string
	for _, d := range doc.Dockers {
		repos, tags := splitRefs(d.ImageTemplates)
		tags = stripArchTags(tags)
		key := strings.Join(repos, ",")
		img, ok := byRepo[key]
		if !ok {
			img = &Image{
				Dockerfile:   orDefault(d.Dockerfile, "Dockerfile"),
				Context:      ".",
				Repositories: repos,
				Labels:       map[string]string{},
			}
			byRepo[key] = img
			order = append(order, key)
		}
		img.Tags = mergeUnique(img.Tags, tags)
		if p := platform(d.Goos, d.Goarch); p != "" {
			img.Platforms = mergeUnique(img.Platforms, []string{p})
		}
		args, labels, plats := parseBuildFlags(d.BuildFlagTemplates)
		img.BuildArgs = mergeUnique(img.BuildArgs, args)
		img.Platforms = mergeUnique(img.Platforms, plats)
		maps.Copy(img.Labels, labels)
	}

	var imgs []Image
	for _, key := range order {
		img := byRepo[key]
		img.ID = idFromRepos(img.Repositories)
		if len(img.Labels) == 0 {
			img.Labels = nil
		}
		imgs = append(imgs, *img)
	}
	return imgs, nil
}

// parseBuildFlags splits GoReleaser build_flag_templates into build args,
// labels, and platforms.
func parseBuildFlags(flags []string) (args []string, labels map[string]string, platforms []string) {
	labels = map[string]string{}
	for _, f := range flags {
		switch {
		case strings.HasPrefix(f, "--build-arg="):
			args = append(args, strings.TrimPrefix(f, "--build-arg="))
		case strings.HasPrefix(f, "--label="):
			kv := strings.TrimPrefix(f, "--label=")
			if k, v, ok := strings.Cut(kv, "="); ok {
				labels[k] = v
			}
		case strings.HasPrefix(f, "--platform="):
			platforms = append(platforms, strings.TrimPrefix(f, "--platform="))
		}
	}
	return args, labels, platforms
}

// --- shared helpers ---

// splitRefs turns full "repo:tag" references into distinct repositories and tags.
func splitRefs(refs []string) (repos, tags []string) {
	repoSet, tagSet := map[string]bool{}, map[string]bool{}
	for _, ref := range refs {
		repo, tag := splitRef(ref)
		if repo != "" && !repoSet[repo] {
			repoSet[repo] = true
			repos = append(repos, repo)
		}
		if tag != "" && !tagSet[tag] {
			tagSet[tag] = true
			tags = append(tags, tag)
		}
	}
	return repos, tags
}

// splitRef splits a reference into repo and tag on the last colon, taking care
// not to split a registry:port host.
func splitRef(ref string) (repo, tag string) {
	i := strings.LastIndexByte(ref, ':')
	if i < 0 {
		return ref, ""
	}
	// A colon before the last slash is a host port, not a tag separator.
	if strings.IndexByte(ref[i:], '/') >= 0 {
		return ref, ""
	}
	return ref[:i], ref[i+1:]
}

// stripArchTags drops GoReleaser's per-arch tag suffixes (e.g. "-amd64",
// "-{{ .Arch }}") so the merged multi-arch image keeps clean tags.
func stripArchTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		for _, suf := range []string{"-amd64", "-arm64", "-arm", "-{{ .Arch }}", "-{{.Arch}}"} {
			t = strings.TrimSuffix(t, suf)
		}
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func platform(goos, goarch string) string {
	if goarch == "" {
		return ""
	}
	if goos == "" {
		goos = "linux"
	}
	return goos + "/" + goarch
}

func mapToArgs(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func mergeUnique(existing, add []string) []string {
	seen := map[string]bool{}
	for _, e := range existing {
		seen[e] = true
	}
	for _, a := range add {
		if !seen[a] {
			seen[a] = true
			existing = append(existing, a)
		}
	}
	return existing
}

// idFromRepos derives a short image id from the last path element of the first
// repository.
func idFromRepos(repos []string) string {
	if len(repos) == 0 {
		return "image"
	}
	parts := strings.Split(repos[0], "/")
	return parts[len(parts)-1]
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
