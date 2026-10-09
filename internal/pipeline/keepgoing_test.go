// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/fingerprint"
	"github.com/blairham/stevedore/internal/gitinfo"
	"github.com/blairham/stevedore/internal/tmpl"
)

// A grype that fails the gate for any image in a repository named */bad.
const fakeGrypeFailsBad = `#!/bin/sh
echo "grype $*" >> "$FAKE_LOG"
case "$*" in
*/bad@*) echo '{"matches":[{"vulnerability":{"id":"CVE-1","severity":"Critical"},"artifact":{"name":"x","version":"1"}}]}' ;;
*) echo '{"matches":[]}' ;;
esac
`

// keepGoingHarness sets up two independent groups, "good" and "bad", whose
// only difference is that bad fails its scan gate, and a notify webhook that
// records which images it was told about.
func keepGoingHarness(
	t *testing.T,
) (dir, log string, o Options, p *Prepared, groups map[string][]imageEval, notified func() []string) {
	t.Helper()
	dir, log, o, p, _ = gateHarness(t)
	if err := os.WriteFile(
		filepath.Join(dir, "bin", "grype"),
		[]byte(fakeGrypeFailsBad),
		0o755,
	); err != nil { //nolint:gosec // G306: a test fake must be executable
		t.Fatal(err)
	}
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	t.Setenv("GITHUB_OUTPUT", "")
	groups = map[string][]imageEval{}
	for _, id := range []string{"good", "bad"} {
		groups[id] = []imageEval{{plan: ImagePlan{
			Image: config.Image{
				ID:         id,
				Dockerfile: "Dockerfile",
				Context:    ".",
				Platforms:  []string{"linux/amd64", "linux/arm64"},
			},
			Repos:   []string{"ghcr.io/x/" + id},
			Refs:    []string{"ghcr.io/x/" + id + ":1.0.0"},
			Version: "1.0.0",
		}}}
	}

	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("TEST_NOTIFY_URL", srv.URL)
	p.Config.ProjectName = "proj"
	p.Git = &gitinfo.Info{Version: "1.0.0"}
	p.Ctx = &tmpl.Context{ProjectName: "proj", Version: "1.0.0"}
	p.Config.Notify.Webhook = config.NotifyWebhook{Enabled: true, URLEnv: "TEST_NOTIFY_URL"}
	notified = func() []string {
		mu.Lock()
		defer mu.Unlock()
		var ids []string
		for _, b := range bodies {
			for _, id := range []string{"good", "bad"} {
				if strings.Contains(b, `"image":"`+id+`"`) {
					ids = append(ids, id)
				}
			}
		}
		return ids
	}
	return dir, log, o, p, groups, notified
}

func summaryMentions(t *testing.T, dir, id string) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "dist", "release-summary.json"))
	if err != nil {
		t.Fatalf("no release summary: %v", err)
	}
	return strings.Contains(string(data), `"id": "`+id+`"`) || strings.Contains(string(data), `"id":"`+id+`"`)
}

// One group failing must not cost the groups already pushed their record:
// they are tagged and signed, and without a notification, marker and summary
// the next run would republish them as a new version.
func TestReleaseRecordsGroupsPushedBeforeAFailure(t *testing.T) {
	dir, _, o, p, g, notified := keepGoingHarness(t)
	posts := announceServer(t, p)
	toBuild := [][]imageEval{g["good"], g["bad"]}
	err := buildAndFinish(
		o,
		p,
		quietRunner(t),
		toBuild,
		nil,
		filepath.Join(dir, "dist", "fingerprints.json"),
		fingerprint.State{},
	)
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("want the bad group's failure, got %v", err)
	}
	if got := notified(); len(got) != 1 || got[0] != "good" {
		t.Errorf("notified %v, want [good]", got)
	}
	if !summaryMentions(t, dir, "good") {
		t.Error("release summary does not record the pushed image")
	}
	// The release as a whole is incomplete: it is not announced.
	if got := posts.Load(); got != 0 {
		t.Errorf("an incomplete release posted %d announcement(s), want 0", got)
	}
}

// Without --keep-going the first failure stops dispatch; with it every group
// builds and the run fails at the end.
func TestKeepGoingBuildsEveryGroup(t *testing.T) {
	for _, keepGoing := range []bool{false, true} {
		name := map[bool]string{false: "default", true: "keep-going"}[keepGoing]
		t.Run(name, func(t *testing.T) {
			dir, log, o, p, g, notified := keepGoingHarness(t)
			o.KeepGoing = keepGoing
			toBuild := [][]imageEval{g["bad"], g["good"]}
			err := buildAndFinish(
				o,
				p,
				quietRunner(t),
				toBuild,
				nil,
				filepath.Join(dir, "dist", "fingerprints.json"),
				fingerprint.State{},
			)
			if err == nil || !strings.Contains(err.Error(), "bad") {
				t.Fatalf("want the bad group's failure, got %v", err)
			}
			goodScanned := indexOf(readCalls(t, log), "grype ghcr.io/x/good@") >= 0
			if goodScanned != keepGoing {
				t.Errorf("good built = %v, want %v", goodScanned, keepGoing)
			}
			wantNotified := 0
			if keepGoing {
				wantNotified = 1
			}
			if got := notified(); len(got) != wantNotified || (wantNotified == 1 && got[0] != "good") {
				t.Errorf("notified %v, want good only when keep-going", got)
			}
		})
	}
}

// merge --keep-going publishes every image whose digests are all present, and
// still fails for the one missing a leg.
func TestMergeKeepGoingMergesCompleteImages(t *testing.T) {
	dir, log, o, p, g, notified := keepGoingHarness(t)
	// Only good's legs ran; bad is missing every digest. Its scan would pass:
	// the failure here is the missing leg.
	if err := os.WriteFile(
		filepath.Join(dir, "bin", "grype"),
		[]byte(fakeGrype),
		0o755,
	); err != nil { //nolint:gosec // G306: a test fake must be executable
		t.Fatal(err)
	}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		if err := writeSplitDigest(dir, "dist", []string{"good"}, []string{platform}, "sha256:"+platform[6:]); err != nil {
			t.Fatal(err)
		}
	}
	o.FromDigests = true
	o.KeepGoing = true
	toBuild := [][]imageEval{g["bad"], g["good"]}
	err := buildAndFinish(
		o,
		p,
		quietRunner(t),
		toBuild,
		nil,
		filepath.Join(dir, "dist", "fingerprints.json"),
		fingerprint.State{},
	)
	if err == nil || !strings.Contains(err.Error(), "no split digest") {
		t.Fatalf("want bad's missing digests reported, got %v", err)
	}
	if indexOf(readCalls(t, log), "--tag ghcr.io/x/good:1.0.0") < 0 {
		t.Errorf("good was not tagged:\n%s", strings.Join(readCalls(t, log), "\n"))
	}
	if got := notified(); len(got) != 1 || got[0] != "good" {
		t.Errorf("notified %v, want [good]", got)
	}
}
