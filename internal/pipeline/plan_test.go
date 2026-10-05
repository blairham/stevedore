// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/fingerprint"
)

func evalFor(id, dockerfile, version string, changed bool, reason string) imageEval {
	return imageEval{
		plan: ImagePlan{
			Image:   config.Image{ID: id, Dockerfile: dockerfile, Context: "."},
			Version: version,
		},
		changed: changed,
		reason:  reason,
	}
}

func TestGroupPlans_GroupsIdenticalSpecsAndSkipsUnchangedGroups(t *testing.T) {
	evals := []imageEval{
		// a and b share one build spec; only a changed → both build together.
		evalFor("a", "Dockerfile", "1.0.0", true, "src changed"),
		evalFor("b", "Dockerfile", "2.0.0", false, "unchanged"),
		// c has its own spec and didn't change → skipped.
		evalFor("c", "other/Dockerfile", "3.0.0", false, "unchanged"),
	}
	toBuild, skipped := groupPlans("/repo", evals)

	if len(toBuild) != 1 {
		t.Fatalf("toBuild groups = %d, want 1", len(toBuild))
	}
	if got := evalIDs(toBuild[0]); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("group members = %v, want [a b]", got)
	}
	if len(skipped) != 1 || skipped[0].plan.Image.ID != "c" {
		t.Errorf("skipped = %v, want just c", evalIDs(skipped))
	}
}

func TestNewPlanResult_MatrixShape(t *testing.T) {
	toBuild := [][]imageEval{
		{
			evalFor("a", "Dockerfile", "1.0.1", true, "src changed"),
			evalFor("b", "Dockerfile", "2.0.5", false, "unchanged"),
		},
	}
	skipped := []imageEval{evalFor("c", "other/Dockerfile", "3.0.0", false, "unchanged")}

	r := newPlanResult(toBuild, skipped, false)

	if len(r.Include) != 1 {
		t.Fatalf("include entries = %d, want 1", len(r.Include))
	}
	e := r.Include[0]
	if e.Group != "a" {
		t.Errorf("group = %q, want a (first member)", e.Group)
	}
	if e.Only != "a,b" {
		t.Errorf("only = %q, want a,b", e.Only)
	}
	if e.Versions["a"] != "1.0.1" || e.Versions["b"] != "2.0.5" {
		t.Errorf("versions = %v, want per-member resolved versions", e.Versions)
	}
	if e.Pins != "--pin-version a=1.0.1 --pin-version b=2.0.5" {
		t.Errorf("pins = %q", e.Pins)
	}
	if e.Reason != "src changed" {
		t.Errorf("reason = %q, want the first changed member's reason", e.Reason)
	}
	if len(r.Skipped) != 1 || r.Skipped[0].ID != "c" {
		t.Errorf("skipped = %v, want just c", r.Skipped)
	}
}

func TestNewPlanResult_EmptyPlanMarshalsEmptyInclude(t *testing.T) {
	r := newPlanResult(nil, nil, false)
	if r.Include == nil || len(r.Include) != 0 {
		t.Errorf("include should be an empty (non-nil) slice so JSON emits [], got %#v", r.Include)
	}
}

func TestResolvePlans_OnlySkipsExcludedImages(t *testing.T) {
	ctx := newCtx("main", false)
	cfg := &config.Config{
		Versioning: config.Versioning{Strategy: "registry"},
		Images: []config.Image{
			{ID: "a", Repositories: []string{"reg/a"}, Tags: []string{"{{ .Version }}"}},
			{ID: "b", Repositories: []string{"reg/b"}, Tags: []string{"{{ .Version }}"}},
			{ID: "c", Repositories: []string{"reg/c"}, Tags: []string{"{{ .Version }}"}},
		},
	}
	// Excluded images must never reach version resolution — a matrix job's
	// credentials may only have registry access to its own repositories.
	versionFor := func(repo string) (string, error) {
		if repo == "reg/b" {
			t.Errorf("versionFor called for excluded image repo %s", repo)
		}
		return map[string]string{"reg/a": "1.0.1", "reg/c": "3.0.3"}[repo], nil
	}
	got, err := resolvePlans(cfg, ctx, false, versionFor, nil, []string{"c", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Image.ID != "a" || got[1].Image.ID != "c" {
		t.Errorf("plans = %v, want [a c] in config order", got)
	}
	if got[0].Version != "1.0.1" || got[1].Version != "3.0.3" {
		t.Errorf("versions = %s/%s, want 1.0.1/3.0.3", got[0].Version, got[1].Version)
	}
	anyVersion := func(string) (string, error) { return "0.0.1", nil }
	if all, _ := resolvePlans(cfg, ctx, false, anyVersion, nil, nil); len(all) != 3 {
		t.Errorf("empty filter should pass everything through, got %d", len(all))
	}
}

func TestValidateImageIDs(t *testing.T) {
	cfg := &config.Config{Images: []config.Image{{ID: "a"}, {ID: "b"}}}

	if err := validateImageIDs(cfg, Options{Only: []string{"a"}, PinVersions: map[string]string{"b": "1.0.0"}}); err != nil {
		t.Errorf("valid ids should pass, got %v", err)
	}
	if err := validateImageIDs(cfg, Options{Only: []string{"nope"}}); err == nil {
		t.Error("unknown --only id should error")
	}
	if err := validateImageIDs(cfg, Options{PinVersions: map[string]string{"nope": "1.0.0"}}); err == nil {
		t.Error("unknown --pin-version id should error")
	}
}

// unscopedRepo builds a git repo with one image that declares no paths: its
// context is services/api, whose .dockerignore drops Markdown. It returns the
// repo dir and a helper that writes a file and commits it.
func unscopedRepo(t *testing.T) (string, func(name string)) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.co",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.co",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(name string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(name+time.Now().String()), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", name)
		git("commit", "-q", "-m", name)
	}
	git("init", "-q", "-b", "main")
	for _, f := range []string{".stevedore.yaml", "README.md", "services/api/Dockerfile", "services/api/main.go"} {
		commit(f)
	}
	if err := os.WriteFile(filepath.Join(dir, "services/api/.dockerignore"), []byte("*.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "dockerignore")
	return dir, commit
}

func unscopedPrepared(dir string, markers bool) (Options, *Prepared) {
	cfg := &config.Config{Dist: "dist"}
	cfg.ChangeDetection.MarkerRefs = markers
	cfg.ChangeDetection.MarkerPrefix = "refs/releases/image/"
	o := Options{Dir: dir, ConfigPath: filepath.Join(dir, ".stevedore.yaml")}
	p := &Prepared{Config: cfg, Plans: []ImagePlan{{
		Image: config.Image{ID: "api", Dockerfile: "services/api/Dockerfile", Context: "services/api"},
	}}}
	return o, p
}

// An image with no paths used to count as changed on every commit, so a
// README-only commit released it. Its default scope is now its build context
// minus what its .dockerignore drops.
func TestEvaluateImages_UnscopedImageUsesItsContext(t *testing.T) {
	dir, commit := unscopedRepo(t)
	for _, tc := range []struct {
		file   string
		want   bool
		reason string
	}{
		{"README.md", false, "no matching files in context services/api (.dockerignore) since HEAD~1"},
		{"services/api/NOTES.md", false, "no matching files in context services/api (.dockerignore) since HEAD~1"},
		{"services/api/main.go", true, "services/api/main.go (in context services/api (.dockerignore)) since HEAD~1"},
		{"services/api/Dockerfile", true, "services/api/Dockerfile (in context"},
		{".stevedore.yaml", true, ".stevedore.yaml (in context"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			commit(tc.file)
			o, p := unscopedPrepared(dir, false)
			o.ChangedSince = "HEAD~1"
			evals, err := evaluateImages(o, p, fingerprint.State{})
			if err != nil {
				t.Fatal(err)
			}
			if e := evals[0]; e.changed != tc.want || !strings.HasPrefix(e.reason, tc.reason) {
				t.Errorf("changed=%v reason=%q, want changed=%v reason starting %q", e.changed, e.reason, tc.want, tc.reason)
			}
		})
	}
}

// Under marker refs an empty diff since the marker — a re-run of the commit
// already released — is unchanged, and so is a commit outside the context.
func TestEvaluateImages_UnscopedImageMarkerMode(t *testing.T) {
	dir, commit := unscopedRepo(t)
	cmd := exec.Command("git", "update-ref", "refs/releases/image/api", "HEAD")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v\n%s", err, out)
	}
	check := func(want bool, reason string) {
		t.Helper()
		o, p := unscopedPrepared(dir, true)
		evals, err := evaluateImages(o, p, fingerprint.State{})
		if err != nil {
			t.Fatal(err)
		}
		if e := evals[0]; e.changed != want || e.reason != reason {
			t.Errorf("changed=%v reason=%q, want changed=%v reason %q", e.changed, e.reason, want, reason)
		}
	}
	check(false, "no files changed since its release marker")
	commit("README.md")
	check(false, "no matching files in context services/api (.dockerignore) since its release marker")
	commit("services/api/main.go")
	check(true, "services/api/main.go (in context services/api (.dockerignore)) since its release marker")
}

// marker_refs and --changed-since together diff from the older base, per
// image. CI commonly passes --changed-since <push's before>; that used to turn
// marker mode off, so a change whose release failed (marker never advanced)
// was never rebuilt once the next push moved past it.
func TestEvaluateImages_MarkerWithChangedSince(t *testing.T) {
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.co",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.co",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	eval := func(dir, since string) imageEval {
		t.Helper()
		o, p := unscopedPrepared(dir, true)
		o.ChangedSince = since
		evals, err := evaluateImages(o, p, fingerprint.State{})
		if err != nil {
			t.Fatal(err)
		}
		return evals[0]
	}
	const marker = "refs/releases/image/api"

	t.Run("marker older than --changed-since: the marker", func(t *testing.T) {
		dir, commit := unscopedRepo(t)
		git(dir, "update-ref", marker, "HEAD")
		commit("services/api/main.go") // released, but the release failed
		commit("README.md")            // the next push touches nothing of api's
		e := eval(dir, "HEAD~1")
		want := "services/api/main.go (in context services/api (.dockerignore)) since its release marker"
		if !e.changed || e.reason != want {
			t.Errorf("changed=%v reason=%q, want changed=true reason %q", e.changed, e.reason, want)
		}
	})

	t.Run("--changed-since older than the marker: the ref", func(t *testing.T) {
		dir, commit := unscopedRepo(t)
		commit("services/api/main.go")
		git(dir, "update-ref", marker, "HEAD")
		commit("README.md")
		e := eval(dir, "HEAD~2")
		want := "services/api/main.go (in context services/api (.dockerignore)) since HEAD~2 (older than the release marker)"
		if !e.changed || e.reason != want {
			t.Errorf("changed=%v reason=%q, want changed=true reason %q", e.changed, e.reason, want)
		}
	})

	t.Run("both at HEAD: unchanged", func(t *testing.T) {
		dir, _ := unscopedRepo(t)
		git(dir, "update-ref", marker, "HEAD")
		e := eval(dir, "HEAD")
		if want := "no files changed since its release marker"; e.changed || e.reason != want {
			t.Errorf("changed=%v reason=%q, want changed=false reason %q", e.changed, e.reason, want)
		}
	})

	t.Run("unrelated bases: the union", func(t *testing.T) {
		dir, _ := unscopedRepo(t)
		git(dir, "update-ref", marker, "HEAD")
		tree := git(dir, "mktree") // empty tree: an orphan commit sharing no history
		orphan := git(dir, "commit-tree", tree, "-m", "orphan")
		e := eval(dir, orphan)
		if !e.changed || !strings.HasSuffix(e.reason, "since its release marker or "+orphan+" (unrelated bases)") {
			t.Errorf("changed=%v reason=%q, want changed=true via the union", e.changed, e.reason)
		}
	})
}

func TestUnion(t *testing.T) {
	if got, want := union([]string{"a", "b"}, []string{"b", "c", "c"}), []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("union = %v, want %v", got, want)
	}
}
