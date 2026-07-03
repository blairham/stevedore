package projgraph

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeProj writes a minimal .csproj referencing the given sibling projects.
func writeProj(t *testing.T, root, name string, refs ...string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var body string
	for _, r := range refs {
		// MSBuild uses backslashes even on Unix.
		body += `  <ItemGroup><ProjectReference Include="..\` + r + `\` + r + `.csproj" /></ItemGroup>` + "\n"
	}
	content := "<Project>\n" + body + "</Project>\n"
	if err := os.WriteFile(filepath.Join(dir, name+".csproj"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDotnetDepsTransitive(t *testing.T) {
	root := t.TempDir()
	// Graph: Gateway -> {Payments, Fix}; Payments -> Shared; Fix -> Shared; Billing -> Shared.
	writeProj(t, root, "Shared")
	writeProj(t, root, "Fix", "Shared")
	writeProj(t, root, "Payments", "Shared")
	writeProj(t, root, "Gateway", "Payments", "Fix")
	writeProj(t, root, "Billing", "Shared")

	deps, err := DotnetDeps(root, "Gateway/Gateway.csproj")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Fix/**", "Gateway/**", "Payments/**", "Shared/**"}
	if !slices.Equal(deps, want) {
		t.Errorf("Gateway deps = %v, want %v", deps, want)
	}

	// Billing must NOT pull in Fix — the whole point of graph precision.
	kdeps, _ := DotnetDeps(root, "Billing/Billing.csproj")
	if slices.Contains(kdeps, "Fix/**") {
		t.Errorf("Billing should not depend on Fix: %v", kdeps)
	}
	if !slices.Equal(kdeps, []string{"Billing/**", "Shared/**"}) {
		t.Errorf("Billing deps = %v, want [Billing/** Shared/**]", kdeps)
	}
}

func TestDotnetDepsCycle(t *testing.T) {
	root := t.TempDir()
	// A -> B -> A (pathological but must terminate).
	writeProj(t, root, "A", "B")
	writeProj(t, root, "B", "A")
	deps, err := DotnetDeps(root, "A/A.csproj")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(deps, []string{"A/**", "B/**"}) {
		t.Errorf("cyclic deps = %v, want [A/** B/**]", deps)
	}
}

func TestDotnetDepsMissingRefErrors(t *testing.T) {
	root := t.TempDir()
	writeProj(t, root, "A", "Ghost") // Ghost.csproj doesn't exist
	if _, err := DotnetDeps(root, "A/A.csproj"); err == nil {
		t.Error("expected error for missing referenced project")
	}
}
