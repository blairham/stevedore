// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/blairham/stevedore/internal/importer"
	"github.com/blairham/stevedore/internal/run"
	"github.com/blairham/stevedore/internal/scaffold"
)

func newInitCmd() *cobra.Command {
	var (
		force        bool
		from         string
		file         string
		mapFields    []string
		mapBuildArgs []string
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scan for Dockerfiles (or import an existing config) and write .stevedore.yaml",
		Long: "init scaffolds a .stevedore.yaml. By default it scans the project for\n" +
			"Dockerfiles (one image each). With --from it imports an existing setup:\n\n" +
			"  --from dockerfiles   scan Dockerfiles (default)\n" +
			"  --from goreleaser    import a GoReleaser config's dockers: blocks\n" +
			"  --from bake          import a docker-bake target set\n" +
			"  --from services      import a directory of per-service manifests\n" +
			"                       (--file <dir>, one YAML per service)\n\n" +
			"For --from services, each manifest maps to one image. The default field\n" +
			"names are name, image, dockerfile, context, target, sourcePaths, and\n" +
			"project (emitted as a PROJECT build arg); adapt them to your schema with\n" +
			"--map (e.g. --map paths=source_paths, dotted paths reach nested keys) and\n" +
			"--map-build-arg (e.g. --map-build-arg BUILD_PROJECT=build.project).\n\n" +
			"Signing, SBOM, scan, and provenance are enabled by default — review before\n" +
			"running `stevedore release`.",
		RunE: func(c *cobra.Command, _ []string) error {
			path := filepath.Join(flagDir, ".stevedore.yaml")
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			}
			name := filepath.Base(mustAbs(flagDir))

			content, summary, err := scaffoldContent(
				run.New(c.Context(), false, flagVerbose),
				from,
				file,
				name,
				mapFields,
				mapBuildArgs,
			)
			if err != nil {
				return err
			}
			// The config is committed to the repository, so it gets the mode git
			// would check it out with.
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // G306: see above
				return err
			}
			fmt.Printf("wrote %s — %s\n", path, summary)
			added, err := ignoreDist(flagDir)
			if err != nil {
				return err
			}
			if added {
				fmt.Println("added dist/ to .gitignore — release refuses a dirty tree, and dist/ is written on every run")
			}
			fmt.Println("review the repositories/tags, then run `stevedore check`")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	cmd.Flags().StringVar(&from, "from", "dockerfiles", "source: dockerfiles | goreleaser | bake | services")
	cmd.Flags().StringVar(&file, "file", "", "source file (for --from goreleaser|bake) or directory (for --from services)")
	cmd.Flags().
		StringArrayVar(&mapFields, "map", nil, "services: map a config field to a manifest key, field=key (fields: id, repositories, dockerfile, context, target, paths)")
	cmd.Flags().
		StringArrayVar(&mapBuildArgs, "map-build-arg", nil, "services: emit a build arg from a manifest key, ARG=key (replaces the default PROJECT=project)")
	return cmd
}

func scaffoldContent(
	r *run.Runner,
	from, file, name string,
	mapFields, mapBuildArgs []string,
) (content, summary string, err error) {
	switch from {
	case "", "dockerfiles":
		imgs, err := scaffold.ScanDockerfiles(flagDir, name)
		if err != nil {
			return "", "", err
		}
		if len(imgs) == 0 {
			return scaffold.Render(name, "your-org", nil), "no Dockerfiles found (added one generic image)", nil
		}
		return scaffold.Render(name, "your-org", imgs), fmt.Sprintf("detected %d image(s) from Dockerfiles", len(imgs)), nil

	case "goreleaser":
		if file == "" {
			file = firstExisting(".goreleaser.yaml", ".goreleaser.yml")
		}
		data, err := os.ReadFile(filepath.Clean(filepath.Join(flagDir, file)))
		if err != nil {
			return "", "", fmt.Errorf("read goreleaser config: %w", err)
		}
		imgs, err := importer.FromGoReleaser(data)
		if err != nil {
			return "", "", err
		}
		return importer.RenderYAML(
				name,
				"goreleaser ("+file+")",
				imgs,
			), fmt.Sprintf(
				"imported %d image(s) from %s",
				len(imgs),
				file,
			), nil

	case "bake":
		imgs, err := bakeImages(r, file)
		if err != nil {
			return "", "", err
		}
		return importer.RenderYAML(
				name,
				"docker-bake",
				imgs,
			), fmt.Sprintf(
				"imported %d image(s) from docker-bake",
				len(imgs),
			), nil

	case "services":
		if file == "" {
			return "", "", fmt.Errorf("--from services needs --file <dir> pointing at the per-service manifests")
		}
		m, err := serviceMapping(mapFields, mapBuildArgs)
		if err != nil {
			return "", "", err
		}
		imgs, err := importer.FromServicesDir(filepath.Join(flagDir, file), m)
		if err != nil {
			return "", "", err
		}
		return importer.RenderYAML(
				name,
				"services ("+file+")",
				imgs,
			), fmt.Sprintf(
				"imported %d image(s) from %s",
				len(imgs),
				file,
			), nil

	default:
		return "", "", fmt.Errorf("unknown --from %q (want dockerfiles, goreleaser, bake, or services)", from)
	}
}

// ignoreDist makes sure dir's .gitignore covers dist/, creating the file if
// needed, and reports whether it changed anything. Every run writes to dist/ —
// a --dry-run included — so without this the quick start's own preview leaves
// the tree dirty and the real release that follows refuses to run.
func ignoreDist(dir string) (bool, error) {
	path := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		switch strings.TrimSpace(line) {
		case "dist", "dist/", "/dist", "/dist/":
			return false, nil
		}
	}
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	f, err := os.OpenFile( //nolint:gosec // G302: .gitignore is committed, like the config
		filepath.Clean(path),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0o644,
	)
	if err != nil {
		return false, err
	}
	_, err = f.WriteString(prefix + "dist/\n")
	// Close reports a write the kernel deferred; it is part of the write.
	err = errors.Join(err, f.Close())
	if err != nil {
		return false, err
	}
	return true, nil
}

// serviceMapping applies --map / --map-build-arg overrides on top of the
// default per-service manifest field names. Any --map-build-arg replaces the
// default build-arg set outright — the org's args are theirs to define.
func serviceMapping(mapFields, mapBuildArgs []string) (importer.ServiceMapping, error) {
	m := importer.DefaultServiceMapping()
	for _, kv := range mapFields {
		field, key, ok := strings.Cut(kv, "=")
		if !ok || field == "" || key == "" {
			return m, fmt.Errorf("--map %q: want field=manifest_key", kv)
		}
		dst, known := map[string]*string{
			"id":           &m.ID,
			"repositories": &m.Repositories,
			"dockerfile":   &m.Dockerfile,
			"context":      &m.Context,
			"target":       &m.Target,
			"paths":        &m.Paths,
		}[field]
		if !known {
			return m, fmt.Errorf("--map: unknown field %q (want id, repositories, dockerfile, context, target, or paths)", field)
		}
		*dst = key
	}
	if len(mapBuildArgs) > 0 {
		m.BuildArgs = map[string]string{}
	}
	for _, kv := range mapBuildArgs {
		arg, key, ok := strings.Cut(kv, "=")
		if !ok || arg == "" || key == "" {
			return m, fmt.Errorf("--map-build-arg %q: want ARG=manifest_key", kv)
		}
		m.BuildArgs[arg] = key
	}
	return m, nil
}

// bakeImages resolves a bake target set via `docker buildx bake --print` and
// imports the resulting targets.
func bakeImages(r *run.Runner, file string) ([]importer.Image, error) {
	args := []string{"buildx", "bake", "--print"}
	if file != "" {
		args = append(args, "--file", file)
	}
	out, err := r.Capture("docker", args...)
	if err != nil {
		return nil, fmt.Errorf("docker buildx bake --print: %w", err)
	}
	return importer.FromBakeJSON([]byte(out))
}

func firstExisting(names ...string) string {
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(flagDir, n)); err == nil {
			return n
		}
	}
	return names[0]
}

func mustAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
