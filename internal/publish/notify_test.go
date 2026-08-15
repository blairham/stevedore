package publish

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	if err := Notify(&run.Runner{}, cfg, notes); err != nil {
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
	if err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}}); err != nil {
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
	if err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}}); err == nil {
		t.Error("expected error when url env var is empty")
	}

	t.Setenv("TEST_NOTIFY_URL", "http://localhost:1")
	cfg = config.NotifyWebhook{Enabled: true, URLEnv: "TEST_NOTIFY_URL", BearerEnv: "DEFINITELY_UNSET_TOKEN"}
	if err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}}); err == nil {
		t.Error("expected error when bearer env var is empty")
	}
	cfg = config.NotifyWebhook{Enabled: true, URLEnv: "TEST_NOTIFY_URL", HMACEnv: "DEFINITELY_UNSET_SECRET"}
	if err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}}); err == nil {
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
	if err := Notify(&run.Runner{}, cfg, []Notification{{Image: "api"}}); err == nil {
		t.Error("expected error on non-2xx response")
	}
}

func TestNotifyDryRunDoesNotPost(t *testing.T) {
	srv, got := notifyServer(t)
	t.Setenv("TEST_NOTIFY_URL", srv.URL)

	cfg := config.NotifyWebhook{Enabled: true, URLEnv: "TEST_NOTIFY_URL"}
	if err := Notify(&run.Runner{DryRun: true}, cfg, []Notification{{Image: "api"}}); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 0 {
		t.Errorf("dry-run must not POST, got %d requests", len(*got))
	}
}

func TestNotifyDisabledIsNoop(t *testing.T) {
	if err := Notify(&run.Runner{}, config.NotifyWebhook{}, []Notification{{Image: "api"}}); err != nil {
		t.Errorf("disabled notify should be a no-op, got %v", err)
	}
}
