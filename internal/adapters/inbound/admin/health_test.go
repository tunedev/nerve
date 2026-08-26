package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tunedev/nerve/internal/adapters/inbound/admin"
	"github.com/tunedev/nerve/internal/config"
)

// probeBudget is far longer than answering from memory takes and far shorter
// than a dial to an address no packet reaches. A probe that spends more than
// this went somewhere.
const probeBudget = time.Second

// probe runs one request against a handler carrying the given health.
func probe(t *testing.T, health *admin.Health, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler := admin.Handler(config.Defaults().Effective(nil), health)
	handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// says decodes a probe's body and returns the word in it.
func says(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var answer struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return answer.Status
}

// unreachable names a Postgres, a broker and a model host in the reserved
// TEST-NET-3 range, which routes nowhere.
func unreachable() config.Config {
	cfg := config.Defaults()
	cfg.PostgresDSN = "postgres://nerve:nerve@203.0.113.1:5432/nerve"
	cfg.RabbitMQURL = "amqp://nerve:nerve@203.0.113.1:5672/"
	cfg.OllamaURL = "http://203.0.113.1:11434"
	return cfg
}

// The acceptance criterion, timed: every dependency this process has is at an
// address no packet reaches, and both probes still answer. One that dialled
// would spend its dial timeout, not a millisecond.
func TestProbesAnswerWithoutTouchingADependency(t *testing.T) {
	handler := admin.Handler(unreachable().Effective(nil), &admin.Health{})

	for _, path := range []string{"/livez", "/readyz"} {
		rec := httptest.NewRecorder()
		start := time.Now()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		spent := time.Since(start)

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want %d", path, rec.Code, http.StatusOK)
		}
		if spent > probeBudget {
			t.Errorf("GET %s answered in %s, want under %s: it reached for something", path, spent, probeBudget)
		}
	}
}

// A process that has not been asked to stop is ready.
func TestReadinessPassesUntilDrain(t *testing.T) {
	rec := probe(t, &admin.Health{}, http.MethodGet, "/readyz")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := says(t, rec); got != "ready" {
		t.Errorf("body says %q, want %q", got, "ready")
	}
}

// Draining is the whole reason readiness is a second endpoint: it fails first,
// so a balancer stops routing before the server stops answering.
func TestReadinessFailsWhileDraining(t *testing.T) {
	health := &admin.Health{}
	health.Drain()

	rec := probe(t, health, http.MethodGet, "/readyz")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if got := says(t, rec); got != "draining" {
		t.Errorf("body says %q, want %q", got, "draining")
	}
}

// Liveness is about the process, so it keeps passing through a clean shutdown.
// A liveness probe that fails while draining gets the process killed for doing
// exactly what it was told to do.
func TestLivenessPassesWhileDraining(t *testing.T) {
	health := &admin.Health{}
	health.Drain()

	rec := probe(t, health, http.MethodGet, "/livez")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := says(t, rec); got != "alive" {
		t.Errorf("body says %q, want %q", got, "alive")
	}
}

// The probes are read-only like the rest of the surface.
func TestProbesRefuseWrites(t *testing.T) {
	for _, path := range []string{"/livez", "/readyz"} {
		rec := probe(t, &admin.Health{}, http.MethodPost, path)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s status = %d, want %d", path, rec.Code, http.StatusMethodNotAllowed)
		}
	}
}
