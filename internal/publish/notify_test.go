// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package publish

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blairham/stevedore/internal/config"
	"github.com/blairham/stevedore/internal/run"
)

type captured struct {
	body      string
	auth      string
	signature string
	ctype     string
}

func notifyServer(t *testing.T) (*httptest.Server, *[]captured) {
	t.Helper()
	var got []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, captured{
			body:      string(b),
			auth:      r.Header.Get("Authorization"),
			signature: r.Header.Get(SignatureHeader),
			ctype:     r.Header.Get("Content-Type"),
		})
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestNotifyPostsPerImage(t *testing.T) {
	srv, got := notifyServer(t)
	t.Setenv("TEST_NOTIFY_URL", srv.URL)

	cfg := config.NotifyWebhook{Enabled: true, URLEnv: "TEST_NOTIFY_URL"}
	notes := []Notification{
		{Project: "acme", Image: "api", Version: "1.2.3", Digest: "sha256:abc", Refs: []string{"ghcr.io/acme/api:1.2.3"}},
		{Project: "acme", Image: "web", Version: "1.2.3", Digest: "sha256:def"},
	}
	if _, err := Notify(&run.Runner{}, cfg, notes); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("want 2 POSTs, got %d", len(*got))
	}
	first := (*got)[0]
	if first.ctype != "application/json" {
		t.Errorf("content-type = %q", first.ctype)
	}
	var n Notification
	if err := json.Unmarshal([]byte(first.body), &n); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if n.Image != "api" || n.Digest != "sha256:abc" || len(n.Refs) != 1 {
		t.Errorf("payload = %s", first.body)
	}
	if first.auth != "" || first.signature != "" {
		t.Errorf("unexpected auth headers without configured secrets: %+v", first)
	}
}

func TestNotifyBearerAndHMAC(t *testing.T) {
	srv, got := notifyServer(t)
	t.Setenv("TEST_NOTIFY_URL", srv.URL)
	t.Setenv("TEST_NOTIFY_TOKEN", "tok123")
	t.Setenv("TEST_NOTIFY_SECRET", "s3cret")

	cfg := config.NotifyWebhook{
		Enabled: true, URLEnv: "TEST_NOTIFY_URL",
		BearerEnv: "TEST_NOTIFY_TOKEN", HMACEnv: "TEST_NOTIFY_SECRET",
	}
	if _, err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}}); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("want 1 POST, got %d", len(*got))
	}
	req := (*got)[0]
	if req.auth != "Bearer tok123" {
		t.Errorf("authorization = %q", req.auth)
	}
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(req.body))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if req.signature != want {
		t.Errorf("signature = %q, want %q", req.signature, want)
	}
}

func TestNotifyMissingEnvErrors(t *testing.T) {
	cfg := config.NotifyWebhook{Enabled: true, URLEnv: "DEFINITELY_UNSET_NOTIFY"}
	if _, err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}}); err == nil {
		t.Error("expected error when url env var is empty")
	}

	t.Setenv("TEST_NOTIFY_URL", "http://localhost:1")
	cfg = config.NotifyWebhook{Enabled: true, URLEnv: "TEST_NOTIFY_URL", BearerEnv: "DEFINITELY_UNSET_TOKEN"}
	if _, err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}}); err == nil {
		t.Error("expected error when bearer env var is empty")
	}
	cfg = config.NotifyWebhook{Enabled: true, URLEnv: "TEST_NOTIFY_URL", HMACEnv: "DEFINITELY_UNSET_SECRET"}
	if _, err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}}); err == nil {
		t.Error("expected error when hmac env var is empty")
	}
}

func TestNotifyServerErrorFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	t.Setenv("TEST_NOTIFY_URL", srv.URL)

	cfg := config.NotifyWebhook{Enabled: true, URLEnv: "TEST_NOTIFY_URL"}
	if _, err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}}); err == nil {
		t.Error("expected error on non-2xx response")
	}
}

func TestNotifyDryRunDoesNotPost(t *testing.T) {
	srv, got := notifyServer(t)
	t.Setenv("TEST_NOTIFY_URL", srv.URL)

	cfg := config.NotifyWebhook{Enabled: true, URLEnv: "TEST_NOTIFY_URL"}
	if _, err := Notify(&run.Runner{DryRun: true}, cfg, []Notification{{Image: "api"}}); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 0 {
		t.Errorf("dry-run must not POST, got %d requests", len(*got))
	}
}

func TestNotifyDisabledIsNoop(t *testing.T) {
	if _, err := Notify(&run.Runner{}, config.NotifyWebhook{}, []Notification{{Image: "api"}}); err != nil {
		t.Errorf("disabled notify should be a no-op, got %v", err)
	}
}

func TestNotifyRefusesCleartextBearer(t *testing.T) {
	t.Setenv("TEST_NOTIFY_URL", "http://deploy.example.com/hook")
	t.Setenv("TEST_NOTIFY_TOKEN", "tok3n")
	cfg := config.NotifyWebhook{Enabled: true, URLEnv: "TEST_NOTIFY_URL", BearerEnv: "TEST_NOTIFY_TOKEN"}
	_, err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}})
	if err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Fatalf("a plain-http notify URL off loopback should be refused, got %v", err)
	}
}

// flakyServer fails the first request with 503 and accepts the rest.
func flakyServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func TestNotifyRequiredFailsOnDeliveryError(t *testing.T) {
	srv, bodies := flakyServer(t)
	t.Setenv("TEST_NOTIFY_URL", srv.URL)
	for _, required := range []*bool{nil, ptr(true)} {
		*bodies = nil
		cfg := config.NotifyWebhook{Enabled: true, URLEnv: "TEST_NOTIFY_URL", Required: required}
		sent, err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}, {Image: "web"}})
		if err == nil || !strings.Contains(err.Error(), "503") {
			t.Errorf("required=%v: a 503 should fail the release, got %v", required, err)
		}
		if sent != 0 || len(*bodies) != 1 {
			t.Errorf("required=%v: should stop at the first failure, sent=%d posts=%d", required, sent, len(*bodies))
		}
	}
}

func TestNotifyNotRequiredWarnsAndContinues(t *testing.T) {
	srv, bodies := flakyServer(t)
	t.Setenv("TEST_NOTIFY_URL", srv.URL)
	cfg := config.NotifyWebhook{Enabled: true, URLEnv: "TEST_NOTIFY_URL", Required: ptr(false)}
	sent, err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}, {Image: "web"}})
	if err != nil {
		t.Fatalf("required: false should not fail on a 503, got %v", err)
	}
	if sent != 1 || len(*bodies) != 2 {
		t.Errorf("want the second notification still sent: sent=%d posts=%d", sent, len(*bodies))
	}
}

func TestNotifyNotRequiredStillFailsOnMisconfiguration(t *testing.T) {
	cfg := config.NotifyWebhook{Enabled: true, URLEnv: "DEFINITELY_UNSET_NOTIFY", Required: ptr(false)}
	if _, err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}}); err == nil {
		t.Error("a missing URL is a configuration error even when delivery is optional")
	}
}

func TestNotifyPayloadTemplate(t *testing.T) {
	srv, got := notifyServer(t)
	t.Setenv("TEST_NOTIFY_URL", srv.URL)
	t.Setenv("TEST_NOTIFY_SECRET", "s3cret")
	cfg := config.NotifyWebhook{
		Enabled:         true,
		URLEnv:          "TEST_NOTIFY_URL",
		HMACEnv:         "TEST_NOTIFY_SECRET",
		PayloadTemplate: `{"service": {{ json .Image }}, "version": {{ json .Version }}, "first_ref": {{ json (index .Refs 0) }}}`,
	}
	notes := []Notification{{Image: `a"pi`, Version: "1.2.3", Refs: []string{"ghcr.io/acme/api:1.2.3"}}}
	if _, err := Notify(&run.Runner{}, cfg, notes); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("want 1 POST, got %d", len(*got))
	}
	var body map[string]string
	if err := json.Unmarshal([]byte((*got)[0].body), &body); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, (*got)[0].body)
	}
	if body["service"] != `a"pi` || body["version"] != "1.2.3" || body["first_ref"] != "ghcr.io/acme/api:1.2.3" {
		t.Errorf("templated body = %v", body)
	}
	// The signature covers the rendered body, not the default one.
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte((*got)[0].body))
	if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); (*got)[0].signature != want {
		t.Errorf("signature = %q, want %q", (*got)[0].signature, want)
	}
}

func TestNotifyPayloadTemplateErrorsBeforeAnyPost(t *testing.T) {
	srv, got := notifyServer(t)
	t.Setenv("TEST_NOTIFY_URL", srv.URL)
	for _, tpl := range []string{
		`{"service": "{{ .Image }}"`,    // not JSON
		`{"service": {{ json .Nope }}}`, // no such field
	} {
		cfg := config.NotifyWebhook{Enabled: true, URLEnv: "TEST_NOTIFY_URL", Required: ptr(false), PayloadTemplate: tpl}
		if _, err := Notify(
			&run.Runner{},
			cfg,
			[]Notification{{Image: "api"}, {Image: "web"}},
		); err == nil ||
			!strings.Contains(err.Error(), "payload_template") {
			t.Errorf("%s: want a payload_template error, got %v", tpl, err)
		}
	}
	if len(*got) != 0 {
		t.Errorf("a template error must stop every POST, got %d", len(*got))
	}
}

func ptr[T any](v T) *T { return &v }
