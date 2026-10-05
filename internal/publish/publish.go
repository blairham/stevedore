// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

// Package publish handles post-build release steps: creating a GitHub release
// and announcing to chat webhooks (Slack, Discord).
package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"time"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

// GitHubRelease creates (or updates) a GitHub release for tag using the gh CLI,
// with notesPath as the body. Assets are the files to attach (e.g. SBOMs).
//
// target is the commit the tag is created at when it does not exist yet (gh
// otherwise tags the default branch's tip, which need not be what was built);
// it is ignored by gh when the tag already exists, and omitted when empty.
//
// prerelease is whether the version is a semver prerelease; it marks the
// release as one unless cfg.Prerelease says otherwise.
func GitHubRelease(r *run.Runner, cfg config.GitHubRelease, tag, target, title, notesPath string, prerelease bool, assets []string) error {
	if !cfg.Enabled {
		return nil
	}
	if tag == "" {
		return fmt.Errorf("github release needs a tag (use the git versioning strategy or tag the release)")
	}
	if !r.DryRun && !run.Has("gh") {
		return fmt.Errorf("release.github.enabled but gh not found on PATH")
	}
	args := []string{"release", "create", tag, "--title", title, "--notes-file", notesPath}
	if target != "" {
		args = append(args, "--target", target)
	}
	if cfg.Draft {
		args = append(args, "--draft")
	}
	if cfg.Prerelease != nil {
		prerelease = *cfg.Prerelease
	}
	if prerelease {
		args = append(args, "--prerelease")
	}
	args = append(args, assets...)
	if err := r.Run("gh", args...); err != nil {
		return fmt.Errorf("gh release create: %w", err)
	}
	return nil
}

// Message is the data passed to announcement templates.
type Message struct {
	ProjectName string
	Version     string
	Tag         string
	Refs        []string
	Body        string // rendered plain-text message
}

// Announce posts the message to every enabled webhook. Missing webhook URLs are
// a hard error so a misconfigured secret fails loudly rather than silently
// skipping the notification.
func Announce(r *run.Runner, cfg config.Announce, msg Message) error {
	targets := []struct {
		name string // also the payload kind renderPayload builds
		w    config.Webhook
	}{
		{"slack", cfg.Slack},
		{"discord", cfg.Discord},
	}
	for _, t := range targets {
		if !t.w.Enabled {
			continue
		}
		url := os.Getenv(t.w.WebhookEnv)
		if url == "" {
			return fmt.Errorf("announce.%s.enabled but %s is empty", t.name, t.w.WebhookEnv)
		}
		if err := checkWebhookURL(t.w.WebhookEnv, url); err != nil {
			return fmt.Errorf("announce.%s: %w", t.name, err)
		}
		payload, err := renderPayload(t.name, msg.Body)
		if err != nil {
			return err
		}
		if r.DryRun || r.Verbose {
			fmt.Fprintf(os.Stderr, "[announce:%s] POST %s %s\n", t.name, redact(url), string(payload))
		}
		if r.DryRun {
			continue
		}
		if err := post(r.Context(), url, payload); err != nil {
			return fmt.Errorf("announce %s: %w", t.name, err)
		}
	}
	return nil
}

// renderPayload builds the JSON body for a webhook kind. Slack expects {"text"},
// Discord expects {"content"}.
func renderPayload(kind, text string) ([]byte, error) {
	key := "text"
	if kind == "discord" {
		key = "content"
	}
	return json.Marshal(map[string]string{key: text})
}

func post(ctx context.Context, url string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload)) //nolint:gosec // G704: the operator's own webhook, checked by checkWebhookURL
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return send(req)
}

// send performs a webhook request and turns a non-2xx response into an error
// carrying the start of the response body.
func send(req *http.Request) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req) //nolint:gosec // G704: as above
	if err != nil {
		// net/http quotes the whole URL in its errors, and webhook URLs are
		// credentials; report the redacted form.
		var uerr *neturl.Error
		if errors.As(err, &uerr) {
			uerr.URL = redact(uerr.URL)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s: %s", resp.Status, bodyPrefix(resp.Body))
	}
	return nil
}

// bodyPrefix returns up to 512 bytes of an error response for the message. A
// body that fails to read is reported as such: the status already says what
// went wrong.
func bodyPrefix(body io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(body, 512))
	if err != nil {
		return fmt.Sprintf("(reading body: %v)", err)
	}
	return string(b)
}

// checkWebhookURL rejects a webhook URL that is not absolute http(s), so a
// mis-set variable fails here rather than as a request to somewhere odd. The
// error names the variable, never the URL: webhook URLs are credentials.
//
// Plain http is refused unless the host is loopback: the URL itself, and for
// notify the bearer token beside it, would otherwise cross the network in
// cleartext. Loopback stays open so a local receiver can be tested without a
// certificate; nothing on the wire leaves the machine.
func checkWebhookURL(env, raw string) error {
	u, err := neturl.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%s is not an absolute http(s) URL", env)
	}
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return fmt.Errorf("%s is a plain http URL; webhooks must use https (http is allowed only for localhost, 127.0.0.0/8 and [::1])", env)
	}
	return nil
}

// isLoopback reports whether host names this machine: "localhost" or a
// loopback IP literal. Other names are not resolved — a DNS answer is not a
// promise that the request stays local.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// redact hides all but the scheme and host of a webhook URL for log output.
// Userinfo, path, query and fragment can each carry a credential, so none of
// them is printed; anything dropped after the host is marked "/…". A URL that
// does not parse to a scheme and host is replaced whole — there is no safe
// part of it to show.
func redact(raw string) string {
	u, err := neturl.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "…"
	}
	out := u.Scheme + "://" + u.Host
	if u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		out += "/…"
	}
	return out
}
