package versioner

import (
	"fmt"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/gitinfo"
)

func gi() *gitinfo.Info {
	return &gitinfo.Info{Version: "1.2.3", Tag: "v1.2.3", ShortCommit: "abc1234"}
}

func TestParseSemver(t *testing.T) {
	cases := map[string]struct {
		ok bool
		s  string
	}{
		"1.2.3":        {true, "1.2.3"},
		"v1.2.3":       {true, "1.2.3"},
		"0.0.0":        {true, "0.0.0"},
		"10.20.30":     {true, "10.20.30"},
		"1.2":          {false, ""},
		"1.2.3.4":      {false, ""},
		"latest":       {false, ""},
		"1.2.3-rc1":    {false, ""}, // prereleases excluded from selection
		"v1.2.3+build": {false, ""},
		"sha-abc":      {false, ""},
	}
	for in, want := range cases {
		got, ok := parseSemver(in)
		if ok != want.ok {
			t.Errorf("parseSemver(%q) ok=%v, want %v", in, ok, want.ok)
			continue
		}
		if ok && got.String() != want.s {
			t.Errorf("parseSemver(%q) = %s, want %s", in, got.String(), want.s)
		}
	}
}

func TestHighestSemver(t *testing.T) {
	tags := []string{"v1.0.0", "1.2.0", "latest", "1.10.0", "1.2.9", "v0.9.0", "nightly"}
	h, ok := highestSemver(tags)
	if !ok || h.String() != "1.10.0" {
		t.Errorf("highestSemver = %v (%v), want 1.10.0", h.String(), ok)
	}
	if _, ok := highestSemver([]string{"latest", "edge"}); ok {
		t.Error("expected no semver among non-semver tags")
	}
}

func TestBump(t *testing.T) {
	base, _ := parseSemver("1.4.9")
	cases := map[string]string{
		"patch": "1.4.10",
		"minor": "1.5.0",
		"major": "2.0.0",
	}
	for part, want := range cases {
		if got := bump(base, part).String(); got != want {
			t.Errorf("bump(1.4.9, %s) = %s, want %s", part, got, want)
		}
	}
}

func TestResolveGit(t *testing.T) {
	// git strategy returns gitinfo's version verbatim.
	got, err := Resolve(Input{Cfg: config.Versioning{Strategy: "git"}, Git: gi()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.2.3" {
		t.Errorf("git strategy = %q, want 1.2.3", got)
	}
}

func TestResolveRegistryBumps(t *testing.T) {
	list := func(repo string) ([]string, error) {
		if repo != "ghcr.io/x/app" {
			return nil, fmt.Errorf("unexpected repo %q", repo)
		}
		return []string{"1.3.9", "1.3.8", "latest", "v1.2.0"}, nil
	}
	got, err := Resolve(Input{
		Cfg:      config.Versioning{Strategy: "registry", Bump: "minor", Initial: "0.1.0"},
		Git:      gi(),
		Repo:     "ghcr.io/x/app",
		ListTags: list,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.4.0" {
		t.Errorf("registry minor bump of 1.3.9 = %q, want 1.4.0", got)
	}
}

func TestResolveRegistryEmptyUsesInitial(t *testing.T) {
	list := func(string) ([]string, error) { return []string{"latest", "main"}, nil }
	got, err := Resolve(Input{
		Cfg:      config.Versioning{Strategy: "registry", Bump: "patch", Initial: "0.1.0"},
		Git:      gi(),
		Repo:     "r",
		ListTags: list,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "0.1.0" {
		t.Errorf("empty registry = %q, want initial 0.1.0", got)
	}
}

func TestResolveRegistryPrefersConfiguredRepo(t *testing.T) {
	var queried string
	list := func(repo string) ([]string, error) { queried = repo; return []string{"2.0.0"}, nil }
	_, err := Resolve(Input{
		Cfg:      config.Versioning{Strategy: "registry", Bump: "patch", Repo: "explicit/repo"},
		Git:      gi(),
		Repo:     "default/repo",
		ListTags: list,
	})
	if err != nil {
		t.Fatal(err)
	}
	if queried != "explicit/repo" {
		t.Errorf("queried %q, want the explicit versioning.repo", queried)
	}
}

func TestResolveStaticEnvCommand(t *testing.T) {
	got, _ := Resolve(Input{Cfg: config.Versioning{Strategy: "static", Value: "v9.9.9"}, Git: gi()})
	if got != "9.9.9" {
		t.Errorf("static = %q, want 9.9.9 (v stripped)", got)
	}

	got, _ = Resolve(Input{
		Cfg:    config.Versioning{Strategy: "env", Env: "REL"},
		Git:    gi(),
		Getenv: func(k string) string { return map[string]string{"REL": "3.2.1"}[k] },
	})
	if got != "3.2.1" {
		t.Errorf("env = %q, want 3.2.1", got)
	}

	if _, err := Resolve(Input{
		Cfg:    config.Versioning{Strategy: "env", Env: "MISSING"},
		Git:    gi(),
		Getenv: func(string) string { return "" },
	}); err == nil {
		t.Error("empty env var should error")
	}

	got, _ = Resolve(Input{
		Cfg:    config.Versioning{Strategy: "command", Command: "echo x"},
		Git:    gi(),
		RunCmd: func(string) (string, error) { return "4.5.6\nextra\n", nil },
	})
	if got != "4.5.6" {
		t.Errorf("command = %q, want 4.5.6 (first line)", got)
	}
}

func TestResolveSnapshotSuffix(t *testing.T) {
	g := gi()
	g.Dirty = true
	got, err := Resolve(Input{
		Cfg:      config.Versioning{Strategy: "static", Value: "2.0.0"},
		Git:      g,
		Snapshot: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "2.0.0-SNAPSHOT-abc1234-dirty" {
		t.Errorf("snapshot static = %q, want 2.0.0-SNAPSHOT-abc1234-dirty", got)
	}
}

func TestResolveRegistrySnapshotBumpsThenSuffixes(t *testing.T) {
	list := func(string) ([]string, error) { return []string{"1.0.0"}, nil }
	got, err := Resolve(Input{
		Cfg:      config.Versioning{Strategy: "registry", Bump: "patch"},
		Git:      gi(),
		Repo:     "r",
		Snapshot: true,
		ListTags: list,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.0.1-SNAPSHOT-abc1234" {
		t.Errorf("registry snapshot = %q, want 1.0.1-SNAPSHOT-abc1234", got)
	}
}
