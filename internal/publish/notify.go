// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package publish

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
	"github.com/blairham/stevedore/internal/tmpl"
)

// SignatureHeader carries the HMAC-SHA256 signature of the notification body,
// as "sha256=<hex>", when notify.webhook.hmac_env is configured.
const SignatureHeader = "X-Stevedore-Signature"

// Notification is the machine-readable payload POSTed for one pushed image, so
// a CD system can react to the newly published digest (GitOps sync, rollout).
type Notification struct {
	Project  string `json:"project,omitempty"`
	Snapshot bool   `json:"snapshot"`
	Image    string `json:"image"`
	Version  string `json:"version,omitempty"`
	Digest   string `json:"digest,omitempty"`
	// Repositories are the bare repos (no tag) the image was pushed to.
	Repositories []string `json:"repositories,omitempty"`
	// Refs are the full repo:tag references actually published.
	Refs []string `json:"refs,omitempty"`
}

// Notify POSTs one JSON notification per pushed image to the configured
// webhook and returns how many were delivered. A missing URL or credential env
// var, or a payload template that does not render to JSON, is a hard error so
// a misconfigured secret fails loudly rather than silently skipping the CD
// trigger. A failed delivery (transport error or non-2xx response) fails the
// release too, unless notify.webhook.required is false: then it is reported as
// a warning and the remaining notifications are still sent.
func Notify(r *run.Runner, cfg config.NotifyWebhook, notes []Notification) (int, error) {
	if !cfg.Enabled || len(notes) == 0 {
		return 0, nil
	}
	target, err := notifyTarget(cfg)
	if err != nil {
		return 0, err
	}
	// Render every payload before sending any, so a template error is a
	// configuration error found before the first POST, not halfway through.
	payloads := make([][]byte, len(notes))
	for i, n := range notes {
		if payloads[i], err = notificationPayload(cfg.PayloadTemplate, n); err != nil {
			return 0, fmt.Errorf("notify %s: %w", n.Image, err)
		}
	}
	sent := 0
	for i, n := range notes {
		if r.DryRun || r.Verbose {
			fmt.Fprintf(os.Stderr, "[notify] POST %s %s\n", redact(target.url), string(payloads[i]))
		}
		if r.DryRun {
			continue
		}
		if err := postNotification(r.Context(), target, payloads[i]); err != nil {
			if cfg.IsRequired() {
				return sent, fmt.Errorf("notify %s: %w", n.Image, err)
			}
			fmt.Fprintf(os.Stderr, "warning: notify %s: %v (notify.webhook.required is false; continuing)\n", n.Image, err)
			continue
		}
		sent++
	}
	return sent, nil
}

// webhookTarget is where and how notifications are sent.
type webhookTarget struct {
	url     string
	bearer  string
	hmacKey []byte
}

// notifyTarget reads the webhook URL and credentials from the environment.
func notifyTarget(cfg config.NotifyWebhook) (webhookTarget, error) {
	var t webhookTarget
	if t.url = os.Getenv(cfg.URLEnv); t.url == "" {
		return t, fmt.Errorf("notify.webhook.enabled but %s is empty", cfg.URLEnv)
	}
	if err := checkWebhookURL(cfg.URLEnv, t.url); err != nil {
		return t, fmt.Errorf("notify.webhook: %w", err)
	}
	if cfg.BearerEnv != "" {
		if t.bearer = os.Getenv(cfg.BearerEnv); t.bearer == "" {
			return t, fmt.Errorf("notify.webhook.bearer_env set but %s is empty", cfg.BearerEnv)
		}
	}
	if cfg.HMACEnv != "" {
		secret := os.Getenv(cfg.HMACEnv)
		if secret == "" {
			return t, fmt.Errorf("notify.webhook.hmac_env set but %s is empty", cfg.HMACEnv)
		}
		t.hmacKey = []byte(secret)
	}
	return t, nil
}

// notificationPayload renders one notification: the struct's own JSON, or the
// configured template, which must produce a valid JSON document because it is
// sent as application/json.
func notificationPayload(tpl string, n Notification) ([]byte, error) {
	if tpl == "" {
		return json.Marshal(n)
	}
	out, err := tmpl.RenderData(tpl, n)
	if err != nil {
		return nil, fmt.Errorf("notify.webhook.payload_template: %w", err)
	}
	if !json.Valid([]byte(out)) {
		return nil, fmt.Errorf("notify.webhook.payload_template did not render valid JSON: %s", out)
	}
	return []byte(out), nil
}

func postNotification(ctx context.Context, t webhookTarget, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(payload)) //nolint:gosec // G704: the operator's own webhook, checked by checkWebhookURL
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if t.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+t.bearer)
	}
	if len(t.hmacKey) > 0 {
		mac := hmac.New(sha256.New, t.hmacKey)
		mac.Write(payload)
		req.Header.Set(SignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return send(req)
}
