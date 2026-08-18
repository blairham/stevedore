package tmpl

import (
	"testing"
	"time"

	"github.com/blairham/stevedore/internal/gitinfo"
)

func testCtx() *Context {
	gi := &gitinfo.Info{
		Version:     "1.2.3",
		Tag:         "v1.2.3",
		Commit:      "deadbeefcafe",
		ShortCommit: "deadbee",
		Branch:      "main",
	}
	return NewContext("demo", "main", gi, false, time.Unix(1700000000, 0).UTC(), map[string]string{"FOO": "bar"})
}

func TestNewContext(t *testing.T) {
	ctx := testCtx()
	if !ctx.IsDefault {
		t.Error("IsDefault should be true when branch == default")
	}
	if ctx.Version != "1.2.3" || ctx.ShortCommit != "deadbee" {
		t.Errorf("unexpected context: %+v", ctx)
	}
	if ctx.Date != "2023-11-14T22:13:20Z" {
		t.Errorf("Date = %q", ctx.Date)
	}
	if ctx.Timestamp != 1700000000 {
		t.Errorf("Timestamp = %d", ctx.Timestamp)
	}
}

func TestNewContextNonDefaultBranch(t *testing.T) {
	gi := &gitinfo.Info{Branch: "feature/x"}
	ctx := NewContext("demo", "main", gi, true, time.Unix(0, 0).UTC(), nil)
	if ctx.IsDefault {
		t.Error("IsDefault should be false on a feature branch")
	}
	if !ctx.IsSnapshot {
		t.Error("IsSnapshot should reflect the snapshot arg")
	}
}

func TestRender(t *testing.T) {
	ctx := testCtx()
	cases := map[string]string{
		"{{ .Version }}":                    "1.2.3",
		"v{{ .Version }}":                   "v1.2.3",
		"{{ .ShortCommit }}":                "deadbee",
		"{{ upper .Branch }}":               "MAIN",
		"{{ .Version }}-{{ .ShortCommit }}": "1.2.3-deadbee",
		`{{ trimPrefix .Tag "v" }}`:         "1.2.3",
		"{{ .Env.FOO }}":                    "bar",
		"static":                            "static",
	}
	for in, want := range cases {
		got, err := Render(in, ctx)
		if err != nil {
			t.Errorf("Render(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Render(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderMissingKeyIsError(t *testing.T) {
	ctx := testCtx()
	if _, err := Render("{{ .Nonexistent }}", ctx); err == nil {
		t.Error("expected error for unknown field (missingkey=error)")
	}
	if _, err := Render("{{ .Env.NOPE }}", ctx); err == nil {
		t.Error("expected error for missing env key")
	}
}

func TestRenderParseError(t *testing.T) {
	if _, err := Render("{{ .Version", testCtx()); err == nil {
		t.Error("expected parse error for malformed template")
	}
}

func TestRenderAll(t *testing.T) {
	ctx := testCtx()
	got, err := RenderAll([]string{"{{ .Version }}", "latest"}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "1.2.3" || got[1] != "latest" {
		t.Errorf("RenderAll = %v", got)
	}
	if _, err := RenderAll([]string{"{{ .Bad }}"}, ctx); err == nil {
		t.Error("RenderAll should propagate render errors")
	}
}

// TestNewContextDetachedHEAD covers the seam that let the floating-tag bug
// ship: every test above builds an Info with a branch name, which is the one
// shape a tag-triggered release never has.
func TestNewContextDetachedHEAD(t *testing.T) {
	onMain := &gitinfo.Info{
		Tag:      "v1.2.3",
		Branch:   "HEAD",
		Detached: true,
		Branches: []string{"origin/main", "main"},
	}
	ctx := NewContext("demo", "main", onMain, false, time.Unix(0, 0).UTC(), nil)
	if !ctx.IsDefault {
		t.Error("IsDefault = false for a tag cut from main; floating tags would be withheld from every release")
	}
	if !ctx.Detached {
		t.Error("Detached should be carried into the context so the pipeline can explain itself")
	}

	offMain := &gitinfo.Info{
		Tag:      "v1.2.3",
		Branch:   "HEAD",
		Detached: true,
		Branches: []string{"origin/release-1.x", "release-1.x"},
	}
	if NewContext("demo", "main", offMain, false, time.Unix(0, 0).UTC(), nil).IsDefault {
		t.Error("IsDefault = true for a tag cut off the default branch")
	}

	// A shallow clone knows no branches at all: unknown resolves to "no".
	unknown := &gitinfo.Info{Tag: "v1.2.3", Branch: "HEAD", Detached: true}
	if NewContext("demo", "main", unknown, false, time.Unix(0, 0).UTC(), nil).IsDefault {
		t.Error("IsDefault = true with no branch refs to go on")
	}
}
