package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tunedev/nerve/internal/adapters/inbound/admin"
	"github.com/tunedev/nerve/internal/config"
)

// withPassword is a config an operator would be alarmed to read back verbatim.
func withPassword() config.Config {
	cfg := config.Defaults()
	cfg.PostgresDSN = "postgres://nerve:hunter2@db:5432/nerve?sslmode=disable"
	cfg.Mounts = []config.Mount{{
		Prefix: "/blob",
		Kind:   "s3",
		Settings: map[string]string{
			"endpoint":          "https://key:s3cret@minio:9000",
			"secret_access_key": "AKIAIOSFODNN7EXAMPLE",
		},
	}}
	return cfg
}

// get runs one request against the handler.
func get(t *testing.T, cfg config.Config, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	admin.Handler(cfg.Effective(nil)).ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// The acceptance criterion, curled: the answer names every setting, and names
// no password.
func TestConfigEndpointAnswersWithoutGuessing(t *testing.T) {
	cfg := withPassword()
	rec := get(t, cfg, http.MethodGet, "/config")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	body := rec.Body.String()
	for _, secret := range []string{"hunter2", "s3cret", "AKIAIOSFODNN7EXAMPLE"} {
		if strings.Contains(body, secret) {
			t.Errorf("body carries %q:\n%s", secret, body)
		}
	}

	var answer config.Effective
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode body %s: %v", body, err)
	}
	if len(answer.Settings) != len(cfg.Effective(nil).Settings) {
		t.Errorf("body names %d settings, want %d", len(answer.Settings), len(cfg.Effective(nil).Settings))
	}
	if len(answer.Mounts) != 1 || answer.Mounts[0].Prefix != "/blob" {
		t.Errorf("body mounts = %+v, want the one at /blob", answer.Mounts)
	}
}

// The surface is read-only: it reports what the process resolved and offers no
// way to change it.
func TestConfigEndpointRefusesWrites(t *testing.T) {
	rec := get(t, config.Defaults(), http.MethodPost, "/config")

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /config status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

// Nothing else is served here, so a typo does not silently return the index.
func TestUnknownPathIsNotFound(t *testing.T) {
	rec := get(t, config.Defaults(), http.MethodGet, "/confg")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
