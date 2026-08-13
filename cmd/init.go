package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/blairham/stevedore/internal/importer"
	"github.com/blairham/stevedore/internal/run"
	"github.com/blairham/stevedore/internal/scaffold"
)

func newInitCmd() *cobra.Command {
	var (
		force bool
		from  string
		file  string
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scan for Dockerfiles (or import an existing config) and write .stevedore.yaml",
		Long: "init scaffolds a .stevedore.yaml. By default it scans the project for\n" +
			"Dockerfiles (one image each). With --from it imports an existing setup:\n\n" +
			"  --from dockerfiles   scan Dockerfiles (default)\n" +
			"  --from goreleaser    import a GoReleaser config's dockers: blocks\n" +
			"  --from bake          import a docker-bake target set\n\n" +
			"Signing, SBOM, scan, and provenance are enabled by default — review before\n" +
			"running `stevedore release`.",
		RunE: func(_ *cobra.Command, _ []string) error {
			path := filepath.Join(flagDir, ".stevedore.yaml")
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			}
			name := filepath.Base(mustAbs(flagDir))

			content, summary, err := scaffoldContent(from, file, name)
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
			fmt.Printf("wrote %s — %s\n", path, summary)
			fmt.Println("review the repositories/tags, then run `stevedore check`")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	cmd.Flags().StringVar(&from, "from", "dockerfiles", "source: dockerfiles | goreleaser | bake")
	cmd.Flags().StringVar(&file, "file", "", "source file (for --from goreleaser|bake)")
	return cmd
}

func scaffoldContent(from, file, name string) (content, summary string, err error) {
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
		data, err := os.ReadFile(filepath.Join(flagDir, file))
		if err != nil {
			return "", "", fmt.Errorf("read goreleaser config: %w", err)
		}
		imgs, err := importer.FromGoReleaser(data)
		if err != nil {
			return "", "", err
		}
		return importer.RenderYAML(name, "goreleaser ("+file+")", imgs), fmt.Sprintf("imported %d image(s) from %s", len(imgs), file), nil

	case "bake":
		imgs, err := bakeImages(file)
		if err != nil {
			return "", "", err
		}
		return importer.RenderYAML(name, "docker-bake", imgs), fmt.Sprintf("imported %d image(s) from docker-bake", len(imgs)), nil

	default:
		return "", "", fmt.Errorf("unknown --from %q (want dockerfiles, goreleaser, or bake)", from)
	}
}

// bakeImages resolves a bake target set via `docker buildx bake --print` and
// imports the resulting targets.
func bakeImages(file string) ([]importer.Image, error) {
	r := run.New(false, flagVerbose)
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
