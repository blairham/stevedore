// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/blairham/stevedore/internal/progress"
	"github.com/blairham/stevedore/internal/summary"
	"github.com/blairham/stevedore/internal/tmpl"
)

// pinDigests returns images with DigestRefs filled in for every image whose
// digest is a published one: pushed by this run, or already released from
// this commit (its commit tag names it). A --no-push or dry-run image has no
// such digest, and is left alone.
func pinDigests(images []summary.Image) []summary.Image {
	out := make([]summary.Image, len(images))
	for i, img := range images {
		out[i] = img
		published := (img.Pushed && !img.Skipped) || img.AlreadyReleased
		if img.Digest == "" || !published {
			continue
		}
		out[i].DigestRefs = make([]string, len(img.Repositories))
		for j, repo := range img.Repositories {
			out[i].DigestRefs[j] = digestRef(repo, img.Digest)
		}
	}
	return out
}

// outputImage is one image as the outputs.template sees it.
type outputImage struct {
	ID, Version, Digest string
	Repository          string // the first repository
	Ref                 string // Repository@Digest
	Repositories        []string
	DigestRefs          []string
	Refs                []string // the tagged references
}

// outputsData is the outputs.template's data.
type outputsData struct {
	Project, Version string
	Images           []outputImage
}

// writeOutputsFile renders outputs.template over the pinned images to
// outputs.file. Only a real run writes it — never a dry run, a --no-push
// build or a split leg, none of which has a digest a consumer can pull — and
// only when some image was pinned: an empty file would read downstream as
// "deploy nothing".
func writeOutputsFile(o Options, p *Prepared, result summary.Result) error {
	cfg := p.Config.Outputs
	if cfg.File == "" {
		return nil
	}
	if o.DryRun || o.NoPush || len(o.SplitPlatforms) > 0 {
		return nil
	}
	pinned := result.Pinned()
	if len(pinned) == 0 {
		progress.Printf(progressOut, "==> no image pushed; %s not written\n", cfg.File)
		return nil
	}
	data := outputsData{Project: p.Config.ProjectName}
	if p.Ctx != nil {
		data.Version = p.Ctx.Version
	}
	for _, img := range pinned {
		data.Images = append(data.Images, outputImage{
			ID: img.ID, Version: img.Version, Digest: img.Digest,
			Repository: img.Repositories[0], Ref: img.DigestRefs[0],
			Repositories: img.Repositories, DigestRefs: img.DigestRefs, Refs: img.Refs,
		})
	}
	content, err := tmpl.RenderData(cfg.Template, data)
	if err != nil {
		return fmt.Errorf("outputs.template: %w", err)
	}
	path := cfg.File
	if !filepath.IsAbs(path) {
		path = filepath.Join(o.Dir, path)
	}
	if err := os.MkdirAll( //nolint:gosec // a directory the config names, like dist/
		filepath.Dir(path),
		0o755,
	); err != nil {
		return fmt.Errorf("outputs.file: %w", err)
	}
	if err := writeDistFile(path, []byte(content)); err != nil {
		return fmt.Errorf("outputs.file: %w", err)
	}
	progress.Printf(progressOut, "==> outputs written to %s\n", path)
	return nil
}
