// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/summary"
)

// notifyCount points the notify webhook at a local server and returns a
// Prepared that has it enabled, plus the number of POSTs the server received.
func notifyCount(t *testing.T) (*Prepared, *atomic.Int32) {
	t.Helper()
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("STEVEDORE_TEST_NOTIFY_URL", srv.URL)
	p := &Prepared{Config: &config.Config{
		ProjectName: "x",
		Notify:      config.Notify{Webhook: config.NotifyWebhook{Enabled: true, URLEnv: "STEVEDORE_TEST_NOTIFY_URL"}},
	}}
	return p, &posts
}

var pushedImage = []summary.Image{
	{ID: "app", Version: "1.0.0", Digest: "sha256:built", Refs: []string{"ghcr.io/x/app:1.0.0"}},
}

// build --push is an inner-loop push; it must not fire the CD trigger.
func TestBuildPushDoesNotNotify(t *testing.T) {
	p, posts := notifyCount(t)
	o, err := releaseOptions(BuildPushOptions(Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := notifyWebhook(o, p, quietRunner(t), pushedImage); err != nil {
		t.Fatal(err)
	}
	if n := posts.Load(); n != 0 {
		t.Errorf("build --push POSTed the notify webhook %d time(s)", n)
	}
}

// The control: a snapshot release still notifies, so the test above is not
// passing because the webhook could never fire.
func TestSnapshotReleaseStillNotifies(t *testing.T) {
	p, posts := notifyCount(t)
	o, err := releaseOptions(Options{Snapshot: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := notifyWebhook(o, p, quietRunner(t), pushedImage); err != nil {
		t.Fatal(err)
	}
	if n := posts.Load(); n != 1 {
		t.Errorf("release --snapshot POSTed the notify webhook %d time(s), want 1", n)
	}
}

// build --push keeps the gates and skips every release extra.
func TestBuildPushOptions(t *testing.T) {
	o := BuildPushOptions(Options{})
	if !o.Snapshot || !o.Push {
		t.Errorf("build --push must be a pushed snapshot: %+v", o)
	}
	if !o.SkipSign || !o.SkipSBOM || !o.SkipChangelog || !o.SkipPublish {
		t.Errorf("build --push must skip sign/sbom/changelog/publish: %+v", o)
	}
	if o.SkipScan || o.SkipTest || o.NoPush {
		t.Errorf("build --push must keep the gates and push: %+v", o)
	}
}
