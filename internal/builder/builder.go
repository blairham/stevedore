// Package builder invokes `docker buildx build` to build and push images.
package builder

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

// Spec is a fully-resolved build request (all templates already rendered).
type Spec struct {
	ID         string
	Dockerfile string
	Context    string
	Target     string
	Platforms  []string
	BuildArgs  []string
	Labels     map[string]string
	Secrets    []config.Secret
	// Refs are the full repo:tag references to tag the build with.
	Refs []string
	// Push publishes to the registries.
	Push bool
	// Load loads the built image into the local docker daemon (single platform
	// only). Used for local builds; ignored when Push is set. When neither Push
	// nor Load is set, the build runs to validate only (no output) — the
	// --no-push case.
	Load bool
	// Provenance requests a SLSA provenance attestation (push only).
	Provenance bool
	// ProvenanceMode is "min" or "max" when Provenance is set.
	ProvenanceMode string
	// CacheFrom/CacheTo map to buildx --cache-from/--cache-to.
	CacheFrom []string
	CacheTo   []string
	// ExtraFlags are appended verbatim.
	ExtraFlags []string
}

// Build runs buildx for the spec and returns the pushed image digest (empty
// when not pushing or in dry-run mode).
func Build(r *run.Runner, s Spec) (string, error) {
	metaFile, cleanup, err := tempMetaFile(r)
	if err != nil {
		return "", err
	}
	defer cleanup()

	args := []string{"buildx", "build"}
	if len(s.Platforms) > 0 {
		args = append(args, "--platform", strings.Join(s.Platforms, ","))
	}
	args = append(args, "--file", s.Dockerfile)
	if s.Target != "" {
		args = append(args, "--target", s.Target)
	}
	for _, ref := range s.Refs {
		args = append(args, "--tag", ref)
	}
	for _, ba := range s.BuildArgs {
		args = append(args, "--build-arg", ba)
	}
	for _, k := range sortedKeys(s.Labels) {
		args = append(args, "--label", k+"="+s.Labels[k])
	}
	for _, sec := range s.Secrets {
		args = append(args, "--secret", secretArg(sec))
	}
	for _, c := range s.CacheFrom {
		args = append(args, "--cache-from", c)
	}
	for _, c := range s.CacheTo {
		args = append(args, "--cache-to", c)
	}
	switch {
	case s.Push:
		args = append(args, "--push")
		if metaFile != "" {
			args = append(args, "--metadata-file", metaFile)
		}
		// Attestations are only carried by the registry (OCI) exporter, so they
		// apply to pushes, not local --load or validate-only builds.
		if s.Provenance {
			mode := s.ProvenanceMode
			if mode == "" {
				mode = "max"
			}
			args = append(args, "--provenance=mode="+mode)
		}
	case s.Load:
		args = append(args, "--load")
	default:
		// Neither push nor load: build to validate only (no output). This is the
		// --no-push case — it proves every platform builds without publishing.
	}
	args = append(args, s.ExtraFlags...)
	args = append(args, s.Context)

	if err := r.Run("docker", args...); err != nil {
		return "", err
	}
	if !s.Push || r.DryRun || metaFile == "" {
		return "", nil
	}
	return readDigest(metaFile)
}

// secretArg renders a --secret value from an env- or file-backed secret.
func secretArg(s config.Secret) string {
	if s.File != "" {
		return fmt.Sprintf("id=%s,src=%s", s.ID, s.File)
	}
	env := s.Env
	if env == "" {
		env = s.ID
	}
	return fmt.Sprintf("id=%s,env=%s", s.ID, env)
}

func readDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read build metadata: %w", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", fmt.Errorf("parse build metadata: %w", err)
	}
	if d, ok := meta["containerimage.digest"].(string); ok && d != "" {
		return d, nil
	}
	return "", fmt.Errorf("build metadata missing containerimage.digest")
}

func tempMetaFile(r *run.Runner) (string, func(), error) {
	if r.DryRun {
		return "", func() {}, nil
	}
	f, err := os.CreateTemp("", "stevedore-meta-*.json")
	if err != nil {
		return "", func() {}, err
	}
	name := f.Name()
	f.Close()
	return name, func() { os.Remove(name) }, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
