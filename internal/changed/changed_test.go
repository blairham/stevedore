package changed

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMarkerRef(t *testing.T) {
	if got := MarkerRef("refs/releases/image/", "checkout"); got != "refs/releases/image/checkout" {
		t.Errorf("MarkerRef = %q", got)
	}
}

func TestRefExistsAndAdvance(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@t.co")
	git("config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644)
	git("add", "-A")
	git("commit", "-qm", "init")

	ref := MarkerRef("refs/releases/image/", "svc")
	if RefExists(dir, ref) {
		t.Fatal("marker should not exist yet")
	}
	// No origin remote: AdvanceMarker just sets the ref locally.
	if err := AdvanceMarker(dir, ref); err != nil {
		t.Fatal(err)
	}
	if !RefExists(dir, ref) {
		t.Fatal("marker should exist after AdvanceMarker")
	}
	// The marker points at HEAD, so nothing changed since it.
	files, err := FilesSince(dir, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("expected no changes since marker, got %v", files)
	}
}

func TestMatch(t *testing.T) {
	patterns := []string{"Acme.Reports/**", "Directory.*", "*.sln"}
	cases := map[string]bool{
		"Acme.Reports/Foo.cs":         true,
		"Acme.Reports/sub/dir/Bar.cs": true,
		"Acme.Payments/Foo.cs":        false,
		"Directory.Build.props":       true,
		"Acme.sln":                    true,
		"README.md":                   false,
		"./Acme.Reports/Foo.cs":       true, // leading ./ tolerated
	}
	for path, want := range cases {
		if got := Match(patterns, path); got != want {
			t.Errorf("Match(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestEvaluateUnscoped(t *testing.T) {
	// No paths -> always changed (can't prove it's safe to skip).
	d := Evaluate(nil, []string{"Dockerfile"}, []string{"anything.txt"})
	if !d.Changed || d.Scoped {
		t.Errorf("unscoped image should be changed & unscoped: %+v", d)
	}
}

func TestEvaluateScoped(t *testing.T) {
	scoped := []string{"Acme.PaymentsGateway/**", "Acme.Fix/**", "Acme.Shared/**"}
	shared := []string{"Dockerfile", "*.sln"}

	// A file under one of its dependency dirs -> changed.
	d := Evaluate(scoped, shared, []string{"Acme.Fix/FixEngine.cs"})
	if !d.Changed || !d.Scoped {
		t.Errorf("should be changed via Fix dep: %+v", d)
	}

	// A shared file -> changed.
	if d := Evaluate(scoped, shared, []string{"Dockerfile"}); !d.Changed {
		t.Errorf("shared Dockerfile change should rebuild: %+v", d)
	}

	// An unrelated service -> not changed.
	if d := Evaluate(scoped, shared, []string{"Acme.Billing/Client.cs"}); d.Changed {
		t.Errorf("unrelated Billing change should NOT rebuild PaymentsGateway: %+v", d)
	}
}
