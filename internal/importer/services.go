package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ServiceMapping names the manifest fields the services importer reads. Each
// value is a dotted path into a service's YAML document (e.g. "build.target"),
// so the importer adapts to whatever per-service schema an org settled on.
type ServiceMapping struct {
	ID           string
	Repositories string
	Dockerfile   string
	Context      string
	Target       string
	Paths        string
	// BuildArgs maps build-arg names to manifest fields: every field present in
	// a manifest becomes "NAME=<value>" on that image.
	BuildArgs map[string]string
}

// DefaultServiceMapping is the conventional per-service manifest shape:
// name, image, dockerfile, target, project (→ PROJECT build arg), sourcePaths.
func DefaultServiceMapping() ServiceMapping {
	return ServiceMapping{
		ID:           "name",
		Repositories: "image",
		Dockerfile:   "dockerfile",
		Context:      "context",
		Target:       "target",
		Paths:        "sourcePaths",
		BuildArgs:    map[string]string{"PROJECT": "project"},
	}
}

// FromServicesDir reads every *.yaml / *.yml in dir — one service manifest
// each — and converts them to images using the field mapping. Files are
// processed in name order so the output is deterministic.
func FromServicesDir(dir string, m ServiceMapping) ([]Image, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read services dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch filepath.Ext(e.Name()) {
		case ".yaml", ".yml":
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var imgs []Image
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var doc map[string]any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		fallbackID := strings.TrimSuffix(name, filepath.Ext(name))
		img, err := serviceImage(doc, m, fallbackID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		imgs = append(imgs, img)
	}
	if len(imgs) == 0 {
		return nil, fmt.Errorf("no service manifests (*.yaml, *.yml) found in %s", dir)
	}
	return imgs, nil
}

// serviceImage maps one parsed manifest to an image. The id falls back to the
// manifest's filename when the mapped field is absent; repositories are
// required — a manifest that publishes nowhere is a config error worth
// surfacing at import time rather than at `stevedore check`.
func serviceImage(doc map[string]any, m ServiceMapping, fallbackID string) (Image, error) {
	id := lookupString(doc, m.ID)
	if id == "" {
		id = fallbackID
	}
	repos := lookupStrings(doc, m.Repositories)
	if len(repos) == 0 {
		return Image{}, fmt.Errorf("field %q missing (need at least one image repository)", m.Repositories)
	}
	var args []string
	for _, name := range sortedKeys(m.BuildArgs) {
		if v := lookupString(doc, m.BuildArgs[name]); v != "" {
			args = append(args, name+"="+v)
		}
	}
	return Image{
		ID:           id,
		Dockerfile:   lookupString(doc, m.Dockerfile),
		Context:      lookupString(doc, m.Context),
		Target:       lookupString(doc, m.Target),
		BuildArgs:    args,
		Repositories: repos,
		Paths:        lookupStrings(doc, m.Paths),
	}, nil
}

// lookup walks a dotted path through nested maps; nil when any hop is missing.
func lookup(doc map[string]any, path string) any {
	if path == "" {
		return nil
	}
	var cur any = doc
	for part := range strings.SplitSeq(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

// lookupString returns the scalar at path, stringifying numbers and bools so a
// manifest value like `port: 8080` still maps cleanly.
func lookupString(doc map[string]any, path string) string {
	switch v := lookup(doc, path).(type) {
	case nil:
		return ""
	case string:
		return v
	case bool, int, int64, uint64, float64:
		return fmt.Sprintf("%v", v)
	default:
		return ""
	}
}

// lookupStrings returns the value at path as a string list: a scalar becomes a
// one-element list, a sequence keeps its (string) elements.
func lookupStrings(doc map[string]any, path string) []string {
	switch v := lookup(doc, path).(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
