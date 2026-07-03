// Package publish handles post-build release steps: creating a GitHub release
// and announcing to chat webhooks (Slack, Discord).
package publish

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

// GitHubRelease creates (or updates) a GitHub release for tag using the gh CLI,
// with notesPath as the body. Assets are the files to attach (e.g. SBOMs).
func GitHubRelease(r *run.Runner, cfg config.GitHubRelease, tag, title, notesPath string, assets []string) error {
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
	if cfg.Draft {
		args = append(args, "--draft")
	}
	if cfg.Prerelease {
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
		name string
		w    config.Webhook
		kind string
	}{
		{"slack", cfg.Slack, "slack"},
		{"discord", cfg.Discord, "discord"},
	}
	for _, t := range targets {
		if !t.w.Enabled {
			continue
		}
		url := os.Getenv(t.w.WebhookEnv)
		if url == "" {
			return fmt.Errorf("announce.%s.enabled but %s is empty", t.name, t.w.WebhookEnv)
		}
		payload, err := renderPayload(t.kind, msg.Body)
		if err != nil {
			return err
		}
		if r.DryRun || r.Verbose {
			fmt.Fprintf(os.Stderr, "[announce:%s] POST %s %s\n", t.name, redact(url), string(payload))
		}
		if r.DryRun {
			continue
		}
		if err := post(url, payload); err != nil {
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

func post(url string, payload []byte) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("webhook returned %s: %s", resp.Status, string(body))
	}
	return nil
}

// redact hides all but the scheme+host of a webhook URL for log output.
func redact(url string) string {
	for i := 0; i < len(url); i++ {
		if url[i] == '/' && i > 0 && url[i-1] == '/' {
			// found "://", now find the next slash after the host
			for j := i + 1; j < len(url); j++ {
				if url[j] == '/' {
					return url[:j] + "/…"
				}
			}
			return url
		}
	}
	return url
}
