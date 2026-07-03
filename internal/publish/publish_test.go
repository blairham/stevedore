package publish

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

func TestRenderPayload(t *testing.T) {
	slack, _ := renderPayload("slack", "hi")
	var s map[string]string
	json.Unmarshal(slack, &s)
	if s["text"] != "hi" {
		t.Errorf("slack payload = %s", slack)
	}

	discord, _ := renderPayload("discord", "hi")
	var d map[string]string
	json.Unmarshal(discord, &d)
	if d["content"] != "hi" {
		t.Errorf("discord payload = %s", discord)
	}
}

func TestRedact(t *testing.T) {
	got := redact("https://hooks.slack.com/services/T00/B00/XXXXSECRET")
	if got != "https://hooks.slack.com/…" {
		t.Errorf("redact = %q", got)
	}
}

func TestAnnouncePostsToWebhook(t *testing.T) {
	var gotBody string
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	os.Setenv("TEST_SLACK_HOOK", srv.URL)
	defer os.Unsetenv("TEST_SLACK_HOOK")

	cfg := config.Announce{Slack: config.Webhook{Enabled: true, WebhookEnv: "TEST_SLACK_HOOK"}}
	r := &run.Runner{}
	if err := Announce(r, cfg, Message{Body: "release 1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q", gotContentType)
	}
	var payload map[string]string
	json.Unmarshal([]byte(gotBody), &payload)
	if payload["text"] != "release 1.0.0" {
		t.Errorf("posted body = %s", gotBody)
	}
}

func TestAnnounceMissingWebhookEnvErrors(t *testing.T) {
	cfg := config.Announce{Discord: config.Webhook{Enabled: true, WebhookEnv: "DEFINITELY_UNSET_XYZ"}}
	if err := Announce(&run.Runner{}, cfg, Message{Body: "x"}); err == nil {
		t.Error("expected error when webhook env var is empty")
	}
}

func TestAnnounceDisabledIsNoop(t *testing.T) {
	if err := Announce(&run.Runner{}, config.Announce{}, Message{Body: "x"}); err != nil {
		t.Errorf("no enabled targets should be a no-op, got %v", err)
	}
}

func TestGitHubReleaseNeedsTag(t *testing.T) {
	err := GitHubRelease(&run.Runner{DryRun: true}, config.GitHubRelease{Enabled: true}, "", "title", "notes.md", nil)
	if err == nil {
		t.Error("expected error when tag is empty")
	}
}

func TestGitHubReleaseDisabledIsNoop(t *testing.T) {
	if err := GitHubRelease(&run.Runner{}, config.GitHubRelease{}, "", "", "", nil); err != nil {
		t.Errorf("disabled github release should be a no-op, got %v", err)
	}
}
