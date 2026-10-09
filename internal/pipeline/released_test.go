// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/summary"
	"github.com/blairham/stevedore/internal/tmpl"
)

// fakeImmutableDocker is fakeDocker plus a registry with tag immutability:
// FAKE_EXISTING lists "ref=revision" entries (an empty revision means the
// image carries no revision label). `imagetools inspect` reports those and
// "not found" for anything else; `imagetools create` refuses to move any of
// them, exactly as ECR does with immutable tags.
const fakeImmutableDocker = `#!/bin/sh
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
	if [ "$3" = inspect ]; then
		for a; do ref=$a; done
		for e in $FAKE_EXISTING; do
			if [ "${e%%=*}" = "$ref" ]; then
				rev=${e#*=}
				if [ -n "$rev" ]; then
					printf '{"digest":"sha256:old","image":{"linux/amd64":{"config":{"Labels":{"org.opencontainers.image.revision":"%s"}}}}}\n' "$rev"
				else
					printf '{"digest":"sha256:old","image":{"architecture":"amd64","config":{}}}\n'
				fi
				exit 0
			fi
		done
		echo "ERROR: $ref: not found" >&2
		exit 1
	fi
	for a; do
		for e in $FAKE_EXISTING; do
			if [ "${e%%=*}" = "$a" ]; then
				echo "tag ${a##*:} already exists and is immutable" >&2
				exit 1
			fi
		done
	done
	case " $* " in
	*" --dry-run "*) printf '{"schemaVersion":2}\n' ;;
	esac
	;;
esac
exit 0
`

const (
	headSHA   = "abcdef1234567890abcdef1234567890abcdef12"
	headShort = "abcdef1"
)

// rereleaseHarness is gateHarness with a per-commit tag in both repos (listed
// after the version tag, as `init` scaffolds it) and the immutable fake.
func rereleaseHarness(t *testing.T, existing string) (log string, o Options, p *Prepared, grp []imageEval) {
	t.Helper()
	dir, log, o, p, grp := gateHarness(t)
	if err := os.WriteFile(
		filepath.Join(dir, "bin", "docker"),
		[]byte(fakeImmutableDocker),
		0o755,
	); err != nil { //nolint:gosec // G306: a test fake must be executable
		t.Fatal(err)
	}
	t.Setenv("FAKE_EXISTING", existing)
	p.Ctx = &tmpl.Context{Commit: headSHA, ShortCommit: headShort}
	grp[0].plan.Refs = []string{
		"ghcr.io/x/app:1.0.1", "ghcr.io/x/app:" + headShort, "ghcr.io/x/app:latest",
		"reg.io/x/app:1.0.1", "reg.io/x/app:" + headShort, "reg.io/x/app:latest",
	}
	return log, o, p, grp
}

func callsOrEmpty(t *testing.T, log string) []string {
	t.Helper()
	if _, err := os.Stat(log); os.IsNotExist(err) {
		return nil
	}
	return readCalls(t, log)
}

// Re-running the release of an already-released commit: every commit tag
// exists from this commit, so nothing is built, nothing is tagged, and the
// image is reported released (its marker advances).
func TestRerelease_SameCommitIsAlreadyReleased(t *testing.T) {
	for name, rev := range map[string]string{"revision label": headSHA, "no label": ""} {
		t.Run(name, func(t *testing.T) {
			log, o, p, grp := rereleaseHarness(t,
				"ghcr.io/x/app:"+headShort+"="+rev+" reg.io/x/app:"+headShort+"="+rev)
			irs, _, err := buildGroup(o, p, quietRunner(t), grp)
			if err != nil {
				t.Fatalf("re-release failed: %v", err)
			}
			calls := callsOrEmpty(t, log)
			if i := indexOf(calls, "buildx build"); i >= 0 {
				t.Errorf("rebuilt an already-released image:\n%s", strings.Join(calls, "\n"))
			}
			if indexOf(calls, "imagetools inspect", ":"+headShort) < 0 {
				t.Fatalf("commit tag never looked up:\n%s", strings.Join(calls, "\n"))
			}
			if i := indexOf(calls, "imagetools create"); i >= 0 {
				t.Errorf("tagged an already-released image: %s", calls[i])
			}
			if len(irs) != 1 || !irs[0].AlreadyReleased || !irs[0].Skipped || irs[0].Digest != "sha256:old" {
				t.Fatalf("summary = %+v, want one already-released entry carrying the existing digest", irs)
			}
			if !advancesMarker(irs[0]) {
				t.Error("an already-released image must still advance its release marker")
			}
		})
	}
}

// A commit tag that names a build of another commit fails the run before
// anything is built or tagged.
func TestRerelease_CommitTagFromAnotherCommitFailsBeforeBuilding(t *testing.T) {
	log, o, p, grp := rereleaseHarness(t, "ghcr.io/x/app:"+headShort+"=0123456789")
	_, _, err := buildGroup(o, p, quietRunner(t), grp)
	if err == nil || !strings.Contains(err.Error(), "0123456789") {
		t.Fatalf("want an error naming the other commit, got %v", err)
	}
	calls := callsOrEmpty(t, log)
	if indexOf(calls, "buildx build") >= 0 || indexOf(calls, "imagetools create") >= 0 {
		t.Errorf("pushed despite the conflict:\n%s", strings.Join(calls, "\n"))
	}
}

// Commit tags present in one repo but not the other (an earlier run that
// stopped partway) are reported, not papered over with a second build.
func TestRerelease_PartialCommitTagsFailBeforeBuilding(t *testing.T) {
	log, o, p, grp := rereleaseHarness(t, "ghcr.io/x/app:"+headShort+"="+headSHA)
	_, _, err := buildGroup(o, p, quietRunner(t), grp)
	if err == nil || !strings.Contains(err.Error(), "reg.io/x/app:"+headShort) {
		t.Fatalf("want an error naming the missing commit tag, got %v", err)
	}
	calls := callsOrEmpty(t, log)
	if indexOf(calls, "buildx build") >= 0 || indexOf(calls, "imagetools create") >= 0 {
		t.Errorf("pushed despite the partial tag set:\n%s", strings.Join(calls, "\n"))
	}
}

// A first release tags normally, with each repository's commit tag first and
// its floating tag last, so an immutable collision the lookup could not see
// fails the call before the version tag moves.
func TestFirstRelease_TagsCommitTagFirst(t *testing.T) {
	log, o, p, grp := rereleaseHarness(t, "")
	irs, _, err := buildGroup(o, p, quietRunner(t), grp)
	if err != nil {
		t.Fatal(err)
	}
	if len(irs) != 1 || irs[0].Skipped || irs[0].Digest != "sha256:built" {
		t.Fatalf("summary = %+v, want one built image", irs)
	}
	calls := readCalls(t, log)
	for _, repo := range []string{"ghcr.io/x/app", "reg.io/x/app"} {
		want := "--tag " + repo + ":" + headShort + " --tag " + repo + ":1.0.1 --tag " + repo + ":latest " + repo + "@sha256:built"
		if indexOf(calls, "imagetools create --prefer-index=false "+want) < 0 {
			t.Errorf("%s not tagged commit-first (%q):\n%s", repo, want, strings.Join(calls, "\n"))
		}
	}
}

func TestAdvancesMarker(t *testing.T) {
	cases := []struct {
		im   summary.Image
		want bool
	}{
		{summary.Image{}, true},
		{summary.Image{Skipped: true}, false},
		{summary.Image{Skipped: true, AlreadyReleased: true}, true},
	}
	for _, c := range cases {
		if got := advancesMarker(c.im); got != c.want {
			t.Errorf("advancesMarker(%+v) = %v, want %v", c.im, got, c.want)
		}
	}
}

func TestIsCommitTag(t *testing.T) {
	cases := map[string]bool{
		"ghcr.io/x/app:" + headShort:           true,
		"ghcr.io/x/app:main-" + headShort:      true,
		"ghcr.io/x/app:" + headSHA:             true,
		"ghcr.io/x/app:1.0.1":                  false,
		"localhost:5000/x/app:latest":          false,
		"localhost:5000/" + headShort + "/app": false, // no tag at all
	}
	for ref, want := range cases {
		if got := isCommitTag(ref, headSHA, headShort); got != want {
			t.Errorf("isCommitTag(%q) = %v, want %v", ref, got, want)
		}
	}
}
