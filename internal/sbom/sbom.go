// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package sbom generates software bills of materials for pushed images.
package sbom

import (
	"fmt"
	"path/filepath"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

// cyclonedx is the cosign predicate type for CycloneDX SBOMs, and also one of
// syft's spellings of the format.
const cyclonedx = "cyclonedx"

// PredicateType maps a syft format to the cosign attestation predicate type.
func PredicateType(format string) string {
	switch format {
	case "cyclonedx-json", cyclonedx:
		return cyclonedx
	default:
		return "spdxjson"
	}
}

// Generate produces an SBOM file, sbom-<name>.<ext>, for ref and returns its
// path. A non-empty platform selects that variant of a multi-platform image;
// without one syft picks the host's. The image must already be pushed so the
// generator can pull it by digest.
func Generate(r *run.Runner, cfg config.SBOM, distDir, name, ref, platform string) (string, error) {
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
	if PredicateType(cfg.Format) == cyclonedx {
		ext = "cdx.json"
	}
	out := filepath.Join(distDir, fmt.Sprintf("sbom-%s.%s", name, ext))
	// syft <ref> [--platform <platform>] -o <format>=<file>
	args := []string{ref}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	if err := r.Run("syft", append(args, "-o", cfg.Format+"="+out)...); err != nil {
		return "", fmt.Errorf("syft %s: %w", ref, err)
	}
	return out, nil
}
