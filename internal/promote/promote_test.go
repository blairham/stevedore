// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package promote

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/run"
	"github.com/blairham/stevedore/internal/verifier"
)

// The fakes log every invocation. crane resolves the source tag and reports
// which legacy cosign tags exist; cosign verify fails for any ref listed in
// FAKE_UNSIGNED.
const fakeCrane = `#!/bin/sh
echo "crane $*" >> "$FAKE_LOG"
if [ "$1" = digest ]; then
	case "$2" in
	*:sha256-*.sig) [ -n "$FAKE_LEGACY" ] && echo sha256:sig && exit 0; exit 1 ;;
	*:sha256-*) exit 1 ;;
	*) echo sha256:abc ;;
	esac
fi
exit 0
`

const fakeCosign = `#!/bin/sh
echo "cosign $*" >> "$FAKE_LOG"
for a in "$@"; do last=$a; done
case " $FAKE_UNSIGNED " in
*" $last "*) echo "no signatures found" >&2; exit 10 ;;
esac
echo ok
`

const fakeOras = `#!/bin/sh
echo "oras $*" >> "$FAKE_LOG"
`

func harness(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"crane": fakeCrane, "cosign": fakeCosign, "oras": fakeOras} {
		if err := os.WriteFile(
			filepath.Join(dir, name),
			[]byte(body),
			0o755,
		); err != nil { //nolint:gosec // G306: a test fake must be executable
			t.Fatal(err)
		}
	}
	log := filepath.Join(dir, "calls.log")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_LOG", log)
	t.Setenv("FAKE_UNSIGNED", "")
	t.Setenv("FAKE_LEGACY", "")
	return log
}

func calls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func index(cs []string, sub string) int {
	for i, c := range cs {
		if strings.Contains(c, sub) {
			return i
		}
	}
	return -1
}

func runner(t *testing.T) *run.Runner {
	t.Helper()
	sink, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	return &run.Runner{Stdout: sink, Stderr: sink}
}

func opts() Options {
	return Options{
		Source: "reg.io/dev/app",
		From:   "1.2.3",
		Tags:   []string{"1.2.3", "stable"},
		Repos:  []string{"reg.io/dev/app", "reg.io/prod/app"},
		Verify: verifier.Options{Key: "cosign.pub"},
	}
}

// The source is verified before anything is copied, the destination is
// verified after the copy, and no tag is applied anywhere until both pass.
func TestPromoteOrder(t *testing.T) {
	log := harness(t)
	d, err := Promote(runner(t), opts(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if d != "sha256:abc" {
		t.Errorf("digest = %q", d)
	}
	cs := calls(t, log)
	srcVerify := index(cs, "cosign verify --key cosign.pub reg.io/dev/app@sha256:abc")
	cp := index(cs, "oras copy -r reg.io/dev/app@sha256:abc reg.io/prod/app@sha256:abc")
	dstVerify := index(cs, "cosign verify --key cosign.pub reg.io/prod/app@sha256:abc")
	firstTag := index(cs, "crane tag")
	if srcVerify < 0 || cp < 0 || dstVerify < 0 || firstTag < 0 {
		t.Fatalf("missing step:\n%s", strings.Join(cs, "\n"))
	}
	if srcVerify >= cp || cp >= dstVerify || dstVerify >= firstTag {
		t.Errorf("want verify source < copy < verify destination < tag:\n%s", strings.Join(cs, "\n"))
	}
	for _, want := range []string{
		"crane tag reg.io/dev/app@sha256:abc 1.2.3", "crane tag reg.io/dev/app@sha256:abc stable",
		"crane tag reg.io/prod/app@sha256:abc 1.2.3", "crane tag reg.io/prod/app@sha256:abc stable",
	} {
		if index(cs, want) < 0 {
			t.Errorf("missing %q:\n%s", want, strings.Join(cs, "\n"))
		}
	}
	if index(cs, "oras copy -r reg.io/dev/app@sha256:abc reg.io/dev/app@") >= 0 {
		t.Errorf("the source repository must not be copied onto itself:\n%s", strings.Join(cs, "\n"))
	}
}

// An unsigned source is refused before anything is copied or tagged; a copy
// whose destination does not verify is never tagged there.
func TestPromoteRefusesUnverified(t *testing.T) {
	for name, unsigned := range map[string]string{
		"source":      "reg.io/dev/app@sha256:abc",
		"destination": "reg.io/prod/app@sha256:abc",
	} {
		t.Run(name, func(t *testing.T) {
			log := harness(t)
			t.Setenv("FAKE_UNSIGNED", unsigned)
			if _, err := Promote(runner(t), opts(), io.Discard); err == nil {
				t.Fatal("want an error")
			}
			cs := calls(t, log)
			if index(cs, "crane tag") >= 0 {
				t.Errorf("tagged despite a failed verification:\n%s", strings.Join(cs, "\n"))
			}
			if name == "source" && index(cs, "oras") >= 0 {
				t.Errorf("copied an unverified source:\n%s", strings.Join(cs, "\n"))
			}
		})
	}
}

// Tag-based cosign artifacts (sha256-<hex>.sig, from cosign v2 or
// --new-bundle-format=false) are not referrers, so they are copied by tag.
func TestPromoteCopiesLegacySignatureTags(t *testing.T) {
	log := harness(t)
	t.Setenv("FAKE_LEGACY", "1")
	if _, err := Promote(runner(t), opts(), io.Discard); err != nil {
		t.Fatal(err)
	}
	cs := calls(t, log)
	cp := index(cs, "crane copy reg.io/dev/app:sha256-abc.sig reg.io/prod/app:sha256-abc.sig")
	if cp < 0 || cp > index(cs, "cosign verify --key cosign.pub reg.io/prod/app@") {
		t.Errorf("legacy .sig tag not copied before the destination check:\n%s", strings.Join(cs, "\n"))
	}
	if index(cs, "crane copy reg.io/dev/app:sha256-abc.att") >= 0 {
		t.Errorf("copied a legacy tag that does not exist:\n%s", strings.Join(cs, "\n"))
	}
}

// A digest --from is used as is; a same-repository promotion needs no copy.
func TestPromoteByDigestInPlace(t *testing.T) {
	log := harness(t)
	o := opts()
	o.From, o.Repos = "sha256:def", []string{"reg.io/dev/app"}
	if _, err := Promote(runner(t), o, io.Discard); err != nil {
		t.Fatal(err)
	}
	cs := calls(t, log)
	if index(cs, "crane digest") >= 0 || index(cs, "oras") >= 0 {
		t.Errorf("digest promotion in place should neither resolve nor copy:\n%s", strings.Join(cs, "\n"))
	}
	if index(cs, "crane tag reg.io/dev/app@sha256:def stable") < 0 {
		t.Errorf("not tagged:\n%s", strings.Join(cs, "\n"))
	}
}

// A dry run executes nothing that writes: no copy, no tag.
func TestPromoteDryRun(t *testing.T) {
	log := harness(t)
	r := runner(t)
	r.DryRun = true
	if _, err := Promote(r, opts(), io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls(t, log) {
		if strings.HasPrefix(c, "oras") || strings.HasPrefix(c, "crane tag") || strings.HasPrefix(c, "crane copy") {
			t.Errorf("dry run executed %q", c)
		}
	}
}
