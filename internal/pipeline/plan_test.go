package pipeline

import (
	"reflect"
	"testing"

	"github.com/blairham/stevedore/internal/config"
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

	r := newPlanResult(toBuild, skipped)

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
	r := newPlanResult(nil, nil)
	if r.Include == nil || len(r.Include) != 0 {
		t.Errorf("include should be an empty (non-nil) slice so JSON emits [], got %#v", r.Include)
	}
}

func TestResolvePlans_OnlySkipsExcludedImages(t *testing.T) {
	ctx := newCtx("main", "main", false)
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
