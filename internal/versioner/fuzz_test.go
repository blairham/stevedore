package versioner

import "testing"

// FuzzParseSemver checks that whatever parseSemver accepts survives a round
// trip through String, and that bumping it moves it strictly forward — the
// two things highest-existing-version selection relies on.
func FuzzParseSemver(f *testing.F) {
	for _, seed := range []string{"1.2.3", "v0.0.0", " v10.20.30 ", "1.2", "1.2.3-rc.1", "v-1.2.3", "01.2.3"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, ok := parseSemver(s)
		if !ok {
			return
		}
		back, ok := parseSemver(v.String())
		if !ok || back != v {
			t.Fatalf("parseSemver(%q) = %v, but %q parses as %v (ok=%v)", s, v, v.String(), back, ok)
		}
		// Overflow is out of scope here: no real tag carries a component
		// near MaxInt.
		if v.major > 1<<30 || v.minor > 1<<30 || v.patch > 1<<30 {
			return
		}
		for _, part := range []string{"major", "minor", "patch"} {
			if n := bump(v, part); !v.less(n) {
				t.Fatalf("bump(%v, %q) = %v, not greater", v, part, n)
			}
		}
	})
}
