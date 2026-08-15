package publish

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

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
		if err := postNotification(url, payload, bearer, hmacKey); err != nil {
			return fmt.Errorf("notify %s: %w", n.Image, err)
		}
	}
	return nil
}

func postNotification(url string, payload []byte, bearer string, hmacKey []byte) error {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
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
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
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
