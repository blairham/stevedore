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
	cases := []struct {
		name   string
		secret config.Secret
		want   string
	}{
		{"file-backed", config.Secret{ID: "npmrc", File: "/run/secrets/npmrc"}, "id=npmrc,src=/run/secrets/npmrc"},
		{"env-backed", config.Secret{ID: "token", Env: "GITHUB_TOKEN"}, "id=token,env=GITHUB_TOKEN"},
		{"env defaults to id", config.Secret{ID: "GITHUB_TOKEN"}, "id=GITHUB_TOKEN,env=GITHUB_TOKEN"},
		{"file wins over env", config.Secret{ID: "s", Env: "E", File: "/f"}, "id=s,src=/f"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := secretArg(tc.secret); got != tc.want {
				t.Errorf("secretArg = %q, want %q", got, tc.want)
			}
		})
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
