// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/summary"
	"github.com/blairham/stevedore/internal/tmpl"
)

func TestPinDigests(t *testing.T) {
	in := []summary.Image{
		{ID: "pushed", Pushed: true, Digest: "sha256:aa", Repositories: []string{"reg/a", "mirror/a"}},
		{ID: "released", AlreadyReleased: true, Skipped: true, Digest: "sha256:bb", Repositories: []string{"reg/b"}},
		{ID: "validated", Pushed: false, Digest: "", Repositories: []string{"reg/c"}},
		{ID: "unchanged", Pushed: true, Skipped: true, Digest: "sha256:dd", Repositories: []string{"reg/d"}}, // a skip carries no fresh push
	}
	out := pinDigests(in)
	if !slices.Equal(out[0].DigestRefs, []string{"reg/a@sha256:aa", "mirror/a@sha256:aa"}) {
		t.Errorf("pushed: %v", out[0].DigestRefs)
	}
	if !slices.Equal(out[1].DigestRefs, []string{"reg/b@sha256:bb"}) {
		t.Errorf("already released: %v", out[1].DigestRefs)
	}
	if out[2].DigestRefs != nil || out[3].DigestRefs != nil {
		t.Errorf("unpublished images pinned: %v / %v", out[2].DigestRefs, out[3].DigestRefs)
	}
}

// The kustomize images: stanza from the docs, written by a real run only.
const kustomizeTemplate = `images:
{{- range .Images }}
  - name: {{ .Repository }}
    digest: {{ .Digest }}
{{- end }}
`

func TestWriteOutputsFile(t *testing.T) {
	dir := t.TempDir()
	p := &Prepared{
		Config: &config.Config{ProjectName: "acme", Outputs: config.Outputs{File: "deploy/images.yaml", Template: kustomizeTemplate}},
		Ctx:    &tmpl.Context{Version: "1.2.3"},
	}
	result := summary.Result{Images: pinDigests([]summary.Image{
		{ID: "api", Pushed: true, Digest: "sha256:aa", Repositories: []string{"ghcr.io/acme/api"}},
		{ID: "web", Pushed: true, Skipped: true, Repositories: []string{"ghcr.io/acme/web"}},
	})}
	path := filepath.Join(dir, "deploy", "images.yaml")

	for name, o := range map[string]Options{
		"dry run":   {Dir: dir, DryRun: true},
		"no push":   {Dir: dir, NoPush: true},
		"split leg": {Dir: dir, SplitPlatforms: []string{"linux/arm64"}},
	} {
		if err := writeOutputsFile(o, p, result); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s wrote %s", name, path)
		}
	}

	if err := writeOutputsFile(Options{Dir: dir}, p, result); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "images:\n  - name: ghcr.io/acme/api\n    digest: sha256:aa\n"; string(got) != want {
		t.Errorf("outputs file =\n%s\nwant\n%s", got, want)
	}

	// Nothing pushed: no file, rather than one that reads as "deploy nothing".
	os.Remove(path)
	if err := writeOutputsFile(Options{Dir: dir}, p, summary.Result{Images: result.Images[1:]}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("outputs file written with no pinned image")
	}
}
