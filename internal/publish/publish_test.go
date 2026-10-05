// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package publish

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
	err := GitHubRelease(&run.Runner{DryRun: true}, config.GitHubRelease{Enabled: true}, "", "", "title", "notes.md", nil)
	if err == nil {
		t.Error("expected error when tag is empty")
	}
}

func TestGitHubReleaseDisabledIsNoop(t *testing.T) {
	if err := GitHubRelease(&run.Runner{}, config.GitHubRelease{}, "", "", "", "", nil); err != nil {
		t.Errorf("disabled github release should be a no-op, got %v", err)
	}
}

func TestAnnounceRejectsNonHTTPWebhook(t *testing.T) {
	for _, hook := range []string{
		"hooks.slack.com/services/T00/B00/XXXXSECRET",       // no scheme, so no host
		"ftp://hooks.slack.com/services/T00/B00/XXXXSECRET", // a host, wrong scheme
		"https:///services/T00/B00/XXXXSECRET",              // right scheme, no host
	} {
		t.Setenv("TEST_SLACK_HOOK", hook)
		cfg := config.Announce{Slack: config.Webhook{Enabled: true, WebhookEnv: "TEST_SLACK_HOOK"}}
		err := Announce(&run.Runner{}, cfg, Message{Body: "x"})
		if err == nil || !strings.Contains(err.Error(), "not an absolute http(s) URL") {
			t.Errorf("%q should be rejected before any request, got %v", hook, err)
			continue
		}
		if strings.Contains(err.Error(), "SECRET") {
			t.Errorf("error leaks the webhook URL: %v", err)
		}
	}
}

func TestWebhookRefusesPlainHTTPOffLoopback(t *testing.T) {
	for _, hook := range []string{
		"http://hooks.slack.com/services/T00/B00/XXXXSECRET",
		"http://10.0.0.5/services/T00/B00/XXXXSECRET",
		"http://localhost.example.com/XXXXSECRET", // a name that merely starts with localhost
		"HTTP://hooks.slack.com/XXXXSECRET",       // scheme case does not slip past
	} {
		t.Setenv("TEST_SLACK_HOOK", hook)
		cfg := config.Announce{Slack: config.Webhook{Enabled: true, WebhookEnv: "TEST_SLACK_HOOK"}}
		err := Announce(&run.Runner{DryRun: true}, cfg, Message{Body: "x"})
		if err == nil || !strings.Contains(err.Error(), "must use https") {
			t.Errorf("%q should be refused as cleartext, got %v", hook, err)
			continue
		}
		if strings.Contains(err.Error(), "SECRET") {
			t.Errorf("error leaks the webhook URL: %v", err)
		}
	}
}

func TestWebhookAllowsPlainHTTPOnLoopback(t *testing.T) {
	for _, hook := range []string{
		"http://localhost:8080/hook",
		"http://LOCALHOST/hook",
		"http://127.0.0.1:9/hook",
		"http://127.1.2.3/hook",
		"http://[::1]:8080/hook",
		"https://hooks.slack.com/services/T00/B00/XXXXSECRET",
	} {
		if err := checkWebhookURL("TEST_SLACK_HOOK", hook); err != nil {
			t.Errorf("%q should be accepted, got %v", hook, err)
		}
	}
}

func TestWebhookTransportErrorIsRedacted(t *testing.T) {
	// Port 1 on localhost refuses the connection, so client.Do fails.
	t.Setenv("TEST_SLACK_HOOK", "http://127.0.0.1:1/services/T00/B00/XXXXSECRET")
	cfg := config.Announce{Slack: config.Webhook{Enabled: true, WebhookEnv: "TEST_SLACK_HOOK"}}
	err := Announce(&run.Runner{}, cfg, Message{Body: "x"})
	if err == nil {
		t.Fatal("a refused connection should fail the announce")
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Errorf("error leaks the webhook URL: %v", err)
	}
}
