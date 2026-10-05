// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

// The scan and smoke-test gates exist to keep a bad image from being
// published, so no tag may exist until both pass. These tests run buildGroup
// for real against fake docker/grype/cosign executables that log every
// invocation in order, and read the order back.

const fakeDocker = `#!/bin/sh
echo "docker $*" >> "$FAKE_LOG"
case "$1 $2" in
"buildx build")
	while [ $# -gt 0 ]; do
		if [ "$1" = "--metadata-file" ]; then
			printf '{"containerimage.digest":"sha256:built"}' > "$2"
		fi
		shift
	done
	;;
"buildx imagetools")
	case " $* " in
	*" --dry-run "*) printf '{"schemaVersion":2}\n' ;;
	esac
	;;
"run "*)
	[ "$FAKE_SMOKE_FAIL" = 1 ] && exit 1
	;;
"version "*)
	echo "${FAKE_DOCKER_HOST:-linux/amd64}"
	;;
"buildx inspect")
	[ -n "$FAKE_EMULATES" ] && echo "Platforms: $FAKE_EMULATES"
	;;
esac
exit 0
`

const fakeGrype = `#!/bin/sh
echo "grype $*" >> "$FAKE_LOG"
crit=$FAKE_SCAN_CRITICAL
case " $* " in
*" --platform $FAKE_SCAN_CRITICAL_ON "*) crit=1 ;;
esac
if [ "$crit" = 1 ]; then
	echo '{"matches":[{"vulnerability":{"id":"CVE-1","severity":"Critical"},"artifact":{"name":"x","version":"1"}}]}'
else
	echo '{"matches":[]}'
fi
`

const fakeCosign = `#!/bin/sh
echo "cosign $*" >> "$FAKE_LOG"
`

// gateHarness installs the fakes on PATH and returns a release setup whose
// image is published to two repositories, each with a version tag and latest.
func gateHarness(t *testing.T) (dir, log string, o Options, p *Prepared, grp []imageEval) {
	t.Helper()
	dir = t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"docker": fakeDocker, "grype": fakeGrype, "cosign": fakeCosign} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil { //nolint:gosec // G306: a test fake must be executable
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	log = filepath.Join(dir, "calls.log")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_LOG", log)
	t.Setenv("FAKE_SMOKE_FAIL", "")
	t.Setenv("FAKE_SCAN_CRITICAL", "")
	t.Setenv("FAKE_SCAN_CRITICAL_ON", "-")
	t.Setenv("FAKE_DOCKER_HOST", "")
	t.Setenv("FAKE_EMULATES", "")

	p = &Prepared{Config: &config.Config{
		Dist: "dist",
		Scan: config.Scan{Enabled: true, Scanner: "grype", FailOn: "critical"},
		Test: config.Test{Enabled: true, Cmd: []string{"--version"}},
		Sign: config.Sign{Cosign: config.Cosign{Enabled: true}},
	}}
	plan := ImagePlan{
		Image: config.Image{ID: "app", Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64", "linux/arm64"}},
		Repos: []string{"ghcr.io/x/app", "reg.io/x/app"},
		Refs:  []string{"ghcr.io/x/app:1.0.0", "ghcr.io/x/app:latest", "reg.io/x/app:1.0.0", "reg.io/x/app:latest"},
	}
	return dir, log, Options{Dir: dir}, p, []imageEval{{plan: plan}}
}

func quietRunner(t *testing.T) *run.Runner {
	t.Helper()
	sink, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	return &run.Runner{Stdout: sink, Stderr: sink}
}

func readCalls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// indexOf returns the first call containing every substring, or -1.
func indexOf(calls []string, subs ...string) int {
	for i, c := range calls {
		ok := true
		for _, s := range subs {
			if !strings.Contains(c, s) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// taggingCalls returns the indexes of every call that names a tag of the
// release — "repo:" anywhere in an argument, which neither a bare repo nor a
// repo@digest reference contains.
func taggingCalls(calls []string) []int {
	var out []int
	for i, c := range calls {
		if strings.Contains(c, "ghcr.io/x/app:") || strings.Contains(c, "reg.io/x/app:") {
			out = append(out, i)
		}
	}
	return out
}

// assertTaggedAfterGates checks that the gates and signing ran, and that every
// tag of every repo was applied only after them.
func assertTaggedAfterGates(t *testing.T, calls []string, digest string) {
	t.Helper()
	scan := indexOf(calls, "grype ghcr.io/x/app@"+digest)
	smoke := indexOf(calls, "docker run --rm --platform linux/amd64 ghcr.io/x/app@"+digest)
	sign := indexOf(calls, "cosign sign", "reg.io/x/app@"+digest)
	if scan < 0 || smoke < 0 || sign < 0 {
		t.Fatalf("gates did not run on the digest (scan=%d smoke=%d sign=%d):\n%s", scan, smoke, sign, strings.Join(calls, "\n"))
	}
	gatesDone := max(scan, smoke, sign)
	tagging := taggingCalls(calls)
	if len(tagging) == 0 {
		t.Fatalf("no tagging call after the gates passed:\n%s", strings.Join(calls, "\n"))
	}
	for _, i := range tagging {
		if i < gatesDone {
			t.Errorf("call %d names a tag before the gates finished (call %d):\n%s", i, gatesDone, strings.Join(calls, "\n"))
		}
	}
	for _, ref := range []string{"ghcr.io/x/app:1.0.0", "ghcr.io/x/app:latest", "reg.io/x/app:1.0.0", "reg.io/x/app:latest"} {
		if indexOf(calls, "--tag "+ref) < 0 {
			t.Errorf("tag %s never applied:\n%s", ref, strings.Join(calls, "\n"))
		}
	}
	// Every tag is a carbon copy of the gated digest, from its own repo.
	for _, repo := range []string{"ghcr.io/x/app", "reg.io/x/app"} {
		if indexOf(calls, "imagetools create --prefer-index=false --tag "+repo+":", repo+"@"+digest) < 0 {
			t.Errorf("%s not tagged from %s@%s:\n%s", repo, repo, digest, strings.Join(calls, "\n"))
		}
	}
}

func TestBuildGroupTagsOnlyAfterGates(t *testing.T) {
	_, log, o, p, grp := gateHarness(t)
	if _, _, err := buildGroup(o, p, quietRunner(t), grp); err != nil {
		t.Fatal(err)
	}
	calls := readCalls(t, log)
	build := indexOf(calls, "docker buildx build")
	if build < 0 || !strings.Contains(calls[build], "push-by-digest=true") {
		t.Fatalf("build did not push by digest:\n%s", strings.Join(calls, "\n"))
	}
	assertTaggedAfterGates(t, calls, "sha256:built")
}

func TestMergeTagsOnlyAfterGates(t *testing.T) {
	dir, log, o, p, grp := gateHarness(t)
	for _, leg := range []struct{ platform, digest string }{
		{"linux/amd64", "sha256:aaa"},
		{"linux/arm64", "sha256:bbb"},
	} {
		if err := writeSplitDigest(dir, "dist", []string{"app"}, []string{leg.platform}, leg.digest); err != nil {
			t.Fatal(err)
		}
	}
	o.FromDigests = true
	if _, _, err := buildGroup(o, p, quietRunner(t), grp); err != nil {
		t.Fatal(err)
	}
	// The fake's --dry-run list, as Capture returns it (trailing newline gone).
	list := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(`{"schemaVersion":2}`)))
	calls := readCalls(t, log)
	for _, repo := range []string{"ghcr.io/x/app", "reg.io/x/app"} {
		if indexOf(calls, "imagetools create --tag "+repo+"@"+list, repo+"@sha256:aaa", repo+"@sha256:bbb") < 0 {
			t.Errorf("list not pushed to %s by digest:\n%s", repo, strings.Join(calls, "\n"))
		}
	}
	assertTaggedAfterGates(t, calls, list)
}

func TestFailingGatePublishesNoTag(t *testing.T) {
	cases := map[string]string{
		"scan":  "FAKE_SCAN_CRITICAL",
		"smoke": "FAKE_SMOKE_FAIL",
	}
	for name, env := range cases {
		for _, merge := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/merge=%v", name, merge), func(t *testing.T) {
				dir, log, o, p, grp := gateHarness(t)
				t.Setenv(env, "1")
				if merge {
					for _, platform := range []string{"linux/amd64", "linux/arm64"} {
						if err := writeSplitDigest(dir, "dist", []string{"app"}, []string{platform}, "sha256:"+platform[6:]); err != nil {
							t.Fatal(err)
						}
					}
					o.FromDigests = true
				}
				if _, _, err := buildGroup(o, p, quietRunner(t), grp); err == nil {
					t.Fatal("want the gate to fail the build")
				}
				calls := readCalls(t, log)
				if got := taggingCalls(calls); len(got) > 0 {
					t.Errorf("a failed %s gate still tagged the image:\n%s", name, strings.Join(calls, "\n"))
				}
				if indexOf(calls, "cosign") >= 0 {
					t.Errorf("a failed %s gate still signed the image:\n%s", name, strings.Join(calls, "\n"))
				}
			})
		}
	}
}

// --no-push builds to validate only: no push, no gates, no tagging.
func TestNoPushBuildsWithoutPublishing(t *testing.T) {
	_, log, o, p, grp := gateHarness(t)
	o.NoPush = true
	if _, _, err := buildGroup(o, p, quietRunner(t), grp); err != nil {
		t.Fatal(err)
	}
	calls := readCalls(t, log)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "docker buildx build") {
		t.Fatalf("want exactly one validate-only build, got:\n%s", strings.Join(calls, "\n"))
	}
	if strings.Contains(calls[0], "push") {
		t.Errorf("--no-push build pushes: %s", calls[0])
	}
}
