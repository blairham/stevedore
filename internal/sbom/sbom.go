// Package sbom generates software bills of materials for pushed images.
package sbom

import (
	"fmt"
	"path/filepath"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

// PredicateType maps a syft format to the cosign attestation predicate type.
func PredicateType(format string) string {
	switch format {
	case "cyclonedx-json", "cyclonedx":
		return "cyclonedx"
	default:
		return "spdxjson"
	}
}

// Generate produces an SBOM file for ref and returns its path. The image must
// already be pushed so the generator can pull it by digest.
func Generate(r *run.Runner, cfg config.SBOM, distDir, imageID, ref string) (string, error) {
	if !cfg.Enabled {
		return "", nil
	}
	if cfg.Generator != "syft" {
		return "", fmt.Errorf("unsupported sbom generator %q (only syft)", cfg.Generator)
	}
	if !r.DryRun && !run.Has("syft") {
		return "", fmt.Errorf("sbom.enabled but syft not found on PATH")
	}
	ext := "spdx.json"
	if PredicateType(cfg.Format) == "cyclonedx" {
		ext = "cdx.json"
	}
	out := filepath.Join(distDir, fmt.Sprintf("sbom-%s.%s", imageID, ext))
	// syft <ref> -o <format>=<file>
	if err := r.Run("syft", ref, "-o", cfg.Format+"="+out); err != nil {
		return "", fmt.Errorf("syft %s: %w", ref, err)
	}
	return out, nil
}
