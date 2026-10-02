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
// webhook. A missing URL or credential env var is a hard error so a
// misconfigured secret fails loudly rather than silently skipping the CD
// trigger. A non-2xx response fails the release for the same reason.
func Notify(r *run.Runner, cfg config.NotifyWebhook, notes []Notification) error {
	if !cfg.Enabled || len(notes) == 0 {
		return nil
	}
	url := os.Getenv(cfg.URLEnv)
	if url == "" {
		return fmt.Errorf("notify.webhook.enabled but %s is empty", cfg.URLEnv)
	}
	if err := checkWebhookURL(cfg.URLEnv, url); err != nil {
		return fmt.Errorf("notify.webhook: %w", err)
	}
	bearer := ""
	if cfg.BearerEnv != "" {
		if bearer = os.Getenv(cfg.BearerEnv); bearer == "" {
			return fmt.Errorf("notify.webhook.bearer_env set but %s is empty", cfg.BearerEnv)
		}
	}
	var hmacKey []byte
	if cfg.HMACEnv != "" {
		secret := os.Getenv(cfg.HMACEnv)
		if secret == "" {
			return fmt.Errorf("notify.webhook.hmac_env set but %s is empty", cfg.HMACEnv)
		}
		hmacKey = []byte(secret)
	}
	for _, n := range notes {
		payload, err := json.Marshal(n)
		if err != nil {
			return fmt.Errorf("notify %s: %w", n.Image, err)
		}
		if r.DryRun || r.Verbose {
			fmt.Fprintf(os.Stderr, "[notify] POST %s %s\n", redact(url), string(payload))
		}
		if r.DryRun {
			continue
		}
		if err := postNotification(r.Context(), url, payload, bearer, hmacKey); err != nil {
			return fmt.Errorf("notify %s: %w", n.Image, err)
		}
	}
	return nil
}

func postNotification(ctx context.Context, url string, payload []byte, bearer string, hmacKey []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload)) //nolint:gosec // G704: the operator's own webhook, checked by checkWebhookURL
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if len(hmacKey) > 0 {
		mac := hmac.New(sha256.New, hmacKey)
		mac.Write(payload)
		req.Header.Set(SignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return send(req)
}
