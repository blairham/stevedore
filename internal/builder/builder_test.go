// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

func TestSecretArg(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "x")
	t.Setenv("GH_PRIVATE_TOKEN", "x")
	cases := []struct {
		name   string
		secret config.Secret
		want   string
		ok     bool
	}{
		{"file-backed", config.Secret{ID: "npmrc", File: "/run/secrets/npmrc"}, "id=npmrc,src=/run/secrets/npmrc", true},
		{"env-backed", config.Secret{ID: "token", Env: "GITHUB_TOKEN"}, "id=token,env=GITHUB_TOKEN", true},
		{"env defaults to id", config.Secret{ID: "GH_PRIVATE_TOKEN"}, "id=GH_PRIVATE_TOKEN,env=GH_PRIVATE_TOKEN", true},
		{"file wins over env", config.Secret{ID: "s", Env: "E", File: "/f"}, "id=s,src=/f", true},
		// An env-backed secret whose backing variable is unset is skipped, so a
		// config can declare a CI-minted token that is simply absent locally.
		{"env unset skips", config.Secret{ID: "gh_token", Env: "STEVEDORE_UNSET_SECRET_ENV"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := secretArg(tc.secret)
			if ok != tc.ok {
				t.Fatalf("secretArg ok = %v, want %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Errorf("secretArg = %q, want %q", got, tc.want)
			}
		})
	}
}

// An env-backed secret set to the empty string is treated as absent, matching
// the unset case — a minted-but-empty token must not reach buildx.
func TestSecretArgEmptyEnvSkips(t *testing.T) {
	t.Setenv("STEVEDORE_EMPTY_SECRET_ENV", "")
	if got, ok := secretArg(config.Secret{ID: "gh_token", Env: "STEVEDORE_EMPTY_SECRET_ENV"}); ok {
		t.Errorf("secretArg = %q, ok=true, want skipped", got)
	}
}

func TestSortedKeys(t *testing.T) {
	keys := sortedKeys(map[string]string{"b": "2", "a": "1", "c": "3"})
	want := []string{"a", "b", "c"}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("sortedKeys = %v, want %v", keys, want)
		}
	}
}

func TestReadDigest(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "good.json")
	os.WriteFile(good, []byte(`{"containerimage.digest":"sha256:abc123"}`), 0o644)
	got, err := readDigest(good)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sha256:abc123" {
		t.Errorf("digest = %q", got)
	}

	missing := filepath.Join(dir, "missing.json")
	os.WriteFile(missing, []byte(`{"other":"field"}`), 0o644)
	if _, err := readDigest(missing); err == nil {
		t.Error("expected error when digest field absent")
	}

	if _, err := readDigest(filepath.Join(dir, "nope.json")); err == nil {
		t.Error("expected error for missing file")
	}
}

// Push-by-digest legs must publish untagged: the tag flags are replaced by an
// --output that names every repo and pushes by digest only.
func TestBuildPushByDigest(t *testing.T) {
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	r := &run.Runner{DryRun: true, Stderr: stderr}

	_, err = Build(r, Spec{
		Dockerfile:   "Dockerfile",
		Context:      ".",
		Platforms:    []string{"linux/arm64"},
		Refs:         []string{"ghcr.io/x/app", "reg.io/x/app"},
		Push:         true,
		PushByDigest: true,
		Provenance:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	cmd := string(out)
	if !strings.Contains(cmd, `type=image,"name=ghcr.io/x/app,reg.io/x/app",push-by-digest=true,name-canonical=true,push=true`) {
		t.Errorf("push-by-digest output flag missing: %s", cmd)
	}
	if strings.Contains(cmd, "--tag") {
		t.Errorf("push-by-digest build must not tag: %s", cmd)
	}
	if strings.Contains(cmd, "--push ") || strings.HasSuffix(strings.TrimSpace(cmd), "--push") {
		t.Errorf("push-by-digest build must not also pass --push: %s", cmd)
	}
	if !strings.Contains(cmd, "--platform linux/arm64") {
		t.Errorf("platform flag missing: %s", cmd)
	}
	if !strings.Contains(cmd, "--provenance=mode=max") {
		t.Errorf("provenance must still apply to digest pushes: %s", cmd)
	}
}

func TestBuildSkipsEmptyCacheEntries(t *testing.T) {
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	r := &run.Runner{DryRun: true, Stderr: stderr}

	_, err = Build(r, Spec{
		Dockerfile: "Dockerfile",
		Context:    ".",
		CacheFrom:  []string{"", "type=gha,scope=build"},
		CacheTo:    []string{""},
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := os.ReadFile(stderr.Name())
	if err != nil {
		t.Fatal(err)
	}
	cmd := string(out)
	if !strings.Contains(cmd, "--cache-from type=gha,scope=build") {
		t.Errorf("non-empty cache_from entry missing from command: %s", cmd)
	}
	if strings.Count(cmd, "--cache-from") != 1 {
		t.Errorf("empty cache_from entry should be skipped: %s", cmd)
	}
	if strings.Contains(cmd, "--cache-to") {
		t.Errorf("empty cache_to entry should be skipped: %s", cmd)
	}
}

func TestSourceDateEpochBuildArg(t *testing.T) {
	args := strings.Join(buildxArgs(Spec{Dockerfile: "Dockerfile", Context: ".", SourceDateEpoch: "1714979289"}, ""), " ")
	if !strings.Contains(args, "--build-arg SOURCE_DATE_EPOCH=1714979289") {
		t.Errorf("SOURCE_DATE_EPOCH build arg missing: %s", args)
	}
	// The image's own wins and is not duplicated.
	args = strings.Join(buildxArgs(Spec{
		Dockerfile: "Dockerfile", Context: ".", SourceDateEpoch: "5",
		BuildArgs: []string{"SOURCE_DATE_EPOCH=5"},
	}, ""), " ")
	if strings.Count(args, "SOURCE_DATE_EPOCH=") != 1 {
		t.Errorf("SOURCE_DATE_EPOCH passed more than once: %s", args)
	}
	if args := strings.Join(buildxArgs(Spec{Dockerfile: "Dockerfile", Context: "."}, ""), " "); strings.Contains(args, "SOURCE_DATE_EPOCH") {
		t.Errorf("SOURCE_DATE_EPOCH passed when unset: %s", args)
	}
}

// buildx itself must see SOURCE_DATE_EPOCH in its environment: that is where
// it reads the value it hands BuildKit to clamp the image's timestamps.
func TestSourceDateEpochEnv(t *testing.T) {
	bin := t.TempDir()
	out := filepath.Join(bin, "env")
	script := "#!/bin/sh\necho \"$SOURCE_DATE_EPOCH\" > " + out + "\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SOURCE_DATE_EPOCH", "")
	if _, err := Build(&run.Runner{}, Spec{Dockerfile: "Dockerfile", Context: ".", SourceDateEpoch: "1714979289"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "1714979289" {
		t.Errorf("buildx saw SOURCE_DATE_EPOCH=%q, want 1714979289", got)
	}
}
