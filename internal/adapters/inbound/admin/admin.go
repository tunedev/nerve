// Package admin is the operator surface: plain HTTP with a JSON body, read
// only, answering what this process actually resolved and how it is faring.
//
// It is not the service contract. Service-to-service traffic is gRPC generated
// from a .proto; this is what someone curls at 3am and what an orchestrator
// probes, so it is a URL and a document and nothing more.
package admin

import (
	"encoding/json"
	"net/http"

	"github.com/tunedev/nerve/internal/config"
)

// status is the whole body of a probe answer. The code is the part a probe
// reads; the word is for whoever curls it.
type status struct {
	Status string `json:"status"`
}

// Handler answers GET /config with the effective config, GET /livez with
// whether the process is running, and GET /readyz with whether it is taking
// traffic.
//
// It takes the redacted view rather than the Config it came from, so the
// surface has no unredacted value to leak by accident.
//
// The view is a value because config is resolved once, at startup: a surface
// that re-read it would answer a different question from the one the process
// booted on. Health is a pointer for the opposite reason — it is the one thing
// here that changes while the process runs.
func Handler(effective config.Effective, health *Health) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /config", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, effective)
	})

	// Liveness is the process and nothing else. It answers while draining
	// too: killing a process for shutting down cleanly helps nobody.
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, status{"alive"})
	})

	// Readiness is the process's willingness to be sent work, which is a
	// different question and the reason this is a second endpoint.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if health.draining.Load() {
			writeJSON(w, http.StatusServiceUnavailable, status{"draining"})
			return
		}
		writeJSON(w, http.StatusOK, status{"ready"})
	})

	return mux
}

// writeJSON encodes before it writes a header, so a failure can still be
// reported as one.
func writeJSON(w http.ResponseWriter, code int, body any) {
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		http.Error(w, "nerved: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// A client that hung up mid-answer is not this handler's problem.
	_, _ = w.Write(append(data, '\n'))
}
