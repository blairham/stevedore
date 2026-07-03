package changed

import "testing"

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
